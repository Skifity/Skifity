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
multi-architecture image to `ghcr.io/<this repository, lowercased>`, tagged with
the version and with `latest`. Nothing here names a registry path: it is derived
from `GITHUB_REPOSITORY`, so it is correct wherever the repository lives.

The release notes carry the install command, pinned to the tag:

```sh
curl -fsSL https://raw.githubusercontent.com/<repo>/<version>/installer/install.sh | sudo sh
```

The version is in the URL on purpose. The installer fetches `deploy/*.yaml` from
the same ref it was fetched from, so the objects applied are the ones that
version's image was built with. An installer that read them from a branch would
eventually apply a Deployment to an image that had never seen it.

## After the tag

1. **Install from the published command on a throwaway server**, not from a
   clone. It is the only way to find out whether the image is public, whether
   the manifests are reachable at that ref, and whether the CLI download works.
2. **Upgrade an existing install** to it, from Settings, and check the apps kept
   answering.
3. Move any row in `docs/checklist.md` that the run proved, and say which run
   proved it.

## Where this project publishes

Two lines: `PROJECT_REPO` in `installer/install.sh` and `PROJECT_REPO` in the
`Makefile`. Everything else derives from them, and `scripts/check-home.sh` fails
the build if a name this project does not own appears anywhere else. Moving to an
organisation of its own is those two lines and a re-run of `make check`.
