# Levelrail

> **Status.** Written on 2026-11-08 against Skifity's code at `23ec07f` and Levelrail's `main` at
> `2299805` (v0.9.3, published 2026-11-08; the docs already speak of a `release/v0.9.5` branch).
> Nothing in Skifity was changed for it; the gaps below are proposals.

Levelrail (`glincker/levelrail`, levelrail.com, docs.levelrail.com) is a self-hosted deployment
platform that turns Linux servers into "your own Vercel, on your own servers": Git and image
deploys, HTTPS, managed databases with verified backups, metrics, logs and alerts, previews, and
an MCP server for coding agents. It is the same product shape as Skifity — one control plane in
Go, a CLI and an MCP server over the same API — with Docker underneath instead of Kubernetes, and
its README says "No Kubernetes" in its first line. It is aimed at solo developers on one VPS,
people building with coding agents, agencies, and people leaving Coolify, Dokploy, CapRover or
Dokku. **Architecture:** the control plane is one Go binary, `levelrail`, that serves the REST API
(OpenAPI at `/api/v1/openapi.json`), the embedded React dashboard, an **embedded Caddy** ingress
that keeps its certificates in the store, a level-triggered reconciler over the Docker Engine API,
BuildKit used as a library, and node-local metrics and logs. State is **SQLite** through a pure-Go
driver (505 migration files on 2026-11-08). The CLI (`levelrail-cli`, also on npm and Homebrew),
the node agent (`levelrail-agent`) and the MCP server (`levelrail-mcp`) are **separate binaries**,
so "one binary" means the control plane. Extra servers run the agent, which dials out to the
control plane over gRPC with mutual TLS; a WireGuard mesh joins them. Podman works through its
Docker-compatible socket. **Licence:** Apache-2.0, one edition, no licence key. **Pricing:** free;
there is no paid tier and no hosted service (`docs/pricing.md`). **Maturity:** created
2026-08-14; 0.1.0 on 2026-09-09; twenty `0.2.0-beta` builds; v0.2.0 on 2026-10-12 and then
nineteen more releases to **v0.9.3** (changelog 2026-11-07, published 2026-11-08), one every 1.4
days. Every release is pre-1.0; the project's own label is "single node is stable, multi-node is
beta", and `docs/feature-status.md` gives every feature a label with the evidence behind it.
**Adoption** (2026-11-08 and the first hour of 2026-11-09 UTC): 190 stars, 16 forks, 13 open
issues and 9 open pull requests (GitHub search API); 3,393 npm downloads of `levelrail-cli` from
2026-10-09 to 2026-11-07; `ADOPTERS.md` is empty; the linked Discord ("GLINR", shared with the
maker's other projects) has 11 members. **People:** one maintainer, Gagan Deep Singh of GLINR
STUDIOS (`MAINTAINERS.md`). Of 1,372 pull requests, 1,031 are his, 192 are Dependabot's, and the
rest are bots: release-please (`glinr-bot`), Google's Jules agent and GitHub Actions. No pull
request and no issue has come from anybody else: of the 42 issues, 23 are the maintainer's (22 of
them release epics) and 19 are flaky-test reports from a bot. The tree is large for its age: 16,487 files, 8,785 Go files
(3,688 of them tests), 2,878 TSX files and 829 documentation pages.

## Feature inventory

Labels in **bold brackets** are Levelrail's own, from `docs/feature-status.md` (updated
2026-11-08): stable means unit tests, a doc page and a live end-to-end run on one machine; beta
means tested without an end-to-end run; flagged means off by default behind `APP_EXPERIMENTAL` or
Settings, Beta features. "Real infrastructure" — a fresh VPS, a public domain, a real vendor — is
"none found" for almost every row by the project's own count.

### What shipped, release by release

* **0.1.0 (2026-09-09)** to **0.2.0-beta.20 (2026-10-11):** the control plane, reconciler, agent,
  ingress, builds, databases and backups, the dashboard, the first MCP server.
* **0.2.0 to 0.3.0 (2026-10-12):** first non-beta tags; release-please; a separate `kit/` Go module
  of reusable packages, tagged alongside.
* **0.4.0 (2026-10-15):** a demo mode, the infrastructure canvas, a deploy timeline, secret
  rotation, sleep when idle, deploy guardrails. **0.5.0 (2026-10-16):** "observability suite,
  Pages, and hardened git deploys". **0.6.0 (2026-10-17):** an image for `levelrail-mcp`, pipeline
  artifacts and triggers, monorepo detection. **0.7.0 (2026-10-20):** two entries named for
  internal releases "0.13" and "0.14" — "a plugin marketplace and one search across everything",
  "easier migration, one-command deploys, git push deploys and stronger SSO". **0.8.0
  (2026-10-21):** the inbox, upgrade recovery, zone spread. **0.8.7 (2026-10-29):** the agent
  reports a silent control plane to an off-host URL, a v2 API with list, get and watch, GitLab and
  CircleCI OIDC tokens.
* **0.9.0 (2026-11-02):** several dozen templates. **0.9.2 (2026-11-06):** 150 feature lines in the
  changelog (domain bulk actions, onboarding, appearance settings, a storage page, a Next.js
  doctor, deploy receipts, audit chain investigation, DNS providers, CI gate setup, image
  auto-update windows), nearly all under two squash commits. **0.9.3 (2026-11-07):** one feature
  commit and CI fixes.

### Deploy sources and builds

* **Sources:** GitHub webhooks **[stable]**; GitLab, Bitbucket and Gitea webhooks **[beta]**; a
  Git push remote to the server itself, behind the `git-push` flag **[beta]**; a registry image;
  `deploy from CLI` of the current directory with no Git remote and no registry; dragging a folder
  or a `.zip` onto a static site ("drop deploy"); a Docker Compose file as one app with per-service
  status, start order and rollback to an earlier compose file **[beta]**; Kubernetes YAML converted
  to apps as text only (`import k8s`).
* **Builds:** Dockerfiles through BuildKit used as a library, Railpack for everything else (Node,
  Go, Java and Python have live builds; Ruby and PHP do not) **[beta]**, a separate path for static
  sites; builds can be routed to chosen nodes; a "build center" with the queue, history, cache hit
  ratio and each node's BuildKit cache; build hooks; monorepo paths.
* **Functions:** a ten-line Node, Python or Go handler deployed as a function that sleeps when idle
  **[beta]**.
* **Next.js:** skew protection, a shared cache handler, an image cache, a stable Server Actions key
  across replicas (0.9.2), and a "Next.js doctor" with fixes for build, memory and cache.
* **Image updates:** an image app redeploys when its tag points at a new digest, optionally only
  inside a cron window; push events from GHCR and Docker Hub deploy the pushed digest (0.9.2);
  scheduled deploys of a branch or a one-shot ref.
* **CI:** a bundled composite GitHub Action that deploys a prebuilt image; GitLab CI, CircleCI,
  Jenkins and Buildkite recipes; **OIDC deploy trust** for GitHub Actions, GitLab CI and CircleCI
  so a CI job needs no stored token **[beta, unit tests only]**; a "CI gate" that holds an
  automatic deploy until the commit's checks conclude on GitHub or GitLab; YAML pipelines with
  jobs, matrices and approvals that run on the user's own nodes **[stable]**.
* **Supply chain:** an SBOM per Dockerfile build, an optional scan in a short-lived container and a
  gate that can block a release **[stable, off by default]**; secret scanning of the repository and
  of plain variables before a build, per app off, warn or block.
