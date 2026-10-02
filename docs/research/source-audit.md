# Research: Coolify, Dokploy and Kubero, read from their source

Written 2026-09-30. The earlier passes in [competitors.md](competitors.md) read
documentation, changelogs, issue trackers and forums. This one cloned the three
products Skifity is most often compared with and read their code, then read
Skifity's against it, looking for three things: what they have that Skifity
lacks, what they get wrong that Skifity must not, and anything in Skifity that
only a comparison would show is wrong.

| Product | Revision read | State |
|---|---|---|
| Coolify | main at 5921fb2 | Active; v4 on Laravel and Docker, v5 in progress |
| Dokploy | main at cec5b65 | Active; Next.js on Docker Swarm, pre-1.0 |
| Kubero | main at 36a5046 (2026-06-24), operator at ea8327a (2025-09-17) | Dormant: only templates land, and the operator has had no commit for a year |

Nothing was run: every finding below comes from reading. A competitor's path is
given as its repository writes it, and a Skifity path as this repository does.

## Defects in Skifity that the comparison found

Each was fixed before anything else was built.

| Found by comparing with | What was wrong | Commit |
|---|---|---|
| Coolify | `mariadb-dump --password=…` and `redis-cli -a …` put database passwords on a process's command line inside the backup pod, where anything that can list processes reads them | `0d1e5da` |
| Coolify | A build variable marked secret was still passed as a build argument, which Docker writes into the image's history | `7123905` |
| Coolify | A backup due while the panel was down — an upgrade at 03:00 — was skipped without a word; the scheduler only looked five minutes back | `e61837d` |
| Coolify | Ten templates kept their data where a restart loses it: GitLab, Minecraft, Navidrome, File Browser, Foundry VTT, Homarr, Audiobookshelf, LibreChat, Strapi, Paymenter | `4f0e5b3` |
| Coolify, Kubero | 32 image references named a major line (`n8n:1`, `mongo:7`) rather than a release, which the pinned-version test did not catch | `c9bc382` |
| Coolify | Minecraft and Mosquitto were marked public behind an HTTP ingress, so neither could ever be reached | `62d2adb` |
| Dokploy | Saving a Slack, Mattermost, ntfy or Pushover channel, or one a plugin provides, was refused by the database: the table's check still listed only the first four kinds | `faeecc1` |
| Writing the MCP tools | A backup taken by hand counted against the schedule's retention, so a few in a row deleted the scheduled copy from before the problem | `e215a65` |
| Kubero | k3s's Traefik refuses ExternalName backends unless told otherwise, and an app that scales to zero is reached through one: every such app would have answered 404 | `784fa9f` |
| Reading 390 images' configuration | Homebox's 0.15 image is gone from ghcr.io, MinIO no longer publishes images at all, and the MinIO template never said how to start its server | `66f305f` |
| Reading 390 images' configuration | A release, one-off or scheduled command pinned the group to the app's uid where the app itself leaves it to the image | `1c2310f` |
| Adding GPUs | The panel's own namespace enforced the baseline Pod Security profile, which refuses the host paths the panel keeps its database and master key on: the first install would have had no panel | `6a181d9` |

## What they had that Skifity did not, and the commit that closed it

"C", "D" and "K" say which of Coolify, Dokploy and Kubero has it; "◐" is part
of it.

