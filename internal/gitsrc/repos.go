package gitsrc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"skifity/internal/netguard"
	"skifity/internal/version"
)

// Listing what a connection can read, so the form offers a repository and a
// branch instead of asking for them to be typed.
//
// Somebody who has connected an account has already told the panel where
// their code is. Making them go to the Git host, copy an address and come back
// was the one step of creating an app that the panel could have done, and a
// typed branch name is where "mian" comes from. Typing still works: the list
// is an offer, and a repository it does not show is one address away.

// MaxListed bounds one answer. Somebody in a large organisation can read
// thousands of repositories, and the form is a list to pick from, not an
// inventory: the most recently active come first, and a name typed into the
// search narrows the list rather than paging through it.
const MaxListed = 300

// maxListBytes bounds one page of a provider's answer. GitHub describes a
// repository in five or six kilobytes, so a hundred of them is well under a
// megabyte; anything past this is not a page of repositories.
const maxListBytes = 4 << 20

// ErrListingUnsupported is a connection with no API the panel knows how to
// ask: a plain Git connection with a token and nothing else.
var ErrListingUnsupported = errors.New("this kind of Git connection cannot list repositories")

// ListRequest is a connection to ask, and what to ask it for.
type ListRequest struct {
	// Kind is github_pat, gitlab, gitea or bitbucket.
	Kind string
	// BaseURL is the provider's own address, for a self-hosted instance.
	BaseURL string
	// Token authenticates. It is only ever sent to BaseURL's host, which is
	// the host it was issued by.
	Token string
	// Email is the Atlassian account a Bitbucket API token belongs to. With
	// it the token is sent as Basic auth, the way Atlassian documents first;
	// without it, as a bearer token. Nothing else reads it.
	Email string
	// Account is the connection's user or organisation. On Bitbucket it is
	// the workspace to list, which an access token needs: it belongs to a
	// workspace or a repository, not to somebody who can be asked for theirs.
	Account string
	// Query narrows the answer to names containing it, ignoring case.
	Query string
}

// Repository is one repository a connection can read.
type Repository struct {
	// FullName is owner/name, or group/subgroup/name on GitLab.
	FullName string `json:"full_name"`
	// URL is the browser address, which is what the app form takes.
	URL           string `json:"url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// Branch is one branch of a repository.
type Branch struct {
	Name string `json:"name"`
}

// Listing is a page of answers, and whether there were more than MaxListed.
type Listing[T any] struct {
	Items     []T  `json:"items"`
	Truncated bool `json:"truncated"`
}

// CanList reports whether a kind of connection can be asked for its
// repositories.
func CanList(kind string) bool {
	switch kind {
	case "github", "github_pat", "gitlab", "gitea", "bitbucket":
		return true
	}
	return false
}

// validFullName is a repository's path on its host: segments of the
// characters GitHub, GitLab, Gitea and Bitbucket allow in a name, separated by
// slashes. A Bitbucket repository is workspace/slug, which fits.
var validFullName = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+$`)

// ValidRepoName checks a repository's full name before it becomes part of a
// request to a provider. Only GitLab nests groups, so everywhere else it is
// exactly an owner and a name.
func ValidRepoName(kind, fullName string) bool {
	if len(fullName) > 255 || !validFullName.MatchString(fullName) {
		return false
	}
	parts := strings.Split(fullName, "/")
	for _, part := range parts {
		// A segment of dots is a way out of the path the request was meant
		// for, not a name.
		if strings.Trim(part, ".") == "" {
			return false
		}
	}
	return kind == "gitlab" || len(parts) == 2
}

// hubRepository is a repository as GitHub and Gitea both describe it.
type hubRepository struct {
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

func fromHub(page []hubRepository) []Repository {
	out := make([]Repository, 0, len(page))
	for _, r := range page {
		out = append(out, Repository{FullName: r.FullName, URL: r.HTMLURL, DefaultBranch: r.DefaultBranch, Private: r.Private})
	}
	return out
}

// gitlabProject is a repository as GitLab describes it with simple=true.
type gitlabProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
	DefaultBranch     string `json:"default_branch"`
	Visibility        string `json:"visibility"`
}

func fromGitLab(page []gitlabProject) []Repository {
	out := make([]Repository, 0, len(page))
	for _, r := range page {
		out = append(out, Repository{FullName: r.PathWithNamespace, URL: r.WebURL,
			DefaultBranch: r.DefaultBranch, Private: r.Visibility != "public"})
	}
	return out
}

// ListRepositories lists the repositories a connection can read, most
// recently active first.
func ListRepositories(ctx context.Context, req ListRequest) (Listing[Repository], error) {
	switch req.Kind {
	case "github", "github_pat":
		base := apiBase(req.BaseURL, "https://api.github.com", "/api/v3")
		// affiliation names a token's own repositories, the ones it
		// collaborates on and its organisations', which is everything it can
		// deploy from.
		endpoint := base + "/user/repos?sort=updated&affiliation=owner,collaborator,organization_member"
		return collect(ctx, endpoint, githubPages, req, fromHub, repoName, false)

	case "gitlab":
		base := apiBase(req.BaseURL, "https://gitlab.com", "") + "/api/v4"
		endpoint := base + "/projects?membership=true&simple=true&order_by=last_activity_at"
		// GitLab searches on its side, which finds a project past the first
		// few hundred; GitHub and Gitea are narrowed here.
		if req.Query != "" {
			endpoint += "&search=" + url.QueryEscape(req.Query)
		}
		return collect(ctx, endpoint, gitlabPages, req, fromGitLab, repoName, true)

	case "gitea":
		base := apiBase(req.BaseURL, "https://codeberg.org", "") + "/api/v1"
		return collect(ctx, base+"/user/repos?", giteaPages, req, fromHub, repoName, false)

	case "bitbucket":
		return listBitbucketRepositories(ctx, req)
	}
	return Listing[Repository]{}, ErrListingUnsupported
}

