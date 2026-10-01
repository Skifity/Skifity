package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/dnsprov"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// Checking a domain's DNS before it goes live.
//
// A domain of somebody's own works once its record points at the cluster, and
// until then the only sign was a certificate that stayed "waiting" — for a
// minute when the record was right and new, and forever when it pointed at the
// old host. Asking DNS is what tells the two apart, so the panel asks: when a
// domain is added, and whenever somebody presses the button.
//
// Adding a domain is never refused for its DNS. People add the domain first
// and create the record second, because the panel is what tells them what the
// record should say.

// Resolver is what the panel asks DNS through. *net.Resolver is one; a test
// passes its own, so that nothing in the tests reaches the network.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
}

// systemResolver is what a panel asks when Options names no resolver.
var systemResolver Resolver = net.DefaultResolver

const (
	// dnsCheckTimeout bounds a check somebody asked for. A resolver that has
	// not answered in five seconds is not going to, and the button should say
	// so rather than spin.
	dnsCheckTimeout = 5 * time.Second
	// dnsCheckOnAddTimeout is shorter: the check that comes with adding a
	// domain is a courtesy, and the domain is added whether it answers or not.
	dnsCheckOnAddTimeout = 3 * time.Second
)

// What a hostname's DNS says.
const (
	// DNSHere is every address the hostname has being the cluster's.
	DNSHere = "here"
	// DNSPartly is some of them being the cluster's and some not, which is
	// a visitor reaching the app or not depending on which one they get. An
	// old AAAA record left beside a new A record is the usual cause.
	DNSPartly = "partly"
	// DNSElsewhere is none of them being the cluster's.
	DNSElsewhere = "elsewhere"
	// DNSMissing is the hostname having no address at all yet.
	DNSMissing = "missing"
	// DNSUnknown is addresses found and nothing to compare them with: the
	// panel does not know its own public address.
	DNSUnknown = "unknown"
	// DNSProxied is every address being Cloudflare's: the hostname is behind
	// Cloudflare's proxy, or a Cloudflare tunnel, and DNS cannot say where
	// Cloudflare sends it. When the panel keeps the record at Cloudflare it
	// can, from its own record, and says so in OriginConfirmed.
	DNSProxied = "proxied"
)

// DNSRecord is one record a hostname was found to have.
type DNSRecord struct {
	// Type is A, AAAA or CNAME.
	Type  string `json:"type"`
	Value string `json:"value"`
	// Here says the address is one of the cluster's. Always false for a
	// CNAME, whose name is followed to the addresses listed after it.
	Here bool `json:"here"`
	// Cloudflare says the address is one of Cloudflare's own, which a
	// proxied hostname resolves to.
	Cloudflare bool `json:"cloudflare,omitempty"`
}

// DNSCheck is what a hostname's DNS says, against where it has to point.
type DNSCheck struct {
	Hostname   string      `json:"hostname"`
	Status     string      `json:"status"`
	PointsHere bool        `json:"points_here"`
	Found      []DNSRecord `json:"found"`
	// Expected is every address that counts as here: the one the panel asks
	// people to use first, then each ready server's own.
	Expected  []string  `json:"expected"`
	CheckedAt time.Time `json:"checked_at"`
	// OriginConfirmed is a proxied hostname whose record at Cloudflare the
	// panel keeps and says where the cluster is: DNS cannot see past the
	// proxy, and the panel's own record can.
	OriginConfirmed bool `json:"origin_confirmed,omitempty"`
}

// checkDNS looks a hostname up and compares what it finds with the addresses
// that are the cluster's.
//
// An answer of "no such host" is an answer — the record has not been created
// yet — and is reported as missing. A resolver that times out or fails is not
// an answer, and is a problem the caller reports.
func checkDNS(ctx context.Context, resolver Resolver, hostname string, expected []string) (DNSCheck, error) {
	check := DNSCheck{
		Hostname:  hostname,
		Found:     []DNSRecord{},
		Expected:  expected,
		CheckedAt: time.Now().UTC(),
	}
	if check.Expected == nil {
		check.Expected = []string{}
	}

	addresses, err := resolver.LookupIPAddr(ctx, hostname)
	if err != nil && !notFound(err) {
		return DNSCheck{}, errdoc.DNSLookupFailed(hostname, err)
	}
	if len(addresses) == 0 {
		check.Status = DNSMissing
		return check, nil
	}

	// The addresses that count as here, and the names: a load balancer's
	// hostname set as the cluster's address is followed to its addresses, and
	// a CNAME to that name is pointing exactly where it was told to.
	ours := map[string]bool{}
	names := map[string]bool{}
	for _, target := range expected {
		if ip := net.ParseIP(target); ip != nil {
			ours[ip.String()] = true
			continue
		}
		names[canonicalName(target)] = true
		if resolved, err := resolver.LookupIPAddr(ctx, target); err == nil {
			for _, address := range resolved {
				ours[address.IP.String()] = true
			}
		}
	}

	viaExpectedName := false
	if cname, err := resolver.LookupCNAME(ctx, hostname); err == nil {
		if name := canonicalName(cname); name != "" && name != canonicalName(hostname) {
			check.Found = append(check.Found, DNSRecord{Type: "CNAME", Value: name})
			viaExpectedName = names[name]
		}
	}

	here, cloudflare, seen := 0, 0, map[string]bool{}
	for _, address := range addresses {
		value := address.IP.String()
		if seen[value] {
			continue
		}
		seen[value] = true
		record := DNSRecord{Type: "AAAA", Value: value, Here: viaExpectedName || ours[value]}
		if address.IP.To4() != nil {
			record.Type = "A"
		}
		if record.Here {
			here++
		} else if dnsprov.CloudflareAddress(value) {
			record.Cloudflare = true
			cloudflare++
		}
		check.Found = append(check.Found, record)
	}

	switch {
	// Behind Cloudflare: its addresses and none of the cluster's. Not
	// "elsewhere", which is what a working domain behind the orange cloud
	// used to be called.
	case here == 0 && cloudflare == len(seen):
		check.Status = DNSProxied
	case len(ours) == 0 && len(names) == 0:
		check.Status = DNSUnknown
	case here == len(seen):
		check.Status = DNSHere
	case here > 0:
		check.Status = DNSPartly
	default:
		check.Status = DNSElsewhere
	}
	check.PointsHere = check.Status == DNSHere
	return check, nil
}

