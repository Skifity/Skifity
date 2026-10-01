package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CloudProvider is a team's connection to a cloud provider: one project's API
// token, which never leaves the panel once stored.
type CloudProvider struct {
	ID     string `json:"id"`
	TeamID string `json:"team_id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	// TokenHint is the token's last four characters, to tell two apart.
	TokenHint string    `json:"token_hint"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Servers is how many servers the panel created with it and still has.
	Servers int `json:"servers"`
}

// CloudProviderRow carries the sealed token alongside.
type CloudProviderRow struct {
	CloudProvider
	SealedToken string
}

// CloudProviderContext is what a connection's token is sealed under: the
// connection itself, so a token copied onto another row does not open.
func CloudProviderContext(id string) string { return "cloud_provider:" + id }

// NewCloudProviderID is a connection's id, known before it is stored so the
// token can be sealed under it.
func NewCloudProviderID() string { return NewID("cld") }

const cloudProviderColumns = `p.id, p.team_id, p.kind, p.name, p.token_hint, p.checked_at, p.created_by,
	p.created_at, p.updated_at, p.token_enc,
	(SELECT COUNT(*) FROM cloud_servers c WHERE c.provider_id = p.id)`

func scanCloudProvider(row interface{ Scan(...any) error }) (CloudProviderRow, error) {
	var r CloudProviderRow
	var checked sql.NullString
	var created, updated string
	err := row.Scan(&r.ID, &r.TeamID, &r.Kind, &r.Name, &r.TokenHint, &checked, &r.CreatedBy,
		&created, &updated, &r.SealedToken, &r.Servers)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, ErrNotFound
		}
		return r, fmt.Errorf("read a cloud connection: %w", err)
	}
	r.CheckedAt = scanTime(checked)
	r.CreatedAt, _ = ParseTime(created)
	r.UpdatedAt, _ = ParseTime(updated)
	return r, nil
}

