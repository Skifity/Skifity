package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
