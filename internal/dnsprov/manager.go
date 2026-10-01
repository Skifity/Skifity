package dnsprov

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Manager keeps the records of a team's domains at its DNS providers: it
// creates a domain's record when the domain is added, points it at the new
// address when the cluster's address changes, and deletes it when the domain
// goes — only ever a record it created. See plan.go for the rules.
//
// It is called from three places: the API, when a domain is added, removed or
// its switch changed; the minute tick, every few minutes, which is also what
// finishes a removal the provider did not answer; and AddressChanged, the hook
// for when the cluster's address is known to have changed.
type Manager struct {
	DB      *store.DB
	Keyring *crypto.Keyring
	Log     *slog.Logger
	// Address is where a team's domains point: the cluster's public
	// address, an IP or a load balancer's name. Empty when nobody knows.
	Address func(ctx context.Context, teamID string) string
	// Open signs in to a provider. Nil is New: the provider's real address,
	// through internal/netguard.
	Open func(kind string, creds Credentials) (Provider, error)
	// Now is the clock. Nil is time.Now.
	Now func() time.Time

	// locks keeps two changes at one name apart: a domain added while the
	// sync is looking at it, or a domain removed and added again.
	locks sync.Map
	// syncing stops a sync starting while the last one is still going.
	syncing atomic.Bool
}

const (
	// ensureTimeout bounds one domain's work: three or four requests.
	ensureTimeout = 45 * time.Second
	// recheckAfter is how often a record the panel keeps is looked at when
	// nothing says it has to be: somebody may have changed or deleted it.
	recheckAfter = time.Hour
	// tunnelComponent is the component the Cloudflare tunnel installs under,
	// as cluster.TunnelComponent names it.
	tunnelComponent = "cloudflare-tunnel"
	// tunnelTarget is what a hostname served by a Cloudflare tunnel points
	// at: the tunnel's own name.
	tunnelTarget = "%s.cfargotunnel.com"
)

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

