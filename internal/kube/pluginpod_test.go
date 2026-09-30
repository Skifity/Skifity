package kube

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

func pluginParts(t *testing.T, objects []any) (*corev1.Namespace, *appsv1.Deployment, *corev1.Service, []*networkingv1.NetworkPolicy) {
	t.Helper()
	var ns *corev1.Namespace
	var deployment *appsv1.Deployment
	var service *corev1.Service
	var policies []*networkingv1.NetworkPolicy
	for _, object := range objects {
		switch typed := object.(type) {
		case *corev1.Namespace:
			ns = typed
		case *appsv1.Deployment:
			deployment = typed
		case *corev1.Service:
			service = typed
		case *networkingv1.NetworkPolicy:
			policies = append(policies, typed)
		default:
			t.Fatalf("a plugin rendered an object nothing expects: %T", object)
		}
	}
	if ns == nil || deployment == nil || service == nil || len(policies) != 2 {
		t.Fatalf("a plugin needs a namespace, a Deployment, a Service and two policies; got %d policies", len(policies))
	}
	return ns, deployment, service, policies
}

func samplePlugin() PluginSpec {
	return PluginSpec{
		ID:   "com.example.backup-to-b2",
		Name: "Backup to Backblaze B2",
		Image: "ghcr.io/example/skifity-b2@sha256:" +
			"0000000000000000000000000000000000000000000000000000000000000000",
		SecretName: "plugin", Port: 8080, Health: "/healthz", MemoryMB: 64,
		SystemNamespace: "skifity-system",
	}
}

// A plugin is somebody else's code running beside the panel. What it holds and
// what it can reach are the two things worth a test.
func TestAPluginHoldsNoKubernetesCredentials(t *testing.T) {
	_, deployment, _, _ := pluginParts(t, BuildPluginObjects(samplePlugin()))
	pod := deployment.Spec.Template.Spec

	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("a plugin must not be given a Kubernetes API token: its own token is what it asked for")
	}
	container := pod.Containers[0]
	sc := container.SecurityContext
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("a plugin must run as a non-root user")
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("a plugin must run with a read-only root filesystem")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("a plugin must not be able to gain privileges it did not start with")
	}
	// The uid is deliberately not pinned: a plugin author controls their own
	// image and declares its USER, and overriding it would be the guess that
	// was wrong for almost every catalogue image.
	if pod.SecurityContext.RunAsUser != nil {
		t.Errorf("a plugin's image was pinned to uid %d", *pod.SecurityContext.RunAsUser)
	}
}

// Its namespace enforces the strict profile, which is a requirement a plugin
// author can meet — unlike an off-the-shelf application image, they control the
// Dockerfile.
func TestAPluginsNamespaceIsStrict(t *testing.T) {
	ns, _, _, _ := pluginParts(t, BuildPluginObjects(samplePlugin()))
	for _, key := range []string{"enforce", "audit", "warn"} {
		if got := ns.Labels["pod-security.kubernetes.io/"+key]; got != "restricted" {
			t.Errorf("%s = %q, want restricted", key, got)
		}
	}
}