* **Deploy strategies:** blue-green, rolling or recreate, each gated on a readiness probe; images
  pinned by digest; `max_surge`, `max_unavailable` and a `min_available` budget per app.
* **After cutover:** a **post-deploy watch** (5 minutes by default) rolls back to the previous
  pinned image when restarts reach 2, or when 5xx answers through the ingress reach 20% after 20
  requests; a p95 latency regression is a soft signal that rolls back only when asked
  **[stable for crash loop, OOM and 5xx; beta for latency]**. The previous release is held running
  for the watch window so the rollback is a switch.
* **Before cutover:** protected environments where a deploy waits for a **second person's
  approval** **[stable, live e2e]**; **freeze windows** **[stable]**; deploy policies at
  organisation, project and environment scope in warn or enforce mode; a stale-deploy guard; an
  optional snapshot of an app's linked databases before each deploy so code and data roll back
  together; a downloadable "deploy receipt" with the commit, digest, requester, approver and gates.
* **Canary and split testing:** a share of traffic to a new image in stages with automatic rollback
  on errors or latency; split testing sticky per visitor between a branch deploy or a retained
  release. Neither has a row in the feature-status page.

### Domains, TLS and routing

* **Embedded Caddy.** "The default is an internal certificate until ACME is enabled"; the installer
  turns HTTPS on after a healthy install (0.9.2). Public ACME has **one recorded run** against a
  live VPS (2026-10-05); renewal and DNS-01 wildcards are unverified **[beta]**. Names a public CA
  can never sign get Caddy's internal issuer instead. A certificate attempt ledger and a certificate
  budget per instance.
* **DNS:** zones at Cloudflare and Route 53 through a DNS provider, more providers with token
  verification and a background DNS watcher (0.9.2), an apex-and-www plan with a canonical host,
  dynamic DNS for home connections, a domain doctor that probes a domain end to end with a cause,
  evidence and fix, and a cutover verifier for a domain moved from elsewhere **[beta]**.
* **Request filtering:** ordered rules, IP lists, automatic bans, a browser challenge, bot handling
  and an attack mode; per-app rate limits and deny lists with a log-only mode that replays recent
  traffic before enforcing **[beta]**; per-domain request body caps.
* Static routes and redirects, a CDN-style cache with hit ratios per node, UDP ports from Compose
  services, an "app exposure" switch between proxy-only and a direct port, a Cloudflare tunnel
  **[flagged]**, a load balancer with sticky sessions and weights **[flagged]**.

### Databases and services

* **Engines:** thirteen are defined — PostgreSQL, MySQL, MariaDB, MongoDB, Redis, Valkey, KeyDB,
  Dragonfly, ClickHouse, Meilisearch, Qdrant, Typesense, Memcached — and the project counts nine as
  supported and four as "built but not yet live-verified". PostgreSQL has `pgvector`, TimescaleDB
  and PostGIS flavours and a pgbouncer-style pooler. TLS on new PostgreSQL, Redis, MySQL, MariaDB
  and MongoDB databases.
* **Versions:** minor and patch changes in place with a health check and an automatic revert;
  PostgreSQL major upgrades by a guarded dump and restore; scheduled upgrades with security and
  end-of-life checks.
* **Owner password rotation** on a schedule for PostgreSQL, MySQL, MariaDB and MongoDB, with the old
  password put back if the engine refuses the new one.
* A database explorer and viewer, external databases, database "branches", export, clone and import
  **[beta]**; S3-compatible object storage destinations; a mail server (mox) with mailboxes
  **[flagged]**; feature flags for apps **[stable]**.

### Storage and backups

* Named volumes, inline config files, a **volume file browser** (list, download, upload, delete one
  file, restore one file from a backup), NFS and CIFS shares, volume export and import **[beta]**.
* **Backups:** volume backups compressed with zstd and encrypted with age on the control plane by
  default, with a key file written to the data directory that the docs say to copy elsewhere;
  database dumps encrypted per backup target, on by default for new targets. Schedules,
  retention, restore as a new volume or a new database, restore onto another node.
* **Point-in-time recovery:** PostgreSQL **[stable, live e2e against an S3-compatible bucket]**;
  MySQL and MariaDB by binary log replay **[beta]**; none for MongoDB.
* **Restore drills:** a drill checks the object's size, downloads it and compares its SHA-256 with
  the one recorded at backup time, restores it into a scratch volume or a throwaway container of the
  database's own image, validates it (file manifest for volumes, a replayed dump and a table count
  for PostgreSQL, MySQL and MariaDB) and destroys the scratch resource. Every resource with a
  backup is drilled on its newest backup **once a week by default**
  (`APP_BACKUP_DRILL_INTERVAL_HOURS=168`). The Backups page shows "last backup" and "last verified
  restore" as separate columns, and a failed drill fires a `restore_drill_failed` alert.
* Control plane snapshots, master key escrow, offline restore, and a "whole server recovery" that
  pulls every volume and database back from its latest backup after the control plane is restored.

### Scaling and high availability

* Replicas per service; autoscaling between a minimum and a maximum on CPU, memory or request load
  **[beta]**; **sleep when idle**, with the ingress holding the first request **[stable, run
  against a real Docker daemon on macOS]**.
* Multi-node **[beta]**: enrollment, cordon, drain with container relocation and explicit
  placement were verified across two Docker-in-Docker daemons on one machine, not across hosts.
  "The WireGuard mesh and cross-host remote transport are unverified outside fakes and local Docker
  daemons" (`docs/known-limits.md`). Taints, tolerations, affinity and spread or bin-pack scoring
  **[beta]**; an opt-in node failure policy, tested with fakes only.
* A resilience page measures what survives a `SIGKILL` of the control plane: the route table keeps
  answering and apps keep running; containers use `unless-stopped`.

### Observability (logs, metrics, alerts, uptime)

* **Node-local by design** (ADR-008, ADR-009): each node keeps its own time series in SQLite and its
  own log store, and the control plane queries nodes on demand. A log explorer with saved queries
  and error signatures, a log archive to a bucket **[beta]**, log budgets, a Loki-compatible API, a
  Prometheus scrape endpoint, custom dashboards.
* **Traces** from OpenTelemetry kept on the node and read as a waterfall; **error tracking** that
  takes any Sentry SDK and groups errors by deploy; web vitals; usage, cost showback per app and
  team, budgets and **spend caps** that throttle or pause an app **[beta]**.
* **Alerts:** 25 rule kinds and an alert inbox; an "attention center" for what waits on an operator;
  a doctor that gives a fix or a next step for every check, and "fix it" actions that preview and
  verify a fix.
* **Uptime:** a public **status page** with components, 90-day bars, incidents, maintenance and RSS
  **[stable]**; HTTP, TCP, TLS and heartbeat monitors **[flagged, off unless
  `APP_MONITORS_ENABLED`]**; a **heartbeat** that calls a URL of the operator's while the control
  plane is healthy, so an outside monitor notices when the whole server is down.

### Security, auth, roles, SSO, audit

* Passwords with a policy, passkeys, TOTP with recovery codes; OAuth and OIDC sign-in with 17 named
  providers plus generic OIDC (generic run against Keycloak 26.0.8); **SAML and SCIM** behind the
  `saml-scim` flag **[beta, local lab with Keycloak and SimpleSAMLphp]**; an optional automatic
  redirect to SSO with a password escape hatch.
