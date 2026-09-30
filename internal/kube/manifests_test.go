package kube

import (
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"skifity/internal/version"
)

func baseSpec() AppSpec {
	return AppSpec{
		Name:         "web",
		Namespace:    "acme-shop-production",
		AppID:        "app_123",
		ProjectID:    "prj_123",
		TeamID:       "team_123",
		Environment:  "production",
		DisplayName:  "Web",
		Image:        "registry.internal/acme/web:abc123",
		Port:         3000,
		HealthPath:   "/healthz",
		Replicas:     1,
		CPURequestM:  50,
		CPULimitM:    1000,
		MemRequestMB: 128,
		MemLimitMB:   512,
	}
}

func TestSpecValidate(t *testing.T) {
	if err := baseSpec().Validate(); err != nil {
		t.Fatalf("a valid spec was rejected: %v", err)
	}

	cases := map[string]func(*AppSpec){
		"bad name":            func(s *AppSpec) { s.Name = "Not A Name" },
		"bad namespace":       func(s *AppSpec) { s.Namespace = "UPPER" },
		"no image":            func(s *AppSpec) { s.Image = "" },
		"port out of range":   func(s *AppSpec) { s.Port = 70000 },
		"cpu limit below req": func(s *AppSpec) { s.CPULimitM = 10 },
		"mem limit below req": func(s *AppSpec) { s.MemLimitMB = 10 },
		"autoscale no target": func(s *AppSpec) { s.Autoscale = true; s.MinReplicas = 1; s.MaxReplicas = 3 },
		"autoscale bad range": func(s *AppSpec) {
			s.Autoscale, s.MinReplicas, s.MaxReplicas, s.CPUTarget = true, 5, 2, 70
		},
		"relative mount": func(s *AppSpec) {
			s.Volumes = []VolumeSpec{{Name: "data", MountPath: "data", SizeGB: 1}}
		},
		"duplicate mount": func(s *AppSpec) {
			s.Volumes = []VolumeSpec{
				{Name: "a", MountPath: "/data", SizeGB: 1},
				{Name: "b", MountPath: "/data", SizeGB: 1},
			}
		},
		"bad hostname": func(s *AppSpec) {
			s.Domains = []DomainSpec{{Hostname: "not a host", TLS: true}}
		},
		"duplicate hostname": func(s *AppSpec) {
			s.Domains = []DomainSpec{
				{Hostname: "a.example.com", TLS: true},
				{Hostname: "a.example.com", TLS: true},
			}
		},
	}
	for name, mutate := range cases {
		s := baseSpec()
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: Validate accepted an invalid spec", name)
		}
	}
}

func TestDeploymentDefaultsAreSafe(t *testing.T) {
	d := BuildDeployment(baseSpec())

	if *d.Spec.Replicas != 1 {
		t.Fatalf("replicas = %d, want 1", *d.Spec.Replicas)
	}
	container := d.Spec.Template.Spec.Containers[0]

	// Health checks: an app without them is restarted blindly and routed to
	// before it is ready.
	if container.ReadinessProbe == nil || container.LivenessProbe == nil || container.StartupProbe == nil {
		t.Fatal("a container was rendered without all three probes")
	}
	if container.ReadinessProbe.HTTPGet == nil || container.ReadinessProbe.HTTPGet.Path != "/healthz" {
		t.Fatalf("readiness probe does not use the configured health path: %+v", container.ReadinessProbe)
	}

	// Resource limits: without a memory limit one app can take down a node.
	if container.Resources.Limits.Memory().IsZero() {
		t.Fatal("no memory limit was set")
	}
	if container.Resources.Requests.Cpu().IsZero() {
		t.Fatal("no CPU reservation was set")
	}

	// Security context.
	sc := container.SecurityContext
	if sc == nil || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Fatal("privilege escalation is not blocked")
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Fatal("the container is allowed to run as root")
	}
	if len(sc.Capabilities.Drop) == 0 || sc.Capabilities.Drop[0] != "ALL" {
		t.Fatal("capabilities are not dropped")
	}
	if d.Spec.Template.Spec.AutomountServiceAccountToken == nil ||
		*d.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Fatal("the Kubernetes API token is mounted into the app, which it never needs")
	}

	// PORT is what almost every framework reads.
	var sawPort bool
	for _, e := range container.Env {
		if e.Name == "PORT" && e.Value == "3000" {
			sawPort = true
		}
	}
	if !sawPort {
		t.Fatal("PORT was not set from the configured port")
	}
}

func TestDeploymentSelectorIsStableAcrossDeploys(t *testing.T) {
	// A Deployment's selector is immutable. If anything that changes per deploy
	// leaks into it, every second deploy fails with an error nobody can act on.
	first := baseSpec()
	first.DeploymentID = "dep_1"
	first.Revision = "rev-1"
	second := baseSpec()
	second.DeploymentID = "dep_2"
	second.Revision = "rev-2"

	a := BuildDeployment(first).Spec.Selector.MatchLabels
	b := BuildDeployment(second).Spec.Selector.MatchLabels
	if len(a) != len(b) {
		t.Fatalf("selector changed size between deploys: %v then %v", a, b)
	}
	for k, v := range a {
		if b[k] != v {
			t.Fatalf("selector label %q changed between deploys: %q then %q", k, v, b[k])
		}
	}

	// The pod template annotations must differ, or the rollout is a no-op.
	pa := BuildDeployment(first).Spec.Template.Annotations
	pb := BuildDeployment(second).Spec.Template.Annotations
	if pa["skifity.com/revision"] == pb["skifity.com/revision"] {
		t.Fatal("the pod template did not change between deploys, so no rollout would happen")
	}
}

