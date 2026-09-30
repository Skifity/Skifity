package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/blueprint"
	"skifity/internal/builder"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// processCluster answers for the objects a process becomes, and remembers
// which it was asked about.
type processCluster struct {
	Cluster
	asked []string
}

func (c *processCluster) AppStatus(_ context.Context, _, name string) (AppRuntimeStatus, error) {
	c.asked = append(c.asked, name)
	return AppRuntimeStatus{Phase: "running", ReadyReplicas: 2, DesiredReplicas: 2}, nil
}

func (c *processCluster) AppLogs(_ context.Context, _, name string, _ LogOptions) (io.ReadCloser, error) {
	c.asked = append(c.asked, name)
	return io.NopCloser(strings.NewReader("working on job 1\n")), nil
}

func TestAnAppsProcessesAreItsOwnToChange(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "shop")
	path := "/api/apps/" + app.ID + "/processes"

	status, body := h.do(acme, http.MethodPut, path+"/worker", map[string]any{"command": "bundle exec sidekiq", "instances": 2})
	if status != http.StatusOK {
		t.Fatalf("adding a worker answered %d: %s", status, body)
	}
	// The same name again is the same process, changed.
	if status, body := h.do(acme, http.MethodPut, path+"/worker", map[string]any{"command": "bundle exec sidekiq -c 5", "instances": 0}); status != http.StatusOK {
		t.Fatalf("changing the worker answered %d: %s", status, body)
	}
	stored, err := h.db.ListProcesses(t.Context(), app.ID)
	if err != nil || len(stored) != 1 || stored[0].Command != "bundle exec sidekiq -c 5" || stored[0].Instances != 0 {
		t.Fatalf("stored %+v (%v)", stored, err)
	}
	// A new command alone, as `processes set worker -- cmd` sends it, leaves
	// how many run alone: the stopped worker is not started by it.
	if status, body := h.do(acme, http.MethodPut, path+"/worker", map[string]any{"command": "bundle exec sidekiq -c 10"}); status != http.StatusOK {
		t.Fatalf("changing the command answered %d: %s", status, body)
	}
	if stored, _ := h.db.ListProcesses(t.Context(), app.ID); stored[0].Instances != 0 || stored[0].Command != "bundle exec sidekiq -c 10" {
		t.Fatalf("a command change made the worker %+v", stored[0])
	}

	for name, body := range map[string]map[string]any{
		"web":     {"command": "x"},
		"release": {"command": "x"},
		"Worker":  {"command": "x"},
		"clock":   {"command": ""},
		"beat":    {"command": "one\ntwo"},
		"sweeper": {"command": "x", "instances": 101},
		"trimmer": {"command": "x", "instances": -1},
	} {
		if status, answer := h.do(acme, http.MethodPut, path+"/"+name, body); status != http.StatusBadRequest {
			t.Errorf("%s %v answered %d: %s", name, body, status, answer)
		}
	}
	if status, answer := h.do(acme, http.MethodPut, path+"/web", map[string]any{"command": "x"}); !strings.Contains(answer, "process.name_invalid") {
		t.Errorf("a process called web answered %d: %s", status, answer)
	}

	// Ten beside web, and no more; changing one of the ten is still allowed.
	for _, name := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"} {
		if status, body := h.do(acme, http.MethodPut, path+"/"+name, map[string]any{"command": "run " + name}); status != http.StatusOK {
			t.Fatalf("process %s answered %d: %s", name, status, body)
		}
	}
	if _, answer := h.do(acme, http.MethodPut, path+"/a10", map[string]any{"command": "x"}); !strings.Contains(answer, "process.too_many") {
		t.Fatalf("an eleventh process: %s", answer)
	}
	if status, _ := h.do(acme, http.MethodPut, path+"/a9", map[string]any{"command": "run a9 faster"}); status != http.StatusOK {
		t.Fatalf("changing the tenth process answered %d", status)
	}

	// Somebody who can only look, looks.
	viewer := h.newMember(acme, "auditor", store.RoleViewer)
	if status, _ := h.do(viewer, http.MethodGet, path, nil); status != http.StatusOK {
		t.Fatalf("a viewer listing processes answered %d", status)
	}
	if status, _ := h.do(viewer, http.MethodPut, path+"/worker", map[string]any{"command": "rm -rf /"}); status != http.StatusForbidden {
		t.Fatalf("a viewer changing a process answered %d", status)
	}

	if status, _ := h.do(acme, http.MethodDelete, path+"/worker", nil); status != http.StatusNoContent {
		t.Fatalf("removing the worker answered %d", status)
	}
	if status, _ := h.do(acme, http.MethodDelete, path+"/worker", nil); status != http.StatusNotFound {
		t.Fatalf("removing it twice answered %d", status)
	}
	for _, action := range []string{"app.process_set", "app.process_removed"} {
		if entries, _ := h.db.ListAudit(t.Context(), acme.team.ID, action, app.ID, 50); len(entries) == 0 {
			t.Errorf("%s was not recorded", action)
		}
	}
}

func TestAProcessChangeIsRecordedWhenTheClusterDoesNotAnswer(t *testing.T) {
	// The row is changed before the cluster is told, so the audit log has to
	// say so even when telling the cluster fails.
	h := newHarness(t)
	h.api.deployer = &failingSyncDeployer{fakeDeployer{log: &recorder{}}}
	acme := h.newTenant("acme")
	app := h.app(acme, "shop")
	path := "/api/apps/" + app.ID + "/processes/worker"
	if status, _ := h.do(acme, http.MethodPut, path, map[string]any{"command": "run worker"}); status < 400 {
		t.Fatalf("adding answered %d with the cluster refusing", status)
	}
	if status, _ := h.do(acme, http.MethodDelete, path, nil); status < 400 {
		t.Fatalf("removing answered %d with the cluster refusing", status)
	}
	for _, action := range []string{"app.process_set", "app.process_removed"} {
		if entries, _ := h.db.ListAudit(t.Context(), acme.team.ID, action, app.ID, 50); len(entries) != 1 {
			t.Errorf("%s was recorded %d times", action, len(entries))
		}
	}
}

