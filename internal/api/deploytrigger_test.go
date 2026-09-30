package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// requestLog is a deployer that keeps what it was asked, so a test can say
// which commit was deployed and why.
type requestLog struct {
	fakeDeployer
	mu       sync.Mutex
	requests []DeployRequest
}

func (d *requestLog) Deploy(_ context.Context, req DeployRequest) (store.Deployment, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, req)
	return store.Deployment{ID: "dep_" + req.Trigger, AppID: req.AppID, Number: len(d.requests)}, nil
}

func (d *requestLog) all() []DeployRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]DeployRequest(nil), d.requests...)
}

// "[skip ci]" in the commit a push leaves at the tip of the branch is somebody
// saying it is not worth a build. The push deploys nothing and says why in
// the delivery log, where somebody looks for why a push did not deploy; a
// deploy asked for by hand is not a push, and is never skipped.
func TestAPushThatSaysSkipCIDeploysNothing(t *testing.T) {
	h := newHarness(t)
	deploys := &requestLog{}
	h.api.deployer = deploys
	acme := h.newTenant("acme")
	const repo = "https://github.com/acme/shop"
	app := store.App{EnvironmentID: acme.env.ID, Name: "web", Slug: "web", Replicas: 1,
		RepoURL: repo, Branch: "main", AutoDeploy: true}
	if err := h.db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	deployedAt(t, h, app.ID, "a")
	source := store.GitSource{TeamID: acme.team.ID}
	push := func(before, commit, marker string) webhookResult {
		event := gitsrc.PushEvent{Kind: "push", RepoURL: repo, Branch: "main", Before: before, CommitSHA: commit,
			SkipMarker: marker}
		return h.api.dispatchGitEvent(httptest.NewRequest(http.MethodPost, "/api/webhooks/git/src", nil), source, event)
	}

	result := push("a", "b", "[skip ci]")
	if len(result.Deployments) != 0 || len(deploys.all()) != 0 {
		t.Fatalf("a push that says [skip ci] deployed: %+v", result)
	}
	if !slices.Contains(result.Skipped, "web (the commit says [skip ci])") {
		t.Fatalf("the skip was not said: %v", result.Skipped)
	}

	// The skipped commit's changes are not running, so the next push brings
	// them — it is not skipped as "older", nor compared from the skipped one.
	result = push("b", "c", "")
	if len(result.Deployments) != 1 {
		t.Fatalf("the push after a skipped one did not deploy: %+v", result)
	}

	status, body := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/deploy", map[string]any{})
	if status != http.StatusAccepted {
		t.Fatalf("a deploy by hand answered %d: %s", status, truncate(body, 200))
	}
	if last := deploys.all(); last[len(last)-1].Trigger != "manual" {
		t.Errorf("the deploy by hand went out as %q", last[len(last)-1].Trigger)
	}
}

