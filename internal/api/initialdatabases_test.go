package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"skifity/internal/store"
)

// Creating an app together with the databases detection said it needs.
//
// The order is the whole point. The first deploy of an app written with an
// assistant used to crash on a DATABASE_URL nothing had set, because making the
// database, linking it and deploying were three screens and somebody who has
// never deployed does not know which comes first. These tests hold the order
// and the rest of the promise: nothing half-made, and a failed database not
// taking the app down with it.

// recorder notes what happened, in order, across the fakes.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) note(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// fakeDatabases stands in for internal/dbsvc. Link writes the variable the way
// the real one does, so the test can see it land on the app.
type fakeDatabases struct {
	log     *recorder
	db      *store.DB
	failFor string
}

func (f *fakeDatabases) Create(_ context.Context, env store.Environment, req CreateDatabaseRequest) (store.Database, error) {
	if req.Engine == f.failFor {
		return store.Database{}, errors.New("the cluster said no")
	}
	f.log.note("create " + req.Engine)
	return store.Database{ID: "db_" + req.Engine, Name: req.Name, Engine: req.Engine, EnvironmentID: env.ID}, nil
}

func (f *fakeDatabases) Link(ctx context.Context, databaseID, appID, varName string) error {
	f.log.note("link " + databaseID + " as " + varName)
	variable := store.Variable{AppID: appID, Key: varName, IsSecret: true}
	return f.db.SetVariable(ctx, &variable, "sealed-in-test")
}

func (f *fakeDatabases) Delete(context.Context, string) error { return nil }
func (f *fakeDatabases) Credentials(context.Context, string) (DatabaseCredentials, error) {
	return DatabaseCredentials{}, nil
}
func (f *fakeDatabases) Unlink(context.Context, string, string) error { return nil }
func (f *fakeDatabases) Status(context.Context, string) (string, string, error) {
	return "", "", nil
}

// fakeDeployer records when the first deploy starts.
type fakeDeployer struct{ log *recorder }

func (f *fakeDeployer) Deploy(_ context.Context, req DeployRequest) (store.Deployment, error) {
	f.log.note("deploy")
	return store.Deployment{ID: "dep_1", AppID: req.AppID, Number: 1}, nil
}
func (f *fakeDeployer) Rollback(context.Context, string, string, string) (store.Deployment, error) {
	return store.Deployment{}, nil
}
func (f *fakeDeployer) Cancel(context.Context, string) error { return nil }
func (f *fakeDeployer) Sync(context.Context, string) error   { return nil }
func (f *fakeDeployer) ScalingReadiness(context.Context, string) ([]ScalingFinding, error) {
	return nil, nil
}
func (f *fakeDeployer) RunOnce(context.Context, string, string) (RunHandle, error) {
	return RunHandle{}, nil
}
func (f *fakeDeployer) RunLogs(context.Context, string, string, bool) (io.ReadCloser, error) {
	return nil, nil
}

func withDatabases(t *testing.T, failFor string) (*harness, *recorder) {
	t.Helper()
	h := newHarness(t)
	log := &recorder{}
	h.api.databases = &fakeDatabases{log: log, db: h.db, failFor: failFor}
	h.api.deployer = &fakeDeployer{log: log}
	return h, log
}

type createAnswer struct {
	App       store.App        `json:"app"`
	Databases []databaseResult `json:"databases"`
}

