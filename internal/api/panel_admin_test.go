package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/auth"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/plugins"
	"skifity/internal/store"
)

func TestOnlyAPanelAdministratorManagesPlugins(t *testing.T) {
	// Any signed-in user can make a team and own it, so owning one was no
	// bar: a viewer invited anywhere could install an image beside the panel,
	// rewrite another plugin's settings, or remove somebody's deploy policy.
	h := newHarness(t)
	owner := h.newTenant("shop")
	admin := adminTenant(h, "ops")

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/plugins"},
		{http.MethodGet, "/api/plugins/store"},
		{http.MethodPost, "/api/plugins/inspect"},
		{http.MethodPost, "/api/plugins"},
		{http.MethodPatch, "/api/plugins/plg_1"},
		{http.MethodDelete, "/api/plugins/plg_1"},
	} {
		if status, body := h.do(owner, route.method, route.path, map[string]any{}); status != http.StatusForbidden {
			t.Errorf("a team owner who is not an administrator: %s %s answered %d: %s", route.method, route.path, status, truncate(body, 120))
		}
	}
	if status, body := h.do(admin, http.MethodGet, "/api/plugins", nil); status != http.StatusOK {
		t.Fatalf("an administrator listing plugins answered %d: %s", status, truncate(body, 120))
	}
}

const storedManifest = `apiVersion: plugin.skifity.com/v1
id: com.example.chat
name: Chat notifications
description: Sends notifications to a chat service.
version: 1.0.0
license: MIT
author:
  name: Example Ltd
image: ghcr.io/example/chat@sha256:0000000000000000000000000000000000000000000000000000000000000000
requires:
  skifity: ">=0.0.0"
`

func TestAStoreManifestIsTheOneTheStoreSigned(t *testing.T) {
	// The signed index names each manifest by its digest. Reviewing and
	// installing fetched the address again and never compared, so whoever
	// served it could swap the image under a page saying "verified".
	served := storedManifest
	manifests := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, served)
	}))
	t.Cleanup(manifests.Close)
	was := pluginManifestClient
	pluginManifestClient = manifests.Client()
	t.Cleanup(func() { pluginManifestClient = was })

	h := newHarness(t)
	admin := adminTenant(h, "ops")
	sum := sha256.Sum256([]byte(storedManifest))
	signed := hex.EncodeToString(sum[:])

	body := map[string]any{"url": manifests.URL, "sha256": signed}
	if status, answer := h.do(admin, http.MethodPost, "/api/plugins/inspect", body); status != http.StatusOK {
		t.Fatalf("the manifest the store signed answered %d: %s", status, truncate(answer, 200))
	}
	served = strings.Replace(storedManifest, "chat@sha256:0000", "chat@sha256:ffff", 1)
	if status, answer := h.do(admin, http.MethodPost, "/api/plugins/inspect", body); status != http.StatusConflict ||
		!strings.Contains(answer, "plugin.manifest_changed") {
		t.Fatalf("a manifest swapped after signing answered %d: %s", status, truncate(answer, 200))
	}
}

func TestOnlyAPanelAdministratorChangesTheClustersServers(t *testing.T) {
	// A server that joins is handed the cluster's join token, and a control
	// plane's is the whole cluster. Owning a team was enough to add one, and
	// anybody signed in can make a team.
	h := newHarness(t)
	owner := h.newTenant("shop")
	server := store.Server{TeamID: owner.team.ID, Name: "box", Host: "203.0.113.9", Role: "worker", Status: "failed"}
	if err := h.db.CreateServer(t.Context(), &server); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/teams/" + owner.team.ID + "/servers"},
		{http.MethodPost, "/api/servers/" + server.ID + "/retry"},
		{http.MethodPost, "/api/servers/" + server.ID + "/promote"},
		{http.MethodDelete, "/api/servers/" + server.ID},
	} {
		if status, body := h.do(owner, route.method, route.path, map[string]any{"host": "203.0.113.10", "control_plane": true}); status != http.StatusForbidden {
			t.Errorf("a team owner: %s %s answered %d: %s", route.method, route.path, status, truncate(body, 120))
		}
	}
	// Seeing them, and naming one, stay the team's.
	if status, _ := h.do(owner, http.MethodGet, "/api/teams/"+owner.team.ID+"/servers", nil); status != http.StatusOK {
		t.Errorf("a team owner listing its servers answered %d", status)
	}
	if status, _ := h.do(owner, http.MethodPatch, "/api/servers/"+server.ID, map[string]any{"name": "edge"}); status != http.StatusOK {
		t.Errorf("a team owner renaming its server answered %d", status)
	}
}

