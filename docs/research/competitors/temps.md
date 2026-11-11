# Temps

> **Status.** Written on 2026-11-08 against the code as it was that day. Nothing in Skifity was
> changed for it; the gaps below are proposals.

Temps (`gotempsh/temps`, temps.sh) is a self-hosted platform that deploys apps on Docker and runs
what surrounds them in the same process: managed databases, web analytics, session replay,
Sentry-compatible error tracking, OpenTelemetry ingest, uptime monitors and status pages,
transactional email, AI agent sandboxes and an AI gateway. Its own description is "AI-native
open-source alternative to Vercel + Sentry + PostHog + Pingdom + Resend + E2B". It is aimed at solo
developers and small teams on one VPS who would otherwise pay for several of those services.
**Architecture:** one Rust binary (127 crate directories under `crates/` on `main` on 2026-11-08;
the docs say 94 at v0.1.0-beta.57) that runs the API and React console (Axum), a reverse proxy built
on Cloudflare's Pingora, and the background workers. It needs Docker on the host for the apps and
for its own PostgreSQL with TimescaleDB; ClickHouse is optional and the installer now preselects it
for traces, metrics, proxy logs and analytics. `temps serve --role=console` plus `temps proxy` split
the console from the proxy so the console can restart without dropping traffic. Further servers run
`temps agent` with Docker and join over a private network with mutual TLS; **there is no
orchestrator**, and the docs say plainly that Temps "clusters worker nodes over your private
network, not Kubernetes". **Licence:** MIT or Apache-2.0 (the GitHub API reports Apache-2.0). A
separate, closed **Enterprise Edition** binary carries the Premium features; no Enterprise code is
in the public tree. **Pricing** (pricing page, updated 2026-11-03): self-hosted free with no limits;
**Premium $399 per organisation per month**, flat, for the Enterprise Edition (per-project request
firewall and IP lists, SAML, SCIM, custom roles, deploy approval gates, a hash-chained audit log
and SIEM streaming, Vault / AWS Secrets Manager / Infisical, OpenTelemetry forwarding, white
label); **Temps Cloud** $29 a month, an invite-only beta of a hosted control plane for the
customer's own servers, EU only. **Maturity:** created 2025-10-21; v0.0.1 on 2026-01-13; every
v0.1.0 build is a prerelease — beta.57 (2026-10-11), beta.58 (2026-11-03), beta.59 and beta.60
(2026-11-05), **beta.61 (2026-11-06)**, nightly `v0.1.0-nightly.20261107` — so the installer's
`stable` channel still resolves to **v0.0.8 (2026-03-30)**, and the multi-node guide warns that a
worker installed from it cannot join. Issues #1214 and #1195 (open) plan a v0.1.0 release
candidate and a GA scope. `CHANGELOG.md` holds 197 version entries. **Adoption** (2026-11-08): 905
stars, 80 forks, 348 issues of which 11 open, 1,395 merged pull requests of which 100 are from
people other than the maintainer and Dependabot; `@temps-sdk/cli` was downloaded 692 times from npm
in the month to 2026-11-07. The project is essentially one maintainer (`dviejokfs`; the blog is
signed David Viejo). It publishes comparison pages and posts that rank Temps first, such as
"Coolify Alternatives 2026: 5 Self-Hosted PaaS" (updated 2026-11-05).

## Feature inventory

Dates are the changelog entry that added a feature. "Nightly" means it was in a nightly build
before a beta.

### Deploy sources and builds

* **Sources:** GitHub (App or token), GitLab, Bitbucket, Gitea and any Git URL (the last three since
  beta.40, 2026-07-01); a registry image; a static bundle; an uploaded source archive ("drop",
  2026-08-04, which `temps up` uses); a locally built image pushed straight to the server with
  `deploy:local-image`; a Docker Compose project from Git or pasted (since 0.0.7-beta1, 2026-03-28;
  on worker nodes since 2026-10-29).
* **Builds:** framework presets that generate a Dockerfile (Next.js, Vite and other static sites,
  Python, Go, Rust, Java detected from files; Node, Laravel, Rails and others picked by hand);
  **Autopack**, Temps' own builder, replaced the Nixpacks binary on 2026-08-04 and reads
  `railpack.json` or `nixpacks.toml`; your own Dockerfile with BuildKit caching. Static sites are
  served by the proxy with **no container** (2026-08-31). Variables become build arguments; secret
  mounts are kept out of image metadata (fixed in beta.59).
* **Where builds run:** the control plane by default; on worker nodes since 2026-09-26 and
  2026-10-09; a bounded builder queue that falls back to another builder (2026-10-17); **native
  builds per architecture on mixed amd64/arm64 workers** (2026-10-13), and the scheduler refuses a
  node with no image for its platform.
* **Pipeline:** clone, build, start, health-check (`/` or `health.path` in `.temps.yaml`; two
  consecutive successes; 2 s after a success, 5 s after a failure; startup timeout 30–3600 s, 300 s
  by default, configurable since beta.57), switch the route, then post-deploy jobs (cron
  definitions, source-map capture, screenshots if on, Trivy if on).
* **Rollback** creates a new deployment from the target's image if it is still on the server, and
  otherwise **rebuilds the target's commit** for Git projects. It uses the environment's *current*
  variables. Nothing rolls back automatically; a failed health check simply never takes traffic.
* **Promotion** between environments (0.0.6-beta5, 2026-03-11); an environment marked
  **Protected** never deploys on push and only receives promotions.
* **CI:** deployment tokens, GitHub Actions and GitLab CI examples in the docs, `deploy:image`,
  "dispatch verified CI builds" (2026-10-17). Per-project image retention with nightly pruning
  (2026-08-17); a registry mirror prefix and Docker Hub rate-limit diagnosis (2026-09-06).

### Domains, TLS and routing

* **TLS in the proxy:** on-demand HTTP-01 issuance when a hostname is first opened (ADR-018,
  beta.35, 2026-06-19); wildcard DNS-01 in the installer's Advanced mode through Cloudflare,
  Route 53 or DigitalOcean; DNS providers Cloudflare, Namecheap, Azure and Google Cloud for records
  (2025-12-16), with ownership-guarded managed records (2026-10-04). An external-TLS mode leaves
  HTTPS to a tunnel or reverse proxy.
