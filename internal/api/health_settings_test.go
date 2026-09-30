package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// A health check can be chosen when an app is made and changed afterwards,
// and what the probes could not be written from is refused with a problem
// that says so.
func TestAnAppsHealthCheckCanBeChosen(t *testing.T) {
	h := newHarness(t)
	owner := h.newTenant("acme")

	code, body := h.do(owner, "POST", "/api/environments/"+owner.env.ID+"/apps", map[string]any{
		"name": "keycloak", "image": "quay.io/keycloak/keycloak:26.0", "port": 8080,
		"health_path": "/health/ready", "health_start_seconds": 600, "health_timeout_seconds": 10,
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		App store.App `json:"app"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	app := created.App
	if app.HealthCheck != "http" || app.HealthStartSeconds != 600 || app.HealthTimeoutSeconds != 10 {
		t.Fatalf("created as %q, %ds, %ds", app.HealthCheck, app.HealthStartSeconds, app.HealthTimeoutSeconds)
	}

	for _, tc := range []struct {
		name string
		body map[string]any
		code string
	}{
		{"an unknown check", map[string]any{"health_check": "grpc"}, "app.health_check_unknown"},
		{"http with no path", map[string]any{"health_check": "http", "health_path": ""}, "app.health_path_required"},
		{"too little time to start", map[string]any{"health_start_seconds": 5}, "app.health_start_out_of_range"},
		{"too much time to start", map[string]any{"health_start_seconds": 3600}, "app.health_start_out_of_range"},
		{"no time to answer", map[string]any{"health_timeout_seconds": 0}, "app.health_timeout_out_of_range"},
		{"too long to answer", map[string]any{"health_timeout_seconds": 61}, "app.health_timeout_out_of_range"},
	} {
		code, body := h.do(owner, "PATCH", "/api/apps/"+app.ID, tc.body)
		if code != http.StatusBadRequest || !strings.Contains(body, `"`+tc.code+`"`) {
			t.Errorf("%s: %d %s, want 400 %s", tc.name, code, body, tc.code)
		}
	}
	unchanged, err := h.db.GetApp(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.HealthCheck != "http" || unchanged.HealthPath != "/health/ready" || unchanged.HealthStartSeconds != 600 {
		t.Fatalf("a refused change was written anyway: %+v", unchanged)
	}

	// Off, for software nothing can ask.
	code, body = h.do(owner, "PATCH", "/api/apps/"+app.ID, map[string]any{"health_check": "none"})
	if code != http.StatusOK || !strings.Contains(body, `"health_check":"none"`) {
		t.Fatalf("switching the check off: %d %s", code, body)
	}
}

// A client that only knows the health path gets what it always got: a path
// turns an HTTP check on, and clearing it falls back to a connect. A check
// that was switched off stays off.
func TestTheHealthPathStillDecidesTheCheckForClientsThatOnlySendIt(t *testing.T) {
	h := newHarness(t)
	owner := h.newTenant("acme")
	app, err := h.db.GetApp(t.Context(), h.app(owner, "web").ID)
	if err != nil {
		t.Fatal(err)
	}
	if app.HealthCheck != "tcp" {
		t.Fatalf("an app with no path starts as %q", app.HealthCheck)
	}
	// What the form gives every app, so the resources are not what a change
	// is refused for.
	app.CPURequestM, app.MemRequestMB = 50, 128
	if err := h.db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}

	step := func(body map[string]any, want string) {
		t.Helper()
		code, answer := h.do(owner, "PATCH", "/api/apps/"+app.ID, body)
		if code != http.StatusOK {
			t.Fatalf("%v: %d %s", body, code, answer)
		}
		got, err := h.db.GetApp(t.Context(), app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.HealthCheck != want {
			t.Fatalf("%v: the check is %q, want %q", body, got.HealthCheck, want)
		}
	}
	step(map[string]any{"health_path": "/healthz"}, "http")
	step(map[string]any{"health_path": ""}, "tcp")
	step(map[string]any{"health_check": "none"}, "none")
	step(map[string]any{"health_path": "/up"}, "none")
	// Sending the path it already has changes nothing about the check.
	step(map[string]any{"health_check": "tcp"}, "tcp")
	step(map[string]any{"health_path": "/up"}, "tcp")
}
