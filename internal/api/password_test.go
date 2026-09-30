package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// A password in front of an app. What is stored is a hash Traefik can check
// and nothing else; only somebody who decides who may reach an app can set it;
// and a pull request's preview of a locked app is locked too.

// syncingDeployer counts the times an app was re-applied.
type syncingDeployer struct {
	fakeDeployer
	synced []string
}

func (f *syncingDeployer) Sync(_ context.Context, appID string) error {
	f.synced = append(f.synced, appID)
	return nil
}

func TestAPasswordIsStoredAsAHashTraefikCanCheck(t *testing.T) {
	h := newHarness(t)
	deployer := &syncingDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}
	h.api.deployer = deployer
	acme := h.newTenant("acme")
	app := h.app(acme, "staging")

	status, body := h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/password",
		map[string]any{"username": "client", "password": "correct horse battery"})
	if status != http.StatusOK {
		t.Fatalf("setting a password answered %d\n%s", status, body)
	}
	// Neither the password nor its hash is ever sent back.
	if strings.Contains(body, "correct horse battery") || strings.Contains(body, "$2") {
		t.Fatalf("the answer carries the password or its hash:\n%s", body)
	}
	var view passwordView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Enabled || view.Username != "client" {
		t.Errorf("the answer says %+v", view)
	}

	stored, ok, err := h.db.GetAppPassword(t.Context(), app.ID)
	if err != nil || !ok {
		t.Fatalf("no password was stored: %v", err)
	}
	// htpasswd's prefix, which every reader of that format expects.
	if !strings.HasPrefix(stored.Hash, "$2y$") {
		t.Errorf("the hash is %q, not in htpasswd's bcrypt form", stored.Hash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.Hash), []byte("correct horse battery")); err != nil {
		t.Errorf("the stored hash does not match the password: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.Hash), []byte("wrong")) == nil {
		t.Error("the stored hash matches a wrong password")
	}
	// Saved is not enforced: the Ingress has to gain the middleware.
	if len(deployer.synced) != 1 || deployer.synced[0] != app.ID {
		t.Errorf("the app was re-applied %v times, want once", deployer.synced)
	}

	// Reading it back says who, not what.
	status, body = h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/password", nil)
	if status != http.StatusOK || !strings.Contains(body, `"username":"client"`) || strings.Contains(body, "$2") {
		t.Errorf("reading the password answered %d\n%s", status, body)
	}
}

// A member can deploy, and should not be able to lock a team out of its own
// app or quietly open one up.
func TestOnlyAnAdminCanChangeAnAppsPassword(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	member := h.newMember(acme, "dev", store.RoleMember)
	app := h.app(acme, "staging")

	if status, _ := h.do(member, http.MethodPut, "/api/apps/"+app.ID+"/password",
		map[string]any{"username": "client", "password": "long enough"}); status != http.StatusForbidden {
		t.Errorf("a member setting a password answered %d, want 403", status)
	}
	if status, _ := h.do(member, http.MethodDelete, "/api/apps/"+app.ID+"/password", nil); status != http.StatusForbidden {
		t.Errorf("a member removing a password answered %d, want 403", status)
	}
	// Seeing whether there is one is fine: it is on the app's page.
	if status, _ := h.do(member, http.MethodGet, "/api/apps/"+app.ID+"/password", nil); status != http.StatusOK {
		t.Errorf("a member reading the password state answered %d, want 200", status)
	}
}

func TestAPasswordAccountIsCheckedBeforeItIsStored(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "staging")

	for _, tc := range []struct {
		username, password, want string
	}{
		{"", "long enough", "username is needed"},
		{"a:b", "long enough", "cannot contain a colon"},
		{"a b", "long enough", "cannot contain a colon"},
		{strings.Repeat("u", 65), "long enough", "too long"},
		{"client", "short", "too short"},
		{"client", strings.Repeat("p", 73), "72 bytes"},
	} {
		status, body := h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/password",
			map[string]any{"username": tc.username, "password": tc.password})
		if status != http.StatusBadRequest || !strings.Contains(body, tc.want) {
			t.Errorf("%q / %d characters answered %d, want 400 saying %q\n%s",
				tc.username, len(tc.password), status, tc.want, body)
		}
	}
	if _, ok, _ := h.db.GetAppPassword(t.Context(), app.ID); ok {
		t.Fatal("an invalid account was stored")
	}
}

