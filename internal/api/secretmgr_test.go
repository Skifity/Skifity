package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skifity/internal/cli"
	"skifity/internal/store"
)

// Variables read from a secret manager, through the API: who may connect
// one, who may use one, and — the part that matters most — that the value
// read never comes back out of anything: not a response, not the audit log,
// not the panel's log, not the database, not the CLI, not an assistant.

// referencedValue is what the fake manager holds. It is searched for in
// everything afterwards.
const referencedValue = "sk_live_SENTINEL_never_shown_4f2a"

const apiVaultToken = "hvs.fake-vault-token-for-api-tests"

// withVault starts a fake Vault the harness's panel may reach.
func (h *harness) withVault() *httptest.Server {
	h.t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Vault-Token") != apiVaultToken {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		switch r.URL.Path {
		case "/v1/auth/token/lookup-self":
			_, _ = w.Write([]byte(`{"data":{"accessor":"fake","policies":["default"],"ttl":3600}}`))
		case "/v1/secret/data/shop":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"data":     map[string]any{"stripe_key": referencedValue, "public_key": referencedValue + "_public"},
				"metadata": map[string]any{"version": 1},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
		}
	}))
	h.t.Cleanup(server.Close)
	// The panel's own client will not dial loopback, which is where this
	// listens; that refusal is tested in internal/secretmgr.
	h.api.secrets.Client = &http.Client{Timeout: 5 * time.Second}
	return server
}

func vaultConnection(name, address, token string) map[string]any {
	return map[string]any{
		"name": name, "kind": "vault",
		"settings":    map[string]string{"address": address, "mount": "secret"},
		"credentials": map[string]string{"token": token},
	}
}