func TestVolumesForceRecreateStrategy(t *testing.T) {
	// Two instances writing the same ReadWriteOnce volume corrupts it, and a
	// rolling update would briefly run both.
	s := baseSpec()
	s.Volumes = []VolumeSpec{{Name: "data", MountPath: "/data", SizeGB: 5}}
	d := BuildDeployment(s)
	if d.Spec.Strategy.Type != "Recreate" {
		t.Fatalf("strategy is %s for an app with a volume, want Recreate", d.Spec.Strategy.Type)
	}

	claims := BuildPVCs(s)
	if len(claims) != 1 {
		t.Fatalf("got %d claims, want 1", len(claims))
	}
	if claims[0].Name != "web-data" {
		t.Fatalf("claim name %q, want web-data", claims[0].Name)
	}
	if got := claims[0].Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "5Gi" {
		t.Fatalf("claim size %s, want 5Gi", got.String())
	}
	// The pod must actually mount the claim it asks for.
	mounted := d.Spec.Template.Spec.Containers[0].VolumeMounts
	if len(mounted) != 1 || mounted[0].MountPath != "/data" {
		t.Fatalf("volume mounts are %+v", mounted)
	}
	if d.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "web-data" {
		t.Fatal("the pod references a claim that is not the one rendered")
	}
}

func TestRollingUpdateKeepsCapacity(t *testing.T) {
	s := baseSpec()
	s.Replicas = 3
	d := BuildDeployment(s)
	if d.Spec.Strategy.Type != "RollingUpdate" {
		t.Fatalf("strategy is %s, want RollingUpdate", d.Spec.Strategy.Type)
	}
	// MaxUnavailable 0 keeps the capacity.
	if d.Spec.Strategy.RollingUpdate.MaxUnavailable.IntValue() != 0 {
		t.Fatalf("maxUnavailable is %v, which allows a gap in capacity during a deploy",
			d.Spec.Strategy.RollingUpdate.MaxUnavailable)
	}

	// And on its own it is not enough. A pod is removed from the Service and
	// told to stop at the same moment, and the ingress controller learns about
	// the first through a watch — so it keeps sending requests to a process
	// that has started shutting down. Without a pause before SIGTERM, every
	// rolling update drops a handful of requests, which is exactly the thing
	// nobody can reproduce afterwards.
	lifecycle := d.Spec.Template.Spec.Containers[0].Lifecycle
	if lifecycle == nil || lifecycle.PreStop == nil || lifecycle.PreStop.Sleep == nil {
		t.Fatal("no preStop pause: a rolling update drops the requests already on their way")
	}
	grace := *d.Spec.Template.Spec.TerminationGracePeriodSeconds
	if lifecycle.PreStop.Sleep.Seconds >= grace {
		t.Fatalf("the preStop pause is %ds against a %ds grace period, so the app is killed "+
			"before it is asked to stop", lifecycle.PreStop.Sleep.Seconds, grace)
	}
	// A shell command would need a shell, and a distroless image has none.
	if lifecycle.PreStop.Exec != nil {
		t.Fatal("preStop runs a command, which fails on any image without a shell")
	}
}

func TestSpreadAcrossServersToleratesOneNode(t *testing.T) {
	s := baseSpec()
	s.Replicas = 3
	s.SpreadAcrossServers = true
	d := BuildDeployment(s)

	constraints := d.Spec.Template.Spec.TopologySpreadConstraints
	if len(constraints) != 1 {
		t.Fatalf("got %d spread constraints, want 1", len(constraints))
	}
	// DoNotSchedule would leave instances Pending forever on a one-node
	// cluster, which is where most installs start.
	if constraints[0].WhenUnsatisfiable != "ScheduleAnyway" {
		t.Fatalf("spread constraint is %s; on a single node this strands instances in Pending",
			constraints[0].WhenUnsatisfiable)
	}
	if constraints[0].TopologyKey != "kubernetes.io/hostname" {
		t.Fatalf("spreading by %q, want kubernetes.io/hostname", constraints[0].TopologyKey)
	}
}

func TestServiceTargetsNamedPort(t *testing.T) {
	svc := BuildService(baseSpec())
	if svc == nil {
		t.Fatal("no Service was rendered for an app with a port")
	}
	if svc.Spec.Ports[0].Port != 80 {
		t.Fatalf("service port is %d, want 80", svc.Spec.Ports[0].Port)
	}
	// Targeting the named port rather than the number means changing the app's
	// port does not need the Service to be edited in step.
	if svc.Spec.Ports[0].TargetPort.StrVal != "http" {
		t.Fatalf("service targets %v, want the named port http", svc.Spec.Ports[0].TargetPort)
	}
	// An app with no port is a worker and needs no Service.
	s := baseSpec()
	s.Port = 0
	if BuildService(s) != nil {
		t.Fatal("a Service was rendered for an app with no port")
	}
}

