// Package registry talks to the in-cluster image registry.
//
// It exists for one reason: every build pushes an image and nothing ever
// removed one. Layers are shared and the images are small, so the disk fills
// slowly rather than suddenly, which is exactly why nobody notices until a node
// has no space left and every pod on it stops at once. That is the worst shape
// a failure can take on a self-hosted cluster, because it arrives long after
// the person stopped thinking about the thing that caused it.
//
// Two halves are needed, and only the first one lives here. Deleting a manifest
// over the API removes the tag and leaves the blobs; the registry's own
// `garbage-collect` is what frees the disk, and that has to run next to the
// files. This package is the part the panel can do by itself, over HTTP, from
// inside the cluster.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"skifity/internal/version"
)

// CacheTag is the tag BuildKit writes its layer cache to. It is never pruned:
// removing it makes the next build of every app a cold build.
const CacheTag = "buildcache"

// KeptPerApp is how many of an app's images the sweep keeps.
//
// Ten, which is the Deployment's revision history: a rollback further back than
// Kubernetes itself remembers is not offered, so the image for it would be kept
// for nobody.
//
// It lives here rather than next to the sweep because it is also the answer to
// "can this deployment still be rolled back to". The panel keeps far more
// deployment records than images, and offering a rollback to a version whose
// image was collected would be offering a deployment that fails on a pull.
const KeptPerApp = 10

// manifestTypes are the media types a manifest may have.
//
// The Accept header is not optional here. Without it the registry answers a
// HEAD with the digest of a converted schema-1 manifest, and deleting that
// digest either fails or removes something other than what was asked for.
var manifestTypes = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// Client is a registry v2 API client.
type Client struct {
	base string
	http *http.Client
}

// New builds a client for a registry reached at host, for example
// skifity-registry.skifity-builds.svc.cluster.local:5000.
//
// Plain HTTP: this registry is only ever reached over the cluster network, and
// giving it a certificate would mean giving every node's container runtime a
// trust store to manage. See ADR-0017.
func New(host string) *Client {
	base := strings.TrimSuffix(host, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	return &Client{base: base, http: &http.Client{Timeout: 30 * time.Second}}
}

// Repositories lists every repository the registry holds.
func (c *Client) Repositories(ctx context.Context) ([]string, error) {
	var page struct {
		Repositories []string `json:"repositories"`
	}
	// n is the page size. The catalogue is paginated by Link header; one large
	// page is enough for any cluster a single panel manages, and asking for
	// more than there are is not an error.
	if err := c.getJSON(ctx, "/v2/_catalog?n=10000", &page); err != nil {
		return nil, err
	}
	return page.Repositories, nil
}

// Tags lists the tags in one repository.
//
// A repository that does not exist is not an error: it is what the registry
// says about an app that has never been built, and the caller wants an empty
// list rather than a failure.
func (c *Client) Tags(ctx context.Context, repo string) ([]string, error) {
	var page struct {
		Tags []string `json:"tags"`
	}
	err := c.getJSON(ctx, "/v2/"+repo+"/tags/list?n=10000", &page)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return page.Tags, nil
}

// Digest resolves a tag to the manifest digest, which is what a delete takes.
func (c *Client) Digest(ctx context.Context, repo, tag string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead,
		c.base+"/v2/"+repo+"/manifests/"+tag, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", manifestTypes)
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("read the manifest for %s:%s: %w", repo, tag, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", errNotFound
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("the registry answered %s for %s:%s", resp.Status, repo, tag)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("the registry did not say what digest %s:%s has", repo, tag)
	}
	return digest, nil
}

// DeleteManifest removes a manifest by digest, which untags every tag pointing
// at it.
//
// Already gone is success, because this is called from a sweep that has to be
// safe to run again after it was interrupted.
func (c *Client) DeleteManifest(ctx context.Context, repo, digest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.base+"/v2/"+repo+"/manifests/"+digest, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("delete %s@%s: %w", repo, digest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode == http.StatusMethodNotAllowed {
		// The registry was started without deletion enabled. Saying so is the
		// point: the sweep would otherwise report that it removed things and
		// the disk would not move.
		return fmt.Errorf("this registry refuses deletes; it needs REGISTRY_STORAGE_DELETE_ENABLED=true")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the registry answered %s deleting %s@%s", resp.Status, repo, digest)
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ask the registry for %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("the registry answered %s for %s: %s",
			resp.Status, path, strings.TrimSpace(string(snippet)))
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// errNotFound is returned for a repository or tag the registry does not have.
var errNotFound = errors.New("not in the registry")

// IsNotFound reports whether an error means the registry has no such thing.
func IsNotFound(err error) bool { return errors.Is(err, errNotFound) }

// RepoAndTag splits an image reference into the repository path and the tag.
//
// The reference the panel builds is <host>:<port>/<namespace>/<app>:<tag>, so
// the host has a colon in it too and a naive split on ":" takes the port for a
// tag. The tag is the last colon after the last slash, and the repository is
// everything after the first slash.
func RepoAndTag(image string) (repo, tag string, ok bool) {
	slash := strings.Index(image, "/")
	if slash < 0 {
		return "", "", false
	}
	// The first segment has to be a registry host, by Docker's own rule: it
	// contains a dot or a colon, or it is localhost. Without this, "library/
	// nginx:1.25" — an image from Docker Hub that is not in this registry at
	// all — reads as the repository "nginx", and would speak for a repository
	// of that name that happens to be here.
	if !looksLikeHost(image[:slash]) {
		return "", "", false
	}
	rest := image[slash+1:]

	// A digest reference is not a tag, and the two are easy to confuse because
	// "@sha256:..." has a colon in it. Splitting on the last colon turned
	// "ns/app@sha256:abc" into the repository "ns/app@sha256" and the tag
	// "abc": a repository that does not exist, so the real one is left with
	// nothing marked as worth keeping. The comment here always said a digest
	// was not something to guess about; the code did not check.
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		return "", "", false
	}

	colon := strings.LastIndex(rest, ":")
	if colon < 0 || colon < strings.LastIndex(rest, "/") {
		// No tag. A bare name is not something this sweep should guess about.
		return "", "", false
	}
	repo, tag = rest[:colon], rest[colon+1:]
	if repo == "" || tag == "" {
		return "", "", false
	}
	return repo, tag, true
}

// looksLikeHost reports whether a reference's first segment names a registry.
//
// Docker's rule, and the only one that can tell "library/nginx" from
// "registry:5000/app": a host has a dot or a port, or it is localhost.
func looksLikeHost(segment string) bool {
	return segment == "localhost" ||
		strings.ContainsAny(segment, ".:")
}

// Prunable decides which of a repository's tags may be removed.
//
// keep is the set of tags that are still referenced: the image an app is
// running and the images of the deployments it could still be rolled back to.
// The cache tag is never prunable, and neither is anything in keep. What is
// left is a build nobody can reach any more.
//
// A repository with nothing to keep is an app that was deleted, and every tag
// goes.
func Prunable(tags []string, keep map[string]bool) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag == CacheTag || keep[tag] {
			continue
		}
		out = append(out, tag)
	}
	return out
}
