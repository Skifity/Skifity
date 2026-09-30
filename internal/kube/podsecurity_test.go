package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The defect this whole type exists for.
//
// Every app's pod spec pinned uid 1000, whatever the image declared it ran as.
// Measured against the one-click catalogue, of the 124 images whose config
// could be read from their registries, 120 do not run as uid 1000. Pinning it
// was a guess, and it was wrong almost every time.
func TestOnlyAnImageWeBuiltGetsItsUidPinned(t *testing.T) {
	built := Confinement{Level: PodSecurityRestricted, BuiltHere: true}
	if uid := built.RunAsUser(); uid == nil || *uid != 1000 {
		t.Errorf("an image Skifity built should run as 1000; got %v", uid)
	}

	for _, level := range PodSecurityLevels {
		theirs := Confinement{Level: level, BuiltHere: false}
		if uid := theirs.RunAsUser(); uid != nil {
			t.Errorf("at %q somebody else's image was pinned to uid %d instead of its own USER",
				level, *uid)
		}
	}
}

// What each level actually decides, in one table, because the rules are the
// point and they are easy to get subtly wrong.
func TestConfinementRules(t *testing.T) {
	cases := []struct {
		name        string
		c           Confinement
		nonRoot     bool // RunAsNonRoot is set to true
		dropAll     bool
		sysctlValue string
	}{
		{
			name:    "an app we built, strict",
			c:       Confinement{Level: PodSecurityRestricted, BuiltHere: true, Port: 3000},
			nonRoot: true, dropAll: true,
		},
		{
			// Lowering an environment so a third-party image can run is not a
			// reason to stop checking the one image whose contents are known.
			name:    "an app we built, in a lowered environment",
			c:       Confinement{Level: PodSecurityBaseline, BuiltHere: true, Port: 3000},
			nonRoot: true, dropAll: true,
		},
		{
			name:    "somebody else's image, strict",
			c:       Confinement{Level: PodSecurityRestricted, BuiltHere: false, Port: 3000},
			nonRoot: true, dropAll: true,
		},
		{
			// An image that starts as root and drops to its own user calls
			// setuid, which needs CAP_SETUID and CAP_SETGID. Dropping ALL from
			// a root container breaks the very images this level exists to run.
			name:    "somebody else's image, lowered",
			c:       Confinement{Level: PodSecurityBaseline, BuiltHere: false, Port: 80},
			nonRoot: false, dropAll: false,
		},
		{
			// 51 of the catalogue's 334 services listen below 1024. With every
			// capability dropped and containerd's default of 1024, not one of
			// them could bind its port.
			name:    "a low port with no capabilities left",
			c:       Confinement{Level: PodSecurityRestricted, BuiltHere: false, Port: 80},
			nonRoot: true, dropAll: true, sysctlValue: "0",
		},
		{
			name:    "an ordinary port needs no sysctl",
			c:       Confinement{Level: PodSecurityRestricted, BuiltHere: true, Port: 8080},
			nonRoot: true, dropAll: true,
		},
		{
			name:    "no port at all",
			c:       Confinement{Level: PodSecurityRestricted, BuiltHere: true},
			nonRoot: true, dropAll: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nonRoot := tc.c.RunAsNonRoot()
			if tc.nonRoot && (nonRoot == nil || !*nonRoot) {
				t.Errorf("RunAsNonRoot = %v, want true", nonRoot)
			}
			if !tc.nonRoot && nonRoot != nil {
				t.Errorf("RunAsNonRoot = %v, want unset so the image decides", *nonRoot)
			}
			if got := tc.c.DropAllCapabilities(); got != tc.dropAll {
				t.Errorf("DropAllCapabilities = %v, want %v", got, tc.dropAll)
			}
			if got := tc.c.UnprivilegedPortStart(); got != tc.sysctlValue {
				t.Errorf("UnprivilegedPortStart = %q, want %q", got, tc.sysctlValue)
			}
		})
	}
}

// An unknown or empty level must land on the strict one. Empty is every
// environment that existed before this was a column, and a typo that fell
// through to "enforce nothing" would be the worst possible failure.
func TestAnUnknownLevelIsTheStrictOne(t *testing.T) {
	for _, value := range []string{"", "strict", "privileged", "RESTRICTED", "Baseline"} {
		if got := NormalizePodSecurity(value); got != PodSecurityRestricted {
			t.Errorf("NormalizePodSecurity(%q) = %q, want restricted", value, got)
		}
	}
	if got := NormalizePodSecurity("baseline"); got != PodSecurityBaseline {
		t.Errorf("NormalizePodSecurity(\"baseline\") = %q, want baseline", got)
	}

	// "privileged" is a real Pod Security level and is not one this panel
	// offers: it permits host mounts and host networking, which is one tenant
	// reading another's disk.
	for _, value := range []string{"privileged", "", "strict"} {
		if ValidPodSecurity(value) {
			t.Errorf("%q was accepted as a level the panel offers", value)
		}
	}
}

