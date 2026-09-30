package gitsrc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"skifity/internal/version"
)

// Telling the Git host what happened to a commit.
//
// A preview environment was built for every pull request, and the only way to
// find its address was to open the panel and go looking for an environment
// named after the pull request. The people who most need that address — the
// reviewer, the person who opened the pull request — are the ones least likely
// to have a panel account. Every hosted platform answers this the same way:
// a status on the commit, and one comment on the pull request with the URL in
// it, kept up to date rather than added to on every push.
//
// Both are best effort. A token that can clone but not write statuses is an
// ordinary token to have, so a refusal comes back as a reason to show somebody,
// never as a failed deployment.

// State is where a commit's deployment stands, in the panel's own words. Each
// host spells these differently and the mapping lives here, once.
type State string

const (
	StatePending State = "pending"
	StateSuccess State = "success"
	StateFailure State = "failure"
)

// StatusRequest is one commit status to set.
type StatusRequest struct {
	// RepoURL is the repository the commit is in.
	RepoURL string
	// Kind is github, gitlab, gitea or bitbucket. Empty is worked out from the
	// host.
	Kind string
	// BaseURL is the provider's own address, for a self-hosted instance.
	BaseURL string
	// Token must be able to write commit statuses.
	Token string
	// Email goes with a Bitbucket API token; see ListRequest.Email.
	Email string
	// CommitSHA is the full commit the status is for.
	CommitSHA string
	// Ref is the branch the commit was deployed from, when that is a pull
	// request's: Bitbucket shows a status on a pull request only when it
	// names the pull request's source branch. The other hosts go by the
	// commit alone and do not read it.
	Ref   string
	State State
	// Context names the check, so several apps built from one repository show
	// as several lines rather than overwriting each other.
	Context string
	// Description is one short line. Hosts cut it off at 140 characters.
	Description string
	// TargetURL is where clicking the status goes.
	TargetURL string
}

// ReportStatus sets a status on a commit.
func ReportStatus(ctx context.Context, req StatusRequest) error {
	if strings.TrimSpace(req.Token) == "" {
		return errNoToken
	}
	if !validSHA(req.CommitSHA) {
		return fmt.Errorf("%q is not a commit the Git host would recognise", req.CommitSHA)
	}
	owner, repo, err := ownerAndRepo(req.RepoURL)
	if err != nil {
		return err
	}
	description := shorten(req.Description, 140)

	switch kindOf(req.Kind, req.RepoURL) {
	case "github", "github_pat":
		base := apiBase(req.BaseURL, "https://api.github.com", "/api/v3")
		endpoint := fmt.Sprintf("%s/repos/%s/%s/statuses/%s", base, owner, repo, req.CommitSHA)
		return expectOK(send(ctx, http.MethodPost, endpoint, req.Token, "token", map[string]any{
			"state":       githubState(req.State),
			"context":     req.Context,
			"description": description,
			"target_url":  req.TargetURL,
		}))
	case "gitlab":
		base := apiBase(req.BaseURL, "https://gitlab.com/api/v4", "/api/v4")
		endpoint := fmt.Sprintf("%s/projects/%s/statuses/%s",
			base, url.PathEscape(owner+"/"+repo), req.CommitSHA)
		status, answer, err := send(ctx, http.MethodPost, endpoint, req.Token, "bearer", map[string]any{
			"state":       gitlabState(req.State),
			"name":        req.Context,
			"description": description,
			"target_url":  req.TargetURL,
		})
		// GitLab refuses to set a status to the state it is already in, with a
		// 400 that says so. The commit already says what this call wanted it to
		// say, which is success rather than an error to show anybody.
		if status == http.StatusBadRequest && strings.Contains(string(answer), "Cannot transition status") {
			return nil
		}
		return expectOK(status, answer, err)
	case "gitea":
		base := apiBase(req.BaseURL, "https://gitea.com/api/v1", "/api/v1")
		endpoint := fmt.Sprintf("%s/repos/%s/%s/statuses/%s", base, owner, repo, req.CommitSHA)
		return expectOK(send(ctx, http.MethodPost, endpoint, req.Token, "token", map[string]any{
			"state":       githubState(req.State),
			"context":     req.Context,
			"description": description,
			"target_url":  req.TargetURL,
		}))
	case "bitbucket":
		return reportBitbucketStatus(ctx, req, owner, repo, description)
	}
	return errUnknownHost
}

