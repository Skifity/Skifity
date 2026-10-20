# The crosscheck

Everything in `test/smoke` checks this product against fakes, golden files and an
in-process SSH server, because the machine it was built on could not start a
cluster. Those checks are fast and run on every change; they are not the same
thing as a cluster.

`verify.sh` is the other half. It installs Skifity on a real server and works
through the checklist a self-hosted platform is actually judged on —
`docs/checklist.md` is that list, and the column that says which of the eighteen
items have a test that runs and which are still only written. It runs on every
push to `main` (below), and `docs/progress.md` says what each run found.

## The phases

| Phase | What it settles | Checklist |
|---|---|---|
| **1 — the basic path** | An app is built *from Git*, its log can be read while it is building, it gets an address that answers, a variable change restarts it without rebuilding, one call rolls it back, a rolling restart under continuous traffic drops nothing, switching autoscaling on keeps the instances there are, and a folder with no repository is built and deployed. | 1, 2, 3, 4, 8, 9 |
| **2 — the data** | A volume keeps what was written to it across a restart, a managed database comes up on its own, a backup restores, and the export carries every app's Kubernetes objects and none of its secrets. | 5, 6, 7, 16 |
| **3 — security and isolation** | A token for one team answers 404 on another, a member cannot run the panel, a pod in one namespace cannot reach another's app, an app's pod starts behind its fence and reaches neither the Kubernetes API, the kubelet nor the registry, a build runs its own BuildKit and cannot write to the registry without its token, a rate limit answers 429 at the ingress, and asking for sixty-four cores is refused by a real quota. | 11, 12, 13 |
| **4 — what survives** | The panel is scaled to zero and the app keeps answering every request. A node is drained and the work moves, with the disruption budget letting it move one instance at a time. | 14, 9 |
| **5 — running it** | Logs and per-instance usage can be read, the panel serves its own metrics, an app serves every request while the panel is replaced under it, a deployment that cannot pull its image actually sends a notification, and an app asleep at zero instances wakes for a visitor and for its own deploy. | 10, 15, 17 |

Phase 6 is a person and should not become a script: give somebody
`docs/quick-start.md` and nothing else, and write down where they get stuck.

## Running it

**It installs k3s and changes the machine permanently.** Use a server you are
willing to rebuild — a fresh VPS, not something you care about.

### In CI

`.github/workflows/cluster.yml` runs it on every push to `main`, and on demand
from the Actions tab with a choice of phases. A GitHub-hosted runner is a fresh
Ubuntu server with systemd that is thrown away afterwards, which is what this
script asks for: the workflow builds the panel's image from the commit, hands
it to k3s before k3s exists, stops the runner's Docker so the machine looks like
a server nobody has touched, and runs the real installer through `verify.sh`.
Let's Encrypt's staging server is used, because nothing on the internet can
reach a runner and the production server is not the place to fail a challenge
on every push.

Whatever happens, `collect.sh` then gathers what the cluster says about itself
— nodes, pods, events, network policies, certificates, the k3s journal, the
logs of every part of the platform and of every build — and the workflow
uploads it with the installer's and this script's logs as the `cluster-report`
artifact. Secrets are left out entirely.

The workflow has a second job, on a runner of its own, and a second workflow
runs beside it.

**Upgrade from the last release** runs `upgrade.sh`. A fresh install is not
what most servers see: an earlier release installed them, and they get a new
one by running the newer installer over the top. So it installs the newest
published release with the installer published with it, sets the panel up,
deploys an app with a variable and asks for a PostgreSQL database, then runs
this commit's installer over it while asking the app for an answer twice a
second. The new panel has to work on the old database (the account signs in,
the old API token works, the app and its variable are there, the database was
copied first, k3s has what a fresh install gives it, and the new panel deploys
the old panel's app); then the four commands the installer printed for going
back are run as printed, and the old version has to come back on the copy. The
app's answers are counted the whole way through. Its report is `upgrade-report`.

**Two servers**, `.github/workflows/servers.yml`, runs `verify.sh` across
two. A runner is one machine that nothing outside can reach, but it can run a
virtual machine: `second-server.sh` boots Ubuntu's cloud image as a KVM guest
on a bridge, with
root reachable over SSH by a key made for it. Given `VERIFY_SECOND_SERVER` and
`VERIFY_SECOND_SERVER_KEY`, `verify.sh` has the panel add that server through
its API after first-run setup, the way a person does, and every phase then runs
across both: an instance of the app built on the first server has to run on
the second, which pulls it through its own registry port; the second server's
ingress has to answer for it; phase 4 drains a server instead of skipping; and
at the end the panel takes the second server out with the app being asked
throughout. Its report is `servers-report`, with the guest's console and its
k3s agent's journal.

The same collector works on a server of your own:

```sh
sudo bash test/cluster/collect.sh /tmp/skifity-report
```

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
| `SKIFITY_IMAGE` | **Required.** The panel image: a release's, such as `ghcr.io/skifity/skifity:v0.1.0`, or one built from this checkout and pushed somewhere the server can pull from. |
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
That is deliberate: a field renamed in the API shows up in the fast checks
first, rather than as a cluster run that fails twenty minutes in.

The rest runs on every push to `main`, on one server, and passes. Three checks
skip there, and each says why rather than pretending:

* **The HTTPS certificate.** The runner's free address is on sslip.io, served
  over plain HTTP on purpose (ADR-0015). Set `SKIFITY_DOMAIN` to a domain you
  own, pointed at the server.
* **A backup that restores.** It needs a bucket; set the `VERIFY_S3_*`
  variables.
* **A server going away.** It needs a second server, and a runner is one.

None of those three has run anywhere yet, and neither has anything else that
needs more than one server.

## What to send back

The whole of `/var/log/skifity-verify.log`, or the `cluster-report` artifact. A
failure in it is worth more than a pass: every check names what it expected, so
the report can be acted on rather than interpreted.

## Why it is not in `make check`

It needs a machine to destroy, several minutes, and the internet, and `make
check` runs on a contributor's own computer. The fast checks in CI must not do
any of those either, so the cluster has a workflow of its own (above) that runs
beside them on a machine that is destroyed afterwards. A release is not tagged
until it has passed.
