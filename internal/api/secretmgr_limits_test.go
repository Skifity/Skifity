package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"skifity/internal/cli"
	"skifity/internal/store"
)

// What a secret manager connection may be used for, through the API: the
// limits are set and changed by an administrator only, refuse a reference
// outside them before anything is read, and a narrowing that would cut
// working variables off is refused until it is forced.

func listedManagers(t *testing.T, h *harness, as tenant) map[string]secretManagerView {
	t.Helper()
	status, body := h.do(as, http.MethodGet, "/api/teams/"+as.team.ID+"/secret-managers", nil)
	if status != http.StatusOK {
		t.Fatalf("listing the secret managers: %d %s", status, body)
	}
	var listed struct {
		Items []secretManagerView `json:"items"`
	}
	decodeInto(t, body, &listed)
	out := map[string]secretManagerView{}
	for _, item := range listed.Items {
		out[item.Name] = item
	}
	return out
}

func TestASecretManagerIsLimitedToSomePathsAndProjects(t *testing.T) {
	h := newHarness(t)
	vault := h.withVault()
	acme := h.newTenant("acme")
	blog := h.otherProject(acme, "blog")
	member := h.newMember(acme, "bob", store.RoleMember)
	viewer := h.newMember(acme, "val", store.RoleViewer)
	contractor := h.limitedMember(acme, "carl", store.RoleMember, blog.project.ID)
	other := h.newTenant("other")
	h.api.deployer = &fakeDeployer{log: &recorder{}}
	base := "/api/teams/" + acme.team.ID + "/secret-managers"

	// Refused before anything is stored: a star that would be a string
	// prefix, a list of blanks, another team's project.
	for _, bad := range []map[string]any{
		{"allowed_paths": []string{"shop*"}},
		{"allowed_paths": []string{" "}},
		{"allowed_paths": []string{"secret/data"}},
		{"allowed_project_ids": []string{other.project.ID}},
		{"allowed_project_ids": []string{""}},
	} {
		body := vaultConnection("company-vault", vault.URL, apiVaultToken)
		for k, v := range bad {
			body[k] = v
		}
		if status, answer := h.do(acme, http.MethodPost, base, body); status != http.StatusBadRequest {
			t.Errorf("%v was answered %d: %s", bad, status, answer)
		}
	}

	// Written the way a Vault policy writes it, and kept the way a reference
	// is.
	body := vaultConnection("company-vault", vault.URL, apiVaultToken)
	body["allowed_paths"] = []string{"secret/data/shop/*"}
	body["allowed_project_ids"] = []string{acme.project.ID}
	status, answer := h.do(acme, http.MethodPost, base, body)
	if status != http.StatusCreated {
		t.Fatalf("connecting a limited Vault: %d %s", status, answer)
	}
	var created secretManagerView
	decodeInto(t, answer, &created)
	if !slices.Equal(created.AllowedPaths, []string{"shop"}) || !slices.Equal(created.AllowedProjectIDs, []string{acme.project.ID}) {
		t.Fatalf("the limits came back as %v and %v", created.AllowedPaths, created.AllowedProjectIDs)
	}
	// One nobody limited answers empty lists, never null.
	if status, answer := h.do(acme, http.MethodPost, base, vaultConnection("shared-vault", vault.URL, apiVaultToken)); status != http.StatusCreated ||
		!strings.Contains(answer, `"allowed_paths":[]`) || !strings.Contains(answer, `"allowed_project_ids":[]`) {
		t.Fatalf("an unlimited connection: %d %s", status, answer)
	}

	web := h.app(acme, "web")
	blogApp := h.app(blog, "blog-web")
	from := func(path string) map[string]string {
		return map[string]string{"connection": "company-vault", "path": path, "key": "stripe_key"}
	}
	refused := func(status int, answer, limit string) {
		t.Helper()
		if status != http.StatusForbidden || !strings.Contains(answer, "secrets.reference_not_allowed") ||
			!strings.Contains(answer, `"limit":"`+limit+`"`) || !strings.Contains(answer, "company-vault") {
			t.Errorf("want a refusal by the %s limit, got %d %s", limit, status, answer)
		}
		if strings.Contains(answer, referencedValue) {
			t.Errorf("a refusal carries the value: %s", answer)
		}
	}

	// Set-time: a path outside it, at a string prefix of an allowed one.
	status, answer = h.do(member, http.MethodPut, "/api/apps/"+web.ID+"/variables",
		map[string]any{"key": "ADMIN_KEY", "from": from("shop-admin")})
	refused(status, answer, "path")
	// An app in a project it is not for, and a project's shared variable, and
	// a batch — including for a member limited to that project.
	status, answer = h.do(member, http.MethodPut, "/api/apps/"+blogApp.ID+"/variables",
		map[string]any{"key": "STRIPE_KEY", "from": from("shop")})
	refused(status, answer, "project")
	status, answer = h.do(contractor, http.MethodPut, "/api/apps/"+blogApp.ID+"/variables",
		map[string]any{"key": "STRIPE_KEY", "from": from("shop")})
	refused(status, answer, "project")
	if strings.Contains(answer, acme.project.Name+",") || strings.Contains(answer, "for the project "+acme.project.Name) {
		t.Errorf("the refusal names a project the contractor cannot see: %s", answer)
	}
	status, answer = h.do(member, http.MethodPut, "/api/projects/"+blog.project.ID+"/variables",
		map[string]any{"key": "SHARED_KEY", "from": from("shop")})
	refused(status, answer, "project")
	status, answer = h.do(member, http.MethodPost, "/api/projects/"+blog.project.ID+"/variables/batch",
		map[string]any{"set": []map[string]any{{"key": "SHARED_KEY", "from": from("shop")}}})
	refused(status, answer, "project")
	status, answer = h.do(member, http.MethodPost, "/api/apps/"+blogApp.ID+"/variables/batch",
		map[string]any{"set": []map[string]any{{"key": "STRIPE_KEY", "from": from("shop")}}})
	refused(status, answer, "project")
	if stored := variableKeys(t, h, blogApp.ID); len(stored) != 0 {
		t.Errorf("refused references were stored: %v", stored)
	}

	// Inside both: set, and shared.
	if status, answer := h.do(member, http.MethodPut, "/api/apps/"+web.ID+"/variables",
		map[string]any{"key": "STRIPE_KEY", "from": from("shop")}); status != http.StatusOK {
		t.Fatalf("a reference inside the limits: %d %s", status, answer)
	}
	if status, answer := h.do(member, http.MethodPut, "/api/projects/"+acme.project.ID+"/variables",
		map[string]any{"key": "SHARED_KEY", "from": from("shop")}); status != http.StatusOK {
		t.Fatalf("a shared reference inside the limits: %d %s", status, answer)
	}

	// Who sees what. A viewer sees the limits; a member limited to blog is
	// not shown a connection blog may not use, and of one it may, only blog.
	if seen := listedManagers(t, h, viewer)["company-vault"]; !slices.Equal(seen.AllowedProjectIDs, []string{acme.project.ID}) {
		t.Errorf("a viewer sees %v", seen.AllowedProjectIDs)
	}
	seen := listedManagers(t, h, contractor)
	if _, ok := seen["company-vault"]; ok {
		t.Error("a member limited to blog is shown a connection blog may not use")
	}
	if _, ok := seen["shared-vault"]; !ok {
		t.Error("a member limited to blog is not shown a connection every project may use")
	}
	if status, answer := h.do(acme, http.MethodPatch, base+"/shared-vault",
		map[string]any{"allowed_project_ids": []string{acme.project.ID, blog.project.ID}}); status != http.StatusOK {
		t.Fatalf("limiting shared-vault: %d %s", status, answer)
	}
	if got := listedManagers(t, h, contractor)["shared-vault"].AllowedProjectIDs; !slices.Equal(got, []string{blog.project.ID}) {
		t.Errorf("a member limited to blog is shown the projects %v", got)
	}

	// Only an administrator changes them, as before.
	for _, who := range []tenant{member, viewer, contractor} {
		if status, _ := h.do(who, http.MethodPatch, base+"/company-vault",
			map[string]any{"allowed_paths": []string{}}); status != http.StatusForbidden {
			t.Errorf("%s changed the limits: %d", who.user.Name, status)
		}
	}
	if status, _ := h.do(other, http.MethodPatch, "/api/teams/"+other.team.ID+"/secret-managers/"+created.ID,
		map[string]any{"allowed_paths": []string{}}); status != http.StatusNotFound {
		t.Errorf("another team changed acme's limits: %d", status)
	}

	// Narrowing it away from what works is refused, naming what, and changes
	// nothing.
	status, answer = h.do(acme, http.MethodPatch, base+"/company-vault", map[string]any{"allowed_paths": []string{"billing"}})
	if status != http.StatusConflict || !strings.Contains(answer, "secrets.limits_break_references") ||
		!strings.Contains(answer, "web/STRIPE_KEY") || !strings.Contains(answer, "acme/SHARED_KEY") {
		t.Fatalf("a narrowing that cuts variables off: %d %s", status, answer)
	}
	if row, _ := h.db.GetSecretConnection(t.Context(), created.ID); !slices.Equal(row.AllowedPaths, []string{"shop"}) {
		t.Fatalf("a refused narrowing was saved: %v", row.AllowedPaths)
	}
	// A narrowing that cuts nothing off needs nobody to insist.
	if status, answer := h.do(acme, http.MethodPatch, base+"/company-vault",
		map[string]any{"allowed_paths": []string{"shop", "billing"}}); status != http.StatusOK {
		t.Errorf("adding a path: %d %s", status, answer)
	}
	// Forced, it is saved, and the answer says what stopped.
	status, answer = h.do(acme, http.MethodPatch, base+"/company-vault",
		map[string]any{"allowed_project_ids": []string{blog.project.ID}, "force": true})
	if status != http.StatusOK {
		t.Fatalf("a forced narrowing: %d %s", status, answer)
	}
	var forced secretManagerView
	decodeInto(t, answer, &forced)
	if !slices.Equal(forced.StoppedResolving, []string{"web/STRIPE_KEY", "acme/SHARED_KEY"}) {
		t.Errorf("the forced narrowing says %v stopped", forced.StoppedResolving)
	}
	// Lifting it again needs no force either.
	status, answer = h.do(acme, http.MethodPatch, base+"/company-vault",
		map[string]any{"allowed_paths": []string{}, "allowed_project_ids": []string{}})
	if status != http.StatusOK || !strings.Contains(answer, `"allowed_paths":[]`) || strings.Contains(answer, "stopped_resolving") {
		t.Errorf("lifting the limits: %d %s", status, answer)
	}

	// Audited: who changed the limits, to what, and that it was forced.
	status, answer = h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID+"/audit", nil)
	if status != http.StatusOK || !strings.Contains(answer, "secret_manager.limited") ||
		!strings.Contains(answer, "company-vault: paths shop, billing; projects blog; forced, 2 variables no longer resolve") ||
		!strings.Contains(answer, "company-vault (Vault): paths shop; projects acme") {
		t.Errorf("the audit log: %d %s", status, answer)
	}
}

