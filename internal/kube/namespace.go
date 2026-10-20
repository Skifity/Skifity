package kube

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/version"
)

// Tenant isolation.
//
// Each environment is a namespace carrying three guards:
//
//   - a LimitRange, so a workload created without limits cannot starve a node;
//   - a ResourceQuota, so one environment cannot consume the whole cluster;
//   - a default-deny NetworkPolicy, so one team's apps cannot reach another's.
//
// The quota defaults are generous: they exist to contain a mistake, not to
// ration. An operator raises them per environment when they need to.

// NamespaceQuota describes an environment's ceiling.
type NamespaceQuota struct {
	CPUCores    int
	MemoryGB    int
	StorageGB   int
	MaxPods     int
	MaxServices int
	// LoadBalancers is how many apps in the environment have public ports,
	// each of which is one LoadBalancer Service. Zero for every other
	// environment, which keeps the guard below.
	LoadBalancers int
}

// DefaultQuota is what a new environment gets.
func DefaultQuota() NamespaceQuota {
	return NamespaceQuota{CPUCores: 8, MemoryGB: 16, StorageGB: 100, MaxPods: 60, MaxServices: 30}
}

// BuildNamespace renders the Namespace object for an environment.
func BuildNamespace(name, teamID, projectID string, level PodSecurity) *corev1.Namespace {
	if level == "" {
		level = PodSecurityRestricted
	}
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": version.Binary,
				version.LabelKey("team-id"):    teamID,
				version.LabelKey("project-id"): projectID,
				// Pod Security Admission. Enforcement is the environment's
				// chosen level; audit and warn stay at the strictest one, so a
				// namespace that has been lowered still records in the API
				// server's own log every pod restricted would have refused.
				// Lowering the bar should not also turn off the measurement.
				"pod-security.kubernetes.io/enforce": string(level),
				"pod-security.kubernetes.io/audit":   string(PodSecurityRestricted),
				"pod-security.kubernetes.io/warn":    string(PodSecurityRestricted),
			},
		},
	}
}

// BuildLimitRange renders the per-container defaults for a namespace.
func BuildLimitRange(namespace string) *corev1.LimitRange {
	return &corev1.LimitRange{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "LimitRange"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "defaults",
			Namespace: namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": version.Binary},
		},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{{
				Type: corev1.LimitTypeContainer,
				// Applied when a container sets no limit of its own.
				//
				// Ephemeral storage is the container's writable layer, its
				// logs and its emptyDir volumes, all on the node's own disk. A
				// container with no limit on it can fill that disk, and a full
				// disk is not one app failing: the kubelet evicts pods from the
				// whole machine, neighbours included. With a limit, the one
				// that writes without end is the one that is evicted. Volumes
				// the app asked for are claims on storage and are not counted.
				Default: corev1.ResourceList{
					corev1.ResourceMemory:           resource.MustParse("512Mi"),
					corev1.ResourceCPU:              resource.MustParse("1"),
					corev1.ResourceEphemeralStorage: resource.MustParse("8Gi"),
				},
				DefaultRequest: corev1.ResourceList{
					corev1.ResourceMemory:           resource.MustParse("128Mi"),
					corev1.ResourceCPU:              resource.MustParse("50m"),
					corev1.ResourceEphemeralStorage: resource.MustParse("100Mi"),
				},
			}},
		},
	}
}

// BuildResourceQuota renders an environment's ceiling.
func BuildResourceQuota(namespace string, q NamespaceQuota) *corev1.ResourceQuota {
	return &corev1.ResourceQuota{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ResourceQuota"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "environment",
			Namespace: namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": version.Binary},
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				"requests.cpu":     resource.MustParse(itoaResource(q.CPUCores)),
				"requests.memory":  resource.MustParse(itoaResource(q.MemoryGB) + "Gi"),
				"limits.cpu":       resource.MustParse(itoaResource(q.CPUCores * 2)),
				"limits.memory":    resource.MustParse(itoaResource(q.MemoryGB*2) + "Gi"),
				"requests.storage": resource.MustParse(itoaResource(q.StorageGB) + "Gi"),
				"pods":             resource.MustParse(itoaResource(q.MaxPods)),
				"services":         resource.MustParse(itoaResource(q.MaxServices)),
				// A LoadBalancer or NodePort service would open a port on every
				// node, bypassing the ingress and its TLS. The only ones allowed
				// are the panel's own, one per app with public ports.
				"services.loadbalancers": resource.MustParse(itoaResource(q.LoadBalancers)),
				"services.nodeports":     resource.MustParse("0"),
			},
		},
	}
}

