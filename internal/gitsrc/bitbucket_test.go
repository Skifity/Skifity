package gitsrc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/netguard"
)

// Bitbucket's API, answered by a server that speaks its shapes: paginated
// lists with values and an absolute next link, repositories under their
// workspace, statuses and comments under the commit and the pull request. The
// token is checked on every request, so a request sent without it — or with
// the email where the token should be — fails the way Bitbucket would fail it.

const (
	bbToken = "ATATT3xFfGF0-fake-api-token-for-tests"
	bbEmail = "ada@example.test"
)

type fakeBitbucket struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	requests []*http.Request
	bodies   []map[string]any

	// workspaces the token can see, and each one's repositories.
	workspaces map[string]int
	// comments on pull request 42, as Bitbucket lists them.
	comments []map[string]any
	// hooks already on acme/shop.
	hooks []string
	// writeStatus answers every write; 201 when zero.
	writeStatus int
}

func newFakeBitbucket(t *testing.T) *fakeBitbucket {
	f := &fakeBitbucket{t: t, workspaces: map[string]int{"acme": 3, "side": 1}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

// authorized accepts the API token as Basic with its email, or as Bearer.
func authorized(r *http.Request) bool {
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(bbEmail+":"+bbToken))
	auth := r.Header.Get("Authorization")
	return auth == basic || auth == "Bearer "+bbToken
}

func (f *fakeBitbucket) page(w http.ResponseWriter, r *http.Request, values []any) {
	size, _ := strconv.Atoi(r.URL.Query().Get("pagelen"))
	if size <= 0 || size > 100 {
		f.t.Errorf("asked for pages of %q; Bitbucket allows 10 to 100", r.URL.Query().Get("pagelen"))
		size = 10
	}
	start, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if start < 1 {
		start = 1
	}
	from := (start - 1) * size
	to := min(from+size, len(values))
	body := map[string]any{"pagelen": size, "page": start, "values": []any{}}
	if from < len(values) {
		body["values"] = values[from:to]
	}
	if to < len(values) {
		next := *r.URL
		query := next.Query()
		query.Set("page", strconv.Itoa(start+1))
		next.RawQuery = query.Encode()
		body["next"] = f.server.URL + next.RequestURI()
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeBitbucket) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.bodies = append(f.bodies, body)
	f.mu.Unlock()

	if !authorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type": "error", "error": {"message": "Unauthorized"}}`))
		return
	}
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/2.0")
	switch {
	case path == "/user/workspaces":
		var values []any
		for _, slug := range []string{"acme", "side"} {
			values = append(values, map[string]any{"type": "workspace_access", "administrator": slug == "acme",
				"workspace": map[string]any{"type": "workspace_base", "slug": slug, "uuid": "{" + slug + "}"}})
		}
		f.page(w, r, values)

	case strings.HasPrefix(path, "/repositories/") && strings.Count(path, "/") == 2:
		workspace := strings.TrimPrefix(path, "/repositories/")
		count, ok := f.workspaces[workspace]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("pagelen") == "100" && r.URL.Query().Get("sort") != "-updated_on" {
			f.t.Errorf("repositories were not asked for newest first: %s", r.URL.RawQuery)
		}
		var values []any
		for i := range count {
			name := fmt.Sprintf("repo-%d", i)
			if q := r.URL.Query().Get("q"); q != "" && !strings.Contains(q, name) {
				continue
			}
			// The side workspace's one repository is the newest of all.
			updated := time.Date(2026, 9, 1+10-i, 0, 0, 0, 0, time.UTC)
			if workspace == "side" {
				updated = time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
			}
			values = append(values, map[string]any{
				"type": "repository", "full_name": workspace + "/" + name, "is_private": i%2 == 0,
				"updated_on": updated.Format("2006-01-02T15:04:05.000000-07:00"),
				"mainbranch": map[string]any{"type": "branch", "name": "main"},
				"links":      map[string]any{"html": map[string]any{"href": "https://bitbucket.org/" + workspace + "/" + name}},
			})
		}
		f.page(w, r, values)

	case path == "/repositories/acme/shop/refs/branches":
		var values []any
		for i := range 150 {
			values = append(values, map[string]any{"type": "branch", "name": fmt.Sprintf("branch-%03d", i),
				"target": map[string]any{"type": "commit", "hash": bbHead}})
		}
		f.page(w, r, values)

	case path == "/repositories/acme/shop/commit/d3022fc0ca3d":
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "commit", "hash": "d3022fc0ca3d" + strings.Repeat("0", 28)})

	case path == "/repositories/acme/shop/hooks" && r.Method == http.MethodGet:
		var values []any
		for _, url := range f.hooks {
			values = append(values, map[string]any{"uuid": "{hook}", "url": url, "active": true})
		}
		f.page(w, r, values)

	case path == "/repositories/acme/shop/pullrequests/42/comments" && r.Method == http.MethodGet:
		values := make([]any, 0, len(f.comments))
		for _, c := range f.comments {
			values = append(values, c)
		}
		f.page(w, r, values)

	case path == "/repositories/acme/shop":
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "repository", "full_name": "acme/shop",
			"mainbranch": map[string]any{"type": "branch", "name": "trunk"}})

	case strings.HasPrefix(path, "/repositories/acme/shop/src/"):
		f.serveSource(w, r, strings.TrimPrefix(path, "/repositories/acme/shop/src/"))

	case r.Method != http.MethodGet:
		status := f.writeStatus
		if status == 0 {
			status = http.StatusCreated
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// serveSource lists a directory, or answers a file raw, at a ref.
func (f *fakeBitbucket) serveSource(w http.ResponseWriter, r *http.Request, rest string) {
	files := map[string]string{
		"package.json":            `{"dependencies":{"next":"15.0.0"}}`,
		"Dockerfile":              "FROM node:22\nEXPOSE 3000\n",
		"src/app/page.tsx":        "export default function Page() {}",
		"a/b/c/d/deep.txt":        "deep",
		"apps/web/package.json":   `{"dependencies":{"vite":"6"}}`,
		"apps/web/src/main.tsx":   "",
		"apps/api/go.mod":         "module api",
		"apps/web/docker/nginx.c": "",
	}
	ref, file, _ := strings.Cut(rest, "/")
	if ref != "trunk" {
		f.t.Errorf("the tree was read at %q, not the main branch", ref)
	}
	if file == "" || strings.HasSuffix(file, "/") {
		if r.URL.Query().Get("max_depth") == "" {
			f.t.Errorf("a directory was listed without max_depth: %s", r.URL.RawQuery)
		}
		dir := strings.TrimSuffix(file, "/")
		var values []any
		seen := map[string]bool{}
		for name := range files {
			if dir != "" && !strings.HasPrefix(name, dir+"/") {
				continue
			}
			relative := strings.TrimPrefix(name, dir+"/")
			parts := strings.Split(relative, "/")
			// Directories as deep as max_depth=4 goes, then files within it.
			for i := 1; i < len(parts) && i <= 4; i++ {
				directory := strings.TrimPrefix(dir+"/"+strings.Join(parts[:i], "/"), "/")
				if !seen[directory] {
					seen[directory] = true
					values = append(values, map[string]any{"type": "commit_directory", "path": directory})
				}
			}
			if len(parts) <= 4 {
				values = append(values, map[string]any{"type": "commit_file", "path": name})
			}
		}
		f.page(w, r, values)
		return
	}
	content, ok := files[file]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = w.Write([]byte(content))
}

func (f *fakeBitbucket) request(i int) (*http.Request, map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i], f.bodies[i]
}

