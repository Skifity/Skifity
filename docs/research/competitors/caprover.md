# CapRover

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

CapRover ("Scalable PaaS (automated Docker+nginx) - aka Heroku on Steroids") is
the oldest of the dashboard-driven Docker panels still shipping: a web panel and
a CLI that turn one Ubuntu server, or a Docker Swarm of several, into a
Heroku-style host for solo developers and small teams who want a GUI and a
catalogue of one-click apps without learning Docker. Architecture: one
`docker run` starts **Captain**, a Node.js/TypeScript service that initialises
**Docker Swarm**, runs itself as the Swarm service `captain-captain` with the
host's `/var/run/docker.sock` mounted, and drives an **nginx** reverse proxy,
**Certbot** for Let's Encrypt, an optional private Docker registry (required
once there is more than one node), optional **Netdata** server monitoring and,
since 1.14 (June 2025), **GoAccess** traffic statistics. Its own state is a flat
JSON file, `/captain/data/config-captain.json`; a "backup" is a tar of
`/captain/data`. Requirements: Ubuntu 24.04 with Docker 25+, 1 GB of RAM, AMD64
or ARM64 (ARMv7 images are being dropped in the next release), ports 80, 443/tcp,
443/udp and 3000 open. **Licence:** Apache 2.0 with a custom appendix added in
July 2023 ("the paid features of CapRover (as a service) cannot be modified", no
redistributing another paid version), plus a Terms and Conditions file that the
installer makes you accept (`ACCEPTED_TERMS=true`) and that discloses anonymous
usage analytics since v1.11 (opt out with `CAPROVER_DISABLE_ANALYTICS=true`).
**Pricing:** the core is free; **CapRover Pro** is a subscription that adds
TOTP two-factor authentication, "proactive login monitoring", build
success/failure alerts, "up to 5 instances per license" and 24-hour email
support. The price is loaded at runtime from a Paddle checkout and could not be
confirmed from any page or article (read 2026-09-30). **Maturity:** repository
created 2017-10-25; releases 1.13.0 (2024-10-19), 1.14.0 (2025-06-07), 1.14.1
(2025-11-11), 1.14.2 (2026-05-14), 1.15.0 (2026-08-04) and four patch releases
to 1.15.4 (2026-08-30) — two or three feature releases a year. **Adoption:**
15,177 GitHub stars, 1,004 forks, 178 open issues and pull requests
(GitHub API, 2026-09-30); 228,673,959 Docker Hub pulls of `caprover/caprover`
(Docker Hub API, 2026-09-30); 360 one-click apps in the official catalogue
(`oneclickapps.caprover.com/v4/list`, counted 2026-09-30).

## Feature inventory

### Deploy sources and builds

* **`captain-definition`** file in the repository (`schemaVersion: 2`) pointing
  at a `dockerfilePath`, inline `dockerfileLines`, or an `imageName`. There is
  no language detection of the Railpack/Nixpacks kind; the old per-language
  `templateId` builds are a legacy path with a migration guide.
* **Builds happen on the server** (the leader node). BuildKit became the
  default in 1.15.0 (2026-08-04); 1.15.3 had to restore build output that the
  switch had hidden.
* **Four ways in:** `caprover deploy` from a Git checkout (it tars the branch and
  streams upload, build and deploy progress), a tarball uploaded in the
  dashboard ("typically used for testing purposes only"), a prebuilt image
  (`caprover deploy --imageName`), and a webhook per app for GitHub, GitLab or
  Bitbucket pushes. App tokens (1.10) let CI deploy without the root password;
  the docs have GitHub and GitLab CI recipes.
* **Docker Compose, simplified** (1.15.0, "still experimental"): paste a Compose
  file and it becomes several apps. Only `image`, `environment`, `ports`,
  `volumes`, `depends_on`, `hostname`, `cap_add` and `command` are read; `build`,
  `networks`, `secrets`, `configs`, `deploy` and `restart` are ignored.
