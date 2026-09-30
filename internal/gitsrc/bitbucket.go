package gitsrc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"skifity/internal/version"
)

// Bitbucket Cloud: bitbucket.org and its REST API 2.0.
//
// A connection holds one of two credentials, both pasted into the same field:
//
//   - An API token, made by a person under their Atlassian account's security
//     settings, with scopes chosen for Bitbucket. Atlassian documents it with
//     Basic auth and the account's email as the user name, and since August
//     2026 takes it as a bearer token too. Given an email the panel sends
//     Basic; without one, Bearer.
//   - An access token, made by an administrator for one repository, project or
//     workspace. Bearer only, and it is nobody's: there is no user to ask for
//     their workspaces, which is why the connection names the workspace.
//
// App passwords are not supported. Atlassian stopped new ones on 9 September
// 2025 and switched the last of them off on 28 July 2026.
//
// Git over HTTPS takes either kind of token with the static user name
// x-token-auth, which is what CloneUsername hands the build, so the build
// never needs the email.
//
// Bitbucket Data Center is a different product with a different API, on a
// host of its own. None of this speaks to it.
//
// https://support.atlassian.com/bitbucket-cloud/docs/using-api-tokens/
// https://support.atlassian.com/bitbucket-cloud/docs/using-access-tokens/
// https://developer.atlassian.com/cloud/bitbucket/rest/intro/

const (
	// BitbucketURL is Bitbucket Cloud's own address. Every Bitbucket
	// connection is for it, which is what lets SameHost say a bitbucket.org
	// repository may be sent the connection's token.
	BitbucketURL = "https://bitbucket.org"
	bitbucketAPI = "https://api.bitbucket.org/2.0"
	// bitbucketPageSize is the most Bitbucket answers with at once: "the
	// minimum length is 10 and the maximum is 100".
	bitbucketPageSize = 100
	// bitbucketMaxPages is enough pages to fill MaxListed and see whether
	// there was more.
	bitbucketMaxPages = MaxListed/bitbucketPageSize + 1
	// bitbucketTreeDepth is how many directories deep a listing of a
	// repository's files goes. Everything detection reads is two deep at
	// most, and a listing of every file in a monorepo is thousands of pages.
	bitbucketTreeDepth = 4
)

// bitbucketBase is the API address for a connection. Bitbucket Cloud has
// exactly one; any other base address is a server standing in for it in a
// test, answering the same paths under /2.0.
func bitbucketBase(baseURL string) string {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || strings.EqualFold(baseURL, BitbucketURL) || strings.EqualFold(baseURL, bitbucketAPI) {
		return bitbucketAPI
	}
	return baseURL + "/2.0"
}

// bitbucketAuth is how a request carries a connection's credential: Basic
// with the account's email for an API token given one, Bearer otherwise.
func bitbucketAuth(token, email string) (credential, scheme string) {
	if token == "" {
		return "", ""
	}
	if email = strings.TrimSpace(email); email != "" {
		return email + ":" + token, "basic"
	}
	return token, "bearer"
}

// CloneUsername is the user name a build presents with a connection's token
// when it fetches over HTTPS. GitHub reads the token and ignores the name, and
// so do GitLab and Gitea; Bitbucket wants one of its static names for a token,
// and x-token-auth is the one both an API token and an access token take.
func CloneUsername(kind string) string {
	if kind == "bitbucket" {
		return "x-token-auth"
	}
	return "x-access-token"
}

// validWorkspace is a Bitbucket workspace's id as it appears in its URLs.
var validWorkspace = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

// ValidBitbucketWorkspace checks the workspace a connection names before it
// becomes part of every request the connection makes.
func ValidBitbucketWorkspace(workspace string) bool {
	return validWorkspace.MatchString(workspace)
}

// bitbucketPage is one page of any Bitbucket list.
type bitbucketPage[T any] struct {
	Values []T    `json:"values"`
	Next   string `json:"next"`
}

