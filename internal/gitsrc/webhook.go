// Package gitsrc understands Git hosts: webhooks, repository URLs and the
// credentials needed to clone.
package gitsrc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// PushEvent is what a webhook boils down to, whichever host sent it.
type PushEvent struct {
	// Kind is push, pull_request_opened, pull_request_closed or ping.
	Kind string
	// RepoURL is the canonical HTTPS clone URL, normalised so it matches what
	// the app record stores.
	RepoURL string
	// Branch is the branch that was pushed, without refs/heads/.
	Branch string
	// CommitSHA is the commit now at the tip of that branch.
	CommitSHA string
	// CommitMessage and CommitAuthor are for the deploy history.
	CommitMessage string
	CommitAuthor  string
	// PullRequest is set for pull request events, which drive preview
	// environments.
	PullRequest int
	// SourceBranch is the pull request's head branch.
	SourceBranch string
	// Deleted marks a branch deletion, which tears a preview environment down.
	Deleted bool
	// Fork is true when a pull request comes from a repository other than the
	// one it targets — which means its author does not necessarily have write
	// access, and its code must not be handed the project's secrets.
	Fork bool
}

// ErrUnsupportedEvent is returned for events we deliberately ignore, such as a
// star or a comment. It is not a failure: the webhook is answered with 200 so
// the host does not retry.
var ErrUnsupportedEvent = errors.New("this event is not one Skifity acts on")

// ErrBadSignature means the payload could not be verified.
var ErrBadSignature = errors.New("the webhook signature does not match")

// VerifyGitHubSignature checks the X-Hub-Signature-256 header.
func VerifyGitHubSignature(secret string, body []byte, header string) error {
	if secret == "" {
		// Refusing unsigned webhooks is the point: without this, anyone who
		// learns the URL can trigger deploys.
		return errors.New("this Git connection has no webhook secret, so pushes cannot be verified")
	}
	expected, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(expected)) {
		return ErrBadSignature
	}
	return nil
}

// VerifyGiteaSignature checks the X-Gitea-Signature header.
//
// It is an HMAC-SHA256 of the body in hex, like GitHub's, but with no
// "sha256=" in front of it. That distinction matters: this header used to be
// checked with VerifyGitLabToken, which compares the header against the secret
// itself — so it was comparing a secret with a digest, two things that can
// never be equal, and every push from a Gitea that sends only this header was
// rejected as unverified.
func VerifyGiteaSignature(secret string, body []byte, header string) error {
	if secret == "" {
		return errors.New("this Git connection has no webhook secret, so pushes cannot be verified")
	}
	if header == "" {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(strings.TrimPrefix(header, "sha256="))) {
		return ErrBadSignature
	}
	return nil
}

// VerifyGitLabToken checks the X-Gitlab-Token header, which is a shared secret
// rather than a signature.
func VerifyGitLabToken(secret, header string) error {
	if secret == "" {
		return errors.New("this Git connection has no webhook secret, so pushes cannot be verified")
	}
	if !hmac.Equal([]byte(secret), []byte(header)) {
		return ErrBadSignature
	}
	return nil
}

// ParseWebhook turns a request body into a PushEvent.
func ParseWebhook(header http.Header, body []byte) (PushEvent, error) {
	switch {
	case header.Get("X-GitHub-Event") != "":
		return parseGitHub(header.Get("X-GitHub-Event"), body)
	case header.Get("X-Gitlab-Event") != "":
		return parseGitLab(header.Get("X-Gitlab-Event"), body)
	case header.Get("X-Gitea-Event") != "":
		// Gitea copies GitHub's payload shape closely enough to share a parser.
		return parseGitHub(header.Get("X-Gitea-Event"), body)
	default:
		return PushEvent{}, fmt.Errorf("this request has no event header, so Skifity cannot tell which Git host sent it")
	}
}

type githubPush struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Repository struct {
		CloneURL string `json:"clone_url"`
		HTMLURL  string `json:"html_url"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	HeadCommit *struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"head_commit"`
}

type githubPullRequest struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		Head struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				FullName string `json:"full_name"`
				Fork     bool   `json:"fork"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
		Title string `json:"title"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"pull_request"`
	Repository struct {
		CloneURL string `json:"clone_url"`
		HTMLURL  string `json:"html_url"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func parseGitHub(event string, body []byte) (PushEvent, error) {
	switch event {
	case "ping":
		return PushEvent{Kind: "ping"}, nil

	case "push":
		var payload githubPush
		if err := json.Unmarshal(body, &payload); err != nil {
			return PushEvent{}, fmt.Errorf("read push payload: %w", err)
		}
		branch, ok := strings.CutPrefix(payload.Ref, "refs/heads/")
		if !ok {
			// A tag push is not a branch update; ignoring it is correct.
			return PushEvent{}, ErrUnsupportedEvent
		}
		ev := PushEvent{
			Kind:      "push",
			RepoURL:   NormaliseRepoURL(firstNonEmpty(payload.Repository.CloneURL, payload.Repository.HTMLURL)),
			Branch:    branch,
			CommitSHA: payload.After,
			Deleted:   payload.Deleted,
		}
		if payload.HeadCommit != nil {
			ev.CommitMessage = firstLine(payload.HeadCommit.Message)
			ev.CommitAuthor = payload.HeadCommit.Author.Name
		}
		return ev, nil

	case "pull_request":
		var payload githubPullRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			return PushEvent{}, fmt.Errorf("read pull request payload: %w", err)
		}
		kind := ""
		switch payload.Action {
		case "opened", "reopened", "synchronize":
			kind = "pull_request_opened"
		case "closed":
			kind = "pull_request_closed"
		default:
			return PushEvent{}, ErrUnsupportedEvent
		}
		// A pull request whose head is in a different repository was opened by
		// somebody who does not necessarily have write access here. Anything
		// unexpected — a head repository GitHub did not send, a name that does
		// not match — counts as a fork, because the safe answer to "whose code
		// is this" is the cautious one.
		head := payload.PullRequest.Head.Repo.FullName
		base := firstNonEmpty(payload.PullRequest.Base.Repo.FullName, payload.Repository.FullName)
		fork := head == "" || base == "" || !strings.EqualFold(head, base)

		return PushEvent{
			Kind:          kind,
			RepoURL:       NormaliseRepoURL(firstNonEmpty(payload.Repository.CloneURL, payload.Repository.HTMLURL)),
			Branch:        payload.PullRequest.Head.Ref,
			SourceBranch:  payload.PullRequest.Head.Ref,
			CommitSHA:     payload.PullRequest.Head.SHA,
			CommitMessage: payload.PullRequest.Title,
			CommitAuthor:  payload.PullRequest.User.Login,
			PullRequest:   payload.Number,
			Fork:          fork,
		}, nil

	default:
		return PushEvent{}, ErrUnsupportedEvent
	}
}

