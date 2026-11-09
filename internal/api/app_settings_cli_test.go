package api

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"skifity/internal/cli"
	"skifity/internal/store"
)

// Changing an app from the CLI, through the real API: its settings, a
// restart, cancelling a deploy, deleting it, and a rollback that says what it
// puts back before it does it.

// settingsDeployer remembers what it was asked to apply, cancel and roll back.
type settingsDeployer struct {
	recordingDeployer
	db        *store.DB
	syncs     []string
	cancelled []string
}

func (d *settingsDeployer) Sync(_ context.Context, appID string) error {
	d.syncs = append(d.syncs, appID)
	return nil
}

func (d *settingsDeployer) Cancel(_ context.Context, deploymentID string) error {
	d.cancelled = append(d.cancelled, deploymentID)
	return nil
}

func (d *settingsDeployer) Rollback(ctx context.Context, appID, deploymentID, _ string) (store.Deployment, error) {
	previous, err := d.db.GetDeployment(ctx, deploymentID)
	if err != nil {
		return store.Deployment{}, err
	}
	deployment := store.Deployment{AppID: appID, Trigger: "rollback", RollbackOf: previous.Number, Image: previous.Image}
	err = d.db.CreateDeployment(ctx, &deployment)
	return deployment, err
}

// restartRecorder is a cluster that remembers what it restarted and deleted.
type restartRecorder struct {
	Cluster
	restarted, deleted *[]string
}

func (c restartRecorder) RestartApp(_ context.Context, namespace, name string) error {
	*c.restarted = append(*c.restarted, namespace+"/"+name)
	return nil
}

func (c restartRecorder) DeleteApp(_ context.Context, namespace, name string) error {
	*c.deleted = append(*c.deleted, namespace+"/"+name)
	return nil
}

type settingsRun struct {
	h                  *harness
	acme               tenant
	app                store.App
	deployer           *settingsDeployer
	restarted, deleted []string
}