// CommentRequest is one pull request comment to keep up to date.
type CommentRequest struct {
	RepoURL string
	Kind    string
	BaseURL string
	// Token must be able to comment on pull requests.
	Token string
	// Email goes with a Bitbucket API token; see ListRequest.Email.
	Email string
	// PullRequest is the pull request's number (a merge request's iid on
	// GitLab), as the host shows it in its own URLs.
	PullRequest int
	// Marker identifies this comment among everything else on the pull
	// request, so the next push edits it instead of adding another. It goes
	// into the comment as an HTML comment, which every host hides.
	Marker string
	// Body is the Markdown to show.
	Body string
}

// UpsertComment writes the comment, or rewrites the one written before.
//
// One comment per app per pull request, edited in place. A comment per push is
// what turns a bot from useful into muted: on a pull request with twenty
// commits, the address somebody wants is at the bottom of twenty copies of
// itself.
func UpsertComment(ctx context.Context, req CommentRequest) error {
	if strings.TrimSpace(req.Token) == "" {
		return errNoToken
	}
	if req.PullRequest <= 0 {
		return fmt.Errorf("there is no pull request to comment on")
	}
	if strings.TrimSpace(req.Marker) == "" {
		return fmt.Errorf("a comment needs a marker, or every push would add another")
	}
	owner, repo, err := ownerAndRepo(req.RepoURL)
	if err != nil {
		return err
	}
	body := "<!-- " + req.Marker + " -->\n" + req.Body
	number := strconv.Itoa(req.PullRequest)

	switch kindOf(req.Kind, req.RepoURL) {
	case "github", "github_pat":
		base := apiBase(req.BaseURL, "https://api.github.com", "/api/v3")
		issue := fmt.Sprintf("%s/repos/%s/%s/issues/%s/comments", base, owner, repo, number)
		edit := fmt.Sprintf("%s/repos/%s/%s/issues/comments/", base, owner, repo)
		return upsert(ctx, req, body, issue+"?per_page=100", issue, edit, http.MethodPatch, "token")
	case "gitlab":
		base := apiBase(req.BaseURL, "https://gitlab.com/api/v4", "/api/v4")
		notes := fmt.Sprintf("%s/projects/%s/merge_requests/%s/notes",
			base, url.PathEscape(owner+"/"+repo), number)
		return upsert(ctx, req, body, notes+"?per_page=100&sort=asc", notes, notes+"/", http.MethodPut, "bearer")
	case "gitea":
		base := apiBase(req.BaseURL, "https://gitea.com/api/v1", "/api/v1")
		issue := fmt.Sprintf("%s/repos/%s/%s/issues/%s/comments", base, owner, repo, number)
		edit := fmt.Sprintf("%s/repos/%s/%s/issues/comments/", base, owner, repo)
		return upsert(ctx, req, body, issue, issue, edit, http.MethodPatch, "token")
	case "bitbucket":
		// Its own marker: Bitbucket escapes HTML in Markdown, so the HTML
		// comment above would be shown to everybody as text.
		return upsertBitbucketComment(ctx, req, owner, repo, number)
	}
	return errUnknownHost
}

// upsert finds this panel's comment by its marker and edits it, or adds one.
//
// All three hosts return comments as a list of objects with an id and a body,
// which is the only part read here.
func upsert(ctx context.Context, req CommentRequest, body, list, create, editPrefix, editMethod, scheme string) error {
	var existing []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := getJSON(ctx, list, req.Token, scheme, &existing); err != nil {
		return err
	}
	marker := "<!-- " + req.Marker + " -->"
	for _, comment := range existing {
		if strings.HasPrefix(comment.Body, marker) {
			if comment.Body == body {
				return nil
			}
			endpoint := editPrefix + strconv.FormatInt(comment.ID, 10)
			return expectOK(send(ctx, editMethod, endpoint, req.Token, scheme, map[string]any{"body": body}))
		}
	}
	return expectOK(send(ctx, http.MethodPost, create, req.Token, scheme, map[string]any{"body": body}))
}

// PreviewComment is what a preview deployment says on its pull request.
type PreviewComment struct {
	// App is the app's name as the panel shows it.
	App string
	// State is where the deployment stands.
	State State
	// URL is the preview's address, once it has one.
	URL string
	// LogsURL opens this deployment in the panel.
	LogsURL string
	// Commit is the commit that was deployed.
	Commit string
	// Reason says why it failed, in one line.
	Reason string
}