func TestIngressRendersTLSAndIssuer(t *testing.T) {
	s := baseSpec()
	s.ClusterIssuer = "letsencrypt"
	s.Domains = []DomainSpec{
		{Hostname: "shop.example.com", TLS: true},
		{Hostname: "internal.example.com", TLS: false},
	}
	ing := BuildIngress(s)
	if ing == nil {
		t.Fatal("no Ingress was rendered for an app with domains")
	}
	if len(ing.Spec.Rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(ing.Spec.Rules))
	}
	if len(ing.Spec.TLS) != 1 || len(ing.Spec.TLS[0].Hosts) != 1 {
		t.Fatalf("TLS section is %+v; only the TLS domain belongs in it", ing.Spec.TLS)
	}
	if ing.Spec.TLS[0].Hosts[0] != "shop.example.com" {
		t.Fatalf("TLS covers %q, want shop.example.com", ing.Spec.TLS[0].Hosts[0])
	}
	if ing.Annotations["cert-manager.io/cluster-issuer"] != "letsencrypt" {
		t.Fatal("the cert-manager issuer annotation is missing, so no certificate would be issued")
	}

	// No domains means no Ingress, which is correct for a worker.
	s.Domains = nil
	if BuildIngress(s) != nil {
		t.Fatal("an Ingress was rendered for an app with no domains")
	}
}

func TestIngressWithoutIssuerSkipsAnnotation(t *testing.T) {
	s := baseSpec()
	s.Domains = []DomainSpec{{Hostname: "shop.example.com", TLS: true}}
	ing := BuildIngress(s)
	if _, ok := ing.Annotations["cert-manager.io/cluster-issuer"]; ok {
		t.Fatal("an issuer annotation was set with no issuer configured, which cert-manager would reject")
	}
}

func TestHPAOnlyWhenAutoscaling(t *testing.T) {
	if BuildHPA(baseSpec()) != nil {
		t.Fatal("an HPA was rendered for an app that is not autoscaling")
	}

	s := baseSpec()
	s.Autoscale, s.MinReplicas, s.MaxReplicas, s.CPUTarget, s.MemoryTarget = true, 2, 10, 75, 80
	hpa := BuildHPA(s)
	if hpa == nil {
		t.Fatal("no HPA was rendered for an autoscaling app")
	}
	if *hpa.Spec.MinReplicas != 2 || hpa.Spec.MaxReplicas != 10 {
		t.Fatalf("HPA range is %d-%d, want 2-10", *hpa.Spec.MinReplicas, hpa.Spec.MaxReplicas)
	}
	if len(hpa.Spec.Metrics) != 2 {
		t.Fatalf("got %d metrics, want CPU and memory", len(hpa.Spec.Metrics))
	}
	// Scaling down faster than up causes flapping under bursty traffic.
	up := *hpa.Spec.Behavior.ScaleUp.StabilizationWindowSeconds
	down := *hpa.Spec.Behavior.ScaleDown.StabilizationWindowSeconds
	if down <= up {
		t.Fatalf("scale-down window (%ds) is not longer than scale-up (%ds), which causes flapping", down, up)
	}

	// And the Deployment must not carry a replica count at all. This test used
	// to assert the opposite — that it was rendered at the minimum — which is
	// what made the bug invisible: see TestAnAutoscalerIsNotArguedWith.
	if d := BuildDeployment(s); d.Spec.Replicas != nil {
		t.Fatalf("an autoscaling Deployment was rendered with %d replicas; the HPA owns that field",
			*d.Spec.Replicas)
	}
}

// TestAnAutoscalerIsNotArguedWith is about the field nobody looks at.
//
// Objects are applied with server-side apply and Force, and an apply happens on
// a deploy, a rollback, a variable change, a domain change and a scaling
// change. Any `replicas` the panel sends is therefore reasserted every time —
// so an app the HPA had taken to six under load dropped back to its minimum the
// moment somebody edited a variable, and a sleeping app was forced awake.
// Omitting the field is what Kubernetes documents for this case.
func TestAnAutoscalerIsNotArguedWith(t *testing.T) {
	fixed := baseSpec()
	fixed.Replicas = 3
	if got := BuildDeployment(fixed).Spec.Replicas; got == nil || *got != 3 {
		t.Fatalf("an app with a fixed instance count must still be written with it, got %v", got)
	}

	for _, tc := range []struct {
		name  string
		shape func(*AppSpec)
	}{
		{"autoscaling", func(s *AppSpec) {
			s.Autoscale, s.MinReplicas, s.MaxReplicas, s.CPUTarget = true, 2, 8, 70
		}},
		{"scale to zero", func(s *AppSpec) { s.ScaleToZero = true }},
		{"both", func(s *AppSpec) {
			s.ScaleToZero = true
			s.Autoscale, s.MinReplicas, s.MaxReplicas, s.CPUTarget = true, 1, 8, 70
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := scalableSpec()
			spec.Replicas = 3
			tc.shape(&spec)

			if got := BuildDeployment(spec).Spec.Replicas; got != nil {
				t.Fatalf("replicas was written as %d, so the next apply undoes the autoscaler", *got)
			}
			// A nil pointer is only half the answer: server-side apply reads
			// the JSON, and a field that is serialised as null claims ownership
			// just as firmly as one with a number in it.
			raw, err := ToUnstructured(BuildDeployment(spec))
			if err != nil {
				t.Fatal(err)
			}
			if _, found, _ := unstructured.NestedFieldNoCopy(raw.Object, "spec", "replicas"); found {
				t.Fatal("spec.replicas is present in the applied object, so apply owns it")
			}
		})
	}
}