type gitlabPush struct {
	Ref      string `json:"ref"`
	After    string `json:"after"`
	UserName string `json:"user_name"`
	Project  struct {
		GitHTTPURL string `json:"git_http_url"`
		WebURL     string `json:"web_url"`
	} `json:"project"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commits"`
}

type gitlabMergeRequest struct {
	ObjectAttributes struct {
		Action          string `json:"action"`
		IID             int    `json:"iid"`
		SourceBranch    string `json:"source_branch"`
		SourceProjectID int    `json:"source_project_id"`
		TargetProjectID int    `json:"target_project_id"`
		LastCommit      struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"last_commit"`
		Title string `json:"title"`
	} `json:"object_attributes"`
	Project struct {
		GitHTTPURL string `json:"git_http_url"`
	} `json:"project"`
	User struct {
		Name string `json:"name"`
	} `json:"user"`
}

func parseGitLab(event string, body []byte) (PushEvent, error) {
	switch event {
	case "Push Hook":
		var payload gitlabPush
		if err := json.Unmarshal(body, &payload); err != nil {
			return PushEvent{}, fmt.Errorf("read push payload: %w", err)
		}
		branch, ok := strings.CutPrefix(payload.Ref, "refs/heads/")
		if !ok {
			return PushEvent{}, ErrUnsupportedEvent
		}
		ev := PushEvent{
			Kind:      "push",
			RepoURL:   NormaliseRepoURL(firstNonEmpty(payload.Project.GitHTTPURL, payload.Project.WebURL)),
			Branch:    branch,
			CommitSHA: payload.After,
			// GitLab sends all zeroes for a deleted branch.
			Deleted:      strings.Trim(payload.After, "0") == "",
			CommitAuthor: payload.UserName,
		}
		if len(payload.Commits) > 0 {
			last := payload.Commits[len(payload.Commits)-1]
			ev.CommitMessage = firstLine(last.Message)
		}
		return ev, nil

	case "Merge Request Hook":
		var payload gitlabMergeRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			return PushEvent{}, fmt.Errorf("read merge request payload: %w", err)
		}
		kind := ""
		switch payload.ObjectAttributes.Action {
		case "open", "reopen", "update":
			kind = "pull_request_opened"
		case "close", "merge":
			kind = "pull_request_closed"
		default:
			return PushEvent{}, ErrUnsupportedEvent
		}
		// GitLab calls it a fork when the source project is a different one.
		// As with GitHub, anything missing counts as a fork.
		attrs := payload.ObjectAttributes
		fork := attrs.SourceProjectID == 0 || attrs.TargetProjectID == 0 ||
			attrs.SourceProjectID != attrs.TargetProjectID

		return PushEvent{
			Kind:          kind,
			RepoURL:       NormaliseRepoURL(payload.Project.GitHTTPURL),
			Branch:        attrs.SourceBranch,
			SourceBranch:  attrs.SourceBranch,
			CommitSHA:     attrs.LastCommit.ID,
			CommitMessage: attrs.Title,
			CommitAuthor:  payload.User.Name,
			PullRequest:   attrs.IID,
			Fork:          fork,
		}, nil

	default:
		return PushEvent{}, ErrUnsupportedEvent
	}
}

// NormaliseRepoURL reduces the many ways of writing a repository URL to one, so
// that a webhook's URL matches what the app record stores.
//
//	git@github.com:owner/repo.git  ->  https://github.com/owner/repo
//	https://github.com/owner/repo/ ->  https://github.com/owner/repo
func NormaliseRepoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// SSH shorthand is not a URL, so convert before parsing.
	if strings.HasPrefix(raw, "git@") {
		if host, path, ok := strings.Cut(strings.TrimPrefix(raw, "git@"), ":"); ok {
			raw = "https://" + host + "/" + path
		}
	}
	raw = strings.TrimSuffix(raw, ".git")
	raw = strings.TrimSuffix(raw, "/")

	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(raw)
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	// Credentials embedded in a URL must never be stored or compared.
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String()
}

// RepoName returns "owner/repo" from a repository URL, for display.
func RepoName(repoURL string) string {
	parsed, err := url.Parse(NormaliseRepoURL(repoURL))
	if err != nil {
		return repoURL
	}
	return strings.Trim(parsed.Path, "/")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}