* Team roles owner, admin, developer and viewer; **IAM policies**, AWS-style Allow and Deny on apps,
  databases and environments, with a simulator **[stable]**; custom roles and per-environment grants
  **[flagged]**; session policy per team (idle timeout, maximum age, IP allowlist, required
  two-factor); step-up for sensitive actions; emergency access with break-glass tokens.
* **Audit log:** every mutating request with the actor and the **caller surface** (CLI, dashboard,
  MCP or API), a **hash chain** verifiable at `GET /api/v1/audit-log/verify`, CSV export, retention,
  forwarding and an "audit anchor" webhook (0.9.2); a signed compliance evidence pack.
* Secrets encrypted per app with a data key wrapped by a master key; reveal policies; scheduled
  secret rotation; references to Vault, 1Password Connect and Infisical, the last two tested only
  against local stand-ins.
* Host hardening from the dashboard: `ufw` rules with a lockout guard, an **exposure audit** of
  Docker-published ports that bypass `ufw`, OS package updates with holds. "AI control" modes from
  off to admin for agent tokens **[flagged]**. No usage telemetry (`docs/privacy.md`).

### Preview environments and branches

* Per pull request **[stable for GitHub, beta for GitLab and Bitbucket]**, with caps, approval for
  forks, a single PR comment, `PR_NUMBER` injected into Compose previews (0.9.2), a basic-auth gate
  on static previews, a migration step tested with a fake runner, and PR comment commands to
  redeploy or roll back **[off by default]**. Deploy URLs per recent successful deploy **[beta]**.

### Templates and catalogue

* A one-click catalogue in `internal/catalog`, each entry a `template.yaml` and a `compose.yaml`,
  with generated secrets, logos, an arm64 compatibility page and a per-template security level. The
  count differs by page: **359** in the feature-status page, **360** in the README, **442** on the
  website (all read 2026-11-08). A local smoke on 2026-10-25 brought up 100 of 359 on one arm64
  Mac with 93 healthy; the published status page counts **101 of 108** that need no configuration
  as passing. A weekly `template-smoke` workflow exists; its results are not published.
* Template upgrades with a read-only plan; five bundled starter stacks; a "Deploy to Levelrail"
  button for other projects' READMEs.

### CLI, API, IaC, integrations

* **CLI:** `levelrail-cli` (npm, Homebrew, a shell installer), a reference of 182 KB, `--json`,
  a keyboard-driven TUI, `apps wait` that blocks until a deploy converges and exits non-zero on
  failure, `bench run` with an open-loop load generator, `doctor`, `import`, and **device login**
  (`auth login --device`, approved in the dashboard; the README's "Connect your CLI" card prints a
  `login --code ABCD-EF23` line). `levelrail-cli init` writes `app.yaml`, an **`AGENTS.md`** with
  the project's deploy instructions and an **`.mcp.json`** entry.
* **Platform as code:** projects, environments, apps, domains and databases as YAML with plan and
  apply under a plan hash, from the CLI, the dashboard, MCP or CI **[flagged `iac`]**; a visual
  "canvas" on top of it **[beta, flagged]**; a Terraform and OpenTofu provider
  (`integrations/terraform-provider`).
* **MCP** (`levelrail-mcp`, **[beta]**): **259 tools** in 44 toolsets, 24,511 estimated tokens for
  the whole surface without output schemas (`docs/mcp-tool-surface.md`, characters divided by 4).
  By default a session lists **about 2,400 tokens**: a small core plus `list_toolsets`,
  `enable_toolset` and `disable_toolset`, which add a toolset and send `tools/list_changed`.
  Output schemas are omitted unless `--output-schemas`, because they were "about 60 percent" of the
  listing. **The list follows the token's abilities**: a read token never sees a deploy tool, and
  `--readonly` refuses to start with a write token. Modes `read-only`, `standard` (the default, no
  destructive tools) and `full`; two tools sit behind `--allow-write` and need a `confirm` argument
  equal to the resource's name. A refusal names the missing ability and a read-only tool to use
  instead. Logs, deploy output, commit messages and probe text come back **sanitised and wrapped in
  an untrusted-data block** with a `_meta` flag. Seven prompts (`diagnose_app`, `plan_deploy`,
  `explain_failed_deploy`, `migrate_from_coolify` and others), resources for apps, logs, deploys and
  four runbooks, `--print-config` for Claude, Cursor, VS Code and Zed, and a budget test that fails
  when a listing passes its ceiling. A remote Streamable HTTP endpoint at `/mcp` serves read tools
  only, with OAuth for clients that need discovery **[flagged `mcp-http`]**; each call is audited as
  `mcp.tool_call` with the tool and argument names, never values. A scripted eval of sixteen agent
  tasks runs without a model.
* **For agents:** a skill (`npx skills add https://github.com/glincker/levelrail --skill
  levelrail`), `llms.txt`, `AGENTS.md`, a `context7.json` and a `glama.json`.
* **Importers:** Coolify (HTTP API or a `docker inspect` snapshot), Dokploy, CapRover, Dokku (over
  SSH or a pasted `config:export`), a Compose file, Vercel (projects, variable names, domains),
  Cloud Run, and a same-server takeover. Each starts with a dry run, the old server keeps serving
  while apps are staged stopped and unrouted, data and volumes are copied, a parity report compares
  the two before cutover, and cutover goes one domain at a time. The Coolify importer is "built and
  unit tested against fixtures only" (`docs/known-limits.md`). No MCP tool can start a takeover or
  a cutover.
* A mobile API with paired devices **[beta, flagged]**, an in-dashboard AI chat **[beta,
  flagged]**, GPU model serving on NVIDIA **[flagged]**, plugins in signed containers whose first
  kind is notification channels **[flagged]**.

### Notifications

* **18 channel kinds:** generic webhook and SMTP **[stable]**; Slack, Discord, Telegram, Pushover,
  PagerDuty, Teams, Resend, ntfy, Gotify, Mattermost, Lark, Rocket.Chat, Opsgenie, Webex, Google
  Chat and browser push **[beta: "No real vendor has accepted a send"]**. Seven email providers with
  a primary and a backup. Signed event webhooks. Plugins for DingTalk, WeCom, Zulip, Matrix and
  Twilio SMS in `plugins/`. The doctor warns when nobody can be notified (0.9.2).

### Multi-server and networking

* Agents dial out, so a managed node needs no inbound port for control traffic. Nodes join with a
  one-time token, or the control plane installs Docker and the agent over SSH **[beta]**, or it
  creates a server at **Hetzner, DigitalOcean, AWS, Azure or GCP** **[beta, no run against a real
  cloud]**. Private networks between named apps **[beta]**; apps and volumes move between nodes.
  A control plane started without `APP_AGENT_ADVERTISE_HOST` lets a node enroll and then leaves it
  `pending` on a TLS name mismatch (found in their own two-daemon test, documented).

### Team and collaboration

* Organisations with a profile, teams, projects, instance-wide environments (dev, test, UAT,
  production) with a switcher **[flagged]**, team and project quotas, invitations, team-scoped
  tokens, deploy approvals, freeze windows, deploy policies, access reviews, white-labelling through
  `brand.yaml`.

### Developer experience and onboarding

* `curl -fsSL https://levelrail.com/install.sh | sudo sh` on Linux amd64 or arm64 with systemd:
  installs Docker from `get.docker.com`, warns under 1,024 MB of memory, offers a **minimal
  profile** under 2,048 MB, prints the provider's firewall rules, and **verifies the release's
  cosign keyless signature** when cosign is present (it installs a pinned cosign v3.1.3 unless
  told not to). Guides for a free Oracle Cloud Arm VM, home hardware, installing from China, and
  "what you need" in plain words.
