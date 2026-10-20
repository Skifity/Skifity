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
| **1 — the basic path** | An app is built *from Git*, its log can be read while it is building, it gets an address that answers, a domain of its own gets a certificate from an ACME server and sends visitors to HTTPS while the free address stays plain HTTP, a variable change restarts it without rebuilding, a variable read from a secret manager runs it with the value the manager holds and a changed value reaches it without a build, one call rolls it back, a rolling restart under continuous traffic drops nothing, switching autoscaling on keeps the instances there are, a folder with no repository is built and deployed, and a repository on a Git host at a private address is refused until an administrator lists the host and its authority, and then built; a push to that host is deployed and a pull request on it previewed, both through the webhook the panel registered there. | 1, 2, 3, 4, 8, 9 |
| **2 — the data** | A volume keeps what was written to it across a restart; a managed PostgreSQL comes up on its own, and so does each of the eight other engines; a backup of every engine the panel backs up is restored over the live database and holds what it held when the backup was taken, and so does a volume's; a backup sealed with a passphrase is ciphertext in the bucket and still restores; the panel reads a backup through and copies its own database to the bucket; and the export carries every app's Kubernetes objects and none of its secrets. | 5, 6, 7, 16 |
| **3 — security and isolation** | A token for one team answers 404 on another, a member cannot run the panel, a person signed in through an identity provider gets an account and the team their group names, a pod in one namespace cannot reach another's app, an app's pod starts behind its fence and reaches neither the Kubernetes API, the kubelet nor the registry, a build runs its own BuildKit and cannot write to the registry without its token, a rate limit answers 429 at the ingress, and asking for sixty-four cores is refused by a real quota. | 11, 12, 13 |
| **4 — what survives** | The panel is scaled to zero and the app keeps answering every request. With a second server, a node is drained and the work moves, with the disruption budget letting it move one instance at a time and an app with an instance on each server answering throughout, while an app whose volume is on that server's own disk answers again once the server is back; and at the end of the run the panel refuses to take out a server holding data, naming it, and takes it out when told the data may go, with an app that has nothing on its disk answering throughout — or a restart is seen to leave one of the app's instances on each server, the server is switched off without warning, and it is removed as one that is gone. | 14, 9 |
| **5 — running it** | Logs and per-instance usage can be read, the panel serves its own metrics, an app serves every request while the panel is replaced under it, a deployment that cannot pull its image actually sends a notification — to a webhook, and, given servers for them, by email, to ntfy, to Gotify and to Mattermost, each read back from the server it went to — a forgotten password is reset through the link the panel emails, an object changed with kubectl is reported, with who changed it, and put back, a maintenance notice is answered in front of an app, a worker, a command run once and one on a schedule run from an app's image, an app's own log lines reach a drain of the team's, a preview of a branch copies the whole environment into a namespace of its own, seeds it once and is gone when closed, a Compose file is deployed as a stack whose services reach each other by name, a template is installed from the catalogue, a version is promoted to another environment, a server reports its disk, a database is reached from the machine through the panel and through Traefik, and an app asleep at zero instances wakes for a visitor and for its own deploy. | 10, 15, 17 |

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
Let's Encrypt's staging server is used for the panel's own address, because
nothing on the internet can reach a runner and the production server is not
the place to fail a challenge on every push.

Stand-ins run on the runner beside it, on its own address, which is a private
one: a bucket (moto, which answers presigned URLs as S3 does) for the backups;
an ACME server (Pebble, Let's Encrypt's own test server, its address signed by
an authority made for the run) for the certificate; a Gitea over HTTPS with an
authority of its own, holding the sample app; Mailpit, ntfy, Gotify and
Mattermost, the last on the runner's own PostgreSQL, for notifications; and
OpenBao, which speaks Vault's API, for a variable read from a secret manager.
The crosscheck gives the bucket and the ACME server to the panel under
Settings, the way a bucket and an ACME server of one's own are given, and
points the app's domain at the runner. What
they stand in for is checked against them exactly as it would be against the
real thing: the backup jobs reach a bucket on a private address through their
own network policy, and the certificate is validated through the same ingress
visitors use.

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

The same workflow has a second job, **A server that fails**, which runs phases
1 and 4 and then, given `VERIFY_SECOND_SERVER_FAILS`, kills the virtual
machine's process: to the cluster, a power cut. With the app on both servers it
checks how long the cluster takes to know, that the app answers every request
from the instance it has left once it does, that the panel marks the server as
not responding, that the panel can remove a server that is gone, and that the
app is back to two instances on the server that is left. Its report is
`server-fails-report`.

