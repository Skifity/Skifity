package gitsrc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// Listing what a connection can read runs against a server speaking each
// provider's API, so the paths, the paging and the way the token is sent are
// what a real host would see.

func TestRepositoriesAreListedFromEachHost(t *testing.T) {
	var sawAuth, sawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth, sawQuery = r.Header.Get("Authorization"), r.URL.RawQuery
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		var items []map[string]any
		switch r.URL.Path {
		case "/api/v3/user/repos": // GitHub Enterprise
			// A full page and then a short one: the walk has to ask twice.
			count := 100
			if page == 2 {
				count = 3
			}
			for i := range count {
				name := fmt.Sprintf("acme/app-%d-%d", page, i)
				if page == 2 && i == 0 {
					name = "acme/Shop"
				}
				items = append(items, map[string]any{"full_name": name, "html_url": "https://github.example.test/" + name,
					"default_branch": "main", "private": true})
			}
		case "/api/v4/projects": // GitLab
			items = append(items, map[string]any{"path_with_namespace": "group/sub/shop",
				"web_url": "https://gitlab.example.test/group/sub/shop", "default_branch": "trunk", "visibility": "public"})
		case "/api/v1/user/repos": // Gitea
			items = append(items, map[string]any{"full_name": "acme/shop", "html_url": "https://gitea.example.test/acme/shop",
				"default_branch": "main", "private": false})
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(items)
	}))
	defer server.Close()

	listing, err := ListRepositories(t.Context(), ListRequest{Kind: "github_pat", BaseURL: server.URL, Token: "ghp_fake"})
	if err != nil {
		t.Fatalf("GitHub: %v", err)
	}
	if len(listing.Items) != 103 || listing.Truncated {
		t.Fatalf("GitHub: %d repositories, truncated %t; want both pages, 103, and nothing more", len(listing.Items), listing.Truncated)
	}
	if sawAuth != "token ghp_fake" || !strings.Contains(sawQuery, "per_page=100") || !strings.Contains(sawQuery, "sort=updated") {
		t.Errorf("GitHub was asked with %q, %q", sawAuth, sawQuery)
	}
	first := listing.Items[100]
	if first.FullName != "acme/Shop" || first.URL != "https://github.example.test/acme/Shop" || first.DefaultBranch != "main" || !first.Private {
		t.Errorf("GitHub: a repository reads as %+v", first)
	}

	// GitHub is narrowed here, ignoring case.
	listing, err = ListRepositories(t.Context(), ListRequest{Kind: "github_pat", BaseURL: server.URL, Token: "ghp_fake", Query: "shop"})
	if err != nil || len(listing.Items) != 1 || listing.Items[0].FullName != "acme/Shop" {
		t.Fatalf("GitHub searched for shop and found %+v, %v", listing.Items, err)
	}

	// GitLab searches on its own side, and takes the token as a bearer.
	listing, err = ListRepositories(t.Context(), ListRequest{Kind: "gitlab", BaseURL: server.URL, Token: "glpat-fake", Query: "shop"})
	if err != nil {
		t.Fatalf("GitLab: %v", err)
	}
	if sawAuth != "Bearer glpat-fake" || !strings.Contains(sawQuery, "search=shop") || !strings.Contains(sawQuery, "membership=true") {
		t.Errorf("GitLab was asked with %q, %q", sawAuth, sawQuery)
	}
	if len(listing.Items) != 1 || listing.Items[0].FullName != "group/sub/shop" || listing.Items[0].Private ||
		listing.Items[0].DefaultBranch != "trunk" {
		t.Errorf("GitLab: %+v", listing.Items)
	}

	// Gitea pages by limit, fifty at most.
	listing, err = ListRepositories(t.Context(), ListRequest{Kind: "gitea", BaseURL: server.URL, Token: "gitea-fake"})
	if err != nil {
		t.Fatalf("Gitea: %v", err)
	}
	if sawAuth != "token gitea-fake" || !strings.Contains(sawQuery, "limit=50") {
		t.Errorf("Gitea was asked with %q, %q", sawAuth, sawQuery)
	}
	if len(listing.Items) != 1 || listing.Items[0].URL != "https://gitea.example.test/acme/shop" {
		t.Errorf("Gitea: %+v", listing.Items)
	}
}

