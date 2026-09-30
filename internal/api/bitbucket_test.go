package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// A Bitbucket connection, from the form to a deploy: its token checked before
// it is kept, sealed and never shown again, its deliveries believed only when
// they are signed with its secret, and nobody outside the team able to use it.

const (
	bitbucketTestToken = "ATATT3xFfGF0-fake-bitbucket-api-token"
	bitbucketTestEmail = "ada@example.test"
	bitbucketTestRepo  = "https://bitbucket.org/acme/shop"
	bitbucketHead      = "709d658dc5b6d6afcd46049c2f332ee3f515a67d"
)

// fakeBitbucketCheck stands in for Bitbucket's answer to the token check.
type fakeBitbucketCheck struct {
	mu     sync.Mutex
	asked  []gitsrc.ListRequest
	answer error
}

func (f *fakeBitbucketCheck) check(_ context.Context, req gitsrc.ListRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, req)
	return f.answer
}

func stubBitbucketCheck(t *testing.T) *fakeBitbucketCheck {
	t.Helper()
	fake := &fakeBitbucketCheck{}
	previous := checkBitbucketToken
	checkBitbucketToken = fake.check
	t.Cleanup(func() { checkBitbucketToken = previous })
	return fake
}

// connectBitbucket makes a Bitbucket connection through the API, the way the
// form does, and returns it and its webhook secret.
func connectBitbucket(t *testing.T, h *harness, as tenant, body map[string]string) (store.GitSource, string) {
	t.Helper()
	status, answer := h.do(as, http.MethodPost, "/api/teams/"+as.team.ID+"/git-sources", body)
	if status != http.StatusCreated {
		t.Fatalf("connecting Bitbucket answered %d: %s", status, answer)
	}
	var created struct {
		Source        store.GitSource `json:"source"`
		WebhookSecret string          `json:"webhook_secret"`
	}
	if err := json.Unmarshal([]byte(answer), &created); err != nil {
		t.Fatal(err)
	}
	source, err := h.db.GetGitSource(t.Context(), created.Source.ID)
	if err != nil {
		t.Fatal(err)
	}
	return source, created.WebhookSecret
}