func (f *fakeBitbucket) writes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for i, r := range f.requests {
		if r.Method != http.MethodGet {
			out = append(out, i)
		}
	}
	return out
}

func TestBitbucketRepositoriesAreListedWorkspaceByWorkspace(t *testing.T) {
	fake := newFakeBitbucket(t)

	// An API token with its email: every workspace it can see, the newest
	// repository first whichever workspace it is in, over Basic auth.
	listing, err := ListRepositories(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken, Email: bbEmail})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(listing.Items) != 4 || listing.Truncated {
		t.Fatalf("%d repositories, truncated %t; want both workspaces' four", len(listing.Items), listing.Truncated)
	}
	first := listing.Items[0]
	if first.FullName != "side/repo-0" || first.URL != "https://bitbucket.org/side/repo-0" || first.DefaultBranch != "main" || !first.Private {
		t.Errorf("the newest repository reads as %+v", first)
	}
	if listing.Items[1].FullName != "acme/repo-0" || listing.Items[3].FullName != "acme/repo-2" {
		t.Errorf("the rest are not newest first: %+v", listing.Items)
	}
	if req, _ := fake.request(0); !strings.HasPrefix(req.Header.Get("Authorization"), "Basic ") {
		t.Errorf("an API token with an email was sent as %q", req.Header.Get("Authorization"))
	}

	// An access token belongs to one workspace, which the connection names:
	// nobody is asked for theirs, and it goes as a bearer token.
	before := len(fake.requests)
	listing, err = ListRepositories(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken, Account: "acme"})
	if err != nil || len(listing.Items) != 3 {
		t.Fatalf("the acme workspace listed %+v, %v", listing.Items, err)
	}
	for _, req := range fake.requests[before:] {
		if req.URL.Path == "/2.0/user/workspaces" {
			t.Error("a connection that names its workspace asked for the user's workspaces")
		}
		if req.Header.Get("Authorization") != "Bearer "+bbToken {
			t.Errorf("a token with no email was sent as %q", req.Header.Get("Authorization"))
		}
	}

	// A search is Bitbucket's own, in its query language.
	listing, err = ListRepositories(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken, Account: "acme", Query: `repo-1"`})
	if err != nil || len(listing.Items) != 1 || listing.Items[0].FullName != "acme/repo-1" {
		t.Fatalf("searching found %+v, %v", listing.Items, err)
	}
	last, _ := fake.request(len(fake.requests) - 1)
	if got := last.URL.Query().Get("q"); got != `name ~ "repo-1"` {
		t.Errorf("the search was sent as %q; a quote in it must not end the string", got)
	}
}