// BuildNetworkPolicies renders the default-deny policy and the exceptions.
//
// Two objects rather than one: a deny-all baseline and an allow policy, because
// Kubernetes network policies are additive and this is far easier to reason
// about than one combined object.
//
// nodeAddresses is every address the cluster's nodes report; the public ones
// among them are kept out of reach (see InternetEgress).
func BuildNetworkPolicies(namespace, systemNamespace string, nodeAddresses []string) []*networkingv1.NetworkPolicy {
	labels := map[string]string{"app.kubernetes.io/managed-by": version.Binary}
	dnsPort := intstr.FromInt32(53)
	udp := corev1.ProtocolUDP
	tcp := corev1.ProtocolTCP

	denyAll := &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-deny", Namespace: namespace, Labels: labels,
		},
		Spec: networkingv1.NetworkPolicySpec{
			// An empty selector means every pod in the namespace.
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
		},
	}

	allow := &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "allow-ingress-and-egress", Namespace: namespace, Labels: labels,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{
					// Traffic from the ingress controller and the panel.
					{NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
					}},
					{NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kubernetes.io/metadata.name": systemNamespace},
					}},
					// KEDA's interceptor, which is what a request to an app
					// that has scaled to zero arrives through. Without this the
					// app wakes up and the request that woke it is dropped.
					{NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kubernetes.io/metadata.name": KEDANamespace},
					}},
					// And between pods of this same environment, so an app can
					// reach its own database.
					{PodSelector: &metav1.LabelSelector{}},
				},
			}},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					// DNS, without which nothing resolves anything.
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
				{
					// Within the environment.
					To: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}},
				},
			},
		},
	}
	// And out to the internet, but not to other namespaces, the private
	// networks, the cloud metadata services, or the nodes themselves, which is
	// where the Kubernetes API, the kubelet, SSH and the registry are.
	allow.Spec.Egress = append(allow.Spec.Egress, InternetEgress(nodeAddresses)...)
	return []*networkingv1.NetworkPolicy{denyAll, allow, databasePolicy(namespace, nodeAddresses)}
}

// The managed databases' operator, and the port the Kubernetes API listens on
// on a server.
const (
	DatabaseOperatorNamespace = "cnpg-system"
	APIServerPort             = 6443
	// databaseStatusPort is where a database pod's instance manager answers
	// the operator.
	databaseStatusPort = 8000
)

// databasePolicy opens what a managed database's own pods need beyond what
// every pod in the environment has.
//
// CloudNativePG's instance manager runs inside each database pod and talks to
// the Kubernetes API: it reads its Cluster before PostgreSQL is initialised,
// and gives up when it cannot. The API is on the servers' own addresses, which
// the environment's policy keeps every pod away from, so a database was asked
// for, its first pod spent half a minute refused by an API it would never
// reach, and the database stayed "unknown" for good. The operator, in its own
// namespace, asks each instance how it is on its status port, and without that
// it cannot tell that a primary is up.
//
// Both are opened for the database's pods only, chosen by the labels
// CloudNativePG gives them. No app's pod carries those: an app's labels are
// the panel's, never the team's.
func databasePolicy(namespace string, nodeAddresses []string) *networkingv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	status := intstr.FromInt32(databaseStatusPort)
	policy := &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "allow-databases", Namespace: namespace,
			Labels: map[string]string{"app.kubernetes.io/managed-by": version.Binary},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/managed-by": "cloudnative-pg"},
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: "cnpg.io/cluster", Operator: metav1.LabelSelectorOpExists},
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"kubernetes.io/metadata.name": DatabaseOperatorNamespace},
				}}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &status}},
			}},
		},
	}
	// The API is reached through its Service, which is translated to a
	// server's own address before the policy looks; so it is named by those
	// addresses, on the API's port only. With no address known there is no
	// rule at all: a rule with ports and no peers allows that port anywhere.
	var servers []networkingv1.NetworkPolicyPeer
	for _, address := range NodeIPv4(nodeAddresses) {
		servers = append(servers, networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: address + "/32"}})
	}
	if len(servers) > 0 {
		api := intstr.FromInt32(APIServerPort)
		policy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
			To:    servers,
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &api}},
		}}
	}
	return policy
}

