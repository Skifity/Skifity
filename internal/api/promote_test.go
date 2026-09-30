package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// capturingDeployer keeps the requests it was given.
type capturingDeployer struct {
	fakeDeployer
	requests []DeployRequest
}

func (c *capturingDeployer) Deploy(ctx context.Context, req DeployRequest) (store.Deployment, error) {
	c.requests = append(c.requests, req)
	return c.fakeDeployer.Deploy(ctx, req)
}

// stagingOf is a second environment in the tenant's project, with an app of
// the same name that has deployed once.
func stagingOf(t *testing.T, h *harness, owner tenant, name string) (store.App, store.Deployment) {
	t.Helper()
	staging := store.Environment{ProjectID: owner.project.ID, Name: "Staging", Slug: "staging",
		Namespace: owner.env.Namespace + "-staging"}
	if err := h.db.CreateEnvironment(t.Context(), &staging); err != nil {
		t.Fatal(err)
	}
	app := store.App{EnvironmentID: staging.ID, Name: name, Slug: name, SourceType: "git",
		RepoURL: "https://github.com/acme/shop", Replicas: 1}
	if err := h.db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	deployment := store.Deployment{AppID: app.ID, Status: store.DeploySucceeded, Trigger: "push",
		CommitSHA: "abc123", CommitMessage: "Fix the basket", Image: "registry.local/staging/web:abc123",
		BuildFingerprint: "fp-staging"}
	if err := h.db.CreateDeployment(t.Context(), &deployment); err != nil {
		t.Fatal(err)
	}
	if err := h.db.UpdateDeploymentStatus(t.Context(), deployment.ID, store.DeploySucceeded, "", "", ""); err != nil {
		t.Fatal(err)
	}
	return app, deployment
}

func TestAVersionIsPromotedToTheSameAppInAnotherEnvironment(t *testing.T) {
	h := newHarness(t)
	deployer := &capturingDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}
	h.api.deployer = deployer
	acme := h.newTenant("acme")
	production := store.App{EnvironmentID: acme.env.ID, Name: "web", Slug: "web", SourceType: "git",
		RepoURL: "https://github.com/acme/shop", Replicas: 1}
	if err := h.db.CreateApp(t.Context(), &production); err != nil {
		t.Fatal(err)
	}
	staging, version := stagingOf(t, h, acme, "web")

	// Where staging's versions can go.
	_, body := h.do(acme, http.MethodGet, "/api/apps/"+staging.ID+"/promote", nil)
	if !strings.Contains(body, production.ID) {
		t.Fatalf("production is not offered: %s", body)
	}

	status, body := h.do(acme, http.MethodPost, "/api/apps/"+production.ID+"/promote",
		map[string]any{"deployment_id": version.ID})
	if status != http.StatusAccepted {
		t.Fatalf("promoting answered %d: %s", status, truncate(body, 200))
	}
	req := deployer.requests[len(deployer.requests)-1]
	if req.AppID != production.ID || req.Trigger != "promote" || req.Image != version.Image ||
		req.Fingerprint != "fp-staging" || req.CommitSHA != "abc123" || req.Force {
		t.Fatalf("the deploy asked for was %+v", req)
	}
	if events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "app.promoted", "", 10); len(events) != 1 {
		t.Fatal("the promotion was not audited")
	}
}

func TestOnlyADeployedVersionOfTheSameProjectIsPromoted(t *testing.T) {
	h := newHarness(t)
	h.api.deployer = &capturingDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}
	acme := h.newTenant("acme")
	production := h.app(acme, "web")
	staging, version := stagingOf(t, h, acme, "web")
	path := "/api/apps/" + production.ID + "/promote"

	// A version that did not deploy has no image to run.
	failed := store.Deployment{AppID: staging.ID, Status: store.DeployFailed, Trigger: "push"}
	if err := h.db.CreateDeployment(t.Context(), &failed); err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(acme, http.MethodPost, path, map[string]any{"deployment_id": failed.ID}); status != http.StatusConflict ||
		!strings.Contains(body, "promote.not_possible") {
		t.Fatalf("a failed version answered %d: %s", status, truncate(body, 200))
	}
	// The app's own version is a rollback, not a promotion.
	if status, _ := h.do(acme, http.MethodPost, "/api/apps/"+staging.ID+"/promote",
		map[string]any{"deployment_id": version.ID}); status != http.StatusConflict {
		t.Fatalf("promoting to itself answered %d", status)
	}
	// Another team's version is not one this team can name.
	other := h.newTenant("other")
	_, theirs := stagingOf(t, h, other, "web")
	if status, _ := h.do(acme, http.MethodPost, path, map[string]any{"deployment_id": theirs.ID}); status != http.StatusNotFound {
		t.Fatalf("another team's version answered %d", status)
	}
	// Another project of the same team is not another stage of this app.
	project := store.Project{TeamID: acme.team.ID, Name: "Blog", Slug: "blog"}
	if err := h.db.CreateProject(t.Context(), &project); err != nil {
		t.Fatal(err)
	}
	blog := store.Environment{ProjectID: project.ID, Name: "Production", Slug: "production", Namespace: "acme-blog-production"}
	if err := h.db.CreateEnvironment(t.Context(), &blog); err != nil {
		t.Fatal(err)
	}
	blogApp := store.App{EnvironmentID: blog.ID, Name: "web", Slug: "web", SourceType: "git", Replicas: 1}
	if err := h.db.CreateApp(t.Context(), &blogApp); err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(acme, http.MethodPost, "/api/apps/"+blogApp.ID+"/promote",
		map[string]any{"deployment_id": version.ID}); status != http.StatusConflict || !strings.Contains(body, "promote.not_possible") {
		t.Fatalf("another project answered %d: %s", status, truncate(body, 200))
	}
}

func TestAPromotionRefusedLeavesTheImageAsItWas(t *testing.T) {
	// An image app's image is set by promoting to it. A deploy refused by a
	// lock left it set anyway, for `skifity run` and the next deploy to pick
	// up without anybody deciding they should.
	h := newHarness(t)
	h.api.deployer = &refusingDeployer{fakeDeployer{log: &recorder{}}}
	acme := h.newTenant("acme")
	source, deployment := stagingOf(t, h, acme, "cache")
	source.SourceType, source.Image = "image", deployment.Image
	source.CPURequestM, source.MemRequestMB = 50, 128
	if err := h.db.UpdateApp(t.Context(), &source); err != nil {
		t.Fatal(err)
	}
	target := h.app(acme, "cache")
	target.SourceType, target.Image = "image", "valkey/valkey:8"
	if err := h.db.UpdateApp(t.Context(), &target); err != nil {
		t.Fatal(err)
	}
	status, body := h.do(acme, http.MethodPost, "/api/apps/"+target.ID+"/promote", map[string]any{"deployment_id": deployment.ID})
	if status < 400 || !strings.Contains(body, "deploy.locked") {
		t.Fatalf("a refused promotion answered %d: %s", status, body)
	}
	if after, _ := h.db.GetApp(t.Context(), target.ID); after.Image != "valkey/valkey:8" {
		t.Fatalf("after a refused promotion the app is set to run %s", after.Image)
	}
}
