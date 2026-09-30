package gitsrc

import (
	"net/http"
	"strings"
	"testing"
)

func TestWatchPathsMatchWhatTheySay(t *testing.T) {
	cases := []struct {
		patterns string
		file     string
		want     bool
	}{
		// Nothing to watch means everything is watched.
		{"", "anything.txt", true},
		// A plain path covers what is under it, and only that.
		{"apps/web", "apps/web/src/main.ts", true},
		{"apps/web", "apps/web", true},
		{"apps/web", "apps/website/index.ts", false},
		{"apps/web", "apps/api/main.go", false},
		{"/apps/web/", "apps/web/package.json", true},
		{"./apps/web", "apps/web/package.json", true},
		// "*" stays in one directory; "**" crosses any number.
		{"apps/*/package.json", "apps/web/package.json", true},
		{"apps/*/package.json", "apps/web/src/package.json", false},
		{"apps/**/package.json", "apps/web/src/package.json", true},
		{"apps/**/package.json", "apps/package.json", true},
		{"**/*.go", "cmd/main.go", true},
		{"**/*.go", "main.go", true},
		{"**", "anything/at/all", true},
		// Every pattern starts at the root: a bare name is not "anywhere".
		{"docs", "apps/web/docs/readme.md", false},
		// A later line wins over an earlier one.
		{"apps/web\n!**/*.md", "apps/web/README.md", false},
		{"apps/web\n!**/*.md", "apps/web/index.ts", true},
		{"!**/*.md\napps/web", "apps/web/README.md", true},
		// Only exclusions reads as "everything except".
		{"!docs/**", "docs/index.md", false},
		{"!docs/**", "src/index.ts", true},
		// Comments and blank lines are not patterns.
		{"# the web app\n\napps/web", "apps/api/main.go", false},
	}
	for _, c := range cases {
		patterns, err := ParseWatchPaths(c.patterns)
		if err != nil {
			t.Fatalf("%q: %v", c.patterns, err)
		}
		if got := Watches(patterns, c.file); got != c.want {
			t.Errorf("patterns %q, file %s: watched=%v, want %v", c.patterns, c.file, got, c.want)
		}
	}
}

func TestAPatternThatCannotMatchIsRefusedWhenSaved(t *testing.T) {
	for _, bad := range []string{
		"!",
		"/",
		"../secrets",
		"apps/../../etc",
		"apps/[web",
		strings.Repeat("a", maxWatchPathLength+1),
		strings.Repeat("apps/web\n", MaxWatchPaths+1),
	} {
		if _, err := ParseWatchPaths(bad); err == nil {
			t.Errorf("%.40q was accepted", bad)
		}
	}
}

func githubPushWith(t *testing.T, extra string) PushEvent {
	t.Helper()
	header := http.Header{}
	header.Set("X-GitHub-Event", "push")
	ev, err := ParseWebhook(header, []byte(`{
		"ref": "refs/heads/main",
		"before": "1111111111111111111111111111111111111111",
		"after": "2222222222222222222222222222222222222222",
		"repository": {"clone_url": "https://github.com/acme/shop.git"},
		`+extra+`
	}`))
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// What decides whether a monorepo app is built at all: the files a push
// touched, and whether that list can be believed.
func TestAPushSaysWhichFilesItChanged(t *testing.T) {
	ev := githubPushWith(t, `"commits": [
		{"id": "a", "added": ["apps/web/new.ts"], "modified": ["apps/web/index.ts"], "removed": []},
		{"id": "b", "added": [], "modified": ["apps/web/index.ts"], "removed": ["docs/old.md"]}
	]`)
	if !ev.FilesKnown {
		t.Fatal("an ordinary push was treated as unknown")
	}
	if got := strings.Join(ev.ChangedFiles, ","); got != "apps/web/new.ts,apps/web/index.ts,docs/old.md" {
		t.Fatalf("changed files: %s", got)
	}
	if ev.Touches([]string{"apps/api"}) {
		t.Error("a push to apps/web touched apps/api")
	}
	if !ev.Touches([]string{"apps/web"}) || !ev.Touches([]string{"docs"}) || !ev.Touches(nil) {
		t.Error("a push was not seen to touch what it changed")
	}
}

// Anything that might not be the whole story counts as everything changed: a
// deploy that was not needed costs a build, one that was needed and skipped
// leaves old code running.
func TestAPushWhoseFilesAreUncertainTouchesEverything(t *testing.T) {
	someCommits := `"commits": [{"id": "a", "modified": ["docs/readme.md"]}]`
	many := make([]string, listedCommitsCap)
	for i := range many {
		many[i] = `{"id": "c", "modified": ["docs/readme.md"]}`
	}
	cases := map[string]string{
		"a new branch":            `"created": true, ` + someCommits,
		"a force push":            `"forced": true, ` + someCommits,
		"no commits listed":       `"commits": []`,
		"an empty commit":         `"commits": [{"id": "a", "added": [], "modified": [], "removed": []}]`,
		"a list cut short":        `"commits": [` + strings.Join(many, ",") + `]`,
		"Gitea listing only some": `"total_commits": 9, ` + someCommits,
	}
	for name, extra := range cases {
		ev := githubPushWith(t, extra)
		if ev.FilesKnown {
			t.Errorf("%s: the file list was believed", name)
		}
		if !ev.Touches([]string{"apps/web"}) {
			t.Errorf("%s: the push was skipped", name)
		}
	}
}

func TestAGitLabOrGiteaPushDeploysEveryApp(t *testing.T) {
	// Neither says whether a push was forced, and a force push lists the
	// commits it added and not the ones it took away: an app a removed
	// commit had changed would be skipped and keep running code no longer on
	// the branch. So for them every push deploys.
	header := http.Header{}
	header.Set("X-Gitlab-Event", "Push Hook")
	ev, err := ParseWebhook(header, []byte(`{
		"ref": "refs/heads/main",
		"before": "1111111111111111111111111111111111111111",
		"after": "2222222222222222222222222222222222222222",
		"total_commits_count": 1,
		"project": {"git_http_url": "https://gitlab.example.com/acme/shop.git"},
		"commits": [{"id": "a", "message": "m", "added": [], "modified": ["docs/readme.md"], "removed": []}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.FilesKnown || !ev.Touches([]string{"services/api"}) || ev.Before != "1111111111111111111111111111111111111111" {
		t.Fatalf("GitLab: known=%v before=%s", ev.FilesKnown, ev.Before)
	}

	gitea := http.Header{}
	gitea.Set("X-Gitea-Event", "push")
	ev, err = ParseWebhook(gitea, []byte(`{
		"ref": "refs/heads/main",
		"before": "1111111111111111111111111111111111111111",
		"after": "2222222222222222222222222222222222222222",
		"total_commits": 1,
		"repository": {"clone_url": "https://gitea.example.com/acme/shop.git"},
		"commits": [{"id": "a", "modified": ["docs/readme.md"]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.FilesKnown || !ev.Touches([]string{"services/api"}) {
		t.Fatalf("Gitea: known=%v", ev.FilesKnown)
	}
}
