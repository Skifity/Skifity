# Render

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Researched 2026-09-30 against live sources. Skifity's side of every comparison was
checked in the code at commit `1ae3276` (2026-09-30). Uncommitted work seen in the
working tree that day is not counted.

Render is the managed platform most often named as "the modern Heroku": a
dashboard, a Git push, and a set of **service types** — static sites, web
services, private services, background workers, cron jobs, managed PostgreSQL and
Redis-compatible **Key Value** — described together in a **Blueprint**
(`render.yaml`) and copied per pull request into **preview environments**. It is
for small teams through mid-size companies; since 2026 it also sells to AI and
agent workloads (Workflows for durable execution in public beta since
2026-04-07, Sandboxes in early access). **Architecture:** Render "operates its own
Kubernetes clusters on AWS instead of using EKS", with a Kubernetes control plane
that "runs independently of the clusters that customer workloads run in" (Render
blog on the 2025-10-20 AWS outage); regions Oregon, Ohio, Virginia, Frankfurt and
Singapore. The platform's own state is not documented. **Licence:** proprietary;
the CLI (`render-oss/cli`, Go, 117 stars, 16 open issues), the MCP server
(`render-oss/render-mcp-server`, Go, 175 stars, 19 open issues) and the Terraform
provider (57 stars, 27 open issues) are public on GitHub (GitHub API,
2026-09-30). **Pricing (read 2026-09-30, changed 2026-04-23, forced on every
workspace 2026-08-01):** Hobby $0, Pro $25/month flat with unlimited seats, Scale
$499/month, Enterprise custom — each *plus compute*: a web service from $7 (0.5
CPU, 512 MB) through $25 (1 CPU, 2 GB) and $85 (2 CPU, 4 GB) to $1,500 (12 CPU,
96 GB); Postgres from $6 (256 MB) to $11,000/month plus $0.30/GB storage; Key Value
$10–$1,100; disks $0.25/GB-month; bandwidth 5 GB / 25 GB / 1 TB included, then
$0.15/GB. **Maturity and adoption:** founded 2018 by Anurag Goel; an $80M Series C
in January 2025 and a $100M extension at a $1.5B valuation on 2026-02-17, $258M
raised in total, "4.5 million+ developers" and "250,000+ new developers joining
monthly" (Render blog, 2026-02-17). The CLI released v2.28.0 on 2026-09-10.

## Feature inventory

### Deploy sources and builds

- Git (GitHub, GitLab, Bitbucket) or a prebuilt image from a registry. Native
  runtimes (Node, Python, Go, Rust, Ruby, Elixir, Bun) or a Dockerfile.
- **Auto-deploys** on push; **deploy hooks** (a secret URL that starts a deploy,
  for CI); **monorepo support** with root directories and build filters.
- **Pre-deploy command** on every plan: runs after the build, before the new
  version takes traffic; a failure stops the deploy.
- **Zero-downtime deploys** by default (except with a disk); **instant rollbacks**
  to retained builds (5 Hobby, 15 Pro, 30 Scale/Enterprise).
- Build minutes: 500 / 1,000 / 5,000 a month included, then $5 per 1,000; a
  "Performance" build pipeline on larger machines at $25 per 1,000 minutes (Pro+).
- Builds got faster through 2026: median service build time down 40% to about
  21 seconds (changelog 2026-08-07), Docker builds down 60% (2026-06-11).
- A service's repository or image can be changed from the dashboard
  (2026-05-11); a new **Deploys** page keeps the full deploy history with a Live
  badge (2026-09-03).

### Domains, TLS and routing

- An `onrender.com` subdomain per service; custom domains (2 / 15 / 25 included,
  then $0.25/domain/month), **wildcard domains**, automatic TLS, HTTP/2,
  WebSockets.
- **Zero-config CDN** for static sites; **edge caching for web services** (GA
  2025-08-13, all file types since 2025-10-17).
- Automatic DDoS mitigation and a firewall on every plan; **inbound IP rules** for
  Postgres and Key Value on every plan, for web services and static sites on
  Enterprise (2025-10-02).
- **Dedicated outbound IPs**, $100/month per IP set (2026-05-19); AWS PrivateLink,
  $30/month for up to three links.
- **Maintenance mode**: a paid web service answers every request with a 503 and a
  default or custom maintenance page while staying reachable on the private
  network and over SSH.

### Databases and services

- **Service types**: static site, web service, **private service** (reachable
  only on the private network), **background worker** (no inbound port), **cron
  job**, **Workflows** (durable tasks with retries, SDKs in Python and TypeScript,
  a "flex" plan billed on actual CPU and RAM since 2026-09-01), Postgres, Key
  Value.