// Pages are followed by Bitbucket's next link until the list ends or
// MaxListed is reached, and a list longer than that says so.
func TestBitbucketListsFollowNextAndStopAtTheCeiling(t *testing.T) {
	fake := newFakeBitbucket(t)
	fake.workspaces["big"] = MaxListed + 20

	listing, err := ListRepositories(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken, Account: "big"})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(listing.Items) != MaxListed || !listing.Truncated {
		t.Fatalf("%d repositories, truncated %t; want %d and truncated", len(listing.Items), listing.Truncated, MaxListed)
	}
	pages := 0
	for _, req := range fake.requests {
		if req.URL.Path == "/2.0/repositories/big" {
			pages++
		}
	}
	if pages != MaxListed/100+1 {
		t.Errorf("read %d pages for %d repositories", pages, MaxListed)
	}

	branches, err := ListBranches(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken}, "acme/shop")
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches.Items) != 150 || branches.Truncated || branches.Items[149].Name != "branch-149" {
		t.Fatalf("%d branches, truncated %t", len(branches.Items), branches.Truncated)
	}
}

// The next link is an address in an answer, and the token goes wherever it
// points. One on another host is not followed.
func TestABitbucketNextLinkToAnotherHostIsNotFollowed(t *testing.T) {
	var elsewhere int
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere++
		_, _ = w.Write([]byte(`{"values": []}`))
	}))
	defer other.Close()
	hostile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"values": []any{map[string]any{"type": "branch", "name": "main"}},
			"next":   other.URL + "/2.0/steal?page=2",
		})
	}))
	defer hostile.Close()

	_, err := ListBranches(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: hostile.URL, Token: bbToken}, "acme/shop")
	if err == nil || !strings.Contains(err.Error(), "not the host it was asked") {
		t.Fatalf("a next link to another host answered %v", err)
	}
	if elsewhere != 0 {
		t.Fatal("the token was sent to the host the next link named")
	}
}