// An app that deploys on a tag is deployed by a matching tag, with the commit
// the tag points at, and not by its branch moving: the tag is put on a commit
// the branch already had, and both would be the same code twice.
func TestAnAppThatDeploysOnATagIgnoresItsBranch(t *testing.T) {
	h := newHarness(t)
	deploys := &requestLog{}
	h.api.deployer = deploys
	acme := h.newTenant("acme")
	const repo = "https://github.com/acme/shop"
	release := store.App{EnvironmentID: acme.env.ID, Name: "release", Slug: "release", Replicas: 1,
		RepoURL: repo, Branch: "main", AutoDeploy: true, DeployTrigger: gitsrc.DeployOnTag, TagPattern: "v*"}
	edge := store.App{EnvironmentID: acme.env.ID, Name: "edge", Slug: "edge", Replicas: 1,
		RepoURL: repo, Branch: "main", AutoDeploy: true}
	for _, app := range []*store.App{&release, &edge} {
		if err := h.db.CreateApp(t.Context(), app); err != nil {
			t.Fatal(err)
		}
	}
	source := store.GitSource{TeamID: acme.team.ID}
	send := func(event gitsrc.PushEvent) webhookResult {
		event.RepoURL = repo
		return h.api.dispatchGitEvent(httptest.NewRequest(http.MethodPost, "/api/webhooks/git/src", nil), source, event)
	}

	result := send(gitsrc.PushEvent{Kind: "push", Branch: "main", Before: "a", CommitSHA: "b"})
	if !slices.Contains(result.Skipped, "release (deploys tags matching v*)") {
		t.Fatalf("a push to the branch was not skipped for the app that deploys tags: %+v", result)
	}
	if got := deploys.all(); len(got) != 1 || got[0].AppID != edge.ID {
		t.Fatalf("a push to the branch deployed %+v, want the edge app alone", got)
	}

	result = send(gitsrc.PushEvent{Kind: "tag", Tag: "v1.4.0", CommitSHA: "c0ffee"})
	if len(result.Deployments) != 1 {
		t.Fatalf("a matching tag did not deploy: %+v", result)
	}
	last := deploys.all()[len(deploys.all())-1]
	if last.AppID != release.ID || last.Trigger != "tag" || last.CommitSHA != "c0ffee" {
		t.Fatalf("the tag deployed %+v, want the release app at the tag's commit", last)
	}
	if !slices.Contains(result.Skipped, "edge (deploys pushes to its branch, not tags)") {
		t.Errorf("the app that deploys its branch was not said to skip the tag: %v", result.Skipped)
	}

	before := len(deploys.all())
	result = send(gitsrc.PushEvent{Kind: "tag", Tag: "nightly-2026-09-30", CommitSHA: "d00d"})
	if len(deploys.all()) != before || !slices.Contains(result.Skipped, "release (nightly-2026-09-30 does not match v*)") {
		t.Fatalf("a tag the pattern does not match deployed, or did not say why not: %+v", result)
	}

	// The same tag delivered again is recognised by its commit.
	deployedAt(t, h, release.ID, "c0ffee")
	result = send(gitsrc.PushEvent{Kind: "tag", Tag: "v1.4.0", CommitSHA: "c0ffee"})
	if len(deploys.all()) != before || !slices.Contains(result.Skipped, "release (already runs c0ffee)") {
		t.Fatalf("a tag sent again deployed again: %+v", result)
	}

	// A locked app is not deployed by a tag either.
	if err := h.db.LockDeploys(t.Context(), &store.DeployLock{AppID: release.ID, Reason: "incident", LockedBy: acme.user.Email}); err != nil {
		t.Fatal(err)
	}
	result = send(gitsrc.PushEvent{Kind: "tag", Tag: "v1.4.1", CommitSHA: "beef"})
	if len(deploys.all()) != before || !slices.Contains(result.Skipped, "release (deploys are locked)") {
		t.Fatalf("a locked app was deployed by a tag: %+v", result)
	}
}

func TestWhatAnAppDeploysOnIsCheckedWhenItIsSaved(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := store.App{EnvironmentID: acme.env.ID, Name: "web", Slug: "web", Replicas: 1,
		CPURequestM: 50, CPULimitM: 1000, MemRequestMB: 128, MemLimitMB: 512}
	if err := h.db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	if app.DeployTrigger != gitsrc.DeployOnBranch || app.TagPattern != gitsrc.DefaultTagPattern {
		t.Fatalf("a new app deploys on %q with %q, want its branch and v*", app.DeployTrigger, app.TagPattern)
	}
	path := "/api/apps/" + app.ID

	status, body := h.do(acme, http.MethodPatch, path, map[string]any{"deploy_trigger": "nightly"})
	if status != http.StatusBadRequest || !strings.Contains(body, "app.deploy_trigger_invalid") {
		t.Fatalf("an unknown trigger answered %d: %s", status, truncate(body, 200))
	}
	status, body = h.do(acme, http.MethodPatch, path, map[string]any{"deploy_trigger": "tag", "tag_pattern": "v[0-9"})
	if status != http.StatusBadRequest || !strings.Contains(body, "app.tag_pattern_invalid") || !strings.Contains(body, "v[0-9") {
		t.Fatalf("a malformed pattern answered %d: %s", status, truncate(body, 200))
	}
	if stored, _ := h.db.GetApp(t.Context(), app.ID); stored.DeployTrigger != gitsrc.DeployOnBranch {
		t.Fatalf("a refused change was stored: %q", stored.DeployTrigger)
	}

	status, body = h.do(acme, http.MethodPatch, path, map[string]any{"deploy_trigger": "tag", "tag_pattern": " release-* "})
	if status != http.StatusOK {
		t.Fatalf("deploying on a tag answered %d: %s", status, truncate(body, 200))
	}
	if stored, _ := h.db.GetApp(t.Context(), app.ID); stored.DeployTrigger != gitsrc.DeployOnTag || stored.TagPattern != "release-*" {
		t.Fatalf("stored %q and %q", stored.DeployTrigger, stored.TagPattern)
	}
	// Back to the branch, and the pattern is kept for the next time.
	if status, body = h.do(acme, http.MethodPatch, path, map[string]any{"deploy_trigger": "branch"}); status != http.StatusOK {
		t.Fatalf("going back to the branch answered %d: %s", status, truncate(body, 200))
	}
	if stored, _ := h.db.GetApp(t.Context(), app.ID); stored.DeployTrigger != gitsrc.DeployOnBranch || stored.TagPattern != "release-*" {
		t.Fatalf("stored %q and %q", stored.DeployTrigger, stored.TagPattern)
	}

	// And when an app is made.
	status, body = h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "tagged", "repo_url": "https://github.com/acme/shop", "deploy_trigger": "tag", "tag_pattern": "v[",
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "app.tag_pattern_invalid") {
		t.Fatalf("a new app with a malformed pattern answered %d: %s", status, truncate(body, 200))
	}
	status, body = h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "tagged", "repo_url": "https://github.com/acme/shop", "deploy_trigger": "tag",
	})
	if status != http.StatusCreated || !strings.Contains(body, `"deploy_trigger":"tag"`) || !strings.Contains(body, `"tag_pattern":"v*"`) {
		t.Fatalf("a new app that deploys on a tag answered %d: %s", status, truncate(body, 300))
	}
}

