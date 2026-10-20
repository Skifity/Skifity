package cluster

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/kube"
	"skifity/internal/version"
)

// The build namespace.
//
// Builds run code out of somebody's repository. That code is not hostile by
// assumption, but it is not the panel's either, and the panel's namespace
// holds the master key, the database and a service account that can reach the
// whole cluster. So builds get their own namespace, with the registry they
// push to and nothing else.
//
// The namespace carries the privileged Pod Security profile, which nothing
// else in Skifity does. Each build runs its own rootless BuildKit, which needs
// an unconfined seccomp profile to use user namespaces, and the baseline
// profile refuses exactly that. The alternative is a privileged container
// holding the host's container runtime socket, which is worse in every way:
// the builder still runs as an unprivileged user, and it lives as long as one
// build.

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
	nodes, err := c.client.NodeAddresses(ctx)
	if err != nil {
		return fmt.Errorf("prepare the build namespace: %w", err)
	}
	gateways, err := c.client.NodePodGateways(ctx)
	if err != nil {
		return fmt.Errorf("prepare the build namespace: %w", err)
	}
	for _, policy := range buildNetworkPolicies(namespace, c.client.SystemNamespace(), nodes, gateways) {
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
// packages are, and the registry, to push what it built. It does not need the
// panel, another team's namespace, another build, the kubelet or the cloud
// metadata service, and the point of the deny-all baseline is that it never
// gets them by accident. Nothing comes in to a build at all: the panel reads
// its log and hands it an upload through the Kubernetes API, not over the
// pod network.
//
// The registry is the one thing here that is listened to: by the builds and
// the scans beside it, by the panel, which sweeps it, and by every server's
// container runtime pulling an app through the NodePort. A pull on the
// registry's own server comes from that server, which the policy controller
// always lets through; a pull on another server comes from that server's
// pod-network gateway or its own address, and is named.
func buildNetworkPolicies(namespace, systemNamespace string, nodeAddresses, podGateways []string) []*networkingv1.NetworkPolicy {
	labels := map[string]string{"app.kubernetes.io/managed-by": version.Binary}
	dnsPort := intstr.FromInt32(53)
	registryPort := intstr.FromInt32(RegistryPort)
	udp := corev1.ProtocolUDP
	tcp := corev1.ProtocolTCP
	registryPods := &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": RegistryService}}

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
				// The registry, through its guard, and no other pod.
				{
					To:    []networkingv1.NetworkPolicyPeer{{PodSelector: registryPods}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &registryPort}},
				},
			},
		},
	}
	// Out to the internet for the repository and the packages, but not to the
	// nodes' own addresses, where the Kubernetes API, the kubelet and the
	// registry's NodePort are, nor to the cloud metadata service.
	allow.Spec.Egress = append(allow.Spec.Egress, kube.InternetEgress(nodeAddresses)...)

	from := []networkingv1.NetworkPolicyPeer{
		// The builds and the scans beside it.
		{PodSelector: &metav1.LabelSelector{}},
		// The panel, which sweeps it.
		{NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"kubernetes.io/metadata.name": systemNamespace},
		}},
	}
	for _, address := range append(kube.NodeIPv4(nodeAddresses), podGateways...) {
		from = append(from, networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: address + "/32"}})
	}
	registry := &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "allow-registry", Namespace: namespace, Labels: labels},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: *registryPods,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  from,
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &registryPort}},
			}},
		},
	}
	return []*networkingv1.NetworkPolicy{denyAll, allow, registry}
}
