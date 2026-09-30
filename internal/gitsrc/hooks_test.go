package gitsrc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Registering the webhook rather than asking somebody to paste it.
//
// The connect form has always said a token is needed "so Skifity can read the
// repository and register a webhook", and only the first half happened. These
// check the second: that a hook is created, that a repository already pointing
// here is left alone, and that a token which cannot manage hooks comes back as
// something to act on rather than as a failure.

func TestAWebhookIsRegisteredOnTheRepository(t *testing.T) {
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &created)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		}
	}))
	defer server.Close()

	result := EnsureWebhook(t.Context(), HookRequest{
		RepoURL:   "https://github.example.test/acme/site",
		Kind:      "github",
		BaseURL:   server.URL,
		Token:     "a-token",
		DeliverTo: "https://panel.example.test/api/webhooks/git/src_1",
		Secret:    "a-secret",
	})
	if !result.Created {
		t.Fatalf("no webhook was created: %s", result.Reason)
	}

	config, _ := created["config"].(map[string]any)
	if config["url"] != "https://panel.example.test/api/webhooks/git/src_1" {
		t.Errorf("the hook delivers to %v", config["url"])
	}
	// Unsigned deliveries are refused by the panel, so a hook with no secret
	// would be a hook whose every delivery is rejected.
	if config["secret"] != "a-secret" {
		t.Error("the hook was created without the signing secret")
	}
	// Preview deployments are built from pull requests; a push-only hook makes
	// that feature quietly do nothing.
	events, _ := created["events"].([]any)
	var hasPullRequest bool
	for _, event := range events {
		if event == "pull_request" {
			hasPullRequest = true
		}
	}
	if !hasPullRequest {
		t.Errorf("the hook does not carry pull requests, so preview deploys never fire: %v", events)
	}
}

// Two apps from one repository must not mean two hooks and two deploys per push.
func TestARepositoryAlreadyPointingHereIsLeftAlone(t *testing.T) {
	posted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posted = true
		}
		_, _ = w.Write([]byte(`[{"config":{"url":"https://panel.example.test/api/webhooks/git/src_1/"}}]`))
	}))
	defer server.Close()

	result := EnsureWebhook(t.Context(), HookRequest{
		RepoURL: "https://github.example.test/acme/site", Kind: "github", BaseURL: server.URL,
		Token: "a-token", DeliverTo: "https://panel.example.test/api/webhooks/git/src_1",
		Secret: "a-secret",
	})
	if !result.AlreadyThere {
		t.Fatalf("an existing hook was not recognised: %+v", result)
	}
	if posted {
		t.Error("a second hook was created for a repository that already delivers here")
	}
	if !result.Registered() {
		t.Error("a repository that already delivers here is registered")
	}
}

// A read-only token is the right token for somebody who deploys by hand, so
// this has to be a sentence rather than an error that loses the app.
func TestATokenThatCannotManageHooksSaysSo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	result := EnsureWebhook(t.Context(), HookRequest{
		RepoURL: "https://github.example.test/acme/site", Kind: "github", BaseURL: server.URL,
		Token: "a-token", DeliverTo: "https://panel.example.test/api/webhooks/git/src_1",
		Secret: "a-secret",
	})
	if result.Registered() {
		t.Fatal("a refused hook was reported as registered")
	}
	if !strings.Contains(result.Reason, "cannot manage") {
		t.Errorf("the reason does not say what is wrong: %q", result.Reason)
	}
}

// GitLab verifies with a plain token header rather than a signature, and a
// hook created with the wrong field is a hook whose deliveries are all refused.
func TestGitLabHooksCarryTheTokenTheyAreVerifiedWith(t *testing.T) {
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &created)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	result := EnsureWebhook(t.Context(), HookRequest{
		RepoURL: "https://gitlab.example.test/acme/site", Kind: "gitlab", BaseURL: server.URL,
		Token: "a-token", DeliverTo: "https://panel.example.test/api/webhooks/git/src_1",
		Secret: "a-secret",
	})
	if !result.Created {
		t.Fatalf("no webhook was created: %s", result.Reason)
	}
	if created["token"] != "a-secret" {
		t.Errorf("the hook carries %v as its token, so every delivery would be refused", created["token"])
	}
	if created["merge_requests_events"] != true {
		t.Error("the hook does not carry merge requests, so preview deploys never fire")
	}
}

// Nothing is attempted without the things a working hook needs.
func TestAHookIsNotAttemptedWithoutWhatItNeeds(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  HookRequest
	}{
		{"no token", HookRequest{RepoURL: "https://github.com/a/b", DeliverTo: "https://p/x", Secret: "s"}},
		{"no address", HookRequest{RepoURL: "https://github.com/a/b", Token: "t", Secret: "s"}},
		{"no secret", HookRequest{RepoURL: "https://github.com/a/b", Token: "t", DeliverTo: "https://p/x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := EnsureWebhook(t.Context(), tc.req)
			if result.Registered() {
				t.Fatal("a hook was reported registered with something missing")
			}
			if result.Reason == "" {
				t.Error("nothing said why")
			}
		})
	}
}
