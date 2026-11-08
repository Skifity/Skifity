# Openship

> **Status.** Written on 2026-11-08 against Openship's `main` at commit `430afc6`
> (2026-11-08) and npm release 0.9.11, and against Skifity's working tree the same
> day. No gap named here has been closed yet.

Openship (`oblien/openship`, openship.io, plus the hosted Openship Cloud) is a
self-hostable deployment platform: point it at a GitHub repository, a local folder
or a prebuilt image and it detects the stack, builds a Docker image (or a "bare"
release run as a supervised host process), starts it, and routes and
TLS-terminates it through an **OpenResty edge** with certbot. Its pitch is that
the control plane can live somewhere other than the servers it deploys to: the
same TypeScript code runs as a **desktop app** (Electron, for macOS, Windows and
Linux) that runs the control plane on the operator's laptop only while the app is
open and drives servers over SSH with nothing public on them; as a **self-hosted
server** installed by `openship up`, which on Linux with Docker is "Compose mode"
(Postgres, Redis, API, dashboard and the edge as containers on the box that also
hosts the apps) and elsewhere "bare mode" (one process with an embedded PGlite
database that deploys out over SSH); and as **Openship Cloud**, the managed
service. **Architecture:** a monorepo with a Hono API on Bun or Node 22, Drizzle
over Postgres or PGlite, Redis and BullMQ, Better Auth, a Next.js dashboard, a
Commander CLI, a Node SDK, and adapters for three runtimes (Docker, Bare, Cloud).
Since 0.8.0 there is a fourth: a **k3s cluster runtime** that Openship installs
across selected SSH servers, with CloudNativePG, the Redis Operator and Longhorn
underneath. In Compose mode the API container mounts the host Docker socket.
**Licence:** Apache-2.0 since 0.1.11 (2026-07-18); npm shows 0.1.0 and 0.1.1 as
MIT and 0.1.7 (2026-07-14) as "SEE LICENSE IN LICENSE", and a dev.to review
(2026-07-26) says it launched under AGPL-3.0 with the Commons Clause. The bundled
iRedMail mail engine is GPL. **Pricing** (openship.io/pricing, read 2026-11-08):
self-hosted is free; Cloud plans are $12 (1 vCPU, 4 GB, 50 GB, 3 projects), $24,
$47 and $119 a month, or $10/$20/$39/$99 a month billed yearly, each one managed
"Cloud Space" with no per-seat fees; **Manage**, the hosted control plane for your
own servers, is $5 a month per workspace with two servers included and $3 per
extra server ($20 with priority support). A competitor's review read the pricing
page in October 2026 at $5/$20/$39/$99, so the entry price has moved since.
**Maturity:** 45 npm versions from 0.1.0 (2026-03-05) to **0.9.11 (2026-11-06)**,
public from mid-July 2026 (first issues 2026-07-17), still 0.x; issue #216 "Heavy
tests needed for the stable release" (2026-07-24) is open. **Adoption** (read
2026-11-08): 15,237 GitHub stars, 1,400 forks, 629 issues (111 open), 795 merged
and 57 open pull requests (GitHub search API); 15,562 npm downloads of `openship`
between 2026-03-01 and 2026-11-07, 3,574 of them in the last month. The stars are
not matched by discussion: the most-reacted issue has 7 reactions, Hacker News has
one story (2026-07-19, 2 points, no comments) and two passing mentions, and the
Product Hunt page lists no reviews. The company is Oblien LLC, which also sells
cloud VMs for AI agents; Openship Cloud runs on Oblien's API.

## Feature inventory

### What shipped, release by release

From `CHANGELOG.md` (headings undated) and npm publish times.