// The CLI sets the limits when it connects a manager, changes them with
// `secrets connections limit`, and answers each as JSON.
func TestTheCLILimitsASecretManager(t *testing.T) {
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
		return out.String(), errs.String(), code
	}
	mustJSON := func(args ...string) secretManagerView {
		t.Helper()
		out, errs, code := run(append(args, "--json")...)
		if code != 0 {
			t.Fatalf("%s exited %d:\n%s\n%s", strings.Join(args, " "), code, out, errs)
		}
		var view secretManagerView
		if err := json.Unmarshal([]byte(out), &view); err != nil {
			t.Fatalf("%s did not print JSON: %v\n%s", strings.Join(args, " "), err, out)
		}
		return view
	}

	created := mustJSON("secrets", "connections", "add", "company-vault", "--kind", "vault", "--address", vault.URL,
		"--credentials-file", credentials, "--allow-path", "shop", "--allow-path", "secret/data/billing/*", "--allow-project", "acme")
	if !slices.Equal(created.AllowedPaths, []string{"shop", "billing"}) || !slices.Equal(created.AllowedProjectIDs, []string{acme.project.ID}) {
		t.Fatalf("add answered %v and %v", created.AllowedPaths, created.AllowedProjectIDs)
	}
	if shown := mustJSON("secrets", "connections", "limit", "company-vault"); !slices.Equal(shown.AllowedPaths, created.AllowedPaths) {
		t.Errorf("limit alone showed %v", shown.AllowedPaths)
	}
	if out, _, code := run("secrets", "connections", "limit", "company-vault"); code != 0 || !strings.Contains(out, "paths shop, billing; projects acme") {
		t.Errorf("limit alone printed (exit %d): %s", code, out)
	}

	if _, errs, code := run("env", "set", "ADMIN_KEY", "--from", "company-vault:shop-admin#stripe_key", "--app", app.ID); code == 0 ||
		!strings.Contains(errs, "reads only what is at or under shop, billing") {
		t.Errorf("a reference outside the limits exited %d:\n%s", code, errs)
	}
	if _, errs, code := run("env", "set", "STRIPE_KEY", "--from", "company-vault:shop#stripe_key", "--app", app.ID); code != 0 {
		t.Fatalf("a reference inside the limits exited %d:\n%s", code, errs)
	}

	// Narrowing it away from STRIPE_KEY is refused, then forced.
	if _, errs, code := run("secrets", "connections", "limit", "company-vault", "--allow-path", "billing", "--json"); code == 0 ||
		!strings.Contains(errs, "web/STRIPE_KEY") {
		t.Errorf("a narrowing that cuts a variable off exited %d:\n%s", code, errs)
	}
	forced := mustJSON("secrets", "connections", "limit", "company-vault", "--allow-path", "billing", "--force")
	if !slices.Equal(forced.AllowedPaths, []string{"billing"}) || !slices.Equal(forced.StoppedResolving, []string{"web/STRIPE_KEY"}) {
		t.Errorf("the forced narrowing answered %v, stopped %v", forced.AllowedPaths, forced.StoppedResolving)
	}
	lifted := mustJSON("secrets", "connections", "limit", "company-vault", "--any-path", "--any-project")
	if len(lifted.AllowedPaths) != 0 || len(lifted.AllowedProjectIDs) != 0 {
		t.Errorf("lifting the limits answered %v and %v", lifted.AllowedPaths, lifted.AllowedProjectIDs)
	}

	for _, args := range [][]string{
		{"secrets", "connections", "limit", "company-vault", "--allow-project", "nosuch"},
		{"secrets", "connections", "limit", "company-vault", "--allow-path", "shop", "--any-path"},
	} {
		if _, _, code := run(append(args, "--json")...); code == 0 {
			t.Errorf("%s was accepted", strings.Join(args, " "))
		}
	}
}