func TestABitbucketConnectionIsCheckedSealedAndNeverShown(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	check := stubBitbucketCheck(t)

	status, answer := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/git-sources", map[string]string{
		"kind": "bitbucket", "name": "acme-bitbucket", "token": " " + bitbucketTestToken + " ",
		"email": bitbucketTestEmail, "account": "acme",
	})
	if status != http.StatusCreated {
		t.Fatalf("connecting answered %d: %s", status, answer)
	}
	if len(check.asked) != 1 {
		t.Fatalf("the token was checked %d times before it was saved", len(check.asked))
	}
	asked := check.asked[0]
	if asked.Token != bitbucketTestToken || asked.Email != bitbucketTestEmail || asked.Account != "acme" ||
		asked.BaseURL != gitsrc.BitbucketURL {
		t.Errorf("Bitbucket was asked with %+v", asked)
	}

	var created struct {
		Source        store.GitSource `json:"source"`
		WebhookSecret string          `json:"webhook_secret"`
	}
	if err := json.Unmarshal([]byte(answer), &created); err != nil {
		t.Fatal(err)
	}
	if created.Source.Kind != "bitbucket" || created.Source.BaseURL != gitsrc.BitbucketURL || created.WebhookSecret == "" {
		t.Fatalf("the connection reads as %+v", created)
	}
	source, err := h.db.GetGitSource(t.Context(), created.Source.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Sealed, and sealed to where it is stored.
	if strings.Contains(source.ConfigEnc, bitbucketTestToken) || strings.Contains(source.ConfigEnc, bitbucketTestEmail) {
		t.Fatal("the token or the email is stored in the clear")
	}
	raw, err := h.keyring.Open(source.ConfigEnc, "git_source:"+acme.team.ID+":"+source.Name)
	if err != nil {
		t.Fatalf("the configuration does not open where it is stored: %v", err)
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if config["token"] != bitbucketTestToken || config["email"] != bitbucketTestEmail || config["webhook_secret"] != created.WebhookSecret {
		t.Errorf("sealed as %v", config)
	}
	if _, err := h.keyring.Open(source.ConfigEnc, "git_source:"+acme.team.ID+":another"); err == nil {
		t.Error("the configuration opens under another connection's name")
	}

	// No answer carries the token or the email: not the one that made it, not
	// the list, not the webhook.
	for _, path := range []string{
		"/api/teams/" + acme.team.ID + "/git-sources",
		"/api/teams/" + acme.team.ID + "/git-sources/" + source.ID + "/webhook",
	} {
		status, body := h.do(acme, http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("%s answered %d", path, status)
		}
		if strings.Contains(body, bitbucketTestToken) || strings.Contains(body, bitbucketTestEmail) {
			t.Errorf("%s shows the credential: %s", path, body)
		}
	}
	if strings.Contains(answer, bitbucketTestToken) || strings.Contains(answer, bitbucketTestEmail) {
		t.Errorf("connecting answered with the credential: %s", answer)
	}
}

func TestABitbucketConnectionThatDoesNotWorkIsNotSaved(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	check := stubBitbucketCheck(t)
	path := "/api/teams/" + acme.team.ID + "/git-sources"
	connect := func(body map[string]string) (int, string) {
		base := map[string]string{"kind": "bitbucket", "token": bitbucketTestToken}
		for k, v := range body {
			base[k] = v
		}
		return h.do(acme, http.MethodPost, path, base)
	}

	for _, tc := range []struct {
		name   string
		answer error
		body   map[string]string
		status int
		code   string
	}{
		{"a token Bitbucket refuses", &gitsrc.HostError{Status: http.StatusUnauthorized}, nil,
			http.StatusBadRequest, "git.bitbucket_token_refused"},
		{"a token without the scopes", &gitsrc.HostError{Status: http.StatusForbidden}, nil,
			http.StatusBadRequest, "git.bitbucket_token_refused"},
		{"a workspace the token cannot see", &gitsrc.HostError{Status: http.StatusNotFound}, map[string]string{"account": "nobody"},
			http.StatusBadRequest, "git.bitbucket_workspace_unknown"},
		{"Bitbucket not answering", errors.New("could not reach the Git host: timeout"), nil,
			http.StatusBadGateway, "git.bitbucket_check_failed"},
	} {
		check.answer = tc.answer
		status, body := connect(tc.body)
		if status != tc.status || !strings.Contains(body, `"code":"`+tc.code+`"`) {
			t.Errorf("%s answered %d: %s", tc.name, status, truncate(body, 300))
		}
	}
	check.answer = nil
	asked := len(check.asked)

	// Refused before Bitbucket is asked anything.
	for name, body := range map[string]map[string]string{
		"another server":  {"base_url": "https://bitbucket.example.test"},
		"not an email":    {"email": "ada"},
		"an email with :": {"email": "ada:x@example.test"},
		"a path":          {"account": "../admin"},
		"no token":        {"token": ""},
	} {
		if status, answer := connect(body); status != http.StatusBadRequest {
			t.Errorf("%s answered %d: %s", name, status, truncate(answer, 200))
		}
	}
	if len(check.asked) != asked {
		t.Error("Bitbucket was asked about a connection the panel had already refused")
	}
	sources, _ := h.db.ListGitSources(t.Context(), acme.team.ID)
	if len(sources) != 0 {
		t.Fatalf("%d connections were saved", len(sources))
	}

	// Only an administrator connects an account.
	member := h.newMember(acme, "dev", store.RoleMember)
	if status, _ := h.do(member, http.MethodPost, path, map[string]string{"kind": "bitbucket", "token": bitbucketTestToken}); status != http.StatusForbidden {
		t.Fatalf("a member connected Bitbucket: %d", status)
	}
}

// deliverBitbucket sends a delivery the way Bitbucket does, signed with a
// header the test chooses.
func deliverBitbucket(h *harness, sourceID, event string, payload []byte, headers map[string]string) (int, webhookResult) {
	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/git/"+sourceID, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Event-Key", event)
	request.Header.Set("X-Request-UUID", "{a0b1c2d3-0000-4000-8000-000000000002}")
	for k, v := range headers {
		request.Header.Set(k, v)
	}
	recorder := httptest.NewRecorder()
	h.api.ServeHTTP(recorder, request)
	var result webhookResult
	_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	return recorder.Code, result
}

