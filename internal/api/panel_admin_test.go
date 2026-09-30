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