// A lowered namespace must still be measured. Enforcement drops; audit and warn
// do not, so the API server's own log keeps recording every pod the strict
// profile would have refused.
func TestLoweringANamespaceDoesNotTurnOffTheMeasurement(t *testing.T) {
	ns := BuildNamespace("acme-shop-staging", "team_1", "prj_1", PodSecurityBaseline)
	if got := ns.Labels["pod-security.kubernetes.io/enforce"]; got != "baseline" {
		t.Errorf("enforce = %q, want baseline", got)
	}
	for _, key := range []string{"audit", "warn"} {
		if got := ns.Labels["pod-security.kubernetes.io/"+key]; got != "restricted" {
			t.Errorf("%s = %q, want restricted", key, got)
		}
	}

	// And an environment with nothing recorded is the strict one.
	strict := BuildNamespace("acme-shop-production", "team_1", "prj_1", "")
	if got := strict.Labels["pod-security.kubernetes.io/enforce"]; got != "restricted" {
		t.Errorf("an unset level enforced %q, want restricted", got)
	}
}

// The rendered Deployment, which is what actually reaches the cluster.
func TestDeploymentSecurityContextFollowsTheEnvironment(t *testing.T) {
	base := func() AppSpec {
		return AppSpec{
			Name: "web", Namespace: "acme-shop-production", Image: "wordpress:6",
			Port: 80, Replicas: 1,
		}
	}

	// A template on a strict environment: no uid pinned, non-root demanded,
	// and the sysctl that lets it bind port 80 without any capability.
	strict := BuildDeployment(base())
	pod := strict.Spec.Template.Spec
	if pod.SecurityContext.RunAsUser != nil {
		t.Errorf("a third-party image was pinned to uid %d", *pod.SecurityContext.RunAsUser)
	}
	if pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Error("the strict level must still demand a non-root image")
	}
	if len(pod.SecurityContext.Sysctls) != 1 ||
		pod.SecurityContext.Sysctls[0].Name != "net.ipv4.ip_unprivileged_port_start" ||
		pod.SecurityContext.Sysctls[0].Value != "0" {
		t.Errorf("port 80 with no capabilities needs the sysctl; got %+v", pod.SecurityContext.Sysctls)
	}

	// The same app on a lowered environment: the image decides everything about
	// its user, and it keeps the runtime's default capability set so that
	// dropping privileges still works.
	lowered := base()
	lowered.PodSecurity = PodSecurityBaseline
	pod = BuildDeployment(lowered).Spec.Template.Spec
	if pod.SecurityContext.RunAsNonRoot != nil {
		t.Error("a lowered environment must not demand a non-root image")
	}
	if caps := pod.Containers[0].SecurityContext.Capabilities; caps != nil {
		t.Errorf("a root image must keep the runtime's default capabilities; got %+v", caps)
	}
	if len(pod.SecurityContext.Sysctls) != 0 {
		t.Errorf("CAP_NET_BIND_SERVICE is already in the default set; got %+v", pod.SecurityContext.Sysctls)
	}

	// And an app Skifity built is unchanged by any of it.
	ours := base()
	ours.ImageBuiltHere = true
	ours.Port = 3000
	ours.PodSecurity = PodSecurityBaseline
	pod = BuildDeployment(ours).Spec.Template.Spec
	if pod.SecurityContext.RunAsUser == nil || *pod.SecurityContext.RunAsUser != 1000 {
		t.Error("an image Skifity built still runs as 1000")
	}
	if pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Error("an image Skifity built is still held to non-root")
	}
	drop := pod.Containers[0].SecurityContext.Capabilities
	if drop == nil || len(drop.Drop) != 1 || drop.Drop[0] != corev1.Capability("ALL") {
		t.Errorf("an image Skifity built still drops every capability; got %+v", drop)
	}

	// Whatever the level, nothing may gain privileges it did not start with.
	for _, spec := range []AppSpec{base(), lowered, ours} {
		sc := BuildDeployment(spec).Spec.Template.Spec.Containers[0].SecurityContext
		if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
			t.Error("allowPrivilegeEscalation must be false at every level")
		}
		if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
			t.Error("the seccomp profile must be RuntimeDefault at every level")
		}
	}
}

