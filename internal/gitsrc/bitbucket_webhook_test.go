package gitsrc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// Bitbucket's webhooks, read from payloads built the way Atlassian documents
// them (https://support.atlassian.com/bitbucket-cloud/docs/event-payloads/):
// the Repository and Account entities, a push as a list of changes each with a
// new and an old ref, and a pull request with its source and destination.

const (
	bbHead   = "709d658dc5b6d6afcd46049c2f332ee3f515a67d"
	bbBefore = "1e65c05c1d5171631d92438a13901ca7dae9618c"
	bbShop   = `{
		"type": "repository",
		"full_name": "acme/shop",
		"name": "shop",
		"uuid": "{673a6070-3421-46c9-9d48-90745f7bfe8e}",
		"is_private": true,
		"scm": "git",
		"workspace": {"type": "workspace", "slug": "acme", "name": "Acme", "uuid": "{a1b2c3d4-0000-4000-8000-000000000001}"},
		"project": {"type": "project", "key": "SHOP", "name": "Shop", "uuid": "{3b7898dc-6891-4225-ae60-24613bb83080}"},
		"links": {
			"self": {"href": "https://api.bitbucket.org/2.0/repositories/acme/shop"},
			"html": {"href": "https://bitbucket.org/acme/shop"},
			"avatar": {"href": "https://bytebucket.org/ravatar/%7B673a6070%7D?ts=default"}
		}
	}`
	bbFork = `{
		"type": "repository",
		"full_name": "stranger/shop",
		"name": "shop",
		"uuid": "{0f0f0f0f-1111-4222-8333-444444444444}",
		"is_private": false,
		"scm": "git",
		"links": {"html": {"href": "https://bitbucket.org/stranger/shop"}}
	}`
	bbActor = `{
		"type": "user",
		"display_name": "Ada Lovelace",
		"uuid": "{d301aafa-d676-4ee0-88be-962be7417567}",
		"account_id": "557058:c0b72ad0-1cb5-4018-9cdc-0cde8492c443",
		"nickname": "ada"
	}`
)

