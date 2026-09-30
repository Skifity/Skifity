package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"skifity/internal/store"
)

// A branch previewed by hand is the preview a pull request from it would
// get: made once, deployed again when asked again, and removed when closed.
func TestABranchIsPreviewedWithoutAPullRequest(t *testing.T) {
	p, deployer, _ := stackHarness(t, false)
	path := "/api/apps/" + p.app.ID + "/previews"

	status, body := p.do(p.acme, http.MethodPost, path, map[string]any{"branch": "feature/checkout"})
	if status != http.StatusCreated {
		t.Fatalf("starting a preview answered %d: %s", status, body)
	}
	var started startedPreview
	if err := json.Unmarshal([]byte(body), &started); err != nil || started.AppID == "" {
		t.Fatalf("the answer is %s (%v)", body, err)
	}
	copy, err := p.db.GetApp(t.Context(), started.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if copy.Branch != "feature/checkout" || copy.EnvironmentID != started.EnvironmentID {
		t.Errorf("the preview builds %q in %s", copy.Branch, copy.EnvironmentID)
	}
	env, _ := p.db.GetEnvironment(t.Context(), started.EnvironmentID)
	if env.Kind != store.EnvPreview || env.FromFork {
		t.Errorf("the preview environment is %+v", env)
	}
	last := deployer.requests[len(deployer.requests)-1]
	if last.AppID != copy.ID || last.Trigger != "preview" || last.CommitSHA != "" {
		t.Errorf("the deploy was %+v; the branch's tip is built, so no commit is named", last)
	}

	// Asked again, the same preview is deployed again.
	status, body = p.do(p.acme, http.MethodPost, path, map[string]any{"branch": "feature/checkout"})
	var again startedPreview
	_ = json.Unmarshal([]byte(body), &again)
	if status != http.StatusCreated || again.EnvironmentID != started.EnvironmentID {
		t.Errorf("asking again answered %d with %s", status, body)
	}

	if status, body := p.do(p.acme, http.MethodPost, path+"/close", map[string]any{"branch": "feature/checkout"}); status != http.StatusOK {
		t.Fatalf("closing answered %d: %s", status, body)
	}
	if _, err := p.db.GetEnvironment(t.Context(), started.EnvironmentID); err == nil {
		t.Error("the preview is still there after it was closed")
	}
	if status, _ := p.do(p.acme, http.MethodPost, path+"/close", map[string]any{"branch": "feature/checkout"}); status != http.StatusNotFound {
		t.Errorf("closing a preview that is not there answered %d", status)
	}
}

func TestOnlyABranchOfAGitAppIsPreviewedByItsMembers(t *testing.T) {
	p, _, apps := stackHarness(t, false)
	for _, branch := range []string{"", "-x", "a..b", "has space", "x.lock", "refs~1", "a:b"} {
		if status, _ := p.do(p.acme, http.MethodPost, "/api/apps/"+p.app.ID+"/previews", map[string]any{"branch": branch}); status != http.StatusBadRequest {
			t.Errorf("the branch %q answered %d", branch, status)
		}
	}
	if status, _ := p.do(p.acme, http.MethodPost, "/api/apps/"+apps["cache"].ID+"/previews", map[string]any{"branch": "main"}); status != http.StatusBadRequest {
		t.Errorf("an image app was previewed: %d", status)
	}
	globex := p.newTenant("globex")
	if status, _ := p.do(globex, http.MethodPost, "/api/apps/"+p.app.ID+"/previews", map[string]any{"branch": "main"}); status != http.StatusNotFound {
		t.Errorf("another team previewed the app: %d", status)
	}
	viewer := p.newMember(p.acme, "auditor", store.RoleViewer)
	if status, _ := p.do(viewer, http.MethodPost, "/api/apps/"+p.app.ID+"/previews", map[string]any{"branch": "main"}); status != http.StatusForbidden {
		t.Errorf("a viewer previewed the app: %d", status)
	}
}