// TestTheWakeServiceAndTheIngressAgreeOnAPort is the whole of scale to zero
// working or not working.
//
// An ExternalName Service is a DNS alias: no kube-proxy rule is made for it, so
// `targetPort` is never applied and the ingress controller dials whatever
// number it settles on — Traefik the Service's port, nginx the number in the
// Ingress backend. The alias said port 80, so both dialled port 80 of KEDA's
// interceptor, which listens on 8080 and nothing else. Every request to an app
// that could sleep was a 502, and the app was never woken.
func TestTheWakeServiceAndTheIngressAgreeOnAPort(t *testing.T) {
	spec := scalableSpec()
	spec.ScaleToZero = true

	service := BuildInterceptorService(spec)
	if service == nil {
		t.Fatal("no wake service")
	}
	ports, _, _ := unstructured.NestedSlice(service.Object, "spec", "ports")
	if len(ports) != 1 {
		t.Fatalf("the wake service has %d ports, want one", len(ports))
	}
	port := ports[0].(map[string]any)
	if port["port"] != int64(KEDAInterceptorPort) {
		t.Errorf("the wake service listens on %v, want %d — the interceptor's own port",
			port["port"], KEDAInterceptorPort)
	}
	if port["targetPort"] != int64(KEDAInterceptorPort) {
		t.Errorf("targetPort is %v, want %d: it is ignored on an ExternalName, so it must "+
			"not be the one value that disagrees", port["targetPort"], KEDAInterceptorPort)
	}

	backend := BuildIngress(spec).Spec.Rules[0].HTTP.Paths[0].Backend.Service
	if backend.Port.Number != int32(KEDAInterceptorPort) {
		t.Errorf("the ingress dials port %d of the interceptor, which listens on %d",
			backend.Port.Number, KEDAInterceptorPort)
	}

	// An ordinary app is unaffected: its own Service really does listen on 80.
	ordinary := scalableSpec()
	if got := BuildIngress(ordinary).Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Number; got != 80 {
		t.Errorf("an ordinary app's ingress dials port %d, want 80", got)
	}
}

func TestPDBSkippedForSingleInstance(t *testing.T) {
	// A budget requiring one available instance on a one-instance app blocks
	// every node drain, which breaks cluster upgrades.
	if BuildPDB(baseSpec()) != nil {
		t.Fatal("a PodDisruptionBudget was rendered for a single-instance app")
	}
	s := baseSpec()
	s.Replicas = 3
	pdb := BuildPDB(s)
	if pdb == nil {
		t.Fatal("no PodDisruptionBudget for a three-instance app")
	}
	if pdb.Spec.MinAvailable != nil {
		t.Fatalf("minAvailable is set to %v; it permits every instance but one to "+
			"be evicted at once, and deadlocks a drain when there is one left",
			pdb.Spec.MinAvailable)
	}
	if pdb.Spec.MaxUnavailable == nil || pdb.Spec.MaxUnavailable.IntValue() != 1 {
		t.Fatalf("maxUnavailable is %v, want 1", pdb.Spec.MaxUnavailable)
	}
}

func TestPDBNeverBlocksADrainForever(t *testing.T) {
	// An autoscaled app is allowed a minimum of one instance, and an HPA with
	// nothing to do sits at that minimum. A budget that insists one instance
	// stays available then refuses every eviction, and `kubectl drain` waits
	// for an eviction that can never be permitted: the node never empties and
	// the cluster cannot be upgraded.
	s := baseSpec()
	s.Autoscale = true
	s.MinReplicas = 1
	s.MaxReplicas = 5
	s.CPUTarget = 70

	pdb := BuildPDB(s)
	if pdb == nil {
		t.Fatal("no PodDisruptionBudget for an autoscaling app")
	}
	if pdb.Spec.MinAvailable != nil {
		t.Fatal("minAvailable on an app whose minimum is one instance blocks every drain")
	}
	if pdb.Spec.MaxUnavailable == nil || pdb.Spec.MaxUnavailable.IntValue() != 1 {
		t.Fatalf("maxUnavailable is %v, want 1 so one instance can always be taken",
			pdb.Spec.MaxUnavailable)
	}
}

func TestScaleToZeroLeavesTheAutoscalingToKEDA(t *testing.T) {
	// KEDA's HTTPScaledObject creates its own HorizontalPodAutoscaler. A second
	// one from us, pointed at the same Deployment, does not divide the work:
	// each overwrites the other's replica count on every reconcile and the app
	// oscillates for as long as both exist.
	s := baseSpec()
	s.Autoscale = true
	s.MinReplicas = 1
	s.MaxReplicas = 4
	s.CPUTarget = 70
	s.ScaleToZero = true
	s.Domains = []DomainSpec{{Hostname: "app.example.com", TLS: true}}

	if hpa := BuildHPA(s); hpa != nil {
		t.Fatal("an HorizontalPodAutoscaler was rendered for an app KEDA already scales")
	}
	scaled := BuildHTTPScaledObject(s)
	if scaled == nil {
		t.Fatal("no HTTPScaledObject, so nothing scales this app at all")
	}
	replicas, _, _ := unstructured.NestedMap(scaled.Object, "spec", "replicas")
	if replicas["max"] != int64(4) {
		t.Fatalf("the ceiling is %v, want the 4 the app asked for", replicas["max"])
	}

	// Without scale to zero the autoscaler is still ours.
	s.ScaleToZero = false
	if BuildHPA(s) == nil {
		t.Fatal("no HorizontalPodAutoscaler for an app that only asked for autoscaling")
	}
}

