# Coolify

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Coolify (`coollabsio/coolify`, coolify.io, plus the hosted control plane Coolify Cloud) is the
open-source, self-hostable PaaS most often put next to Skifity: "an alternative to Vercel,
Heroku & Netlify" for solo developers, agencies and small teams who want to push code or paste a
Compose file and get containers, databases and one-click services on their own Linux servers.
**Architecture:** a Laravel (PHP) control plane with a Livewire/Alpine UI (an Inertia/React
"next-generation infrastructure experience" shipped as an early preview in v4.3.0), running as
four base containers — `coolify` (dashboard, REST API, Laravel Horizon queue workers, scheduler),
`coolify-db` (**PostgreSQL**, all configuration and state), `coolify-redis` (queues and cache) and
`coolify-realtime` (Soketi websockets plus the browser-terminal websocket). Secrets are encrypted
with an `APP_KEY` kept in `/data/coolify/source/.env`. There is **no orchestrator**: the control
plane SSHes into each connected server and runs Docker / Docker Compose there, with a per-server
Traefik (default, v3.7 since v4.3.0) or Caddy proxy and a `coolify-sentinel` agent (Rust) for
health and metrics; Docker Swarm support is deprecated and will be removed in v5. **Licence:**
Apache-2.0. **Pricing:** self-hosted is free with every feature; Cloud is $5/month for up to two
connected servers plus $3/month per extra server, 20% off annually, one subscription per team.
**Maturity:** v4.0.0 went stable on 27 April 2026 after roughly two years of `4.0.0-beta.4xx`
builds; the current release is v4.3.23 (18 September 2026) and v4.4-rc.1 (19 August 2026) is in
release-candidate. **Adoption** (all read 2026-09-30): 62,412 GitHub stars, 5,572 forks, 554 open
issues and 197 open pull requests (GitHub); a homepage counter of **511,959 "self-hosted
instances"** (`undead.coolify.io/instances`, counting method unpublished — treat as installs ever
seen, not live instances); **3,765 Cloud users, MRR 21,200 and a 21,000-member Discord**
(`cdn.coollabs.io/business.json`, the file the homepage reads); Censys counted **52,890 publicly
reachable Coolify dashboards** on 8 January 2026 (The Hacker News). The company is coolLabs
Solutions Kft.; founder Andras Bacsai.

## Feature inventory

### What shipped from v4.0.0 stable to today

Dates are from the GitHub releases and tags pages (read 2026-09-30).

| Release | Date (2026) | What changed that matters for a comparison |
|---|---|---|
| 4.0.0-beta.474 | 21 Apr | **Docker Swarm marked deprecated** "ahead of v5 removal"; optional **API token expiry**; DELETE preview by PR id; a page of input-validation and shell-escaping hardening |
| **v4.0.0** | 27 Apr | Stable tag. "Finally releasing v4.0." Same codebase promoted; v5 announced as "core scalability" work |
| v4.1.0 | 18 May | **Railpack (Beta)** build pack; **structured audit log** for API mutations and webhooks; **instance-level MCP server (read-only tools)**; `[skip ci]`/`[skip cd]`; deployment configuration diff; stop grace period; per-server SSH timeouts |
| v4.1.1 / 4.1.2 | 27 May / 4 Jun | Team-scoping fixes for sources, keys, environments, servers, networks; fork-PR preview safety; restart-loop limits; Sentinel moved to its own tab; custom Docker DNS |
| v4.2.0 (pre-release) | 21 Jul | **Member role becomes read-only**; state-changing API calls must be POST; logs API; move resources between environments; **Hetzner firewall/backups, Vultr and DigitalOcean provisioning**; per-team MCP toggle; sensitive API fields hidden by default; ClickHouse backups |
| **v4.3.0** | 12 Aug | Redesigned UI; **scheduled persistent-volume backups** (local or S3, retention); DNS checks and **automatic Cloudflare DNS setup**; MCP gains diagnostics and deployment controls; Authentik/Clerk/Zitadel SSO; Traefik 3.7; deploy confirmations removed |
| v4.3.1 – 4.3.23 | 12 Aug – 18 Sep | 23 patch releases in 37 days: project icons, parallel-gzip backups, **alerts for missed database backups** (4.3.18), **Sentinel made mandatory** on regular servers (4.3.19), **host-path volumes removed from UI and API** as a security fix (4.3.22), per-domain internal ports, preview DNS checks, preview runtime-logs API |
| v4.4-rc.1 (pre-release) | 19 Aug | **First-class generic OpenID Connect** (discovery, JWKS, PKCE, identity linking), team-scoped integration tokens, Alpine host support |

The release process (`RELEASE.md`, read 2026-09-30) is `feature/* → next → RC → main`, with
hotfixes cut from `main`. There is no documented LTS or security-only branch.

### Deploy sources and builds

- **Sources:** public Git URL; GitHub App (private repos, auto-deploy, PR comments); deploy keys;
  GitLab App or deploy key (self-hosted GitLab private repos since v4.3.0); Bitbucket; Gitea;
  any Git host over HTTPS/SSH; an existing Docker image; a Dockerfile pasted without Git; a Docker
  Compose file from Git (an *Application*) or pasted ("Docker Compose Empty", a *Service*).
- **Build packs:** Nixpacks (default), Railpack (Beta, needs Docker Buildx), Static (nginx),
  Dockerfile, Docker Compose. Builds run on the deployment server unless a **build server** is
  added (a build server cannot run workloads; with several, one is picked at random) and the image
  is pushed to a registry.
- **Build vs runtime:** each variable has independent *Build Variable* and *Runtime Variable*
  flags; Docker build secrets with BuildKit; `SOURCE_COMMIT` excluded from builds so cache survives.
  The docs now say "Use Restart when only runtime values changed", which reuses the image — the
  user picks the button. Issue #5748 "Restart button causes a rebuild" (Dockerfile apps) is open.
- **Rollback:** redeploys an older image still on the server, *with the current runtime
  configuration*; availability depends on image retention; no settings restore.
- **Rolling updates:** for single-container build packs, start-new/health-check/stop-old. Not for
  Docker Compose, previews, published host ports, custom container names or fixed IPs. No request
  draining: issue #8627 (Feb 2026) reports 18–31 s of 502/503s because both containers carry the
  same proxy labels; discussion #4692 asks for draining.
- Pre/post-deployment commands, custom Docker run options, GPU flags, restart-loop limits,
  stop grace period, deploy webhooks for CI, deploy-by-tag (tags are team-scoped labels).

### Domains, TLS and routing

- Per-server Traefik or Caddy; Let's Encrypt HTTP-01 by default; **DNS-01 and wildcard
  certificates** (Traefik, Caddy with a DNS module); custom certificates loaded from the server.
- Several domains per resource, path routing, a per-domain internal port (v4.3.15), **www ↔ apex
  redirects**, per-domain search-indexing control, forced-HTTPS toggles.
- Generated domains from a server's wildcard domain, else `sslip.io`. Asynchronous **DNS
  validation** against 1.1.1.1 (v4.3.11); v4.3.0 notes "automatic Cloudflare DNS setup".
- HTTP basic auth, custom Traefik middlewares, file-based dynamic configuration, Authentik
  forward-auth. **No built-in firewall or WAF**: the docs point to Cloudflare and CrowdSec.
- Cloudflare Tunnel guides: all resources, one resource, server SSH, full TLS.

### Databases and services

- **Standalone databases (8 engines):** PostgreSQL, MySQL, MariaDB, MongoDB, Redis, Dragonfly,
  KeyDB, ClickHouse. Generated credentials, persistent volumes, health checks, resource limits,
  **SSL/TLS with a Coolify CA**, optional **public access through a TCP proxy** (configurable
  timeout), clone and move between environments. No replication or failover is documented.
