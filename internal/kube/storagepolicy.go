package kube

import (
	"net/netip"
	"sort"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/version"
)

// StoragePolicyName is the policy that lets the panel's own backup and restore
// jobs in an environment reach the bucket backups go to.
const StoragePolicyName = "allow-backup-storage"

// BackupJobComponent is the component label on every job that moves a backup
// to or from the bucket: database and volume backups, their restores, and an
// imported dump. Only the panel writes it.
const BackupJobComponent = "backup"

// notStorage are addresses no bucket is at, whatever its name resolves to: the
// link-local range with the usual metadata service on it, Oracle Cloud's,
// Azure's wire server and Alibaba Cloud's. Each answers whatever asks it with
// the machine's own credentials or configuration.
var notStorage = []netip.Prefix{
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("100.100.100.200/32"),
}

// StorageAddresses picks, from the addresses a bucket's host resolves to, the
// ones an environment's own policy keeps its pods away from: the private and
// reserved ranges, and the servers' public addresses (InternetEgress). A
// bucket on the public internet needs nothing more and gets nothing; one at
// home, on the office network or on a server of the cluster's own gets its
// addresses named here. IPv4 only, like the rest of the policies.
func StorageAddresses(resolved, nodeAddresses []string) []string {
	servers := map[string]bool{}
	for _, address := range PublicNodeAddresses(nodeAddresses) {
		servers[address] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, address := range resolved {
		ip, err := netip.ParseAddr(address)
		if err != nil {
			continue
		}
		ip = ip.Unmap()
		if !ip.Is4() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || isNotStorage(ip) {
			continue
		}
		key := ip.String()
		if seen[key] || (!IsReservedAddress(key) && !servers[key]) {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func isNotStorage(ip netip.Addr) bool {
	for _, prefix := range notStorage {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// BackupStoragePolicy lets the backup and restore jobs in one namespace reach
// a bucket at the given addresses on its own port, and nothing else.
//
// The environment's policy keeps every pod off the private ranges and the
// servers' addresses, which is right for an app and wrong for a bucket: MinIO
// on the same server or on the office network was an address no backup could
// reach, so the documentation's "anything that speaks S3" meant anything on the
// public internet, and anything else failed after a timeout. The pods it
// selects are the panel's own jobs, by labels no tenant writes, and the hole
// is the bucket's own addresses on the bucket's port. The metadata services are
// never named, whatever the bucket's name resolves to (StorageAddresses).
func BackupStoragePolicy(namespace string, addresses []string, port int) *networkingv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	target := intstr.FromInt32(int32(port))
	var peers []networkingv1.NetworkPolicyPeer
	for _, address := range addresses {
		peers = append(peers, networkingv1.NetworkPolicyPeer{
			IPBlock: &networkingv1.IPBlock{CIDR: address + "/32"},
		})
	}
	return &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{
			Name: StoragePolicyName, Namespace: namespace,
			Labels: map[string]string{"app.kubernetes.io/managed-by": version.Binary},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/managed-by": version.Binary,
				"app.kubernetes.io/component":  BackupJobComponent,
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To:    peers,
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &target}},
			}},
		},
	}
}