func TestALimitedTokenIsNotHandedASecret(t *testing.T) {
	// The legacy "read" scope allowed every GET, the recovery key — which is
	// the master key — among them, along with every database's password.
	h := newHarness(t)
	admin := adminTenant(h, "ops")
	database := h.database(admin, "orders")
	_, readOnly, err := h.auth.CreateAPIToken(t.Context(), admin.user.ID, admin.team.ID, "dashboard", "read", 0)
	if err != nil {
		t.Fatal(err)
	}
	limited := admin
	limited.token = readOnly
	for _, path := range []string{
		"/api/security/recovery-key",
		"/api/databases/" + database.ID + "/credentials",
		"/api/teams/" + admin.team.ID + "/export",
	} {
		if status, body := h.do(limited, http.MethodGet, path, nil); status != http.StatusForbidden {
			t.Errorf("a read-only token: GET %s answered %d: %s", path, status, truncate(body, 120))
		}
	}
	// Not even a token with no scopes reads the master key: that wants a
	// person who has just typed their password.
	if status, body := h.do(admin, http.MethodGet, "/api/security/recovery-key", nil); status != http.StatusForbidden ||
		!strings.Contains(body, "auth.needs_person") {
		t.Errorf("a full token reading the recovery key answered %d: %s", status, truncate(body, 120))
	}
}

func TestAPluginThatAskedForNothingGetsNothing(t *testing.T) {
	manifest, err := plugins.Parse([]byte(storedManifest))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Permissions) != 0 {
		t.Fatalf("the test manifest asks for %v", manifest.Permissions)
	}
	// An empty scope list is full access: "asked for nothing" written as
	// nothing handed the plugin the administrator's whole account.
	scopes := manifest.Scopes()
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/apps/app_1"},
		{http.MethodDelete, "/api/apps/app_1"},
		{http.MethodGet, "/api/security/recovery-key"},
	} {
		if auth.TokenAllows(scopes, request.method, request.path) {
			t.Errorf("a plugin with no permissions may %s %s (scopes %q)", request.method, request.path, scopes)
		}
	}
	if err := auth.ValidateScopes(scopes); err != nil {
		t.Fatalf("its scopes are refused when the token is made: %v", err)
	}
}

func TestTheTeamStreamSaysAnEntryWasRecordedAndNotWhatItSays(t *testing.T) {
	// A viewer may subscribe to the team's stream and may not read the audit
	// log. Every entry — actor, address, user agent, the full command a run
	// ran — went out on that stream.
	h := newHarness(t)
	acme := h.newTenant("acme")
	stream := h.api.hub.Subscribe(t.Context(), 0, events.TeamTopic(acme.team.ID))
	defer stream.Close()

	if status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/projects", map[string]any{"name": "secret plans"}); status >= 400 {
		t.Fatalf("creating a project answered %d: %s", status, truncate(body, 120))
	}
	for {
		select {
		case event := <-stream.Events():
			if event.Type != "audit" {
				continue
			}
			payload, _ := json.Marshal(event.Data)
			if string(payload) != "{}" {
				t.Fatalf("the team's stream carried the audit entry: %s", payload)
			}
			return
		case <-time.After(5 * time.Second):
			t.Fatal("nothing said an entry was recorded, so the activity page never refreshes")
		}
	}
}