- **Services** are Compose stacks from the one-click catalogue or pasted by the user. "Magic"
  variables generate values once and share them across containers: `SERVICE_URL_<ID>`,
  `SERVICE_FQDN_<ID>` (with a port suffix for routing), `SERVICE_USER_*`, `SERVICE_PASSWORD_*`
  (32/64, with or without symbols), `SERVICE_BASE64_*`, `SERVICE_REALBASE64_*`, `SERVICE_HEX_*`.
  `${VAR:?}` marks a variable required and blocks deploy until it is set.
- A deployed service keeps its own copy of the Compose file; later template changes are not
  applied. "Pull Latest Images & Restart" re-pulls the saved tags. Discussions #1753 "auto-update
  images" (44 votes) and #3162 (22) are open.

### Storage and backups

- Volume, bind (directory) and file mounts. Host-path mounts were removed from the UI and API in
  v4.3.22 as a security fix.
- **Database backups** (PostgreSQL, MySQL, MariaDB, MongoDB, ClickHouse; not Redis-family):
  cron or named schedules, engine-native dumps, local copy under `/data/coolify/backups` plus an
  optional S3 copy (AWS, Cloudflare R2, Supabase, any S3-compatible), separate local and S3
  retention by count, days and GB, "Disable Local Backup", timeouts, success/failure
  notifications, **missed-backup alerts after N days** (v4.3.18). Databases *inside* a Compose
  service are backed up by reading the container's `POSTGRES_*`/`MYSQL_*`/`MONGO_*` variables.
- **Database restore** from a server path, an upload or S3 for PostgreSQL, MySQL, MariaDB and
  MongoDB. Redis, Dragonfly, KeyDB and ClickHouse: "Database restore is not supported".
- **Volume and directory backups** (v4.3.0): scheduled `.tar.gz`, optional "stop containers while
  archiving", streamed to S3 (v4.3.18). **"Coolify does not restore storage archives from this
  page"** — download and restore by hand.
- **Instance (control-plane) backup:** daily by default, optional S3 copy. The `APP_KEY` is not in
  it and must be saved separately. Restore is a documented terminal procedure; discussion #1684
  "Restore from Instance Backup" (62 votes) is open.
- No export/import of an application: the migration guide says "Coolify does not provide a
  single export and import operation for an application".

### Scaling and high availability

- The scaling overview offers three paths: resize the server; **multi-server deployment** (one
  image pushed to a registry and run on extra standalone servers); a provider load balancer in
  front. "Coolify does not create or manage that external load balancer for you."
- Multi-server is refused for **apps with persistent storage** and for **Docker Compose apps**;
  previews are not deployed to additional servers.
- **No replicas on one server in v4.** On 23 Oct 2025 the maintainer wrote in discussion #3862
  that replicas do not "make sense for v4. We will have it in v5." No autoscaling of any kind.
- **Docker Swarm is "Experimental and deprecated … some parts of the integration were never
  fully implemented. Docker Swarm will be removed in Coolify v5."** v5 promises "native Docker
  Compose replicas and Coolify's own scaling solution" with no date (tracking issue #5685).
- Running containers keep serving when the control plane is down, as with Skifity.

### Observability (logs, metrics, alerts, uptime)

- **Sentinel** (`coolify-sentinel`, Rust): pushes a heartbeat, container state, Docker health and
  root-disk usage every 60 s; metrics sampling every 10 s with **7-day local retention** under
  `/data/coolify/sentinel`; charts from 5 minutes to 30 days. Metrics cover the server, a
  **non-Compose application** or a **standalone database** — not Compose apps or one-click
  services, and not build or Swarm servers.
- Container logs in the UI and API (`lines=all` since v4.3.20); deployment logs with level
  filtering and download; preview runtime logs over the API (v4.3.23).
- **Log drains** to Axiom, New Relic or a custom Fluent Bit config, opted in per resource;
  deployment logs are not forwarded.
- The docs are explicit that **threshold alerts on CPU/memory, external uptime checks and log
  alerting "require an external system"**. Server disk-usage threshold, unreachable-server and
  container-restart notifications exist.

### Security, auth, roles, SSO, audit

- Email and password, TOTP 2FA with recovery codes, rate limits per real client IP (v4.3.12).
- OAuth sign-in: GitHub, GitLab, Google, Azure/Entra, Bitbucket, Discord, Infomaniak. SSO with
  **named** providers Authentik, Clerk and Zitadel (v4.3.0). "Generic OpenID Connect
  configuration is not available" in v4.x stable; it is in v4.4-rc.1. Accounts are matched by
  email and **no provider groups map to teams or roles**.
- **Roles:** Owner, Admin, Member per team; **Member is read-only since v4.3.0**. Instance
  administration means being Owner or Admin of the default ("root") team. Terminals are limited to
  Owners and Admins.
- **API tokens:** per user and team, permissions `read`, `read:sensitive`, `write`, `deploy`,
  `root`; expiry of 7 days to 1 year or never; hourly expiry notifications. The API is **off by
  default on self-hosted** and can be limited to an IP allowlist.
- Credentials encrypted at rest with `APP_KEY`; webhook signature or token checks; audit log of
  API mutations and webhook events (v4.1.0); a **web terminal** into servers and containers;
  **server patching** (list and apply OS package updates, weekly check and notification).

### Preview environments and branches

- PR/MR previews from a GitHub App or repository webhooks (GitHub, GitLab, Bitbucket, Gitea);
  URL template `{{pr_id}}.{{domain}}` needing wildcard DNS; per-service domains for Compose
  previews; **a separate set of preview environment variables**.
- Fork PRs are blocked unless the author is an owner, member or collaborator, or "Allow Public PR
  Deployments" is on. A GitHub App posts and updates a **PR comment** with status and URL.
- Auto-deleted when the PR is closed or merged; preview volumes removed on delete (v4.3.11); a
  per-volume PR suffix option (beta.469); DNS status per preview (v4.3.15); manual previews of a
  Docker image tag for CI-built images; "Load Pull Requests" for PRs opened earlier.
- **No per-preview database** and no reclaim of previews whose close event never arrived.
  Discussion #4757 (37 votes) asks for `pr_id` inside preview variables so one database can be
  split by hand.

### Templates and catalogue

- `templates/service-templates.json` on `main` held **359 entries** on 2026-09-30 (last template
  updated 2026-09-28); the site still says "280+". Each template is a Compose file with comment
  metadata; eligibility requires **≥1,000 GitHub stars** and a matching docs PR.
- The contribution guide says "Do not use latest or other floating tags", yet decoding the 359
  Compose files (744 `image:` lines) finds **163 templates (45%) with an explicit `:latest`
  image** and **194 (54%) with at least one `:latest` or untagged image** (this analysis,
  2026-09-30).
- 189 templates are multi-container and 168 bundle their own database container. The top
  categories are productivity (72), media (27), ai (25), devtools (25) and cms (22).
- The most-voted open template requests are Sentry (207), MailCow (110), Kafka (69), Airflow (47),
  Firecrawl (42) and Postal (41).

### CLI, API, IaC, integrations

- **REST API** `/api/v1`: 275 documented endpoint pages across 24 groups — applications (37),
  databases (40), servers (30), services (27), shared variables (16), scheduled tasks,
  notifications, Hetzner/DigitalOcean/Vultr, GitHub/GitLab apps, cloud-init. Sensitive fields are
  hidden unless the token has `read:sensitive`.
- **CLI** (`coollabsio/coolify-cli`, Go, 473 stars): named contexts, table/JSON/pretty output,
  retries on 5xx and 429; 273 command pages covering apps, services, servers, database backups,
  shared variables, previews, tasks, storage and tags. It publishes `llms.txt` and
  `llms-full.txt` for agents.
- **MCP server built in** at `/mcp` over **Streamable HTTP**: an instance toggle and a per-team
  toggle; `read` tools plus `deploy` tools (deploy, cancel, start, stop, restart); entry points
  `coolify_help`, `get_infrastructure_overview`, `search_resources`, `list_unhealthy_resources`.
  "MCP responses never return environment values or file contents." Setup guides exist for
  Claude Code and Cursor.
