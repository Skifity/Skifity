# Dokku

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Dokku ("A docker-powered PaaS that helps you build and manage the lifecycle of
applications") is the original self-hosted Heroku: `git push dokku main` to a
server you own, buildpacks or a Dockerfile, and a command-line interface over
SSH. It is for developers who like Heroku's workflow and a terminal, and it has
been maintained since 2013 (started by Jeff Lindsay, led for most of its life by
Jose Diaz-Gonzalez). Architecture: there is **no daemon and no control plane**.
Dokku is a set of Bash and, increasingly, Go plugins dispatched by `plugn`
through a `dokku` command that users reach as `ssh dokku@host <command>`; a git
`pre-receive` hook starts builds. Builders are pluggable (herokuish buildpacks,
Cloud Native Buildpacks via `pack`, Dockerfile, Nixpacks, Railpack, Lambda,
null), and so are **schedulers** — `docker-local` by default, **`k3s` in core
since 0.33.0 (2024-01-23)**, Nomad as a plugin — and proxies (nginx by default,
OpenResty, Caddy, HAProxy, Traefik). State is **flat files**: plugin properties
under `/var/lib/dokku/config`, generated files under `/var/lib/dokku/data`, and
parts of core under `/home/dokku`. **Licence:** MIT. **Pricing:** Dokku is free;
**Dokku Pro** is a commercial web UI, JSON API and HTTPS git push sold as a
lifetime licence covering one production and two pre-production servers.
`pro.dokku.com` showed **"$849 for life"** on 2026-09-30, while the Pro 1.5.0
announcement (2026, after 2026-08-09) says "this release increases Dokku Pro's
price to $999"; Pro must reach the internet to validate its licence "or it will
fail to start". **Maturity:** still 0.x after 13 years; minor releases 0.36.0
(2025-07-24), 0.37.0 (2025-11-20), 0.38.0 (2026-04-30), then **31 patch releases
to 0.38.31 (2026-09-26)** in five months (Docker Hub tag dates). **Adoption:**
32,166 GitHub stars, 2,081 forks, and only 29 open issues and pull requests
(GitHub API, 2026-09-30) — a number every review mentions. Requirements: Ubuntu
22.04/24.04/26.04 or Debian 11+, amd64 or arm64, **1 GB of memory for the Docker
scheduler and 2 GB on every node for the k3s scheduler** (Dokku's own docs).

## Feature inventory

### Deploy sources and builds

* **`git push dokku main`** over SSH, the defining workflow; also `git:sync`
  (clone from a remote repository, used by Pro's GitHub integration),
  `git:from-image` (a prebuilt image), `git:from-archive` (a tar, tar.gz or zip
  from a URL) and `git:load-image`.
* **Builders:** herokuish (Heroku buildpacks), CNB via `pack`, Dockerfile,
  Nixpacks, **Railpack (0.37.0)**, Lambda, and `null`. Buildpacks can be declared
  in `app.json` since 0.38.0; `buildpacks:detect` (0.37.0).
* **Build records** (0.38.0): the builds plugin, rewritten in Go, keeps a history
  per app and `builds:output` prints any build's log after the fact.
* **Build containers can be resource-limited** (0.38.0), so one runaway build
  does not starve running apps.
* **`app.json`** is the declarative per-repository file: `formation` (process
  counts and autoscaling), `healthchecks`, `cron`, `scripts.dokku.predeploy` /
  `postdeploy`, buildpacks and `env`.
* **Procfile process types**: `web`, `worker`, anything; each scaled on its own
  with `ps:scale app web=2 worker=1`, restarted on its own, and given its own
  Docker options (0.38.0).
* `apps:clone`, `apps:rename` (which since 0.38.0 keeps every domain).

### Domains, TLS and routing

* A global domain makes every app `<app>.<domain>`; the install docs suggest
  `<ip>.sslip.io` when there is no domain. Per-app domains with `domains:add`,
  **wildcard domains on k3s (0.38.26)**.
* **Custom certificates** with `certs:add` (a tar of `.crt` and `.key`; since
  0.38.2 the archive is validated against traversal and symlinks).
* **Let's Encrypt** through the official `letsencrypt` plugin (nginx), Traefik's
  own ACME with **DNS-01 challenges for wildcards** (`traefik:set --global
  challenge-mode dns`, dns-provider credentials masked in reports), or
  cert-manager on k3s with a per-app email (0.38.25) and manual issuers (0.38.26).
* nginx is configured by `nginx:set` properties or a per-app `nginx.conf.sigil`
  template, pre-validated before a deploy since 0.38.0. A catch-all default site
  rejects unknown `Host` headers on fresh installs (0.38.0).
* Official plugins for **HTTP auth**, **redirects** and **maintenance mode**;
  port mappings with `ports:add` (`http:80:5000`, `https:443:5000`, TCP via
  Docker options).

### Databases and services

* **28 official plugins**, most of them datastores: PostgreSQL, MySQL, MariaDB,
  Redis, MongoDB, Elasticsearch, RabbitMQ, Memcached, ClickHouse, CouchDB,
  Meilisearch, NATS, RethinkDB, Solr, Typesense, OmniSci, Pushpin, Graphite. 54
  more community plugins are listed as active (and 71 as deprecated or
  unmaintained).
* The PostgreSQL plugin shows the shape of all of them: `create`, `link` (sets
  `DATABASE_URL`), `promote`, `expose` (publish a port), `import`/`export` (dump
  files), `clone`, `upgrade`, `connect`, `enter`, and **backups to any
  S3-compatible bucket with a cron schedule and passphrase or GPG encryption**
  (`backup-schedule`, `backup-set-encryption`,
  `backup-set-public-key-encryption`).
* Services are Docker containers on the Dokku host; nothing in the k3s docs
  describes running them inside the cluster.

### Storage and backups

* **Named storage entries** (0.38.0): `storage:create` / `storage:mount`,
  scheduler-aware. On k3s an entry becomes a PersistentVolumeClaim in its own
  Helm release, with a storage class (the docs' example is Longhorn), access mode
  (ReadWriteMany allowed) and reclaim policy; annotations propagate so Velero or
  Longhorn snapshots can find them.
* **Backups of Dokku itself** are a documented `tar` of `/home/dokku` and
  `/var/lib/dokku/{config,data,services,plugins}` taken "at a time when not
  executing any Dokku commands"; restore is untar and rebuild. No schedule, no
  volume backups, no restore command.
* Datastore backups as above.

### Scaling and high availability

* **docker-local zero downtime:** start new containers, wait (10 s by default,
  or user-defined `startup`/`readiness`/`liveness` checks from `app.json` since
  0.31.0, with wait/timeout/attempts), switch nginx, then send SIGTERM to the old
  containers immediately (0.38.0) and stop them after `wait-to-retire`
  (default 60 s). `checks:disable` trades that for downtime.
* **k3s scheduler** (per app: `scheduler:set app selected k3s`):
  `scheduler-k3s:initialize` puts k3s on the Dokku host;
  `scheduler-k3s:cluster:add ssh://root@host` joins workers, or servers for an
  HA etcd ("an odd number of nodes spread across several availability zones");
  node profiles, taints, kubelet arguments, node sysctls; ingress-nginx or
  Traefik; cert-manager; Vector for log shipping; **KEDA autoscaling from
  `app.json`** with any KEDA trigger, trigger authentication, a fallback replica
  count, and the **KEDA HTTP add-on** for request-rate or concurrency scaling
  including scale to zero; `rollback-on-failure`; Kustomize overlays from the
  repository; `scheduler-k3s:preview`, a unified diff of the Helm manifests the
  next deploy would apply, with Secret values redacted by default. Dokku "pins
  the version of every chart it manages", and `ensure-charts` reconciles drift
  back to the pin. It can also drive an external cluster via `kubeconfig-path`.
* The k3s scheduler **requires an external or self-run Docker registry**; images
  are still built on the Dokku host.
* Only the initial server is the git remote: "Ensure this server is properly
  backed up and restorable or deployments will not work."

### Observability (logs, metrics, alerts, uptime)

* `dokku logs -t`, `nginx:access-logs`, `nginx:error-logs`; on k3s these read
  from the Kubernetes API and one ingress-nginx pod.
* **Vector** log shipping to any sink (`logs:set --global vector-sink`), and a
  separate sink for cron output (0.38.27).
* `dokku events` event log; every `:report` command has `--format json` and
  `--global`.
* No metrics dashboards, alerts or uptime checks in core; an official
  Graphite/StatsD/Grafana plugin exists. "No first-class observability or cost
  tooling" (Ownkube, 2026-05-08).

### Security, auth, roles, SSO, audit

* **SSH keys** (`ssh-keys:add`). By default every key can run every command;
  "support for scoping commands to specific users can be added through plugins
  that take advantage of the `user-auth` plugin trigger".
* **Dokku Pro:** user and team management with per-team commands, apps and
  services (1.2/1.3.0, 2025), passwords set from the UI, API or CLI (1.5.0),
  identity-aware reverse-proxy authentication (1.4.0), JWT-authenticated
  JSON:API "enabling easier, audited remote management".
* Secret-looking values are masked in reports; no encryption of app config at
  rest; no 2FA of its own (SSH keys and, for Pro, whatever the reverse proxy
  enforces).

### Preview environments and branches

* None built in. `apps:clone` is a building block; "Preview environments require
  scripting" (Ownkube, 2026).

### Templates and catalogue

* No one-click catalogue of applications. Datastore plugins and `app.json` fill
  part of that role.

### CLI, API, IaC, integrations

* The CLI *is* the product: `ssh dokku@host apps:create`, `config:set`,
  `ps:scale`, `run`, `enter`, `cron:run`; `config:import` (0.37.0) for a whole
  `.env`; `config:set --no-restart` to batch changes.
* **Official GitHub Action** (`dokku/github-action`) and documented GitLab CI,
  Woodpecker and generic CI recipes.
* No HTTP API in core; Dokku Pro adds a JSON:API with Docker-image deploys,
  batch operations and certificate management (1.4.0).
* An unofficial MCP server exists (`dokku-MCP/dokku-mcp`), and third-party tools
  reconstruct a server's declarative state from the reports (issue #8800 cites
  `docket export`).

### Notifications

* None built in. Plugin triggers (`post-deploy`, `post-release-builder`, ...)
  are where community plugins send Slack or webhook messages.

### Multi-server and networking

* docker-local is one host; `network:create` attaches apps to Docker networks.
* k3s scheduler for a cluster (above); Nomad scheduler plugin.
* **Dokku Pro 1.5.0** manages *several independent Dokku servers* from one UI:
  "the browser makes cross-origin, Bearer-authenticated calls straight to the
  selected server's API", with no server-side proxy "so one compromised server
  cannot borrow another's credentials".

### Team and collaboration

* Core: shared SSH access. Pro: users, teams, owners, per-team app and service
  scopes; "read-only teams that can be scoped to specific apps" is next on Pro's
  roadmap.

### Developer experience and onboarding

* `bootstrap.sh` via apt, "about 5-10 minutes"; then add an SSH key and a global
  domain. Heroku habits transfer directly.
* A migration guide for every minor version; 0.38.0 moved many `DOKKU_*`
  config variables into plugin properties (migrated automatically) and changed
  the storage model.
* Pro 1.4.0 (2026) redesigned the web UI with light and dark themes, in-browser
  process management, streaming logs and bulk environment variables.

## What users love

* **The Heroku loop, unchanged.** "Create an app, add a Dokku remote, and deploy
  with `git push dokku main`" (Sliplane, 2026). "Rock-solid on a single host.
  Buildpacks mean your Heroku-style app code just works. Tiny resource
  footprint" (Ownkube, 2026-05-08).
* **Better than Kamal, for some.** "I've tried several times to migrate a few
  apps I have from Dokku to Kamal ... but I've always given up after an hour or
  so ... the Dokku experience is so much better than Kamal" (HN, 2025-01-21).
* **Maintenance quality.** "15 open issues on GitHub is remarkable for a project
  with 31,900 stars"; "most battle-tested option" with "a decade of production
  use" (BuildMVPFast, 2026-06-12). 29 open issues and PRs on 2026-09-30.
* **Light.** A third-party comparison puts Dokku's idle overhead at "~95 MB"
  (selfhostable.dev, 2026-01-28; not independently measured, and the same
  article gets other numbers wrong).
* **Datastores with real operations.** Ownkube (2026-05-08) singles out the
  "mature Postgres plugin with backups"; the official plugins cover about
  nineteen engines with the same link/expose/import/backup commands.

## What users complain about

* **No UI unless you pay.** "No web GUI by default; CLI-only operation"
  (BuildMVPFast, 2026); the UI is Dokku Pro at $849–$999.
* **"Single server."** Nearly every 2026 comparison says so — "Dokku is
  single-server only" (BuildMVPFast), "Single-node by design" (Ownkube) — even
  though the k3s scheduler has been in core since January 2024. Either the
  multi-server story is not being told or it is not trusted; an HN user in April
  2025: "I am also curious about Dokku + k3s. I have used Dokku for a long time
  but only on a single host."
* **Odd ergonomics.** "Dokku's design is a little weird. Overcomplicated shell
  scripts and Go, with a weird command-line argument format" (HN, 2025-01-21).
* **You still own the server.** "Updates, security, monitoring, backups, and
  recovery"; "Plugins need care: Plugins are powerful, but you need to understand
  what they install and how they store data" (Sliplane, 2026).
* **Rough edges on k3s.** Initialisation failing while the KEDA HTTP add-on's
  interceptor never became ready (#7573, 2025-03); `cluster:add` failing with "no
  nodes found in the cluster" (#8721, 2026-05); chart versions ignored on upgrade
  (#9053, 2026-09); `dokku run` not propagating exit codes (#9002, 2026-09). The
  0.38.x patch stream is mostly k3s and storage fixes.
* **Zero-downtime is not always zero.** With the Traefik proxy, "traffic goes to
  both the new container and the old (retiring) container", so an HTML page from
  the new version asked the old one for `new.js` (#8282, 2026-01-16).
* **Breaking changes every minor.** 0.38.0 changed env storage, the storage
  model and nginx defaults; a migration guide per minor is honest and also a
  tax.

## Security record

Six GitHub Security Advisories in 2026, five of them Critical, all in the same
class: a string a user controls reaching a shell or `tar` on the host.

| Advisory | CVE | Published | Severity | What | Fixed in |
|---|---|---|---|---|---|
| GHSA-9x85-7gxq-fcr3 | CVE-2026-45408 | 2026-05-13 | Critical (CVSS 9.0) | App name regex allowed shell metacharacters, embedded unquoted in the git `pre-receive` hook via an unquoted heredoc; any user with push access runs commands as `dokku` | 0.38.2 |
| GHSA-ggqh-98fj-8mg9 | CVE-2026-45406 | 2026-05-13 | Critical (9.0) | OpenResty include filenames from the app's repository interpolated into a single-quoted string later passed to `eval` | 0.38.2 |
| GHSA-j6qq-xg73-ghqg | CVE-2026-45405 | 2026-05-13 | Critical (9.0) | `git:from-archive` and `certs:add` extracted archives without sanitising paths or symlinks; overwrite `~/.ssh/authorized_keys` | 0.38.2 |
| GHSA-xh7p-9crg-pchr | CVE-2026-45407 | 2026-05-13 | Moderate (5.0) | `.netrc` with git credentials created 0644 by `touch` before `netrc` could set 0600 | 0.38.2 |
| GHSA-72vm-7pc2-x95w | CVE-2026-54636 | 2026-06-16 | Critical (9.0) | `app.json` cron commands with `>` or `;` escaped the container and ran on the host as `dokku` | 0.38.7 |
| GHSA-fccf-j7qf-2xp9 | CVE-2026-86002 (as listed on the advisory) | 2026-09-24 | Critical (9.0) | `docker-options` strings concatenated into `DOCKER_ARGS` and run through Bash `eval`; "any user with app-level dokku access" | 0.38.25 |

NVD lists the first five (published there 2026-06-26); the sixth was not yet in
NVD's keyword results on 2026-09-30. The `dokku` user can run Docker, so
command execution as `dokku` is effectively root on the host. The pattern is
the one `docs/research/competitors.md` already quotes for Coolify — "user input
reaches a shell without enough sanitization, in many independent code paths" —
and Dokku's 13 years of Bash plugins are many paths. To its credit, every fix
shipped within days of the report, with advisories and a blog post urging the
upgrade.

## Against Skifity

Dokku's k3s scheduler is the closest architectural relative Skifity has: k3s
joined over SSH, cert-manager, Traefik or ingress-nginx, KEDA with the HTTP
add-on, Longhorn-backed volumes. The difference is that Dokku bolts it onto a
CLI whose default is still docker-local, while Skifity is k3s-only and hides it.
Skifity statuses: almost everything cluster-facing is **Written, never run**
(ADR-0010).

| Capability | Dokku | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy sources and builds | git push, git:sync, image, archive URL; herokuish, CNB, Dockerfile, Nixpacks, Railpack; build history; build limits | Git with webhooks (`internal/gitsrc/webhook.go`), image, folder upload (`internal/cli/up.go`, `internal/upload/upload.go`); Railpack, Nixpacks, Dockerfile, static (`internal/builder/detect.go`); build logs kept for the last 20 builds; builds in-cluster (`internal/builder/job.go`). No git-push remote, no Heroku buildpacks. Written, never run | Parity; different entry points |
| Process types | Procfile `web`/`worker`/..., `ps:scale web=2 worker=1` from one build | One app runs one process; a Procfile is only noted (`internal/builder/detect.go:348-350`); a worker is a second app with its own build | **Behind** |
| Release/predeploy tasks | `app.json` predeploy/postdeploy | Release command before traffic (`internal/store/models.go:247-249`, `internal/deploy/deployer.go:239`) | Parity |
| Domains, TLS and routing | Custom certs, Let's Encrypt, DNS-01 wildcards (Traefik), cert-manager on k3s, HTTP auth, redirects, maintenance plugins, nginx templates | sslip.io address, cert-manager HTTP-01 only (`internal/cluster/components.go:160-166`), HTTPS redirect (`internal/kube/namespace.go:351`); no custom certs, DNS-01, basic auth, redirects or maintenance mode | **Behind** on TLS options and proxy features |
| Databases and services | 19-ish datastore plugins; link, expose, import/export, clone, upgrade, S3 backups with encryption | PostgreSQL (CloudNativePG), MySQL, Redis (`internal/dbsvc/manifests.go`); sealed credentials; S3 backups and restore (`internal/backup/`); no import of an external dump, no expose, no backup encryption, MongoDB named but not makeable (`internal/builder/needs.go`) | **Behind on breadth and data-migration tools**; ahead on operator-managed PostgreSQL (Written) |
| Storage and backups | Named PVC entries on k3s, RWX allowed; Dokku itself backed up by hand-run tar | PVCs, volume backup and restore to S3 on a schedule (`internal/backup/volume.go`, `volumerestore.go`); panel DB backed up by hand (`internal/cli/admin.go:207`) | Skifity ahead on volumes; parity on the control plane |
| Scaling and high availability | Checks-based zero downtime; k3s: KEDA any trigger + HTTP add-on, rollback-on-failure, preview diff, pinned charts | Rolling update with `maxUnavailable: 0` and a 5 s preStop (`internal/kube/manifests.go:72-86`), HPA (`:362`), KEDA scale-to-zero (`internal/kube/scaletozero.go`), scaling readiness checker (`internal/deploy/scaling.go`); **new servers follow k3s `stable` unless pinned** (`internal/provision/scripts.go:396-401`) | Parity on mechanisms; Skifity ahead on readiness checks; **behind on version pinning** and on non-CPU autoscaling triggers |
| Observability | Logs, access/error logs, Vector shipping, events | Live logs and the previous container's log (`internal/api/api.go:287`), per-instance usage, `/api/metrics`, optional Prometheus/Grafana; no log shipping | Parity; behind on log shipping |
| Security, auth, roles, SSO, audit | SSH keys, every key can do everything; Pro teams; 5 critical CVEs in 2026 | Argon2id, TOTP, OIDC, roles, audit, route-walk tests; one quoting function tested against a real `sh` (`internal/shellsafe`); archives refused if they hold links, absolute paths or `..` (`internal/upload/upload.go:207`); commands run inside the app's container, never on the host (`internal/kube/runjob.go:115-116`). Works for panel auth | Skifity ahead by construction; not yet tested by outsiders |
| Preview environments | None | Per-PR previews with fork isolation (`internal/api/webhook_handlers.go:184-280`). Written, never run | Skifity ahead |
| Templates and catalogue | None | 282 templates (`internal/templates/catalogue/`) | Skifity ahead |
| CLI, API, IaC | SSH CLI, `app.json`, `--format json`, official GitHub Action; API only in Pro | One API for panel, CLI, MCP; `--json`; `skifity.toml`; errors with cause/impact/fix (`internal/errdoc/catalogue.go`); **no CI recipe, no one-shot image deploy** (`internal/cli/commands.go:468-476`); **N variables = N rollouts** (`internal/api/apps_handlers.go:800-807`) | Skifity ahead overall; **behind on batch config and CI** |
| Notifications | None in core | Seven events, four channels plus plugins (`internal/notify/notify.go:41-47`) | Skifity ahead |
| Multi-server and networking | k3s cluster add over SSH, HA etcd, node profiles; Pro manages several servers | k3s join over SSH, password used once (`internal/provision/`), promote a server (`internal/api/api.go:245`), Cloudflare tunnel (`internal/cluster/tunnel.go`) | Parity in design; Dokku's has run in production |
| Team and collaboration | Pro only | Teams, invitations, roles, environments (`internal/api/api.go:201-258`) | Skifity ahead |
| Developer experience and onboarding | Heroku muscle memory; CLI only; 2 GB per k3s node | Panel, CLI and browser upload, five languages; installer asks for 900 MB (`installer/install.sh:81-82`) against Skifity's own estimate of ~550 MiB for k3s plus panel before cert-manager, registry, BuildKit and CloudNativePG (`docs/performance.md`) | Skifity ahead on UX; **its memory floor is unmeasured and looks optimistic next to Dokku's 2 GB** |

## Gaps worth closing in Skifity

### P0 — Join new servers at the cluster's own k3s version

* **What:** when a server is added, install the k3s version the control plane is
  running, not whatever the `stable` channel says that day.
* **Evidence:** Skifity passes `INSTALL_K3S_CHANNEL="stable"` whenever the
  "Kubernetes version" setting is empty (`internal/provision/scripts.go:396-401`),
  and it is empty by default (`internal/settings/settings.go:223-227`, whose help
  text asks the operator to remember to pin it). A server added a few months
  after install therefore gets a newer minor than the API server, and the
  Kubernetes skew policy is explicit: "kubelet must not be newer than
  kube-apiserver." Dokku's k3s scheduler takes the opposite stance: it "pins the
  version of every chart it manages" and reconciles drift back to the pin.
  CapRover's November 2025 outage (Docker 29 dropping an API every older
  CapRover used) is what following the host runtime's latest looks like.
* **Fit:** `internal/provision/provisioner.go` `k3sVersion()` falls back to the
  control-plane node's `KubeletVer`, which the panel already reads
  (`internal/api/ports.go`, `NodeInfo.KubeletVer`); the setting remains an
  override; the Add Server page shows the version it will install.
* **Size:** S. **Without a cluster:** yes — a provisioner test with a fake
  clientset asserting the script carries the control plane's version.

### P1 — Several processes from one build

* **What:** an app declares processes (`web`, `worker`, `scheduler`), read from a
  Procfile or entered by hand; one build, one Deployment per process, each with
  its own command, instance count and resources; only `web` gets a port and a
  domain.
* **Evidence:** Dokku's `ps:scale app web=2 worker=1` and per-process options,
  Kamal's roles (same image, different `cmd`), Heroku's formation. Skifity's
  detector sees a Procfile and says only "The Procfile is used to start the app"
  (`internal/builder/detect.go:348-350`); a Rails, Django or Laravel app with a
  queue worker becomes two apps and two builds, and the templates already model
  workers as separate services with `port: 0` because there is nothing else.
* **Fit:** a `processes` table beside `apps` (`internal/store`), rendering in
  `internal/kube/manifests.go` (one Deployment and HPA per process, one Service
  for `web`), the scaling readiness checker per process
  (`internal/deploy/scaling.go`), rollback restoring the formation, and a
  Processes card on the Scaling tab; `skifity scale worker=3`.
* **Size:** L. **Without a cluster:** yes for golden manifests, the API, the CLI
  and a Playwright step; the running result needs one.

### P1 — Set several variables in one rollout, and import a `.env`

* **What:** a batch variables endpoint and `skifity env set A=1 B=2` sending one
  request; `skifity env import .env` and a paste box on the Variables tab.
* **Evidence:** every `PUT /api/apps/{app}/variables` calls `Deployer.Sync`
  (`internal/api/apps_handlers.go:800-807`) and the CLI loops over pairs
  (`internal/cli/commands.go:696-723`), so three variables are three rollouts.
  Dokku has `config:set A=1 B=2`, `--no-restart` and `config:import` (0.37.0);
  Dokku Pro 1.4.0 made "bulk environment variables ... a single rebuild" a
  headline; Kamal pushes the whole env at once.
* **Fit:** `internal/api` accepts a list and syncs once, reusing the
  `secretness()` rule per key; the new-app form already accepts a pasted `.env`,
  so the parser exists.
* **Size:** S. **Without a cluster:** yes (API tests, CLI, `make smoke`).

### P1 — Import an existing database dump

* **What:** upload a `pg_dump`/`mysqldump` file (or give a URL) and load it into a
  managed database; the matching export as a download.
* **Evidence:** Dokku's `postgres:import` / `export` / `clone` are how people
  move onto and off Dokku. Skifity can restore only its own S3 backups
  (`POST /api/databases/{db}/restore/{backupID}`, `internal/api/api.go:327`), so
  somebody arriving from Heroku, Dokku or CapRover has no documented way to
  bring data.
* **Fit:** the upload path of `internal/upload` and the exec delivery of
  ADR-0022 feed the file to the restore Job in `internal/backup`; the restore
  script already refuses a half-read pipeline (Phase 72). A Database page
  "Import" action, confirmation that it replaces contents.
* **Size:** M. **Without a cluster:** scripts yes, with the stub `psql` tests
  that already exist; the end-to-end load needs one.

### P2 — Encrypt backups before they leave the cluster

* **What:** database and volume backups encrypted with a key the panel holds
  (and the recovery key can recover), so a leaked bucket is not a leaked
  database.
* **Evidence:** Dokku's datastore plugins offer passphrase and GPG public-key
  encryption for scheduled backups. `internal/backup` uploads plaintext
  compressed dumps (no encryption anywhere in the package).
* **Fit:** the dump script pipes through `age` (or `openssl`) with a per-install
  key sealed by the keyring; the restore script decrypts; `docs/backups.md`
  explains restoring on a different cluster.
* **Size:** M. **Without a cluster:** yes, with the stub-tool script tests.

### P2 — Show what the next deploy will change

* **What:** a diff between the objects last applied and the objects the next
  deploy would apply, Secrets redacted, on the Advanced tab and as
  `skifity deploy --dry-run`.
* **Evidence:** Dokku's `scheduler-k3s:preview` (0.38.x) does exactly this.
  Skifity renders deterministically and already shows the current objects
  (`GET /api/apps/{app}/advanced`, `internal/api/api.go:314`).
* **Size:** S–M. **Without a cluster:** yes; rendering is pure.

### P2 — More managed engines, starting with MongoDB

* **What:** MongoDB first, then MariaDB (whose client image the MySQL backup
  Job already uses, `internal/backup/jobs.go:56`), as managed databases with
  backups.
* **Evidence:** Dokku ships official plugins for about nineteen datastores.
  Skifity's own detector finds MongoDB drivers and has to say it cannot make one
  (`internal/builder/needs.go`).
* **Size:** L per engine (operator choice, backup and restore scripts, the
  engine list the form reads). **Without a cluster:** manifests and scripts yes;
  the operator needs one.

## Things to deliberately not copy

* **Bash plugins with `eval`, unquoted heredocs and string-built `DOCKER_ARGS`.**
  Five critical advisories in four months came from exactly that. Skifity's rule
  of one quoting function (`internal/shellsafe`), tested against a real shell and
  linted by shellcheck at `style`, is the thing to keep defending.
* **Running user-supplied commands on the host.** Dokku's cron plugin put
  `app.json` commands into the host's crontab; Skifity runs them in the app's own
  container, under the environment's Pod Security level (`restricted` by
  default, `baseline` at most, `internal/kube/podsecurity.go`), which refuses
  host namespaces, hostPath and privileged containers either way. Keep the
  boundary.
* **Every credential can do everything.** Dokku's default is that any SSH key
  runs any command; scoping is a plugin. Skifity's authorization in one place
  (`authorizeTeam`, `authorizeApp`) with a route walk is the right default.
* **A licence check that phones home before the UI will start.** Dokku Pro fails
  to start without reaching the internet. Skifity's promise is the opposite.
* **Selling the UI.** Dokku's biggest complaint in every comparison is "CLI only
  unless you pay"; Skifity's panel is the product.
* **Two schedulers with different feature matrices.** Dokku's k3s docs list what
  docker-local does that k3s does not (and contradict themselves on persistent
  storage). Skifity being k3s-only is simpler to document and to test.
* **Letting users edit proxy templates** (`nginx.conf.sigil`, OpenResty
  includes). Powerful, and one of the 2026 RCEs came through include filenames.
* **Requiring an external registry for multi-node.** Skifity's in-cluster
  registry with garbage collection is better.
* **State spread across three directories of flat files** with a backup
  procedure that says "at a time when not executing any Dokku commands". One
  SQLite file with `VACUUM INTO` is the better shape.

## Sources

All read 2026-09-30.

* https://github.com/dokku/dokku (stars, forks, open issues via the GitHub search API)
* https://github.com/dokku/dokku/releases and https://raw.githubusercontent.com/dokku/dokku/master/HISTORY.md
* https://hub.docker.com/v2/repositories/dokku/dokku/tags/<tag> (release dates for 0.33.0, 0.34.0, 0.35.0, 0.36.0, 0.37.0, 0.38.0, 0.38.2, 0.38.7, 0.38.25, 0.38.31)
* https://github.com/dokku/dokku/releases/tag/v0.37.0 and /v0.38.0 (feature lists; dates taken from Docker Hub instead)
* https://dokku.com/blog/2026/dokku-0.38.0/
* https://dokku.com/blog/2026/pro-release-1.5.0/
* https://dokku.com/blog/2026/pro-release-1.4.0/
* https://dokku.com/blog/2025/pro-release-1.3.0/
* https://pro.dokku.com/
* https://dokku.com/docs/enterprise/pro/
* https://dokku.com/docs/getting-started/installation/
* https://dokku.com/docs/deployment/schedulers/k3s/
* https://dokku.com/docs/deployment/zero-downtime-deploys/
* https://dokku.com/docs/processes/process-management/
* https://dokku.com/docs/configuration/ssl/
* https://dokku.com/docs/networking/proxies/traefik/
* https://dokku.com/docs/advanced-usage/backup-recovery/
* https://dokku.com/docs/deployment/user-management/
* https://dokku.com/docs/deployment/continuous-integration/github-actions/
* https://dokku.com/docs/community/plugins/
* https://raw.githubusercontent.com/dokku/dokku-postgres/master/README.md
* https://github.com/dokku/dokku/security/advisories
* https://github.com/dokku/dokku/security/advisories/GHSA-fccf-j7qf-2xp9
* https://github.com/dokku/dokku/security/advisories/GHSA-9x85-7gxq-fcr3
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=dokku
* https://github.com/dokku/dokku/issues/7573
* https://github.com/dokku/dokku/issues/8282
* https://github.com/dokku/dokku/issues/8721
* https://github.com/dokku/dokku/issues/8800
* https://github.com/dokku/dokku/issues/9002
* https://github.com/dokku/dokku/issues/9053
* https://kubernetes.io/releases/version-skew-policy/
* https://news.ycombinator.com/item?id=42782854 (via hn.algolia.com API)
* https://news.ycombinator.com/item?id=43591098 (via hn.algolia.com API)
* https://www.buildmvpfast.com/blog/coolify-vs-dokku-vs-caprover-self-hosted-paas-production-2026
* https://ownkube.io/blog/self-hosted-paas-comparison-2026
* https://selfhostable.dev/blog/coolify-vs-caprover-vs-dokku/
* https://sliplane.io/blog/dokku-self-hosted-heroku-alternative
* https://github.com/dokku-MCP/dokku-mcp (via search result)
