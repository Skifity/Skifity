# Cutting a release

A release is an invitation to install. Everything in this list exists so that
the person who accepts it does not become the first person to find out something
was never true.

## Before the tag

**The cluster run has to have passed.** `test/cluster/verify.sh` on a real
server, or `make verify-remote HOST=root@…` from here. `docs/checklist.md` says
which of the eighteen rows that moves; until it has run once, a release ships
code that has never met a cluster.

Then the ordinary gates:

```sh
make check      # every linter, the tests, the vulnerability scan
make smoke      # the panel and the installer, against the real binary
make e2e        # the interface, against the real binary
```

And the release itself, dry, which nothing else runs:

```sh
IMAGE_REPO=ghcr.io/skifity/skifity REPO_URL=https://github.com/Skifity/Skifity \
  goreleaser release --snapshot --clean --skip=publish
```

Add `docker` to `--skip` on a machine with no Docker daemon, and read
`dist/checksums.txt`: those are the names people will download. The first dry
run found three things the first tag would have got wrong — an image build
copying from the wrong path, an image tag without its `v`, and `.exe.exe`.

## The tag

The installer names the release it installs, in its own source, because it has
to work when it is one file downloaded by `curl` with no repository around it.
So the release commit sets that line and the tag points at that commit:

```sh
# 1. Say which release this is. One line, in installer/install.sh.
RELEASED_VERSION="v0.1.0"

# 2. Commit it.
git commit -am "chore(release): v0.1.0"

# 3. Tag that commit.
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The workflow refuses a tag whose installer disagrees with it — a step reads
`RELEASED_VERSION` out of `installer/install.sh` and fails the release when it is
not the tag being built. Getting that wrong would publish an installer that
either pulls the wrong image or refuses to install at all, and it would be found
by a stranger rather than by CI.

## What the tag does

`.github/workflows/release.yml` builds the binaries with GoReleaser and pushes a
multi-architecture image to `ghcr.io/<this repository, lowercased>`, tagged
`v0.1.0`, `0.1.0` and `latest`. Nothing here names a registry path: it is
derived from `GITHUB_REPOSITORY`, so it is correct wherever the repository
lives.

The image carries the CLI for every other platform, gzipped, so the panel can
hand a Mac or a Windows laptop its CLI (`scripts/release-cli.sh`). A second job
signs SLSA provenance for every binary in `checksums.txt` and attaches it to the
release as `skifity.intoto.jsonl`:

```sh
slsa-verifier verify-artifact skifity-linux-amd64 \
  --provenance-path skifity.intoto.jsonl \
  --source-uri github.com/Skifity/Skifity --source-tag v0.1.0
```

`.github/workflows/release-dry-run.yml` builds all of this without publishing
whenever a file the release is made of changes, and looks inside both images, so
the tag is not the first time the pipeline runs.

`installer/install.sh` and `installer/uninstall.sh` are attached to the release
and listed in `checksums.txt`. That is what lets the README's install command
name no version:

```sh
curl -fsSL https://github.com/<repo>/releases/latest/download/install.sh | sudo sh
```

GitHub sends `releases/latest/download/` to the newest release that is neither
a draft nor a pre-release, so the link is always the newest installer — and
each installer installs its own release. `RELEASED_VERSION` inside it is the
tag, and it fetches `deploy/*.yaml` from that tag, so the objects applied are
the ones that version's image was built with. An installer that read them from
a branch would eventually apply a Deployment to an image that had never seen
it. The release notes give both that link and the one for the release itself,
`releases/download/<tag>/install.sh`.

Until the first release is published the link is a 404, which is why the tag
is not optional.

## After the tag

1. **Install from the published command on a throwaway server**, not from a
   clone. It is the only way to find out whether the image is public, whether
   the manifests are reachable at that ref, and whether the CLI download works.
2. **Upgrade an existing install** to it by running the install command again,
   and check that the database was copied first, the apps kept answering, and
   the commands it prints go back.
3. Move any row in `docs/checklist.md` that the run proved, and say which run
   proved it.

## Where this project publishes

Two lines: `PROJECT_REPO` in `installer/install.sh` and `PROJECT_REPO` in the
`Makefile`. Everything else derives from them, and `scripts/check-home.sh` fails
the build if a name this project does not own appears anywhere else. Moving to an
organisation of its own is those two lines and a re-run of `make check`.