// A one-off command runs in the app's own image, so it has to be confined the
// same way the app is. An environment lowered so that its app can run an image
// starting as root, where a migration in that same image was still refused,
// is a panel saying "your app runs, and you cannot run anything in it".
func TestARunJobIsConfinedLikeTheAppItRunsIn(t *testing.T) {
	app := AppSpec{
		Name: "web", Namespace: "acme-shop-production", Image: "wordpress:6",
		Port: 80, Replicas: 1, PodSecurity: PodSecurityBaseline,
	}
	job, err := BuildRunJob(RunSpec{App: app, Name: "web-run-abc", Command: "wp core update-db"})
	if err != nil {
		t.Fatalf("BuildRunJob: %v", err)
	}
	pod := job.Spec.Template.Spec
	if pod.SecurityContext.RunAsNonRoot != nil {
		t.Error("a run in a lowered environment must not demand a non-root image")
	}
	if pod.SecurityContext.RunAsUser != nil {
		t.Errorf("a third-party image was pinned to uid %d", *pod.SecurityContext.RunAsUser)
	}
	if caps := pod.Containers[0].SecurityContext.Capabilities; caps != nil {
		t.Errorf("a root image must keep the runtime's default capabilities; got %+v", caps)
	}

	// And the strict default is unchanged.
	app.PodSecurity = ""
	app.ImageBuiltHere = true
	job, err = BuildRunJob(RunSpec{App: app, Name: "web-run-abc", Command: "rake db:migrate"})
	if err != nil {
		t.Fatalf("BuildRunJob: %v", err)
	}
	pod = job.Spec.Template.Spec
	if pod.SecurityContext.RunAsUser == nil || *pod.SecurityContext.RunAsUser != 1000 {
		t.Error("a run in an image Skifity built still runs as 1000")
	}
	if pod.Containers[0].SecurityContext.Capabilities == nil {
		t.Error("a run in an image Skifity built still drops every capability")
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("a run must never be given a Kubernetes API token")
	}
}

// Traefik refuses a cross-namespace middleware reference unless
// allowCrossNamespace is turned on, and it is off by default — on k3s's bundled
// Traefik included. The annotation used to name the panel's namespace, so the
// reference resolved to nothing and plain HTTP was never redirected, with no
// error anywhere: a middleware Traefik will not load is not a middleware
// Traefik complains about.
func TestTheRedirectMiddlewareIsInTheAppsOwnNamespace(t *testing.T) {
	spec := AppSpec{
		Name: "web", Namespace: "acme-shop-production", Image: "app:1", Port: 3000,
		Replicas: 1, ClusterIssuer: "skifity",
		Domains: []DomainSpec{{Hostname: "shop.example.com", Path: "/", TLS: true}},
	}
	ingress := BuildIngress(spec)
	if ingress == nil {
		t.Fatal("an app with a domain rendered no Ingress")
	}
	got := ingress.Annotations["traefik.ingress.kubernetes.io/router.middlewares"]
	want := "acme-shop-production-redirect-https@kubernetescrd"
	if got != want {
		t.Errorf("middleware reference = %q, want %q", got, want)
	}

	// And the middleware it names is rendered into that same namespace.
	middleware := BuildRedirectMiddleware(spec.Namespace)
	if ns := middleware.GetNamespace(); ns != spec.Namespace {
		t.Errorf("the middleware was created in %q, not beside the app in %q", ns, spec.Namespace)
	}
	if name := middleware.GetName(); name != RedirectMiddleware {
		t.Errorf("the middleware is named %q, and the annotation names %q", name, RedirectMiddleware)
	}
}

// The firewall's middleware goes in front of the redirect: a request nobody is
// allowed to make should not be answered with a redirect telling them where to
// make it instead. And both live in the app's own namespace, because Traefik
// will not load a middleware from another one.
func TestTheFirewallMiddlewareComesFirstAndIsLocal(t *testing.T) {
	spec := AppSpec{
		Name: "web", Namespace: "acme-shop-production", Image: "app:1", Port: 3000,
		Replicas: 1, ClusterIssuer: "skifity", Protected: true,
		Domains: []DomainSpec{{Hostname: "shop.example.com", Path: "/", TLS: true}},
	}
	got := BuildIngress(spec).Annotations["traefik.ingress.kubernetes.io/router.middlewares"]
	want := "acme-shop-production-firewall@kubernetescrd,acme-shop-production-redirect-https@kubernetescrd"
	if got != want {
		t.Errorf("middlewares = %q, want %q", got, want)
	}

	// An app with no rules carries only the redirect.
	spec.Protected = false
	got = BuildIngress(spec).Annotations["traefik.ingress.kubernetes.io/router.middlewares"]
	if got != "acme-shop-production-redirect-https@kubernetescrd" {
		t.Errorf("an unprotected app got %q", got)
	}
}

// A rule may test any header by name, so the middleware must forward all of
// them. A list would make a rule on X-Api-Key match nothing, with no error and
// no sign: the rule saved, the request allowed, and the header never sent to
// the process judging it.
func TestTheGuardMiddlewareForwardsEveryHeader(t *testing.T) {
	middleware := BuildGuardMiddleware("acme-shop-production", "skifity-system")
	spec, _, _ := unstructured.NestedMap(middleware.Object, "spec", "forwardAuth")
	if _, listed := spec["authRequestHeaders"]; listed {
		t.Error("a header allowlist would silently break a rule on any header not in it")
	}
	address, _ := spec["address"].(string)
	want := "http://skifity-guard.skifity-system.svc.cluster.local:9000/authorize"
	if address != want {
		t.Errorf("address = %q, want %q", address, want)
	}
	if ns := middleware.GetNamespace(); ns != "acme-shop-production" {
		t.Errorf("the middleware was created in %q, not beside the Ingress that names it", ns)
	}
}
