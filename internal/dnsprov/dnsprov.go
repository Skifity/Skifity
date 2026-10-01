// Package dnsprov talks to the DNS providers a team connects: Cloudflare,
// Hetzner, DigitalOcean and Amazon Route 53.
//
// The panel asks a provider for four things and no more: the zones the
// credentials can see, the records at one name, and to create, change or
// delete one record. Everything that decides *whether* to — which records are
// the panel's own, what counts as a conflict, what an address change means —
// is Plan, in plan.go, which is pure and is where the rules are tested.
//
// Every request goes through internal/netguard. The addresses are the
// providers' own and are not configurable from the API; a test that points a
// provider at a server of its own has to swap the HTTP client as well, and
// the guarded client refuses that server because it is on loopback.
package dnsprov

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"skifity/internal/netguard"
)

// The providers there are.
const (
	Cloudflare   = "cloudflare"
	Hetzner      = "hetzner"
	DigitalOcean = "digitalocean"
	Route53      = "route53"
)

// Kinds lists them, in the order the panel offers them.
var Kinds = []string{Cloudflare, Hetzner, DigitalOcean, Route53}

// Title is a provider's own name, as a person writes it.
func Title(kind string) string {
	switch kind {
	case Cloudflare:
		return "Cloudflare"
	case Hetzner:
		return "Hetzner"
	case DigitalOcean:
		return "DigitalOcean"
	case Route53:
		return "Amazon Route 53"
	}
	return kind
}

// KeepsNotes reports whether a provider stores a note with each record, so a
// record the panel made says so where people look. Cloudflare keeps a comment
// and Hetzner a comment and labels. DigitalOcean and Route 53 have nowhere to
// put one: for them the panel's own record of what it created is all there is,
// which is what it goes by for every provider anyway.
func KeepsNotes(kind string) bool { return kind == Cloudflare || kind == Hetzner }

// Proxies reports whether a provider can put its own proxy in front of a
// record. Only Cloudflare does, and only it can serve a Cloudflare tunnel.
func Proxies(kind string) bool { return kind == Cloudflare }

