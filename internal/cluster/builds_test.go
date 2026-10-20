package cluster

import (
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"skifity/internal/kube"
)

// TestBuildsAreNotInThePanelsNamespace: a build runs code out of somebody's
// repository, and the panel's namespace holds the master key, the database and
// a service account that can reach the whole cluster.
func TestBuildsAreNotInThePanelsNamespace(t *testing.T) {
	if kube.BuildsNamespace == "skifity-system" || kube.BuildsNamespace == "" {
		t.Fatalf("builds run in %q", kube.BuildsNamespace)
	}
}

func policyNamed(t *testing.T, policies []*networkingv1.NetworkPolicy, name string) *networkingv1.NetworkPolicy {
	t.Helper()
	for _, policy := range policies {
		if policy.Name == name {
			return policy
		}
	}
	t.Fatalf("there is no %s policy", name)
	return nil
}

// TestBuildNetworkPolicyLetsBuildsOutButNotIn: a build needs the repository,
// the packages and the registry, and nothing else on the node network — not
// another build, and nothing comes in to it.
func TestBuildNetworkPolicyLetsBuildsOutButNotIn(t *testing.T) {
	policies := buildNetworkPolicies(kube.BuildsNamespace, "skifity-system", []string{"203.0.113.10"}, []string{"10.42.1.0"})
	deny := policyNamed(t, policies, "default-deny")
	if len(deny.Spec.Ingress) != 0 || len(deny.Spec.Egress) != 0 {
		t.Fatal("the first policy is not a deny-all baseline")
	}
	allow := policyNamed(t, policies, "allow-builds")

	// The metadata service hands out the provider's credentials to anything
	// that asks, and a build is the last thing that should; the registry's
	// NodePort and the Kubernetes API are on the node's own address, which a
	// build has no business reaching either.
	for _, blocked := range [][2]any{
		{"169.254.169.254", 80}, {"10.0.0.5", 80}, {"172.16.0.1", 80}, {"192.168.0.1", 80},
		{"203.0.113.10", 30500}, {"203.0.113.10", 6443}, {"203.0.113.10", 10250},
	} {
		if kube.EgressAllows(allow.Spec.Egress, blocked[0].(string), blocked[1].(int)) {
			t.Errorf("a build can reach %s:%d", blocked[0], blocked[1])
		}
	}
	if !kube.EgressAllows(allow.Spec.Egress, "140.82.121.4", 443) {
		t.Fatal("a build cannot reach the internet, so it cannot clone anything")
	}

	// Inside the namespace, the registry's guard on its port and nothing else:
	// no other build, and not the registry's own listener behind the guard.
	var toPods int
	for _, rule := range allow.Spec.Egress {
		for _, peer := range rule.To {
			if peer.PodSelector == nil {
				continue
			}
			toPods++
			if peer.PodSelector.MatchLabels["app.kubernetes.io/name"] != RegistryService {
				t.Errorf("a build can reach the pods %+v", peer.PodSelector)
			}
			if len(rule.Ports) != 1 || rule.Ports[0].Port.IntValue() != RegistryPort {
				t.Errorf("a build can reach the registry's pod on %+v", rule.Ports)
			}
		}
	}
	if toPods != 1 {
		t.Errorf("a build reaches %d kinds of pod, want the registry alone", toPods)
	}
	if len(allow.Spec.Ingress) != 0 || !slices.Contains(allow.Spec.PolicyTypes, networkingv1.PolicyTypeIngress) {
		t.Error("something may connect to a build")
	}
}

// TestEveryServerCanPullFromTheRegistry: a server's container runtime pulls
// through the registry's NodePort on itself, and a pull on a server the
// registry does not run on arrives from that server's pod-network gateway. The
// registry's policy has to let it in, or a second server can run nothing the
// panel built.
func TestEveryServerCanPullFromTheRegistry(t *testing.T) {
	policies := buildNetworkPolicies(kube.BuildsNamespace, "skifity-system",
		[]string{"203.0.113.10", "10.0.0.2", "2001:db8::1"}, []string{"10.42.0.0", "10.42.1.0"})
	registry := policyNamed(t, policies, "allow-registry")
	if registry.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"] != RegistryService {
		t.Fatalf("the registry's policy selects %+v", registry.Spec.PodSelector)
	}
	if len(registry.Spec.Ingress) != 1 || len(registry.Spec.Ingress[0].Ports) != 1 ||
		registry.Spec.Ingress[0].Ports[0].Port.IntValue() != RegistryPort {
		t.Fatalf("the registry is let in on %+v", registry.Spec.Ingress)
	}
	var blocks []string
	var panel, builds bool
	for _, peer := range registry.Spec.Ingress[0].From {
		switch {
		case peer.IPBlock != nil:
			blocks = append(blocks, peer.IPBlock.CIDR)
		case peer.NamespaceSelector != nil:
			panel = peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] == "skifity-system"
		case peer.PodSelector != nil:
			builds = len(peer.PodSelector.MatchLabels) == 0
		}
	}
	for _, want := range []string{"203.0.113.10/32", "10.0.0.2/32", "10.42.0.0/32", "10.42.1.0/32"} {
		if !slices.Contains(blocks, want) {
			t.Errorf("a pull from %s is not let in: %v", want, blocks)
		}
	}
	for _, block := range blocks {
		if strings.Contains(block, ":") || !strings.HasSuffix(block, "/32") {
			t.Errorf("the registry lets in %s", block)
		}
	}
	if !panel || !builds {
		t.Errorf("the panel (%v) or the builds beside it (%v) cannot reach the registry", panel, builds)
	}
}

