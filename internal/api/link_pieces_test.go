package api

import (
	"net/http"
	"testing"

	"skifity/internal/store"
)

// The link route hands the pieces to the database manager, trimmed, and
// leaves them out when the request does: a link being renamed keeps its own.
func TestALinkRequestPassesItsPiecesOn(t *testing.T) {
	h, log := withDatabases(t, "")
	acme := h.newTenant("acme")
	web := h.app(acme, "web")
	orders := h.database(acme, "orders")

	status, body := h.do(acme, http.MethodPost, "/api/databases/"+orders.ID+"/link", map[string]any{
		"app_id": web.ID, "var_name": "DATABASE_URL",
		"pieces": map[string]any{"host": " DB_HOST ", "password": "DB_PASSWORD"},
	})
	if status != http.StatusOK {
		t.Fatalf("linking answered %d: %s", status, body)
	}
	if pieces, ok := log.piecesOf(web.ID); !ok || pieces != (store.LinkPieces{Host: "DB_HOST", Password: "DB_PASSWORD"}) {
		t.Errorf("the manager was given %+v (given %v)", pieces, ok)
	}

	other := h.app(acme, "worker")
	if status, body := h.do(acme, http.MethodPost, "/api/databases/"+orders.ID+"/link", map[string]any{"app_id": other.ID}); status != http.StatusOK {
		t.Fatalf("linking without pieces answered %d: %s", status, body)
	}
	if _, given := log.piecesOf(other.ID); given {
		t.Error("a request with no pieces was passed some")
	}

	// Pieces nobody asked about are not a field to slip anything else in.
	if status, _ := h.do(acme, http.MethodPost, "/api/databases/"+orders.ID+"/link", map[string]any{
		"app_id": web.ID, "pieces": map[string]any{"socket": "DB_SOCKET"},
	}); status != http.StatusBadRequest {
		t.Errorf("an unknown piece answered %d", status)
	}
}
