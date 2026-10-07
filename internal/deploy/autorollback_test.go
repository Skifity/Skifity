package deploy

import (
	"testing"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// An app with a volume cannot be updated by starting the new version beside the
// old: two instances writing to one volume would corrupt it. So its old instance
// is stopped first, and a version that does not start is an app that is down.

// failedRollout makes a good version, then a version that fails the way a
// rollout does, and returns both.
func failedRollout(t *testing.T, d *Deployer, db *store.DB, app store.App) (good, bad store.Deployment) {
	t.Helper()
	good, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "aaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	markBuilt(t, db, good.ID, "registry/acme/web:good")

	bad, err = d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "bbbbbbbbbbbb"})
	if err != nil {
		t.Fatal(err)
	}
	// With no cluster the second deploy has already failed, for want of one.
	bad = waitFinished(t, db, bad.ID)
	return good, bad
}

func rollbacksOf(t *testing.T, db *store.DB, appID string) []store.Deployment {
	t.Helper()
	all, err := db.ListDeployments(t.Context(), appID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Deployment
	for _, deployment := range all {
		if deployment.Trigger == "rollback" {
			out = append(out, deployment)
		}
	}
	return out
}

func TestAnAppWithAVolumeIsPutBackWhenItsNewVersionDoesNotStart(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	if err := db.CreateVolume(t.Context(), &store.Volume{AppID: app.ID, Name: "data", MountPath: "/data", SizeGB: 1}); err != nil {
		t.Fatal(err)
	}
	good, bad := failedRollout(t, d, db, app)

	d.fail(t.Context(), bad, errdoc.RolloutTimedOut(app.Name, 0, 1, "it never became ready"))

	rolled := rollbacksOf(t, db, app.ID)
	if len(rolled) != 1 || rolled[0].RollbackOf != good.Number || rolled[0].Image != "registry/acme/web:good" {
		t.Fatalf("the app was not put back on the version that worked: %+v", rolled)
	}
}

// Most apps are still serving: a rolling update stops the old instances only
// once the new are ready, so a version that does not start changes nothing, and
// putting a version back would be churn.
func TestAnAppThatIsStillServingIsLeftAlone(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	good, bad := failedRollout(t, d, db, app)
	_ = good

	d.fail(t.Context(), bad, errdoc.RolloutTimedOut(app.Name, 0, 1, "it never became ready"))

	if rolled := rollbacksOf(t, db, app.ID); len(rolled) != 0 {
		t.Fatalf("an app with nothing to be left down by was rolled back: %+v", rolled)
	}
}

// A rollback that fails is not rolled back, which would be a loop, and a
// failure that is not a rollout's (nothing built, nothing started) leaves the
// old version where it was.
func TestARollbackThatFailsIsNotRolledBackAndNeitherIsABuildThatFailed(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	if err := db.CreateVolume(t.Context(), &store.Volume{AppID: app.ID, Name: "data", MountPath: "/data", SizeGB: 1}); err != nil {
		t.Fatal(err)
	}
	_, bad := failedRollout(t, d, db, app)

	d.fail(t.Context(), bad, errdoc.BadRequest("the build failed"))
	if rolled := rollbacksOf(t, db, app.ID); len(rolled) != 0 {
		t.Fatalf("a build that never produced an image rolled the app back: %+v", rolled)
	}

	bad.Trigger = "rollback"
	d.fail(t.Context(), bad, errdoc.RolloutTimedOut(app.Name, 0, 1, "it never became ready"))
	if rolled := rollbacksOf(t, db, app.ID); len(rolled) != 0 {
		t.Fatalf("a rollback that failed was itself rolled back: %+v", rolled)
	}
}

// Nothing to go back to is not an error.
func TestAnAppWithNoVersionThatWorkedIsNotRolledBack(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	if err := db.CreateVolume(t.Context(), &store.Volume{AppID: app.ID, Name: "data", MountPath: "/data", SizeGB: 1}); err != nil {
		t.Fatal(err)
	}
	first, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "cccccccccccc"})
	if err != nil {
		t.Fatal(err)
	}
	first = waitFinished(t, db, first.ID)
	d.fail(t.Context(), first, errdoc.RolloutTimedOut(app.Name, 0, 1, "it never became ready"))
	if rolled := rollbacksOf(t, db, app.ID); len(rolled) != 0 {
		t.Fatalf("a first deploy was rolled back to nothing: %+v", rolled)
	}
}