- **Render Postgres**: PostgreSQL up to 18 (2025-11-13); **point-in-time recovery**
  (3 days on Hobby, 7 days on paid plans); logical backups for paid instances;
  **read replicas**; **high availability** with automatic failover to a standby;
  **connection pooling** (2026-07-01); **storage that grows itself** when low
  (2025-11-06); zero-downtime credential rotation (2025-11-21); pgvector, PostGIS,
  logical replication, table repacking. Free databases **expire after 30 days**,
  with 14 days' grace.
- **Render Key Value**: Redis-compatible (Redis 6 or Valkey 8), disk-backed on
  paid plans with configurable persistence (2026-06-11); the free instance "do[es]
  not continually persist [its] state to disk".
- Postgres and Key Value are manageable from the CLI (2026-06-30).

### Storage and backups

- **Persistent disks** at $0.25/GB-month on web services, private services and
  background workers. **A daily snapshot**, kept at least seven days, restorable
  from the dashboard (with a warning not to use it to recover a database). Grow
  without downtime, never shrink.
- A disk **prevents zero-downtime deploys** ("Render must stop the existing
  instance before launching the new one") and **prevents scaling** past one
  instance.
- No object storage yet; "native object storage integrated with global CDN" is
  on the list the 2026-02-17 funding is for.

### Scaling and high availability

- Manual scaling to 100 instances on most paid plans.
- **Autoscaling** on Pro and above between a minimum and maximum "based on target
  CPU and/or memory utilization"; it scales up at once and "waits a few minutes"
  before scaling down, and does not scale down if load rises meanwhile.
- **Free web services spin down** after 15 minutes without inbound traffic and take
  about a minute to come back; 750 free instance hours per workspace per month.
  Free services now stay up while receiving WebSocket messages (2026-02-24).
- Memory-optimised and 12-CPU plans (2026-08-26).

### Observability (logs, metrics, alerts, uptime)

- **Service metrics** (CPU, memory, instance count), extended retention on Scale.
- **Logs** kept 7 / 14 / 30 days by plan, searchable in the dashboard and CLI
  (reworked 2025-12-17); **log streams** to an external syslog endpoint on every
  plan; **HTTP request logs** and an **OpenTelemetry metrics stream** on Pro+.
- **Health checks** gate deploys and restart unhealthy instances.
- **SSH** into a running instance, a specific instance of a scaled service
  (2025-09-11), or a fresh **ephemeral instance** (2026-06-02). Audit logs record
  shell sessions (2026-03-25).

### Security, auth, roles, SSO, audit

- 2FA and Google login (enforceable on Pro+); roles Admin and Developer on Pro,
  plus Contributor, Viewer and Billing on Scale; organisation roles Owner, Member,
  Guest on Scale.
- **SAML SSO and SCIM** on Scale; **OIDC sign-in** on Pro+.
- **Audit logs** (workspace on Pro, organisation on Scale), exportable through the
  API (2025-09-24), with source IP and user agent (2026-03-25).
- **Protected environments**: only Admins may delete, suspend or *view secret
  values* in them. **Network-isolated environments**: private traffic from other
  environments is blocked.
- **Managed OIDC workload identity** (Pro+): services get short-lived identity
  tokens to authenticate to AWS (GA 2026-07-15), Anthropic and OpenAI
  (2026-07-24) "without storing long-lived API keys".
- SOC 2 Type II and ISO 27001 reports on Pro; HIPAA workspaces on Scale at a 20%
  compute premium.

### Preview environments and branches

- **Service previews** (single service, every plan): a pull request builds one
  service.
- **Preview environments** (Pro+, requires a Blueprint): "a fresh copy of your
  production environment (including services, datastores, and environment groups)
  with every pull request". "Copies of datastores and persistent disks do *not*
  include data"; an `initialDeployHook` seeds them. Per-preview overrides:
  `previewPlan` for smaller databases, `previews.plan` for services, `previewValue`
  for instance counts and variables. `previews.expireAfterDays` deletes idle ones;
  `generation: manual` builds only for pull requests titled `[render preview]`,
  `automatic` builds all but `[skip preview]`. GitHub gets a status; GitLab
  previews are not supported. Billed per second while they exist. Workflows are
  skipped in previews (2026-09-16).
- **Projects and environments**: two environments per project on Hobby, unlimited
  on paid plans; services can be moved between environments; environments are not
  cloned.

### Templates and catalogue

- A template gallery of example repositories with Blueprints and "Deploy to Render"
  buttons, plus framework quickstarts and migration guides from Heroku and Railway.
  No marketplace and no revenue share for authors were found.

### CLI, API, IaC, integrations

- **Blueprints** (`render.yaml`, any filename and path since 2026-02-09): services,
  databases, environment groups, disks, cron jobs, workers, previews, projects and
  environments (2025-10-27) and Workflows (2026-09-16). Values can come from
  another resource (`fromDatabase`, `fromService`), be generated
  (`generateValue`), or be asked for at creation (`sync: false`). Pushing to the
  linked branch syncs; "changes to a Blueprint never cause a resource to be
  deleted"; a resource deleted by hand is recreated on the next sync; dashboard
  edits that conflict are overwritten. `render blueprints validate` and an API
  endpoint check a file (2026-01-28).
- **REST API**; an official **Terraform provider**; Python and TypeScript **SDKs**
  (v1.2.0, 2026-09-23).
- **CLI** (v2.28.0): create services (2026-04-16), manage Postgres and Key Value,
  logs, deploys, SSH. Usage telemetry **on by default** since 2.26.0 (2026-09-01),
  disabled with `RENDER_CLI_DISABLE_ANALYTICS` or `DO_NOT_TRACK`.
- **MCP server** (GA 2025-08-21, hosted, OAuth for Claude Code, Codex and Cursor
  since 2026-07-22): create web services, static sites, cron jobs, Postgres and Key
  Value; **run a read-only SQL query**; fetch metrics and filtered logs; update
  environment variables; `trigger_deploy` (2026-07-17). It "supports limited
  changes to existing Render resources" and cannot delete anything or change
  scaling. Agent skills in `render-oss/skills`; debugging builds with Jules
  (2025-12-10).

### Notifications

- Email and Slack, per workspace with per-service overrides. Events: build and
  deploy failures, failed image pulls, **failed cron jobs**, **unhealthy
  services**, **disk usage above 80%**, failed Blueprint syncs, suspensions,
  failed one-off jobs (Slack only), and optionally successful deploys and recovery.
- Webhooks for completed actions, carrying the action's result (2025-11-14).

### Multi-server and networking

- **Automatic private networking** with service discovery inside a region and
  workspace; private services exist for exactly this.
- No servers to add: capacity is Render's, per region. There is no multi-region
  service; a service lives in one region.

### Team and collaboration

- Unlimited seats on Pro and Scale since the April 2026 change (previously $19 and
  $29 per member per month); multiple workspaces on Scale.
- Support: email on Hobby, chat on paid plans, premium support, Slack channel and
  a technical account manager as add-ons.

### Developer experience and onboarding

- One bill made of predictable parts: "your bill is just the sum of your
  services" (devtoolpicks, 2026-03-27).
- Blueprint-based "Deploy to Render" buttons; migration credits up to $10,000 and
  startup credits up to $100,000 (pricing page).
- "Zero DevOps" defaults: TLS, CDN, DDoS protection, health checks and
  zero-downtime deploys with nothing configured.

## What users love

- **Predictability.** Render's per-service prices make the bill a sum rather than a
  meter: "you know exactly what you will pay" (devtoolpicks, 2026-03-27).
- **"Very close to Heroku", done better.** In the February 2026 Hacker News thread
  on Heroku's freeze (525 points, 352 comments), Render is the first alternative
  named: "this is very close to heroku". Older threads say the same ("Render.com
  is such a better option these days (and I've been a huge Heroku fan since the
  beginning)").
- **Full-stack previews.** A complete copy of services and databases per pull
  request, seeded by a hook, at a smaller plan — Heroku review apps "at a fraction
  of the cost" (summary of Render's preview-environment example and HN threads).
- **Blueprints.** The whole system in one reviewed file in the repository, with
  `generateValue` and `fromDatabase` so no secret is ever written into it.
- **Staying up when AWS did not.** During the 15-hour AWS incident of 2025-10-20,
  "active Render services and workloads remained up and running throughout";
  builds and provisioning slowed.
- **Paid-tier reliability** when it works: "Using for years - amazing, no bugs no
  issues. Deployment swift" (Trustpilot, 2026-04-30).

### Why they stay, and what of it travels to a user's own servers

Render's loyalty rests on three DX ideas, all reproducible on a user's own
servers because Render itself is Kubernetes underneath:

1. **The system is a file** (Blueprints). A declarative description, synced on
   push, that never deletes. Pure panel work for Skifity.
2. **A pull request is a whole environment** (preview environments with fresh,
   seeded databases). Skifity's environments are already namespaces.
3. **Service types with good defaults** (web, private, worker, cron; health
   checks, zero-downtime, TLS on by default). Skifity has most of the machinery
   and less of the vocabulary.

What does not travel: a global CDN and edge caching, DDoS absorption at AWS
scale, and managed OIDC identities federated with AWS and model providers (the
last is possible on k3s but needs a publicly reachable issuer).

## What users complain about

- **The April 2026 pricing change.** Seats went flat, but included bandwidth fell
  and overage became metered: Hobby from 100 GB to 5 GB, Pro to 25 GB, then
  $0.15/GB (Render changelog 2026-04-23; the old figures are from bex.co,
  2026-09-03, a third party). Render's own line: "the new Hobby and Pro plans
  include less bandwidth than their legacy counterparts, because legacy plans
  subsidized bandwidth usage with seat fees." bex.co's worked examples: a solo
  developer serving 150 GB goes from $0 to $21.75–$46.75 a month. Render says 75%
  of paying customers pay the same or less. A "Tell HN" thread about the cut drew
  5 points — loud on blogs, quiet on Hacker News.
- **Free tier cold starts.** Fifteen minutes idle, about a minute to wake;
  "the forced upgrade from free tiers to avoid cold-start spin-downs feels like a
  structural nudge" (G2 summary via search); "Free tier is a trap and billing is a
  nightmare ... instances aggressively go to sleep" (Trustpilot, 2026-09-03).
- **Step-shaped compute prices.** 512 MB at $7, then 2 GB at $25; "most production
  workloads need at least the Standard tier at $25/month" (search summaries of
  pricing guides). A typical web + database + worker stack costs $21–52/month
  before any business logic (devtoolpicks, 2026-03-27).
- **A steady rhythm of control-plane incidents.** Render's status page lists 50
  incidents between 2026-02-03 and 2026-09-27, mostly builds and deploys, GitHub
  connectivity and single regions: "Render Dashboard Unavailable" (2026-07-15, 32
  minutes, critical), "GitHub git clone 403 errors" (2026-07-19, 5.5 hours,
  critical), "Service Disruption in Oregon" (2026-07-24, 2.2 hours, critical),
  "Disruption affecting the provisioning of new instances in Singapore"
  (2026-04-08, 12 hours), "HA Postgres Unavailability" (2026-05-07, 54 minutes).
  Serving outages are rarer than deploy outages.
- **Suspensions and support.** Trustpilot 2.2/5 from 60 reviews (read
  2026-09-30): "account was suddenly suspended ... within only a few hours"
  (2026-09-09), "Their support is horrible" (2026-04-16), charges on free accounts.
- **Disks are second-class.** One instance, no zero-downtime deploys, snapshots
  "not for custom database recovery".
- **Free Postgres expires** after 30 days; free Key Value loses its data on
  restart; free services cannot send SMTP (2025-09-16).

## Security record

- **No CVE or advisory against Render's platform, CLI or MCP server was found**
  (GitHub Advisory Database and web searches, 2026-09-30).
- **RediShell (CVE-2025-49844)**, a critical RCE in Redis and Valkey disclosed by
  Wiz in 2025: Render published a response, scheduled maintenance to move every
  Key Value instance to patched Redis 6.2.20 or Valkey 8.1.4 (about a minute of
  unavailability each), and reported no evidence of exploitation. Key Value
  instances block inbound internet traffic by default.
- The 2026-09-01 change turning CLI telemetry on by default is not a
  vulnerability, but it is a data-flow change users had to opt out of.
- Render's own incident on 2026-07-15 ("Some users enrolled in 2FA are
  experiencing login issues", 65 minutes) is an availability issue, not a
  disclosure.

## Against Skifity

| Capability | Render | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy sources | GitHub, GitLab, Bitbucket, image | GitHub, GitLab, Gitea, a generic Git URL, image, a folder from the CLI or browser (`internal/api/integrations_handlers.go:62`, `internal/cli/up.go`). Written | Parity |
| Builds | Native runtimes, Docker, performance pipeline | Railpack, Nixpacks, Dockerfile, static front end served by Caddy; registry-backed build cache (`internal/builder/job.go:587-601`). Written | Parity |
| Pre-deploy command | Yes | Release command, stops the deploy on failure (`internal/deploy/run.go:124-170`) | Parity |
| Deploy hook for CI | Secret URL | `POST /api/apps/{id}/deploy` with a scoped API token (`internal/api/api.go:282`, `internal/auth/scopes.go`) | Parity |
| Rollback | Retained builds | Image *and* settings, refuses a collected image (`internal/deploy/deployer.go:483-535`); last ten images kept | Skifity ahead |
| Config change without rebuild | Env var change redeploys | Runtime change is a rollout, build-time change a build (ADR-0007) | Skifity ahead |
| Monorepo filters | Root dir + build filters | Root dir only (`store.App.RootDir`); every push to the branch deploys (`internal/api/webhook_handlers.go:152-176`) | Behind |
| Domains, TLS | Subdomain, custom, wildcard, auto TLS | sslip.io address, custom domains with cert-manager, a wildcard domain setting for app subdomains (`internal/settings/settings.go:124`). Written | Parity |
| CDN and edge caching | Yes | None | Absent in Skifity |
| Firewall, IP rules | DDoS, firewall; IP rules on datastores (all), web (Enterprise) | Per-app rules on IP, country, ASN, and/or (`internal/edgerules`, `internal/guard`). Written, never through a live Traefik | Skifity ahead on web-service rules |
| Password in front of an app | None found in the docs read | HTTP basic auth through Traefik from a bcrypt hash in the app's namespace, inherited by previews, admin-only to change (`internal/api/password_handlers.go`, `internal/kube/password.go`, commit `7865bc8`). Written | Skifity ahead |
| Maintenance mode | 503 page | None | Absent in Skifity |
| Service types | Web, private, worker, cron, workflow | An app with port 0 renders no Service or Ingress (`internal/kube/manifests.go:225,252`) and templates mark workers (`internal/templates/templates.go`, `Public`); the new-app form has no type and defaults the port to 3000 (`internal/api/apps_handlers.go:297`) | Behind on vocabulary |
| Cron jobs | Separate service, per-second billing, failure notifications | Named schedules per app as CronJobs, no overlap (`internal/api/api.go:295-298`); no failure notification (`internal/notify/notify.go:41-47`) | Parity; behind on alerting |
| Postgres | PITR 3–7 days, replicas, HA, pooling, auto storage, credential rotation | CloudNativePG with replicas (`internal/dbsvc/manifests.go:107-121`); logical dumps to S3 (ADR-0012); no PITR, pooler, resize or rotation | Behind |
| Redis-compatible | Key Value, persistence options | Redis 7 with AOF on a volume, single instance (`internal/dbsvc/manifests.go:277-283`) | Parity (single node) |
| MySQL | None managed | MariaDB 11.4 as "MySQL", single instance (`internal/dbsvc/manifests.go:29-33`) | Skifity ahead |
| Engine patching | Scheduled maintenance per instance (RediShell) | Floating major tags (`redis:7-alpine`, `ghcr.io/cloudnative-pg/postgresql:17`); no maintenance action, no database update endpoint (`internal/api/api.go:320-330`) | Behind |
| Disks | Daily snapshots, grow live | PVC, tar backups to S3 with schedule and retention, restore (`internal/backup/volume.go`, `internal/api/api.go:309-316`); no resize. Written | Parity (different trade-offs) |
| Disk-full warning | Notification at 80% | None | Absent in Skifity |
| Autoscaling | CPU/memory targets, Pro+ | HPA on CPU/memory, scale to zero via KEDA (`internal/kube/manifests.go:362`, `internal/kube/scaletozero.go`); scaling readiness check (`internal/deploy/scaling.go`). Written | Skifity ahead |
| Idle sleep | Free tier only, 15 min, ~1 min wake | Opt-in per app, five minutes, request held by KEDA (`docs/concepts.md`). Written | Skifity ahead |
| Logs | 7–30 days, search, syslog streams, HTTP logs | Live stream and previous container, browser-side filter (`web/src/components/app/logs-tab.tsx`); no retention, no streams | Behind |
| Metrics | CPU, memory, instances; OTel stream | Current usage only (`internal/kube/client.go:324-381`); the panel's own Prometheus endpoint (`internal/api/metrics_handlers.go`) | Behind |
| SSH / shell | SSH into instance or ephemeral instance | None by decision; one-off commands in the app's image instead (`docs/roadmap.md`, "Not on this roadmap") | Absent by decision |
| Sign-in, SSO | 2FA, Google; OIDC Pro+; SAML/SCIM Scale | Argon2id, TOTP, recovery keys, OIDC with PKCE, all free (`internal/auth`) | Skifity ahead for self-hosters |
| Roles | 2 on Pro, 5 on Scale | Owner, admin, member (`internal/store/models.go:5-20`); scoped API tokens | Behind on granularity |
| Protected environments | Admin-only destructive actions and secret viewing | None per environment; secrets are write-only for everyone (`internal/crypto`) | Behind on protection, ahead on secrets |
| Environment network isolation | Opt-in block | Default-deny NetworkPolicy per environment (`internal/kube/namespace.go:127-217`). Written | Skifity ahead (default) |
| Workload identity (OIDC) | AWS, Anthropic, OpenAI | None | Absent in Skifity |
| Audit log | Workspace/org, API export | Team audit log, 365 days by default (`internal/api/api.go:213,519`, `internal/store/retention.go:42`) | Parity |
| Preview environments | Whole Blueprint, fresh datastores, seed hook, plan overrides, expiry, title flags | One app per PR, resealed variables, no secrets for forks, an empty database of its own per linked one since `1ae3276` (none for forks), 7-day reaper, commit status and one PR comment (`internal/api/webhook_handlers.go:183-445`, `internal/watch/watch.go:136-190`, `internal/deploy/gitreport.go`); no other apps, no seed, no overrides, no title flags. Written | Behind (narrowing) |
| Environments | Unlimited, move services | Create and delete; no move, clone or compare (`internal/api/teams_handlers.go:429-468`) | Behind |
| IaC | Blueprints, Terraform, SDK | None; `skifity.toml` links a folder to an app (`internal/cli/project.go:25-31`); `skifity export` writes JSON and Kubernetes YAML (`internal/api/export_handlers.go`) | Absent in Skifity (export only) |
| Templates | Example repos with Blueprints | 282 verified one-click templates, multi-service stacks with linked databases (`internal/templates/catalogue`) | Skifity ahead |
| CLI | Services, DBs, logs, SSH, blueprint validate | 17 commands, `--json` everywhere (`internal/cli/commands.go:34-71`) | Behind on breadth |
| MCP | Hosted, OAuth, create resources, read-only SQL, metrics, logs, no deletes | Local stdio, 15 tools, no deletes, errors with cause/impact/fix (`internal/mcpserver/server.go:314-391`) | Behind on breadth; parity on safety |
| Notifications | Email, Slack; failures incl. cron, disk, blueprint | Telegram, Discord, webhook, email, plugin kinds such as Slack; seven events (`internal/notify/notify.go`, ADR-0021). Written | Parity on channels, behind on events |
| Private networking | Automatic, service discovery | Service DNS inside an environment; addresses not surfaced in the panel | Parity; behind on DX |
| Servers and regions | Five AWS regions | Your servers, one k3s cluster, SSH-provisioned (`internal/provision`) | Different model |
| Telemetry | CLI telemetry on by default | Never phones home (README, "It never phones home") | Skifity ahead |
| Team and cost | Flat plan + compute + bandwidth meter | Unlimited users, no billing, your hardware | Different model |

## Gaps worth closing in Skifity

### No P0

The P0 this research found — previews inheriting production's `DATABASE_URL`,
because a database link is stored as a variable (`internal/dbsvc/manager.go:341-352`)
naming the production namespace's Service (`internal/dbsvc/manifests.go:144`) and
`copyPreviewVariables` copied it — was fixed in commit `1ae3276` on 2026-09-30
while this was being written: previews now get an empty database of their own per
linked one (`internal/api/webhook_handlers.go:373`). Written, never run on a
cluster. The rest of Render's preview lead is P1 below.

### P1 — Full-stack previews: the rest of the stack, a seed hook, plan overrides

**What.** A pull request's preview should bring every app of the environment that
the changed app needs, not only the changed app, reusing the image production
runs for the ones the pull request did not touch; run a per-app **seed command**
once after the first successful preview deploy; let previews ask for less than
production (Render's `previewPlan` and `previewValue`: resources, instance count,
a variable overridden only in previews); and honour `[skip preview]` in a pull
request's title, or build only on `[preview]` when previews are manual.

**Evidence.** Render: "a fresh copy of your production environment (including
services, datastores, and environment groups) with every pull request", seeded by
`initialDeployHook`, sized by `previewPlan`. Railway and Heroku do the same.
Skifity copies exactly one app (`internal/api/webhook_handlers.go:250-300`), gives it
an empty database (since `1ae3276`) and nothing to put in it beyond what the
release command migrates.

