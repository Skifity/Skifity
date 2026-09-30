package deploy

import (
	"testing"

	"skifity/internal/store"
)

// A preview of a pull request from a fork runs code anybody could have
// written. It never copied the app's own secrets, and it was still handed the
// project's shared ones at every deploy, because nothing recorded where the
// preview came from.

func shareVariable(t *testing.T, d *Deployer, db *store.DB, projectID, key, value string, secret bool) {
	t.Helper()
	sealed, err := d.keyring.Seal([]byte(value), "shared_variable:"+projectID+":"+key)
	if err != nil {
		t.Fatal(err)
	}
	v := store.SharedVariable{ProjectID: projectID, Key: key, IsSecret: secret}
	if err := db.SetSharedVariable(t.Context(), &v, sealed); err != nil {
		t.Fatalf("SetSharedVariable: %v", err)
	}
}

func TestAForkPreviewGetsNoSharedSecrets(t *testing.T) {
	d, db, app, env := testDeployer(t)
	shareVariable(t, d, db, env.ProjectID, "STRIPE_SECRET_KEY", "sk_live_production", true)
	shareVariable(t, d, db, env.ProjectID, "SUPPORT_EMAIL", "help@example.test", false)

	fork := store.Environment{
		ProjectID: env.ProjectID, Name: "Pull request #9", Slug: "pr-9", Kind: store.EnvPreview,
		SourceRef: "pr-9", Namespace: "acme-shop-pr-9", FromFork: true,
	}
	if err := db.CreateEnvironment(t.Context(), &fork); err != nil {
		t.Fatal(err)
	}
	// Read back, so the flag is what the database kept rather than what the
	// test set.
	stored, err := db.GetEnvironment(t.Context(), fork.ID)
	if err != nil || !stored.FromFork {
		t.Fatalf("the environment did not keep that it came from a fork: %v", err)
	}

	variables, err := d.runtimeVariables(t.Context(), app, stored)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := variables["STRIPE_SECRET_KEY"]; ok {
		t.Fatal("a fork's preview was handed the project's shared secret")
	}
	if variables["SUPPORT_EMAIL"] != "help@example.test" {
		t.Errorf("a plain shared setting was withheld too: %v", variables)
	}

	// Every other environment still gets both.
	variables, err = d.runtimeVariables(t.Context(), app, env)
	if err != nil {
		t.Fatal(err)
	}
	if variables["STRIPE_SECRET_KEY"] != "sk_live_production" || variables["SUPPORT_EMAIL"] != "help@example.test" {
		t.Errorf("production lost a shared variable: %v", variables)
	}
}