| Release | Date (2026) | What changed that matters for a comparison |
|---|---|---|
| 0.1.7 – 0.1.11 | 14–18 Jul | Public launch; licence moves to Apache-2.0 |
| 0.2.0 – 0.5.0 | 20–31 Jul | Catalogue of 15 apps, service-based projects, per-server Git credentials, prebuilt Docker images (#151) |
| 0.6.1 – 0.6.9 | 7–30 Aug | One credential store, inbound mail, instance transfer with credentials, Compose reconciliation |
| 0.7.0 – 0.7.2 | 3–5 Sep | Several GitHub Apps per self-hosted workspace, DNS plan/apply for service domains |
| **0.8.0** | 27 Sep | **Server clusters on k3s**, managed WireGuard networking, 1–100 replicas, clustered PostgreSQL and Redis, Longhorn shared files, Node SDK, release commands, wildcard HTTPS by DNS, project export |
| 0.8.1 – 0.8.2 | 6 Oct | Managed Cloud servers, passkeys and TOTP |
| **0.9.0** | 12 Oct | **Openship Actions** (GitHub-compatible CI on your servers), GitHub Checks on by default, moving the control plane between desktop and a server |
| 0.9.1 – 0.9.3 | 13–17 Oct | Russian UI, runtime roles (web, worker, scheduler), opt-in pull-request previews |
| 0.9.4 – 0.9.5 | 24 Oct | **Runtime security advisories** matched to deployed apps, with a reviewed patch through the deploy pipeline; deploy to an existing Docker Swarm; route passwords |
| 0.9.6 – 0.9.8 | 1–2 Nov | Self-hosted OIDC, moving single apps between servers, a Kubernetes manifest foundation, **AI Agents** (Claude Code or Codex in the panel), chat-app commands |
| 0.9.9 | 3 Nov | "Critical security update": Better Auth 1.7.15, Hono adapter, OAuth state, host namespace and bind-mount restrictions; reviewed host package patches |
| 0.9.10 – 0.9.11 | 4–6 Nov | Wildcard route rules, Compose source review, Cloud host execution boundary, catalogue additions |

### Deploy sources and builds

* **Sources:** GitHub (an App, the `gh` CLI's sign-in, a personal token or deploy
  keys), a local folder, or a prebuilt image or artifact. Other hosts clone with a
  server credential, but there is no GitLab, Gitea or Bitbucket integration:
  `TODO.md` says "no second provider exists anywhere on disk" and issues #75
  (GitLab) and #382 (Forgejo/Gitea) are open, while the pricing page says "Deploy
  from GitHub, GitLab, or any Git remote".
* **Detection** reads `package.json`, framework config, lockfiles, a
  `docker-compose.yml` and an optional `openship.json`; stacks include Node,
  Python, Go, Rust, PHP, Ruby, Java and .NET. Monorepos choose which apps to deploy
  and rebuild only the services a push touched.
* **Where the build runs:** on the controller (the desktop or the self-hosted
  instance) or on the target server, with BuildKit. The resolved configuration is
  frozen into a snapshot so a redeploy or rollback runs what shipped.
* **Release commands** run after the build and before activation, ten minutes each
  (0.8.0); **runtime roles** add workers and schedulers beside the web process
  (0.9.2, design in #935).
* **Rollback** keeps five past deployments by default plus pinned ones; redeploy
  without rebuilding; routing and TLS happen after the app is up, so a DNS or
  certificate problem shows as "Action required" instead of failing the deploy.
* **Openship Actions** (0.9.0): runners on your servers for `.github/workflows/`,
  or Openship-scheduled workflows in `.openship/workflows/` executed with `act`;
  artifacts, caches, OIDC, approvals. Native runners "execute trusted workflow code
  as the server's connected user" (MCP catalogue).
* **GitHub Checks** per deployment, on by default.

### Domains, TLS and routing

* OpenResty on :80/:443 with certbot HTTP-01, a vhost per domain; free domains on
  Cloud. **Wildcard certificates** by DNS-01, automatic with Cloudflare or by a
  manual TXT record (0.8.0); a valid parent wildcard is reused; manual certificates
  can be replaced without breaking renewal.
* Primary and `www` behaviour, redirect-only domains, Cloudflare Tunnel managed
  from the route menu (0.9.2), trusted proxies, an `x-visitor-country` header to
  the app from a bundled GeoIP database.
* **Route rules** per path: rate limits with a burst, IP and country bans, country
  and CIDR allowlists, bad user agents, hotlink protection, a chosen block status;
  pushed to the edge without a reload. **Route passwords**, including for previews
  (0.9.4). Self-hosted only.
* **Taking over an existing proxy**: Openship reads a running Traefik, Caddy or
  nginx, journals it, disables it, installs OpenResty, re-registers the sites and
  their certificates, and rolls back if anything fails.
* The README advertises a CDN with "Edge caching, HTTP/3, Brotli compression,
  instant purge". Openship's own migration guide says the opposite: "The edge is
  programmable access rules + analytics — not a CDN and not a WAF. There's no
  HTTP/3, no brotli, and no response caching layer."

### Databases and services

* On Docker targets, databases are catalogue apps (PostgreSQL, MariaDB, MongoDB,
  Redis, ClickHouse and others) linked by environment variables; issue #309 (July)
  complained there was no linking, and "Connect application" now writes
  `DATABASE_URL` or `REDIS_URL` on cluster databases.
* **On a cluster** (0.8.0): PostgreSQL standalone or a 3–9 instance CloudNativePG
  cluster with a read-only endpoint; Redis standalone or a 6–18 instance Redis
  Cluster through the Redis Operator. An **upgraded copy** moves PostgreSQL 17 to
  18 into a new database without touching the source.
* **Compose** files deploy as one project, with dashboard edits reconciled against
  the repository and unsupported hardening fields reported rather than dropped
  (#749 asks for `read_only`, `cap_drop`, `security_opt`, `tmpfs`, `user`).
* **A mail server**: iRedMail's Postfix, Dovecot and webmail, DKIM/SPF/DMARC,
  aliases, relays, inbound mail, Fail2ban management (0.9.11).

### Storage and backups

* Destinations: S3-compatible, **SFTP**, an existing server's SSH login, or a local
  disk; credentials encrypted at rest and checked by writing, reading and deleting
  a probe. Nothing found about encrypting the archives themselves.
* Policies per service or volume, run now, optional incremental backups, a copy
  protected from deletion, pre-deploy backups, retention by disk capacity.
* Restore goes **Review → Prepare (a safe check) → Confirm → Apply**.
* On clusters: WAL archiving and physical recovery for PostgreSQL, per-shard RDB
  snapshots for Redis, and **restore into a separate database** with new
  credentials, leaving the original and the app's connection untouched until
  somebody switches it. Shared volumes (Longhorn 1.12.1, two or three copies) have
  manual, hourly or daily backups and restore into a new volume.

### Scaling and high availability

* On Docker and Bare targets, one instance per service on one server.
* On a **cluster**: one control server for one or two machines, three with
  embedded etcd from three up; 1–100 replicas set by hand, spread across servers,
  Kubernetes replacing failed ones. Only one app or worker per project; no Compose
  stacks; "automatic gateway failover, highly available public ingress, workload
  metrics and policy-driven autoscaling are not implemented"; adding or removing a
  live worker is "not yet exposed" (`docs/k3s-cluster-runtime.md`).
* The same document says "No production host was provisioned as part of this
  implementation" and that the latest shared-storage and database-recovery
  additions "have not completed a local acceptance run".
* **Sleep mode** (`auto_sleep` by default, or `always_on`) is documented for
  Openship Cloud; nothing in `packages/adapters` reads it, so it appears to have no
  effect on self-hosted targets (read from the code, not run).
* Docker Swarm deployment to an existing swarm (0.9.4).

### Observability (logs, metrics, alerts, uptime)

* Runtime logs, request logs, and a **live per-route request-log stream**.
* **Traffic analytics counted at the edge**: requests, bandwidth, status-code mix,
  top paths and visitor countries, kept in OpenResty shared memory (about 1.4 µs
  of work per request, after the response is sent) and flushed to Postgres every
  30 minutes (`docs/monitoring.md`). Container CPU and memory per service.
* A continuous health monitor marks a crash-looping or down service as **Action
  Required** on self-hosted targets.
* No log drains for apps were found in the documentation. Uptime checks are a
  catalogue app (Uptime Kuma), not a feature.

### Security, auth, roles, SSO, audit

* Email and password, GitHub and Google sign-in, **generic OIDC** on self-hosted
  instances (0.9.6), requiring `email_verified` and binding accounts to issuer and
  subject; passkeys and TOTP (0.8.1).
* **Roles:** owner, admin, member and **restricted**. The docs call the first three
  "coarse by design (beta)": a member can read, write and delete everything but
  billing and the audit log. A restricted member starts with nothing and is given
  grants of read, write or admin on a project, server, mail server, backup
  destination, GitHub organisation or repository, or a wildcard. Scoped tokens can
  also carry **capabilities** such as `deployments:trigger` or `domains:write` on
  one project.
* Personal access tokens, read-only or scoped below the creator's own access;
  **MCP over OAuth 2.1** (below); an audit log; credentials encrypted at rest and
  not returned; a terminal into servers and services.
* **Runtime security advisories** (0.9.4): a signed feed that Openship publishes,
  polled from GitHub every five minutes with a GitHub credential and hourly
  without, matched against recorded image digests and
  runtime versions probed inside the containers (Node, Bun, Deno, Python, PHP, Ruby,
  Java, .NET, Go). Affected apps appear in a banner and in notifications, and
  "Apply security patch" rebuilds a Node or Bun app on the fixed runtime of the
  same major. It is "curated advisory matching, not an image filesystem, or
  application dependency vulnerability scanner".
* **Host patching** (0.9.9): a reviewed list of exact APT package changes, then an applied
  run with verification and a "reboot required" outcome; never automatic.
* The desktop mode accepts requests from loopback with no login, through one gate
  function (`zeroAuthAllowed()`).

### Preview environments and branches

* Branch environments created from a switcher, and **pull-request previews** (opt
  in, 0.9.3) for same-repository, non-draft pull requests; fork pull requests are
  excluded. Only variables set under **Environment → Preview** are copied; fresh
  named volumes; a URL per public port under a wildcard preview domain; GitHub
  Checks and one comment with the URL; cleanup on close, merge or draft, with
  reconciliation for missed webhooks. Not on cluster targets.

### Templates and catalogue

* 56 app definitions in `packages/core/src/apps/catalog` (read at `430afc6`),
  databases and the mail server among them; each is JSON with generated secrets, a
  settings form and a connection card. Of 120 image references, **18 use
  `:latest`**, none is untagged. Openship's own guide concedes that "Coolify's 280+
  catalog" is larger.

### CLI, API, IaC, integrations

* A REST API; a CLI with named contexts, `--json`, shell completion, `doctor`, and
  `openship api <method> <path>` for any route; the **Ship SDK** (`npm install
  openship`), either a client to an instance or an embedded engine with no API
  server.
* **MCP** at `POST /api/mcp`, stateless Streamable HTTP. Tools are generated from
  REST routes that opt in — **560** of them (`docs/api/mcp-tools.mdx`) — each with
  `readOnlyHint` and `destructiveHint`; every call is re-dispatched through the
  route's own authentication and permission checks; the `tokens`, `auth` and `mcp`
  modules can never become tools; prompts describe ordered flows. Clients connect
  by **OAuth 2.1**: paste the URL, a browser page asks for read-only or full
  control over all or chosen projects, servers and repositories, and the client
  appears under **Settings → MCP → Connected clients**. That page has a copy-ready
  recipe for Claude Code, Codex, Cursor, VS Code, Claude Desktop, Windsurf and Zed.
* `openship.json` describes the build and deployment in the repository; projects
  and whole instances export and import with their credentials.

### Notifications

* Channels: email, a signed webhook, in-app, Slack, Discord, Microsoft Teams and
  Telegram; each person subscribes channels to categories such as `deploy.failed`
  or `backup.failed`, with workspace defaults.
* **Chat apps** (0.9.7): Slack, Discord, Teams and Telegram can talk to the
  assistant and run commands.

### Multi-server and networking

* Any Linux server over SSH as root or a passwordless-sudo user, with no agent on
  it; a "Test connection" button. **A managed WireGuard network** across
  providers, or a provider's private network, with per-direction connection rules
  and **on-demand link tests** (TCP/UDP reachability, MTU, round-trip time, loss,
  jitter, throughput between a pair).
* **Adopting running containers** from Coolify, Dokploy, Dokku or plain Docker:
  scan everything on the server over SSH, recover domains from the old proxy,
  create a project that reuses the running images, take over the proxy, then cut
  over. **Server-to-server moves** copy the data; "every server-to-server move
  stop[s] the originals for a consistent copy ... (up to ~20 min)".
* "Leave management" drops Openship's record and leaves the apps running.

### Team and collaboration

* Organisations with unlimited members on every plan, invitations, roles and
  restricted grants, audit log, shared project variables, a service reused across
  projects. Several isolated workspaces on one self-hosted instance is open
  (#758).

### Developer experience and onboarding

* Three ways to run the control plane, chosen first: desktop, `curl -fsSL
  https://get.openship.io | sh` then `openship` (an interactive wizard that creates
  the admin, wires a domain and installs a boot service), or Cloud. The installer
  brings its own Node 22. The control plane lists itself as an app in its own
  dashboard.
* **Ten dashboard languages**: Arabic, German, English, Spanish, French, Japanese,
  Portuguese, Russian, Turkish, Chinese (`apps/dashboard/src/i18n/locales`).
* About 230 documentation pages; a migration guide from Coolify, Dokploy and Dokku
  with an "Honest caveats" section.
* **AI Agents** (0.9.7): the panel runs Mindwire with your Claude Code or Codex
  sign-in on the desktop, the self-hosted host or a paid Cloud instance; off by
  default, granted through the same consent page as MCP.
* No published minimum hardware was found.

## What users love

* **The control plane off the server.** "Every self-hosted PaaS in that drawer runs
  its entire control plane on your server ... Openship inverts it", so "Your
  production server does not need build-sized resources" and "Your deployment tool
  has no public attack surface" (dev.to, 2026-07-26); "OpenShip is not a Coolify
  clone with a different UI" (snackonai headline, via search result).
* **Several ways in.** "CLI + desktop + MCP: Openship" and "Feels closer to a
  productized deploy toolkit than a pure hosting panel" (bitdoze, 2026-07-20);
  "`openship api` following the `gh api` pattern is a good call" (dev.to).
* **A mail server in the box.** Picked out by every review read (dev.to, bitdoze,
  temps.sh).
* **Desktop and SSH targets.** "Choose Openship when 'connect any clean server over
  SSH' is the requirement, or when you want k3s underneath your cluster" (Temps'
  comparison, re-checked against 0.8.2 in October 2026).
* **Recognition.** "Reminds me a lot of OpenShip" and "openship does the same thing
  using SSH" (HN, 2026-10-16).

## What users complain about

* **First runs that stall without saying why.** A user on a fresh Hetzner server
  (0.6.1) found the mail setup form "briefly disappears and instantly comes back to
  the same form. No Error no hint", then could not get Openship to connect to the
  server it was running on: "the errors the UI displays are not very helpful"
  (#527, 2026-08-09, 20 comments, open).
* **Early breakage.** "As of July 2026 there are open reports of breakage in the
  CLI installer and in the self-hosted SSH deploy path, including deploys failing
  immediately after a successful Docker build" (dev.to); the earliest issues are
  install loops, unsigned macOS builds and missing images (#21, #3, #11).
* **Apps that are more than one process.** #231 (26 comments): Laravel support was
  "the generic PHP recipe plus a display name", and an app on stock defaults kept
  its whole database in a container-local SQLite file that was "gone on the next
  deploy". Runtime roles and release commands since 0.8.0–0.9.2 answer part of it;
  #935 is still open.
* **The Docker socket.** "API compromise can become host root via docker.sock"
  (#1048, 2026-10-04, open); the Compose path is described by its own README as
  "host-privileged through the socket, so run it only on a trusted host".
* **Thin catalogue, short history.** "If you want one-click Plausible, Outline, n8n,
  and friends, Coolify still wins"; "I would still put Coolify or Dokploy first
  until Openship has more public battle scars" (bitdoze).
* **The keys move to the laptop.** "This mostly relocates the crown jewels onto
  your laptop ... a compromised dev laptop means a compromised production" (dev.to
  comment, 2026-07-26).
* **Claims ahead of the code.** The README's CDN line, "Multi-node clusters ...
  coming next" after 0.8.0 shipped them, and GitLab on the pricing page, all
  contradicted by Openship's own guide, changelog and `TODO.md`.
* **A licence that changed in the first week** (npm metadata; dev.to).

## Security record

**Where I looked (2026-11-08):** the repository's `SECURITY.md`, `SECURITY_GUIDE.md`,
`security-advisories.json` and changelog; 2,670 commits of history; the GitHub
issue search; the NVD API. The repository's published-advisories page could not
be read from here (github.com answered 403 and the repository advisory API is not
reachable from this session), so **whether any GHSA has been published is not
verified**.

* **NVD:** a keyword search for "openship" returns no CVEs. The name is shared with
  an unrelated open-source order-management system (`openshiporg/openship`).
* **What the project says about itself:** "Real RCE / takeover bugs in this
  codebase came from violating these" — shell-quoting every remote argument with
  one escaper, and one zero-authentication gate (`SECURITY_GUIDE.md`). Remote
  execution runs "as root on the user's servers"; host keys are pinned with
  `StrictHostKeyChecking=yes`.
* **Fixes visible in the history, none with an advisory found:**
  command injection in mail provisioning and SSRF through webhook URLs (2026-07-24,
  `9d580ba`); path traversal out of the app directory through `..` in a volume path
  (2026-08-06, `2658f55`, from a contributor); read-only secret projections
  sanitised (#918, 2026-09-22); a tenant-boundary audit — "enforce tenant isolation
  across hosted server operations" and "preserve terminal authority and constrain
  backup and SSH access" (2026-10-21/22, `bc796d0`, `e9b1204`); remote builds able
  to reach cloud metadata endpoints (#1051, reported with a public lab
  2026-10-04, fixed in 0.9.4); Bun's bundled trust store still holding the
  compromised eMudhra root (#1413, 2026-10-22); 0.9.9, called a "Critical security
  update" (2026-11-03): Better Auth 1.7.15, the Hono Node adapter, OAuth state,
  proxy and consent advisories, and host namespaces and bind mounts refused across
  HTTP, MCP and SDK; and "refuse workload execution on the Cloud API host" (#1615,
  2026-11-05).
* **Reporting:** for the first days the listed addresses bounced and private
  advisories did not work (#46, 2026-07-20). On 2026-07-23 the maintainer asked
  volunteers to review authentication, routes, mail and the pipeline for "RCE,
  privilege escalation, authentication bypass" and recommended "everyone to use
  the local mode for the beta version" (#148, open). `SECURITY.md` now has a
  safe-harbour policy and no paid bounty.
* **Exposure by design:** the Docker socket in the API container (#1048; a
  "socket-free remote-only control plane" is PR #1049, open); root over SSH for
  every deploy; a terminal; CI runners executing workflow code as the server's
  user; a GPL mail stack in the control-plane distributions; a no-login loopback
  mode on the desktop.
* **Done well:** a signed advisory feed with revisions and expiry; MCP tools that
  pass back through route authorisation and never include token or auth routes;
  permission tags checked against real resources at start-up (#1521); OIDC that
  requires a verified email and binds to issuer and subject.

## Against Skifity

Skifity's statuses: much of it now runs on a real single-server and two-server k3s
on every push (`docs/progress.md`, "A real cluster, at last" and "What one
throwaway server could not show"); a real S3 bucket, a mixed-architecture cluster
and production traffic never have. Openship's cluster runtime is in the same
position by its own account. Line numbers are from the working tree on 2026-11-08.

| Capability | Openship | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git providers | GitHub only (App, `gh`, token, deploy key); other hosts clone without integration | GitHub (personal token), GitLab, Gitea, Bitbucket and generic, with webhooks and commit statuses (`internal/gitsrc`); **no GitHub App** — removed because it was never finished (`internal/api/integrations_handlers.go:77-81`) | ahead on providers; **behind on connecting GitHub** |
| Where builds run | Controller (laptop or instance) or target server | In-cluster BuildKit Jobs with a queue (`internal/builder`) | different |
| Build detection | Own stack detection, monorepos, roles | Railpack, Nixpacks, Dockerfile, static (`internal/builder/detect.go`); watch paths; Procfile and `app.json` | parity |
| Release step and workers | Release commands (0.8.0), roles (0.9.2) | `ReleaseCommand` (`internal/store/models.go:296-298`, `internal/deploy/run.go:186`); processes from one build (`internal/kube/process.go`) | parity |
| Rollback | Five kept plus pinned; snapshot of config | Image and settings restored; ten images kept per app, none pinnable (`internal/registry/registry.go:34-42`) | ahead on settings, **behind on pinning** |
| CI runners | Openship Actions on your servers | None, by design | deliberate difference |
| **Domains, TLS and routing** | | | |
| Certificates | HTTP-01; **DNS-01 wildcards** with Cloudflare or a TXT record; manual certificates | HTTP-01 through cert-manager (`internal/cluster/issuer.go`); a team's own certificates (`internal/tlscert`); wildcard DNS-01 "is not built" (`docs/concepts.md:423-426`) although DNS provider tokens exist (`internal/dnsprov`) | **behind** |
| Redirects, tunnel, DNS | www/primary, redirect-only domains, Cloudflare Tunnel | Hostname redirects (`internal/api/domain_redirects.go`), Cloudflare tunnel (`internal/cluster/tunnel.go`), records written at Cloudflare, Hetzner, DigitalOcean, Route 53 (`internal/dnsprov`) | parity |
| Edge rules | Rate limits, IP/country/CIDR, user agent, hotlink; self-hosted only | Allow, block and rate-limit rules on address, country, ASN, host, path, method, user agent and any header, nested and/or (`internal/edgerules/edgerules.go:104-125`, `internal/guard/limiter.go`); maintenance mode (`internal/guard/maintenance.go`) | ahead |
| Route passwords | Yes, previews included | Yes (`docs/firewall.md`, commit `7865bc8`) | parity |
| Proxy takeover | Takes over Traefik, Caddy or nginx with rollback | None: the installer wants ports 80 and 443 free | absent (see gap 3) |
| **Databases and services** | | | |
| Engines | Catalogue containers on Docker; PostgreSQL and Redis on clusters | Nine managed engines (`docs/databases.md:10-20`, `internal/dbsvc`) | ahead |
| High availability | CNPG 3–9; Redis Cluster 6–18 | CNPG with an odd instance count (`internal/dbsvc/manifests.go:143`); every other engine single | ahead on PostgreSQL, behind on Redis |
| Major upgrade and restore beside | Upgraded copy 17→18; cluster restores into a separate database | Restore replaces the database in place (`pg_dump --clean`, `docs/backups.md:115-120`); no path between majors | **behind** |
| Compose | Deployed and reconciled | Stacks of apps reaching each other by name (`internal/api/stack_handlers.go`) | parity |
| Mail server | Built in | None | deliberate difference |
| **Storage and backups** | | | |
| Destinations | S3, SFTP, an existing server, local disk | S3-compatible only (`docs/backups.md:12`) | behind (small) |
| Archives | Credentials encrypted; archive encryption not found | Sealed with a passphrase and verifiable (`docs/backups.md`, Phase 104); panel backup on a schedule (Phase 84) | ahead |
| Shared files across servers | Longhorn volumes mounted by every instance | Longhorn as an optional component (`internal/settings/settings.go:604-605`) but every volume `ReadWriteOnce` (`internal/kube/manifests.go:550`), and a volume blocks a second instance (`internal/deploy/scaling.go:29-44`) | **behind** |
| **Scaling and HA** | | | |
| Instances | Manual 1–100 on clusters only; one app per project; no Compose | Instances on every app, HPA (`internal/kube/apply.go:48`), scale to zero with KEDA (`internal/kube/scaletozero.go`), readiness checker (`internal/deploy/scaling.go`) | **ahead** |
| Autoscaling | "Not implemented" | CPU and memory | ahead |
| Ingress availability | The first control server is the only gateway | Traefik on k3s; the front door is still yours (`docs/adding-servers.md`) | parity |
| Mixed architectures | Image architecture constrains scheduling | Preflight accepts amd64 and arm64 (`internal/provision/preflight.go:217`); nothing in `internal/kube` or `internal/builder` sets `kubernetes.io/arch` | **behind** |
| **Observability** | | | |
| Request metrics | Requests, bandwidth, status mix, **top paths, countries**, live request log | Requests, status classes, p50/p95 from Traefik (`internal/api/app_metrics_handlers.go:52-70`, `internal/traffic/collector.go:143-148`); no countries, paths, bandwidth or request stream | **behind** |
| Usage and alerts | Container metrics; health "Action Required" | CPU, memory, disk history and thresholds; `app.alert`, `server.alert`, `backup.missed` (`internal/notify/notify.go:57-92`) | ahead |
| Log drains | Not found | Yes (`internal/logdrain`) | ahead |
| Vulnerabilities | Curated runtime feed, one-click patch for Node and Bun | Trivy on every deploy and nightly, a deploy gate on fixable criticals, `app.vulnerable` (`internal/vulnscan`, `internal/serverapp/serverapp.go:497-554`) | ahead on coverage; behind on a one-step fix |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | Password, GitHub, Google, OIDC, passkeys, TOTP | Argon2id, TOTP, passkeys, OIDC with groups mapped to roles, team-required 2FA or SSO (`internal/auth`) | ahead |
| Roles | Owner, admin, member, restricted with per-resource read/write/admin | Owner, admin, member, viewer (`internal/store/models.go:10-24`); members limited to projects with one role (`:69-73`) | parity |
| Tokens | Read-only or scoped to chosen projects, servers, repos; capabilities like `deployments:trigger` | Scopes by resource type, expiry, network allowlist (`internal/auth/scopes.go`); **no project field** on a token (`internal/api/auth_handlers.go:495-500`) | **behind** |
| Secrets | Encrypted; `.env` download exports saved secrets | Sealed with context, write-only everywhere (`internal/crypto`) | ahead |
| Host upkeep | Reviewed APT patches applied as a run | Hardening report reads auto-updates and reboot-required and changes nothing (`internal/provision/hardening.go:63-85`); k3s upgrades (`system-upgrade` component) | **behind** on OS packages |
| Control-plane exposure | Desktop: nothing public; Compose mode holds the Docker socket | Panel served through a public Ingress (`deploy/ingress.yaml`, `deploy/ingress-tls.yaml`); the network allowlist binds API tokens, not browser sign-in (`web/src/locales/en.json`, token network help); Kubernetes API, no socket | ahead on privilege; **behind on keeping the panel private** |
| Terminal | Servers and services | None, by decision | deliberate difference |
| **Preview environments** | | | |
| PR previews | Opt-in, same-repo, preview-only values, fresh volumes, Checks and comment | Per-PR namespace, preview-only values, whole-environment previews with a seed (Phases 93, 107), statuses and comment (`internal/deploy/gitreport.go`) | parity |
| **Templates** | | | |
| Catalogue | 56 apps, 18 of 120 images `:latest` | 374 (`internal/templates/catalogue`), every image a checked version | ahead |
| **CLI, API, IaC, integrations** | | | |
| MCP transport and auth | Streamable HTTP, **OAuth 2.1** with a consent page, per-client recipes, connected-client list | Streamable HTTP at `/api/mcp` and stdio (`internal/mcpserver/server.go:103-116`); a pasted Bearer token only (`internal/api/mcp_handlers.go:50-58`); no OAuth code anywhere | **behind** |
| MCP tools | 560 generated from routes, hints, prompts | 47 written by hand, hints (`internal/mcpserver`), no prompts | different; behind on prompts |
| CLI | Contexts, completion, `doctor`, `api` passthrough | About 40 commands with `api` passthrough (`internal/cli/commands.go:20-125`); one panel at a time (`internal/cli/client.go:65-95`); no completion | behind (small) |
| SDK and IaC | Node SDK; `openship.json`; instance export | OpenAPI description (`a0f45bd`); `skifity.yaml` plan and apply (`internal/blueprint`); team export | parity |
| Server provisioning | Managed Cloud servers | Hetzner and DigitalOcean from the panel (`internal/cloud`) | parity |
| **Notifications** | | | |
| Channels | Email, webhook, in-app, Slack, Discord, Teams, Telegram; chat-app commands | Email, webhook, Slack, Discord, Teams, Telegram, Mattermost, ntfy, Pushover, Gotify (`internal/notify`) | ahead on channels |
| **Multi-server and networking** | | | |
| Joining servers | Any SSH host, no agent; k3s on request | k3s joined over SSH in checked steps (`internal/provision`); WireGuard flannel | parity |
| Link tests | TCP/UDP, MTU, RTT, loss, jitter, throughput | None in the product; the `Two servers` CI workflow tests a slow link | behind (small) |
| Moving apps | Adopt running containers; server-to-server with data | Apps move off a server being removed (`internal/provision/move.go`); nothing imports from another panel or host | **behind** |
| Leaving | Record-only removal keeps apps | Uninstall keeps k3s and the apps by default (`installer/uninstall.sh:4-7`) | parity |
| **Team and collaboration** | | | |
| Teams | Organisations, unlimited members, grants | Teams, invitations, roles, audit, environment cloning (`internal/api/envclone_handlers.go`) | parity |
| **Developer experience** | | | |
| Ways to run | Desktop, self-hosted, Cloud | One install on a server (`installer/install.sh`) | different |
| Languages | Ten, Arabic included | Five with a build gate (`web/src/locales`) | **behind** |
| Footprint | No published minimum | Panel 19–47 MiB in its pod; k3s about 1 GB (`docs/performance.md`) | unproven for Openship |

### Claims that no longer hold

* The discovery sweep, and Openship's own README, list multi-node clusters as
  "coming next". **0.8.0 (2026-09-27) shipped them**: Openship installs k3s across
  chosen SSH servers, with CloudNativePG, the Redis Operator and Longhorn, from the
  desktop or a self-hosted controller. What remains Skifity's is that its whole
  product runs on the cluster — every app, Compose stack and preview can have
  several instances, autoscaling and scale to zero — while Openship's cluster takes
  one app or worker per project, scales by hand and has one gateway.
* `docs/research/competitors.md` says no competitor "puts Kubernetes on one small
  server with one command". Openship's "Enable scaling" puts k3s on one or two
  servers with one control server. It is a second step after the Docker install,
  not the base, and Openship calls it unproven on production hosts; the claim
  should be narrowed rather than dropped.
* Phase 87's remote MCP endpoint closed the gap against Coolify; against Openship it
  is not a lead. Openship's is remote too, reaches 560 routes, and signs clients in
  with OAuth.

## Gaps worth closing in Skifity

**P1**

1. **Let an assistant connect with OAuth, and show how.** *What:* make `/api/mcp`
   an OAuth 2.1 protected resource with an authorisation server in the panel
   (metadata, dynamic client registration, PKCE), so `claude mcp add --transport
   http skifity https://panel/api/mcp` opens a browser page where the person picks
   read-only or full access and all or some projects; the grant becomes a token with
   those scopes, listed under Account as a connected client that can be revoked.
   Add a **Connect an assistant** page with the endpoint filled in and a recipe for
   each common client. *Evidence:* Openship's MCP is OAuth 2.1 with a consent page
   and per-client recipes for seven clients. Skifity
   refuses anything but a pasted Bearer token (`internal/api/mcp_handlers.go:50-58`)
   and has no OAuth code anywhere, and its only setup instructions are in
   `docs/cli.md`. *Fit:* `internal/auth` (an authorisation-code store, sealed like
   other secrets), `internal/api` routes, the existing scoped tokens as the grant,
   an Account section in `web/src/pages/account.tsx`. *Size:* M. *Without a
   cluster:* yes, end to end against the real binary.
2. **Connect GitHub as an App.** *What:* the manifest flow (one click creates the
   App on the person's GitHub and hands its keys back), installation tokens minted
   per clone and webhook from the App's private key, a repository picker, and
   Checks. *Evidence:* Openship supports several GitHub Apps per workspace (0.7.1)
   and Checks by default (0.9.0); Coolify has a GitHub App. Skifity removed a
   half-built App and asks for a personal access token
   (`internal/api/integrations_handlers.go:77-81`, `internal/settings/settings.go:474-483`),
   which is long-lived, tied to one person and usually wider than one repository,
   and making one with the right scopes is the first thing a new user gets wrong.
   *Fit:* `internal/gitsrc` (a JWT signer and token exchange), the source form in
   the new-app page, `internal/deploy/gitreport.go` for Checks. *Size:* M. *Without
   a cluster:* yes, against an `httptest` GitHub.
3. **Bring apps over from another panel or a Docker host.** *What:* `skifity
   import docker --server <ssh>` and a panel form that read a server's running
   containers, Compose labels, volumes and the old proxy's routes (Coolify's and
   Dokploy's Traefik, Dokku's nginx), show what becomes an app, a stack, a managed
   database or a volume, recreate them, copy volume data and database dumps across,
   and leave DNS and the old containers alone until the person cuts over.
   *Evidence:* adoption is the first row of Openship's case against Coolify, Dokploy
   and Dokku; the Coolify page's gap 7 (import from Coolify) was never closed; and
   Skifity has nothing that reads another host's containers (no importer under
   `internal/`). *Fit:* `internal/sshx` (it already has an in-process SSH server for
   tests), the Compose conversion in `internal/api/stack_handlers.go`, the template
   installer, the database import added in Phase 112. *Size:* L. *Without a
   cluster:* the scan and the conversion yes; copying data in needs one.

**P2**

4. **Tokens limited to projects, and to deploying.** *What:* an optional list of
   projects on a token, and narrower actions such as "deploy and read logs" or
   "manage domains", enforced next to the existing scopes; the same grant used by
   gap 1. *Evidence:* Openship's scoped tokens and MCP grants name projects and carry
   capabilities like `deployments:trigger`. Skifity's token request has a name, team,
   scopes by resource type and a TTL, and no project (`internal/api/auth_handlers.go:495-500`);
   confining an agent to one app today means inviting a second user limited to that
   project. *Fit:* `internal/auth/scopes.go` `TokenAllows`, the project resolution in
   `authorizeApp`, the token form. *Size:* S–M. *Without a cluster:* yes, with the
   route walk extended to project-bound tokens.
5. **Wildcard certificates through the DNS provider already connected.** *What:* a
   cert-manager DNS-01 issuer for Cloudflare, Route 53, DigitalOcean and Hetzner
   using a token the team gives for that purpose, so `*.apps.example.com` and
   preview domains get one certificate. *Evidence:* Openship (0.8.0) and Coolify
   both issue wildcards by DNS-01; Skifity's own docs say "That is not built"
   (`docs/concepts.md:423-426`) while `internal/dnsprov` already talks to all four
   providers. *Fit:* `internal/cluster/issuer.go`, a sealed token Secret per team,
   the Domains settings. *Size:* M. *Without a cluster:* the rendered issuer yes;
   issuance needs a cluster and a real zone.
6. **Where requests come from.** *What:* per app, visitor countries, top paths,
   bytes sent, and a live tail of requests (time, status, path, country, duration)
   on the Usage tab. *Evidence:* Openship counts all of these at the edge and calls
   it its widest lead over Coolify and Dokploy. Skifity records requests, status
   classes and latency only (`internal/api/app_metrics_handlers.go:52-70`,
   `internal/traffic/collector.go:143-148`) and has the country database already
   (`internal/geoip`). *Fit:* Traefik's JSON access log read by a small collector,
   or counted by the guard where it runs; minute buckets in the existing traffic
   table. *Size:* M. *Without a cluster:* the parser and storage yes; real traffic
   needs one.
7. **Files shared by instances on different servers.** *What:* a volume option,
   offered when Longhorn is installed, that makes the claim `ReadWriteMany`, so a
   scaled app keeps uploads in one place; the readiness checker stops flagging such
   a volume. *Evidence:* Openship's "Shared files" on clusters. Skifity already
   installs Longhorn (`internal/settings/settings.go:604-605`) but makes every
   volume `ReadWriteOnce` (`internal/kube/manifests.go:550`), and a volume is the
   first thing `ScalingReadiness` refuses (`internal/deploy/scaling.go:29-44`).
   *Size:* S–M. *Without a cluster:* manifests and checker yes; RWX behaviour needs
   one.
8. **Restore or upgrade a database beside the old one.** *What:* "Restore as a new
   database" and "Upgrade to PostgreSQL 18 as a copy": create a database, fill it
   from a backup or a fresh dump, show it, then switch the app's link with a
   reviewed deploy, keeping the old one until it is deleted. *Evidence:* Openship's
   cluster restores never overwrite and its upgraded copy moves 17 to 18. Skifity
   restores with `pg_dump --clean` into the live database (`docs/backups.md:115-120`)
   and offers 16, 17 and 18 only at creation (`docs/databases.md:10-20`), so a
   database created on 16 stays on 16. *Fit:* `internal/dbsvc`, `internal/backup`
   restore jobs, `link_database`. *Size:* M. *Without a cluster:* the API and UI
   yes; the copy needs one.
9. **Operating-system updates, reviewed, one server at a time.** *What:* on a
   server's page, the pending package updates with versions, then "apply": cordon,
   move the apps (`internal/provision/move.go` already does this), upgrade, reboot
   if required, uncordon, check, next server. *Evidence:* Openship applies reviewed
   APT patches as a recorded run; the Coolify page listed an OS update view as gap
   19 and only the k3s half was built (Phase 109). Skifity's hardening report reads
   `auto_updates` and `reboot_required` and changes nothing
   (`internal/provision/hardening.go:63-85`). *Size:* M–L. *Without a cluster:* the
   plan and the script against the in-process SSH server yes; drain and reboot need
   one.
10. **A panel that is not on the internet.** *What:* an installer option and a
    setting that serve the panel only on the server's private address or through
    the guard with an allowlist, and `skifity connect` that opens an SSH forward to
    it and points the CLI and browser there. *Evidence:* Openship's desktop mode
    exists so that "nothing is exposed publicly", and its maintainer told beta users
    to stay in that mode for security (#148). Skifity always publishes the panel
    through an Ingress (`deploy/ingress.yaml`, `deploy/ingress-tls.yaml`), and its
    network allowlist applies to API tokens, not to signing in. *Fit:*
    `installer/install.sh`, the panel's Ingress rendering, `internal/sshx`,
    `internal/cli`. *Size:* M. *Without a cluster:* the CLI forward yes; the panel
    change needs a real install.
11. **Put an image on servers of its architecture.** *What:* read the platforms in
    the built or pulled image's manifest and set a `kubernetes.io/arch` node
    selector, refusing with an `errdoc` entry when no server matches. *Evidence:*
    Openship's cluster runtime constrains scheduling by the image's architecture;
    Kamal's page raised the same gap. Skifity accepts arm64 and amd64 servers
    (`internal/provision/preflight.go:217`), orders ARM servers at Hetzner
    (`internal/cloud/provider.go:119-120`), and never sets the label. *Size:* S.
    *Without a cluster:* the selector and the refusal yes, against a fake registry;
    a mixed cluster to prove it never.

**P3**

12. **More languages.** Openship ships ten, among them Arabic, Japanese, German,
    French, Spanish, Portuguese and Turkish; Skifity five (`web/src/locales`).
    Spanish and Portuguese reach the most new users; Arabic needs right-to-left
    layout first. M per language, no cluster (`check:i18n` already gates it).
13. **SFTP as a backup destination**, for Hetzner Storage Boxes and a second server.
    Openship has SFTP and "an existing server"; Skifity is S3 only
    (`docs/backups.md:12`). M; the uploader against `internal/sshx`'s server
    without a cluster, the backup Job with one.
14. **CLI comforts:** shell completion, named panels (`skifity context use
    staging`), and `skifity doctor` (config, panel reachable, token valid, clock).
    `internal/cli/client.go:65-95` holds one panel. S, no cluster.
15. **Keep a chosen version.** A pin on a deployment that the registry sweep honours
    beyond its ten images (`internal/registry/registry.go:34-42`). S, no cluster.
16. **Test the link between two servers** — latency, loss and MTU over the
    WireGuard interface — from the server page, for the slow-link failures the
    `Two servers` workflow had to provoke on purpose. S–M; needs two servers.

## Things to deliberately not copy

* **The Docker socket in the control plane.** Openship's own issue (#1048) explains
  that an API bug becomes host root. Skifity talks to the Kubernetes API with its
  own RBAC; keep it that way.
* **Root over SSH on every deploy.** Openship drives its server target over SSH for
  every deployment, its guide says those commands run "as root on the user's
  servers", and its history has the shell-injection fixes that go with it. Keep SSH to provisioning, removal and the explicit host
  operations, with `internal/shellsafe` as the one place a string meets a shell.
* **A mail server.** iRedMail brings Postfix, Dovecot, webmail, Fail2ban, a GPL
  engine shipped in every control-plane distribution, and deliverability work that
  is never finished. Openship's own guide warns that "mail ops are yours to run".
* **CI runners on the production servers.** Openship Actions runs "trusted workflow
  code as the server's connected user" next to the apps. A pull request's code on
  the same machines as production is the risk Skifity's fork-preview rules exist
  to avoid.
* **An agent harness inside the panel.** Running Claude Code or Codex with the
  operator's sign-in on the host makes the panel responsible for an agent's
  credentials. MCP with scoped grants is the boundary; improve that (gap 1).
* **Tools generated from every route.** 560 tools is more than an assistant can
  choose among well, and a generated description is only as good as the route's.
  Skifity's 47 written tools are the better shape; add prompts, not volume.
* **The laptop as the control plane.** It moves the SSH keys to production onto a
  laptop and stops working when the lid closes, which is why Openship needs a
  server install for webhooks and teams anyway. Take the goal — nothing public on
  the server — through gap 10, not the shape.
* **A second authentication path.** The desktop's no-login loopback mode is guarded
  by one function and is still a whole class of bug; Skifity has none.
* **Four runtimes and a fifth on request.** Docker, Bare, Cloud, k3s and an existing
  Swarm each need their own previews, backups and migrations, and Openship's
  feature tables already carry "not on cluster targets" notes. Skifity's
  one-cluster rule stays.
* **Claims ahead of the code.** A CDN, multi-node "coming next" and GitLab on a
  pricing page, each contradicted by the project's own documents. Skifity's
  "Written, never run" labels are the opposite habit.
* **Polling GitHub on a schedule.** Openship's advisory and update checks call out
  every five minutes to an hour while it runs. Skifity "never phones home"; any advisory feed should be
  opt-in.
* **`.env` downloads that include saved secrets** (0.8.0). Skifity's write-only
  secrets are worth more than the convenience.

## Sources

All read on 2026-11-08.

* https://github.com/oblien/openship, cloned at `430afc6` (2,670 commits fetched); files read: `README.md`, `CHANGELOG.md`, `TODO.md`, `SECURITY.md`, `SECURITY_GUIDE.md`, `security-advisories.json`, `release-advisories.json`, `docs/k3s-cluster-runtime.md`, `docs/monitoring.md`, `docs/security-advisories.md`, `packages/core/src/apps/catalog/*.json`, `apps/dashboard/src/i18n/locales/`, `packages/platform/src/engine/lib/resources.ts`
* Openship documentation sources in `apps/web/content/docs/`: `getting-started/core-concepts.mdx`, `getting-started/installation.mdx`, `architecture/overview.mdx`, `mcp.mdx`, `api/mcp-tools.mdx`, `api/notifications.mdx`, `api/backup-destinations.mdx`, `security/auth.mdx`, `security/permissions.mdx`, `guides/the-edge.mdx`, `guides/logs-monitoring.mdx`, `guides/backups-restore.mdx`, `guides/preview-environments.mdx`, `guides/sleep-mode-resources.mdx`, `guides/actions.mdx`, `guides/migrate-from-coolify-dokploy-dokku.mdx`, `guides/git-authentication.mdx`, `guides/mcp-clusters-and-scaling.mdx`, `dashboard/assistant.mdx`, `reference/app-catalog.mdx`, `cli/reference/edge/rules/index.mdx`
* GitHub search API: `search/repositories?q=repo:oblien/openship` (stars, forks, dates, licence); `search/issues` for issue and pull-request counts, the most-reacted and most-commented issues, and security issues
* Issues #3, #11, #21, #46, #75, #148, #151, #216, #231, #309, #382, #527, #749, #758, #935, #1048, #1051, #1413, #1504 (bodies via the search API); pull requests #918, #1049, #1615
* Commits `9d580ba`, `2658f55`, `bc796d0`, `e9b1204`, `f24d89b`
* https://registry.npmjs.org/openship (versions, publish times, licence per version)
* https://api.npmjs.org/downloads/point/last-month/openship and `/2026-03-01:2026-11-07/openship`
* https://openship.io/pricing
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=openship
* https://hn.algolia.com/api/v1/search?query=openship, `openship.io`, `oblien` (stories 48969022, 50120591, 50119941, 50335849)
* https://dev.to/arshtechpro/openship-your-deployment-tool-shouldnt-compete-with-your-app-for-ram-5a4k (2026-07-26, with its one comment)
* https://www.bitdoze.com/openship-self-hosted-paas/ (2026-07-20)
* https://temps.sh/blog/temps-vs-openship-self-hosted-paas-comparison-2026 (a competitor's comparison; used for dates and the October pricing it states)
* Web search result summaries only, the pages themselves could not be read from here: two r/selfhosted threads ("I saw a CI/CD platform called OpenShip. Opinion?", "Anyone Tried it https://openship.io?"), the Product Hunt page (`producthunt.com/products/openship-2`, "No reviews yet") and the snackonai article
* Not readable from here: `github.com/oblien/openship/security/advisories` (HTTP 403), the repository advisory API, Reddit
