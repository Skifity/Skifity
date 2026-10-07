package kube

import (
	"net/netip"
	"sort"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// What a pod may reach once it is allowed out to the internet.
//
// "The internet" is every address that is not somebody's private network, and
// the node's own address is one of those on most providers: a Hetzner,
// DigitalOcean or Vultr server holds its public address on its network card.
// A policy that excepts only the private ranges therefore let a pod reach the
// Kubernetes API (a Service address is translated to a server's own address
// before the policy looks at it), the kubelet, SSH, and the registry's NodePort
// on the machine it runs on, and the host firewall trusts the pods' network on
// every port. The nodes' own addresses are excepted by name instead, apart from
// ports 80 and 443, which is how an app calls another by its public hostname.

// ReservedRanges are the addresses no tenant pod, build or plugin has any
// business reaching: private networks, the carrier-grade NAT range that
// providers and Tailscale use, link-local (where every cloud's metadata
// service lives), and the two metadata addresses that are not link-local.
var ReservedRanges = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"100.64.0.0/10",
	"169.254.0.0/16",
	// Oracle Cloud's metadata service, and Azure's wire server.
	"192.0.0.0/24",
	"168.63.129.16/32",
}

var reservedPrefixes = func() []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(ReservedRanges))
	for _, cidr := range ReservedRanges {
		prefixes = append(prefixes, netip.MustParsePrefix(cidr))
	}
	return prefixes
}()

// IsReservedAddress reports whether an address is in a range pods are kept
// away from.
func IsReservedAddress(address string) bool {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, prefix := range reservedPrefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// PublicNodeAddresses picks, out of every address the nodes report, the ones the
// reserved ranges do not already cover: the addresses a policy has to name.
// IPv6 is left out, because the cluster's networks are IPv4 and so are the
// ranges above.
func PublicNodeAddresses(addresses []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, address := range addresses {
		ip, err := netip.ParseAddr(address)
		if err != nil {
			continue
		}
		ip = ip.Unmap()
		if !ip.Is4() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || IsReservedAddress(ip.String()) {
			continue
		}
		if !seen[ip.String()] {
			seen[ip.String()] = true
			out = append(out, ip.String())
		}
	}
	sort.Strings(out)
	return out
}

// InternetEgress is the egress a pod gets towards everything outside the
// cluster: the whole internet except the reserved ranges and the nodes' own
// public addresses, and those addresses on the web ports only.
func InternetEgress(nodeAddresses []string) []networkingv1.NetworkPolicyEgressRule {
	nodes := PublicNodeAddresses(nodeAddresses)
	except := append([]string{}, ReservedRanges...)
	var named []networkingv1.NetworkPolicyPeer
	for _, address := range nodes {
		except = append(except, address+"/32")
		named = append(named, networkingv1.NetworkPolicyPeer{
			IPBlock: &networkingv1.IPBlock{CIDR: address + "/32"},
		})
	}

	rules := []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: except},
		}},
	}}
	if len(named) > 0 {
		tcp := corev1.ProtocolTCP
		http, https := intstr.FromInt32(80), intstr.FromInt32(443)
		rules = append(rules, networkingv1.NetworkPolicyEgressRule{
			To:    named,
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &http}, {Protocol: &tcp, Port: &https}},
		})
	}
	return rules
}

// EgressAllows says whether a set of egress rules lets a pod open a TCP
// connection to an address and port, as far as the rules' IP blocks decide it.
// Peers chosen by label match pods, not addresses, and are not counted.
//
// It is how the tests ask the question that matters, "can a pod reach X?",
// instead of checking that one particular string is in a list.
func EgressAllows(rules []networkingv1.NetworkPolicyEgressRule, address string, port int) bool {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	for _, rule := range rules {
		if len(rule.Ports) > 0 {
			matched := false
			for _, p := range rule.Ports {
				if (p.Protocol == nil || *p.Protocol == corev1.ProtocolTCP) && p.Port != nil && p.Port.IntValue() == port {
					matched = true
				}
			}
			if !matched {
				continue
			}
		}
		for _, peer := range rule.To {
			if peer.IPBlock == nil {
				continue
			}
			block, err := netip.ParsePrefix(peer.IPBlock.CIDR)
			if err != nil || !block.Contains(ip) {
				continue
			}
			excepted := false
			for _, cidr := range peer.IPBlock.Except {
				if prefix, err := netip.ParsePrefix(cidr); err == nil && prefix.Contains(ip) {
					excepted = true
				}
			}
			if !excepted {
				return true
			}
		}
	}
	return false
}
