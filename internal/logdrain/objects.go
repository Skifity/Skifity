package logdrain

import (
	"fmt"
	"net"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/kube"
	"skifity/internal/version"
)

// The collector, as Kubernetes objects.
//
// The collector is Vector, pinned by version and by digest like every image
// the panel runs on somebody's behalf (internal/builder/images.go,
// internal/vulnscan/job.go): an agent that moves under a tag is one whose
// behaviour changes with nothing in the panel changed, and whoever can move
// the tag chooses what runs on every server with every team's credentials.
//
// The digest is the multi-architecture index of
// timberio/vector:0.58.0-distroless-static (amd64, arm64 and armv7), read
// from Docker Hub's registry API on 2026-09-30. To move it, read the new
// tag's index digest the same way and change the version and the digest
// together. The distroless static image is one statically linked binary and
// the CA certificates: no shell, no package manager.
const (
	VectorVersion = "0.58.0"
	VectorImage   = "timberio/vector:" + VectorVersion + "-distroless-static" +
		"@sha256:f41132f3675185e4b63ef608ba3d6b6406727d8fe57169c495696ac610a7d1dc"
)

// Where the collector lives.
//
// A namespace of its own rather than the panel's. Reading the servers' log
// files takes a host path, which the baseline Pod Security profile the
// panel's namespace enforces refuses, and lowering the namespace that holds
// the master key to make room for an agent would be exactly backwards — the
// same reasoning as the builds' namespace (ADR-0016). Here the privileged
// profile covers one DaemonSet whose own pod spec asks for almost none of it.
const (
	Namespace     = "skifity-logs"
	CollectorName = "skifity-log-collector"
	// PodLogDir is where the kubelet keeps each container's log file, and the
	// one part of a server the collector sees.
	PodLogDir = "/var/log/pods"
	// collectorUID is who Vector runs as: nobody in particular. It reads the
	// log files as group root, which is what containerd writes them for
	// (mode 0640, owner and group root); it needs no user of its own and no
	// capability at all.
	collectorUID = 65532
)

// Labels are the collector's own, which its pods are found by.
func Labels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       CollectorName,
		"app.kubernetes.io/component":  "logs",
		"app.kubernetes.io/managed-by": version.Binary,
	}
}

// Selector finds the collector's pods.
func Selector() string { return "app.kubernetes.io/name=" + CollectorName }

// Rules are everything the collector may do with the Kubernetes API: read
// which pods run on its server and which namespaces they are in, to know which
// files to read and whose each line is. Nothing is written, and no Secret,
// ConfigMap or log is readable through this: the files are read from the
// server's disk.
//
// Nodes are the one addition to pods and namespaces, list and watch only.
// Vector's kubernetes_logs source watches its own Node unconditionally (in
// 0.58, src/sources/kubernetes_logs/mod.rs) to put the node's labels beside
// each line; refused, it keeps going and logs "Failed to annotate event with
// node metadata" at error level with the whole event in it — every app's
// line, in the collector's own log.
func Rules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{"namespaces", "pods"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"list", "watch"}},
	}
}

// Objects renders the collector, in the order it is applied: somewhere to
// run, who it runs as and what that may read, what it may reach, its
// configuration, and then the collector itself — so the pods find their
// configuration already there.
func Objects(config Config, image string) ([]any, error) {
	if image == "" {
		image = VectorImage
	}
	labels := Labels()
	meta := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: Namespace, Labels: Labels()}
	}

	namespace := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name: Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": version.Binary,
				version.LabelKey("component"):  "logs",
				// A host path is refused by every profile but privileged.
				// Audited and warned at restricted, so anything else put
				// here is noticed.
				"pod-security.kubernetes.io/enforce": "privileged",
				"pod-security.kubernetes.io/audit":   string(kube.PodSecurityRestricted),
				"pod-security.kubernetes.io/warn":    string(kube.PodSecurityRestricted),
			},
		},
	}

	account := &corev1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: meta(CollectorName),
	}
	role := &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
		ObjectMeta: metav1.ObjectMeta{Name: CollectorName, Labels: Labels()},
		Rules:      Rules(),
	}
	binding := &rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: CollectorName, Labels: Labels()},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: CollectorName},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: CollectorName, Namespace: Namespace}},
	}

	policy, err := networkPolicy()
	if err != nil {
		return nil, err
	}

	secret := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: meta(CollectorName),
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			ConfigFile: config.YAML,
			NamesFile:  config.Names,
		},
	}

	return []any{namespace, account, role, binding, policy, secret, daemonSet(image, labels)}, nil
}

