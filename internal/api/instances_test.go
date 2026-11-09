package api

import (
	"context"
	"encoding/json"
	"testing"

	"skifity/internal/store"
)

// versionedCluster reports one instance per deployment id it is given, the
// way the cluster client reads them off the pods' own annotations, all on the
// same image, as a variable-only redeploy leaves them.
type versionedCluster struct {
	Cluster
	instances []InstanceInfo
}

func (c versionedCluster) AppStatus(context.Context, string, string) (AppRuntimeStatus, error) {
	return AppRuntimeStatus{
		Phase: "updating", DesiredReplicas: len(c.instances), Image: "registry.local/web:a",
		Instances: append([]InstanceInfo(nil), c.instances...),
	}, nil
}

// Each instance says which deployment it runs, by its number.
//
// The image cannot say it: a variable-only redeploy runs the image the one
// before it built, every app from an image runs the same one each time, and a
// rollback is a newer deployment with an older image. Read by image, the old
// pods of a rollout and the new ones were the same version. The pod names its
// deployment, and that is what is believed — and only when it is one of this
// app's.
func TestEachInstanceNamesTheDeploymentItRuns(t *testing.T) {
	h := newHarness(t)
	owner := h.newTenant("acme")
	other := h.newTenant("globex")
	app := h.app(owner, "web")
	ctx := t.Context()

	deploy := func(appID, image string, rollbackOf int) store.Deployment {
		t.Helper()
		deployment := store.Deployment{
			AppID: appID, Image: image, Status: store.DeploySucceeded, RollbackOf: rollbackOf,
		}
		if err := h.db.CreateDeployment(ctx, &deployment); err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		return deployment
	}
	deploy(app.ID, "registry.local/web:a", 0)               // #1
	built := deploy(app.ID, "registry.local/web:b", 0)      // #2
	reconfigured := deploy(app.ID, built.Image, 0)          // #3: a variable changed
	rolledBack := deploy(app.ID, "registry.local/web:a", 1) // #4: back to #1's image
	theirs := deploy(h.app(other, "web").ID, "registry.local/web:a", 0)

	h.withCluster(versionedCluster{instances: []InstanceInfo{
		{Name: "web-old", DeploymentID: reconfigured.ID},
		{Name: "web-new", DeploymentID: rolledBack.ID},
		{Name: "web-also-new", DeploymentID: rolledBack.ID},
		{Name: "web-theirs", DeploymentID: theirs.ID},
		{Name: "web-unknown", DeploymentID: "dep_gone"},
		{Name: "web-unmarked"},
	}})

	code, body := h.do(owner, "GET", "/api/apps/"+app.ID+"/status", nil)
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var got AppRuntimeStatus
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]struct {
		number int
		id     string
	}{
		"web-old":      {3, reconfigured.ID},
		"web-new":      {4, rolledBack.ID},
		"web-also-new": {4, rolledBack.ID},
		// Another app's deployment is not this app's version, and its id is
		// not passed on.
		"web-theirs":   {0, ""},
		"web-unknown":  {0, ""},
		"web-unmarked": {0, ""},
	}
	if len(got.Instances) != len(want) {
		t.Fatalf("got %d instances, want %d: %s", len(got.Instances), len(want), body)
	}
	for _, instance := range got.Instances {
		expected := want[instance.Name]
		if instance.Deployment != expected.number || instance.DeploymentID != expected.id {
			t.Errorf("%s runs #%d (%q), want #%d (%q)", instance.Name,
				instance.Deployment, instance.DeploymentID, expected.number, expected.id)
		}
	}
}
