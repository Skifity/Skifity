package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// In a monorepo a push that touches one app's code used to rebuild every app
// built from the repository.
func TestAPushDeploysOnlyTheAppsWhosePathsItTouched(t *testing.T) {
	h := newHarness(t)
	deploys := &recorder{}
	h.api.deployer = &fakeDeployer{log: deploys}
	acme := h.newTenant("acme")
	const repo = "https://github.com/acme/monorepo"
	for name, watch := range map[string]string{
		"web":      "apps/web\npackages/ui",
		"api":      "apps/api",
		"everyone": "",
	} {
		app := store.App{EnvironmentID: acme.env.ID, Name: name, Slug: name, Replicas: 1,
			RepoURL: repo, Branch: "main", AutoDeploy: true, WatchPaths: watch}
		if err := h.db.CreateApp(t.Context(), &app); err != nil {
			t.Fatal(err)
		}
	}
	source := store.GitSource{TeamID: acme.team.ID}
	push := func(event gitsrc.PushEvent) webhookResult {
		event.Kind, event.RepoURL, event.Branch, event.CommitSHA = "push", repo, "main", "abc"
		request := httptest.NewRequest(http.MethodPost, "/api/webhooks/git/src", nil)
		return h.api.dispatchGitEvent(request, source, event)
	}

	result := push(gitsrc.PushEvent{FilesKnown: true, ChangedFiles: []string{"packages/ui/button.tsx"}})
	if len(result.Deployments) != 2 {
		t.Fatalf("a change to a shared package deployed %d apps, want web and the one watching everything: %+v", len(result.Deployments), result)
	}
	if !slices.Contains(result.Skipped, "api (nothing it watches changed)") {
		t.Fatalf("the api was not skipped, or not said to be: %v", result.Skipped)
	}

	// A push whose files are not known deploys everything, as before.
	before := len(deploys.all())
	result = push(gitsrc.PushEvent{FilesKnown: false})
	if len(result.Deployments) != 3 || len(deploys.all())-before != 3 {
		t.Fatalf("a push with unknown files deployed %d apps, want all three: %+v", len(result.Deployments), result)
	}
}

func TestWatchPathsAreCheckedWhenTheyAreSaved(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := store.App{EnvironmentID: acme.env.ID, Name: "web", Slug: "web", Replicas: 1,
		CPURequestM: 50, CPULimitM: 1000, MemRequestMB: 128, MemLimitMB: 512}
	if err := h.db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	path := "/api/apps/" + app.ID

	status, body := h.do(acme, http.MethodPatch, path, map[string]any{"watch_paths": "apps/web\n../outside"})
	if status != http.StatusBadRequest || !strings.Contains(body, "app.watch_path_invalid") || !strings.Contains(body, "../outside") {
		t.Fatalf("a path outside the repository answered %d: %s", status, truncate(body, 200))
	}
	if stored, _ := h.db.GetApp(t.Context(), app.ID); stored.WatchPaths != "" {
		t.Fatalf("the refused paths were stored: %q", stored.WatchPaths)
	}

	status, body = h.do(acme, http.MethodPatch, path, map[string]any{"watch_paths": "  # the web app\n\n apps/web/** \n!**/*.md\n"})
	if status != http.StatusOK {
		t.Fatalf("saving watch paths answered %d: %s", status, truncate(body, 200))
	}
	if stored, _ := h.db.GetApp(t.Context(), app.ID); stored.WatchPaths != "# the web app\napps/web/**\n!**/*.md" {
		t.Fatalf("stored %q", stored.WatchPaths)
	}

	status, body = h.do(acme, http.MethodPatch, path, map[string]any{"watch_paths": strings.Repeat("apps/web\n", gitsrc.MaxWatchPaths+1)})
	if status != http.StatusBadRequest || !strings.Contains(body, "app.too_many_watch_paths") {
		t.Fatalf("too many paths answered %d: %s", status, truncate(body, 200))
	}
}