// Credentials are what a connection signs in with. Which fields are used
// depends on the provider: a token for Cloudflare, Hetzner and DigitalOcean,
// an access key for Route 53.
type Credentials struct {
	Token           string `json:"token,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
}

// Zone is one domain a provider serves.
type Zone struct {
	// ID is the provider's own: Cloudflare's and Hetzner's id, Route 53's
	// hosted zone id, and for DigitalOcean the domain name itself.
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Record is one DNS record as a provider holds it.
type Record struct {
	// ID is the provider's handle on the record. A provider that stores a
	// name and type as one set of values — Hetzner, Route 53 — gives every
	// value in the set the same ID.
	ID   string `json:"id"`
	Type string `json:"type"`
	// Name is fully qualified, lower case, with no final dot.
	Name string `json:"name"`
	// Content is an address, or for a CNAME a name, lower case and with no
	// final dot, whatever spelling the provider stores.
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	// Proxied is Cloudflare's orange cloud.
	Proxied bool `json:"proxied,omitempty"`
	// Note is the comment the provider keeps with the record, if it keeps one.
	Note string `json:"note,omitempty"`
}

// Describe is a record as a sentence names it: "A 203.0.113.10".
func (r Record) Describe() string { return r.Type + " " + r.Content }

// Provider is one connection, signed in.
type Provider interface {
	Kind() string
	// Zones lists every public zone the credentials can see.
	Zones(ctx context.Context) ([]Zone, error)
	// Records lists every record at exactly this name, of every type.
	Records(ctx context.Context, zone Zone, name string) ([]Record, error)
	// Create adds a record and answers it as the provider stored it.
	Create(ctx context.Context, zone Zone, record Record) (Record, error)
	// SetContent points a record the panel created somewhere else and
	// changes nothing else about it: not its proxy, not its TTL, not its note.
	SetContent(ctx context.Context, zone Zone, record Record, content string) (Record, error)
	// Delete removes a record. ErrGone when it is not there any more.
	Delete(ctx context.Context, zone Zone, record Record) error
}

var (
	// ErrGone is a record that is not at the provider any more.
	ErrGone = errors.New("the record is not there any more")
	// ErrChanged is a provider refusing a change because the record is no
	// longer what the panel left there. Only Route 53 says so itself; for the
	// others the panel looks before it writes.
	ErrChanged = errors.New("the record is no longer what the panel left there")
)

// APIError is a provider answering with a failure.
type APIError struct {
	Provider string
	Status   int
	Message  string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s answered %d", Title(e.Provider), e.Status)
	}
	return fmt.Sprintf("%s answered %d: %s", Title(e.Provider), e.Status, e.Message)
}

// Refused reports whether the credentials themselves were turned down, as
// opposed to the provider failing: that is a different problem with a
// different fix.
func (e *APIError) Refused() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// IsRefused reports whether err is a provider turning the credentials down.
func IsRefused(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Refused()
}

// requestTimeout bounds one request. Every one is small, and each is made
// while somebody waits or the sync holds its lock.
const requestTimeout = 20 * time.Second

// guarded is the client every provider uses outside tests.
var guarded = netguard.Client(requestTimeout)

// New signs in to a provider, at its real address, through the guard.
func New(kind string, creds Credentials) (Provider, error) {
	return open(kind, creds, "", guarded)
}

// open is New with the address and the client chosen by the caller, which
// only this package's tests do.
func open(kind string, creds Credentials, base string, client *http.Client) (Provider, error) {
	if err := CheckCredentials(kind, creds); err != nil {
		return nil, err
	}
	switch kind {
	case Cloudflare:
		return &cloudflare{token: creds.Token, base: or(base, cloudflareAPI), client: client}, nil
	case Hetzner:
		return &hetzner{token: creds.Token, base: or(base, hetznerAPI), client: client}, nil
	case DigitalOcean:
		return &digitalocean{token: creds.Token, base: or(base, digitaloceanAPI), client: client}, nil
	case Route53:
		return &route53{creds: creds, base: or(base, route53API), client: client, now: time.Now}, nil
	}
	return nil, fmt.Errorf("%q is not a DNS provider this panel knows", kind)
}

func or(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// ErrGlobalKey is a Cloudflare Global API Key pasted where a token belongs.
var ErrGlobalKey = errors.New("that is Cloudflare's Global API Key, which can do anything in the account")

// globalKey is the shape of a Cloudflare Global API Key: 37 lower-case hex
// characters. An API token is 40 characters of letters, digits, - and _.
var globalKey = regexp.MustCompile(`^[0-9a-f]{37}$`)

// CheckCredentials refuses credentials that are the wrong shape before any of
// them is sent anywhere.
func CheckCredentials(kind string, creds Credentials) error {
	switch kind {
	case Cloudflare, Hetzner, DigitalOcean:
		token := strings.TrimSpace(creds.Token)
		if token == "" {
			return fmt.Errorf("%s needs an API token", Title(kind))
		}
		if strings.ContainsAny(token, " \t\r\n") {
			return errors.New("paste the token only: it has no spaces in it")
		}
		if kind == Cloudflare && globalKey.MatchString(token) {
			return ErrGlobalKey
		}
	case Route53:
		if strings.TrimSpace(creds.AccessKeyID) == "" || strings.TrimSpace(creds.SecretAccessKey) == "" {
			return errors.New("an access key id and its secret access key are both needed for Route 53")
		}
		if strings.ContainsAny(creds.AccessKeyID+creds.SecretAccessKey, " \t\r\n") {
			return errors.New("paste the access key id and the secret only: neither has spaces in it")
		}
	default:
		return fmt.Errorf("%q is not a DNS provider this panel knows", kind)
	}
	return nil
}

// Canonical is a name as DNS compares it: lower case, with no final dot.
func Canonical(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// canonicalContent is a record's value as Plan compares it: an address in
// its standard form, a name as Canonical has it.
func canonicalContent(kind, content string) string {
	content = strings.TrimSpace(content)
	if ip := net.ParseIP(content); ip != nil && (kind == "A" || kind == "AAAA") {
		return ip.String()
	}
	return Canonical(content)
}

// relative is a name inside its zone, "@" for the zone itself, as Hetzner and
// DigitalOcean write it.
func relative(name, zone string) string {
	name, zone = Canonical(name), Canonical(zone)
	if name == zone {
		return "@"
	}
	return strings.TrimSuffix(name, "."+zone)
}

// absolute is the other way round.
func absolute(name, zone string) string {
	name = Canonical(name)
	if name == "@" || name == "" {
		return Canonical(zone)
	}
	return name + "." + Canonical(zone)
}

// MatchZone picks, of the zones given, the one a hostname is in: the longest
// that is the hostname itself or one it ends in, label by label. Longest,
// because a team may connect both example.co.uk and shop.example.co.uk, and a
// record for www.shop.example.co.uk belongs in the second. By label, because
// myexample.com does not contain anything in example.com.
//
// It answers the index into zones, or -1. Two zones of the same name — the
// same domain reached through two connections — go to the first given.
func MatchZone(hostname string, zones []string) int {
	hostname = Canonical(hostname)
	best, bestLength := -1, -1
	for i, zone := range zones {
		zone = Canonical(zone)
		if zone == "" {
			continue
		}
		if hostname != zone && !strings.HasSuffix(hostname, "."+zone) {
			continue
		}
		if len(zone) > bestLength {
			best, bestLength = i, len(zone)
		}
	}
	return best
}

// Marker is the note a record the panel created carries, where the provider
// keeps one: which panel made it, so a person reading the zone knows not to
// edit it by hand and which install to look at. Cloudflare allows 100
// characters in a comment on its free plan; this is far below that.
func Marker(panelID string) string { return "managed by Skifity " + panelID }

// sortZones puts zones in name order, which is how the panel lists them.
func sortZones(zones []Zone) []Zone {
	sort.Slice(zones, func(i, j int) bool { return zones[i].Name < zones[j].Name })
	return zones
}