func bitbucketSignature(newHash func() hash.Hash, method, secret string, payload []byte) string {
	mac := hmac.New(newHash, []byte(secret))
	mac.Write(payload)
	return method + "=" + hex.EncodeToString(mac.Sum(nil))
}

func bitbucketPushPayload(changes ...string) []byte {
	return []byte(`{
		"actor": {"type": "user", "display_name": "Ada Lovelace"},
		"repository": {"type": "repository", "full_name": "acme/shop", "uuid": "{shop}",
			"links": {"html": {"href": "https://bitbucket.org/acme/shop"}}},
		"push": {"changes": [` + strings.Join(changes, ",") + `]}
	}`)
}

func bitbucketChange(kind, name, hash string) string {
	return `{"new": {"type": "` + kind + `", "name": "` + name + `", "target": {"type": "commit", "hash": "` + hash +
		`", "message": "Release\n", "author": {"raw": "Ada <ada@example.test>"}}}, "old": null,
		"created": true, "closed": false, "forced": false, "commits": [], "truncated": false}`
}

func TestABitbucketDeliveryIsBelievedOnlyWhenSigned(t *testing.T) {
	h := newHarness(t)
	deploys := &requestLog{}
	h.api.deployer = deploys
	acme := h.newTenant("acme")
	stubBitbucketCheck(t)
	source, secret := connectBitbucket(t, h, acme, map[string]string{"kind": "bitbucket", "token": bitbucketTestToken})
	app := store.App{EnvironmentID: acme.env.ID, Name: "web", Slug: "web", Replicas: 1, SourceType: "git",
		RepoURL: bitbucketTestRepo, Branch: "main", AutoDeploy: true, GitSourceID: source.ID}
	if err := h.db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}

	// A branch and a tag pushed together, as git push --follow-tags does.
	payload := bitbucketPushPayload(bitbucketChange("branch", "main", bitbucketHead), bitbucketChange("tag", "v1.0.0", bitbucketHead))
	signed := bitbucketSignature(sha256.New, "sha256", secret, payload)

	for name, headers := range map[string]map[string]string{
		"no signature":        nil,
		"another secret":      {"X-Hub-Signature": bitbucketSignature(sha256.New, "sha256", "not-the-secret", payload)},
		"no method":           {"X-Hub-Signature": strings.TrimPrefix(signed, "sha256=")},
		"sha1":                {"X-Hub-Signature": bitbucketSignature(sha1.New, "sha1", secret, payload)},
		"signed by the token": {"X-Hub-Signature": bitbucketSignature(sha256.New, "sha256", bitbucketTestToken, payload)},
		// GitHub's header, rightly signed, on a Bitbucket connection: how a
		// delivery is checked is the connection's kind, not the sender's
		// choice of header.
		"GitHub's header": {"X-Hub-Signature-256": signed},
	} {
		if code, _ := deliverBitbucket(h, source.ID, "repo:push", payload, headers); code != http.StatusUnauthorized {
			t.Errorf("a delivery with %s answered %d", name, code)
		}
	}
	if len(deploys.all()) != 0 {
		t.Fatalf("an unverified delivery deployed: %+v", deploys.all())
	}

	code, result := deliverBitbucket(h, source.ID, "repo:push", payload, map[string]string{"X-Hub-Signature": signed})
	if code != http.StatusAccepted || result.Status != "accepted" {
		t.Fatalf("a signed delivery answered %d: %+v", code, result)
	}
	got := deploys.all()
	if len(got) != 1 || got[0].CommitSHA != bitbucketHead || got[0].Trigger != "push" || got[0].AppID != app.ID {
		t.Fatalf("the push deployed %+v", got)
	}
	// Both refs were read: the tag is named in the delivery log too.
	if !slices.Contains(result.Skipped, "web (deploys pushes to its branch, not tags)") {
		t.Errorf("the tag pushed with the branch was not read: %v", result.Skipped)
	}

	// Bitbucket's own events it does not act on are answered, not failed.
	other := []byte(`{"repository": {"full_name": "acme/shop"}}`)
	code, _ = deliverBitbucket(h, source.ID, "repo:fork", other,
		map[string]string{"X-Hub-Signature": bitbucketSignature(sha256.New, "sha256", secret, other)})
	if code != http.StatusOK {
		t.Errorf("an event Skifity does not act on answered %d", code)
	}
}