// What a plugin can reach is the whole of its blast radius.
func TestAPluginCanReachThePanelDNSAndTheInternetAndNothingElse(t *testing.T) {
	_, _, _, policies := pluginParts(t, BuildPluginObjects(samplePlugin()))

	var deny, allow *networkingv1.NetworkPolicy
	for _, policy := range policies {
		if policy.Name == "default-deny" {
			deny = policy
		} else {
			allow = policy
		}
	}
	if deny == nil || allow == nil {
		t.Fatal("a plugin namespace needs a deny-all and an allow policy")
	}
	if len(deny.Spec.Ingress) != 0 || len(deny.Spec.Egress) != 0 {
		t.Error("the deny-all policy allows something")
	}

	// Only the panel may reach a plugin: an app must not be able to call a
	// plugin's endpoint directly and skip whatever the panel checks first.
	if len(allow.Spec.Ingress) != 1 || len(allow.Spec.Ingress[0].From) != 1 {
		t.Fatalf("ingress = %+v, want only the panel", allow.Spec.Ingress)
	}
	from := allow.Spec.Ingress[0].From[0].NamespaceSelector
	if from == nil || from.MatchLabels["kubernetes.io/metadata.name"] != "skifity-system" {
		t.Errorf("a plugin accepts traffic from %+v, want only the panel's namespace", from)
	}

	// And the one egress rule that reaches outward must exclude the private
	// ranges and the metadata address.
	var internet *networkingv1.IPBlock
	for _, rule := range allow.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				internet = peer.IPBlock
			}
		}
	}
	if internet == nil {
		t.Fatal("a plugin cannot reach the internet at all, which is what most plugins are for")
	}
	for _, blocked := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.169.254/32"} {
		found := false
		for _, except := range internet.Except {
			if except == blocked {
				found = true
			}
		}
		if !found {
			t.Errorf("a plugin can reach %s, which is another namespace or the cloud metadata service", blocked)
		}
	}
}

// Two plugins must not land in one namespace, and a long id must still produce
// a name Kubernetes accepts.
func TestEachPluginGetsItsOwnNamespace(t *testing.T) {
	first := PluginNamespace("com.example.backups")
	second := PluginNamespace("com.example.deploys")
	if first == second {
		t.Fatalf("two plugins share the namespace %q", first)
	}
	if !strings.HasPrefix(first, PluginNamespacePrefix) {
		t.Errorf("%q does not say what it is", first)
	}

	long := PluginNamespace("com.a-very-long-organisation-name.with-a-very-long-plugin-name-indeed.v2")
	if len(long) > 63 {
		t.Errorf("%q is %d characters; Kubernetes accepts 63", long, len(long))
	}
	if !ValidLabel(long) {
		t.Errorf("%q is not a usable Kubernetes name", long)
	}
	// Two ids that shorten the same way must still differ.
	other := PluginNamespace("com.a-very-long-organisation-name.with-a-very-long-plugin-name-indeed.v3")
	if long == other {
		t.Errorf("two long ids collided on %q", long)
	}
}

// A manifest asking for four gigabytes gets a sensible number instead.
func TestMemoryIsBounded(t *testing.T) {
	for _, tc := range []struct{ asked, want int }{
		{0, PluginMinMemoryMB},
		{16, PluginMinMemoryMB},
		{64, 64},
		{4096, PluginMaxMemoryMB},
	} {
		spec := samplePlugin()
		spec.MemoryMB = tc.asked
		_, deployment, _, _ := pluginParts(t, BuildPluginObjects(spec))
		limit := deployment.Spec.Template.Spec.Containers[0].Resources.Limits.Memory()
		want := int64(tc.want) * 1024 * 1024
		if limit.Value() != want {
			t.Errorf("asking for %d MB gave a limit of %d bytes, want %d", tc.asked, limit.Value(), want)
		}
	}
}

// The address the panel posts to has to be the Service that was rendered.
func TestThePanelKnowsWhereToPost(t *testing.T) {
	spec := samplePlugin()
	_, _, service, _ := pluginParts(t, BuildPluginObjects(spec))
	url := PluginServiceURL(spec.ID, spec.Port)

	if !strings.Contains(url, service.Name+"."+PluginNamespace(spec.ID)) {
		t.Errorf("the panel posts to %q and the Service is %s/%s",
			url, PluginNamespace(spec.ID), service.Name)
	}
	if !strings.HasSuffix(url, ":8080") {
		t.Errorf("the panel posts to %q and the Service listens on %d", url, spec.Port)
	}
	// A manifest that named no port still has to produce a working address.
	if got := PluginServiceURL(spec.ID, 0); !strings.HasSuffix(got, ":8080") {
		t.Errorf("with no port declared the address is %q", got)
	}
}
