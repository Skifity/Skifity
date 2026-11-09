package api

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/dbsvc/engine"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/store"
	"skifity/internal/templates"
)

// A template that does not name the variable its database arrives as falls back
// to a default per engine. A missing default links the app to its database
// through a variable called "", which the app cannot read and nothing reports.
//
// The engines come from the catalogue in internal/dbsvc/engine, which
// imports nothing of the panel's; internal/dbsvc itself imports this package.
func TestEveryEngineHasADefaultVariableName(t *testing.T) {
	for _, name := range engine.Names() {
		if defaultVarNameFor(name) == "" {
			t.Errorf("a %s database with no var_name would arrive as an empty variable name", name)
		}
	}
	// Every MariaDB made before it was offered apart was a "mysql" linked as
	// MYSQL_URL. A blueprint that links one by default must keep finding the
	// variable its app already reads.
	if defaultVarNameFor(engine.MariaDB) != defaultVarNameFor(engine.MySQL) {
		t.Errorf("mariadb is linked as %s by default, and the mysql databases it used to be as %s",
			defaultVarNameFor(engine.MariaDB), defaultVarNameFor(engine.MySQL))
	}
}

// TestEveryTemplateDatabaseLinksToEveryAppThatNeedsIt: a stack is usually a web
// app and a worker sharing one database, and link_to used to be a single name.
// Linking the first app only leaves the worker starting without the variable it
// cannot run without — the same crash loop with no reason on screen as a link
// that names nothing.
func TestEveryTemplateDatabaseLinksToAServiceThatExists(t *testing.T) {
	for _, tpl := range templates.All() {
		services := map[string]bool{}
		for _, svc := range tpl.Services {
			services[svc.Name] = true
		}
		for _, db := range tpl.Databases {
			if len(db.LinkTo) == 0 {
				t.Errorf("%s: the database %s is created and linked to nothing", tpl.ID, db.Name)
			}
			for _, target := range db.LinkTo {
				if !services[target] {
					t.Errorf("%s: the database %s links to %q, which this template does not have",
						tpl.ID, db.Name, target)
				}
			}
		}
	}
}

// Every template's databases must name an engine that has such a default.
func TestEveryTemplateDatabaseArrivesAsAVariable(t *testing.T) {
	for _, tpl := range templates.All() {
		for _, db := range tpl.Databases {
			name := db.VarName
			if name == "" {
				name = defaultVarNameFor(db.Engine)
			}
			if name == "" {
				t.Errorf("the %s template's %s database would arrive as an empty variable name",
					tpl.ID, db.Name)
			}
		}
	}
}

// acceptRoot lets an environment run images that start as root, which two
// thirds of the catalogue's templates have, for a test about something else.
func (h *harness) acceptRoot(t *testing.T, as *tenant) {
	t.Helper()
	if err := h.db.SetEnvironmentPodSecurity(t.Context(), as.env.ID, "baseline"); err != nil {
		t.Fatal(err)
	}
	as.env.PodSecurity = "baseline"
}

// installDirectly plans and installs a template the way the handler does,
// for a template that is not in any catalogue the handler could look it up in.
func (h *harness) installDirectly(t *testing.T, as tenant, tpl templates.Template, name string, values map[string]string) (installedTemplate, error) {
	t.Helper()
	plan, err := h.api.planTemplateInstall(t.Context(), as.env, tpl, installTemplateRequest{Name: name, Values: values})
	if err != nil {
		return installedTemplate{}, err
	}
	request := httptest.NewRequest(http.MethodPost, "/api/templates/"+tpl.ID+"/install", nil).WithContext(t.Context())
	return h.api.installTemplate(request, plan, as.user)
}

