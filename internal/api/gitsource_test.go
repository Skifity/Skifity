package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

func TestAnAppCannotBorrowAnotherTeamsGitConnection(t *testing.T) {
	// Detection already refused it; creating the app did not, and the
	// connection is what a build clones with and a webhook is made with.
	h := newHarness(t)
	acme := h.newTenant("acme")
	rival := h.newTenant("rival")
	theirs := store.GitSource{TeamID: rival.team.ID, Kind: "github_pat", Name: "rival", BaseURL: "https://github.com"}
	if err := h.db.CreateGitSource(t.Context(), &theirs); err != nil {
		t.Fatal(err)
	}
	ours := store.GitSource{TeamID: acme.team.ID, Kind: "github_pat", Name: "acme", BaseURL: "https://github.com"}
	if err := h.db.CreateGitSource(t.Context(), &ours); err != nil {
		t.Fatal(err)
	}

	create := func(source string) int {
		status, _ := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
			"name": "private-" + source, "source_type": "git",
			"repo_url": "https://github.com/rival/secret", "git_source_id": source,
		})
		return status
	}
	if status := create(theirs.ID); status != http.StatusNotFound {
		t.Fatalf("an app with another team's Git connection answered %d", status)
	}
	if status := create(ours.ID); status != http.StatusCreated {
		t.Fatalf("an app with the team's own connection answered %d", status)
	}

	status, _ := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/stack", map[string]any{
		"repo_url": "https://github.com/rival/secret", "git_source_id": theirs.ID,
		"services": []map[string]any{{"name": "web", "build": "."}},
	})
	if status != http.StatusNotFound {
		t.Fatalf("a stack with another team's Git connection answered %d", status)
	}
}

// pushDeployer records what a settings change asked of the deployer: a sync
// applies the change, a deploy would build.
type pushDeployer struct {
	fakeDeployer
}

func (p *pushDeployer) Sync(context.Context, string) error {
	p.log.note("sync")
	return nil
}

// hookCalls stands in for the Git host. The panel's own client will not dial
// the loopback a test server listens on, and gitsrc's tests cover the
// exchange with the host; what is tested here is when the panel asks.
type hookCalls struct {
	mu    sync.Mutex
	asked []gitsrc.HookRequest
}

func (c *hookCalls) register(_ context.Context, req gitsrc.HookRequest) gitsrc.HookResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, req)
	return gitsrc.HookResult{Created: true}
}

func (c *hookCalls) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.asked)
}

// connection is a Git connection of the team's with a token and a webhook
// secret, as connecting one leaves it.
func (h *harness) connection(owner tenant, name, baseURL string) store.GitSource {
	h.t.Helper()
	sealed, err := h.keyring.Seal([]byte(`{"token":"fake-token-for-tests","webhook_secret":"fake-hook-secret"}`),
		"git_source:"+owner.team.ID+":"+name)
	if err != nil {
		h.t.Fatal(err)
	}
	source := store.GitSource{TeamID: owner.team.ID, Kind: "github_pat", Name: name, BaseURL: baseURL, ConfigEnc: sealed}
	if err := h.db.CreateGitSource(h.t.Context(), &source); err != nil {
		h.t.Fatal(err)
	}
	return source
}

// gitApp is an app built from github.com/o/r, deploying on push.
func (h *harness) gitApp(owner tenant, name, sourceID string, autoDeploy bool) store.App {
	h.t.Helper()
	app := store.App{
		EnvironmentID: owner.env.ID, Name: name, Slug: name, Replicas: 1, SourceType: "git",
		RepoURL: "https://github.com/o/r", Branch: "main", GitSourceID: sourceID, AutoDeploy: autoDeploy,
		CPURequestM: 50, CPULimitM: 1000, MemRequestMB: 128, MemLimitMB: 512,
	}
	if err := h.db.CreateApp(h.t.Context(), &app); err != nil {
		h.t.Fatal(err)
	}
	return app
}

// The quick start's own path: a public repository, no connection, and deploy
// on push shown on. Nothing was registered and nothing said so.
func TestAGitAppWithNoConnectionIsToldNoWebhookWasRegistered(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	var answer struct {
		Webhook webhookStatus `json:"webhook"`
	}
	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "public", "source_type": "git", "repo_url": "https://github.com/o/r",
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	decodeInto(t, body, &answer)
	if answer.Webhook.Registered || answer.Webhook.ReasonCode != hookNoConnection || answer.Webhook.Reason == "" {
		t.Errorf("a Git app with no connection was answered %+v", answer.Webhook)
	}

	status, body = h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "nginx", "source_type": "image", "image": "nginx:1.27",
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	answer.Webhook = webhookStatus{}
	decodeInto(t, body, &answer)
	if answer.Webhook.ReasonCode != "" {
		t.Errorf("an image app was answered %+v", answer.Webhook)
	}
}