**Fit.** `deployPreview` walks links and references to the other apps; a
deployment carrying an existing image skips the build
(`internal/deploy/deployer.go:198-212`); `seed_command` and a small set of
preview overrides on `store.App`; the title flags in `internal/gitsrc/webhook.go`,
which already parses pull-request events; the seed's outcome in the PR comment
(`internal/deploy/gitreport.go`).

**Size.** M. **Without a cluster:** the dependency walk, overrides, title flags and
seed bookkeeping are unit-testable against the fake clientset; the seed actually
running needs a cluster.

### P1 — A project file: the environment as reviewed YAML

**What.** `skifity.yaml` in the repository describing an environment's apps,
databases, links, variables (literal, `generate: secret`, `prompt: true`,
`${{ app.VAR }}` references), scaling, volumes, domains, scheduled commands, the
release and seed commands, and preview settings. `skifity plan` shows the diff
against the live environment; `skifity apply` applies it; the panel can link an
environment to the file and sync on push. **Never deletes** unless asked
(`--prune`), like Render. Secrets are generated or prompted and never written back.
`skifity export --format project` writes the file *from* an existing environment,
so adoption starts from what already runs.

**Evidence.** Blueprints are the backbone of Render's previews and "Deploy to
Render" buttons; Railway replaced its JSON config with a TypeScript IaC SDK with
plan and apply (docs, read 2026-09-30); Heroku's `app.json` drives review apps and
buttons. Skifity has an export (`internal/api/export_handlers.go`) and a
`skifity.toml` that only links a folder to an app (`internal/cli/project.go:25-31`)
— nothing declarative.