// Markdown renders the comment.
//
// It is written for the reviewer, who may never have heard of Skifity: the
// address first, whether it works, and where to look when it does not.
func (c PreviewComment) Markdown() string {
	return c.MarkdownFor("")
}

// MarkdownFor renders the comment for one kind of host. Bitbucket escapes
// every HTML tag in Markdown and shows it as text, so its footer is in italics
// rather than in <sub>; the rest is the same everywhere.
func (c PreviewComment) MarkdownFor(kind string) string {
	var b strings.Builder
	b.WriteString("**Preview of " + escapeMarkdown(c.App) + "**\n\n")
	b.WriteString("| Status | Preview | Commit |\n|---|---|---|\n")

	var status, preview string
	switch c.State {
	case StateSuccess:
		status = "Ready"
		preview = "—"
		if c.URL != "" {
			preview = "[" + escapeMarkdown(displayURL(c.URL)) + "](" + c.URL + ")"
		}
	case StateFailure:
		status = "Failed"
		preview = "—"
	default:
		status = "Building"
		preview = "Building…"
	}
	if c.LogsURL != "" {
		status += " ([logs](" + c.LogsURL + "))"
	}
	b.WriteString("| " + status + " | " + preview + " | `" + shortCommit(c.Commit) + "` |\n")

	if c.State == StateFailure && strings.TrimSpace(c.Reason) != "" {
		b.WriteString("\n" + escapeMarkdown(shorten(c.Reason, 300)) + "\n")
	}
	const footer = "Updated on every push by Skifity. The preview is removed when this pull request is closed."
	if kind == "bitbucket" {
		b.WriteString("\n_" + footer + "_\n")
	} else {
		b.WriteString("\n<sub>" + footer + "</sub>\n")
	}
	return b.String()
}

var (
	errNoToken     = fmt.Errorf("this Git connection has no token, so the panel cannot report back to the host")
	errUnknownHost = fmt.Errorf("the panel does not know how to report to this Git host")
)

func kindOf(kind, repoURL string) string {
	if kind != "" {
		return kind
	}
	return KindFor(repoURL)
}

// githubState maps onto GitHub's and Gitea's shared vocabulary.
func githubState(state State) string {
	switch state {
	case StateSuccess:
		return "success"
	case StateFailure:
		return "failure"
	}
	return "pending"
}

// gitlabState maps onto GitLab's, which calls a failure "failed" and has a
// separate "running" for work that has started.
func gitlabState(state State) string {
	switch state {
	case StateSuccess:
		return "success"
	case StateFailure:
		return "failed"
	}
	return "running"
}

// send makes one write to the host's API and returns what it answered.
func send(ctx context.Context, method, endpoint, token, scheme string, body map[string]any) (int, []byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", version.UserAgent())
	authorize(request, token, scheme)

	resp, err := client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("reach the Git host: %w", err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
	return resp.StatusCode, answer, nil
}

// expectOK turns an answer into nil or a reason somebody can act on.
func expectOK(status int, answer []byte, err error) error {
	if err != nil {
		return err
	}
	switch {
	case status < 300:
		return nil
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return fmt.Errorf("the token cannot write commit statuses or comments on this repository")
	case status == http.StatusNotFound:
		return fmt.Errorf("the repository, commit or pull request was not found, or the token cannot see it")
	}
	return fmt.Errorf("the Git host answered %d: %s", status, strings.TrimSpace(string(answer)))
}

// validSHA accepts a full commit hash, SHA-1 or SHA-256. A short hash is
// refused: the hosts accept one inconsistently, and it goes into a URL path.
func validSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func shorten(s string, limit int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit-1]) + "…"
}

func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func displayURL(raw string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://"), "/")
}

// escapeMarkdown keeps an app name from breaking out of its table cell or
// turning into a link. The name is the team's own and the pull request is
// theirs too, so this is about a tidy comment rather than an attack.
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"|", `\|`, "[", `\[`, "]", `\]`, "<", "&lt;", ">", "&gt;",
		"*", `\*`, "_", `\_`, "`", "\\`", "\n", " ",
	)
	return replacer.Replace(s)
}
