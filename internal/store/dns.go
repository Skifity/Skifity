package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A team's DNS providers, and the records the panel creates at them. See
// migration 0057 for what each table is for.

// DNSProvider is one connection to a DNS provider. Its credentials are never
// part of it: the only query that reads them is DNSProviderCredentials.
type DNSProvider struct {
	ID     string `json:"id"`
	TeamID string `json:"team_id"`
	// Kind is cloudflare, hetzner, digitalocean or route53.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Zones are the ones it could see when it was last asked.
	Zones         []DNSZone `json:"zones"`
	ZonesListedAt time.Time `json:"zones_listed_at,omitzero"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// DNSZone is one domain a provider serves.
type DNSZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DNSProviderContext is what a connection's credentials are sealed under.
func DNSProviderContext(teamID, id string) string { return "dns_provider:" + teamID + ":" + id }

const dnsProviderColumns = `id, team_id, kind, name, zones, zones_listed_at, created_at, updated_at`

func scanDNSProvider(row interface{ Scan(...any) error }) (DNSProvider, error) {
	var p DNSProvider
	var zones, listed, created, updated string
	if err := row.Scan(&p.ID, &p.TeamID, &p.Kind, &p.Name, &zones, &listed, &created, &updated); err != nil {
		return DNSProvider{}, err
	}
	if err := json.Unmarshal([]byte(zones), &p.Zones); err != nil {
		return DNSProvider{}, fmt.Errorf("read the zones of DNS provider %s: %w", p.ID, err)
	}
	if p.Zones == nil {
		p.Zones = []DNSZone{}
	}
	p.ZonesListedAt, _ = ParseTime(listed)
	p.CreatedAt, _ = ParseTime(created)
	p.UpdatedAt, _ = ParseTime(updated)
	return p, nil
}

// CreateDNSProvider stores a connection. The caller chooses the id, because
// the credentials are sealed under a context that names it; sealed is them.
func (db *DB) CreateDNSProvider(ctx context.Context, p *DNSProvider, sealed string) error {
	if p.ID == "" || sealed == "" {
		return errors.New("a DNS provider is saved with its id and its sealed credentials")
	}
	if p.Zones == nil {
		p.Zones = []DNSZone{}
	}
	zones, err := json.Marshal(p.Zones)
	if err != nil {
		return err
	}
	now := Now()
	_, err = db.Exec(ctx, `INSERT INTO dns_providers (id, team_id, kind, name, credentials_enc, zones, zones_listed_at,
		created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		p.ID, p.TeamID, p.Kind, p.Name, sealed, string(zones), now, now, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: the team already has a DNS provider called %q", ErrConflict, p.Name)
		}
		return fmt.Errorf("save the DNS provider %q: %w", p.Name, err)
	}
	p.CreatedAt, _ = ParseTime(now)
	p.UpdatedAt, p.ZonesListedAt = p.CreatedAt, p.CreatedAt
	return nil
}