// An app made without a connection can be given one, and only one it may
// have: the team's, for the repository's host.
func TestAnAppCanBeGivenAGitConnectionAfterItWasMade(t *testing.T) {
	h := newHarness(t)
	log := &recorder{}
	h.api.deployer = &pushDeployer{fakeDeployer{log: log}}
	hooks := &hookCalls{}
	h.api.registerHook = hooks.register
	acme := h.newTenant("acme")
	rival := h.newTenant("rival")
	app := h.gitApp(acme, "web", "", true)

	theirs := h.connection(rival, "rival-github", "https://github.com")
	gitlab := h.connection(acme, "acme-gitlab", "https://gitlab.com")
	ours := h.connection(acme, "acme-github", "https://github.com")

	patch := func(sourceID string) (int, string) {
		return h.do(acme, http.MethodPatch, "/api/apps/"+app.ID, map[string]any{"git_source_id": sourceID})
	}
	stored := func() string {
		got, err := h.db.GetApp(t.Context(), app.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.GitSourceID
	}

	if status, body := patch(theirs.ID); status != http.StatusNotFound || stored() != "" {
		t.Fatalf("another team's connection answered %d and left %q: %s", status, stored(), body)
	}
	if status, body := patch(gitlab.ID); status != http.StatusBadRequest || stored() != "" ||
		!strings.Contains(body, "git.connection_wrong_host") {
		t.Fatalf("a connection for another host answered %d and left %q: %s", status, stored(), body)
	}
	if hooks.count() != 0 {
		t.Fatalf("a refused connection asked a Git host for a webhook %d times", hooks.count())
	}

	status, body := patch(ours.ID)
	if status != http.StatusOK || stored() != ours.ID {
		t.Fatalf("the team's own connection answered %d and left %q: %s", status, stored(), body)
	}
	var answer struct {
		ID      string         `json:"id"`
		Name    string         `json:"name"`
		Webhook *webhookStatus `json:"webhook"`
	}
	decodeInto(t, body, &answer)
	if answer.ID != app.ID || answer.Name != "web" || answer.Webhook == nil || !answer.Webhook.Registered {
		t.Fatalf("the answer is not the app with its webhook: %s", body)
	}
	if hooks.count() != 1 || hooks.asked[0].RepoURL != "https://github.com/o/r" ||
		!strings.HasSuffix(hooks.asked[0].DeliverTo, "/api/webhooks/git/"+ours.ID) {
		t.Fatalf("the Git host was asked %+v", hooks.asked)
	}
	// A connection is not part of the image: the change is applied, and
	// nothing is built.
	if events := strings.Join(log.all(), " "); !strings.Contains(events, "sync") || strings.Contains(events, "deploy") {
		t.Errorf("giving the app a connection did %q", events)
	}

	// And "" detaches it, which says why no webhook is registered.
	status, body = patch("")
	if status != http.StatusOK || stored() != "" || !strings.Contains(body, `"reason_code":"no_connection"`) {
		t.Errorf("detaching answered %d and left %q: %s", status, stored(), body)
	}
}

// The settings form sends auto_deploy and deploy_trigger with every save, so
// the repository is asked only when one of them changes what it means.
func TestDeployOnPushIsAskedForOnlyWhenItChanges(t *testing.T) {
	h := newHarness(t)
	hooks := &hookCalls{}
	h.api.registerHook = hooks.register
	acme := h.newTenant("acme")
	source := h.connection(acme, "acme-github", "https://github.com")
	app := h.gitApp(acme, "web", source.ID, false)

	save := func(fields map[string]any) {
		t.Helper()
		if status, body := h.do(acme, http.MethodPatch, "/api/apps/"+app.ID, fields); status != http.StatusOK {
			t.Fatalf("save %v: %d %s", fields, status, body)
		}
	}
	save(map[string]any{"auto_deploy": true, "deploy_trigger": "branch"})
	if hooks.count() != 1 {
		t.Fatalf("turning deploy on push on asked the host %d times, want once", hooks.count())
	}
	save(map[string]any{"auto_deploy": true, "deploy_trigger": "branch", "start_command": "npm start"})
	if hooks.count() != 1 {
		t.Fatalf("a save that changed nothing about pushes asked the host again (%d)", hooks.count())
	}
	save(map[string]any{"auto_deploy": true, "deploy_trigger": "tag"})
	if hooks.count() != 2 {
		t.Fatalf("deploying tags did not ask the host for a hook that carries them (%d)", hooks.count())
	}
}

// Giving an app a connection and asking for its webhook are a member's, like
// any other change to it, and only for the team's own apps.
func TestAViewerCannotGiveAnAppAConnectionOrAskForItsWebhook(t *testing.T) {
	h := newHarness(t)
	h.api.registerHook = (&hookCalls{}).register
	acme := h.newTenant("acme")
	rival := h.newTenant("rival")
	viewer := h.newMember(acme, "viewer", store.RoleViewer)
	source := h.connection(acme, "acme-github", "https://github.com")
	app := h.gitApp(acme, "web", "", true)

	if status, _ := h.do(viewer, http.MethodPatch, "/api/apps/"+app.ID, map[string]any{"git_source_id": source.ID}); status != http.StatusForbidden {
		t.Errorf("a viewer giving an app a connection answered %d", status)
	}
	if status, _ := h.do(viewer, http.MethodPost, "/api/apps/"+app.ID+"/webhook", nil); status != http.StatusForbidden {
		t.Errorf("a viewer asking for a webhook answered %d", status)
	}
	if status, _ := h.do(rival, http.MethodPost, "/api/apps/"+app.ID+"/webhook", nil); status != http.StatusNotFound {
		t.Errorf("another team asking for this app's webhook answered %d", status)
	}

	status, body := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/webhook", nil)
	if status != http.StatusOK || !strings.Contains(body, `"reason_code":"no_connection"`) {
		t.Errorf("asking for the webhook of an app with no connection answered %d %s", status, body)
	}
}
