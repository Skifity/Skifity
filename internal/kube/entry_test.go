package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// Which servers take traffic, read the way k3s's ServiceLB decides which
// addresses it advertises. The fake clientset only: see entry.go.

func entryNode(name string, ready bool) *corev1.Node {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}},
	}
}

func traefikLoadBalancer(policy corev1.ServiceExternalTrafficPolicy) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: traefikService, Namespace: TraefikNamespace},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, ExternalTrafficPolicy: policy},
	}
}

// balancerPod is ServiceLB's pod on a server, labelled as k3s labels it.
func balancerPod(node string, ready bool) *corev1.Pod {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "svclb-traefik-0a1b2c3d-" + node, Namespace: "kube-system",
			Labels: map[string]string{
				"app":                   "svclb-traefik-0a1b2c3d",
				serviceLBNameLabel:      traefikService,
				serviceLBNamespaceLabel: TraefikNamespace,
			},
		},
		Spec:   corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}},
	}
}

// traefikEndpoints is the Service's endpoint slice: one Traefik per server
// named, ready or not.
func traefikEndpoints(ready map[string]bool) *discoveryv1.EndpointSlice {
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: "traefik-abcde", Namespace: TraefikNamespace,
			Labels: map[string]string{discoveryv1.LabelServiceName: traefikService},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	for node, isReady := range ready {
		slice.Endpoints = append(slice.Endpoints, discoveryv1.Endpoint{
			Addresses:  []string{"10.42.0.10"},
			Conditions: discoveryv1.EndpointConditions{Ready: &isReady},
			NodeName:   &node,
		})
	}
	return slice
}

func entryOf(t *testing.T, objects ...runtime.Object) map[string]Node {
	t.Helper()
	summary, err := NewClientWith(fake.NewClientset(objects...), nil, "").Summary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	for _, node := range summary.Nodes {
		nodes[node.Name] = node
	}
	return nodes
}

func TestEntryTellsAServerThatIsUpFromOneThatServes(t *testing.T) {
	// The installer's setup: Traefik on every server, the Local policy.
	nodes := entryOf(t,
		entryNode("serving", true), entryNode("no-traefik", true), entryNode("no-balancer", true),
		entryNode("off", false), entryNode("not-a-way-in", true),
		traefikLoadBalancer(corev1.ServiceExternalTrafficPolicyLocal),
		balancerPod("serving", true), balancerPod("no-traefik", true),
		balancerPod("no-balancer", false), balancerPod("off", true),
		traefikEndpoints(map[string]bool{"serving": true, "no-traefik": false, "no-balancer": true, "off": true}),
	)

	want := map[string][2]string{
		"serving":      {EntryServing, ""},
		"no-traefik":   {EntryNotServing, EntryNoIngressHere},
		"no-balancer":  {EntryNotServing, EntryBalancerNotReady},
		"off":          {EntryNotServing, EntryNodeNotReady},
		"not-a-way-in": {EntryNone, ""},
	}
	for name, expected := range want {
		got := [2]string{nodes[name].Entry, nodes[name].EntryReason}
		if got != expected {
			t.Errorf("%s reads %v, want %v", name, got, expected)
		}
	}
}

func TestEntryUnderTheClusterPolicyNeedsATraefikSomewhere(t *testing.T) {
	// Traefik as somebody configured it: one replica, any server forwards to
	// it. A server with no Traefik of its own still serves...
	nodes := entryOf(t,
		entryNode("a", true), entryNode("b", true),
		traefikLoadBalancer(corev1.ServiceExternalTrafficPolicyCluster),
		balancerPod("a", true), balancerPod("b", true),
		traefikEndpoints(map[string]bool{"a": true}),
	)
	if nodes["b"].Entry != EntryServing {
		t.Errorf("b forwards to the Traefik on a and reads %q (%q)", nodes["b"].Entry, nodes["b"].EntryReason)
	}

	// ...and with none ready anywhere, none does.
	nodes = entryOf(t,
		entryNode("a", true), entryNode("b", true),
		traefikLoadBalancer(corev1.ServiceExternalTrafficPolicyCluster),
		balancerPod("a", true), balancerPod("b", true),
		traefikEndpoints(map[string]bool{"a": false}),
	)
	for _, name := range []string{"a", "b"} {
		if nodes[name].Entry != EntryNotServing || nodes[name].EntryReason != EntryNoIngress {
			t.Errorf("%s reads %q (%q) with no Traefik ready", name, nodes[name].Entry, nodes[name].EntryReason)
		}
	}
}

func TestEntryIsNotGuessedWhenTheClusterCannotSay(t *testing.T) {
	cases := map[string][]runtime.Object{
		// k3s started with --disable traefik, another ingress in its place.
		"no Traefik": {entryNode("a", true)},
		// MetalLB or a provider's load balancer in front: ServiceLB has no
		// pods, and which servers take traffic is not the cluster's to say.
		"no ServiceLB": {entryNode("a", true), traefikLoadBalancer(corev1.ServiceExternalTrafficPolicyLocal),
			traefikEndpoints(map[string]bool{"a": true})},
		// A Traefik reached some other way than a load balancer.
		"not a load balancer": {entryNode("a", true), &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: traefikService, Namespace: TraefikNamespace},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP},
		}, balancerPod("a", true)},
	}
	for name, objects := range cases {
		t.Run(name, func(t *testing.T) {
			node := entryOf(t, objects...)["a"]
			if node.Entry != "" || node.EntryReason != "" {
				t.Errorf("read %q (%q) where the cluster cannot say", node.Entry, node.EntryReason)
			}
		})
	}
}

func TestEntryCountsEitherBalancerPodWhileTheyRoll(t *testing.T) {
	nodes := []Node{{Name: "a", Ready: true}}
	judgeEntry(nodes, true,
		[]corev1.Pod{*balancerPod("a", false), *balancerPod("a", true)},
		[]discoveryv1.EndpointSlice{*traefikEndpoints(map[string]bool{"a": true})})
	if nodes[0].Entry != EntryServing {
		t.Errorf("a server with one ready ServiceLB pod of two reads %q (%q)", nodes[0].Entry, nodes[0].EntryReason)
	}
}
