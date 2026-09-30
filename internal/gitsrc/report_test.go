package gitsrc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Reporting a deployment back to the Git host: a status on the commit, and one
// comment on the pull request that is edited rather than repeated. These run
// against a fake host that records what it was sent, one per provider, because
// the three spell the same thing three different ways.

const commit = "0123456789abcdef0123456789abcdef01234567"

type recorded struct {
	Method string
	Path   string
	Auth   string
	Body   map[string]any
}

// fakeHost answers like a Git host and remembers every write.
type fakeHost struct {
	mu       sync.Mutex
	writes   []recorded
	comments string // the JSON list returned for a comment listing
	status   int    // the status every write answers with; 201 when zero
	answer   string
}

func (f *fakeHost) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if f.comments == "" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(f.comments))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.writes = append(f.writes, recorded{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: body})
		f.mu.Unlock()
		status := f.status
		if status == 0 {
			status = http.StatusCreated
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(f.answer))
	}))
	t.Cleanup(server.Close)
	return server
}

func (f *fakeHost) only(t *testing.T) recorded {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 1 {
		t.Fatalf("expected exactly one write to the host, got %d: %+v", len(f.writes), f.writes)
	}
	return f.writes[0]
}

func TestACommitStatusIsSetOnEachHostInItsOwnWords(t *testing.T) {
	cases := []struct {
		kind      string
		wantPath  string
		wantAuth  string
		stateKey  string
		wantState string
		nameKey   string
	}{
		{"github", "/api/v3/repos/acme/site/statuses/" + commit, "token a-token", "state", "failure", "context"},
		{"gitea", "/api/v1/repos/acme/site/statuses/" + commit, "token a-token", "state", "failure", "context"},
		// GitLab calls a failure "failed" and the check's name "name".
		{"gitlab", "/api/v4/projects/acme%2Fsite/statuses/" + commit, "Bearer a-token", "state", "failed", "name"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			host := &fakeHost{}
			server := host.server(t)
			err := ReportStatus(t.Context(), StatusRequest{
				RepoURL: "https://git.example.test/acme/site", Kind: tc.kind, BaseURL: server.URL,
				Token: "a-token", CommitSHA: commit, State: StateFailure,
				Context: "skifity/production/web", Description: "The build failed",
				TargetURL: "https://panel.example.test/apps/app_1/deployments",
			})
			if err != nil {
				t.Fatalf("the status was not set: %v", err)
			}
			write := host.only(t)
			if write.Method != http.MethodPost {
				t.Errorf("the status was sent with %s", write.Method)
			}
			// The escaped slash is what GitLab needs to find the project, and
			// RawPath is where Go keeps it.
			if got := write.Path; got != strings.ReplaceAll(tc.wantPath, "%2F", "/") {
				t.Errorf("the status went to %s, want %s", got, tc.wantPath)
			}
			if write.Auth != tc.wantAuth {
				t.Errorf("the host was sent %q as authorization", write.Auth)
			}
			if write.Body[tc.stateKey] != tc.wantState {
				t.Errorf("the state was %v, want %s", write.Body[tc.stateKey], tc.wantState)
			}
			if write.Body[tc.nameKey] != "skifity/production/web" {
				t.Errorf("the check was named %v", write.Body[tc.nameKey])
			}
			if write.Body["target_url"] != "https://panel.example.test/apps/app_1/deployments" {
				t.Errorf("the status links to %v", write.Body["target_url"])
			}
		})
	}
}

// GitLab answers 400 when a status is set to the state it is already in. That
// is the commit saying what was wanted, not a failure to report.
func TestGitLabRepeatingAStateIsNotAnError(t *testing.T) {
	host := &fakeHost{status: http.StatusBadRequest,
		answer: `{"message":"Cannot transition status via :run from :running"}`}
	server := host.server(t)
	err := ReportStatus(t.Context(), StatusRequest{
		RepoURL: "https://gitlab.example.test/acme/site", Kind: "gitlab", BaseURL: server.URL,
		Token: "a-token", CommitSHA: commit, State: StatePending, Context: "skifity/web",
	})
	if err != nil {
		t.Fatalf("a repeated state came back as an error: %v", err)
	}
}

