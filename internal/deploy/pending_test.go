package deploy

import (
	"reflect"
	"strings"
	"testing"

	"skifity/internal/api"
	"skifity/internal/store"
)

// What the app's page says is waiting for a deploy: the settings and
// build-time variables saved since the version it runs was built, and
// nothing a rollout already gave it.
func TestPendingChangesAreWhatTheRunningVersionWasNotBuiltWith(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	pending := func() []store.BuildChange {
		t.Helper()
		changes, err := d.PendingChanges(t.Context(), app.ID)
		if err != nil {
			t.Fatalf("PendingChanges: %v", err)
		}
		return changes
	}
	deploy := func() {
		t.Helper()
		deployment, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
		if err != nil {
			t.Fatalf("Deploy: %v", err)
		}
		markBuilt(t, db, deployment.ID, "registry/acme/web:"+deployment.ID)
	}

	if got := pending(); len(got) != 0 {
		t.Fatalf("an app never deployed is waiting for %+v", got)
	}
	setVariable(t, d, db, app.ID, "NEXT_PUBLIC_API", "https://api.example", true)
	setVariable(t, d, db, app.ID, "LOG_LEVEL", "info", false)
	deploy()
	if got := pending(); len(got) != 0 {
		t.Fatalf("straight after a deploy, %+v is waiting", got)
	}

	app.Branch, app.Port = "develop", 4000
	if err := db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	// A runtime variable is rolled out, never waiting.
	setVariable(t, d, db, app.ID, "LOG_LEVEL", "debug", false)
	if got, want := pending(), []store.BuildChange{{Setting: "branch", From: "main", To: "develop"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after a new branch and port, %+v is waiting, want %+v", got, want)
	}

	setVariable(t, d, db, app.ID, "NEXT_PUBLIC_API", "https://api2.example", true)
	got := pending()
	if len(got) != 2 || got[1] != (store.BuildChange{Setting: "variable", Key: "NEXT_PUBLIC_API"}) {
		t.Fatalf("after a new build-time value, %+v is waiting", got)
	}
	for _, change := range got {
		if strings.Contains(change.From+change.To, "api") {
			t.Errorf("a variable's value is in %+v", change)
		}
	}

	deploy()
	if got := pending(); len(got) != 0 {
		t.Fatalf("after the deploy that builds them, %+v is still waiting", got)
	}
}

// An image app's image is what it runs, and a new one waits for a deploy.
func TestAnImageAppsNewImageWaitsForADeploy(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	app.SourceType, app.Image = "image", "valkey/valkey:8"
	if err := db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	deployment, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	markBuilt(t, db, deployment.ID, "valkey/valkey:8")

	app.Image = "valkey/valkey:8.1"
	if err := db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	changes, err := d.PendingChanges(t.Context(), app.ID)
	want := []store.BuildChange{{Setting: "image", From: "valkey/valkey:8", To: "valkey/valkey:8.1"}}
	if err != nil || !reflect.DeepEqual(changes, want) {
		t.Fatalf("a new image is %+v, %v", changes, err)
	}
}

// A version deployed before what it was built from was recorded says nothing
// is waiting, rather than guessing.
func TestAVersionWithNoBuildSpecSaysNothingIsWaiting(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	old := store.Deployment{AppID: app.ID, Trigger: "push", CommitSHA: "abc", Image: "registry/acme/web:abc"}
	if err := db.CreateDeployment(t.Context(), &old); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDeploymentStatus(t.Context(), old.ID, store.DeploySucceeded, "", "", ""); err != nil {
		t.Fatal(err)
	}
	app.Branch, app.BuildCommand = "develop", "make"
	if err := db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	if changes, err := d.PendingChanges(t.Context(), app.ID); err != nil || len(changes) != 0 {
		t.Fatalf("a version with no record is waiting for %+v, %v", changes, err)
	}
}