* **No domain needed:** QuickStart gives `console.<dashed-ip>.sslip.io` and
  `<project>.<dashed-ip>.sslip.io`, each with a **real Let's Encrypt certificate issued on demand**.
  sslip.io is not on the Public Suffix List (read 2026-11-08), so these certificates share one
  registered domain's limit across every user; sslip.io's README thanks Let's Encrypt "for bumping
  our rate limits many, many times", and Temps' troubleshooting page tells a user who hits the limit
  to wait or use a domain.
* Domain redirects, a per-environment HTTPS redirect, path routes, request timeouts per project that
  know about SSE and WebSockets (2026-08-12), a password wall, security-header presets (CSP,
  X-Frame-Options, HSTS, Referrer-Policy), real client addresses behind Cloudflare and Bunny CDN.
* **Request filtering:** an instance-wide IP block list (free); **per-project and per-environment
  allow/deny lists and a request firewall are Premium**; "Attack Mode", a proof-of-work challenge
  per project (Experimental); edge rules that block, allow, challenge, redirect or only log
  (nightly 2026-11-01); "Shield" with JA4 TLS fingerprints, reputation lists in log mode and a page
  per client address (2026-10-26 to 2026-10-28).
* **Reachability** (ADR-065, 2026-10-26): each address can be public, reachable only from a private
  network, served at a Tailscale name or as a Tailscale Service, limited to named tailnet users,
  put **behind the Temps sign-in**, or behind Cloudflare Access; worker apps can be published
  through a Cloudflare tunnel.
* Any host name that resolved to the server and matched no app showed the console login until
  #1671 was closed on 2026-10-29; whether the fix is a default or an option was not read.

### Databases and services

* **Managed services:** PostgreSQL (TimescaleDB image, WAL-G), MariaDB, Redis (a Dragonfly image
  can be swapped in), MongoDB, and S3-compatible storage (RustFS by default, Garage and MinIO,
  2026-10-17). HA PostgreSQL through pg_auto_failover (Experimental). Credentials are injected into
  linked projects. External services can be registered; data can be imported from an external
  database into a managed one (2026-10-09).
* A read-only **data browser** for PostgreSQL, MariaDB, MongoDB, Redis and S3, with a per-service
  opt-in for the AI assistant to read it (2026-08-06); a **Query Performance** page from
  `pg_stat_statements` with a reset (beta.56, 2026-08-21).
* App-facing services in the same binary: KV (Redis-backed) and blob storage with SDKs; feature
  flags (2026-08-04); **transactional email** through SES, Scaleway, SMTP or Cloudflare Email
  Sending, with DKIM and DMARC help, open and click tracking and a Mailhog-style capture; revenue
  tracking (Experimental).
* **No custom service types** — issue #1591 (open since 2026-10-21) asks for Qdrant.

### Storage and backups

* **App containers have no persistent volumes** (FAQ, import guides, #1591). Named volumes exist
  only in Compose projects and are not backed up. A volume model is designed in ADR-055
  ("Proposed", 2026-10-21) and the workload plane it describes merged into nightlies on 2026-10-29;
  the docs do not offer app volumes yet.
* **Backups go to S3 only**, per engine: PostgreSQL WAL-G base backups plus continuous WAL with
  **point-in-time recovery**; MariaDB `mariadb-backup` plus binary logs with point-in-time recovery
  (2026-09-02); Redis RDB; MongoDB `mongodump`; S3 by bucket mirror; the control plane by `pg_dump`
  with the observability tables dumped schema-only. Schedules take a six-field cron and must be at
  least an hour apart; retention in days. Restore in place or **into a new service** (2026-08-10).
  Pending migrations are backed up before an upgrade applies them (2026-10-06).
* Two data-loss bugs reached users: #849 (reported 2026-08-30, closed 2026-10-03), the published
  `docker-compose.yml` mounted the control plane's PostgreSQL volume where the image did not keep
  its data, so "we lost a complete Temps instance this way"; and #1415 (2026-10-12, fixed the same
  day), where **Run backup** on a managed PostgreSQL recreated the container to turn on WAL archiving
  and "all data was gone ~1 second later", while the console reported the backup completed — of the
  empty database.

### Scaling and high availability

* Replicas per environment, load-balanced by the proxy; CPU and memory limits (requests are
  recorded but not enforced). "**Temps does not auto-scale.**"
* **Scale to zero:** "on-demand" environments sleep after an idle time and wake on the first
  request, which the proxy holds (idle 300 s, wake 30 s by default); opt-in for new previews.
* **Multi-node** (Beta): workers join with a shared token or a single-use enrollment token, serve
  mutual TLS since beta.57, and are reached directly over a private network or a WireGuard mesh set
  up by pairing (there is no public relay). The scheduler is least-loaded (60% CPU, 40% memory) with
  anti-affinity, labels, target nodes and architecture; a node silent for 90 s is marked offline and
  its workloads fail over. Dedicated nodes (2026-10-10); a Mac joins as a worker through its Docker
  app, as the user and without sudo (beta.61, 2026-11-06). Traffic enters through the control
  plane's proxy, or through worker ingress and tunnels (since 2026-09-22).
* The control plane is one machine; "replaceable stateless installations" (2026-09-23) and split
  processes are the answers offered. Cluster CA rotation is a manual ceremony and node certificates
  are not renewed automatically.

### Observability (logs, metrics, alerts, uptime)

* **Logs:** container and request logs from every service in one searchable history with volume by
  level, remote nodes included, finished build logs archived to S3, a per-container ingest budget
  (2026-10-26).
* **Metrics:** CPU, memory and network per container with history; request rate and p95/p99 per
  project; node and control-plane metrics with CPU, memory and disk alerts.
