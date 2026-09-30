package geoip

import (
	"net/netip"
	"os"
	"testing"
)

// databasesAt is where a real pair of databases can be put to run the lookups
// against. CI has no internet and no licence to ship 18 MB of data, so the
// tests that need them skip rather than fail — and say what would have run.
const envDatabases = "SKIFITY_GEOIP_DIR"

func realDatabases(t *testing.T) *Lookup {
	t.Helper()
	dir := os.Getenv(envDatabases)
	if dir == "" {
		t.Skipf("set %s to a directory holding dbip-country.mmdb and dbip-asn.mmdb to run this", envDatabases)
	}
	l := New()
	t.Cleanup(l.Close)
	if err := l.LoadCountry(dir + "/dbip-country.mmdb"); err != nil {
		t.Fatalf("LoadCountry: %v", err)
	}
	if err := l.LoadASN(dir + "/dbip-asn.mmdb"); err != nil {
		t.Fatalf("LoadASN: %v", err)
	}
	return l
}

// The struct tags are the part that breaks silently: a wrong one decodes to
// the zero value, every rule about a country becomes "unknown", and nothing
// anywhere says the name was wrong.
func TestLookupAgainstRealDatabases(t *testing.T) {
	l := realDatabases(t)

	cases := []struct {
		name    string
		ip      string
		country string
		asn     uint32
	}{
		// Cloudflare's resolver: one of the few addresses whose country and
		// network are stable enough to assert on.
		{"Cloudflare 1.1.1.1", "1.1.1.1", "AU", 13335},
		// Google's resolver.
		{"Google 8.8.8.8", "8.8.8.8", "US", 15169},
		// And the same address in the form a proxy hands over, which must not
		// quietly find nothing.
		{"an IPv4 address in its IPv6 clothing", "::ffff:8.8.8.8", "US", 15169},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := l.Lookup(netip.MustParseAddr(tc.ip))
			if got.Country != tc.country {
				t.Errorf("country = %q, want %q", got.Country, tc.country)
			}
			if got.ASN != tc.asn {
				t.Errorf("ASN = %d, want %d", got.ASN, tc.asn)
			}
			if got.ASNOrg == "" {
				t.Error("the network has no name, so the tag for it is wrong")
			}
		})
	}
}

// A panel on its first morning has downloaded nothing, which is the ordinary
// state and must answer "I do not know" rather than a country that matches
// nothing or a panic.
func TestNothingLoadedKnowsNothing(t *testing.T) {
	l := New()
	defer l.Close()

	if l.HasCountry() || l.HasASN() {
		t.Fatal("an empty lookup claimed to have databases")
	}
	got := l.Lookup(netip.MustParseAddr("1.1.1.1"))
	if got.Country != "" || got.ASN != 0 {
		t.Errorf("an empty lookup answered %+v, want nothing", got)
	}
	// An address nobody could parse is the other half of the same question.
	if got := l.Lookup(netip.Addr{}); got.Country != "" || got.ASN != 0 {
		t.Errorf("an invalid address answered %+v", got)
	}
}

// Clearing the setting has to unload the database rather than needing a
// restart, and a path to nothing has to say so rather than leaving the old one
// loaded while the panel reports success.
func TestLoadingAndUnloading(t *testing.T) {
	l := New()
	defer l.Close()

	if err := l.LoadCountry(""); err != nil {
		t.Errorf("unloading an absent database failed: %v", err)
	}
	err := l.LoadCountry("/nowhere/dbip-country.mmdb")
	if err == nil {
		t.Fatal("a path to nothing was accepted")
	}
	if l.HasCountry() {
		t.Error("a failed load left something loaded")
	}

	// Something that is a file and is not a database.
	bad := t.TempDir() + "/not.mmdb"
	if err := os.WriteFile(bad, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.LoadCountry(bad); err == nil {
		t.Error("a file that is not a database was accepted")
	}
}
