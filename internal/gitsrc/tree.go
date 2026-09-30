package gitsrc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"skifity/internal/netguard"
	"skifity/internal/version"
)

// Reading a repository without cloning it.
//
// The panel could work out that a repository is a Next.js app that listens on
// 3000 — the detector for it was written and tested — and it never ran, because
// nothing could give it the repository's file list. Railpack does its own
// detection inside the build, so builds worked; what was missing was the panel
// saying anything at all while somebody was still filling in the form.
//
// Cloning to find out would mean a Job, a volume and a minute of waiting for a
// question asked while a person is typing. Every provider has an API that lists
// a tree and reads a file, so this uses those: two requests, under a second,
// and nothing is written anywhere.

// MaxTreeEntries bounds what is read from a repository.
//
// A monorepo has hundreds of thousands of paths and the detector looks at the
// top of the tree. Reading all of them to answer a question about the root
// would be slow for no gain, and is also how a hostile repository makes the
// panel allocate until it dies.
const MaxTreeEntries = 4000

// readableFiles are fetched in full, because their contents decide the answer:
// a package.json says Next.js or Vite, a go.mod says nothing useful but a
// requirements.txt says Django.
// A Dockerfile is here because its EXPOSE line is where the port comes from,
// and a Compose file because its services are what the panel offers to turn
// into apps. Both were detected from the file list alone and then read as an
// empty string, so neither answer was ever produced.
var readableFiles = []string{
	"package.json", "go.mod", "requirements.txt", "pyproject.toml",
	"composer.json", "Gemfile", "Cargo.toml", "pom.xml", "build.gradle",
	"deno.json", "bun.lockb", "Procfile",
	"Dockerfile", "dockerfile", "docker/Dockerfile", "build/Dockerfile",
	"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml",
	// What the app will need once it runs. A Prisma schema names its database
	// outright, and an .env.example lists the settings the app reads — the
	// template, never the real file. See the test that holds that line.
	"prisma/schema.prisma", "schema.prisma",
	".env.example", ".env.sample", ".env.template",
}

// ReadableFiles is the same list, for a detector reading a folder on disk
// instead of a repository: `skifity up` reads exactly what the panel would.
func ReadableFiles() []string { return append([]string(nil), readableFiles...) }

// TreeRequest is a repository to look at.
type TreeRequest struct {
	// RepoURL is the browser address of the repository.
	RepoURL string
	// Ref is a branch, tag or commit. Empty means the default branch.
	Ref string
	// RootDir narrows the answer to a subdirectory, for a monorepo.
	RootDir string
	// Kind is github, gitlab or gitea. Empty is worked out from the host.
	Kind string
	// BaseURL is the provider's own address, for a self-hosted instance.
	BaseURL string
	// Token authenticates, and is only sent to the host BaseURL names. A public
	// repository needs none.
	Token string
}

// FileTree is a repository's paths and the contents of the few files that
// decide what it is.
type FileTree struct {
	Files    []string
	Contents map[string]string
	// Truncated is true when the repository has more paths than were read. The
	// answer is still usable — the detector reads the top of the tree — and
	// saying so is what keeps a "no framework found" honest.
	Truncated bool
}

// client is shared so lookups reuse connections and always time out.
//
// Fifteen seconds: this runs while somebody is looking at a form, and a
// provider that has not answered by then has effectively said no.
//
// Guarded, because a Git connection's base URL is a setting and this request is
// made by the panel's own process. See internal/netguard.
var client = netguard.Client(15 * time.Second)

