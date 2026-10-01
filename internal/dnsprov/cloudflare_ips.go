package dnsprov

import "net/netip"

// Cloudflare's own addresses, which a proxied hostname resolves to instead of
// the server behind it.
//
// A domain behind Cloudflare's proxy (the orange cloud), or pointed at a
// Cloudflare tunnel, answers DNS with these, and a DNS check that compared
// them with the cluster's addresses said "points elsewhere" about a domain
// that works. Knowing them is how the check says "behind Cloudflare" instead.
//
// Published at https://www.cloudflare.com/ips-v4 and /ips-v6; Cloudflare
// changes them rarely and announces it. An address missing from here makes the
// check say "elsewhere", which is what it said before this list existed.
var cloudflareRanges = mustPrefixes(
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
)

func mustPrefixes(values ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(values))
	for i, value := range values {
		out[i] = netip.MustParsePrefix(value)
	}
	return out
}

// CloudflareAddress reports whether an address is one of Cloudflare's.
func CloudflareAddress(address string) bool {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, prefix := range cloudflareRanges {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}
