package edgerules

import (
	"net/http"
	"net/netip"
	"testing"
)

func hdr(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

func clusterTrust() Trust {
	return Trust{Proxies: DefaultProxies("10.42.0.0/16", "10.43.0.0/16")}
}

// Every rule in this package is worth exactly as much as this function. The
// cases that matter are the ones where believing a header is the way in.
func TestClientIP(t *testing.T) {
	cases := []struct {
		name    string
		peer    string
		headers http.Header
		trust   Trust
		want    string
	}{
		{
			// The ordinary case: the ingress controller is a pod, it appended
			// the client, and the client is what we judge.
			name: "through our own ingress",
			peer: "10.42.1.5", headers: hdr("X-Forwarded-For", "203.0.113.7"),
			trust: clusterTrust(), want: "203.0.113.7",
		},
		{
			// The attack: a client sends its own X-Forwarded-For to claim an
			// address on the allowlist. The ingress appends the real one on the
			// right, and walking from the right finds it.
			name:    "a client that wrote its own header",
			peer:    "10.42.1.5",
			headers: hdr("X-Forwarded-For", "203.0.113.7, 198.51.100.9"),
			trust:   clusterTrust(), want: "198.51.100.9",
		},
		{
			// Nothing in front of us that we trust, so the header is the
			// client's own writing and the connection is the only truth.
			name: "a direct connection claiming to be somebody else",
			peer: "198.51.100.9", headers: hdr("X-Forwarded-For", "203.0.113.7"),
			trust: clusterTrust(), want: "198.51.100.9",
		},
		{
			// Two of our own hops, then the client.
			name:    "cloudflared and then the ingress",
			peer:    "10.42.1.5",
			headers: hdr("X-Forwarded-For", "203.0.113.7, 10.42.9.9"),
			trust:   clusterTrust(), want: "203.0.113.7",
		},
		{
			// Junk in the list stops the walk rather than being skipped:
			// stepping past an entry we cannot vouch for would start believing
			// the ones behind it, which is exactly the way in.
			name:    "junk in the middle of the chain",
			peer:    "10.42.1.5",
			headers: hdr("X-Forwarded-For", "203.0.113.7, not-an-address"),
			trust:   clusterTrust(), want: "10.42.1.5",
		},
		{
			name: "no header at all",
			peer: "203.0.113.7", headers: hdr(), trust: clusterTrust(), want: "203.0.113.7",
		},
		{
			// Inside the cluster talking to another app: every hop is ours.
			name: "a request that never left the cluster",
			peer: "10.42.1.5", headers: hdr("X-Forwarded-For", "10.42.7.7"),
			trust: clusterTrust(), want: "10.42.7.7",
		},
		{
			// Cloudflare overwrites this header at its edge, so a client cannot
			// set it — but only a trusted peer may hand it to us.
			name:    "the Cloudflare header from our own tunnel",
			peer:    "10.42.1.5",
			headers: hdr("CF-Connecting-IP", "203.0.113.7", "X-Forwarded-For", "198.51.100.9"),
			trust:   Trust{Proxies: clusterTrust().Proxies, Cloudflare: true},
			want:    "203.0.113.7",
		},
		{
			name: "the same header from somebody on the internet",
			peer: "198.51.100.9", headers: hdr("CF-Connecting-IP", "203.0.113.7"),
			trust: Trust{Proxies: clusterTrust().Proxies, Cloudflare: true},
			want:  "198.51.100.9",
		},
		{
			// It is only believed when the tunnel is actually in front.
			name:    "the Cloudflare header with no tunnel installed",
			peer:    "10.42.1.5",
			headers: hdr("CF-Connecting-IP", "203.0.113.7", "X-Forwarded-For", "198.51.100.9"),
			trust:   clusterTrust(), want: "198.51.100.9",
		},
		{
			// A proxy handing over the IPv4-in-IPv6 form must be the same
			// address as far as a rule is concerned.
			name: "an IPv4 address in its IPv6 clothing",
			peer: "10.42.1.5", headers: hdr("X-Forwarded-For", "::ffff:203.0.113.7"),
			trust: clusterTrust(), want: "203.0.113.7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClientIP(netip.MustParseAddr(tc.peer), tc.headers, tc.trust)
			if got.String() != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// The country header is believed on exactly the same terms, and its two
// non-answers are not treated as countries.
func TestClientCountry(t *testing.T) {
	tunnel := Trust{Proxies: clusterTrust().Proxies, Cloudflare: true}
	cases := []struct {
		name  string
		peer  string
		value string
		trust Trust
		want  string
	}{
		{"from our own tunnel", "10.42.1.5", "ID", tunnel, "ID"},
		{"lowercase from the wire", "10.42.1.5", "id", tunnel, "ID"},
		{"from somebody on the internet", "198.51.100.9", "ID", tunnel, ""},
		{"with no tunnel installed", "10.42.1.5", "ID", clusterTrust(), ""},
		// XX is Cloudflare saying it does not know, and T1 is Tor. Both are
		// honest answers and neither is a country.
		{"an address Cloudflare could not place", "10.42.1.5", "XX", tunnel, ""},
		{"Tor", "10.42.1.5", "T1", tunnel, ""},
		{"nonsense", "10.42.1.5", "Indonesia", tunnel, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClientCountry(netip.MustParseAddr(tc.peer), hdr("CF-IPCountry", tc.value), tc.trust)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A typo in the trusted-proxy setting must not be the reason a site stops
// answering, so the reader drops what it cannot parse. The setting's own
// validator is what tells the operator, at the moment they save it.
func TestParseProxies(t *testing.T) {
	got := ParseProxies("203.0.113.0/24, 198.51.100.9\n nonsense  2001:db8::/32")
	if len(got) != 3 {
		t.Fatalf("parsed %v, want three ranges", got)
	}
	if got[1].String() != "198.51.100.9/32" {
		t.Errorf("a bare address became %s, want a single-address range", got[1])
	}
}
