package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"skifity/internal/settings"
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

// A new value for a variable the build reads, sent by a form or a command that
// did not ask whether the build reads it, made it a runtime variable: the
// next build went without NEXT_PUBLIC_API_URL.
func TestANewValueKeepsAVariableInTheBuild(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	path := "/api/apps/" + app.ID + "/variables"
	if status, body := h.do(acme, http.MethodPut, path, map[string]any{"key": "NEXT_PUBLIC_API_URL", "value": "https://a.example.test", "build_time": true}); status != http.StatusOK {
		t.Fatalf("setting it answered %d: %s", status, body)
	}
	buildTime := func() bool {
		rows, _ := h.db.ListVariables(t.Context(), app.ID)
		for _, row := range rows {
			if row.Key == "NEXT_PUBLIC_API_URL" {
				return row.BuildTime
			}
		}
		t.Fatal("the variable is gone")
		return false
	}
	h.do(acme, http.MethodPut, path, map[string]any{"key": "NEXT_PUBLIC_API_URL", "value": "https://b.example.test"})
	if !buildTime() {
		t.Fatal("a new value on its own took the variable out of the build")
	}
	h.do(acme, http.MethodPost, path+"/batch", map[string]any{"set": []map[string]any{{"key": "NEXT_PUBLIC_API_URL", "value": "https://c.example.test"}}})
	if !buildTime() {
		t.Fatal("a batch with a new value took the variable out of the build")
	}
	h.do(acme, http.MethodPut, path, map[string]any{"key": "NEXT_PUBLIC_API_URL", "value": "https://d.example.test", "build_time": false})
	if buildTime() {
		t.Fatal("saying so did not take it out of the build")
	}
}

// A panel moved to a domain of its own under Settings kept registering its
// webhooks at the address the installer gave it.
func TestAWebhookIsDeliveredToThePanelURLInSettings(t *testing.T) {
	h := newHarness(t)
	h.api.cfg.PublicURL = "http://10-0-0-5.sslip.io"
	request := httptest.NewRequest(http.MethodPost, "http://elsewhere.test/api/apps", nil)
	if got := h.api.webhookURL(request, "gs_1"); got != "http://10-0-0-5.sslip.io/api/webhooks/git/gs_1" {
		t.Fatalf("with only the installer's address the webhook goes to %q", got)
	}
	if err := h.db.SetSetting(t.Context(), settings.KeyPanelURL, "https://panel.acme.test/", false, "test"); err != nil {
		t.Fatal(err)
	}
	if got := h.api.webhookURL(request, "gs_1"); got != "https://panel.acme.test/api/webhooks/git/gs_1" {
		t.Errorf("with a Panel URL saved the webhook goes to %q, want the saved address", got)
	}
}