// A token that can clone but not write statuses is normal. It has to come back
// as something a person can act on.
func TestATokenThatCannotWriteSaysSo(t *testing.T) {
	host := &fakeHost{status: http.StatusForbidden, answer: `{"message":"Resource not accessible by integration"}`}
	server := host.server(t)
	err := ReportStatus(t.Context(), StatusRequest{
		RepoURL: "https://github.example.test/acme/site", Kind: "github", BaseURL: server.URL,
		Token: "read-only", CommitSHA: commit, State: StateSuccess, Context: "skifity/web",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot write commit statuses") {
		t.Fatalf("a refused status came back as %v", err)
	}
}

// The commit goes into a URL path. Anything that is not a full hash is refused
// before a request is made.
func TestOnlyAFullCommitHashIsSent(t *testing.T) {
	host := &fakeHost{}
	server := host.server(t)
	for _, sha := range []string{"", "abc1234", "../../hooks", strings.Repeat("g", 40), strings.ToUpper(commit)} {
		err := ReportStatus(t.Context(), StatusRequest{
			RepoURL: "https://github.example.test/acme/site", Kind: "github", BaseURL: server.URL,
			Token: "a-token", CommitSHA: sha, State: StateSuccess,
		})
		if err == nil {
			t.Errorf("%q was accepted as a commit", sha)
		}
	}
	if len(host.writes) != 0 {
		t.Fatalf("a request was made for an invalid commit: %+v", host.writes)
	}
}

func TestNoTokenMeansNoRequest(t *testing.T) {
	host := &fakeHost{}
	server := host.server(t)
	if err := ReportStatus(t.Context(), StatusRequest{
		RepoURL: "https://github.example.test/acme/site", Kind: "github", BaseURL: server.URL,
		CommitSHA: commit, State: StateSuccess,
	}); err == nil {
		t.Fatal("a status with no token was reported as sent")
	}
	if len(host.writes) != 0 {
		t.Fatal("a request was made with no token")
	}
}

func TestTheDescriptionFitsTheHostsLimit(t *testing.T) {
	host := &fakeHost{}
	server := host.server(t)
	err := ReportStatus(t.Context(), StatusRequest{
		RepoURL: "https://github.example.test/acme/site", Kind: "github", BaseURL: server.URL,
		Token: "a-token", CommitSHA: commit, State: StateFailure, Context: "skifity/web",
		Description: strings.Repeat("a long reason ", 30),
	})
	if err != nil {
		t.Fatal(err)
	}
	description, _ := host.only(t).Body["description"].(string)
	if n := len([]rune(description)); n > 140 {
		t.Fatalf("the description is %d characters; GitHub refuses more than 140", n)
	}
}

// The first push to a pull request adds a comment.
func TestThePreviewCommentIsAddedOnce(t *testing.T) {
	cases := []struct {
		kind, wantPath string
	}{
		{"github", "/api/v3/repos/acme/site/issues/12/comments"},
		{"gitea", "/api/v1/repos/acme/site/issues/12/comments"},
		{"gitlab", "/api/v4/projects/acme/site/merge_requests/12/notes"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			host := &fakeHost{comments: `[{"id":1,"body":"Looks good to me"}]`}
			server := host.server(t)
			err := UpsertComment(t.Context(), CommentRequest{
				RepoURL: "https://git.example.test/acme/site", Kind: tc.kind, BaseURL: server.URL,
				Token: "a-token", PullRequest: 12, Marker: "skifity-preview:app_9", Body: "the preview",
			})
			if err != nil {
				t.Fatalf("the comment was not written: %v", err)
			}
			write := host.only(t)
			if write.Method != http.MethodPost || write.Path != tc.wantPath {
				t.Errorf("the comment was sent as %s %s, want POST %s", write.Method, write.Path, tc.wantPath)
			}
			body, _ := write.Body["body"].(string)
			if !strings.HasPrefix(body, "<!-- skifity-preview:app_9 -->") || !strings.Contains(body, "the preview") {
				t.Errorf("the comment does not carry its marker and body: %q", body)
			}
		})
	}
}

