package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/cli"
	"skifity/internal/store"
)

// A build-time variable is baked into the image, and the panel says "the app
// is being rebuilt" when one is saved. It used to say so and queue nothing.

// rebuildDeployer keeps the deploys and rollouts it was asked for, and says
// what waits for a deploy.
type rebuildDeployer struct {
	fakeDeployer
	requests []DeployRequest
	syncs    int
	pending  []store.BuildChange
}

func (d *rebuildDeployer) Deploy(_ context.Context, req DeployRequest) (store.Deployment, error) {
	d.requests = append(d.requests, req)
	return store.Deployment{ID: "dep_rebuild", AppID: req.AppID, Number: 3, Trigger: req.Trigger, Status: store.DeployQueued}, nil
}

func (d *rebuildDeployer) Sync(context.Context, string) error {
	d.syncs++
	return nil
}

func (d *rebuildDeployer) PendingChanges(context.Context, string) ([]store.BuildChange, error) {
	return d.pending, nil
}

// deployedApp is an app built from a repository that runs commit abc123.
func deployedApp(t *testing.T) (*harness, tenant, store.App, *rebuildDeployer) {
	t.Helper()
	h := newHarness(t)
	deployer := &rebuildDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}
	h.api.deployer = deployer
	acme := h.newTenant("acme")
	app := store.App{EnvironmentID: acme.env.ID, Name: "web", Slug: "web", SourceType: "git",
		RepoURL: "https://github.com/acme/web", Branch: "main", Replicas: 1}
	if err := h.db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	live := store.Deployment{AppID: app.ID, Trigger: "push", CommitSHA: "abc123", Image: "registry.local/web:abc123"}
	if err := h.db.CreateDeployment(t.Context(), &live); err != nil {
		t.Fatal(err)
	}
	if err := h.db.UpdateDeploymentStatus(t.Context(), live.ID, store.DeploySucceeded, "", "", ""); err != nil {
		t.Fatal(err)
	}
	return h, acme, app, deployer
}

type variableAnswer struct {
	RequiresRebuild bool              `json:"requires_rebuild"`
	Deployment      *store.Deployment `json:"deployment"`
	NotRebuilt      string            `json:"not_rebuilt"`
}

func TestABuildTimeVariableRebuildsTheVersionTheAppRuns(t *testing.T) {
	h, acme, app, deployer := deployedApp(t)

	status, body := h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/variables",
		map[string]any{"key": "NEXT_PUBLIC_API", "value": "https://api.example", "build_time": true})
	var answer variableAnswer
	decodeInto(t, body, &answer)
	if status != http.StatusOK || !answer.RequiresRebuild || answer.Deployment == nil || answer.NotRebuilt != "" {
		t.Fatalf("answered %d %s", status, body)
	}
	if len(deployer.requests) != 1 {
		t.Fatalf("%d deploys were queued, want one", len(deployer.requests))
	}
	if req := deployer.requests[0]; req.CommitSHA != "abc123" || req.Trigger != "variables" || req.AppID != app.ID || req.Force {
		t.Errorf("the rebuild asked for %+v; it should build the commit the app runs", req)
	}
	if deployer.syncs != 0 {
		t.Errorf("the old image was rolled out %d times with a value it was not built with", deployer.syncs)
	}

	// A runtime variable beside it in a batch arrives now, and the build
	// is still one.
	status, body = h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/variables/batch", map[string]any{
		"set": []map[string]any{
			{"key": "NEXT_PUBLIC_API", "value": "https://api2.example"},
			{"key": "LOG_LEVEL", "value": "debug"},
		},
	})
	decodeInto(t, body, &answer)
	if status != http.StatusOK || !answer.RequiresRebuild || len(deployer.requests) != 2 || deployer.syncs != 1 {
		t.Fatalf("a mixed batch answered %d %s, with %d deploys and %d rollouts", status, body, len(deployer.requests), deployer.syncs)
	}

	// Removing it takes it out of the running instances, as removing any
	// variable does, and out of the image, which is a build too.
	status, body = h.do(acme, http.MethodDelete, "/api/apps/"+app.ID+"/variables/NEXT_PUBLIC_API", nil)
	decodeInto(t, body, &answer)
	if status != http.StatusOK || !answer.RequiresRebuild || len(deployer.requests) != 3 || deployer.syncs != 2 {
		t.Fatalf("removing a build-time variable answered %d %s", status, body)
	}
	// A runtime one only rolls out.
	answer = variableAnswer{}
	status, body = h.do(acme, http.MethodDelete, "/api/apps/"+app.ID+"/variables/LOG_LEVEL", nil)
	decodeInto(t, body, &answer)
	if status != http.StatusOK || answer.RequiresRebuild || len(deployer.requests) != 3 || deployer.syncs != 3 {
		t.Fatalf("removing a runtime variable answered %d %s", status, body)
	}
}

