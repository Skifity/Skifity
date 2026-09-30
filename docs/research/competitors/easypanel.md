# Easypanel

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Easypanel is a commercial, closed-source server control panel that turns one VPS
(or, on the Business plan, a small Docker Swarm) into a self-hosted PaaS. It is
aimed at solo developers, agencies and small teams who want Heroku-style deploys
and one-click open-source apps without learning Docker. Architecture: the
installer puts Docker on an Ubuntu server, **initialises Docker Swarm** and runs
the panel as a Swarm service with Traefik in front; the panel itself is a
Node.js 22 bundle (`node backend.js start`, Alpine image, ~352 MB of compressed
layers) that drives Docker through the socket, and keeps its own state in
**SQLite at `/etc/easypanel/data/data.sdb`** (better-sqlite3 plus an LMDB
key-value module), read out of the published 2.36.0 image on 2026-09-30.
Minimum is 2 GB of RAM and ports 80/443 free. Licence: proprietary; only the
template definitions are public (and that repository has no LICENSE file).
Pricing is per server: Free (up to 3 projects, unlimited services, basic
monitoring), Hobby $10.90/server/month billed yearly ($14.90 monthly), Growth
$16.90 ($23.90), Business $29.90 ($37.90); cancelling drops back to the free
plan and running projects keep running. Maturity: 2.36.0 released 2026-09-24,
78 releases listed on the changelog, roughly monthly minor releases, a public
API/CLI/MCP only since 2.33.0 (2026-07-31). Adoption: the site claims "145,000+
self-hosters"; `easypanel/easypanel` has 7,489,914 Docker Hub pulls; the
templates repository has 206 stars and 195 forks; the panel is not on GitHub
(all as of 2026-09-30, sources below).

## Feature inventory

### Deploy sources and builds

* **App service** from GitHub (repo picker and branch dropdown since 2.31.0),
  any Git URL over SSH with a per-service deploy key, an uploaded archive (zip,
  tar, tar.gz/bz2/xz/zst, 7z, rar since 2.32.0), a Docker image with optional
  registry credentials, or an **inline Dockerfile** stored in the panel.
* Builders: Dockerfile, Cloud Native Buildpacks, Nixpacks and Railpack, each
  with install/build/start overrides; "Force Rebuild" skips the cache. A remote
  Docker builder can be attached (`docs/guides/remote-docker-builder`).
* **Compose service**: inline YAML or a Compose file from Git, deployed with
  `docker compose up --build -d`; the panel flags `container_name` and `ports`
  as conflicts but otherwise runs what it is given.
* **Box service**: a runtime assembled from modules (Node, PHP, Python, Ruby,
  Nginx) with processes, deploy scripts and an optional browser IDE.
* **WordPress service**: a first-class service type with PHP/Nginx settings,
  WP-CLI operations (users, roles, plugins, themes, search-replace with a dry
  run, cache flush, DB optimise) and an **Update** section that updates core
  and runs the database upgrade. About 40 API operations exist for it alone.
* Auto-deploy through a GitHub webhook, plus a per-service **deployment
  trigger URL** for CI. Saving settings does not deploy; the panel asks.
* **No rollback.** The 2.36.0 API has 372 operations and none rolls a service
  back; "Deployments" is a history list. (Read from the bundle, 2026-09-30.)

### Domains, TLS and routing

* Automatic Let's Encrypt; an automatic `*.easypanel.host` service domain; a
  wildcard/custom service domain on paid plans; custom certificates.
* Per-domain path routing, internal HTTP/HTTPS, **Traefik middlewares**, custom
  Traefik config and the Traefik dashboard (Traefik 3.7.13 since 2.35.0).
* Regex **redirect rules** (temporary or permanent, can be disabled) and
  **maintenance mode** (a page replaces traffic while containers keep running;
  custom logo/CSS on white-label plans).
* **Cloudflare Tunnel** with rules managed from the panel (11 API operations).
* Published TCP/UDP ports for non-HTTP services.

### Databases and services

* Managed PostgreSQL (18 is the default since 2.34.0), MySQL, MariaDB, MongoDB
  and Redis, each with credentials, resource limits, "expose" to the internet,
  and a one-click admin UI: DbGate for all, plus phpMyAdmin, pgweb,
  Mongo Express or Redis Commander.
* Templates create database *services* next to the app and wire them by
  hostname (`$(PROJECT_NAME)_<service>`), with a generated password.