* A first-15-minutes checklist, onboarding steps, a "get started" chip, a first-run strip from
  install to a live app, a command palette, appearance settings (text size, accent, contrast,
  roundness, motion), a sticky app header with one primary action.
* **Measured footprint:** "idle RSS 200 MB (peak 253 MB), 273 goroutines, 11 threads, 1.5 percent
  CPU" on a 2 vCPU, 8 GB box with no apps under load, plus a 395 MB metrics database
  (`docs/benchmarks.md`, 2026-10-19); a boot harness records 232 MB on Linux arm64 and is kept as a
  ratchet. `docs/known-limits.md` adds that the idle footprint "was measured in a Docker Desktop
  container. A bare metal or VPS number is still open." Docker's own memory is not in either.
* **Languages:** the README in seven (English, Simplified Chinese, Spanish, Brazilian Portuguese,
  German, French, Japanese). The dashboard ships English and Spanish only: 114 of 155 English
  namespace files have a Spanish copy (`web/src/locales`, 2026-11-08), and `docs/i18n.md` says
  "Many older components still hardcode English". The documentation is English.
* Upgrades: `levelrail-cli upgrade --plan` lists breaking changes, the schema change and the disk
  needed; the upgrade verifies the release signature, snapshots the data, tries the migrations on a
  copy, and **puts the old binary and data back by itself** when the new release is unhealthy
  **[beta]**. After the first verified signature, an unsigned release is refused.

## What users love

**Not found.** There is no independent account of using Levelrail to quote. Hacker News has no
submission or comment about it (Algolia, 2026-11-08; the maker's earlier "Show HN" for another
project is the only hit for "glincker"); Reddit refused this sandbox; the web-search credits ran
out before a search could run; GitHub Discussions could not be read from here. The issue tracker
has no issue from anybody but the maintainer and a bot, and no pull request from another person.
What can be said is what the project offers that it says other tools do not, which is its own
claim and not a user's:

* **Proof as a feature.** Every feature carries a stable, beta or flagged label with the test that
  backs it, "Known limits" names what has not run on real infrastructure, benchmarks state their
  hardware, and the README's comparison table says "Levelrail is the youngest project here, with
  fewer users and less production mileage".
* **Agents treated as untrusted.** Read-only tokens by default, tool lists that follow the token,
  wrapped untrusted text, and every call audited with its surface.
* **Switching in.** Dry-run importers for the four platforms its audience leaves, with the old
  server serving throughout.

The signals of use are small: 190 stars and 16 forks in under three months, 3,393 npm downloads of
the CLI in a month, an 11-member Discord, and an empty adopters file.

## What users complain about

No user complaints were found, for the same reason. What follows comes from the project's own
pages, and is what a user would run into.

* **Most of it has not run where users run it.** "Tier 4 is empty for almost every row":
  multi-node and the WireGuard mesh across real hosts, ACME renewal and wildcards, fifteen of the
  eighteen notification kinds, SAML against Okta or Entra ID, the Coolify importer against a real
  Coolify, `databases import` into a live database, cloud provisioning against a real cloud. Seventeen
  features sit behind flags.
* **Numbers that disagree between pages.** Templates are 359, 360 or 442 depending on the page,
  and the README's "360 smoke-tested" links to a page that counts 108. The README says MCP writes
  are "off until you pass `--allow-write`"; `docs/mcp.md` says `deploy_app`, `rollback_app` and
  `restart_app` "are governed by `--mode`, not `--allow-write`", and the default mode is
  `standard`, which includes them (a new token is read-only, which is what actually keeps them
  away). The footprint is from "a 2 vCPU and 8 GB test box" in one page and "a Docker Desktop
  container" in another.
* **Automatic upgrades, described two ways.** `docs/privacy.md` says the release check "only runs
  when an operator opts in" and "never applies an update by itself". `docs/upgrade-safely.md` says
  applying safe patch releases is "On by default (any eligible release, 03:00 UTC window) until you
  save a policy", and migration `3210_upgrade_policy_auto_default.sql` adds a "one-time default-on
  notice"; the first migration (`0476`) had the default off. Which holds in the code was not read.
* **Flaky live tests.** Nineteen flaky-test issues opened by a bot since 2026-10-05, three still
  open on 2026-11-08 (`#1477`, `#1441`, `#1410`), two of them the remote interactive shell over
  the agent.
* **Pace.** A release every 1.4 days, with 150 feature lines in 0.9.2 alone, by one maintainer;
  anything built on it moves under the user's feet.

## Security record

**Where I looked (2026-11-08 and the first hour of 2026-11-09 UTC):** NVD keyword search for
"levelrail" (no results); OSV for `github.com/glincker/levelrail` (no results); the project's
`docs/security-advisories.md`, `docs/threat-model.md` and `.github/SECURITY.md`; issue search for
security terms. The repository's GitHub advisory page could not be read from here.

* **No CVE and no GitHub advisory found.** The project keeps its **own advisory list** with stable
  IDs, `LRSA-2026-0001` to `-0013`, every one "found by internal audit", every one "before 0.9" and
  "fixed in next release". Three are High:
  * **LRSA-2026-0001:** the route that attaches a database to an app checked the right to change
    the app and not the right to see the database, so a writer of one app — including one denied
    the database by IAM, or in another team — could attach it and receive its connection details.
  * **LRSA-2026-0006:** routes that took a resource name from the body, a spec or a query string
    authorized only the app in the URL, so a caller could "overwrite another team's service by name
    through a compose or spec deploy, point a git source at other apps so every push deployed into
    them, promote into an app they were denied", or clone apps they were denied. The fix is one
    reference-authorization helper and a guard test that fails when a handler decodes a
    resource-shaped field without authorizing it.
  * **LRSA-2026-0002:** behind the embedded ingress every request reached the control plane from
    `127.0.0.1`, so per-visitor rate limits were one shared budget, and an anonymous client could
    keep the operator locked out of sign-in or make Git providers' webhooks answer 429.
* Medium: unbounded request bodies on unauthenticated routes (0003); HTML attribute injection
  through link targets in assistant replies (0004); **the dashboard assistant ran every tool with a
  permanent root token, and chat sessions had no owner** (0007), now a short-lived token per turn
  carrying the sender's abilities; **upgrades fell back to a same-origin checksum when a release
  signature was missing** (0013). Low: notification credentials in plaintext (0008), session-link
  tokens in a GET path (0009), the OAuth redirect URI taken from the `Host` header (0010), secret
  -looking values in an IaC export (0011), unbounded request IDs and failed webhook deliveries
  (0005, 0012).
* **Policy:** latest release only; "There is no dedicated security team yet and no formal SLA";
  acknowledgement within three business days as a goal; reports through GitHub's private
  reporting.
* **Process:** CodeQL, a SonarCloud workflow, gitleaks, a scheduled security workflow, Dependabot
  (192 pull requests), signed releases with provenance. The threat model says losing the master
  key "is unrecoverable", and leaves out "a fully compromised control plane host (it holds the
  master key in memory and the Docker socket)".
* **Exposure by design:** the control plane, the API and the public ingress are one process on
  ports 80 and 443 of the same host; an AI chat and a CLI console in the dashboard (both flagged);
  a remote interactive shell into containers over the agent; the Docker socket in the control
  plane's hands.

## Against Skifity