func TestADatabaseIsLinkedBeforeTheFirstDeploy(t *testing.T) {
	h, log := withDatabases(t, "")
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "shop", "repo_url": "https://github.com/acme/shop", "deploy": true,
		"databases": []map[string]string{{"engine": "postgres", "variable": "DATABASE_URL"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	var answer createAnswer
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}

	events := log.all()
	want := []string{"create postgres", "link db_postgres as DATABASE_URL", "deploy"}
	if strings.Join(events, " | ") != strings.Join(want, " | ") {
		t.Fatalf("the steps happened as %v, want %v — the first start has to find DATABASE_URL there", events, want)
	}
	if len(answer.Databases) != 1 || answer.Databases[0].DatabaseID != "db_postgres" || answer.Databases[0].Error != "" {
		t.Errorf("the answer does not report the database: %+v", answer.Databases)
	}

	rows, err := h.db.ListVariables(t.Context(), answer.App.ID)
	if err != nil {
		t.Fatalf("list variables: %v", err)
	}
	if len(rows) != 1 || rows[0].Key != "DATABASE_URL" || !rows[0].IsSecret {
		t.Errorf("the connection string is not on the app as a secret: %+v", rows)
	}
}

// A name the app does not read is a name that does nothing. Leaving it out
// takes the panel's default for the engine.
func TestAnOmittedVariableTakesTheEnginesDefault(t *testing.T) {
	h, log := withDatabases(t, "")
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "shop", "repo_url": "https://github.com/acme/shop",
		"databases": []map[string]string{{"engine": "redis"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	if got := strings.Join(log.all(), " | "); !strings.Contains(got, "link db_redis as REDIS_URL") {
		t.Errorf("redis was linked as something else: %s", got)
	}
}

// A request that cannot be honoured is refused before anything exists, so it
// cannot leave a half-made app behind.
func TestABadDatabaseRequestCreatesNothing(t *testing.T) {
	for _, tc := range []struct {
		name      string
		databases []map[string]string
	}{
		{"an engine this panel does not run", []map[string]string{{"engine": "mongodb"}}},
		{"the same engine twice", []map[string]string{{"engine": "postgres"}, {"engine": "postgres"}}},
		{"a variable name that is not one", []map[string]string{{"engine": "postgres", "variable": "not a name"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, log := withDatabases(t, "")
			acme := h.newTenant("acme")

			status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
				"name": "shop", "repo_url": "https://github.com/acme/shop", "databases": tc.databases,
			})
			if status != http.StatusBadRequest {
				t.Fatalf("got %d, want 400: %s", status, body)
			}
			apps, err := h.db.ListApps(t.Context(), acme.env.ID)
			if err != nil {
				t.Fatalf("list apps: %v", err)
			}
			if len(apps) != 0 {
				t.Errorf("a refused request left an app behind: %+v", apps)
			}
			if events := log.all(); len(events) != 0 {
				t.Errorf("something was created anyway: %v", events)
			}
		})
	}
}

// A database the cluster would not make does not take the app with it: the app
// is what somebody asked for, and the database can be retried from its page.
func TestAFailedDatabaseDoesNotUndoTheApp(t *testing.T) {
	h, _ := withDatabases(t, "postgres")
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "shop", "repo_url": "https://github.com/acme/shop",
		"databases": []map[string]string{{"engine": "postgres"}, {"engine": "redis"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	var answer createAnswer
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.App.ID == "" {
		t.Fatal("the app was not created")
	}
	if len(answer.Databases) != 2 {
		t.Fatalf("both databases should be reported: %+v", answer.Databases)
	}
	for _, result := range answer.Databases {
		switch result.Engine {
		case "postgres":
			if result.Error == "" {
				t.Error("the failure was not reported")
			}
		case "redis":
			if result.Error != "" || result.DatabaseID == "" {
				t.Errorf("one failure stopped the other: %+v", result)
			}
		}
	}
}

// A pasted .env usually has the address of the database on the computer it was
// written on. The database created with the app owns that name: the pasted one
// is not stored, even when the database fails, because then it would be the
// app's only DATABASE_URL and it would point at somebody's localhost.
func TestADatabaseCreatedWithTheAppOwnsItsVariable(t *testing.T) {
	h, _ := withDatabases(t, "postgres")
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "shop", "repo_url": "https://github.com/acme/shop",
		"variables": map[string]string{"DATABASE_URL": "postgres://me@localhost/shop", "SITE_NAME": "Shop"},
		"databases": []map[string]string{{"engine": "postgres", "variable": "DATABASE_URL"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	var answer createAnswer
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatal(err)
	}
	rows, err := h.db.ListVariables(t.Context(), answer.App.ID)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, row := range rows {
		keys = append(keys, row.Key)
	}
	if strings.Join(keys, ",") != "SITE_NAME" {
		t.Fatalf("the app has %v; the pasted DATABASE_URL should have been left to the database", keys)
	}
}