func TestATokenMadeForOneTeamSeesThatTeam(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	// The same person owns a second team the token was not made for.
	other := store.Team{Name: "side project", Slug: "side-project"}
	if err := h.db.CreateTeam(t.Context(), &other); err != nil {
		t.Fatal(err)
	}
	if err := h.db.AddMember(t.Context(), other.ID, acme.user.ID, store.RoleOwner); err != nil {
		t.Fatal(err)
	}
	_, body := h.do(acme, http.MethodGet, "/api/teams", nil)
	if strings.Contains(body, other.ID) || !strings.Contains(body, acme.team.ID) {
		t.Fatalf("a token made for one team listed %s", truncate(body, 300))
	}
	if status, _ := h.do(acme, http.MethodPost, "/api/teams", map[string]any{"name": "another"}); status != http.StatusForbidden {
		t.Fatalf("a token made for one team made another: %d", status)
	}
}

func TestAnImageAppCannotRunAnotherEnvironmentsBuild(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	rival := h.newTenant("rival")
	theirs := kube.RegistryHost() + "/" + rival.env.Namespace + "/web:0123456789ab"
	path := "/api/environments/" + acme.env.ID + "/apps"
	for _, image := range []string{theirs, fmt.Sprintf("127.0.0.1:%d/%s/web:d1", kube.RegistryNodePort, rival.env.Namespace)} {
		if status, body := h.do(acme, http.MethodPost, path, map[string]any{"name": "copy", "source_type": "image", "image": image}); status != http.StatusBadRequest {
			t.Errorf("an app of another team's build %s answered %d: %s", image, status, truncate(body, 160))
		}
	}
	// Its own environment's build, and any public image, are fine.
	ours := kube.RegistryHost() + "/" + acme.env.Namespace + "/web:0123456789ab"
	for i, image := range []string{ours, "nginx:1.27"} {
		body := map[string]any{"name": fmt.Sprintf("ok%d", i), "source_type": "image", "image": image}
		if status, answer := h.do(acme, http.MethodPost, path, body); status >= 400 {
			t.Errorf("%s answered %d: %s", image, status, truncate(answer, 160))
		}
	}
	// And not by changing an existing app's image either.
	app := h.app(acme, "cache")
	if status, _ := h.do(acme, http.MethodPatch, "/api/apps/"+app.ID, map[string]any{"image": theirs}); status != http.StatusBadRequest {
		t.Errorf("changing an app to another team's build answered %d", status)
	}
}

// --- the accounts on the panel ---

