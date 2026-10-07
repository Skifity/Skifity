package cluster

import (
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

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

// TestBuildNetworkPolicyLetsBuildsOutButNotIn: a build needs the repository
// and the packages, and nothing else on the node network.
func TestBuildNetworkPolicyLetsBuildsOutButNotIn(t *testing.T) {
	policies := buildNetworkPolicies(kube.BuildsNamespace, "skifity-system", []string{"203.0.113.10"})
	if len(policies) != 2 {
		t.Fatalf("got %d policies, want a deny-all and an allow", len(policies))
	}
	if len(policies[0].Spec.Ingress) != 0 || len(policies[0].Spec.Egress) != 0 {
		t.Fatal("the first policy is not a deny-all baseline")
	}

	// The metadata service hands out the provider's credentials to anything
	// that asks, and a build is the last thing that should; the registry's
	// NodePort and the Kubernetes API are on the node's own address, which a
	// build has no business reaching either.
	for _, blocked := range [][2]any{
		{"169.254.169.254", 80}, {"10.0.0.5", 80}, {"172.16.0.1", 80}, {"192.168.0.1", 80},
		{"203.0.113.10", 30500}, {"203.0.113.10", 6443}, {"203.0.113.10", 10250},
	} {
		if kube.EgressAllows(policies[1].Spec.Egress, blocked[0].(string), blocked[1].(int)) {
			t.Errorf("a build can reach %s:%d", blocked[0], blocked[1])
		}
	}
	if !kube.EgressAllows(policies[1].Spec.Egress, "140.82.121.4", 443) {
		t.Fatal("a build cannot reach the internet, so it cannot clone anything")
	}
}

// TestRegistryIsReachableByEveryNodesContainerRuntime is the bug this shape
// exists for: an image pushed to a Service name that containerd cannot resolve
// is an image no node can ever pull.
func TestRegistryIsReachableByEveryNodesContainerRuntime(t *testing.T) {
	var service *corev1.Service
	for _, object := range registryObjects(kube.BuildsNamespace) {
		if s, ok := object.(*corev1.Service); ok {
			service = s
		}
	}
	if service == nil {
		t.Fatal("the registry has no Service")
	}
	if service.Spec.Type != corev1.ServiceTypeNodePort {
		t.Fatalf("the registry Service is %s; a node's container runtime cannot reach a ClusterIP by name",
			service.Spec.Type)
	}
	if got := service.Spec.Ports[0].NodePort; got != kube.RegistryNodePort {
		t.Fatalf("the registry is on node port %d, but every node is configured for %d",
			got, kube.RegistryNodePort)
	}
}

// TestBuildKitProbeKnowsWhereBuildKitIs: buildkitd is started with --addr,
// which replaces the default socket rather than adding to it, so a bare
// `buildctl debug workers` looks for a socket that does not exist and the
// builder never becomes ready.
func TestBuildKitProbeKnowsWhereBuildKitIs(t *testing.T) {
	var deployment *appsv1.Deployment
	for _, object := range buildKitObjects(kube.BuildsNamespace) {
		if d, ok := object.(*appsv1.Deployment); ok {
			deployment = d
		}
	}
	if deployment == nil {
		t.Fatal("BuildKit has no Deployment")
	}
	container := deployment.Spec.Template.Spec.Containers[0]

	probe := container.ReadinessProbe
	if probe == nil || probe.Exec == nil {
		t.Fatal("BuildKit has no readiness probe")
	}
	command := strings.Join(probe.Exec.Command, " ")
	if !strings.Contains(command, "--addr") {
		t.Fatalf("the probe does not say where buildkitd is: %q", command)
	}
	// The address it probes has to be the one buildkitd was told to listen on.
	var listen string
	for i, arg := range container.Args {
		if arg == "--addr" && i+1 < len(container.Args) {
			listen = container.Args[i+1]
		}
	}
	if listen == "" {
		t.Fatal("buildkitd is not told where to listen")
	}
	if !strings.HasSuffix(listen, buildKitLocalAddress[strings.LastIndexByte(buildKitLocalAddress, ':'):]) {
		t.Fatalf("the probe uses %q and buildkitd listens on %q", buildKitLocalAddress, listen)
	}

	// A moving tag means a builder that changes under an operator, and a build
	// that breaks for no reason they can see.
	if strings.HasSuffix(container.Image, ":latest") || strings.Contains(container.Image, ":master") {
		t.Errorf("BuildKit is pinned to a moving tag: %q", container.Image)
	}
}

// TestTheSharedBuilderCannotFillTheNodesDisk: one BuildKit serves every team and
// keeps its cache on the node's disk. Left alone the cache grows until the disk
// is full, and a full disk is the kubelet evicting pods from the whole machine.
func TestTheSharedBuilderCannotFillTheNodesDisk(t *testing.T) {
	var deployment *appsv1.Deployment
	for _, object := range buildKitObjects(kube.BuildsNamespace) {
		if d, ok := object.(*appsv1.Deployment); ok {
			deployment = d
		}
	}
	if deployment == nil {
		t.Fatal("BuildKit has no Deployment")
	}
	pod := deployment.Spec.Template.Spec
	args := strings.Join(pod.Containers[0].Args, " ")
	if !strings.Contains(args, "--oci-worker-gc-keepstorage "+fmt.Sprint(BuildKitKeepStorageMB)) {
		t.Errorf("BuildKit is not told how much cache to keep: %s", args)
	}
	for _, volume := range pod.Volumes {
		if volume.Name == "cache" && (volume.EmptyDir == nil || volume.EmptyDir.SizeLimit == nil) {
			t.Error("the builder's cache has no ceiling")
		}
	}
	if pod.Containers[0].Resources.Limits.StorageEphemeral().IsZero() {
		t.Error("the builder has no ephemeral-storage limit")
	}
	// The collector is what keeps the cache down; the ceiling is for a runaway.
	keep := resource.MustParse(fmt.Sprintf("%dM", BuildKitKeepStorageMB))
	ceiling := resource.MustParse(BuildKitCacheLimit)
	if ceiling.Cmp(keep) <= 0 {
		t.Errorf("the cache ceiling %s is not above what the collector keeps (%s), so it would be evicted in normal use",
			BuildKitCacheLimit, keep.String())
	}
}