- **IaC:** no official Terraform provider; community ones exist (`SierraJC/terraform-provider-
  coolify` 61 stars, `coolify-terraform/terraform-provider-coolify` 26 stars).
- Cloud server provisioning from the dashboard: Hetzner (firewalls, private networks, backups,
  cloud-init), DigitalOcean, Vultr.

### Notifications

- Channels: email (SMTP, Resend, or hosted on Cloud), Discord, Telegram (with topics), Slack,
  Mattermost, Pushover, and a generic JSON webhook with a published payload reference.
- Events are selected per channel: deployment success/failure, container status changes (stopped,
  restarted, restart limit reached), backup success/failure (including S3-upload warnings),
  scheduled-task success/failure, Docker-cleanup success/failure, server disk usage, server
  reachable/unreachable, server patching, Traefik outdated, API token expiring, missed backups.
- No per-application routing: discussion #4569 "Per-app notifications" is open.

### Multi-server and networking

- Add a server by SSH as root or a sudo user, or provision one from the dashboard. A "localhost"
  server runs Coolify itself. Docker network "destinations" group resources on a server.
- Servers are **independent Docker hosts**. Nothing encrypts or routes traffic between them:
  discussions "Full Tailscale integration" (#4975, 71 votes) and "VPN support" (#1847) are open.
- Teams cannot share a server (Q&A "Teams can't share a server?", 58 votes; #3235, 33 votes), but
  GitHub Apps can be system-wide since v4.3.11.
- Automated Docker cleanup; custom Docker DNS; exponential backoff for unreachable servers.

### Team and collaboration

- Unlimited teams and members (Cloud needs one subscription per team); email or link invitations.
- Shared variables at **team, project, environment and server** scope, referenced as
  `{{environment.KEY}}`.
- Tags, project icons, clone to another destination, move between projects and environments.
  **Moving a resource to another team** (#2029, 85 votes) and **per-resource membership** (issue
  #2378, milestoned to v5) are open.

### Developer experience and onboarding

- One-line installer (`curl -fsSL https://cdn.coollabs.io/coolify/install.sh | bash`), dashboard
  on port 8000. It supports Debian/Ubuntu, the RHEL family, SUSE, Arch, Alpine and Raspberry Pi OS
  (64-bit). The documented minimum is **2 CPU cores, 2 GB RAM, 10 GB disk**, with 1 core and 1 GB
  possible but "not recommended".
- The docs were rewritten in 2026 and the index `llms.txt` runs to 1,029 lines. Framework guides
  cover Next.js, Nuxt, SvelteKit, Laravel, Rails, Django and Phoenix. Auto-update is configurable.
- **English-only UI**: a Turkish translation PR (#11900) was closed on 21 Sep 2026 and its author
  noted "most of the dashboard is not externalized yet"; discussion #4717 asks for localisation.

## What users love

- **The catalogue.** Comparisons pick Coolify for the catalogue first: "Coolify has a huge
  catalog of one-click apps" (getdeploying, Jan 2026); "Coolify likely has a pre-configured
  template" for Supabase, Plausible or n8n (Contabo/LumaDock round-ups, 2026).
- **Free and complete.** Every feature is in the free self-hosted edition, under Apache-2.0 with
  no proprietary directories (pricing page; getdeploying contrasts this with Dokploy's mixed
  licence).
- **Low maintenance once running.** "I've been using Coolify for about a year now and have been
  very happy with it" — low maintenance, built-in backups, easy (HN, amanzi, 5 Apr 2025). Another
  commenter said "it's liberating" and that juniors pick it up quickly (HN, nikodunk).
- **Cheap managed control plane.** Cloud at $5/month with your own servers (pricing page), and
  3,765 paying Cloud users (business.json).
- **Community and content.** A 21,000-member Discord, the most tutorials in the category, and
  docs described as "LLM-optimized" (LumaDock, 2026).
- **Pace.** 24 stable releases between 12 Aug and 18 Sep 2026, plus an MCP server, a CLI, Railpack,
  volume backups and SSO in the five months after the stable tag.

## What users complain about

- **Security.** 75 CVEs and 70 GitHub advisories (see below), most of them authenticated command
  injection ending as root on the host. One reviewer's hardening advice starts with "Move the
  dashboard behind VPN or IP allowlist" (Hostzero, Jul 2026).
- **Weight and CPU spikes.** The official minimum is 2 CPU / 2 GB. MassiveGrid (Feb 2026) puts
  platform overhead at "approximately 750MB–1.2GB RAM", including OS and Docker. Open issue #3226
  reports a "High CPU Spike every hour" (confirmed bug, high priority, since Aug 2024), and there
  are Horizon CPU reports in #5651, #5676 and #6285.
- **Scaling is a runbook.** App scaling (#1579, 102 votes) and replicas (#3862, 64) are deferred
  to v5; multi-server needs a hand-built load balancer and excludes stateful and Compose apps;
  Swarm is deprecated.
- **Zero-downtime is partial.** "Zero-Downtime deployment" is the most-voted open feature
  request (#3767, 159 votes). Rolling updates skip Compose, and #8627 documents 502/503s during
  them.
- **Updates that break the proxy.** An auto-update silently switched Caddy back to Traefik and
  caused an HTTPS outage (#9127, Mar 2026, fixed). Traefik stopped after a Docker API bump (#7193,
  Nov 2025). Malformed routing labels needed manual fixes after each deploy (#6877).
- **Restore is the weak half of backup.** There is no restore UI for instance backups (#1684) or
  volume archives (docs), and Redis-family and ClickHouse databases have no restore at all.
  "Restore options aren't available in the GUI or documentation" (HN, tharos47, Apr 2025).
- **Restart still rebuilds sometimes** (#5748 open; #3560 "Restart container without triggering
  a new build", 31 votes).
- **Metrics are thin.** CPU and memory only, none for Compose apps or services, and no alerting on
  them (docs). "Display Current Server Utilization for Deployed Containers" is the third
  most-voted request (#2039, 139).
- **Coarse permissions.** Granular roles (#2378), team member permissions (#3232), shared servers
  across teams (#3235) and forced 2FA (#1892) are all open.
- **No support window.** Breaking changes landed inside a minor (v4.3.0: Member became read-only,
  the API became POST-only, router names changed). bex.co (Aug 2026): "Coolify does not document a
  separate security-only maintenance branch or an LTS window."

## Security record

**Where I looked (2026-09-30):** the repository's GitHub Security Advisories (7 pages,
**70 published**), the CIRCL Vulnerability-Lookup index of NVD records for vendor `coollabsio`
product `coolify` (**75 CVEs**), The Hacker News (8 Jan 2026), Hostzero (Jul 2026), WZ-IT (Jan
2026) and VulDB. Scores are the first score CIRCL lists: some CNAs publish CVSS 4.0 and others
3.1, and The Hacker News reported CVE-2025-66210…66213 at 10.0 where CIRCL shows 9.4.

**Shape of the record.** Of the 75, **36 are command injection or RCE** through a shell
(48%), **14 missing authorization or privilege escalation**, **10 cross-team IDOR**
(tenant isolation), 6 authentication or account takeover, 2 SSRF, 2 XSS, 1 information
disclosure and 4 other (webhook forgery, timing, unauthenticated endpoint, unvalidated upload).
Twenty score 9.0 or higher. They arrived in four waves by NVD publication date: January 2025 (9),
December 2025 – January 2026 (16; the "11 critical in one day" story on 8 Jan 2026), **late June –
mid July 2026 (46)**, and September 2026 (4). Most of the 2026 wave was fixed in betas 461–474
(March–April 2026) but **published after the stable tag**. Six CVEs affect stable v4 releases:
CVE-2026-12815 (4.0.0), 100746 (≤4.1.0), 15507 (≤4.1.1), 100744 (≤4.1.2), 84694 (<4.2.0) and
**86117 (≤4.3.17, OAuth account takeover, 9.2)**. For CVE-2026-12815 VulDB wrote that "the
vendor was contacted early about this disclosure but did not respond in any way".

The pattern, as WZ-IT put it: "Multiple independent code paths … implement the 'User Input →
Shell' pattern without sufficient validation." Coolify's answer was a regex allowlist
(`SHELL_SAFE_COMMAND_PATTERN`), and **that allowlist itself regressed** — CVE-2026-42204 let `&`
through in beta.471–473.

| CVE | NVD published | Score | Class | What | Fixed |
|---|---|---|---|---|---|
| CVE-2026-100746 | 2026-09-27 | 6.9 | Auth | GitHub App setup redirect missing authentication | 4.1.1 |
| CVE-2026-100744 | 2026-09-27 | 6.9 | Authz | `CanUpdateResource` route middleware bypass | 4.2.0 |
| CVE-2026-86117 | 2026-09-05 | 9.2 | Auth | OAuth callback signs into existing account by email alone | after 4.3.17 |
| CVE-2026-84694 | 2026-09-02 | 8.7 | Injection | Env var *key names* unescaped in Docker commands over SSH | 4.2.0 |
| CVE-2026-15507 | 2026-07-12 | 5.3 | Authz | Missing checks in `/app/Policies` | ≤4.1.1 affected |
| CVE-2026-59734 | 2026-07-09 | 8.8 | Injection | Health-check host/method/path into shell | beta.469 |
| CVE-2026-42201 | 2026-07-07 | 3.3 | Injection | Database credential fields into Compose commands | beta.474 |
| CVE-2026-34158 | 2026-07-07 | 8.8 | Injection | `executeInDocker()` single-quote breakout | beta.469 |
| CVE-2026-42200 | 2026-07-07 | 8.8 | Injection | PostgreSQL init-script path traversal → file write → RCE | beta.474 |
| CVE-2026-42172 | 2026-07-07 | 3.1 | Auth | API tokens never expired | beta.474 |
| CVE-2026-42147 | 2026-07-07 | 4.9 | SSRF | S3 endpoint `testConnection()` | beta.474 |
| CVE-2026-42145 | 2026-07-07 | 3.1 | Other | Backup-restore upload with no type or size check | beta.474 |
| CVE-2026-42143 | 2026-07-07 | 8.8 | Injection | Persistent volume names into shell | beta.471 |
| CVE-2026-34198 | 2026-07-07 | 5.3 | Auth | `TrustProxies='*'` → forwarded-host poisoning | beta.471 |
| CVE-2026-34171 | 2026-07-07 | 8.0 | Auth | GET invitation link resets password (CSRF takeover) | beta.471 |
| CVE-2026-34170 | 2026-07-07 | 4.3 | SSRF | GitHub App `api_url` | beta.471 |
| CVE-2026-34168 | 2026-07-07 | 8.8 | Injection | `LocalPersistentVolume.name` into `docker volume` | beta.471 |
| CVE-2026-34152 | 2026-07-07 | 8.8 | Injection | Newline in pre/post-deploy commands via heredoc | beta.471 |
| CVE-2026-34149 | 2026-07-07 | 3.3 | Injection | Backup job credentials / Mongo collection names | beta.471 |
| CVE-2026-34058 | 2026-07-07 | 8.8 | Injection | Unmanaged-container start/stop by browser-supplied id | beta.471 |
| CVE-2026-34057 | 2026-07-07 | 8.8 | Injection | Database import container/server properties | beta.471 |
| CVE-2026-34048 | 2026-07-07 | 9.9 | Authz | Terminal websocket: members execute on team servers | beta.471 |
| CVE-2026-34047 | 2026-07-07 | 9.9 | Authz | Terminal websocket missing middleware | beta.471 |
| CVE-2026-34044 | 2026-07-07 | 7.7 | Cross-team | `Logs::mount()` not team-scoped | beta.466 |
| CVE-2026-34037 | 2026-07-07 | 9.9 | Cross-team | `cloneTo()` resolves destinations unscoped | beta.464 |
| CVE-2026-34035 | 2026-07-07 | 8.8 | Injection | Log-drain secrets/env into shell | beta.466 |
| CVE-2026-34034 | 2026-07-07 | 8.8 | Injection | Sentinel token into shell → host RCE | beta.466 |
| CVE-2026-42204 | 2026-07-06 | 8.8 | Injection | Regression in `SHELL_SAFE_COMMAND_PATTERN` allowed `&` | beta.474 |
| CVE-2026-42153 | 2026-07-06 | 8.8 | Injection | PostgreSQL health check from `postgres_user`/`postgres_db` | beta.474 |
| CVE-2026-42148 | 2026-07-06 | 3.8 | Injection | `dev_helper_version` into `docker build` | beta.474 |
| CVE-2026-41899 | 2026-07-06 | 6.5 | Other | Unauthenticated, unlimited `/api/feedback` → Discord | beta.474 |
| CVE-2026-34599 | 2026-07-06 | 8.8 | Injection | GetLogs component: lowest role runs commands as root | beta.471 |
| CVE-2026-34167 | 2026-07-06 | 5.0 | Cross-team | ActivityMonitor unlocked property | beta.471 |
| CVE-2026-34153 | 2026-07-06 | 8.8 | Injection | File-volume `fs_path` into shell | beta.471 |
| CVE-2026-34050 | 2026-07-06 | 6.5 | Authz | Updates settings missing instance-admin check | beta.471 |
| CVE-2026-34049 | 2026-07-06 | 3.3 | Injection | Incomplete fix of CVE-2025-66209 (Mongo names) | beta.471 |
| CVE-2026-32718 | 2026-07-06 | 6.5 | Authz | `read` token can mutate via validation endpoints | beta.466 |
| CVE-2026-34038 | 2026-07-06 | 9.9 | Injection | Deployment fields → RCE and env exfiltration via logs | beta.469 |
| CVE-2026-27957 | 2026-06-30 | 8.8 | Injection | CA-certificate management | beta.464 |
| CVE-2026-27956 | 2026-06-30 | 4.3 | Cross-team | `servers/{uuid}/domains?uuid=` enumeration | beta.464 |
| CVE-2026-27955 | 2026-06-30 | 6.6 | Injection | `bash -c '{$command}'` quote breakout | beta.464 |
| CVE-2026-27883 | 2026-06-30 | 5.0 | Cross-team | `GET /api/v1/deployments/{uuid}` IDOR | beta.464 |
| CVE-2026-27882 | 2026-06-30 | 4.8 | Other | GitLab webhook token compared non-constant-time | beta.461 |
| CVE-2026-27881 | 2026-06-30 | 5.0 | Cross-team | Deployments IDOR (second report) | beta.464 |
| CVE-2026-34592 | 2026-06-29 | 7.7 | Cross-team | Server and project lookups unscoped (SSH keys exposed) | beta.471 |
| CVE-2026-41896 | 2026-06-29 | 7.5 | Other | Null webhook secret → forgeable HMAC → unauthenticated deploy | beta.474 |
| CVE-2026-34597 | 2026-06-29 | 8.8 | Injection | Nixpacks build parameters → host RCE | beta.470 |
| CVE-2026-34594 | 2026-06-29 | 8.8 | Injection | Destination network management | beta.471 |
| CVE-2026-57498 | 2026-06-29 | 9.6 | Cross-team | Livewire `server_id`/`destination_uuid` → deploy to other teams' servers | beta.474 |
| CVE-2026-12815 | 2026-06-21 | 5.3 | Injection | Image name handler (4.0.0) | 4.1.2 (changelog) |
| CVE-2025-64425 | 2026-01-05 | 8.5 | Auth | Password-reset host-header poisoning | >beta.434 |
| CVE-2025-64424 | 2026-01-05 | 9.4 | Injection | Git source fields, member → root | >beta.434 |
| CVE-2025-64423 | 2026-01-05 | 7.7 | Authz | Member uses admin invitation links | >beta.434 |
| CVE-2025-64422 | 2026-01-05 | 5.5 | Auth | Login rate limit bypass via `X-Forwarded-For` | after beta.434 |
| CVE-2025-64421 | 2026-01-05 | 8.7 | Authz | Member invites higher-privileged user | >beta.434 |
| CVE-2025-64420 | 2026-01-05 | 10 | Authz | Members see the root user's SSH private key | >beta.434 |
| CVE-2025-64419 | 2026-01-05 | 9.7 | Injection | `docker-compose.yaml` parameters from an attacker repo | beta.445 |
| CVE-2025-59955 | 2026-01-05 | 5.7 | Info | Team members API disclosure | >beta.420.8 |
| CVE-2025-59158 | 2026-01-05 | 9.4 | XSS | Stored XSS in project name | beta.420.7 |
| CVE-2025-59157 | 2026-01-05 | 10 | Injection | Git repository field | beta.420.7 |
| CVE-2025-59156 | 2026-01-05 | 9.4 | Injection | Compose directive injection by member | beta.420.7 |
| CVE-2025-66213 | 2025-12-23 | 9.4 | Injection | File storage directory mount path | beta.451 |
| CVE-2025-66212 | 2025-12-23 | 9.4 | Injection | Dynamic proxy config filename | beta.451 |
| CVE-2025-66211 | 2025-12-23 | 9.4 | Injection | PostgreSQL init script filename | beta.451 |
| CVE-2025-66210 | 2025-12-23 | 9.4 | Injection | Database import | beta.451 |
| CVE-2025-66209 | 2025-12-23 | 10 | Injection | Database backup | beta.451 |
| CVE-2025-24025 | 2025-01-24 | 6.1 | XSS | Reflected XSS on tag search | beta.380 |
| CVE-2025-22612 | 2025-01-24 | 10 | Authz | Any user reads any private key | beta.374 |
| CVE-2025-22611 | 2025-01-24 | 10 | Authz | Any user escalates anyone to owner | beta.361 |
| CVE-2025-22610 | 2025-01-24 | 5.7 | Authz | Instance OAuth client secrets readable | beta.361 |
| CVE-2025-22609 | 2025-01-24 | 10 | Authz | Attach any private key to your own server | beta.361 |
| CVE-2025-22608 | 2025-01-24 | 6.5 | Cross-team | Revoke any team's invitations (incrementing ids) | beta.361 |
| CVE-2025-22607 | 2025-01-24 | 4.7 | Cross-team | Read any GitHub/GitLab source by UUID | beta.361 |
| CVE-2025-22606 | 2025-01-24 | 8.5 | Injection | Project name into shell | >beta.358 |
| CVE-2025-22605 | 2025-01-24 | 8.5 | Injection | Remote command path executes in the Coolify container | beta.253 |

## Against Skifity

"Written" means code and unit tests exist but it has never run on a cluster (ADR-0010). Line
numbers are from the working tree read on 2026-09-30.

| Capability | Coolify | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git providers | GitHub, GitLab, Bitbucket, Gitea, any Git | GitHub, GitLab, Gitea webhooks and statuses; no Bitbucket (`internal/gitsrc/webhook.go`, `report.go`) | behind (small) |
| Deploy without a repository | Image, pasted Dockerfile, pasted Compose | Image, or a folder sent with `skifity up` (`internal/cli/up.go`, `internal/upload`) | parity |
| Build packs | Nixpacks default, Railpack beta, Static, Dockerfile, Compose | Railpack default, Nixpacks, Dockerfile, static dir (`internal/builder/detect.go`); Written | parity on paper |
| **Docker Compose** | Whole stack from Git or pasted, magic variables, previews | Read only: offers each service as a separate app; the `compose` source is refused (`internal/api/apps_handlers.go` "A Compose file is several services…"; ADR-0008) | **behind** |
| Config change without rebuild | Runtime-only change reuses image if the user picks Restart; #5748 open | Build fingerprint decides and says before save (ADR-0007, `internal/deploy`); Written | ahead (narrower than claimed) |
| Rollback | Old image with *current* settings | Old image *and* its settings; refuses a garbage-collected image (`internal/deploy/deployer.go` `Rollback`); Written | ahead |
| Zero-downtime deploys | Rolling update for single-container apps only; 502s reported (#8627) | `maxUnavailable: 0` plus 5 s `preStop` (`internal/kube/manifests.go`); Written | ahead on paper |
| Dedicated build machines | Build servers | Builds are in-cluster Jobs with no node pinning (`internal/builder/job.go`) | behind (small) |
| **Domains, TLS and routing** | | | |
| Automatic HTTPS | HTTP-01, DNS-01, wildcard certs, custom certs | cert-manager HTTP-01 installed on first domain (`internal/cluster/components.go`); no DNS-01, wildcard or custom cert | behind |
| Free address | `sslip.io` or a server wildcard | `sslip.io` over HTTP by design (ADR-0015) or `domains.wildcard` (`internal/settings/settings.go`) | parity |
| Redirects and auth | www↔apex, basic auth, custom middlewares, Authentik forward-auth | HTTPS redirect and password protection (`internal/kube/manifests.go`); no www redirect | behind (small) |
| Edge firewall | None built in | IP/country/ASN rules at the edge (`internal/edgerules`, `internal/guard`); never through a live Traefik | ahead on paper |
| DNS help | DNS validation, automatic Cloudflare records | Record shown as type/name/value with "Check again" (progress Phases 57–58); no provider automation | behind |
| **Databases and services** | | | |
| Engines | 8 (PG, MySQL, MariaDB, Mongo, Redis, Dragonfly, KeyDB, ClickHouse) | 3: PostgreSQL 17, Redis 7, "mysql" as MariaDB 11.4 (`internal/dbsvc/manifests.go`) | behind |
| Database high availability | None documented | CloudNativePG with an odd number of instances (`internal/dbsvc/manifests.go`); Written | ahead on paper |
| Reaching a DB from a laptop | Public TCP proxy with SSL | Private only; no port-forward or tunnel command | behind (by design) |
| Arbitrary multi-container stacks | Compose services | Only through templates (38 multi-service) | behind |
| **Storage and backups** | | | |
| DB backups to S3 with retention | Yes, plus S3-failure warnings | Yes, presigned URLs (`internal/backup`); Written | parity |
| DB restore | PG/MySQL/MariaDB/Mongo from file, upload or S3 | From the backup list (`internal/api/api.go` `/restore/{backupID}`, `internal/backup/jobs.go` `restoreScript`); Written | parity |
| Volume backup **and restore** | Backup only; restore by hand | Both, scaling the app to zero and back (`/volumes/{volumeID}/restore/{backupID}`, `docs/backups.md`); Written | **ahead** |
| Control-plane backup | Daily schedule, S3 copy | Manual `skifity admin backup-db` only (`internal/cli/admin.go`) | **behind** |
| Missed-backup alert | Yes (v4.3.18) | None; backups fire from the panel's minute tick (`internal/serverapp/serverapp.go` `runScheduler`) | behind |
| **Scaling and HA** | | | |
| More than one instance | None in v4 (deferred to v5) | Instances, HPA on CPU/memory, KEDA scale-to-zero (`internal/kube/apply.go`, `scaletozero.go`); Written | **ahead** on paper |
| Several servers | Standalone hosts plus your load balancer; not for stateful or Compose apps | One k3s pool with rescheduling; front door still yours (`docs/adding-servers.md`); Written | ahead on paper |
| Scaling readiness | A prose checklist in docs | `ScalingReadiness` names the variable that breaks (`internal/deploy/scaling.go`) | ahead |
| **Observability** | | | |
| Logs | Live, API, download, level filter | Live over SSE, plus the previous container's log (`llms.txt`); Written | parity |
| Metrics history | CPU/mem charts, 5 min – 30 days | Point-in-time from metrics-server (`internal/kube/client.go`); no chart library in `web/package.json` | **behind** |
| Resource alerts | Disk-usage threshold, unreachable, container restarts | `app.unhealthy`, `server.lost` (`internal/notify/notify.go`); no disk alert | behind |
| Log forwarding | Axiom, New Relic, Fluent Bit | None | absent in Skifity |
| Panel self-monitoring | "Nightwatch monitoring support" (beta.470 notes) | Prometheus `/api/metrics` behind auth (`internal/api/api.go`, `docs/configuration.md`) | different |
| **Security, auth, roles, SSO, audit** | | | |
| SSO | Named OIDC providers; generic OIDC only in 4.4-rc.1 | Generic OIDC with PKCE, nonce, single-use state, domain allowlist (`internal/auth/oidc.go`); Works | ahead until 4.4 ships |
| SSO account binding | Email match caused CVE-2026-86117 (fixed) | Also matches by email, no subject stored, absent `email_verified` accepted (`internal/api/sso_handlers.go` `GetUserByEmail`; `oidc.go`) | **same weakness** |
| Roles | Owner/Admin/**read-only** Member | Owner/Admin/Member; Member can create and deploy (`internal/store/models.go`; `apps_handlers.go` create requires `RoleMember`) | behind (no viewer) |
| API tokens | 5 permissions, expiry, API IP allowlist | `resource:action` scopes, TTL, team-bound (`internal/auth/scopes.go`, `service.go`); no IP allowlist | parity |
| Secrets | Encrypted; readable with `read:sensitive` and in the UI | Write-only on every surface, sealed to their context (`internal/crypto`, `llms.txt`) | ahead |
| Audit log | API mutations and webhooks | Team audit log (`/teams/{team}/audit` in `internal/api/api.go`) | parity |
| Webhook verification | Null-secret HMAC bypass, timing leak (CVEs above) | Empty secret refused, `hmac.Equal` (`internal/gitsrc/webhook.go` `VerifyGitHubSignature` etc.) | ahead |
| Shell-injection surface | 36 injection CVEs, regex allowlist | One quoting place tested against a real `sh` (`internal/shellsafe`); workloads are API objects, not shell | ahead architecturally, undemonstrated |
| Tenant isolation | 10 cross-team CVEs | Route walk: 91 team-scoped routes must 404 across teams (checklist #11); namespaces with default-deny (`internal/kube/namespace.go`) | ahead (panel Works, network Written) |
| Proxy header trust | `TrustProxies='*'` (CVE-2026-34198) | Trusted-proxy count, default 0 (`internal/api/middleware.go`) | ahead |
| Terminal | Web terminal (two 9.9 CVEs) | None, by decision; one-off `run` instead (`docs/roadmap.md`) | deliberate difference |
| **Preview environments** | | | |
| PR previews | URL template, PR comment, preview-only variables, fork gate | Per-PR namespace, commit status and PR comment (`internal/deploy/gitreport.go`), fork previews get no secrets (`webhook_handlers.go` `copyPreviewVariables`), TTL reclaim (`internal/watch/watch.go`); no preview-only variables | parity (different strengths) |
| Per-preview database | No | No; the copy inherits the parent's `DATABASE_URL`, which points at the parent environment's database across a default-deny namespace (`internal/dbsvc/manager.go` `Link`) | absent in both |
| **Templates** | | | |
| Catalogue | 359 entries, 45% with an explicit `:latest` | 282, every image a checked version (`internal/templates/catalogue`, README) | behind on count, ahead on pinning |
| **CLI, API, IaC, integrations** | | | |
| API breadth | 275 documented endpoints | ~150 routes (`internal/api/api.go`) | behind |
| CLI breadth | ~270 commands incl. backups, services, servers | 17 commands (`internal/cli/commands.go`), `--json` everywhere | behind |
| MCP | Remote Streamable HTTP; read + deploy tools | stdio only (`internal/mcpserver/server.go` `StdioTransport`); 15 tools incl. create, set variable, run, scale | parity (deeper tools vs remote transport) |
| Error format | Generic 5xx messages | cause/impact/fix everywhere (`internal/errdoc`) | ahead |
| Commit statuses | Not documented (PR comments only); #1671 "Update commit status on deployment" (48 votes) open | GitHub and GitLab statuses (`internal/gitsrc/report.go`) | ahead |
| IaC / export | Community Terraform; no export | `skifity.toml`; team export with Kubernetes objects (`/teams/{team}/export`); Works | ahead (export) |
| Cloud provisioning | Hetzner, DigitalOcean, Vultr | None | absent in Skifity |
| **Notifications** | | | |
| Channels | Email, Discord, Telegram, Slack, Mattermost, Pushover, webhook | Telegram, Discord, webhook, email plus plugin providers (`internal/notify/notify.go`, `internal/plugins`) | behind |
| Events | ~16 | 7 (`internal/notify/notify.go`) | behind |
| **Multi-server and networking** | | | |
| Adding a server | SSH key, validate, install Docker | Password once, own key installed, k3s joined in seven checked steps (`internal/provision`); Written | parity |
| Traffic between servers | Unencrypted; Tailscale asked for (#4975) | WireGuard flannel by default (`internal/settings/settings.go` `FlannelWireGuard`); Written | ahead |
| Tunnels | Cloudflare Tunnel guides | Cloudflare tunnel component (`internal/cluster/tunnel.go`); never reached Cloudflare | parity |
| Host upkeep | OS patching page, Docker cleanup | Registry GC only (roadmap 11.1); no OS or k3s update view | behind |
| **Team and collaboration** | | | |
| Clone and move resources | Across projects/environments | None | behind |
| Shared variables | Team, project, environment, server | Project level (`internal/deploy/deployer.go` `runtimeVariables`) | behind (small) |
| **Developer experience** | | | |
| Install and release | Released, one-liner, seven distro families | Installer written, **no tag** (`llms.txt`, `docs/checklist.md`) | behind |
| Footprint | Official minimum 2 CPU / 2 GB; ~0.75–1.2 GB overhead | Panel 35 MiB idle (`docs/performance.md`); k3s's own minimum is 2 cores / 2 GB (docs.k3s.io); whole stack unmeasured | unproven |
| Languages | English (Turkish PR closed) | en, id, hi, ru, zh-CN with a build gate (`web/src/locales`); Works | ahead |
| Maturity | Stable, ~512k installs counted | Nothing has run on a cluster (ADR-0010) | behind |

### Claims in this repository that no longer hold

- `docs/research/competitors.md` says of the MCP server: "Nobody else in the category has this."
  **Coolify has shipped one since v4.1.0 (18 May 2026)**, remote over Streamable HTTP with
  per-team toggles, plus a Go CLI that publishes `llms.txt`. What is still Skifity's is depth —
  create, set variables, run, scale, readiness — and errors written as cause, impact and fix.
- The same file says SSO is something "Coolify does not" have. Coolify has had named OIDC
  providers since v4.3.0 and generic OIDC in v4.4-rc.1.
- "A config change does not rebuild — the top Coolify complaint" is now narrower. Coolify's docs
  send runtime-only changes to Restart, which reuses the image. The gap is that the user has to
  pick the right button and that #5748 is open.
- "Coolify has no volume backups" (common in early-2026 comparisons) stopped being true on
  12 Aug 2026. Volume **restore** is still missing.
- The catalogue is **359**, not 342. "More than half ship `latest`" holds only when untagged
  images count: 54% with them, 45% counting an explicit `:latest` only.
- The spring-2026 CVEs were *fixed* in spring and *published* in late June and July 2026.

## Gaps worth closing in Skifity

**P0 — before the first tag**

1. **Deploy a Compose file as one stack.** *What:* turn a Compose file, from a repository or
   pasted, into one environment: each service becomes an app, recognised database images become
   managed databases or stay stateful apps, named volumes become disks, `depends_on` becomes
   start order, `${VAR:?}` becomes a required input, and whatever does not carry over is named.
   *Evidence:* every one of Coolify's 359 templates is Compose, as are most self-hosted apps'
   install instructions, and discussion #2270 (32) is about Compose `.env` handling. Today
   Skifity refuses the `compose` source and makes people create one app per service. Someone
   leaving Coolify arrives with a Compose file. *Fit:* `internal/builder` (the parser exists),
   the template installer in `internal/api/template_install.go` (it already installs 38
   multi-service templates), a "Stack from Compose" step in `web/src/pages/new-app.tsx`, and the
   same call in the CLI and MCP. *Size:* L. *Without a cluster:* conversion, rendered manifests,
   API and the Playwright flow, yes; a stack actually starting needs one.
2. **Scheduled off-server backup of the panel.** *What:* a setting that copies `panel.db` (via
   the existing `VACUUM INTO` path) on a schedule to the team's S3 bucket, never with the master
   key, plus `skifity admin restore-db`, a restore section in `docs/backups.md` and a warning when
   the last copy is old. *Evidence:* Coolify backs up its control plane daily by default and still
   gets #1684 (62 votes) asking for restore. Skifity's panel is pinned to one node with a SQLite
   file, so losing that node loses every team, app and setting unless someone ran `backup-db` by
   hand — which undercuts the "a server dies, nothing notices" pitch. *Fit:* `internal/serverapp`
   minute tick, `internal/backup` uploader, `internal/cli/admin.go`, Settings page. *Size:* S–M.
   *Without a cluster:* yes, against an `httptest` S3 and a smoke test.
3. **Bind SSO sign-ins to the identity, not the email.** *What:* store `(issuer, subject)` on the
   user when an SSO account is created or explicitly linked; sign in by that pair; never sign into
   an existing password account because the email matches, unless it was linked from inside the
   panel; treat a missing `email_verified` as unverified unless the admin opts in per provider.
   *Evidence:* Coolify's CVE-2026-86117 (9.2) is this exact flow. `internal/api/sso_handlers.go`
   looks the user up with `GetUserByEmail` and issues a session, and `internal/auth/oidc.go`
   accepts a token with no `email_verified` claim. The single configured IdP and the domain
   allowlist narrow it (a multi-tenant or self-service IdP widens it again), but this is a small
   fix to make before release rather than an advisory after. *Fit:* `internal/auth`,
   `internal/api/sso_handlers.go`, a store migration, an Account-page "link SSO" action. *Size:*
   S. *Without a cluster:* yes, unit tests plus `make smoke`.

**P1**

4. **Metrics history and charts.** *What:* sample per-app and per-node CPU and memory once a
   minute on the panel tick, keep 24 h at 1-minute and 7 days at 10-minute resolution in SQLite,
   and draw sparklines on the app and server pages. *Evidence:* #2039 (139 votes, third-highest);
   Coolify's docs make the metrics page part of the product. Skifity only reads point-in-time
   values. *Fit:* `internal/kube/client.go` readers, a new store table with retention (Phase 11.2
   pattern), `GET /api/apps/{app}/metrics?range=`, a chart built from shadcn primitives. *Size:*
   M. *Without a cluster:* sampler with a fake metrics client, store, API and Playwright with
   seeded data, yes; real numbers need a cluster.
5. **Alerts for what quietly goes wrong.** *What:* new events for a backup schedule with no
   success in N intervals (a missed backup is not a failed one), a server over a disk threshold,
   and a scheduled command failing. *Evidence:* Coolify added missed-backup alerts in v4.3.18 and
   has disk-usage and scheduled-task events. Skifity's backups depend on the panel's minute tick,
   so a panel that was down simply skips them and nothing says so, and the registry-GC phase
   exists because nodes fill up. *Fit:* `internal/watch`, `internal/notify` event list, the
   Settings channel form. *Size:* S. *Without a cluster:* missed-backup and job-failure, yes;
   disk (kubelet stats or node `DiskPressure`) needs one to prove.
6. **Previews that do not touch production data.** *What:* preview-only variable overrides, and a
   per-preview database option — an empty database, or one restored from the parent's latest
   backup — deleted with the preview. *Evidence:* Coolify gives previews their own variable set
   and users still ask for `pr_id` to split databases (#4757, 37 votes); managed platforms
   (Vercel with Neon branches, Railway PR environments) offer it, which was not re-verified for
   this page. Skifity copies the parent's variables, so a same-repository preview gets a
   `DATABASE_URL` for the parent environment's database. *Fit:* `internal/api/webhook_handlers.go`
   `deployPreview`, `internal/dbsvc`, backup restore into a new database, a Previews section on
   the app page. *Size:* M. *Without a cluster:* store, API and UI, yes; provisioning and restore
   into it need one.
7. **Import from Coolify.** *What:* `skifity import coolify --url … --token …` (and a panel form)
   that reads applications, domains, variables (with a `read:sensitive` token), databases and
   schedules through Coolify's API and recreates them as apps, databases and backup schedules,
   with a report of what did not carry over. *Evidence:* a counter of 511,959 installs; Coolify's
   own migration guide says there is no export/import; the API exposes all of it. *Fit:*
   `internal/cli`, a new `internal/importer`, reusing template-install code. *Size:* M. *Without
   a cluster:* yes, against a recorded Coolify API in `httptest`.
8. **A remote MCP endpoint.** *What:* serve the existing tools over Streamable HTTP at `/mcp`,
   authenticated with the same scoped tokens and a per-team switch, so claude.ai or Cursor can
   connect without installing the binary. *Evidence:* Coolify's MCP is remote with Claude Code
   and Cursor guides; Skifity's `mcpserver` is stdio only. *Fit:* `internal/mcpserver`,
   `internal/api` routes and middleware. *Size:* S–M. *Without a cluster:* yes.
9. **A read-only role.** *What:* a Viewer role below Member (or Member made read-only with a
   Deployer above it), enforced in the existing `authorize*` helpers and covered by the route
   walk. *Evidence:* Coolify made Member read-only in v4.3.0; #2378 and #3232 ask for finer
   roles. Today a Skifity Member can create and deploy. *Fit:* `internal/store/models.go` roles,
   `internal/api`, the invite and members UI. *Size:* S. *Without a cluster:* yes.

**P2**

10. **Notification breadth.** Slack- and Mattermost-compatible webhooks and Pushover as built-in
    channels, and events for backup success, API token expiring and certificate renewal failing
    early. `internal/notify`. S, no cluster.
11. **`skifity db connect` and a wider CLI.** A port-forward through the panel (the safe answer
    to Coolify's public TCP proxy), plus `db backup|restore|list`, `domains`, `jobs` and
    `previews` commands. `internal/cli`, `internal/api`. M; the CLI is testable in `make smoke`,
    the tunnel needs a cluster.
12. **MongoDB, then Valkey and ClickHouse, as managed engines.** Coolify's database-request list
    is led by CouchDB (36), Valkey (32) and InfluxDB (29), and MongoDB is the most common engine
    Skifity lacks. `internal/dbsvc`, `internal/backup`. M–L; needs a cluster.
13. **Routing extras.** www↔apex redirect, DNS-01 wildcard certificates through Cloudflare, and
    optionally creating the DNS record. `internal/kube/manifests.go`, `internal/cluster`. M;
    manifests testable, issuance needs a cluster and a zone.
14. **Deploy on tag or release, `[skip ci]`, Bitbucket.** Coolify has not shipped deploy-on-release
    (#4532, 28; #3471, 27), so this one would lead. `internal/gitsrc/webhook.go` currently
    ignores tag pushes. S, no cluster.
15. **Clone an app or an environment** into another environment or project, without data at
    first. `internal/api`, `internal/store`. M; mostly without a cluster.
16. **Create servers at Hetzner** (then DigitalOcean) from the Add Server page. `internal/provision`.
    M; API mocks without a cluster, a real run needs an account.
17. **Log forwarding** (OTLP or Loki) as a plugin-provided capability through `provides`.
    `internal/plugins`. M; needs a cluster.
18. **Team-wide 2FA enforcement and an API IP allowlist.** Coolify's #1892 (25) is open, so this
    one would lead too. `internal/auth`, `internal/api/middleware.go`. S, no cluster.
19. **A k3s and OS update view** per server. `internal/provision`, server page. M–L; needs a
    cluster.

## Things to deliberately not copy

- **A browser terminal into servers and containers.** Coolify's terminal websocket produced two
  9.9 CVEs (CVE-2026-34047, 34048) that let the lowest role run commands on team servers. The
  roadmap's reasoning holds: a one-off command in the app's image is safer and more useful.
- **Many paths from user input to a shell, guarded by a regex allowlist.** Thirty-six of
  Coolify's 75 CVEs are that, and the allowlist regressed (CVE-2026-42204). Keep one quoting
  function tested against a real shell, and keep workloads as API objects rather than scripts.
- **Escape hatches that hand the host to the user.** Raw Compose deployment, free-form Docker run
  options, custom proxy files and host-path mounts each became an injection or traversal CVE.
  Coolify removed host-path mounts itself in v4.3.22.
- **Root SSH as the permanent control channel.** Coolify keeps a root-capable private key for
  every server and three CVEs leaked it (2025-22609, 2025-22612, 2025-64420). Skifity should keep
  SSH to provisioning and removal and talk to the Kubernetes API otherwise.
- **Secrets that can be read back.** `read:sensitive` tokens and UI reveal make every
  authorization bug a secret leak. Keep secrets write-only.
- **Instance admin as a side effect of a team role.** "Owner or Admin of the root team" is how a
  settings page shipped without an instance-admin check (CVE-2026-34050).
- **`latest` in the catalogue and "Pull Latest & Restart"** — a rollback to `latest` is not a
  rollback.
- **Unattended control-plane updates that restart the proxy** (#9127: an auto-update silently
  changed the proxy and took HTTPS down). Skifity's "never phones home, upgrade on purpose" is the
  right default.
- **Breaking changes inside a minor and no support window.** v4.3.0 changed roles, HTTP methods
  and router names; nothing says how long a release is patched. When Skifity tags, write the
  support policy on the same day.
- **Convenience endpoints without authentication** (the feedback-to-Discord endpoint,
  CVE-2026-41899) and **email-matched OAuth sign-in** (CVE-2026-86117; see gap 3).
- **Half-built second orchestrators.** Swarm was "experimental … never fully implemented" and is
  being removed after two years. Skifity's one-cluster rule in the roadmap avoids the same trap.

## Sources

All read on 2026-09-30.

- https://github.com/coollabsio/coolify (repository metadata through the GitHub search API: stars, forks, open issues, licence)
- https://github.com/coollabsio/coolify/releases, `?page=2`, `?page=3`, `?page=4`
- https://github.com/coollabsio/coolify/releases/tag/v4.0.0
- https://github.com/coollabsio/coolify/releases/tag/v4.3.0
- https://github.com/coollabsio/coolify/releases/tag/v4.4-rc.1
- https://github.com/coollabsio/coolify/tags
- https://github.com/coollabsio/coolify/pulls (open and closed counts)
- https://raw.githubusercontent.com/coollabsio/coolify/main/RELEASE.md
- https://raw.githubusercontent.com/coollabsio/coolify/main/templates/service-templates.json (decoded and analysed locally)
- https://github.com/coollabsio/coolify/security/advisories?state=published (pages 1–8)
- https://cvepremium.circl.lu/search?vendor=coollabsio&product=coolify (75 NVD records, parsed locally)
- https://github.com/coollabsio/coolify/discussions?discussions_q=is%3Aopen+sort%3Atop
- https://github.com/coollabsio/coolify/discussions/categories/feature-requests (sorted by top, pages 1–2)
- https://github.com/coollabsio/coolify/discussions/categories/improvement-requests
- https://github.com/coollabsio/coolify/discussions/categories/database-requests
- https://github.com/coollabsio/coolify/discussions/85, /3862
- https://github.com/coollabsio/coolify/issues?q=is%3Aissue+is%3Aopen+sort%3Areactions-%2B1-desc
- https://github.com/coollabsio/coolify/issues/5685, /5748, /3226, /2378, /8627, /9127, /7193, /5676
- https://github.com/coollabsio/coolify/pull/11900
- https://github.com/coollabsio/coolify-cli (via the GitHub search API)
- https://github.com/coollabsio/sentinel (via the GitHub search API)
- https://github.com/SierraJC/terraform-provider-coolify and https://github.com/coolify-terraform/terraform-provider-coolify (via the GitHub search API)
- https://coolify.io/ and https://coolify.io/pricing
- https://cdn.coollabs.io/business.json and https://undead.coolify.io/instances
- https://coolify.io/docs/llms.txt (the documentation index)
- https://coolify.io/docs/core/how-coolify-works
- https://coolify.io/docs/core/selfhosted-cloud-comparison
- https://coolify.io/docs/core/security-model
- https://coolify.io/docs/start-with-self-hosted and https://coolify.io/docs/start-with-cloud
- https://coolify.io/docs/core/infrastructure/scaling/overview, /multi-server-deployments, /cloud-load-balancing, /docker-swarm
- https://coolify.io/docs/core/infrastructure/servers/build-servers, /provision-cloud-server, /server-patching, /web-terminal
- https://coolify.io/docs/core/team/roles-and-permissions and https://coolify.io/docs/core/team/shared-variables
- https://coolify.io/docs/core/security/credentials/api-tokens
- https://coolify.io/docs/core/security/authentication/sso/overview
- https://coolify.io/docs/core/observability/monitoring/overview, /sentinel, /metrics
- https://coolify.io/docs/core/observability/log-drains/overview
- https://coolify.io/docs/core/notifications/overview and /events
- https://coolify.io/docs/core/persistent-storage/storage-mounts/backups
- https://coolify.io/docs/core/backup-and-recovery/instance-backup
- https://coolify.io/docs/databases/backups and https://coolify.io/docs/databases/restore
- https://coolify.io/docs/applications/deployments/preview-deployments, /rolling-updates, /rollbacks
- https://coolify.io/docs/applications/sources/github/preview-deploy
- https://coolify.io/docs/applications/builds/overview, /docker-compose, /railpack
- https://coolify.io/docs/applications/configuration/environment-variables
- https://coolify.io/docs/applications/operations/migrate-between-instances
- https://coolify.io/docs/core/networking/domains and /dns
- https://coolify.io/docs/services and https://coolify.io/docs/contribute/service
- https://coolify.io/docs/mcp/overview, /how-mcp-works, /capabilities/overview, /security
- https://coolify.io/docs/cli/what-is-the-coolify-cli and /how-cli-works
- https://docs.k3s.io/installation/requirements
- https://thehackernews.com/2026/01/coolify-discloses-11-critical-flaws.html
- https://hostzero.com/articles/is-coolify-safe-2026-cves
- https://wz-it.com/en/blog/coolify-cve-security-vulnerabilities-update-2025-2026/
- https://bex.co/blog/2026/08/16/coolify-v4-stable-what-production-ready-means
- https://temps.sh/blog/coolify-review-2026 (a competitor's review; used only for dates it states)
- https://massivegrid.com/blog/coolify-resource-planning-multiple-apps/
- https://getdeploying.com/guides/coolify-vs-dokploy
- https://news.ycombinator.com/item?id=43589149
- Web search result summaries for Contabo, LumaDock and Northflank Coolify-vs-Dokploy comparisons (2026), cited only for the "catalogue" and "LLM-optimized docs" points
