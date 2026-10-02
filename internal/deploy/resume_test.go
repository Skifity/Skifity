package deploy

import (
	"strings"
	"testing"
	"time"

	"skifity/internal/store"
)

// A push that landed while the panel was being upgraded used to be lost: the
// deployment was failed on the next start with "deploy again". It is picked up
// again instead, once, and an older one the newer had replaced is marked so.
func TestAnInterruptedDeploymentIsPickedUpAgainOnce(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	ctx := t.Context()

	older := store.Deployment{AppID: app.ID, Status: store.DeployQueued, Trigger: "manual"}
	if err := db.CreateDeployment(ctx, &older); err != nil {
		t.Fatalf("create the older deployment: %v", err)
	}
	newer := store.Deployment{AppID: app.ID, Status: store.DeployBuilding, Trigger: "push"}
	if err := db.CreateDeployment(ctx, &newer); err != nil {
		t.Fatalf("create the newer deployment: %v", err)
	}
	finished := store.Deployment{AppID: app.ID, Status: store.DeploySucceeded, Trigger: "manual"}
	if err := db.CreateDeployment(ctx, &finished); err != nil {
		t.Fatalf("create a finished deployment: %v", err)
	}

	resumed, err := d.ResumeInterrupted(ctx)
	if err != nil {
		t.Fatalf("ResumeInterrupted: %v", err)
	}
	if resumed != 1 {
		t.Fatalf("resumed %d deployments, want the newest one only", resumed)
	}

	if got := statusOf(t, db, older.ID); got != store.DeploySuperseded {
		t.Errorf("the older deployment is %q; the newer one had replaced it, so it should be superseded", got)
	}
	if got := statusOf(t, db, finished.ID); got != store.DeploySucceeded {
		t.Errorf("a deployment that had finished was changed to %q", got)
	}

	// Started again: with no cluster here its build cannot run, so it fails —
	// but for that reason, and not for having been interrupted.
	again := waitFinished(t, db, newer.ID)
	if again.ErrorCode == "deploy.interrupted" {
		t.Fatal("the deployment was failed for the restart instead of being started again")
	}
	if !logContains(t, db, newer.ID, "picks up again from the build") {
		t.Error("the deployment's log does not say it was picked up again, and from where")
	}

	// Interrupted a second time: not started a third.
	if _, err := db.Exec(ctx, `UPDATE deployments SET status = 'deploying' WHERE id = ?`, newer.ID); err != nil {
		t.Fatalf("put the deployment back in progress: %v", err)
	}
	resumed, err = d.ResumeInterrupted(ctx)
	if err != nil {
		t.Fatalf("ResumeInterrupted, the second time: %v", err)
	}
	if resumed != 0 {
		t.Fatalf("a deployment interrupted twice was started again")
	}
	twice, err := db.GetDeployment(ctx, newer.ID)
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if twice.Status != store.DeployFailed || twice.ErrorCode != "deploy.interrupted" {
		t.Fatalf("a deployment interrupted twice is %q (%q), want failed with deploy.interrupted",
			twice.Status, twice.ErrorCode)
	}
	if twice.ErrorMessage == "" || twice.ErrorHint == "" {
		t.Error("the failure has no explanation or no fix")
	}
}

// The line in the log says where it picks up: an image that was already built
// is not built again.
func TestResumeSaysWhereItPicksUp(t *testing.T) {
	cases := []struct {
		deployment store.Deployment
		want       string
	}{
		{store.Deployment{Status: store.DeployQueued}, "from the start"},
		{store.Deployment{Status: store.DeployBuilding}, "from the build"},
		{store.Deployment{Status: store.DeployDeploying, Image: "registry/app:7"}, "from the rollout"},
	}
	for _, c := range cases {
		if got := resumeNote(c.deployment); !strings.Contains(got, c.want) {
			t.Errorf("a %s deployment: %q does not say %q", c.deployment.Status, got, c.want)
		}
	}
}

func statusOf(t *testing.T, db *store.DB, id string) store.DeploymentStatus {
	t.Helper()
	deployment, err := db.GetDeployment(t.Context(), id)
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	return deployment.Status
}

func waitFinished(t *testing.T, db *store.DB, id string) store.Deployment {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		deployment, err := db.GetDeployment(t.Context(), id)
		if err != nil {
			t.Fatalf("GetDeployment: %v", err)
		}
		if deployment.Status.Terminal() {
			return deployment
		}
		if time.Now().After(deadline) {
			t.Fatalf("the deployment was still %s after 10 seconds", deployment.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func logContains(t *testing.T, db *store.DB, id, text string) bool {
	t.Helper()
	lines, err := db.ListBuildLogs(t.Context(), id, 0, 1000)
	if err != nil {
		t.Fatalf("ListBuildLogs: %v", err)
	}
	for _, line := range lines {
		if strings.Contains(line.Line, text) {
			return true
		}
	}
	return false
}
