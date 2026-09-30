package blueprint

import (
	"errors"
	"strings"
	"testing"

	"skifity/internal/errdoc"
)

// `health: /healthz` is still the path, and the long form chooses the rest.
func TestAHealthCheckIsReadInEitherForm(t *testing.T) {
	file := mustParse(t, shop)
	if h := file.Apps["web"].Health; h == nil || *h != (Health{Path: "/healthz"}) {
		t.Fatalf("the short form read as %+v", h)
	}

	file = mustParse(t, "apps:\n  auth:\n    image: quay.io/keycloak/keycloak:26.0\n"+
		"    health: {check: HTTP, path: \" /health/ready \", start: 600, timeout: 5}\n")
	if h := file.Apps["auth"].Health; h == nil || *h != (Health{Check: "http", Path: "/health/ready", Start: 600, Timeout: 5}) {
		t.Fatalf("the long form read as %+v", h)
	}

	_, err := Parse([]byte("apps:\n  web:\n    image: nginx\n    health: {chek: none}\n"))
	if err == nil || !strings.Contains(err.Error(), `"chek"`) {
		t.Fatalf("a misspelt health field: %v", err)
	}

	_, err = Parse([]byte("apps:\n  web:\n    image: nginx\n    health: {check: grpc, start: 5, timeout: 120}\n"))
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "blueprint.invalid" {
		t.Fatalf("a wrong health check: %v", err)
	}
	for _, want := range []string{`"grpc"`, "health start", "health timeout"} {
		if !strings.Contains(problem.Cause, want) {
			t.Errorf("the problems do not mention %q: %s", want, problem.Cause)
		}
	}
}

// A new app is made with the file's health check; one that exists is changed
// only where it differs, and a file that says nothing about it changes nothing.
func TestAHealthCheckIsPlannedLikeAnyOtherSetting(t *testing.T) {
	file := "apps:\n  auth:\n    image: quay.io/keycloak/keycloak:26.0\n    port: 8080\n" +
		"    health: {check: http, path: /health/ready, start: 600, timeout: 5}\n"
	steps, err := Plan(mustParse(t, file), State{EnvironmentID: "env_1"})
	if err != nil {
		t.Fatal(err)
	}
	body := steps[0].Call.Body
	if body["health_check"] != "http" || body["health_path"] != "/health/ready" ||
		body["health_start_seconds"] != 600 || body["health_timeout_seconds"] != 5 {
		t.Fatalf("the app is made with %+v", body)
	}

	state := matching()
	web := &state.Apps[0]
	web.App.HealthCheck, web.App.HealthStartSeconds, web.App.HealthTimeoutSeconds = "http", 120, 3
	if steps := planned(t, shop, state); len(steps) != 0 {
		t.Fatalf("a file that says only the path changed %+v", steps)
	}

	longer := strings.Replace(shop, "health: /healthz", "health: {path: /healthz, start: 900}", 1)
	steps = planned(t, longer, state)
	if len(steps) != 1 || steps[0].Kind != "settings" {
		t.Fatalf("a longer start planned %+v", steps)
	}
	if patch := steps[0].Call.Body; len(patch) != 1 || patch["health_start_seconds"] != 900 {
		t.Fatalf("a longer start sent %+v", patch)
	}

	off := strings.Replace(shop, "health: /healthz", "health: {check: none}", 1)
	steps = planned(t, off, state)
	if len(steps) != 1 || steps[0].Call.Body["health_check"] != "none" || len(steps[0].Call.Body) != 1 {
		t.Fatalf("switching the check off planned %+v", steps)
	}
}

// planned is the changes a plan makes, leaving out what it only says.
func planned(t *testing.T, file string, state State) []Step {
	t.Helper()
	steps, err := Plan(mustParse(t, file), state)
	if err != nil {
		t.Fatal(err)
	}
	var out []Step
	for _, step := range steps {
		if step.Op != "note" && step.Kind != "deploy" {
			out = append(out, step)
		}
	}
	return out
}