* **Escape hatches:** a *pre-deploy script* — JavaScript executed inside the
  CapRover process with access to the Docker service update object ("literally
  do anything") — and a *Service Update Override*, a raw Docker Engine API
  service spec merged over what CapRover generates.
* `CAPROVER_GIT_COMMIT_SHA` is available at build and run time.
* **Rollback** to a previously deployed version from the Deployment tab.

### Domains, TLS and routing

* A root wildcard domain (`*.apps.example.com`) is required: every app gets
  `<app>.<root>`. Custom domains per app on top.
* Let's Encrypt through Certbot over HTTP-01, one click per domain. **DNS-01 and
  wildcard certificates** since 1.12.0 by building a custom Certbot image and
  hand-editing `/captain/data/config-override.json`.
* Per app: *Force HTTPS*, *Websocket support*, **HTTP Basic Auth**, "Redirect all
  domains to" one canonical domain (1.11, "experimental"; redirects kept the
  path from 1.12), container HTTP port, and a **per-app nginx config template**
  that can be edited in the dashboard; the captain-wide nginx template can be
  overridden too.
* HTTP/3 (UDP 443) since 1.13.3; gzip on by default since 1.15.0.
* **Port mappings** expose any container port on the host (typically a database
  for external access); "custom ports" were extended in 1.14.0.

### Databases and services

* No managed databases. MySQL, MariaDB, PostgreSQL, MongoDB, Redis and the rest
  are **one-click apps**: ordinary containers with a persistent directory,
  reached from other apps at `srv-captain--<name>` (or `<name>` for apps created
  on 1.15+).
* One-click variables such as the database password are applied **at install
  only**; changing the environment variable afterwards does not change the
  password inside the database, as the one-click docs warn.
* No credential injection, no linking, no rotation, no replicas.

### Storage and backups

* *Persistent apps* get persistent directories (named volumes or host paths)
  and can be pinned to a node by Node ID. 1.15.0 warns when a volume label is
  already used by another app.
* **Backup** (Settings → Backup) downloads a tar of `/captain/data`: app
  settings, configuration and certificates. It **excludes container images and
  every persistent directory**, so no database or upload is in it. There is no
  schedule and no S3 target; the docs suggest scripting the API and using
  `mysqldump`/`mongodump` or a volume-backup container yourself.
* Restoring onto a new server means repointing `*.root` DNS and, for a cluster,
  hand-editing node IPs and uploading an SSH key. The unreleased edge build fixes
  "backups created with an incorrect nested data directory that prevented them
  from being restored".
* Automated disk clean-up of old images (1.12.0).

### Scaling and high availability

* An **instance count** per app, set by hand. No autoscaling.
* **Cluster** = Docker Swarm: join nodes from the dashboard or with
  `docker swarm join`. A registry becomes mandatory, and existing apps have to be
  redeployed after it is set up.
* "Only apps without 'Persistent Data' can be scaled across nodes. Apps that
  have 'Persistent Data' enabled will only run on 1 node."
* The leader node runs Captain, nginx and Certbot; third-party reviews call it a
  single point of failure even in cluster mode.
* **Zero downtime:** apps with no volumes are updated `start-first`, and Swarm
  waits for a Docker `HEALTHCHECK` before routing to the new container; apps
  with volumes are updated `stop-first`, "which results in some amount of
  downtime", deliberately, to avoid two containers writing one directory.

### Observability (logs, metrics, alerts, uptime)

* App logs in the dashboard, searchable since 1.12.0; build logs streamed to the
  CLI and the dashboard.
* **Netdata** for server CPU, memory, disk and network (optional, one click).
* **GoAccess** (1.14.0) for per-app traffic statistics from nginx logs, with an
  option to stop logging client IPs (1.15.0).
* No alerting in the free core beyond whatever Netdata is configured to send;
  build success/failure alerts are a Pro feature.

### Security, auth, roles, SSO, audit

