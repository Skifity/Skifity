package api

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// Previews of a whole environment.
//
// A preview used to be a copy of the apps its repository builds and nothing
// else: a front end's preview called an API that was not there, and a web
// app's preview had no worker, because the worker was an app of its own.
// And two apps sharing a database each got a preview database of their own.

// recordingDeployer remembers what it was asked to deploy.
type recordingDeployer struct {
	fakeDeployer
	requests []DeployRequest
}

func (d *recordingDeployer) Deploy(_ context.Context, req DeployRequest) (store.Deployment, error) {
	d.requests = append(d.requests, req)
	return store.Deployment{ID: "dep_" + req.AppID, AppID: req.AppID}, nil
}

// stackHarness is withLinkedDatabase's web app, plus the rest of an
// environment: an API another repository builds, sharing web's database; a
// cache that is an image; and an app never deployed.
func stackHarness(t *testing.T, stack bool) (previewDBHarness, *recordingDeployer, map[string]store.App) {
	t.Helper()
	p := withLinkedDatabase(t)
	deployer := &recordingDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}
	p.api.deployer = deployer
	if err := p.db.SetEnvironmentPreviewStack(t.Context(), p.acme.env.ID, stack); err != nil {
		t.Fatal(err)
	}

	web := p.app
	web.SourceType = "git"
	web.RepoURL, web.AutoDeploy, web.PreviewDeploys, web.PreviewSeed = "https://github.com/acme/web", true, true, "npm run seed"
	if err := p.db.UpdateApp(t.Context(), &web); err != nil {
		t.Fatal(err)
	}
	p.app = web

	apps := map[string]store.App{"web": web}
	for _, app := range []store.App{
		{Name: "api", Slug: "api", SourceType: "git", RepoURL: "https://github.com/acme/api", AutoDeploy: true, Internal: true},
		{Name: "cache", Slug: "cache", SourceType: "image", Image: "valkey/valkey:8", Internal: true},
		{Name: "admin", Slug: "admin", SourceType: "git", RepoURL: "https://github.com/acme/admin"},
	} {
		app.EnvironmentID, app.Replicas = p.acme.env.ID, 3
		if err := p.db.CreateApp(t.Context(), &app); err != nil {
			t.Fatal(err)
		}
		apps[app.Slug] = app
	}
	// The API runs a version built here, and shares web's database.
	running := store.Deployment{AppID: apps["api"].ID, Status: store.DeployQueued, Image: "registry.internal/acme/api:abc",
		CommitSHA: "abc", BuildFingerprint: "fp-api"}
	if err := p.db.CreateDeployment(t.Context(), &running); err != nil {
		t.Fatal(err)
	}
	if err := p.db.UpdateDeploymentStatus(t.Context(), running.ID, store.DeploySucceeded, "", "", ""); err != nil {
		t.Fatal(err)
	}
	links, _ := p.db.ListLinksForApp(t.Context(), web.ID)
	if err := p.db.LinkDatabase(t.Context(), links[0].DatabaseID, apps["api"].ID, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	return p, deployer, apps
}

