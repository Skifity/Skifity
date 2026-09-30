package gitsrc

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"skifity/internal/builder"
)

// These run against a real HTTP server speaking each provider's API, so the
// requests are built, sent and parsed for real.

func TestReadingAGitHubRepository(t *testing.T) {
	var sawAuth, sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		switch {
		case strings.Contains(r.URL.Path, "/git/trees/"):
			sawPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tree": []map[string]any{
					{"path": "package.json", "type": "blob"},
					{"path": "src", "type": "tree"},
					{"path": "src/index.ts", "type": "blob"},
				},
				"truncated": false,
			})
		case strings.Contains(r.URL.Path, "/contents/package.json"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"encoding": "base64",
				// Wrapped at 60 columns, the way GitHub sends it, which the
				// strict decoder refuses without the newlines stripped.
				"content": wrap(base64.StdEncoding.EncodeToString(
					[]byte(`{"dependencies":{"next":"15.0.0"}}`))),
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// The repository and the connection are on the same host, which is what a
	// self-hosted provider looks like and what lets the token be sent.
	tree, err := ReadTree(t.Context(), TreeRequest{
		RepoURL: server.URL + "/acme/shop",
		Kind:    "github",
		BaseURL: server.URL,
		Token:   "ghp_secret",
	})
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}

	// Directories are not files, and the detector asks about files.
	if len(tree.Files) != 2 {
		t.Fatalf("files are %v, want the two blobs only", tree.Files)
	}
	if tree.Contents["package.json"] == "" {
		t.Fatal("package.json was not read, so a framework could never be named")
	}
	if !strings.Contains(tree.Contents["package.json"], "next") {
		t.Errorf("package.json decoded to %q", tree.Contents["package.json"])
	}
	if sawAuth != "token ghp_secret" {
		t.Errorf("the token was sent as %q", sawAuth)
	}
	if !strings.Contains(sawPath, "/repos/acme/shop/git/trees/") {
		t.Errorf("the tree was asked for at %q", sawPath)
	}
}

func TestATokenIsNeverSentToAnotherHost(t *testing.T) {
	// The same bug the build had: a repository address somebody types into a
	// form decides where a credential goes. A connection for one host must not
	// authenticate against another.
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"tree": []any{}})
	}))
	defer server.Close()

	_, err := ReadTree(t.Context(), TreeRequest{
		// The connection is for github.com; the repository is not.
		RepoURL: "https://evil.example.test/acme/shop",
		Kind:    "github",
		BaseURL: server.URL,
		Token:   "ghp_secret",
	})
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	if sawAuth != "" {
		t.Fatalf("a token was sent to a host the connection is not for: %q", sawAuth)
	}
}

func TestAMonorepoIsNarrowedToItsSubdirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/trees/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": []map[string]any{
				{"path": "README.md", "type": "blob"},
				{"path": "apps/web/package.json", "type": "blob"},
				{"path": "apps/api/go.mod", "type": "blob"},
			}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tree, err := ReadTree(t.Context(), TreeRequest{
		RepoURL: server.URL + "/acme/shop",
		Kind:    "github", BaseURL: server.URL, RootDir: "apps/api",
	})
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	// The detector must see that directory as though it were the repository,
	// or every monorepo is detected as whatever is at the root.
	if len(tree.Files) != 1 || tree.Files[0] != "go.mod" {
		t.Fatalf("files are %v, want go.mod at the top", tree.Files)
	}
}

func TestReadingAGitLabRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/repository/tree"):
			if r.Header.Get("Authorization") != "Bearer glpat_secret" {
				t.Errorf("GitLab was not sent a bearer token: %q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"path": "requirements.txt", "type": "blob"},
				{"path": "app", "type": "tree"},
			})
		case strings.Contains(r.URL.Path, "/repository/files/"):
			_, _ = w.Write([]byte("Django==5.0\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tree, err := ReadTree(t.Context(), TreeRequest{
		RepoURL: server.URL + "/acme/shop",
		Kind:    "gitlab", BaseURL: server.URL, Token: "glpat_secret",
	})
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	if len(tree.Files) != 1 || tree.Files[0] != "requirements.txt" {
		t.Fatalf("files are %v", tree.Files)
	}
	// GitLab returns a raw file rather than a base64 envelope.
	if !strings.Contains(tree.Contents["requirements.txt"], "Django") {
		t.Errorf("the file was not read: %q", tree.Contents["requirements.txt"])
	}
}

