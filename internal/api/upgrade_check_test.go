package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"skifity/internal/version"
)

func TestARunningVersionIsComparedWithTheNewestRelease(t *testing.T) {
	for _, c := range []struct {
		running, latest, want string
	}{
		{"v0.1.0", "v0.2.0", upgradeAvailable},
		{"v0.1.0", "v0.1.1", upgradeAvailable},
		{"v1.9.0", "v2.0.0", upgradeAvailable},
		// Numbers, not text: 10 is more than 9.
		{"v0.9.0", "v0.10.0", upgradeAvailable},
		{"v0.10.0", "v0.9.0", upgradeAhead},
		{"v0.1.0", "v0.1.0", upgradeUpToDate},
		{"0.1.0", "v0.1.0", upgradeUpToDate},
		{"v0.2.0", "v0.1.0", upgradeAhead},
		// A build made after the tag is neither the release nor behind it.
		{"v0.1.0-3-gabc1234", "v0.1.0", upgradeDevelopment},
		{"v0.1.0-3-gabc1234-dirty", "v0.1.0", upgradeDevelopment},
		// But it is behind the next one.
		{"v0.1.0-3-gabc1234", "v0.2.0", upgradeAvailable},
		// A pre-release comes before the release it leads to.
		{"v0.2.0-rc.1", "v0.2.0", upgradeAvailable},
		{"v0.3.0-rc.1", "v0.2.0", upgradeAhead},
		// What is not a release number cannot be compared.
		{"dev", "v0.1.0", upgradeUnknown},
		{"", "v0.1.0", upgradeUnknown},
		{"v0.1.0", "latest", upgradeUnknown},
	} {
		if got := compareReleases(c.running, c.latest); got != c.want {
			t.Errorf("running %q, newest %q: %s, want %s", c.running, c.latest, got, c.want)
		}
	}
}

func TestATagIsReadOutOfWhereGitHubSendsYou(t *testing.T) {
	for path, want := range map[string]string{
		"/Skifity/Skifity/releases/tag/v0.1.0":      "v0.1.0",
		"/Skifity/Skifity/releases/tag/v1.2.3-rc.1": "v1.2.3-rc.1",
	} {
		if got, err := tagFromReleasePath(path); err != nil || got != want {
			t.Errorf("%s: %q, %v, want %q", path, got, err, want)
		}
	}
	// A repository with nothing published is sent to the list.
	for _, path := range []string{"/Skifity/Skifity/releases", "/Skifity/Skifity/releases/"} {
		if _, err := tagFromReleasePath(path); !errors.Is(err, errNoRelease) {
			t.Errorf("%s: %v, want no release", path, err)
		}
	}
	// Whatever else is not a release, and a tag that is not a version is not
	// put into a link.
	for _, path := range []string{
		"/login",
		"/Skifity/Skifity/releases/tag/",
		"/Skifity/Skifity/releases/tag/v1/../../x",
		"/Skifity/Skifity/releases/tag/<script>",
		"/Skifity/Skifity/releases/tag/latest",
	} {
		if tag, err := tagFromReleasePath(path); err == nil {
			t.Errorf("%s was read as the release %q", path, tag)
		}
	}
}

// githubStub answers the way github.com answers /releases/latest.
func githubStub(t *testing.T, routes map[string][2]string) (*httptest.Server, *http.Client) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if answer[1] != "" {
			w.Header().Set("Location", answer[1])
		}
		status := http.StatusFound
		switch answer[0] {
		case "moved":
			status = http.StatusMovedPermanently
		case "ok":
			status = http.StatusOK
		case "error":
			status = http.StatusBadGateway
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	// The panel's own client will not dial loopback; this one is what it is
	// minus that, and the same about redirects.
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return server, client
}

func TestTheNewestReleaseIsFoundByFollowingGitHubsRedirect(t *testing.T) {
	server, client := githubStub(t, map[string][2]string{
		"/acme/skifity/releases/latest": {"found", "/acme/skifity/releases/tag/v0.4.2"},

		// Nothing published.
		"/acme/empty/releases/latest": {"found", "/acme/empty/releases"},

		// A repository that was renamed: one more redirect, then the answer.
		"/acme/old/releases/latest": {"moved", "/acme/new/releases/latest"},
		"/acme/new/releases/latest": {"found", "/acme/new/releases/tag/v2.0.0"},

		// A loop, and somewhere else entirely.
		"/acme/loop/releases/latest":  {"found", "/acme/loop/releases/latest"},
		"/acme/login/releases/latest": {"found", "/login?return_to=x"},
		"/acme/away/releases/latest":  {"found", "https://evil.example.test/acme/away/releases/tag/v9.9.9"},

		// A tag that is not a version.
		"/acme/odd/releases/latest": {"found", "/acme/odd/releases/tag/%3Cscript%3E"},

		"/acme/broken/releases/latest": {"error", ""},
	})
	lookup := func(repository string) (string, error) {
		return lookupLatestRelease(t.Context(), client, server.URL, repository)
	}

	if tag, err := lookup("acme/skifity"); err != nil || tag != "v0.4.2" {
		t.Errorf("the newest release: %q, %v", tag, err)
	}
	if _, err := lookup("acme/empty"); !errors.Is(err, errNoRelease) {
		t.Errorf("a repository with no release: %v", err)
	}
	if tag, err := lookup("acme/old"); err != nil || tag != "v2.0.0" {
		t.Errorf("a renamed repository: %q, %v", tag, err)
	}
	for _, repository := range []string{"acme/loop", "acme/login", "acme/away", "acme/odd", "acme/broken", "acme/nothing"} {
		if tag, err := lookup(repository); err == nil || errors.Is(err, errNoRelease) {
			t.Errorf("%s was answered %q, %v", repository, tag, err)
		}
	}
	if _, err := lookup("acme/nothing"); err == nil || !strings.Contains(err.Error(), "no repository") {
		t.Errorf("a repository that is not there should say so: %v", err)
	}
}