// CreateCloudProvider stores a connection. p.ID must already be set, from
// NewCloudProviderID, because sealed is bound to it.
func (db *DB) CreateCloudProvider(ctx context.Context, p *CloudProvider, sealed string) error {
	if p.ID == "" {
		return fmt.Errorf("a cloud connection needs its id before its token is sealed")
	}
	now := Now()
	checked := sql.NullString{}
	if !p.CheckedAt.IsZero() {
		checked = sql.NullString{String: FormatTime(p.CheckedAt), Valid: true}
	}
	_, err := db.Exec(ctx, `INSERT INTO cloud_providers
		(id, team_id, kind, name, token_enc, token_hint, checked_at, created_by, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.TeamID, p.Kind, p.Name, sealed, p.TokenHint, checked, p.CreatedBy, now, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: the team already has a cloud connection called %s", ErrConflict, p.Name)
		}
		return fmt.Errorf("save the cloud connection: %w", err)
	}
	p.CreatedAt, _ = ParseTime(now)
	p.UpdatedAt = p.CreatedAt
	return nil
}

// ListCloudProviders returns a team's connections, by name.
func (db *DB) ListCloudProviders(ctx context.Context, teamID string) ([]CloudProviderRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+cloudProviderColumns+`
		FROM cloud_providers p WHERE p.team_id = ? ORDER BY p.name`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list the cloud connections: %w", err)
	}
	defer rows.Close()
	out := []CloudProviderRow{}
	for rows.Next() {
		row, err := scanCloudProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetCloudProvider reads one of a team's connections. Another team's answers
// ErrNotFound, exactly as one that does not exist.
func (db *DB) GetCloudProvider(ctx context.Context, teamID, id string) (CloudProviderRow, error) {
	return scanCloudProvider(db.QueryRowContext(ctx, `SELECT `+cloudProviderColumns+`
		FROM cloud_providers p WHERE p.team_id = ? AND p.id = ?`, teamID, id))
}

// TouchCloudProviderChecked records that the provider accepted the token.
func (db *DB) TouchCloudProviderChecked(ctx context.Context, id string, at time.Time) error {
	_, err := db.Exec(ctx, `UPDATE cloud_providers SET checked_at = ?, updated_at = ? WHERE id = ?`,
		FormatTime(at), Now(), id)
	if err != nil {
		return fmt.Errorf("record the cloud connection's check: %w", err)
	}
	return nil
}

// DeleteCloudProvider removes one of a team's connections. One that servers
// were created with is refused by the schema; the API asks first, to say so
// properly.
func (db *DB) DeleteCloudProvider(ctx context.Context, teamID, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM cloud_providers WHERE team_id = ? AND id = ?`, teamID, id)
	if err != nil {
		return fmt.Errorf("delete the cloud connection: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SSH access to a created machine.
const (
	// SSHFromAnywhere leaves port 22 open to the internet, with passwords off.
	SSHFromAnywhere = "anywhere"
	// SSHFromCluster opens port 22 to the cluster's own servers only, which
	// is where the panel's requests come from when it runs in the cluster.
	SSHFromCluster = "cluster"
)

// CloudServer is what the panel knows about a server it ordered from a
// provider.
type CloudServer struct {
	ServerID   string `json:"server_id"`
	ProviderID string `json:"provider_id"`
	// ProviderKind and ProviderName are the connection's, read alongside.
	ProviderKind string `json:"provider_kind"`
	ProviderName string `json:"provider_name"`
	// MachineID is the provider's id for the machine. Empty until it is
	// ordered, and the only machine the panel will ever delete for this server.
	MachineID  string `json:"machine_id"`
	Location   string `json:"location"`
	ServerType string `json:"server_type"`
	Image      string `json:"image"`
	SSHAccess  string `json:"ssh_access"`
	FirewallID string `json:"firewall_id"`
	// SSHKeyID is the panel's key as imported at the provider, until it is
	// removed there once the machine has it.
	SSHKeyID string `json:"-"`
	// HostKeyRotated is true once the bootstrap host key from the user data
	// has been replaced by one the machine generated.
	HostKeyRotated bool      `json:"host_key_rotated"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

const cloudServerColumns = `c.server_id, c.provider_id, p.kind, p.name, c.machine_id, c.location,
	c.server_type, c.image, c.ssh_access, c.firewall_id, c.ssh_key_id, c.host_key_rotated,
	c.created_at, c.updated_at`

func scanCloudServer(row interface{ Scan(...any) error }) (CloudServer, error) {
	var c CloudServer
	var created, updated string
	err := row.Scan(&c.ServerID, &c.ProviderID, &c.ProviderKind, &c.ProviderName, &c.MachineID,
		&c.Location, &c.ServerType, &c.Image, &c.SSHAccess, &c.FirewallID, &c.SSHKeyID,
		&c.HostKeyRotated, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c, ErrNotFound
		}
		return c, fmt.Errorf("read a created server: %w", err)
	}
	c.CreatedAt, _ = ParseTime(created)
	c.UpdatedAt, _ = ParseTime(updated)
	return c, nil
}

// CreateCloudServer records that a server is being ordered from a provider.
func (db *DB) CreateCloudServer(ctx context.Context, c *CloudServer) error {
	if c.SSHAccess == "" {
		c.SSHAccess = SSHFromAnywhere
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO cloud_servers
		(server_id, provider_id, machine_id, location, server_type, image, ssh_access,
		 firewall_id, ssh_key_id, host_key_rotated, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ServerID, c.ProviderID, c.MachineID, c.Location, c.ServerType, c.Image, c.SSHAccess,
		c.FirewallID, c.SSHKeyID, c.HostKeyRotated, now, now)
	if err != nil {
		return fmt.Errorf("record the created server: %w", err)
	}
	c.CreatedAt, _ = ParseTime(now)
	c.UpdatedAt = c.CreatedAt
	return nil
}

// GetCloudServer reads what the panel knows about a server it created.
// ErrNotFound means the panel did not create it.
func (db *DB) GetCloudServer(ctx context.Context, serverID string) (CloudServer, error) {
	return scanCloudServer(db.QueryRowContext(ctx, `SELECT `+cloudServerColumns+`
		FROM cloud_servers c JOIN cloud_providers p ON p.id = c.provider_id
		WHERE c.server_id = ?`, serverID))
}

// ListCloudServers returns every server the panel created, on every team:
// the firewalls in front of them all follow the one cluster.
func (db *DB) ListCloudServers(ctx context.Context) ([]CloudServer, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+cloudServerColumns+`
		FROM cloud_servers c JOIN cloud_providers p ON p.id = c.provider_id
		ORDER BY c.created_at`)
	if err != nil {
		return nil, fmt.Errorf("list the created servers: %w", err)
	}
	defer rows.Close()
	out := []CloudServer{}
	for rows.Next() {
		c, err := scanCloudServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCloudServer writes what ordering the machine learned.
func (db *DB) UpdateCloudServer(ctx context.Context, c *CloudServer) error {
	now := Now()
	res, err := db.Exec(ctx, `UPDATE cloud_servers SET machine_id = ?, firewall_id = ?, ssh_key_id = ?,
		host_key_rotated = ?, updated_at = ? WHERE server_id = ?`,
		c.MachineID, c.FirewallID, c.SSHKeyID, c.HostKeyRotated, now, c.ServerID)
	if err != nil {
		return fmt.Errorf("update the created server: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	c.UpdatedAt, _ = ParseTime(now)
	return nil
}