* **One password**, no user names, no roles, no SSO. The first password is
  `captain42` until it is changed, and the dashboard listens on port 3000 until
  a domain is attached. Issue #2172 (users and roles, October 2024) is open and
  unanswered.
* **TOTP two-factor authentication and login alerts are paid** (Pro), and Pro
  authenticates through the external `pro.caprover.com` service; the Pro
  troubleshooting page explains how to downgrade by editing the config file when
  that service is unavailable.
* App tokens scope a webhook or CI deploy to one app.
* No audit log.
* Captain has the Docker socket, so a compromised dashboard is root on every
  node.

### Preview environments and branches

* None. One app follows one branch.

### Templates and catalogue

* **360 one-click apps** (2026-09-30), **150 of them multi-service**, in the
  community repository `caprover/one-click-apps` (637 stars, 284 open issues and
  PRs). The format is `captainVersion: 4`, Compose-like YAML with a
  `caproverExtra` block and `$$cap_*` variables, including generated secrets
  (`$$cap_gen_random_hex(10)`).
* **The version is a variable**: most templates expose `$$cap_<app>_version`
  with a pinned default, so the installer can pick another tag. Resolving every
  default, **13 of 360** templates end up on `latest`, `stable`, `main`,
  `master` or no tag at all (counted 2026-09-30 from the v4 API).
* Third-party template repositories can be added in the dashboard.
* Installed apps never update themselves; upgrading is changing the image or
  using the app's own updater.

### CLI, API, IaC, integrations

* `caprover` CLI (npm): `serversetup`, `login` (several servers at once),
  `list`, `logout`, `deploy`, and `api` for any dashboard endpoint.
* The HTTP API is the one the dashboard uses and is not documented as a public
  contract.
* No declarative configuration beyond `captain-definition`; nothing like a
  project file that describes an app's settings.
* An unofficial CapRover MCP server exists (`ivan-saorin/caprover-mcp`).

### Notifications

* Pro only: build success/failure and login alerts. Nothing in the free core.

### Multi-server and networking

* Swarm overlay network; the firewall page lists 2377/tcp, 7946/tcp+udp and
  4789/udp between nodes and warns that exposing VXLAN publicly is dangerous and
  that Docker-published ports bypass UFW.
* An existing Swarm can be adopted (1.11.0).

### Team and collaboration

* **Projects** (1.13.0) and **tags** (1.11.0) group apps. Everyone who can sign
  in shares the one password.

### Developer experience and onboarding

* One `docker run`, then `caprover serversetup` asks for the IP, root domain,
  new password and email. Translations and themes since 1.13.0.
* Installation refuses known-incompatible hosts such as Proxmox LXC with a
  descriptive error since 1.15.0.

## What users love

* **Simple and cheap on one small VPS.** "You can run CapRover on a cheap VPS
  and avoid per-service PaaS pricing" (Sliplane, 2026). Ownkube's 2026
  comparison calls it "friendly to non-DevOps founders" with an "active app
  store with prebuilt deployments".
* **Mature and boring.** BuildMVPFast (2026-06-12): "a mature, stable codebase",
  "over 100 million Docker Hub pulls" (the Docker Hub API said 228.7 million on
  2026-09-30). selfhostable.dev (2026-01-28): "Stable and battle-tested — It's
  been around long enough to have real users", with a "lower resource footprint
  (~200 MB) than Coolify" (a third-party figure, not measured here).
* **The only one of the classic three with a built-in multi-server story.**
  Kanopy Labs (2026-04-07) recommends it when "you need to scale horizontally
  across multiple servers in the near term" — while adding that "most startups
  do not need clustering".
* **The catalogue.** Hundreds of one-click apps, with a version you can choose.

## What users complain about

