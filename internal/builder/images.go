package builder

// The images every build runs, pinned by version and by digest.
//
// They were `latest` and `master`, which is two problems at once. A build on
// Tuesday could run different tools from the build on Monday with nothing in
// the repository changed, which is the opposite of what a fingerprint that
// decides whether to rebuild is supposed to guarantee. And the build runs
// somebody's install scripts with the team's build variables in its
// environment: whoever can move a tag the panel follows chooses what runs
// there.
//
// Two of them were also wrong, and never ran because nothing here has had a
// cluster. `ghcr.io/railwayapp/railpack` is not a public image, so every
// Railpack build would have stopped at pulling its first step; and
// `ghcr.io/railwayapp/nixpacks` is Nixpacks' base image, which has no
// `nixpacks` command in it. See docs/progress.md, Phase 80.
//
// The digests were read from the registries on 2026-09-30. To move one, read
// the new tag's index digest the same way and change the version and the
// digest together.
const (
	// railpackVersion is used for both halves of a Railpack build: the step
	// that writes the plan and the BuildKit frontend that reads it. They are
	// one program, and a plan written by one version and read by another is
	// not a format anybody promises to keep.
	railpackVersion = "v0.40.1"
	// RailpackImage is the frontend image. It is Alpine with the complete
	// railpack binary at /railpack, `prepare` included, which is why the plan
	// step runs in it rather than in an image of its own.
	RailpackImage = "ghcr.io/railwayapp/railpack-frontend:" + railpackVersion +
		"@sha256:f1973377693af30c9b37a92c97c661c07b277ccdc6be909213c74c771f8d2d6d"

	// BuildKitVersion is the version of the BuildKit every build starts for
	// itself.
	BuildKitVersion = "v0.33.0"
	// BuildKitImage is the rootless builder: the daemon and buildctl, which
	// the build container runs together. It runs as an ordinary user, which
	// can read the workspace the clone step left behind and write nothing in
	// it, and does not have to: what the build writes is its own.
	BuildKitImage = "moby/buildkit:" + BuildKitVersion + "-rootless" +
		"@sha256:80b15f0735e87bab7bf59ec4d695dfb4a7cfb25521cf56dc75d6f256285b63ef"

	// GitImage fetches the repository.
	GitImage = "alpine/git:v2.54.0@sha256:832b1cd1a271509f3d5272a1a62d4cb2ab1a53426ebde1f6c2cb7349f907dc6f"
)
