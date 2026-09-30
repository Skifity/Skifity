package cluster

import (
	"io"
	"log/slog"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// A fake token, shaped like a real one so the validator and the fingerprint
// both have something ordinary to work on. It is not a credential: the account,
// the tunnel and the secret are all made up.
const fakeTunnelToken = "eyJhIjoiMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMCIsInQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJzIjoiMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAifQ=="

func tunnelParts(t *testing.T, objects []any) (*corev1.Secret, *appsv1.Deployment) {
	t.Helper()
	var secret *corev1.Secret
	var deployment *appsv1.Deployment
	for _, object := range objects {
		switch typed := object.(type) {
		case *corev1.Secret:
			secret = typed
		case *appsv1.Deployment:
			deployment = typed
		default:
			t.Fatalf("the tunnel rendered an object nothing expects: %T", object)
		}
	}
	if secret == nil || deployment == nil {
		t.Fatal("the tunnel must render both a Secret and a Deployment")
	}
	return secret, deployment
}

// The point of the tunnel is surviving a server, so a rendering that cannot do
// that is the one failure worth a test: one replica, or both on one node, and
// the front door goes down with the machine it happens to be on.
func TestTunnelSurvivesAServer(t *testing.T) {
	secret, deployment := tunnelParts(t, tunnelObjects("skifity", fakeTunnelToken))

	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas < 2 {
		t.Errorf("the connector must run more than once; got %v", deployment.Spec.Replicas)
	}

	spread := deployment.Spec.Template.Spec.TopologySpreadConstraints
	if len(spread) != 1 || spread[0].TopologyKey != "kubernetes.io/hostname" {
		t.Fatalf("the replicas must be spread across hosts; got %+v", spread)
	}
	// ScheduleAnyway, not DoNotSchedule: on the single-node cluster most
	// installs start as, the second replica would otherwise be Pending for ever
	// and the tunnel would run at half strength with nothing saying so.
	if spread[0].WhenUnsatisfiable != corev1.ScheduleAnyway {
		t.Errorf("a one-server cluster must still get both replicas; got %q", spread[0].WhenUnsatisfiable)
	}

	rolling := deployment.Spec.Strategy.RollingUpdate
	if rolling == nil || rolling.MaxUnavailable == nil || rolling.MaxUnavailable.IntValue() != 0 {
		t.Errorf("a rollout must not take a connector away before its replacement is connected; got %+v", rolling)
	}

	if secret.StringData["token"] != fakeTunnelToken {
		t.Error("the token must reach the cluster as a Secret")
	}
}

// The token must not be an argument, a label or an annotation. A Deployment is
// readable by anything that can list Deployments; a Secret is the object the
// cluster's own access rules are about.
func TestTunnelKeepsTheTokenOutOfThePodSpec(t *testing.T) {
	_, deployment := tunnelParts(t, tunnelObjects("skifity", fakeTunnelToken))
	pod := deployment.Spec.Template.Spec

	if len(pod.Containers) != 1 {
		t.Fatalf("expected one container; got %d", len(pod.Containers))
	}
	container := pod.Containers[0]

	for _, arg := range container.Args {
		if arg == fakeTunnelToken {
			t.Fatal("the token was passed as a command line argument, where every process on the node can read it")
		}
	}
	for _, env := range container.Env {
		if env.Value == fakeTunnelToken {
			t.Fatal("the token was inlined into the pod spec instead of being read from the Secret")
		}
	}
	for _, value := range deployment.Spec.Template.Annotations {
		if value == fakeTunnelToken {
			t.Fatal("the token was written into an annotation")
		}
	}

	var found bool
	for _, env := range container.Env {
		if env.Name == "TUNNEL_TOKEN" && env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
			found = true
			if env.ValueFrom.SecretKeyRef.Name != TunnelSecret {
				t.Errorf("the token comes from %q, not the tunnel's Secret", env.ValueFrom.SecretKeyRef.Name)
			}
		}
	}
	if !found {
		t.Error("the connector has no token to authenticate with")
	}

	// It talks to Cloudflare and to the ingress. A service account token in
	// the container is an outward-facing pod holding a key to the API server.
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("the connector must not be given a Kubernetes API token")
	}
}

// Changing the token in Settings has to change what is running. An environment
// variable is read once at startup, so without a new pod template the old
// credential keeps being used and the panel still says installed.
func TestTunnelRollsWhenTheTokenChanges(t *testing.T) {
	_, first := tunnelParts(t, tunnelObjects("skifity", fakeTunnelToken))
	_, second := tunnelParts(t, tunnelObjects("skifity", fakeTunnelToken+"x"))
	_, again := tunnelParts(t, tunnelObjects("skifity", fakeTunnelToken))

	before := first.Spec.Template.Annotations
	after := second.Spec.Template.Annotations
	if len(before) == 0 {
		t.Fatal("the pod template records nothing about the token, so a new one is not a new template")
	}
	if fmtAnnotations(before) == fmtAnnotations(after) {
		t.Error("a different token produced an identical pod template, so the connectors would never restart")
	}
	if fmtAnnotations(before) != fmtAnnotations(again.Spec.Template.Annotations) {
		t.Error("the same token produced a different pod template, so every apply would restart the front door")
	}
}

func fmtAnnotations(m map[string]string) string {
	out := ""
	for k, v := range m {
		out += k + "=" + v + ";"
	}
	return out
}

// Installing without a token must refuse, and say where the token comes from.
// A cloudflared with no token starts, fails to authenticate, and restarts for
// ever while the panel shows the component as installed.
func TestInstallCloudflareTunnelRefusesWithoutAToken(t *testing.T) {
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	c := New(nil, db, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err = c.installCloudflareTunnel(t.Context())
	if err == nil {
		t.Fatal("installing with no token was allowed")
	}
	problem := errdoc.From(err)
	if problem.Code != "tunnel.no_token" {
		t.Errorf("code = %q, want tunnel.no_token", problem.Code)
	}
	if problem.Fix == "" {
		t.Error("a refusal with no fix is a dead end")
	}
}

// Refreshing a component nobody installed must not install it. Saving a token
// is saving a token; installing is a button.
func TestRefreshCloudflareTunnelDoesNothingWhenNotInstalled(t *testing.T) {
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.SetSetting(t.Context(), settings.KeyCloudflareTunnelToken, fakeTunnelToken, false, ""); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	// A nil Kubernetes client: reaching the cluster at all would panic, which
	// is exactly the assertion.
	c := New(nil, db, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := c.RefreshCloudflareTunnel(t.Context()); err != nil {
		t.Fatalf("refreshing an uninstalled tunnel: %v", err)
	}
}
