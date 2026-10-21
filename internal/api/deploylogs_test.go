package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"skifity/internal/store"
)

// workingDeployer is a deployer that says which deployments it is still
// working on.
type workingDeployer struct {
	fakeDeployer
	mu      sync.Mutex
	working map[string]bool
}

func (d *workingDeployer) Working(deploymentID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.working[deploymentID]
}

func (d *workingDeployer) set(deploymentID string, working bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.working[deploymentID] = working
}

// A deployment is recorded as succeeded the moment the app serves it, and goes
// on writing its log: the processes it waits for, a preview's seed,
// "Deployed.". A page told the log was finished at that moment kept it as it
// was for good, and never showed the rest.
func TestABuildLogIsFinishedOnlyOnceNothingWritesToIt(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	acme := h.newTenant("acme")
	app := h.app(acme, "web")

	deployment := store.Deployment{AppID: app.ID, Status: store.DeployDeploying}
	if err := h.db.CreateDeployment(ctx, &deployment); err != nil {
		t.Fatal(err)
	}
	if err := h.db.UpdateDeploymentStatus(ctx, deployment.ID, store.DeploySucceeded, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.AppendBuildLog(ctx, deployment.ID, "stdout", "Waiting for the app's other processes."); err != nil {
		t.Fatal(err)
	}
	deployer := &workingDeployer{working: map[string]bool{deployment.ID: true}}
	h.api.deployer = deployer

	read := func(query string) (bool, []store.LogLine) {
		t.Helper()
		status, body := h.do(acme, http.MethodGet,
			"/api/apps/"+app.ID+"/deployments/"+deployment.ID+"/logs"+query, nil)
		if status != http.StatusOK {
			t.Fatalf("answered %d: %s", status, truncate(body, 200))
		}
		var answer struct {
			Finished bool            `json:"finished"`
			Lines    []store.LogLine `json:"lines"`
		}
		if err := json.Unmarshal([]byte(body), &answer); err != nil {
			t.Fatal(err)
		}
		return answer.Finished, answer.Lines
	}

	if finished, _ := read(""); finished {
		t.Fatal("a succeeded deployment still writing its log was said to be finished")
	}

	if _, err := h.db.AppendBuildLog(ctx, deployment.ID, "stdout", "Deployed."); err != nil {
		t.Fatal(err)
	}
	deployer.set(deployment.ID, false)
	// Read on from the last line the page has, which is how it keeps up.
	finished, lines := read("?since=1")
	if !finished {
		t.Error("a log nothing writes to any more was not said to be finished")
	}
	if len(lines) != 1 || lines[0].Seq != 2 || lines[0].Line != "Deployed." {
		t.Errorf("reading on from line 1 gave %+v", lines)
	}
}

// runWatcher is a deployer with one run, which has printed a line and has or
// has not ended.
type runWatcher struct {
	fakeDeployer
	ended    bool
	exitCode int
}

func (r *runWatcher) RunLogs(context.Context, string, string, bool) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("migrating\n")), nil
}

func (r *runWatcher) RunEnded(context.Context, string, string) (RunResult, error) {
	return RunResult{Finished: r.ended, ExitCode: r.exitCode}, nil
}

// A look at a run's output says whether it has ended. The console reads it
// when its followed request was cut, and offered Run again while the command
// it had started was still going.
func TestARunsOutputSoFarSaysWhetherItHasEnded(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	runs := &runWatcher{}
	h.api.deployer = runs

	read := func() map[string]any {
		t.Helper()
		status, body := h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/runs/web-run-1/logs", nil)
		if status != http.StatusOK {
			t.Fatalf("answered %d: %s", status, truncate(body, 200))
		}
		var answer map[string]any
		if err := json.Unmarshal([]byte(body), &answer); err != nil {
			t.Fatal(err)
		}
		return answer
	}

	if answer := read(); answer["finished"] != false || answer["exit_code"] != nil {
		t.Errorf("a run still going: %v", answer)
	}
	runs.ended, runs.exitCode = true, 3
	if answer := read(); answer["finished"] != true || answer["exit_code"] != float64(3) {
		t.Errorf("a run that ended: %v", answer)
	}
}
