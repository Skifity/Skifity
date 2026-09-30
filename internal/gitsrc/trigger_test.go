package gitsrc

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// "[skip ci]" is how every CI system is told a commit is not worth building,
// and it is written in every case and anywhere in the message.
func TestASkipMarkerIsFoundAnywhereInTheMessage(t *testing.T) {
	for message, want := range map[string]string{
		"docs: fix a typo [skip ci]":                   "[skip ci]",
		"Update README\n\nNothing to build. [CI SKIP]": "[ci skip]",
		"chore: bump the changelog [Skip Deploy]":      "[skip deploy]",
		"[no deploy] try the new linter":               "[no deploy]",
		"fix: the checkout button":                     "",
		// Close is not the same: a marker is the whole bracketed phrase.
		"skip ci, it is only docs":      "",
		"[skip-ci] is not the spelling": "",
	} {
		if got := SkipMarker(message); got != want {
			t.Errorf("SkipMarker(%q) = %q, want %q", message, got, want)
		}
	}
}

func TestATagPatternIsCheckedBeforeItIsStored(t *testing.T) {
	for _, good := range []string{"v*", "release-*", "v[0-9]*", "v?.*", "releases/*", "*"} {
		if err := ValidTagPattern(good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}
	for _, bad := range []string{"", "v[", "v[0-9", `v\`, "v 1", "v*\n", "v^1", "v~1", "v:1", strings.Repeat("v", 101)} {
		err := ValidTagPattern(bad)
		if err == nil {
			t.Errorf("%q was accepted as a tag pattern", bad)
			continue
		}
		if !errors.Is(err, ErrBadTagPattern) {
			t.Errorf("%q was refused with %v, which is not ErrBadTagPattern", bad, err)
		}
	}
}

func TestATagDeploysOnlyWhenItsNameMatches(t *testing.T) {
	for _, tc := range []struct {
		pattern, tag string
		want         bool
	}{
		{"v*", "v1.4.0", true},
		{"v*", "v2.0.0-rc.1", true},
		{"v*", "1.4.0", false},
		{"v*", "", false},
		{"release-*", "release-2026-09", true},
		{"release-*", "v1.4.0", false},
		{"v[0-9]*", "v1", true},
		{"v[0-9]*", "vnext", false},
		// A star stays inside one part of a name, as in a path.
		{"v*", "v1/hotfix", false},
		{"releases/*", "releases/1.0", true},
		// A pattern that is not one matches nothing rather than everything.
		{"v[", "v[", false},
	} {
		if got := TagMatches(tc.pattern, tc.tag); got != tc.want {
			t.Errorf("TagMatches(%q, %q) = %t, want %t", tc.pattern, tc.tag, got, tc.want)
		}
	}
}

// The marker is read from the head commit's whole message, for each host, and
// nowhere else: a commit further down the push that says "[skip ci]" is not the
// one being deployed.
func TestTheHeadCommitsSkipMarkerIsReadFromEveryHost(t *testing.T) {
	github := http.Header{}
	github.Set("X-GitHub-Event", "push")
	event, err := ParseWebhook(github, []byte(`{
		"ref": "refs/heads/main", "before": "aaa111", "after": "bbb222",
		"commits": [{"id": "bbb222", "message": "Update the docs\n\n[skip ci]", "modified": ["README.md"]}],
		"head_commit": {"id": "bbb222", "message": "Update the docs\n\n[skip ci]", "author": {"name": "Ann"}},
		"repository": {"clone_url": "https://github.com/acme/shop.git"}
	}`))
	if err != nil {
		t.Fatalf("GitHub: %v", err)
	}
	if event.SkipMarker != "[skip ci]" {
		t.Errorf("GitHub: the marker in the message's body was not found: %+v", event)
	}
	if event.CommitMessage != "Update the docs" {
		t.Errorf("GitHub: the history shows %q, want the first line", event.CommitMessage)
	}

	gitlab := http.Header{}
	gitlab.Set("X-Gitlab-Event", "Push Hook")
	body := func(after string) []byte {
		return []byte(`{
			"ref": "refs/heads/main", "before": "aaa111", "after": "` + after + `",
			"user_name": "Ann", "total_commits_count": 2,
			"project": {"git_http_url": "https://gitlab.example.test/acme/shop.git"},
			"commits": [
				{"id": "ccc333", "message": "wip [CI SKIP]"},
				{"id": "ddd444", "message": "Ship the checkout"}
			]
		}`)
	}
	event, err = ParseWebhook(gitlab, body("ccc333"))
	if err != nil {
		t.Fatalf("GitLab: %v", err)
	}
	if event.SkipMarker != "[ci skip]" || event.CommitMessage != "wip [CI SKIP]" {
		t.Errorf("GitLab: the head commit is the one whose id is after: %+v", event)
	}
	event, err = ParseWebhook(gitlab, body("ddd444"))
	if err != nil {
		t.Fatalf("GitLab: %v", err)
	}
	if event.SkipMarker != "" {
		t.Errorf("GitLab: a marker on a commit that is not the head skipped the push: %q", event.SkipMarker)
	}

	gitea := http.Header{}
	gitea.Set("X-Gitea-Event", "push")
	event, err = ParseWebhook(gitea, []byte(`{
		"ref": "refs/heads/main", "before": "aaa111", "after": "eee555",
		"head_commit": {"id": "eee555", "message": "Bump the changelog [no deploy]", "author": {"name": "Ann"}},
		"repository": {"clone_url": "https://gitea.example.test/acme/shop.git"}
	}`))
	if err != nil {
		t.Fatalf("Gitea: %v", err)
	}
	if event.Kind != "push" || event.SkipMarker != "[no deploy]" {
		t.Errorf("Gitea: %+v", event)
	}
}

// A tag push is its own kind of event, and what is built is the commit the tag
// points at — not an annotated tag's own object, which cannot be checked out
// as a commit.
func TestATagPushIsReadFromEveryHost(t *testing.T) {
	github := http.Header{}
	github.Set("X-GitHub-Event", "push")
	event, err := ParseWebhook(github, []byte(`{
		"ref": "refs/tags/v1.4.0", "before": "0000000000000000000000000000000000000000",
		"after": "7a9f000000000000000000000000000000000001", "created": true,
		"head_commit": {"id": "c0ffee0000000000000000000000000000000002", "message": "Release 1.4.0 [skip ci]",
			"author": {"name": "Ann"}},
		"repository": {"clone_url": "https://github.com/acme/shop.git"}
	}`))
	if err != nil {
		t.Fatalf("GitHub: %v", err)
	}
	if event.Kind != "tag" || event.Tag != "v1.4.0" || event.CommitSHA != "c0ffee0000000000000000000000000000000002" {
		t.Errorf("GitHub: %+v", event)
	}
	if event.RepoURL != "https://github.com/acme/shop" {
		t.Errorf("GitHub: the repository is %q", event.RepoURL)
	}
	// Release tools write "[skip ci]" into the commit they tag, so a tag
	// never carries the marker.
	if event.SkipMarker != "" {
		t.Errorf("GitHub: a tag carried the marker of the commit it points at: %q", event.SkipMarker)
	}

	// A tag taken away deploys nothing.
	_, err = ParseWebhook(github, []byte(`{
		"ref": "refs/tags/v1.4.0", "after": "0000000000000000000000000000000000000000", "deleted": true,
		"repository": {"clone_url": "https://github.com/acme/shop.git"}
	}`))
	if !errors.Is(err, ErrUnsupportedEvent) {
		t.Errorf("GitHub: a deleted tag was read as %v", err)
	}

	gitlab := http.Header{}
	gitlab.Set("X-Gitlab-Event", "Tag Push Hook")
	event, err = ParseWebhook(gitlab, []byte(`{
		"object_kind": "tag_push", "ref": "refs/tags/release-7",
		"before": "0000000000000000000000000000000000000000",
		"after": "7a9f000000000000000000000000000000000003",
		"checkout_sha": "c0ffee0000000000000000000000000000000004",
		"message": "Seventh release\n\nWith notes.", "user_name": "Ann",
		"project": {"git_http_url": "https://gitlab.example.test/acme/shop.git"},
		"commits": [], "total_commits_count": 0
	}`))
	if err != nil {
		t.Fatalf("GitLab: %v", err)
	}
	if event.Kind != "tag" || event.Tag != "release-7" || event.CommitSHA != "c0ffee0000000000000000000000000000000004" ||
		event.CommitMessage != "Seventh release" {
		t.Errorf("GitLab: %+v", event)
	}
	_, err = ParseWebhook(gitlab, []byte(`{
		"ref": "refs/tags/release-7", "before": "7a9f000000000000000000000000000000000003",
		"after": "0000000000000000000000000000000000000000", "checkout_sha": null,
		"project": {"git_http_url": "https://gitlab.example.test/acme/shop.git"}
	}`))
	if !errors.Is(err, ErrUnsupportedEvent) {
		t.Errorf("GitLab: a deleted tag was read as %v", err)
	}
	// GitLab sends tags as their own hook; a Push Hook naming one is not a
	// branch, and is ignored rather than deployed as one.
	pushHook := http.Header{}
	pushHook.Set("X-Gitlab-Event", "Push Hook")
	if _, err := ParseWebhook(pushHook, []byte(`{"ref": "refs/tags/v1", "after": "abc"}`)); !errors.Is(err, ErrUnsupportedEvent) {
		t.Errorf("GitLab: a tag in a Push Hook was read as %v", err)
	}

	gitea := http.Header{}
	gitea.Set("X-Gitea-Event", "push")
	event, err = ParseWebhook(gitea, []byte(`{
		"ref": "refs/tags/v2.0.0", "before": "0000000000000000000000000000000000000000",
		"after": "7a9f000000000000000000000000000000000005",
		"head_commit": {"id": "c0ffee0000000000000000000000000000000006", "message": "2.0", "author": {"name": "Ann"}},
		"repository": {"clone_url": "https://gitea.example.test/acme/shop.git"}
	}`))
	if err != nil {
		t.Fatalf("Gitea: %v", err)
	}
	if event.Kind != "tag" || event.Tag != "v2.0.0" || event.CommitSHA != "c0ffee0000000000000000000000000000000006" {
		t.Errorf("Gitea: %+v", event)
	}
}
