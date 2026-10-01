package logdrain

import (
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

type collectorObjects struct {
	namespace *corev1.Namespace
	account   *corev1.ServiceAccount
	role      *rbacv1.ClusterRole
	binding   *rbacv1.ClusterRoleBinding
	policy    *networkingv1.NetworkPolicy
	secret    *corev1.Secret
	daemonSet *appsv1.DaemonSet
	order     []string
}

func renderedObjects(t *testing.T) (collectorObjects, Config) {
	t.Helper()
	config, err := Render(RenderInput{Drains: everyKind(t), Names: everyName(), BuildsNamespace: "skifity-builds"})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := Objects(config, "")
	if err != nil {
		t.Fatal(err)
	}
	var out collectorObjects
	for _, object := range objects {
		switch typed := object.(type) {
		case *corev1.Namespace:
			out.namespace = typed
			out.order = append(out.order, "Namespace")
		case *corev1.ServiceAccount:
			out.account = typed
			out.order = append(out.order, "ServiceAccount")
		case *rbacv1.ClusterRole:
			out.role = typed
			out.order = append(out.order, "ClusterRole")
		case *rbacv1.ClusterRoleBinding:
			out.binding = typed
			out.order = append(out.order, "ClusterRoleBinding")
		case *networkingv1.NetworkPolicy:
			out.policy = typed
			out.order = append(out.order, "NetworkPolicy")
		case *corev1.Secret:
			out.secret = typed
			out.order = append(out.order, "Secret")
		case *appsv1.DaemonSet:
			out.daemonSet = typed
			out.order = append(out.order, "DaemonSet")
		default:
			t.Fatalf("the collector rendered an object nothing expects: %T", object)
		}
	}
	return out, config
}

// The credentials are in the Secret and nowhere else: not the DaemonSet,
// which anything that can list DaemonSets reads, not a label, not an
// argument, not the RBAC or the policy.
func TestTheCredentialsAreOnlyInTheSecret(t *testing.T) {
	objects, config := renderedObjects(t)
	inSecret := string(objects.secret.Data[ConfigFile])
	for _, secret := range fakeSecrets {
		if !strings.Contains(inSecret, secret) {
			t.Errorf("the configuration in the Secret does not carry %q; the collector would send without it", secret)
		}
	}
	for name, object := range map[string]any{
		"DaemonSet": objects.daemonSet, "Namespace": objects.namespace, "ServiceAccount": objects.account,
		"ClusterRole": objects.role, "ClusterRoleBinding": objects.binding, "NetworkPolicy": objects.policy,
		"Secret's metadata": objects.secret.ObjectMeta,
	} {
		encoded, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range fakeSecrets {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("the %s carries a credential: %q", name, secret)
			}
		}
	}
	// And the summary a log line is given has nothing of any drain in it.
	for _, secret := range fakeSecrets {
		if strings.Contains(config.Summary(), secret) {
			t.Errorf("the summary carries %q", secret)
		}
	}
	if strings.Contains(config.Summary(), "logs.example.com") {
		t.Error("the summary names a drain's address")
	}

	// The DaemonSet reads the configuration from that Secret, as files.
	var found bool
	for _, volume := range objects.daemonSet.Spec.Template.Spec.Volumes {
		if volume.Secret != nil && volume.Secret.SecretName == objects.secret.Name {
			found = true
		}
		if volume.ConfigMap != nil {
			t.Errorf("the collector reads a ConfigMap, %s", volume.ConfigMap.Name)
		}
	}
	if !found {
		t.Error("the collector does not mount its Secret")
	}
	// Applied before the collector, so the pods find it there.
	if !slices.Equal(objects.order, []string{"Namespace", "ServiceAccount", "ClusterRole", "ClusterRoleBinding",
		"NetworkPolicy", "Secret", "DaemonSet"}) {
		t.Errorf("the objects are applied in the order %v", objects.order)
	}
}

// Exactly these rules, and no more: reading pods, namespaces and its own node
// to know which files to read and whose each line is. No write, no Secret, no
// ConfigMap, no log through the API.
func TestTheCollectorMayOnlyReadWhatItNeeds(t *testing.T) {
	objects, _ := renderedObjects(t)
	want := []rbacv1.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{"namespaces", "pods"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"list", "watch"}},
	}
	if !reflect.DeepEqual(objects.role.Rules, want) {
		t.Errorf("the collector's rules are %+v", objects.role.Rules)
	}
	if objects.binding.RoleRef.Name != objects.role.Name || len(objects.binding.Subjects) != 1 ||
		objects.binding.Subjects[0].Name != objects.account.Name || objects.binding.Subjects[0].Namespace != Namespace {
		t.Errorf("the binding gives the rules to %+v", objects.binding.Subjects)
	}
	if objects.daemonSet.Spec.Template.Spec.ServiceAccountName != objects.account.Name {
		t.Error("the collector does not run as its own account")
	}
}

var pinned = regexp.MustCompile(`^timberio/vector:[0-9]+\.[0-9]+\.[0-9]+-distroless-static@sha256:[0-9a-f]{64}$`)

