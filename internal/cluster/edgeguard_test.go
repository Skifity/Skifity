package cluster

import (
	"encoding/json"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"skifity/internal/edgerules"
	"skifity/internal/guard"
	"skifity/internal/kube"
)

func guardParts(t *testing.T, objects []any) (*corev1.ConfigMap, *appsv1.Deployment, *corev1.Service) {
	t.Helper()
	var cm *corev1.ConfigMap
	var deployment *appsv1.Deployment
	var service *corev1.Service
	for _, object := range objects {
		switch typed := object.(type) {
		case *corev1.ConfigMap:
			cm = typed
		case *appsv1.Deployment:
			deployment = typed
		case *corev1.Service:
			service = typed
		default:
			t.Fatalf("the guard rendered an object nothing expects: %T", object)
		}
	}
	if cm == nil || deployment == nil || service == nil {
		t.Fatal("the guard must render a ConfigMap, a Deployment and a Service")
	}
	return cm, deployment, service
}

func sampleConfig() guard.Config {
	return guard.Config{Sets: map[string]guard.Protected{
		"shop.example.com": {AppID: "app_1", RuleSet: edgerules.RuleSet{
			Default: edgerules.ActionBlock,
			Rules: []edgerules.Rule{{
				ID: "r1", Name: "the office", Action: edgerules.ActionAllow, Enabled: true,
				Expr: edgerules.Expr{Test: &edgerules.Test{
					Field: edgerules.FieldIP, Op: edgerules.OpIn, Values: []string{"203.0.113.0/24"},
				}},
			}},
		}},
	}}
}

// Traefik has no way to ignore an authorizer that does not answer, so anything
// that can take both replicas away at once takes every protected site with it.
func TestTheGuardSurvivesAServerAndARollout(t *testing.T) {
	objects, err := guardObjects("skifity-system", "ghcr.io/example/skifity:1.0.0", sampleConfig())
	if err != nil {
		t.Fatalf("guardObjects: %v", err)
	}
	_, deployment, service := guardParts(t, objects)

	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas < 2 {
		t.Errorf("the guard must run more than once; got %v", deployment.Spec.Replicas)
	}
	spread := deployment.Spec.Template.Spec.TopologySpreadConstraints
	if len(spread) != 1 || spread[0].TopologyKey != "kubernetes.io/hostname" {
		t.Fatalf("the replicas must be spread across hosts; got %+v", spread)
	}
	if spread[0].WhenUnsatisfiable != corev1.ScheduleAnyway {
		t.Errorf("a one-server cluster must still get both replicas; got %q", spread[0].WhenUnsatisfiable)
	}
	rolling := deployment.Spec.Strategy.RollingUpdate
	if rolling == nil || rolling.MaxUnavailable == nil || rolling.MaxUnavailable.IntValue() != 0 {
		t.Errorf("a rollout must not take a replica away before its replacement is ready; got %+v", rolling)
	}

	// A replica with no rules reports itself not ready, so a rollout never puts
	// one in front of traffic it cannot judge.
	container := deployment.Spec.Template.Spec.Containers[0]
	if container.ReadinessProbe == nil || container.ReadinessProbe.HTTPGet == nil ||
		container.ReadinessProbe.HTTPGet.Path != "/healthz" {
		t.Error("the guard has no readiness probe, so an unloaded replica would take traffic")
	}

	// And Traefik has to be able to find it at the address the middleware names.
	if service.Name != kube.GuardService {
		t.Errorf("the Service is %q and the middleware dials %q", service.Name, kube.GuardService)
	}
	if len(service.Spec.Ports) != 1 || service.Spec.Ports[0].Port != int32(kube.GuardPort) {
		t.Errorf("the Service listens on %+v and the middleware dials %d", service.Spec.Ports, kube.GuardPort)
	}
}

// It reads a file and answers HTTP. A token for the Kubernetes API in a pod
// that every request on the internet reaches is the first thing an attacker
// looks for.
func TestTheGuardHoldsNoCredentials(t *testing.T) {
	objects, err := guardObjects("skifity-system", "ghcr.io/example/skifity:1.0.0", sampleConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, deployment, _ := guardParts(t, objects)
	pod := deployment.Spec.Template.Spec

	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("the guard must not be given a Kubernetes API token")
	}
	for _, volume := range pod.Volumes {
		if volume.Secret != nil {
			t.Errorf("the guard mounts a Secret: %s", volume.Name)
		}
	}
	container := pod.Containers[0]
	for _, env := range container.Env {
		if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
			t.Errorf("the guard reads a Secret into %s", env.Name)
		}
	}
	sc := container.SecurityContext
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot ||
		sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("the guard must run non-root with a read-only root filesystem")
	}
}

// The rules the guard will read have to be the rules that were written, and
// the file it is told to read has to be the file that is mounted.
func TestTheRulesReachTheFileTheGuardReads(t *testing.T) {
	objects, err := guardObjects("skifity-system", "img:1", sampleConfig())
	if err != nil {
		t.Fatal(err)
	}
	cm, deployment, _ := guardParts(t, objects)

	var decoded guard.Config
	if err := json.Unmarshal([]byte(cm.Data[GuardRulesFile]), &decoded); err != nil {
		t.Fatalf("the ConfigMap does not hold readable rules: %v", err)
	}
	set, ok := decoded.Sets["shop.example.com"]
	if !ok || len(set.RuleSet.Rules) != 1 || set.RuleSet.Rules[0].Name != "the office" {
		t.Fatalf("the rules did not survive the round trip: %+v", decoded)
	}

	container := deployment.Spec.Template.Spec.Containers[0]
	var rulesPath string
	for _, env := range container.Env {
		if env.Name == "SKIFITY_GUARD_RULES" {
			rulesPath = env.Value
		}
	}
	want := GuardMountPath + "/" + GuardRulesFile
	if rulesPath != want {
		t.Errorf("the guard is told to read %q and the ConfigMap is mounted at %q", rulesPath, want)
	}
	var mounted bool
	for _, mount := range container.VolumeMounts {
		if mount.MountPath == GuardMountPath {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("nothing is mounted at %s, so the file the guard reads is not there", GuardMountPath)
	}
	if container.Args[0] != "edge-guard" {
		t.Errorf("the container runs %v, not the guard", container.Args)
	}
}

// Kubernetes refuses an object above about a megabyte. A refusal at apply time
// would be rules somebody saved that never reached the cluster, so it is
// refused in the panel where they are looking at the form.
func TestTooManyRulesIsRefusedBeforeItIsApplied(t *testing.T) {
	huge := guard.Config{Sets: map[string]guard.Protected{}}
	values := make([]string, 4000)
	for i := range values {
		values[i] = "203.0.113.0/24"
	}
	for i := range 20 {
		huge.Sets[string(rune('a'+i))+".example.com"] = guard.Protected{
			RuleSet: edgerules.RuleSet{Rules: []edgerules.Rule{{
				Name: "big", Action: edgerules.ActionBlock, Enabled: true,
				Expr: edgerules.Expr{Test: &edgerules.Test{
					Field: edgerules.FieldIP, Op: edgerules.OpIn, Values: values,
				}},
			}}},
		}
	}
	_, err := guardObjects("skifity-system", "img:1", huge)
	if err == nil {
		t.Fatal("a rule set too large for a ConfigMap was accepted")
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}