// Somebody in a large organisation can read thousands of repositories. The
// walk stops at the ceiling and says it did, rather than asking page after
// page while a form waits.
func TestAListingStopsAtTheCeilingAndSaysSo(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		items := make([]map[string]any, 100)
		for i := range items {
			items[i] = map[string]any{"name": fmt.Sprintf("feature-%s-%d", r.URL.Query().Get("page"), i)}
		}
		_ = json.NewEncoder(w).Encode(items)
	}))
	defer server.Close()

	listing, err := ListBranches(t.Context(), ListRequest{Kind: "github_pat", BaseURL: server.URL, Token: "ghp_fake"}, "acme/shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != MaxListed || !listing.Truncated {
		t.Fatalf("%d branches, truncated %t; want %d and truncated", len(listing.Items), listing.Truncated, MaxListed)
	}
	if n := requests.Load(); n != MaxListed/100 {
		t.Errorf("%d pages were asked for, want %d", n, MaxListed/100)
	}
}

func TestBranchesAreAskedForByTheRepositorysPath(t *testing.T) {
	var sawPath, sawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath, sawQuery = r.URL.EscapedPath(), r.URL.RawQuery
		_, _ = w.Write([]byte(`[{"name": "main", "commit": {"id": "abc"}}, {"name": "release/1.x"}]`))
	}))
	defer server.Close()

	listing, err := ListBranches(t.Context(), ListRequest{Kind: "github_pat", BaseURL: server.URL, Token: "ghp_fake"}, "acme/shop")
	if err != nil {
		t.Fatalf("GitHub: %v", err)
	}
	if sawPath != "/api/v3/repos/acme/shop/branches" || len(listing.Items) != 2 || listing.Items[1].Name != "release/1.x" {
		t.Errorf("GitHub: asked %s and read %+v", sawPath, listing.Items)
	}

	// A project in a subgroup is named by its whole path, escaped as one.
	if _, err := ListBranches(t.Context(), ListRequest{Kind: "gitlab", BaseURL: server.URL, Query: "rel"}, "group/sub/shop"); err != nil {
		t.Fatalf("GitLab: %v", err)
	}
	if sawPath != "/api/v4/projects/group%2Fsub%2Fshop/repository/branches" || !strings.Contains(sawQuery, "search=rel") {
		t.Errorf("GitLab: asked %s?%s", sawPath, sawQuery)
	}

	if _, err := ListBranches(t.Context(), ListRequest{Kind: "gitea", BaseURL: server.URL}, "acme/shop"); err != nil {
		t.Fatalf("Gitea: %v", err)
	}
	if sawPath != "/api/v1/repos/acme/shop/branches" || !strings.Contains(sawQuery, "limit=50") {
		t.Errorf("Gitea: asked %s?%s", sawPath, sawQuery)
	}
}

// The name becomes part of a request made with the team's token, so anything
// that could walk out of the path it is meant for is refused before it is sent.
func TestARepositoryNameIsCheckedBeforeItIsAsked(t *testing.T) {
	for _, tc := range []struct {
		kind, name string
		want       bool
	}{
		{"github_pat", "acme/shop", true},
		{"gitea", "acme/shop.web", true},
		{"gitlab", "group/sub/shop", true},
		{"github_pat", "group/sub/shop", false},
		{"github_pat", "acme", false},
		{"github_pat", "acme/..", false},
		{"gitlab", "group/../../admin", false},
		{"github_pat", "acme/shop?per_page=1", false},
		{"github_pat", "acme/shop%2F..", false},
		{"github_pat", "/acme/shop", false},
		{"github_pat", "", false},
	} {
		if got := ValidRepoName(tc.kind, tc.name); got != tc.want {
			t.Errorf("ValidRepoName(%s, %q) = %t, want %t", tc.kind, tc.name, got, tc.want)
		}
	}

	if _, err := ListBranches(t.Context(), ListRequest{Kind: "github_pat", BaseURL: "https://github.example.test"}, "acme/.."); err == nil {
		t.Error("a name leaving the repository's path was asked for")
	}
}

func TestAConnectionWithNoAPIIsNotAsked(t *testing.T) {
	if _, err := ListRepositories(t.Context(), ListRequest{Kind: "generic"}); !errors.Is(err, ErrListingUnsupported) {
		t.Errorf("a plain Git connection answered %v", err)
	}
	if _, err := ListBranches(t.Context(), ListRequest{Kind: "generic"}, "acme/shop"); !errors.Is(err, ErrListingUnsupported) {
		t.Errorf("a plain Git connection answered %v", err)
	}
}

func TestARefusedTokenSaysSo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	_, err := ListRepositories(t.Context(), ListRequest{Kind: "gitea", BaseURL: server.URL, Token: "expired"})
	if err == nil || !strings.Contains(err.Error(), "refused the connection's token") {
		t.Fatalf("a refused token answered %v", err)
	}
}
