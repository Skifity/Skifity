package registry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These run against a real HTTP server speaking the registry's v2 API, so the
// requests are built, sent and parsed for real. What is faked is the registry,
// not the client.

func TestRepoAndTag(t *testing.T) {
	cases := []struct {
		image, repo, tag string
		ok               bool
	}{
		// What the panel actually builds: the host has a port, so a naive
		// split on the last colon would take 5000/acme-prod/web as a tag.
		{"skifity-registry.skifity-builds.svc.cluster.local:5000/acme-prod/web:a1b2c3d4e5f6",
			"acme-prod/web", "a1b2c3d4e5f6", true},
		{"registry:5000/acme-prod/web:buildcache", "acme-prod/web", "buildcache", true},
		{"ghcr.io/example/skifity:v1.2.3", "example/skifity", "v1.2.3", true},
		// A reference with no tag is not something a sweep should guess about.
		{"registry:5000/acme-prod/web", "", "", false},
		{"nginx", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		repo, tag, ok := RepoAndTag(c.image)
		if ok != c.ok || repo != c.repo || tag != c.tag {
			t.Errorf("RepoAndTag(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.image, repo, tag, ok, c.repo, c.tag, c.ok)
		}
	}
}

func TestPrunableKeepsWhatCanStillBeReached(t *testing.T) {
	tags := []string{"aaa", "bbb", "ccc", CacheTag}
	keep := map[string]bool{"ccc": true}

	got := Prunable(tags, keep)
	if len(got) != 2 || got[0] != "aaa" || got[1] != "bbb" {
		t.Fatalf("prunable tags are %v, want aaa and bbb", got)
	}

	// The cache tag is never prunable. Removing it does not break anything
	// visible, which is why it would go unnoticed: every app's next build would
	// simply be a cold build.
	for _, tag := range got {
		if tag == CacheTag {
			t.Fatal("the build cache would be deleted, making every next build a cold one")
		}
	}

	// A repository nothing wants is an app that was deleted, and all of it goes
	// except the cache.
	all := Prunable(tags, nil)
	if len(all) != 3 {
		t.Fatalf("a deleted app left %d tags, want all three removed", len(all))
	}
}

func TestSweepingARepository(t *testing.T) {
	deleted := []string{}
	accepts := ""

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/_catalog":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"repositories":["acme-prod/web","gone/app"]}`))

		case strings.HasSuffix(r.URL.Path, "/tags/list"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tags":["old1","old2","current","buildcache"]}`))

		case r.Method == http.MethodHead:
			accepts = r.Header.Get("Accept")
			// The digest is what a delete takes, and it only comes back when
			// the request said which manifest types it understands.
			w.Header().Set("Docker-Content-Digest", "sha256:"+strings.TrimPrefix(
				r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], "sha256:"))
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodDelete:
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusAccepted)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := New(strings.TrimPrefix(server.URL, "http://"))
	ctx := t.Context()

	repos, err := client.Repositories(ctx)
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("the catalogue has %d repositories, want 2", len(repos))
	}

	tags, err := client.Tags(ctx, "acme-prod/web")
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	prunable := Prunable(tags, map[string]bool{"current": true})
	if len(prunable) != 2 {
		t.Fatalf("prunable is %v, want old1 and old2", prunable)
	}

	for _, tag := range prunable {
		digest, err := client.Digest(ctx, "acme-prod/web", tag)
		if err != nil {
			t.Fatalf("Digest(%s): %v", tag, err)
		}
		if err := client.DeleteManifest(ctx, "acme-prod/web", digest); err != nil {
			t.Fatalf("DeleteManifest(%s): %v", tag, err)
		}
	}

	if len(deleted) != 2 {
		t.Fatalf("%d manifests were deleted, want 2", len(deleted))
	}
	// Without this header the registry answers with the digest of a converted
	// schema-1 manifest, and deleting that either fails or removes the wrong
	// thing.
	if !strings.Contains(accepts, "application/vnd.oci.image.manifest.v1+json") {
		t.Errorf("the digest was asked for without naming the manifest types: %q", accepts)
	}
}