func TestEnvSecretCarriesValues(t *testing.T) {
	secret := BuildEnvSecret(baseSpec(), map[string]string{"DATABASE_URL": "postgres://x", "DEBUG": "false"})
	if secret.Name != "web-env" {
		t.Fatalf("secret name %q, want web-env", secret.Name)
	}
	if secret.StringData["DATABASE_URL"] != "postgres://x" {
		t.Fatal("the secret lost a value")
	}

	d := BuildDeployment(func() AppSpec { s := baseSpec(); s.EnvFromSecret = "web-env"; return s }())
	envFrom := d.Spec.Template.Spec.Containers[0].EnvFrom
	if len(envFrom) != 1 || envFrom[0].SecretRef.Name != "web-env" {
		t.Fatalf("the container does not read the env secret: %+v", envFrom)
	}
}

func TestEnvHashChangesWithValues(t *testing.T) {
	a := EnvHash(map[string]string{"A": "1", "B": "2"})
	// Key order must not matter, or every reconcile would look like a change.
	b := EnvHash(map[string]string{"B": "2", "A": "1"})
	if a != b {
		t.Fatal("EnvHash depends on map iteration order, so pods would restart for no reason")
	}
	if a == EnvHash(map[string]string{"A": "1", "B": "3"}) {
		t.Fatal("changing a value did not change the hash, so pods would not pick it up")
	}
	if a == EnvHash(map[string]string{"A": "1"}) {
		t.Fatal("removing a variable did not change the hash")
	}
}

func TestBuildFingerprintSeparatesBuildFromRuntime(t *testing.T) {
	// This is the mechanism behind ADR-0007 and the fix for the most common
	// complaint about comparable products.
	base := BuildFingerprint("https://github.com/a/b", "sha1", "railpack", "", "", nil)

	if base != BuildFingerprint("https://github.com/a/b", "sha1", "railpack", "", "", nil) {
		t.Fatal("the fingerprint is not stable for identical inputs")
	}
	if base == BuildFingerprint("https://github.com/a/b", "sha2", "railpack", "", "", nil) {
		t.Fatal("a new commit did not change the fingerprint, so it would never rebuild")
	}
	if base == BuildFingerprint("https://github.com/a/b", "sha1", "nixpacks", "", "", nil) {
		t.Fatal("changing the builder did not change the fingerprint")
	}
	if base == BuildFingerprint("https://github.com/a/b", "sha1", "railpack", "", "apps/web", nil) {
		t.Fatal("changing the root directory did not change the fingerprint")
	}
	if base == BuildFingerprint("https://github.com/a/b", "sha1", "railpack", "", "", map[string]string{"NODE_ENV": "production"}) {
		t.Fatal("a build argument did not change the fingerprint")
	}
	// Build arguments must hash in a stable order.
	args := map[string]string{"A": "1", "B": "2"}
	if BuildFingerprint("r", "c", "b", "", "", args) !=
		BuildFingerprint("r", "c", "b", "", "", map[string]string{"B": "2", "A": "1"}) {
		t.Fatal("build argument order changes the fingerprint")
	}
}

func TestLabelsCarryOwnership(t *testing.T) {
	labels := baseSpec().Labels()
	for _, key := range []string{
		"app.kubernetes.io/name", "app.kubernetes.io/managed-by",
		"skifity.com/app-id", "skifity.com/project-id", "skifity.com/team-id",
	} {
		if labels[key] == "" {
			t.Errorf("label %q is missing, so the object cannot be traced back to its owner", key)
		}
	}
	if labels["app.kubernetes.io/managed-by"] != "skifity" {
		t.Errorf("managed-by is %q", labels["app.kubernetes.io/managed-by"])
	}
}

// TestAWorkerIsNotHeldOpenForNothing: the preStop pause exists so proxies can
// notice an endpoint disappearing. A worker has no endpoints, so pausing would
// only make every deploy of it five seconds slower.
func TestAWorkerIsNotHeldOpenForNothing(t *testing.T) {
	s := baseSpec()
	s.Port = 0
	if l := BuildDeployment(s).Spec.Template.Spec.Containers[0].Lifecycle; l != nil {
		t.Fatal("a worker was given a preStop pause it has no use for")
	}
}

func TestNoPortMeansNoProbes(t *testing.T) {
	// A worker has nothing to probe; a probe against no port would fail forever.
	s := baseSpec()
	s.Port = 0
	container := BuildDeployment(s).Spec.Template.Spec.Containers[0]
	if container.ReadinessProbe != nil || container.LivenessProbe != nil {
		t.Fatal("probes were rendered for an app with no port")
	}
	if len(container.Ports) != 0 {
		t.Fatal("a container port was rendered for an app with no port")
	}
}

