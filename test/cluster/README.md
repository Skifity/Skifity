# The crosscheck

Everything in `test/smoke` checks this product against fakes, golden files and an
in-process SSH server, because the machine it was built on could not start a
cluster. `docs/progress.md` says so plainly, and it is the largest single gap in
this repository.

`verify.sh` is the other half. It installs Skifity on a real server and works
through the checklist a self-hosted platform is actually judged on —
`docs/checklist.md` is that list, and the column that says which of the eighteen
items have a test that runs and which are still only written.

## The phases

| Phase | What it settles | Checklist |
|---|---|---|
| **1 — the basic path** | An app is built *from Git*, its log can be read while it is building, it gets an address that answers, a variable change restarts it without rebuilding, one call rolls it back, and a rolling restart under continuous traffic drops nothing. | 1, 2, 3, 4, 8, 9 |
| **2 — the data** | A volume keeps what was written to it across a restart, a managed database comes up on its own, a backup restores, and the export carries every app's Kubernetes objects and none of its secrets. | 5, 6, 7, 16 |
| **3 — security and isolation** | A token for one team answers 404 on another, a member cannot run the panel, a pod in one namespace cannot reach another's app, and asking for sixty-four cores is refused by a real quota. | 11, 12, 13 |
| **4 — what survives** | The panel is scaled to zero and the app keeps answering every request. A node is drained and the work moves, with the disruption budget letting it move one instance at a time. | 14, 9 |
| **5 — running it** | Logs and per-instance usage can be read, the panel serves its own metrics, an app serves every request while the panel is replaced under it, and a deployment that cannot pull its image actually sends a notification. | 10, 15, 17 |

Phase 6 is a person and should not become a script: give somebody
`docs/quick-start.md` and nothing else, and write down where they get stuck.

## Running it

**It installs k3s and changes the machine permanently.** Use a server you are
willing to rebuild — a fresh VPS, not something you care about.

From a laptop, against a server you are willing to rebuild:

```sh
make verify-remote HOST=root@203.0.113.10
```

That builds the image for the server's architecture, streams it into the
directory k3s imports from before k3s exists — so nothing is pushed to a
registry and no credentials are needed — copies this checkout across, runs the
phases, and brings the report back to `verify-report.log`. `DRY_RUN=1` prints
every command instead of running it.

On the server itself:

```sh
git clone <this repository> && cd Skifity
make image                                   # or pull one from somewhere
sudo SKIFITY_IMAGE=skifity:local bash test/cluster/verify.sh
```

It asks for a typed `yes` before touching anything. `SKIFITY_ASSUME_YES=1` skips
the question, which is for a machine that is already disposable.

`SKIFITY_PHASES="2 3"` runs only those phases, which is what you want when one
of them failed and you are fixing it.

| Variable | What it is |
|---|---|
| `SKIFITY_IMAGE` | **Required.** The panel image. No release is published yet, so build one or push one somewhere the server can pull from. |
| `SKIFITY_DOMAIN` | The panel's domain. Defaults to `<public-ip>.nip.io`, so there is nothing to set up in DNS. A domain you own is what makes the HTTPS check run rather than skip. |
| `SKIFITY_PUBLIC_IP` | Set it when the machine cannot work its own out. |
| `SKIFITY_PHASES` | Which phases to run. Default `1 2 3 4 5`. |
| `HOST` | For `make verify-remote`: the server to run on, `root@1.2.3.4`. |
| `SKIFITY_ASSUME_YES` | `1` to skip the confirmation. |
| `VERIFY_GIT_REPO` / `VERIFY_GIT_BRANCH` / `VERIFY_GIT_ROOT` | The application phase 1 builds. Defaults to this repository's own `test/cluster/sample-app`: one Go file and a Dockerfile with nothing to download, so a build failure is the builder's and not the network's. |
| `VERIFY_S3_ENDPOINT` / `VERIFY_S3_BUCKET` / `VERIFY_S3_ACCESS_KEY` / `VERIFY_S3_SECRET_KEY` | Where backups go. Without them phase 2 skips the backup check and says so — a backup with nowhere to put it is not a backup. |

## What is already proven, and what is not

The parts of this script that only talk to the panel — first-run setup, the CSRF
header, creating an API token, inviting somebody, what a member is refused, the
export — are also checked by `make smoke` against the real binary on every push.
That is deliberate: a field renamed in the API would otherwise leave this script
quietly checking nothing, on the one machine nobody can run from CI.

Everything else here has never run.

## What to send back

The whole of `/var/log/skifity-verify.log`. A failure in it is worth more than a
pass: it is the first thing this product has ever been told by a real cluster,
and every check names what it expected so the report can be acted on rather than
interpreted.

## Why it is not in `make check`

It needs a machine to destroy, several minutes, and the internet. CI runs on
every push and must not do any of those. This is a thing a person runs before a
release, and the release is not tagged until it has passed.