func TestARegistryThatRefusesDeletesSaysSo(t *testing.T) {
	// The registry is started with deletion enabled; if somebody changes that,
	// the sweep must not report success while the disk stays exactly as full.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	client := New(strings.TrimPrefix(server.URL, "http://"))
	err := client.DeleteManifest(t.Context(), "acme-prod/web", "sha256:abc")
	if err == nil {
		t.Fatal("a registry that refuses deletes reported success")
	}
	if !strings.Contains(err.Error(), "REGISTRY_STORAGE_DELETE_ENABLED") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}
}

func TestAnAppThatWasNeverBuiltIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tags, err := New(strings.TrimPrefix(server.URL, "http://")).Tags(t.Context(), "acme-prod/web")
	if err != nil {
		t.Fatalf("a repository that does not exist gave %v, want no error", err)
	}
	if len(tags) != 0 {
		t.Fatalf("got %v tags for a repository that does not exist", tags)
	}
}

// A reference this sweep cannot read has to say so, rather than producing a
// repository that does not exist.
//
// What is kept is worked out from these. A reference read as the repository
// "ns/app@sha256" leaves the real "ns/app" with nothing marked as worth
// keeping, and everything in it becomes prunable — including the image the app
// is running. The comment always said a digest was not something to guess
// about; the code guessed.
func TestRepoAndTagRefusesWhatItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		image string
	}{
		{"a digest, which has a colon of its own",
			"skifity-registry:5000/team/web@sha256:" +
				"0000000000000000000000000000000000000000000000000000000000000000"},
		{"a digest with no tag anywhere",
			"ghcr.io/example/app@sha256:" +
				"1111111111111111111111111111111111111111111111111111111111111111"},
		{"an image from Docker Hub, which is not in this registry",
			"library/nginx:1.25"},
		{"a bare name", "nginx"},
		{"no tag", "skifity-registry:5000/team/web"},
		{"nothing", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, tag, ok := RepoAndTag(tc.image)
			if ok {
				t.Errorf("read %q as %q:%q; it should have said it could not", tc.image, repo, tag)
			}
		})
	}
}

// The references the panel really builds still read correctly.
func TestRepoAndTagReadsWhatThePanelBuilds(t *testing.T) {
	for _, tc := range []struct{ image, repo, tag string }{
		{"skifity-registry.skifity-builds.svc.cluster.local:5000/team/web:abc123", "team/web", "abc123"},
		{"skifity-registry:5000/team/web:buildcache", "team/web", "buildcache"},
		{"localhost:5000/team/web:v2", "team/web", "v2"},
		{"ghcr.io/example/app:1.4.0", "example/app", "1.4.0"},
		// A deeper path, which a registry may well have.
		{"registry.example.com/team/group/web:abc", "team/group/web", "abc"},
	} {
		t.Run(tc.image, func(t *testing.T) {
			repo, tag, ok := RepoAndTag(tc.image)
			if !ok {
				t.Fatalf("could not read %q", tc.image)
			}
			if repo != tc.repo || tag != tc.tag {
				t.Errorf("read %q as %q:%q, want %q:%q", tc.image, repo, tag, tc.repo, tc.tag)
			}
		})
	}
}

// The consequence, stated as the property that matters: a reference the sweep
// cannot read must never leave a repository unprotected by being half-read.
func TestAnUnreadableReferenceProtectsNothingAndSpeaksForNothing(t *testing.T) {
	keep := map[string]map[string]bool{}
	for _, image := range []string{
		"skifity-registry:5000/team/web:live",
		"ghcr.io/example/app@sha256:" +
			"2222222222222222222222222222222222222222222222222222222222222222",
	} {
		repo, tag, ok := RepoAndTag(image)
		if !ok {
			continue
		}
		if keep[repo] == nil {
			keep[repo] = map[string]bool{}
		}
		keep[repo][tag] = true
	}

	if len(keep) != 1 {
		t.Fatalf("the digest reference invented a repository: %v", keep)
	}
	if !keep["team/web"]["live"] {
		t.Error("the image the app is running is not protected")
	}
	if prunable := Prunable([]string{"live", "old", CacheTag}, keep["team/web"]); len(prunable) != 1 ||
		prunable[0] != "old" {
		t.Errorf("the sweep would remove %v", prunable)
	}
}