func bitbucketPullRequestPayload(event, state, sourceRepo string) []byte {
	return []byte(`{
		"actor": {"type": "user", "display_name": "Ada Lovelace"},
		"repository": {"type": "repository", "full_name": "acme/shop", "uuid": "{shop}",
			"links": {"html": {"href": "https://bitbucket.org/acme/shop"}}},
		"pullrequest": {
			"id": 42, "title": "Faster checkout", "state": "` + state + `",
			"author": {"type": "user", "display_name": "Ada Lovelace"},
			"source": {"branch": {"name": "feature/checkout"}, "commit": {"hash": "d3022fc0ca3d"}, "repository": ` + sourceRepo + `},
			"destination": {"branch": {"name": "main"}, "commit": {"hash": "ce5965ddd289"},
				"repository": {"type": "repository", "full_name": "acme/shop", "uuid": "{shop}"}}
		}
	}`)
}

func TestABitbucketPullRequestIsPreviewedFromItsWholeCommit(t *testing.T) {
	h := newHarness(t)
	deploys := &requestLog{}
	h.api.deployer = deploys
	acme := h.newTenant("acme")
	stubBitbucketCheck(t)
	source, secret := connectBitbucket(t, h, acme, map[string]string{
		"kind": "bitbucket", "token": bitbucketTestToken, "email": bitbucketTestEmail})
	app := store.App{EnvironmentID: acme.env.ID, Name: "web", Slug: "web", Replicas: 1, SourceType: "git",
		RepoURL: bitbucketTestRepo, Branch: "main", AutoDeploy: true, PreviewDeploys: true, GitSourceID: source.ID}
	if err := h.db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}

	var resolved []string
	var resolvedWith gitsrc.ListRequest
	resolveAnswer := error(nil)
	previous := resolveBitbucketCommit
	resolveBitbucketCommit = func(_ context.Context, req gitsrc.ListRequest, fullName, short string) (string, error) {
		resolved = append(resolved, fullName+"@"+short)
		resolvedWith = req
		if resolveAnswer != nil {
			return "", resolveAnswer
		}
		return short + strings.Repeat("0", 40-len(short)), nil
	}
	t.Cleanup(func() { resolveBitbucketCommit = previous })

	deliver := func(event, state, sourceRepo string) webhookResult {
		t.Helper()
		payload := bitbucketPullRequestPayload(event, state, sourceRepo)
		code, result := deliverBitbucket(h, source.ID, event, payload,
			map[string]string{"X-Hub-Signature": bitbucketSignature(sha256.New, "sha256", secret, payload)})
		if code != http.StatusAccepted && code != http.StatusOK {
			t.Fatalf("%s answered %d", event, code)
		}
		return result
	}
	const sameRepo = `{"type": "repository", "full_name": "acme/shop", "uuid": "{shop}"}`
	const fork = `{"type": "repository", "full_name": "stranger/shop", "uuid": "{stranger}"}`

	// The webhook names the head by twelve characters; the build is given
	// the whole commit, asked of Bitbucket with the connection's credential.
	result := deliver("pullrequest:created", "OPEN", sameRepo)
	got := deploys.all()
	if len(got) != 1 || got[0].Trigger != "preview" || got[0].CommitSHA != "d3022fc0ca3d"+strings.Repeat("0", 28) {
		t.Fatalf("the pull request deployed %+v (%+v)", got, result)
	}
	if len(resolved) != 1 || resolved[0] != "acme/shop@d3022fc0ca3d" ||
		resolvedWith.Token != bitbucketTestToken || resolvedWith.Email != bitbucketTestEmail {
		t.Errorf("the commit was asked for as %v with %+v", resolved, resolvedWith)
	}

	// When Bitbucket cannot say, the preview builds its branch rather than a
	// commit nobody can fetch.
	resolveAnswer = errors.New("Bitbucket is down")
	deliver("pullrequest:updated", "OPEN", sameRepo)
	got = deploys.all()
	if len(got) != 2 || got[1].CommitSHA != "" {
		t.Fatalf("an update whose commit could not be found deployed %+v", got[len(got)-1])
	}

	// A fork's commits are not in this repository: no build, no lookup, and
	// the delivery log says why.
	asked := len(resolved)
	result = deliver("pullrequest:created", "OPEN", fork)
	if len(deploys.all()) != 2 || len(resolved) != asked {
		t.Fatalf("a fork's pull request deployed or was looked up: %+v", deploys.all())
	}
	if !slices.Contains(result.Skipped, "web ("+"Bitbucket Cloud does not make a fork's commits fetchable from the repository "+
		"a pull request targets, so it has no preview)") {
		t.Errorf("the fork's skip was not said: %v", result.Skipped)
	}

	// Merged: the preview goes.
	envs, _ := h.db.ListEnvironments(t.Context(), acme.project.ID)
	previews := 0
	for _, env := range envs {
		if env.Kind == store.EnvPreview {
			previews++
		}
	}
	if previews != 1 {
		t.Fatalf("%d previews before the merge", previews)
	}
	deliver("pullrequest:fulfilled", "MERGED", sameRepo)
	envs, _ = h.db.ListEnvironments(t.Context(), acme.project.ID)
	for _, env := range envs {
		if env.Kind == store.EnvPreview {
			t.Fatalf("the preview outlived its merged pull request: %s", env.Name)
		}
	}
}

