package api

import (
	"net/http"
	"testing"
)

// The uid for an image that names its user: set on an image, refused on an
// app built here (which always runs as 1000), never root, never nonsense.
func TestRunAsUserIsForAnImageSomebodyElseBuilt(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	prometheus := h.app(acme, "prometheus")
	prometheus.SourceType, prometheus.Image = "image", "prom/prometheus:v3.15.0"
	prometheus.CPURequestM, prometheus.MemRequestMB = 50, 128
	if err := h.db.UpdateApp(t.Context(), &prometheus); err != nil {
		t.Fatal(err)
	}
	built := h.app(acme, "web")
	built.SourceType, built.RepoURL = "git", "https://git.example.test/acme/web.git"
	built.CPURequestM, built.MemRequestMB = 50, 128
	if err := h.db.UpdateApp(t.Context(), &built); err != nil {
		t.Fatal(err)
	}

	if status, body := h.do(acme, http.MethodPatch, "/api/apps/"+prometheus.ID, map[string]any{"run_as_user": 65534}); status != http.StatusOK {
		t.Fatalf("setting the uid answered %d: %s", status, body)
	}
	if got, _ := h.db.GetApp(t.Context(), prometheus.ID); got.RunAsUser != 65534 {
		t.Errorf("the uid saved is %d", got.RunAsUser)
	}
	for _, uid := range []int{-1, 1 << 31} {
		if status, _ := h.do(acme, http.MethodPatch, "/api/apps/"+prometheus.ID, map[string]any{"run_as_user": uid}); status != http.StatusBadRequest {
			t.Errorf("the uid %d was accepted: %d", uid, status)
		}
	}
	if status, _ := h.do(acme, http.MethodPatch, "/api/apps/"+built.ID, map[string]any{"run_as_user": 65534}); status != http.StatusBadRequest {
		t.Errorf("a uid for an app built here was accepted: %d", status)
	}
	// Back to the image's own, which is 0, is always allowed.
	if status, body := h.do(acme, http.MethodPatch, "/api/apps/"+prometheus.ID, map[string]any{"run_as_user": 0}); status != http.StatusOK {
		t.Fatalf("clearing the uid answered %d: %s", status, body)
	}
}