// walkBitbucket reads a list page by page until it ends, each says it has
// enough, or maxPages have been read. It reports whether it stopped before the
// list did.
//
// Bitbucket says to follow the next link it gives rather than to build one,
// which this does — but the link is an address in an answer, and the token
// goes wherever it points. One on another host is refused, not followed.
func walkBitbucket[T any](ctx context.Context, first, token, scheme string, maxPages int, each func(T) bool) (bool, error) {
	next := first
	for page := 0; next != ""; page++ {
		if page == maxPages {
			return true, nil
		}
		if !sameOrigin(next, first) {
			return false, fmt.Errorf("the next page was on %s, which is not the host it was asked", hostOf(next))
		}
		var body bitbucketPage[T]
		if err := getList(ctx, next, token, scheme, &body); err != nil {
			return false, err
		}
		for i, item := range body.Values {
			if !each(item) {
				return i < len(body.Values)-1 || body.Next != "", nil
			}
		}
		next = body.Next
	}
	return false, nil
}

// sameOrigin reports whether two addresses have the same scheme and host.
func sameOrigin(a, b string) bool {
	left, err := url.Parse(a)
	if err != nil {
		return false
	}
	right, err := url.Parse(b)
	if err != nil {
		return false
	}
	return left.Scheme != "" && strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

// bitbucketQueryText makes a search safe to put inside a BBQL string. A
// repository's or a branch's name never has a quote or a backslash in it, so
// they are dropped rather than escaped in a syntax Atlassian does not document.
func bitbucketQueryText(query string) string {
	return strings.TrimSpace(strings.NewReplacer(`"`, "", `\`, "").Replace(query))
}

// escapePath escapes each segment of a path on its own, keeping the slashes.
func escapePath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// --- the connection ---

// CheckBitbucket asks Bitbucket whether a connection's token works before the
// connection is saved: the workspace's repositories when it names one, the
// token's workspaces otherwise. Both are what listing will ask later, so a
// token that passes here can fill the repository picker. A *HostError says
// what Bitbucket answered.
func CheckBitbucket(ctx context.Context, req ListRequest) error {
	base := bitbucketBase(req.BaseURL)
	token, scheme := bitbucketAuth(req.Token, req.Email)
	endpoint := base + "/user/workspaces?pagelen=10"
	if workspace := strings.TrimSpace(req.Account); workspace != "" {
		endpoint = fmt.Sprintf("%s/repositories/%s?pagelen=10", base, url.PathEscape(workspace))
	}
	var page bitbucketPage[json.RawMessage]
	return getList(ctx, endpoint, token, scheme, &page)
}

// --- listing ---

// bitbucketRepo is a repository as Bitbucket's API describes it.
type bitbucketRepo struct {
	FullName   string `json:"full_name"`
	IsPrivate  bool   `json:"is_private"`
	UpdatedOn  string `json:"updated_on"`
	Mainbranch *struct {
		Name string `json:"name"`
	} `json:"mainbranch"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

func (r bitbucketRepo) repository() Repository {
	out := Repository{FullName: r.FullName, URL: r.Links.HTML.Href, Private: r.IsPrivate}
	if out.URL == "" {
		out.URL = BitbucketURL + "/" + r.FullName
	}
	if r.Mainbranch != nil {
		out.DefaultBranch = r.Mainbranch.Name
	}
	return out
}

// listBitbucketRepositories lists what a connection can read, workspace by
// workspace, the most recently updated first.
//
// Workspace by workspace because there is no other way any more: Bitbucket
// removed GET /2.0/repositories?role=member, which listed every repository a
// person could read wherever it was, in 2026. The workspaces come from
// /2.0/user/workspaces, or are the one the connection names.
func listBitbucketRepositories(ctx context.Context, req ListRequest) (Listing[Repository], error) {
	base := bitbucketBase(req.BaseURL)
	token, scheme := bitbucketAuth(req.Token, req.Email)
	workspaces, more, err := bitbucketWorkspaces(ctx, base, req.Account, token, scheme)
	if err != nil {
		return Listing[Repository]{}, err
	}

	type dated struct {
		repo    Repository
		updated time.Time
	}
	var found []dated
	out := Listing[Repository]{Items: []Repository{}, Truncated: more}
	// Bitbucket searches on its side, like GitLab, which finds a repository
	// past the first few hundred.
	query := bitbucketQueryText(req.Query)
	for i, workspace := range workspaces {
		endpoint := fmt.Sprintf("%s/repositories/%s?pagelen=%d&sort=-updated_on",
			base, url.PathEscape(workspace), bitbucketPageSize)
		if query != "" {
			endpoint += "&q=" + url.QueryEscape(`name ~ "`+query+`"`)
		}
		stopped, err := walkBitbucket(ctx, endpoint, token, scheme, bitbucketMaxPages, func(r bitbucketRepo) bool {
			if len(found) == MaxListed {
				return false
			}
			updated, _ := time.Parse(time.RFC3339Nano, r.UpdatedOn)
			found = append(found, dated{repo: r.repository(), updated: updated})
			return true
		})
		if err != nil {
			return Listing[Repository]{}, err
		}
		if stopped || (len(found) == MaxListed && i < len(workspaces)-1) {
			out.Truncated = true
			break
		}
	}
	// Each workspace answered newest first; this puts them together.
	sort.SliceStable(found, func(a, b int) bool { return found[a].updated.After(found[b].updated) })
	for _, item := range found {
		out.Items = append(out.Items, item.repo)
	}
	return out, nil
}

// bitbucketWorkspaces are the workspaces to list: the connection's own, or
// every one the token can see. It reports whether there were more than it
// read.
func bitbucketWorkspaces(ctx context.Context, base, account, token, scheme string) ([]string, bool, error) {
	if workspace := strings.TrimSpace(account); workspace != "" {
		return []string{workspace}, false, nil
	}
	var slugs []string
	more, err := walkBitbucket(ctx, fmt.Sprintf("%s/user/workspaces?pagelen=%d", base, bitbucketPageSize),
		token, scheme, 5, func(access struct {
			Workspace struct {
				Slug string `json:"slug"`
			} `json:"workspace"`
		}) bool {
			if access.Workspace.Slug != "" {
				slugs = append(slugs, access.Workspace.Slug)
			}
			return true
		})
	if err != nil {
		return nil, false, fmt.Errorf("list the workspaces this token can see: %w "+
			"(an API token needs read:workspace:bitbucket; an access token needs the connection to name its workspace)", err)
	}
	return slugs, more, nil
}

// listBitbucketBranches lists one repository's branches, searched on
// Bitbucket's side like its repositories.
func listBitbucketBranches(ctx context.Context, req ListRequest, workspace, slug string) (Listing[Branch], error) {
	base := bitbucketBase(req.BaseURL)
	token, scheme := bitbucketAuth(req.Token, req.Email)
	endpoint := fmt.Sprintf("%s/repositories/%s/%s/refs/branches?pagelen=%d",
		base, url.PathEscape(workspace), url.PathEscape(slug), bitbucketPageSize)
	if query := bitbucketQueryText(req.Query); query != "" {
		endpoint += "&q=" + url.QueryEscape(`name ~ "`+query+`"`)
	}
	out := Listing[Branch]{Items: []Branch{}}
	stopped, err := walkBitbucket(ctx, endpoint, token, scheme, bitbucketMaxPages, func(b Branch) bool {
		if len(out.Items) == MaxListed {
			return false
		}
		out.Items = append(out.Items, Branch{Name: b.Name})
		return true
	})
	if err != nil {
		return Listing[Branch]{}, err
	}
	out.Truncated = stopped
	return out, nil
}

// --- commits ---

// ResolveBitbucketCommit turns the abbreviated hash a Bitbucket pull request
// webhook carries into the whole one, asking the repository the pull request
// targets. fullName is workspace/slug.
func ResolveBitbucketCommit(ctx context.Context, req ListRequest, fullName, short string) (string, error) {
	if !ValidRepoName("bitbucket", fullName) {
		return "", fmt.Errorf("%q is not a repository's name", fullName)
	}
	short = strings.ToLower(strings.TrimSpace(short))
	if len(short) < 7 || len(short) > 64 || strings.Trim(short, "0123456789abcdef") != "" {
		return "", fmt.Errorf("%q is not a commit's hash", short)
	}
	workspace, slug, _ := strings.Cut(fullName, "/")
	token, scheme := bitbucketAuth(req.Token, req.Email)
	endpoint := fmt.Sprintf("%s/repositories/%s/%s/commit/%s",
		bitbucketBase(req.BaseURL), url.PathEscape(workspace), url.PathEscape(slug), short)
	var body struct {
		Hash string `json:"hash"`
	}
	if err := getJSON(ctx, endpoint, token, scheme, &body); err != nil {
		return "", err
	}
	full := strings.ToLower(body.Hash)
	if !FullCommit(full) || !strings.HasPrefix(full, short) {
		return "", fmt.Errorf("the commit %s was answered with %q, which is not it", short, body.Hash)
	}
	return full, nil
}

// --- webhooks ---

// ensureBitbucketHook registers the panel's webhook on a repository, with the
// connection's secret, which Bitbucket then signs every delivery with. It
// needs a token with the webhook scopes and the repository and pull request
// ones the events need; one without them is told to add the hook by hand.
func ensureBitbucketHook(ctx context.Context, req HookRequest, workspace, slug string) HookResult {
	base := bitbucketBase(req.BaseURL)
	token, scheme := bitbucketAuth(req.Token, req.Email)
	endpoint := fmt.Sprintf("%s/repositories/%s/%s/hooks", base, url.PathEscape(workspace), url.PathEscape(slug))

	found := false
	_, err := walkBitbucket(ctx, fmt.Sprintf("%s?pagelen=%d", endpoint, bitbucketPageSize), token, scheme, 5,
		func(hook struct {
			URL string `json:"url"`
		}) bool {
			found = sameHookTarget(hook.URL, req.DeliverTo)
			return !found
		})
	if err != nil {
		return HookResult{Reason: err.Error()}
	}
	if found {
		return HookResult{AlreadyThere: true}
	}
	return sendHook(ctx, http.MethodPost, endpoint, token, scheme, map[string]any{
		"description": version.Name,
		"url":         req.DeliverTo,
		"active":      true,
		// Bitbucket signs a delivery only for a hook whose secret is set, and
		// never shows the secret again. See VerifyBitbucketSignature.
		"secret": req.Secret,
		"events": BitbucketEvents,
	})
}

// --- reporting back ---

// bitbucketState maps onto Bitbucket's build states. STOPPED is the fourth,
// for a build somebody cancelled, which the panel has no state for.
func bitbucketState(state State) string {
	switch state {
	case StateSuccess:
		return "SUCCESSFUL"
	case StateFailure:
		return "FAILED"
	}
	return "INPROGRESS"
}

// bitbucketStatusKey is the key a status is stored under. Bitbucket refuses a
// key over forty characters, and skifity/<environment>/<app> is often longer,
// so the key is a hash of the check's name and the name itself — which is what
// Bitbucket shows — goes in the status's name. Posting the same key again
// replaces the status, so each check keeps one line per commit.
func bitbucketStatusKey(name string) string {
	sum := sha256.Sum256([]byte(name))
	return "skifity-" + hex.EncodeToString(sum[:])[:32]
}

func reportBitbucketStatus(ctx context.Context, req StatusRequest, workspace, slug, description string) error {
	token, scheme := bitbucketAuth(req.Token, req.Email)
	endpoint := fmt.Sprintf("%s/repositories/%s/%s/commit/%s/statuses/build",
		bitbucketBase(req.BaseURL), url.PathEscape(workspace), url.PathEscape(slug), req.CommitSHA)
	body := map[string]any{
		"key":         bitbucketStatusKey(req.Context),
		"state":       bitbucketState(req.State),
		"name":        req.Context,
		"description": description,
	}
	// Left out rather than sent empty: a status whose link goes nowhere is
	// refused, and one with no link is still worth having.
	if req.TargetURL != "" {
		body["url"] = req.TargetURL
	}
	if req.Ref != "" {
		body["refname"] = req.Ref
	}
	return expectOK(send(ctx, http.MethodPost, endpoint, token, scheme, body))
}

// bitbucketMarker is how the panel's comment is found again on Bitbucket. The
// HTML comment the other hosts hide is escaped by Bitbucket and shown as text;
// a Markdown link reference nothing refers to is read by the renderer and
// shows nothing, and stays in the comment's raw text to be found by.
func bitbucketMarker(marker string) string {
	return "[//]: # (" + strings.NewReplacer("(", "", ")", "", "\n", " ").Replace(marker) + ")"
}

// bitbucketComment is a pull request comment as Bitbucket lists it.
type bitbucketComment struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
}

// upsertBitbucketComment writes the panel's comment on a pull request, or
// rewrites the one it wrote before.
func upsertBitbucketComment(ctx context.Context, req CommentRequest, workspace, slug, number string) error {
	token, scheme := bitbucketAuth(req.Token, req.Email)
	comments := fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%s/comments",
		bitbucketBase(req.BaseURL), url.PathEscape(workspace), url.PathEscape(slug), number)
	marker := bitbucketMarker(req.Marker)
	body := marker + "\n\n" + req.Body

	var mine *bitbucketComment
	_, err := walkBitbucket(ctx, fmt.Sprintf("%s?pagelen=%d", comments, bitbucketPageSize), token, scheme, 5,
		func(comment bitbucketComment) bool {
			if !comment.Deleted && strings.HasPrefix(comment.Content.Raw, marker) {
				mine = &comment
				return false
			}
			return true
		})
	if err != nil {
		return err
	}
	payload := map[string]any{"content": map[string]string{"raw": body}}
	if mine != nil {
		if mine.Content.Raw == body {
			return nil
		}
		return expectOK(send(ctx, http.MethodPut, comments+"/"+strconv.FormatInt(mine.ID, 10), token, scheme, payload))
	}
	return expectOK(send(ctx, http.MethodPost, comments, token, scheme, payload))
}

// --- reading a repository ---

// readBitbucketTree lists a repository's files at a ref, the main branch when
// none is given, and answers the ref it read so the files come from the same
// place. Bitbucket lists a directory and, with max_depth, what is under it,
// giving every path from the repository's root.
func readBitbucketTree(ctx context.Context, req TreeRequest, workspace, slug, token string) (FileTree, string, error) {
	credential, scheme := bitbucketAuth(token, req.Email)
	repository := fmt.Sprintf("%s/repositories/%s/%s", bitbucketBase(req.BaseURL), url.PathEscape(workspace), url.PathEscape(slug))

	ref := strings.TrimSpace(req.Ref)
	if ref == "" {
		var body struct {
			Mainbranch *struct {
				Name string `json:"name"`
			} `json:"mainbranch"`
		}
		if err := getJSON(ctx, repository, credential, scheme, &body); err != nil {
			return FileTree{}, "", err
		}
		if body.Mainbranch == nil || body.Mainbranch.Name == "" {
			return FileTree{}, "", fmt.Errorf("the repository has no main branch yet")
		}
		ref = body.Mainbranch.Name
	}

	dir := strings.Trim(strings.TrimSpace(req.RootDir), "/")
	listing := repository + "/src/" + url.PathEscape(ref) + "/"
	if dir != "" {
		listing += escapePath(dir) + "/"
	}
	listing += fmt.Sprintf("?max_depth=%d&pagelen=%d", bitbucketTreeDepth, bitbucketPageSize)

	var tree FileTree
	stopped, err := walkBitbucket(ctx, listing, credential, scheme, MaxTreeEntries/bitbucketPageSize,
		func(entry struct {
			Path string `json:"path"`
			Type string `json:"type"`
		}) bool {
			switch entry.Type {
			case "commit_file":
				tree.Files = append(tree.Files, entry.Path)
			case "commit_directory":
				// A directory as deep as the listing goes has contents it
				// did not list.
				relative := strings.TrimPrefix(entry.Path, dir+"/")
				if strings.Count(strings.Trim(relative, "/"), "/")+1 >= bitbucketTreeDepth {
					tree.Truncated = true
				}
			}
			return len(tree.Files) < MaxTreeEntries
		})
	if err != nil {
		return FileTree{}, "", err
	}
	if stopped {
		tree.Truncated = true
	}
	return tree, ref, nil
}

// readBitbucketFile fetches one file's contents, raw, at a ref.
func readBitbucketFile(ctx context.Context, req TreeRequest, workspace, slug, token, file string) (string, error) {
	credential, scheme := bitbucketAuth(token, req.Email)
	endpoint := fmt.Sprintf("%s/repositories/%s/%s/src/%s/%s", bitbucketBase(req.BaseURL),
		url.PathEscape(workspace), url.PathEscape(slug), url.PathEscape(req.Ref), escapePath(file))
	return getText(ctx, endpoint, credential, scheme)
}
