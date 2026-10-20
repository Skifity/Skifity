# The checklist, crosschecked

Eighteen things a self-hosted platform is judged on, and where Skifity actually
stands on each. This page exists because "implemented" and "works" are different
words, and this repository has been caught confusing them before — see
`docs/progress.md`, which is a list of sentences nobody checked.

Three statuses, and only three:

| | Means |
|---|---|
| **Works** | There is a test that runs, on every push, and it passes: `make check`, `make smoke`, the Playwright suite, or the Cluster workflow, which installs Skifity on a fresh single-server k3s and runs `test/cluster/verify.sh` against it. |
| **Written** | The code and its unit tests exist, and no cluster run has exercised it yet. |
| **Missing** | Not there. |

Nothing is marked Works because it looks right.

## The eighteen

| # | Item | Status | Checked by |
|---|---|---|---|
| 1 | Git → build → deploy | **Works** | Cluster workflow, phase 1 |
| 2 | Live build logs | **Works** | Cluster workflow, phase 1 |
| 3 | Domain and HTTPS, automatically | Works (address) / Written (certificate) | Cluster workflow, phase 1; a certificate needs a domain of one's own |
| 4 | Variables and secrets, safely | **Works** | Go tests, `make smoke`; cluster workflow, phase 1 |
| 5 | Persistent storage | **Works** | Cluster workflow, phase 2 |
| 6 | One-click databases | Works (PostgreSQL) / Written (the other eight) | Cluster workflow, phase 2 |
| 7 | Backup and restore, proven | Written | Phase 2 |
| 8 | One-click rollback | **Works** | Cluster workflow, phase 1 |
| 9 | Zero-downtime deploys | **Works** | Cluster workflow, phases 1 and 5 |
| 10 | Application logs and metrics | **Works** | Cluster workflow, phase 5 |
| 11 | Safe sign-in (2FA/SSO) and roles | **Works** | Go tests, `make smoke` |
| 12 | Isolation between projects | **Works** | Go tests; cluster workflow, phase 3 |
| 13 | Resource limits and quotas | **Works** | Cluster workflow, phase 3 |
| 14 | Apps keep running when the panel is down | **Works** | Cluster workflow, phase 4 |
| 15 | Easy install and upgrade | Works (install) / Written (upgrade between releases) | `make smoke`; the cluster workflow installs with the real installer and replaces the panel under an app serving, phase 5 |
| 16 | Export, no lock-in | **Works** | Go tests, `make smoke` |
| 17 | Notification on failure | Works (webhook) / Written (the other nine services) | Cluster workflow, phase 5 |
| 18 | Documentation | Works (it is served and its links resolve) | Go tests; Phase 6 is a person |

Twelve are Works end to end, five in part, one Written. What is left Written
is what a single throwaway server cannot show, and one thing it could: a backup
taken and restored from a real bucket, a certificate for a domain of one's own,
the eight database engines that are not PostgreSQL, delivery to the nine
notification services that are not a webhook, everything that needs a second
server, and an upgrade from one release to the next.

## Where each one is

**1. Git → build → deploy.** `internal/builder` detects the language and builds
with Railpack or a Dockerfile, in a Job in the cluster, pushing to an
in-cluster registry. `internal/gitsrc` connects GitHub, GitLab and Gitea, and a
webhook deploys on push. The build fingerprint (ADR-0007) means a variable
change never rebuilds.

**2. Live build logs.** Lines are stored as they arrive and streamed over SSE,
with the last twenty builds kept per app. The Cluster workflow reads a real
build's log through the panel while the build is still running.

**3. Domain and HTTPS.** cert-manager is installed the first time a domain is
added. Every app also gets a free address with no DNS to configure: `sslip.io`
over plain HTTP, on purpose — every install in the world shares that domain's
Let's Encrypt rate limit (ADR-0015). A domain you own gets a certificate. The
Cluster workflow checks the free address answers through the ingress; the
certificate needs a domain of one's own pointed at the server, which a runner
does not have, so it has not been issued on a real cluster yet.

**4. Variables and secrets.** Every secret is sealed with a key of its own,
wrapped by a master key kept outside the database, and bound to where it is
stored so a copied row will not open (`internal/crypto`). Once stored, a value
is never shown again — not by the panel, the CLI, the API, the export or an
assistant. `internal/logging` redacts by key and by value pattern, and
`make smoke` greps the panel's own log for every secret it sent. The last step
is proven too: the Cluster workflow changes a variable and reads the new value
back from the app, with the image unchanged.