// ReadTree lists a repository's files and reads the ones that matter.
func ReadTree(ctx context.Context, req TreeRequest) (FileTree, error) {
	owner, repo, err := ownerAndRepo(req.RepoURL)
	if err != nil {
		return FileTree{}, err
	}

	kind := req.Kind
	if kind == "" {
		kind = KindFor(req.RepoURL)
	}
	// A token belongs to one host. Sending it anywhere else is the same bug
	// the build had: the credential goes to whoever owns the address somebody
	// typed into a form.
	token := req.Token
	if token != "" && req.BaseURL != "" && !SameHost(req.RepoURL, req.BaseURL) {
		token = ""
	}

	var tree FileTree
	switch kind {
	case "gitlab":
		tree, err = readGitLabTree(ctx, req, owner, repo, token)
	case "gitea":
		tree, err = readGiteaTree(ctx, req, owner, repo, token)
	default:
		tree, err = readGitHubTree(ctx, req, owner, repo, token)
	}
	if err != nil {
		return FileTree{}, err
	}

	tree.Files = narrow(tree.Files, req.RootDir)
	tree.Contents = map[string]string{}
	for _, name := range readableFiles {
		if !contains(tree.Files, name) {
			continue
		}
		content, err := readFile(ctx, kind, req, owner, repo, token, path(req.RootDir, name))
		if err != nil {
			// One file that could not be read is not a failure: the detector
			// works from the file list alone, less precisely.
			continue
		}
		tree.Contents[name] = content
	}
	return tree, nil
}

// KindFor guesses the provider from the address, for a repository added
// without a connected account.
func KindFor(repoURL string) string {
	host := strings.ToLower(hostOf(repoURL))
	switch {
	case strings.Contains(host, "gitlab"):
		return "gitlab"
	case strings.Contains(host, "gitea"), strings.Contains(host, "codeberg"):
		return "gitea"
	default:
		return "github"
	}
}

// --- providers ---

func readGitHubTree(ctx context.Context, req TreeRequest, owner, repo, token string) (FileTree, error) {
	base := apiBase(req.BaseURL, "https://api.github.com", "/api/v3")
	ref := req.Ref
	if ref == "" {
		ref = "HEAD"
	}
	var body struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1",
		base, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(ref))
	if err := getJSON(ctx, endpoint, token, "token", &body); err != nil {
		return FileTree{}, err
	}

	tree := FileTree{Truncated: body.Truncated}
	for _, entry := range body.Tree {
		if entry.Type != "blob" {
			continue
		}
		tree.Files = append(tree.Files, entry.Path)
		if len(tree.Files) >= MaxTreeEntries {
			tree.Truncated = true
			break
		}
	}
	return tree, nil
}

func readGitLabTree(ctx context.Context, req TreeRequest, owner, repo, token string) (FileTree, error) {
	base := apiBase(req.BaseURL, "https://gitlab.com", "") + "/api/v4"
	project := url.PathEscape(owner + "/" + repo)

	var tree FileTree
	// GitLab pages its tree and says nothing about a total, so this walks
	// until a short page or the ceiling.
	for page := 1; page <= MaxTreeEntries/100; page++ {
		var body []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		}
		endpoint := fmt.Sprintf("%s/projects/%s/repository/tree?recursive=true&per_page=100&page=%d",
			base, project, page)
		if req.Ref != "" {
			endpoint += "&ref=" + url.QueryEscape(req.Ref)
		}
		if err := getJSON(ctx, endpoint, token, "bearer", &body); err != nil {
			if page == 1 {
				return FileTree{}, err
			}
			break
		}
		for _, entry := range body {
			if entry.Type != "blob" {
				continue
			}
			tree.Files = append(tree.Files, entry.Path)
		}
		if len(body) < 100 {
			return tree, nil
		}
		if len(tree.Files) >= MaxTreeEntries {
			tree.Truncated = true
			break
		}
	}
	return tree, nil
}

func readGiteaTree(ctx context.Context, req TreeRequest, owner, repo, token string) (FileTree, error) {
	base := apiBase(req.BaseURL, "https://codeberg.org", "") + "/api/v1"
	ref := req.Ref
	if ref == "" {
		ref = "HEAD"
	}
	var body struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=true&per_page=%d",
		base, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(ref), MaxTreeEntries)
	if err := getJSON(ctx, endpoint, token, "token", &body); err != nil {
		return FileTree{}, err
	}

	tree := FileTree{Truncated: body.Truncated}
	for _, entry := range body.Tree {
		if entry.Type != "blob" {
			continue
		}
		tree.Files = append(tree.Files, entry.Path)
	}
	return tree, nil
}