// ListBranches lists a repository's branches. fullName must have passed
// ValidRepoName for the connection's kind.
func ListBranches(ctx context.Context, req ListRequest, fullName string) (Listing[Branch], error) {
	if !CanList(req.Kind) {
		return Listing[Branch]{}, ErrListingUnsupported
	}
	if !ValidRepoName(req.Kind, fullName) {
		return Listing[Branch]{}, fmt.Errorf("%q is not a repository's name", fullName)
	}
	owner, name, _ := strings.Cut(fullName, "/")

	// All three describe a branch with its name first and a great deal we
	// do not need after it, so one shape reads them all.
	switch req.Kind {
	case "gitlab":
		base := apiBase(req.BaseURL, "https://gitlab.com", "") + "/api/v4"
		// The whole path, escaped as one: a project in a subgroup is named by
		// all of it.
		endpoint := fmt.Sprintf("%s/projects/%s/repository/branches?", base, url.PathEscape(fullName))
		if req.Query != "" {
			endpoint += "search=" + url.QueryEscape(req.Query)
		}
		return collect(ctx, endpoint, gitlabPages, req, identity[Branch], branchName, true)
	case "gitea":
		base := apiBase(req.BaseURL, "https://codeberg.org", "") + "/api/v1"
		endpoint := fmt.Sprintf("%s/repos/%s/%s/branches?", base, url.PathEscape(owner), url.PathEscape(name))
		return collect(ctx, endpoint, giteaPages, req, identity[Branch], branchName, false)
	case "bitbucket":
		return listBitbucketBranches(ctx, req, owner, name)
	default:
		base := apiBase(req.BaseURL, "https://api.github.com", "/api/v3")
		endpoint := fmt.Sprintf("%s/repos/%s/%s/branches?", base, url.PathEscape(owner), url.PathEscape(name))
		return collect(ctx, endpoint, githubPages, req, identity[Branch], branchName, false)
	}
}

func identity[T any](page []T) []T { return page }
func repoName(r Repository) string { return r.FullName }
func branchName(b Branch) string   { return b.Name }

// pager is how a provider pages a list and how it takes a token.
type pager struct {
	size   int
	param  string
	scheme string
}

var (
	githubPages = pager{size: 100, param: "per_page", scheme: "token"}
	gitlabPages = pager{size: 100, param: "per_page", scheme: "bearer"}
	// Fifty is the most a Gitea answers with unless its operator raised it;
	// asking for more is answered with fifty anyway.
	giteaPages = pager{size: 50, param: "limit", scheme: "token"}
)

// collect walks a provider's pages until a short one or MaxListed, keeping
// what matches the query unless the provider already searched.
func collect[P any, T any](ctx context.Context, endpoint string, pages pager, req ListRequest,
	convert func(P) []T, nameOf func(T) string, searched bool) (Listing[T], error) {
	out := Listing[T]{Items: []T{}}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	separator := "&"
	if strings.HasSuffix(endpoint, "?") {
		separator = ""
	}
	seen := 0
	for page := 1; seen < MaxListed; page++ {
		var body P
		pageURL := fmt.Sprintf("%s%s%s=%d&page=%d", endpoint, separator, pages.param, pages.size, page)
		if err := getList(ctx, pageURL, req.Token, pages.scheme, &body); err != nil {
			return Listing[T]{}, err
		}
		items := convert(body)
		seen += len(items)
		for _, item := range items {
			if searched || query == "" || strings.Contains(strings.ToLower(nameOf(item)), query) {
				if len(out.Items) == MaxListed {
					out.Truncated = true
					return out, nil
				}
				out.Items = append(out.Items, item)
			}
		}
		if len(items) < pages.size {
			return out, nil
		}
	}
	// Stopped at the ceiling with a full page last: there is probably more.
	out.Truncated = true
	return out, nil
}

// HostError is a Git host that answered, and not with what was asked for. The
// status is what a caller decides by — a token refused is a different thing
// to fix from a host that is down — and the message is what a person reads.
type HostError struct {
	Status int
	msg    string
}

func (e *HostError) Error() string { return e.msg }

// getList fetches one page. Its errors say what a listing needs, which is not
// what a single repository's lookup says: a 404 here is an address that is
// not a Git host's API, not a repository that is missing.
func getList(ctx context.Context, endpoint, token, scheme string, into any) error {
	if !netguard.Address.MatchString(endpoint) {
		return &HostError{msg: "the Git host's address is not one the panel can ask"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	authorize(req, token, scheme)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the Git host: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return &HostError{Status: resp.StatusCode,
			msg: fmt.Sprintf("the Git host refused the connection's token (%s)", resp.Status)}
	case resp.StatusCode == http.StatusNotFound:
		return &HostError{Status: resp.StatusCode,
			msg: "the Git host answered 404: the repository is not there, or the connection's address is not the host's"}
	case resp.StatusCode >= 300:
		return &HostError{Status: resp.StatusCode, msg: fmt.Sprintf("the Git host answered %s", resp.Status)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes))
	if err != nil {
		return fmt.Errorf("read the Git host's answer: %w", err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("the Git host's answer was not a list: %w", err)
	}
	return nil
}
