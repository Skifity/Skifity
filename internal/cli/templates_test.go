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

// `skifity templates catalogues` adds, lists, refreshes and removes a team's
// catalogues through the panel, names one the way it is listed, and never
// takes the token a private host wants as a flag: it is asked for, or piped
// in, so it is not in the shell's history.
func TestTemplateCataloguesAreManagedByName(t *testing.T) {
	const token = "glpat-not-a-real-token"
	var mu sync.Mutex
	var asked []string
	var added map[string]any
	catalogue := `{"id":"tcat_1","name":"Acme","url":"https://git.acme.example/catalogue.yaml",
		"auth_header_name":"PRIVATE-TOKEN","fetched_at":"2026-09-30T03:17:00Z","attempted_at":"2026-09-30T03:17:00Z",
		"templates":2,"problems":[{"file":"templates[2]","id":"floating","errors":["floating/floating runs \"x:latest\""]}]}`
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/me":
			_, _ = io.WriteString(w, `{"teams":[{"id":"team_1","name":"acme"}]}`)
		case "GET /api/teams/team_1/template-catalogues":
			_, _ = io.WriteString(w, `{"items":[`+catalogue+`],"total":1}`)
		case "POST /api/teams/team_1/template-catalogues":
			_ = json.NewDecoder(r.Body).Decode(&added)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, catalogue)
		case "POST /api/teams/team_1/template-catalogues/tcat_1/refresh":
			_, _ = io.WriteString(w, catalogue)
		case "DELETE /api/teams/team_1/template-catalogues/tcat_1":
			_, _ = io.WriteString(w, `{"ok":true}`)
		case "GET /api/teams/team_1/templates":
			_, _ = io.WriteString(w, `{"items":[{"id":"wiki","name":"Acme Wiki","category":"developer",
				"catalogue":{"id":"tcat_1","name":"Acme"}},{"id":"adminer","name":"Adminer","category":"developer"}]}`)
		default:
			http.Error(w, `{"error":{"code":"resource.not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")

	// The token, piped in the way a script would.
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stdin.WriteString(token + "\n")
	_, _ = stdin.Seek(0, 0)
	was := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = was })

	var out bytes.Buffer
	if err := cmdTemplates(t.Context(), []string{"catalogues", "add", "Acme", "https://git.acme.example/catalogue.yaml",
		"--header", "PRIVATE-TOKEN"}, &out); err != nil {
		t.Fatalf("add: %v", err)
	}
	if added["auth_header_name"] != "PRIVATE-TOKEN" || added["auth_header_value"] != token || added["name"] != "Acme" {
		t.Errorf("the panel was sent %v", added)
	}
	if strings.Contains(out.String(), token) {
		t.Errorf("the token was printed back: %s", out.String())
	}
	if !strings.Contains(out.String(), "floating") || !strings.Contains(out.String(), "cannot be installed") {
		t.Errorf("add did not say which template was refused and why:\n%s", out.String())
	}

	out.Reset()
	if err := cmdTemplates(t.Context(), []string{"catalogues", "--json"}, &out); err != nil {
		t.Fatalf("list: %v", err)
	}
	var listed []catalogueAnswer
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].Templates != 2 {
		t.Errorf("--json printed %s (%v)", out.String(), err)
	}

	for _, action := range []string{"refresh", "remove"} {
		out.Reset()
		if err := cmdTemplates(t.Context(), []string{"catalogues", action, "acme", "--json"}, &out); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if !json.Valid(out.Bytes()) {
			t.Errorf("%s --json printed %s", action, out.String())
		}
	}
	mu.Lock()
	if !contains(asked, "POST /api/teams/team_1/template-catalogues/tcat_1/refresh") ||
		!contains(asked, "DELETE /api/teams/team_1/template-catalogues/tcat_1") {
		t.Errorf("the panel was asked %q", asked)
	}
	mu.Unlock()

	if err := cmdTemplates(t.Context(), []string{"catalogues", "refresh", "globex"}, &out); err == nil ||
		!strings.Contains(err.Error(), "Acme") {
		t.Errorf("an unknown catalogue answered %v", err)
	}

	out.Reset()
	if err := cmdTemplates(t.Context(), []string{"--search", "acme"}, &out); err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if !strings.Contains(out.String(), "Acme Wiki") || strings.Contains(out.String(), "Adminer") {
		t.Errorf("searching for the catalogue printed:\n%s", out.String())
	}
}
