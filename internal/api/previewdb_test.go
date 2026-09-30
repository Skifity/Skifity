package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"skifity/internal/crypto"
	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// A preview's database.
//
// A preview used to copy every variable of the app it previews, and a database
// link is a variable: the pull request's copy got production's DATABASE_URL.
// Where the environment's network policy is enforced that address is refused,
// so every preview of an app with a database failed; where it is not, a
// branch's migration ran against production. A preview now gets an empty
// database of its own, and production's address is never copied.

// previewDatabaseManager records what a preview asked to be made, and links the
// way the real one does: by writing the variable.
type previewDatabaseManager struct {
	fakeDatabases
	keyring *crypto.Keyring
	created []struct {
		envID string
		req   CreateDatabaseRequest
	}
}

func (m *previewDatabaseManager) Create(_ context.Context, env store.Environment, req CreateDatabaseRequest) (store.Database, error) {
	m.created = append(m.created, struct {
		envID string
		req   CreateDatabaseRequest
	}{env.ID, req})
	record := store.Database{Name: req.Name, Slug: "preview-db", Engine: req.Engine, EnvironmentID: env.ID, Status: "creating"}
	if err := m.db.CreateDatabase(context.Background(), &record); err != nil {
		return store.Database{}, err
	}
	return record, nil
}

func (m *previewDatabaseManager) Link(ctx context.Context, databaseID, appID, varName string) error {
	sealed, err := m.keyring.Seal([]byte("postgres://preview-db"), variableContext(appID, varName))
	if err != nil {
		return err
	}
	variable := store.Variable{AppID: appID, Key: varName, IsSecret: true}
	if err := m.db.SetVariable(ctx, &variable, sealed); err != nil {
		return err
	}
	return m.db.LinkDatabase(ctx, databaseID, appID, varName)
}

type previewDBHarness struct {
	*harness
	manager *previewDatabaseManager
	acme    tenant
	app     store.App
}

// withLinkedDatabase is an app linked to a production database as
// DATABASE_URL, beside an ordinary secret it also has.
func withLinkedDatabase(t *testing.T) previewDBHarness {
	t.Helper()
	h := newHarness(t)
	manager := &previewDatabaseManager{}
	manager.fakeDatabases = fakeDatabases{log: &recorder{}, db: h.db}
	manager.keyring = h.keyring
	h.api.databases = manager
	h.api.deployer = &fakeDeployer{log: &recorder{}}

	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	production := store.Database{
		EnvironmentID: acme.env.ID, Name: "shop", Slug: "shop", Engine: "postgres",
		EngineVersion: "17", Status: "running", Instances: 2, StorageGB: 20,
	}
	if err := h.db.CreateDatabase(t.Context(), &production); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"DATABASE_URL": "postgres://shop-rw.acme-production.svc.cluster.local/app",
		"STRIPE_KEY":   "sk_live_production",
	} {
		sealed, err := h.keyring.Seal([]byte(value), variableContext(app.ID, key))
		if err != nil {
			t.Fatal(err)
		}
		variable := store.Variable{AppID: app.ID, Key: key, IsSecret: true}
		if err := h.db.SetVariable(t.Context(), &variable, sealed); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.db.LinkDatabase(t.Context(), production.ID, app.ID, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	return previewDBHarness{harness: h, manager: manager, acme: acme, app: app}
}

func (p previewDBHarness) openPullRequest(t *testing.T, number int, fork bool) store.App {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/git/src", nil)
	if _, err := p.api.deployPreview(request, p.app, gitsrc.PushEvent{
		Kind: "pull_request_opened", PullRequest: number, SourceBranch: "feature",
		CommitSHA: "abc", Fork: fork,
	}); err != nil {
		t.Fatalf("deployPreview: %v", err)
	}
	envs, err := p.db.ListEnvironments(t.Context(), p.acme.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range envs {
		if env.Kind != store.EnvPreview {
			continue
		}
		apps, err := p.db.ListApps(t.Context(), env.ID)
		if err != nil || len(apps) != 1 {
			t.Fatalf("the preview has %d apps: %v", len(apps), err)
		}
		return apps[0]
	}
	t.Fatal("no preview environment was made")
	return store.App{}
}

func (p previewDBHarness) value(t *testing.T, appID, key string) (string, bool) {
	t.Helper()
	rows, err := p.db.ListVariables(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Key != key {
			continue
		}
		plaintext, err := p.keyring.Open(row.Sealed, variableContext(appID, key))
		if err != nil {
			t.Fatal(err)
		}
		return string(plaintext), true
	}
	return "", false
}

func TestAPreviewGetsADatabaseOfItsOwn(t *testing.T) {
	p := withLinkedDatabase(t)
	preview := p.openPullRequest(t, 12, false)

	if len(p.manager.created) != 1 {
		t.Fatalf("%d databases were made for the preview, want 1", len(p.manager.created))
	}
	made := p.manager.created[0]
	if made.envID != preview.EnvironmentID {
		t.Errorf("the preview's database was made in %s, not in the preview's environment %s",
			made.envID, preview.EnvironmentID)
	}
	if made.req.Engine != "postgres" || made.req.Version != "17" {
		t.Errorf("the preview got %s %s, not the engine and version production runs", made.req.Engine, made.req.Version)
	}
	// A throwaway copy for a pull request, whatever production asked for.
	if made.req.Instances != 1 || made.req.StorageGB != 1 {
		t.Errorf("the preview's database has %d instances and %d GB", made.req.Instances, made.req.StorageGB)
	}

	url, ok := p.value(t, preview.ID, "DATABASE_URL")
	if !ok || url != "postgres://preview-db" {
		t.Errorf("the preview's DATABASE_URL is %q, want its own database's", url)
	}
	// Everything else is still copied from a same-repository pull request.
	if stripe, _ := p.value(t, preview.ID, "STRIPE_KEY"); stripe != "sk_live_production" {
		t.Errorf("an ordinary variable was not copied: %q", stripe)
	}
}

// The one thing that must never happen: a pull request's code holding the
// production database's address.
func TestAPreviewNeverGetsProductionsDatabaseAddress(t *testing.T) {
	p := withLinkedDatabase(t)
	// No way to make a database: the preview has to start without the
	// variable rather than fall back to production's.
	p.api.databases = nil
	preview := p.openPullRequest(t, 12, false)

	if url, ok := p.value(t, preview.ID, "DATABASE_URL"); ok {
		t.Fatalf("a preview was given %q", url)
	}
}

// Anybody can open a pull request from a fork, and a database per pull request
// is a way for a stranger to fill somebody's server.
func TestAPreviewFromAForkIsGivenNoDatabase(t *testing.T) {
	p := withLinkedDatabase(t)
	preview := p.openPullRequest(t, 13, true)

	if len(p.manager.created) != 0 {
		t.Fatalf("a fork's preview made %d databases", len(p.manager.created))
	}
	if url, ok := p.value(t, preview.ID, "DATABASE_URL"); ok {
		t.Fatalf("a fork's preview was given %q", url)
	}
}

// Where a preview came from is recorded, because the deployer decides what a
// fork's preview may read at every deploy, long after the webhook is gone.
func TestAPreviewRemembersItCameFromAFork(t *testing.T) {
	p := withLinkedDatabase(t)
	forked := p.openPullRequest(t, 21, true)
	env, err := p.db.GetEnvironment(t.Context(), forked.EnvironmentID)
	if err != nil || !env.FromFork {
		t.Fatalf("a fork's preview environment does not say it came from a fork: %+v, %v", env, err)
	}
}
