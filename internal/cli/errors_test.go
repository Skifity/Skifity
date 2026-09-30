package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/store"
)

// `skifity deploy --help` printed the flags, then "Error: flag: help
// requested", and exited 1 — a script checking the help thought it failed.
func TestHelpIsNotAnError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(t.Context(), []string{"deploy", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--help exited %d\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "-commit") || strings.Contains(stderr.String(), "help requested") {
		t.Fatalf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

// The stored token belongs to the stored panel. SKIFITY_URL alone, pointing
// anywhere else, sent it there.
func TestAnotherPanelIsNotSentTheStoredToken(t *testing.T) {
	seen := make(chan string, 4)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(elsewhere.Close)

	dir := t.TempDir()
	t.Setenv("SKIFITY_CONFIG", filepath.Join(dir, "config.json"))
	t.Setenv("SKIFITY_TOKEN", "")
	if err := SaveConfig(Config{PanelURL: "https://panel.example.test", Token: "skf_the_stored_token", TeamID: "team_1"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKIFITY_URL", elsewhere.URL)
	if _, err := LoadConfig(); err == nil {
		t.Fatal("a panel nobody signed in to was given the stored token")
	}
	select {
	case header := <-seen:
		t.Fatalf("a request reached the other panel with %q", header)
	default:
	}

	// The same panel, written differently, is still the same panel.
	t.Setenv("SKIFITY_URL", "https://PANEL.example.test/")
	if cfg, err := LoadConfig(); err != nil || cfg.Token != "skf_the_stored_token" {
		t.Fatalf("the stored panel lost its token: %+v, %v", cfg, err)
	}
}

// Every error said "Copy this for an AI assistant with: skifity status
// --explain CODE", and that printed a pointer to the panel.
func TestAnErrorCanBeExplained(t *testing.T) {
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("SKIFITY_URL", "")
	t.Setenv("SKIFITY_TOKEN", "")
	var stdout, stderr bytes.Buffer
	// Scaling with a fixed count and a limit is refused before any request.
	t.Setenv("SKIFITY_URL", "http://127.0.0.1:1")
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	if code := Run(t.Context(), []string{"scale", "--app", "app_1", "--instances", "2", "--max", "4"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exited %d", code)
	}
	if !strings.Contains(stderr.String(), "status --explain request.invalid") {
		t.Fatalf("stderr %q", stderr.String())
	}
	stdout.Reset()
	if code := Run(t.Context(), []string{"status", "--explain", "request.invalid"}, &stdout, &stderr); code != 0 {
		t.Fatalf("explaining exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "--instances is a fixed number") || !strings.Contains(stdout.String(), "Error code: request.invalid") {
		t.Fatalf("the explanation is %q", stdout.String())
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(os.Getenv("SKIFITY_CONFIG")), "last-error.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the kept error is %v, %v", info, err)
	}
}

// With no skifity.toml the CLI took the team's first project, so a rollback
// run in the wrong folder rolled back another project's app.
func TestTwoProjectsAreAQuestionNotAGuess(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/me":
			_, _ = w.Write([]byte(`{"user":{"email":"a@example.test"},"teams":[{"id":"team_1","name":"Acme","slug":"acme"}]}`))
		case "/api/teams/team_1/projects":
			_, _ = w.Write([]byte(`{"items":[{"id":"prj_1","name":"Shop"},{"id":"prj_2","name":"Blog"}]}`))
		default:
			t.Errorf("asked for %s after the project was unclear", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Chdir(t.TempDir())
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolveEnvironment(t.Context(), NewClient(cfg), cfg)
	if err == nil || !strings.Contains(err.Error(), "Shop, Blog") {
		t.Fatalf("two projects gave %v", err)
	}
}

func TestLoginStoresTheTeamNamed(t *testing.T) {
	teams := []store.Team{{ID: "team_1", Name: "Acme", Slug: "acme"}, {ID: "team_2", Name: "Globex", Slug: "globex"}}
	var out bytes.Buffer
	if team, err := chooseTeam(teams, "globex", &out); err != nil || team.ID != "team_2" {
		t.Fatalf("--team globex chose %+v (%v)", team, err)
	}
	if _, err := chooseTeam(teams, "initech", &out); err == nil {
		t.Fatal("a team the account is not in was accepted")
	}
	// Nobody to ask: no team is stored, rather than the first one.
	if team, err := chooseTeam(teams, "", &out); err != nil || team.ID != "" {
		t.Fatalf("with nobody to ask, %+v was stored (%v)", team, err)
	}
}
