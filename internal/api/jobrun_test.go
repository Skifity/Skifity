package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// runRecorder is a deployer that remembers what it was asked to run.
type runRecorder struct {
	fakeDeployer
	ran []string
}

func (r *runRecorder) RunOnce(_ context.Context, appID, command string) (RunHandle, error) {
	r.ran = append(r.ran, appID+": "+command)
	return RunHandle{Name: "web-run-1"}, nil
}

// A scheduled command can be run now, with the command its schedule holds —
// not one the request names, which would make this a second, unaudited way
// to run anything.
func TestAScheduledCommandRunsNowWithItsOwnCommand(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	app := h.app(acme, "web")
	runs := &runRecorder{fakeDeployer: fakeDeployer{log: &recorder{}}}
	h.api.deployer = runs

	job := store.AppJob{AppID: app.ID, Name: "nightly", Schedule: "0 3 * * *", Command: "bin/report", Enabled: true}
	if err := h.db.CreateAppJob(t.Context(), &job); err != nil {
		t.Fatal(err)
	}
	path := "/api/apps/" + app.ID + "/jobs/" + job.ID + "/run"

	status, body := h.do(acme, http.MethodPost, path, map[string]any{"command": "rm -rf /"})
	if status != http.StatusAccepted || !strings.Contains(body, "web-run-1") {
		t.Fatalf("running it now answered %d: %s", status, body)
	}
	if len(runs.ran) != 1 || runs.ran[0] != app.ID+": bin/report" {
		t.Fatalf("ran %v, want the schedule's own command", runs.ran)
	}

	member := h.newMember(acme, "dev", store.RoleMember)
	if status, _ := h.do(member, http.MethodPost, path, nil); status != http.StatusForbidden {
		t.Errorf("a member ran it: %d", status)
	}
	if status, _ := h.do(globex, http.MethodPost, path, nil); status != http.StatusNotFound {
		t.Errorf("another team ran it: %d", status)
	}
	if status, _ := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/jobs/job_missing/run", nil); status != http.StatusNotFound {
		t.Errorf("a command that is not there answered %d", status)
	}
	if len(runs.ran) != 1 {
		t.Errorf("refused requests ran something: %v", runs.ran)
	}
}
