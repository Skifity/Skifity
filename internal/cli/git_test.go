package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// `skifity git` names a connection the way the panel lists it, and asks the
// panel — never the Git host — with that connection's id.
func TestAGitConnectionIsNamedAsItIsListed(t *testing.T) {
	var asked []string
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		asked = append(asked, r.URL.RequestURI())
		switch r.URL.Path {
		case "/api/me":
			_, _ = io.WriteString(w, `{"teams":[{"id":"team_1","name":"acme"}]}`)
		case "/api/teams/team_1/git-sources":
			_, _ = io.WriteString(w, `{"items":[{"id":"src_1","name":"Acme GitHub","kind":"github_pat"}]}`)
		case "/api/teams/team_1/git-sources/src_1/repositories":
			_, _ = io.WriteString(w, `{"items":[{"full_name":"acme/shop","url":"https://github.com/acme/shop",
				"default_branch":"main","private":true}],"truncated":true}`)
		case "/api/teams/team_1/git-sources/src_1/branches":
			_, _ = io.WriteString(w, `{"items":[{"name":"main"},{"name":"release/1.x"}],"truncated":false}`)
		default:
			http.Error(w, `{"error":{"code":"resource.not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")

	var out bytes.Buffer
	if err := cmdGit(t.Context(), []string{"repos", "acme github", "--search", "shop"}, &out); err != nil {
		t.Fatalf("repos: %v", err)
	}
	if !strings.Contains(out.String(), "acme/shop") || !strings.Contains(out.String(), "private") ||
		!strings.Contains(out.String(), "--search") {
		t.Errorf("repos printed:\n%s", out.String())
	}
	if last := asked[len(asked)-1]; last != "/api/teams/team_1/git-sources/src_1/repositories?q=shop" {
		t.Errorf("the panel was asked %s", last)
	}

	out.Reset()
	if err := cmdGit(t.Context(), []string{"branches", "src_1", "acme/shop"}, &out); err != nil {
		t.Fatalf("branches: %v", err)
	}
	if out.String() != "main\nrelease/1.x\n" {
		t.Errorf("branches printed %q", out.String())
	}
	if last := asked[len(asked)-1]; !strings.HasPrefix(last, "/api/teams/team_1/git-sources/src_1/branches?repo=acme%2Fshop") {
		t.Errorf("the panel was asked %s", last)
	}

	err := cmdGit(t.Context(), []string{"repos", "gitlab"}, &out)
	if err == nil || !strings.Contains(err.Error(), `"gitlab"`) || !strings.Contains(err.Error(), "Acme GitHub") {
		t.Errorf("an unknown connection answered %v", err)
	}
}
