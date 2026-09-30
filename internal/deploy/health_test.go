package deploy

import (
	"testing"
	"time"

	"skifity/internal/api"
	"skifity/internal/kube"
)

// A health check is runtime configuration: changing one reuses the image and
// never rebuilds (ADR-0007).
func TestAHealthCheckChangeIsARolloutNotABuild(t *testing.T) {
	d, db, app, _ := testDeployer(t)

	first, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	markBuilt(t, db, first.ID, "registry/acme/web:abc123def456")

	app.HealthPath = "/ready"
	app.HealthCheck = kube.HealthHTTP
	app.HealthStartSeconds = 900
	app.HealthTimeoutSeconds = 20
	if err := db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}

	second, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if second.BuildFingerprint != first.BuildFingerprint {
		t.Fatal("a health check change changed the build fingerprint, so it would rebuild")
	}
	if second.Image != "registry/acme/web:abc123def456" {
		t.Fatalf("a health check change did not reuse the image: %q", second.Image)
	}
}

// Rolling back puts back how the version's instances were checked: a check
// changed to one the app cannot pass is a bad configuration change like any
// other.
func TestRollbackRestoresTheHealthCheck(t *testing.T) {
	d, db, app, _ := testDeployer(t)

	app.HealthPath = "/healthz"
	app.HealthCheck = kube.HealthHTTP
	app.HealthStartSeconds = 600
	app.HealthTimeoutSeconds = 10
	if err := db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	good, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "aaaaaaaaaaaa"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	markBuilt(t, db, good.ID, "registry/acme/web:good")

	app.HealthCheck = kube.HealthNone
	app.HealthStartSeconds = 30
	app.HealthTimeoutSeconds = 1
	if err := db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}

	if _, err := d.Rollback(t.Context(), app.ID, good.ID, "usr_1"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	restored, err := db.GetApp(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if restored.HealthCheck != kube.HealthHTTP || restored.HealthStartSeconds != 600 || restored.HealthTimeoutSeconds != 10 {
		t.Fatalf("the health check was not put back: %q, %ds, %ds",
			restored.HealthCheck, restored.HealthStartSeconds, restored.HealthTimeoutSeconds)
	}
}

// A version recorded before the health check could be chosen ran with the
// defaults, and rolling back to it puts those back — not zeroes, which would
// be an app with no time to start.
func TestRollingBackToAnOlderRecordPutsTheDefaultsBack(t *testing.T) {
	d, db, app, _ := testDeployer(t)

	good, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "aaaaaaaaaaaa"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	markBuilt(t, db, good.ID, "registry/acme/web:good")
	if _, err := db.Exec(t.Context(), `UPDATE deployments SET runtime_spec = ? WHERE id = ?`,
		`{"replicas":1,"port":3000,"health_path":"","start_command":""}`, good.ID); err != nil {
		t.Fatal(err)
	}

	app.HealthCheck = kube.HealthNone
	app.HealthStartSeconds = 900
	if err := db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if _, err := d.Rollback(t.Context(), app.ID, good.ID, "usr_1"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	restored, err := db.GetApp(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if restored.HealthCheck != kube.HealthTCP ||
		restored.HealthStartSeconds != kube.DefaultHealthStartSeconds ||
		restored.HealthTimeoutSeconds != kube.DefaultHealthTimeoutSeconds {
		t.Fatalf("an old version came back as %q, %ds, %ds; want what it ran with",
			restored.HealthCheck, restored.HealthStartSeconds, restored.HealthTimeoutSeconds)
	}
}

// A deployment waits at least as long as the app is allowed to start, or it
// gives up on an instance still inside its budget.
func TestADeploymentWaitsAsLongAsTheAppMayTakeToStart(t *testing.T) {
	if got := rolloutWait(kube.AppSpec{}); got != 10*time.Minute {
		t.Errorf("an app on the default budget waits %s, not the ten minutes it always did", got)
	}
	for _, budget := range []int{kube.DefaultHealthStartSeconds, 600, kube.MaxHealthStartSeconds} {
		wait := rolloutWait(kube.AppSpec{HealthStartSeconds: budget})
		if wait <= time.Duration(budget)*time.Second {
			t.Errorf("an app allowed %ds to start is waited for only %s", budget, wait)
		}
	}
	// The bound on a whole deploy grows by at least as much as the wait does,
	// so the slowest start is not cut off by it after a slow build.
	extraWait := rolloutWait(kube.AppSpec{HealthStartSeconds: kube.MaxHealthStartSeconds}) - rolloutWait(kube.AppSpec{})
	if maxDeployDuration-45*time.Minute < extraWait {
		t.Errorf("a whole deploy is bounded at %s, which does not leave room for the slowest start", maxDeployDuration)
	}
}

// The readiness checker says so when an app is not checked at all.
func TestScalingReadinessNoticesTheHealthCheckIsOff(t *testing.T) {
	for check, want := range map[string]string{
		kube.HealthNone: "health_check_off",
		kube.HealthTCP:  "no_health_check",
		kube.HealthHTTP: "",
	} {
		d, db, app, _ := testDeployer(t)
		app.HealthPath = "/healthz"
		app.HealthCheck = check
		if err := db.UpdateApp(t.Context(), &app); err != nil {
			t.Fatalf("UpdateApp: %v", err)
		}
		findings, err := d.ScalingReadiness(t.Context(), app.ID)
		if err != nil {
			t.Fatalf("ScalingReadiness: %v", err)
		}
		codes := map[string]api.ScalingFinding{}
		for _, f := range findings {
			codes[f.Code] = f
		}
		for _, code := range []string{"health_check_off", "no_health_check"} {
			if _, found := codes[code]; found != (code == want) {
				t.Errorf("check %q: %s reported %t, want %t", check, code, found, code == want)
			}
		}
		if f, ok := codes[want]; want != "" && (!ok || f.Fix == "") {
			t.Errorf("check %q: %s has no fix", check, want)
		}
	}
}