// previewApps is what the preview environment has, by slug.
func previewApps(t *testing.T, p previewDBHarness) map[string]store.App {
	t.Helper()
	envs, err := p.db.ListEnvironments(t.Context(), p.acme.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.App{}
	for _, env := range envs {
		if env.Kind != store.EnvPreview {
			continue
		}
		apps, err := p.db.ListApps(t.Context(), env.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, app := range apps {
			out[app.Slug] = app
		}
	}
	return out
}

func TestAPreviewCanCopyTheWholeEnvironment(t *testing.T) {
	p, deployer, _ := stackHarness(t, true)
	request := httptestRequest()
	if _, err := p.api.deployPreview(request, p.app, pullRequest(21)); err != nil {
		t.Fatal(err)
	}
	copies := previewApps(t, p)
	if _, ok := copies["admin"]; ok {
		t.Error("an app that has never been deployed was copied, with nothing to run")
	}
	for _, slug := range []string{"web", "api", "cache"} {
		if _, ok := copies[slug]; !ok {
			t.Fatalf("the preview has no %s: %v", slug, slices.Sorted(maps.Keys(copies)))
		}
	}

	// The API runs the version it runs here; only web is built from the pull
	// request.
	byApp := map[string]DeployRequest{}
	for _, req := range deployer.requests {
		byApp[req.AppID] = req
	}
	if req := byApp[copies["web"].ID]; req.Image != "" || req.CommitSHA != "abc" {
		t.Errorf("web was deployed with %+v, not built from the pull request", req)
	}
	if req := byApp[copies["api"].ID]; req.Image != "registry.internal/acme/api:abc" || req.Fingerprint != "fp-api" {
		t.Errorf("the API was deployed with %+v, not the image it runs", req)
	}
	if req, ok := byApp[copies["cache"].ID]; !ok || req.Image != "" {
		t.Errorf("the cache was deployed with %+v; an image app pulls its own image", req)
	}

	// The copies stay where they are put: no deploy on push, no preview of a
	// preview, one instance, and still internal.
	api := copies["api"]
	if api.AutoDeploy || api.PreviewDeploys || api.Replicas != 1 || !api.Internal {
		t.Errorf("the API's copy is %+v", api)
	}
	if copies["web"].PreviewSeed != "npm run seed" || copies["web"].SeededAt != "" {
		t.Errorf("web's copy has seed %q, seeded %q", copies["web"].PreviewSeed, copies["web"].SeededAt)
	}

	// One database for the two apps that share one here.
	if len(p.manager.created) != 1 {
		t.Fatalf("%d databases were made for a preview of apps sharing one", len(p.manager.created))
	}
	webLinks, _ := p.db.ListLinksForApp(t.Context(), copies["web"].ID)
	apiLinks, _ := p.db.ListLinksForApp(t.Context(), copies["api"].ID)
	if len(webLinks) != 1 || len(apiLinks) != 1 || webLinks[0].DatabaseID != apiLinks[0].DatabaseID {
		t.Fatalf("web is linked to %+v and the API to %+v", webLinks, apiLinks)
	}

	// A second push to the pull request copies nothing again.
	deployer.requests = nil
	if _, err := p.api.deployPreview(request, p.app, pullRequest(21)); err != nil {
		t.Fatal(err)
	}
	if len(deployer.requests) != 1 || len(previewApps(t, p)) != 3 {
		t.Fatalf("a second push deployed %d apps", len(deployer.requests))
	}
}

func TestAPreviewCopiesOnlyItsOwnAppsUnlessAsked(t *testing.T) {
	p, deployer, _ := stackHarness(t, false)
	if _, err := p.api.deployPreview(httptestRequest(), p.app, pullRequest(22)); err != nil {
		t.Fatal(err)
	}
	copies := previewApps(t, p)
	if len(copies) != 1 || len(deployer.requests) != 1 {
		t.Fatalf("without the setting the preview has %d apps and %d deploys", len(copies), len(deployer.requests))
	}
}

func TestPreviewsOfTheWholeEnvironmentAreAnAdminsToTurnOn(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	path := "/api/environments/" + acme.env.ID

	member := h.newMember(acme, "dev", store.RoleMember)
	if status, _ := h.do(member, http.MethodPatch, path, map[string]any{"preview_stack": true}); status != http.StatusForbidden {
		t.Fatalf("a member turning it on answered %d", status)
	}
	status, body := h.do(acme, http.MethodPatch, path, map[string]any{"preview_stack": true})
	if status != http.StatusOK {
		t.Fatalf("answered %d: %s", status, body)
	}
	if env, _ := h.db.GetEnvironment(t.Context(), acme.env.ID); !env.PreviewStack || env.PodSecurity != "restricted" {
		t.Fatalf("stored %+v", env)
	}
	if entries, _ := h.db.ListAudit(t.Context(), acme.team.ID, "environment.previews_changed", acme.env.ID, 5); len(entries) != 1 {
		t.Fatal("not audited")
	}
	// The confinement level still changes on its own.
	if status, body := h.do(acme, http.MethodPatch, path, map[string]any{"pod_security": "baseline"}); status != http.StatusOK {
		t.Fatalf("changing the level answered %d: %s", status, body)
	}
	if env, _ := h.db.GetEnvironment(t.Context(), acme.env.ID); !env.PreviewStack || env.PodSecurity != "baseline" {
		t.Fatalf("stored %+v", env)
	}

	preview := store.Environment{ProjectID: acme.project.ID, Name: "pr-1", Slug: "pr-1", Kind: store.EnvPreview,
		SourceRef: "pr-1", Namespace: "acme-pr-1"}
	if err := h.db.CreateEnvironment(t.Context(), &preview); err != nil {
		t.Fatal(err)
	}
	if status, _ := h.do(acme, http.MethodPatch, "/api/environments/"+preview.ID, map[string]any{"preview_stack": true}); status != http.StatusBadRequest {
		t.Fatalf("a preview's previews answered %d", status)
	}
}

func TestAnAppsSeedIsKeptAndRunsOncePerPreview(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	app.SourceType, app.CPURequestM, app.CPULimitM, app.MemRequestMB, app.MemLimitMB = "git", 50, 1000, 128, 512
	if err := h.db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(acme, http.MethodPatch, "/api/apps/"+app.ID, map[string]any{"preview_seed": "bin/rails db:seed"}); status != http.StatusOK {
		t.Fatalf("answered %d: %s", status, body)
	}
	stored, _ := h.db.GetApp(t.Context(), app.ID)
	if stored.PreviewSeed != "bin/rails db:seed" {
		t.Fatalf("stored %q", stored.PreviewSeed)
	}
	// Two deploys finishing together: one of them seeds.
	first, err := h.db.MarkAppSeeded(t.Context(), app.ID)
	if err != nil || !first {
		t.Fatalf("the first mark: %v %v", first, err)
	}
	if again, _ := h.db.MarkAppSeeded(t.Context(), app.ID); again {
		t.Fatal("a preview was seeded twice")
	}
}

func httptestRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/webhooks/git/src", nil)
}

func pullRequest(number int) gitsrc.PushEvent {
	return gitsrc.PushEvent{Kind: "pull_request_opened", PullRequest: number, SourceBranch: "feature",
		CommitSHA: "abc", RepoURL: "https://github.com/acme/web"}
}
