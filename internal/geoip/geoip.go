// Package geoip answers which country and which network an address belongs to.
//
// It exists because a rule about a country is worth nothing without it, and
// because the alternative — asking a web service per request — would put a
// third party in the path of every request to every app, which is the thing
// self-hosting is for avoiding.
//
// # Where the data comes from
//
// DB-IP publish a free country database and a free network database every
// month, in MaxMind's own format, under CC BY 4.0 and with no account, no
// licence key and no sign-up. That last part is why they are the default:
// MaxMind's GeoLite2 is the better-known dataset and needs a registered account
// and a licence key before a single byte can be downloaded, which is a setup
// step in the middle of turning on a firewall rule.
//
// The licence asks for attribution, and the panel gives it on the page where
// the rules are written rather than in a file nobody opens.
//
// An operator who has a MaxMind subscription, or a copy of either file on their
// own network, points the setting at it instead. The format is the same.
//
// # What it deliberately does not do
//
// It does not download anything by itself and it does not know about settings.
// Loading is the caller's, so that the one process that reaches the internet
// for these files is the one an operator chose to run.
package geoip

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sync"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Attribution is what the CC BY 4.0 licence asks for, and what the panel shows
// beside the rules when either database is loaded from its default source.
const Attribution = "IP Geolocation by DB-IP"

// AttributionURL is the link that attribution has to carry.
const AttributionURL = "https://db-ip.com"

// DefaultCountryURL and DefaultASNURL are the current month's free databases.
//
// The month is substituted by the caller, because a package that reads the
// clock is a package whose tests have to as well.
const (
	DefaultCountryURL = "https://download.db-ip.com/free/dbip-country-lite-%s.mmdb.gz"
	DefaultASNURL     = "https://download.db-ip.com/free/dbip-asn-lite-%s.mmdb.gz"
)

// Result is what is known about an address.
//
// Both are zero when nothing is known, which is what the rules engine turns
// into "unknown" rather than into a decision.
type Result struct {
	Country string
	ASN     uint32
	// ASNOrg is the network's name, for showing beside a rule rather than for
	// matching. A rule matches on the number, which does not get renamed.
	ASNOrg string
}

// Lookup answers for one address.
type Lookup struct {
	mu      sync.RWMutex
	country *maxminddb.Reader
	asn     *maxminddb.Reader
}

// New returns a Lookup that knows nothing, which every caller must handle
// anyway: a database that has not been downloaded yet is the ordinary state of
// a panel on its first morning.
func New() *Lookup { return &Lookup{} }

// LoadCountry opens a country database, replacing whatever was loaded.
//
// An empty path unloads it, which is how clearing the setting takes effect
// without a restart.
func (l *Lookup) LoadCountry(path string) error {
	reader, err := open(path)
	if err != nil {
		return fmt.Errorf("open the country database: %w", err)
	}
	l.mu.Lock()
	old := l.country
	l.country = reader
	l.mu.Unlock()
	closeReader(old)
	return nil
}

// LoadASN opens a network database, replacing whatever was loaded.
func (l *Lookup) LoadASN(path string) error {
	reader, err := open(path)
	if err != nil {
		return fmt.Errorf("open the network database: %w", err)
	}
	l.mu.Lock()
	old := l.asn
	l.asn = reader
	l.mu.Unlock()
	closeReader(old)
	return nil
}

func open(path string) (*maxminddb.Reader, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s is not there", path)
		}
		return nil, err
	}
	return maxminddb.Open(path)
}

func closeReader(r *maxminddb.Reader) {
	if r != nil {
		_ = r.Close()
	}
}

// Close releases both databases.
func (l *Lookup) Close() {
	l.mu.Lock()
	country, asn := l.country, l.asn
	l.country, l.asn = nil, nil
	l.mu.Unlock()
	closeReader(country)
	closeReader(asn)
}

// HasCountry and HasASN report which questions can be answered, which is what
// the panel asks before letting somebody save a rule that depends on one.
func (l *Lookup) HasCountry() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.country != nil
}

// HasASN reports whether the network database is loaded.
func (l *Lookup) HasASN() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.asn != nil
}

// Lookup answers for an address. A field nothing knows stays zero.
func (l *Lookup) Lookup(ip netip.Addr) Result {
	if !ip.IsValid() {
		return Result{}
	}
	// A proxy hands over an IPv4 address in its IPv6 clothing often enough that
	// looking it up in that form — and finding nothing — would be a quiet way
	// for every rule about a country to stop working.
	ip = ip.Unmap()

	l.mu.RLock()
	country, asn := l.country, l.asn
	l.mu.RUnlock()

	var out Result
	if country != nil {
		var record struct {
			Country struct {
				ISOCode string `maxminddb:"iso_code"`
			} `maxminddb:"country"`
		}
		if err := country.Lookup(ip).Decode(&record); err == nil {
			out.Country = record.Country.ISOCode
		}
	}
	if asn != nil {
		var record struct {
			Number uint32 `maxminddb:"autonomous_system_number"`
			Org    string `maxminddb:"autonomous_system_organization"`
		}
		if err := asn.Lookup(ip).Decode(&record); err == nil {
			out.ASN = record.Number
			out.ASNOrg = record.Org
		}
	}
	return out
}