func TestABitbucketTokenIsCheckedBeforeItIsSaved(t *testing.T) {
	fake := newFakeBitbucket(t)
	if err := CheckBitbucket(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken, Email: bbEmail}); err != nil {
		t.Fatalf("a good API token was refused: %v", err)
	}
	if req, _ := fake.request(0); req.URL.Path != "/2.0/user/workspaces" {
		t.Errorf("an API token was checked at %s", req.URL.Path)
	}
	if err := CheckBitbucket(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken, Account: "acme"}); err != nil {
		t.Fatalf("a good access token was refused: %v", err)
	}
	if req, _ := fake.request(1); req.URL.Path != "/2.0/repositories/acme" {
		t.Errorf("a connection naming its workspace was checked at %s", req.URL.Path)
	}

	var answered *HostError
	err := CheckBitbucket(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: "wrong", Email: bbEmail})
	if !errors.As(err, &answered) || answered.Status != http.StatusUnauthorized {
		t.Fatalf("a wrong token answered %v", err)
	}
	// The email is the user name, never where the token goes.
	err = CheckBitbucket(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken, Email: "someone-else@example.test"})
	if !errors.As(err, &answered) || answered.Status != http.StatusUnauthorized {
		t.Fatalf("the token with another account's email answered %v", err)
	}
	err = CheckBitbucket(t.Context(), ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken, Account: "nobody"})
	if !errors.As(err, &answered) || answered.Status != http.StatusNotFound {
		t.Fatalf("a workspace that is not there answered %v", err)
	}
}

func TestABitbucketPullRequestsCommitIsResolved(t *testing.T) {
	fake := newFakeBitbucket(t)
	req := ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken}
	full, err := ResolveBitbucketCommit(t.Context(), req, "acme/shop", "d3022fc0ca3d")
	if err != nil || full != "d3022fc0ca3d"+strings.Repeat("0", 28) || !FullCommit(full) {
		t.Fatalf("resolved to %q, %v", full, err)
	}
	for _, bad := range []string{"", "xyz", "d302", "../../user"} {
		if _, err := ResolveBitbucketCommit(t.Context(), req, "acme/shop", bad); err == nil {
			t.Errorf("%q was looked up", bad)
		}
	}
	if _, err := ResolveBitbucketCommit(t.Context(), req, "acme/../../user", "d3022fc0ca3d"); err == nil {
		t.Error("a repository name with a way out of the path was looked up")
	}
}

