package kube

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/version"
)

// Where a plugin runs, and what it can reach from there.
//
// One namespace per plugin, not one shared namespace with all of them in it.
// Two plugins from two publishers have no more reason to reach each other than
// two tenants do, and a namespace is the boundary Kubernetes already enforces —
// a NetworkPolicy, a quota and a Pod Security level come with it for free.
//
// What a plugin can reach is deliberately short:
//
//   - the panel's API, because that is what its token is for;
//   - DNS, without which nothing resolves anything;
//   - the internet, because a plugin that copies backups to a cloud provider is
//     the obvious first plugin anybody writes.
//
// And what it cannot: any other namespace, which is every app, every database
// and every other plugin; the node network, where the Kubernetes API and the
// kubelet live; and the cloud metadata address, which hands out the provider
// credentials for the whole machine to anything that asks.

// PluginNamespacePrefix is how a plugin's namespace is named.
const PluginNamespacePrefix = "skifity-plugin-"

// PluginPort is where a plugin answers if its manifest named nothing.
const PluginPort = 8080

// Plugin resource ceilings. A plugin declares what it expects to use and the
// panel shows that before installing; these are the bounds, so that a manifest
// asking for four gigabytes is a manifest that gets a sensible number instead.
const (
	PluginMinMemoryMB = 32
	PluginMaxMemoryMB = 512
)

// PluginNamespace is the namespace a plugin runs in.
//
// The id is reverse-DNS and a namespace is a DNS label, so it is slugified and
// given a short hash of the original: two ids that slugify the same must not
// land in one namespace, and a name that is merely long must stay readable.
func PluginNamespace(id string) string {
	slug := Slugify(strings.ReplaceAll(id, ".", "-"))
	name := PluginNamespacePrefix + slug
	if len(name) > maxLabelLength {
		keep := maxLabelLength - len(PluginNamespacePrefix) - 7
		name = PluginNamespacePrefix + strings.TrimRight(slug[:keep], "-") + "-" + shortHash(id, 6)
	}
	return name
}

// PluginSpec is everything needed to run one plugin.
//
// A plain struct with no manifest type in it, so that rendering is a pure
// function of values a test can write down.
type PluginSpec struct {
	// ID is the plugin's own identifier, for labels and for the log.
	ID string
	// Name is what it calls itself, for the object's own annotation.
	Name string
	// Image is pinned by digest; the standard refuses anything else.
	Image string
	// SecretName holds the token, the signing secret and the settings.
	SecretName string
	// Port is where the plugin answers, and Health the path that says it is up.
	Port   int
	Health string
	// MemoryMB is what the manifest asked for, before bounding.
	MemoryMB int
	// SystemNamespace is where the panel runs, which is the one namespace this
	// plugin may talk to.
	SystemNamespace string
}

func (s PluginSpec) port() int {
	if s.Port <= 0 {
		return PluginPort
	}
	return s.Port
}

func (s PluginSpec) health() string {
	if s.Health == "" {
		return "/healthz"
	}
	return s.Health
}

func (s PluginSpec) memory() int {
	switch {
	case s.MemoryMB < PluginMinMemoryMB:
		return PluginMinMemoryMB
	case s.MemoryMB > PluginMaxMemoryMB:
		return PluginMaxMemoryMB
	default:
		return s.MemoryMB
	}
}

// PluginDeploymentName is the one object a plugin's pod belongs to.
const PluginDeploymentName = "plugin"

func pluginLabels(id string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       PluginDeploymentName,
		"app.kubernetes.io/component":  "plugin",
		"app.kubernetes.io/managed-by": version.Binary,
		version.LabelKey("plugin-id"):  Slugify(strings.ReplaceAll(id, ".", "-")),
	}
}