func (m *Manager) lock(hostname string) func() {
	value, _ := m.locks.LoadOrStore(Canonical(hostname), &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// --- connections ---

// Connect signs in to a provider, lists its zones, and saves the connection
// with its credentials sealed. Nothing is saved unless the provider accepted
// the credentials and they can see at least one zone.
func (m *Manager) Connect(ctx context.Context, teamID, kind, name string, creds Credentials) (store.DNSProvider, error) {
	creds = Credentials{
		Token:           strings.TrimSpace(creds.Token),
		AccessKeyID:     strings.TrimSpace(creds.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(creds.SecretAccessKey),
	}
	if err := CheckCredentials(kind, creds); err != nil {
		if errors.Is(err, ErrGlobalKey) {
			return store.DNSProvider{}, errdoc.DNSGlobalKey()
		}
		return store.DNSProvider{}, errdoc.BadRequest(capitalise(err.Error()) + ".")
	}
	zones, err := m.listZones(ctx, kind, creds)
	if err != nil {
		return store.DNSProvider{}, err
	}
	provider := store.DNSProvider{
		ID: store.NewID("dnsp"), TeamID: teamID, Kind: kind,
		Name: or(strings.TrimSpace(name), Title(kind)), Zones: zones,
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return store.DNSProvider{}, err
	}
	sealed, err := m.Keyring.Seal(data, store.DNSProviderContext(teamID, provider.ID))
	if err != nil {
		return store.DNSProvider{}, err
	}
	if err := m.DB.CreateDNSProvider(ctx, &provider, sealed); err != nil {
		return store.DNSProvider{}, err
	}
	return provider, nil
}

// Refresh asks a saved connection's provider for its zones now, and keeps
// them. It is the test of a connection as well: credentials that were
// revoked answer the same problem they would have when it was made.
func (m *Manager) Refresh(ctx context.Context, teamID, id string) (store.DNSProvider, error) {
	provider, err := m.DB.GetDNSProvider(ctx, teamID, id)
	if err != nil {
		return store.DNSProvider{}, err
	}
	creds, err := m.credentials(ctx, provider)
	if err != nil {
		return store.DNSProvider{}, err
	}
	zones, err := m.listZones(ctx, provider.Kind, creds)
	if err != nil {
		return store.DNSProvider{}, err
	}
	if err := m.DB.SetDNSProviderZones(ctx, teamID, id, zones); err != nil {
		return store.DNSProvider{}, err
	}
	return m.DB.GetDNSProvider(ctx, teamID, id)
}

func (m *Manager) listZones(ctx context.Context, kind string, creds Credentials) ([]store.DNSZone, error) {
	provider, err := m.open(kind, creds)
	if err != nil {
		return nil, err
	}
	found, err := provider.Zones(ctx)
	if err != nil {
		return nil, providerProblem(kind, err)
	}
	if len(found) == 0 {
		return nil, errdoc.DNSNoZones(Title(kind))
	}
	zones := make([]store.DNSZone, len(found))
	for i, z := range found {
		zones[i] = store.DNSZone{ID: z.ID, Name: z.Name}
	}
	return zones, nil
}

func (m *Manager) open(kind string, creds Credentials) (Provider, error) {
	if m.Open != nil {
		return m.Open(kind, creds)
	}
	return New(kind, creds)
}

func (m *Manager) credentials(ctx context.Context, provider store.DNSProvider) (Credentials, error) {
	sealed, err := m.DB.DNSProviderCredentials(ctx, provider.TeamID, provider.ID)
	if err != nil {
		return Credentials{}, err
	}
	plain, err := m.Keyring.Open(sealed, store.DNSProviderContext(provider.TeamID, provider.ID))
	if err != nil {
		return Credentials{}, fmt.Errorf("open the credentials of the DNS provider %s: %w", provider.Name, err)
	}
	var creds Credentials
	if err := json.Unmarshal(plain, &creds); err != nil {
		return Credentials{}, fmt.Errorf("read the credentials of the DNS provider %s: %w", provider.Name, err)
	}
	return creds, nil
}

func (m *Manager) signIn(ctx context.Context, provider store.DNSProvider) (Provider, error) {
	creds, err := m.credentials(ctx, provider)
	if err != nil {
		return nil, err
	}
	return m.open(provider.Kind, creds)
}

// providerProblem is what a provider's failure is to the person who asked.
func providerProblem(kind string, err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Refused() {
		return errdoc.DNSProviderRefused(Title(kind), apiErr.Message)
	}
	return errdoc.DNSProviderFailed(Title(kind), err)
}

// --- which zone ---

// ZoneFor finds the connected zone a hostname is in, from the zones the
// team's connections could see when they were last asked. Nothing is asked of
// any provider, so the Domains tab can call it as somebody types.
func ZoneFor(providers []store.DNSProvider, hostname string) (store.DNSProvider, store.DNSZone, bool) {
	var names []string
	var owners []int
	var zones []store.DNSZone
	for i, provider := range providers {
		for _, zone := range provider.Zones {
			names = append(names, zone.Name)
			owners = append(owners, i)
			zones = append(zones, zone)
		}
	}
	i := MatchZone(hostname, names)
	if i < 0 {
		return store.DNSProvider{}, store.DNSZone{}, false
	}
	return providers[owners[i]], zones[i], true
}

// --- what a record should say ---

// Target is where a team's domains are reached.
type Target struct {
	IPv4, IPv6 string
	// Name is a load balancer's hostname, set as the cluster's address.
	Name string
	// Tunnel is <tunnel id>.cfargotunnel.com while the Cloudflare tunnel is
	// installed.
	Tunnel string
}

// Target works out where a team's domains point now.
func (m *Manager) Target(ctx context.Context, teamID string) Target {
	var t Target
	if m.Address != nil {
		address := strings.TrimSpace(m.Address(ctx, teamID))
		if ip := net.ParseIP(address); ip != nil {
			if ip.To4() != nil {
				t.IPv4 = ip.String()
			} else {
				t.IPv6 = ip.String()
			}
		} else if address != "" {
			t.Name = Canonical(address)
		}
	}
	if value, _, err := m.DB.GetSetting(ctx, settings.KeyClusterIPv6); err == nil && t.IPv6 == "" {
		if ip := net.ParseIP(strings.TrimSpace(value)); ip != nil && ip.To4() == nil {
			t.IPv6 = ip.String()
		}
	}
	t.Tunnel = m.tunnel(ctx)
	return t
}

// tunnel is the tunnel's name while it is installed and has a token.
func (m *Manager) tunnel(ctx context.Context) string {
	component, err := m.DB.GetComponent(ctx, tunnelComponent)
	if err != nil || component.Status != "installed" {
		return ""
	}
	value, encrypted, err := m.DB.GetSetting(ctx, settings.KeyCloudflareTunnelToken)
	if err != nil || value == "" {
		return ""
	}
	if encrypted {
		plain, err := m.Keyring.Open(value, settings.Context(settings.KeyCloudflareTunnelToken))
		if err != nil {
			return ""
		}
		value = string(plain)
	}
	if id := settings.TunnelID(value); id != "" {
		return fmt.Sprintf(tunnelTarget, strings.ToLower(id))
	}
	return ""
}

// Wants is what a hostname's records should say at one kind of provider.
//
// A tunnel wins at Cloudflare, where it is the way in: a CNAME to the tunnel,
// which Cloudflare requires to be proxied. Elsewhere the addresses are used,
// A and AAAA, never proxied, because cert-manager proves the hostname over
// HTTP and Let's Encrypt has to reach the server itself. A load balancer's
// name is a CNAME, which DNS does not allow at a zone's own name — except at
// Cloudflare, which flattens it.
func Wants(t Target, kind, hostname, zone string) ([]Want, error) {
	apex := Canonical(hostname) == Canonical(zone)
	switch {
	case t.Tunnel != "" && kind == Cloudflare:
		return []Want{{Type: "CNAME", Content: t.Tunnel, Proxied: true}}, nil
	case t.IPv4 != "" || t.IPv6 != "":
		var want []Want
		if t.IPv4 != "" {
			want = append(want, Want{Type: "A", Content: t.IPv4})
		}
		if t.IPv6 != "" {
			want = append(want, Want{Type: "AAAA", Content: t.IPv6})
		}
		return want, nil
	case t.Name != "":
		if apex && kind != Cloudflare {
			return nil, errdoc.DNSApexCNAME(Canonical(hostname), t.Name, Title(kind))
		}
		return []Want{{Type: "CNAME", Content: t.Name}}, nil
	case t.Tunnel != "":
		return nil, errdoc.DNSTunnelNeedsCloudflare(Canonical(hostname), Title(kind))
	}
	return nil, errdoc.DNSNoAddress(Canonical(hostname))
}

// --- one domain ---

// Ensure makes a domain's record say what it should, at the provider whose
// zone covers it, and records what happened on the domain. A refusal or a
// failure is returned as the problem that explains it, and is recorded too.
func (m *Manager) Ensure(ctx context.Context, teamID string, domain store.Domain) error {
	ctx, cancel := context.WithTimeout(ctx, ensureTimeout)
	defer cancel()
	hostname := Canonical(domain.Hostname)
	defer m.lock(hostname)()

	providers, err := m.DB.ListDNSProviders(ctx, teamID)
	if err != nil {
		return err
	}
	provider, zone, ok := ZoneFor(providers, hostname)
	if !ok {
		return m.settle(ctx, domain.ID, store.DNSStateRefused, errdoc.DNSNoZone(hostname))
	}

	// The domain's records elsewhere: the hostname it had before its
	// automatic address moved, or a zone that another connection serves now.
	current, err := m.DB.DNSRecordsForDomain(ctx, domain.ID)
	if err != nil {
		return err
	}
	for _, row := range current {
		if row.Hostname != hostname || row.ProviderID != provider.ID || row.ZoneID != zone.ID {
			if err := m.release(ctx, row); err != nil {
				m.log().Warn("could not remove a record a domain no longer needs; the sync tries again",
					"hostname", row.Hostname, "error", err)
			}
		}
	}

	want, err := Wants(m.Target(ctx, teamID), provider.Kind, hostname, zone.Name)
	if err != nil {
		return m.settle(ctx, domain.ID, store.DNSStateRefused, err)
	}
	remote, err := m.signIn(ctx, provider)
	if err != nil {
		return m.settle(ctx, domain.ID, store.DNSStateFailed, err)
	}
	title := Title(provider.Kind)
	z := Zone{ID: zone.ID, Name: zone.Name}
	existing, err := remote.Records(ctx, z, hostname)
	if err != nil {
		return m.settle(ctx, domain.ID, store.DNSStateFailed, providerProblem(provider.Kind, err))
	}
	rows, err := m.DB.DNSRecordsAt(ctx, provider.ID, zone.ID, hostname)
	if err != nil {
		return err
	}
	owned := make([]Owned, len(rows))
	byRow := map[string]store.DNSRecord{}
	for i, row := range rows {
		owned[i] = Owned{Row: row.ID, RemoteID: row.RemoteID, Type: row.Type, Content: row.Content}
		byRow[row.ID] = row
	}

	// Nothing at the name, and nothing of the panel's: a wildcard record that
	// already sends it here is somebody's decision, and enough.
	if len(existing) == 0 && len(owned) == 0 {
		if wildcard := Wildcard(hostname, zone.Name); wildcard != "" {
			covering, err := remote.Records(ctx, z, wildcard)
			if err != nil {
				return m.settle(ctx, domain.ID, store.DNSStateFailed, providerProblem(provider.Kind, err))
			}
			if Covers(covering, want) {
				return m.settle(ctx, domain.ID, store.DNSStateElsewhere, nil)
			}
		}
	}

	marker := Marker(m.panelID(ctx))
	plan, decided := Decide(hostname, existing, owned, want, marker, KeepsNotes(provider.Kind))
	for _, gone := range plan.Forget {
		if err := m.DB.ForgetDNSRecord(ctx, gone.Row); err != nil {
			return err
		}
	}
	for _, changed := range plan.Changed {
		m.log().Info("a record the panel created was changed outside it, so it is left to whoever changed it",
			"hostname", hostname, "type", changed.Owned.Type)
		m.audit(ctx, teamID, "dns_record.abandoned", hostname, changed.Owned.Type+" "+changed.Owned.Content)
	}
	if decided != nil {
		var conflict *Conflict
		if !errors.As(decided, &conflict) {
			return decided
		}
		return m.settle(ctx, domain.ID, store.DNSStateRefused, conflictProblem(conflict, plan, title))
	}

	record := func(r Record, proxied bool, rowID string) error {
		row := store.DNSRecord{
			ID: rowID, TeamID: teamID, ProviderID: provider.ID, DomainID: domain.ID, ZoneID: zone.ID,
			ZoneName: zone.Name, Hostname: hostname, Type: r.Type, Content: r.Content, RemoteID: r.ID, Proxied: proxied,
			Keep: true,
		}
		if previous, ok := byRow[rowID]; ok {
			row.CreatedAt = previous.CreatedAt
		}
		return m.DB.SaveDNSRecord(ctx, &row)
	}

	for _, gone := range plan.Delete {
		if err := remote.Delete(ctx, z, gone.Record); err != nil && !errors.Is(err, ErrGone) {
			return m.settle(ctx, domain.ID, store.DNSStateFailed, providerProblem(provider.Kind, err))
		}
		if err := m.DB.ForgetDNSRecord(ctx, gone.Owned.Row); err != nil {
			return err
		}
		m.audit(ctx, teamID, "dns_record.removed", hostname, gone.Record.Describe())
	}
	for _, update := range plan.Update {
		moved, err := remote.SetContent(ctx, z, update.Record, update.To)
		switch {
		case errors.Is(err, ErrGone), errors.Is(err, ErrChanged):
			// Between looking and writing, somebody deleted or changed it.
			// It is not the panel's to put back; the next look decides.
			if err := m.DB.ForgetDNSRecord(ctx, update.Owned.Row); err != nil {
				return err
			}
			return m.settle(ctx, domain.ID, store.DNSStatePending, nil)
		case err != nil:
			return m.settle(ctx, domain.ID, store.DNSStateFailed, providerProblem(provider.Kind, err))
		}
		if moved.ID == "" {
			moved.ID = update.Record.ID
		}
		if moved.Type == "" {
			moved.Type = update.Record.Type
		}
		moved.Content = canonicalContent(moved.Type, update.To)
		if err := record(moved, byRow[update.Owned.Row].Proxied, update.Owned.Row); err != nil {
			return err
		}
		m.audit(ctx, teamID, "dns_record.updated", hostname, update.Record.Describe()+" → "+update.To)
	}
	proxied := map[string]bool{}
	for _, w := range want {
		proxied[w.Type] = w.Proxied
	}
	for _, w := range plan.Create {
		note := ""
		if KeepsNotes(provider.Kind) || provider.Kind == Route53 {
			note = marker
		}
		made, err := remote.Create(ctx, z, Record{Type: w.Type, Name: hostname, Content: w.Content, Proxied: w.Proxied, Note: note})
		if err != nil {
			return m.settle(ctx, domain.ID, store.DNSStateFailed, providerProblem(provider.Kind, err))
		}
		if err := record(made, w.Proxied, ""); err != nil {
			return err
		}
		m.audit(ctx, teamID, "dns_record.created", hostname, made.Describe()+" at "+title)
	}
	for _, kept := range plan.Keep {
		// Saved again so a record of a domain removed and added back is
		// this domain's now.
		if err := record(kept.Record, byRow[kept.Owned.Row].Proxied, kept.Owned.Row); err != nil {
			return err
		}
	}
	for _, adopted := range plan.Adopt {
		if err := record(adopted, adopted.Proxied || proxied[adopted.Type], ""); err != nil {
			return err
		}
	}

	state := store.DNSStateElsewhere
	if len(plan.Create)+len(plan.Update)+len(plan.Keep)+len(plan.Adopt) > 0 {
		state = store.DNSStateCreated
	}
	return m.settle(ctx, domain.ID, state, nil)
}

// conflictProblem names what is in the way.
func conflictProblem(conflict *Conflict, plan Plan, provider string) error {
	found := conflict.Found.Describe()
	for _, changed := range plan.Changed {
		for _, now := range changed.Now {
			if now.ID == conflict.Found.ID && now.Type == conflict.Found.Type {
				return errdoc.DNSRecordChanged(conflict.Name, changed.Owned.Type, provider, now.Content)
			}
		}
	}
	if conflict.Reason == "extra" {
		return errdoc.DNSRecordExtra(conflict.Name, found, provider)
	}
	return errdoc.DNSRecordConflict(conflict.Name, found, provider, conflict.Want.Describe())
}

// settle records what happened to a domain's record, and returns the problem
// when there was one. A domain's switch stays on through a refusal: the next
// look, or Try again, may find the way clear.
func (m *Manager) settle(ctx context.Context, domainID, state string, problem error) error {
	row := store.DomainDNS{DomainID: domainID, Manage: true, State: state}
	if problem != nil {
		encoded, err := json.Marshal(errdoc.From(problem))
		if err == nil {
			row.Problem = string(encoded)
		}
	}
	if err := m.DB.SetDomainDNS(context.WithoutCancel(ctx), row); err != nil {
		return err
	}
	return problem
}

// Release deletes the records the panel created for a domain, and forgets
// them. A record somebody changed is forgotten and left where it is. What the
// provider does not answer stays in the books, and the sync tries again once
// the domain is gone.
func (m *Manager) Release(ctx context.Context, domainID string) error {
	ctx, cancel := context.WithTimeout(ctx, ensureTimeout)
	defer cancel()
	rows, err := m.DB.DNSRecordsForDomain(ctx, domainID)
	if err != nil {
		return err
	}
	var failed error
	for _, row := range rows {
		func() {
			defer m.lock(row.Hostname)()
			if err := m.release(ctx, row); err != nil && failed == nil {
				failed = err
			}
		}()
	}
	return failed
}

// Stop turns a domain's switch off: the panel leaves its records where they
// are, stops changing them, and does not delete them when the domain goes.
// They stay in the books, so turning the switch on again finds them its own.
func (m *Manager) Stop(ctx context.Context, domainID string) error {
	if err := m.DB.LeaveDNSRecords(ctx, domainID); err != nil {
		return err
	}
	return m.DB.SetDomainDNS(ctx, store.DomainDNS{DomainID: domainID, Manage: false, State: store.DNSStateOff})
}

// release deletes one record the panel created, if it is still as the panel
// left it. The caller holds the name's lock.
func (m *Manager) release(ctx context.Context, row store.DNSRecord) error {
	if !row.Keep {
		return m.DB.ForgetDNSRecord(ctx, row.ID)
	}
	provider, err := m.DB.GetDNSProvider(ctx, row.TeamID, row.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return m.DB.ForgetDNSRecord(ctx, row.ID)
	}
	if err != nil {
		return err
	}
	remote, err := m.signIn(ctx, provider)
	if err != nil {
		return err
	}
	z := Zone{ID: row.ZoneID, Name: row.ZoneName}
	existing, err := remote.Records(ctx, z, row.Hostname)
	if err != nil {
		return providerProblem(provider.Kind, err)
	}
	owned := Owned{Row: row.ID, RemoteID: row.RemoteID, Type: row.Type, Content: row.Content}
	if record, ok := Still(existing, owned, Marker(m.panelID(ctx)), KeepsNotes(provider.Kind)); ok {
		err := remote.Delete(ctx, z, record)
		switch {
		case err == nil:
			m.audit(ctx, row.TeamID, "dns_record.removed", row.Hostname, record.Describe())
		case errors.Is(err, ErrGone):
		case errors.Is(err, ErrChanged):
			m.log().Info("a record the panel created was changed outside it, so it was left where it is",
				"hostname", row.Hostname, "type", row.Type)
		default:
			return providerProblem(provider.Kind, err)
		}
	} else {
		m.log().Info("a record the panel created is gone or was changed outside it, so it was left where it is",
			"hostname", row.Hostname, "type", row.Type)
	}
	return m.DB.ForgetDNSRecord(ctx, row.ID)
}

// --- all of them ---

// Sync looks at every domain whose record the panel keeps, and at every
// automatic address in a zone the team connected, and does what is due:
// a record not made yet, one whose address changed, one not looked at for an
// hour, and the records of domains that are gone. With force, every kept
// record is looked at now.
func (m *Manager) Sync(ctx context.Context, force bool) {
	if !m.syncing.CompareAndSwap(false, true) {
		return
	}
	defer m.syncing.Store(false)

	orphans, err := m.DB.OrphanDNSRecords(ctx)
	if err != nil {
		m.log().Warn("could not list the DNS records of domains that are gone", "error", err)
	}
	for _, row := range orphans {
		if ctx.Err() != nil {
			return
		}
		m.releaseOrphan(ctx, row)
	}

	domains, err := m.DB.DomainsForDNS(ctx)
	if err != nil {
		m.log().Warn("could not list the domains whose DNS records the panel keeps", "error", err)
		return
	}
	providers := map[string][]store.DNSProvider{}
	targets := map[string]Target{}
	for _, d := range domains {
		if ctx.Err() != nil {
			return
		}
		// An internal app has no route from outside, so its domains are
		// not served and there is nothing to point.
		if d.Internal || (d.DNS != nil && !d.DNS.Manage) {
			continue
		}
		if _, ok := providers[d.TeamID]; !ok {
			list, err := m.DB.ListDNSProviders(ctx, d.TeamID)
			if err != nil {
				continue
			}
			providers[d.TeamID] = list
			targets[d.TeamID] = m.Target(ctx, d.TeamID)
		}
		provider, zone, ok := ZoneFor(providers[d.TeamID], d.Hostname)
		if !ok {
			continue
		}
		if !force && !m.due(ctx, d, provider, zone, targets[d.TeamID]) {
			continue
		}
		if err := m.Ensure(ctx, d.TeamID, d.Domain); err != nil {
			m.log().Debug("a domain's DNS record was not made", "hostname", d.Hostname, "error", errdoc.From(err).Title)
		}
	}
}

// AddressChanged is the hook for the cluster's address changing: every
// record the panel keeps is pointed at the new one now, rather than at the
// next sync.
func (m *Manager) AddressChanged(ctx context.Context) { m.Sync(ctx, true) }

// due says whether a domain's record needs looking at now.
func (m *Manager) due(ctx context.Context, d store.DomainForDNS, provider store.DNSProvider, zone store.DNSZone, t Target) bool {
	if d.DNS == nil {
		return true
	}
	switch d.DNS.State {
	case store.DNSStatePending, store.DNSStateFailed, "":
		return true
	}
	if m.now().Sub(d.DNS.UpdatedAt) > recheckAfter {
		return true
	}
	if d.DNS.State != store.DNSStateCreated {
		return false
	}
	// What the books say against what is wanted now: an address that
	// changed is found here, without asking the provider anything.
	want, err := Wants(t, provider.Kind, d.Hostname, zone.Name)
	if err != nil {
		return true
	}
	rows, err := m.DB.DNSRecordsForDomain(ctx, d.ID)
	if err != nil {
		return true
	}
	return signature(rowsAsRecords(rows)) != signature(wantsAsRecords(want))
}

func (m *Manager) releaseOrphan(ctx context.Context, row store.DNSRecord) {
	ctx, cancel := context.WithTimeout(ctx, ensureTimeout)
	defer cancel()
	defer m.lock(row.Hostname)()
	// Looked at again under the lock: the domain may have been added back,
	// and its record taken up by it, since the list was read.
	rows, err := m.DB.DNSRecordsAt(ctx, row.ProviderID, row.ZoneID, row.Hostname)
	if err != nil {
		return
	}
	for _, again := range rows {
		if again.ID == row.ID && again.DomainID == "" {
			if err := m.release(ctx, again); err != nil {
				m.log().Warn("could not remove the DNS record of a domain that is gone; tried again later",
					"hostname", row.Hostname, "error", errdoc.From(err).Title)
			}
		}
	}
}

func rowsAsRecords(rows []store.DNSRecord) []Record {
	out := make([]Record, len(rows))
	for i, row := range rows {
		out[i] = Record{Type: row.Type, Content: row.Content}
	}
	return out
}

func wantsAsRecords(want []Want) []Record {
	out := make([]Record, len(want))
	for i, w := range want {
		out[i] = Record{Type: strings.ToUpper(w.Type), Content: w.Content}
	}
	return out
}

func signature(records []Record) string {
	parts := make([]string, len(records))
	for i, r := range records {
		parts[i] = r.Type + " " + canonicalContent(r.Type, r.Content)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// panelID is this panel's name for itself, which its records' notes carry.
func (m *Manager) panelID(ctx context.Context) string {
	id, _, err := m.DB.GetSetting(ctx, settings.KeyPanelID)
	if err != nil || id == "" {
		return "panel"
	}
	return id
}

// audit records what the panel did at a provider. The panel did it, on the
// team's connection, so the panel is who it says did it.
func (m *Manager) audit(ctx context.Context, teamID, action, hostname, detail string) {
	event := store.AuditEvent{
		TeamID: teamID, ActorLabel: version.Name, Action: action,
		TargetType: "domain", TargetLabel: hostname + " " + detail,
	}
	if err := m.DB.RecordAudit(context.WithoutCancel(ctx), &event); err != nil {
		m.log().Warn("could not record an audit event", "action", action, "error", err)
	}
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