func TestABitbucketWebhookIsRegisteredWithItsSecret(t *testing.T) {
	fake := newFakeBitbucket(t)
	hook := HookRequest{RepoURL: "https://bitbucket.org/acme/shop", Kind: "bitbucket", BaseURL: fake.server.URL,
		Token: bbToken, Email: bbEmail, DeliverTo: "https://panel.example.test/api/webhooks/git/gs_1", Secret: "whsec-fake"}

	result := EnsureWebhook(t.Context(), hook)
	if !result.Created {
		t.Fatalf("the hook was not registered: %+v", result)
	}
	writes := fake.writes()
	if len(writes) != 1 {
		t.Fatalf("%d writes", len(writes))
	}
	req, body := fake.request(writes[0])
	if req.URL.Path != "/2.0/repositories/acme/shop/hooks" || req.Method != http.MethodPost {
		t.Fatalf("registered with %s %s", req.Method, req.URL.Path)
	}
	if body["url"] != hook.DeliverTo || body["secret"] != "whsec-fake" || body["active"] != true {
		t.Errorf("the hook was sent as %v", body)
	}
	events := fmt.Sprint(body["events"])
	for _, want := range BitbucketEvents {
		if !strings.Contains(events, want) {
			t.Errorf("the hook does not ask for %s: %s", want, events)
		}
	}

	// One already delivering here is left alone.
	fake.hooks = []string{hook.DeliverTo + "/"}
	if result := EnsureWebhook(t.Context(), hook); !result.AlreadyThere || len(fake.writes()) != 1 {
		t.Fatalf("a second registration answered %+v", result)
	}

	// A token that cannot manage hooks is told to add it by hand.
	fake.hooks = nil
	fake.writeStatus = http.StatusForbidden
	if result := EnsureWebhook(t.Context(), hook); result.Registered() || result.Reason == "" {
		t.Fatalf("a refusal answered %+v", result)
	}
}

func TestABitbucketCommitGetsABuildStatus(t *testing.T) {
	fake := newFakeBitbucket(t)
	status := StatusRequest{RepoURL: "https://bitbucket.org/acme/shop", Kind: "bitbucket", BaseURL: fake.server.URL,
		Token: bbToken, Email: bbEmail, CommitSHA: commit, Ref: "feature/checkout",
		Context: "skifity/production-europe-west/storefront-web", Description: "Building and deploying",
		TargetURL: "https://panel.example.test/apps/app_1/deployments"}

	want := map[State]string{StatePending: "INPROGRESS", StateSuccess: "SUCCESSFUL", StateFailure: "FAILED"}
	keys := map[string]bool{}
	for state, bitbucket := range want {
		status.State = state
		if err := ReportStatus(t.Context(), status); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		writes := fake.writes()
		req, body := fake.request(writes[len(writes)-1])
		if req.URL.Path != "/2.0/repositories/acme/shop/commit/"+commit+"/statuses/build" {
			t.Fatalf("the status went to %s", req.URL.Path)
		}
		if body["state"] != bitbucket || body["name"] != status.Context || body["url"] != status.TargetURL ||
			body["refname"] != "feature/checkout" || body["description"] != "Building and deploying" {
			t.Errorf("%s was sent as %v", state, body)
		}
		key, _ := body["key"].(string)
		if key == "" || len(key) > 40 {
			t.Errorf("the key %q is not one Bitbucket takes: it refuses more than forty characters", key)
		}
		keys[key] = true
	}
	// The same check keeps one line on the commit.
	if len(keys) != 1 {
		t.Errorf("one check was reported under %d keys", len(keys))
	}
	if bitbucketStatusKey("skifity/preview/web") == bitbucketStatusKey("skifity/production/web") {
		t.Error("two checks share a key, so one overwrites the other")
	}

	// A short commit goes into no URL.
	status.CommitSHA = "d3022fc0ca3d"
	if err := ReportStatus(t.Context(), status); err == nil {
		t.Fatal("a status was set on an abbreviated commit")
	}
}