// bbRef is one side of a change: a branch or a tag pointing at a commit.
func bbRef(kind, name, hash, message string) string {
	return `{
		"type": "` + kind + `",
		"name": "` + name + `",
		"target": {
			"type": "commit",
			"hash": "` + hash + `",
			"message": ` + quote(message) + `,
			"date": "2026-09-29T10:15:00+00:00",
			"author": {"type": "author", "raw": "Ada Lovelace <ada@example.test>", "user": ` + bbActor + `},
			"parents": [{"type": "commit", "hash": "` + bbBefore + `"}],
			"links": {"html": {"href": "https://bitbucket.org/acme/shop/commits/` + hash + `"}}
		},
		"links": {"html": {"href": "https://bitbucket.org/acme/shop/branch/` + name + `"}}
	}`
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

func bbChange(newRef, oldRef string, created, closed, forced bool) string {
	flag := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	return `{
		"new": ` + newRef + `,
		"old": ` + oldRef + `,
		"links": {"html": {"href": "https://bitbucket.org/acme/shop/branches/compare/a..b"}},
		"created": ` + flag(created) + `,
		"closed": ` + flag(closed) + `,
		"forced": ` + flag(forced) + `,
		"commits": [{"type": "commit", "hash": "` + bbHead + `", "message": "the newest\n"}],
		"truncated": false
	}`
}

func bbPush(changes ...string) []byte {
	return []byte(`{"actor": ` + bbActor + `, "repository": ` + bbShop + `, "push": {"changes": [` + strings.Join(changes, ",") + `]}}`)
}

func bbPullRequest(state, sourceRepo string) []byte {
	return []byte(`{
		"actor": ` + bbActor + `,
		"repository": ` + bbShop + `,
		"pullrequest": {
			"id": 42,
			"title": "Faster checkout",
			"description": "Makes the checkout faster.",
			"state": "` + state + `",
			"draft": false,
			"author": ` + bbActor + `,
			"source": {"branch": {"name": "feature/checkout"}, "commit": {"hash": "d3022fc0ca3d"}, "repository": ` + sourceRepo + `},
			"destination": {"branch": {"name": "main"}, "commit": {"hash": "ce5965ddd289"}, "repository": ` + bbShop + `},
			"merge_commit": null,
			"participants": [],
			"reviewers": [],
			"close_source_branch": true,
			"created_on": "2026-09-29T10:15:00.179678+00:00",
			"updated_on": "2026-09-29T10:16:00.205705+00:00",
			"links": {"html": {"href": "https://bitbucket.org/acme/shop/pull-requests/42"}}
		}
	}`)
}

func bbHeader(event string) http.Header {
	header := http.Header{}
	header.Set("X-Event-Key", event)
	header.Set("X-Hook-UUID", "{e2a1ad6c-0000-4000-8000-000000000001}")
	header.Set("X-Request-UUID", "{a0b1c2d3-0000-4000-8000-000000000002}")
	header.Set("X-Attempt-Number", "1")
	return header
}

func TestABitbucketPushToABranchIsAPush(t *testing.T) {
	events, err := ParseWebhookEvents(bbHeader("repo:push"), bbPush(bbChange(
		bbRef("branch", "main", bbHead, "Speed up the checkout\n\nIt was slow."),
		bbRef("branch", "main", bbBefore, "old commit message\n"),
		false, false, false)))
	if err != nil {
		t.Fatalf("ParseWebhookEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("one change became %d events", len(events))
	}
	ev := events[0]
	if ev.Kind != "push" || ev.Branch != "main" || ev.CommitSHA != bbHead || ev.Before != bbBefore {
		t.Fatalf("read as %+v", ev)
	}
	if ev.RepoURL != "https://bitbucket.org/acme/shop" {
		t.Errorf("the repository is %q, which no app stores", ev.RepoURL)
	}
	if ev.CommitMessage != "Speed up the checkout" || ev.CommitAuthor != "Ada Lovelace" {
		t.Errorf("the commit reads as %q by %q", ev.CommitMessage, ev.CommitAuthor)
	}
	// Bitbucket lists no files, so watch paths cannot skip anything: an app
	// that watches a path deploys, as it does for GitLab and Gitea.
	if ev.FilesKnown || len(ev.ChangedFiles) != 0 {
		t.Errorf("a Bitbucket push claims to know its files: %v", ev.ChangedFiles)
	}
	if ev.Deleted || ev.Forced || ev.SkipMarker != "" || ev.Fork {
		t.Errorf("an ordinary push reads as %+v", ev)
	}
	if !ev.Touches([]string{"apps/api/**"}) {
		t.Error("a push whose files are unknown was said not to touch a watched path")
	}

	// A force push says so, which is somebody going back on purpose.
	events, err = ParseWebhookEvents(bbHeader("repo:push"), bbPush(bbChange(
		bbRef("branch", "main", bbHead, "rewritten"), bbRef("branch", "main", bbBefore, "old"), false, false, true)))
	if err != nil || !events[0].Forced {
		t.Fatalf("a forced push reads as %+v, %v", events, err)
	}

	// A new branch has no old side.
	events, err = ParseWebhookEvents(bbHeader("repo:push"), bbPush(bbChange(
		bbRef("branch", "feature/x", bbHead, "start"), "null", true, false, false)))
	if err != nil || events[0].Branch != "feature/x" || events[0].Before != "" || events[0].CommitSHA != bbHead {
		t.Fatalf("a new branch reads as %+v, %v", events, err)
	}

	// ParseWebhook, which the other hosts' callers use, reads it too.
	single, err := ParseWebhook(bbHeader("repo:push"), bbPush(bbChange(
		bbRef("branch", "main", bbHead, "m"), bbRef("branch", "main", bbBefore, "o"), false, false, false)))
	if err != nil || single.Branch != "main" {
		t.Fatalf("ParseWebhook read %+v, %v", single, err)
	}
}

func TestABitbucketPushSaysSkipCI(t *testing.T) {
	events, err := ParseWebhookEvents(bbHeader("repo:push"), bbPush(bbChange(
		bbRef("branch", "main", bbHead, "Bump the changelog\n\n[skip ci]"),
		bbRef("branch", "main", bbBefore, "old"), false, false, false)))
	if err != nil {
		t.Fatalf("ParseWebhookEvents: %v", err)
	}
	if events[0].SkipMarker != "[skip ci]" {
		t.Fatalf("the marker in the newest commit's message was read as %q", events[0].SkipMarker)
	}
}

func TestABitbucketTagIsATag(t *testing.T) {
	for _, kind := range []string{"tag", "annotated_tag"} {
		events, err := ParseWebhookEvents(bbHeader("repo:push"), bbPush(bbChange(
			bbRef(kind, "v1.4.0", bbHead, "Release 1.4.0\n"), "null", true, false, false)))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		ev := events[0]
		if ev.Kind != "tag" || ev.Tag != "v1.4.0" || ev.CommitSHA != bbHead || ev.Branch != "" {
			t.Fatalf("%s reads as %+v", kind, ev)
		}
		// A tag is somebody releasing on purpose: its commit's marker is not
		// read, the same as on every other host.
		if ev.SkipMarker != "" {
			t.Errorf("%s carried a skip marker", kind)
		}
	}

	// A tag taken away deploys nothing.
	_, err := ParseWebhookEvents(bbHeader("repo:push"), bbPush(bbChange(
		"null", bbRef("tag", "v1.4.0", bbHead, "Release"), false, true, false)))
	if !errors.Is(err, ErrUnsupportedEvent) {
		t.Fatalf("a deleted tag answered %v", err)
	}
}

func TestADeletedBitbucketBranchTakesItsPreviewAway(t *testing.T) {
	events, err := ParseWebhookEvents(bbHeader("repo:push"), bbPush(bbChange(
		"null", bbRef("branch", "feature/checkout", bbBefore, "last"), false, true, false)))
	if err != nil {
		t.Fatalf("ParseWebhookEvents: %v", err)
	}
	ev := events[0]
	if ev.Kind != "push" || !ev.Deleted || ev.Branch != "feature/checkout" || ev.CommitSHA != "" {
		t.Fatalf("a deleted branch reads as %+v", ev)
	}
}

// git push --follow-tags sends the branch and the tag in one push, and
// Bitbucket in one delivery. Reading only the first change lost the release.
func TestEveryRefInABitbucketPushIsRead(t *testing.T) {
	events, err := ParseWebhookEvents(bbHeader("repo:push"), bbPush(
		bbChange(bbRef("branch", "main", bbHead, "Release 2.0"), bbRef("branch", "main", bbBefore, "o"), false, false, false),
		bbChange(bbRef("tag", "v2.0.0", bbHead, "Release 2.0"), "null", true, false, false),
		// A Mercurial bookmark, which Bitbucket no longer hosts, is nothing.
		bbChange(bbRef("bookmark", "old", bbHead, "x"), "null", true, false, false),
	))
	if err != nil {
		t.Fatalf("ParseWebhookEvents: %v", err)
	}
	if len(events) != 2 || events[0].Kind != "push" || events[1].Kind != "tag" || events[1].Tag != "v2.0.0" {
		t.Fatalf("a branch and a tag pushed together read as %+v", events)
	}
}

func TestABitbucketPullRequestFromTheRepositoryItself(t *testing.T) {
	for _, event := range []string{"pullrequest:created", "pullrequest:updated"} {
		events, err := ParseWebhookEvents(bbHeader(event), bbPullRequest("OPEN", bbShop))
		if err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		ev := events[0]
		if ev.Kind != "pull_request_opened" || ev.PullRequest != 42 || ev.SourceBranch != "feature/checkout" ||
			ev.Branch != "feature/checkout" || ev.RepoURL != "https://bitbucket.org/acme/shop" {
			t.Fatalf("%s reads as %+v", event, ev)
		}
		if ev.Fork || ev.NoPreview != "" {
			t.Errorf("%s from the repository itself was treated as a fork: %+v", event, ev)
		}
		// The webhook names the head by twelve characters. It is passed on
		// as it is, and FullCommit says it is not one a build can fetch.
		if ev.CommitSHA != "d3022fc0ca3d" || FullCommit(ev.CommitSHA) {
			t.Errorf("%s: the head commit is %q", event, ev.CommitSHA)
		}
		if ev.CommitMessage != "Faster checkout" || ev.CommitAuthor != "Ada Lovelace" {
			t.Errorf("%s: the pull request reads as %q by %q", event, ev.CommitMessage, ev.CommitAuthor)
		}
	}
}

func TestABitbucketForkIsRecognised(t *testing.T) {
	events, err := ParseWebhookEvents(bbHeader("pullrequest:created"), bbPullRequest("OPEN", bbFork))
	if err != nil {
		t.Fatalf("ParseWebhookEvents: %v", err)
	}
	ev := events[0]
	if !ev.Fork {
		t.Fatal("a pull request from another repository was not treated as a fork")
	}
	// Its commits are not in this repository, so there is nothing a build
	// could fetch, and the delivery log says so.
	if ev.NoPreview == "" {
		t.Error("a fork's pull request did not say why it has no preview")
	}

	// The same name in another case is the same repository; a different id
	// is a different one, whatever it is called.
	renamed := strings.Replace(bbShop, `"full_name": "acme/shop"`, `"full_name": "Acme/Shop"`, 1)
	events, _ = ParseWebhookEvents(bbHeader("pullrequest:created"), bbPullRequest("OPEN", renamed))
	if events[0].Fork {
		t.Error("the repository itself, named in another case, was treated as a fork")
	}
	sameName := strings.Replace(bbFork, `"full_name": "stranger/shop"`, `"full_name": "acme/shop"`, 1)
	events, _ = ParseWebhookEvents(bbHeader("pullrequest:created"), bbPullRequest("OPEN", sameName))
	if !events[0].Fork {
		t.Error("a repository with another id was trusted because of its name")
	}

	// Anything missing counts as a fork.
	events, err = ParseWebhookEvents(bbHeader("pullrequest:created"), bbPullRequest("OPEN", "null"))
	if err != nil || !events[0].Fork {
		t.Fatalf("a pull request with no source repository was trusted: %+v, %v", events, err)
	}
	events, _ = ParseWebhookEvents(bbHeader("pullrequest:created"), bbPullRequest("OPEN", `{"type": "repository"}`))
	if !events[0].Fork {
		t.Error("a source repository with neither an id nor a name was trusted")
	}
}

func TestAMergedOrDeclinedBitbucketPullRequestIsClosed(t *testing.T) {
	cases := map[string]string{"pullrequest:fulfilled": "MERGED", "pullrequest:rejected": "DECLINED"}
	for event, state := range cases {
		for _, source := range []string{bbShop, bbFork} {
			events, err := ParseWebhookEvents(bbHeader(event), bbPullRequest(state, source))
			if err != nil {
				t.Fatalf("%s: %v", event, err)
			}
			ev := events[0]
			if ev.Kind != "pull_request_closed" || ev.PullRequest != 42 || ev.SourceBranch != "feature/checkout" {
				t.Fatalf("%s reads as %+v", event, ev)
			}
			if ev.NoPreview != "" {
				t.Errorf("%s: closing a pull request is not making a preview: %q", event, ev.NoPreview)
			}
		}
	}

	// An edit to one already merged does not bring its preview back.
	_, err := ParseWebhookEvents(bbHeader("pullrequest:updated"), bbPullRequest("MERGED", bbShop))
	if !errors.Is(err, ErrUnsupportedEvent) {
		t.Fatalf("an update to a merged pull request answered %v", err)
	}
}

func TestOtherBitbucketEventsAreIgnored(t *testing.T) {
	for _, event := range []string{"repo:fork", "pullrequest:approved", "pullrequest:comment_created", "repo:commit_status_updated"} {
		if _, err := ParseWebhookEvents(bbHeader(event), []byte(`{}`)); !errors.Is(err, ErrUnsupportedEvent) {
			t.Errorf("%s answered %v", event, err)
		}
	}
	events, err := ParseWebhookEvents(bbHeader("diagnostics:ping"), []byte(`{"test": true}`))
	if err != nil || events[0].Kind != "ping" {
		t.Fatalf("a test delivery reads as %+v, %v", events, err)
	}
	if _, err := ParseWebhookEvents(bbHeader("repo:push"), []byte(`{"push":`)); err == nil || errors.Is(err, ErrUnsupportedEvent) {
		t.Fatalf("a broken payload answered %v", err)
	}
}

func TestTheBitbucketSignatureIsChecked(t *testing.T) {
	// Atlassian's own example: https://support.atlassian.com/bitbucket-cloud/docs/manage-webhooks/
	const (
		secret  = "It's a Secret to Everybody"
		payload = "Hello World!"
		header  = "sha256=a4771c39fbe90f317c7824e83ddef3caae9cb3d976c214ace1f2937e133263c9"
	)
	if err := VerifyBitbucketSignature(secret, []byte(payload), header); err != nil {
		t.Fatalf("Atlassian's example does not verify: %v", err)
	}
	// Hex is hex in either case.
	if err := VerifyBitbucketSignature(secret, []byte(payload), strings.ToUpper(header[:6])+header[6:]); err != nil {
		t.Errorf("SHA256= was refused: %v", err)
	}

	sign := func(body string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		return hex.EncodeToString(mac.Sum(nil))
	}
	refused := map[string]string{
		"another body":        "sha256=" + sign("Hello World?"),
		"another secret":      "sha256=" + hmacHex("not the secret", payload),
		"no header":           "",
		"no method":           sign(payload),
		"sha1":                "sha1=" + sign(payload),
		"the wrong method":    "sha512=" + sign(payload),
		"an empty signature":  "sha256=",
		"the method alone":    "sha256",
		"the secret in clear": "sha256=" + secret,
	}
	for name, header := range refused {
		if err := VerifyBitbucketSignature(secret, []byte(payload), header); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: answered %v", name, err)
		}
	}
	// A connection with no secret verifies nothing, rather than everything.
	if err := VerifyBitbucketSignature("", []byte(payload), header); err == nil {
		t.Error("a connection with no secret accepted a delivery")
	}
}

func hmacHex(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}
