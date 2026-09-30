package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// Public ports: one LoadBalancer Service per app, and a policy letting
// connections through to exactly those ports.

func withPorts() AppSpec {
	s := baseSpec()
	s.PublicPorts = []PublicPort{
		{Port: 25565, PublicPort: 25565, Protocol: "tcp"},
		{Port: 19132, PublicPort: 19133, Protocol: "udp"},
	}
	return s
}

func TestAnAppsPublicPortsAreOpenedOnEveryServer(t *testing.T) {
	s := withPorts()
	service := BuildPortsService(s)
	if service == nil || service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		t.Fatalf("the ports are not a LoadBalancer Service: %+v", service)
	}
	// A node port each would be refused by the environment's quota.
	if service.Spec.AllocateLoadBalancerNodePorts == nil || *service.Spec.AllocateLoadBalancerNodePorts {
		t.Error("the load balancer asks for node ports, which the quota refuses")
	}
	if len(service.Spec.Ports) != 2 {
		t.Fatalf("ports %+v", service.Spec.Ports)
	}
	udp := service.Spec.Ports[1]
	if udp.Port != 19133 || udp.TargetPort.IntValue() != 19132 || udp.Protocol != corev1.ProtocolUDP {
		t.Errorf("the UDP port is %+v, want 19133 outside to 19132 inside", udp)
	}
	for key, value := range s.SelectorLabels() {
		if service.Spec.Selector[key] != value {
			t.Errorf("the Service does not select the app's pods: %v", service.Spec.Selector)
		}
	}

	policy := BuildPortsPolicy(s)
	if policy == nil || len(policy.Spec.Ingress) != 1 || len(policy.Spec.Ingress[0].From) != 0 {
		t.Fatalf("the policy is %+v, want one rule from anywhere", policy)
	}
	allowed := map[string]bool{}
	for _, p := range policy.Spec.Ingress[0].Ports {
		allowed[p.Port.String()+"/"+string(*p.Protocol)] = true
	}
	// The container's ports, which is what a policy on a pod is about, and
	// nothing else of the app's.
	if len(allowed) != 2 || !allowed["25565/TCP"] || !allowed["19132/UDP"] {
		t.Errorf("the policy lets through %v", allowed)
	}

	container := BuildDeployment(s).Spec.Template.Spec.Containers[0]
	if len(container.Ports) != 3 {
		t.Errorf("the container declares %+v, want its HTTP port and the two public ones", container.Ports)
	}
}

func TestAnAppWithoutPublicPortsOpensNothing(t *testing.T) {
	if BuildPortsService(baseSpec()) != nil || BuildPortsPolicy(baseSpec()) != nil {
		t.Error("an app with no public ports has a load balancer or a policy")
	}
	// A worker takes no connections from outside, whatever its app does.
	process := ProcessSpec(withPorts(), "worker", "run", 1)
	if BuildPortsService(process) != nil {
		t.Error("a process opened its app's ports")
	}
}

func TestAPortTheServersUseIsNotOpened(t *testing.T) {
	for _, bad := range []struct {
		port     int
		protocol string
	}{{22, "tcp"}, {443, "tcp"}, {6443, "tcp"}, {10250, "tcp"}, {8472, "udp"}, {30500, "tcp"}, {0, "tcp"}, {70000, "tcp"}, {25565, "sctp"}} {
		if ValidatePublicPort(bad.port, bad.protocol) == nil {
			t.Errorf("%d/%s was accepted", bad.port, bad.protocol)
		}
	}
	for _, good := range []int{25, 53, 1883, 25565, 64738} {
		if err := ValidatePublicPort(good, "tcp"); err != nil {
			t.Errorf("%d was refused: %v", good, err)
		}
	}
	s := withPorts()
	s.PublicPorts = append(s.PublicPorts, s.PublicPorts[0])
	if s.Validate() == nil {
		t.Error("the same public port twice was accepted")
	}
}

// The quota allows exactly the load balancers the environment's apps need.
func TestTheQuotaAllowsOnlyThePanelsLoadBalancers(t *testing.T) {
	q := DefaultQuota()
	if got := BuildResourceQuota("ns", q).Spec.Hard["services.loadbalancers"]; got.Value() != 0 {
		t.Errorf("an environment without public ports allows %s load balancers", got.String())
	}
	q.LoadBalancers = 2
	if got := BuildResourceQuota("ns", q).Spec.Hard["services.loadbalancers"]; got.Value() != 2 {
		t.Errorf("two apps with ports are allowed %s", got.String())
	}
	if got := BuildResourceQuota("ns", q).Spec.Hard["services.nodeports"]; got.Value() != 0 {
		t.Errorf("node ports are allowed: %s", got.String())
	}
}