func TestABitbucketPullRequestGetsOneCommentKeptUpToDate(t *testing.T) {
	fake := newFakeBitbucket(t)
	comment := CommentRequest{RepoURL: "https://bitbucket.org/acme/shop", Kind: "bitbucket", BaseURL: fake.server.URL,
		Token: bbToken, PullRequest: 42, Marker: "skifity-preview:app_1",
		Body: PreviewComment{App: "web", State: StateSuccess, URL: "https://web-pr-42.example.test", Commit: commit}.MarkdownFor("bitbucket")}

	// None yet: one is added.
	if err := UpsertComment(t.Context(), comment); err != nil {
		t.Fatalf("UpsertComment: %v", err)
	}
	writes := fake.writes()
	req, body := fake.request(writes[0])
	if req.Method != http.MethodPost || req.URL.Path != "/2.0/repositories/acme/shop/pullrequests/42/comments" {
		t.Fatalf("commented with %s %s", req.Method, req.URL.Path)
	}
	raw := body["content"].(map[string]any)["raw"].(string)
	// Bitbucket escapes HTML and would show it as text, so neither the
	// marker nor the footer may be HTML.
	if strings.Contains(raw, "<!--") || strings.Contains(raw, "<sub>") {
		t.Errorf("the comment has HTML Bitbucket would print as text:\n%s", raw)
	}
	if !strings.HasPrefix(raw, "[//]: # (skifity-preview:app_1)\n") || !strings.Contains(raw, "https://web-pr-42.example.test") {
		t.Errorf("the comment reads:\n%s", raw)
	}

	// Found again by its marker among other people's, and edited in place.
	fake.comments = []map[string]any{
		{"id": 7, "content": map[string]any{"raw": "Looks good to me"}},
		{"id": 9, "deleted": true, "content": map[string]any{"raw": "[//]: # (skifity-preview:app_1)\n\nold and deleted"}},
		{"id": 11, "content": map[string]any{"raw": "[//]: # (skifity-preview:app_1)\n\nBuilding…"}},
	}
	if err := UpsertComment(t.Context(), comment); err != nil {
		t.Fatalf("UpsertComment: %v", err)
	}
	writes = fake.writes()
	req, _ = fake.request(writes[len(writes)-1])
	if req.Method != http.MethodPut || req.URL.Path != "/2.0/repositories/acme/shop/pullrequests/42/comments/11" {
		t.Fatalf("the second push wrote %s %s", req.Method, req.URL.Path)
	}

	// Already saying this: nothing is written.
	fake.comments[2]["content"] = map[string]any{"raw": raw}
	count := len(fake.writes())
	if err := UpsertComment(t.Context(), comment); err != nil || len(fake.writes()) != count {
		t.Fatalf("an unchanged comment was written again: %v", err)
	}
}

func TestABitbucketRepositoryIsReadWithoutCloning(t *testing.T) {
	fake := newFakeBitbucket(t)
	// On the fake's own host, as a bitbucket.org repository is on a
	// Bitbucket connection's: the token only goes to the host it is for.
	repoURL := fake.server.URL + "/acme/shop"
	tree, err := ReadTree(t.Context(), TreeRequest{RepoURL: repoURL, Kind: "bitbucket",
		BaseURL: fake.server.URL, Token: bbToken, Email: bbEmail})
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	if !contains(tree.Files, "package.json") || !contains(tree.Files, "src/app/page.tsx") {
		t.Fatalf("the tree is %v", tree.Files)
	}
	if contains(tree.Files, "a/b/c/d/deep.txt") || !tree.Truncated {
		t.Errorf("a file deeper than the listing goes was listed, or the listing did not say it stopped: %v, %t",
			tree.Files, tree.Truncated)
	}
	if tree.Contents["package.json"] != `{"dependencies":{"next":"15.0.0"}}` || tree.Contents["Dockerfile"] == "" {
		t.Errorf("the files detection reads were read as %v", tree.Contents)
	}

	// A monorepo's app reads its own directory, as though it were the root.
	tree, err = ReadTree(t.Context(), TreeRequest{RepoURL: repoURL, Kind: "bitbucket",
		BaseURL: fake.server.URL, Token: bbToken, RootDir: "apps/web"})
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	if !contains(tree.Files, "package.json") || contains(tree.Files, "go.mod") {
		t.Fatalf("apps/web reads as %v", tree.Files)
	}
	if tree.Contents["package.json"] != `{"dependencies":{"vite":"6"}}` {
		t.Errorf("apps/web/package.json read as %q", tree.Contents["package.json"])
	}
}

