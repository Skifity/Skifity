package edgerules

import (
	"net/netip"
	"strings"
)

// Working out who is actually asking.
//
// Every rule in this package is worth exactly as much as this file. A firewall
// that reads the wrong address does not fail loudly: it blocks the ingress
// controller instead of the attacker, or it lets an attacker choose the address
// it will be judged by, by sending a header.
//
// X-Forwarded-For is a list, appended to by each proxy. The last entry was
// added by the proxy nearest to us and is therefore the only one we know is
// true; every entry to the left of it was added by something further away and
// could have been written by the client itself. So the address to use is found
// by walking the list from the right and stopping at the first entry that did
// not come from a proxy we trust.
//
// Which is why trust is a list of ranges and not a number of hops. A hop count
// is wrong the moment somebody adds a load balancer, and the failure is silent.

// Trust says which upstreams may speak for somebody else.
type Trust struct {
	// Proxies are the ranges our own infrastructure occupies: the pod network,
	// the service network, and anything an operator adds for a load balancer
	// sitting in front of the cluster.
	Proxies []netip.Prefix
	// Cloudflare, when true, means requests arrive through the tunnel and the
	// CF-Connecting-IP and CF-IPCountry headers may be believed — but only when
	// the peer that sent them is a trusted proxy, which is what stops anybody
	// on the internet setting their own country.
	Cloudflare bool
}

// Contains reports whether an address belongs to a trusted proxy.
func (t Trust) Contains(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, prefix := range t.Proxies {
		if prefix.Addr().Unmap().BitLen() == ip.BitLen() && prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// Headers is the subset of a request's headers this package reads. It is an
// interface so that the caller can pass http.Header, a map, or a fake.
type Headers interface {
	Get(name string) string
}

// MultiHeaders is a Headers whose every value can be read.
//
// A header can arrive more than once, and Get answers with the first one. For
// a header a client is also free to send, the first one is the client's:
// anything a proxy added afterwards sits behind it, unread. A proxy that
// appends a second X-Forwarded-For line rather than extending the first would
// therefore have its chain ignored in favour of the one the attacker wrote,
// and every address rule would judge an address the attacker chose — the exact
// failure the top of this file warns about.
//
// So where a caller can offer every value, it is asked for every value.
// http.Header can.
type MultiHeaders interface {
	Headers
	Values(name string) []string
}

func headerValues(headers Headers, name string) []string {
	if multi, ok := headers.(MultiHeaders); ok {
		return multi.Values(name)
	}
	if value := headers.Get(name); value != "" {
		return []string{value}
	}
	return nil
}

// LastHeaderValue is the value the hop nearest to us set.
//
// The last, not the first: header lines arrive in the order they were added,
// so the client's own is in front and the proxy's is behind it. A caller must
// still decide whether that hop is trusted; this only says which line is theirs.
func LastHeaderValue(headers Headers, name string) string {
	values := headerValues(headers, name)
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[len(values)-1])
}

// ClientIP is the address the rules should judge.
//
// peer is the address the connection came from, which is the only thing nobody
// can lie about.
func ClientIP(peer netip.Addr, headers Headers, trust Trust) netip.Addr {
	peer = peer.Unmap()
	if headers == nil || !trust.Contains(peer) {
		// Nothing in front of us that we trust, so every header is the client's
		// own writing and the connection is the truth.
		return peer
	}

	// Cloudflare replaces this header at its edge with the address it saw, and
	// a client cannot set it: whatever it sends is overwritten. We only believe
	// it because the hop that handed it to us is ours.
	if trust.Cloudflare {
		if ip, err := netip.ParseAddr(LastHeaderValue(headers, "CF-Connecting-IP")); err == nil {
			return ip.Unmap()
		}
	}

	// Every line, in the order they arrived, read as one chain. A proxy that
	// adds its own line instead of extending the client's would otherwise have
	// its entry hidden behind the client's — see MultiHeaders.
	forwarded := strings.Join(headerValues(headers, "X-Forwarded-For"), ",")
	if strings.TrimSpace(forwarded) == "" {
		return peer
	}
	parts := strings.Split(forwarded, ",")
	var leftmost netip.Addr
	for i := len(parts) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			// An unparseable entry is somebody's junk. Stopping here rather
			// than skipping it is deliberate: continuing left would step past
			// an entry we cannot vouch for and start believing the ones behind
			// it, which is exactly the way in.
			return peer
		}
		ip = ip.Unmap()
		leftmost = ip
		if !trust.Contains(ip) {
			return ip
		}
	}
	// Every hop was ours, which happens when the client is itself inside the
	// cluster. The leftmost is then the closest thing to an origin there is.
	if leftmost.IsValid() {
		return leftmost
	}
	return peer
}

// ClientCountry is the country header Cloudflare adds, or "" when there is
// nobody trusted to have added it.
//
// It is read before any database is consulted, because it is the one source
// that is right about a proxy, a VPN exit and a mobile carrier without the
// panel downloading anything.
func ClientCountry(peer netip.Addr, headers Headers, trust Trust) string {
	if headers == nil || !trust.Cloudflare || !trust.Contains(peer.Unmap()) {
		return ""
	}
	country := strings.ToUpper(LastHeaderValue(headers, "CF-IPCountry"))
	// Cloudflare sends XX for an address it cannot place and T1 for Tor. Both
	// are honest answers and neither is a country, so they are no answer here
	// rather than a country code that matches nothing.
	if !isCountryCode(country) || country == "XX" || country == "T1" {
		return ""
	}
	return country
}

// DefaultProxies are the ranges inside a k3s cluster: the pod network, the
// service network, and loopback for a request that never left the machine.
//
// An operator behind their own load balancer adds its range in Settings; these
// are the ones that are true on every install and that nobody should have to
// know to type.
func DefaultProxies(podCIDR, serviceCIDR string) []netip.Prefix {
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
	for _, cidr := range []string{podCIDR, serviceCIDR} {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr)); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

// ParseProxies reads the extra trusted ranges an operator configured.
//
// An entry that is not a range is dropped rather than failing the whole list:
// this is read on every request, and a typo in Settings must not be the reason
// a site stops answering. The setting's own validator is what tells the
// operator, at the moment they save it.
func ParseProxies(value string) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	}) {
		if prefix, err := netip.ParsePrefix(field); err == nil {
			prefixes = append(prefixes, prefix)
			continue
		}
		if addr, err := netip.ParseAddr(field); err == nil {
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	return prefixes
}