* **Error tracking** (Beta): Sentry's envelope protocol, so Sentry SDKs work with a new DSN; only
  error events are stored ("Transactions, spans and session updates are accepted but not stored
  yet"); source maps; grouping by pgvector similarity; a "Fix with agent" button.
* **OpenTelemetry** (Beta): OTLP/HTTP traces, metrics and logs into TimescaleDB or ClickHouse;
  metric dashboards, threshold alerts and seasonal anomaly bands (beta.40, 2026-07-01).
* **Web analytics and session replay** (Beta): first-party cookies on the app's own domain,
  funnels, web vitals, AI-crawler traffic; rrweb replay off by default with inputs masked.
* **Uptime monitors** per environment (60 s by default) that open incidents; **public status pages**
  on custom domains (nightly 2026-10-13, platform-wide 2026-10-30, fixes in beta.58). The docs still
  say "Temps does not host a public status page". Cron monitors and check-ins have their own pages
  (beta.58).
* An AI chat that answers from a project's errors, logs and traces (Experimental).

### Security, auth, roles, SSO, audit

* Argon2 passwords, TOTP, step-up verification for sensitive actions (2026-08-04), admin reset of
  another user's MFA (2026-10-23). **OIDC SSO is free** (beta.21, 2026-05-24), with group-to-role
  mappings; SAML, SCIM and "require SSO" are Premium.
* Instance roles Admin and User; team roles Owner, Admin, Deployer, Viewer granted per project
  (free since 2026-08-04); API-key roles including a custom set; 144 permissions. No per-environment
  permissions. **No email invitations**: an admin creates each account and hands over a temporary
  password.
* Variables encrypted at rest (AES-256-GCM since 0.0.6-beta3); a write-only secret flag, and since
  beta.58 a suggestion to mark credential-looking values as secrets. Secret managers are Premium.
* Audit log free; hash-chained log and streaming are Premium. An **admin listener** binds the
  console and management API to a private interface with CIDR and Host allowlists.
* App containers cannot reach cloud metadata addresses (2026-08-02). Trivy scans are opt-in per
  project (Experimental). A **web terminal** into containers and sandboxes.
* **Anonymous telemetry is on by default** and can be turned off in Settings or with
  `TEMPS_TELEMETRY=0`.

### Preview environments and branches

* With previews on, every push to a branch other than the main one creates and deploys a preview
  environment (only if the project deploys automatically); no cap on their number; opt-in sleep
  when idle; sticky pull request comments (beta.22); commits previewed before deploying (beta.51);
  preview passwords encrypted (beta.7).

### Templates and catalogue

* Project templates and a one-click demo deploy that needs no Git account (beta.39); service
  templates from 2026-09-04, labelled Beta because they "support one application container with
  Temps-managed service bindings; multi-container template workloads are not available yet". The
  number of templates is not published; the vendor's own comparison says its marketplace is
  smaller than Coolify's.

### CLI, API, IaC, integrations

* **CLI** `@temps-sdk/cli` (0.1.38), run with `bunx`, about 60 command groups and "440+
  operations", JSON output. **REST API** with an OpenAPI document, RFC 7807 errors and a generated
  TypeScript client.
* **MCP:** a 210-tool server shipped in 0.0.5 (2026-02-27) and was rebuilt (ADR-039, 2026-08-26);
  since beta.57 the binary has a built-in MCP server, **off by default**, switched on in Settings,
  and `mcp add <client>` writes a client's configuration. Separately, MCP servers can be registered
  for agents running in sandboxes.
* **For coding agents:** installable skills (`bunx skills add gotempsh/temps`), `llms.txt`,
  `llms-full.txt` (1.7 MB of docs), `llms-cli.txt`, Markdown on `Accept: text/markdown`, and every
  docs task written three ways: a sentence to give an agent, the CLI, and dashboard steps ending in a
  "Check:".
* **`.temps.yaml`** in the repository: health path, cron jobs (HTTP GET to an app path with a
  `CRON_SECRET`), metric alerts and agent definitions. It does not describe the environment.
* **Importers** (Beta): Coolify, Dokploy, CapRover, Portainer, Kubernetes, Kamal and containers on
  the Temps host's own Docker, in a wizard and in `temps migrate`; Vercel from the CLI only. Each
  goes connect, discover, plan (nothing created), execute, verify.
* **AI:** an OpenAI-compatible gateway in front of OpenAI, Anthropic, Gemini, xAI and self-hosted
  providers, with budgets and per-project limits (Experimental); sandboxes on Docker, Firecracker or
  microsandbox (Experimental); autofix agents; an assistant that can deploy, create repositories and
  open pull requests, whose changes "run without asking, with an opt-in to confirm" (2026-10-23).
* External binary plugins with a signed registry (Experimental); Temps Cloud; Temps Fleet, a
  desktop app with an iPhone companion for agents and servers.

### Notifications

* Email, Slack and signed webhooks; routes by severity (2026-08-27); a weekly digest (beta.20);
  Cloudflare Email Sending as a mail provider (beta.40). Events cover deployments, error alerts with
  deep links, metric alerts with a chart in the email, node offline and recovered, resource
  pressure, disk, file descriptors, certificate renewal and backup lifecycle.

### Multi-server and networking

* See "Scaling and high availability" for joining workers. Containers share an overlay network;
  internal names `<environment>.<project>.temps.local` exist only on worker nodes, and the internal
  proxy refuses callers from another project, so two projects on a single-server install talk
  through public domains (#1591). Workers can have no public address (2026-10-23). Two open bugs say
  managed-service health probes fail under `docker-compose.yml` (#1804) and that a standalone
  Redis is bound to loopback and not reachable from other nodes (#1782).

### Team and collaboration

* Teams with project grants, intersected with the instance role; protected environments; "project
  groups" that share a repository, variables and routes (nightly 2026-10-14, Experimental). Approval
  gates and custom roles are Premium. No invitations, no per-environment roles.

### Developer experience and onboarding

* `curl -fsSL https://temps.sh/deploy.sh | bash` (the docs advise downloading and reading it
  first): installs Docker, PostgreSQL, the binary and a systemd service; asks for Local, QuickStart
  or Advanced; idempotent; headless with `--yes`, which writes `~/.temps/setup-result.json` (mode
  0600) with the console URL, admin password and an API key; `--admin-password-file` keeps the
  password out of shell history. Minimum **2 cores, 4 GB, 20 GB**; Ubuntu, Debian, RHEL and macOS.
  There is no uninstall procedure in the docs (`llms-full.txt` mentions uninstalling only for
  Firecracker; #1131 asked).
* A getting-started checklist, a one-click first deploy (2026-10-05), a project tour (beta.53), a
  "Connect your coding agent" page at `/setup/ai` that creates a dedicated key and waits for the
  agent's first request, and **feature maturity labels**: a registry built into the binary that the
  console (badges), the CLI (a one-time notice) and the OpenAPI document (`x-maturity`) all read.
* The README is in seven languages; no translation files were found in the console's source.

## What users love

Independent reviews were not found: Hacker News has no submission of temps.sh or the repository
(Algolia, 2026-11-08), Reddit refused this sandbox, and the web-search credits ran out. What there
is comes from the issue tracker and is weaker evidence than the other files in this directory.

* **Running real things on it.** Several reporters write from production: #1415's reporter ran
  "a production service holding live data" with an independent nightly `pg_dump`; #849's lost a
  whole instance and still sent the fix. Two outside accounts have 36 and 26 merged pull requests
  (2026-11-08).
* **The bundle.** The most-reacted requests extend it rather than question it: pluggable S3 storage
  (#140), Mailtrap as an email provider (#1252), deSEC for DNS (#1605), the multi-node work (#30,
  six reactions).
* **Fixes arrive fast.** #1415 was fixed in under 14 hours, #1671 in under 2, and the automated QA
  pass in beta.59 closed twenty issues at once (#1779). The pace is visible: a nightly almost every
  day from September.
* **Price shape.** No per-seat or per-event charge on any plan; self-hosting is free with teams,
  OIDC, audit and HA databases included (pricing page).

## What users complain about

* **Rough to install and run.** "Couldn't find anything in the docs. Project looks interesting, but
  it's so bugged that it's currently unusable. The desktop app is full of dead ends, the installer
  is bugged & leaves you half-way into things" (#1131, 2026-09-24, asking how to uninstall).
  Installer failures on aarch64 (#180), a Raspberry Pi 5 (#119), Ubuntu 24 (#15), an existing
  TimescaleDB (#743), SELinux and `--yes` stopping at a menu (#1026).
* **Data safety.** #849 and #1415 above: both lost every row, and in #1415 the backup that ran
  afterwards was a backup of the empty database.
* **No volumes, no custom services, no cross-project names** on one server (#1591): "Every deploy
  starts with a fresh filesystem, so large files (ML model weights, caches, generated files) are
  downloaded or rebuilt on every deploy".
* **Multi-node is young.** Probes failing on workers (#1772, #1804), a loopback-bound Redis
  (#1782), a 10-second completion gate shorter than the node agent's 30-second sync (#1783), image
  imports timing out at 120 s (#1781).
* **The console says yes when the server said no.** Several reports of refused actions shown as
  success: API key edits, routes, AI gateway keys, alert rules (#1722, #1748, #1792, all November
  2026, fixed).
* **Exposure by default.** Unknown hosts reached the console login (#1671). The docs carry warnings
  instead of fixes in places: the CLI's `--cpu` flag is documented as millicores and stored as
  microcores, so `--cpu 1000` asks for 0.001 of a core; `PUT /api/settings` replaces the whole
  settings document, so a partial body "can overwrite unrelated production settings".
* **Docs that disagree with the build.** The feature page says there is no public status page
  while beta.58 ships one; the rollback picker "usually offers only the deployment that is already
  live".

## Security record

**Where I looked (2026-11-08):** NVD keyword searches for "gotempsh" and "temps.sh" (no results);
the GitHub search API over the repository's issues and pull requests for CVE, GHSA, security, SSRF,
privilege escalation, injection and tenant; `SECURITY.md`; the changelog's one Security section.
The repository's advisory page could not be read from here, so whether GitHub advisories exist is
**not known**.

* **No CVE and no advisory found.** `SECURITY.md` promises advisories and CVEs for fixed
  vulnerabilities and a 30-day fix target for high severity, but its support table covers only
  1.x lines and says pre-1.0 releases and betas are **not supported**. Every release is pre-1.0, so
  no line is supported today. Reports go to the maintainer's email; issue #1216, an external
  penetration test, is open.
* **Findings fixed in the open, as issues and pull requests:**
  * PR #402 (July 2026) closed "the anonymous-access CRITICAL" on all twelve `/api/proxy-logs*`
    handlers; #403 then found any authenticated user could read another project's proxy logs —
    "for Temps Cloud multi-tenant it is a real HIGH" — fixed 2026-09-14.
  * #705 (2026-08-17): AI-gateway usage endpoints returned any project's usage and deployment token
    IDs to any project member.
  * #560 and #561 (2026-08-05): a private-address guard defeated by DNS rebinding, and a cluster
    admin password sent in cleartext, chainable to a credential leak.
  * #1123 (2026-09-24): a stale host port could route one project's traffic to another workload.
  * #646 (2026-08-13): no per-upstream connection cap, a cross-tenant denial of service with the
    opt-in timeouts.
  * #1583 (2026-10-20): "an authorized security assessment", 33 findings — 1 critical, 2 high, 10
    medium, 19 low, 1 info — across the plugin proxy, authorization tiers, outbound requests,
    encryption at rest and webhook ingest. #1602 (2026-10-22): twelve more (F1–F12), among them
    host-Docker import open to non-admins and restores from incomplete backups.
  * #1640 (2026-10-26): terminal and workspace WebSockets did not check `Origin`; #1599: read-only
    API keys could change their owner's AI conversations; #1607: sandbox creation accepted any
    memory and PID limit from a user.
* **Dependencies** are patched promptly: RUSTSEC issues are filed by a bot and closed (eleven
  closed on 2026-11-02), Dependabot batches most days, a ClickHouse pin for
  CVE-2026-116366 (#1705), and Redis 8.4.0 in its own Redis image replaced for two CVEs (#1844).
* **Exposure by design:** the console, ingest endpoints and proxy are one process and listen
  publicly unless the admin listener is set up; telemetry is on by default; the web terminal; the
  AI assistant has write tools; app containers may be granted the host Docker socket for
  "operator-owned infrastructure services" (2026-09-22); multi-node keeps a shared join token valid
  by default.

## Against Skifity

Temps is the closest thing in this directory to a self-hosted Vercel plus its observability
add-ons; Skifity is a Kubernetes platform that leaves observability to apps it installs. Rows say
what a user gets. The Skifity column was checked in the named files on 2026-11-08.

| Capability | Temps | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git providers | GitHub, GitLab, Bitbucket, Gitea, any Git | GitHub, GitLab, Gitea, Bitbucket (`internal/gitsrc/webhook.go`, `internal/api/bitbucket.go`) | parity |
| Without a repository | Image, static bundle, source archive, local image pushed to the server | Image, or a folder with `skifity up` (`internal/cli/up.go`); no push of a local image | behind (small) |
| Builders | Presets, Autopack, Dockerfile; static sites with no container | Railpack, Nixpacks, Dockerfile, static directory in a small web server (`internal/builder/detect.go`) | parity |
| Compose | Stack from Git or pasted, named volumes | Stack of apps reaching each other by name (`internal/api/stack_handlers.go`, Phase 101) | parity |
| Mixed architectures | Native build per worker architecture; scheduler refuses mismatched nodes | Server architecture recorded (`internal/provision/provisioner.go:448`) and never used to place a pod: no `kubernetes.io/arch` in `internal/` | **behind** |
| Prebuilt image from CI | `deploy:image`, `deploy:local-image`, CI examples in the docs | Deploy body has `commit_sha`, `force`, `accept_vulnerabilities` only (`internal/api/deploy_handlers.go:20-25`); no CI section in `docs/cli.md` | **behind** |
| Rollback | Old image or a rebuilt commit, with *current* variables | Old image *and its settings*; refuses a collected image (`internal/deploy/deployer.go` `Rollback`, `internal/errdoc/catalogue.go:889`) | ahead on settings; behind on old versions |
| Promotion | Promote; protected environments take only promotions | Promote (`internal/api/promote_handlers.go`); auto-deploy per app (`internal/store/models.go:310`) | parity |
| Drain and stop | Stop timeout per project (#1806), request timeouts per project | Fixed 30 s grace, 5 s preStop (`internal/kube/manifests.go:79,130`) | behind (small) |
| **Domains, TLS and routing** | | | |
| HTTPS on a fresh install | Let's Encrypt on the sslip.io names, on demand | sslip.io over HTTP by decision (ADR-0015); a domain, or `SKIFITY_DOMAIN`, for HTTPS | different; Temps nicer on day one |
| Certificates | On-demand HTTP-01, DNS-01 wildcard in the installer | cert-manager HTTP-01 (`internal/cluster/components.go`), the team's own certificates (`internal/tlscert`) | parity |
| DNS automation | Cloudflare, Namecheap, Azure, Google Cloud; Route 53 and DigitalOcean in the installer | Cloudflare, Hetzner, DigitalOcean, Route 53 (`internal/dnsprov/dnsprov.go`) | parity |
| Request firewall | Instance block list free; per-project rules Premium; challenge, log-only, JA4 | Per-app allow, block and rate limit by address, country, network and path, free (`internal/edgerules/edgerules.go:56-66`, `internal/guard`) | ahead on licence; behind on challenge and on seeing what it blocked |
| Gated access | Private network, tailnet, behind the Temps sign-in, Cloudflare Access | Password in front of an app (`internal/kube/password.go`), internal apps, Cloudflare tunnel (`internal/cluster/tunnel.go`) | behind on a sign-in gate |
| Security headers | Presets per project and instance-wide | None set by the panel (nothing in `internal/kube`) | behind (small) |
| Unknown hosts | Showed the console login until #1671 was closed (2026-10-29) | Panel Ingress bound to its own host (`deploy/ingress.yaml`) | ahead |
| **Databases and services** | | | |
| Engines | PostgreSQL, MariaDB, Redis, MongoDB, S3 storage | Nine: PostgreSQL, MySQL, MariaDB, MongoDB, Redis, Valkey, Dragonfly, ClickHouse, Memcached (`internal/dbsvc/engine/engine.go:21-29`); object storage only as a template | ahead on engines; behind on managed buckets |
| High availability | pg_auto_failover, Experimental | CloudNativePG with several instances (`internal/dbsvc/manifests.go`) | ahead |
| Loading existing data | From a reachable external database | A dump file, `skifity db import` (`docs/databases.md`, `internal/api/api.go:572`) | parity |
| Look inside a database | Read-only data browser; slow queries from `pg_stat_statements` | A tunnel, `skifity db connect` (`internal/api/api.go:575`); no browser, no query statistics | behind |
| Error tracking, analytics, email, AI gateway, flags | In the binary, mostly Beta or Experimental | Separate apps from the catalogue: GlitchTip, Bugsink, Plausible, Umami, Rybbit, OpenPanel, SigNoz, LiteLLM, useSend, Listmonk, Unleash, Flagsmith (`internal/templates/catalogue/`), not wired to other apps | different by design |
| **Storage and backups** | | | |
| App volumes | None (Compose only, not backed up) | Volumes with backup and restore (`internal/api/api.go:544-548`) | **ahead** |
| Database backups | S3, schedules, retention | S3, sealed with a passphrase, verifiable (`internal/backup`, `docs/backups.md`) | ahead |
| Point-in-time recovery | PostgreSQL (WAL-G), MariaDB (binary logs) | Dumps only; no WAL archiving in `internal/dbsvc` or `internal/backup` | **behind** |
| Restore into a new database | Yes | In place (`internal/api/data_handlers.go` `handleRestoreBackup`); a preview can be seeded from a backup (Phase 107) | behind (small) |
| Control-plane backup | Scheduled `pg_dump`, before upgrades | Scheduled off-site panel backup (Phase 84) | parity |
| **Scaling and high availability** | | | |
| More instances | Replicas; no autoscaling | Instances, HPA, scale to zero through KEDA (`internal/kube/apply.go:48`, `internal/kube/scaletozero.go`) | **ahead** |
| Scale to zero | Proxy holds the first request | KEDA HTTP interceptor (`internal/kube/scaletozero.go:27-29`) | parity |
| Server failure | Workloads fail over after 90 s | Kubernetes reschedules | parity |
| **Observability** | | | |
| Logs | Searchable history across services, archived to S3 | Live logs with the previous container; drains to eight kinds (`internal/logdrain/logdrain.go:31-40`) | behind on search; ahead on drains |
| Metrics and requests | Container history, p95/p99 per project | Usage history and Traefik's requests, errors and response times per app (`internal/traffic`, `internal/watch/usage.go`) | parity |
| Uptime and status pages | HTTP monitors with incidents; public status pages | `app.unhealthy` from readiness (`internal/watch/watch.go` `checkApp`) and error-rate alerts; nothing probes the public address; no status page | **behind** |
| Scheduled job failures | Cron monitors and check-ins | No event for a failed scheduled command (`internal/notify/notify.go:57-99`) | behind |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | Argon2, TOTP, step-up; OIDC free; SAML Premium | Argon2id, TOTP, passkeys, OIDC with group mapping (`internal/auth`, `internal/api/sso_groups.go`), re-authentication for three actions (ADR-0020) | ahead |
| Roles | Instance plus four team roles; custom roles Premium | Owner, admin, member, viewer; members limited to projects (`internal/store/models.go`, Phase 86) | parity |
| Secrets | AES-GCM, a secret flag; secret managers Premium | Sealed to their context and write-only; Vault, AWS, Doppler, Infisical free (`internal/crypto`, `internal/secretmgr`) | ahead |
| Panel exposure | Admin listener with CIDR and Host lists | API tokens limited by network (`internal/settings/settings.go:179`); the browser sign-in has no network limit | behind (small) |
| Terminal | Web terminal (#1640 Origin bug) | None, by decision | deliberate |
| Telemetry | On by default | "It never phones home" (`README.md:276`) | ahead |
| Known vulnerabilities | No CVE or advisory found; many findings fixed in the open | — | — |
| **Preview environments** | Per branch, sleep when idle, PR comments | Per pull request, comments, preview-only values, seeded databases (`internal/api/webhook_handlers.go`, `internal/api/preview_manual.go`, Phases 93 and 107) | ahead |
| **Templates** | Single-container service templates, count not published | 374 templates, multi-service included (`internal/templates/catalogue`) | ahead |
| **CLI, API, IaC, integrations** | | | |
| Breadth | ~60 command groups, OpenAPI client | About 40 commands (`internal/cli/commands.go`), `--json` | behind |
| MCP | Built in, off by default; `mcp add <client>` | Local and served at `/api/mcp` (`internal/mcpserver`, `docs/cli.md:492-514`); no setup page in the panel | parity; behind on setup |
| Environment as code | `.temps.yaml`: health, cron, alerts | `skifity.yaml` with plan and apply (`internal/blueprint`) | **ahead** |
| Importing another platform | Seven sources plus Vercel | None: no importer in `internal/`; Coolify gap 7 was never closed (`docs/research/competitors/coolify.md`) | **behind** |
| Creating servers at a provider | Not found | Hetzner, DigitalOcean (`internal/cloud`) | ahead |
| **Notifications** | Email, Slack, webhook; severity routes; digest | Ten kinds (`internal/notify/provider.go:89`), fourteen events (`internal/notify/notify.go:96-99`) | ahead |
| **Multi-server and networking** | Own agent with mTLS, private network or WireGuard; Mac workers | k3s joined over SSH, WireGuard between servers (`internal/provision`, `internal/settings/settings.go`); GPUs (`internal/kube/gpu.go`) | ahead |
| **Team and collaboration** | Admin creates accounts; no invitations | Invitations (`internal/api/invitation_handlers.go`), audit (`internal/api/api.go:345`) | ahead |
| **Developer experience** | | | |
| Install | Interactive or headless, JSON result file, macOS too; 2 cores / 4 GB | One command, setup token; 2 GB (`README.md:102-116`); no machine-readable result | ahead on size; behind for agents |
| Uninstall | Not documented | `skifity-uninstall` (`installer/uninstall.sh`) | ahead |
| Feature maturity | Registry read by console, CLI and OpenAPI | Beta badges on templates and components only (`web/src/pages/templates.tsx:354`, `web/src/pages/settings.tsx:1046`) | behind (small) |
| Languages | Console English | en, id, hi, ru, zh-CN with a build gate (`web/src/locales`) | ahead |

## Gaps worth closing in Skifity

### P1 — Import from Coolify, Dokploy, CapRover, Portainer, Kamal and a Kubernetes cluster

* **What:** `skifity import <source>` and a panel wizard that connect to another platform, discover
  what runs there, show a plan in which nothing has been created yet, then create the apps,
  databases, domains and variables, copy database data where it can, deploy, and say the import is
  done only once the app answers.
* **Why:** Temps makes migration its headline: seven sources, the same five steps for each, and a
  plan that names what will not move (Secret values, Redis data, volume files, platform-generated
  domains). Coolify's own docs say it has no export. Skifity has no importer at all — Coolify gap 7
  was proposed on 2026-09-30 and `openship.md` notes it is still open — so somebody leaving Coolify
  or Dokploy rebuilds every app by hand. Kubernetes is where Skifity can do better than Temps:
  Temps drops replicas, HPAs and PVCs because it has nothing to put them in, and Skifity has all
  three.
* **Sketch:** a new importer package under `internal/` whose readers turn a source into a
  `skifity.yaml` file and a list of data copies, so the existing `blueprint.Plan` and
  `skifity apply` path creates everything with the usual authorization and audit. Order: Coolify and Dokploy (largest audience,
  token APIs), a Kubernetes namespace by kubeconfig (Deployments, StatefulSets, CronJobs, HPAs,
  Ingress hosts, PVCs as volumes; Secrets as names only), then CapRover, Portainer and Kamal's
  `config/deploy.yml`. Data copy reuses the `db import` job, fed from a dump the job takes through a
  temporary tunnel rather than by asking the user to open a public port, which is what Temps asks
  of Coolify users. Each skipped item gets an `errdoc` entry. An MCP tool runs the plan.
* **Size:** L overall, M per source. **Without a cluster:** discovery and planning against
  recorded APIs in `httptest`, yes; creating and copying need one.

### P1 — Check the public address from outside, and a status page

* **What:** a monitor per domain that requests the public URL every minute through DNS, TLS and the
  ingress, opens an incident after a few failures and closes it on recovery; a public status page
  for chosen apps; and a notification when a scheduled command fails.
* **Why:** Temps has HTTP monitors with incidents and, since beta.58, status pages on custom
  domains. Skifity's `app.unhealthy` comes from ready replicas (`internal/watch/watch.go`
  `checkApp`), so a wrong DNS record, an expired certificate the watcher missed, a broken Ingress or
  a Traefik outage leave every app "running". A failed scheduled command raises nothing: there is
  no such event in `internal/notify/notify.go`. These are the things a person buys Uptime Kuma or
  Pingdom for, and the catalogue's Uptime Kuma and Gatus do not know the apps exist.
* **Sketch:** monitors and incidents in the store, probed from the panel's minute tick through
  `internal/netguard` with the app's own hostname; `app.down` and `job.failed` events in
  `internal/notify`; a status page rendered by the panel into a ConfigMap and served by a small
  static server in the cluster, so it stays up while the panel is down. A monitors section on the
  app's Domains tab.
* **Size:** M. **Without a cluster:** probing, incidents, events and the page, yes.

### P2 — Point-in-time recovery for PostgreSQL

* **What:** continuous WAL archiving to the team's backup bucket and "restore to a moment" for
  PostgreSQL, then binary logs for MySQL and MariaDB.
* **Why:** Temps archives WAL with WAL-G and binary logs with `mariadb-backup` and restores to a
  point in time. Skifity's backups are dumps on a schedule (`internal/backup`), so the most a
  database can lose is a whole interval. CloudNativePG, which Skifity already runs, does archiving
  and point-in-time restore itself through its backup plugin.
* **Sketch:** switch on CloudNativePG's object-store archiving per database with the credentials
  the backup policy already holds, sealed the same way; a restore that creates a new cluster at a
  time and links it after a check. A Backups tab field: "can be restored to any moment since".
* **Size:** L. **Without a cluster:** manifests yes; archiving and restore need one.

### P2 — Deploy a prebuilt image in one call, and a CI page

* **What:** `skifity deploy --image ghcr.io/acme/web:<sha>` and the same field on the deploy API
  and MCP tool; optionally `--from-local` that pushes a local image through the registry's guard;
  GitHub Actions and GitLab CI examples in the docs.
* **Why:** Temps has `deploy:image`, `deploy:local-image` and CI recipes; `kamal.md` proposed the
  same on 2026-09-30 and it is still open (`internal/api/deploy_handlers.go:20-25`, no CI section in
  `docs/cli.md`).
* **Sketch:** an `image` field recorded on the deployment so rollback restores exactly what ran;
  for a local image, a short-lived push token from `internal/registryguard`.
* **Size:** S (image), M (local push). **Without a cluster:** yes for the API and CLI.

### P2 — Place pods on servers of the right architecture

* **What:** pin an app's pods to nodes matching the architecture its image was built for, refuse
  with a reason when none matches, and later build for both.
* **Why:** Temps builds natively per worker architecture and its scheduler refuses mismatched nodes
  (2026-10-13). Skifity records each server's architecture and offers ARM servers at Hetzner, but
  nothing in `internal/kube` or `internal/builder` uses it, so an amd64 build can land on an arm64
  server and fail with an exec format error. Also open in `kamal.md`.
* **Sketch:** read the image's platforms after the build, add a `kubernetes.io/arch` node affinity
  in `internal/kube/manifests.go`, an `errdoc` entry, BuildKit `--platform` later.
* **Size:** S (pinning), M (multi-architecture builds). **Without a cluster:** pinning yes.

### P2 — Team sign-in in front of an app

* **What:** a protection mode that lets through only people signed in to this panel who belong to
  the app's team (or a named group), as an alternative to the shared password.
* **Why:** Temps can put any address behind its own sign-in or Cloudflare Access (ADR-065,
  2026-10-26). Skifity's only gate is one password per app (`internal/kube/password.go`), which is
  shared, never expires and leaves no trail of who looked at a preview.
* **Sketch:** Traefik forwardAuth to a panel endpoint that checks a short-lived cookie set on the
  app's host after a redirect through the panel, the same way an OAuth proxy works; SSO users come
  through the existing OIDC sign-in.
* **Size:** M. **Without a cluster:** the endpoint and cookie flow yes; Traefik needs one.

### P2 — Connect an assistant from the panel

* **What:** a page that creates a token for one assistant, prints the exact command for Claude
  Code, Cursor or Codex, and shows when that token makes its first call; `skifity mcp add <client>`
  writing the configuration for the user.
* **Why:** Temps' `/setup/ai` and `mcp add <client>`, and its skills package, make the agent path
  something a person can follow. Skifity serves MCP at `/api/mcp` and documents it in
  `docs/cli.md:509-514`, but the panel has no page for it.
* **Size:** S. **Without a cluster:** yes.

### P3

* **Rollback to a version whose image was collected** by building its commit with that version's
  settings, instead of refusing with `deploy.image_collected`
  (`internal/errdoc/catalogue.go:889`). Temps rebuilds the commit. S, no cluster.
* **See what the firewall did:** recent blocks and limits per app with counts, and a "log only"
  mode for a rule before it enforces. Today they reach only the guard's own log. Temps has a log
  action and a page per client address. M; the guard's counting is testable without a cluster.
* **Security headers per app:** HSTS, X-Frame-Options, Referrer-Policy and a CSP preset through a
  Traefik headers middleware. S; manifests without a cluster.
* **Slow queries and restore into a new database:** `pg_stat_statements` on CloudNativePG with a
  read-only page, and "restore as a new database" beside "restore". M and S; need a cluster.
* **Wire an app to an installed tool:** when GlitchTip, Bugsink or SigNoz from the catalogue is in
  the environment, offer to set `SENTRY_DSN` or `OTEL_EXPORTER_OTLP_ENDPOINT` on an app, the way a
  database is linked. This is Skifity's answer to Temps' bundle without putting it in the panel. M.
* **A drain and stop time per app**, still fixed at 30 s (`internal/kube/manifests.go:130`);
  Temps added a per-project stop timeout on 2026-11-04 (#1806). S.
* **A machine-readable installer result** for agents and CI: the panel URL and setup token in a
  0600 file, as Temps' `setup-result.json`. S, `test/smoke/installer.sh`.
* **Feature maturity labels** read from one list by the panel, the CLI and `llms.txt`, so "run on
  a real cluster" and "never run" are visible where a feature is used, not only in
  `docs/progress.md`. S.

## Things to deliberately not copy

* **Seven products inside the control plane.** Error tracking, analytics, replay, traces, email,
  sandboxes and an AI gateway share one process and one database with deployments. Most are Beta
  or Experimental by Temps' own registry, they set the 2 core / 4 GB minimum, and they are where
  its security reviews keep finding cross-project reads (#403, #705, #1583). Skifity's catalogue
  and log drains, plus the wiring proposed above, give the same result without making the panel a
  telemetry store.
* **Paying for the firewall, SSO and secret managers.** Per-project IP rules, SAML, SCIM, approval
  gates and secret managers are in the $399 Premium edition. Skifity gives its firewall, OIDC and
  secret managers away; keep that.
* **Telemetry on by default.** Skifity never phones home.
* **Every release a prerelease, and a security policy that covers none of them.** Write the support
  window down with each Skifity release, as `SECURITY.md` already does.
* **Recreating a database container to change its settings.** #1415 is what it costs when the data
  is not where the new container looks. Skifity's databases are CloudNativePG clusters and
  StatefulSets with claims; any change that replaces a pod must keep proving the claim is the same.
* **Rollback with today's variables**, and a picker that cannot reach older versions.
* **A whole-document `PUT` for settings** that documentation has to warn about.
* **A web terminal and AI write tools that act without asking.** Temps' assistant runs changes
  "without asking, with an opt-in to confirm"; Skifity's MCP tools carry destructive annotations and
  its terminal is a one-off command.
* **Certificates for the sslip.io names by default.** It does make the first minute nicer, and
  sslip.io's raised limit softens ADR-0015's worry; but the limit is shared with every Temps and
  Coolify install, and Temps' own troubleshooting names the failure. If Skifity offers it, it should
  be an installer choice, with a domain still the answer the panel recommends.
* **Asking a user to open a database's public port so the import can copy it**, which is what the
  Coolify importer requires. Copy through a tunnel the import closes afterwards.
* **Comparison pages that rank the author first.** Skifity's research stays in the repository and
  says where Skifity is behind.

## Sources

All read 2026-11-08.

* GitHub search API: `repo:gotempsh/temps` (stars, forks, dates, licence, topics); issues sorted by
  comments and reactions, open issues, issues by outside authors; issues and pull requests matching
  CVE, GHSA, security, SSRF, privilege escalation, injection and tenant; merged pull requests by
  author. Issue and pull request bodies read: #30, #140, #403, #560, #561, #646, #705, #849, #1123,
  #1131, #1210, #1214, #1216, #1415, #1583, #1591, #1602, #1671; titles of #15, #119, #180, #743,
  #1026, #1599, #1607, #1640, #1705, #1722, #1748, #1772, #1779, #1781, #1782, #1783, #1792, #1804,
  #1806, #1844
* https://ungh.cc/repos/gotempsh/temps/releases (thirty most recent releases and dates)
* https://ungh.cc/repos/gotempsh/temps/files/main (the file tree: crates, importers, ADRs)
* https://raw.githubusercontent.com/gotempsh/temps/HEAD/README.md
* https://raw.githubusercontent.com/gotempsh/temps/HEAD/CHANGELOG.md
* https://raw.githubusercontent.com/gotempsh/temps/HEAD/SECURITY.md
* https://raw.githubusercontent.com/gotempsh/temps/main/crates/temps-import-types/src/importer.rs and `plan.rs`
* https://raw.githubusercontent.com/gotempsh/temps/main/crates/temps-workload-volumes/src/lib.rs
* https://raw.githubusercontent.com/gotempsh/temps/main/docs/adr/055-workload-plane-and-runtime-drivers.md
* https://temps.sh/llms.txt, https://temps.sh/llms-full.txt (the documentation, pages read:
  overview, quickstart, choosing-a-server, deployments, rollbacks, scaling, multi-node,
  single-binary-architecture, backups, monitoring, cron-jobs, set-up-preview-deployments,
  authorization, security, mcp-servers, import-existing-platform, from-coolify, from-kubernetes,
  feature-maturity, troubleshooting), https://temps.sh/llms-cli.txt (`deploy:image`,
  `deploy:local-image`, `migrate`)
* https://temps.sh/pricing.md (updated 2026-11-03)
* https://temps.sh/blog/5-best-coolify-alternatives-self-hosted-paas-2026.md (updated 2026-11-05)
* https://api.npmjs.org/downloads/point/last-month/@temps-sdk/cli and https://registry.npmjs.org/@temps-sdk/cli
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=gotempsh and `keywordSearch=temps.sh` (no results)
* https://hn.algolia.com/api/v1/search for "temps.sh", "gotempsh" and by URL (no submission found)
* https://publicsuffix.org/list/public_suffix_list.dat (sslip.io absent)
* https://raw.githubusercontent.com/cunnie/sslip.io/main/README.md (rate-limit acknowledgement)
* Not reachable from here: the repository's GitHub API and advisory pages, Reddit, and web search
  (credits exhausted).