Levelrail is the closest thing in this directory to Skifity's own shape — one Go control plane,
a CLI, an MCP server and a panel over the same API, single-maintainer, documented to excess — with
Docker where Skifity has Kubernetes. Rows say what a user gets. The Skifity column was checked in
the named files on 2026-11-08 at `23ec07f`.

| Capability | Levelrail | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git providers | GitHub stable; GitLab, Bitbucket, Gitea beta; push remote flagged | GitHub, GitLab, Gitea, Bitbucket (`internal/gitsrc/webhook.go`, `internal/api/bitbucket.go`) | parity |
| Without a repository | Image, folder from the CLI, a zip onto a static site, functions | Image, or a folder with `skifity up` (`internal/cli/up.go`) | parity; Levelrail adds functions |
| Builders | BuildKit library, Railpack, static path; SBOM per Dockerfile build | Railpack, Nixpacks, Dockerfile, static (`internal/builder/detect.go`); image scan with a gate (`internal/vulnscan`); no SBOM | parity; behind on SBOM (small) |
| Prebuilt image from CI | Composite GitHub Action, CI OIDC trust (unit tests only), registry push events, image auto-update | Deploy body is `commit_sha`, `force`, `accept_vulnerabilities` (`internal/api/deploy_handlers.go:19-26`); no CI section in `docs/cli.md` | **behind** |
| Cutover | Blue-green, rolling or recreate; previous release held for the watch | Rolling with `MaxSurge: 1` (`internal/kube/manifests.go:198`), recreate for apps with a volume; grace fixed at 30 s (`:130`) | behind on a single switch (`kamal.md` P2, still open) |
| After cutover | 5-minute watch; crash loop, OOM, 5xx share roll back by themselves | Rolls back only an app with a volume left with nothing running (`internal/deploy/deployer.go:1075-1112`) | **behind** |
| Approvals and freezes | Second-person approval on protected environments, scheduled freeze windows, deploy policies | A manual lock per app with a reason (`internal/store/migrations/0024_deploy_locks.sql`, Phase 94); no approval anywhere outside plugins | **behind** |
| Rollback | Pinned digest, previous release still running, optional database snapshot first | Old image and its runtime settings, plan shown first (`internal/deploy/deployer.go:840`, Phase 94) | parity |
| Canary, split tests | Share of traffic with automatic rollback; sticky split tests | None (`kamal.md` P2, still open) | behind |
| **Domains, TLS and routing** | | | |
| Certificates | Embedded Caddy, internal certificate until ACME is switched on, one live ACME run | cert-manager HTTP-01 (`internal/cluster/components.go`), the team's own certificates (`internal/tlscert`), issued against a test ACME server on every push (`docs/roadmap.md`) | ahead on evidence |
| DNS | Cloudflare, Route 53 and more; apex and www plan; domain doctor | Cloudflare, Hetzner, DigitalOcean, Route 53 (`internal/dnsprov/dnsprov.go`); a DNS check per domain (`internal/api/domain_dns.go`) | parity |
| Request firewall | Rules, lists, bans, browser challenge, attack mode, log-only replay | Allow, block and rate limit by address, country, network and path (`internal/edgerules/edgerules.go`, `internal/guard`) | parity; behind on challenge and log-only |
| Maintenance | Scheduled maintenance; spend cap pause | Maintenance answered by the guard (`internal/api/maintenance_handlers.go`, Phase 98) | parity |
| **Databases and services** | | | |
| Engines | 13 defined, 9 counted as supported; PostgreSQL flavours and a pooler | Nine (`internal/dbsvc/engine/engine.go`); PostgreSQL highly available through CloudNativePG (`internal/dbsvc/manifests.go`) | parity; ahead on HA |
| Newer engine version | Minor and patch in place with verify and revert; PostgreSQL major by dump and restore | Chosen at creation (`docs/databases.md`); nothing in `internal/dbsvc` changes it | **behind** |
| Password change | Scheduled rotation, four engines | On demand, with the old password kept beside the new where the engine allows (`internal/dbsvc/password.go`) | parity; behind on a schedule (small) |
| Look inside | Explorer and viewer | A tunnel, `skifity db connect` (Phase 105) | behind |
| **Storage and backups** | | | |
| Encryption | age and zstd on by default for volumes; per target for dumps; key file on the server | Sealed with a passphrase when set (`internal/sealed`, Phase 104) | parity |
| Verification | Size, SHA-256, a restore into a scratch database and a table count, **weekly by default**; "last verified restore" column; alert on failure | On request only: download, open, read the gzip to its end (`internal/backup/verify.go`, called only from `internal/api/verify_handlers.go`) | **behind** |
| Point-in-time | PostgreSQL stable, MySQL and MariaDB beta | Dumps only (`temps.md` P2, still open) | behind |
| Volume files | Browse, download, upload, restore one file | Config files mounted into an app (`internal/mcpserver/files.go`); no browser for a volume | behind (small) |
| **Scaling and high availability** | | | |
| More instances | Replicas, autoscaling beta | Instances, HPA, KEDA scale to zero (`internal/kube/apply.go:48`, `internal/kube/scaletozero.go`) | ahead |
| Scale to zero | Sleep when idle, stable on one machine | KEDA HTTP interceptor (`internal/kube/scaletozero.go`) | parity |
| Several servers | Own agent and mesh, "unverified" across hosts | k3s joined over SSH, WireGuard between servers, run across two servers and a lossy link (`.github/workflows/servers.yml`) | **ahead** |
| **Observability** | | | |
| Logs | Searchable, saved queries, error signatures, archive | Live logs with the previous container; drains to eight kinds (`internal/logdrain/logdrain.go:32-41`) | behind on search; ahead on drains |
| Metrics and requests | Node-local series, traces, web vitals, error tracking | Usage history and Traefik's requests, errors and response times per app (`internal/traffic`, `internal/watch/usage.go`) | behind on traces and errors, by design |
| Uptime | Status page stable; monitors flagged; heartbeat ping-out | `app.unhealthy` from readiness; no status page, no outside probe, no heartbeat (`internal/notify/notify.go`) | **behind** (`temps.md` P1) |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | Passkeys, TOTP, 17 OIDC providers, SAML and SCIM flagged | Argon2id, TOTP, passkeys, OIDC with group mapping (`internal/auth`, `internal/api/sso_groups.go`); SAML refused by decision (`docs/configuration.md:565`) | parity |
| Authorization | Roles, IAM Allow and Deny, custom roles flagged | Owner, admin, member, viewer, project limits, scoped tokens (`internal/auth/scopes.go`) | different; fewer moving parts |
| Ids in request bodies | Two High advisories (0001, 0006), now a helper and a guard test | Few bodies name a resource, and those read are authorized (`internal/api/data_handlers.go:182-201`, `integrations_handlers.go:639`, `cloud_handlers.go:261`); no test makes a new handler do the same | parity today; behind on the guard |
| Audit log | Caller surface, hash chain, verify, CSV, forwarding | Actor, address, user agent (`internal/store/models.go:672`); the remote MCP adds "(MCP)" to the agent (`internal/api/mcp_handlers.go`), the local one sends the CLI's (`internal/cli/client.go:176`) | behind |
| Release integrity | Cosign keyless signature verified by the installer and the upgrade; unsigned refused once pinned | SLSA provenance on binaries (`.github/workflows/release.yml:92-112`); the image is pulled by tag (`installer/install.sh:80-81`) and an upgrade changes the tag with no check (`internal/api/upgrade.go`) | **behind** |
| Failed upgrade | Puts the old binary and data back by itself | Copies the database first and hands back the commands (`internal/api/upgrade.go`, Phase 90) | behind (`sailbox.md` P2) |
| Telemetry | None | None ("It never phones home", `README.md`) | parity |
| **Preview environments** | Per PR, three forges, fork approval, comment commands | Per PR, comments, preview-only values, seeded databases (`internal/api/webhook_handlers.go`, `internal/api/preview_manual.go`, Phases 93 and 107) | parity |
| **Templates** | 359, 360 or 442 by page; 101 of 108 booted in a published local run; weekly smoke unpublished | 374 (`internal/templates/catalogue`); one, Homer, installed on every push (`test/cluster/verify.sh:2887`) | parity on count; behind on proof |
| **CLI, API, IaC, integrations** | | | |
| MCP tool list | 259 tools, ~2,400 tokens by default, toolsets, list follows the token | **46 tools, ~16,700 tokens** in every session, 40% of it output schemas; the same list for every token (measured, see P1) | **behind** |
| MCP safety | Untrusted text wrapped; prompts; resources; confirm argument | Read and destroy annotations checked by test (`internal/mcpserver/server.go:358-372`); no wrapping, prompts or resources | behind |
| MCP over HTTP | Read tools only, flagged, OAuth | Every tool but `deploy_folder`, on by default, token only (`internal/api/mcp_handlers.go`) | ahead on reach |
| Environment as code | YAML plan and apply, flagged; Terraform provider | `skifity.yaml` with plan and apply, not flagged (`internal/blueprint`, Phase 108); no Terraform provider | ahead; behind on Terraform |
| Importing another platform | Coolify, Dokploy, CapRover, Dokku, Compose, Vercel, Kubernetes YAML | None in `internal/` (`temps.md` P1) | **behind** |
| Creating servers | Hetzner, DigitalOcean, AWS, Azure, GCP (beta) | Hetzner, DigitalOcean (`internal/cloud`) | behind (small) |
| Agent onboarding | Skill, `AGENTS.md` and `.mcp.json` from `init`, `--print-config`, device login | `llms.txt` (`llms.txt`), `claude mcp add` lines in `docs/cli.md:490-520`; login pastes a token (`docs/cli.md:40-50`) | behind |
| **Notifications** | 18 kinds, 15 never accepted by a real vendor | Ten kinds (`internal/notify/provider.go:88`), fourteen events (`internal/notify/notify.go`) | behind on count; parity on what is proven |
| **Multi-server and networking** | Agent dials out over mTLS | k3s agents and WireGuard (`internal/provision`), GPUs (`internal/kube/gpu.go`) | ahead |
| **Team and collaboration** | Organisations, quotas, approvals, access reviews | Teams, invitations, project limits, audit (`internal/api/invitation_handlers.go`, Phase 86) | behind on approvals |
| **Developer experience** | | | |
| Footprint | 200 MB control plane idle, Docker not counted | Panel 35 MiB idle; about 1,000 MiB for the whole install, k3s 748–780 MiB, measured on every push (`docs/performance.md`) | behind on total, by the choice of Kubernetes |
| Evidence | A label per feature; tier 4 empty | `docs/progress.md` says "not executed" where it applies; the `Cluster` and `Two servers` workflows run the real installer on every push (`docs/roadmap.md`) | ahead on real runs; behind on a per-feature page |
| Languages | Dashboard English and part Spanish; README in seven | Panel in en, id, hi, ru, zh-CN with a build gate (`web/src/locales`, `check:i18n`); README in English | **ahead** in the product; behind on the README |
| Upgrades | Breaking changes listed, migrations tried on a copy, automatic revert | Database copied, newer schema refused, commands to go back (`internal/api/upgrade.go`) | behind |

