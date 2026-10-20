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
// Except one: the cluster's own pod and Service networks, for an address a
// team gave the panel rather than its administrator — a webhook, a Git host,
// a secret manager, a registry, a template catalogue. Every environment's
// namespace lets the panel in, because the panel has to reach the apps, so a
// team admin who could point the panel at 10.43.x.x could send requests to
// another team's internal Services that nothing else in the cluster may reach.
// TenantClient and TenantDialer refuse those too. An app a team runs here is
// reached at the address it is published at, the same as from anywhere else;
// a build could not reach an address inside the cluster either.
//
// The check runs on the resolved address immediately before the connection is
// made, rather than on the hostname. A name that resolves to a public address
// when it is validated and a link-local one when it is dialled is the oldest
// way around a check written the other way round.
package netguard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"syscall"
	"time"
)

// Address is the shape of an address the panel is asked to fetch: http or
// https, a host name or a bracketed IPv6 address, a port, and a path and query.
// No user information, no whitespace, no fragment.
//
// It says nothing about where the host is. That is decided when the
// connection is made, by Client and Dialer, on the address the name resolved
// to; checking a name beforehand is what a DNS answer that changes in between
// gets past. What this does is refuse, before anything is sent, an address
// that is not one, and say so in a sentence rather than in a dial error.
var Address = regexp.MustCompile(`^https?://(?:[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:.]+\])(?::[0-9]{1,5})?(?:[/?][^\s#]*)?$`)

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
	case InCluster(e.IP):
		return fmt.Sprintf("refusing to connect to %s: that is inside this cluster, where every team's "+
			"apps run. Use the address the service is published at instead", e.IP)
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
	for _, metadata := range metadataAddresses {
		if metadata.Equal(ip) {
			return false
		}
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

// metadataAddresses are the metadata services that do not live on the
// link-local range the check below already refuses: AWS's over IPv6, which is
// a unique-local address, Alibaba Cloud's, and Oracle Cloud's second one. Each
// hands out the machine's credentials to whatever asks, like the one on
// 169.254.169.254.
var metadataAddresses = []net.IP{
	net.ParseIP("fd00:ec2::254"),
	net.ParseIP("100.100.100.200"),
	net.ParseIP("192.0.0.192"),
}

// clusterNetworks are the cluster's own: every pod's address and every
// Service's. They are k3s's defaults, which Skifity does not override;
// internal/kube names the same two, and a test there keeps them the same.
var clusterNetworks = func() []*net.IPNet {
	var out []*net.IPNet
	for _, cidr := range []string{"10.42.0.0/16", "10.43.0.0/16"} {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		out = append(out, network)
	}
	return out
}()

// ClusterNetworks are the cluster's pod and Service networks, as written.
func ClusterNetworks() []string {
	out := make([]string, 0, len(clusterNetworks))
	for _, network := range clusterNetworks {
		out = append(out, network.String())
	}
	return out
}

// InCluster reports whether an address belongs to a pod or a Service of this
// cluster.
func InCluster(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, network := range clusterNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// TenantAllowed reports whether the panel may connect to an address a team
// gave it: what Allowed allows, apart from the cluster's own networks. See the
// package comment for why a team's address is held to more than an
// administrator's.
func TenantAllowed(ip net.IP) bool {
	return Allowed(ip) && !InCluster(ip)
}

// Dialer is a net.Dialer that refuses the addresses above.
//
// Control is the hook that matters: it runs after the name has been resolved
// and before the socket connects, and it is given the address actually being
// dialled. Checking the hostname instead would be checking something the other
// side can change between the check and the connection.
func Dialer(timeout time.Duration) *net.Dialer {
	return guardedDialer(timeout, Allowed)
}

// TenantDialer is Dialer for an address a team gave the panel: it refuses the
// cluster's own networks as well.
func TenantDialer(timeout time.Duration) *net.Dialer {
	return guardedDialer(timeout, TenantAllowed)
}

func guardedDialer(timeout time.Duration, allowed func(net.IP) bool) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("refusing to connect to %q", address)
			}
			ip := net.ParseIP(host)
			if !allowed(ip) {
				return &Blocked{IP: ip}
			}
			return nil
		},
	}
}

// Client returns an HTTP client that will not reach a refused address.
//
// timeout bounds the whole exchange, which is right for every caller: each
// makes one small request while somebody waits for the answer.
func Client(timeout time.Duration) *http.Client {
	return guardedClient(timeout, Dialer(10*time.Second))
}

// TenantClient is Client for an address a team gave the panel: a webhook, a
// Git host, a secret manager, a registry. It refuses the cluster's own
// networks as well.
func TenantClient(timeout time.Duration) *http.Client {
	return guardedClient(timeout, TenantDialer(10*time.Second))
}

// TenantClientTrusting is TenantClient verifying certificates against roots
// rather than the system's alone: a Git host whose certificate the
// operator's own authority signed, with that authority added to the public
// ones.
func TenantClientTrusting(timeout time.Duration, roots *x509.CertPool) *http.Client {
	client := guardedClient(timeout, TenantDialer(10*time.Second))
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return client
}

func guardedClient(timeout time.Duration, dialer *net.Dialer) *http.Client {
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
