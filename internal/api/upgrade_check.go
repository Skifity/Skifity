package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/netguard"
	"skifity/internal/version"
)

// Which release is the newest is the one thing the panel does not know, and
// it does not look on its own: no timer, no start-up check, nothing that sends
// an address anywhere unless a panel administrator presses "Check for updates"
// in Settings, or makes this call. That is what "never phones home" means here.
// The check is a POST for the same reason: a GET could be prefetched, retried or
// followed by a crawler, and this one contacts a server on somebody's behalf.

// releaseHost is where releases are published.
const releaseHost = "https://github.com"

// errNoRelease is a repository with nothing published yet, which is an answer
// and not a failure.
var errNoRelease = errors.New("the repository has no release yet")

// releaseClient asks GitHub through the same guard every other outbound
// request goes through, and does not follow redirects: the redirect is the
// answer.
var releaseClient = func() *http.Client {
	client := netguard.Client(15 * time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}()

// What a check can find, for the interface and anything else that reads it.
const (
	// A release newer than the one running exists.
	upgradeAvailable = "available"
	// The newest release is the one running.
	upgradeUpToDate = "up_to_date"
	// This is a build between releases, such as one made from the repository.
	upgradeDevelopment = "development"
	// This is newer than the newest release, such as a pre-release.
	upgradeAhead = "ahead"
	// The running version is not a release number, so there is nothing to compare.
	upgradeUnknown = "unknown"
	// Nothing is published.
	upgradeNoRelease = "no_release"
)

// upgradeCheck is the answer to a check.
type upgradeCheck struct {
	CurrentVersion string `json:"current_version"`
	// LatestVersion and ReleaseURL are empty when nothing is published.
	LatestVersion string    `json:"latest_version,omitempty"`
	ReleaseURL    string    `json:"release_url,omitempty"`
	State         string    `json:"state"`
	Repository    string    `json:"repository"`
	CheckedAt     time.Time `json:"checked_at"`
}

func (s *Server) handleUpgradeCheck(w http.ResponseWriter, r *http.Request) {
	repository := s.cfg.UpdateRepository
	if repository == "" {
		writeError(w, r, errdoc.New("upgrade.check_unconfigured", "This panel does not know where its releases are").
			WithCause("No release repository is set. The installer sets one; this panel was installed another way.").
			WithImpact("Nothing was asked of anyone, and nothing was changed.").
			WithFix("Run the installer again, which sets it, or set SKIFITY_UPDATE_REPOSITORY to owner/name on the panel's Deployment. You can still upgrade by naming the version yourself.").
			WithDocs("/docs/configuration#upgrading").
			WithStatus(http.StatusBadRequest))
		return
	}

	lookup := s.latestRelease
	if lookup == nil {
		lookup = func(ctx context.Context, repository string) (string, error) {
			return lookupLatestRelease(ctx, releaseClient, releaseHost, repository)
		}
	}
	s.log.Info("asking for the newest release, because an administrator pressed the button", "repository", repository)
	latest, err := lookup(r.Context(), repository)

	answer := upgradeCheck{
		CurrentVersion: version.Version,
		Repository:     repository,
		CheckedAt:      time.Now().UTC(),
	}
	switch {
	case errors.Is(err, errNoRelease):
		answer.State = upgradeNoRelease
	case err != nil:
		writeError(w, r, errdoc.New("upgrade.check_failed", "The newest release could not be found out").
			WithCause("%s", err.Error()).
			WithImpact("Nothing was changed.").
			WithFix("Check that the panel can reach github.com and try again. You can also upgrade by naming the version yourself.").
			WithDocs("/docs/configuration#upgrading").
			WithStatus(http.StatusBadGateway).Retry())
		return
	default:
		answer.LatestVersion = latest
		answer.ReleaseURL = fmt.Sprintf("%s/%s/releases/tag/%s", releaseHost, repository, url.PathEscape(latest))
		answer.State = compareReleases(version.Version, latest)
	}
	writeJSON(w, http.StatusOK, answer)
}

// lookupLatestRelease finds the tag of the newest release by following GitHub's
// own redirect from /releases/latest to /releases/tag/<tag>.
//
// The API would say it in JSON, and would count the request against sixty an
// hour for the whole address — which a shared one, behind NAT or a CI runner,
// has often spent. The redirect has no such limit and says the same thing. It
// skips drafts and pre-releases, as "latest" does everywhere on GitHub.
func lookupLatestRelease(ctx context.Context, client *http.Client, base, repository string) (string, error) {
	next := base + "/" + repository + "/releases/latest"
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	// A renamed repository answers with one more redirect to the new name,
	// which is worth following; anything further is not.
	for hop := 0; hop < 3; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", version.Binary+"/"+version.Version)
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("reaching %s: %w", baseURL.Host, err)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
			http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		case http.StatusNotFound:
			return "", fmt.Errorf("%s has no repository %s, or it is private", baseURL.Host, repository)
		default:
			return "", fmt.Errorf("%s answered %d", baseURL.Host, resp.StatusCode)
		}

		location, err := baseURL.Parse(resp.Header.Get("Location"))
		if err != nil || location.Host != baseURL.Host {
			return "", fmt.Errorf("%s sent the request somewhere unexpected", baseURL.Host)
		}
		switch tag, err := tagFromReleasePath(location.Path); {
		case err == nil:
			return tag, nil
		case errors.Is(err, errNoRelease):
			return "", err
		}
		if !strings.HasSuffix(location.Path, "/releases/latest") {
			return "", fmt.Errorf("%s sent the request somewhere unexpected", baseURL.Host)
		}
		next = location.String()
	}
	return "", fmt.Errorf("%s redirected too many times", baseURL.Host)
}