### Storage and backups

* Mounts: named **volumes**, **bind mounts** to any host path, and **file
  mounts** whose content is stored in the service config.
* **Database backups** (paid for scheduled runs): logical dumps (`mysqldump`,
  `mariadb-dump`, `pg_dump` custom format, `mongodump`), cron schedule, count
  retention, named manual runs, and a **restore** that replaces the database in
  place. Redis has no backup UI.
* **Volume backups** are an `rclone sync` **mirror**: "not a timestamped
  snapshot", later runs "can delete destination files", **no retention and no
  restore action** — recovery is a documented manual procedure. Bind and file
  mounts are not covered.
* Destinations: S3 and S3-compatible (R2 preset, storage class since 2.32.0),
  FTP/SFTP (password only), Dropbox, Google Drive, local path. Connection
  validation only proves the provider can *list*.
* The panel's own state (`/etc/easypanel`) has no documented backup or
  migration path; `projects/importProject` exists but a 2025 issue reports
  import problems (#108).

### Scaling and high availability

* A **replicas** field and a zero-downtime toggle per app; resource
  reservations/limits in MB and cores.
* **Cluster support (alpha)** on Business only: one Swarm manager, many workers,
  "best suited for stateless services"; "does not support multiple manager
  nodes, meaning you will not achieve a fully redundant infrastructure".
  Optional `SWARM_ENDPOINT_MODE=dnsrr` since 2.35.0.
* No autoscaling, no scale-to-zero, no readiness check before scaling.

### Observability (logs, metrics, alerts, uptime)

* Live logs per service, Compose logs filterable by container, level, stream
  and text; a "detected service errors" panel.
* **Advanced monitoring** (2.29.0, 2026-04-16, paid): opt-in Prometheus metrics
  (CPU, memory, disk, network, per service and system) and **Loki** log search
  with retention.
* Alerts are notification events only (see Notifications); there is no
  uptime/HTTP check and no "app is down" event.

### Security, auth, roles, SSO, audit

* Password sign-in, **TOTP 2FA** (free), per-user API keys.
* Users are `admin: true|false`; non-admins get **per-project access**
  (`projects/updateAccess`) — "Multiple users" and "Access control" are Growth
  plan features.
* **No SSO, OIDC, SAML or LDAP.** No audit log as such; the "Actions" page is
  an operation history.
* Per-service **HTTP Basic Auth** at the proxy.
* Per-app `capAdd`/`capDrop`, sysctls, supplemental groups and bind mounts are
  ordinary App settings. The docs do not say whether a non-admin project member
  is prevented from using them; I could not confirm either way.
* The panel container is given the Docker socket (`/var/run/docker.sock`).
* **Telemetry is on by default**: every hour the 2.36.0 bundle posts a
  `serverInfo` event with a machine ID, licence plan and counts of projects,
  services by type, custom domains and backup schedules to
  `https://t.easypanel.io/api`, unless `telemetryDisabled` is set. Licence
  validation talks to `portal.easypanel.io`. (Read from `backend.js` in the
  published image, 2026-09-30.)

### Preview environments and branches

* None. No per-PR or per-branch environments.

### Templates and catalogue

* **845 templates** on the site and in `easypanel-io/templates` (845 template
  directories, last commit 2026-09-24). Each is a `meta.yaml` (description,
  links, JSON-schema form, per-template changelog, instructions) plus an
  `index.ts` whose `generate()` returns services.
* Growth is fast and curated by the vendor: +34 (2.28.0), +121 and "updated 400
  existing templates" (2.31.0), +17 (2.32.0), +48 (2.34.0), plus "refreshed
  version pins" (2.36.0).
* Of the 845: **390** carry post-install `instructions`; **392** generate random
  passwords with `randomPassword()`; **299** create a managed database service;
  **49** still reference a `:latest` image somewhere (my count of the repository,
  2026-09-30). The PR checklist asks for "static", "specific versions (2.1.7,
  instead of 2.1, or 2)" and official images.
* 559 of 845 templates have a changelog entry dated 2026; 14 in September 2026.
* The flagship **WordPress template still defaults to `wordpress:latest`** and
  its last template change is dated 2022-07-12 (the WordPress *service* type,
  above, is the maintained path).
* "Create from JSON/schema" lets a user test or run their own template.