func TestAProcessIsReadAndStatedByItsOwnName(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	cluster := &processCluster{}
	h.withCluster(cluster)
	app := h.app(acme, "shop")
	if err := h.db.SetProcess(t.Context(), &store.AppProcess{AppID: app.ID, Name: "worker", Command: "work", Instances: 2}); err != nil {
		t.Fatal(err)
	}

	status, body := h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/processes", nil)
	var listed struct {
		Items []processView `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &listed); status != http.StatusOK || err != nil || len(listed.Items) != 1 {
		t.Fatalf("listing answered %d: %s", status, body)
	}
	if ready := listed.Items[0].Ready; ready == nil || *ready != 2 {
		t.Fatalf("the worker's running count is %v", ready)
	}

	status, body = h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/logs?process=worker", nil)
	if status != http.StatusOK || !strings.Contains(body, "working on job 1") {
		t.Fatalf("the worker's logs answered %d: %s", status, body)
	}
	want := kube.ProcessDeploymentName(app.Slug, "worker")
	for _, asked := range cluster.asked {
		if asked != want {
			t.Fatalf("the cluster was asked about %q; the worker is %q", asked, want)
		}
	}
	if status, _ := h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/logs?process=clock", nil); status != http.StatusNotFound {
		t.Fatalf("the logs of a process the app does not have answered %d", status)
	}
}

func TestAnAppIsCreatedWithItsProcfilesProcesses(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	create := func(name string, processes []map[string]any) (int, string) {
		return h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
			"name": name, "source_type": "image", "image": "ghcr.io/acme/shop:1", "processes": processes,
		})
	}

	status, body := create("shop", []map[string]any{
		{"name": "worker", "command": "bundle exec sidekiq"},
		{"name": "clock", "command": "bundle exec clockwork", "instances": 0},
	})
	if status != http.StatusCreated {
		t.Fatalf("answered %d: %s", status, body)
	}
	var created struct {
		App store.App `json:"app"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	processes, _ := h.db.ListProcesses(t.Context(), created.App.ID)
	if len(processes) != 2 || processes[0].Name != "clock" || processes[0].Instances != 0 || processes[1].Instances != 1 {
		t.Fatalf("created with %+v", processes)
	}

	// One bad process refuses the whole app, before there is one.
	status, _ = create("blog", []map[string]any{{"name": "worker", "command": "x"}, {"name": "worker", "command": "y"}})
	if status != http.StatusBadRequest {
		t.Fatalf("two processes of one name answered %d", status)
	}
	apps, _ := h.db.ListApps(t.Context(), acme.env.ID)
	for _, app := range apps {
		if app.Name == "blog" {
			t.Fatal("an app was created with processes that were refused")
		}
	}
}

func TestTheBuilderAndTheClusterAgreeOnAProcessName(t *testing.T) {
	// The builder cannot import kube, so it keeps a copy of the rule. A name
	// detection offers and the panel then refuses is a new app that fails to
	// be created.
	for _, name := range []string{
		"worker", "clock", "celery-beat", "a", "a1", "web", "release", "Worker", "2fast",
		"-x", "x-", "under_score", "a-very-long-process-name", "twenty-characters-ab", "twenty-one-characters",
	} {
		normalised, ok := builder.ProcessName(name)
		if ok && !kube.ValidProcessName(normalised) {
			t.Errorf("detection offers %q as %q, which the panel refuses", name, normalised)
		}
		if !ok && kube.ValidProcessName(name) {
			t.Errorf("detection drops %q, which the panel would take", name)
		}
	}
	// And on how many: detection offering an eleventh is the same failure.
	if builder.MaxProcesses != maxProcesses {
		t.Errorf("detection offers up to %d processes and the panel takes %d", builder.MaxProcesses, maxProcesses)
	}
}

func TestAPreviewRunsTheAppsProcessesCheaply(t *testing.T) {
	p := withLinkedDatabase(t)
	for _, process := range []store.AppProcess{
		{AppID: p.app.ID, Name: "worker", Command: "work", Instances: 4},
		{AppID: p.app.ID, Name: "clock", Command: "tick", Instances: 0},
	} {
		if err := p.db.SetProcess(t.Context(), &process); err != nil {
			t.Fatal(err)
		}
	}
	preview := p.openPullRequest(t, 7, false)

	processes, err := p.db.ListProcesses(t.Context(), preview.ID)
	if err != nil || len(processes) != 2 {
		t.Fatalf("the preview has processes %+v (%v)", processes, err)
	}
	// Sorted by name: the clock the app stopped stays stopped, and the
	// worker runs once, whatever production asks for.
	if processes[0].Instances != 0 || processes[1].Instances != 1 || processes[1].Command != "work" {
		t.Fatalf("the preview runs %+v", processes)
	}
}

func TestABlueprintLinksADatabaseAsThePanelWould(t *testing.T) {
	for _, engine := range blueprint.Engines {
		if blueprint.DefaultVariable(engine) != defaultVarNameFor(engine) {
			t.Errorf("%s: skifity.yaml links it as %s and the panel as %s",
				engine, blueprint.DefaultVariable(engine), defaultVarNameFor(engine))
		}
	}
}
