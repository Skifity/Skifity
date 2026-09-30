package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// A Git host does not promise to deliver pushes in the order they happened,
// and a delivery signed once is good for ever: a push that arrived late, or
// one sent again, deployed its commit over a newer one already running, and
// production went back in time.
func TestAnOlderPushDoesNotDeployOverANewerOne(t *testing.T) {
	h := newHarness(t)
	h.api.deployer = &fakeDeployer{log: &recorder{}}
	acme := h.newTenant("acme")
	const repo = "https://github.com/acme/shop"
	app := store.App{EnvironmentID: acme.env.ID, Name: "web", Slug: "web", Replicas: 1,
		RepoURL: repo, Branch: "main", AutoDeploy: true}
	if err := h.db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	deployedAt(t, h, app.ID, "a")
	source := store.GitSource{TeamID: acme.team.ID}
	push := func(before, commit string, forced bool) webhookResult {
		event := gitsrc.PushEvent{Kind: "push", RepoURL: repo, Branch: "main", Before: before, CommitSHA: commit, Forced: forced}
		return h.api.dispatchGitEvent(httptest.NewRequest(http.MethodPost, "/api/webhooks/git/src", nil), source, event)
	}

	// Pushed a→b, then b→c; delivered c first.
	if result := push("b", "c", false); len(result.Deployments) != 1 {
		t.Fatalf("the newest push did not deploy: %+v", result)
	}
	deployedAt(t, h, app.ID, "c")
	result := push("a", "b", false)
	if len(result.Deployments) != 0 || !slices.Contains(result.Skipped, "web (b is older than a commit already deployed)") {
		t.Fatalf("the late push deployed its older commit: %+v", result)
	}
	// The newest one, sent again, is already what runs.
	result = push("b", "c", false)
	if len(result.Deployments) != 0 || !slices.Contains(result.Skipped, "web (already runs c)") {
		t.Fatalf("a push sent again deployed again: %+v", result)
	}
	// A force push back to b is somebody going back on purpose.
	if result := push("c", "b", true); len(result.Deployments) != 1 {
		t.Fatalf("a force push back to an older commit did not deploy: %+v", result)
	}
}
