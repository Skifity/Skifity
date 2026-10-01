package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// A database's life, at the API: who may ask, and what is refused before the
// database manager or the backup manager is asked anything.

func lifeHarness(t *testing.T) (*harness, *recorder, *recordingBackups) {
	t.Helper()
	h, log := withDatabases(t, "")
	backups := &recordingBackups{}
	h.api.backups = backups
	return h, log, backups
}

// raw sends a body that is not JSON, as an import's is.
func (h *harness) raw(as tenant, method, path, body string) (int, string) {
	h.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+as.token)
	resp, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(answer)
}

func TestADatabaseOfAnotherTeamCannotBeStoppedResizedOrImportedInto(t *testing.T) {
	h, log, backups := lifeHarness(t)
	acme, globex := h.newTenant("acme"), h.newTenant("globex")
	theirs := h.database(globex, "orders")
	base := "/api/databases/" + theirs.ID
	for _, request := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, base + "/stop?force=true", map[string]any{}},
		{http.MethodPost, base + "/start", map[string]any{}},
		{http.MethodPatch, base, map[string]any{"mem_limit_mb": 2048}},
		{http.MethodPost, base + "/password", map[string]any{}},
		{http.MethodGet, base + "/operations", nil},
	} {
		if status, body := h.do(acme, request.method, request.path, request.body); status != http.StatusNotFound {
			t.Errorf("%s %s answered another team %d: %s", request.method, request.path, status, body)
		}
	}
	if status, _ := h.raw(acme, http.MethodPost, base+"/import", "CREATE TABLE t (id int);"); status != http.StatusNotFound {
		t.Errorf("an import into another team's database answered %d", status)
	}
	if events := log.all(); len(events) != 0 || backups.imported != "" {
		t.Fatalf("another team's database was changed: %v, imported %q", events, backups.imported)
	}
}

func TestAViewerCannotChangeADatabasesLife(t *testing.T) {
	h, log, _ := lifeHarness(t)
	acme := h.newTenant("acme")
	viewer := h.newMember(acme, "viewer", store.RoleViewer)
	record := h.database(acme, "orders")
	base := "/api/databases/" + record.ID
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, base + "/stop?force=true"},
		{http.MethodPost, base + "/start"},
		{http.MethodPatch, base},
		{http.MethodPost, base + "/password"},
	} {
		if status, _ := h.do(viewer, request.method, request.path, map[string]any{"mem_limit_mb": 2048}); status != http.StatusForbidden {
			t.Errorf("%s %s answered a viewer %d", request.method, request.path, status)
		}
	}
	if status, _ := h.raw(viewer, http.MethodPost, base+"/import", "CREATE TABLE t (id int);"); status != http.StatusForbidden {
		t.Errorf("an import answered a viewer %d", status)
	}
	// The history is theirs to read.
	if status, body := h.do(viewer, http.MethodGet, base+"/operations", nil); status != http.StatusOK {
		t.Errorf("the history answered a viewer %d: %s", status, body)
	}
	if events := log.all(); len(events) != 0 {
		t.Fatalf("a viewer changed something: %v", events)
	}
	// A member may stop, start and resize, as they may scale an app, and
	// may not change the password or import, as they may not restore.
	member := h.newMember(acme, "member", store.RoleMember)
	if status, _ := h.do(member, http.MethodPost, base+"/password", map[string]any{}); status != http.StatusForbidden {
		t.Errorf("a member changed the password: %d", status)
	}
	if status, body := h.do(member, http.MethodPost, base+"/start", map[string]any{}); status != http.StatusAccepted {
		t.Errorf("a member could not start it: %d %s", status, body)
	}
}