**Fit.** Skifity's template schema is already most of a Blueprint: services with
image, port, health path, variables, volumes and resources; databases with
`link_to` and `var_name`; inputs with `generate`, `secret` and `required`
(`internal/templates/templates.go`, the `Template`, `Service`, `DatabaseSpec` and
`Input` types). Extend it with a Git source, scaling, domains, jobs and previews,
reuse the template installer for creation, and add a diff/apply layer in a new
`internal/projectfile` package behind `POST /api/environments/{env}/plan` and
`/apply`, used by the CLI, the MCP server and a panel page. YAML rather than a
TypeScript SDK, because the binary is Go and must evaluate the file without a
JavaScript runtime. Version the format and read old versions forever (Railway is
retiring `railway.json` on 2026-12-01; do not do that to users).

**Size.** L. **Without a cluster:** entirely — parsing, validation, diffing against
the store, secret handling and the CLI are unit and smoke-test work.

### P1 — Service types in the new-app form

**What.** Ask "What is it?" — **Website or API** (public, port, domain), **Private
service** (port, no domain), **Background worker** (no port), **Scheduled task**
(an image and schedules, no long-running instance) — and set the defaults from the
answer, including the scaling checker's view of workers.

**Evidence.** Render's five service types are how its documentation, pricing and
Blueprints are organised; Heroku's Procfile has `web` and `worker`. Skifity can run
all four shapes (port 0 renders no Service or Ingress, `internal/kube/manifests.go:225,252`;
scheduled commands exist) but the form defaults the port to 3000
(`internal/api/apps_handlers.go:297`) and never asks, so a worker is created as a web
app with a health check that fails.

