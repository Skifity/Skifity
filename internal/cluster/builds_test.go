package cluster

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

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
	policies := buildNetworkPolicies(kube.BuildsNamespace, "skifity-system")
	if len(policies) != 2 {
		t.Fatalf("got %d policies, want a deny-all and an allow", len(policies))
	}
	if len(policies[0].Spec.Ingress) != 0 || len(policies[0].Spec.Egress) != 0 {
		t.Fatal("the first policy is not a deny-all baseline")
	}

	var sawInternet bool
	for _, rule := range policies[1].Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock == nil || peer.IPBlock.CIDR != "0.0.0.0/0" {
				continue
			}
			sawInternet = true
			except := strings.Join(peer.IPBlock.Except, ",")
			// The metadata service hands out the provider's credentials to
			// anything that asks, and a build is the last thing that should.
			for _, blocked := range []string{"169.254.169.254/32", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
				if !strings.Contains(except, blocked) {
					t.Errorf("a build can reach %s", blocked)
				}
			}
		}
	}
	if !sawInternet {
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