// daemonSet is the collector: one on every server.
//
// What it is given, and why, because a log collector is the one thing the
// panel runs that reads the servers themselves:
//
//   - /var/log/pods, read-only. That is where the kubelet keeps every
//     container's output, and the only way to read it without going through
//     the Kubernetes API for every line of every app. Nothing else of the
//     server is mounted.
//   - A service account token, for the three reads in Rules.
//   - Group root, for the log files; see collectorUID. Not root itself,
//     no capability, no privilege escalation, a read-only root filesystem
//     and the runtime's seccomp profile.
//   - Its configuration, from a Secret, because it holds the drains'
//     credentials. None of them is in this spec, which anything that can
//     list DaemonSets can read.
//   - A scratch volume for where it is in each file.
func daemonSet(image string, labels map[string]string) *appsv1.DaemonSet {
	unavailable := intstr.FromInt32(1)
	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name: CollectorName, Namespace: Namespace, Labels: labels,
			Annotations: map[string]string{version.LabelKey("collector-version"): VectorVersion},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": CollectorName}},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type:          appsv1.RollingUpdateDaemonSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDaemonSet{MaxUnavailable: &unavailable},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName:           CollectorName,
					AutomountServiceAccountToken: ptr(true),
					EnableServiceLinks:           ptr(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr(true),
						RunAsUser:      ptr(int64(collectorUID)),
						RunAsGroup:     ptr(int64(0)),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					// On every server an app can run on, the control plane
					// included: an app's lines are on the server it runs on.
					Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}},
					Containers: []corev1.Container{{
						Name:            "vector",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						// A changed drain reaches a running collector through
						// its configuration file, which Kubernetes updates in
						// place and Vector reads again: the collector keeps its
						// place in every file rather than being replaced, and
						// replaced would mean reading them all again. Polled
						// rather than watched with inotify, which does not see
						// the way Kubernetes swaps a Secret's files.
						Args: []string{
							"--config", ConfigDir + "/" + ConfigFile,
							"--watch-config",
							"--watch-config-method", "poll",
							"--watch-config-poll-interval-seconds", "30",
						},
						Env: []corev1.EnvVar{{
							Name: "VECTOR_SELF_NODE_NAME",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
							},
						}},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: ConfigDir, ReadOnly: true},
							{Name: "pod-logs", MountPath: PodLogDir, ReadOnly: true},
							{Name: "data", MountPath: DataDir},
							{Name: "tmp", MountPath: "/tmp"},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("96Mi"),
							},
							// Each drain holds at most a thousand lines while
							// its service is slow; see Render.
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1"),
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							Privileged:               ptr(false),
							AllowPrivilegeEscalation: ptr(false),
							RunAsNonRoot:             ptr(true),
							ReadOnlyRootFilesystem:   ptr(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
							SecretName: CollectorName,
							// Owner and group, and the collector's group is
							// root's: nobody else in the pod reads them.
							DefaultMode: ptr(int32(0o440)),
						}}},
						{Name: "pod-logs", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
							Path: PodLogDir,
							Type: ptr(corev1.HostPathDirectory),
						}}},
						{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
							SizeLimit: ptr(resource.MustParse("64Mi")),
						}}},
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
							SizeLimit: ptr(resource.MustParse("16Mi")),
						}}},
					},
				},
			},
		},
	}
}

// networkPolicy is where the collector may connect: DNS, the Kubernetes API,
// and anywhere outside the cluster's own networks and the cloud's metadata
// service. A drain pointed at the panel, at another team's app or at a
// service inside the cluster is refused here, as the panel's test refuses the
// machine itself and the metadata service. Nothing may connect to it.
func networkPolicy() (*networkingv1.NetworkPolicy, error) {
	apiServer, err := firstAddress(kube.ServiceCIDR)
	if err != nil {
		return nil, err
	}
	dns := intstr.FromInt32(53)
	https := intstr.FromInt32(443)
	udp, tcp := corev1.ProtocolUDP, corev1.ProtocolTCP
	return &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: CollectorName, Namespace: Namespace, Labels: Labels()},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": CollectorName}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
					}}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dns}, {Protocol: &tcp, Port: &dns}},
				},
				{
					// The Kubernetes API by its Service address, for a network
					// plugin that checks a connection before the address is
					// translated; after, it is a server's own address and the
					// rule below.
					To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: apiServer + "/32"}}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &https}},
				},
				{
					To: []networkingv1.NetworkPolicyPeer{
						{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{
							kube.PodCIDR, kube.ServiceCIDR,
							// Link-local, where the cloud's metadata service
							// is, and the two that are not on it.
							"169.254.0.0/16", "100.100.100.200/32", "192.0.0.192/32",
						}}},
						{IPBlock: &networkingv1.IPBlock{CIDR: "::/0", Except: []string{"fe80::/10", "fd00:ec2::254/128"}}},
					},
				},
			},
		},
	}, nil
}

// firstAddress is a network's first host address, which is where k3s puts
// the Kubernetes API's Service.
func firstAddress(cidr string) (string, error) {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", cidr, err)
	}
	v4 := ip.To4()
	if v4 == nil {
		return "", fmt.Errorf("%s is not an IPv4 network", cidr)
	}
	next := make(net.IP, len(v4))
	copy(next, v4)
	next[3]++
	return next.String(), nil
}

// Removal is what taking the collector away deletes, in order: the collector
// first, so nothing is left running without its configuration.
type Removal struct {
	APIVersion, Kind, Namespace, Name string
}

// Removals is every object Objects makes but the namespace, which is left:
// an empty namespace costs nothing, and one being deleted cannot take the
// next drain's objects until it is gone.
func Removals() []Removal {
	return []Removal{
		{"apps/v1", "DaemonSet", Namespace, CollectorName},
		{"v1", "Secret", Namespace, CollectorName},
		{"networking.k8s.io/v1", "NetworkPolicy", Namespace, CollectorName},
		{"rbac.authorization.k8s.io/v1", "ClusterRoleBinding", "", CollectorName},
		{"rbac.authorization.k8s.io/v1", "ClusterRole", "", CollectorName},
		{"v1", "ServiceAccount", Namespace, CollectorName},
	}
}

func ptr[T any](v T) *T { return &v }