// BuildPluginObjects renders everything one plugin needs, except its Secret,
// which the caller builds because only it can seal anything.
func BuildPluginObjects(s PluginSpec) []any {
	namespace := PluginNamespace(s.ID)
	labels := pluginLabels(s.ID)
	replicas := int32(1)

	probe := func(delay, period int32) *corev1.Probe {
		return &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
				Path: s.health(), Port: intstr.FromString("plugin"),
			}},
			InitialDelaySeconds: delay,
			PeriodSeconds:       period,
			FailureThreshold:    3,
		}
	}

	ns := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": version.Binary,
				version.LabelKey("plugin-id"):  labels[version.LabelKey("plugin-id")],
				// Restricted, and a plugin author can meet it: unlike an
				// off-the-shelf application image, a plugin is written for this
				// panel and its author controls the Dockerfile. The standard
				// says so, so that "my plugin will not start" is answered
				// before it is asked.
				"pod-security.kubernetes.io/enforce": string(PodSecurityRestricted),
				"pod-security.kubernetes.io/audit":   string(PodSecurityRestricted),
				"pod-security.kubernetes.io/warn":    string(PodSecurityRestricted),
			},
			Annotations: map[string]string{version.LabelKey("plugin-name"): s.Name},
		},
	}

	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name: PluginDeploymentName, Namespace: namespace, Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			// One. A plugin is not in the request path of anything, so a
			// moment without one during a restart costs an event that is
			// retried rather than a page that does not load — and two copies
			// of a plugin that writes somewhere is a plugin that writes twice.
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:            "plugin",
						Image:           s.Image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Ports: []corev1.ContainerPort{{
							Name: "plugin", ContainerPort: int32(s.port()),
						}},
						// Everything the plugin is given arrives in one Secret:
						// its token, the secret it verifies events with, and
						// its own settings. One object to rotate and one to
						// delete when the plugin goes.
						EnvFrom: []corev1.EnvFromSource{{
							SecretRef: &corev1.SecretEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: s.SecretName},
							},
						}},
						Env: []corev1.EnvVar{
							{Name: "SKIFITY_PLUGIN_ID", Value: s.ID},
							{Name: "SKIFITY_API", Value: fmt.Sprintf(
								"http://skifity-panel.%s.svc.cluster.local", s.SystemNamespace)},
							{Name: "PORT", Value: fmt.Sprint(s.port())},
						},
						ReadinessProbe: probe(3, 5),
						LivenessProbe:  probe(30, 30),
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse(
									fmt.Sprintf("%dMi", s.memory()/2)),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse(
									fmt.Sprintf("%dMi", s.memory())),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr(false),
							RunAsNonRoot:             ptr(true),
							ReadOnlyRootFilesystem:   ptr(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
					}},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr(true),
						// Deliberately not pinned to a uid: a plugin author
						// controls their own image and declares its USER, and
						// overriding it here would be the guess that was wrong
						// for almost every catalogue image.
						FSGroup: ptr(int64(1000)),
					},
					// A plugin talks to the panel's API with a token. Giving it
					// a Kubernetes token as well would be handing it a second,
					// wider way in that nothing in its manifest asked for.
					AutomountServiceAccountToken:  ptr(false),
					TerminationGracePeriodSeconds: ptr(int64(20)),
				},
			},
		},
	}

	service := &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name: PluginDeploymentName, Namespace: namespace, Labels: labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name: "plugin", Port: int32(s.port()), TargetPort: intstr.FromString("plugin"),
			}},
		},
	}

	objects := []any{ns, deployment, service}
	for _, policy := range buildPluginNetworkPolicies(namespace, s.SystemNamespace) {
		objects = append(objects, policy)
	}
	return objects
}

// PluginServiceURL is where the panel posts a plugin's events.
func PluginServiceURL(id string, port int) string {
	if port <= 0 {
		port = PluginPort
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		PluginDeploymentName, PluginNamespace(id), port)
}

// buildPluginNetworkPolicies confines a plugin to the panel, DNS and the
// internet.
func buildPluginNetworkPolicies(namespace, systemNamespace string) []*networkingv1.NetworkPolicy {
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
				networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress,
			},
		},
	}

	allow := &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "allow-panel-and-internet", Namespace: namespace, Labels: labels},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				// Only the panel may reach a plugin. An app must not be able to
				// call a plugin's endpoint directly and skip whatever the panel
				// checks before it does.
				From: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kubernetes.io/metadata.name": systemNamespace},
					},
				}},
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
				{
					// The panel's API, which is what the plugin's token is for.
					To: []networkingv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"kubernetes.io/metadata.name": systemNamespace},
						},
					}},
				},
				{
					// Out to the internet, and nowhere private. A plugin that
					// copies backups to a cloud provider is the obvious first
					// plugin anybody writes; a plugin reading another
					// namespace's database is not something to make possible.
					To: []networkingv1.NetworkPolicyPeer{{
						IPBlock: &networkingv1.IPBlock{
							CIDR: "0.0.0.0/0",
							Except: []string{
								"10.0.0.0/8",
								"172.16.0.0/12",
								"192.168.0.0/16",
								// The cloud metadata endpoint, which hands out
								// the provider credentials for the whole
								// machine to anything that asks.
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