// NodeIPv4 is every IPv4 address the nodes report, private ones included,
// once each and in order. The cluster's networks are IPv4, and a policy with
// an IPv6 block in it is one the policy controller may refuse whole.
func NodeIPv4(addresses []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, address := range addresses {
		ip, err := netip.ParseAddr(address)
		if err != nil {
			continue
		}
		ip = ip.Unmap()
		if !ip.Is4() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || seen[ip.String()] {
			continue
		}
		seen[ip.String()] = true
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return out
}

// PolicyAddressKey says whether the nodes' addresses have changed in a way
// the environments' policies would notice: the public ones, which every pod is
// kept from, and every IPv4 one, which a database is let through to the API on.
func PolicyAddressKey(addresses []string) string {
	return strings.Join(PublicNodeAddresses(addresses), ",") + ";" + strings.Join(NodeIPv4(addresses), ",")
}

func itoaResource(n int) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// QuotaUsage is what an environment has used out of what it is allowed.
//
// The quota exists from the moment a namespace is created and nothing in the
// panel showed it, so the first sign of reaching one was a deployment that
// failed with a message about a resource nobody had heard of. A number on the
// page is the difference between a limit and a surprise.
type QuotaUsage struct {
	// Found is false when the namespace has no quota, which is the case for an
	// environment created before quotas existed and for a cluster somebody has
	// deliberately opened up.
	Found bool `json:"found"`
	// Items are the individual limits, in the order the panel shows them.
	Items []QuotaItem `json:"items"`
}

// QuotaItem is one limit and what has been used against it.
type QuotaItem struct {
	// Resource is the Kubernetes name, such as requests.memory.
	Resource string `json:"resource"`
	// Used and Hard are the quantities as Kubernetes writes them, for example
	// "3" or "1536Mi". They are strings because that is what they are: a
	// quantity carries its unit, and rendering it is the panel's job.
	Used string `json:"used"`
	Hard string `json:"hard"`
	// UsedValue and HardValue are the same two as plain numbers, so a bar can
	// be drawn without the browser having to parse Kubernetes quantities.
	// Memory and storage are in mebibytes, CPU in millicores, and a count is
	// itself.
	UsedValue int64 `json:"used_value"`
	HardValue int64 `json:"hard_value"`
}

// Percent is how full one limit is, bounded at a hundred.
func (q QuotaItem) Percent() int {
	if q.HardValue <= 0 {
		return 0
	}
	percent := int(q.UsedValue * 100 / q.HardValue)
	if percent > 100 {
		return 100
	}
	return percent
}

// QuotaUsage reads an environment's ResourceQuota.
func (c *Client) QuotaUsage(ctx context.Context, namespace string) (QuotaUsage, error) {
	quota, err := c.clientset.CoreV1().ResourceQuotas(namespace).Get(ctx, "environment", metav1.GetOptions{})
	if err != nil {
		if IsNotFound(err) {
			return QuotaUsage{}, nil
		}
		return QuotaUsage{}, fmt.Errorf("read the limits for %s: %w", namespace, err)
	}

	usage := QuotaUsage{Found: true}
	// A fixed order rather than the map's: the two that decide whether a
	// deployment fits come first, and a page whose rows move between reloads is
	// a page nobody can scan.
	for _, name := range []corev1.ResourceName{
		"requests.cpu", "requests.memory", "pods",
		"limits.cpu", "limits.memory", "requests.storage", "services",
	} {
		hard, ok := quota.Status.Hard[name]
		if !ok {
			continue
		}
		used := quota.Status.Used[name]
		usage.Items = append(usage.Items, QuotaItem{
			Resource:  string(name),
			Used:      used.String(),
			Hard:      hard.String(),
			UsedValue: quantityValue(name, used),
			HardValue: quantityValue(name, hard),
		})
	}
	return usage, nil
}

// quantityValue turns a Kubernetes quantity into a number the UI can compare.
//
// CPU in millicores and bytes in mebibytes, because those are the units the
// rest of the panel already speaks; anything else is a count and is itself.
func quantityValue(name corev1.ResourceName, q resource.Quantity) int64 {
	switch {
	case strings.HasSuffix(string(name), "cpu"):
		return q.MilliValue()
	case strings.HasSuffix(string(name), "memory"), strings.HasSuffix(string(name), "storage"):
		return q.Value() / (1024 * 1024)
	default:
		return q.Value()
	}
}

// GuardMiddleware is the name of the Traefik middleware that asks the guard
// whether a request may go through.
const GuardMiddleware = "firewall"

// GuardService is where the guard answers, and the port it answers on.
const (
	GuardService = "skifity-guard"
	GuardPort    = 9000
)

// BuildGuardMiddleware renders the forwardAuth middleware into one namespace.
//
// The address is a URL rather than a Kubernetes reference, so pointing it at a
// Service in the panel's namespace is not a cross-namespace object reference
// and needs nothing turned on. The middleware object itself does have to be
// beside the Ingress that names it: Traefik refuses a middleware from another
// namespace unless allowCrossNamespace is turned on, and it is off by default.
//
// authResponseHeaders is deliberately empty. The guard answers yes or no and
// has nothing to add to the request, and a header it did set would be one an
// app might come to trust — which would make this a way of telling an app
// something rather than a way of stopping a request.
func BuildGuardMiddleware(namespace, systemNamespace string) *unstructured.Unstructured {
	address := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/authorize",
		GuardService, systemNamespace, GuardPort)
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "Middleware",
		"metadata": map[string]any{
			"name":      GuardMiddleware,
			"namespace": namespace,
			"labels":    map[string]any{"app.kubernetes.io/managed-by": version.Binary},
		},
		"spec": map[string]any{
			"forwardAuth": map[string]any{
				"address": address,
				// authRequestHeaders is deliberately not set, which forwards
				// every header rather than a list.
				//
				// A list was the first draft and it was wrong: a rule may test
				// any header by name, so a list would have to hold every header
				// anybody might ever write a rule about. What it would actually
				// do is make a rule on X-Api-Key match nothing, with no error
				// and no sign — the rule saved, the request allowed, and the
				// header never sent to the process judging it.
			},
		},
	}}
}

// The networks k3s gives pods and services, which are its defaults and which
// Skifity does not override.
//
// They are here rather than beside either user because two packages need them
// and neither may import the other: the server firewall, which has to trust
// them wholesale because traffic between pods is not on a fixed port and cannot
// be enumerated, and the request firewall, which has to know which addresses
// are its own proxies rather than a visitor's.
const (
	PodCIDR     = "10.42.0.0/16"
	ServiceCIDR = "10.43.0.0/16"
)