**What Easypanel does about running other people's apps.** Very little after
install. A template is a generator: it emits ordinary services and is then
forgotten. There is no record of which template produced a service, no check
for a newer image, no pre-update backup, no rollback and no volume restore. To
upgrade, the user edits the image tag and redeploys — which is exactly how
issue #114 happened: Chatwoot 4.0.1 → 4.2.0 stopped at `db:migrate`, the
service became unreachable, and the thread has no answer. The one exception is
WordPress, which Easypanel treats as a product of its own with a guarded core
update. The safety Easypanel does add is at install time: pinned versions in
most templates, generated secrets, instructions, and managed database services.

### CLI, API, IaC, integrations

* Public OpenAPI-documented API (oRPC, 372 operations in 2.36.0), bearer API
  keys per user; the old internal tRPC API "may change without notice".
* **CLI** (`easypanel`/`ep`, 2.33.0): commands are fetched from the connected
  server, keys stored in the OS keychain, destructive commands need `--yes`,
  secrets redacted unless `--show-secrets`.
* **Remote MCP server** (2.33.0) at `/api/mcp`, Streamable HTTP, with **four
  tools split by risk**: `search_procedures`, `execute_query`,
  `execute_mutation`, `execute_destructive`. The key can be put in the URL path
  (`/api/mcp/<API_KEY>`) for clients that cannot send headers.
* WHMCS plugin for hosting resellers; marketplace images on DigitalOcean, AWS,
  Vultr, Linode, Hostinger and others. No Terraform provider.

### Notifications

* Channels: Discord, Slack, Telegram, email and a generic webhook.
* Events: app deployed, database backup, Docker cleanup, **disk load above a
  threshold** (default 80 %, checked on a cron), Easypanel update available.
  No "deploy failed", "app down" or "certificate failed" events.

### Multi-server and networking

* Swarm workers joined with a generated command; Business-only, alpha.
  Cloudflare Tunnel for servers without a public address.

### Team and collaboration

* Multiple users and project-level access on Growth and above; white-labelling
  (logos, custom code injection, error/maintenance pages) on Business; service
  notes. Support is Discord for standard plans and direct on Business, though
  the Cloudron comparison page says "support from the Easypanel team" on every
  paid plan — the two pages disagree.

### Developer experience and onboarding

* One-line installer (`curl -sSL https://get.easypanel.io | sh`), marketplace
  images, ten framework quickstarts, "Dockerizer" (open source, 286 stars) to
  generate Dockerfiles, a demo instance, English and Brazilian Portuguese site.
* The UI is the most-praised thing about the product (below).

## What users love

* **The interface.** "Easypanel has been pretty great for me so far, a couple
  issues with ports but otherwise really nice UI and features … Coolify just
  isn't a good fit yet until they upgrade the UX" (HN, 2025-07-13). A 2026
  comparison: "CloudPanel and EasyPanel stand out for their polished
  interfaces", and "Best for teams: EasyPanel (multi-user access control,
  business features)" (bitdoze.com, best self-hosted panels).
* **Templates you can click.** "They have a bunch of templates for open source
  projects, so u can click and deploy them easily" (HN, 2025-04-05). 845 is the
  largest curated catalogue in this category; Easypanel's own comparison says
  Dokploy's public repo has 532 blueprints.
* **Stability relative to Coolify/Dokploy.** One HN user moved to Easypanel
  after a Dokploy outage (HN, 2025-07-13). The vendor's testimonials say the
  same ("most easy, stable and evolving product"), but they are selected by the
  vendor and are cited only as the claim it makes.
* **Predictable pricing** for small users: a real free plan and $10.90/server
  (bitdoze.com review, updated 2026-07-10).

## What users complain about

* **Closed source and licence-gated features.** Easypanel's own comparison
  pages concede "Dokploy wins for open source" and "Portainer wins for open
  source"; multi-user, access control, backups-on-schedule, notifications and
  clustering are paid; the free plan stops at 3 projects ("Only 3 projects
  compared to competitors offering unlimited", bitdoze.com).
* **Multi-server is thin.** "Multi-server support is 'coming soon'" (bitdoze.com)
  — it now exists as Business-only alpha on Swarm with a single manager.
* **Resource floor.** 2 GB RAM and 2 cores minimum (bitdoze.com; install docs
  say 2 GB).
* **Upgrading apps is on the user.** Issue #114 (Chatwoot migration failure
  after a manual tag bump, 2025-08-27, unanswered). There is no rollback to
  fall back to.