**Fit.** `web/src/pages/new-app.tsx`, a `kind` on the create request mapped to
port, domain and probe defaults in `internal/api/apps_handlers.go`, detection hints
from a Procfile (see `heroku.md`). **Size.** S. **Without a cluster:** yes
(Playwright and API tests).

### P1 — Postgres point-in-time recovery and a connection pooler

**What.** Continuous WAL archiving for PostgreSQL to the S3 bucket backups already
use, with "restore to a time" on the database page (as a new database, so the old
one stays); and a PgBouncer pooler per database behind a second connection string.

**Evidence.** Render gives PITR even on Hobby (3 days) and added pooling in July
2026; Railway shipped both in 2026; Heroku sells rollback from Standard up. Skifity
takes logical dumps on a schedule (ADR-0012), so the worst-case loss is a whole
schedule interval.

**Fit.** CloudNativePG already does both (barman object-store backups and
`Pooler` resources); `internal/dbsvc/manifests.go` renders the cluster, and
`internal/backup` holds the S3 settings. **Size.** M. **Needs a cluster** to
verify; manifests are unit-testable.

### P1 — The failure notifications Render sends and Skifity does not

**What.** New events: a scheduled command failed; a one-off command failed; a
volume or database disk is above 80%; a server's disk is above 85%; a backup is
overdue (not just failed). Respect the hysteresis problem noted in Phase 72.