// A template can say how its image starts, give it the files it is
// configured by, and hand it its database as a host, a port, a user and a
// password — the three things software written for Compose takes for
// granted, and the reason dozens of apps could not be offered at all.
func TestATemplateCanStartItsImageItsWayWithItsFilesAndDatabasePieces(t *testing.T) {
	h := newHarness(t)
	log := &recorder{}
	h.api.databases = &fakeDatabases{log: log, db: h.db}
	h.api.deployer = &fakeDeployer{log: log}
	acme := h.newTenant("acme")

	tpl := templates.Template{
		ID: "assets", Name: "Assets",
		Services: []templates.Service{
			{Name: "web", Image: "example/assets:1.2.3", Port: 80, Public: true,
				Files: []templates.FileSpec{{Path: "/etc/assets/app.ini", Content: "debug = false\n"},
					{Path: "/docker-entrypoint.d/10-init.sh", Content: "#!/bin/sh\n", Executable: true}}},
			{Name: "worker", Image: "example/assets:1.2.3", Command: "php artisan queue:work"},
		},
		Databases: []templates.DatabaseSpec{{Name: "assets-db", Engine: "mysql", LinkTo: []string{"web", "worker"},
			VarName: "DATABASE_URL",
			Vars:    templates.DatabaseVars{Host: "DB_HOST", Port: "DB_PORT", Name: "DB_DATABASE", User: "DB_USERNAME", Password: "DB_PASSWORD"}}},
	}
	// Announced as a hand-made app is, so a project page open elsewhere
	// shows them without a reload.
	stream := h.api.hub.Subscribe(t.Context(), 0, events.TeamTopic(acme.team.ID))
	defer stream.Close()

	result, err := h.installDirectly(t, acme, tpl, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	apps := map[string]store.App{}
	for _, app := range result.Apps {
		apps[app.Name] = app
	}
	announced := 0
	for announced < len(result.Apps) {
		select {
		case event := <-stream.Events():
			if event.Type == "app.created" {
				announced++
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%d of %d apps were announced", announced, len(result.Apps))
		}
	}
	if apps["worker"].StartCommand != "php artisan queue:work" || apps["web"].StartCommand != "" {
		t.Errorf("start commands are web %q, worker %q", apps["web"].StartCommand, apps["worker"].StartCommand)
	}

	files, err := h.db.ListFiles(t.Context(), apps["web"].ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("the web app has files %+v (%v)", files, err)
	}
	for _, f := range files {
		content, err := h.keyring.Open(f.Sealed, store.FileContext(apps["web"].ID, f.Path))
		if err != nil || len(content) == 0 {
			t.Errorf("%s did not arrive sealed for the app: %v", f.Path, err)
		}
		if f.Path == "/docker-entrypoint.d/10-init.sh" && !f.Executable {
			t.Error("the script arrived without being executable")
		}
	}

	// The pieces are the link's to write, keep current and remove: the
	// install hands them to it, and writes none of them itself.
	want := store.LinkPieces{Host: "DB_HOST", Port: "DB_PORT", Name: "DB_DATABASE", User: "DB_USERNAME", Password: "DB_PASSWORD"}
	for _, name := range []string{"web", "worker"} {
		if pieces, ok := log.piecesOf(apps[name].ID); !ok || pieces != want {
			t.Errorf("%s was linked with the pieces %+v (given %v)", name, pieces, ok)
		}
		rows, err := h.db.ListVariables(t.Context(), apps[name].ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if slices.Contains(want.Names(), row.Key) {
				t.Errorf("%s: the install wrote %s itself, outside the link", name, row.Key)
			}
		}
	}
}

// An install is all or nothing.
//
// These hold the promise from both ends: everything that can be known before
// anything is written is found by the plan, and stops the install with nothing
// made; what can only fail while writing removes what was made. In between, a
// dry run answers the plan and writes nothing.

// storedDatabases keeps its databases in the panel's store, as the real
// manager does, so a second install finds the names the first one took.
type storedDatabases struct {
	fakeDatabases
	// failDelete makes removing a database fail, as a cluster that has gone
	// away would.
	failDelete bool
}

func (f *storedDatabases) Create(ctx context.Context, env store.Environment, req CreateDatabaseRequest) (store.Database, error) {
	if req.Engine == f.failFor {
		return store.Database{}, errdoc.ClusterUnreachable(errors.New("the cluster said no"))
	}
	record := store.Database{EnvironmentID: env.ID, Name: req.Name, Slug: kube.Slugify(req.Name),
		Engine: req.Engine, Status: "creating", Instances: 1, StorageGB: req.StorageGB}
	if err := f.db.CreateDatabase(ctx, &record); err != nil {
		return store.Database{}, err
	}
	f.log.note("create " + record.Name)
	return record, nil
}

func (f *storedDatabases) Link(ctx context.Context, databaseID, appID, varName string, pieces *store.LinkPieces) error {
	if err := f.db.LinkDatabase(ctx, databaseID, appID, varName); err != nil {
		return err
	}
	return f.fakeDatabases.Link(ctx, databaseID, appID, varName, pieces)
}

func (f *storedDatabases) Credentials(ctx context.Context, databaseID string) (DatabaseCredentials, error) {
	record, err := f.db.GetDatabase(ctx, databaseID)
	if err != nil {
		return DatabaseCredentials{}, err
	}
	return DatabaseCredentials{Engine: record.Engine, Host: record.Slug + ".acme-production.svc", Port: 3306,
		Database: "app", Username: "app", Password: "not-a-real-password", URL: "mysql://app@" + record.Slug}, nil
}

func (f *storedDatabases) Delete(ctx context.Context, databaseID string) error {
	if f.failDelete {
		return errors.New("the cluster is not answering")
	}
	f.log.note("delete " + databaseID)
	return f.db.DeleteDatabase(ctx, databaseID)
}

func withStoredDatabases(t *testing.T) (*harness, *storedDatabases) {
	t.Helper()
	h := newHarness(t)
	databases := &storedDatabases{fakeDatabases: fakeDatabases{log: &recorder{}, db: h.db}}
	h.api.databases = databases
	return h, databases
}

// rowCounts is how many of everything an install makes there are.
func (h *harness) rowCounts(t *testing.T) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{"apps", "databases", "database_links", "volumes", "app_variables",
		"app_ports", "app_files", "app_templates"} {
		var n int
		if err := h.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = n
	}
	return counts
}

// noAppAnnounced fails when anything was said about an app being created.
func noAppAnnounced(t *testing.T, stream *events.Subscription) {
	t.Helper()
	for {
		select {
		case event := <-stream.Events():
			if event.Type == "app.created" {
				t.Errorf("an app was announced as created by an install that made nothing: %s", event.Data)
			}
		case <-time.After(200 * time.Millisecond):
			return
		}
	}
}

// WordPress installed a second time, named shop, made the app shop and then
// failed on the database name the template fixes — leaving shop behind with
// no database, under an error that said nothing was created. A template of one
// app installed under a name of its own names its databases after it.
func TestASecondCopyOfATemplateTakesNamesOfItsOwn(t *testing.T) {
	h, _ := withStoredDatabases(t)
	acme := h.newTenant("acme")
	h.acceptRoot(t, &acme)

	for _, name := range []string{"WordPress", "shop"} {
		status, body := h.do(acme, http.MethodPost, "/api/templates/wordpress/install",
			map[string]any{"environment_id": acme.env.ID, "name": name})
		if status != http.StatusCreated {
			t.Fatalf("installing WordPress as %s: %d %s", name, status, body)
		}
	}
	apps, err := h.db.ListApps(t.Context(), acme.env.ID)
	if err != nil {
		t.Fatal(err)
	}
	slugs := map[string]string{}
	for _, app := range apps {
		slugs[app.Slug] = app.ID
	}
	databases, err := h.db.ListDatabases(t.Context(), acme.env.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked := map[string]string{}
	for _, database := range databases {
		links, err := h.db.ListLinksForDatabase(t.Context(), database.ID)
		if err != nil || len(links) != 1 {
			t.Fatalf("%s is linked %+v (%v)", database.Name, links, err)
		}
		linked[database.Slug] = links[0].AppID
	}
	if len(apps) != 2 || slugs["wordpress"] == "" || slugs["shop"] == "" {
		t.Fatalf("the apps are %v", slugs)
	}
	if linked["wordpress-db"] != slugs["wordpress"] || linked["shop-db"] != slugs["shop"] {
		t.Errorf("the databases are linked %v, and the apps are %v", linked, slugs)
	}
}

// Another app's public port, wanted by the second of a template's services,
// used to leave the first service's app behind. It is found before anything
// is made, and nothing is announced.
func TestAPortTakenStopsTheInstallBeforeAnythingIsMade(t *testing.T) {
	h, _ := withStoredDatabases(t)
	acme := h.newTenant("acme")
	other := h.app(acme, "game-server")
	if err := h.db.AddPort(t.Context(), &store.AppPort{AppID: other.ID, Port: 25565, Protocol: "tcp", PublicPort: 25565}); err != nil {
		t.Fatal(err)
	}
	tpl := templates.Template{
		ID: "arcade", Name: "Arcade",
		Services: []templates.Service{
			{Name: "lobby", Image: "example/lobby:1.2.3", Port: 8080, Public: true,
				Volumes: []templates.VolumeSpec{{Name: "data", MountPath: "/data", SizeGB: 1}}},
			{Name: "game", Image: "example/game:1.2.3", Ports: []templates.PortSpec{{Port: 25565}}},
		},
		Databases: []templates.DatabaseSpec{{Name: "arcade-db", Engine: "postgres", StorageGB: 5,
			LinkTo: []string{"lobby"}, VarName: "DATABASE_URL"}},
	}
	before := h.rowCounts(t)
	stream := h.api.hub.Subscribe(t.Context(), 0, events.TeamTopic(acme.team.ID))
	defer stream.Close()

	_, err := h.installDirectly(t, acme, tpl, "", nil)
	problem := errdoc.From(err)
	if problem == nil || problem.Code != "port.taken" || problem.Status != http.StatusConflict {
		t.Fatalf("the install answered %v", err)
	}
	if after := h.rowCounts(t); !maps.Equal(before, after) {
		t.Errorf("an install that was refused changed %v into %v", before, after)
	}
	noAppAnnounced(t, stream)
}

// What can only fail while writing — the cluster refusing a database — comes
// after the apps exist. They are removed, and the answer says so.
func TestADatabaseRefusedRemovesTheAppsMadeBeforeIt(t *testing.T) {
	h, databases := withStoredDatabases(t)
	databases.failFor = "mariadb"
	acme := h.newTenant("acme")
	h.acceptRoot(t, &acme)
	before := h.rowCounts(t)
	stream := h.api.hub.Subscribe(t.Context(), 0, events.TeamTopic(acme.team.ID))
	defer stream.Close()

	status, body := h.do(acme, http.MethodPost, "/api/templates/wordpress/install",
		map[string]any{"environment_id": acme.env.ID})
	// With the status of what stopped it, so a cluster that is not there is
	// still a 503 and a name taken still a 409.
	if code := problemCode(body); code != "template.install_rolled_back" || status != http.StatusServiceUnavailable {
		t.Fatalf("a refused database answered %d %s", status, body)
	}
	if !strings.Contains(body, "The cluster is not responding") || !strings.Contains(body, "nothing was deployed: wordpress.") {
		t.Errorf("the answer does not say what stopped it or what was removed: %s", body)
	}
	if after := h.rowCounts(t); !maps.Equal(before, after) {
		t.Errorf("an install that was undone changed %v into %v", before, after)
	}
	noAppAnnounced(t, stream)
	if entries, err := h.db.ListAudit(t.Context(), acme.team.ID, "template.installed", "", 10); err != nil || len(entries) != 0 {
		t.Errorf("an undone install was recorded as installed: %+v (%v)", entries, err)
	}
}

// An undo that cannot finish says what is still there.
func TestAnUndoThatCannotFinishNamesWhatIsLeft(t *testing.T) {
	h, databases := withStoredDatabases(t)
	databases.failFor, databases.failDelete = "redis", true
	acme := h.newTenant("acme")
	tpl := templates.Template{
		ID: "board", Name: "Board",
		Services: []templates.Service{{Name: "board", Image: "example/board:1.2.3", Port: 3000, Public: true}},
		Databases: []templates.DatabaseSpec{
			{Name: "board-db", Engine: "postgres", StorageGB: 5, LinkTo: []string{"board"}, VarName: "DATABASE_URL"},
			{Name: "board-redis", Engine: "redis", StorageGB: 1, LinkTo: []string{"board"}, VarName: "REDIS_URL"},
		},
	}
	_, err := h.installDirectly(t, acme, tpl, "", nil)
	problem := errdoc.From(err)
	if problem.Code != "template.install_left_over" || !strings.Contains(problem.Impact, "board-db") ||
		strings.Contains(problem.Impact, "board,") {
		t.Fatalf("an undo that could not remove the database answered %+v", problem)
	}
	if apps, _ := h.db.ListApps(t.Context(), acme.env.ID); len(apps) != 0 {
		t.Errorf("the app was not removed: %+v", apps)
	}
}

// An app and a database in one environment share an address, so an app whose
// name a database already has is a name taken, found before anything is made.
func TestTheAppsNameIsCheckedAgainstTheDatabases(t *testing.T) {
	h, _ := withStoredDatabases(t)
	acme := h.newTenant("acme")
	h.database(acme, "notes")
	tpl := templates.Template{ID: "notes", Name: "Notes",
		Services: []templates.Service{{Name: "notes", Image: "example/notes:2.4.1", Port: 8080, Public: true}}}
	before := h.rowCounts(t)

	plan, err := h.api.planTemplateInstall(t.Context(), acme.env, tpl, installTemplateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Problems) != 1 || plan.Problems[0].Code != "resource.name_taken" || plan.Problems[0].Context["taken_by"] != "database" {
		t.Fatalf("the plan found %+v", plan.Problems)
	}
	if after := h.rowCounts(t); !maps.Equal(before, after) {
		t.Errorf("planning changed %v into %v", before, after)
	}
}

// A dry run answers the plan — the names, the disks, what stops it — and makes
// nothing, which is how the install dialog says a name is taken before Install
// is pressed.
func TestADryRunSaysWhatAnInstallWouldDoAndDoesNothing(t *testing.T) {
	h, _ := withStoredDatabases(t)
	acme := h.newTenant("acme")
	h.acceptRoot(t, &acme)
	if status, body := h.do(acme, http.MethodPost, "/api/templates/homer/install",
		map[string]any{"environment_id": acme.env.ID}); status != http.StatusCreated {
		t.Fatalf("installing Homer: %d %s", status, body)
	}
	game := h.app(acme, "game")
	if err := h.db.AddPort(t.Context(), &store.AppPort{AppID: game.ID, Port: 25565, Protocol: "tcp", PublicPort: 25565}); err != nil {
		t.Fatal(err)
	}
	before := h.rowCounts(t)

	var plan struct {
		Apps     []plannedTemplateApp    `json:"apps"`
		Volumes  []plannedTemplateVolume `json:"volumes"`
		Problems []errdoc.Problem        `json:"problems"`
	}
	for _, c := range []struct{ template, name, problem string }{
		{"homer", "", "resource.name_taken"},
		{"minecraft", "", "port.taken"},
		{"homer", "home", ""},
	} {
		status, body := h.do(acme, http.MethodPost, "/api/templates/"+c.template+"/install",
			map[string]any{"environment_id": acme.env.ID, "name": c.name, "dry_run": true})
		if status != http.StatusOK {
			t.Fatalf("a dry run of %s answered %d %s", c.template, status, body)
		}
		plan.Problems = nil
		if err := json.Unmarshal([]byte(body), &plan); err != nil {
			t.Fatal(err)
		}
		codes := []string{}
		for _, problem := range plan.Problems {
			codes = append(codes, problem.Code)
		}
		want := []string{}
		if c.problem != "" {
			want = append(want, c.problem)
		}
		if !slices.Equal(codes, want) {
			t.Errorf("a dry run of %s as %q found %v, want %v", c.template, c.name, codes, want)
		}
		if len(plan.Apps) != 1 || len(plan.Volumes) != 1 {
			t.Errorf("a dry run of %s planned %+v and %+v", c.template, plan.Apps, plan.Volumes)
		}
	}
	if plan.Apps[0].Slug != "home" || plan.Volumes[0].App != "home" || plan.Volumes[0].SizeGB != 1 {
		t.Errorf("Homer as home is planned as %+v with %+v", plan.Apps, plan.Volumes)
	}
	if after := h.rowCounts(t); !maps.Equal(before, after) {
		t.Errorf("a dry run changed %v into %v", before, after)
	}
}

// A database is named after the one app of its template when that app is
// given a name of its own, by the last word of its own name.
func TestADatabaseIsRenamedAfterItsApp(t *testing.T) {
	two := []templates.DatabaseSpec{{Name: "outline-db"}, {Name: "outline-redis"}}
	same := []templates.DatabaseSpec{{Name: "teable-db"}, {Name: "teable-cache-db"}}
	for _, c := range []struct {
		name string
		all  []templates.DatabaseSpec
		want string
	}{
		{"wordpress-db", []templates.DatabaseSpec{{Name: "wordpress-db"}}, "shop-db"},
		{"wordpress-with-mariadb-db", []templates.DatabaseSpec{{Name: "wordpress-with-mariadb-db"}}, "shop-db"},
		{"outline-redis", two, "shop-redis"},
		{"teable-cache-db", same, "shop-teable-cache-db"},
	} {
		if got := renamedDatabase("shop", c.name, c.all); got != c.want {
			t.Errorf("%s is renamed %s, want %s", c.name, got, c.want)
		}
	}
}

// A template whose image starts as root.
//
// The strictest level is the default and refuses such an image, and only an
// admin can change it. The install used to make the app anyway, and the
// person found out afterwards from an app that never started — and a member
// could do nothing about it. Now it is said before anything is made, and an
// admin can accept root images for the environment in the same step.

// namespaceCluster records the namespaces it is asked to bring up to date.
type namespaceCluster struct {
	Cluster
	mu      sync.Mutex
	ensured []string
}

func (c *namespaceCluster) EnsureNamespace(_ context.Context, env store.Environment, _, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensured = append(c.ensured, env.Namespace+"="+env.PodSecurity)
	return nil
}

func withRootCluster(t *testing.T) (*harness, *namespaceCluster, *storedDatabases) {
	t.Helper()
	h := newHarness(t)
	cluster := &namespaceCluster{}
	h.withCluster(cluster)
	databases := &storedDatabases{fakeDatabases: fakeDatabases{log: &recorder{}, db: h.db}}
	h.api.databases = databases
	return h, cluster, databases
}

func TestATemplateThatNeedsRootIsRefusedBeforeAnythingIsMade(t *testing.T) {
	if tpl, _ := templates.Lookup("wordpress"); !tpl.Services[0].NeedsRoot {
		t.Fatal("the WordPress template no longer says its image starts as root; pick another for this test")
	}
	h, cluster, databases := withRootCluster(t)
	acme := h.newTenant("acme")
	member := h.newMember(acme, "mia", store.RoleMember)
	before := h.rowCounts(t)

	status, body := h.do(member, http.MethodPost, "/api/templates/wordpress/install",
		map[string]any{"environment_id": acme.env.ID})
	if status != http.StatusConflict || problemCode(body) != "template.needs_root_images" ||
		!strings.Contains(body, "wordpress") || !strings.Contains(body, "production") {
		t.Fatalf("a member installing WordPress at the strictest level answered %d %s", status, body)
	}
	// Asking for the level to be lowered is an admin's to ask, and refused
	// before anything is read or written.
	status, body = h.do(member, http.MethodPost, "/api/templates/wordpress/install",
		map[string]any{"environment_id": acme.env.ID, "allow_root": true})
	if status != http.StatusForbidden {
		t.Fatalf("a member asking to accept root images answered %d %s", status, body)
	}
	if after := h.rowCounts(t); !maps.Equal(before, after) {
		t.Errorf("a refused install changed %v into %v", before, after)
	}
	if events := databases.log.all(); len(events) != 0 {
		t.Errorf("a refused install asked for a database: %v", events)
	}
	env, _ := h.db.GetEnvironment(t.Context(), acme.env.ID)
	if env.PodSecurity == "baseline" || len(cluster.ensured) != 0 {
		t.Errorf("a refused install changed the level to %q, applying %v", env.PodSecurity, cluster.ensured)
	}
}

func TestAnAdminCanAcceptRootImagesAsPartOfTheInstall(t *testing.T) {
	h, cluster, _ := withRootCluster(t)
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/templates/wordpress/install",
		map[string]any{"environment_id": acme.env.ID, "allow_root": true})
	if status != http.StatusCreated || !strings.Contains(body, `"accepts_root":true`) {
		t.Fatalf("an owner accepting root images answered %d %s", status, body)
	}
	env, _ := h.db.GetEnvironment(t.Context(), acme.env.ID)
	if env.PodSecurity != "baseline" {
		t.Errorf("the environment is at %q", env.PodSecurity)
	}
	if !slices.Equal(cluster.ensured, []string{"acme-production=baseline"}) {
		t.Errorf("the namespace was brought up to date as %v", cluster.ensured)
	}
	entries, err := h.db.ListAudit(t.Context(), acme.team.ID, "environment.pod_security_changed", acme.env.ID, 10)
	if err != nil || len(entries) != 1 {
		t.Errorf("the change of level is recorded as %+v (%v)", entries, err)
	}
	if apps, _ := h.db.ListApps(t.Context(), acme.env.ID); len(apps) != 1 {
		t.Errorf("the install made %+v", apps)
	}

	// Into an environment that accepts root images already, nothing has to
	// be asked, and the level is not touched again.
	if status, body := h.do(acme, http.MethodPost, "/api/templates/wordpress/install",
		map[string]any{"environment_id": acme.env.ID, "name": "shop"}); status != http.StatusCreated ||
		strings.Contains(body, "accepts_root") {
		t.Fatalf("a second WordPress into an environment that accepts root answered %d %s", status, body)
	}
	if len(cluster.ensured) != 1 {
		t.Errorf("the level was applied again: %v", cluster.ensured)
	}
}

// An install that fails after the level was lowered — or that would have —
// leaves the environment as it was, because the level changes last of what
// can fail.
func TestAnInstallThatFailsLeavesTheLevelAsItWas(t *testing.T) {
	h, cluster, databases := withRootCluster(t)
	databases.failFor = "mariadb"
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/templates/wordpress/install",
		map[string]any{"environment_id": acme.env.ID, "allow_root": true})
	if problemCode(body) != "template.install_rolled_back" {
		t.Fatalf("a refused database answered %d %s", status, body)
	}
	env, _ := h.db.GetEnvironment(t.Context(), acme.env.ID)
	if env.PodSecurity == "baseline" || len(cluster.ensured) != 0 {
		t.Errorf("an install that was undone left the level at %q, applying %v", env.PodSecurity, cluster.ensured)
	}
}
