package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"skifity/internal/dbsvc/engine"
	"skifity/internal/store"
)

// Every engine the catalogue lists is one a database can be asked for, and
// is handed to the manager as asked.
func TestEveryEngineCanBeAskedFor(t *testing.T) {
	h, log := withDatabases(t, "")
	acme := h.newTenant("acme")
	for _, name := range engine.Names() {
		status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/databases",
			map[string]any{"name": name + " db", "engine": name})
		if status != http.StatusAccepted {
			t.Errorf("%s: got %d, want 202: %s", name, status, body)
		}
	}
	var created []string
	for _, event := range log.all() {
		if name, ok := strings.CutPrefix(event, "create "); ok {
			created = append(created, name)
		}
	}
	if !slices.Equal(created, engine.Names()) {
		t.Errorf("the manager was asked for %v, want %v", created, engine.Names())
	}
}

// An engine nobody runs, and a version that is not offered, are refused
// before anything is made: the version is part of an image's name.
func TestAnEngineOrVersionThatIsNotOfferedIsRefused(t *testing.T) {
	h, log := withDatabases(t, "")
	acme := h.newTenant("acme")
	for _, body := range []map[string]any{
		{"name": "db", "engine": "cassandra"},
		{"name": "db", "engine": "mysql", "version": "11.4"},
		{"name": "db", "engine": "mongodb", "version": "latest"},
		{"name": "db", "engine": "mariadb", "version": "11.4 --privileged"},
	} {
		status, answer := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/databases", body)
		if status != http.StatusBadRequest {
			t.Errorf("%v: got %d, want 400: %s", body, status, answer)
		}
	}
	if events := log.all(); len(events) != 0 {
		t.Errorf("a refused request still reached the manager: %v", events)
	}
	// A version that is offered goes through.
	status, answer := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/databases",
		map[string]any{"name": "db", "engine": "mysql", "version": "9.7"})
	if status != http.StatusAccepted {
		t.Errorf("MySQL 9.7: got %d: %s", status, answer)
	}
}

// The form to create a database is built from this list, and the database's
// page reads from it whether backups are offered.
func TestTheEnginesAreListed(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	status, body := h.do(acme, http.MethodGet, "/api/database-engines", nil)
	if status != http.StatusOK {
		t.Fatalf("got %d: %s", status, body)
	}
	var list struct {
		Items []engine.Engine `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	backups := map[string]bool{}
	for _, e := range list.Items {
		backups[e.Name] = e.Backups
		if len(e.Versions) == 0 || e.DefaultVersion == "" || e.Port == 0 {
			t.Errorf("%s is listed without its versions or port: %+v", e.Name, e)
		}
	}
	if len(list.Items) != len(engine.Names()) {
		t.Errorf("%d engines are listed, and the panel runs %d", len(list.Items), len(engine.Names()))
	}
	for name, want := range map[string]bool{"postgres": true, "mongodb": true, "valkey": true,
		"dragonfly": false, "clickhouse": false, "memcached": false} {
		if backups[name] != want {
			t.Errorf("%s is listed as backed up %v, want %v", name, backups[name], want)
		}
	}
	// Nothing about the list is a secret, and nothing in it is anybody's.
	if strings.Contains(body, "password\":\"") {
		t.Errorf("the list carries a password: %s", body)
	}
}

// The API description names the engines in several places, by hand. An
// engine added to the catalogue and not to the description is a client
// generated from it refusing a database the panel makes.
func TestTheAPIDescriptionNamesEveryEngine(t *testing.T) {
	document := readOpenAPI(t)
	components, _ := document["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	enumOf := func(schema, property string) []string {
		object, _ := schemas[schema].(map[string]any)
		properties, _ := object["properties"].(map[string]any)
		field, _ := properties[property].(map[string]any)
		values, _ := field["enum"].([]any)
		var out []string
		for _, value := range values {
			if name, ok := value.(string); ok {
				out = append(out, name)
			}
		}
		return out
	}
	for _, place := range []struct{ schema, property string }{
		{"Database", "engine"}, {"CreateDatabaseRequest", "engine"}, {"DatabaseEngine", "name"},
	} {
		if got := enumOf(place.schema, place.property); !slices.Equal(got, engine.Names()) {
			t.Errorf("%s.%s is one of %v, and the panel runs %v", place.schema, place.property, got, engine.Names())
		}
	}
}

// A backup, a schedule or a restore of an engine the panel does not back up
// is refused, and said to be — on a panel with no backup storage too, since
// whether a cache can be backed up does not depend on a bucket.
func TestABackupIsRefusedWhereNoneIsOffered(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	ctx := t.Context()
	for _, name := range []string{"dragonfly", "clickhouse", "memcached"} {
		record := store.Database{EnvironmentID: acme.env.ID, Name: name, Slug: name, Engine: name, Status: "running"}
		if err := h.db.CreateDatabase(ctx, &record); err != nil {
			t.Fatal(err)
		}
		base := "/api/databases/" + record.ID
		for _, call := range []struct {
			method, path string
			body         any
		}{
			{http.MethodPost, base + "/backups", nil},
			{http.MethodPut, base + "/backup-policy", map[string]any{"enabled": true, "schedule": "0 3 * * *", "retention": 7}},
			{http.MethodPost, base + "/restore/bak_whatever", nil},
		} {
			status, body := h.do(acme, call.method, call.path, call.body)
			if status != http.StatusConflict || !strings.Contains(body, "backup.not_offered") {
				t.Errorf("%s: %s %s got %d: %s", name, call.method, call.path, status, body)
			}
		}
		// A schedule that is off asks nothing of the engine.
		status, body := h.do(acme, http.MethodPut, base+"/backup-policy",
			map[string]any{"enabled": false, "schedule": "0 3 * * *", "retention": 7})
		if status != http.StatusOK {
			t.Errorf("%s: turning a schedule off got %d: %s", name, status, body)
		}
	}

	// An engine that is backed up gets as far as the storage it needs.
	record := store.Database{EnvironmentID: acme.env.ID, Name: "docs", Slug: "docs", Engine: "mongodb", Status: "running"}
	if err := h.db.CreateDatabase(ctx, &record); err != nil {
		t.Fatal(err)
	}
	status, body := h.do(acme, http.MethodPost, "/api/databases/"+record.ID+"/backups", nil)
	if status == http.StatusConflict || strings.Contains(body, "backup.not_offered") {
		t.Errorf("a MongoDB backup was refused as not offered: %d %s", status, body)
	}
}