**5. Persistent storage.** A volume becomes a PersistentVolumeClaim, and an app
with one switches to the Recreate strategy, because two instances writing one
ReadWriteOnce disk corrupts it. The scaling checker refuses to stay quiet about
a volume and several instances.

**6. One-click databases.** PostgreSQL through CloudNativePG, plus MySQL,
MariaDB, MongoDB, Redis, Valkey, Dragonfly, ClickHouse and Memcached as
single-instance StatefulSets. The operator is installed on first use. Credentials are generated,
sealed, and injected into a linked app as a connection string.

**7. Backup and restore.** Scheduled or manual, to any S3-compatible bucket,
for databases and for volumes. **"Proven" is exactly the word this cannot
claim**: no backup has ever been taken and no backup has ever been restored.
Phase 2 does both, given a bucket, and the Cluster workflow has none to give it,
so it skips and says so. The drill worth running once by hand is the other half:
restore into a *different* cluster, which is the only version of this that
matters on the day it matters.

**8. One-click rollback.** A rollback is a new deployment carrying the old
image, so history stays a straight line, and it restores the settings that
version ran with rather than only its image. It refuses when the image has been
garbage-collected, instead of leaving a pod in ImagePullBackOff.

**9. Zero-downtime deploys.** `maxUnavailable: 0` keeps the capacity, and a
five-second `preStop` pause stops a proxy sending to a pod that has already
begun shutting down — the half that was missing until the scaling audit. Phase 1
holds the app under continuous traffic through a rolling restart and counts what
fails, and on every push the count is zero.

**10. Application logs and metrics.** Logs stream from the pods through the
panel. Each instance reports what it is *using*, not what it reserved. The panel
also reports on itself in Prometheus format, behind the same authentication as
everything else, with route patterns as labels so the endpoint cannot become a
memory leak.

**11. Safe sign-in and roles.** Argon2id passwords with a lockout, TOTP,
recovery keys, and OpenID Connect with PKCE, a verified ID token, a per-sign-in
nonce and a single-use state. Four roles, ordered. Authorization lives in one
place and a test walks every route in the router: 126 of them must refuse an
anonymous request (13 are open on purpose), 91 team-scoped ones must answer
404 for another team's id, a viewer must be refused every one of the 57 that
change something and answered by every read but six named ones, and a member
limited to one project must get 404 on all 69 routes for another project in the
same team and 403 on the 25 team-wide ones not named as open to them. An API token is bound to one team and its scopes are enforced.

**12. Isolation between projects.** An environment is a namespace with a
default-deny NetworkPolicy, a ResourceQuota, a LimitRange and the `restricted`
Pod Security profile. The panel side is proven by the route walk above, and
the network side by Phase 3 on a real CNI: a pod in another namespace cannot
reach an environment's app, and an app's own pod reaches neither the Kubernetes
API, the kubelet nor the registry, from the first moment it runs (ADR-0027).

**13. Resource limits and quotas.** Generous by default — they exist to contain
a mistake, not to ration — and visible: the panel shows how much of an
environment's ceiling is in use, because the first sign of hitting one used to
be a deployment failing with a message about a resource nobody had heard of.
Phase 3 asks for sixty-four cores and a real quota refuses them.

**14. Apps keep running when the panel is down.** True by construction: apps are
served by Kubernetes and the panel is not in the request path. Being true by
construction is not the same as having been seen, and it has been: Phase 4
scales the panel to zero and the app answers every request.

**15. Install and upgrade.** One POSIX shell script, no bashisms, with a
preflight that refuses before it changes anything and an uninstaller that can be
dry-run. `make smoke` checks its logic without installing anything, and the
Cluster workflow installs every push with it on a fresh server. The panel
upgrades itself by changing the image on its own Deployment; the apps do not
notice, and Phase 5 checks that by replacing the panel's pod under an app
serving traffic. An upgrade from one release to the next, by running the
installer again, is the part that has not run.

**16. Export, no lock-in.** `skifity export` writes the whole team as JSON plus
each app's Kubernetes objects, ready for `kubectl apply` on any cluster. The
panel has the JSON as a download. Secret values are not in it, which is the
promise being kept rather than a gap — they are already in your own cluster as
ordinary Kubernetes Secrets, and the export's README has the one line that reads
them out.

