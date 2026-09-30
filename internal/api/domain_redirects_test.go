package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"skifity/internal/store"
)

func TestADomainRedirectsToAnotherOfItsApp(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "shop")
	path := "/api/apps/" + app.ID + "/domains"

	if status, body := h.do(acme, http.MethodPost, path, map[string]any{"hostname": "example.com"}); status != http.StatusCreated {
		t.Fatalf("adding the domain answered %d: %s", status, body)
	}
	// Added redirecting straight away.
	status, body := h.do(acme, http.MethodPost, path, map[string]any{"hostname": "www.example.com", "redirect_to": "Example.com"})
	if status != http.StatusCreated {
		t.Fatalf("adding a redirecting domain answered %d: %s", status, body)
	}
	var added struct {
		ID         string `json:"id"`
		RedirectTo string `json:"redirect_to"`
	}
	_ = json.Unmarshal([]byte(body), &added)
	if added.RedirectTo != "example.com" {
		t.Errorf("the redirect was saved as %q", added.RedirectTo)
	}

	// Somewhere the app does not answer, a chain, and itself are refused.
	if status, _ := h.do(acme, http.MethodPost, path, map[string]any{"hostname": "a.example.com", "redirect_to": "elsewhere.test"}); status != http.StatusBadRequest {
		t.Errorf("a redirect to a hostname the app does not have answered %d", status)
	}
	if status, _ := h.do(acme, http.MethodPost, path, map[string]any{"hostname": "b.example.com", "redirect_to": "www.example.com"}); status != http.StatusBadRequest {
		t.Errorf("a redirect to a redirect answered %d", status)
	}
	domains, _ := h.db.ListDomains(t.Context(), app.ID)
	var apex store.Domain
	for _, d := range domains {
		if d.Hostname == "example.com" {
			apex = d
		}
	}
	if status, _ := h.do(acme, http.MethodPatch, path+"/"+apex.ID, map[string]any{"redirect_to": "www.example.com"}); status != http.StatusBadRequest {
		t.Errorf("redirecting a hostname others redirect to answered %d", status)
	}
	if status, _ := h.do(acme, http.MethodPatch, path+"/"+added.ID, map[string]any{"redirect_to": "www.example.com"}); status != http.StatusBadRequest {
		t.Errorf("a domain redirecting to itself answered %d", status)
	}

	// Serving the app again, then redirecting again.
	if status, body := h.do(acme, http.MethodPatch, path+"/"+added.ID, map[string]any{"redirect_to": ""}); status != http.StatusOK {
		t.Fatalf("clearing the redirect answered %d: %s", status, body)
	}
	if status, body := h.do(acme, http.MethodPatch, path+"/"+added.ID, map[string]any{"redirect_to": "example.com"}); status != http.StatusOK {
		t.Fatalf("setting the redirect answered %d: %s", status, body)
	}

	// Removing the target leaves the redirecting domain serving the app, not
	// pointing at nothing.
	if status, body := h.do(acme, http.MethodDelete, path+"/"+apex.ID, nil); status != http.StatusOK {
		t.Fatalf("removing the target answered %d: %s", status, body)
	}
	domains, _ = h.db.ListDomains(t.Context(), app.ID)
	for _, d := range domains {
		if d.RedirectTo != "" {
			t.Errorf("%s still redirects to %s, which is gone", d.Hostname, d.RedirectTo)
		}
	}
}

func TestOnlyTheAppsMembersChangeItsRedirects(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	app := h.app(acme, "shop")
	domain := store.Domain{AppID: app.ID, Hostname: "www.example.com", Path: "/", TLS: true}
	if err := h.db.CreateDomain(t.Context(), &domain); err != nil {
		t.Fatal(err)
	}
	other := h.app(globex, "site")
	if status, _ := h.do(globex, http.MethodPatch, "/api/apps/"+app.ID+"/domains/"+domain.ID, map[string]any{"redirect_to": ""}); status != http.StatusNotFound {
		t.Errorf("another team changed the redirect: %d", status)
	}
	// Another app's domain through this app's path is not found either.
	if status, _ := h.do(globex, http.MethodPatch, "/api/apps/"+other.ID+"/domains/"+domain.ID, map[string]any{"redirect_to": ""}); status != http.StatusNotFound {
		t.Errorf("a domain was reached through another app: %d", status)
	}
}