* **Development pace and a dated UI.** "The UI feels dated and is occasionally
  buggy"; "the release cadence is slow (a handful of releases per year)"
  (Deploynix on dev.to, 2026-06-21). "Community members have reported
  compatibility issues with Docker v29+ and no major feature releases landed in
  early 2026 ... CapRover is functional but not evolving" (temps.sh, 2026-03-29,
  updated 2026-08-07 — before 1.15.0 shipped). "The update cadence has slowed
  compared to Coolify" (BuildMVPFast, 2026-06-12). Sliplane (2026) adds that
  "Coolify, Sliplane, Railway, Render, and others have moved the ecosystem
  forward".
* **Coupled to the host's Docker.** Docker 29.0.0 dropped API 1.43, and every
  CapRover before 1.14.1 stopped starting: "client version 1.43 is too old.
  Minimum supported API version is 1.44" (issue #2351, 2025-11-11). The reverse
  happened too: 1.14.0 needed a newer Docker API than some hosts had, and a
  user following AI advice ended with "This node is already part of a swarm"
  (discussion #2342, 2025-09-28), fixed by the maintainer hand-writing a
  `docker service create` command.
* **Swarm fails badly.** A full disk corrupted the Swarm raft log — "irreparable
  WAL error: wal: max entry size limit exceeded" — and the node could not be
  recovered even from a snapshot (issue #2281, 2025-03-19, unresolved).
  Ownkube (2026-05-08): "Docker Swarm scaling, fragile beyond a few nodes" with
  "quiet and painful" failure modes, and "Swarm has been in maintenance mode for
  years".
* **Stateful apps do not scale.** An HN user in January 2025: "I once tried
  scaling with CapRover, and it worked, but the main challenge was storage
  management ... its storage is tied to a specific VPS node."
* **One shared password.** "Only a single password field is required on the
  admin console" (HN, 2022) — still true in 2026, and two-factor is paid.
* **"Backup" is not a backup of data.** The documented backup leaves out every
  volume and image.
* **The licence change.** Issue #2035 ("CapRover is no longer open source",
  2024-03-28) was closed as not planned; the HN thread called the maintainer's
  reading of "open source" convenient.
* **Recurring HTTP-setting bugs.** "Redirect all domains to" redirects to the
  `http://` URL (issue #1858); enabling Force HTTPS or websocket support broke
  deployments (issue #950); legacy custom nginx templates broke on 1.15.0
  (fixed in 1.15.2).

## Security record

* **NVD:** a keyword search for "caprover" returns **0 CVEs** (queried
  2026-09-30).
* **GitHub Security Advisories:** "There aren't any published security
  advisories" on `caprover/caprover` (read 2026-09-30). The policy: "Only the
  last version receives security patches"; reports go to security at caprover
  dot com.
* **Unexplained fixes:** 1.13.3 (2024-12-01) shipped "Critical security patches"
  with no advisory or description. 1.14.2 (2026-05-14) was an nginx hotfix
  published "out of an abundance of caution", CapRover's own config not using
  the vulnerable `rewrite` directives.
* **Exposure by design:** the default `captain42` password is detected by
  vulnerability scanners (pentest-tools lists "Caprover - Default Login", High);
  the dashboard is one password with no second factor unless you pay; Captain
  holds the Docker socket; the pre-deploy script runs arbitrary JavaScript
  inside the control-plane process.
* As with Skifity, no CVEs is not evidence of safety: CapRover has had very
  little published security research, and silent "critical security patches"
  make the history impossible to audit.

## Against Skifity

Skifity statuses follow `docs/checklist.md`: almost everything cluster-facing is
**Written, never run** (ADR-0010).

| Capability | CapRover | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy sources and builds | captain-definition + Dockerfile on the server; CLI tarball, dashboard upload, image, webhooks; simplified Compose → several apps | Git (GitHub, GitLab, Gitea) with webhooks (`internal/gitsrc/webhook.go`), image, folder upload from CLI or browser (`internal/cli/up.go`, `internal/upload/upload.go`); Railpack/Nixpacks/Dockerfile/static detection (`internal/builder/detect.go`); a Compose file offers **one service at a time** (`internal/api/apps_handlers.go:257-266`). Written, never run | Skifity ahead on detection and upload; **behind on whole-Compose import** |
| Build/runtime separation | Any env change updates the Swarm service | Build fingerprint: a runtime variable never rebuilds (ADR-0007, `internal/api/apps_handlers.go:800-807`) | Skifity ahead |
| Domains, TLS and routing | Wildcard root required; Let's Encrypt HTTP-01; DNS-01 by hand-editing; Force HTTPS; basic auth; canonical redirect; per-app nginx templates; port mappings; HTTP/3 | sslip.io address with no DNS (`internal/cluster/cluster.go:228-276`); cert-manager HTTP-01 only (`internal/cluster/components.go:160-166`); HTTPS redirect per namespace (`internal/kube/namespace.go:351`); path on a domain (`internal/store/models.go` Domain.Path); **no basic auth, no redirects, no custom certificate, no DNS-01**; NodePort/LoadBalancer forbidden by quota (`internal/kube/namespace.go:113-116`) | Parity on the basics; **behind on basic auth, redirects, TCP ports**; ahead on zero-DNS first URL |
| Databases and services | One-click containers, no linking, no rotation, credentials fixed at install | Managed PostgreSQL (CloudNativePG), MySQL, Redis; generated sealed credentials injected on link (`internal/dbsvc/manifests.go`, `internal/api/api.go:316-327`). Written, never run | Skifity ahead |
| Storage and backups | Persistent dirs; config-only backup download, no schedule, no S3, no data | Volumes as PVCs with Recreate strategy; scheduled S3 backups and restore for databases and volumes (`internal/backup/`, `internal/api/api.go:306-327`). Panel's own DB: manual `skifity admin backup-db` only (`internal/cli/admin.go:207`, `docs/backups.md` "What is not backed up"). Written, never run | Skifity ahead on data; **parity-to-behind on backing up the panel itself** |
| Scaling and high availability | Manual instance count; Swarm; stateful apps pinned to one node; start-first only without volumes; leader node is a SPOF | Replicas, HPA on CPU/memory (`internal/kube/manifests.go:362`), scale-to-zero via KEDA (`internal/kube/scaletozero.go`), readiness checker (`internal/deploy/scaling.go`), rescheduling from k3s; volume apps also Recreate (`internal/kube/manifests.go:176-186`). Written, never run | Skifity ahead (on paper) |
| Observability | Searchable app logs, Netdata, GoAccess traffic stats | Live logs over SSE incl. the previous container (`internal/api/api.go:287`), per-instance usage, panel metrics at `/api/metrics`, optional Prometheus/Grafana component (`internal/cluster/components.go:68`); **no per-app request statistics** | Parity; **behind on traffic stats** |
| Security, auth, roles, SSO, audit | One shared password; 2FA paid; no audit | Argon2id, TOTP free, OIDC SSO (`internal/auth/`), owner/admin/member roles (`internal/store/models.go:6-17`), audit log (`internal/api/api.go:213,516`), sealed secrets (`internal/crypto`). Works (Go tests, `make smoke`) | Skifity ahead |
| Preview environments | None | Per-PR preview environments, forks get no secrets, TTL (`internal/api/webhook_handlers.go:184-280`, `internal/settings/settings.go:230`). Written, never run | Skifity ahead |
| Templates and catalogue | 360 apps, 150 multi-service, version chosen at install, 13 on floating tags | 282 templates, 38 multi-service (`internal/templates/catalogue/`), every tag pinned and tested, **version fixed by the file** (`internal/templates/templates.go:28-35`) | **Behind on count and version choice**; ahead on the floating-tag guarantee |
| CLI, API, IaC | `caprover` CLI with generic `api`; undocumented API; no IaC | One API for panel, CLI and MCP (`internal/cli/commands.go:34-71`, `internal/mcpserver/server.go`), `--json` everywhere, `skifity.toml`, documented errors (`internal/errdoc/catalogue.go`), export to kubectl-ready YAML (`internal/cli/export.go`) | Skifity ahead |
| Notifications | Pro only | Telegram, Discord, webhook, email on seven events (`internal/notify/notify.go:41-47`) plus plugin-provided channels. Written, never run | Skifity ahead |
| Multi-server and networking | Swarm join; VXLAN overlay; registry required | k3s join over SSH with password used once (`internal/provision/`), encrypted WireGuard or plain VXLAN between servers (`internal/settings/settings.go:236-245`), in-cluster registry, Cloudflare tunnel (`internal/cluster/tunnel.go`). Written, never run | Skifity ahead (on paper) |
| Team and collaboration | Projects and tags; no users | Teams, invitations, roles, projects, environments, shared project variables (`internal/api/api.go:201-258`) | Skifity ahead |
| Developer experience and onboarding | One `docker run`; wildcard DNS needed first; dated UI; translations | One installer, sslip.io URL with no DNS, five languages enforced by `check:i18n`; **not released yet** (`docs/checklist.md`) | Skifity ahead in design; **behind in that CapRover can be installed today** |

## Gaps worth closing in Skifity

Only things Skifity does not already have. Sizes: S ≈ days, M ≈ a week or two,
L ≈ several weeks.

### P1 — Import a whole Compose file as several apps

* **What:** paste or detect a Compose file and create every service as an app in
  one environment, reachable by service name, with volumes and variables carried
  over and the unsupported keys named rather than dropped.
* **Evidence:** CapRover made this a headline of 1.15.0 (August 2026), even as an
  "experimental" subset; Compose is how most self-hosted software is published,
  and Skifity's own catalogue was converted from Compose files offline. Today
  Skifity refuses `source: compose` and offers one service at a time
  (`internal/api/apps_handlers.go:257-266`), so a five-service stack is five
  trips through the new-app form.
* **Fit:** `internal/builder/detect.go` already parses services into
  `ComposeService` and collects `ComposeWarnings`; `internal/api/template_install.go`
  already installs a multi-service template into one environment. A
  `POST /api/environments/{env}/compose` that turns parsed services into a
  transient template reuses both; the panel's new-app page gets "All services".
  Database services can be offered as managed databases the way `needs.go` does.
* **Size:** M. **Without a cluster:** yes for the parser, the API, the panel and
  a Playwright step; running the stack needs one.

### P1 — Expose a TCP or UDP port, and reach a managed database from outside

* **What:** a per-app "public port" for non-HTTP services (MQTT, SMTP, a game
  server, a database a BI tool connects to), with an allowlist.
* **Evidence:** CapRover has had port mappings for years and extended them in
  1.14.0 ("custom ports") and 1.13.3 (UDP for HTTP/3); Dokku has `ports:add` and
  `postgres:expose`; Kamal accessories publish ports. Skifity makes it
  impossible on purpose — the quota sets `services.nodeports` and
  `services.loadbalancers` to 0 (`internal/kube/namespace.go:113-116`) — and
  `internal/dbsvc` has no external-access path at all.
* **Fit:** a Traefik TCP/UDP entrypoint plus `IngressRouteTCP` (or k3s ServiceLB
  for a raw port) rendered in `internal/kube`, gated by the firewall rules that
  already exist for HTTP (`internal/edgerules`) so a public database is never an
  accident; a "Public port" card on the app's Domains tab and the database page.
* **Size:** M. **Without a cluster:** manifests and API yes; whether Traefik and
  ServiceLB actually open the port needs one.

### P1 — Back up the panel's own database off-site, on a schedule

* **What:** a scheduled copy of the panel's SQLite database to the backup bucket
  already configured for databases, with retention, and a restore procedure in
  the docs.
* **Evidence:** CapRover's dashboard has a Backup button for its own state;
  Skifity's only path is `skifity admin backup-db <path>` run by hand on the
  server (`internal/cli/admin.go:207`), and `docs/backups.md` says the panel's
  state "is not backed up". The database is one file on one node; losing it
  loses every app's settings while the apps keep running unmanaged.
* **Fit:** `VACUUM INTO` already produces a consistent copy
  (`internal/store/store.go:402-419`); the panel already holds the S3
  credentials (`internal/backup/storage.go`) and the minute tick
  (`internal/serverapp`). Secrets in the copy stay sealed, and the master key is
  deliberately **not** uploaded — the docs keep saying where it goes instead.
* **Size:** S. **Without a cluster:** yes, against a fake S3 server in a Go test.

### P1 — Choose a template's version at install time

* **What:** show the pinned tag as the default and let the installer pick
  another one, validated by the same floating-tag rule the catalogue tests use.
* **Evidence:** most CapRover templates expose `$$cap_<app>_version` with a
  pinned default. Skifity pins versions in the file
  (`internal/templates/templates.go:28-35`), so installing an older major for
  compatibility, or a newer patch released yesterday, means installing and then
  editing the image.
* **Fit:** an optional `version` input per service in the template schema,
  applied in `internal/api/template_install.go`; refusal messages from the same
  rule as `templates_test.go`.
* **Size:** S. **Without a cluster:** yes (unit tests and the Templates page).

### P2 — HTTP basic auth per app

* **What:** a username and password in front of an app, stored sealed.
* **Evidence:** a standard CapRover app setting; Dokku ships an official
  `http-auth` plugin and Dokku Pro 1.4 put it in the UI; it is the usual way to
  hide a staging site or an admin tool that has no login of its own.
* **Fit:** a Traefik `basicAuth` middleware rendered into the app's namespace
  next to the HTTPS redirect `internal/kube/namespace.go` already renders; the
  htpasswd Secret sealed like any variable; a switch on the Domains tab.
* **Size:** S. **Without a cluster:** manifests yes; Traefik honouring it needs
  one.

### P2 — Canonical-domain redirects

* **What:** mark one domain canonical and 301 the others (www ↔ apex, old
  domains) to it, keeping path and query, always to `https://`.
* **Evidence:** CapRover's "Redirect all domains to", whose long-standing bug of
  redirecting to `http://` (issue #1858) is the lesson; Dokku's official
  `redirect` plugin; kamal-proxy's `--canonical-host`.
* **Fit:** a `redirect_to` on `store.Domain` rendered as a Traefik
  `redirectRegex` middleware.
* **Size:** S. **Without a cluster:** manifests yes; behaviour needs one.

### P2 — Per-app request statistics

* **What:** requests, status codes and top paths per app, from the ingress.
* **Evidence:** CapRover added GoAccess as a headline of 1.14.0; Dokku Pro 1.4
  streams access and error logs; kamal-proxy exports Prometheus request metrics.
  Skifity reports what an instance uses, not what it serves.
* **Fit:** Traefik's Prometheus metrics (router/service labels map to app
  namespaces) read by `internal/cluster`, shown on the app page; no second
  analytics engine.
* **Size:** M. **Without a cluster:** the parsing and the panel yes; the numbers
  need one.

## Things to deliberately not copy

* **Docker Swarm.** CapRover's worst 2025 incidents are Swarm state: a raft log
  that a full disk made unrecoverable, and "already part of a swarm" loops after
  Docker upgrades. Skifity's bet on k3s is exactly the answer to this; keep it.
* **Tracking the host runtime's latest release.** Docker 29 broke every CapRover
  older than 1.14.1 overnight. Skifity has the same exposure through k3s's
  `stable` channel (see the Dokku file for the concrete fix).
* **Security behind a paywall.** Two-factor authentication and login alerts are
  CapRover Pro features. Skifity's TOTP is free and must stay free.
* **A licence appendix and an accepted EULA,** plus analytics that are on until
  you opt out. The goodwill cost is visible in issue #2035 and on HN; Skifity's
  "never phones home" is worth more than the data.
* **Arbitrary code in the control plane.** The pre-deploy script runs
  JavaScript inside Captain, and Service Update Override merges a raw Docker
  spec. Skifity's Advanced tab shows the Kubernetes objects read-only; an escape
  hatch that edits them would bypass every invariant the panel enforces
  (quotas, Pod Security, the volume/Recreate rule).
* **One shared password** for everybody who operates the server.
* **A "backup" that silently omits the data.** If Skifity backs up the panel,
  the page must say in the same place that app data and the master key are
  elsewhere.
* **Install-time-only template variables without saying so.** CapRover's
  database passwords stop applying after install and the variable still looks
  editable. Skifity's templates should mark such inputs, or apply them.
* **Requiring wildcard DNS before the first app works.** Skifity's sslip.io
  address is the better first minute.
* **The Docker socket inside the panel.** Skifity talks to the Kubernetes API
  with a ServiceAccount; keep it that way.

## Sources

All read 2026-09-30.

* https://github.com/caprover/caprover (stars, forks, open issues via the GitHub search API)
* https://github.com/caprover/caprover/releases
* https://raw.githubusercontent.com/caprover/caprover/master/CHANGELOG.md
* https://github.com/caprover/caprover/blob/master/LICENSE
* https://raw.githubusercontent.com/caprover/caprover/master/TERMS_AND_CONDITIONS.md
* https://github.com/caprover/caprover/security
* https://github.com/caprover/caprover/issues/2035
* https://github.com/caprover/caprover/issues/2172
* https://github.com/caprover/caprover/issues/2281
* https://github.com/caprover/caprover/issues/2351
* https://github.com/caprover/caprover/discussions/2342
* https://github.com/caprover/caprover/issues/1858
* https://github.com/caprover/caprover/issues/950
* https://github.com/caprover/caprover-cli
* https://github.com/caprover/one-click-apps
* https://oneclickapps.caprover.com/v4/list and https://oneclickapps.caprover.com/v4/apps/<name> (all 360 definitions, for the multi-service and floating-tag counts)
* https://hub.docker.com/v2/repositories/caprover/caprover/ (pull count and tag dates)
* https://pro.caprover.com/ and its JavaScript bundle (Pro plan feature list)
* https://caprover.com/docs/troubleshooting-pro
* https://caprover.com/docs/get-started
* https://caprover.com/docs/app-configuration
* https://caprover.com/docs/zero-downtime
* https://caprover.com/docs/app-scaling-and-cluster
* https://caprover.com/docs/backup-and-restore
* https://caprover.com/docs/one-click-apps
* https://caprover.com/docs/cli-commands
* https://caprover.com/docs/deployment-methods
* https://caprover.com/docs/docker-compose
* https://caprover.com/docs/pre-deploy-script
* https://caprover.com/docs/service-update-override
* https://caprover.com/docs/certbot-config
* https://caprover.com/docs/resource-monitoring
* https://caprover.com/docs/firewall
* https://caprover.com/sitemap.xml
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=caprover
* https://pentest-tools.com/vulnerabilities-exploits/caprover-default-login_24307
* https://news.ycombinator.com/item?id=39857768 (via hn.algolia.com API)
* https://news.ycombinator.com/item?id=31378884 (via hn.algolia.com API)
* https://news.ycombinator.com/item?id=42850272 (via hn.algolia.com API)
* https://sliplane.io/blog/caprover-self-hosted-heroku-alternative
* https://dev.to/deploynix/self-hosted-paas-showdown-2026-coolify-vs-dokploy-vs-caprover-vs-deploynix-46l3
* https://temps.sh/blog/5-best-coolify-alternatives-self-hosted-paas-2026
* https://www.buildmvpfast.com/blog/coolify-vs-dokku-vs-caprover-self-hosted-paas-production-2026
* https://ownkube.io/blog/self-hosted-paas-comparison-2026
* https://selfhostable.dev/blog/coolify-vs-caprover-vs-dokku/
* https://kanopylabs.com/blog/coolify-vs-dokku-vs-caprover
* https://github.com/ivan-saorin/caprover-mcp (via search result)
