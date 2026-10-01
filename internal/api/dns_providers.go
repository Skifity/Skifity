package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/dnsprov"
	"skifity/internal/errdoc"
	"skifity/internal/runsafe"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// A team's DNS providers, and the records the panel keeps at them.
//
// Adding a domain used to end with a record to create by hand. With the zone
// connected — Cloudflare, Hetzner, DigitalOcean or Route 53 — the panel
// creates it, points it at the new address when the cluster moves, and
// deletes it with the domain. It only ever changes a record it created, and
// refuses rather than overwriting one somebody else made. The rules are in
// internal/dnsprov; this is who may ask, and the answers.

// dnsConnectTimeout bounds connecting, testing and listing zones: a few
// requests to the provider while somebody waits.
const dnsConnectTimeout = 30 * time.Second

// dnsSyncTimeout bounds the sync that follows the cluster's address changing.
const dnsSyncTimeout = 10 * time.Minute

// dnsProviderView is a connection as the panel shows it. Never its
// credentials.
type dnsProviderView struct {
	store.DNSProvider
	// Title is the provider's own name, such as Amazon Route 53.
	Title string `json:"title"`
	// KeepsNotes says whether a record the panel creates there carries the
	// "managed by Skifity" note.
	KeepsNotes bool `json:"keeps_notes"`
	// Records is how many records the panel keeps through it: what stays at
	// the provider if the connection is removed.
	Records int `json:"records"`
}

func (s *Server) dnsProviderViews(ctx context.Context, teamID string, providers []store.DNSProvider) ([]dnsProviderView, error) {
	counts, err := s.db.CountDNSRecords(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]dnsProviderView, len(providers))
	for i, provider := range providers {
		out[i] = s.dnsProviderView(provider, counts[provider.ID])
	}
	return out, nil
}

func (s *Server) dnsProviderView(provider store.DNSProvider, records int) dnsProviderView {
	return dnsProviderView{
		DNSProvider: provider, Title: dnsprov.Title(provider.Kind),
		KeepsNotes: dnsprov.KeepsNotes(provider.Kind), Records: records,
	}
}

// handleListDNSProviders lists the team's connections with their zones as
// last seen. Nothing is asked of any provider.
//
// A member limited to some projects is answered too: adding a domain to an
// app in their project is where the panel offers to create its record, and
// the zones are how it knows whether it can. Names and zones only.
func (s *Server) handleListDNSProviders(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, _, err := s.authorizeTeamMember(r, teamID, store.RoleViewer); err != nil {
		writeError(w, r, err)
		return
	}
	providers, err := s.db.ListDNSProviders(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	views, err := s.dnsProviderViews(r.Context(), teamID, providers)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, views)
}

type connectDNSProviderRequest struct {
	// Kind is cloudflare, hetzner, digitalocean or route53.
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	// Token is the API token, for every kind but Route 53.
	Token string `json:"token,omitempty"`
	// AccessKeyID and SecretAccessKey are Route 53's.
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
}

// maxDNSProviderName is as long as a connection's name gets.
const maxDNSProviderName = 64

func (s *Server) handleConnectDNSProvider(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	// An administrator's: the connection can change the team's DNS.
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	var req connectDNSProviderRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	if !slices.Contains(dnsprov.Kinds, req.Kind) {
		writeError(w, r, errdoc.BadRequest("Kind must be cloudflare, hetzner, digitalocean or route53."))
		return
	}
	if len(strings.TrimSpace(req.Name)) > maxDNSProviderName {
		writeError(w, r, errdoc.BadRequest("A connection's name is 64 characters at most."))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dnsConnectTimeout)
	defer cancel()
	provider, err := s.dns.Connect(ctx, teamID, req.Kind, req.Name, dnsprov.Credentials{
		Token: req.Token, AccessKeyID: req.AccessKeyID, SecretAccessKey: req.SecretAccessKey,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = errdoc.Conflict("The team already has a DNS provider called "+defaultString(req.Name, dnsprov.Title(req.Kind))+".",
				"Give this one another name, or remove the other first.")
		}
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "dns_provider.connected", "team", teamID, provider.Name+" ("+dnsprov.Title(provider.Kind)+")")
	// The automatic addresses in the zones just connected are given their
	// records now rather than at the next sync.
	s.syncDNSSoon(false)
	writeJSON(w, http.StatusCreated, s.dnsProviderView(provider, 0))
}

func (s *Server) handleDeleteDNSProvider(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	id := chi.URLParam(r, "providerID")
	provider, err := s.db.GetDNSProvider(r.Context(), teamID, id)
	if err != nil {
		writeError(w, r, dnsProviderNotFound(err, id))
		return
	}
	// The records it made stay at the provider: removing a connection is
	// not a reason to take every domain in its zones off the internet.
	left, err := s.db.DeleteDNSProvider(r.Context(), teamID, id)
	if err != nil {
		writeError(w, r, dnsProviderNotFound(err, id))
		return
	}
	s.audit(r, teamID, "dns_provider.removed", "team", teamID, provider.Name+" ("+dnsprov.Title(provider.Kind)+")")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "left": left})
}