func TestHealthPathFallsBackToTCP(t *testing.T) {
	s := baseSpec()
	s.HealthPath = ""
	container := BuildDeployment(s).Spec.Template.Spec.Containers[0]
	if container.ReadinessProbe.TCPSocket == nil {
		t.Fatal("without a health path the probe should be a TCP connect, not an HTTP request to an unknown path")
	}
}

func TestManifestsAreDeterministic(t *testing.T) {
	s := baseSpec()
	s.PlainEnv = map[string]string{"Z": "1", "A": "2", "M": "3"}
	first := BuildDeployment(s).Spec.Template.Spec.Containers[0].Env
	for range 20 {
		next := BuildDeployment(s).Spec.Template.Spec.Containers[0].Env
		if len(next) != len(first) {
			t.Fatal("environment length changed between renders")
		}
		for i := range first {
			if next[i].Name != first[i].Name {
				t.Fatalf("environment order changed between renders at %d: %q then %q",
					i, first[i].Name, next[i].Name)
			}
		}
	}
	// And the order must actually be sorted, so a diff is readable.
	var plain []string
	for _, e := range first {
		if e.Name == "Z" || e.Name == "A" || e.Name == "M" {
			plain = append(plain, e.Name)
		}
	}
	if strings.Join(plain, "") != "AMZ" {
		t.Fatalf("plain environment is ordered %v, want sorted", plain)
	}
}

func TestNamespaceIsolation(t *testing.T) {
	ns := BuildNamespace("acme-shop-production", "team_1", "prj_1", PodSecurityRestricted)
	if ns.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		t.Fatal("the namespace does not enforce the restricted pod security profile")
	}

	quota := BuildResourceQuota("acme-shop-production", DefaultQuota())
	// A LoadBalancer or NodePort service opens a port on every node and
	// bypasses the ingress, and with it TLS.
	if got := quota.Spec.Hard["services.loadbalancers"]; got.String() != "0" {
		t.Fatalf("LoadBalancer services are not blocked: %s", got.String())
	}
	if got := quota.Spec.Hard["services.nodeports"]; got.String() != "0" {
		t.Fatalf("NodePort services are not blocked: %s", got.String())
	}
	if mem := quota.Spec.Hard["requests.memory"]; mem.IsZero() {
		t.Fatal("no memory quota was set")
	}

	limits := BuildLimitRange("acme-shop-production")
	if limits.Spec.Limits[0].Default.Memory().IsZero() {
		t.Fatal("a container created without a memory limit would get none")
	}
}

func TestNetworkPoliciesDenyByDefaultAndBlockMetadata(t *testing.T) {
	policies := BuildNetworkPolicies("acme-shop-production", "skifity-system")
	if len(policies) != 2 {
		t.Fatalf("got %d policies, want a deny-all and an allow", len(policies))
	}

	deny := policies[0]
	if len(deny.Spec.Ingress) != 0 || len(deny.Spec.Egress) != 0 {
		t.Fatal("the deny-all policy has rules, so it does not deny anything")
	}
	if len(deny.Spec.PolicyTypes) != 2 {
		t.Fatal("the deny-all policy does not cover both directions")
	}

	allow := policies[1]
	var sawMetadataBlock bool
	for _, rule := range allow.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock == nil {
				continue
			}
			for _, except := range peer.IPBlock.Except {
				if except == "169.254.169.254/32" {
					sawMetadataBlock = true
				}
			}
		}
	}
	// The cloud metadata endpoint hands provider credentials to anything that
	// asks, which is a well-trodden path from a compromised app to the account.
	if !sawMetadataBlock {
		t.Fatal("egress to the cloud metadata endpoint is not blocked")
	}
}

// TestScaleToZeroRendersWhatItPromises: the toggle carried all the way into
// the spec and then rendered nothing at all, so turning it on installed KEDA
// and changed the app not one bit.
// scalableSpec is an app with a hostname, which is what scale to zero needs.
func scalableSpec() AppSpec {
	spec := baseSpec()
	spec.Domains = []DomainSpec{{Hostname: "shop.example.com", Path: "/", TLS: true}}
	return spec
}

func TestScaleToZeroRendersWhatItPromises(t *testing.T) {
	spec := scalableSpec()
	spec.ScaleToZero = true
	spec.Autoscale = true
	spec.MinReplicas, spec.MaxReplicas = 1, 4

	scaled := BuildHTTPScaledObject(spec)
	if scaled == nil {
		t.Fatal("scale to zero renders no KEDA object, so nothing ever scales to zero")
	}
	replicas, _, _ := unstructured.NestedMap(scaled.Object, "spec", "replicas")
	if replicas["min"] != int64(0) {
		t.Errorf("the floor is %v, want 0", replicas["min"])
	}
	if replicas["max"] != int64(4) {
		t.Errorf("the ceiling is %v, want the app's maximum", replicas["max"])
	}
	hosts, _, _ := unstructured.NestedSlice(scaled.Object, "spec", "hosts")
	if len(hosts) != len(spec.Domains) {
		t.Errorf("the object knows %d hostnames, want %d", len(hosts), len(spec.Domains))
	}

	// The interceptor is what wakes the app, so the Ingress has to go through
	// it. Pointing at the app's own Service would give a sleeping app a 503
	// and start nothing.
	ingress := BuildIngress(spec)
	if ingress == nil {
		t.Fatal("no ingress")
	}
	backend := ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name
	if backend != InterceptorServiceName(spec.Name) {
		t.Fatalf("the ingress points at %q, so a request to a sleeping app wakes nothing", backend)
	}
	if BuildInterceptorService(spec) == nil {
		t.Fatal("the ingress points at a service that is never created")
	}
}

