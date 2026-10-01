package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// `skifity drains` adds, lists, tests and removes a team's drains through the
// panel, names one the way it is listed, and never takes a credential as a
// flag's value: it comes from a file, from stdin, or a prompt.
func TestDrainsAreManagedByNameAndSecretsNeverAreFlags(t *testing.T) {
	const token = "xaat-not-a-real-token"
	var mu sync.Mutex
	var asked []string
	var added map[string]any
	drain := `{"id":"ldr_1","name":"Axiom","kind":"axiom","destination":"https://api.axiom.co/v1/datasets/apps/ingest",
		"status":"applied","enabled":true,"scoped":true,"projects":["prj_1"],"include_builds":true,"tested_at":"2026-09-30T03:17:00Z"}`
	kinds := `[{"kind":"axiom","fields":[{"key":"dataset","required":true},{"key":"region"},{"key":"org_id"},
		{"key":"token","secret":true,"required":true}]}]`
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/me":
			_, _ = io.WriteString(w, `{"teams":[{"id":"team_1","name":"acme"}]}`)
		case "GET /api/teams/team_1/projects":
			_, _ = io.WriteString(w, `{"items":[{"id":"prj_1","name":"Shop","slug":"shop"}],"total":1}`)
		case "GET /api/teams/team_1/log-drains":
			_, _ = io.WriteString(w, `{"items":[`+drain+`],"total":1,"kinds":`+kinds+`,
				"collector":{"configuration":"installed","live":{"state":"running","desired":2,"ready":2,"problems":[]}}}`)
		case "POST /api/teams/team_1/log-drains":
			_ = json.NewDecoder(r.Body).Decode(&added)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, drain)
		case "POST /api/teams/team_1/log-drains/ldr_1/test":
			_, _ = io.WriteString(w, `{"ok":true,"tested_at":"2026-09-30T03:18:00Z"}`)
		case "DELETE /api/teams/team_1/log-drains/ldr_1":
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.Error(w, `{"error":{"code":"resource.not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")

	// A secret as a flag's value is refused before anything is sent.
	var out bytes.Buffer
	err := cmdDrains(t.Context(), []string{"add", "Axiom", "--kind", "axiom", "--set", "dataset=apps", "--set", "token=" + token}, &out)
	if err == nil || !strings.Contains(err.Error(), "shell's history") {
		t.Errorf("a token as a flag answered %v", err)
	}

	// From a file, limited to a project by name, with the builds.
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cmdDrains(t.Context(), []string{"add", "Axiom", "--kind", "axiom", "--set", "dataset=apps",
		"--secret-file", "token=" + file, "--project", "shop", "--builds"}, &out); err != nil {
		t.Fatalf("add: %v", err)
	}
	settings, _ := added["settings"].(map[string]any)
	if settings["token"] != token || settings["dataset"] != "apps" || added["include_builds"] != true {
		t.Errorf("the panel was sent %v", added)
	}
	if projects, _ := added["projects"].([]any); len(projects) != 1 || projects[0] != "prj_1" {
		t.Errorf("the drain was limited to %v", added["projects"])
	}
	if strings.Contains(out.String(), token) {
		t.Errorf("the token was printed back: %s", out.String())
	}

	// Piped in, for a required secret nobody gave.
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stdin.WriteString(token + "\n")
	_, _ = stdin.Seek(0, 0)
	was := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = was })
	out.Reset()
	if err := cmdDrains(t.Context(), []string{"add", "Axiom", "--kind", "axiom", "--set", "dataset=apps", "--json"}, &out); err != nil {
		t.Fatalf("add from stdin: %v", err)
	}
	if settings, _ := added["settings"].(map[string]any); settings["token"] != token {
		t.Errorf("the piped token was not sent: %v", added)
	}

	out.Reset()
	if err := cmdDrains(t.Context(), []string{"--json"}, &out); err != nil {
		t.Fatalf("list: %v", err)
	}
	var listed drainList
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil || len(listed.Items) != 1 || listed.Items[0].Status != "applied" {
		t.Errorf("--json printed %s (%v)", out.String(), err)
	}
	out.Reset()
	if err := cmdDrains(t.Context(), nil, &out); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "Axiom") || !strings.Contains(out.String(), "running on 2 of 2") {
		t.Errorf("the list printed:\n%s", out.String())
	}

	for _, action := range []string{"test", "remove"} {
		out.Reset()
		if err := cmdDrains(t.Context(), []string{action, "axiom", "--json"}, &out); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if !json.Valid(out.Bytes()) {
			t.Errorf("%s --json printed %s", action, out.String())
		}
	}
	mu.Lock()
	if !contains(asked, "POST /api/teams/team_1/log-drains/ldr_1/test") || !contains(asked, "DELETE /api/teams/team_1/log-drains/ldr_1") {
		t.Errorf("the panel was asked %q", asked)
	}
	mu.Unlock()

	if err := cmdDrains(t.Context(), []string{"remove", "loki"}, &out); err == nil || !strings.Contains(err.Error(), "Axiom") {
		t.Errorf("an unknown drain answered %v", err)
	}
}
