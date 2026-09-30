package deploy

import (
	"testing"

	"skifity/internal/api"
	"skifity/internal/store"
)

// A lock stops every deploy and rollback, wherever it comes from, because it
// is checked here and not by each caller.
func TestALockedAppIsNeitherDeployedNorRolledBack(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	first, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, Trigger: "manual"})
	if err != nil {
		t.Fatalf("an unlocked app could not be deployed: %v", err)
	}
	markBuilt(t, db, first.ID, "registry.local/acme/web:1")

	lock := store.DeployLock{AppID: app.ID, Reason: "incident 42: database failover", LockedBy: "oncall@example.test"}
	if err := db.LockDeploys(t.Context(), &lock); err != nil {
		t.Fatal(err)
	}
	for _, trigger := range []string{"manual", "push", "template"} {
		if _, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, Trigger: trigger}); problemCode(err) != "deploy.locked" {
			t.Fatalf("a %s deploy of a locked app answered %v", trigger, err)
		}
	}
	if _, err := d.Rollback(t.Context(), app.ID, first.ID, "someone"); problemCode(err) != "deploy.locked" {
		t.Fatalf("a rollback of a locked app answered %v", err)
	}

	// Lifting it is all it takes.
	if err := db.UnlockDeploys(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, Trigger: "manual"}); err != nil {
		t.Fatalf("an unlocked app could not be deployed: %v", err)
	}
}

// What a rollback would change, from what the deployment recorded.
func TestARollbackPlanNamesEachSettingItPutsBack(t *testing.T) {
	_, _, app, _ := testDeployer(t)
	recorded := `{"replicas":3,"autoscale":false,"min_replicas":1,"max_replicas":3,"cpu_target":75,` +
		`"cpu_request_m":50,"cpu_limit_m":1000,"mem_request_mb":128,"mem_limit_mb":1024,"port":8080,` +
		`"health_path":"/healthz","start_command":"","domains":["shop.example.test"]}`
	changes, err := store.RollbackChanges(app, recorded)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]store.SpecChange{}
	for _, change := range changes {
		got[change.Field] = change
	}
	if len(changes) != 4 || got["replicas"].To != "3" || got["mem_limit_mb"].From != "512" ||
		got["port"].To != "8080" || got["health_path"].To != "/healthz" {
		t.Fatalf("the plan is %+v", changes)
	}
	if none, _ := store.RollbackChanges(app, ""); len(none) != 0 {
		t.Fatalf("a deployment that recorded nothing would change %+v", none)
	}
}
