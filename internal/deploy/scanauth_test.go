package deploy

import (
	"encoding/json"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"skifity/internal/settings"
	"skifity/internal/store"
)

// A scan pulls a private image with the team's credentials for that image's
// registry and no other, and an image the panel pushed to its own external
// registry with the panel's.
func TestAScanIsGivenTheLoginForItsImagesRegistryOnly(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	ctx := t.Context()
	teamID, err := db.TeamIDForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, login := range []struct{ host, user, password string }{
		{"ghcr.io", "acme-bot", "ghp_not_a_real_token"},
		{"docker.io", "acme", "dckr_not_a_real_token"},
	} {
		sealed, err := d.keyring.Seal([]byte(login.password), store.RegistryContext(teamID, login.host))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SetRegistryCredential(ctx, &store.RegistryCredential{
			TeamID: teamID, Name: login.host, Host: login.host, Username: login.user,
		}, sealed); err != nil {
			t.Fatal(err)
		}
	}

	auths := func(image string) map[string]any {
		t.Helper()
		secret, err := d.scanAuth(ctx, app.ID, image)
		if err != nil {
			t.Fatalf("%s: %v", image, err)
		}
		if secret == nil {
			return nil
		}
		var config struct {
			Auths map[string]any `json:"auths"`
		}
		if err := json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config); err != nil {
			t.Fatal(err)
		}
		return config.Auths
	}

	got := auths("ghcr.io/acme/private-app:1.2.3")
	if len(got) != 1 || got["ghcr.io"] == nil || strings.Contains(mustJSON(t, got), "dckr_not_a_real_token") {
		t.Errorf("a ghcr.io image was given %v", got)
	}
	if got := auths("nginx:1.27"); len(got) != 1 || got["https://index.docker.io/v1/"] == nil {
		t.Errorf("a Docker Hub image was given %v", got)
	}
	if got := auths("quay.io/prometheus/prometheus:v3.0.0"); got != nil {
		t.Errorf("an image with no login was given %v", got)
	}

	// The panel's own external registry wins for its host, as it does for a
	// build, because that is where the panel pushed the image.
	for key, value := range map[string]string{
		settings.KeyRegistryURL: "ghcr.io/acme-builds", settings.KeyRegistryUser: "panel-bot",
	} {
		if err := db.SetSetting(ctx, key, value, false, "test"); err != nil {
			t.Fatal(err)
		}
	}
	sealed, err := d.keyring.Seal([]byte("panel_not_a_real_token"), settings.Context(settings.KeyRegistryPassword))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, settings.KeyRegistryPassword, sealed, true, "test"); err != nil {
		t.Fatal(err)
	}
	got = auths("ghcr.io/acme-builds/acme-shop-production/web:abc")
	encoded := mustJSON(t, got)
	if !strings.Contains(encoded, "panel-bot") || strings.Contains(encoded, "acme-bot") {
		t.Errorf("an image in the panel's registry was given %s", encoded)
	}
}

// A scan pod whose scanner cannot start says so, and one that is only slow
// does not.
func TestAScannerThatCannotStartIsNoticed(t *testing.T) {
	waiting := func(reason string) corev1.Pod {
		return corev1.Pod{Status: corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{{
			Name:  "scan",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: "pull access denied"}},
		}}}}
	}
	if got := scannerNotStarting(waiting("ImagePullBackOff")); !strings.Contains(got, "pull access denied") {
		t.Errorf("a scanner whose image will not pull reads as %q", got)
	}
	if got := scannerNotStarting(waiting("PodInitializing")); got != "" {
		t.Errorf("a scanner starting reads as %q", got)
	}

	timedOut := &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
		Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "DeadlineExceeded",
	}}}}
	if !jobFailed(timedOut) || !jobTimedOut(timedOut) {
		t.Error("a Job past its deadline is not read as one")
	}
	failed := &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
		Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded",
	}}}}
	if !jobFailed(failed) || jobTimedOut(failed) {
		t.Error("a Job that failed is read as one that timed out")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