**Evidence.** Render notifies on "failed cron jobs", "disk usage exceeding 80%" and
"failed one-off jobs"; Skifity's seven events (`internal/notify/notify.go:41-47`)
cover deploys, health, servers, backups and certificates only.

**Fit.** `internal/watch` already runs every minute and dedupes by stored state;
CronJob and Job outcomes are readable from the API; disk figures need the
kubelet's stats summary. **Size.** S (jobs) + M (disks). **Without a cluster:**
event logic and delivery yes; real disk figures need one.

### P2 — Protected environments

**What.** Mark an environment protected: members may deploy to it (by push or
button) but only admins may delete apps or databases, restore backups, change
domains or variables, or roll back.

**Evidence.** Render's protected environments; Railway's environment RBAC. Skifity
checks the team role per action and never the environment: deleting a database or
restoring a backup already needs an admin (`internal/api/data_handlers.go:91-92`,
`377-378`), but a member may delete an app or change its variables
(`internal/api/apps_handlers.go:573-574`, `758-759`) in production exactly as in a
preview. **Fit:** a column on `environments` and a raised role inside
`authorizeApp` and friends when the environment is protected; the route-walk test
in `internal/api/api_test.go` extends naturally. **Size:** M. **Without a
cluster:** yes.

### P2 — Maintenance mode