// signedPing delivers GitHub's ping to a connection, signed with secret, and
// answers the status.
func (h *harness) signedPing(sourceID, secret string) int {
	h.t.Helper()
	payload := []byte(`{"zen": "Design for failure.", "hook_id": 2}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/git/"+sourceID, bytes.NewReader(payload))
	request.Header.Set("X-GitHub-Event", "ping")
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	recorder := httptest.NewRecorder()
	h.api.ServeHTTP(recorder, request)
	return recorder.Code
}

// storedToken is the token the panel reads a connection with now.
func (h *harness) storedToken(sourceID string) string {
	h.t.Helper()
	source, err := h.db.GetGitSource(h.t.Context(), sourceID)
	if err != nil {
		h.t.Fatal(err)
	}
	token, _ := h.api.gitCredentials(httptest.NewRequest(http.MethodGet, "/", nil), source)
	return token
}

// A token that expired was replaced by disconnecting and connecting again,
// which left every app with no connection and gave the new one a new id, so
// the webhook on every repository — at /api/webhooks/git/{the old id} — was
// refused from then on.
func TestReplacingAGitTokenKeepsTheConnectionItsAppsAndItsSecret(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	const old = "ghp_faketokenfortests0000000000000000"
	const replacement = "ghp_faketokenfortests1111111111111111"
	status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/git-sources",
		map[string]string{"kind": "github_pat", "name": "acme-github", "token": old})
	if status != http.StatusCreated {
		t.Fatalf("connecting answered %d: %s", status, body)
	}
	var created struct {
		Source        store.GitSource `json:"source"`
		WebhookSecret string          `json:"webhook_secret"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	app := h.gitApp(acme, "web", created.Source.ID, true)
	path := "/api/teams/" + acme.team.ID + "/git-sources/" + created.Source.ID

	status, body = h.do(acme, http.MethodPatch, path, map[string]string{"token": replacement})
	var updated store.GitSource
	if err := json.Unmarshal([]byte(body), &updated); status != http.StatusOK || err != nil {
		t.Fatalf("replacing the token answered %d: %s", status, body)
	}
	if updated.ID != created.Source.ID || updated.Name != "acme-github" || strings.Contains(body, replacement) {
		t.Errorf("the answer is %s", body)
	}
	if got := h.storedToken(created.Source.ID); got != replacement {
		t.Errorf("the panel reads the connection with %q, want the new token", got)
	}
	if after, err := h.db.GetApp(t.Context(), app.ID); err != nil || after.GitSourceID != created.Source.ID {
		t.Errorf("the app's connection is %q after the token was replaced (%v)", after.GitSourceID, err)
	}
	status, body = h.do(acme, http.MethodGet, path+"/webhook", nil)
	if status != http.StatusOK || !strings.Contains(body, created.WebhookSecret) {
		t.Errorf("the webhook secret changed with the token: %d %s", status, body)
	}
	if code := h.signedPing(created.Source.ID, created.WebhookSecret); code != http.StatusOK {
		t.Errorf("a delivery signed with the unchanged secret answered %d", code)
	}

	// A token is what such a connection is for: an empty one is refused,
	// and nothing changes.
	if status, body := h.do(acme, http.MethodPatch, path, map[string]string{"token": "  "}); status != http.StatusBadRequest {
		t.Errorf("an empty token answered %d: %s", status, body)
	}
	if got := h.storedToken(created.Source.ID); got != replacement {
		t.Errorf("a refused replacement left %q", got)
	}

	events, err := h.db.ListAudit(t.Context(), acme.team.ID, "git_source.updated", "", 5)
	if err != nil || len(events) != 1 {
		t.Fatalf("%d git_source.updated events (%v)", len(events), err)
	}
	if strings.Contains(events[0].TargetLabel+events[0].Metadata, replacement) {
		t.Errorf("the activity log has the token in it: %+v", events[0])
	}
}

// A connection made before each had a webhook secret of its own signs with
// its token, and the hooks on its repositories were registered with that
// token. Replacing it keeps the old one as the secret, so they are still
// accepted, and it is not shown as one.
func TestReplacingTheTokenOfAnOlderConnectionKeepsItsHooksAccepted(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	const old = "ghp_faketokenfortests2222222222222222"
	const replacement = "ghp_faketokenfortests3333333333333333"
	sealed, err := h.keyring.Seal([]byte(`{"token":"`+old+`"}`), gitSourceContext(acme.team.ID, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	source := store.GitSource{TeamID: acme.team.ID, Kind: "github_pat", Name: "legacy", ConfigEnc: sealed}
	if err := h.db.CreateGitSource(t.Context(), &source); err != nil {
		t.Fatal(err)
	}
	if code := h.signedPing(source.ID, old); code != http.StatusOK {
		t.Fatalf("before: a delivery signed with the token answered %d", code)
	}
	path := "/api/teams/" + acme.team.ID + "/git-sources/" + source.ID
	if status, body := h.do(acme, http.MethodPatch, path, map[string]string{"token": replacement}); status != http.StatusOK {
		t.Fatalf("replacing the token answered %d: %s", status, body)
	}
	if code := h.signedPing(source.ID, old); code != http.StatusOK {
		t.Errorf("a hook signed with the old token answered %d after it was replaced", code)
	}
	if code := h.signedPing(source.ID, replacement); code != http.StatusUnauthorized {
		t.Errorf("a delivery signed with the new token answered %d: the secret is not the token any more", code)
	}
	if got := h.storedToken(source.ID); got != replacement {
		t.Errorf("the panel reads the connection with %q, want the new token", got)
	}
	status, body := h.do(acme, http.MethodGet, path+"/webhook", nil)
	if status != http.StatusOK || strings.Contains(body, old) || !strings.Contains(body, `"secret_is_token":true`) {
		t.Errorf("reading the webhook answered %d: %s", status, body)
	}

	// Replaced again, the secret is the first token still.
	if status, body := h.do(acme, http.MethodPatch, path, map[string]string{"token": "ghp_faketokenfortests4444444444444444"}); status != http.StatusOK {
		t.Fatalf("replacing it again answered %d: %s", status, body)
	}
	if code := h.signedPing(source.ID, old); code != http.StatusOK {
		t.Errorf("after a second replacement a hook signed with the first token answered %d", code)
	}
}

// The name is part of what a connection is sealed with, and the CLI, a
// blueprint's git: and an assistant find a connection by it.
func TestRenamingAGitConnectionSealsItAgainAndKeepsNamesApart(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	const token = "ghp_faketokenfortests5555555555555555"
	status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/git-sources",
		map[string]string{"kind": "github_pat", "name": "acme-github", "token": token})
	var created struct {
		Source        store.GitSource `json:"source"`
		WebhookSecret string          `json:"webhook_secret"`
	}
	if err := json.Unmarshal([]byte(body), &created); status != http.StatusCreated || err != nil {
		t.Fatalf("connecting answered %d: %s", status, body)
	}
	h.connection(acme, "Work GitLab", "")
	path := "/api/teams/" + acme.team.ID + "/git-sources/" + created.Source.ID

	if status, body := h.do(acme, http.MethodPatch, path, map[string]string{"name": "Work GitHub"}); status != http.StatusOK {
		t.Fatalf("renaming answered %d: %s", status, body)
	}
	renamed, err := h.db.GetGitSource(t.Context(), created.Source.ID)
	if err != nil || renamed.Name != "Work GitHub" {
		t.Fatalf("the connection is %+v (%v)", renamed, err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if secret, err := h.api.webhookSecretFor(request, renamed); err != nil || secret != created.WebhookSecret {
		t.Errorf("the webhook secret after the rename is %q (%v)", secret, err)
	}
	if got := h.storedToken(created.Source.ID); got != token {
		t.Errorf("the token after the rename is %q", got)
	}
	if _, err := h.keyring.Open(renamed.ConfigEnc, gitSourceContext(acme.team.ID, "acme-github")); err == nil {
		t.Error("the settings still open under the old name")
	}

	status, body = h.do(acme, http.MethodPatch, path, map[string]string{"name": "work gitlab", "token": "ghp_faketokenfortests6666666666666666"})
	if status != http.StatusConflict {
		t.Errorf("renaming onto another connection's name answered %d: %s", status, body)
	}
	if after, _ := h.db.GetGitSource(t.Context(), created.Source.ID); after.Name != "Work GitHub" || h.storedToken(after.ID) != token {
		t.Errorf("a refused rename changed the connection: %+v", after)
	}
}