// Every later push edits the same comment. A comment per push is the reason
// people mute deployment bots.
func TestThePreviewCommentIsEditedNotRepeated(t *testing.T) {
	cases := []struct {
		kind, wantMethod, wantPath string
	}{
		{"github", http.MethodPatch, "/api/v3/repos/acme/site/issues/comments/77"},
		{"gitea", http.MethodPatch, "/api/v1/repos/acme/site/issues/comments/77"},
		{"gitlab", http.MethodPut, "/api/v4/projects/acme/site/merge_requests/12/notes/77"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			host := &fakeHost{comments: `[
				{"id":5,"body":"First!"},
				{"id":77,"body":"<!-- skifity-preview:app_9 -->\nthe old preview"}
			]`}
			server := host.server(t)
			err := UpsertComment(t.Context(), CommentRequest{
				RepoURL: "https://git.example.test/acme/site", Kind: tc.kind, BaseURL: server.URL,
				Token: "a-token", PullRequest: 12, Marker: "skifity-preview:app_9", Body: "the new preview",
			})
			if err != nil {
				t.Fatal(err)
			}
			write := host.only(t)
			if write.Method != tc.wantMethod || write.Path != tc.wantPath {
				t.Errorf("the comment was sent as %s %s, want %s %s",
					write.Method, write.Path, tc.wantMethod, tc.wantPath)
			}
			if body, _ := write.Body["body"].(string); !strings.Contains(body, "the new preview") {
				t.Errorf("the edited comment says %q", body)
			}
		})
	}
}

// Another app's comment on the same pull request is somebody else's and is not
// touched: two apps built from one repository get one comment each.
func TestAnotherAppsCommentIsLeftAlone(t *testing.T) {
	host := &fakeHost{comments: `[{"id":77,"body":"<!-- skifity-preview:app_other -->\nits preview"}]`}
	server := host.server(t)
	err := UpsertComment(t.Context(), CommentRequest{
		RepoURL: "https://github.example.test/acme/site", Kind: "github", BaseURL: server.URL,
		Token: "a-token", PullRequest: 12, Marker: "skifity-preview:app_9", Body: "this preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	if write := host.only(t); write.Method != http.MethodPost {
		t.Fatalf("another app's comment was edited: %+v", write)
	}
}

// Nothing changed since the last push, so nothing is sent: a redeploy of the
// same commit should not bump the pull request.
func TestAnUnchangedCommentIsNotRewritten(t *testing.T) {
	host := &fakeHost{comments: `[{"id":77,"body":"<!-- skifity-preview:app_9 -->\nthe preview"}]`}
	server := host.server(t)
	err := UpsertComment(t.Context(), CommentRequest{
		RepoURL: "https://github.example.test/acme/site", Kind: "github", BaseURL: server.URL,
		Token: "a-token", PullRequest: 12, Marker: "skifity-preview:app_9", Body: "the preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.writes) != 0 {
		t.Fatalf("an identical comment was rewritten: %+v", host.writes)
	}
}

func TestThePreviewCommentSaysWhatAReviewerNeeds(t *testing.T) {
	ready := PreviewComment{
		App: "web", State: StateSuccess, URL: "https://web-pr-12.example.test",
		LogsURL: "https://panel.example.test/apps/app_9/deployments", Commit: commit,
	}.Markdown()
	for _, want := range []string{
		"Ready", "[web-pr-12.example.test](https://web-pr-12.example.test)",
		"([logs](https://panel.example.test/apps/app_9/deployments))", "`0123456`",
	} {
		if !strings.Contains(ready, want) {
			t.Errorf("a ready preview's comment is missing %q:\n%s", want, ready)
		}
	}

	failed := PreviewComment{App: "web", State: StateFailure, Commit: commit,
		Reason: "The build failed: npm ERR! missing script: build"}.Markdown()
	if !strings.Contains(failed, "Failed") || !strings.Contains(failed, "missing script: build") {
		t.Errorf("a failed preview's comment does not say why:\n%s", failed)
	}

	building := PreviewComment{App: "web", State: StatePending, Commit: commit}.Markdown()
	if !strings.Contains(building, "Building") {
		t.Errorf("a preview being built does not say so:\n%s", building)
	}
}

// An app's name is the team's own, but it goes into a table cell and must not
// break the table or become a link.
func TestAnAppNameCannotBreakTheComment(t *testing.T) {
	comment := PreviewComment{App: "a|b [x](https://evil.example)", State: StateSuccess}.Markdown()
	if strings.Contains(comment, "[x](https://evil.example)") {
		t.Fatalf("an app name became a link:\n%s", comment)
	}
	if strings.Contains(comment, "a|b") {
		t.Fatalf("an app name broke out of its cell:\n%s", comment)
	}
}
