# Dokploy

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Dokploy (Dokploy/dokploy, dokploy.com) is a self-hosted PaaS that describes itself as an "open
source alternative to Vercel, Netlify and Heroku". It is for developers and small teams who want
to push Git, a Docker image or a Compose file to their own VPS and get HTTPS, databases and
backups without learning an orchestrator. **Architecture:** a Next.js application (tRPC,
Drizzle ORM, better-auth) that runs as a Docker Swarm service, a PostgreSQL 16 service for
all of its own state, and a Traefik v3.6.25 container for routing. The installer installs
Docker 28.5.0 and runs `docker swarm init` on the first server. Configuration files such as
Traefik dynamic config and Compose files live under `/etc/dokploy`. Remote servers are reached
over SSH and each runs its own Traefik. Multi-node means Docker Swarm workers and managers
with an external registry. The current installer creates no Redis service. In August 2025 the
maintainer described the stack as "Next.js, PostgreSQL, Traefik and Redis" (discussion #2476),
so Redis has been dropped since then; I could not confirm which release dropped it. A separate
Go monitoring agent ships as `dokploy/monitoring`. **Licence:** Apache 2.0, except code
under `/proprietary` directories. Since 21 January 2026 that code is under the "Dokploy
Source Available License" (DSAL v1.0), which forbids production use without a commercial
agreement. It covers SSO/SAML, SCIM, audit logs, white-labelling and license keys.
**Pricing:**
self-hosted OSS is free. Dokploy Cloud (hosted control plane, your servers over SSH) costs
$4.50 per server per month (Hobby: 1 server, 1 user), or $15/month for the Startup plan
(3 servers included, unlimited users, then $4.50 per extra server), with 20% off yearly.
Enterprise (cloud or self-hosted) is "contact sales". **Maturity:** still pre-1.0. The latest
release is **v0.30.8 (29 September 2026)**, and the only open milestone, `v1.0.0`, is 92%
done (25 of 27 issues closed) with no due date. It shipped 40 releases in 2026.
**Adoption:** 37,579 GitHub stars, 3,023 forks, 541 open issues and 186 open PRs (GitHub,
30 Sep 2026). `dokploy/dokploy` has 14,767,856 Docker Hub pulls (30 Sep 2026). In the month
to 28 Sep 2026 npm counted 23,002 downloads of `@dokploy/mcp`, 5,224 of `@dokploy/cli` and
2,408 of `@dokploy/sdk`. The official catalogue lists 518 templates (templates.dokploy.com,
30 Sep 2026).

**Two findings correct `docs/research/competitors.md` (17 Sep 2026):**
1. That page says "Dokploy has no publicly disclosed CVEs". This is now false. The
   repository has **60 published GitHub security advisories**: 34 critical, 17 high, 8
   moderate and 1 low. 42 of them were published on a single day, **21 July 2026**, and
   most are OS command injection or cross-organisation IDOR. One of them, CVE-2026-45631, is
   a pre-authentication admin takeover rated CVSS 10.0.
2. That page gives Dokploy's own state as "PostgreSQL and Redis". The current installer
   runs only PostgreSQL.

## Release history (2026)

Dates come from GitHub release pages. Docker Hub tag push dates were used where a page gave
no year.

| Version | Date | What mattered |
|---|---|---|
| v0.26.3 – v0.26.5 | 2 – 15 Jan | IANA time zones, choosing a Railpack version, keeping the last N backups, rebuilding a preview by hand, billing |
| **v0.26.6** | 27 Jan | Security release fixing three advisories: a hardcoded Postgres password in `install.sh` (CVE-2026-24840), command injection in the container-terminal WebSocket (CVE-2026-24841, CVSS 9.9) and clickjacking |
| v0.26.7 | 31 Jan | Pushover notifications, PostgreSQL 18 as the default, Traefik 3.6.7 |
| — | 21 Jan | Licence split: Apache 2.0 core plus DSAL for `/proprietary` |
| v0.27.0 | 10 Feb | "Introduce license key pay", resending notifications, internal URLs for GitLab and Gitea |
| v0.27.1 | 18 Feb | SSO trusted origins, IDN domains, Microsoft Teams notifications, Soft Serve webhooks, deleting old deployments. Followed by a memory regression report (#3755) |
| v0.28.0 | 27 Feb | Patches (file overrides applied before the build), ZIP-drop deploys onto the build server, keeping the last N backups, volume-backup notifications, new pricing, Node 24 |
| v0.28.1 – v0.28.8 | Feb – 19 Mar | `docker login` before a rollback, submodules, watch paths in the schema, GitHub `labeled` action for previews, search |
| **v0.29.0** | 17 Apr | AI analysis of logs and build errors, an **MCP server with 508 tools**, a **CLI with 449 commands**, organisation-wide shared Git providers, non-root sudo users for remote servers, libSQL as a database, Mattermost, uploading files into a container. Enterprise: assigning Git providers and servers to individual members |
| v0.29.2 | 25 Apr | Authorisation checks for server- and host-level schedules |
| **v0.29.3** | 11 May | Replaces the hardcoded `BETTER_AUTH_SECRET` (CVE-2026-45631, CVSS 10.0) and requires running `curl … /security/0.29.3.sh \| bash`. 11 advisories published |
| v0.29.4 – v0.29.8 | May – 8 Jun | Deployment worker and queue refactor, base64 Compose import, forward-auth "Application Authentication" (Enterprise) |
| v0.29.9 – v0.29.11 | 6 – 9 Jul | Per-server build queue (2 concurrent builds), OIDC claim mapping, plan limits, **SCIM 2.0** (Enterprise) |
| v0.29.12 | 13 Jul | **Environment variables encrypted at rest** (AES-256-GCM), keyring export |
| **v0.29.13** | 21 Jul | 16 security PRs (command injection, cross-org IDOR, missing WebSocket authorisation). **42 advisories published that day** |
| **v0.30.0** | 14 Aug | Docker network management (and "Isolated Deployments" deprecated), **DNS provider integration** (Cloudflare, Route53), **external secret providers** (HashiCorp Vault/OpenBao, Infisical, AWS Secrets Manager, Doppler, Scaleway), one Docker dashboard (containers, volumes with a file explorer, networks, events, images, disk usage, Swarm), session management, **passkeys**, a default role for new members, a toggle to disable a domain |
| v0.30.1 – v0.30.8 | 18 Aug – 29 Sep | Porkbun, Infomaniak and OVHcloud DNS, Phase.dev and AWS Parameter Store secrets, bulk import from vaults, Cloud onboarding wizard, Compose "fresh volume" deploys, missing Railpack versions |

**Is it 1.0?** No. It is v0.30.8 on 30 September 2026. A community answer in discussion
#4175 (April 2026) advises pinning versions and "waiting for the 1.0 release for
customer-facing applications requiring SLAs".

## Feature inventory

### Deploy sources and builds
- Git sources: a GitHub App, GitLab, Gitea, Bitbucket, and any Git URL over HTTPS or SSH.
  Also Docker Hub or any registry image, a ZIP drop, raw Compose YAML pasted into an editor,
  and a base64 template import. Soft Serve webhooks arrived in v0.27.1.
- Build types: **Nixpacks (the default)**, **Railpack** with a selectable version,
  Dockerfile (path, context and multi-stage target), Heroku buildpacks (default stack 24),
  Paketo buildpacks, and Static (an nginx image). Build-time arguments and secrets are
  supported.
- **Docker Compose** is native in two modes: `docker compose`, and `stack`, which deploys
  to Swarm and does not support `build:`. Domains are injected as Traefik labels.
  "Randomize" utilities rename resources so a template can be deployed twice.
- **Patches**: edit, add or delete files in the cloned repository before every build,
  without committing (v0.28.0).
- **Watch paths**: globs decide whether a push deploys. They work for applications and
  Compose, on GitHub, GitLab, Bitbucket and Git.
- Custom build servers (v0.26.0) clone, build and push to a registry, but only for
  Applications, not Compose. Each server has its own build queue (v0.29.9/v0.30.0).
- Auto-deploy comes from provider webhooks, a generic webhook URL, or the API from CI. You
  can cancel queued builds, kill a running build, and rebuild a preview by hand.
- Missing: no pre- or post-deploy command. Issue #110, open since May 2024, has 41 👍.

### Domains, TLS and routing
- Traefik v3 with Let's Encrypt. Free `traefik.me` hostnames are **HTTP only**. A custom
  certificate can be uploaded and toggled per domain (with open bugs #4503, #4707, #4949).
- **DNS provider integration** (v0.30.0+) creates records automatically on Cloudflare,
  Route53, Porkbun, Infomaniak and OVHcloud.
- Path-based routing, strip-path and internal-path middlewares, preset redirects (for
  example www to apex), HTTP basic auth per app, and a raw Traefik file editor (per app and
  server-wide).
- **Application Authentication (Enterprise)**: an oauth2-proxy forward-auth gate over
  OIDC in front of any app.
- The docs include guides for Cloudflare Tunnels and Tailscale.
- Missing: a WAF (#1547, 19 👍), configurable IP allowlists (#1408), response compression
  (#3494) and Caddy as the proxy (#1246, **70 👍, the most-upvoted open issue**).

### Databases and services
- One-click PostgreSQL, MySQL, MariaDB, MongoDB and Redis, plus **libSQL** (v0.29.0). Each
  has a custom image, resource limits, logs, a terminal, and optional external credentials
  on a public port.
- There is no replication, failover or HA for any engine. The docs say nothing about it,
  and none was found.

### Storage and backups
- **Database backups** go to S3-compatible destinations (AWS, R2, B2 and others through
  rclone) on a cron schedule, keeping the last N. There is a Test button. Restore from S3
  is supported for Postgres, MySQL, MariaDB and MongoDB. CERT/CC VU#280377 also lists
  libSQL among the engines the backup pipeline handles.
- **Volume backups** (a distinctive feature) cover Docker **named volumes only**, not bind
  mounts, for Applications and Compose. The container can be stopped during the backup (the
  recommended option) or left running. Restore goes into a volume name that must not
  already exist.
- **Web-server backup**: the panel's own Postgres plus `/etc/dokploy`, zipped to S3 on a
  schedule, with a restore that drops and replaces both.
- Missing: backup encryption (#2926, 18 👍), local or non-S3 destinations (#416, #1670) and
  NFS/CIFS mounts (#790). An open regression, #4051 (19 👍), is that a backup path change
  broke user prefixes.

### Scaling and high availability
- A **Replicas** field set by hand. The "Swarm Settings" take raw JSON (health check,
  restart policy, placement, update and rollback config, mode, labels). CPU and memory are
  entered in **nanoCPUs and bytes**.
- Zero-downtime deploys are **off by default**. They need a hand-written Swarm health check
  JSON, work for applications only (not Compose), and without one "Bad Gateway" errors are
  expected during a deploy.
- Rollbacks are **registry-based**: each deploy pushes a tagged image, and a rollback
  restores **the image only**.
- Missing: autoscaling (#2579, **54 👍**, asks for Compose replicas plus an HPA-like
  autoscaler), scale-to-zero (#2154, 26 👍), and HA for Traefik or the panel (discussion
  #4060 calls Traefik "a single point of failure").

### Observability (logs, metrics, alerts, uptime)
- Live container logs and build logs. The last 10 deployments are kept per app.
- CPU, memory, disk and network graphs per app and per server. The data comes from the
  `dokploy/monitoring` agent on port 4500 with a metrics token, collected every 20 s by
  default, with 2 days of retention by default.
- **The advanced monitoring page (configurable refresh and retention, CPU and memory
  threshold alerts sent to notification channels) is marked "only available on Cloud Version
  of Dokploy"**. The comparison table gives OSS "Basic" monitoring. On self-hosted,
  remote-server monitoring is unsupported (#2685, 12 👍).
- **AI analysis of logs and build errors** (v0.29.0) uses your own API key.
- Missing: log drains (#2748), Prometheus as a backend (#2376, 19 👍), and uptime checks.

### Security, auth, roles, SSO, audit
- Email and password, TOTP 2FA, **passkeys** (v0.30.0), and a session list with revocation
  (v0.30.0). API keys with expiry for the CLI, SDK and MCP.
- OSS roles: Owner, Admin and Member. Members get per-feature toggles (create projects,
  terminal, API/CLI, Git providers, SSH keys, Docker, Traefik files) and access per project,
  environment or service.
- **Enterprise only** (proprietary code, licence key validated **"every day against our
  servers"** using the server's IP address):
  - SSO over OIDC **and SAML**, with guides for Auth0, Entra ID, Okta, Keycloak and Zitadel
  - SCIM 2.0 provisioning
  - custom roles
  - **audit logs**
  - white-labelling
  - forcing SSO for an organisation
- Secrets: environment variables have been encrypted at rest since v0.29.12 (July 2026).
  Since v0.30.0, external secret providers resolve values at deploy time and never store
  them.
- A built-in **web terminal** to the host over SSH and into any container, over WebSocket.

### Preview environments and branches
- **GitHub only.** GitLab MRs (#1483, 20 👍) and Gitea/Forgejo (#3828) are open requests.
- One preview per PR at `preview-<app>-<id>.traefik.me` or a wildcard domain. A limit per
  app (default 3), a PR label filter, removal on close or merge, a PR comment, and manual
  rebuilds.
- Applications only: **no previews for Compose** (#2028, 51 👍, the #3 open request), none
  from prebuilt images (#2386, 23 👍), and none from the API (#1365).
- There is no per-preview database or migration step (#2462, 28 👍) and no per-preview
  variable overrides (#2743). A Hacker News user calls previews "almost useless because
  there is no variable to get the preview URL dynamically".
- The docs warn against using previews on public repositories. Before 0.24.3 anyone could
  fork a public repo and get code with the app's secrets deployed (CVE-2025-53825, 9.8).

### Templates and catalogue
- **518 templates** (templates.dokploy.com/meta.json, 30 Sep 2026). Each is a Compose file
  plus `template.toml` (domains, env, mounts) plus `meta.json`, in the `Dokploy/templates`
  repository (226 stars), and every PR there gets a preview deploy.
- Image pinning is loose. I fetched all 518 `docker-compose.yml` files: **227 templates
  (44%) use at least one image on `:latest` or with no tag**, which is 292 of 1,154 image
  references.
- **AI Compose generator**: describe what you want, and a model you configure (any
  OpenAI-compatible API, including Anthropic, Ollama, OpenRouter and Azure) writes the
  `docker-compose.yml`.
- Missing: private or custom template sources (#2414, 18 👍).

### CLI, API, IaC, integrations
- A tRPC API with an OpenAPI spec (`openapi.json` at the repository root). The CLI,
  `@dokploy/cli` (**449 commands, generated from the OpenAPI spec**, with `--json`), and
  the TypeScript SDK, `@dokploy/sdk` (May 2026), are both built from it.
- The **MCP server**, `@dokploy/mcp`: 508 tools in 49 categories, generated from OpenAPI,
  over stdio or Streamable HTTP/SSE. It has tool presets
  (`DOKPLOY_TOOL_PRESET=minimal|core|deploy|databases|git`) and tag filters. Secrets are
  redacted by default, and tools carry the `readOnlyHint`, `destructiveHint` and
  `idempotentHint` annotations.
- Missing: infrastructure as code. Pulumi support (#1555) has 42 👍, Import/Export projects
  (#1733) has 38 👍, and Azure DevOps as a source (#1949) has 22 👍.

### Notifications
- **12 providers:** Slack, Discord, Telegram, Microsoft Teams, Mattermost, Lark, email
  (SMTP), Resend, Pushover, Gotify, ntfy and a generic webhook.
- **Events:** app deployed, build error, database backup, volume backup, Docker cleanup and
  Dokploy restart. CPU and memory threshold alerts exist on Cloud monitoring.
- Open bug #4693: build-failure emails and server logs contain the **registry password in
  plain text**.

### Multi-server and networking
- Remote servers are added with an SSH key, as root or (since v0.29.0) a passwordless-sudo
  user. Dokploy installs Docker and Traefik on each and runs a security validation (UFW,
  SSH hardening, fail2ban).
- **Each remote server is independent**, with its own Traefik. A Swarm cluster with
  workers and managers is separate and needs a registry.
- v0.30.0 added Docker network management (overlay or bridge, MTU, IPAM) and attaching a
  service to a network. Before that, isolation was "Isolated Deployments" for Compose,
  which v0.30.0 deprecated. Isolating containers by project (#129) and "Network Management
  and Security Concerns" (#2798, 24 👍) are still open.

### Team and collaboration
- Multiple organisations per instance, with invitations and a default role (v0.30.0).
  Projects contain environments, and variables are inherited from project to environment
  to service.
- Git providers can be shared with the whole organisation (v0.29.0), and projects can have
  tags.
- Cloud plans cap organisations: Hobby 1, Startup 3.

### Developer experience and onboarding
- One-line install, `curl -sSL https://dokploy.com/install.sh | sh`. Needs **2 GB RAM and
  30 GB disk** and supports Ubuntu, Debian, Fedora and CentOS. Proxmox LXC is detected.
- Idle footprint: "300–400 MB idle RAM (community-reported)" (Cloudzy, June 2026). Issue
  #3755 (Feb 2026) measured the Dokploy container alone at 464 MiB fresh and 633 MiB with a
  working stack on v0.27.1. Postgres and Traefik come on top of that.
- A Docker dashboard for people who still think in Docker: containers, images, volumes
  with a file explorer, events and disk usage.
- Cloud has an onboarding wizard (v0.30.5).
- The UI has partial community translations (PRs #792, #994). I could not verify current
  language coverage.

## What users love

- **"Heroku vibe" and UX.** "Solid and stable for the last 12 months running production
  apps. First time in ages I got the Heroku vibe again" (risico, HN, 16 Aug 2025).
  "Hands down the best UX for delivering apps & databases" in a Heroku-style PaaS, over
  Coolify, Dockge and Komodo (radlad, HN, c. April 2026).
- **Staying close to Docker and Compose.** "Dokploy is designed to work directly with
  standard Docker Compose files … the platform largely treats it the same way Docker itself
  would" (LogRocket, 3 Feb 2026). Comparison sites repeat this.
- **Volume backups and host-level schedules**, which Coolify lacks: named volumes go to S3,
  and cron commands can run in containers or on the host (LogRocket, 3 Feb 2026; Dokploy's
  own comparison table).
- **Install speed and a lighter runtime than Coolify.** Its installer ran "21 minutes
  faster in my case" (LogRocket). Idle usage is around 300–400 MB (Cloudzy, June 2026).
- **Built-in monitoring graphs** for CPU, memory, disk and network per app (LogRocket;
  CherryServers).
- **Multi-node from day one**, since Swarm is initialised at install time (bex.co,
  Sep 2026).
- **Fast responses to security reports.** In 2024 a researcher found `auth.createAdmin`
  still open after setup on about 610 instances, and the maintainer shipped a fix in 0.7.2
  quickly (dev.to, 27 Aug 2024).

## What users complain about

- **Licence and "SSO tax".** OIDC, audit logs and SAML are behind a paid Enterprise
  licence. Hacker News users call the licensing "complex" and complain that "OIDC and audit
  logs" are paid (Aug 2025 thread; radlad, 2026). LogRocket: the project "adds restrictions
  around certain features, especially concerning resale and commercial use".
- **Swarm fragility.** Recurring, open issues:
  - Traefik routes time out on a stale service VIP (#3480, the most-commented open bug)
  - a Swarm rollback does not show in the UI, so a failed deploy looks successful (#3987)
  - database backups fail when the database is on a worker node (#3516)
  - worker nodes get Swarm-provider Traefik config and flood their logs "until disk fills"
    (#5112)
  - Swarm settings are not persisted from the UI (#5223)
  - a health check `Test` is serialised as a string, so tasks hang in "Starting" (#5171)
  - custom domains do not work with Swarm (#4130)
  - a deploy builds a new image but the service keeps running the old one (#2150)
- **Upgrades and regressions.**
  - Traefik does not start after a reboot on v0.30.2 (#5131)
  - v0.29.0 crash-loops after "Migration complete" (#4253)
  - updating Dokploy does not update the pinned Traefik container (#5221)
  - migrating off the hardcoded auth secret, as the startup banner tells you to,
    **silently deploys every service with an empty environment**, because decryption fails
    open (#4833)
- **Memory and disk on small VPSs.** Idle usage "roughly doubled" in v0.27.1 (#3755,
  12 reactions). There are also calls to cut memory for "4GB RAM class and smaller" servers
  (#3909), and an orphaned stats loop that writes about 8.7 GB a day (#5504).
- **Environment changes need a rebuild.** "Environment variable changes don't get picked
  up unless you 'Rebuild'" (#2547, Compose). App environment changes do not update the
  running Swarm service (#5193).
- **Weak previews.** They are GitHub-only, need extra setup (LogRocket), have no Compose or
  DB previews, and no preview-URL variable (HN).
- **Operational opacity.**
  - a deploy is marked "live" about 10 s before the app answers, causing Bad Gateway
    (discussion #2476, Aug 2025)
  - a deployment stays "running" forever after a build exhausts the host, with no way to
    cancel short of a reboot (#4461)
  - the maintainer's answer in #2476 was to call most issues "external to Dokploy's scope"
    (DNS challenge belongs to Traefik, data loss to the server)
- **Docs gaps.** DNS-challenge setup was learnt from issues (#2476). LogRocket "could not
  find where that AI help shows up in the actual deployment flow".
- **Access control.** In Aug 2025 "all keys access all projects" (gear54rus, HN). Per-member
  server and Git-provider assignment arrived in v0.29.0, but only as Enterprise features.

## Security record

Where I looked (all on 30 Sep 2026):
- GitHub Security Advisories for Dokploy/dokploy, all six pages
- the release notes for v0.26.6, v0.29.3, v0.29.13 and v0.30.0
- SentinelOne, Strix and OpenCVE entries
- CERT/CC VU#280377
- Hacker News and dev.to

**60 published advisories.** 4 date from 2025, 3 from January 2026, 11 from May 2026 and
**42 from 21 July 2026**. By severity: **34 critical, 17 high, 8 moderate, 1 low**. The
classes:

- **OS command injection, repeatedly, in many independent code paths:**
  - Git clone URL and branch, for every provider, including the Bitbucket owner and repo
  - `customGitUrl` in `ssh-keyscan`
  - Compose `composePath` and custom command (CVE-2026-72865, CVSS 9.9)
  - the `dockerImage` field of six database deploy functions
  - `buildRemoteDocker`
  - registry testing and Swarm `nodeId`
  - the backup and restore pipeline (CVE-2026-72878, 9.6; CERT/CC VU#280377, 17 Sep 2026)
  - `rclone` in `destination.testConnection`
  - `certificatePath` (arbitrary file write plus RCE)
  - `readTraefikFile` and `updateTraefikConfig`
  - volume backups, file mounts and `patch.readRepoDirectories`
  - the `/listen-deployment` and `/docker-container-logs` WebSockets
  - the container-terminal WebSocket (CVE-2026-24841, 9.9)

  Two of the July advisories are titled "**Incomplete fix of**" and "**Fix bypass for**"
  an advisory from May.
- **Pre-authentication takeover.** Versions 0.27.0 up to 0.29.3 fell back to a hardcoded
  `BETTER_AUTH_SECRET` of `better-auth-secret-123456789`. That allowed forging a
  verification JWT, auto-signing in as admin, and then running commands on the host through
  the SSH terminal (CVE-2026-45631, **CVSS 10.0**, published 4 Jun 2026). A hardcoded
  Postgres password in `install.sh` before 0.26.6 is CVE-2026-24840.
- **Cross-organisation IDOR and missing authorisation:**
  - another tenant's S3 credentials and backups reachable through backup destinations
  - root RCE on another tenant's server via `swarm.*`
  - Git provider secrets leaked by `.one` endpoints
  - `server.remove` able to delete another organisation's server
  - a Member reaching root through the Docker and terminal WebSocket handlers, and through
    server-level schedules
  - invitation role escalation taking over an organisation
  - a password change that does not revoke sessions
- **Previews.** CVE-2025-53825 (9.8, July 2025): a fork of a public repo gets its PR
  deployed with the app's secrets.
- **Earlier.** In 2024 `auth.createAdmin` stayed callable after setup, and about 610
  exposed instances were found on Shodan. It was fixed in 0.7.2, without a CVE.

**What this means for Skifity.** Dokploy is now in the same place Coolify was in January
2026, and the root cause is the same: user input reaches `child_process.exec` template
strings "in many independent code paths". The WebSocket terminal and host-level schedules
account for a large share of the critical findings. The responses were fast and
transparent: every advisory is published, and v0.29.13 fixed 16 PRs' worth in one release.

## Against Skifity

Skifity statuses follow `docs/checklist.md`. **Written** means the code and unit tests
exist and the feature has never run on a cluster (ADR-0010). **Works** means a test runs in
CI.

| Capability | Dokploy | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git providers | GitHub App, GitLab, Gitea, Bitbucket, any Git, image, ZIP, raw Compose | GitHub (personal access token), GitLab, Gitea/Codeberg, generic Git, image, folder via `skifity up`. The GitHub App kind was removed as unimplemented (`internal/api/integrations_handlers.go:56-66`, `internal/cli/up.go`). Written | Behind (no GitHub App, no Bitbucket) |
| Zero-config builders | Nixpacks (default), Railpack with version choice, Dockerfile, Heroku and Paketo buildpacks, static | Railpack (default), Nixpacks, Dockerfile, static, image (`internal/builder/detect.go:18-33`). Builder images default to `:latest` (`internal/builder/job.go:104-111`). Written | Parity (no buildpacks, no builder version pin) |
| Docker Compose | Native `compose` and Swarm `stack` | Parsed and converted into one app per chosen service. A `compose` source is refused (`internal/builder/detect.go:487-530`, `internal/api/apps_handlers.go:257-267`). Written | Behind, by design |
| A variable change without a rebuild | Compose needs "Rebuild" (#2547). App environment changes do not reach the Swarm service (#5193) | Build fingerprint separates build-time from runtime (ADR-0007, `internal/deploy/deployer.go` `buildTimeVariables`). Written | **Skifity ahead** |
| Release (pre-deploy) command | None (#110, 41 👍) | `ReleaseCommand` runs after the build, before traffic (`internal/deploy/run.go:124-198`). Written | **Skifity ahead** |
| Patches, watch paths | Both | Neither (`grep -ri watch_path` finds nothing; there is no patch model) | Absent in Skifity |
| Dedicated build servers | Yes, per-server build queues | Builds are Jobs placed anywhere in the cluster. No node pinning in `internal/builder/job.go` | Behind |
| **Domains, TLS and routing** | | | |
| Free address | `traefik.me`, HTTP only | `sslip.io`, HTTP only (ADR-0015), or a wildcard domain setting that gives every app `<app>-<env>.<wildcard>` (`internal/settings/settings.go:124,266`, `internal/kube/naming.go:143-167`). Written | Skifity ahead (Dokploy #1404 open) |
| Automatic HTTPS | Let's Encrypt via Traefik | cert-manager ClusterIssuer (`internal/cluster/cluster.go:473-479`). Written | Parity |
| DNS provider automation, DNS-01, wildcard certificates | Cloudflare, Route53, Porkbun, Infomaniak, OVHcloud | None (no `route53` or `dns01` in `internal/`) | Absent in Skifity |
| Custom or Origin CA certificate upload | Yes | None | Absent in Skifity |
| Path routing, redirects, basic auth | All | Path per domain (`internal/store/models.go` `Domain.Path`). No redirect presets. A password in front of an app, added in `7865bc8` (`internal/store/migrations/0017_app_password.sql`) | Behind on redirects, parity on basic auth |
| Firewall (IP, country, ASN) | None (#1547 WAF, #1408 allowlists open) | Rule engine plus edge guard (`internal/edgerules`, `internal/guard`, `docs/firewall.md`). Written, never through a live Traefik | **Skifity ahead** |
| Cloudflare Tunnel | A docs guide | Built in (`internal/cluster/tunnel.go`). Written | Skifity ahead |
| **Databases and services** | | | |
| Engines | Postgres, MySQL, MariaDB, MongoDB, Redis, libSQL | Postgres (CloudNativePG), MySQL (MariaDB image), Redis (`internal/dbsvc/manifests.go:20-33`). Written | Behind (no MongoDB or libSQL) |
| Database HA | None | Postgres `Instances` with odd-number failover via CNPG (`internal/dbsvc/manifests.go:51-110`). Written | **Skifity ahead** |
| Connecting a database to an app | Copy credentials by hand | `Link` injects a sealed connection string (`internal/dbsvc/manager.go:342-366`). Written | Skifity ahead |
| Reaching a database from a laptop | External port toggle | None, and not documented (no `port-forward` in `docs/`) | Absent in Skifity |
| **Storage and backups** | | | |
| Database backup to S3 plus restore | Yes, several destinations | Yes, one bucket. Restore has a `verify` step (`internal/backup/manager.go:206-255`). Written, never taken or restored | Parity (Skifity unproven) |
| Volume backup and restore | Named volumes, optional stop | Tar while the app runs, and restore stops the app (`internal/backup/volume.go`, `volumerestore.go`, `docs/backups.md`). Written | Parity (Dokploy has a stop-during-backup option) |
| Panel's own backup | Scheduled to S3, with restore from the UI | Manual local copy only: `skifity admin backup-db <path>` (`internal/cli/admin.go:215-226`). `docs/backups.md` says "What is not backed up: the panel's own state" | **Behind** |
| Encrypted backups | None (#2926 open) | None (no encryption in `internal/backup/`) | Absent in both |
| **Scaling and high availability** | | | |
| Autoscaling and scale-to-zero | None (#2579 54 👍, #2154 26 👍) | HPA on CPU or memory, and scale-to-zero via KEDA (`internal/kube/manifests.go`, `internal/kube/scaletozero.go`). Written | **Skifity ahead** |
| Zero-downtime deploys | Off by default. Hand-written health check JSON, apps only | `maxUnavailable: 0` plus a `preStop` pause by default (`internal/kube/manifests.go:72-84,181`). Written | Skifity ahead |
| Rollback | Image only, needs a registry | Image **and settings** (`internal/deploy/deployer.go` `runtimeSpec`). Written | Skifity ahead |
| Checking before adding replicas | None | `ScalingReadiness` (`internal/deploy/scaling.go`). Written | Skifity ahead |
| **Observability** | | | |
| Logs | Live and build logs | Live logs over SSE, previous-container logs, build logs (`internal/api/api.go` routes `/logs`, `/deployments/{id}/logs`). Written | Parity |
| Metrics history and graphs | 20 s samples, 2-day retention, graphs (advanced page Cloud-only) | Live snapshot per node and instance from metrics-server (`internal/api/servers_handlers.go:272-298`). No history, no charts in `web/src` | **Behind** |
| Threshold alerts | CPU and memory thresholds (Cloud) | Only an `app.unhealthy` event (`internal/notify/notify.go:41-47`) | Behind |
| Panel self-metrics | Not found | Prometheus `GET /api/metrics`. Works | Skifity ahead |
| **Security, auth, roles, SSO, audit** | | | |
| OIDC SSO | Enterprise licence only | Free, with PKCE, nonce and single-use state (`internal/auth/oidc.go`). Works | **Skifity ahead** |
| SAML and SCIM | Enterprise | None. SAML declined (`internal/auth/oidc.go:22`) | Behind for enterprise buyers, by decision |
| Passkeys | Yes (v0.30.0) | None (no `webauthn` in `internal/`) | Absent in Skifity |
| Per-project or per-service member access | OSS: per project, environment and service. Enterprise: custom roles | Three team-wide roles only (`internal/store/models.go:6-20`). Tokens bound to a team with read/write scopes (`internal/auth/scopes.go`) | **Behind** |
| Enforcing 2FA or SSO for a team | Force SSO (Enterprise) | None (no such setting in `internal/settings/settings.go:119-160`) | Behind |
| Audit log | Enterprise only | Free: `RecordAudit`, `/teams/{id}/audit`, activity page (`internal/store/misc.go:86`, `web/src/pages/activity.tsx`). Works (Go tests) | **Skifity ahead** |
| Secrets at rest | AES-GCM since Jul 2026, key derived from the auth secret, fails open (#4833) | Envelope encryption, context-bound, fails closed (`internal/crypto/envelope.go`, `internal/deploy/deployer.go:711-737`). Works | **Skifity ahead** |
| External secret stores | Vault/OpenBao, Infisical, AWS SM, Doppler, Azure, Scaleway, Phase, AWS Parameter Store | None | Absent in Skifity |
| One place where input becomes shell | No: 60 advisories, mostly injection | `internal/shellsafe`, tested against a real `sh` (ADR-0002a) | Skifity ahead in structure, not yet audited |
| Web terminal | Host and container, over WebSocket | None, deliberately (`docs/roadmap.md`, "Not on this roadmap") | Deliberate |
| **Preview environments and branches** | | | |
| Previews | GitHub only, applications only, limit 3, label filter | GitHub, GitLab MRs and Gitea PRs. Namespace per PR, forks get no secrets, commit status plus one PR comment, TTL (`internal/api/webhook_handlers.go:183-345`, `internal/gitsrc/webhook.go:109-331`, `internal/deploy/gitreport.go`, `KeyPreviewTTLDays`). Written | **Skifity ahead** |
| A database for each preview | None (#2462) | None. The preview copies the parent's linked `DATABASE_URL`, which points at the parent namespace's `*.svc.cluster.local`, and the default-deny NetworkPolicy blocks egress there (`internal/dbsvc/manifests.go:143`, `internal/kube/namespace.go:127-200`) | Both absent. In Skifity it is **a latent defect** (see P0) |
| **Templates and catalogue** | | | |
| Catalogue | 518, 44% with `latest` or untagged images, AI Compose generator | 282, every image pinned and checked against its registry (`internal/templates/catalogue/`, 282 YAML plus README) | Behind on count, ahead on pinning |
| **CLI, API, IaC, integrations** | | | |
| API description, SDK | OpenAPI, TypeScript SDK | None (no `openapi` anywhere) | Behind |
| CLI | 449 generated commands | About 18 hand-written commands with `--json`, plus `up` from a folder (`internal/cli/commands.go:34-71`). Works (`make smoke`) | Behind on breadth, ahead on `up` and error shape |
| MCP | 508 generated tools, presets, annotations, redaction | 15 curated tools, errors carry cause, impact and fix (`internal/mcpserver/server.go`). No `readOnlyHint` or `destructiveHint` | Different. Skifity is missing the annotations |
| Export and import | Neither (#1733, 38 👍) | Export works (`internal/api/export_handlers.go`, `skifity export`). No import | Skifity ahead on export, import absent |
| IaC | None (Pulumi #1555, 42 👍) | None | Absent in both |
| **Notifications** | | | |
| Providers and events | 12 providers, 6 events | Email, Telegram, Discord, webhook, plus plugin-provided kinds (ADR-0021), with no plugin store published. 7 events (`internal/notify/notify.go:1,41-53`). Written | **Behind** |
| **Multi-server and networking** | | | |
| Adding servers | SSH key, root or sudo. Each server is independent with its own Traefik, or a Swarm with a registry | Password once, then its own key, then k3s join, all one pool (`internal/provision`). Written | Different. Skifity is ahead in design and unproven |
| Isolation between projects | Manual networks (v0.30.0). #129 and #2798 open | Namespace, default-deny NetworkPolicy, quota, restricted PSS (`internal/kube/namespace.go`). Written | Skifity ahead |
| **Team and collaboration** | | | |
| Orgs, teams, invites | Multiple orgs, shared Git providers, tags | Teams, hashed single-use invitations, project-shared variables (`internal/api/api.go` routes `/teams/{id}/invitations`, `/projects/{id}/variables`) | Parity (Dokploy shares Git providers) |
| **Developer experience and onboarding** | | | |
| Install | One command, 2 GB RAM, 30 GB disk, released | No release tag, so it cannot be installed with one command (`docs/checklist.md`). Needs 1 GB RAM and 8 GB disk | **Behind** (unreleased) |
| Footprint | Dokploy container 300–630 MB, plus Postgres and Traefik | Panel 35 MiB measured. k3s about 500 MiB, **estimated rather than measured** (`docs/performance.md`) | Roughly parity in total. Skifity's panel is lighter |
| Languages | Partial community translations (not verified) | 5 complete languages, and the build fails on gaps (`web/src/locales`, ADR-0009). Works | Skifity ahead |
| Docs offline and in-product | Docs site | Served from the binary, with links checked (`internal/docsite`). Works | Skifity ahead |

## Gaps worth closing in Skifity

Ordered by what a person loses without the item. Nothing here is something Skifity already
has.

### P0

1. **Make previews work for an app with a database.**
   - *What:* today `copyPreviewVariables` (`internal/api/webhook_handlers.go`) copies the
     parent's linked connection string into the preview. That string names
     `<db>.<parent-namespace>.svc.cluster.local` (`internal/dbsvc/manifests.go:143`).
     The preview namespace's default-deny policy blocks egress to other namespaces
     (`internal/kube/namespace.go`), so every database-backed preview fails to connect. If
     the policy is not enforced, a same-repository PR's release command runs migrations
     **against production**. The fix: for each database linked to the parent, create an
     empty database of the same engine in the preview environment, re-link it under the same
     variable name, and optionally seed it from the latest backup. Delete it with the
     namespace.
   - *Evidence:* Dokploy's #2462 (28 👍, per-preview migrations) and #2743 (per-preview
     variable overrides). Previews are what users choose Vercel for
     (`docs/research/competitors.md`). Nobody self-hosted does this well.
   - *Fit:* `internal/api/webhook_handlers.go` (`deployPreview`), `internal/dbsvc`
     (`Create`, `Link`), and an environment-page badge "preview database".
   - *Size:* M.
   - *Without a cluster?* Yes for the logic (unit tests against the fake clientset and
     store: the preview gets its own database record and link, and the fork rule still
     applies). Whether the preview connects needs a cluster (add it to `verify.sh` phase 1).
2. **Back up the panel itself, on a schedule, off the machine.**
   - *What:* a scheduled `VACUUM INTO` copy of `panel.db`, uploaded to the configured S3
     bucket with retention. The master key stays out of it on purpose (`docs/backups.md`
     explains why), and the backup is useless without the recovery key, which is fine.
     Add `skifity admin restore-db <s3-key|file>` and a "Panel" row on the Backups page.
   - *Evidence:* Dokploy backs up its Postgres and `/etc/dokploy` to S3 with one-click
     restore. Skifity's panel is pinned to one node's disk (ADR-0013), and its only copy
     is a manual local command (`internal/cli/admin.go:215`). Losing that node loses every
     app definition and every sealed secret.
   - *Fit:* `internal/backup` (a panel target alongside database and volume),
     `internal/serverapp` minute tick, `internal/cli/admin.go`.
   - *Size:* S–M.
   - *Without a cluster?* **Yes, completely**: SQLite, an httptest S3, the CLI and the smoke
     test.

### P1

3. **Metrics history, graphs and threshold alerts.**
   - *What:* sample node and instance usage every 30–60 s into a bounded SQLite table (for
     example 48 h), draw CPU, memory and disk charts on the app and server pages (the shadcn
     chart component, so no second library), and send `server.pressure` and `app.pressure`
     notifications when a threshold is crossed for N minutes.
   - *Evidence:* Dokploy's graphs are praised (LogRocket, CherryServers). It sells
     threshold alerts on Cloud, and self-hosted users ask for remote-server monitoring
     (#2685) and Prometheus (#2376, 19 👍). Skifity shows a snapshot only.
   - *Fit:* `internal/metrics` or `internal/watch`, the store, `internal/notify` events,
     `web/src/pages/app-detail.tsx` and `server-detail.tsx`.
   - *Size:* M.
   - *Without a cluster?* Mostly. The sampler runs against the fake metrics clientset, alert
     evaluation is unit-tested, and Playwright can check charts with seeded rows. Real
     numbers need a cluster.
4. **More notification providers, built in.**
   - *What:* Slack, Microsoft Teams, Mattermost, ntfy, Gotify, Pushover (and Google Chat).
   - *Evidence:* Dokploy has 12, and Slack and Teams are table stakes for a team product.
     Skifity has 4. ADR-0021 intends vendors to arrive as plugins, but no plugin store is
     published (`docs/progress.md`, Next tasks 4), so today users cannot get Slack at all.
   - *Fit:* `internal/notify` (`ValidateConfig`, `Send`), the channel form in Settings, and
     i18n keys in all five languages.
   - *Size:* S.
   - *Without a cluster?* **Yes**: an httptest per provider, as `internal/notify` already
     does for Discord.
5. **Scope a member, and a token, to projects.**
   - *What:* an optional list of projects (or environments) per membership and per API
     token. `authorizeProject`, `authorizeEnvironment` and `authorizeApp` intersect with it,
     and a team Member with no list keeps today's access.
   - *Evidence:* Dokploy OSS has per-project, per-environment and per-service access, and
     sells custom roles. HN complaint: "all keys access all projects". An agency or a
     contractor is the common case.
   - *Fit:* `internal/api` authorisation helpers (the one place, per `CLAUDE.md`),
     `internal/store` memberships and tokens, and the Members tab.
   - *Size:* M.
   - *Without a cluster?* **Yes**: extend the route-walk test in `internal/api` so every
     team-scoped route answers 404 outside the member's projects.
6. **Enforce 2FA, or SSO only, per team.**
   - *What:* two team settings. "Members must have 2FA" sends a signed-in user without TOTP
     to enrolment before anything else. "Single sign-on only" refuses password sign-in for
     that team's members, except owners holding a recovery key.
   - *Evidence:* Dokploy sells forced SSO as Enterprise. Its hardening guide says to
     "enforce 2FA or passkeys for all panel users". Discussion #4360 ranks enforceable 2FA
     in its top 3.
   - *Fit:* `internal/api` middleware, `internal/auth`, and the Settings sign-in group.
   - *Size:* S.
   - *Without a cluster?* **Yes**: Go tests plus the Playwright sign-in flow.
7. **Connect GitHub through a GitHub App.**
   - *What:* the removed `github_app` kind (`internal/api/integrations_handlers.go:56`),
     done properly: a manifest flow that creates the app, a JWT signed with its private key,
     installation tokens, a repository picker, and one webhook for all repositories.
   - *Evidence:* it is how Dokploy (and Vercel and Railway) onboard. A PAT is a long-lived
     personal credential, which is exactly what a team panel should not hold.
   - *Fit:* `internal/gitsrc`, `internal/api/webhook_handlers.go`, and the Git sources page.
   - *Size:* M.
   - *Without a cluster?* **Yes**: an httptest GitHub API and a golden JWT.
8. **Deployment context variables, and fix the ones already injected.**
   - *What:* `buildPlainEnv` (`internal/kube/manifests.go:552-570`) injects
     `version.Name + "_APP"`, which is **`Skifity_APP` and `Skifity_ENVIRONMENT`** in mixed
     case, undocumented. Rename them to `SKIFITY_APP` and `SKIFITY_ENVIRONMENT`, and add
     `SKIFITY_URL` (the app's primary address, which in a preview is the preview's own),
     `SKIFITY_COMMIT_SHA` and `SKIFITY_PREVIEW` (the PR number). They are runtime-only, so
     the build fingerprint is unchanged (ADR-0007).
   - *Evidence:* Dokploy #2715 (12 👍) asks for commit and environment variables, and on HN
     previews are "almost useless because there is no variable to get the preview URL".
   - *Fit:* `internal/kube/manifests.go`, `internal/deploy`, and `docs/concepts.md`.
   - *Size:* S.
   - *Without a cluster?* **Yes** (golden manifests).
9. **Publish an OpenAPI description, and annotate the MCP tools.**
   - *What:* generate `/api/openapi.json` from the chi routes and request types (checked in
     CI, so a route without a schema fails the build). Add `readOnlyHint`, `destructiveHint`
     and `idempotentHint` to the 15 MCP tools.
   - *Evidence:* Dokploy generates its CLI (449 commands), SDK and MCP (508 tools) from
     OpenAPI. `@dokploy/mcp` has 23k monthly downloads, four times its CLI. Pulumi (#1555,
     42 👍) and Terraform providers need a spec.
   - *Fit:* `internal/api`, `internal/mcpserver`, and `docs/cli.md`.
   - *Size:* M (spec), S (annotations).
   - *Without a cluster?* **Yes**.
10. **Pin the builder images.**
    - *What:* Railpack, its frontend and Nixpacks default to `:latest`
      (`internal/builder/job.go:104-111`). That contradicts the catalogue's own rule that
      "an image on `latest` is not a version". Pin them to versions (a setting to override)
      and record the builder version in the deployment.
    - *Evidence:* Dokploy added Railpack version selection (v0.26.4) and "missing Railpack
      versions" (v0.30.7) because builds changed under users.
    - *Fit:* `internal/builder`, `internal/settings`, and the build log.
    - *Size:* S.
    - *Without a cluster?* **Yes** (golden Job).
11. **Encrypt backups before they leave the cluster.**
    - *What:* stream each dump and tar through an age/X25519 (or AES-GCM) encryption step
      whose key is derived from the master key or recovery key, so the S3 bucket holds only
      ciphertext. Restore decrypts.
    - *Evidence:* Dokploy #2926 (18 👍) is open. Dokploy's hardening guide can only suggest
      SSE-S3. Leading here is cheap.
    - *Fit:* `internal/backup/jobs.go` and `volume.go` scripts (through `internal/shellsafe`),
      and `internal/crypto`.
    - *Size:* M.
    - *Without a cluster?* The crypto and the rendered scripts, yes. A real round trip needs
      a cluster (phase 2).

### P2

12. **Watch paths for monorepos.**
    - *What:* globs per app. A push deploys only if a changed file matches.
    - *Fit:* `internal/gitsrc/webhook.go` (commit file lists) and `store.App`.
    - *Size:* S.
    - *Without a cluster?* Yes.
13. **DNS provider automation and DNS-01.**
    - *What:* Cloudflare first. Create the record when a domain is added, and optionally
      issue a wildcard certificate over DNS-01 through cert-manager.
    - *Evidence:* Dokploy v0.30.0 through v0.30.6 added five providers.
    - *Fit:* `internal/cluster`, `internal/settings`, and the domains tab.
    - *Size:* M.
    - *Without a cluster?* The DNS API calls, yes (httptest). DNS-01 issuance needs a
      cluster.
14. **Upload a custom certificate (for example a Cloudflare Origin CA).**
    - *What:* a TLS Secret per domain instead of cert-manager.
    - *Evidence:* Dokploy #1839 (22 👍), and three open bugs around it.
    - *Size:* S–M.
    - *Without a cluster?* Golden manifests, yes. Real TLS, no.
15. **External secret providers.**
    - *What:* resolve `${{vault.<provider>.<ref>}}`-style references at deploy time from
      Vault/OpenBao, Infisical, Doppler or AWS Secrets Manager, never stored.
    - *Evidence:* Dokploy v0.30.x, and Discussion #4360.
    - *Fit:* `internal/deploy/deployer.go` `runtimeVariables`, with a new package for
      providers. Fetching must go through `internal/netguard`.
    - *Size:* M.
    - *Without a cluster?* Yes (httptest providers).
16. **Import what `skifity export` writes.**
    - *What:* recreate a team's projects, apps, domains and settings on a new panel, with
      secrets re-entered or read back from the old cluster's Secrets.
    - *Evidence:* Dokploy #1733 (38 👍), and it completes the no-lock-in story.
    - *Size:* M.
    - *Without a cluster?* The store side, yes. Applying needs a cluster.
17. **`skifity db connect`.**
    - *What:* a local port forwarded through the panel's API to a database, instead of a
      public port.
    - *Evidence:* Dokploy's "external credentials" and its pgAdmin and Mongo connection
      guides.
    - *Size:* M.
    - *Without a cluster?* CLI parsing, yes. The tunnel, no.
18. **Passkeys (WebAuthn)** for panel sign-in.
    - *Evidence:* Dokploy v0.30.0.
    - *Fit:* `internal/auth`.
    - *Size:* M.
    - *Without a cluster?* Yes (Playwright has a virtual authenticator).
19. **MongoDB as a managed engine.**
    - *Evidence:* Dokploy, and 39 "database"-tagged templates in its catalogue.
    - *Size:* M.
    - *Without a cluster?* Manifests, yes. Running it, no.
20. **Quiesce a volume during backup.**
    - *What:* an option to scale an app to zero for a volume backup, as Dokploy recommends,
      for apps that cannot tolerate a hot tar.
    - *Fit:* `internal/backup/volume.go`.
    - *Size:* S.
    - *Without a cluster?* The logic, yes. The behaviour, no.
21. **Pin builds to a server** (a "build node" label, used as the Job's `nodeSelector`).
    - *Evidence:* Dokploy's build servers.
    - *Size:* S.
    - *Without a cluster?* Golden Job, yes.

## Things to deliberately not copy

- **Shell strings built from user input in many places.** Dokploy's 60 advisories are
  mostly `child_process.exec` over template literals, fixed one sink at a time, and two
  July advisories fix bypasses of May's fixes. Keep `internal/shellsafe` and ADR-0002a as
  the only path, and keep the test that runs a real `sh`.
- **A web terminal and host-level scheduled scripts.** Dokploy's terminal and log
  WebSockets and its "server schedules" account for at least eight critical or high
  advisories (Member → root on the host). Skifity's no-shell decision (`docs/roadmap.md`)
  and SSE rather than WebSocket (ADR-0005) remove that whole class. The one-off command in
  the app's image is the right replacement.
- **Fallback secrets and fail-open decryption.** A hardcoded `BETTER_AUTH_SECRET`
  (CVE-2026-45631), a hardcoded installer DB password (CVE-2026-24840), and decryption that
  returns ciphertext and deploys an empty environment (#4833). Skifity's keyring refuses and
  the deployment fails (`internal/deploy/deployer.go:719-734`). Never add a default key "to
  make first boot easier".
- **Charging for sign-in safety, and a licence that phones home.** SSO, audit logs and
  forced SSO are paid, and the key is validated "every day against our servers" with the
  server's IP address. OIDC and the audit log are free in Skifity, and it never calls home.
  That is a stated differentiator (`README.md`). Keep both.
- **Deploying fork PRs with secrets.** CVE-2025-53825. Skifity already gives fork previews
  no secrets (`copyPreviewVariables`). Do not relax it for convenience.
- **`latest` in templates.** 44% of Dokploy's templates, and Skifity's own builder images
  today (see P1 #10). Pinning is Skifity's catalogue advantage.
- **Raw orchestrator knobs in the UI.** Swarm settings as JSON with nanosecond intervals
  and nanoCPUs, plus a string-versus-array bug that hangs tasks (#5171). Skifity's
  "Advanced" tab is read-only YAML. Keep settings in product terms.
- **Credentials in logs and emails.** Dokploy prints full commands, registry password
  included, into server logs and failure emails (#4693). Keep `internal/logging` redaction
  and the smoke test that greps the panel log for every secret.
- **A monitoring page locked to the hosted tier.** If Skifity ever has a hosted version
  (`docs/roadmap.md` says not), basic observability stays in the free product.
- **An in-panel LLM Compose generator.** It sends the user's configuration to a third-party
  model from the panel, produces unpinned Compose files, and one reviewer "could not find
  where that AI help shows up". Skifity's route is better: an MCP server in the binary, and
  errors that carry their own fix. Copy Dokploy's MCP ergonomics (presets, annotations,
  redaction) instead.
- **Five hundred generated MCP tools.** Breadth from OpenAPI is good for the SDK and CLI
  (P1 #9). An assistant does better with a small, curated tool set whose errors explain
  themselves.
- **Every server running its own proxy with no shared state.** Dokploy's remote servers and
  its Traefik are independent single points (discussion #4060). Skifity runs the ingress
  on every node in one cluster. Do not regress to per-server islands.

## Sources

All read on 2026-09-30 unless a different date is given.

- https://github.com/Dokploy/dokploy — repository page (stars, forks, issues, PRs, commits, top-level files); the repository object from the GitHub search API (37,579 stars, 3,023 forks, default branch `canary`)
- https://github.com/Dokploy/dokploy/releases, `?page=2` to `?page=5`
- https://github.com/Dokploy/dokploy/releases/tag/v0.30.0
- https://github.com/Dokploy/dokploy/releases/tag/v0.29.0
- https://github.com/Dokploy/dokploy/releases/tag/v0.29.3
- https://github.com/Dokploy/dokploy/releases/tag/v0.29.7
- https://github.com/Dokploy/dokploy/releases/tag/v0.29.12
- https://github.com/Dokploy/dokploy/releases/tag/v0.29.13
- https://hub.docker.com/v2/repositories/dokploy/dokploy/tags and https://hub.docker.com/v2/repositories/dokploy/ (release push dates, 14,767,856 pulls)
- https://registry.npmjs.org/@dokploy/cli, `@dokploy/mcp`, `@dokploy/sdk`, and https://api.npmjs.org/downloads/point/last-month/ for each
- https://github.com/Dokploy/dokploy/milestones
- https://github.com/Dokploy/dokploy/blob/canary/LICENSE.MD
- https://github.com/Dokploy/dokploy/blob/canary/LICENSE_PROPRIETARY.md
- GitHub code search in `repo:Dokploy/dokploy` for the `proprietary/` paths (sso, scim, audit-log, whitelabeling, license-key) and `better-auth-secret-123456789`
- https://github.com/Dokploy/dokploy/security/advisories, pages 1 to 6
- https://github.com/Dokploy/dokploy/security/advisories/GHSA-h67g-mpq5-6ph5
- https://www.sentinelone.com/vulnerability-database/cve-2026-45631/
- https://www.sentinelone.com/vulnerability-database/cve-2026-24841/
- https://www.sentinelone.com/vulnerability-database/cve-2026-24840/ (from search results)
- https://www.strix.ai/cve/CVE-2026-72865
- https://www.strix.ai/cve/CVE-2026-72868, https://www.strix.ai/cve/CVE-2026-72878, https://www.strix.ai/cve/CVE-2026-72875, https://hol.org/guard/security/cves/CVE-2026-72739-dokploy-command-injection-via-compose-shell (from search results)
- https://www.kb.cert.org/vuls/id/280377 (published 2026-09-17)
- https://dev.to/bscript/how-i-accidentally-became-an-admin-on-600-servers-3ghc (published 2024-08-27)
- https://dokploy.com/pricing
- https://dokploy.com/install.sh
- https://dokploy.com/features/container-server-monitoring
- https://docs.dokploy.com/sitemap.xml
- https://docs.dokploy.com/docs/core
- https://docs.dokploy.com/docs/core/architecture
- https://docs.dokploy.com/docs/core/installation
- https://docs.dokploy.com/docs/core/features
- https://docs.dokploy.com/docs/core/comparison
- https://docs.dokploy.com/docs/core/differences
- https://docs.dokploy.com/docs/core/cloud
- https://docs.dokploy.com/docs/core/monitoring
- https://docs.dokploy.com/docs/core/applications
- https://docs.dokploy.com/docs/core/applications/build-type
- https://docs.dokploy.com/docs/core/applications/advanced
- https://docs.dokploy.com/docs/core/applications/zero-downtime
- https://docs.dokploy.com/docs/core/applications/rollbacks
- https://docs.dokploy.com/docs/core/applications/preview-deployments
- https://docs.dokploy.com/docs/core/docker-compose
- https://docs.dokploy.com/docs/core/backups
- https://docs.dokploy.com/docs/core/volume-backups
- https://docs.dokploy.com/docs/core/databases
- https://docs.dokploy.com/docs/core/databases/backups
- https://docs.dokploy.com/docs/core/databases/restore
- https://docs.dokploy.com/docs/core/domains
- https://docs.dokploy.com/docs/core/remote-servers
- https://docs.dokploy.com/docs/core/cluster
- https://docs.dokploy.com/docs/core/multi-tenancy
- https://docs.dokploy.com/docs/core/permissions
- https://docs.dokploy.com/docs/core/secrets-providers
- https://docs.dokploy.com/docs/core/patches
- https://docs.dokploy.com/docs/core/schedule-jobs
- https://docs.dokploy.com/docs/core/watch-paths
- https://docs.dokploy.com/docs/core/overview (notifications)
- https://docs.dokploy.com/docs/core/ai
- https://docs.dokploy.com/docs/core/guides/production-hardening
- https://docs.dokploy.com/docs/core/enterprise
- https://docs.dokploy.com/docs/core/enterprise/sso
- https://docs.dokploy.com/docs/core/enterprise/sso/application-authentication
- https://docs.dokploy.com/docs/core/enterprise/license-keys
- https://docs.dokploy.com/docs/core/enterprise/audit-logs
- https://docs.dokploy.com/docs/cli
- https://github.com/Dokploy/cli
- https://github.com/Dokploy/mcp
- https://github.com/Dokploy/templates
- https://templates.dokploy.com/meta.json and `https://templates.dokploy.com/blueprints/<id>/docker-compose.yml` for all 518 ids (image-tag count)
- GitHub issue search, `repo:Dokploy/dokploy is:open is:issue`, sorted by 👍 (541 open issues), and open `label:bug` sorted by comments
- Issues: https://github.com/Dokploy/dokploy/issues/1246, /2579, /2028, /1555, /110, /1733, /1404, /2462, /416, /2154, /2798, /2386, /1839, /1949, /1483, /1547, /2926, /2414, /2715, /2685, /2376, /2748, /3480, /3987, /3516, /4051, /4130, /4203, /4461, /4693, /4833, /5112, /5131, /5171, /5193, /5221, /5223, /5504, /2547, /3755, /3909
- https://github.com/Dokploy/dokploy/discussions/2476
- https://github.com/Dokploy/dokploy/discussions/4060
- https://github.com/Dokploy/dokploy/discussions/4175
- https://github.com/Dokploy/dokploy/discussions/4360
- https://news.ycombinator.com/item?id=44921394 (comments of 2025-08-16)
- https://news.ycombinator.com/item?id=44922603
- https://news.ycombinator.com/item?id=44924434
- https://news.ycombinator.com/item?id=47876352 (about April 2026)
- https://news.ycombinator.com/item?id=44922573 (returned 403; its title, "the licensing of Dokploy is a little complex", comes from search results)
- https://blog.logrocket.com/dokploy-vs-coolify-production/ (published 2026-02-03)
- https://cloudzy.com/blog/coolify-vs-dokploy/ (published 2026-06-18)
- https://bex.co/blog/2026/09/09/dokploy-license-restructure-open-core
- https://bex.co/blog/2026/09/19/self-hosted-paas-showdown-second-machine (from search results)
- https://www.cherryservers.com/blog/coolify-vs-dokploy (from search results)
- https://github.com/Dokploy/dokploy/pull/792 and /994 (i18n, from search results)
- Skifity files cited in the table above, read in this repository on 2026-09-30