// ListDNSProviders returns a team's connections, oldest first: when two
// connections reach the same zone, the older one is used.
func (db *DB) ListDNSProviders(ctx context.Context, teamID string) ([]DNSProvider, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+dnsProviderColumns+` FROM dns_providers
		WHERE team_id = ? ORDER BY created_at, id`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list DNS providers: %w", err)
	}
	defer rows.Close()
	out := []DNSProvider{}
	for rows.Next() {
		p, err := scanDNSProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetDNSProvider returns one of a team's connections.
func (db *DB) GetDNSProvider(ctx context.Context, teamID, id string) (DNSProvider, error) {
	p, err := scanDNSProvider(db.QueryRowContext(ctx, `SELECT `+dnsProviderColumns+` FROM dns_providers
		WHERE team_id = ? AND id = ?`, teamID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return DNSProvider{}, ErrNotFound
	}
	return p, err
}

// DNSProviderCredentials returns a connection's sealed credentials, for
// signing in to the provider and for nothing else.
func (db *DB) DNSProviderCredentials(ctx context.Context, teamID, id string) (string, error) {
	var sealed string
	err := db.QueryRowContext(ctx, `SELECT credentials_enc FROM dns_providers WHERE team_id = ? AND id = ?`,
		teamID, id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read the DNS provider %s: %w", id, err)
	}
	return sealed, nil
}

// SetDNSProviderZones records the zones a connection can see now.
func (db *DB) SetDNSProviderZones(ctx context.Context, teamID, id string, zones []DNSZone) error {
	if zones == nil {
		zones = []DNSZone{}
	}
	data, err := json.Marshal(zones)
	if err != nil {
		return err
	}
	now := Now()
	res, err := db.Exec(ctx, `UPDATE dns_providers SET zones = ?, zones_listed_at = ?, updated_at = ?
		WHERE team_id = ? AND id = ?`, string(data), now, now, teamID, id)
	if err != nil {
		return fmt.Errorf("record a DNS provider's zones: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDNSProvider removes a connection, and with it the panel's books of
// the records it made through it. It answers how many there were: those
// records stay at the provider.
func (db *DB) DeleteDNSProvider(ctx context.Context, teamID, id string) (int, error) {
	var left int
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dns_records WHERE team_id = ? AND provider_id = ?`,
			teamID, id).Scan(&left); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM dns_providers WHERE team_id = ? AND id = ?`, teamID, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, err
		}
		return 0, fmt.Errorf("delete a DNS provider: %w", err)
	}
	return left, nil
}

// --- what the panel does about one domain's record ---

// The states a domain's record can be in.
const (
	DNSStatePending   = "pending"
	DNSStateCreated   = "created"
	DNSStateElsewhere = "elsewhere"
	DNSStateRefused   = "refused"
	DNSStateFailed    = "failed"
	DNSStateOff       = "off"
)

// DomainDNS is what the panel does about one domain's record.
type DomainDNS struct {
	DomainID string
	Manage   bool
	State    string
	// Problem is the errdoc.Problem that explains a refusal or a failure, as
	// JSON, so the panel shows the same words the answer to asking did.
	Problem   string
	UpdatedAt time.Time
}

// GetDomainDNS returns a domain's row, and whether it has one.
func (db *DB) GetDomainDNS(ctx context.Context, domainID string) (DomainDNS, bool, error) {
	var d DomainDNS
	var updated string
	err := db.QueryRowContext(ctx, `SELECT domain_id, manage, state, problem, updated_at FROM domain_dns WHERE domain_id = ?`,
		domainID).Scan(&d.DomainID, &d.Manage, &d.State, &d.Problem, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return DomainDNS{}, false, nil
	}
	if err != nil {
		return DomainDNS{}, false, fmt.Errorf("read a domain's DNS: %w", err)
	}
	d.UpdatedAt, _ = ParseTime(updated)
	return d, true, nil
}

// SetDomainDNS writes a domain's row.
func (db *DB) SetDomainDNS(ctx context.Context, d DomainDNS) error {
	_, err := db.Exec(ctx, `INSERT INTO domain_dns (domain_id, manage, state, problem, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT (domain_id) DO UPDATE SET manage = excluded.manage, state = excluded.state,
			problem = excluded.problem, updated_at = excluded.updated_at`,
		d.DomainID, d.Manage, d.State, d.Problem, Now())
	if err != nil {
		return fmt.Errorf("record a domain's DNS: %w", err)
	}
	return nil
}

// DomainForDNS is a domain as the sync sees it: with its team, whether its
// app is served at all, and its row, when it has one.
type DomainForDNS struct {
	Domain
	TeamID   string
	Internal bool
	DNS      *DomainDNS
}

// DomainsForDNS returns every domain the sync has to look at: those with a
// row, and the automatic ones, which are given one when a zone covers them.
func (db *DB) DomainsForDNS(ctx context.Context) ([]DomainForDNS, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.id, d.app_id, d.hostname, d.path, d.tls, d.auto, d.status,
			d.status_detail, d.redirect_to, d.created_at, p.team_id, a.internal,
			x.domain_id IS NOT NULL, COALESCE(x.manage, 0), COALESCE(x.state, ''), COALESCE(x.problem, ''),
			COALESCE(x.updated_at, '')
		FROM domains d
		JOIN apps a ON a.id = d.app_id
		JOIN environments e ON e.id = a.environment_id
		JOIN projects p ON p.id = e.project_id
		LEFT JOIN domain_dns x ON x.domain_id = d.id
		WHERE x.domain_id IS NOT NULL OR d.auto = 1
		ORDER BY p.team_id, d.hostname`)
	if err != nil {
		return nil, fmt.Errorf("list the domains the DNS sync looks at: %w", err)
	}
	defer rows.Close()
	out := []DomainForDNS{}
	for rows.Next() {
		var d DomainForDNS
		var created, updated string
		var has bool
		var row DomainDNS
		if err := rows.Scan(&d.ID, &d.AppID, &d.Hostname, &d.Path, &d.TLS, &d.Auto, &d.Status, &d.StatusDetail,
			&d.RedirectTo, &created, &d.TeamID, &d.Internal, &has, &row.Manage, &row.State, &row.Problem, &updated); err != nil {
			return nil, fmt.Errorf("scan a domain for the DNS sync: %w", err)
		}
		d.CreatedAt, _ = ParseTime(created)
		if has {
			row.DomainID = d.ID
			row.UpdatedAt, _ = ParseTime(updated)
			d.DNS = &row
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DomainDNSForApp returns the rows of an app's domains, by domain id.
func (db *DB) DomainDNSForApp(ctx context.Context, appID string) (map[string]DomainDNS, error) {
	rows, err := db.QueryContext(ctx, `SELECT x.domain_id, x.manage, x.state, x.problem, x.updated_at
		FROM domain_dns x JOIN domains d ON d.id = x.domain_id WHERE d.app_id = ?`, appID)
	if err != nil {
		return nil, fmt.Errorf("read an app's domains' DNS: %w", err)
	}
	defer rows.Close()
	out := map[string]DomainDNS{}
	for rows.Next() {
		var d DomainDNS
		var updated string
		if err := rows.Scan(&d.DomainID, &d.Manage, &d.State, &d.Problem, &updated); err != nil {
			return nil, err
		}
		d.UpdatedAt, _ = ParseTime(updated)
		out[d.DomainID] = d
	}
	return out, rows.Err()
}

// --- the books ---

// DNSRecord is a record the panel created at a provider.
type DNSRecord struct {
	ID         string `json:"-"`
	TeamID     string `json:"-"`
	ProviderID string `json:"provider_id"`
	// DomainID is empty once the domain is gone and the record is still to
	// be removed from the provider.
	DomainID string `json:"-"`
	ZoneID   string `json:"-"`
	ZoneName string `json:"zone"`
	Hostname string `json:"hostname"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	RemoteID string `json:"-"`
	Proxied  bool   `json:"proxied"`
	// Keep is false once the domain's switch was turned off: the record is
	// left where it is, and forgotten rather than deleted with its domain.
	Keep      bool      `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const dnsRecordColumns = `id, team_id, provider_id, COALESCE(domain_id, ''), zone_id, zone_name, hostname, type, content,
	remote_id, proxied, keep, created_at, updated_at`

func (db *DB) queryDNSRecords(ctx context.Context, where string, args ...any) ([]DNSRecord, error) {
	// The where clause is one of the fixed strings in this file, never input.
	rows, err := db.QueryContext(ctx, `SELECT `+dnsRecordColumns+` FROM dns_records `+where+` ORDER BY hostname, type`, args...)
	if err != nil {
		return nil, fmt.Errorf("list DNS records: %w", err)
	}
	defer rows.Close()
	out := []DNSRecord{}
	for rows.Next() {
		var r DNSRecord
		var created, updated string
		if err := rows.Scan(&r.ID, &r.TeamID, &r.ProviderID, &r.DomainID, &r.ZoneID, &r.ZoneName, &r.Hostname, &r.Type,
			&r.Content, &r.RemoteID, &r.Proxied, &r.Keep, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan a DNS record: %w", err)
		}
		r.CreatedAt, _ = ParseTime(created)
		r.UpdatedAt, _ = ParseTime(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DNSRecordsForDomain returns the records the panel created for a domain.
func (db *DB) DNSRecordsForDomain(ctx context.Context, domainID string) ([]DNSRecord, error) {
	return db.queryDNSRecords(ctx, `WHERE domain_id = ?`, domainID)
}

// DNSRecordsForApp returns the records the panel created for an app's
// domains.
func (db *DB) DNSRecordsForApp(ctx context.Context, appID string) ([]DNSRecord, error) {
	return db.queryDNSRecords(ctx, `WHERE domain_id IN (SELECT id FROM domains WHERE app_id = ?)`, appID)
}

// DNSRecordsAt returns the records the panel created at a name through one
// connection, whichever domain they were for.
func (db *DB) DNSRecordsAt(ctx context.Context, providerID, zoneID, hostname string) ([]DNSRecord, error) {
	return db.queryDNSRecords(ctx, `WHERE provider_id = ? AND zone_id = ? AND hostname = ?`, providerID, zoneID, hostname)
}

// OrphanDNSRecords returns the records whose domain is gone: still at the
// provider, to be removed from it.
func (db *DB) OrphanDNSRecords(ctx context.Context) ([]DNSRecord, error) {
	return db.queryDNSRecords(ctx, `WHERE domain_id IS NULL`)
}

// SaveDNSRecord records a record the panel created, or what one it created
// says now.
func (db *DB) SaveDNSRecord(ctx context.Context, r *DNSRecord) error {
	if r.ID == "" {
		r.ID = NewID("dnsr")
	}
	r.Type = strings.ToUpper(r.Type)
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO dns_records (id, team_id, provider_id, domain_id, zone_id, zone_name, hostname,
			type, content, remote_id, proxied, keep, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET domain_id = excluded.domain_id, content = excluded.content,
			remote_id = excluded.remote_id, proxied = excluded.proxied, keep = excluded.keep,
			updated_at = excluded.updated_at`,
		r.ID, r.TeamID, r.ProviderID, NullString(r.DomainID), r.ZoneID, r.ZoneName, r.Hostname, r.Type, r.Content,
		r.RemoteID, r.Proxied, r.Keep, now, now)
	if err != nil {
		return fmt.Errorf("record a DNS record: %w", err)
	}
	r.UpdatedAt, _ = ParseTime(now)
	if r.CreatedAt.IsZero() {
		r.CreatedAt = r.UpdatedAt
	}
	return nil
}

// LeaveDNSRecords marks a domain's records as left where they are: its
// switch was turned off.
func (db *DB) LeaveDNSRecords(ctx context.Context, domainID string) error {
	if _, err := db.Exec(ctx, `UPDATE dns_records SET keep = 0, updated_at = ? WHERE domain_id = ?`, Now(), domainID); err != nil {
		return fmt.Errorf("leave a domain's DNS records: %w", err)
	}
	return nil
}

// ForgetDNSRecord takes a record out of the books. The record at the
// provider is not touched: the caller has removed it, or found it gone, or
// found it changed and left it to whoever changed it.
func (db *DB) ForgetDNSRecord(ctx context.Context, id string) error {
	if _, err := db.Exec(ctx, `DELETE FROM dns_records WHERE id = ?`, id); err != nil {
		return fmt.Errorf("forget a DNS record: %w", err)
	}
	return nil
}

// ManagedDNS is a domain's record at the team's DNS provider, as the Domains
// tab, the CLI and an assistant read it.
type ManagedDNS struct {
	ProviderID string `json:"provider_id"`
	// Provider is its kind — cloudflare, hetzner, digitalocean, route53 —
	// and ProviderName what the team called the connection.
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name"`
	// Zone is the connected zone the hostname is in.
	Zone string `json:"zone"`
	// Manage is the switch: whether the panel keeps this record.
	Manage bool `json:"manage"`
	// State is pending, created, elsewhere, refused, failed or off.
	State string `json:"state"`
	// Records are the ones the panel created and keeps.
	Records []DNSRecord `json:"records"`
	// Problem explains a refusal or a failure: an errdoc.Problem.
	Problem   json.RawMessage `json:"problem,omitempty"`
	UpdatedAt time.Time       `json:"updated_at,omitzero"`
}

// CountDNSRecords is how many records the panel keeps through each of a
// team's connections, by connection id.
func (db *DB) CountDNSRecords(ctx context.Context, teamID string) (map[string]int, error) {
	rows, err := db.QueryContext(ctx, `SELECT provider_id, COUNT(*) FROM dns_records WHERE team_id = ? GROUP BY provider_id`, teamID)
	if err != nil {
		return nil, fmt.Errorf("count DNS records: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
