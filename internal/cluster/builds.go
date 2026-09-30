package cluster

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/version"
)

// The build namespace.
//
// Builds run code out of somebody's repository. That code is not hostile by
// assumption, but it is not the panel's either, and the panel's namespace
// holds the master key, the database and a service account that can reach the
// whole cluster. So builds get their own namespace, with the builder and the
// registry they need and nothing else.
//
// The namespace carries the privileged Pod Security profile, which nothing
// else in Skifity does. Rootless BuildKit needs an unconfined seccomp profile
// to use user namespaces, and the baseline profile the panel's namespace
// enforces refuses exactly that. The alternative is a privileged container
// holding the host's container runtime socket, which is worse in every way:
// this is one namespace with one builder in it, and that builder still runs as
// an unprivileged user.

// EnsureBuildNamespace creates the namespace builds run in, with the guards
// that belong to it.
func (c *Cluster) EnsureBuildNamespace(ctx context.Context) error {
	namespace := c.client.BuildNamespace()
	labels := map[string]string{
		"app.kubernetes.io/managed-by": version.Binary,
		version.LabelKey("component"):  "builds",

		// See the note above: this is the one namespace that needs it, and it
		// is audited and warned at restricted so anything else added here is
		// noticed.
		"pod-security.kubernetes.io/enforce": "privileged",
		"pod-security.kubernetes.io/audit":   "restricted",
		"pod-security.kubernetes.io/warn":    "restricted",
	}

	objects := []any{
		&corev1.Namespace{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
			ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: labels},
		},
	}
	for _, policy := range buildNetworkPolicies(namespace, c.client.SystemNamespace()) {
		objects = append(objects, policy)
	}
	if err := c.client.Applier().ApplyAll(ctx, objects...); err != nil {
		return fmt.Errorf("prepare the build namespace: %w", err)
	}
	return nil
}

// buildNetworkPolicies fence the build namespace in.
//
// A build needs the internet, because that is where the repository and the
// packages are. It does not need the panel, another team's namespace, the
// kubelet or the cloud metadata service, and the point of the deny-all
// baseline is that it never gets them by accident.
func buildNetworkPolicies(namespace, systemNamespace string) []*networkingv1.NetworkPolicy {
	labels := map[string]string{"app.kubernetes.io/managed-by": version.Binary}
	dnsPort := intstr.FromInt32(53)
	udp := corev1.ProtocolUDP
	tcp := corev1.ProtocolTCP

	denyAll := &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "default-deny", Namespace: namespace, Labels: labels},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
		},
	}

	allow := &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "allow-builds", Namespace: namespace, Labels: labels},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{
					// Between the build job, the builder and the registry.
					{PodSelector: &metav1.LabelSelector{}},
					// And from the panel, which reads a build's logs and waits
					// for the builder to come up.
					{NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kubernetes.io/metadata.name": systemNamespace},
					}},
				},
			}},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
						},
					}},
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &udp, Port: &dnsPort},
						{Protocol: &tcp, Port: &dnsPort},
					},
				},
				{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}},
				{
					// Out to the internet for the repository and the packages,
					// but not to the node network, where the Kubernetes API,
					// the kubelet and the cloud metadata service live.
					To: []networkingv1.NetworkPolicyPeer{{
						IPBlock: &networkingv1.IPBlock{
							CIDR: "0.0.0.0/0",
							Except: []string{
								"10.0.0.0/8",
								"172.16.0.0/12",
								"192.168.0.0/16",
								"169.254.169.254/32",
							},
						},
					}},
				},
			},
		},
	}
	return []*networkingv1.NetworkPolicy{denyAll, allow}
}