// newSettingsRun is a panel with one app that has deployed twice, and the CLI
// pointed at it with the owner's token.
func newSettingsRun(t *testing.T) *settingsRun {
	t.Helper()
	h := newHarness(t)
	run := &settingsRun{h: h, acme: h.newTenant("acme")}
	run.deployer = &settingsDeployer{recordingDeployer: recordingDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}, db: h.db}
	h.api.deployer = run.deployer
	h.api.cluster = restartRecorder{restarted: &run.restarted, deleted: &run.deleted}

	run.app = store.App{EnvironmentID: run.acme.env.ID, Name: "web", Slug: "web", SourceType: "git",
		RepoURL: "https://github.com/acme/web", Branch: "main", Port: 8080, Replicas: 1,
		CPURequestM: 100, MemRequestMB: 256}
	if err := h.db.CreateApp(t.Context(), &run.app); err != nil {
		t.Fatal(err)
	}
	for _, port := range []string{"3000", "8080"} {
		deployment := store.Deployment{AppID: run.app.ID, Trigger: "push", CommitSHA: "abc" + port,
			Image: "registry.local/web:" + port, RuntimeSpec: `{"replicas":1,"port":` + port + `,"cpu_request_m":100,"mem_request_mb":256}`}
		if err := h.db.CreateDeployment(t.Context(), &deployment); err != nil {
			t.Fatal(err)
		}
		if err := h.db.UpdateDeploymentStatus(t.Context(), deployment.ID, store.DeploySucceeded, "", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	run.as(t, run.acme)
	return run
}

// as points the CLI at the panel with this tenant's token.
func (r *settingsRun) as(t *testing.T, who tenant) {
	t.Helper()
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("SKIFITY_URL", r.h.server.URL)
	t.Setenv("SKIFITY_TOKEN", who.token)
}

func (r *settingsRun) cli(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	code := cli.Run(t.Context(), args, &out, &errs)
	return out.String() + errs.String(), code
}

func (r *settingsRun) deployments(t *testing.T) int {
	t.Helper()
	list, err := r.h.db.ListDeployments(t.Context(), r.app.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

// The fixes a first deploy needs are a command away, and each says when it
// takes effect: the port is rolled out there and then, without a deployment;
// a build command waits for the next deploy, and says so.
func TestAppsSetRollsOutWhatItCanAndSaysWhatWaitsForADeploy(t *testing.T) {
	r := newSettingsRun(t)
	before := r.deployments(t)

	out, code := r.cli(t, "apps", "set", "--app", r.app.ID, "--port", "3000")
	if code != 0 {
		t.Fatalf("apps set --port exited %d: %s", code, out)
	}
	if !strings.Contains(out, "Rolled out now") || !strings.Contains(out, "port") || strings.Contains(out, "Waiting for the next deploy") {
		t.Errorf("a new port was reported as: %s", out)
	}
	if app, _ := r.h.db.GetApp(t.Context(), r.app.ID); app.Port != 3000 {
		t.Errorf("the port is %d", app.Port)
	}
	if !slices.Contains(r.deployer.syncs, r.app.ID) {
		t.Error("the new port was not applied to the running app")
	}
	if len(r.deployer.requests) != 0 || r.deployments(t) != before {
		t.Error("changing the port started a deployment")
	}

	out, code = r.cli(t, "apps", "set", "--app", r.app.ID, "--build", "npm run build", "--json")
	if code != 0 {
		t.Fatalf("apps set --build exited %d: %s", code, out)
	}
	if !strings.Contains(out, `"next_deploy": [`) || !strings.Contains(out, `"build_command"`) || !strings.Contains(out, `"rolled_out": []`) {
		t.Errorf("a new build command was reported as: %s", out)
	}
	out, _ = r.cli(t, "apps", "set", "--app", r.app.ID, "--build", "npm run build:prod", "--start", "node server.js")
	if !strings.Contains(out, "Waiting for the next deploy: build_command") || !strings.Contains(out, "Rolled out now, no rebuild: start_command") {
		t.Errorf("two settings were reported as: %s", out)
	}
	if len(r.deployer.requests) != 0 {
		t.Error("a settings change started a deployment")
	}

	// Nothing given is nothing sent.
	if out, code := r.cli(t, "apps", "set", "--app", r.app.ID); code == 0 {
		t.Errorf("apps set with no setting exited 0: %s", out)
	}
}

// On an app that has never run, a runtime setting has nothing to roll out to,
// so the answer says the first deploy brings it.
func TestASettingOfAnAppNeverDeployedWaitsForItsFirstDeploy(t *testing.T) {
	r := newSettingsRun(t)
	fresh := store.App{EnvironmentID: r.acme.env.ID, Name: "fresh", Slug: "fresh", Replicas: 1, CPURequestM: 100, MemRequestMB: 256}
	if err := r.h.db.CreateApp(t.Context(), &fresh); err != nil {
		t.Fatal(err)
	}
	status, body := r.h.do(r.acme, http.MethodPatch, "/api/apps/"+fresh.ID, map[string]any{"port": 3000})
	if status != http.StatusOK {
		t.Fatalf("PATCH answered %d: %s", status, body)
	}
	var answer appUpdate
	decodeInto(t, body, &answer)
	if len(answer.RolledOut) != 0 || !slices.Equal(answer.NextDeploy, []string{"port"}) || answer.Port != 3000 || answer.ID != fresh.ID {
		t.Errorf("the answer was %s", body)
	}
}

func TestRestartAndCancelReachThePanel(t *testing.T) {
	r := newSettingsRun(t)

	if out, code := r.cli(t, "restart", "--app", r.app.ID); code != 0 || !slices.Equal(r.restarted, []string{r.acme.env.Namespace + "/web"}) {
		t.Fatalf("restart exited %d (%s) and restarted %v", code, out, r.restarted)
	}

	// Nothing under way is said, not guessed at.
	if out, code := r.cli(t, "deploy", "--app", r.app.ID, "--cancel"); code == 0 || len(r.deployer.cancelled) != 0 {
		t.Errorf("cancelling with nothing deploying exited %d: %s", code, out)
	}
	building := store.Deployment{AppID: r.app.ID, Trigger: "push", CommitSHA: "def456"}
	if err := r.h.db.CreateDeployment(t.Context(), &building); err != nil {
		t.Fatal(err)
	}
	if err := r.h.db.UpdateDeploymentStatus(t.Context(), building.ID, store.DeployBuilding, "", "", ""); err != nil {
		t.Fatal(err)
	}
	out, code := r.cli(t, "deploy", "--app", r.app.ID, "--cancel")
	if code != 0 || !slices.Equal(r.deployer.cancelled, []string{building.ID}) || !strings.Contains(out, "#3 is cancelled") {
		t.Fatalf("deploy --cancel exited %d (%s) and cancelled %v", code, out, r.deployer.cancelled)
	}
	// By its number, as the history prints it.
	if out, code := r.cli(t, "deploy", "--cancel", "#3", "--app", r.app.ID); code != 0 || len(r.deployer.cancelled) != 2 {
		t.Errorf("deploy --cancel #3 exited %d: %s", code, out)
	}
}

// Deleting takes the disks with it, so it is never done by a script that did
// not say --yes, and the panel's own refusal reaches whoever is not allowed.
func TestAppsRmAsksOrNeedsYes(t *testing.T) {
	r := newSettingsRun(t)

	out, code := r.cli(t, "apps", "rm", "web", "--env", r.acme.env.ID)
	if code == 0 || !strings.Contains(out, "cli.delete_unconfirmed") {
		t.Fatalf("apps rm with no terminal and no --yes exited %d: %s", code, out)
	}
	if _, err := r.h.db.GetApp(t.Context(), r.app.ID); err != nil || len(r.deleted) != 0 {
		t.Fatal("the app was deleted without being confirmed")
	}

	viewer := r.h.newMember(r.acme, "vera", store.RoleViewer)
	r.as(t, viewer)
	out, code = r.cli(t, "apps", "rm", "web", "--env", r.acme.env.ID, "--yes")
	if code == 0 || !strings.Contains(out, "auth.") {
		t.Fatalf("a viewer's apps rm exited %d: %s", code, out)
	}
	if _, err := r.h.db.GetApp(t.Context(), r.app.ID); err != nil {
		t.Fatal("a viewer deleted the app")
	}

	r.as(t, r.acme)
	if out, code := r.cli(t, "apps", "rm", "shop", "--env", r.acme.env.ID, "--yes"); code == 0 || !strings.Contains(out, "web") {
		t.Errorf("deleting an app that is not there exited %d: %s", code, out)
	}
	if out, code := r.cli(t, "apps", "rm", "web", "--env", r.acme.env.ID, "--yes"); code != 0 {
		t.Fatalf("apps rm --yes exited %d: %s", code, out)
	}
	if _, err := r.h.db.GetApp(t.Context(), r.app.ID); err == nil || len(r.deleted) != 1 {
		t.Error("apps rm --yes left the app")
	}
}

// A rollback restores settings as well as code, so it says which before it
// starts: here the port it ran with, 3000, back over the 8080 it has now.
func TestRollbackSaysWhatItPutsBackFirst(t *testing.T) {
	r := newSettingsRun(t)

	out, code := r.cli(t, "rollback", "--app", r.app.ID, "--yes", "--follow=false")
	if code != 0 {
		t.Fatalf("rollback exited %d: %s", code, out)
	}
	plan, started := strings.Index(out, "port"), strings.Index(out, "Rolling back. Deployment")
	if plan < 0 || started < 0 || plan > started || !strings.Contains(out, "8080 -> 3000") ||
		!strings.Contains(out, "variables, domains, volumes") {
		t.Errorf("the rollback said: %s", out)
	}
	if r.deployments(t) != 3 {
		t.Error("the rollback did not start")
	}
}
