# The checklist, crosschecked

Eighteen things a self-hosted platform is judged on, and where Skifity actually
stands on each. This page exists because "implemented" and "works" are different
words, and this repository has been caught confusing them before — see
`docs/progress.md`, which is a list of sentences nobody checked.

Three statuses, and only three:

| | Means |
|---|---|
| **Works** | There is a test that runs, on every push, and it passes. `make check`, `make smoke` or the Playwright suite. |
| **Written** | The code and its unit tests exist. It has never run against a real cluster, because the machine this was built on cannot start one (ADR-0010). |
| **Missing** | Not there. |

Nothing is marked Works because it looks right.

## The eighteen

| # | Item | Status | Checked by |
|---|---|---|---|
| 1 | Git → build → deploy | Written | Phase 1 |
| 2 | Live build logs | Written | Phase 1 |
| 3 | Domain and HTTPS, automatically | Written | Phase 1 |
| 4 | Variables and secrets, safely | Works (stored) / Written (delivered) | Go tests, `make smoke`; Phase 1 |
| 5 | Persistent storage | Written | Phase 2 |
| 6 | One-click databases | Written | Phase 2 |
| 7 | Backup and restore, proven | Written | Phase 2 |
| 8 | One-click rollback | Written | Phase 1 |
| 9 | Zero-downtime deploys | Written | Phases 1, 4, 5 |
| 10 | Application logs and metrics | Written | Phase 5 |
| 11 | Safe sign-in (2FA/SSO) and roles | **Works** | Go tests, `make smoke` |
| 12 | Isolation between projects | Works (panel) / Written (network) | Go tests; Phase 3 |
| 13 | Resource limits and quotas | Written | Phase 3 |
| 14 | Apps keep running when the panel is down | Written | Phase 4 |
| 15 | Easy install and upgrade | Written | `make smoke` checks the installer's logic without installing; Phase 5 |
| 16 | Export, no lock-in | **Works** | Go tests, `make smoke` |
| 17 | Notification on failure | Written | Phase 5 |
| 18 | Documentation | Works (it is served and its links resolve) | Go tests; Phase 6 is a person |

Two are Works end to end. Most are Written. That ratio is the honest state of
this product, and it does not change until `test/cluster/verify.sh` has run.

## Where each one is

**1. Git → build → deploy.** `internal/builder` detects the language and builds
with Railpack or a Dockerfile, in a Job in the cluster, pushing to an
in-cluster registry. `internal/gitsrc` connects GitHub, GitLab and Gitea, and a
webhook deploys on push. The build fingerprint (ADR-0007) means a variable
change never rebuilds.

**2. Live build logs.** Lines are stored as they arrive and streamed over SSE,
with the last twenty builds kept per app. The panel's own log streaming is
covered; a real build's output has never been watched.

**3. Domain and HTTPS.** cert-manager is installed the first time a domain is
added. Every app also gets a free address with no DNS to configure: `sslip.io`
over plain HTTP, on purpose — every install in the world shares that domain's
Let's Encrypt rate limit (ADR-0015). A domain you own gets a certificate.

**4. Variables and secrets.** Every secret is sealed with a key of its own,
wrapped by a master key kept outside the database, and bound to where it is
stored so a copied row will not open (`internal/crypto`). Once stored, a value
is never shown again — not by the panel, the CLI, the API, the export or an
assistant. `internal/logging` redacts by key and by value pattern, and
`make smoke` greps the panel's own log for every secret it sent. What has never
been proven is the last step: the Secret reaching the container.

**5. Persistent storage.** A volume becomes a PersistentVolumeClaim, and an app
with one switches to the Recreate strategy, because two instances writing one
ReadWriteOnce disk corrupts it. The scaling checker refuses to stay quiet about
a volume and several instances.

**6. One-click databases.** PostgreSQL through CloudNativePG, plus Redis and
MySQL. The operator is installed on first use. Credentials are generated,
sealed, and injected into a linked app as a connection string.

**7. Backup and restore.** Scheduled or manual, to any S3-compatible bucket,
for databases and for volumes. **"Proven" is exactly the word this cannot
claim**: no backup has ever been taken and no backup has ever been restored.
Phase 2 does both. The drill worth running once by hand is the other half:
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
fails.

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
Pod Security profile. The panel side is proven by the route walk above. The
network side has never been enforced by a real CNI, which is what Phase 3 does
by trying to reach one namespace's app from another.