* **Panel robustness.** Issue #118 (2026-07-31): a second deployment of the
  same service kills the first, the `AbortError` is unhandled, the backend exits
  with code 1 and Swarm restarts it; the API had already answered 200. Issue
  #113: fresh installs on Ubuntu 22.04/24.04 bind to 127.0.0.1.
* **Coolify offers more for free** ("Coolify has caught up and offers more for
  free", bitdoze.com, 2026-07-10).

The public issue tracker (`easypanel-io/community`, 21 open issues) is small;
most support happens on Discord, which I could not read, so recurring
complaints may be under-counted here.

## Security record

**No CVEs or advisories found** for Easypanel (NVD keyword search "easypanel":
0 results; web search for Easypanel advisories: none; GitHub advisory search is
not possible because the panel's source is not public — all 2026-09-30). That
is weak evidence either way: a closed-source product with a fraction of
Coolify's install base gets little outside scrutiny. Structural points worth
knowing, all read from the 2.36.0 image or docs:

* The panel holds the Docker socket, so a panel compromise is host root.
* Bind mounts, added capabilities and sysctls are per-service settings; the
  documentation does not describe a restriction for non-admin users. Portainer
  shipped CVE-2026-33590 and CVE-2026-44850 for exactly this class.
* Default-on telemetry with a machine ID and resource counts (above).
* The MCP endpoint accepts the API key in the URL path, where proxies and
  history record it; Portainer's CVE-2026-44883 is the same shape (JWT in
  `?token=`).

## Against Skifity

Skifity rows marked "Written" follow `docs/checklist.md`: the code and unit
tests exist and have never run against a real cluster (ADR-0010).

| Capability | Easypanel | Skifity (evidence) | Verdict |
|---|---|---|---|
| Git/image/upload deploys | GitHub, Git+SSH, archive (7 formats), image, inline Dockerfile | GitHub/GitLab/Gitea, image, `.tar.gz` upload via `skifity up` (`internal/gitsrc`, `internal/upload`, `internal/cli/up.go`); Written | parity (Easypanel takes more archive formats) |
| Builders | Dockerfile, Buildpacks, Nixpacks, Railpack, remote builder | Railpack default, Nixpacks, Dockerfile, build Job in-cluster (`internal/builder`); Nixpacks never run | parity |
| Compose stacks | Compose service from inline/Git | No Compose; multi-service templates only (`internal/templates/catalogue/README.md`) | behind |
| Rollback | None | Rollback restores image *and* settings (`internal/api/api.go` `/rollback/{deploymentID}`, `internal/deploy`); Written | Skifity ahead |
| Config change without rebuild | Env changes redeploy; Compose rebuilds on deploy | Build fingerprint, ADR-0007 | Skifity ahead |
| TLS, domains | LE, wildcard, custom certs, middlewares, redirects | cert-manager on first domain, sslip.io free address (ADR-0015), wildcard setting, DNS token (`internal/settings/settings.go`); Written | parity |
| Maintenance page | Yes, per service | No (`grep -ri maintenance internal/kube internal/api`: nothing) | absent in Skifity |
| Basic auth in front of an app | Yes, per service | Traefik basicAuth per app, added in `7865bc8` (`internal/kube/password.go`, `internal/api/password_handlers.go`) | parity |
| Cloudflare Tunnel | Yes | Yes, token setting, manifests (`docs/adding-servers.md`, `docs/progress.md` open issues); never reached Cloudflare | parity |
| Managed databases | Postgres, MySQL, MariaDB, Mongo, Redis + admin UIs | PostgreSQL via CloudNativePG, MariaDB, Redis (`internal/dbsvc/manifests.go`); no Mongo, no admin UI; Written | behind |
| DB backup + restore | Scheduled dumps, retention, in-place restore | Scheduled dumps to S3, keep-last-N, restore with typed confirmation and engine/version refusal (`internal/backup`, `docs/backups.md`); never run | parity |
| Volume backup + restore | rclone mirror, no retention, no restore | tar per run, schedule, keep-last-N, **restore** that scales to zero and back (`internal/backup/volume.go`, `volumerestore.go`); never run | Skifity ahead |
| Backup destinations | S3, SFTP, FTP, Dropbox, Drive, local | S3-compatible only, presigned URLs so jobs never see the key (`docs/backups.md`) | behind on breadth, ahead on credential handling |
| Scaling | Replicas field; Swarm workers (Business, alpha) | HPA, KEDA scale-to-zero, readiness checker (`internal/kube/scaletozero.go`, `/scaling/readiness`); Written | Skifity ahead |
| Multi-server | Swarm, single manager, paid | k3s join over SSH in seven steps, promote to control plane (`internal/provision`, `/servers/{id}/promote`); Written | Skifity ahead |
| Logs | Live, filters; Loki search (paid) | Live SSE logs, previous container (`/apps/{id}/logs`); no search/retention | behind |
| Metrics | Prometheus per service + system, history (paid) | Live per-instance usage and node summary only; no history, no charts (`internal/api/servers_handlers.go`; no chart component in `web/src`) | behind |
| Disk alerts | Disk-load threshold notification | None (`internal/notify/notify.go` has 7 events, no disk) | absent in Skifity |
| App-down alerts | None | `app.unhealthy` when no instance is ready (`internal/watch/watch.go`) | Skifity ahead |
| 2FA, sign-in | TOTP | Argon2id, lockout, TOTP, recovery keys (`internal/auth`); Works | parity |
| SSO | None | OIDC with PKCE, nonce, state, domain allowlist (`internal/auth/oidc.go`, `internal/api/sso_handlers.go`); Works | Skifity ahead |
| Roles | admin flag + per-project access (paid) | owner/admin/member per team, no per-project scope (`internal/store/models.go`) | parity (different shape) |
| Audit | Actions history | Audit log per team, 90-day retention, Activity page (`internal/api/api.go` `audit`, `web/src/pages/activity.tsx`) | Skifity ahead |
| Firewall per app | None | IP/country/ASN rules at the edge (`internal/edgerules`, `internal/guard`); never through a live Traefik | Skifity ahead |
| Tenant isolation | Docker networks per project | Namespace per environment, default-deny NetworkPolicy, quota, `restricted` PSA (`internal/kube/namespace.go`, `podsecurity.go`); Written | Skifity ahead |
| Telemetry | On by default, hourly counts | None; "does not contact any server" (`internal/api/integrations_handlers.go:454`, `docs/faq.md`) | Skifity ahead |
| Preview environments | None | Per-PR previews created and torn down by webhook (`internal/api/webhook_handlers.go` `deployPreview`); Written | Skifity ahead |
| Catalogue size | 845 | 282 (`internal/templates/catalogue/*.yaml`) | behind |
| Version pinning | 49/845 reference `latest` | Test refuses floating tags; every image verified to exist (`internal/templates/templates.go`) | Skifity ahead |
| Post-install guidance | 390/845 have instructions | 49/282 have `notes` | behind |
| Generated secrets | 392/845 | 5/282 use `generate: true` | behind |
| Template updates after install | None (manual tag edit) | None; "a template does not update itself" (`docs/templates.md`) | parity (both absent) |
| App-specific management | WordPress service with core update, WP-CLI | None | behind (see "not copy") |
| API | 372 ops, OpenAPI | REST API, same as CLI and MCP (`internal/api/api.go`, `llms.txt`) | parity |
| CLI | Server-driven command tree, keychain | `skifity` with `--json` everywhere (`internal/cli`); Works | parity |
| MCP | Remote, 4 tools split by risk | Local stdio, 15 task tools, errors with cause/impact/fix (`internal/mcpserver/server.go`); no read-only/destructive annotations | parity; Easypanel ahead on risk labelling |
| Export / lock-in | No documented export | `skifity export`: JSON + `kubectl apply`-able objects (`internal/api/export_handlers.go`); Works | Skifity ahead |
| Notifications | Discord, Slack, Telegram, email, webhook | Telegram, Discord, webhook, email + plugin-provided kinds (`internal/notify`, ADR-0021) | parity (Slack via webhook/plugin) |
| Team features | Users, project access, white-label | Teams, invitations, roles (`internal/api/invitation_handlers.go`, `teams_handlers.go`) | parity; no white-label in Skifity |
| Onboarding | One-liner, marketplaces, quickstarts | One-liner (no release tagged yet), in-panel docs, five languages (`docs/checklist.md`, `web/src/locales`) | behind today (no release), ahead on docs/i18n |
| Panel footprint | Node + SQLite, 2 GB minimum | 35 MiB idle panel, ~550 MiB with k3s (`docs/performance.md`) | Skifity ahead |

## Gaps worth closing in Skifity

Only things Skifity does not have. Ordered by priority.

**P1 — Post-install instructions and generated secrets across the catalogue.**
*What:* every template that has a default admin password, a first-user-becomes-
admin rule or a setup wizard says so, and every secret the app needs is an input
with `generate: true`. *Evidence:* Easypanel ships instructions on 390/845 and
generated passwords on 392/845; Skifity has `notes` on 49/282 and `generate` on
5/282 (`grep -l` over `internal/templates/catalogue`). An app that comes up with
`admin/admin` or an empty signup page is the most common way a one-click app
becomes someone else's. *Fit:* data work in `internal/templates/catalogue/*.yaml`
plus a test in `internal/templates/templates_test.go` that flags templates whose
upstream image documents a default credential variable without an input. *Size:*
M (data). *Without a cluster:* yes — tests and the install dialog.

**P1 — Disk-space alert.** *What:* a `server.disk_low` notification event when a
node's filesystem crosses a threshold or reports `DiskPressure`. *Evidence:*
Easypanel's `diskLoad` event (threshold, cron) and Cloudron's "Low disk space"
notification; Skifity's registry sweep (Phase 11) exists because a full disk
stops every pod on a node, yet nothing warns before it happens (`internal/notify`
has seven events, none for disk). *Fit:* `internal/watch` reads node conditions
already fetched by `cluster.Summary`; new event in `internal/notify/notify.go`,
new locale keys, a settings field for the threshold. *Size:* S. *Without a
cluster:* yes — fake clientset node status, dispatcher tests.

**P1 — Project-level access.** *What:* a member can be limited to some projects
rather than the whole team. *Evidence:* Easypanel sells exactly this as its
Growth plan ("Multiple users, Access control"); Cloudron has per-app operators;
Portainer has namespace-scoped roles. Skifity's roles are team-wide
(`internal/store/models.go`). An agency hosting several clients' apps needs it.
*Fit:* a `project_members` table and a check inside `authorizeProject`/
`authorizeApp` in `internal/api`, so the existing route walk test covers it.
*Size:* M. *Without a cluster:* yes — the route walk in `internal/api` tests
and the Playwright test.

**P2 — Maintenance mode.** *What:* a switch that serves a maintenance page for an
app's domains while its instances keep running, for migrations. *Evidence:*
Easypanel has it on every service type; Easypanel's own backup docs tell users
to enable it before restores. *Fit:* one more Traefik middleware in
`internal/kube/manifests.go` next to the redirect and guard middlewares, a static
page served by the panel or the guard. *Size:* S. *Without a cluster:* manifests
and golden tests yes; seeing Traefik serve it needs a cluster.

**P2 — Logs and metrics with history.** *What:* keep a window of CPU/memory per
app and per server and chart it; search recent logs. *Evidence:* Easypanel's
2.29.0 "Advanced monitoring" (Prometheus + Loki, opt-in, paid); Cloudron's graphs;
Skifity has live values only and no chart component in `web/src`. *Fit:* sample
the metrics API from the minute tick in `internal/serverapp` into a bounded
SQLite ring table (the panel's own retention pattern from Phase 11.2), chart
with a shadcn chart; log search would be an optional component like
cert-manager, not core. *Size:* M (metrics), L (log search). *Without a
cluster:* storage, API and UI yes; real samples need one.

**P2 — More backup destinations.** *What:* SFTP (with a key, which Easypanel does
not support) and a local path for a second copy. *Evidence:* Easypanel offers
S3, SFTP, FTP, Dropbox, Google Drive and local; Skifity is S3-only. *Fit:* the
presigned-URL design in `internal/backup/storage.go` does not extend to SFTP
without handing the Job a credential, so this is a design question first; a
plugin `provides` kind (ADR-0021) for "backup destination" is the natural seam.
*Size:* M. *Without a cluster:* the destination code yes; the Job no.

**P2 — Risk labels on MCP tools.** *What:* mark each MCP tool read-only or
destructive (the MCP `readOnlyHint`/`destructiveHint` annotations) so a client
can ask before `rollback_app` or `scale_app`. *Evidence:* Easypanel splits
execution into query/mutation/destructive tools for exactly this reason;
`internal/mcpserver/server.go` has no annotations. *Fit:* one field per tool
definition. *Size:* S. *Without a cluster:* yes.

**P2 — More archive formats for `skifity up` / upload.** *What:* accept zip.
*Evidence:* Easypanel accepts seven formats since 2.32.0; people download zips
from AI builders and GitHub. *Fit:* `internal/upload` already enforces entry and
size limits on tar; zip needs the same limits. *Size:* S. *Without a cluster:*
yes.

## Things to deliberately not copy

* **Telemetry on by default.** Easypanel sends hourly resource counts with a
  machine ID unless the user finds the switch. Skifity's "never phones home" is
  a product promise and a README headline; keep it.
* **Volume "backup" as a mirror.** An `rclone sync` that deletes destination
  files when the source loses them is replication, not a backup, and shipping it
  without a restore button trains users to believe they are covered. Skifity's
  tar-per-run with restore is the right shape.
* **A per-app special service for the most popular app.** The WordPress service
  is a second product (PHP, Nginx, WP-CLI, core updates) inside the panel. It
  wins WordPress users; it also means every other app gets none of that care.
  Skifity's leverage is a generic update and backup path that works for all 282.
* **`latest` as a template default.** 49 of Easypanel's templates, including its
  WordPress template, reference `latest`. Skifity's refusal test is better.
* **API keys in URLs.** `/api/mcp/<API_KEY>` puts a full-access credential in
  proxy logs and client histories. Keep keys in headers only.
* **Letting members set capabilities, sysctls and host bind mounts.** Skifity's
  Pod Security levels deliberately never offer `privileged` or host paths
  (`internal/kube/podsecurity.go`); that is the class of hole Portainer patched
  twice in 2026.
* **Clustering as a paid alpha on Swarm.** Swarm is the thing Skifity's
  architecture exists to avoid; the lesson is that multi-server must be the
  default path, not an upsell.

## Sources

All read 2026-09-30.

* https://easypanel.io/pricing — plans, per-server licence, cancellation, support FAQ, "145,000+ self-hosters"
* https://easypanel.io/changelog — 2.27.0 to 2.36.0, dates, template counts, CLI/MCP/public API in 2.33.0
* https://easypanel.io/docs and https://easypanel.io/docs/maintenance — install, 2 GB, Swarm, updates
* https://easypanel.io/docs/services/app — sources, builders, deploy settings, capabilities, mounts, basic auth, maintenance
* https://easypanel.io/docs/services/compose — Compose service
* https://easypanel.io/docs/services/wordpress — WordPress service and core update
* https://easypanel.io/docs/backups, /docs/backups/database, /docs/backups/volumes, /docs/storage-providers — backups
* https://easypanel.io/docs/guides/notifications — channels and events
* https://easypanel.io/docs/cli and https://easypanel.io/docs/mcp — CLI and MCP
* https://easypanel.io/docs/api/users/createUser, /users/updateUser, /projects/updateAccess — user model
* https://easypanel.io/sitemap.xml — API operation groups (users, cluster, metrics, legacy monitoring, etc.)
* https://easypanel.io/blog/multi-server-support-beta-release — Swarm, single manager, licence enforcement
* https://easypanel.io/templates and https://easypanel.io/templates/wordpress — 845 templates, WordPress defaults
* https://easypanel.io/alternatives/cloudron — vendor comparison (verified by the vendor 2026-09-29)
* https://github.com/easypanel-io/templates — cloned at commit dated 2026-09-24; counts computed locally
* GitHub search API for `org:easypanel-io` (stars/forks of templates, community, dockerizer, compose)
* https://hub.docker.com/v2/repositories/easypanel/easypanel/ — 7,489,914 pulls
* Docker Hub registry manifest and config for `easypanel/easypanel:latest` (2.36.0, created 2026-09-24) and its `/app` layer — `backend.js` read for the datastore, telemetry, notification schema and API operations
* https://github.com/easypanel-io/community/issues, /issues/114, /issues/118 — open issues
* https://news.ycombinator.com/item?id=44550078, item?id=44548998, item?id=43594523 — user comments (via hn.algolia.com)
* https://www.bitdoze.com/easypanel-modern-server-control-panel/ (updated 2026-07-10) and https://www.bitdoze.com/best-self-hosted-panels/
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=easypanel — 0 results
* https://nvd.nist.gov/vuln/detail/CVE-2026-33590, CVE-2026-44850, CVE-2026-44883 (Portainer, cited for the vulnerability classes)