## Gaps worth closing in Skifity

### P1 — Make the MCP tool list cheap to load, and fit it to the token

* **What:** `tools/list` without output schemas unless asked (`skifity mcp --output-schemas`), with
  the structured content still in every result; only the tools the token's scopes allow; and a
  test in `internal/mcpserver` that fails when the listing passes a budget and names the heaviest
  tools.
* **Why:** measured on 2026-11-08 at `23ec07f` by building the binary and sending `initialize` and
  `tools/list` to `skifity mcp` over stdio: **46 tools, 66,618 characters, about 16,700 tokens**
  by Levelrail's own estimate (characters divided by 4). Output schemas are 40% of it;
  `deploy_folder` alone is about 2,000 tokens, 1,640 of them its output schema. Name, description
  and input schema alone come to about 8,400. Levelrail lists about 2,400 tokens by default and
  prices its whole 259-tool surface at 24,511 — about 95 tokens a tool against Skifity's 360 —
  because it drops output schemas ("about 60 percent of it") and lets the agent load toolsets.
  Every Skifity assistant session pays the full cost before it does anything. And a read-only
  token is shown `deploy_app`, `rollback_app` and `run_command`, learning only from the 403 that it
  cannot use them, which is both a wasted turn and an invitation to try.
* **Sketch:** go-sdk fills `OutputSchema` from the handler's output type; register with it cleared
  for the listing, behind a flag for clients that want it. On start (stdio) and per request
  (`/api/mcp`), ask the API what the token may do — `/api/me` or a small route beside it — and
  register only the tools whose routes those scopes reach; `TestTheToolsRequireWhatTheAPIRequires`
  already knows which route each tool calls. A `TestTheToolListFitsItsBudget` with the ceiling and
  the top five by cost on failure. Toolsets are not needed at 46 tools; revisit past 80.
* **Size:** S. **Without a cluster:** yes.

### P1 — Verify every backup on its own, and prove one restores

* **What:** run the existing verification after every successful backup; and once a week, restore
  each database's newest backup into a throwaway database, check it, and delete it. Show "last
  verified restore" beside "last backup", and raise `backup.restore_failed` when a drill fails.
* **Why:** Levelrail separates "an object was uploaded" from "a restore of it worked": a drill
  compares the SHA-256 recorded at backup time, replays the dump into a scratch container of the
  database's own image, counts tables, and runs weekly by default with an alert on failure. Skifity
  verifies only when somebody presses Verify: `VerifyBackup` has one caller,
  `internal/api/verify_handlers.go`. And its own comment says what that cannot prove: "What it does
  not prove is that the database would accept the dump, which only a restore does"
  (`internal/backup/verify.go`). A backup that has never been verified is what people find out
  about during the outage.
* **Sketch:** call `VerifyBackup` at the end of a successful backup in `internal/backup`. Drills as
  a Job in a scratch namespace, built from the restore Job (its image, its `backup-open` step for
  sealed backups) with a database pod of the same engine and version beside it, resource limits,
  and a deadline; the result in a new column (migration) and on the Backups tab; a notify event; an
  `errdoc` entry per way it fails. PostgreSQL, MySQL, MariaDB and MongoDB first; Redis and Valkey
  as "read to the end" only. A volume drill compares the file list with the archive.
* **Size:** S (verify every backup), M (drills). **Without a cluster:** the scheduling, the Job
  manifests and the recording, yes; the `Cluster` workflow already backs up and restores, and
  could run one drill.

### P2 — Watch a release after it takes traffic, and roll it back

* **What:** for a few minutes after a rollout completes, roll back to the previous deployment by
  itself when the new instances restart repeatedly or the share of 5xx answers passes a threshold
  after a minimum number of requests; record the deployment as an automatic rollback and notify.
  Off per app, on by default for apps with a domain.