func decodeInto(t *testing.T, body string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

func TestSecretManagersAreAnAdministratorsToManage(t *testing.T) {
	h := newHarness(t)
	vault := h.withVault()
	acme := h.newTenant("acme")
	member := h.newMember(acme, "bob", store.RoleMember)
	other := h.newTenant("other")
	base := "/api/teams/" + acme.team.ID + "/secret-managers"

	// Wrong credentials are refused before anything is stored.
	status, body := h.do(acme, http.MethodPost, base, vaultConnection("company-vault", vault.URL, "hvs.wrong"))
	if status != http.StatusBadGateway || !strings.Contains(body, "secrets.denied") {
		t.Fatalf("a wrong token was answered %d: %s", status, body)
	}
	if rows, _ := h.db.ListSecretConnections(t.Context(), acme.team.ID); len(rows) != 0 {
		t.Fatal("a connection that could not sign in was stored")
	}

	status, body = h.do(acme, http.MethodPost, base, vaultConnection("company-vault", vault.URL, apiVaultToken))
	if status != http.StatusCreated {
		t.Fatalf("an owner could not connect Vault: %d %s", status, body)
	}
	if strings.Contains(body, apiVaultToken) || !strings.Contains(body, `"credentials":["token"]`) {
		t.Errorf("the answer carries the token, or does not say it has one: %s", body)
	}
	var created secretManagerView
	decodeInto(t, body, &created)
	row, err := h.db.GetSecretConnection(t.Context(), created.ID)
	if err != nil || strings.Contains(row.SealedCredentials, apiVaultToken) {
		t.Fatalf("the token is stored as it was sent: %v", err)
	}

	// A member sees the list, to pick one, and changes nothing.
	if status, body := h.do(member, http.MethodGet, base, nil); status != http.StatusOK || strings.Contains(body, apiVaultToken) {
		t.Errorf("a member listing: %d %s", status, body)
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, base},
		{http.MethodPatch, base + "/" + created.ID},
		{http.MethodDelete, base + "/" + created.ID},
		{http.MethodPost, base + "/" + created.ID + "/test"},
	} {
		var body any = map[string]any{"refresh_minutes": 15}
		if request.method == http.MethodPost && request.path == base {
			body = vaultConnection("mine", vault.URL, apiVaultToken)
		}
		if request.method == http.MethodDelete || strings.HasSuffix(request.path, "/test") {
			body = nil
		}
		if status, _ := h.do(member, request.method, request.path, body); status != http.StatusForbidden {
			t.Errorf("a member's %s %s was answered %d, want 403", request.method, request.path, status)
		}
	}

	// Another team sees nothing, by the team's routes or by its own.
	if status, _ := h.do(other, http.MethodGet, base, nil); status != http.StatusNotFound {
		t.Errorf("another team listed acme's secret managers: %d", status)
	}
	otherBase := "/api/teams/" + other.team.ID + "/secret-managers/" + created.ID
	for _, method := range []string{http.MethodDelete, http.MethodPatch} {
		var body any
		if method == http.MethodPatch {
			body = map[string]any{"refresh_minutes": 15}
		}
		if status, _ := h.do(other, method, otherBase, body); status != http.StatusNotFound {
			t.Errorf("another team's %s of acme's connection was answered %d, want 404", method, status)
		}
	}
	if status, _ := h.do(other, http.MethodPost, otherBase+"/test", nil); status != http.StatusNotFound {
		t.Errorf("another team tested acme's connection: %d", status)
	}

	if status, body := h.do(acme, http.MethodPost, base+"/company-vault/test", nil); status != http.StatusOK {
		t.Errorf("testing by name: %d %s", status, body)
	}
	if status, _ := h.do(acme, http.MethodPatch, base+"/"+created.ID, map[string]any{"refresh_minutes": 3}); status != http.StatusBadRequest {
		t.Errorf("a refresh every 3 minutes was accepted: %d", status)
	}
	status, body = h.do(acme, http.MethodPatch, base+"/"+created.ID, map[string]any{"refresh_minutes": 15})
	if status != http.StatusOK || !strings.Contains(body, `"refresh_minutes":15`) {
		t.Errorf("switching the refresh on: %d %s", status, body)
	}
	// New credentials that do not sign in do not replace ones that do.
	status, _ = h.do(acme, http.MethodPatch, base+"/"+created.ID, map[string]any{"credentials": map[string]string{"token": "hvs.wrong"}})
	if status == http.StatusOK {
		t.Error("credentials that do not sign in replaced ones that do")
	}
	if status, body := h.do(acme, http.MethodPost, base+"/"+created.ID+"/test", nil); status != http.StatusOK {
		t.Errorf("the connection stopped working after a refused change: %d %s", status, body)
	}
}