// A connection's repositories are read with the team's token and name its
// private ones, so only the team's members see them: another team's owner is
// told the connection does not exist, and a viewer that they cannot.
func TestOnlyTheTeamsMembersListWhatItsGitConnectionReads(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	rival := h.newTenant("rival")
	viewer := h.newMember(acme, "viewer", store.RoleViewer)
	contractor := h.limitedMember(acme, "contractor", store.RoleMember, acme.project.ID)

	// Its address is a loopback one, which the panel's guarded client
	// refuses to dial: getting as far as that refusal is getting past every
	// check that comes before it, without asking a real host anything.
	github := store.GitSource{TeamID: acme.team.ID, Kind: "github_pat", Name: "acme-github", BaseURL: "https://127.0.0.1:9"}
	plain := store.GitSource{TeamID: acme.team.ID, Kind: "generic", Name: "acme-plain"}
	for _, source := range []*store.GitSource{&github, &plain} {
		if err := h.db.CreateGitSource(t.Context(), source); err != nil {
			t.Fatal(err)
		}
	}
	repos := func(teamID, sourceID string) string {
		return "/api/teams/" + teamID + "/git-sources/" + sourceID + "/repositories"
	}
	branches := func(teamID, sourceID, repo string) string {
		return "/api/teams/" + teamID + "/git-sources/" + sourceID + "/branches?repo=" + repo
	}

	for _, tc := range []struct {
		who    tenant
		path   string
		status int
		code   string
	}{
		// Another team's owner, through their own team and through acme's.
		{rival, repos(rival.team.ID, github.ID), http.StatusNotFound, "resource.not_found"},
		{rival, branches(rival.team.ID, github.ID, "acme/shop"), http.StatusNotFound, "resource.not_found"},
		{rival, repos(acme.team.ID, github.ID), http.StatusNotFound, "resource.not_found"},
		// A viewer of the team creates no apps.
		{viewer, repos(acme.team.ID, github.ID), http.StatusForbidden, "auth.forbidden"},
		{viewer, branches(acme.team.ID, github.ID, "acme/shop"), http.StatusForbidden, "auth.forbidden"},
		// A member limited to a project creates apps in it, from these.
		{contractor, repos(acme.team.ID, github.ID), http.StatusBadGateway, "git.listing_failed"},
		{acme, repos(acme.team.ID, github.ID), http.StatusBadGateway, "git.listing_failed"},
		{acme, branches(acme.team.ID, github.ID, "acme/shop"), http.StatusBadGateway, "git.listing_failed"},
		{acme, branches(acme.team.ID, github.ID, "acme/.."), http.StatusBadRequest, "request.invalid"},
		{acme, repos(acme.team.ID, plain.ID), http.StatusBadRequest, "git.listing_unsupported"},
	} {
		status, body := h.do(tc.who, http.MethodGet, tc.path, nil)
		if status != tc.status || !strings.Contains(body, `"code":"`+tc.code+`"`) {
			t.Errorf("%s asking %s answered %d, want %d %s:\n%s", tc.who.user.Name, tc.path, status, tc.status, tc.code,
				truncate(body, 200))
		}
	}
}