* **Why:** Levelrail's post-deploy watch (5 minutes, 2 restarts, 20% of at least 20 requests) is
  one of its few features with a live end-to-end test. In Skifity a rolling update protects only
  the moment of cutover: a version that passes readiness and then crashes after a minute, or
  answers 500 to every real request, stays. `rollBackIfLeftDown` covers only apps with a volume
  that a failed deploy left with nothing running (`internal/deploy/deployer.go:1075-1112`).
  Skifity already has both signals: Traefik's requests and errors per app (`internal/traffic`) and
  the crash-loop problem code (`app.crash_loop`).
* **Sketch:** a watch row per deployment read by the minute tick in `internal/serverapp`; the
  existing `Rollback` with trigger `auto_rollback` (respecting deploy locks); an event and a line in
  the deploy log; settings on the app's Scaling tab. Depends on nothing else.
* **Size:** M. **Without a cluster:** the decision and the state machine, yes; `verify.sh` could
  deploy a version that answers 500 and wait for it to be rolled back.

### P2 — A second person before production, and freezes on a schedule

* **What:** an environment can be protected: a deploy or rollback to it from anyone — panel, CLI,
  webhook, assistant — waits as "awaiting approval" until a different admin approves it. Freeze
  windows as a schedule (a cron and a length, or dates) beside the existing manual lock.
* **Why:** Levelrail's approvals and freezes are stable with live tests, and agencies and teams are
  the audience it names for them. Skifity has a manual lock per app (`0024_deploy_locks.sql`) and no
  approval outside plugin permissions. With an MCP server that can deploy, "an assistant may
  propose a production deploy but a person ships it" is the line most teams will want drawn.
* **Sketch:** a `protected` flag on the environment; a deployment status before `queued`; an
  approve route that refuses the requester; the existing lock check in `Deploy` and `Rollback`
  extended to windows read with `internal/cron`; a notify event; a banner on the app. The MCP tool
  answers with the pending deployment rather than failing.
* **Size:** M. **Without a cluster:** yes.

### P2 — Sign the release, and check it before installing or upgrading

* **What:** sign the panel's image and `checksums.txt` with cosign keyless in the release workflow;
  the installer resolves the tag to a digest and installs by digest after checking the signature;
  the panel's upgrade checks the target the same way and refuses an unsigned one once a signed one
  has been seen; and an upgrade that does not come up puts the previous image back.
* **Why:** Levelrail verifies in the installer and on upgrade, pins after the first verified
  signature, and published LRSA-2026-0013 because falling back to a same-origin checksum let a
  stripped signature through. Skifity's binaries carry SLSA provenance
  (`.github/workflows/release.yml:92-112`), but the installer pulls the image by tag
  (`installer/install.sh:80-81`) and `upgradePanel` changes the Deployment's tag with no check and
  no automatic way back — "Kubernetes never rolls a Deployment back on its own"
  (`internal/api/upgrade.go`). `kamal.md` noted the unsigned image on 2026-11-08 and `sailbox.md`
  the missing automatic revert.
* **Sketch:** `cosign sign` on the image digest and the checksums in `release.yml`; in the
  installer, cosign when present and the digest from the signed checksums otherwise, never the tag
  alone; in the panel, verification with sigstore-go before patching the image, then a watcher that
  restores the previous image and the pre-upgrade copy if the new pod is not ready in time.
* **Size:** M. **Without a cluster:** signing and the installer's logic, yes (`test/smoke`); the
  revert needs one, and `test/cluster/upgrade.sh` is where it goes.

### P2 — Tell the assistant which text is not instructions

* **What:** results that carry text written by an app or a third party — logs, build output, events,
  commit messages, a domain check's answer — come back with control and bidirectional characters
  stripped, secrets redacted, and wrapped in a marked block that says it is data; the tool's
  `_meta` says so too.
* **Why:** Levelrail does this for every such tool, documents it as its prompt-injection threat
  model, and still advises a scoped token because "Wrapping reduces risk; it is not a guarantee".
  Skifity's `get_app_logs`, `get_events` and `get_deployment_history` hand the assistant raw text
  from the app, and the same assistant may hold a token that can deploy. Skifity's tool comments
  already reason about injection for passwords (`internal/mcpserver/databases.go:25`) and
  certificates (`domains.go:23`); this extends it to the text that carries it.
* **Sketch:** one wrapper in `internal/mcpserver` reusing `internal/logging`'s redaction patterns;
  a test that every tool returning app-written text uses it.
* **Size:** S. **Without a cluster:** yes.

### P2 — Move a database to a newer version

* **What:** change a database's version from the panel: patch and minor versions in place with a
  health check and a revert; PostgreSQL major versions through CloudNativePG's own upgrade or a
  dump into a new cluster that is linked once it answers.
* **Why:** Levelrail offers guarded minor and patch changes, PostgreSQL majors by dump and restore,
  and scheduled upgrades with end-of-life checks. In Skifity the version is chosen at creation
  (`docs/databases.md`) and nothing in `internal/dbsvc` changes it, so a database made on
  PostgreSQL 16 stays on 16 unless somebody dumps and restores by hand; CloudNativePG keeps minor
  versions patched (`internal/dbsvc/manifests.go:307`), the other eight engines do not.
* **Sketch:** a version field on the database's settings, refused across a major for engines with
  no safe path; a backup taken first; for StatefulSet engines the image changed and readiness
  awaited, with the previous image put back on failure; for PostgreSQL majors a new cluster
  restored from a fresh dump, then the link swapped. `errdoc` entries for each refusal.
* **Size:** M (in place), L (PostgreSQL majors). **Without a cluster:** the plans and refusals,
  yes; the moves need one.

### P2 — Importers, with the old server serving throughout

`temps.md` made this P1 on 2026-11-08, and it is still open: there is no importer in `internal/`.
Levelrail adds three things worth taking into that design. The old platform only ever receives
read requests, and apps are staged here stopped and unrouted until they pass their own health
check. A **parity report** compares the two copies before cutover, with a fix per difference. And
cutover goes **one domain at a time**, with a verifier that checks DNS, the certificate, the
redirect and the response after each. Its Coolify importer has never run against a real Coolify,
which is the warning: record a real instance's API answers before writing the reader.

### P3

* **Say where an action came from.** Record the surface — panel, CLI, local MCP, remote MCP, API —
  as a field of the audit event, and the MCP tool's name; today the remote endpoint appends
  "(MCP)" to the user agent and the local `skifity mcp` sends the CLI's (`internal/cli/client.go:176`),
  so an assistant's deploy reads like a person's. A hash chain with a verify route and CSV export
  after that. S, no cluster.
* **A guard test for ids in request bodies.** Levelrail's two worst advisories were ids read from a
  body and never authorized. Skifity's few such fields are authorized by hand today; a test that
  reads every request struct with an `_id` field and checks the handler calls an `authorize…`
  function would keep it so. S.
* **Log in from the CLI with a code.** `skifity login --device` prints a short code that a signed-in
  person approves in the panel, which mints the token, instead of creating one and pasting it
  (`docs/cli.md:40-50`). S–M.
* **Agent onboarding in the repository.** `skifity init` also writes an `AGENTS.md` section and a
  `.mcp.json`; `skifity mcp --print-config claude|cursor|vscode|zed`; a published skill. Overlaps
  with `temps.md` P2 ("Connect an assistant from the panel"). S.
* **`wait_for_deploy`, prompts and resources in MCP.** `deploy_app` returns at once and the
  assistant polls; a tool that blocks until the deployment ends, and a `diagnose_app` and an
  `explain_failed_deploy` prompt, would save turns. There is no `AddPrompt` or `AddResource` in
  `internal/mcpserver`. S.