func TestAVariableFromASecretManagerNeverShowsItsValue(t *testing.T) {
	h := newHarness(t)
	var logs lockedBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h.api.log = logger
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	vault := h.withVault()
	acme := h.newTenant("acme")
	member := h.newMember(acme, "bob", store.RoleMember)
	other := h.newTenant("other")
	app := h.app(acme, "web")
	h.api.deployer = &fakeDeployer{log: &recorder{}}
	if status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/secret-managers",
		vaultConnection("company-vault", vault.URL, apiVaultToken)); status != http.StatusCreated {
		t.Fatalf("connect: %d %s", status, body)
	}
	if status, body := h.do(other, http.MethodPost, "/api/teams/"+other.team.ID+"/secret-managers",
		vaultConnection("their-vault", vault.URL, apiVaultToken)); status != http.StatusCreated {
		t.Fatalf("connect: %d %s", status, body)
	}

	var bodies []string
	keep := func(status int, body string) (int, string) { bodies = append(bodies, body); return status, body }
	appVars := "/api/apps/" + app.ID + "/variables"
	from := map[string]string{"connection": "company-vault", "path": "shop", "key": "stripe_key"}

	// A member sets one.
	status, body := keep(h.do(member, http.MethodPut, appVars, map[string]any{"key": "STRIPE_KEY", "from": from}))
	if status != http.StatusOK || !strings.Contains(body, `"connection":"company-vault"`) || !strings.Contains(body, `"is_secret":true`) {
		t.Fatalf("a member setting a reference: %d %s", status, body)
	}
	// Several at once, one of them read while building.
	status, body = keep(h.do(member, http.MethodPost, appVars+"/batch", map[string]any{"set": []map[string]any{
		{"key": "NEXT_PUBLIC_KEY", "build_time": true,
			"from": map[string]string{"connection": "company-vault", "path": "shop", "key": "public_key"}},
		{"key": "LOG_LEVEL", "value": "info"},
	}}))
	if status != http.StatusOK || !strings.Contains(body, `"requires_rebuild":true`) {
		t.Fatalf("a batch with a build-time reference: %d %s", status, body)
	}
	// And a shared one.
	status, body = keep(h.do(member, http.MethodPut, "/api/projects/"+acme.project.ID+"/variables",
		map[string]any{"key": "SHARED_KEY", "from": from}))
	if status != http.StatusOK {
		t.Fatalf("a shared reference: %d %s", status, body)
	}

	// Refused, and nothing stored: a key that is not there, another team's
	// connection, a value and a reference both.
	status, body = keep(h.do(member, http.MethodPut, appVars, map[string]any{"key": "MAIL_PASSWORD",
		"from": map[string]string{"connection": "company-vault", "path": "shop", "key": "mail"}}))
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "secrets.reference_unresolved") ||
		!strings.Contains(body, "MAIL_PASSWORD") {
		t.Errorf("a key that is not there: %d %s", status, body)
	}
	status, _ = keep(h.do(member, http.MethodPut, appVars, map[string]any{"key": "STOLEN",
		"from": map[string]string{"connection": "their-vault", "path": "shop", "key": "stripe_key"}}))
	if status != http.StatusNotFound {
		t.Errorf("another team's connection was usable: %d", status)
	}
	status, _ = keep(h.do(member, http.MethodPut, appVars, map[string]any{"key": "BOTH", "value": "x", "from": from}))
	if status != http.StatusBadRequest {
		t.Errorf("a value and a reference both: %d", status)
	}
	stored := variableKeys(t, h, app.ID)
	for _, key := range []string{"MAIL_PASSWORD", "STOLEN", "BOTH"} {
		if _, ok := stored[key]; ok {
			t.Errorf("%s was stored after being refused", key)
		}
	}
	if ref := stored["STRIPE_KEY"].Reference; ref == nil || ref.Path != "shop" || ref.Key != "stripe_key" {
		t.Errorf("the reference stored is %+v", ref)
	}

	// Everything that reads them back.
	keep(h.do(acme, http.MethodGet, appVars, nil))
	keep(h.do(acme, http.MethodGet, "/api/projects/"+acme.project.ID+"/variables", nil))
	keep(h.do(acme, http.MethodPost, appVars+"/refresh", nil))
	keep(h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID+"/export", nil))
	keep(h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID+"/audit", nil))
	keep(h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID+"/secret-managers", nil))

	var listed struct {
		Items []store.Variable `json:"items"`
	}
	decodeInto(t, bodies[len(bodies)-6], &listed)
	found := false
	for _, v := range listed.Items {
		if v.Key == "STRIPE_KEY" {
			found = v.Reference != nil && v.Reference.Connection == "company-vault" && v.Reference.Kind == "vault" && v.Value == ""
		}
	}
	if !found {
		t.Errorf("the list does not say where STRIPE_KEY is read from: %s", bodies[len(bodies)-6])
	}
	if audit := bodies[len(bodies)-2]; !strings.Contains(audit, "variable.reference_set") ||
		!strings.Contains(audit, "company-vault:shop#stripe_key") {
		t.Errorf("the audit log does not say where it is read from: %s", audit)
	}

	// Removing a connection something reads is refused, and says what does.
	status, body = h.do(acme, http.MethodDelete, "/api/teams/"+acme.team.ID+"/secret-managers/company-vault", nil)
	if status != http.StatusConflict || !strings.Contains(body, "web/STRIPE_KEY") || !strings.Contains(body, "secrets.connection_in_use") {
		t.Errorf("removing a connection in use: %d %s", status, body)
	}

	// An assistant, through the panel's own MCP endpoint.
	session := h.connectMCP(t, acme)
	text, failed := callText(t, session, "list_variables", map[string]any{"app_id": app.ID})
	if failed || !strings.Contains(text, "company-vault:shop#stripe_key") {
		t.Errorf("list_variables over MCP: %s", text)
	}
	bodies = append(bodies, text)

	for i, body := range bodies {
		if strings.Contains(body, referencedValue) {
			t.Errorf("answer %d carries the value read from the secret manager: %s", i, body)
		}
		if strings.Contains(body, apiVaultToken) {
			t.Errorf("answer %d carries the connection's token: %s", i, body)
		}
	}
	if strings.Contains(logs.String(), referencedValue) || strings.Contains(logs.String(), apiVaultToken) {
		t.Errorf("the panel's log carries a secret:\n%s", logs.String())
	}
	assertNotStored(t, h.db, referencedValue)
	assertNotStored(t, h.db, apiVaultToken)
}