// TestWithoutScaleToZeroNothingChanges keeps the ordinary path ordinary.
func TestWithoutScaleToZeroNothingChanges(t *testing.T) {
	spec := scalableSpec()
	spec.ScaleToZero = false

	if BuildHTTPScaledObject(spec) != nil || BuildInterceptorService(spec) != nil {
		t.Fatal("an app that does not scale to zero got KEDA objects anyway")
	}
	ingress := BuildIngress(spec)
	if got := ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name; got != spec.Name {
		t.Fatalf("the ingress points at %q, want the app's own service", got)
	}
}

// TestScaleToZeroNeedsAHostname: the interceptor routes by Host header, so an
// app nobody can reach by name has nothing to be woken by.
func TestScaleToZeroNeedsAHostname(t *testing.T) {
	spec := scalableSpec()
	spec.ScaleToZero = true
	spec.Domains = nil

	if BuildHTTPScaledObject(spec) != nil {
		t.Fatal("an app with no hostname was given a scaler that could never fire")
	}
}

// TestTheInterceptorCanReachTheApp: KEDA runs in its own namespace, and the
// environment's default-deny policy would otherwise drop the request that woke
// the app.
func TestTheInterceptorCanReachTheApp(t *testing.T) {
	policies := BuildNetworkPolicies("acme-shop-production", "skifity-system")
	var allowed bool
	for _, policy := range policies {
		for _, rule := range policy.Spec.Ingress {
			for _, peer := range rule.From {
				if peer.NamespaceSelector == nil {
					continue
				}
				if peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] == KEDANamespace {
					allowed = true
				}
			}
		}
	}
	if !allowed {
		t.Fatal("an app that scales to zero wakes up and then drops the request that woke it")
	}
}

// TestARunGetsTheAppsOwnWorld is the whole point of a one-off command: a
// migration has to see the same DATABASE_URL the app sees, in the same image,
// or it migrates the wrong thing or nothing at all.
func TestARunGetsTheAppsOwnWorld(t *testing.T) {
	app := baseSpec()
	app.EnvFromSecret = "web-env"
	app.Volumes = []VolumeSpec{{Name: "data", MountPath: "/data", SizeGB: 1}}

	job, err := BuildRunJob(RunSpec{App: app, Name: "web-run-abc", Command: "npm run migrate"})
	if err != nil {
		t.Fatalf("BuildRunJob: %v", err)
	}
	pod := job.Spec.Template.Spec
	container := pod.Containers[0]

	if container.Image != app.Image {
		t.Errorf("the command runs %q, not the app's own image", container.Image)
	}
	if len(container.EnvFrom) == 0 || container.EnvFrom[0].SecretRef.Name != "web-env" {
		t.Error("the command does not get the app's variables, so a migration has no database to migrate")
	}
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].MountPath != "/data" {
		t.Error("the command cannot see the app's volume")
	}
	if container.Args[0] != "npm run migrate" {
		t.Errorf("the command is %q; it should reach the shell as typed", container.Args[0])
	}
	if container.Command[0] != "/bin/sh" {
		t.Error("the command is not run through a shell, so `a && b` would not work")
	}

	// A migration that half-ran and then ran again is worse than one that
	// failed and said so.
	if *job.Spec.BackoffLimit != 0 {
		t.Errorf("backoffLimit is %d; a failed migration would be retried", *job.Spec.BackoffLimit)
	}
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy is %s, want Never", pod.RestartPolicy)
	}
	if job.Spec.ActiveDeadlineSeconds == nil {
		t.Error("a command with no deadline could run forever")
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("the command's pod mounts an API token it does not need")
	}
}

// TestARunIsRefusedWithoutSomethingToRunIn.
func TestARunIsRefusedWithoutSomethingToRunIn(t *testing.T) {
	cases := map[string]RunSpec{
		"no image":   {App: func() AppSpec { s := baseSpec(); s.Image = ""; return s }(), Name: "x", Command: "ls"},
		"no command": {App: baseSpec(), Name: "x"},
		"no name":    {App: baseSpec(), Command: "ls"},
	}
	for name, spec := range cases {
		if _, err := BuildRunJob(spec); err == nil {
			t.Errorf("%s: an impossible run was accepted", name)
		}
	}
}