// A lock means nothing ships, and a rebuild is shipping: the value waits, and
// the answer says why rather than that a rebuild is under way.
func TestALockedAppIsNotRebuiltAndSaysWhy(t *testing.T) {
	h, acme, app, deployer := deployedApp(t)
	lock := store.DeployLock{AppID: app.ID, Reason: "launch", LockedBy: "ada@example.test"}
	if err := h.db.LockDeploys(t.Context(), &lock); err != nil {
		t.Fatal(err)
	}
	status, body := h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/variables",
		map[string]any{"key": "NEXT_PUBLIC_API", "value": "https://api.example", "build_time": true})
	var answer variableAnswer
	decodeInto(t, body, &answer)
	if status != http.StatusOK || answer.RequiresRebuild || answer.NotRebuilt != "locked" || len(deployer.requests) != 0 {
		t.Fatalf("a locked app answered %d %s and queued %d deploys", status, body, len(deployer.requests))
	}
}

// An image app builds nothing, so a variable it calls build-time is only
// ever read when it runs: it rolls out.
func TestAnImageAppRollsOutWhatItCallsBuildTime(t *testing.T) {
	h, acme, app, deployer := deployedApp(t)
	app.SourceType, app.Image, app.CPURequestM, app.MemRequestMB = "image", "nginx:1.27", 50, 64
	if err := h.db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	status, body := h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/variables",
		map[string]any{"key": "GREETING", "value": "hello", "build_time": true})
	var answer variableAnswer
	decodeInto(t, body, &answer)
	if status != http.StatusOK || answer.RequiresRebuild || answer.NotRebuilt != "" || len(deployer.requests) != 0 || deployer.syncs != 1 {
		t.Fatalf("an image app answered %d %s, with %d deploys and %d rollouts", status, body, len(deployer.requests), deployer.syncs)
	}
}

// What waits for a deploy is on the status, with or without a cluster, and
// names a variable without its value.
func TestTheStatusSaysWhatWaitsForADeploy(t *testing.T) {
	h, acme, app, deployer := deployedApp(t)
	deployer.pending = []store.BuildChange{
		{Setting: "build_command", From: "npm run build", To: "npm run build:prod"},
		{Setting: "variable", Key: "NEXT_PUBLIC_API"},
	}
	status, body := h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/status", nil)
	var answer AppRuntimeStatus
	if err := json.Unmarshal([]byte(body), &answer); err != nil || status != http.StatusOK {
		t.Fatalf("status answered %d %s", status, body)
	}
	if len(answer.PendingChanges) != 2 || answer.PendingChanges[1].Key != "NEXT_PUBLIC_API" {
		t.Errorf("the status says %+v is waiting", answer.PendingChanges)
	}

	// The CLI says it under the instances, a variable by its name alone.
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("SKIFITY_URL", h.server.URL)
	t.Setenv("SKIFITY_TOKEN", acme.token)
	var out, errs bytes.Buffer
	if code := cli.Run(t.Context(), []string{"status", "--app", app.ID}, &out, &errs); code != 0 ||
		!strings.Contains(out.String(), "build_command: npm run build -> npm run build:prod") ||
		!strings.Contains(out.String(), "the build-time variable NEXT_PUBLIC_API") {
		t.Errorf("skifity status exited %d:\n%s%s", code, out.String(), errs.String())
	}
	out.Reset()
	if code := cli.Run(t.Context(), []string{"env", "set", "NEXT_PUBLIC_API=https://api.example", "--build", "--app", app.ID}, &out, &errs); code != 0 ||
		!strings.Contains(out.String(), "being rebuilt with it: deployment #3") {
		t.Errorf("skifity env set --build exited %d:\n%s%s", code, out.String(), errs.String())
	}

	// Nothing waiting is nothing said.
	deployer.pending = []store.BuildChange{}
	_, body = h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/status", nil)
	if strings.Contains(body, "pending_changes") {
		t.Errorf("nothing waiting was answered as %s", body)
	}
}