// assertNotStored reads every text column of every table for a string.
func assertNotStored(t *testing.T, db *store.DB, needle string) {
	t.Helper()
	tables, err := db.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		_ = tables.Scan(&name)
		names = append(names, name)
	}
	tables.Close()
	checked := 0
	for _, table := range names {
		columns, err := db.QueryContext(t.Context(), `SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for columns.Next() {
			var col string
			_ = columns.Scan(&col)
			cols = append(cols, col)
		}
		columns.Close()
		for _, col := range cols {
			checked++
			var n int
			query := `SELECT COUNT(*) FROM "` + table + `" WHERE CAST("` + col + `" AS TEXT) LIKE ?`
			if err := db.QueryRowContext(t.Context(), query, "%"+needle+"%").Scan(&n); err != nil {
				t.Fatalf("%s.%s: %v", table, col, err)
			}
			if n > 0 {
				t.Errorf("%s.%s holds %q", table, col, needle)
			}
		}
	}
	if checked < 100 {
		t.Fatalf("only %d columns were searched", checked)
	}
}

// A preview of a pull request from a fork is given no reference, and one
// from the repository itself is given the same reference, never a value.
func TestAPreviewCopiesTheReferenceAndAForkGetsNone(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	connection := store.SecretConnection{ID: store.NewID("sm"), TeamID: acme.team.ID, Name: "company-vault", Kind: "vault",
		Settings: map[string]string{"address": "https://vault.example.test"}}
	if err := h.db.CreateSecretConnection(t.Context(), &connection, "sealed-in-test"); err != nil {
		t.Fatal(err)
	}
	ref := &store.SecretReference{ConnectionID: connection.ID, Path: "shop", Key: "stripe_key"}
	for _, v := range []store.Variable{
		{AppID: app.ID, Key: "STRIPE_KEY", IsSecret: true, Reference: ref},
		// Somebody marked it not secret: a reference is one anyway.
		{AppID: app.ID, Key: "UNMARKED", IsSecret: false, Reference: ref},
	} {
		if err := h.db.SetVariable(t.Context(), &v, ""); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)

	fork := h.app(acme, "fork-copy")
	if err := h.api.copyPreviewVariables(request, app.ID, fork.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	if copied := variableKeys(t, h, fork.ID); len(copied) != 0 {
		t.Errorf("a fork's preview was given %v", copied)
	}

	same := h.app(acme, "same-repo-copy")
	if err := h.api.copyPreviewVariables(request, app.ID, same.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	copied := variableKeys(t, h, same.ID)
	if got := copied["STRIPE_KEY"].Reference; got == nil || got.ConnectionID != connection.ID || got.Key != "stripe_key" ||
		!copied["STRIPE_KEY"].IsSecret {
		t.Errorf("a preview from the repository itself got %+v", copied["STRIPE_KEY"])
	}

	// A clone of the environment reads the same secret: there is no value
	// here to copy, and opening one that is not there used to fail the clone.
	clone := h.app(acme, "cloned")
	var notes []cloneNote
	if err := h.api.cloneApp(request, app, clone, &notes); err != nil {
		t.Fatalf("cloning an app with a reference: %v", err)
	}
	if got := variableKeys(t, h, clone.ID)["UNMARKED"]; got.Reference == nil || !got.IsSecret {
		t.Errorf("the clone got %+v", got)
	}
}

// The CLI, end to end against the panel: connecting a manager with the
// credentials in a file, setting a variable from it, listing and refreshing,
// all as JSON — and never printing the value or the token.
func TestTheCLIConnectsASecretManagerAndReadsVariablesFromIt(t *testing.T) {
	h := newHarness(t)
	vault := h.withVault()
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	h.api.deployer = &fakeDeployer{log: &recorder{}}
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("SKIFITY_URL", h.server.URL)
	t.Setenv("SKIFITY_TOKEN", acme.token)

	credentials := filepath.Join(t.TempDir(), "vault.json")
	if err := os.WriteFile(credentials, []byte(`{"token": "`+apiVaultToken+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, string, int) {
		t.Helper()
		var out, errs bytes.Buffer
		code := cli.Run(t.Context(), args, &out, &errs)
		for _, secret := range []string{referencedValue, apiVaultToken} {
			if strings.Contains(out.String()+errs.String(), secret) {
				t.Errorf("%s printed a secret:\n%s\n%s", strings.Join(args, " "), out.String(), errs.String())
			}
		}
		return out.String(), errs.String(), code
	}
	mustJSON := func(args ...string) map[string]any {
		t.Helper()
		out, errs, code := run(append(args, "--json")...)
		if code != 0 {
			t.Fatalf("%s exited %d:\n%s\n%s", strings.Join(args, " "), code, out, errs)
		}
		var decoded any
		if err := json.Unmarshal([]byte(out), &decoded); err != nil {
			t.Fatalf("%s did not print JSON: %v\n%s", strings.Join(args, " "), err, out)
		}
		if m, ok := decoded.(map[string]any); ok {
			return m
		}
		return map[string]any{"list": decoded}
	}

	created := mustJSON("secrets", "connections", "add", "company-vault", "--kind", "vault",
		"--address", vault.URL, "--credentials-file", credentials)
	if created["name"] != "company-vault" {
		t.Errorf("add answered %v", created)
	}
	listed := mustJSON("secrets", "connections", "list")
	if !strings.Contains(fmtJSON(listed), "company-vault") {
		t.Errorf("list answered %v", listed)
	}
	if tested := mustJSON("secrets", "connections", "test", "company-vault"); tested["ok"] != true {
		t.Errorf("test answered %v", tested)
	}

	set := mustJSON("env", "set", "STRIPE_KEY", "--from", "company-vault:shop#stripe_key", "--app", app.ID)
	if !strings.Contains(fmtJSON(set), "company-vault") {
		t.Errorf("env set --from answered %v", set)
	}
	out, _, code := run("env", "list", "--app", app.ID)
	if code != 0 || !strings.Contains(out, "(from company-vault:shop#stripe_key)") {
		t.Errorf("env list printed (exit %d):\n%s", code, out)
	}
	mustJSON("env", "list", "--app", app.ID)
	mustJSON("env", "refresh", "--app", app.ID)

	// A credential is never an argument: there is no flag for one.
	if _, _, code := run("secrets", "connections", "add", "x", "--kind", "doppler", "--token", "dp.st.x"); code == 0 {
		t.Error("a token was accepted as an argument")
	}
	// In use, so not removed, and the error says by what.
	_, errs, code := run("secrets", "connections", "remove", "company-vault")
	if code == 0 || !strings.Contains(errs, "STRIPE_KEY") {
		t.Errorf("removing a connection in use exited %d:\n%s", code, errs)
	}
}

func fmtJSON(v any) string {
	encoded, _ := json.Marshal(v)
	return string(encoded)
}