A third job, **Two servers apart**, runs phases 1 and 4 with the two servers
apart: `second-server.sh`, given `SECOND_SERVER_LINK`, has netem hold up every
packet between the guest and the runner's own addresses by 40 ms each way and
drop 0.3 % of them, once the guest is up, and leaves the guest's own way to the
internet alone. The panel adds the server across that link, an app built on one
server is pulled and run on the other, a server is drained, and the panel takes
it out, with the app answering throughout. Its report is
`servers-apart-report`, with what netem counted in `link.txt`.

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
| `VERIFY_S3_ENDPOINT` / `VERIFY_S3_BUCKET` / `VERIFY_S3_ACCESS_KEY` / `VERIFY_S3_SECRET_KEY` | Where backups go. Without them phase 2 skips the backup checks and says so — a backup with nowhere to put it is not a backup. |
| `VERIFY_ACME_DIRECTORY` / `VERIFY_ACME_CA` / `VERIFY_ACME_ADDRESS` / `VERIFY_ACME_ROOT` | An ACME server for the app's certificate: its directory, the authority that signs its address, this machine's address on the network it validates from, and where it hands out the root it issues from. Without them the certificate check skips. |
| `VERIFY_SECOND_SERVER` / `VERIFY_SECOND_SERVER_KEY` | A second server for the panel to add over SSH as root, and the key to it. Without them everything that needs two servers skips. |
| `VERIFY_SECOND_SERVER_FAILS` | A file holding the id of the process that is the second server. With it, the end of the run switches that server off instead of taking it out. |
| `VERIFY_SMTP_SERVER` / `VERIFY_MAILPIT` | A mail server that takes mail without signing in, as `host:port`, and the address of the [Mailpit](https://mailpit.axllent.org) behind it, where what arrived is read. With them the panel's Email settings point at it, an email channel and a password reset are sent through it. Without them both skip. |
| `VERIFY_NTFY_SERVER` / `VERIFY_NTFY_TOKEN` | An ntfy server and a token for it. Without them the ntfy check skips. |
| `VERIFY_GOTIFY_SERVER` / `VERIFY_GOTIFY_TOKEN` / `VERIFY_GOTIFY_CLIENT_TOKEN` | A Gotify server, an application's token to push with and a client's token to read back with. Without them the Gotify check skips. |
| `VERIFY_GIT_HOST_REPO` / `VERIFY_GIT_HOST_CA` | An `https://` repository holding the sample app on a Git host at a private address, and the authority that signed the host's certificate. Without them the private Git host check skips. |
| `VERIFY_GIT_HOST_TOKEN` | A token of that repository's owner on a Gitea that may deliver webhooks to a loopback address. With it the panel connects to the host, registers its webhook, and a push is deployed and a pull request previewed through it. Without it those checks skip. |
| `VERIFY_MATTERMOST_HOOK` / `VERIFY_MATTERMOST_API` / `VERIFY_MATTERMOST_TOKEN` / `VERIFY_MATTERMOST_CHANNEL` | A Mattermost incoming webhook, the server's `/api/v4`, a token that reads the channel the webhook posts to, and that channel's id. Without them the Mattermost check skips. |
| `VERIFY_DEX` | A [Dex](https://dexidp.io) binary. With it phase 3 starts Dex on this machine's address with a connector that vouches for one person in one group, and signs that person in through it. Without it the single sign-on check skips. |
| `VERIFY_VAULT_ADDRESS` / `VERIFY_VAULT_TOKEN` | A Vault or OpenBao with a KV version 2 mount at `secret/`, and a token that may write to it. Without them the check of a variable read from a secret manager skips. |

## What is already proven, and what is not

The parts of this script that only talk to the panel — first-run setup, the CSRF
header, creating an API token, inviting somebody, what a member is refused, the
export — are also checked by `make smoke` against the real binary on every push.
That is deliberate: a field renamed in the API shows up in the fast checks
first, rather than as a cluster run that fails twenty minutes in.

The rest runs on every push to `main`: on one server with a bucket and an
ACME server beside it, on two, and as an upgrade from the last release. What
none of that can show, and so has not been seen:

* **A certificate from Let's Encrypt itself, for a domain on the internet.**
  The ACME conversation is the same with Pebble, and the challenge is answered
  through the same ingress; what differs is a real DNS record and a validator
  outside the machine.
* **A bucket that is not a stand-in**, and restoring into a *different*
  cluster, which is the drill worth running once by hand.
* **Two servers far apart for real**: the third job's link delays and drops
  packets as a long one does, but both servers are still on one runner, and a
  link that goes down for minutes and comes back has not been tried.
* **Services that only exist on the internet**: GitHub's and GitLab's own
  webhooks (Gitea's are run), Bitbucket, servers made at Hetzner, and
  delivery to Telegram, Discord, Slack, Microsoft Teams and Pushover.
* **A person**, which is phase 6.

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