// notFound reports an answer that says the name has no such record.
func notFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

// canonicalName is a name as DNS compares it: lowercase, no final dot.
func canonicalName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// dnsTargets is every address a domain of the team's can point at to reach
// the cluster: the one the Domains tab tells people to use, first, then each
// ready server's own. Traefik answers on every server, so a record that names
// any of them reaches the app.
func (s *Server) dnsTargets(ctx context.Context, teamID string) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	add(s.publicAddress(ctx, teamID))
	add(s.clusterIPv6(ctx))
	if servers, err := s.db.ListServers(ctx, teamID); err == nil {
		for _, server := range servers {
			if server.Status == store.ServerReady {
				add(server.ExternalIP)
			}
		}
	}
	return out
}

// checkDomainDNS is the check for one of an app's domains.
func (s *Server) checkDomainDNS(ctx context.Context, app store.App, domain store.Domain, timeout time.Duration) (DNSCheck, error) {
	teamID, err := s.db.TeamIDForApp(ctx, app.ID)
	if err != nil {
		return DNSCheck{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	check, err := checkDNS(ctx, s.resolver, domain.Hostname, s.dnsTargets(ctx, teamID))
	if err != nil || check.Status != DNSProxied {
		return check, err
	}
	check.OriginConfirmed = s.originConfirmed(ctx, teamID, domain)
	check.PointsHere = check.OriginConfirmed
	return check, nil
}

// originConfirmed reports whether the records the panel keeps for a domain at
// Cloudflare say what they should now. Its books are checked against the
// provider every hour and whenever the address changes, so they are what is
// at Cloudflare.
func (s *Server) originConfirmed(ctx context.Context, teamID string, domain store.Domain) bool {
	rows, err := s.db.DomainDNSForApp(ctx, domain.AppID)
	if err != nil || rows[domain.ID].State != store.DNSStateCreated {
		return false
	}
	providers, err := s.db.ListDNSProviders(ctx, teamID)
	if err != nil {
		return false
	}
	provider, zone, ok := dnsprov.ZoneFor(providers, domain.Hostname)
	if !ok || provider.Kind != dnsprov.Cloudflare {
		return false
	}
	want, err := dnsprov.Wants(s.dns.Target(ctx, teamID), provider.Kind, domain.Hostname, zone.Name)
	if err != nil {
		return false
	}
	records, err := s.db.DNSRecordsForDomain(ctx, domain.ID)
	if err != nil || len(records) != len(want) {
		return false
	}
	for _, w := range want {
		found := false
		for _, record := range records {
			if record.Keep && record.Type == w.Type && dnsprov.Canonical(record.Content) == dnsprov.Canonical(w.Content) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// handleCheckDomainDNS answers whether a domain's DNS points at the cluster.
//
// A member's, like adding the domain: it is a request the panel makes on the
// team's behalf, to a name the team chose. A viewer sees the record to create
// in the list either way.
func (s *Server) handleCheckDomainDNS(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	domainID := chi.URLParam(r, "domainID")
	// The domain has to be this app's: the id in the path is not trusted to be.
	domains, err := s.db.ListDomains(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, domain := range domains {
		if domain.ID != domainID {
			continue
		}
		check, err := s.checkDomainDNS(r.Context(), app, domain, dnsCheckTimeout)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, check)
		return
	}
	writeError(w, r, errdoc.NotFound("domain", domainID))
}

// addedDomain is a domain as adding it answers: the domain, and what its DNS
// said at that moment when the panel could find out.
type addedDomain struct {
	store.Domain
	DNS *DNSCheck `json:"dns,omitempty"`
}
