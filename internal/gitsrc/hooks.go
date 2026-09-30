package gitsrc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"skifity/internal/version"
)

// Registering the webhook, rather than asking somebody to paste it.
//
// The connect form says "a token is needed so Skifity can read the repository
// and register a webhook", and for a long time only the first half was true:
// the panel printed a URL and a secret and left the operator to add them in
// the Git host's settings, once per repository, by hand. Miss it and deploy on
// push silently never happens — there is nothing to see, because nothing is
// wrong, the panel is simply never told.
//
// A hook belongs to a repository and a token covers many, so this runs when an
// app is created rather than when the account is connected: that is the first
// moment the panel knows which repository to hook.
//
// It is never fatal. A token without permission to manage hooks is an ordinary
// thing to have — a read-only token is the right token for somebody who deploys
// by hand — so a refusal comes back as "add it yourself, here is the URL"
// rather than as a failure to create the app.

// HookRequest is what registering a webhook needs.
type HookRequest struct {
	// RepoURL is the repository to hook.
	RepoURL string
	// Kind is github, gitlab or gitea. Empty is worked out from the host.
	Kind string
	// BaseURL is the provider's own address, for a self-hosted instance.
	BaseURL string
	// Token must be able to manage the repository's hooks.
	Token string
	// DeliverTo is the panel's own webhook address for this Git connection.
	DeliverTo string
	// Secret signs the deliveries, and is the same one the panel verifies with.
	Secret string
}

// HookResult says what happened, in words a person can act on.
type HookResult struct {
	// Created is true when this call added the hook.
	Created bool
	// AlreadyThere is true when the repository was already delivering here.
	AlreadyThere bool
	// Reason explains a hook that was not registered. Empty on success.
	Reason string
}

// Registered reports whether pushes will reach the panel.
func (r HookResult) Registered() bool { return r.Created || r.AlreadyThere }

// EnsureWebhook makes the repository deliver pushes to the panel.
//
// Idempotent by the address it delivers to: a repository already pointing at
// this panel is left alone, so creating a second app from the same repository
// does not leave two hooks behind and two deploys per push.
func EnsureWebhook(ctx context.Context, req HookRequest) HookResult {
	if strings.TrimSpace(req.Token) == "" {
		return HookResult{Reason: "this Git connection has no token, so the panel cannot ask the host to add one"}
	}
	if strings.TrimSpace(req.DeliverTo) == "" || strings.TrimSpace(req.Secret) == "" {
		return HookResult{Reason: "the panel does not know its own address yet"}
	}
	owner, repo, err := ownerAndRepo(req.RepoURL)
	if err != nil {
		return HookResult{Reason: err.Error()}
	}

	kind := req.Kind
	if kind == "" {
		kind = KindFor(req.RepoURL)
	}
	switch kind {
	case "github", "github_pat":
		return ensureGitHubHook(ctx, req, owner, repo)
	case "gitlab":
		return ensureGitLabHook(ctx, req, owner, repo)
	case "gitea":
		return ensureGiteaHook(ctx, req, owner, repo)
	}
	return HookResult{Reason: "Skifity does not know how to register a webhook on this host"}
}