// A browser sends the password to a plain-HTTP address readable by anybody on
// the path. The page has to be able to say which addresses those are.
func TestThePageIsToldWhichAddressesHaveNoHTTPS(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "staging")
	for _, domain := range []store.Domain{
		{AppID: app.ID, Hostname: "staging.example.test", TLS: true},
		{AppID: app.ID, Hostname: "staging-production.203-0-113-7.sslip.io", Auto: true},
	} {
		if err := h.db.CreateDomain(t.Context(), &domain); err != nil {
			t.Fatal(err)
		}
	}

	_, body := h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/password", nil)
	var view passwordView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Hostnames) != 2 {
		t.Errorf("the password guards %v", view.Hostnames)
	}
	if len(view.PlainHTTP) != 1 || view.PlainHTTP[0] != "staging-production.203-0-113-7.sslip.io" {
		t.Errorf("the addresses without HTTPS are %v", view.PlainHTTP)
	}
}

func TestRemovingAPasswordOpensTheAppAgain(t *testing.T) {
	h := newHarness(t)
	deployer := &syncingDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}
	h.api.deployer = deployer
	acme := h.newTenant("acme")
	app := h.app(acme, "staging")

	h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/password",
		map[string]any{"username": "client", "password": "long enough"})
	status, body := h.do(acme, http.MethodDelete, "/api/apps/"+app.ID+"/password", nil)
	if status != http.StatusOK || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("removing the password answered %d\n%s", status, body)
	}
	if _, ok, _ := h.db.GetAppPassword(t.Context(), app.ID); ok {
		t.Fatal("the password is still stored")
	}
	if len(deployer.synced) != 2 {
		t.Errorf("the app was re-applied %d times, want once per change", len(deployer.synced))
	}
}

// A pull request's preview of a locked staging site is locked with the same
// password. Otherwise locking staging would leave every copy of it open.
func TestAPreviewOfALockedAppIsLockedToo(t *testing.T) {
	h := newHarness(t)
	h.api.deployer = &fakeDeployer{log: &recorder{}}
	acme := h.newTenant("acme")
	app := h.app(acme, "staging")
	if err := h.db.SetAppPassword(t.Context(), store.AppPassword{
		AppID: app.ID, Username: "client", Hash: "$2y$06$abcdefghijklmnopqrstuuv0123456789abcdefghijklmnopqrs",
	}, "test"); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/git/src", nil)
	if _, err := h.api.deployPreview(request, app, gitsrc.PushEvent{
		Kind: "pull_request_opened", PullRequest: 7, SourceBranch: "feature", CommitSHA: "abc",
	}); err != nil {
		t.Fatalf("deployPreview: %v", err)
	}

	env, err := h.db.FindEnvironmentBySourceRef(t.Context(), acme.project.ID, "pr-7")
	if err != nil {
		t.Fatalf("no preview environment: %v", err)
	}
	previews, err := h.db.ListApps(t.Context(), env.ID)
	if err != nil || len(previews) != 1 {
		t.Fatalf("the preview has %d apps: %v", len(previews), err)
	}
	copied, ok, err := h.db.GetAppPassword(t.Context(), previews[0].ID)
	if err != nil || !ok {
		t.Fatalf("the preview of a locked app is open: %v", err)
	}
	if copied.Username != "client" || !strings.HasPrefix(copied.Hash, "$2y$06$") {
		t.Errorf("the preview has %+v", copied)
	}
}
