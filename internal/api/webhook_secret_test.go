package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"skifity/internal/store"
)

// A Git connection's webhook secret is its own.
//
// It was the connection's access token. The panel registered the token as the
// secret on every repository it hooked, and GitLab sends its secret verbatim
// in a header on every delivery — so each push carried the account's token to
// wherever the webhook pointed, and changing the secret meant changing the
// token.
func TestAGitConnectionHasAWebhookSecretOfItsOwn(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	const token = "ghp_faketokenfortests0000000000000000"
	status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/git-sources",
		map[string]string{"kind": "github_pat", "name": "acme-github", "token": token})
	if status != http.StatusCreated {
		t.Fatalf("connecting answered %d: %s", status, body)
	}
	var created struct {
		Source struct {
			ID string `json:"id"`
		} `json:"source"`
		WebhookSecret string `json:"webhook_secret"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	if created.WebhookSecret == "" || created.WebhookSecret == token {
		t.Fatalf("the webhook secret is %q", created.WebhookSecret)
	}

	deliver := func(sourceID, secret string) int {
		payload := []byte(`{"zen": "Keep it logically awesome.", "hook_id": 1}`)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(payload)
		request := httptest.NewRequest(http.MethodPost, "/api/webhooks/git/"+sourceID, bytes.NewReader(payload))
		request.Header.Set("X-GitHub-Event", "ping")
		request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		recorder := httptest.NewRecorder()
		h.api.ServeHTTP(recorder, request)
		return recorder.Code
	}
	if code := deliver(created.Source.ID, created.WebhookSecret); code != http.StatusOK {
		t.Fatalf("a delivery signed with the webhook secret answered %d", code)
	}
	if code := deliver(created.Source.ID, token); code != http.StatusUnauthorized {
		t.Fatalf("a delivery signed with the token answered %d", code)
	}

	// An admin can read it again, for a host the panel could not hook itself.
	status, body = h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID+"/git-sources/"+created.Source.ID+"/webhook", nil)
	var shown struct {
		URL    string `json:"url"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(body), &shown); status != http.StatusOK || err != nil || shown.Secret != created.WebhookSecret || shown.URL == "" {
		t.Fatalf("reading the webhook answered %d: %s", status, body)
	}
	member := h.newMember(acme, "dev", store.RoleMember)
	if status, _ := h.do(member, http.MethodGet, "/api/teams/"+acme.team.ID+"/git-sources/"+created.Source.ID+"/webhook", nil); status != http.StatusForbidden {
		t.Fatalf("a member read the webhook secret: %d", status)
	}

	// A connection that is not there answers exactly as a bad signature does,
	// so the endpoint does not say which ids exist.
	if code := deliver("gs_doesnotexist", "anything"); code != http.StatusUnauthorized {
		t.Fatalf("an unknown connection answered %d", code)
	}
}
