package kube

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Public ports: connections that are not HTTP — a game server, an MQTT
// broker, a DNS server — which an Ingress cannot carry. They are one
// LoadBalancer Service per app, which k3s's ServiceLB opens on every server at
// the public port, and a NetworkPolicy letting anybody reach those ports on
// the app's pods and nothing else: the environment's default is to refuse
// every connection that does not come through the ingress.
//
// The firewall does not apply to them. It reads HTTP requests, and these are
// not HTTP.

// PublicPort is one port an app takes connections on from outside.
type PublicPort struct {
	// Port is the one the container listens on.
	Port int
	// PublicPort is the one opened on every server.
	PublicPort int
	// Protocol is tcp or udp.
	Protocol string
}

// reservedPorts are ones a server already uses, and a public port there would
// break the cluster or the panel rather than open anything.
var reservedPorts = map[int]string{
	22:    "SSH",
	80:    "HTTP, which every app's domains are served on",
	443:   "HTTPS, which every app's domains are served on",
	2379:  "etcd",
	2380:  "etcd",
	6443:  "the Kubernetes API",
	8472:  "the cluster network (VXLAN)",
	9100:  "node metrics",
	10250: "the kubelet",
	51820: "the cluster network (WireGuard)",
	51821: "the cluster network (WireGuard)",
}

// ValidatePublicPort says why a public port cannot be opened, or nil.
func ValidatePublicPort(port int, protocol string) error {
	if protocol != "tcp" && protocol != "udp" {
		return fmt.Errorf("%q is not a protocol a port can be opened for; use tcp or udp", protocol)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("%d is not a port", port)
	}
	if what, ok := reservedPorts[port]; ok {
		return fmt.Errorf("%d is %s on every server, so it cannot be opened for an app", port, what)
	}
	if port >= 30000 && port <= 32767 {
		return fmt.Errorf("%d is in the range Kubernetes keeps for itself (30000–32767)", port)
	}
	return nil
}

// PortsServiceName is the LoadBalancer Service holding an app's public ports.
func PortsServiceName(appName string) string { return ResourceName(appName, "ports") }

// PortsPolicyName is the NetworkPolicy that lets connections reach them.
func PortsPolicyName(appName string) string { return ResourceName(appName, "public-ports") }

func protocolOf(p PublicPort) corev1.Protocol {
	if strings.EqualFold(p.Protocol, "udp") {
		return corev1.ProtocolUDP
	}
	return corev1.ProtocolTCP
}

// BuildPortsService renders the LoadBalancer Service for an app's public
// ports, or nil when it has none.
func BuildPortsService(s AppSpec) *corev1.Service {
	if len(s.PublicPorts) == 0 {
		return nil
	}
	ports := make([]corev1.ServicePort, 0, len(s.PublicPorts))
	for _, p := range s.PublicPorts {
		ports = append(ports, corev1.ServicePort{
			Name:       fmt.Sprintf("%s-%d", strings.ToLower(p.Protocol), p.PublicPort),
			Port:       int32(p.PublicPort),
			TargetPort: intstr.FromInt32(int32(p.Port)),
			Protocol:   protocolOf(p),
		})
	}
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      PortsServiceName(s.Name),
			Namespace: s.Namespace,
			Labels:    s.Labels(),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeLoadBalancer,
			Selector: s.SelectorLabels(),
			Ports:    ports,
			// No node ports. ServiceLB forwards to the Service's own address
			// and never uses one, and each would count against the
			// environment's quota of none — which is what keeps a port in
			// 30000–32767 from opening on every server behind the panel's back.
			AllocateLoadBalancerNodePorts: ptr(false),
		},
	}
}

// BuildPortsPolicy renders the NetworkPolicy letting anybody reach an app's
// public ports, and only those, or nil when it has none.
func BuildPortsPolicy(s AppSpec) *networkingv1.NetworkPolicy {
	if len(s.PublicPorts) == 0 {
		return nil
	}
	ports := make([]networkingv1.NetworkPolicyPort, 0, len(s.PublicPorts))
	for _, p := range s.PublicPorts {
		protocol := protocolOf(p)
		port := intstr.FromInt32(int32(p.Port))
		ports = append(ports, networkingv1.NetworkPolicyPort{Protocol: &protocol, Port: &port})
	}
	return &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      PortsPolicyName(s.Name),
			Namespace: s.Namespace,
			Labels:    s.Labels(),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: s.SelectorLabels()},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			// No from: that is what public means. The ports are the limit.
			Ingress: []networkingv1.NetworkPolicyIngressRule{{Ports: ports}},
		},
	}
}

// publicContainerPorts are the container's side of its public ports.
func publicContainerPorts(s AppSpec) []corev1.ContainerPort {
	out := make([]corev1.ContainerPort, 0, len(s.PublicPorts))
	seen := map[string]bool{}
	for _, p := range s.PublicPorts {
		key := fmt.Sprintf("%d/%s", p.Port, p.Protocol)
		// The HTTP port is declared already, and a container may not list the
		// same port and protocol twice.
		if seen[key] || (p.Port == s.Port && protocolOf(p) == corev1.ProtocolTCP) {
			continue
		}
		seen[key] = true
		out = append(out, corev1.ContainerPort{ContainerPort: int32(p.Port), Protocol: protocolOf(p)})
	}
	return out
}
