package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Which servers take the traffic that reaches them.
//
// k3s's load balancer, ServiceLB, runs a pod on every server — or on the ones
// labelled for it — that forwards ports 80 and 443 to Traefik's Service. Round-
// robin DNS names every server's address, and a browser that cannot connect to
// one tries the next, which covers a server that is off. It does not cover a
// server that is up and does not serve: its ServiceLB pod not ready, or, since
// the installer keeps visitors' addresses with externalTrafficPolicy: Local,
// no ready Traefik on it, in which case the server drops the connection
// rather than refusing it and the browser waits. Kubernetes still hears from
// such a server, so nothing else in the panel calls it unwell.
//
// The rules are the ones k3s itself uses to choose which addresses its load
// balancer advertises (pkg/cloudprovider/servicelb.go, getStatus and podIPs):
// a server counts while its ServiceLB pod is ready and, under the Local
// policy, while the Service has a ready endpoint on it. They are read from the
// pods and endpoints rather than from the Service's status because the status
// lists the addresses that count, not why one is missing.
//
// This is the cluster's view from inside. A provider's firewall closing port
// 443, or a Traefik that is ready and answers everything with an error, looks
// healthy here; docs/adding-servers.md says so. None of it has run against a
// real cluster: the labels are k3s's own and the test uses a fake clientset.

// A server's part in letting visitors in.
const (
	// EntryServing is a server that takes traffic on ports 80 and 443.
	EntryServing = "serving"
	// EntryNotServing is a server the load balancer runs on that would not
	// answer a visitor now. EntryReason says why.
	EntryNotServing = "not_serving"
	// EntryNone is a server the load balancer does not run on, so not one of
	// the ways in.
	EntryNone = "none"
)

// Why a server is not taking traffic.
const (
	// EntryNodeNotReady is the server itself not ready.
	EntryNodeNotReady = "node_not_ready"
	// EntryBalancerNotReady is its ServiceLB pod not ready: nothing forwards
	// ports 80 and 443.
	EntryBalancerNotReady = "balancer_not_ready"
	// EntryNoIngress is no Traefik ready anywhere in the cluster.
	EntryNoIngress = "no_ingress"
	// EntryNoIngressHere is no Traefik ready on this server, under the Local
	// policy, which serves a visitor only on the server they reached.
	EntryNoIngressHere = "no_ingress_here"
)

const (
	// traefikService is the Service k3s's Traefik chart makes, in
	// TraefikNamespace.
	traefikService = "traefik"
	// ServiceLB labels each of its pods with the Service it fronts. Its pods
	// live in kube-system unless k3s was started with --servicelb-namespace,
	// so they are looked for in every namespace.
	serviceLBNameLabel      = "svccontroller.k3s.cattle.io/svcname"
	serviceLBNamespaceLabel = "svccontroller.k3s.cattle.io/svcnamespace"
)

// describeEntry says, on each node, whether it takes traffic. Both fields stay
// empty when the cluster cannot say: no Traefik Service, one that is not a
// load balancer, or ServiceLB turned off in favour of MetalLB or a provider's
// load balancer. A read that fails leaves them empty too; the rest of the
// summary does not depend on it.
func (c *Client) describeEntry(ctx context.Context, nodes []Node) {
	service, err := c.clientset.CoreV1().Services(TraefikNamespace).Get(ctx, traefikService, metav1.GetOptions{})
	if err != nil || service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return
	}
	balancers, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		LabelSelector: serviceLBNameLabel + "=" + service.Name + "," + serviceLBNamespaceLabel + "=" + service.Namespace,
	})
	if err != nil {
		return
	}
	endpoints, err := c.clientset.DiscoveryV1().EndpointSlices(service.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: discoveryv1.LabelServiceName + "=" + service.Name,
	})
	if err != nil {
		return
	}
	judgeEntry(nodes, service.Spec.ExternalTrafficPolicy == corev1.ServiceExternalTrafficPolicyLocal,
		balancers.Items, endpoints.Items)
}

// judgeEntry is describeEntry once everything is read: whether the Service
// keeps traffic on the server it arrives at, ServiceLB's pods, and the
// Service's endpoints.
func judgeEntry(nodes []Node, local bool, balancers []corev1.Pod, endpoints []discoveryv1.EndpointSlice) {
	if len(balancers) == 0 {
		return
	}
	// A server can briefly have two while the DaemonSet rolls: either one
	// ready is enough.
	balancerReady := map[string]bool{}
	for _, pod := range balancers {
		if pod.Spec.NodeName != "" {
			balancerReady[pod.Spec.NodeName] = balancerReady[pod.Spec.NodeName] || podReady(pod)
		}
	}
	ingressOn := map[string]bool{}
	ingressAnywhere := false
	for _, slice := range endpoints {
		for _, endpoint := range slice.Endpoints {
			// A terminating endpoint is not ready, which is right: it is on
			// its way out.
			if endpoint.Conditions.Ready == nil || !*endpoint.Conditions.Ready {
				continue
			}
			ingressAnywhere = true
			if endpoint.NodeName != nil {
				ingressOn[*endpoint.NodeName] = true
			}
		}
	}
	for i := range nodes {
		node := &nodes[i]
		ready, runs := balancerReady[node.Name]
		switch {
		case !runs:
			node.Entry = EntryNone
		case !node.Ready:
			node.Entry, node.EntryReason = EntryNotServing, EntryNodeNotReady
		case !ready:
			node.Entry, node.EntryReason = EntryNotServing, EntryBalancerNotReady
		case !ingressAnywhere:
			node.Entry, node.EntryReason = EntryNotServing, EntryNoIngress
		case local && !ingressOn[node.Name]:
			node.Entry, node.EntryReason = EntryNotServing, EntryNoIngressHere
		default:
			node.Entry = EntryServing
		}
	}
}