func TestOnlyAPanelAdministratorManagesAccounts(t *testing.T) {
	// Owning a team is no bar — anybody signed in can make one — so the
	// accounts of everybody else are the panel administrator's alone.
	h := newHarness(t)
	owner := h.newTenant("shop")
	admin := adminTenant(h, "ops")
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/users"},
		{http.MethodPatch, "/api/users/" + admin.user.ID},
		{http.MethodDelete, "/api/users/" + admin.user.ID},
		{http.MethodPost, "/api/users/" + admin.user.ID + "/two-factor/reset"},
	}
	for _, route := range routes {
		if status, body := h.do(owner, route.method, route.path, map[string]any{"disabled": true}); status != http.StatusForbidden {
			t.Errorf("a team owner who is not an administrator: %s %s answered %d: %s", route.method, route.path, status, truncate(body, 120))
		}
	}

	status, body := h.do(admin, http.MethodGet, "/api/users", nil)
	if status != http.StatusOK {
		t.Fatalf("the administrator listing accounts answered %d: %s", status, truncate(body, 200))
	}
	var list struct {
		Items []accountView `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	seen := map[string]accountView{}
	for _, account := range list.Items {
		seen[account.User.Email] = account
	}
	shop, ops := seen[owner.user.Email], seen[admin.user.Email]
	if len(list.Items) != 2 || !ops.Current || shop.Current || len(shop.Teams) != 1 ||
		shop.Teams[0].Role != store.RoleOwner || shop.Tokens != 1 || len(shop.Blockers.SoleOwnerOf) != 1 {
		t.Errorf("the accounts are listed as %s", truncate(body, 600))
	}
	if strings.Contains(body, `"x"`) || strings.Contains(body, "password_hash") {
		t.Errorf("the list carries a password hash: %s", truncate(body, 600))
	}
}

func TestGivingAccessAsksForAPersonAndTakingItAwayDoesNot(t *testing.T) {
	// Making somebody an administrator, enabling an account and taking its
	// second factor away are access somebody keeps: a token cannot, and a
	// session has to have proved itself a moment ago. Disabling, demoting and
	// deleting take access away and only need confirming, which the page does.
	h := newHarness(t)
	admin := adminTenant(h, "ops")
	target := h.newTenant("shop")

	gives := []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPatch, "/api/users/" + target.user.ID, map[string]any{"is_admin": true}},
		{http.MethodPatch, "/api/users/" + target.user.ID, map[string]any{"disabled": false}},
		{http.MethodPost, "/api/users/" + target.user.ID + "/two-factor/reset", map[string]any{}},
	}
	for _, give := range gives {
		status, body := h.do(admin, give.method, give.path, give.body)
		if status != http.StatusForbidden || !strings.Contains(body, "auth.needs_person") {
			t.Errorf("a token: %s %s %v answered %d: %s", give.method, give.path, give.body, status, truncate(body, 160))
		}
	}

	b, _ := h.sessionFor(t, admin.user, auth.MethodPassword)
	h.spendReauth(t, admin.user.Email)
	for _, give := range gives {
		status, body := h.send(t, b, give.method, give.path, b.csrf, give.body)
		if status != http.StatusForbidden || !strings.Contains(body, "auth.reauth_required") {
			t.Errorf("a session past its step-up: %s %s %v answered %d: %s", give.method, give.path, give.body, status, truncate(body, 160))
		}
	}
	if user, _ := h.db.GetUser(t.Context(), target.user.ID); user.IsAdmin {
		t.Fatal("a refused grant made the account an administrator anyway")
	}

	// Taking access away is a token's to do.
	if status, body := h.do(admin, http.MethodPatch, "/api/users/"+target.user.ID, map[string]any{"disabled": true}); status != http.StatusOK {
		t.Fatalf("a token disabling an account answered %d: %s", status, truncate(body, 160))
	}

	// And a session that has just proved itself gives it back.
	fresh, _ := h.sessionFor(t, admin.user, auth.MethodPassword)
	if status, body := h.send(t, fresh, http.MethodPatch, "/api/users/"+target.user.ID, fresh.csrf,
		map[string]any{"disabled": false, "is_admin": true}); status != http.StatusOK {
		t.Fatalf("enabling and promoting after a step-up answered %d: %s", status, truncate(body, 160))
	}
	if user, _ := h.db.GetUser(t.Context(), target.user.ID); user.Disabled || !user.IsAdmin {
		t.Fatalf("the account is %+v", user)
	}

	actions := map[string]bool{}
	events, err := h.db.ListAudit(t.Context(), target.team.ID, "", target.user.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		actions[event.Action] = true
	}
	for _, want := range []string{"account.disabled", "account.enabled", "account.admin_granted"} {
		if !actions[want] {
			t.Errorf("the account's team has no %s in its activity log: %v", want, actions)
		}
	}
	// The administrator's own team is not told about somebody it does not have.
	theirs, err := h.db.ListAudit(t.Context(), admin.team.ID, "", target.user.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(theirs) != 0 {
		t.Errorf("another team's activity log shows the change: %+v", theirs)
	}
}

func TestADisabledAccountIsShutOutAtOnce(t *testing.T) {
	const password = "correct horse battery staple"
	h := newHarness(t)
	admin := adminTenant(h, "ops")
	person := h.person(t, "leaver@example.test", password)
	b := h.signIn(t, person.Email, password)
	_, token, err := h.auth.CreateAPIToken(t.Context(), person.ID, admin.team.ID, "ci", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.db.AddMember(t.Context(), admin.team.ID, person.ID, store.RoleMember); err != nil {
		t.Fatal(err)
	}
	if status, _ := h.send(t, b, http.MethodGet, "/api/me", "", nil); status != http.StatusOK {
		t.Fatalf("before, the session answered %d", status)
	}

	// What a sign-in in flight holds: the row as it was before the disable.
	stale, err := h.db.GetUser(t.Context(), person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(admin, http.MethodPatch, "/api/users/"+person.ID, map[string]any{"disabled": true}); status != http.StatusOK {
		t.Fatalf("disabling answered %d: %s", status, truncate(body, 160))
	}
	if status, _ := h.send(t, b, http.MethodGet, "/api/me", "", nil); status != http.StatusUnauthorized {
		t.Errorf("the session answered %d after the disable, want 401", status)
	}
	if status, _ := h.do(tenant{token: token}, http.MethodGet, "/api/me", nil); status != http.StatusUnauthorized {
		t.Errorf("the API token answered %d after the disable, want 401", status)
	}

	// The sign-in finishes: it writes only what it owns, and the session it
	// was about to hand out is refused the first time it is used.
	if err := h.db.UpdatePasswordHash(t.Context(), stale.ID, stale.PasswordHash); err != nil {
		t.Fatal(err)
	}
	if err := h.db.TouchUserLogin(t.Context(), stale.ID); err != nil {
		t.Fatal(err)
	}
	late, _ := h.sessionFor(t, stale, auth.MethodPassword)
	if status, _ := h.send(t, late, http.MethodGet, "/api/me", "", nil); status != http.StatusUnauthorized {
		t.Errorf("a session issued by a sign-in already under way answered %d, want 401", status)
	}
	if user, _ := h.db.GetUser(t.Context(), person.ID); !user.Disabled {
		t.Fatal("a sign-in under way undid the disable")
	}
	if status, body := h.send(t, browser{}, http.MethodPost, "/api/auth/login", "",
		map[string]string{"email": person.Email, "password": password}); status != http.StatusUnauthorized {
		t.Errorf("signing in to a disabled account answered %d: %s", status, truncate(body, 160))
	}

	// Enabled again, the password signs in.
	fresh, _ := h.sessionFor(t, admin.user, auth.MethodPassword)
	if status, body := h.send(t, fresh, http.MethodPatch, "/api/users/"+person.ID, fresh.csrf,
		map[string]any{"disabled": false}); status != http.StatusOK {
		t.Fatalf("enabling answered %d: %s", status, truncate(body, 160))
	}
	h.signIn(t, person.Email, password)
}

func TestTheAccountsPageRefusesWhatWouldLockSomebodyOut(t *testing.T) {
	h := newHarness(t)
	admin := adminTenant(h, "ops")
	owner := h.newTenant("shop")
	self, _ := h.sessionFor(t, admin.user, auth.MethodPassword)

	// Your own account is the account page's.
	for _, route := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPatch, "/api/users/" + admin.user.ID, map[string]any{"disabled": true}},
		{http.MethodPatch, "/api/users/" + admin.user.ID, map[string]any{"is_admin": false}},
		{http.MethodDelete, "/api/users/" + admin.user.ID, nil},
		{http.MethodPost, "/api/users/" + admin.user.ID + "/two-factor/reset", nil},
	} {
		status, body := h.send(t, self, route.method, route.path, self.csrf, route.body)
		if status != http.StatusConflict || !strings.Contains(body, "account.self") {
			t.Errorf("acting on yourself: %s %s answered %d: %s", route.method, route.path, status, truncate(body, 160))
		}
	}

	// A team's only owner, by the team's name.
	status, body := h.do(admin, http.MethodDelete, "/api/users/"+owner.user.ID, nil)
	if status != http.StatusConflict || !strings.Contains(body, "account.last_owner") || !strings.Contains(body, "shop") {
		t.Errorf("deleting a team's only owner answered %d: %s", status, truncate(body, 200))
	}

	// A plugin's installer, by the plugin's name.
	second := h.newMember(owner, "partner", store.RoleOwner)
	if err := h.db.CreatePlugin(t.Context(), &store.Plugin{
		ID: "com.example.chat", Manifest: storedManifest, Version: "1.0.0", InstalledBy: owner.user.ID,
	}); err != nil {
		t.Fatal(err)
	}
	status, body = h.do(admin, http.MethodDelete, "/api/users/"+owner.user.ID, nil)
	if status != http.StatusConflict || !strings.Contains(body, "account.owns_plugins") || !strings.Contains(body, "Chat notifications") {
		t.Errorf("deleting a plugin's installer answered %d: %s", status, truncate(body, 200))
	}
	if err := h.db.DeletePlugin(t.Context(), "com.example.chat"); err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(admin, http.MethodDelete, "/api/users/"+owner.user.ID, nil); status != http.StatusOK {
		t.Fatalf("deleting an account nothing depends on answered %d: %s", status, truncate(body, 200))
	}
	if _, err := h.db.GetUser(t.Context(), owner.user.ID); err == nil {
		t.Fatal("the account is still there")
	}
	events, _ := h.db.ListAudit(t.Context(), owner.team.ID, "account.deleted", owner.user.ID, 10)
	if len(events) != 1 {
		t.Errorf("the team's activity log has %d entries for the delete", len(events))
	}
	if status, _ := h.do(second, http.MethodGet, "/api/teams/"+owner.team.ID, nil); status != http.StatusOK {
		t.Errorf("the team's other owner answered %d afterwards", status)
	}

	// Two administrators taking each other's rights away at the same moment
	// leave one. Which one, and whether the loser hears account.last_admin or
	// that it is no longer an administrator, depends on the order they land in.
	other := adminTenant(h, "infra")
	var wg sync.WaitGroup
	answers := make([]string, 2)
	for i, pair := range [][2]tenant{{admin, other}, {other, admin}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, body := h.do(pair[0], http.MethodPatch, "/api/users/"+pair[1].user.ID, map[string]any{"is_admin": false})
			answers[i] = fmt.Sprintf("%d %s", status, truncate(body, 120))
		}()
	}
	wg.Wait()
	if n, _ := h.db.CountEnabledAdmins(t.Context()); n != 1 {
		t.Errorf("after two administrators demoted each other, %d are left: %v", n, answers)
	}
	for _, answer := range answers {
		if !strings.HasPrefix(answer, "200 ") && !strings.Contains(answer, "account.last_admin") && !strings.Contains(answer, "auth.forbidden") {
			t.Errorf("a demotion answered %s", answer)
		}
	}
}

func TestAnAdministratorCanResetSomebodysTwoFactor(t *testing.T) {
	const password = "correct horse battery staple"
	h := newHarness(t)
	admin := adminTenant(h, "ops")
	person := h.person(t, "phoneless@example.test", password)
	h.withTwoFactor(t, &person)
	b, _ := h.sessionFor(t, person, auth.MethodTOTP)

	fresh, _ := h.sessionFor(t, admin.user, auth.MethodPassword)
	if status, body := h.send(t, fresh, http.MethodPost, "/api/users/"+person.ID+"/two-factor/reset", fresh.csrf, nil); status != http.StatusOK {
		t.Fatalf("the reset answered %d: %s", status, truncate(body, 160))
	}
	after, err := h.db.GetUser(t.Context(), person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPEnabled || after.TOTPSecretEnc != "" {
		t.Errorf("two-factor is still there: %+v", after)
	}
	if left, _ := h.db.CountRecoveryCodes(t.Context(), person.ID); left != 0 {
		t.Errorf("%d recovery codes are left", left)
	}
	if status, _ := h.send(t, b, http.MethodGet, "/api/me", "", nil); status != http.StatusUnauthorized {
		t.Errorf("the account's session answered %d after the reset, want 401", status)
	}
	h.signIn(t, person.Email, password)
	if events, _ := h.db.ListAudit(t.Context(), "", "account.two_factor_reset", person.ID, 10); len(events) != 1 {
		t.Errorf("the activity log has %d entries for the reset", len(events))
	}
}