func TestCheckingForUpdatesAsksOnlyWhenPressed(t *testing.T) {
	h := newHarness(t)
	admin := adminTenant(h, "acme")
	member := h.newTenant("other")

	var asked atomic.Int32
	var askedAbout atomic.Value
	h.api.latestRelease = func(_ context.Context, repository string) (string, error) {
		asked.Add(1)
		askedAbout.Store(repository)
		return "v9.9.9", nil
	}
	h.api.cfg.UpdateRepository = "acme/skifity"

	// Starting up, signing in, looking at the version: none of it asks.
	h.do(admin, http.MethodGet, "/api/upgrade", nil)
	h.do(admin, http.MethodGet, "/api/meta", nil)
	if n := asked.Load(); n != 0 {
		t.Fatalf("the panel asked for the newest release %d times without being pressed", n)
	}
	status, body := h.do(admin, http.MethodGet, "/api/upgrade", nil)
	if status != http.StatusOK || !strings.Contains(body, `"update_check":"manual"`) {
		t.Errorf("the version answer should say checks are manual: %d %s", status, body)
	}

	// A GET is not the button: it could be prefetched.
	if status, _ := h.do(admin, http.MethodGet, "/api/upgrade/check", nil); status == http.StatusOK || asked.Load() != 0 {
		t.Errorf("a GET asked for the newest release (%d)", status)
	}

	// Pressed.
	before := time.Now().Add(-time.Second)
	status, body = h.do(admin, http.MethodPost, "/api/upgrade/check", nil)
	if status != http.StatusOK {
		t.Fatalf("the check: %d %s", status, body)
	}
	for _, want := range []string{
		`"latest_version":"v9.9.9"`,
		`"release_url":"https://github.com/acme/skifity/releases/tag/v9.9.9"`,
		`"repository":"acme/skifity"`,
		`"state":"` + compareReleases(version.Version, "v9.9.9") + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the answer lacks %s: %s", want, body)
		}
	}
	if asked.Load() != 1 || askedAbout.Load() != "acme/skifity" {
		t.Errorf("asked %d times, about %v", asked.Load(), askedAbout.Load())
	}
	if !time.Now().After(before) {
		t.Error("clock")
	}

	// Only the panel's administrators.
	if status, _ := h.do(member, http.MethodPost, "/api/upgrade/check", nil); status != http.StatusForbidden {
		t.Errorf("an ordinary account checked for updates: %d", status)
	}
	if asked.Load() != 1 {
		t.Error("an ordinary account's refused request still asked")
	}
}

func TestCheckingForUpdatesSaysWhatWentWrong(t *testing.T) {
	h := newHarness(t)
	admin := adminTenant(h, "acme")

	// No repository set: nothing is asked, and the error says how to fix it.
	var asked atomic.Int32
	h.api.latestRelease = func(context.Context, string) (string, error) {
		asked.Add(1)
		return "v1.0.0", nil
	}
	h.api.cfg.UpdateRepository = ""
	status, body := h.do(admin, http.MethodPost, "/api/upgrade/check", nil)
	if status != http.StatusBadRequest || !strings.Contains(body, "upgrade.check_unconfigured") ||
		!strings.Contains(body, "SKIFITY_UPDATE_REPOSITORY") {
		t.Errorf("no repository configured: %d %s", status, body)
	}
	if asked.Load() != 0 {
		t.Error("it asked although it had nowhere to ask")
	}

	// GitHub cannot be reached: a failure that can be tried again.
	h.api.cfg.UpdateRepository = "acme/skifity"
	h.api.latestRelease = func(context.Context, string) (string, error) {
		return "", errors.New("reaching github.com: connection refused")
	}
	status, body = h.do(admin, http.MethodPost, "/api/upgrade/check", nil)
	if status != http.StatusBadGateway || !strings.Contains(body, "upgrade.check_failed") ||
		!strings.Contains(body, "connection refused") {
		t.Errorf("GitHub unreachable: %d %s", status, body)
	}

	// Nothing published is an answer, not an error.
	h.api.latestRelease = func(context.Context, string) (string, error) { return "", errNoRelease }
	status, body = h.do(admin, http.MethodPost, "/api/upgrade/check", nil)
	if status != http.StatusOK || !strings.Contains(body, `"state":"no_release"`) || strings.Contains(body, "latest_version") {
		t.Errorf("a repository with no release: %d %s", status, body)
	}
}