* **A heartbeat out.** The panel calls a URL of the operator's every minute while it is healthy, so
  Healthchecks.io or Uptime Kuma notices when the whole server is gone — the case no notification
  from the panel can cover. S, no cluster. The status page and outside probes stay `temps.md` P1.
* **Publish the template smoke.** Boot the catalogue in batches on a schedule in the `Cluster`
  workflow and publish pass or fail per template, as Levelrail's status page does; today one
  template is installed on every push (`test/cluster/verify.sh:2887`). M, needs a cluster.
* **Hold an automatic deploy until CI passes.** Skifity reports a status back to the commit
  (`internal/deploy/gitreport.go`) but deploys a push at once; waiting for the commit's checks on
  GitHub and GitLab, with a timeout, is Levelrail's CI gate. S–M.
* **The README in the panel's languages.** The product speaks Indonesian, Hindi, Russian and
  Chinese and its front page does not; Levelrail has the opposite problem. S.
* **A volume file browser:** list, download and replace one file, and restore one file from a
  backup, through a short-lived pod that mounts the claim. M.

## Things to deliberately not copy

* **Breadth ahead of evidence.** A mail server, functions, an AI chat, GPU model serving, error
  tracking, traces, spend caps, a mobile API and 259 MCP tools, from one maintainer in under three
  months, with "real infrastructure: none found" on almost every row and seventeen features behind
  flags. Levelrail is honest about it, which is to its credit; the honesty does not make the
  features run. Skifity's rule — a feature is "not executed" until a run says otherwise, and the
  `Cluster` workflow runs the real installer on every push — is the better trade.
* **Automatic upgrades on by default** in a pre-1.0 project that releases every 1.4 days, and two
  pages that describe the default differently. Skifity's panel "never asks about new releases on
  its own" (`internal/cli/upgrade.go`); keep it, and if scheduled upgrades come, off until chosen.
* **An assistant inside the panel holding a root token.** LRSA-2026-0007 is what it costs. Skifity's
  `/api/mcp` builds each request with the caller's own token through the same router
  (`internal/api/mcp_handlers.go`); keep that the only way an assistant acts.
* **A second authorization language.** AWS-style Allow and Deny policies on top of roles, custom
  roles and per-environment grants; both of Levelrail's High authorization advisories are places
  where one of those layers was not consulted. Skifity's roles, project limits and scoped tokens
  are smaller and checked in one place each.
* **A `confirm` argument the model fills in.** The assistant can copy a name as easily as a person
  can; what stops it is the token's scopes and the client asking before a destructive tool, which
  Skifity's annotations already drive (`TestWhatCanDestroySomethingSaysSo`).
* **Its own agent, PKI and mesh.** Levelrail wrote a gRPC agent, certificate renewal and a
  WireGuard mesh, and calls the mesh unverified across hosts. Skifity's k3s does that part, and the
  `Two servers` workflow has run it across a link that drops packets.
* **SAML**, for the reason in `docs/configuration.md`.
* **Headline numbers that do not match their own pages.** "360 smoke-tested" over a page that
  counts 108; Skifity's tests that compare `llms.txt` with the tools and the docs' links with the
  pages they point at are the habit to keep.
* **Separate binaries for the CLI, the agent and the MCP server.** Skifity's one binary that is the
  panel, the CLI and the MCP server means the version an assistant runs is the version the panel
  runs.

## Sources

Read 2026-11-08 and in the first hour of 2026-11-09 UTC. The repository's own GitHub API
(`repos/glincker/levelrail`) refused this sandbox, so metadata came from the search API, files
from raw.githubusercontent.com, and the tree and releases from ungh.cc.

* GitHub search API: `repo:glincker/levelrail` (stars, forks, open issues, created and pushed
  dates, licence, topics); issues by date and open issues (42 and 13); pull requests in total
  (1,372), by `thegdsks` (1,031), by Dependabot (192), and by everyone else (bots only)
* https://ungh.cc/repos/glincker/levelrail/files/main (16,487 files at `2299805`; locales,
  migrations, catalogue, workflows)
* https://ungh.cc/repos/glincker/levelrail/releases (thirty most recent releases and dates)
* https://raw.githubusercontent.com/glincker/levelrail/HEAD/ — `README.md`, `CHANGELOG.md`,
  `ADOPTERS.md`, `MAINTAINERS.md`, `GOVERNANCE.md`, `AGENTS.md`, `llms.txt`,
  `.github/SECURITY.md`, `skills/levelrail/SKILL.md`, `.github/workflows/template-smoke.yml`,
  `.github/workflows/vps-smoke.yml`, `internal/store/migrations/0476_upgrade_policy.sql`,
  `internal/store/migrations/3210_upgrade_policy_auto_default.sql`
* The same, `docs/`: feature-status, known-limits, benchmarks, mcp, mcp-tool-surface, i18n,
  architecture, comparison, pricing, privacy, roadmap, security, security-advisories,
  threat-model, migrate-to-levelrail, coolify-exit-gap-report, import-kubernetes,
  prove-your-backups-work, backups-and-storage, managed-databases, deploy-safety, canary-deploys,
  split-testing, rollout-control, zero-downtime-deploys, rollback, upgrade-safely, multi-node,
  node-provisioning, node-scheduling, profiles, installing, first-15-minutes, what-you-need,
  status-page, monitors, heartbeat, observability, log-explorer, tracing, error-tracking,
  audit-log, identity-and-access, teams-and-roles, access-control, iam-design, sso-saml, scim,
  session-policy, emergency-access, platform-as-code, app-spec-reference, terraform-provider,
  github-actions, use-any-ci, ci-gate, pipelines, pr-comment-commands, supply-chain,
  secret-scanning, secret-rotation, vault-secret-references, firewall, abuse-defense,
  exposure-audit, host-firewall, templates-status, template-upgrades, compose-apps, functions,
  sleep-when-idle, autoscaling, load-balancing, volume-files, predeploy-snapshot,
  database-upgrades, database-explorer, postgres-pooler, image-auto-update, deploy-receipts,
  deploy-policies, command-palette, attention-center, doctor, fix-it, cli-reference, cli-tui,
  ai-assistant, ai-control, mcp-safe-setup, agent-tooling-audit, resilience, disaster-recovery,
  whole-server-recovery, control-plane-backup, spend-caps, usage-and-cost, white-labeling
* https://levelrail.com and https://levelrail.com/pricing (442 templates, "100 verified on their
  pinned image tag")
* https://levelrail.com/install.sh (requirements, cosign verification, profiles)
* https://docs.levelrail.com/llms.txt
* https://api.npmjs.org/downloads/point/last-month/levelrail-cli and
  https://registry.npmjs.org/levelrail-cli (3,393 downloads; first published 2026-10-05)
* https://discord.com/api/v9/invites/Ar5pcaZB99?with_counts=true (11 members)
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=levelrail (no results)
* https://api.osv.dev/v1/query for `github.com/glincker/levelrail` (no results)
* https://hn.algolia.com/api/v1/search for "levelrail", "levelrail.com" and "glincker" (no
  submission or comment about Levelrail)
* Skifity, for the measurement in P1: `go build ./cmd/skifity` at `23ec07f`, then `initialize`
  and `tools/list` over stdio to `skifity mcp`; sizes are the compact JSON of each tool
* Not reachable from here: the repository's GitHub API, advisory and discussion pages, Reddit, and
  web search (credits exhausted)