func ensureGitHubHook(ctx context.Context, req HookRequest, owner, repo string) HookResult {
	base := apiBase(req.BaseURL, "https://api.github.com", "/api/v3")
	endpoint := fmt.Sprintf("%s/repos/%s/%s/hooks", base, owner, repo)

	var existing []struct {
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if err := getJSON(ctx, endpoint, req.Token, "token", &existing); err != nil {
		return HookResult{Reason: err.Error()}
	}
	for _, hook := range existing {
		if sameHookTarget(hook.Config.URL, req.DeliverTo) {
			return HookResult{AlreadyThere: true}
		}
	}

	body := map[string]any{
		"name":   "web",
		"active": true,
		// Pull requests as well as pushes, because preview deployments are
		// built from them and a hook that only carries pushes makes that
		// feature quietly do nothing.
		"events": []string{"push", "pull_request"},
		"config": map[string]string{
			"url":          req.DeliverTo,
			"content_type": "json",
			"secret":       req.Secret,
			"insecure_ssl": "0",
		},
	}
	return postHook(ctx, endpoint, req.Token, "token", body)
}

func ensureGitLabHook(ctx context.Context, req HookRequest, owner, repo string) HookResult {
	base := apiBase(req.BaseURL, "https://gitlab.com/api/v4", "/api/v4")
	project := url.PathEscape(owner + "/" + repo)
	endpoint := fmt.Sprintf("%s/projects/%s/hooks", base, project)

	var existing []struct {
		URL string `json:"url"`
	}
	if err := getJSON(ctx, endpoint, req.Token, "bearer", &existing); err != nil {
		return HookResult{Reason: err.Error()}
	}
	for _, hook := range existing {
		if sameHookTarget(hook.URL, req.DeliverTo) {
			return HookResult{AlreadyThere: true}
		}
	}

	// GitLab sends the secret as a plain token header rather than a signature,
	// which is what VerifyGitLabToken checks.
	body := map[string]any{
		"url":                     req.DeliverTo,
		"token":                   req.Secret,
		"push_events":             true,
		"merge_requests_events":   true,
		"enable_ssl_verification": true,
	}
	return postHook(ctx, endpoint, req.Token, "bearer", body)
}

func ensureGiteaHook(ctx context.Context, req HookRequest, owner, repo string) HookResult {
	base := apiBase(req.BaseURL, "https://gitea.com/api/v1", "/api/v1")
	endpoint := fmt.Sprintf("%s/repos/%s/%s/hooks", base, owner, repo)

	var existing []struct {
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if err := getJSON(ctx, endpoint, req.Token, "token", &existing); err != nil {
		return HookResult{Reason: err.Error()}
	}
	for _, hook := range existing {
		if sameHookTarget(hook.Config.URL, req.DeliverTo) {
			return HookResult{AlreadyThere: true}
		}
	}

	body := map[string]any{
		"type":   "gitea",
		"active": true,
		"events": []string{"push", "pull_request"},
		"config": map[string]string{
			"url":          req.DeliverTo,
			"content_type": "json",
			"secret":       req.Secret,
		},
	}
	return postHook(ctx, endpoint, req.Token, "token", body)
}

// sameHookTarget compares two delivery addresses.
//
// Only the address, and ignoring a trailing slash: a hook that already points
// here is this panel's hook whatever else was configured on it, and adding a
// second would deploy twice per push.
func sameHookTarget(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}

// postHook creates the hook and turns whatever the host said into a reason.
func postHook(ctx context.Context, endpoint, token, scheme string, body map[string]any) HookResult {
	encoded, err := json.Marshal(body)
	if err != nil {
		return HookResult{Reason: err.Error()}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return HookResult{Reason: err.Error()}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", version.UserAgent())
	switch scheme {
	case "bearer":
		request.Header.Set("Authorization", "Bearer "+token)
	default:
		request.Header.Set("Authorization", "token "+token)
	}

	resp, err := client.Do(request)
	if err != nil {
		return HookResult{Reason: err.Error()}
	}
	defer resp.Body.Close()
	// Read and discard so the connection can be reused, and so a reason can
	// quote the host when it is short enough to be useful.
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))

	switch {
	case resp.StatusCode < 300:
		return HookResult{Created: true}
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return HookResult{Reason: "the token cannot manage this repository's webhooks"}
	case resp.StatusCode == http.StatusNotFound:
		return HookResult{Reason: "the repository was not found, or the token cannot see it"}
	case resp.StatusCode == http.StatusUnprocessableEntity:
		// GitHub answers this when a hook with the same address exists, which
		// the listing above should already have caught — but a repository can
		// be changed between the two calls.
		return HookResult{AlreadyThere: true}
	}
	return HookResult{Reason: fmt.Sprintf("the Git host answered %s: %s",
		resp.Status, strings.TrimSpace(string(answer)))}
}
