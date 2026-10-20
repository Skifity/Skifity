package kube

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/version"
)

// SourcePolicyName is the policy that lets one build reach the repository it
// clones, named after the build's Job.
func SourcePolicyName(jobName string) string { return ResourceName(jobName, "source") }

// BuildSourcePolicy lets the pods of one build reach the Git host its
// repository is on, at the given addresses on the repository's port, and
// nothing else.
//
// The build namespace keeps every build off the private ranges and off the
// servers, which is right for somebody's install scripts and wrong for the
// repository itself when it is on a Gitea at home or a GitLab on the office
// network: the clone waited for a connection the policy dropped. The pods it
// selects are this build's alone, by the deployment the panel labelled them
// with: the namespace is every team's, and a policy for all of it would open
// the host to every build. Which hosts may be opened at all is the
// administrator's list (settings.KeyGitPrivateHosts), and the metadata
// services are never among the addresses (FencedAddresses).
func BuildSourcePolicy(namespace, jobName, deploymentID string, addresses []string, port int) *networkingv1.NetworkPolicy {
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
			Name: SourcePolicyName(jobName), Namespace: namespace,
			Labels: map[string]string{"app.kubernetes.io/managed-by": version.Binary},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/managed-by":    version.Binary,
				"app.kubernetes.io/component":     "build",
				version.LabelKey("deployment-id"): deploymentID,
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To:    peers,
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &target}},
			}},
		},
	}
}
