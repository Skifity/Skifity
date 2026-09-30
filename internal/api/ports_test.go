package api

import (
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// A public port is the cluster's to hand out once: every server opens it.

func TestAPublicPortIsOpenedOnceAcrossThePanel(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	minecraft := h.app(acme, "minecraft")
	other := h.app(globex, "game")

	status, body := h.do(acme, http.MethodPost, "/api/apps/"+minecraft.ID+"/ports", map[string]any{"port": 25565})
	if status != http.StatusCreated || !strings.Contains(body, `"public_port":25565`) || !strings.Contains(body, `"protocol":"tcp"`) {
		t.Fatalf("opening a port answered %d: %s", status, body)
	}
	// Another team's app cannot have the same one: it is the same port on
	// the same servers. What it is told does not name the app that has it.
	status, body = h.do(globex, http.MethodPost, "/api/apps/"+other.ID+"/ports", map[string]any{"port": 25565})
	if status != http.StatusConflict || !strings.Contains(body, "port.taken") || strings.Contains(body, "minecraft") {
		t.Fatalf("a taken port answered %d: %s", status, body)
	}
	// The same number over UDP is a different port.
	if status, body := h.do(globex, http.MethodPost, "/api/apps/"+other.ID+"/ports",
		map[string]any{"port": 25565, "protocol": "udp"}); status != http.StatusCreated {
		t.Fatalf("the UDP port answered %d: %s", status, body)
	}
	// And a different public port for the same container port is fine.
	if status, body := h.do(globex, http.MethodPost, "/api/apps/"+other.ID+"/ports",
		map[string]any{"port": 25565, "public_port": 25566}); status != http.StatusCreated {
		t.Fatalf("another public port answered %d: %s", status, body)
	}

	for name, body := range map[string]map[string]any{
		"ssh":            {"port": 22},
		"https":          {"port": 8443, "public_port": 443},
		"the node range": {"port": 30080},
		"no port":        {"port": 0},
		"sctp":           {"port": 5000, "protocol": "sctp"},
	} {
		if status, answer := h.do(acme, http.MethodPost, "/api/apps/"+minecraft.ID+"/ports", body); status != http.StatusBadRequest {
			t.Errorf("%s answered %d: %s", name, status, answer)
		}
	}
}

func TestOnlyTheAppsMembersOpenAndCloseItsPorts(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	app := h.app(acme, "mqtt")
	path := "/api/apps/" + app.ID + "/ports"

	viewer := h.newMember(acme, "auditor", store.RoleViewer)
	if status, _ := h.do(viewer, http.MethodPost, path, map[string]any{"port": 1883}); status != http.StatusForbidden {
		t.Errorf("a viewer opened a port: %d", status)
	}
	if status, _ := h.do(globex, http.MethodPost, path, map[string]any{"port": 1883}); status != http.StatusNotFound {
		t.Errorf("another team opened a port on this app: %d", status)
	}
	if status, body := h.do(acme, http.MethodPost, path, map[string]any{"port": 1883}); status != http.StatusCreated {
		t.Fatalf("the owner could not open a port: %d %s", status, body)
	}
	if status, body := h.do(viewer, http.MethodGet, path, nil); status != http.StatusOK || !strings.Contains(body, "1883") {
		t.Errorf("a viewer cannot see the app's ports: %d %s", status, body)
	}
	ports, _ := h.db.ListPorts(t.Context(), app.ID)
	if status, _ := h.do(globex, http.MethodDelete, path+"/"+ports[0].ID, nil); status != http.StatusNotFound {
		t.Errorf("another team closed it: %d", status)
	}
	if status, _ := h.do(acme, http.MethodDelete, path+"/"+ports[0].ID, nil); status != http.StatusOK {
		t.Errorf("the owner could not close it: %d", status)
	}
	// Closed, it is free for anybody.
	if _, err := h.db.PortHolder(t.Context(), 1883, "tcp"); err == nil {
		t.Error("a closed port is still held")
	}
}