| Capability | C | D | K | Closed in |
|---|---|---|---|---|
| Files an app reads, mounted at their paths (a `prometheus.yml`, an `nginx.conf`) | ✓ | ✓ | | `374b40c`, `514aa3b` |
| Templates with a start command, config files and a database handed over in pieces | ✓ | ✓ | | `88a310d`, `f9b4bb2`, `b555b38` |
| Pulling a team's private images with its own registry credentials | ✓ | ✓ | | `eb520c3` |
| A published OpenAPI description, with a test that no route is left out | ✓ | ✓ | ✓ | `a0f45bd` |
| Public TCP and UDP ports for what is not HTTP | ✓ | ✓ | | `62d2adb` |
| Deploy on a tag; `[skip ci]`; repository and branch pickers | ✓ | ✓ | ✓ | `3c39d94` |
| Run a scheduled command now | ✓ | ✓ | | `138d296` |
| A build queue with a concurrency limit | ✓ | ✓ | | `94e6e9b` |
| Reset a forgotten password by email | ✓ | ✓ | | `adddd03` |
| Clone an environment | ✓ | ✓ | | `c09ffc9` |
| An MCP server that can do more than deploy: databases, backups, domains, files, ports, variables | ✓ | ✓ | | `e07ac76` |
| Edit a notification channel; Teams and Gotify; route a channel to some projects | ✓ | ✓ | ✓ | `faeecc1` |
| Configurable health checks, and time to start | ✓ | ✓ | ✓ | `8ed96a2` |
| A DNS check for a domain before its certificate | ✓ | ✓ | | `8ed96a2` |
| Requests, error rates and response times per app | ✓ | ✓ | ✓ | `6fa2876` |
| A server hardening report | | ✓ | | `d2480c7` |
| One hostname redirecting to another (www and the bare domain) | ✓ | ✓ | | `6308052` |
| API tokens limited to listed networks | ✓ | | | `977b982` |
| A preview started by hand | ✓ | | ✓ | `a114e05` |
| More than three database engines | ✓ | ✓ | ✓ | `f4f9d09` |
| Image vulnerability scanning | | | ✓ | `ca539aa` |
| Drift detection, and a Kubernetes events feed | | | ✓ | `080fa91` |
| Private template catalogues | | ✓ | ✓ | `75ff954` |
| Your own TLS certificate | ◐ | ✓ | | `b16f93f` |
| External secret managers (Vault, Infisical, Doppler, AWS), limited to paths and projects | ✓ | ✓ | | `25e2a18`, `43ff267` |
| Bitbucket Cloud as a Git connection | ✓ | ✓ | ◐ | `505b79d` |
| Passkeys | | ✓ | | `6c3eee5` |
| Stop, start and resize a database, and change its password | ✓ | ✓ | ◐ | `6920a57` |
| Import an existing dump, with a backup taken first | ✓ | | | `6920a57` |
| GPU workloads | ✓ | ✓ | | `5dd56cf` |
| Log drains to an outside service | ✓ | | | `3731416` |
| DNS records created and kept at the provider | ✓ | ✓ | | `680449c` |
| Ordering a server at a cloud provider and joining it | ✓ | | | `2f8ef92` |

Two things were built that none of the three has, because the audit showed the
need: **Run as user** (`6f50d5d`, `1c2310f`), since Skifity is the only one of
the four that enforces a non-root user and 109 catalogue services name theirs
rather than numbering it; and scheduled backups counted apart from the ones
taken by hand (`e215a65`).

## What they get wrong that Skifity does not

Every one of these was checked against Skifity's code, not assumed.

**Coolify**

* SSH host keys are never checked (`StrictHostKeyChecking=no`), for the control
  channel that runs everything as root. Skifity records the key on first use
  and refuses a change.
* Root SSH private keys are written to disk in plaintext; runtime `.env` files
  are written in plaintext on every server; database and S3 passwords are on
  command lines. Skifity seals keys and secrets to where they are stored, and
  — since `0d1e5da` — keeps passwords off every command line.
* One application key, no rotation, and secrets readable back unless marked
  otherwise. Skifity's secrets are write-only and its master key rotates.
* Health checks are off by default, so a rolling update calls a new version
  healthy at once. Skifity always checks, TCP when nothing better is known.
* Anybody who can edit an app can add `--privileged` or mount the Docker socket;
  22 templates mount it and 7 run privileged. Skifity has no Docker socket, and
  Pod Security on every namespace.
* Automatic updates run an unsigned script from a CDN as root, on by default.
  Skifity upgrades when a person asks.
* 163 of 386 templates follow `:latest`.

**Dokploy**

* The container terminal's authorization is not tied to the container: a
  member who can read any one service can open a shell in the `dokploy`
  container, which mounts `docker.sock` — root on the host. Skifity has no
  shell, and its Advanced view is read-only.
* "Docker read" permission allows restarting, removing and writing into any
  container; a Traefik file write takes any path; patches allow path
  traversal and shell injection; a backup's bucket and prefix reach a shell
  unquoted; a web-server backup — every organisation's data — can be scheduled
  by any signed-in user; a schedule can be moved to another organisation's
  server. Skifity authorizes in one place, `authorizeTeam`, `authorizeApp` and
  their kin, and has one package for shell input.
* Only environment variables are encrypted, decryption fails open, and SSH
  keys, S3 keys, registry passwords and TLS keys are in plaintext. Skifity's
  encryption is bound to context and fails closed.
* No SSRF guard and no SSH host-key check.
* 242 of 532 templates use `:latest` or no tag.

**Kubero**

* The notifications API has no authentication at all (CVE-2026-92720).
* Any signed-in user, `guest` included, can deploy arbitrary code into any app,
  built in a privileged container.
* GitHub sign-in lets any GitHub account in, and matches accounts by username;
  a hardcoded JWT secret is the fallback; tokens cannot be revoked.
* Read APIs return deploy keys, registry passwords and every Git token.
* Any user can join any WebSocket room, so read any app's logs or type into
  another user's shell.
* Namespaces have no Pod Security, NetworkPolicy or quota; app pods can patch
  their siblings.
* Rollback changes the image only, and `pullPolicy: Always` with 149 of 173
  templates on `latest` means versions drift under running apps.

**Shared, and not yet fixed**

