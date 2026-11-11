# Lucity

> **Status.** Written on 2026-11-08 against Lucity's `main` at commit `d6a67b6`
> (2026-11-06, tag `v26.11.2`) and against Skifity's working tree at `23ec07f`.
> No gap named here has been closed yet.

Lucity (`zeitlos/lucity`, lucity.cloud, plus the hosted Lucity Cloud) is a
Kubernetes platform in the Railway mould: connect a GitHub repository, and
Lucity builds it with Railpack, with no Dockerfile, deploys it, and puts it on
a generated hostname; PostgreSQL, a Redis-compatible store, S3 buckets and
volumes are added on a canvas and wired to a service by reference rather than
by copying credentials. Its pitch is leaving: every environment is one
ordinary Helm release, and **Eject** downloads a project as that chart with
one values file per environment. **Architecture:** a Go control plane,
`conductor` (GraphQL API, Helm releases, build orchestration, GitHub webhooks,
Stripe), with `stagehand` for background work since v26.11.1 (the deploy
admission queue, domain verification, chart upgrades, billing checks,
backups), `cashier` for usage metering into Stripe, a `builder` image run as a
Job per build against one shared BuildKit daemon, and a Vue 3 dashboard. The
conductor keeps **no database**: the state of an environment is its Helm
release's values, identity and workspaces live in Logto (a workspace is a
Logto organisation), and images live in a Zot registry. Underneath are
CloudNativePG, VictoriaMetrics and VictoriaLogs, an OpenTelemetry collector,
cert-manager, the Vertical Pod Autoscaler and a Gateway API controller (Cilium
or Envoy Gateway); the hosted platform adds a bunny.net edge in front of every
workload since v26.10.1. Three more services arrived in October and are not in
the documentation: `agent` and `switchboard` run coding agents (Claude Code,
Codex or OpenCode) inside an environment behind a feature flag, with a
Telegram bridge, and `cockpit` is the operators' own desktop console.
**Licence:** AGPL-3.0. **Pricing** (lucity.cloud/pricing, read 2026-11-08):
Hobby CHF 5 a month (one person, one custom domain, CHF 5 of credit), Pro CHF 25
(unlimited collaborators and custom domains, CHF 25 of credit), and usage on
top: CHF 20 per vCPU-month and CHF 10 per GB-month of memory, billed on what is
actually used, CHF 0.10 per GB-month of disk, CHF 0.03 per GB-month of object
storage and CHF 0.02 per GB of egress. The trial is 14 days or €5 of usage
(`docs/content/21.billing.md`). Self-hosting is free with "All features · No
limits · No fees". **Maturity:** repository created 2026-02-16; first public
release v26.4.1 on 2026-04-01; ten stable calendar-versioned releases since,
the newest v26.11.2 on 2026-11-06. The control plane was rewritten into one
binary in v26.6.1 after, in the release notes' words, "a pile of services that
were vibe-coded together". All 1,140 commits are by one author, Christian
Blättler of zeitlos.software, a Swiss company. The repository has 114,388
lines of Go and two `_test.go` files; the end-to-end suite under `tests/` was
deleted on 2026-08-19 (#75), and the release workflow builds and pushes
without running any test. `CONTRIBUTING.md` says external contributions are not
accepted yet. **Adoption** (read 2026-11-08): 54 GitHub stars, 13 forks, 20
open of 82 issues (GitHub search API). No Hacker News story or comment about
it was found, and Reddit could not be read from here. The v26.10.1 notes
(2026-10-02) say "Hundreds of accounts signed up per day, to the point where we
had to temporarily disable sign-ups", and named production users are the
maintainer's own sites (v26.4.1 notes).

## Feature inventory

### What shipped, release by release

From the GitHub release notes (read through the ungh.cc mirror) and the tags.

| Release | Date (2026) | What changed that matters for a comparison |
|---|---|---|
| v26.4.1 | 1 Apr | First public release. Identity moved from Rauthy to Logto; "proper build job isolation, rootless deployments, authenticated registry pulls" after the March reports (see Security) |
| **v26.6.1** | 19 Jun | Control plane rewritten as one binary, `conductor`. Digest-pinned deploys; Redis (Valkey) preview; databases exposed to the internet; build-time variables; build jobs with mTLS and user namespaces |
| v26.6.2 | 29 Jun | Volumes; CPU and memory charts; one-click buckets; PostgreSQL highly available by default; credentials discovered as variables; graceful rollouts; Eject back after a gap |
| **v26.7.1** | 8 Jul | The CLI with a built-in MCP server and a Claude Code plugin; **keyless deploys from GitHub Actions**; tracked branch and auto-deploy per service; rollout health reasons; a deploy admission queue; **leaked-secret scanning** of every repository with gitleaks and trufflehog |
| v26.7.2 | 27 Jul | Bucket file browser; public buckets behind a CDN; HTTP health checks; `lucity db`, `vars`, `status` |
| **v26.9.1** | 21 Sep | **Trivy vulnerability reports** read from Zot; **PostgreSQL continuous backups and point-in-time restore** over 30 days, restored into a new database; domain redirects; run-as user for image services |
| v26.9.2 | 29 Sep | New documentation; Eject ships a README and a `build.sh`; the MCP server stops returning secret values and masks secrets in logs |
| v26.10.1 | 2 Oct | Every workload behind a bunny.net edge with fair-use limits; HTTP to HTTPS redirects everywhere; CPU and memory requests sized by the VPA from real use; a maintenance banner |
| **v26.11.1** | 2 Nov | Database Metrics tab with the top queries; an address allowlist for exposed databases; plan limits; object storage and egress billed; new navigation; paid workspaces on their own nodes |
| v26.11.2 | 6 Nov | A log explorer over 14 days, stopped instances included; readable names beside fixed IDs; a live canvas; ⌘K palette; builds check out the requested commit (#136, before which a branch deploy could build the default branch's files) |

### Deploy sources and builds

* **GitHub only**, through a GitHub App whose sign-in runs through Logto. A
  service is either built from a repository or pulled from a **public** image;
  "Images from private registries are not supported yet", and an image service's
  tag "is fixed when the service is created", so a new version means a new
  service (`docs/content/03.services.md`).
* Builds are Railpack plans solved by a shared BuildKit daemon, cached per
  service, 30 minutes at most, never retried. **A `Dockerfile` in the repository
  is ignored** (`05.builds.md`). `RAILPACK_*` variables and `railpack.json`
  configure the build.
* A monorepo service has a root directory, but it is set only through the API,
  cannot be changed, and "There is no path filtering": a push deploys every
  service tracking the branch.
* Every deployment has four steps with their own logs: Build, Secrets (a
  leaked-credential scan that reports and never blocks), Deploy, Rollout. One
  deployment is active per environment; the rest queue, and a workspace's
  queue has a cap. Issue #129 (open) asks to skip queued builds a newer push
  made pointless.
* Changing variables, resources, port, start command, health check or a mount
  rolls the running image out again without a build.
* **Rollback** points the service at an image it already ran, through the API
  or the Claude plugin; "Rolling back is not exposed in the dashboard yet"
  (`04.deployments.md`).
* **No promotion between environments.** The comparison page says "Promotion
  copies an image digest from one environment to another", and v26.6.1's notes
  say so too, but the GraphQL schema has no mutation for it; `PROMOTION`
  survives only as an enum value. Promotion existed in the February prototype
  (commit "fix promote cleanup timeout", 2026-02-23).

### Domains, TLS and routing

* A generated hostname under the workload domain (`*.lucity.app`), covered by
  one wildcard certificate obtained through DNS-01, so it works at once.
* Custom domains need **two records: a `TXT` at `_lucity-verify.<domain>` that
  proves the workspace controls the domain**, and the routing record. Each
  custom domain gets its own Gateway API `ListenerSet` and cert-manager
  certificate (HTTP-01). Domain and TLS states are shown separately; apex
  domains use ALIAS or a redirect to `www`.
* 301 redirects between domains of a service; HTTP to HTTPS everywhere.
* Login protection for deployed services is an open request (#63). No
  firewall rules for apps.

### Databases and services

* **PostgreSQL 17 only**, always two CloudNativePG instances on separate
  machines, TLS inside the cluster, 0.25–4 vCPU, 256 MB–16 GB, storage 16–128
  GB grown in place. `pgvector` and contrib; no PostGIS, no other extensions, no
  pooler, no read replicas, no version choice or upgrade (`08.postgres.md`,
  "What's not there yet").
* **Internet access** per database: a public hostname on 5432 where TLS ends at
  PostgreSQL itself (SNI through Traefik), with an address allowlist that starts
  closed since v26.11.1 (#113); `lucity db expose` adds the caller's address.
* **A table browser and a query runner in the dashboard**: tables with
  columns, types and estimated rows, rows a page at a time, and SQL with a
  30-second cutoff and 1,000 rows. "It is a real connection, so it will happily
  write as well as read". The MCP server has a `run_sql` tool.
* A **key-value store** (Valkey): one instance, append-only file, 1 GB, no
  replica, **no backups**, never reachable from outside.
* **Buckets** on OVHcloud object storage, ten per environment, a file browser,
  public read behind a CDN, and connection snippets for the AWS CLI, rclone,
  Node and Python. One access key covers every bucket of the workspace.
* **Dynamic variables**: a service's variable can point at a database's
  `fqdn-uri`, `host`, `port`, `dbname`, `user` or `password`, a store's `uri`
  and parts, a bucket's five values, or an environment's shared variable,
  picked from a list; the value is read from the resource's Secret at start, so
  a rotation reaches the service on its next rollout. Deleting a resource that
  a variable still references is not refused (#68, open).
* No Compose, no templates or catalogue: an image is found through a Docker Hub
  search.

### Storage and backups

* PostgreSQL: a weekly base backup and continuous WAL archiving through the
  Barman Cloud plugin, 30 days, on by default with nothing to configure.
  **Point-in-time restore to the second, always into a new database**: the
  original keeps serving, and the person switches `DATABASE_URL` when ready. A
  Backups tab draws the restorable window as a timeline with the base backups
  marked, and takes a backup by hand.
* Volumes: one PVC, one replica, a stop-and-start on every deploy, 10–128 GB,
  usage charted. **"Volumes are not backed up"** (`10.volumes.md`; #72 open).
* Redis: not backed up. Buckets: deleted with no retention.
* No database export from the dashboard ("Run `pg_dump` yourself").

### Scaling and high availability

* 1–20 replicas, or a CPU-target autoscaler with a 5-minute scale-down window;
  replicas spread across nodes since v26.11.2. **No scale to zero** (#141,
  open). A volume pins a service to one replica.
* Requests sized from measured use by the VPA; the limits the person sets cap
  the bill.
* Each environment has a fixed quota of 32 vCPU, 64 GB and 512 GB; raising it
  is an email to the maintainer.

### Observability (logs, metrics, alerts, uptime)

* CPU and memory per service over 1 hour to 30 days, total or per replica,
  with **every deployment drawn as a dashed line** on the charts.
* A database Metrics tab: CPU and memory against limits, storage, connections,
  transactions, rows read and written, cache hit ratio, deadlocks, replica lag,
  and **Top queries** from `pg_stat_statements`.
* Runtime logs in VictoriaLogs: search by keyword, a time range up to 14 days,
  filter by instance, **stopped and crashed instances included**, a timeline of
  volume with one lane per instance and crashes marked, and live tail. Build
  logs kept a week.
* Rollout failures named in plain words: crash loop, out of memory, image pull
  failed, config error, quota exceeded, unschedulable, not ready, deadline
  exceeded. A service whose new version failed but whose old one still serves
  is **Degraded**, not Failed.
* No alerts, no uptime checks and no notifications for users. Grafana and
  Alertmanager are for the operator, who is paged on Telegram.

### Security, auth, roles, SSO, audit

* Sign-in with GitHub through Logto; no passwords, no second factor of
  Lucity's own, and no other identity provider documented.
* Roles per workspace: **Admin** and **Member**, and a machine-only
  **Deployer**. Everyone in a workspace sees and changes every environment:
  "there are no per-environment permissions" (`14.environments.md`).
* API tokens carry a role and are exchanged by the CLI for short-lived access
  tokens. **Keyless GitHub Actions deploys**: the job's OIDC token is exchanged
  for a session that can deploy, and read and set variables of, services built
  from that repository with CI deploys on — "Treat write access to the
  repository as deploy access" (`17.cli.md`).
* Variables are hidden until revealed in the dashboard and revealable by every
  member; they are stored in the environment's Helm release values.
* Apps run with user namespaces (`hostUsers: false`), no privilege escalation,
  all capabilities dropped, the default seccomp profile, no service account
  token; each environment is a namespace with its own network policy and quota.
* Leaked-credential scanning of every commit (gitleaks, trufflehog) and Trivy
  reports of the running image, read from Zot.
* **No audit log** (#67, open). The MCP server never returns variable values or
  credentials and masks them in runtime logs.

### Preview environments and branches

* **None.** The GitHub App subscribes to push events only; environments are
  made by hand and each service tracks one branch.

### Templates and catalogue

* None. A Docker Hub search when adding an image service.

### CLI, API, IaC, integrations

* A GraphQL API with a playground; errors are HTTP 200 with an `errors` list.
  Logs stream as GraphQL subscriptions.
* CLI (Go, Homebrew cask): `login`, `logout`, `account`, `workspace`, `deploy
  --ref --wait --timeout`, `status` (exits non-zero on a failed or degraded
  rollout), `vars list|available|set`, `db
  create|list|credentials|expose|unexpose|delete`, `token`, `mcp`, `version`.
  The comparison page's `lucity eject` command does not exist; Eject is a
  dashboard button and a GraphQL query.
* MCP over **stdio only**, 22 tools, and a Claude Code plugin with a deploy
  skill that reads a local repository, creates the project, databases and
  buckets, wires them, deploys, and retries a failed build up to three times.
  Setup recipes for Codex, Cursor, VS Code, Gemini CLI and Claude Desktop.
* **Eject**: a zip of the `lucity-app` chart, one values file per environment,
  a README and a `build.sh` that rebuilds source-built services with the pinned
  Railpack and pushes them to a registry the person names. Data, images built
  from source and bucket credentials are not in it; **variables and Redis
  passwords are, in plain text**.

### Notifications

* None for users. A platform banner set by the operator (v26.10.1).

### Multi-server and networking

* A Kubernetes cluster the operator provides, three nodes or more, with a CSI
  driver that can expand volumes, a Gateway API controller and a load
  balancer. Lucity adds no server, joins no machine and talks no SSH.
* Containerd on every node has to be configured by hand to pull from the
  in-cluster registry over plain HTTP at a pinned service address.

### Team and collaboration

* Workspaces with members, invitations and two roles; a personal workspace for
  every account; one subscription and invoice per workspace.

### Developer experience and onboarding

* Hosted: GitHub sign-in, pick a repository, the first build starts. Framework
  guides for Next.js, Nuxt, SvelteKit, Astro, Express, FastAPI, Django, Laravel
  and Go. The documentation is plain and thorough, with limitations stated per
  page.
* Self-hosted: "this is not a one-command install … Budget an afternoon"
  (`22.self-hosting.md`). Two domains, a DNS provider credential for DNS-01, a
  GitHub App, Logto configured by hand in its console (an API resource, five
  organisation roles, three applications, a connector), OVHcloud object
  storage keys without which "the conductor will not start", and two passes of
  the infra chart.
* English only.

## What users love

Outside the project's own pages, very little was found.

* **The documentation.** "The documentation at lucity.cloud/getting-started/concepts
  is genuinely excellent. The concept map that shows exactly how every Lucity
  abstraction maps to Kubernetes primitives is the kind of documentation most
  platforms never bother to write" (issue #25, 2026-03-18).
* **The idea of leaving.** "'A PaaS you can leave' is great philosophy" (issue
  #2, 2026-03-17, in a tongue-in-cheek licence request).
* **Sign-ups.** The maintainer reports hundreds a day in late September 2026,
  enough to pause them (v26.10.1 notes). The bug reports filed from the
  dashboard's button in September and October (#79 to #128, mostly with no
  description) are consistent with many new hosted accounts.
* No review, Hacker News thread or forum post was found.

## What users complain about

* **Billing locked people out.** "my workspace got suspended after less than 1
  hour of usage … 'Failed to open billing portal'" (#5, 2026-03-18), with the
  live Stripe key expired the same day (#22). "Cannot eject when workspace is
  suspended — the exit door is locked" (#16).
* **Nothing to install, early on.** No releases or tags (#14) and amd64 images
  only (#15), both 2026-03-18; tags began on 2026-03-30 and ARM images in
  v26.6.1.
* **Repositories it cannot build.** Railpack refusing a repository whose only
  build instruction is a Dockerfile (#116, #127); names that failed validation
  (#27, #74).
* **GraphQL for automation** (#24, 2026-03-18): "An AI agent has to know the
  full schema to construct a valid query." The CLI and MCP server arrived in
  July.
* **The maintainer's own open list** (issues filed 2026-08-03 to 2026-11-05):
  an app on the wrong port only times out (#142); no scale to zero (#141); no
  one-off commands, so people change the start command to `sh -c '...; sleep
  600'` (#138); domains added before the first build are invisible (#133);
  every queued push is built (#129); a configuration change racing a deploy
  fails with "release: already exists" (#109); no cron jobs (#69), audit log
  (#67), volume backups (#72) or login protection (#63); deleting a referenced
  database breaks the service (#68).
* **Limits written into the docs:** PostgreSQL 17 only and no extensions beyond
  the image; no private registries; image tags fixed; the root directory API
  only; Redis and volumes unbacked; rollback missing from the dashboard; no
  per-environment permissions; quotas raised by email.

## Security record

**Where I looked (2026-11-08):** NVD (no record for "lucity"), OSV (no record
for the Go module), the GitHub search API for issues, `SECURITY.md`, the
release notes and the code. The repository's advisory page could not be read
from here, and no advisory is mentioned anywhere else.

* **A public batch of reports on 2026-03-18.** One reporter filed sixteen
  issues in an afternoon (#6–#21), all closed the next day. Against the
  pre-rewrite architecture of gRPC services, ArgoCD and a Git server for
  values: build containers as root with network access to the platform's
  namespace (#8, #9); the internal registry readable by any build without a
  password, "source code of all tenants readable" (#7); one BuildKit for every
  tenant, cache shared, its API unauthenticated (#11, #21); the internal gRPC
  services trusting a workspace header without authentication, chained into
  "complete cross-tenant takeover … Build RCE → Internal Service Discovery →
  gRPC Workspace Spoofing → Cross-Tenant Write → Deployment Takeover" (#18) and
  a billing-suspension bypass (#20); path traversal through environment names
  into the GitOps repository (#17); SSRF through repository URLs (#12); an
  unauthenticated ArgoCD settings endpoint (#19); no rate limit on the GraphQL
  API, raw SQL included (#13). None has a CVE or advisory.
* **What changed.** v26.4.1: "Proper build job isolation, rootless deployments,
  authenticated registry pulls and authentication between internal service
  calls". v26.6.1: the gRPC services gone, mutual TLS and user namespaces for
  builds, link-local addresses blocked from builds, service account tokens not
  mounted, GitHub tokens scoped to one repository. v26.11.1: a service's root
  directory validated so it cannot leave the repository (#111). Today the
  registry has two password users, `builder` and `reader`, and the builds
  namespace is default-deny with BuildKit reaching the registry, DNS and the
  internet only (`charts/lucity/templates/builds-networkpolicy.yaml`). The
  BuildKit daemon is still one for everybody.
* **Disclosure.** `SECURITY.md` arrived after #3 asked for one: email
  security@lucity.cloud, acknowledgement in two business days, up to 90 days
  before disclosure, the latest release supported and the previous minor for
  critical and high.
* **Exposure by design.** Variables sit in plain text in Helm release values
  and are revealable by every member; the Eject archive carries them in plain
  text; the query runner is a writing connection open to members; a keyless CI
  session can be started by any workflow on any branch of the repository;
  exposed databases are on the internet behind an allowlist. The hosted
  dashboard identifies each signed-in user to its Rybbit analytics, and the
  operators' console lists those sessions with replays.
* **Assurance.** Two Go test files, no tests in the release workflow, and no
  image signing, SBOM or provenance in it.

## Against Skifity

Skifity's statuses: much of it runs on a real single-server and two-server k3s
on every push (`docs/progress.md`, "A real cluster, at last"); a real S3
bucket, a mixed-architecture cluster and production traffic never have.
Lucity runs a hosted platform in production, on its own clusters. Line numbers
are from Skifity's working tree at `23ec07f`.

| Capability | Lucity | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git providers | GitHub through an App | GitHub, GitLab, Gitea, Bitbucket and any Git, with webhooks and statuses (`internal/gitsrc`) | ahead |
| Images | Public only, tag fixed at creation | Any image, tag changeable, pull secrets for private registries (`internal/kube/manifests.go:127`) | ahead |
| Build detection | Railpack; a Dockerfile is ignored | Railpack, Nixpacks, Dockerfile, static (`internal/builder/detect.go`) | ahead |
| Monorepos | Root directory through the API only, no path filter | Root directory and watch paths (Phase 96) | ahead |
| Config change without a build | Yes | Yes, decided by the build fingerprint (ADR-0007) | parity |
| Promotion | Claimed on the comparison page; not in the API | `POST /api/apps/{app}/promote` with a fingerprint check (`internal/api/promote_handlers.go:80`) | **ahead** |
| Rollback | API and plugin only | Image and settings, from the panel, CLI and MCP (`internal/api/deploy_handlers.go`) | ahead |
| Deploy from CI | `deploy --ref --wait`; **keyless from GitHub Actions** | `skifity deploy --commit`, followed by default, with `SKIFITY_TOKEN` (`internal/cli/commands.go:694-740`, `docs/cli.md:58-70`); no OIDC exchange, no image field in the deploy body (`internal/api/deploy_handlers.go:19-26`) | **behind** |
| Leaked-credential scan | gitleaks and trufflehog on every commit | None (no scanner under `internal/`) | behind |
| **Domains, TLS and routing** | | | |
| Domain ownership | TXT record per domain before it routes | Refused only when another app already holds the name (`internal/api/apps_handlers.go:1716-1722`) | **behind** for several teams |
| Generated hostnames | Wildcard certificate by DNS-01 | `sslip.io` or `domains.wildcard`, HTTP-01 per name (`docs/concepts.md`) | different |
| Edge rules, password, maintenance | None (#63 open) | Firewall rules, passwords, maintenance mode (`internal/edgerules`, `internal/guard`) | ahead |
| **Databases and services** | | | |
| Engines | PostgreSQL 17; Valkey | Nine, PostgreSQL 16–18 (`internal/dbsvc/engine/engine.go:117-245`) | ahead |
| PostgreSQL HA | Always two instances | One by default, or an odd number (`internal/dbsvc/manifests.go:90-143`) | different |
| Connecting a service | References to any part, picked from a list | One full URL, sealed, under a chosen name, re-sent after a password change (`internal/dbsvc/manager.go:381-431`) | behind (small) |
| Table browser, query runner | Yes | None: the database page has Connection, Apps, Backups, Manage, History, Advanced (`web/src/pages/database-detail.tsx:164-169`) | **behind** |
| Database metrics | CPU, memory, connections, cache, lag, top queries | None for databases; app and server usage only (`internal/api/api.go` routes) | **behind** |
| From a laptop | Public SNI with an allowlist | `skifity db connect` through the panel (`internal/cli/dbconnect.go`) | different, safer |
| Buckets | Provisioned, linked, browsable | Garage, MinIO and RustFS as templates to set up yourself (`internal/templates/catalogue`) | **behind** |
| Compose, templates | None | Stacks (`internal/api/stack_handlers.go`); 374 templates | ahead |
| **Storage and backups** | | | |
| PostgreSQL | Continuous, 30 days, **PITR into a new database**, on by default | Logical dumps on a schedule once storage is set (`docs/backups.md`, "Nothing is backed up until you say where"); PITR left to CloudNativePG (`docs/decisions.md` ADR-0012, lines 230-255); **restore replaces the live database** (`docs/backups.md:165-178`) | **behind** |
| Other engines, volumes | None | Six engines, volumes with restore, sealed and verifiable (`internal/backup`, Phase 104) | ahead |
| **Scaling and HA** | | | |
| Instances | 1–20, CPU autoscaler | Instances, HPA on CPU and memory, scale to zero (`internal/kube/scaletozero.go`) | ahead |
| Requests | Sized by the VPA | Set per app; usage and thresholds (Phase 95) | different |
| **Observability** | | | |
| Usage charts | 1 h–30 days, deploy markers | 1 h–3 days (`app_samples`, Phase 95); no deploy markers (`web/src/components/usage-chart.tsx`) | behind (small) |
| Logs | 14 days searchable, stopped instances | Kubelet tail and the previous container (`internal/api/stream_handlers.go:427-456`); drains out (`internal/logdrain`) | **behind** |
| Rollout reasons | Eight named reasons, Degraded vs Failed | Named reasons (`internal/kube/explain.go`) | parity |
| Alerts, notifications | None for users | Ten channels, usage and backup alerts (`internal/notify/notify.go`) | ahead |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | GitHub through Logto | Argon2id, TOTP, passkeys, OIDC (`internal/auth`) | ahead |
| Roles | Admin, Member, Deployer; no per-environment access | Owner, admin, member, viewer; members limited to projects (`internal/store/models.go`) | ahead |
| Secrets | Revealable by members; plain in Helm values and in Eject | Sealed with context, write-only (`internal/crypto/envelope.go:182`) | ahead |
| Secrets in logs to an assistant | Masked by the MCP server | Raw: `get_app_logs` and `run_command` return output as printed (`internal/mcpserver/server.go:440-452,778-795`) | **behind** |
| Audit | None | Team audit log | ahead |
| App pods | User namespaces, no SA token | `restricted` profile, no SA token (`internal/kube/manifests.go:46-52,123`); no `HostUsers` anywhere | behind on user namespaces |
| Builds | One shared BuildKit; registry needs a password to read | A BuildKit per build (ADR-0026); **registry readable by any build** (`SECURITY.md:94-100`) | ahead on the daemon, **behind on registry reads** |
| **Preview environments** | None | Per-PR environments, seeded (Phases 76, 93, 107) | ahead |
| **CLI, API, IaC** | | | |
| MCP | stdio, 22 tools, a Claude Code plugin with a skill | stdio and `/api/mcp` (`internal/api/api.go`), 46 tools (`internal/mcpserver`); no plugin | ahead on tools, behind on a packaged skill |
| IaC | Eject only | `skifity.yaml` plan and apply (`internal/blueprint`) | ahead |
| **Exit** | Chart and values per environment, databases as CloudNativePG objects, `build.sh`, plain-text secrets | Team JSON and each app's objects (`internal/api/export_handlers.go`, `internal/cluster/cluster.go:221-275`); **no database objects, namespace or network policy, no way to get built images out** (`internal/cli/export.go:160-200`); whole team only | **behind** on a complete exit, ahead on secrets |
| **Notifications** | None | Ten channels | ahead |
| **Multi-server** | Bring a cluster of three | Servers joined over SSH, WireGuard, Hetzner and DigitalOcean (`internal/provision`, `internal/cloud`) | ahead |
| **Team** | Workspaces, two roles | Teams, four roles, project scoping | ahead |
| **Developer experience** | | | |
| Install | An afternoon, Logto by hand, OVH required | One installer on one server (`installer/install.sh`) | ahead |
| Languages | English | Five (`web/src/locales`) | ahead |

### Claims that no longer hold

* The discovery sweep's "Services reference database and bucket credentials
  instead of copying them" is true of Lucity, and Skifity's version is
  narrower than it looks: a link copies the full connection string into a
  sealed variable and keeps a record so a password change re-sends it
  (`internal/dbsvc/manager.go:381-431`). The value is managed, not referenced,
  and only the whole URL can be linked.
* Lucity's own comparison page describes `lucity eject`, promotion between
  environments and external secret managers. None is in the code read: Eject is
  a dashboard button, there is no promote mutation, and nothing reads Vault,
  Doppler or Infisical.
* ADR-0012 says CloudNativePG's continuous archiving "is documented as the
  advanced option". It is not: `barman` and point-in-time recovery appear
  nowhere in `docs/backups.md` or `docs/databases.md`.

## Gaps worth closing in Skifity

**P1**

1. **Restore into a new database beside the running one.** *What:* "Restore as a
   new database" on a backup: create a database of the same engine and version,
   load the backup into it, show both, and leave the switch of the app's link to
   a reviewed step; keep "replace in place" as the second choice. *Evidence:*
   Lucity's restores "always happen to a fresh database, which avoids any risk
   of data loss on the source"; the Openship page asked for the same (its gap 8)
   and it is still absent: a Skifity restore "replaces everything currently in
   the database … there is no undo", and the database is unavailable while it
   runs (`docs/backups.md:165-178`). *Fit:* `internal/dbsvc` create, the
   restore Job in `internal/backup`, `link_database`; the Backups tab. *Size:*
   M. *Without a cluster:* API, store and UI yes; the load needs one.
2. **Point-in-time recovery for PostgreSQL.** *What:* an opt-in per database:
   continuous WAL archiving to the team's backup storage through the Barman
   Cloud plugin, a base backup on a schedule, a retention window, and a restore
   to any moment in it, into a new database (gap 1). *Evidence:* Lucity turns
   this on for every database with 30 days, and it is the first thing its
   README shows. Skifity's dumps restore only to the last dump, and ADR-0012
   kept WAL archiving out because it puts storage credentials in the
   environment's namespace. *Fit:* a dedicated key per database where the
   storage supports one, otherwise a warning that says what the namespace can
   reach; `internal/dbsvc/manifests.go`, `internal/backup`, an amendment to
   ADR-0012. *Size:* L. *Without a cluster:* manifests and the window maths yes;
   archiving and recovery need CloudNativePG and a bucket.
3. **An export that comes up on another cluster.** *What:* per project or per
   environment as well as per team; each database as the objects that run it
   (the CloudNativePG `Cluster` or the engine's StatefulSet); the namespace with
   its network policy and quota; the `<app>-env` Secret as a template with the
   keys and empty values; and a way to get built images out — `skifity export
   --images <registry>` copying them from the in-cluster registry with the
   panel's token, or a `build.sh` like Lucity's that rebuilds from the recorded
   commit. *Evidence:* Lucity's Eject is a chart that brings up services,
   databases, stores and volumes, plus a rebuild script. Skifity's export renders
   only app objects (`internal/cluster/cluster.go:221-275`), lists databases as
   JSON, and its README says the image registry "has to be reachable from the new
   cluster" (`internal/cli/export.go:178-181`) — and that registry is in the
   cluster being left. *Fit:* `internal/api/export_handlers.go`, `internal/dbsvc`
   renderers, `internal/registry`. Keep secret values out. *Size:* M. *Without a
   cluster:* rendering and the archive yes; `kubectl apply` on a fresh k3s is a
   step for the Cluster workflow.
4. **Keep secret values out of what an assistant or a viewer reads.** *What:*
   before log lines and command output leave the panel — `GET …/logs`, run
   output, and the MCP tools built on them — replace every value of the app's
   secret variables, linked connection strings and their passwords (eight
   characters or longer) with `[secret:KEY]`. *Evidence:* Lucity's MCP server
   masks "every variable value and credential it knows about". Skifity keeps
   secrets write-only everywhere, and its own MCP code says "a value that has
   reached an assistant's context is in its provider's logs", yet `get_app_logs`
   and `run_command` return output exactly as printed
   (`internal/mcpserver/server.go:440-452,778-795`); an app that prints its
   `DATABASE_URL` on a crash hands it to the model, and to a viewer who may not
   read secrets. *Fit:* one masker in `internal/api` fed from the keyring at
   request time, used by the log stream and run output. *Size:* S–M. *Without a
   cluster:* yes.
5. **Registry reads for the servers only.** *What:* make pulling need a
   credential that only the servers' container runtimes hold, written into
   k3s's `registries.yaml` when a server is provisioned, so a build can no longer
   pull another team's image. *Evidence:* Lucity's #7 ("source code of all
   tenants readable") was the same hole, and it now needs a password to read.
   Skifity lists it as a known limit: "a build can pull another team's image,
   which is that team's code" (`SECURITY.md:94-100`). *Fit:* `internal/registryguard`
   (a read scope beside the push token), `internal/provision` scripts, the
   panel's own sweep token. *Size:* M. *Without a cluster:* the guard and the
   scripts yes; that nodes still pull, and a build does not, belongs in
   `test/cluster/verify.sh`.

**P2**

6. **Deploy from GitHub Actions with no stored token.** *What:* a per-app switch
   "deploys from this repository's workflows"; the CLI, seeing
   `ACTIONS_ID_TOKEN_REQUEST_URL`, asks GitHub for an OIDC token with the panel
   as audience and exchanges it for a short-lived token limited to deploying the
   apps whose source is that repository; plus `--ref` and `--image` on `skifity
   deploy`. *Evidence:* Lucity's keyless deploys (v26.7.1). The Kamal page's CI
   gap is still open (no image field in `internal/api/deploy_handlers.go:19-26`),
   and every CI job today stores a long-lived `SKIFITY_TOKEN`. *Fit:* the OIDC
   verifier in `internal/auth/oidc.go`, scoped tokens in `internal/auth/scopes.go`,
   `internal/cli`. State Lucity's caveat in the docs: any workflow on any branch
   can deploy, so offer a branch condition. *Size:* M. *Without a cluster:* yes,
   against an `httptest` issuer.
7. **Prove a team controls a domain before it serves it.** *What:* a TXT record
   at `_skifity-verify.<domain>` with a value per team, checked before the
   Ingress is created and the certificate requested, once per registrable
   domain; off on a panel with one team. *Evidence:* Lucity requires it on every
   custom domain. Skifity's only check is that no other app holds the name
   (`internal/api/apps_handlers.go:1716-1722`), so on a panel shared by teams the
   first team to type a hostname whose DNS already points at the cluster serves
   it. *Fit:* the DNS checks of Phases 57–58, `internal/dnsprov` to write the
   record where a zone is connected. *Size:* S–M. *Without a cluster:* yes.
8. **A table browser and a query runner on the database page.** *What:* tables
   with columns and estimated rows, rows a page at a time, and SQL in a read-only
   transaction with a statement timeout and a row cap; writing only for admins,
   with a confirmation that names the database. PostgreSQL first, then MySQL and
   MariaDB. *Evidence:* Lucity's explorer is one of the four features its README
   leads with. Skifity's database page has no data view (`web/src/pages/database-detail.tsx:164-169`);
   looking at data means `db connect` and a client, or `run_command`. *Fit:* the
   panel already reaches databases for `/tunnel`; audit every statement. *Size:*
   M. *Without a cluster:* yes, against a PostgreSQL service container.
9. **Database metrics and the slowest queries.** *What:* CPU, memory and disk
   against limits, connections, cache hit ratio and replica lag, and the top
   statements by total time from `pg_stat_statements`, on the database page.
   *Evidence:* Lucity's Metrics tab (v26.11.1). Skifity samples apps and servers
   (Phases 95, 99) and nothing for databases. *Fit:* the CloudNativePG exporter
   on 9187, read by the watcher into the sample tables. *Size:* M. *Without a
   cluster:* parsing and storage yes; numbers need one.
10. **Link the parts of a connection, not only the URL.** *What:* when linking,
    choose the URL or any of host, port, user, password and database name, each
    under its own variable, all kept in step by the existing password-change
    flow. *Evidence:* Lucity's dynamic variables expose six values per database;
    Laravel, Django and many images want `DB_HOST` and friends, and Skifity links
    one URL (`internal/dbsvc/manager.go:381-431`). *Size:* S. *Without a cluster:*
    yes.
11. **Buckets as a resource.** *What:* with Garage installed as a component, a
    bucket created like a database, a key that reaches that bucket alone,
    credentials linked to apps, a file list with upload and download, and an
    optional public read through the guard. *Evidence:* Lucity's buckets
    (v26.6.2, v26.7.2); Skifity has Garage, MinIO and RustFS only as templates to
    configure by hand (`internal/templates/catalogue`), so uploads end up on a
    volume, which pins an app to one instance. *Size:* L. *Without a cluster:*
    API and UI against a fake S3; Garage needs one.
12. **Logs that outlive the pod.** *What:* an optional log store as a component
    (VictoriaLogs fed by the existing Vector DaemonSet), with a time range,
    search and a filter by instance on the Logs tab, stopped instances included,
    and a retention setting. *Evidence:* Lucity v26.11.2. Skifity reads the
    kubelet's tail and the previous container (`internal/api/stream_handlers.go:427-456`),
    so the log of an instance replaced twice is gone unless a drain kept it.
    *Fit:* `internal/logdrain` already routes per namespace. *Size:* M–L.
    *Without a cluster:* the query API against a fake; collection needs one.

**P3**

13. **A leaked-credential scan of what is built**, report-only, beside the
    vulnerability report: gitleaks over the commit's diff in the build Job, with
    findings by file and line and never the secret itself. Lucity runs gitleaks
    and trufflehog on every deploy. `internal/vulnscan`, the build Job. S–M; the
    scanner's output parsing without a cluster.
14. **User namespaces for app pods** where every server's kernel supports them
    (6.3 or later), so root in a `baseline` environment is not root on the
    server. Lucity sets `hostUsers: false` on every service; nothing in
    `internal/kube` does. Preflight reads the kernel; S–M; needs a cluster.
15. **Deployment markers and a longer range on usage charts**: a dashed line per
    deployment and 7 and 30 days at a coarser resolution than the three days of
    `app_samples`. `web/src/components/usage-chart.tsx`, Phase 95's store. S, no
    cluster.
16. **"Add the address I am on"** beside the firewall's allow rules, the
    maintenance allowlist and the API token network allowlist, from the address
    the panel already resolves (`clientIPFrom` in `internal/api`). Lucity's `db
    expose` adds it by itself. S, no cluster.
17. **A notice banner** an administrator sets for a maintenance window on a panel
    shared by teams. Lucity added one in v26.10.1; nothing in `internal/settings`
    does it. S, no cluster.

## Things to deliberately not copy

* **Secrets anyone in the workspace can reveal, stored as plain Helm values,
  and shipped in plain text in the exit archive.** Skifity's write-only sealed
  secrets, and an export that says how to read them from your own cluster, are
  worth more than the convenience.
* **A query runner that writes, open to every member.** If gap 8 is built,
  read-only is the default and writing is an admin action, audited.
* **No database of its own.** "There's nothing to back up" holds because the
  state is in Helm values (with the secrets), Logto and Stripe; it moves the
  backup problem into three systems and makes every read a cluster call, which
  is why v26.11.1 added in-memory caches. Skifity's one SQLite file with
  scheduled backups is simpler to restore.
* **One BuildKit daemon for every tenant.** Skifity's daemon per build
  (ADR-0026) is the stronger boundary; keep it.
* **Coding agents run inside the customer's environment** with model-provider
  keys stored by the platform. MCP with scoped tokens is the boundary; the
  panel should not hold an agent's credentials.
* **Billing in the control plane.** Stripe keys in three services, trials that
  suspended people within an hour (#5) and an exit door locked behind a failed
  payment (#16) are what a hosted business brings into the code.
* **Analytics that identify the signed-in user, with session replay.** Skifity
  "never phones home"; keep it.
* **An install that needs an identity provider configured by hand, a GitHub
  App, two domains, a DNS-01 credential and a specific object-storage vendor.**
  Skifity's one-command install on one server is the advantage to protect.
* **Ignoring the Dockerfile and fixing an image's tag at creation.** Both make a
  common migration impossible without a workaround.
* **Marketing claims ahead of the code.** Promotion, `lucity eject` and secret
  managers on the comparison page are not in the product. Skifity's "Not
  executed" lines are the opposite habit.
* **Security reports in public issues, fixed overnight without advisories.**
  Skifity's `SECURITY.md` route and recorded fixes are better for everybody
  running an older version.
* **A release workflow that runs no tests**, in a 114,000-line Go codebase with
  two test files. Skifity's `make check` gate is the reason a phase can say what
  it verified.

## Sources

All read on 2026-11-08 or 2026-11-09; nothing in the repository changed after
2026-11-06.

* https://github.com/zeitlos/lucity, cloned at `d6a67b6` (1,140 commits). Files
  read: `README.md`, `CONTRIBUTING.md`, `SECURITY.md`, `docs/content/*.md`
  (quickstart, services, deployments, builds, variables, domains, postgres,
  object-storage, volumes, redis, workspaces, projects, environments, eject,
  api, cli, claude-plugin, metrics, logs, billing, self-hosting),
  `docs/solutions/2.comparisons/7.vs-coolify.md`,
  `services/conductor/internal/api/graphql/schema/*.graphqls`,
  `services/conductor/internal/conductor/capabilities.go`, `agent.go`,
  `charts/lucity-app/templates/`, `charts/lucity/templates/builds-networkpolicy.yaml`,
  `pkg/deployer/values/`, `cli/internal/mcpserver/`, `services/cockpit/`,
  `services/dashboard/src/router/index.ts`, `services/dashboard/src/composables/useAuth.ts`,
  `.github/workflows/release.yml`, `Makefile`; `git log` for tags, authors,
  component dates and the deletion of `tests/` (commit `c74e5ab2`, 2026-08-19)
* GitHub search API: `search/repositories?q=repo:zeitlos/lucity` (stars, forks,
  open issues, licence, dates) and `search/issues?q=repo:zeitlos/lucity+is:issue`
  (82 issues with bodies). Issue comments and the repository's own API, its
  advisory page and its web pages were refused from here
* https://ungh.cc/repos/zeitlos/lucity/releases (a mirror of GitHub's releases
  API: the notes of every release from v26.4.1 to v26.11.2)
* https://lucity.cloud/pricing
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=lucity (no
  records) and https://api.osv.dev/v1/query for `github.com/zeitlos/lucity` (none)
* https://hn.algolia.com/api/v1/search and `search_by_date` for "lucity",
  "lucity.cloud" and "zeitlos" (nothing about this product)
* Not readable from here: Reddit's search, and web search (the search tool's
  credits were exhausted)
* Skifity files named in "Against Skifity" and the gaps, read in this
  repository at `23ec07f`