// handleTestDNSProvider signs in with a saved connection and lists its zones
// again, which is also how zones added at the provider since reach the panel.
func (s *Server) handleTestDNSProvider(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	provider, err := s.refreshDNSProvider(r, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	counts, err := s.db.CountDNSRecords(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.dnsProviderView(provider, counts[provider.ID]))
}

// handleListDNSZones asks the provider for its zones now. A member's: it is a
// read, made with the team's credentials, of names the list already shows as
// last seen.
func (s *Server) handleListDNSZones(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}
	provider, err := s.refreshDNSProvider(r, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, provider.Zones)
}

func (s *Server) refreshDNSProvider(r *http.Request, teamID string) (store.DNSProvider, error) {
	id := chi.URLParam(r, "providerID")
	ctx, cancel := context.WithTimeout(r.Context(), dnsConnectTimeout)
	defer cancel()
	provider, err := s.dns.Refresh(ctx, teamID, id)
	if err != nil {
		return store.DNSProvider{}, dnsProviderNotFound(err, id)
	}
	return provider, nil
}

func dnsProviderNotFound(err error, id string) error {
	if errors.Is(err, store.ErrNotFound) {
		return errdoc.NotFound("DNS provider", id)
	}
	return err
}

// SyncDNS does what is due for every domain whose record the panel keeps.
// The minute tick calls it every few minutes.
func (s *Server) SyncDNS(ctx context.Context) { s.dns.Sync(ctx, false) }

// syncDNSSoon runs a sync beside the request that asked for it. With force,
// every kept record is looked at: the cluster's address changed.
func (s *Server) syncDNSSoon(force bool) {
	runsafe.Go(s.log, "keeping DNS records at the team's providers", func() {
		ctx, cancel := context.WithTimeout(context.Background(), dnsSyncTimeout)
		defer cancel()
		if force {
			s.dns.AddressChanged(ctx)
			return
		}
		s.dns.Sync(ctx, false)
	})
}

// addressSettings are the settings whose change moves where the team's
// domains point.
var addressSettings = []string{settings.KeyClusterIP, settings.KeyClusterIPv6, settings.KeyCloudflareTunnelToken}

// publicAddress is where a team's domains point, as the Domains tab says.
func (s *Server) publicAddress(ctx context.Context, teamID string) string {
	if s.cluster == nil {
		return ""
	}
	return s.cluster.PublicAddress(ctx, teamID)
}

// clusterIPv6 is the IPv6 address set for the cluster, or "".
func (s *Server) clusterIPv6(ctx context.Context) string {
	value, _, err := s.db.GetSetting(ctx, settings.KeyClusterIPv6)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// describeManagedDNS fills in, on each of an app's domains a connected zone
// covers, what the panel does about its record. Nothing is asked of any
// provider: it is the zones as last seen and the panel's own books.
func (s *Server) describeManagedDNS(ctx context.Context, teamID string, app store.App, domains []store.Domain) {
	if app.Internal {
		return
	}
	providers, err := s.db.ListDNSProviders(ctx, teamID)
	if err != nil || len(providers) == 0 {
		return
	}
	rows, err := s.db.DomainDNSForApp(ctx, app.ID)
	if err != nil {
		return
	}
	records, err := s.db.DNSRecordsForApp(ctx, app.ID)
	if err != nil {
		return
	}
	for i := range domains {
		provider, zone, ok := dnsprov.ZoneFor(providers, domains[i].Hostname)
		if !ok {
			continue
		}
		managed := &store.ManagedDNS{
			ProviderID: provider.ID, Provider: provider.Kind, ProviderName: provider.Name, Zone: zone.Name,
			Records: []store.DNSRecord{},
		}
		if row, has := rows[domains[i].ID]; has {
			managed.Manage, managed.State, managed.UpdatedAt = row.Manage, row.State, row.UpdatedAt
			if row.Problem != "" && json.Valid([]byte(row.Problem)) {
				managed.Problem = json.RawMessage(row.Problem)
			}
		} else if domains[i].Auto {
			// The sync takes an automatic address on by itself.
			managed.Manage, managed.State = true, store.DNSStatePending
		}
		for _, record := range records {
			if record.DomainID == domains[i].ID && record.Keep {
				managed.Records = append(managed.Records, record)
			}
		}
		domains[i].ManagedDNS = managed
	}
}

// startManagingDNS is a domain's switch turned on: its record is created
// now, or the reason it cannot be is kept on the domain and returned.
func (s *Server) startManagingDNS(ctx context.Context, teamID string, app store.App, domain store.Domain) error {
	if app.Internal {
		return errdoc.BadRequest("An internal app is not reached from outside, so its domains have no DNS record to create.")
	}
	providers, err := s.db.ListDNSProviders(ctx, teamID)
	if err != nil {
		return err
	}
	if _, _, ok := dnsprov.ZoneFor(providers, domain.Hostname); !ok {
		return errdoc.DNSNoZone(domain.Hostname)
	}
	if err := s.db.SetDomainDNS(ctx, store.DomainDNS{DomainID: domain.ID, Manage: true, State: store.DNSStatePending}); err != nil {
		return err
	}
	return s.dns.Ensure(ctx, teamID, domain)
}