func registryParts(t *testing.T) (*appsv1.Deployment, *corev1.Service) {
	t.Helper()
	var deployment *appsv1.Deployment
	var service *corev1.Service
	for _, object := range registryObjects(kube.BuildsNamespace, "ghcr.io/skifity/skifity:v0.1.1") {
		switch typed := object.(type) {
		case *appsv1.Deployment:
			deployment = typed
		case *corev1.Service:
			service = typed
		}
	}
	if deployment == nil || service == nil {
		t.Fatal("the registry has no Deployment or no Service")
	}
	return deployment, service
}

// TestRegistryIsReachableByEveryNodesContainerRuntime is the bug this shape
// exists for: an image pushed to a Service name that containerd cannot resolve
// is an image no node can ever pull.
func TestRegistryIsReachableByEveryNodesContainerRuntime(t *testing.T) {
	_, service := registryParts(t)
	if service.Spec.Type != corev1.ServiceTypeNodePort {
		t.Fatalf("the registry Service is %s; a node's container runtime cannot reach a ClusterIP by name",
			service.Spec.Type)
	}
	if got := service.Spec.Ports[0].NodePort; got != kube.RegistryNodePort {
		t.Fatalf("the registry is on node port %d, but every node is configured for %d",
			got, kube.RegistryNodePort)
	}
}

// The registry holds every image the cluster has built, and its volume keeps
// it on the server it first started on. It prefers one that runs the control
// plane, the servers a cluster keeps, over a worker, which is the kind added
// and taken away; preferred, so a cluster whose control plane takes no work
// still gets one.
func TestTheRegistryPrefersAServerThatRunsTheControlPlane(t *testing.T) {
	deployment, _ := registryParts(t)
	affinity := deployment.Spec.Template.Spec.Affinity
	if affinity == nil || affinity.NodeAffinity == nil {
		t.Fatal("the registry goes on whichever server the scheduler picks first")
	}
	if affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		t.Error("the registry requires a server, and one whose control plane takes no work would have none")
	}
	found := false
	for _, term := range affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
		for _, expression := range term.Preference.MatchExpressions {
			if expression.Key == "node-role.kubernetes.io/control-plane" && expression.Operator == corev1.NodeSelectorOpExists {
				found = true
			}
		}
	}
	if !found {
		t.Error("the registry does not prefer a server that runs the control plane")
	}
}