// Stopping a database apps use names them and needs saying so.
func TestStoppingALinkedDatabaseNamesTheApps(t *testing.T) {
	h, log, _ := lifeHarness(t)
	acme := h.newTenant("acme")
	record := h.database(acme, "orders")
	app := h.app(acme, "storefront")
	if err := h.db.LinkDatabase(t.Context(), record.ID, app.ID, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	status, body := h.do(acme, http.MethodPost, "/api/databases/"+record.ID+"/stop", map[string]any{})
	if status != http.StatusConflict || !strings.Contains(body, "database.stop_linked") || !strings.Contains(body, "storefront") {
		t.Fatalf("stopping a linked database: %d %s", status, body)
	}
	if events := log.all(); len(events) != 0 {
		t.Fatalf("the manager was asked anyway: %v", events)
	}
	if status, body := h.do(acme, http.MethodPost, "/api/databases/"+record.ID+"/stop?force=true", map[string]any{}); status != http.StatusOK {
		t.Fatalf("stopping it, meaning it: %d %s", status, body)
	}
	if events := log.all(); len(events) != 1 || events[0] != "stop "+record.ID {
		t.Fatalf("the manager was asked %v", events)
	}
}

// A disk asked to shrink, an empty resize, and a change while something else
// is being done are all refused before the manager is asked.
func TestAResizeIsRefusedBeforeTheManagerIsAsked(t *testing.T) {
	h, log, _ := lifeHarness(t)
	acme := h.newTenant("acme")
	record := h.database(acme, "orders")
	record.StorageGB = 20
	if err := h.db.UpdateDatabase(t.Context(), &record); err != nil {
		t.Fatal(err)
	}
	path := "/api/databases/" + record.ID
	if status, body := h.do(acme, http.MethodPatch, path, map[string]any{"storage_gb": 10}); status != http.StatusBadRequest ||
		!strings.Contains(body, "database.storage_shrink") {
		t.Errorf("a smaller disk: %d %s", status, body)
	}
	if status, _ := h.do(acme, http.MethodPatch, path, map[string]any{}); status != http.StatusBadRequest {
		t.Errorf("a resize of nothing: %d", status)
	}
	if status, _ := h.do(acme, http.MethodPatch, path, map[string]any{"memory": 2048}); status != http.StatusBadRequest {
		t.Errorf("a field nobody reads: %d", status)
	}
	op := store.Operation{TeamID: acme.team.ID, Kind: "database.import", TargetType: "database", TargetID: record.ID,
		Status: store.OpRunning}
	if err := h.db.CreateOperation(t.Context(), &op, []string{"import"}); err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(acme, http.MethodPatch, path, map[string]any{"mem_limit_mb": 2048}); status != http.StatusConflict ||
		!strings.Contains(body, "database.busy") {
		t.Errorf("a resize during an import: %d %s", status, body)
	}
	if events := log.all(); len(events) != 0 {
		t.Fatalf("the manager was asked: %v", events)
	}
	// And the import is the database's history.
	status, body := h.do(acme, http.MethodGet, path+"/operations", nil)
	if status != http.StatusOK || !strings.Contains(body, op.ID) {
		t.Errorf("the history: %d %s", status, body)
	}
}

// An import is refused before its body is read whenever it can be, and the
// body reaches the backup manager whole when it is not.
func TestAnImportIsRefusedBeforeItIsRead(t *testing.T) {
	h, _, backups := lifeHarness(t)
	acme := h.newTenant("acme")
	record := h.database(acme, "orders")
	cache := store.Database{EnvironmentID: acme.env.ID, Name: "cache", Slug: "cache", Engine: "memcached", Status: "running"}
	if err := h.db.CreateDatabase(t.Context(), &cache); err != nil {
		t.Fatal(err)
	}
	if status, body := h.raw(acme, http.MethodPost, "/api/databases/"+cache.ID+"/import", "x"); status != http.StatusConflict ||
		!strings.Contains(body, "import.not_offered") {
		t.Errorf("an import into a cache: %d %s", status, body)
	}
	if status, body := h.raw(acme, http.MethodPost, "/api/databases/"+record.ID+"/import?format=rdb", "x"); status != http.StatusBadRequest ||
		!strings.Contains(body, "import.format_invalid") {
		t.Errorf("a snapshot into PostgreSQL: %d %s", status, body)
	}
	h.api.importLimit = 8
	if status, body := h.raw(acme, http.MethodPost, "/api/databases/"+record.ID+"/import", "CREATE TABLE t (id int);"); status != http.StatusRequestEntityTooLarge ||
		!strings.Contains(body, "import.too_large") {
		t.Errorf("a dump over the limit: %d %s", status, body)
	}
	if backups.imported != "" {
		t.Fatalf("a refused import reached the backup manager: %q", backups.imported)
	}
	h.api.importLimit = 0
	dump := "CREATE TABLE t (id int);\n"
	status, body := h.raw(acme, http.MethodPost, "/api/databases/"+record.ID+"/import?format=custom", dump)
	if status != http.StatusAccepted || !strings.Contains(body, "op_import") {
		t.Fatalf("an import: %d %s", status, body)
	}
	if backups.imported != dump || backups.importFormat != "custom" {
		t.Errorf("the backup manager was handed %q as %q", backups.imported, backups.importFormat)
	}
}

// A password chosen is passed on, and never comes back.
func TestAPasswordChangeIsAnAdministratorsAndSaysNothingOfThePassword(t *testing.T) {
	h, log, _ := lifeHarness(t)
	acme := h.newTenant("acme")
	record := h.database(acme, "orders")
	status, body := h.do(acme, http.MethodPost, "/api/databases/"+record.ID+"/password",
		map[string]any{"password": "chosen-by-hand-password-1"})
	if status != http.StatusAccepted || strings.Contains(body, "chosen-by-hand-password-1") {
		t.Fatalf("a password change: %d %s", status, body)
	}
	if events := log.all(); len(events) != 1 || events[0] != "password "+record.ID+" chosen-by-hand-password-1" {
		t.Fatalf("the manager was asked %v", events)
	}
	audit, err := h.db.ListAudit(t.Context(), acme.team.ID, "database.password_changed", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 1 {
		t.Fatalf("the change is audited as %+v", audit)
	}
	for _, event := range audit {
		if strings.Contains(event.TargetLabel+event.Metadata, "chosen-by-hand") {
			t.Errorf("the audit log has the password in it: %+v", event)
		}
	}
	cache := store.Database{EnvironmentID: acme.env.ID, Name: "cache", Slug: "cache", Engine: "memcached", Status: "running"}
	if err := h.db.CreateDatabase(t.Context(), &cache); err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(acme, http.MethodPost, "/api/databases/"+cache.ID+"/password", map[string]any{}); status != http.StatusConflict ||
		!strings.Contains(body, "database.password_not_offered") {
		t.Errorf("a cache's password: %d %s", status, body)
	}
}