func TestBitbucketIsWhereItsAddressSays(t *testing.T) {
	if KindFor("https://bitbucket.org/acme/shop") != "bitbucket" {
		t.Error("a bitbucket.org repository is not Bitbucket's")
	}
	if KindFor("https://bitbucket.example.test/scm/acme/shop") == "bitbucket" {
		t.Error("a Bitbucket Data Center was taken for Bitbucket Cloud, whose API it does not speak")
	}
	if !SameHost("https://bitbucket.org/acme/shop", BitbucketURL) || SameHost("https://evil.example.test/acme/shop", BitbucketURL) {
		t.Error("a Bitbucket connection's token would go to the wrong host")
	}
	if CloneUsername("bitbucket") != "x-token-auth" || CloneUsername("github_pat") != "x-access-token" {
		t.Error("the build would present the wrong user name")
	}
	if !CanList("bitbucket") || !ValidRepoName("bitbucket", "acme/shop") || ValidRepoName("bitbucket", "acme/group/shop") {
		t.Error("a Bitbucket repository's name is workspace/slug")
	}
	for _, workspace := range []string{"acme", "acme-corp", "a_b.c"} {
		if !ValidBitbucketWorkspace(workspace) {
			t.Errorf("%q was refused", workspace)
		}
	}
	for _, workspace := range []string{"", "../x", "a/b", "a b", "-x", strings.Repeat("a", 101)} {
		if ValidBitbucketWorkspace(workspace) {
			t.Errorf("%q was taken as a workspace", workspace)
		}
	}
}

// Everything Bitbucket is asked goes through the guarded client, which
// refuses the panel's own machine and the cloud metadata address.
func TestBitbucketRequestsGoThroughTheGuard(t *testing.T) {
	fake := newFakeBitbucket(t)
	previous := client
	client = netguard.Client(5 * time.Second)
	t.Cleanup(func() { client = previous })

	req := ListRequest{Kind: "bitbucket", BaseURL: fake.server.URL, Token: bbToken, Email: bbEmail}
	calls := map[string]error{}
	_, calls["listing"] = ListRepositories(t.Context(), req)
	_, calls["branches"] = ListBranches(t.Context(), req, "acme/shop")
	calls["check"] = CheckBitbucket(t.Context(), req)
	_, calls["commit"] = ResolveBitbucketCommit(t.Context(), req, "acme/shop", "d3022fc0ca3d")
	calls["status"] = ReportStatus(t.Context(), StatusRequest{RepoURL: "https://bitbucket.org/acme/shop", Kind: "bitbucket",
		BaseURL: fake.server.URL, Token: bbToken, CommitSHA: commit, State: StateSuccess, Context: "skifity/x"})
	calls["comment"] = UpsertComment(t.Context(), CommentRequest{RepoURL: "https://bitbucket.org/acme/shop", Kind: "bitbucket",
		BaseURL: fake.server.URL, Token: bbToken, PullRequest: 42, Marker: "m", Body: "b"})
	_, calls["tree"] = ReadTree(t.Context(), TreeRequest{RepoURL: "https://bitbucket.org/acme/shop", Kind: "bitbucket",
		BaseURL: fake.server.URL, Token: bbToken})
	for name, err := range calls {
		var blocked *netguard.Blocked
		if !errors.As(err, &blocked) {
			t.Errorf("%s reached %s: %v", name, fake.server.URL, err)
		}
	}
	if hook := EnsureWebhook(t.Context(), HookRequest{RepoURL: "https://bitbucket.org/acme/shop", Kind: "bitbucket",
		BaseURL: fake.server.URL, Token: bbToken, DeliverTo: "https://panel.example.test/h", Secret: "s"}); hook.Registered() ||
		!strings.Contains(hook.Reason, "refusing to connect") {
		t.Errorf("registering a hook reached the panel's own machine: %+v", hook)
	}
	if len(fake.requests) != 0 {
		t.Fatalf("the guarded client let %d requests through to loopback", len(fake.requests))
	}
}
