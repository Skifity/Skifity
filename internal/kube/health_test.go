package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func appContainer(s AppSpec) corev1.Container {
	return BuildDeployment(s).Spec.Template.Spec.Containers[0]
}

// Each of the three checks renders what it says, on all three probes.
func TestEachHealthCheckRendersItsProbes(t *testing.T) {
	for _, tc := range []struct {
		check, path string
		want        string // "http", "tcp" or "" for no probes
	}{
		{check: HealthHTTP, path: "/healthz", want: "http"},
		{check: HealthTCP, path: "/healthz", want: "tcp"},
		{check: HealthTCP, path: "", want: "tcp"},
		{check: HealthNone, path: "/healthz", want: ""},
		// Nobody chose: what every app had before anybody could.
		{check: "", path: "/healthz", want: "http"},
		{check: "", path: "", want: "tcp"},
		// An HTTP check with nothing to ask cannot ask "/" on the app's
		// behalf; it connects instead.
		{check: HealthHTTP, path: "", want: "tcp"},
	} {
		s := baseSpec()
		s.HealthCheck, s.HealthPath = tc.check, tc.path
		c := appContainer(s)
		probes := map[string]*corev1.Probe{"readiness": c.ReadinessProbe, "liveness": c.LivenessProbe, "startup": c.StartupProbe}
		for name, probe := range probes {
			switch tc.want {
			case "":
				if probe != nil {
					t.Errorf("check %q: a %s probe was rendered for an app that asked for none", tc.check, name)
				}
			case "http":
				if probe == nil || probe.HTTPGet == nil || probe.HTTPGet.Path != tc.path {
					t.Errorf("check %q path %q: the %s probe does not ask the path: %+v", tc.check, tc.path, name, probe)
				}
			case "tcp":
				if probe == nil || probe.TCPSocket == nil || probe.HTTPGet != nil {
					t.Errorf("check %q path %q: the %s probe is not a connect: %+v", tc.check, tc.path, name, probe)
				}
			}
		}
	}
}

// Switching the check off takes the probes away and nothing else: the port,
// the Service and the pause before stopping are still what the app needs.
func TestNoHealthCheckKeepsEverythingElse(t *testing.T) {
	s := baseSpec()
	s.HealthCheck = HealthNone
	c := appContainer(s)
	if len(c.Ports) != 1 || c.Lifecycle == nil || c.Lifecycle.PreStop == nil {
		t.Fatalf("an app with no health check lost its port or its pause before stopping: %+v", c)
	}
	if BuildService(s) == nil {
		t.Fatal("an app with no health check lost its Service")
	}
	if d := BuildDeployment(s).Spec.ProgressDeadlineSeconds; d != nil {
		t.Errorf("an app with no probes has nothing to wait for, and was given a deadline of %d", *d)
	}
}

// The startup probe allows at least the time asked for, and not a whole
// period more: its threshold is the budget over the period, rounded up.
func TestTheStartupProbeAllowsTheTimeAskedFor(t *testing.T) {
	for _, budget := range []int{0, MinHealthStartSeconds, 11, DefaultHealthStartSeconds, 121, 599, 600, MaxHealthStartSeconds} {
		s := baseSpec()
		s.HealthStartSeconds = budget
		probe := appContainer(s).StartupProbe
		want := budget
		if want == 0 {
			want = DefaultHealthStartSeconds
		}
		allowed := int(probe.FailureThreshold * probe.PeriodSeconds)
		if allowed < want {
			t.Errorf("a budget of %ds allows only %ds (%d × %ds)", want, allowed, probe.FailureThreshold, probe.PeriodSeconds)
		}
		if allowed >= want+int(probe.PeriodSeconds) {
			t.Errorf("a budget of %ds allows %ds, a whole period more than asked", want, allowed)
		}
	}

	// The default is the two minutes every app had before it could change:
	// forty checks three seconds apart.
	if p := appContainer(baseSpec()).StartupProbe; p.FailureThreshold != 40 || p.PeriodSeconds != 3 {
		t.Errorf("the default startup probe is %d × %ds, not the 40 × 3s it always was", p.FailureThreshold, p.PeriodSeconds)
	}
}

// The timeout reaches every probe, and the default is the three seconds it was.
func TestTheHealthTimeoutReachesEveryProbe(t *testing.T) {
	for _, timeout := range []int{0, 1, 10, MaxHealthTimeoutSeconds} {
		s := baseSpec()
		s.HealthTimeoutSeconds = timeout
		want := int32(timeout)
		if want == 0 {
			want = DefaultHealthTimeoutSeconds
		}
		c := appContainer(s)
		for name, probe := range map[string]*corev1.Probe{"readiness": c.ReadinessProbe, "liveness": c.LivenessProbe, "startup": c.StartupProbe} {
			if probe.TimeoutSeconds != want {
				t.Errorf("timeout %d: the %s probe waits %ds", timeout, name, probe.TimeoutSeconds)
			}
		}
	}
}

// A long start budget moves the rollout's deadline with it, or Kubernetes
// would call the rollout failed while the app was still inside the time it
// was allowed. The default leaves the Deployment exactly as it was rendered
// before the budget could change.
func TestALongStartMovesTheRolloutDeadline(t *testing.T) {
	if d := BuildDeployment(baseSpec()).Spec.ProgressDeadlineSeconds; d != nil {
		t.Errorf("an app on the default budget was given a deadline of %d; it should keep Kubernetes' own", *d)
	}
	s := baseSpec()
	s.HealthStartSeconds = MaxHealthStartSeconds
	d := BuildDeployment(s).Spec.ProgressDeadlineSeconds
	if d == nil || int(*d) <= MaxHealthStartSeconds {
		t.Fatalf("an app allowed %ds to start has a progress deadline of %v", MaxHealthStartSeconds, d)
	}
}

func TestHealthSettingsAreValidated(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*AppSpec)
		ok   bool
	}{
		{"defaults", func(*AppSpec) {}, true},
		{"none", func(s *AppSpec) { s.HealthCheck = HealthNone }, true},
		{"grpc", func(s *AppSpec) { s.HealthCheck = "grpc" }, false},
		{"start too short", func(s *AppSpec) { s.HealthStartSeconds = MinHealthStartSeconds - 1 }, false},
		{"start too long", func(s *AppSpec) { s.HealthStartSeconds = MaxHealthStartSeconds + 1 }, false},
		{"start at the ceiling", func(s *AppSpec) { s.HealthStartSeconds = MaxHealthStartSeconds }, true},
		{"timeout too long", func(s *AppSpec) { s.HealthTimeoutSeconds = MaxHealthTimeoutSeconds + 1 }, false},
		{"timeout negative", func(s *AppSpec) { s.HealthTimeoutSeconds = -1 }, false},
	} {
		s := baseSpec()
		tc.edit(&s)
		if err := s.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v, want ok=%t", tc.name, err, tc.ok)
		}
	}
}