* The panel runs with `cluster-admin`. Kubero's operator is as broad, but in a
  process of its own; Skifity's is the internet-facing panel itself
  (ADR-0014). This is the largest open item from the audit.
* ~~A volume backup runs as uid 1000, so a directory another uid owns with mode
  0700 would archive empty.~~ It runs as the uid the app runs as, with the
  app's group; where the environment allows root, as root with only the four
  capabilities that read and write anybody's files, so a restore keeps every
  owner. A file it still cannot read stops the backup and says which setting
  to change (`internal/backup/volume.go`). Proving it on real volumes still
  needs a cluster.
* ~~A deploy in flight when the panel restarts is marked interrupted, not
  resumed; Dokploy's in-memory queue loses it the same way.~~ Resumed now,
  once, from where it stood: a build starts again, a built image goes
  straight to the rollout (`internal/deploy/resume.go`).

## Scorecard

Eighty capabilities, in the groups a person choosing a panel compares. "✓" is
has it, "◐" is has part of it, "✗" is does not; a ✓ counts one and a ◐ half.
The rows were chosen by Skifity's authors after reading all four codebases,
and weigh the same, so the totals say where the products stand on this list
and nothing more.

| Capability | Coolify | Dokploy | Kubero | Skifity |
|---|---|---|---|---|
| **Build and deploy** | | | | |
| Build from Git: Dockerfile, Nixpacks, Railpack or buildpacks | ✓ | ✓ | ✓ | ✓ |
| Build without a privileged container or the Docker socket | ✗ | ✗ | ✗ | ✓ |
| Deploy a prebuilt image | ✓ | ✓ | ✓ | ✓ |
| Deploy a folder without Git | ✗ | ✓ | ✗ | ✓ |
| A Compose file as one stack of apps | ✓ | ✓ | ✗ | ✓ |
| Repository and branch pickers | ✓ | ✓ | ✓ | ✓ |
| Bitbucket | ✓ | ✓ | ◐ | ✓ |
| Deploy on a tag | ✗ | ✓ | ✗ | ✓ |
| Skip a push with [skip ci] | ✓ | ✓ | ✗ | ✓ |
| Watch paths in a monorepo | ✓ | ✓ | ✗ | ✓ |
| A build queue with a limit | ✓ | ✓ | ✗ | ✓ |
| Secret build variables kept out of the image | ◐ | ✓ | ✗ | ✓ |
| Rollback restores settings, not only the image | ✗ | ✗ | ✗ | ✓ |
| Promote the exact image to the next environment | ✗ | ✗ | ✗ | ✓ |
| Health checks on by default, and configurable | ◐ | ✓ | ✓ | ✓ |
| Autoscaling on CPU or memory | ✗ | ✗ | ✓ | ✓ |
| Scale to zero | ✗ | ✗ | ✓ | ✓ |
| Web and worker processes from one build | ✗ | ✗ | ✓ | ✓ |
| Scheduled commands, and running one now | ✓ | ✓ | ◐ | ✓ |
| Files mounted into an app | ✓ | ✓ | ✗ | ✓ |
| Public TCP and UDP ports | ✓ | ✓ | ✗ | ✓ |
| A team's own registry credentials | ✓ | ✓ | ✗ | ✓ |
| Clone an environment | ✓ | ✓ | ✗ | ✓ |
| **Previews** | | | | |
| A preview per pull request | ✓ | ✓ | ◐ | ✓ |
| A preview of the whole environment, with its own databases | ✗ | ✗ | ✗ | ✓ |
| A fork's pull request kept from secrets | ✓ | ✓ | ✗ | ✓ |
| A preview started by hand | ✓ | ✗ | ✓ | ✓ |
| **Databases and backups** | | | | |
| Managed engines (Coolify 9, Kubero 8, Dokploy 6) | ✓ | ◐ | ✓ | ✓ |
| Off-site backups | ✓ | ✓ | ◐ | ✓ |
| Encrypted backups, verified by reading them back | ✗ | ✗ | ✗ | ✓ |
| A backup missed while the panel was down is taken late | ✓ | ✗ | ✗ | ✓ |
| Volume backups | ✓ | ✓ | ✗ | ✓ |
| The panel backs itself up | ✓ | ✓ | ✗ | ✓ |
| Import an existing dump | ✓ | ✗ | ✗ | ✓ |
| Stop, resize or change the password of a database | ✓ | ✓ | ◐ | ✓ |
| Reach a private database from a laptop | ◐ | ◐ | ✗ | ✓ |
| High-availability PostgreSQL | ✗ | ✗ | ✓ | ✓ |
| **Domains and the edge** | | | | |
| Automatic HTTPS | ✓ | ✓ | ✓ | ✓ |
| A DNS check for a domain | ✓ | ✓ | ✗ | ✓ |
| One hostname redirecting to another | ✓ | ✓ | ✗ | ✓ |
| Your own certificate | ◐ | ✓ | ✗ | ✓ |
| A firewall by address, country and network | ✗ | ✗ | ◐ | ✓ |
| A password in front of an app | ✓ | ✓ | ✓ | ✓ |
| Maintenance mode | ✗ | ✗ | ✗ | ✓ |
| **Watching** | | | | |
| CPU and memory history | ✓ | ✓ | ✓ | ✓ |
| Requests, errors and response times | ✓ | ✓ | ✓ | ✓ |
| Threshold alerts | ✓ | ✓ | ◐ | ✓ |
| A Kubernetes events feed | ✗ | ✗ | ✓ | ✓ |
| Image vulnerability scanning | ✗ | ✗ | ✓ | ✓ |
| Noticing and repairing changes made outside the panel | ✗ | ✗ | ✓ | ✓ |
| Notification channels (Dokploy 12, Skifity 10, Coolify 6, Kubero 3) | ◐ | ✓ | ◐ | ✓ |
| A channel limited to some projects | ✗ | ✗ | ✓ | ✓ |
| **Access and secrets** | | | | |
| A read-only role | ✓ | ✓ | ✓ | ✓ |
| Members limited to some projects | ✗ | ✓ | ✓ | ✓ |
| Single sign-on | ✓ | ◐ | ✓ | ✓ |
| Two-factor authentication | ✓ | ✓ | ✗ | ✓ |
| Reset a forgotten password by email | ✓ | ✓ | ✗ | ✓ |
| Secrets sealed to where they are stored, failing closed, key rotation | ✗ | ✗ | ✗ | ✓ |
| SSH host keys pinned | ✗ | ✗ | ✗ | ✓ |
| API tokens with scopes and an expiry | ✓ | ✓ | ✗ | ✓ |
| API tokens limited to listed networks | ✓ | ✗ | ✗ | ✓ |
| External secret managers | ✓ | ✓ | ✗ | ✓ |
| A server hardening report | ◐ | ✓ | ✗ | ✓ |
| An audit log | ✓ | ◐ | ✓ | ✓ |
| **Automation** | | | | |
| An OpenAPI description | ✓ | ✓ | ✓ | ✓ |
| An MCP server | ✓ | ✓ | ✗ | ✓ |
| A CLI | ✓ | ✓ | ✓ | ✓ |
| The environment described in a file, with plan and apply | ✗ | ✗ | ✓ | ✓ |
| **Templates** | | | | |
| Catalogue size (Dokploy 532, Coolify 386, Skifity 374, Kubero 173) | ◐ | ✓ | ◐ | ◐ |
| Every template on a named release | ◐ | ◐ | ✗ | ✓ |
| A template's updates offered, backed up first | ✗ | ✗ | ✗ | ✓ |
| Private catalogues | ✗ | ✓ | ✓ | ✓ |
| **Where they lead** | | | | |
| An interactive terminal into a running container | ✓ | ✓ | ◐ | ◐ |
| GPU workloads | ✓ | ✓ | ✗ | ✓ |
| Log drains to an outside service | ✓ | ✗ | ✗ | ✓ |
| Passkeys | ✗ | ✓ | ✗ | ✓ |
| DNS records created at the provider | ✓ | ✓ | ✗ | ✓ |
| **Servers** | | | | |
| Installs and joins the servers itself | ✓ | ✓ | ✗ | ✓ |
| Creates servers at a cloud provider | ✓ | ✗ | ✗ | ✓ |
| Survives losing a server (HA control plane) | ✗ | ◐ | ✓ | ✓ |

| | Coolify | Dokploy | Kubero | Skifity |
|---|---|---|---|---|
| Of 80 | 51 | 52 | 32 | 79 |

The three average 45. The one row where Skifity falls short of the best of
them by its own choice is the terminal, below; the other is the size of the
catalogue.

## Still open

* **An interactive terminal.** Skifity runs a command in an app's image and
  shows its output; it has no shell into a running container, and Dokploy's
  shows why that is a decision and not an oversight. It stays half.
* **Catalogue size:** 374 templates against Dokploy's 532. Every one of
  Skifity's is on a named release, which 242 of Dokploy's are not.
* **Wildcard certificates.** DNS records are created at the provider, but a
  wildcard through DNS-01 needs a per-team issuer and checks across teams, and
  was left out rather than half-built.
* **One cloud provider.** Servers are ordered at Hetzner only; the provider
  interface is there for others.
* One of the three shared items above: the panel's `cluster-admin`. The
  volume backup runs as whoever owns the files now, and a deploy interrupted
  by a restart is resumed.
* Everything built in this pass is tested against fakes of the services and
  clusters it talks to, and none of it has run against a real one
  (ADR-0010). `docs/progress.md` says, part by part, what that leaves unseen.