**13. Resource limits and quotas.** Generous by default — they exist to contain
a mistake, not to ration — and visible: the panel shows how much of an
environment's ceiling is in use, because the first sign of hitting one used to
be a deployment failing with a message about a resource nobody had heard of.

**14. Apps keep running when the panel is down.** True by construction: apps are
served by Kubernetes and the panel is not in the request path. Being true by
construction is not the same as having been seen. Phase 4 scales the panel to
zero and keeps asking the app for an answer.

**15. Install and upgrade.** One POSIX shell script, no bashisms, with a
preflight that refuses before it changes anything and an uninstaller that can be
dry-run. `make smoke` checks its logic without installing anything. The panel
upgrades itself by changing the image on its own Deployment; the apps do not
notice, and Phase 5 checks that by holding traffic through it.

**16. Export, no lock-in.** `skifity export` writes the whole team as JSON plus
each app's Kubernetes objects, ready for `kubectl apply` on any cluster. The
panel has the JSON as a download. Secret values are not in it, which is the
promise being kept rather than a gap — they are already in your own cluster as
ordinary Kubernetes Secrets, and the export's README has the one line that reads
them out.

**17. Notification on failure.** Telegram, Discord, a webhook or email, on seven
events: a deploy succeeding or failing, an app going unhealthy, a server added
or lost, a backup failing, a certificate failing. Delivery has never been seen
end to end, which is the only part that counts. Phase 5 stands up a listener and
breaks a deployment on purpose.

**18. Documentation.** Thirteen pages, served from inside the binary so they work on
a machine with no other browser and no outbound network — which is exactly when
they are needed. Every `WithDocs` link in the error catalogue must resolve to a
page the panel serves, at an anchor that exists, and a test fails the build if
one does not. Thirty-eight screenshots, captured by a test against the real
binary. The check that actually matters is Phase 6, and it needs a person.

## Can somebody install this today?

Not with one command, and the reason is now one step rather than three.

**Where this project publishes is settled: the repository it is in.**
`Skifity/Skifity`, so `ghcr.io/skifity/skifity` for the image and
that repository's raw URL for the installer and the manifests. It is one line in
`installer/install.sh` and one in the `Makefile`; everything else derives from
them, and `scripts/check-home.sh` fails the build if a name this project does
not own reappears anywhere. Moving to an organisation of its own later is those
two lines.

**The manifests are pinned to the release**, not to a branch:
`raw.githubusercontent.com/<repo>/<version>/deploy`. That fixes a real defect —
an install that pulled one release's image and another's Deployment — and it
means no default branch has to exist for an install to work, which matters here,
because this repository does not have one.

**What is missing is the tag.** No release has ever been cut, so
`RELEASED_VERSION` in the installer is empty, and the installer says so and
refuses before k3s is installed rather than after. `docs/releasing.md` is the
list of steps, and the release workflow refuses to publish a tag whose installer
disagrees with it.

So the install that works today is still two commands:

```sh
make image
sudo SKIFITY_IMAGE=<the tag it printed> sh installer/install.sh
```

That is a developer's install. The user's install is one `git tag` away — and
that tag should not be cut before the run below, because the first thing a
release does is invite somebody to install code that has never met a cluster.

That is the order of what is left:

1. **`test/cluster/verify.sh`, once, on a real server.** Thirteen of the
   eighteen rows are Written outright and two more are half of one, which means
   the code and its unit tests exist and no cluster has ever seen them. One run
   moves most of them, or tells us which ones were wrong, and nothing else in
   this repository is worth as much. `make verify-remote HOST=root@…` runs it on
   a throwaway server from a laptop.
2. **The tag.** `docs/releasing.md`. Everything it needs is in place; what it
   waits on is the run above, not a decision.
3. **Phase 6, with a person who has not seen it.** The documentation is served
   and its links resolve, which is not the same as it being followed.
   `docs/walkthrough.md` is the sheet they fill in.

Everything else on the roadmap is smaller than any of these.

## The phases

`test/cluster/verify.sh` runs phases 1 to 5 on a real server. It installs k3s
and changes the machine, so it is never in CI and asks before it starts.

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
Written to Works and this page says which run proved it. Until then, every
Written row is a claim, and this file is the list of claims.