// The collector is confined to what a collector needs: the pod logs,
// read-only, and nothing else of the server; not root, no capability, no
// escalation, a read-only root filesystem, limits.
func TestTheCollectorIsConfined(t *testing.T) {
	objects, _ := renderedObjects(t)
	pod := objects.daemonSet.Spec.Template.Spec
	if len(pod.Containers) != 1 || len(pod.InitContainers) != 0 {
		t.Fatalf("the collector runs %d containers", len(pod.Containers))
	}
	container := pod.Containers[0]

	if !pinned.MatchString(container.Image) || container.Image != VectorImage || !strings.Contains(VectorImage, VectorVersion) {
		t.Errorf("the collector's image %q is not pinned by version and digest", container.Image)
	}

	hostPaths := 0
	for _, volume := range pod.Volumes {
		if volume.HostPath != nil {
			hostPaths++
			if volume.HostPath.Path != "/var/log/pods" {
				t.Errorf("the collector mounts %s from the server", volume.HostPath.Path)
			}
		}
	}
	if hostPaths != 1 {
		t.Errorf("the collector mounts %d paths from the server", hostPaths)
	}
	for _, mount := range container.VolumeMounts {
		isHost := false
		for _, volume := range pod.Volumes {
			if volume.Name == mount.Name && volume.HostPath != nil {
				isHost = true
			}
		}
		if isHost && !mount.ReadOnly {
			t.Errorf("%s is mounted writable", mount.MountPath)
		}
		if mount.Name == "config" && !mount.ReadOnly {
			t.Error("the configuration is mounted writable")
		}
	}

	security := container.SecurityContext
	switch {
	case security == nil:
		t.Fatal("the collector has no security context")
	case security.Privileged == nil || *security.Privileged:
		t.Error("the collector is privileged")
	case security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation:
		t.Error("the collector may escalate its privileges")
	case security.ReadOnlyRootFilesystem == nil || !*security.ReadOnlyRootFilesystem:
		t.Error("the collector's root filesystem is writable")
	case security.Capabilities == nil || !slices.Equal(security.Capabilities.Drop, []corev1.Capability{"ALL"}) ||
		len(security.Capabilities.Add) != 0:
		t.Errorf("the collector keeps capabilities: %+v", security.Capabilities)
	case security.RunAsNonRoot == nil || !*security.RunAsNonRoot:
		t.Error("the collector may run as root")
	case security.SeccompProfile == nil || security.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault:
		t.Error("the collector has no seccomp profile")
	}
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsUser == nil || *pod.SecurityContext.RunAsUser == 0 {
		t.Error("the collector runs as root")
	}
	if pod.HostNetwork || pod.HostPID || pod.HostIPC {
		t.Error("the collector shares a namespace with the server")
	}
	if container.Resources.Limits.Memory().IsZero() || container.Resources.Limits.Cpu().IsZero() ||
		container.Resources.Requests.Memory().IsZero() {
		t.Errorf("the collector has no limits: %+v", container.Resources)
	}
	// It learns its server from Kubernetes, which is how it knows which
	// pods' files are its to read.
	if len(container.Env) != 1 || container.Env[0].Name != "VECTOR_SELF_NODE_NAME" || container.Env[0].ValueFrom == nil ||
		container.Env[0].ValueFrom.FieldRef.FieldPath != "spec.nodeName" {
		t.Errorf("the collector's environment is %+v", container.Env)
	}
	// A changed drain reaches it without a restart.
	if !slices.Contains(container.Args, "--watch-config") || !slices.Contains(container.Args, "poll") {
		t.Errorf("the collector does not read its configuration again: %v", container.Args)
	}

	// The namespace allows the host path, and says so to anybody who looks.
	labels := objects.namespace.Labels
	if labels["pod-security.kubernetes.io/enforce"] != "privileged" || labels["pod-security.kubernetes.io/warn"] != "restricted" {
		t.Errorf("the collector's namespace is labelled %v", labels)
	}
	if objects.namespace.Name == "skifity-system" || objects.namespace.Name != Namespace {
		t.Errorf("the collector runs in %s", objects.namespace.Name)
	}
}

// The collector reaches DNS, the Kubernetes API and the world outside the
// cluster, and nothing inside it: not the panel, not another team's app.
func TestTheCollectorCannotReachTheCluster(t *testing.T) {
	objects, _ := renderedObjects(t)
	spec := objects.policy.Spec
	if len(spec.Ingress) != 0 || !slices.Contains(spec.PolicyTypes, networkingv1.PolicyTypeIngress) {
		t.Error("something may connect to the collector")
	}
	var world *networkingv1.IPBlock
	for _, rule := range spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil && peer.IPBlock.CIDR == "0.0.0.0/0" {
				world = peer.IPBlock
			}
			if peer.IPBlock != nil && peer.IPBlock.CIDR == "10.43.0.1/32" && len(rule.Ports) != 1 {
				t.Error("the Kubernetes API is reachable on more than its port")
			}
		}
	}
	if world == nil {
		t.Fatal("the collector cannot reach any service at all")
	}
	for _, refused := range []string{"10.42.0.0/16", "10.43.0.0/16", "169.254.0.0/16"} {
		if !slices.Contains(world.Except, refused) {
			t.Errorf("the collector may reach %s", refused)
		}
	}
}

// Taking the collector away removes everything it was given, the collector
// first, and leaves no credential behind.
func TestRemovalTakesEverythingItMade(t *testing.T) {
	objects, _ := renderedObjects(t)
	made := map[string]bool{}
	for _, kind := range objects.order {
		if kind != "Namespace" {
			made[kind] = true
		}
	}
	removed := map[string]bool{}
	for _, removal := range Removals() {
		removed[removal.Kind] = true
		if removal.Name != CollectorName {
			t.Errorf("removal names %s", removal.Name)
		}
	}
	if !reflect.DeepEqual(made, removed) {
		t.Errorf("the collector makes %v and removal takes %v", made, removed)
	}
	if Removals()[0].Kind != "DaemonSet" {
		t.Error("the collector is not the first thing removed")
	}
}
