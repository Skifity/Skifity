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
	ids := map[string]string{}
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
		ids[name] = app.ID
		// Each runs the commit the pushes below start from.
		deployedAt(t, h, app.ID, "base")
	}
	source := store.GitSource{TeamID: acme.team.ID}
	push := func(event gitsrc.PushEvent) webhookResult {
		event.Kind, event.RepoURL, event.Branch, event.CommitSHA, event.Before = "push", repo, "main", "abc", "base"
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

	// The api does not run the commit this push starts from — the push
	// before it was never deployed, say while it was locked — so this push's
	// files do not describe everything the api is missing, and it deploys.
	deployedAt(t, h, ids["api"], "older")
	result = push(gitsrc.PushEvent{FilesKnown: true, ChangedFiles: []string{"docs/readme.md"}})
	if slices.Contains(result.Skipped, "api (nothing it watches changed)") {
		t.Fatal("an app behind the push's starting commit was skipped for good")
	}
}

// deployedAt records that an app's running version was built from a commit.
func deployedAt(t *testing.T, h *harness, appID, commit string) {
	t.Helper()
	deployment := store.Deployment{AppID: appID, Status: store.DeployQueued, CommitSHA: commit, Image: "registry.internal/x:" + commit}
	if err := h.db.CreateDeployment(t.Context(), &deployment); err != nil {
		t.Fatal(err)
	}
	if err := h.db.UpdateDeploymentStatus(t.Context(), deployment.ID, store.DeploySucceeded, "", "", ""); err != nil {
		t.Fatal(err)
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

func TestAnAppIsSkippedByEveryPushThatTouchesNothingOfIt(t *testing.T) {
	// Skipped once, the app still runs the commit before that push, so the
	// next push — which starts from the skipped one — did not match what it
	// ran, and every monorepo app rebuilt on every other push.
	h := newHarness(t)
	h.api.deployer = &fakeDeployer{log: &recorder{}}
	acme := h.newTenant("acme")
	const repo = "https://github.com/acme/monorepo"
	api := store.App{EnvironmentID: acme.env.ID, Name: "api", Slug: "api", Replicas: 1,
		RepoURL: repo, Branch: "main", AutoDeploy: true, WatchPaths: "apps/api"}
	if err := h.db.CreateApp(t.Context(), &api); err != nil {
		t.Fatal(err)
	}
	deployedAt(t, h, api.ID, "a")
	source := store.GitSource{TeamID: acme.team.ID}
	push := func(before, commit string) webhookResult {
		event := gitsrc.PushEvent{Kind: "push", RepoURL: repo, Branch: "main", Before: before, CommitSHA: commit,
			FilesKnown: true, ChangedFiles: []string{"apps/web/page.tsx"}}
		return h.api.dispatchGitEvent(httptest.NewRequest(http.MethodPost, "/api/webhooks/git/src", nil), source, event)
	}
	for _, step := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}} {
		if result := push(step[0], step[1]); len(result.Deployments) != 0 {
			t.Fatalf("a push from %s to %s that touched nothing of the api deployed it", step[0], step[1])
		}
	}
	// Deployed some other way since — a rollback, a manual deploy — the
	// comparison starts again from what that runs.
	deployedAt(t, h, api.ID, "x")
	if result := push("d", "e"); len(result.Deployments) != 1 {
		t.Fatal("an app deployed since the last skipped push was skipped against a commit it does not run")
	}
}