// TestTheRegistryIsOnlyReachedThroughItsGuard: the guard is what refuses a
// write without a token, so nothing may reach the registry around it.
func TestTheRegistryIsOnlyReachedThroughItsGuard(t *testing.T) {
	deployment, service := registryParts(t)
	pod := deployment.Spec.Template.Spec
	var registry, guard *corev1.Container
	for i := range pod.Containers {
		switch pod.Containers[i].Name {
		case "registry":
			registry = &pod.Containers[i]
		case registryGuardContainer:
			guard = &pod.Containers[i]
		}
	}
	if registry == nil || guard == nil {
		t.Fatalf("the registry's pod has %d containers and not the two it needs", len(pod.Containers))
	}

	env := func(c *corev1.Container, name string) string {
		for _, e := range c.Env {
			if e.Name == name {
				return e.Value
			}
		}
		return ""
	}
	for _, name := range []string{"REGISTRY_HTTP_ADDR", "REGISTRY_HTTP_DEBUG_ADDR"} {
		if got := env(registry, name); !strings.HasPrefix(got, "127.0.0.1:") {
			t.Errorf("%s is %q, where the pod's other address reaches it past the guard", name, got)
		}
	}
	// The image's own configuration starts the debug server on 5001.
	if env(registry, "REGISTRY_HTTP_ADDR") == env(registry, "REGISTRY_HTTP_DEBUG_ADDR") ||
		strings.HasSuffix(env(registry, "REGISTRY_HTTP_ADDR"), ":5001") {
		t.Errorf("the registry and its debug server would both listen on %s", env(registry, "REGISTRY_HTTP_ADDR"))
	}
	if len(registry.Ports) != 0 {
		t.Errorf("the registry names a port of its own: %+v", registry.Ports)
	}
	if env(registry, "REGISTRY_HTTP_RELATIVEURLS") != "true" {
		t.Error("the registry would tell a client to continue an upload at its own address, past the guard")
	}
	if got := env(guard, "SKIFITY_REGISTRY_UPSTREAM"); got != "http://"+env(registry, "REGISTRY_HTTP_ADDR") {
		t.Errorf("the guard forwards to %q, and the registry listens on %q", got, env(registry, "REGISTRY_HTTP_ADDR"))
	}
	if strings.Join(guard.Command, " ") != "/usr/local/bin/skifity registry-guard" {
		t.Errorf("the guard runs %v", guard.Command)
	}
	if len(guard.Ports) != 1 || guard.Ports[0].ContainerPort != RegistryPort || guard.Ports[0].Name != "http" {
		t.Errorf("the guard listens on %+v", guard.Ports)
	}
	if service.Spec.Ports[0].TargetPort.StrVal != "http" {
		t.Errorf("the Service sends to %v, not the guard's port", service.Spec.Ports[0].TargetPort)
	}

	// The key is the guard's alone.
	for _, mount := range registry.VolumeMounts {
		if mount.Name == "key" {
			t.Error("the registry's container can read the key tokens are signed with")
		}
	}
	var keyMounted bool
	for _, mount := range guard.VolumeMounts {
		keyMounted = keyMounted || (mount.Name == "key" && mount.ReadOnly)
	}
	if !keyMounted {
		t.Error("the guard has no key to check tokens against")
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("the registry's pod is given a Kubernetes token it has no use for")
	}
	sc := guard.SecurityContext
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("the guard is not confined")
	}
}

// TestTheSharedBuilderIsRetired: every build runs its own BuildKit now, and
// the one every team shared is removed from a cluster that has it.
func TestTheSharedBuilderIsRetired(t *testing.T) {
	shared := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: legacyBuilder, Namespace: kube.BuildsNamespace}}
	c, _, calls := policyCluster(t, shared)
	if err := c.RetireSharedBuilder(t.Context()); err != nil {
		t.Fatal(err)
	}
	calls.mu.Lock()
	deleted := append([]string{}, calls.deleted...)
	calls.mu.Unlock()
	if !slices.Contains(deleted, "deployments/"+legacyBuilder) || !slices.Contains(deleted, "services/"+legacyBuilder) {
		t.Fatalf("the shared builder was not removed: %v", deleted)
	}

	// Gone already: nothing to do, and no error.
	empty, _, calls := policyCluster(t)
	if err := empty.RetireSharedBuilder(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(calls.deleted) != 0 {
		t.Errorf("a cluster with no shared builder had %v deleted", calls.deleted)
	}
}

// TestARegistryOutOfStepIsPutRight: a registry installed before it had a
// guard, or with an older panel's, or not there at all, is applied again — but
// only while no build holds the registry, because applying it restarts it.
func TestARegistryOutOfStepIsPutRight(t *testing.T) {
	panel := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "skifity-panel", Namespace: "skifity-system"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "panel", Image: "ghcr.io/skifity/skifity:v0.1.1"}},
		}}},
	}
	registry := func(guard string) *appsv1.Deployment {
		containers := []corev1.Container{{Name: "registry", Image: registryImage}}
		if guard != "" {
			containers = append(containers, corev1.Container{Name: registryGuardContainer, Image: guard})
		}
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: RegistryService, Namespace: kube.BuildsNamespace},
			Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: containers}}},
		}
	}
	for name, c := range map[string]struct {
		objects []runtime.Object
		inStep  bool
	}{
		"the panel's own guard": {[]runtime.Object{panel, registry("ghcr.io/skifity/skifity:v0.1.1")}, true},
		"an older panel's":      {[]runtime.Object{panel, registry("ghcr.io/skifity/skifity:v0.1.0")}, false},
		"no guard at all":       {[]runtime.Object{panel, registry("")}, false},
		"no registry at all":    {[]runtime.Object{panel}, false},
	} {
		cluster, _, _ := policyCluster(t, c.objects...)
		got, err := cluster.registryInStep(t.Context())
		if err != nil || got != c.inStep {
			t.Errorf("%s: in step = %v, %v; want %v", name, got, err, c.inStep)
		}
	}

	// A build holding the registry keeps it from being restarted under it.
	cluster, _, calls := policyCluster(t, panel, registry(""))
	release := cluster.BeginBuild()
	if err := cluster.ReconcileRegistry(t.Context()); err != nil {
		t.Fatal(err)
	}
	release()
	calls.mu.Lock()
	defer calls.mu.Unlock()
	if len(calls.applied) != 0 {
		t.Errorf("the registry was applied with a build running: %d objects", len(calls.applied))
	}
}