**What.** A switch that answers every request to an app with a 503 and a
translated or custom page while the app keeps running for one-off commands.

**Evidence.** Render and Heroku both have it. **Fit:** the edge guard already sits
in front of apps with rules (`internal/guard`, `internal/edgerules`); a
"maintenance" default action is a small extension. **Size:** S. **Needs a
cluster** only for the end-to-end check.

### P2 — Database engine maintenance

**What.** Show the running engine version, whether a patched image exists, and a
"restart onto the patched version" action; pin exact patch versions like the
template catalogue does instead of floating major tags.

**Evidence.** Render's RediShell response moved every Key Value instance to
patched versions through a scheduled maintenance. Skifity runs `redis:7-alpine`
and `ghcr.io/cloudnative-pg/postgresql:17` (`internal/dbsvc/manifests.go:210,277`)
and has no database update endpoint (`internal/api/api.go:320-330`), while its
templates refuse floating tags on principle (`internal/templates/templates.go`).

**Size.** M. **Without a cluster:** version logic yes; the restart needs one.

### P2 — Log streams

**What.** Forward every app's stdout/stderr to a syslog, HTTPS or OTLP endpoint
per team, without the panel in the path (a DaemonSet shipper such as Vector or
Fluent Bit, installed on first use like the other components).

**Evidence.** Render's log streams on every plan; Heroku's drains; Railway tells
users to run their own forwarder. Skifity keeps nothing beyond the container.