// TestReleaseAndOneOffRunsAreToldApart: they share a shape and differ in when
// they run, and an operator reading the namespace should be able to see which
// is which.
func TestReleaseAndOneOffRunsAreToldApart(t *testing.T) {
	app := baseSpec()
	release, err := BuildRunJob(RunSpec{
		App: app, Name: RunJobName(app.Name, RunKindRelease, "dep123"),
		Command: "npm run migrate", Kind: RunKindRelease,
	})
	if err != nil {
		t.Fatalf("BuildRunJob: %v", err)
	}
	oneOff, err := BuildRunJob(RunSpec{
		App: app, Name: RunJobName(app.Name, RunKindOneOff, "abc123"), Command: "ls",
	})
	if err != nil {
		t.Fatalf("BuildRunJob: %v", err)
	}

	if release.Name == oneOff.Name {
		t.Fatal("a release and a one-off command would collide on the same name")
	}
	if !strings.Contains(release.Name, "release") {
		t.Errorf("a release job is called %q", release.Name)
	}
	if release.Labels[version.LabelKey("run-kind")] != RunKindRelease {
		t.Error("a release job is not labelled as one")
	}
	for _, name := range []string{release.Name, oneOff.Name} {
		if len(name) > 63 {
			t.Errorf("%q is %d characters, which Kubernetes refuses", name, len(name))
		}
	}
}

// TestAScheduledCommandDoesNotPileUp: a job that is still running when the next
// one is due takes longer than its interval, and two copies of a nightly report
// is worse than one late one.
func TestAScheduledCommandDoesNotPileUp(t *testing.T) {
	app := baseSpec()
	cron, err := BuildCronJob(RunSpec{
		App: app, Name: CronJobName(app.Name, "nightly report"),
		Command: "npm run digest", Kind: RunKindScheduled,
	}, "0 3 * * *")
	if err != nil {
		t.Fatalf("BuildCronJob: %v", err)
	}

	if cron.Spec.ConcurrencyPolicy != batchv1.ForbidConcurrent {
		t.Errorf("concurrency policy is %s, so a slow job would run twice at once", cron.Spec.ConcurrencyPolicy)
	}
	if cron.Spec.Schedule != "0 3 * * *" {
		t.Errorf("schedule is %q", cron.Spec.Schedule)
	}
	// Unset on purpose: a schedule means UTC, because a cluster's idea of local
	// time is not something anybody chose.
	if cron.Spec.TimeZone != nil {
		t.Errorf("the schedule is pinned to %q rather than UTC", *cron.Spec.TimeZone)
	}
	if cron.Spec.StartingDeadlineSeconds == nil {
		t.Error("a job that could not start would fire every missed interval at once when it can")
	}
	if cron.Spec.SuccessfulJobsHistoryLimit == nil || cron.Spec.FailedJobsHistoryLimit == nil {
		t.Error("finished jobs would pile up in the namespace forever")
	}
	// A one-off run deletes itself an hour after it finishes. A scheduled one
	// inheriting that would make the history limits a lie: a nightly job's last
	// three runs would be gone by morning, which is exactly when somebody looks
	// for the one that failed.
	if cron.Spec.JobTemplate.Spec.TTLSecondsAfterFinished != nil {
		t.Errorf("a scheduled job deletes itself after %ds, so the history limits keep nothing",
			*cron.Spec.JobTemplate.Spec.TTLSecondsAfterFinished)
	}
	// And the one-off still does.
	once, err := BuildRunJob(RunSpec{App: app, Name: "web-run-abc", Command: "echo hi"})
	if err != nil {
		t.Fatalf("BuildRunJob: %v", err)
	}
	if once.Spec.TTLSecondsAfterFinished == nil {
		t.Error("a one-off run never deletes itself, so a namespace fills with commands somebody ran once")
	}

	// The name is derived from the app and the job, so two apps can both have a
	// "nightly report" and one app cannot have two.
	if got := CronJobName("web", "Nightly Report"); got != CronJobName("web", "nightly report") {
		t.Errorf("the same name in different cases produced %q and %q", got, CronJobName("web", "nightly report"))
	}
	if len(cron.Name) > 63 {
		t.Errorf("%q is %d characters, which Kubernetes refuses", cron.Name, len(cron.Name))
	}
}

func TestARunsPodIsNotOneOfTheAppsInstances(t *testing.T) {
	// The app's Service, its disruption budget, its topology spread and the
	// panel's own instance list all select on the same two labels, and so does
	// the Deployment the autoscaler reads its metrics through. A pod running a
	// migration that carried both would be counted as an instance of the app.
	app := baseSpec()
	app.Replicas = 2
	job, err := BuildRunJob(RunSpec{App: app, Name: "web-run-abcd1234", Command: "npm run migrate"})
	if err != nil {
		t.Fatalf("BuildRunJob: %v", err)
	}

	selector := BuildDeployment(app).Spec.Selector.MatchLabels
	podLabels := job.Spec.Template.Labels
	matches := true
	for k, v := range selector {
		if podLabels[k] != v {
			matches = false
		}
	}
	if matches {
		t.Fatalf("a run's pod matches the app's own selector %v, so it counts as an instance", selector)
	}
	if svc := BuildService(app); svc != nil {
		matches = true
		for k, v := range svc.Spec.Selector {
			if podLabels[k] != v {
				matches = false
			}
		}
		if matches {
			t.Error("a run's pod matches the app's Service, which would send it traffic")
		}
	}

	// It still says who it belongs to, which is what the panel looks it up by.
	if podLabels[version.LabelKey("app-id")] != app.AppID {
		t.Error("a run's pod no longer says which app it belongs to")
	}
	if job.Labels["app.kubernetes.io/name"] != app.Name {
		t.Error("the Job itself lost the app's name, which is how a run is found again")
	}
	if job.Labels["app.kubernetes.io/component"] != "run" {
		t.Error("the Job is no longer marked as a run")
	}
}