// readFile fetches one file's contents.
func readFile(ctx context.Context, kind string, req TreeRequest, owner, repo, token, file string) (string, error) {
	switch kind {
	case "gitlab":
		base := apiBase(req.BaseURL, "https://gitlab.com", "") + "/api/v4"
		endpoint := fmt.Sprintf("%s/projects/%s/repository/files/%s/raw",
			base, url.PathEscape(owner+"/"+repo), url.PathEscape(file))
		ref := req.Ref
		if ref == "" {
			ref = "HEAD"
		}
		endpoint += "?ref=" + url.QueryEscape(ref)
		return getText(ctx, endpoint, token, "bearer")

	case "gitea":
		base := apiBase(req.BaseURL, "https://codeberg.org", "") + "/api/v1"
		endpoint := fmt.Sprintf("%s/repos/%s/%s/raw/%s",
			base, url.PathEscape(owner), url.PathEscape(repo), file)
		if req.Ref != "" {
			endpoint += "?ref=" + url.QueryEscape(req.Ref)
		}
		return getText(ctx, endpoint, token, "token")

	default:
		base := apiBase(req.BaseURL, "https://api.github.com", "/api/v3")
		endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s",
			base, url.PathEscape(owner), url.PathEscape(repo), file)
		if req.Ref != "" {
			endpoint += "?ref=" + url.QueryEscape(req.Ref)
		}
		var body struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		if err := getJSON(ctx, endpoint, token, "token", &body); err != nil {
			return "", err
		}
		if body.Encoding != "base64" {
			return body.Content, nil
		}
		// GitHub wraps the base64 at 60 columns, which the decoder refuses.
		decoded, err := base64.StdEncoding.DecodeString(
			strings.NewReplacer("\n", "", "\r", "").Replace(body.Content))
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	}
}

// --- plumbing ---

// maxFileBytes bounds one file. A package.json is a few kilobytes; anything
// past this is not one, and reading it would be the same allocation problem as
// an unbounded tree.
const maxFileBytes = 512 * 1024

func getJSON(ctx context.Context, endpoint, token, scheme string, into any) error {
	body, err := get(ctx, endpoint, token, scheme)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

func getText(ctx context.Context, endpoint, token, scheme string) (string, error) {
	body, err := get(ctx, endpoint, token, scheme)
	return string(body), err
}

func get(ctx context.Context, endpoint, token, scheme string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if token != "" {
		switch scheme {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+token)
		default:
			req.Header.Set("Authorization", "token "+token)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ask the Git provider about this repository: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("the repository, or that branch, was not found")
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("the Git provider refused: connect an account with access to this repository")
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("the Git provider answered %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxFileBytes))
}

// apiBase turns a provider's browser address into its API address.
func apiBase(baseURL, hosted, selfHostedSuffix string) string {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || baseURL == hosted ||
		baseURL == "https://github.com" || baseURL == "https://gitlab.com" {
		return hosted
	}
	return baseURL + selfHostedSuffix
}

// ownerAndRepo pulls the two path segments out of a repository address.
func ownerAndRepo(repoURL string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return "", "", fmt.Errorf("that does not look like a repository address")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("that address does not name an owner and a repository")
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}

func hostOf(repoURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// narrow drops everything outside a monorepo's subdirectory and rebases the
// rest, so the detector sees that directory as though it were the repository.
func narrow(files []string, rootDir string) []string {
	rootDir = strings.Trim(strings.TrimSpace(rootDir), "/")
	if rootDir == "" {
		return files
	}
	prefix := rootDir + "/"
	out := make([]string, 0, len(files))
	for _, file := range files {
		if strings.HasPrefix(file, prefix) {
			out = append(out, strings.TrimPrefix(file, prefix))
		}
	}
	return out
}

// path joins a monorepo's root back on, for fetching one file.
func path(rootDir, name string) string {
	rootDir = strings.Trim(strings.TrimSpace(rootDir), "/")
	if rootDir == "" {
		return name
	}
	return rootDir + "/" + name
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