// tagFromReleasePath reads the tag out of /<owner>/<name>/releases/tag/<tag>.
// A repository with no release is sent to /<owner>/<name>/releases instead.
func tagFromReleasePath(path string) (string, error) {
	if strings.HasSuffix(strings.TrimSuffix(path, "/"), "/releases") {
		return "", errNoRelease
	}
	_, tag, found := strings.Cut(path, "/releases/tag/")
	if !found || tag == "" || strings.Contains(tag, "/") {
		return "", fmt.Errorf("%q is not the address of a release", path)
	}
	// The tag ends up in a link and in the interface, so it has to look like
	// what a release is called, whatever the server said.
	if !versionPattern.MatchString(tag) {
		return "", fmt.Errorf("the newest release is called %q, which is not a version number", tag)
	}
	return tag, nil
}

// describeSuffix is what `git describe` adds to a build made some commits
// after a tag: v0.1.0-3-gabc123, and -dirty when the tree had changes.
var describeSuffix = regexp.MustCompile(`^\d+-g[0-9a-f]+(-dirty)?$`)

// releaseNumber is a version split into its three numbers, and what follows
// them: nothing, a pre-release such as rc.1, or a build made after the tag.
type releaseNumber struct {
	core       [3]int
	prerelease bool
	describe   bool
}

func parseReleaseNumber(v string) (releaseNumber, bool) {
	var number releaseNumber
	if !versionPattern.MatchString(v) {
		return number, false
	}
	core, rest, _ := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	for i, part := range strings.SplitN(core, ".", 3) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return number, false
		}
		number.core[i] = n
	}
	switch {
	case rest == "":
	case describeSuffix.MatchString(rest):
		number.describe = true
	default:
		number.prerelease = true
	}
	return number, true
}

// compareReleases says what the running version is next to the newest release.
func compareReleases(running, latest string) string {
	current, ok := parseReleaseNumber(running)
	if !ok {
		return upgradeUnknown
	}
	newest, ok := parseReleaseNumber(latest)
	if !ok {
		return upgradeUnknown
	}
	for i := range current.core {
		switch {
		case newest.core[i] > current.core[i]:
			return upgradeAvailable
		case newest.core[i] < current.core[i]:
			return upgradeAhead
		}
	}
	// The same three numbers, so the difference is in what follows them.
	switch {
	case current.describe:
		return upgradeDevelopment
	case current.prerelease && !newest.prerelease:
		// v0.1.0-rc.1 is before v0.1.0.
		return upgradeAvailable
	case current.prerelease && running != latest:
		return upgradeUnknown
	}
	return upgradeUpToDate
}
