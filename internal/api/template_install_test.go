package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"skifity/internal/dbsvc/engine"
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

// piecesDatabases is fakeDatabases with a connection to hand out in pieces.
type piecesDatabases struct{ fakeDatabases }

func (piecesDatabases) Credentials(context.Context, string) (DatabaseCredentials, error) {
	return DatabaseCredentials{Engine: "mysql", Host: "assets-db-rw.acme.svc", Port: 3306,
		Database: "assets", Username: "assets", Password: "not-a-real-password", URL: "mysql://x"}, nil
}

// A template can say how its image starts, give it the files it is
// configured by, and hand it its database as a host, a port, a user and a
// password — the three things software written for Compose takes for
// granted, and the reason dozens of apps could not be offered at all.
func TestATemplateCanStartItsImageItsWayWithItsFilesAndDatabasePieces(t *testing.T) {
	h := newHarness(t)
	log := &recorder{}
	h.api.databases = &piecesDatabases{fakeDatabases{log: log, db: h.db}}
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
	request := httptest.NewRequest(http.MethodPost, "/api/templates/assets/install", nil).WithContext(t.Context())
	result, err := h.api.installTemplate(request, tpl, acme.env, acme.user, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	apps := map[string]store.App{}
	for _, app := range result.Apps {
		apps[app.Name] = app
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

	for _, name := range []string{"web", "worker"} {
		rows, err := h.db.ListVariables(t.Context(), apps[name].ID)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		secret := map[string]bool{}
		for _, row := range rows {
			if value, err := h.keyring.Open(row.Sealed, variableContext(apps[name].ID, row.Key)); err == nil {
				got[row.Key] = string(value)
			}
			secret[row.Key] = row.IsSecret
		}
		want := map[string]string{"DB_HOST": "assets-db-rw.acme.svc", "DB_PORT": "3306", "DB_DATABASE": "assets",
			"DB_USERNAME": "assets", "DB_PASSWORD": "not-a-real-password"}
		for key, value := range want {
			if got[key] != value {
				t.Errorf("%s: %s is %q, want %q", name, key, got[key], value)
			}
		}
		if !secret["DB_PASSWORD"] || secret["DB_HOST"] {
			t.Errorf("%s: the password is secret %v, the host %v", name, secret["DB_PASSWORD"], secret["DB_HOST"])
		}
	}
}