**17. Notification on failure.** Telegram, Discord, Slack, Mattermost,
Microsoft Teams, ntfy, Pushover, Gotify, a webhook or email, on seven
events: a deploy succeeding or failing, an app going unhealthy, a server added
or lost, a backup failing, a certificate failing. A channel can be changed
after it is added, and limited to some projects; what each event belongs to is
in [Configuration](configuration.md#limiting-a-channel-to-some-projects). The
payloads are checked against a local listener in Go tests. Phase 5 stands up a
listener on a real cluster and breaks a deployment on purpose, and the webhook
arrives; delivery to the nine real services that are not a webhook has never
been seen end to end.

**18. Documentation.** Thirteen pages, served from inside the binary so they work on
a machine with no other browser and no outbound network — which is exactly when
they are needed. Every `WithDocs` link in the error catalogue must resolve to a
page the panel serves, at an anchor that exists, and a test fails the build if
one does not. Thirty-eight screenshots, captured by a test against the real
binary. The check that actually matters is Phase 6, and it needs a person.

## Can somebody install this today?

With one command, since v0.1.0, and since v0.1.1 on code that has met one: every
push to `main` is installed on a fresh server and crosschecked (see the table).

**Where this project publishes is settled: the repository it is in.**
`Skifity/Skifity`, so `ghcr.io/skifity/skifity` for the image and
that repository's raw URL for the installer and the manifests. It is one line in
`installer/install.sh` and one in the `Makefile`; everything else derives from
them, and `scripts/check-home.sh` fails the build if a name this project does
not own reappears anywhere.

**The manifests are pinned to the release**, not to a branch:
`raw.githubusercontent.com/<repo>/<version>/deploy`. An install never pulls one
release's image and another's Deployment.

**The tag exists, and was published on 2026-10-06.** `RELEASED_VERSION` in the
installer is `v0.1.0`, the release workflow refuses a tag whose installer
disagrees, and the one-line install in the README is the newest release's own
installer, `releases/latest/download/install.sh`, which installs the release it
is attached to. The release workflow's first real run passed, provenance
included, and what it published was checked from outside afterwards (see
`docs/progress.md`). Before the tag, a local GoReleaser dry run found
three things the release would have got wrong on the day: an image build that
copied a file from the wrong path, an image tag without its `v` that the
installer would have pulled in vain, and Windows binaries named `.exe.exe`.

v0.1.0 was cut before the cluster run that `docs/releasing.md` asks for, by the
maintainer's decision. The run has happened since, on every push: the `Cluster`
workflow installs the commit on a fresh GitHub-hosted Ubuntu server with the
real installer and works through `test/cluster/verify.sh`. Its first runs found
what the fakes could not — an empty storage class, a database kept off the
API, an app with no address on a cloud machine, a new pod's unfenced first
moment, a scale-to-zero add-on fetched by a name no release has — and v0.1.1 is
the tag with all of it fixed.

What is left, in order:

1. **An upgrade from one release to the next**, by running the newer
   installer over an install of the older one with an app serving.
2. **A second server.** Pulls through another server's NodePort, a server
   going away, the gateway addresses the registry lets in: nothing above has
   run on more than one. Phase 4 says how.
3. **A backup restored from a real bucket**, and a certificate for a domain of
   one's own: set the variables `test/cluster/README.md` names.
4. **Phase 6, with a person who has not seen it.** The documentation is served
   and its links resolve, which is not the same as it being followed.
   `docs/walkthrough.md` is the sheet they fill in.

Everything else on the roadmap is smaller than any of these.

## The phases

`test/cluster/verify.sh` runs phases 1 to 5 on a real server. It installs k3s
and changes the machine, so it is never part of `make check` and asks before it
starts; the `Cluster` workflow runs it on a throwaway GitHub-hosted server on
every push to `main`.

```sh
sudo SKIFITY_IMAGE=... bash test/cluster/verify.sh
sudo SKIFITY_IMAGE=... SKIFITY_PHASES=2 bash test/cluster/verify.sh   # one phase
```

| Phase | Covers | Needs |
|---|---|---|
| 1 — the basic path | 1, 2, 3, 4, 8, 9 | A server |
| 2 — the data | 5, 6, 7, 16 | A server; backups need an S3 bucket |
| 3 — security and isolation | 11, 12, 13 | A server |
| 4 — what survives | 14, 9 | A server; the node check needs two |
| 5 — running it | 10, 15, 17 | A server |
| 6 — release readiness | 18 | A person who has not seen it before |

Phase 6 is not a script and should not become one. Give somebody
`docs/quick-start.md` and nothing else, and write down where they get stuck.
Every place they stop is a documentation bug, and the ones they work around
silently are the expensive ones.

## What this page is for

When a check in `verify.sh` passes on a real cluster, the row above moves from
Written to Works and this page says what proved it; `docs/progress.md` has each
run. Until then, every Written row is a claim, and this file is the list of
claims.
