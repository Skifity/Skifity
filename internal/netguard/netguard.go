// Package netguard stops the panel being used to reach addresses it should
// never reach on somebody else's behalf.
//
// Three settings hold an address the panel then makes a request to: a Git
// connection's base URL, a notification channel's webhook, and the URL a
// cluster component's manifest is downloaded from. They are written by an
// administrator, which on a single-tenant install is the operator themselves
// and no boundary at all — and on an install with more than one team is
// somebody who should not be able to point the panel's own process at
// 169.254.169.254 and read what comes back. The panel holds the master key and
// can reach the Kubernetes API; it is the last process on the box that should
// make arbitrary requests.
//
// The third was missed for as long as this comment said "two", and it is the
// one where what comes back is applied to the cluster as Kubernetes objects.
//
// What is refused is deliberately narrow:
//
//   - the cloud metadata service, on the link-local range, which hands out the
//     provider credentials for the whole machine to anything that asks;
//   - loopback, which is the panel itself and whatever else shares its network;
//   - the unspecified address and multicast, which are not somewhere a Git host
//     or a webhook receiver lives.
//
// What is allowed, on purpose, is every private range. A self-hosted Gitea on
// 10.0.0.5 and a webhook receiver on the same LAN are exactly what this product
// is for, and refusing them to say something about security would break the
// ordinary case to defend against the administrator who configured it.
//
// The check runs on the resolved address immediately before the connection is
// made, rather than on the hostname. A name that resolves to a public address
// when it is validated and a link-local one when it is dialled is the oldest
// way around a check written the other way round.
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Blocked says which address was refused, so an operator who meant it can see
// what happened rather than a timeout.
type Blocked struct {
	IP net.IP
}

func (e *Blocked) Error() string {
	switch {
	case e.IP.IsLinkLocalUnicast():
		return fmt.Sprintf("refusing to connect to %s: that is the cloud metadata address, "+
			"which hands out this machine's credentials", e.IP)
	case e.IP.IsLoopback():
		return fmt.Sprintf("refusing to connect to %s: that is this machine itself", e.IP)
	default:
		return fmt.Sprintf("refusing to connect to %s: that is not an address a service lives at", e.IP)
	}
}

// Allowed reports whether the panel may connect to an address.
//
// Private ranges are allowed. See the package comment: a self-hosted Git server
// on a private network is the ordinary case here, not the attack.
func Allowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	switch {
	case ip.IsUnspecified(),
		ip.IsLoopback(),
		ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(),
		ip.IsMulticast():
		return false
	}
	return true
}

// Dialer is a net.Dialer that refuses the addresses above.
//
// Control is the hook that matters: it runs after the name has been resolved
// and before the socket connects, and it is given the address actually being
// dialled. Checking the hostname instead would be checking something the other
// side can change between the check and the connection.
func Dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("refusing to connect to %q", address)
			}
			ip := net.ParseIP(host)
			if !Allowed(ip) {
				return &Blocked{IP: ip}
			}
			return nil
		},
	}
}

// Client returns an HTTP client that will not reach a refused address.
//
// timeout bounds the whole exchange, which is right for the two callers: both
// make one small request while somebody waits for the answer.
func Client(timeout time.Duration) *http.Client {
	dialer := Dialer(10 * time.Second)
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, address)
			},
			// A redirect is dialled through the same Control, so a provider
			// that redirects to a refused address is refused there too.
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}
