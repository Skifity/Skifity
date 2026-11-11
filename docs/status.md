# Where Skifity stands

The short version, for somebody arriving now. [The checklist](checklist.md)
goes item by item through what a self-hosted platform is judged on, and
[`docs/progress.md`](progress.md) is the full log — some 7,000 lines, an entry
for each piece of work saying what was run and what was not. This page
summarises both. Where it disagrees with them, they are right and this page is
behind.

Written in November 2026, when the newest release is v0.1.1.

## In one paragraph

Skifity is released, installs with one command, and every push to `main` is
installed on a fresh server and checked against the k3s it made. Sixteen of the
eighteen things in [the checklist](checklist.md) have been seen working end to
end, all but one of them on a real cluster, and the other two in part. What has
not been seen is mostly what a machine borrowed for an hour cannot show:
servers of your own in different places, a real bucket, Let's Encrypt itself,
and a person following the documentation cold. It is pre-1.0 software and
nobody is on call for it: try it on a spare server before you move production
onto it.

## Releases

| Version | Published | |
|---|---|---|
| v0.1.0 | 2026-10-06 | The first. Cut before the first run on a real cluster, by the maintainer's decision. |
| v0.1.1 | 2026-10-21 | The first after those runs, with everything they found fixed. Tagged by `.github/workflows/tag.yml` once CI, Cluster and Two servers had passed on it. |

Each release has static binaries for Linux, macOS and Windows on amd64 and
arm64, a multi-architecture image at `ghcr.io/skifity/skifity`, and SLSA
provenance. [Releasing](releasing.md) is the procedure.

## What is checked, and when

| Check | Runs on | What it covers |
|---|---|---|
| CI | Every push to every branch, and pull requests | The linters, the Go tests with and without the race detector, the vulnerability scans and the translations; then the Playwright interface test and the smoke tests, which run the real binary. No cluster. |
| Cluster | Every push to `main` that changes more than documentation | A fresh GitHub-hosted Ubuntu 24.04 server, installed with the real installer, taken through `test/cluster/verify.sh` with a stand-in bucket, Let's Encrypt's test server, a Gitea, Dex, Mailpit, ntfy, Gotify, Mattermost and OpenBao beside it; and an upgrade from the last release with an app serving. |
| Two servers | The same pushes | The same across two servers, the second a virtual machine the panel adds as a worker over SSH; again across a link that delays each packet 40 ms each way and drops 0.3% of them; and with the second switched off without warning. |
| CodeQL | Every push, and weekly | Static analysis of the Go and the frontend. |

**A cluster run happens on `main` only.** Work on a branch has passed CI and
nothing more until it lands there, and an entry in `docs/progress.md` that says
"Not executed" means what it says.

## Seen working

On a real cluster, or against the real binary where no cluster is involved.
From a Git push to a running app, with live build logs; rollback; rolling
deploys under traffic with nothing failing; a domain with a certificate from an
ACME server; variables and secrets; volumes; all nine database engines, and
backup and restore of each engine that is backed up; logs and metrics;
two-factor, single sign-on through Dex, roles and the fences between tenants;
quotas; apps answering while the panel is down or being replaced; install,
upgrade and export. Across two servers: draining one, removing one, and losing
one without warning. [The checklist](checklist.md) says which run proves each.

## Written, and never run

The code and its unit tests exist; no run has exercised it:

* **Notifications** to Telegram, Discord, Slack, Microsoft Teams and Pushover.
  Webhook, email, ntfy, Gotify and Mattermost are seen arriving on every run.
* **Three control plane servers**: joining or promoting one, and the cluster
  surviving the loss of one. The Two servers workflow adds its second server
  as a worker. See [Control plane servers](adding-servers.md#control-plane-servers).
* **Cross-node storage** (Longhorn), which no run has installed.
* **Outside services**: servers ordered at Hetzner Cloud or DigitalOcean, DNS
  records at a real DNS provider, Bitbucket, GitHub's and GitLab's own
  webhooks, Infisical, Doppler and AWS Secrets Manager (OpenBao, which speaks
  Vault's API, is seen), and log drains to Loki, Datadog and the other named
  services (the generic kind is seen, sending to a plain listener).
* **Hardware**: a passkey on a real authenticator, and a GPU.

## Owed by somebody running it

What no machine borrowed for an hour can show:

* servers in different places, rather than two on one runner with a link that
  imitates a long one;
* a bucket that is not a stand-in, and Let's Encrypt itself for a domain on the
  internet;
* restoring into a different cluster, the drill that matters on the day it
  matters;
* k3s on a 2 GB server: it has only been measured on a 16 GB runner
  ([What it costs](performance.md));
* phase 6 of the checklist: a person who has not seen Skifity, given
  [the quick start](quick-start.md) and nothing else;
* a review of the security model by somebody who did not write it. CodeQL and
  Dependabot run, and nobody has attacked it on purpose.

## Known to be missing

* **The plugin store is not published**: a plugin is installed by its address
  or its manifest.
* **An existing Kubernetes cluster cannot be used.** Skifity installs and runs
  its own k3s.
* **A fresh install has one control plane server**, a single point of failure
  for the cluster until there are three.
* **The panel itself is not highly available**: one pod, with its database and
  master key on one server's disk. Three control plane servers keep the
  cluster, not the panel.
* **An app with a volume waits for its server.** The default storage keeps a
  volume on the server that made it.

[The roadmap](roadmap.md) has what comes next and why.