// Another team cannot use a Bitbucket connection: not to list what it reads,
// not to build from, and not — through a delivery to it — to deploy their app.
func TestAnotherTeamCannotUseABitbucketConnection(t *testing.T) {
	h := newHarness(t)
	deploys := &requestLog{}
	h.api.deployer = deploys
	acme := h.newTenant("acme")
	rival := h.newTenant("rival")
	stubBitbucketCheck(t)
	source, secret := connectBitbucket(t, h, acme, map[string]string{"kind": "bitbucket", "token": bitbucketTestToken, "account": "acme"})

	for _, path := range []string{
		"/api/teams/" + rival.team.ID + "/git-sources/" + source.ID + "/repositories",
		"/api/teams/" + acme.team.ID + "/git-sources/" + source.ID + "/repositories",
		"/api/teams/" + rival.team.ID + "/git-sources/" + source.ID + "/branches?repo=acme/shop",
		"/api/teams/" + acme.team.ID + "/git-sources/" + source.ID + "/webhook",
		"/api/teams/" + rival.team.ID + "/git-sources/" + source.ID + "/webhook",
	} {
		status, body := h.do(rival, http.MethodGet, path, nil)
		if status == http.StatusOK || strings.Contains(body, secret) || strings.Contains(body, bitbucketTestToken) {
			t.Errorf("another team asked %s and was answered %d: %s", path, status, truncate(body, 200))
		}
	}
	status, _ := h.do(rival, http.MethodPost, "/api/environments/"+rival.env.ID+"/apps", map[string]any{
		"name": "stolen", "source_type": "git", "repo_url": bitbucketTestRepo, "git_source_id": source.ID,
	})
	if status != http.StatusNotFound {
		t.Fatalf("an app with another team's Bitbucket connection answered %d", status)
	}
	if status, _ := h.do(rival, http.MethodDelete, "/api/teams/"+acme.team.ID+"/git-sources/"+source.ID, nil); status == http.StatusOK {
		t.Fatal("another team removed the connection")
	}

	// Their app builds from the same repository, with a connection of their
	// own. A signed delivery to acme's connection does not deploy it.
	theirs := store.App{EnvironmentID: rival.env.ID, Name: "shop", Slug: "shop", Replicas: 1, SourceType: "git",
		RepoURL: bitbucketTestRepo, Branch: "main", AutoDeploy: true}
	if err := h.db.CreateApp(t.Context(), &theirs); err != nil {
		t.Fatal(err)
	}
	payload := bitbucketPushPayload(bitbucketChange("branch", "main", bitbucketHead))
	code, result := deliverBitbucket(h, source.ID, "repo:push", payload,
		map[string]string{"X-Hub-Signature": bitbucketSignature(sha256.New, "sha256", secret, payload)})
	if code != http.StatusAccepted || len(result.Deployments) != 0 || len(deploys.all()) != 0 {
		t.Fatalf("a delivery to acme's connection deployed another team's app: %d %+v", code, deploys.all())
	}
}