func TestAMissingRepositorySaysSo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := ReadTree(t.Context(), TreeRequest{
		RepoURL: server.URL + "/acme/nope",
		Kind:    "github", BaseURL: server.URL,
	})
	if err == nil {
		t.Fatal("a repository that does not exist read successfully")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("the error does not say what happened: %v", err)
	}

	// And a refusal is told apart from an absence, because the fix is
	// different: one is a typo, the other is a missing connection.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer refusing.Close()
	_, err = ReadTree(t.Context(), TreeRequest{
		RepoURL: refusing.URL + "/acme/private",
		Kind:    "github", BaseURL: refusing.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "connect an account") {
		t.Errorf("a refusal does not suggest connecting an account: %v", err)
	}
}

func TestAnAddressThatNamesNoRepositoryIsRefused(t *testing.T) {
	for _, bad := range []string{"https://github.com", "https://github.com/acme", "not a url at all"} {
		if _, err := ReadTree(t.Context(), TreeRequest{RepoURL: bad}); err == nil {
			t.Errorf("%q was accepted as a repository", bad)
		}
	}
}

func TestKindFor(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/shop":          "github",
		"https://gitlab.com/acme/shop":          "gitlab",
		"https://gitlab.example.test/acme/shop": "gitlab",
		"https://codeberg.org/acme/shop":        "gitea",
		"https://git.example.test/acme/shop":    "github",
	}
	for url, want := range cases {
		if got := KindFor(url); got != want {
			t.Errorf("KindFor(%q) = %q, want %q", url, got, want)
		}
	}
}

// wrap breaks a base64 string at 60 columns, the way GitHub sends it.
func wrap(s string) string {
	var b strings.Builder
	for len(s) > 60 {
		b.WriteString(s[:60])
		b.WriteString("\n")
		s = s[60:]
	}
	b.WriteString(s)
	return b.String()
}

// TestMain lets these tests reach an httptest server.
//
// The shipped client refuses loopback on purpose — see internal/netguard,
// which is where that behaviour is tested. Every server in this file listens on
// 127.0.0.1, so without this the whole package would be testing the guard
// rather than the protocol handling it is about.
func TestMain(m *testing.M) {
	client = &http.Client{Timeout: 5 * time.Second}
	os.Exit(m.Run())
}

// TestTheFilesTheAnswerDependsOnAreRead: the detector reads a Dockerfile for
// its EXPOSE line and a Compose file for its services, and neither was in the
// list of files fetched. Both were found in the tree and then read back as an
// empty string, so the port was never detected and the services never appeared.
func TestTheFilesTheAnswerDependsOnAreRead(t *testing.T) {
	bodies := map[string]string{
		"Dockerfile":         "FROM node:22\nEXPOSE 4000\n",
		"docker-compose.yml": "services:\n  web:\n    build: .\n",
		"package.json":       `{"dependencies":{"express":"4"}}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/trees/") {
			var entries []map[string]any
			for name := range bodies {
				entries = append(entries, map[string]any{"path": name, "type": "blob"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": entries})
			return
		}
		for name, body := range bodies {
			if strings.HasSuffix(r.URL.Path, "/contents/"+name) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"encoding": "base64",
					"content":  wrap(base64.StdEncoding.EncodeToString([]byte(body))),
				})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tree, err := ReadTree(t.Context(), TreeRequest{RepoURL: server.URL + "/acme/shop", Kind: "github", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	for name, body := range bodies {
		if tree.Contents[name] != body {
			t.Errorf("%s was listed but read as %q", name, tree.Contents[name])
		}
	}
}

// The file with the real secrets is never fetched.
//
// Detection reads .env.example to learn the names of the settings an app
// expects. .env beside it is where the values are — somebody's API keys, a
// database password — and a panel that fetched it to find out the names would
// be copying secrets out of a repository nobody meant to publish them from. It
// is usually ignored by git, which is exactly why a repository that commits it
// by mistake must not have it read.
func TestTheRealEnvFileIsNeverFetched(t *testing.T) {
	allowed := map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true}
	for _, file := range readableFiles {
		base := file[strings.LastIndex(file, "/")+1:]
		if !strings.HasPrefix(base, ".env") {
			continue
		}
		if !allowed[base] {
			t.Errorf("%q is fetched for detection; only a template of the settings may be", file)
		}
	}
	// And the templates detection reads are the ones fetched, so a name added
	// on one side is not silently missing from the other.
	for _, file := range builder.EnvExampleFiles {
		found := false
		for _, fetched := range readableFiles {
			if fetched == file {
				found = true
			}
		}
		if !found {
			t.Errorf("detection reads %q and it is never fetched", file)
		}
	}
}