**Size.** M. **Needs a cluster.**

## Things to deliberately not copy

- **Metering the network.** Render's 2026 change moved cost from seats to
  bandwidth, and the complaints followed. On a user's own servers there is no
  meter, and Skifity should not invent quotas that behave like one.
- **Features gated by plan.** Autoscaling, full-stack previews, OIDC sign-in,
  SAML, audit logs and protected environments are all plan-gated at Render.
  Skifity's equivalents are free because nothing is billed; keep them that way,
  including SSO (checklist item 11).
- **Telemetry on by default.** Render's CLI started collecting usage on
  2026-09-01. Skifity's "never phones home" is a product promise worth more than
  the data.
- **Free-tier sleep as a sales lever.** Render's 15-minute spin-down is a
  "structural nudge" to paying. Skifity's scale to zero is opt-in per app and holds
  the request rather than failing it; keep it a user choice.
- **Blueprint sync that silently overwrites dashboard edits.** Render's docs
  admit dashboard changes are overwritten on the next sync. A Skifity project file
  should show drift and ask, not overwrite.
- **Expiring free databases.** A self-hosted database has no reason to expire.
- **Durable Workflows and Sandboxes as core features.** They are products of their
  own; on a user's servers the catalogue already offers Inngest and n8n
  (`internal/templates/catalogue/inngest.yaml`, `n8n*.yaml`). Keep Skifity's scope
  to running apps well.
- **Disk snapshots as the database backup.** Render warns against restoring a
  database from a disk snapshot; Skifity's split — logical dumps for databases,
  tar for volumes, both off-site — is the right one.

## Sources

All read 2026-09-30 unless marked.

- https://render.com/pricing (downloaded and read as text)
- https://render.com/changelog (downloaded and read as text, entries 2025-08-11 to 2026-09-23)
- https://render.com/changelog/updated-plans-for-render-workspaces (2026-04-23)
- https://render.com/blog/better-pricing-for-fast-growing-teams (2026-04-23)
- https://render.com/blog/series-c-extension (2026-02-17)
- https://render.com/blog/how-render-services-stayed-up-during-the-aws-october-outage
- https://render.com/blog/response-to-redishell-cve-2025-49844 (via search results)
- https://render.com/docs/infrastructure-as-code
- https://render.com/docs/preview-environments
- https://render.com/docs/scaling
- https://render.com/docs/free
- https://render.com/docs/disks
- https://render.com/docs/postgresql
- https://render.com/docs/projects
- https://render.com/docs/maintenance-mode
- https://render.com/docs/notifications
- https://render.com/docs/mcp-server
- https://status.render.com/api/v2/incidents.json (the 50 most recent incidents, 2026-02-03 to 2026-09-27)
- https://www.cnbc.com/2026/02/17/render-raises-100-million-at-1point5-billion-valuation.html (via search results)
- https://www.futuriom.com/articles/news/render-scores-80-million-in-series-c-funding/2025/01 (via search results)
- https://bex.co/blog/2026/09/03/render-pro-seats-vs-bandwidth (third-party analysis)
- https://bex.co/blog/2026/07/09/render-april-2026-egress-repricing-hobby (via search results)
- https://news.ycombinator.com/item?id=48235993 ("Tell HN" on the bandwidth cut)
- https://news.ycombinator.com/item?id=46913903 (Heroku freeze thread, where Render is named)
- https://news.ycombinator.com/item?id=29649852 (via search results)
- https://devtoolpicks.com/blog/railway-vs-render-vs-fly-io-solo-developers-2026 (2026-03-27)
- https://www.g2.com/products/render-render/pricing (via search results)
- https://www.trustpilot.com/review/render.com
- https://github.com/render-examples/preview-environment (via search results)
- GitHub API via repository search, `org:render-oss` (stars and open issues for cli, render-mcp-server, terraform-provider-render, skills, sdk)
- Skifity: the files cited in the table, at commit `1ae3276`
