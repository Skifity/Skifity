# Railway

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail. Brought up to date on
> 2026-11-08: facts about Railway that changed are corrected in place, and "Since 2026-09-30"
> says what changed, with its sources.

Researched 2026-09-30 against live sources. Skifity's side of every comparison was
checked in the code at commit `1ae3276` (2026-09-30). Uncommitted work seen in the
working tree that day is not counted.

Railway is a managed, usage-billed cloud for long-running containers: web
services, workers, cron services, databases and volumes, arranged on a visual
**project canvas** and wired together with **reference variables**. It is for
individual developers and small teams first; it now also sells to larger
companies (SOC 2 Type II and HIPAA, SSO, audit logs, access groups). **Architecture:**
a proprietary control plane and its own bare-metal orchestration rather than
Kubernetes (TechTarget, 2026-01-22), running workloads on Railway's own co-located hardware
("Railway Metal", moved into from 2024) plus AWS and Google Cloud. What it keeps
state in is partly visible through its incident reports: the backend API sits on
a PostgreSQL database with a table of "approximately 1 billion records" behind
PgBouncer (incident report, 2025-10-28), deploys go through a task queue of
workers (2025-11-25), and until May 2026 the network control plane's API was
hosted solely on Google Cloud (2026-05-19). **Licence:** the platform is
proprietary; the builders are open source — Railpack (MIT, Go, 1,213 stars, 61 open
issues) and its predecessor Nixpacks (Rust, 3,554 stars, now in maintenance mode);
the CLI `railwayapp/cli` has 621 stars and 119 open issues (GitHub API,
2026-09-30). **Pricing (read 2026-09-30):** Free $0 with $1/month of credit; a
30-day trial with $5 once; Hobby $5/month including $5 of usage; Pro $20/month
including $20 of usage, seats "unlimited and included"; Enterprise custom. Usage is
billed per second: about $20 per vCPU-month, $10 per GB-month of memory, $0.15 per
GB-month of volume, $0.05 per GB of egress, object storage $0.015 per GB-month.
**Maturity and adoption:** founded 2020; a $100M Series B led by TQ Ventures on
2026-01-22, when it reported "more than 2 million users", nearly 200,000 new
developers a month, and 100,000 paid users at 25,000 businesses (SiliconANGLE,
TechTarget, 2026-01-22); InfoQ put it at 3 million users on 2026-05-30, and
Railway itself at "5 million existing users" on 2026-10-26. It ships a numbered
weekly changelog (#0306 on 2026-09-04; the latest on 2026-11-08 was published
on 2026-11-05).

## Feature inventory

### Deploy sources and builds

- Deploy from a GitHub repository, a Docker image, a local folder (`railway up`),
  or a template. Builds use Railpack by default (Nixpacks for older services) or
  a Dockerfile when present.
- **Pre-deploy command** (Settings → Deploy): runs between build and deploy in a
  separate container with the service's variables and private network; "if your
  command fails, it will not be retried and the deployment will not proceed".
  Volumes are *not* mounted in it.
- **Wait for CI**: a deployment sits in `WAITING` until every GitHub Actions check
  suite on the commit finishes; a failed workflow skips the deploy, a run still
  unfinished after two hours skips it too.
- **Watch paths** and **root directory** per service for monorepos: a push that
  touches nothing a service watches does not deploy it.
- Zero-downtime handover is tunable by two variables,
  `RAILWAY_DEPLOYMENT_OVERLAP_SECONDS` and `RAILWAY_DEPLOYMENT_DRAINING_SECONDS`;
  healthchecks gate a deployment becoming Active; restart, redeploy, remove and
  rollback from the deployment list, limited by the plan's retention.
- **Blocking known-vulnerable code:** during the React2Shell wave (CVE-2025-55182,
  December 2025) Railway blocked new deployments of vulnerable Next.js versions
  outright.

### Domains, TLS and routing

- A generated `*.up.railway.app` domain per service, custom domains by CNAME plus
  a TXT verification record, certificates issued and renewed automatically.
  Custom-domain allowance: Trial 1, Free 0, Hobby 2, Pro 20.
- **TCP proxy** for non-HTTP services (a public host and port, exposed to the
  service as `RAILWAY_TCP_PROXY_PORT`), which is how a database is reached from a
  laptop.
- **Edge Rules** (changelog 2026-08-20): rules evaluated before traffic reaches a
  service, matching IP/CIDR, hostname, path and headers, with actions allow,
  block, challenge, redirect or cache override.
- **Railway CDN**: Railway's own CDN ("Hikari", built in 30 days, blog
  2026-06-04), on every plan at no extra cost, off by default and switched on per
  service; a São Paulo region was added on 2026-09-11. This file left it out on
  2026-09-30.
- **Railway Authentication** (2026-10-08): a login in front of a service or a
  whole environment; see "Since 2026-09-30".
- Outbound IPv6 for all services (2026-04-24, off by default).

### Databases and services

- Databases are ordinary services created from templates: PostgreSQL, MySQL,
  Redis, MongoDB, ClickHouse and others, each with a volume.
- **PostgreSQL high availability** on Patroni (one click, March 2026),
  **PgBouncer** one click (June 2026), **point-in-time recovery**, and a
  `railway postgres ha | pitr | pgbouncer` CLI to convert, scale, switch primary,
  restore to a timestamp and watch pool usage (changelog #0306, 2026-09-04).
- **Redis HA** (Sentinel failover behind HAProxy) and **MySQL HA** in beta
  (2026-08-20); conversion rewrites the project's variables for you. Since then:
  one-click **Postgres major-version upgrades** (2026-09-11, missed here on
  2026-09-30), **MySQL point-in-time recovery** (generally available 2026-10-01),
  **MongoDB HA** in Priority Boarding (2026-10-01), and password rotation across
  HA clusters (2026-11-05).
- **Buckets**: S3-compatible object storage on the canvas, $0.015/GB-month with
  free egress.
- **Functions**: single-file TypeScript services run on Bun.
- A database view for PostgreSQL and MySQL: a table list, an entries view, and
  editing or adding rows in the dashboard (forum threads show it sometimes cannot
  connect).

### Storage and backups

- One volume per service; size by plan (0.5 GB Free/Trial, 5 GB Hobby, 50 GB Pro,
  self-serve to 1 TB on Pro). **Resized live, without downtime**; never shrunk.
- "Replicas cannot be used with volumes", and a service with a volume has a short
  outage on every redeploy because two deployments may not mount it at once.
- **Backups** are incremental and copy-on-write: daily (kept 6 days), weekly (kept
  27 days), monthly (kept 89 days), several schedules at once, plus manual ones
  capped at 50% of the volume's size. Restore mounts a *new* volume from the
  backup and keeps the old one unmounted, staged for review. Restores stay in the
  same project and environment; wiping a volume deletes its backups.
- Files are reachable over SFTP/SCP: `railway volume browse` / `railway volume
  files`. A deleted volume is recoverable for 48 hours.

### Scaling and high availability

- **Vertical**: a service may use up to its plan's ceiling (Hobby 48 vCPU / 48 GB,
  Pro 1,000 vCPU / 1 TB per service as listed) and is billed for what it uses.
- **Horizontal**: replicas set by hand (Hobby up to 6, Pro up to 42), across
  regions, with requests spread round-robin. **There is no built-in horizontal
  autoscaler.** Railway's own guide says "the replica count is a setting you
  control, and by default it stays where you set it" and walks through building
  an autoscaler service that calls the Public API.
- **Serverless** (formerly App Sleeping): a service with no outbound traffic for
  roughly 5–10 minutes is put to sleep and woken by the next request.
- Multi-region deploys, all four regions on Railway Metal: US West (California),
  US East (Virginia), EU West (Amsterdam), Southeast Asia (Singapore).

### Observability (logs, metrics, alerts, uptime)

- **Log Explorer** across services with a query language: `@level:error`,
  `@service:<id>`, `@httpStatus:500`, `@responseTime:>500`, boolean `AND`/`OR`/`-`
  and parentheses; structured JSON logs keep attributes and multi-line traces;
  HTTP and DNS logs as separate streams. Retention: Free 3 days, Hobby 7, Pro 30,
  Enterprise up to 90. A hard limit of 500 lines per second per replica.
  **No log drains**: the docs say to run a forwarder (Vector, Fluent Bit) or use
  OpenTelemetry.
- **Metrics**: CPU, memory, disk and network for up to 30 days, per replica or
  summed, with dotted lines marking each deployment.
- **Observability dashboard** per environment: drag-and-drop widgets for metrics,
  logs and project spend.
- **Monitors** (Pro): thresholds above or below a value on CPU, RAM, disk or
  egress, notifying by email, in-app and webhook.
- **Tracing**: "distributed tracing from Railway's edge through your services,
  with a built-in OpenTelemetry collector and automatic instrumentation" (features
  page). It is in preview: the dashboard's Traces page is behind Priority
  Boarding (since 2026-09-25) and `railway trace` is open to everyone (since
  2026-11-05). This line presented it as generally available on 2026-09-30.

### Security, auth, roles, SSO, audit

- SOC 2 Type II and HIPAA; SSO and role-based access control; **audit logs**
  (2025-12-12) filterable by event, environment, project and time.
- **Sealed variables**: a value "provided to builds and deployments but is never
  visible in the UI nor can it be retrieved via the API"; cannot be unsealed, not
  copied into PR or duplicated environments. Sealing is **opt-in per variable**.
- **Environment RBAC** (Enterprise): non-admins cannot read a sensitive
  environment's resources but can still deploy to it by pushing.
- **Access Groups** (Enterprise, beta, 2026-08-14): groups of people with a project
  role attached to a set of projects.

### Preview environments and branches

- **Persistent environments** (production by default, staging and others), each
  an isolated copy of the project's services with its own variables and network.
- **Duplicate** an environment: "a copy of the selected environment, including
  services, variables, and configuration", all staged for review before deploy.
- **Sync** between environments: service cards come back tagged New, Edited or
  Removed, staged until deployed.
- **PR environments**: one per pull request, made from a base environment and
  "deleted as soon as the PR is merged or closed"; services get a domain when
  their base-environment counterpart has one. Since December 2025 GitHub shows
  one deployment per commit per PR environment rather than one per service.
- **Focused PR environments**: deploy only the services whose watch paths or root
  directory the pull request touched, plus their dependencies; the rest show as
  skipped on the canvas.
- **Bot PR environments**: opt in to previews for Dependabot, Renovate, GitHub
  Copilot, Claude Code, Devin, Jules and similar bots.

### Templates and catalogue

- A marketplace of **1,800+ templates** (Railway blog, 2025-12-05), from single
  databases to multi-service stacks; a template is made by composing a project.
- **Kickbacks**: a template's author receives 15% of the usage its deployments
  generate, 25% if they answer users in the Template Queue; paid as credits or
  as cash via Stripe Connect in $100–$10,000 withdrawals. Railway reported
  ~$1M paid, two authors past $100,000, six past $10,000 and thirty past $1,000
  (2025-12-05).
- Deployed templates are told when the author publishes an update. Template
  edits are staged and versioned, and the agent can write a template from a
  description (2026-08-14).

### CLI, API, IaC, integrations

- **CLI**: `init`, `link`, `up`, `run` (a *local* command with the service's
  variables), `shell`, `connect` (a database shell), `ssh` (into the container),
  `logs`, `variable` (including `variable edit` in `$EDITOR` with a diff),
  `environment`, `domain`, `volume`, `add`, `redeploy`, `down`, `dev` (the whole
  environment locally under Docker Compose, rewriting connection variables),
  `postgres`, `templates`, `config`, `agent`, `mcp`, `skills`, `ca`.
- **Public GraphQL API**.
- **Infrastructure as Code**: `.railway/railway.ts` (GA; Python and Go in beta),
  declaring services, databases, volumes, buckets, domains, variables, replicas and
  canvas groups. `railway config plan` previews, `railway config apply` re-plans and
  refuses if the environment changed since it was read; a GitHub Action posts the
  plan on the pull request and applies on merge. It **replaces** the older
  `railway.json`/`railway.toml` Config as Code, which new services may not use
  since 2026-08-28 and which stops working on **2026-12-01**.
- **MCP**: a hosted server at `mcp.railway.com` (OAuth or CLI credentials) exposing
  projects, services, feature flags, redeploy and a `railway-agent` tool; since
  2026-09-04 `railway mcp` proxies to it, and the local server survives as
  `railway mcp local`. The old open-source `railway-mcp-server` repository is
  archived. Destructive tools carry protocol-level hints.
- **Railway Agent** (all workspaces, 2026-04-24): a chat panel and `railway agent`
  that creates services, connects databases, diagnoses a failed deploy from logs
  and config, and opens a pull request with the fix; billed at LLM cost with no
  markup against plan credits, with spending caps. **Connectors** (beta) read
  Notion, Linear and Sentry. **Cloud agents** run coding sessions in Railway VMs
  (2026-08-20). `railway skills` installs agent skills into Claude Code, Cursor,
  Codex and OpenCode. A ChatGPT plugin (2026-07-31) and an iOS app (2026-06-19).

### Notifications

- Email and in-app notifications for deploys and Monitors; webhooks for
  deployment state; no first-party chat integrations beyond webhooks were found
  in the docs read.

### Multi-server and networking

- **Private networking** over encrypted WireGuard tunnels with internal DNS,
  `SERVICE_NAME.railway.internal`, scoped to one project environment, "zero
  configuration".
- There are no "servers" to add: capacity is Railway's, spread across its regions
  and providers. Multi-region replicas are the multi-server story.

### Team and collaboration

- Workspaces with unlimited seats on Pro; roles; access groups and environment
  RBAC on Enterprise; audit logs.
- **Staged changes**: "adding, updating, or removing variables, results in a set of
  staged changes that you must review and deploy" — a team sees a change set before
  it goes out.
- Support: community forum for Free/Hobby ("responses are not guaranteed"); Pro
  "usually within 72 hours"; Business Class with a one-hour P1 acknowledgement.

### Developer experience and onboarding

- **The canvas.** Every service, database, volume and bucket of an environment
  is a card on one screen, with connections drawn between services that reference
  each other, groups to organise them, and staged changes shown on the cards
  before a single Deploy button applies them. Reviewers call it "genuinely
  brilliant" (devtoolpicks, 2026-03-27).
- **Reference variables**: `${{ Postgres.DATABASE_URL }}`, `${{ shared.KEY }}`,
  `${{ api.RAILWAY_PRIVATE_DOMAIN }}`, with autocomplete in both the name and value
  fields. **Suggested variables** are read from `.env`, `.env.example`,
  `.env.local` and friends in the repository.
- **Railway-provided variables**: `RAILWAY_PUBLIC_DOMAIN`, `RAILWAY_PRIVATE_DOMAIN`,
  `RAILWAY_TCP_PROXY_PORT` and others, so wiring is by name, never by address.
- **Anonymous deploys** at `railway.com/new` or `dev.new` (2026-08-20): a site or a
  database without an account, with 60 minutes to claim it.
- `railway dev` runs the whole environment locally with rewritten connection
  variables (2025-12-12, "early").

## What users love

- **The canvas and the wiring.** The single most-quoted thing: all of a project's
  services on one screen "with lines showing how they communicate", which "makes
  debugging simpler without requiring DevOps expertise" (devtoolpicks,
  2026-03-27). Reference variables are what make those lines real: a service
  names another service's variable and never an address.
- **Speed.** Deploys "in seconds rather than minutes" (TechTarget, 2026-01-22);
  "a delightful experience" on first use (Hacker News comment, January 2025, as
  quoted in search results; the page refused a direct fetch).
- **Price for small stacks.** A typical solo stack (web service, database, queue
  worker) at roughly $10–15/month against $21–34 on Render (devtoolpicks,
  2026-03-27); egress at $0.05/GB. Railway's plan prices have not changed since
  July 2023 while Render's rose in 2026 (bex.co, 2026-07-30).
- **Growth by word of mouth.** SiliconANGLE (2026-01-22) reports millions of users
  reached with "zero marketing".
- **Agent-first tooling**: a hosted MCP, an in-dashboard agent that fixes a red
  deploy by opening a pull request, and skills for coding agents. Latent Space
  titled its interview with the CEO "The Agent-Native Cloud".

### Why they stay, and what of it travels to a user's own servers

Railway's loyalty is built on four DX features, and none of them depends on
owning a data centre:

1. **A picture of the project** (the canvas) — pure panel work. Skifity has a
   read-only version already.
2. **Wiring by name** (reference variables, provided variables, private DNS) — a
   variable resolver plus the cluster's own Service DNS. Reproducible entirely.
3. **Whole-environment copies** (duplicate, sync, PR environments with their own
   databases) — namespaces are the natural unit; this is store and manifest work.
4. **Agent tooling** (MCP, a diagnosing agent) — Skifity already has an MCP server
   and cause/impact/fix errors; breadth is what is missing.

What does *not* travel is the usage-billed elasticity (a service bursting to
dozens of vCPU for a minute) and multi-region replicas; on a user's own servers,
capacity is what they bought.

## What users complain about

- **Reliability, repeatedly, with published causes.** Railway writes honest
  post-mortems, and the list for eight months is long:
  - 2025-10-28, 52 minutes: dashboard, API, CLI and GitHub deploys down after an
    index was added without `CONCURRENTLY` to a ~1-billion-row table; running
    deployments stayed up.
  - 2025-11-25, ~3.5 hours: the task queue fell over when GitHub's API slowed;
    all Free, Trial and Hobby deploys were paused.
  - 2025-12-16: cryptominers dropped through CVE-2025-55182 into unpatched
    customer Next.js apps caused "fleet-wide resource starvation" that degraded
    fewer than 10% of workloads and private networking for under 1% of traffic.
  - 2026-02-11, ~9 hours to full recovery: a new automated abuse system sent
    SIGTERM to legitimate workloads, databases included, on ~3% of the fleet.
  - 2026-02-18 to 21: sporadic outages from DDoS plus a Cloudflare outage.
  - **2026-05-19, ~8 hours: Google Cloud suspended Railway's production account**;
    because the network mesh read its routing tables from a control-plane API
    hosted only on GCP, "all Railway workloads across all regions were rendered
    unreachable", including those on AWS and Railway Metal. One customer:
    "we had to make emergency migration off to Azure yesterday ... too many mishaps
    and shortcomings for us to continue running a B2B enterprise app on their
    infrastructure" (InfoQ, 2026-05-30).
- **Support.** Free and Hobby get a forum; Pro "usually within 72 hours". Trustpilot
  (2.8/5 from 86 reviews, read 2026-09-30): "at least 1 major outage each month"
  (2026-07-02), "Account disabled from one day to next without warning"
  (2026-07-27), "no way to contact support properly, only through their forums"
  (2026-08-12).
- **Deploys that hang.** A competitor's analysis of ~5,000 Railway forum threads
  from February 2026 counted 1,908 platform complaints, 57% about build and
  deployment — "deployments hanging indefinitely with no error or alert"
  (stackandsails, 2026). This is a competitor-adjacent source and should be read
  as such; the forum threads on "Wait for CI" never triggering are first-party
  (station.railway.com).
- **Account and abuse enforcement.** The February 2026 incident plus Trustpilot's
  suspension reviews: an automated system can stop your production.
- **Churn in the product surface.** Config as Code, introduced as the way to keep
  settings in the repository, is deprecated with a hard stop on 2026-12-01; the
  open-source MCP server was archived for a hosted one. Users must migrate on
  Railway's schedule.
- **Volume limits**: no replicas with a volume, downtime on every redeploy of a
  service with a volume, one volume per service.
- **No autoscaling**: the docs' answer to load is "build an autoscaler".
- **No GPUs**, which analysts flag as a limit on AI workloads (TechTarget,
  2026-01-22).

## Security record

- **2026-10-20 to 22 — a breach of Railway's own systems**, in which environment
  variables of up to 1.85% of workspaces were decrypted. "Since 2026-09-30" has
  the detail.
- **2026-03-30 — other users' pages served from cache.** For 52 minutes a CDN
  configuration change cached GET responses on about 0.05% of domains that had
  the CDN switched off, so responses meant for one signed-in user, without a
  `Set-Cookie`, could be served to another (incident report, 2026-03-30). This
  file missed it on 2026-09-30.
- **No CVE or GitHub security advisory against Railway's platform or CLI was found**
  in the GitHub Advisory Database or NVD searches (2026-09-30). Searches for
  "railwayapp", "railway cli" and "nixpacks" returned dependency-level items only
  (for example a `rustls` advisory raised in a fork of the CLI) and one unrelated
  Nx advisory.
- **2025-12-16 — React2Shell (CVE-2025-55182) against tenants.** Not a Railway
  vulnerability, but a lesson in shared fate: attackers compromised customers'
  unpatched Next.js apps and ran cryptominers, and the load degraded other
  tenants and the private network. Railway blocked the binary, scanned for
  miners, blocked deploys of vulnerable Next.js versions and emailed affected
  users to rotate secrets.
- **March 2026 — abuse of the platform.** Huntress reported a device-code phishing
  campaign run from Railway infrastructure (as summarised by search results; the
  article refused a direct fetch). Abuse, not a compromise of Railway.
- **2026-02-11 — the abuse defences themselves** terminated legitimate workloads.
- Sealed variables exist, but values are readable by default and `railway run`
  / `railway shell` put them in a developer's local environment.

## Against Skifity

The Railway column was brought up to 2026-11-08. The Skifity column is as read on
2026-09-30 at `1ae3276`, and "Since 2026-09-30" lists the rows it no longer
describes.

| Capability | Railway | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy from Git, image, folder | GitHub, image, `railway up`, template | Git (GitHub, GitLab, Gitea), image, `skifity up` and a browser folder upload (`internal/api/webhook_handlers.go`, `internal/cli/up.go`, `internal/upload`). Written, never run on a cluster | Parity (Skifity supports more Git hosts) |
| Builder | Railpack, Dockerfile | Railpack by default, Nixpacks, Dockerfile, static front end (`internal/builder/job.go`, ADR-0008) | Parity — Skifity uses Railway's own builder |
| Pre-deploy / release command | Pre-deploy command, fails the deploy | Release command in a Job before any traffic, fails the deploy (`internal/deploy/run.go:124-170`) | Parity |
| Config change without rebuild | Staged changes, redeploy | Build fingerprint: a runtime variable is a rollout, not a build (ADR-0007, `internal/deploy/build.go`) | Skifity ahead |
| Rollback | Redeploy an old deployment, within retention | New deployment with the old image *and* its settings; refuses when the image was collected (`internal/deploy/deployer.go:483-535`) | Skifity ahead |
| Wait for CI, watch paths | Both | Neither: a push to the app's branch always deploys (`internal/api/webhook_handlers.go:152-176`) | Absent in Skifity |
| Custom domains and TLS | CNAME+TXT, automatic certs | cert-manager on first domain; free `sslip.io` address over HTTP (ADR-0015); optional wildcard domain for app subdomains (`internal/settings/settings.go:124`). Written | Parity |
| TCP proxy | Yes | No: NodePort and LoadBalancer Services are forbidden by the namespace quota (`internal/kube/namespace.go:113-116`) | Absent in Skifity |
| Edge firewall | Edge Rules (Aug 2026) | Per-app rules on IP, country, ASN, with and/or (`internal/edgerules`, `internal/guard`). Written, never through a live Traefik | Parity |
| Password or login in front of an app | No password (Edge Rules can challenge or block). Since 2026-10-08, Railway Authentication: a Railway login in front of a service or a whole environment, inherited by environments forked from it, Enterprise on $5,000 a month of committed spend (on 2026-09-30 this row said "None found") | HTTP basic auth through Traefik from a bcrypt hash in the app's namespace, inherited by previews, admin-only to change (`internal/api/password_handlers.go`, `internal/kube/password.go`, commit `7865bc8`); no login with panel accounts. Written | Skifity ahead on a password for everyone; behind on a login |
| Managed databases | Postgres (HA, PITR, PgBouncer, one-click major upgrades), MySQL (HA, PITR), Redis HA, Mongo (HA in Priority Boarding), ClickHouse | PostgreSQL via CloudNativePG (replicated), Redis and MySQL single instance only (`internal/dbsvc/manifests.go:107-121`); no PITR, no pooler | Behind |
| Object storage | Buckets | MinIO, Garage, SeaweedFS as templates (`internal/templates/catalogue/minio.yaml` etc.), nothing first-class | Behind |
| Volumes | Live resize, file browser, CoW backups daily/weekly/monthly | PVC, Recreate strategy, tar backups to S3 on a schedule, restore (`internal/api/api.go:309-316`, `internal/backup/volume.go`); no resize, no browser. Written | Behind on features, ahead on off-site copies |
| Horizontal autoscaling | None built in | HPA on CPU/memory, scale to zero through KEDA (`internal/kube/manifests.go:362`, `internal/kube/scaletozero.go`). Written | Skifity ahead (on paper) |
| Scale-safety check | None found | `check_scaling_readiness` finds SQLite on disk, in-memory sessions, cron assumptions (`internal/deploy/scaling.go`) | Skifity ahead |
| Sleep when idle | Serverless (5–10 min) | Scale to zero after five minutes, request held by KEDA's interceptor (`docs/concepts.md`, "Instances and scaling"). Written | Parity |
| Cron | Service-level schedule, ≥5 min apart | Several named schedules per app, five-field, as CronJobs with no overlap (`internal/api/api.go:295-298`, `internal/cron`) | Parity (Skifity finer-grained) |
| Logs | Explorer, query language, 3–90 day retention | Live stream over SSE plus the previous container's log, filtered in the browser (`web/src/components/app/logs-tab.tsx:30-90`); nothing retained beyond the container | Behind |
| Metrics | 30 days, per replica, deploy markers, dashboard | Current usage only, from metrics-server (`internal/kube/client.go:324-381`); no history, no charts | Behind |
| Alerts on thresholds | Monitors on CPU/RAM/disk/egress | Seven events, none a threshold (`internal/notify/notify.go:41-47`) | Absent in Skifity |
| Tracing | Built-in OTel collector, in preview (dashboard behind Priority Boarding; `railway trace` for everyone since 2026-11-05) | None | Absent in Skifity |
| Secrets | Sealing is opt-in | Every secret write-only on every surface, sealed per value with context (`internal/crypto`, `internal/api/apps_handlers.go`) | Skifity ahead |
| SSO, roles, audit | SSO, RBAC, environment RBAC and access groups (Enterprise), audit logs | OIDC with PKCE for everyone, three roles, audit log (`internal/auth/oidc.go`, `internal/store/models.go:5-20`, `internal/api/api.go:519`); no per-environment protection | Parity; behind on environment protection |
| Environments | Persistent, duplicate, sync | Create empty only (`internal/api/teams_handlers.go:429-468`); no duplicate, no sync | Behind |
| PR environments | Whole base environment, focused, bots | One app copied per PR, variables resealed, fork PRs get no secrets; since `1ae3276` an empty database of its own per linked database (same engine and version, one instance, 1 GB), none for forks (`internal/api/webhook_handlers.go:183-445`, `previewDatabases` at `:373`); reaped after 7 idle days (`internal/watch/watch.go:136-190`); commit status and one PR comment with the URL (`internal/deploy/gitreport.go`). Only the pull request's own app is copied, and there is no seed step. Written | Behind (narrowing) |
| Templates | 1,800+, community-authored, kickbacks | 282 curated files, every image a verified version (`internal/templates/catalogue`, `internal/templates/templates.go`) | Behind on count and authorship |
| Canvas | Interactive, staged, groups, lines | Read-only grid per environment, app→database arrows only (`internal/api/integrations_handlers.go:367-440`, `web/src/pages/project-detail.tsx:442-545`) | Behind (by design on dragging) |
| Shared / reference variables | Shared, reference, provided variables, autocomplete | Project-wide shared variables (`internal/api/api.go:255-257`, `internal/deploy/deployer.go:728-754`); database connection injected by link; no references, no provided service addresses | Behind |
| CLI | ~25 commands incl. ssh, connect, dev, postgres | 17 commands (`internal/cli/commands.go:34-71`); no db, domain or environment commands | Behind |
| MCP and agents | Hosted MCP, Railway Agent, skills, cloud agents | Local stdio MCP with 15 tools, no database/domain/environment tools (`internal/mcpserver/server.go:314-391`); every error carries cause/impact/fix (`internal/errdoc`) | Behind on breadth |
| IaC | `.railway/railway.ts`, plan/apply, GitHub Action | None; `skifity.toml` only links a folder to an app (`internal/cli/project.go:25-31`) | Absent in Skifity |
| Notifications | Email, in-app, webhooks | Telegram, Discord, webhook, email, plus plugin-provided kinds (`internal/notify`, ADR-0021). Written | Parity |
| Private networking | WireGuard mesh, `*.railway.internal` | One namespace per environment, apps reach each other by Service name, default-deny between environments, WireGuard between nodes (`internal/kube/namespace.go:127-217`, ADR-0003); addresses not shown in the panel. Written | Parity on mechanism, behind on DX |
| Servers and regions | Railway's regions, no servers | Your servers, one k3s cluster, added over SSH (`internal/provision`); multi-cluster out of scope (`docs/roadmap.md`) | Different model |
| Blast radius of the vendor | A GCP suspension took every region down; a breach of Railway's systems decrypted variables of up to 1.85% of workspaces, about 100,000, and stopped every customer's builds for most of two days (2026-10-20 to 22) | Apps are served by Kubernetes; the panel is not in the request path (checklist item 14). Written | Skifity ahead (structurally) |
| Team and cost | Unlimited seats on Pro, usage billing | Unlimited users, no billing (`docs/roadmap.md`, "A hosted version") | Different model |
| Onboarding | Anonymous deploys, agent, suggested variables | Detection of framework *and* needs (databases, `.env.example`) before the first deploy (`internal/builder/needs.go`); browser folder upload; five languages | Parity |

## Gaps worth closing in Skifity

### No P0

The one P0 this research found was fixed while it was being written. A preview
copied every variable of the app it previewed, and a database link *is* a
variable (`internal/dbsvc/manager.go:341-352`) naming the production database's
fully-qualified Service (`internal/dbsvc/manifests.go:144`), so every same-repository
pull request was handed production's `DATABASE_URL`, with only an unexercised
NetworkPolicy in the way. Commit `1ae3276` (2026-09-30) stops copying link
variables and gives each preview an empty database of its own per linked one
(`internal/api/webhook_handlers.go:373`, tested in `internal/api/previewdb_test.go`).
Written, never run on a cluster. What remains of Railway's lead on previews is
below.

### P1 — Previews of the whole stack, with a seed step

**What.** Two things Skifity's previews still lack. First, the rest of the stack:
when the pull request's app depends on other apps in the environment (by
reference, see the next item, or by a declared link), bring those into the
preview too — reusing the image production already runs rather than rebuilding
them, which is Railway's "focused PR environments" idea and cheap on a user's own
servers. Second, a **seed command** run once after a preview's first successful
deploy (Heroku's `postdeploy`, Render's `initialDeployHook`), because the new
database is empty and a release command only migrates. Add Render's two title
flags: `[skip preview]` and, when previews are manual, `[preview]`.

**Evidence.** Railway's PR environments copy the base environment and deploy only
what the pull request touched; Render builds "a fresh copy of your production
environment (including services, datastores, and environment groups)" and seeds it
with `initialDeployHook`; Heroku review apps seed with `postdeploy`. Skifity copies
exactly one app (`internal/api/webhook_handlers.go:250-300`), so a web app whose
API is a second app previews against nothing.

**Fit.** `deployPreview` walks the app's dependencies; a deployment with the source
app's current image skips the build (`internal/deploy/deployer.go:198-212`);
`seed_command` on `store.App` beside `ReleaseCommand` runs through the existing
run-Job path once and reports into the PR comment (`internal/deploy/gitreport.go`).

**Size.** M. **Without a cluster:** the dependency walk, flags and seed bookkeeping
are unit-testable with the fake clientset; the seed actually running needs a
cluster.

### P1 — Reference variables and provided service addresses

**What.** Let a variable's value reference another app's or database's value:
`${{ api.PRIVATE_URL }}`, `${{ db.DATABASE_URL }}`, `${{ shared.SENTRY_DSN }}`,
resolved at deploy time; and have the panel provide `SKIFITY_PRIVATE_HOST`,
`SKIFITY_PRIVATE_URL` and `SKIFITY_PUBLIC_URL` for every app, so wiring is by name.
Draw the resulting app→app edges on the canvas.

**Evidence.** Reference variables are the other half of what reviewers praise in
Railway's canvas; the lines are the references. Skifity's apps can already reach
each other by Service name inside an environment, but nothing in the panel says
what that name is (no internal address appears in `web/src/locales/en.json` or
any page), and the template engine's wiring (`internal/templates/templates.go`)
is not available to hand-made apps.

**Fit.** `runtimeVariables` in `internal/deploy/deployer.go:728-754` already merges
shared, own and linked values; add a resolver pass with cycle detection and a
typed errdoc entry for an unresolved reference. Secret references resolve
server-side and never leave the panel. Variables editor: autocomplete from the
environment's apps and databases. A changed reference target must trigger a
rollout of the apps that reference it, not a rebuild (ADR-0007 holds because
references are runtime-only; a build-time reference should be refused).

**Size.** M. **Without a cluster:** yes — resolver, cycles, fingerprint effects
and the editor are unit and Playwright work.

### P1 — Metrics history and threshold alerts

**What.** Keep a sample of each app's CPU and memory and each volume's and
server's disk use every minute, downsampled (1-minute for 24 hours, 10-minute for
30 days), draw it on the app page with deployment markers, and add alert rules:
"memory above 90% of the limit for 5 minutes", "volume above 80%", "server disk
above 85%", delivered through the existing channels.

**Evidence.** Railway keeps 30 days with deploy markers and sells Monitors on Pro;
Render notifies when "disk usage exceeding 80%"; Heroku keeps 7 days with p95
response time. Skifity shows only the instant value from metrics-server
(`internal/kube/client.go:324-381`) and has no threshold event
(`internal/notify/notify.go:41-47`). A full disk is the failure a self-hosted user
meets first, and nothing warns before it.

**Fit.** A sampler on the existing minute tick (`internal/serverapp`), a samples
table with the same pruning discipline as Phase 11.2 (`internal/store/retention.go`),
alert rules evaluated in `internal/watch`, new `notify` events, charts built from
shadcn primitives (no second component library). Volume usage needs the kubelet
stats summary API, which metrics-server does not provide.

**Size.** M–L. **Without a cluster:** sampling store, downsampling, rule
evaluation with hysteresis (the flapping problem noted in Phase 72) and charts
from fixture data are all testable; real numbers need a cluster.

### P1 — Duplicate an environment, and sync between two

**What.** "New environment from Production": copy apps (settings and resealed
variables, no domains), recreate linked databases empty, keep scheduled commands
off until confirmed. Later, "Compare with Production" shows what differs per app
(image, variables by name, scaling, domains) and applies chosen differences.

**Evidence.** Railway's duplicate and sync; Render's projects keep environments
but do not clone, and Railway's is the praised one. Skifity creates an
environment empty (`internal/api/teams_handlers.go:429-468`), so a staging
environment is rebuilt by hand.

**Fit.** Reuses the preview-copy code (`copyPreviewVariables` and
`previewDatabases` in `internal/api/webhook_handlers.go`), so previews and
duplicates share one copy routine. API `POST /api/projects/{id}/environments` with
`from_environment`; a diff endpoint; a dialog on the project page.

**Size.** M. **Without a cluster:** yes for the copy and diff; applying needs one.

### P1 — Infrastructure as code

Railway replaced Config as Code with a TypeScript IaC SDK that plans and applies
the whole project; Render's Blueprints do the same in YAML. Skifity has nothing
declarative. The full proposal, with the reasons YAML beats a TypeScript SDK for a
Go binary, is in `render.md` ("P1 — A project file"). Size L; no cluster needed.

### P2 — Canvas that shows the whole environment

**What.** Add app→app edges (from references), volumes, scheduled commands,
previews and each node's instance count to the existing canvas, and a "what
changed since the last deploy" badge. Keep it non-draggable, as the component's
own comment decides (`web/src/pages/project-detail.tsx:442-448`).

**Evidence.** The canvas is Railway's most-praised feature; Skifity's shows only
app→database links. **Fit:** `handleProjectCanvas` plus the page. **Size:** S.
**Without a cluster:** yes (Playwright).

### P2 — Watch paths and wait for CI

**What.** Per app, a list of path globs a push must touch to deploy (default: the
root directory), and an optional "wait for the repository's checks" that holds
a push-triggered deploy until the commit's check suites pass.

**Evidence.** Both are Railway features with their own forum threads; monorepos
with several apps currently rebuild every app on every push. **Fit:**
`internal/gitsrc/webhook.go` already parses push payloads (GitHub, GitLab and
Gitea list changed files per commit); a `waiting` deployment status polled by the
minute tick. **Size:** S (paths) + M (checks). **Without a cluster:** yes.

### P2 — MCP and CLI breadth

**What.** MCP tools for databases (list, create, link, back up), domains,
environments and scheduled commands; CLI `skifity db` (list, create, `connect`
through a port-forward), `skifity domains`, `skifity env pull` for non-secret
values. Optionally serve MCP over HTTP from the panel itself with token auth, so
an assistant needs no local binary.

**Evidence.** Railway's hosted MCP and agent; Skifity's MCP has 15 tools and none
touch databases or domains (`internal/mcpserver/server.go:314-391`). **Fit:** the
API already has every endpoint; this is surface. **Size:** M. **Without a
cluster:** yes (the MCP and CLI tests run against the API).

### P2 — Refuse to build known-exploited framework versions

**What.** Detection already reads `package.json` and lockfiles
(`internal/builder/detect.go`, `internal/builder/needs.go`); add a short,
curated list of actively exploited versions (starting with CVE-2025-55182 in
Next.js/React Server Components) that turns into an amber warning, or a refusal
with an override, before the build.

**Evidence.** Railway blocked such deploys during React2Shell; on a user's own
servers a cryptominer costs them their whole server, not a noisy neighbour.
**Size:** S. **Without a cluster:** yes.

### P2 — A TCP entry point for a database

**What.** A per-database "reach it from outside" switch that opens a TCP route
through Traefik (`IngressRouteTCP` with TLS passthrough/SNI, or an allocated port)
guarded by the firewall's IP rules, off by default.

**Evidence.** Railway's TCP proxy is how users open a database in a GUI client;
Skifity forbids NodePorts on purpose (`internal/kube/namespace.go:113-116`).
**Size:** M. **Needs a cluster** to verify.

## Things to deliberately not copy

- **A control plane that the data plane depends on.** Railway's worst outage was
  a routing mesh that could not serve traffic without an API hosted at one
  vendor. Skifity's apps are served by Kubernetes with the panel outside the
  request path (checklist item 14, ADR-0013). Keep that property explicit in
  every new feature: metrics sampling, alerts, reference resolution and preview
  databases must all fail *closed and quiet* with the apps still serving.
- **Staged changes for everything.** Railway makes a variable edit a change set
  to review and deploy. Skifity's promise is the opposite — a runtime change is a
  rollout in seconds, and rollback restores it (ADR-0007). Staging belongs only to
  bulk operations such as syncing two environments.
- **Opt-in sealing and `railway run` on a laptop.** Railway returns values unless
  sealed and exports them to local shells. Skifity's secrets are write-only
  everywhere; a local-run feature would break that promise. `skifity env pull`
  should carry non-secret values only.
- **A free-form draggable canvas.** Railway's canvas is loved on a large screen;
  Skifity's is readable on a phone by design. Add information, not dragging.
- **Kickbacks.** They exist because Railway bills usage. Skifity never phones home
  and has no billing, so it cannot count installs or pay authors; credit authors
  in the catalogue and review their pull requests quickly instead.
- **Anonymous provisioning.** On a hosted platform it is onboarding; on a panel
  somebody exposes to the internet it is an open door.
- **Replacing a configuration format on the vendor's schedule.** Railway is
  forcing every user off `railway.json` by 2026-12-01. Whatever format Skifity
  adopts should be versioned and read forever.
- **Automated enforcement that can kill production.** Railway's abuse system
  terminated legitimate databases. A panel run by its owner has no reason to
  stop that owner's workloads on a heuristic.

## Since 2026-09-30

Read on 2026-11-08. The changelog's feed, the blog, the docs, the pricing page,
two incident pages on status.railway.com and Hacker News (Algolia API) were read;
GitHub only through its search API (repository endpoints were refused from this
sandbox), and Trustpilot refused the request. A web search was not available.

### Product

- **Missed on 2026-09-30, from the three weeks before it.** One-click Postgres
  major-version upgrades from 14–17 to any newer major, with a compatibility
  report, a backup before and after, a few minutes of downtime and a limited
  rollback window (2026-09-11); email forwarding for domains bought through
  Railway (2026-09-11). **Sandboxes** generally available: Linux VMs for agents,
  tests or untrusted code, with checkpoints, forks and the environment's private
  network, managed from a TypeScript SDK, the CLI or the API (2026-09-17). **Free
  VMs without an account**: `ssh railway.new` gives a 2 vCPU, 2 GB VM with coding
  agents installed and a preview URL, 60 minutes to build and 24 hours to claim
  it (2026-09-25).
- **Databases.** MongoDB HA, a replica set behind HAProxy, in Priority Boarding;
  MySQL point-in-time recovery generally available, a 7-day archive restored into
  a separate service (both 2026-10-01). Regenerating a database's password or a
  bucket's credentials redeploys every service that references them through
  Railway variables, at their current deployment and without newer commits
  (2026-10-30); for HA Postgres, MySQL and Redis the change is coordinated across
  the cluster's members and proxies, with the old password restored if it fails
  before it is committed (2026-11-05).
- **Railway Authentication** (blog 2026-10-08, changelog 2026-10-09). An
  identity-aware proxy puts a service's public domains, or every service of an
  environment by default, behind a Railway login that only the team's members
  pass, and environments forked from a protected one inherit it. Railway names
  staging previews and internal tools as the use, and it needs the $5,000 a month
  committed-spend tier.
- **Webhooks** carry custom headers, stored encrypted and not shown again
  (2026-10-01).
- **Activity feed** filtered by service and by kind, with image auto-updates,
  variable changes and volume operations now listed (2026-10-16); manually stopped
  deployments are recorded with who stopped them (2026-11-05).
- **Automatic security patches** (2026-10-30): services and functions affected by
  a known CVE are redeployed on a patched image, and workspace admins are told in
  the app and by email.
- **Workspace tokens** (2026-11-05): admins see every token of the workspace, who
  made it and when, and revoke any; creation and revocation are in the audit log.
  Before, only a token's creator could see it.
- **CLI and IaC.** `railway cron list` (recent runs with status, start, duration,
  the schedule and the next run) and `railway cron run` (2026-10-16); `railway
  trace` (2026-11-05); `railway redeploy --commit` for one deploy of one commit
  (merged 2026-10-15); a guided move from `railway.json` to `.railway/railway.ts`
  with `migrate`, `cutover`, `status` and `undo` (2026-10-16); CDN settings in the
  IaC file; `--environment` on `config plan`, `apply` and `pull`. The docs still
  give 2026-12-01 as the day Config as Code stops being read. On 2026-11-08 the
  CLI has 629 stars and 166 open issues, Railpack 1,265 and 86, Nixpacks 3,557 and
  97 (GitHub search API); 36 pull requests opened in the CLI since 2026-09-30 were
  merged, most of them for IaC and the MCP.
- **Elsewhere.** OpenRouter keys for Railway Agent in beta (2026-10-09); a
  ChatGPT subscription as its model (2026-09-17, Priority Boarding).

### Pricing

- The per-second rates are unchanged on 2026-11-08. The page now also prices
  Sandboxes and VMs, at $0.00001929 per vCPU-second and per GB-second while
  running (about $50 per vCPU-month or GB-month), and lists what committed monthly
  spend unlocks: $1,000 for HIPAA agreements; $2,000 for log history, RBAC, SSO and
  audit logs; $5,000 for enterprise support and higher limits (and, by Railway's
  blog, Railway Authentication); $10,000 for dedicated VMs. Whether the tiers
  were on the page on 2026-09-30 was not recorded then.

### Incidents and security

- **2026-09-30, 07:35–07:40 UTC.** Every domain on Railway answered 404 to new
  connections in every region, and private-network lookups failed, because a new
  version of the routing control plane rolled out before the schema migration it
  needed; the origin proxies treated a failed lookup as "this domain does not
  exist" instead of keeping the last route they knew. The change was groundwork
  for Railway Authentication.
- **2026-10-20 to 22: a breach.** From the preliminary report (2026-10-22, updated
  2026-10-26): an attacker after AI providers' API keys escalated privileges in
  the build infrastructure, pulled encrypted environment variables, then reached
  credentials for Railway's CI that decrypted artifacts holding more access, and
  decrypted a subset of customers' variables. Railway puts it at "at most 1.85%
  of all workspaces", about 100,000; it gave GitHub, GitLab, Anthropic, OpenAI,
  OpenRouter, Stripe and others the keys it held so they could revoke them (for
  some services all of them), revoked every Railway token, re-rolled the GitHub
  App's installations, and asked everybody who signed up before 2026-10-21 to
  rotate every secret. Running deployments stayed up; builds were stopped twice,
  and the dashboard, API, CLI and support were down at times until 22:49 UTC on
  2026-10-22. Builders moved from a multi-tenant design to single-tenant ones,
  ahead of plan. New signups were still paused on 2026-10-26. The final report had
  not been published on 2026-11-08.
- What users said, in the Hacker News thread on the disclosure (603 points, 344
  comments): keys "created just for railway were running up usage" hours before
  the email, and "How is it possible to rotate keys if you disable deployments?
  You're basically telling us to take our services offline" — a new value only
  reached a running service through a deploy, and deploys were stopped.
- **From 2026-11-04**, networks and security vendors blocked Railway's addresses
  after a third party sent scanning traffic with forged Railway source addresses;
  Railway found no tie to the breach, and the partial outage was still open on
  2026-11-08.
- **2026-11-03.** Primcloud's founder accused a Railway product engineer of trying
  to break into Primcloud; Railway's CEO apologised in public, said the activity
  looked like an agent "off the rails", and said Railway would ban external
  testing that is not done by hand (RuntimeWire, from posts on X; what was done
  and how far it got is not public).
- The CLI's tracker has three RUSTSEC advisories against its `zip` dependency
  open since 2026-10-17. OSV lists no advisory against Railway's own packages
  (2026-11-08).

### What users say

- The CLI's tracker since 2026-09-30, on IaC: `config plan` refused with a project
  token (#1270, #1333, #1334); `config apply` reports success but leaves build
  settings the file no longer declares (#1327); `--confirm-destructive` skips a
  planned bucket deletion and exits 0 (#1266); `config migrate` quietly turns
  Dockerfile builds into Railpack (#1290); `postgres()` stores its password as an
  unrendered template (#1304).
- And on secrets: `railway up` uploads `.gitignore`d files such as `.env` when the
  folder is not a Git repository (#1331); `railway connect --tunnel-only` prints
  the database password unless asked not to (#1318); `railway variable list --kv`
  prints values unquoted, so sourcing the output can run them (#1335).
- "Show HN: Stackdome – An open source self-hostable Railway alternative on K8s"
  (2026-10-22, 10 points) is a new project in Skifity's own category, not
  researched here.

### For Skifity

Checked against the code on 2026-11-08.

- **Rows of "Against Skifity" that no longer describe Skifity.** Watch paths
  (Phase 96; waiting for CI is still absent). TCP proxy: public TCP and UDP ports
  (`62d2adb`). Edge firewall: rate limits too (`internal/edgerules`). Managed
  databases: nine engines (`f4f9d09`), still one instance for all but
  PostgreSQL, and no PITR, no pooler and no major-version upgrade (the database
  routes in `internal/api/api.go` stop at resize, stop, start, password, import,
  backup and restore). Metrics and threshold alerts (Phases 95 and 99). Logs: log
  drains (`3731416`), still nothing kept in the panel. Environments: cloning
  (`c09ffc9`), no sync. PR environments: optionally the whole environment, and a
  seed command (Phase 107). Templates: 374 files. CLI: `db` with `connect`,
  `password`, `import` and the rest (Phase 105, `6920a57`), `deploy --commit`.
  MCP: served by the panel (Phase 87), 46 tools, databases, domains, ports and
  backups among them. IaC: `skifity.yaml` with plan and apply (Phase 108). Notifications: Slack,
  Mattermost, ntfy, Pushover, Teams and Gotify too (Phase 88, `faeecc1`). Reference
  variables between apps and the canvas's app-to-app lines are still absent.
- **Already there, against this period's releases.** Changing a database's
  password gives every linked app the new connection string in a rollout and keeps
  the old password working until they have it (`docs/databases.md`, "Changing the
  password"). Each build runs its own rootless BuildKit, and only its token may
  write its app's repository (ADR-0026): the single-tenant builder Railway moved
  to under pressure. A build Job never receives the panel's keyring. A runtime
  variable is a rollout, not a build (ADR-0007), so a key can be rotated while
  builds are broken — what Railway's users could not do. `skifity up` leaves out
  every `.env` and what `.gitignore` leaves out (`internal/upload/upload.go`), the
  bug in #1331. The guard keeps its previous rules when a new rules file cannot
  be read (`internal/guard/guard.go`), the opposite of Railway's 2026-09-30
  failure.
- **What the breach says about the category.** One hosted platform holding every
  customer's keys made a single intrusion worth about 100,000 workspaces' secrets
  and stopped everybody's deploys. A self-hosted panel holds one organisation's,
  sealed with context by a keyring only that panel has. That is the argument for
  Skifity's model, and only as strong as the panel's own security.
- **The same as Railway: `skifity db connect` prints the password.** It shows the
  connection URL with the password in it (`connectDatabase` in
  `internal/cli/dbconnect.go`; `docs/cli.md` warns about CI logs), while
  `skifity db password` prints one only with `--show-password`. A Railway user
  filed the same behaviour as a bug (#1318). Print the URL with the password
  masked, and the full one behind `--show-password`.
- **New gap — what happened to a scheduled command** (as `dokku.md` found).
  Railway shows each cron service's recent runs with status, start, duration and
  next run, in the dashboard and now `railway cron list`. Skifity's `AppJob`
  (`web/src/lib/types.ts`) has no last run, the `/jobs` routes have no list of
  runs (`internal/api/api.go`), and no event fires when a run fails
  (`internal/notify/notify.go`). Size M; no cluster for the code.
- **New gap — a login in front of an app.** Railway Authentication, and Netlify's
  private projects (`netlify.md`, "Since 2026-09-30", which has the proposal).
  Railway sells it at $5,000 a month; Skifity's should be free.
- **New gap — team tokens an admin can see.** Tokens are listed and revoked only
  by their owner (`/me/tokens` in `internal/api/api.go`, `ListAPITokens(userID)`
  in `internal/store/users.go`), so an admin can stop a member's token only by
  removing the member. A team's tokens, with owner, scopes and last use, revocable
  by an owner or admin and audited. Size S; no cluster.
- **New gap — headers on a webhook channel.** The webhook kind sends one fixed
  `X-Skifity-Secret` (`sendWebhook` in `internal/notify/notify.go`), so a receiver
  that wants `Authorization: Bearer` needs a proxy in between; log drains already
  take a named header (`internal/logdrain/logdrain.go`). The same header option
  for webhooks, and an HMAC signature with a timestamp rather than the secret
  itself. Size S; no cluster.
- **New gap — filters on the activity page.** The audit API already filters by
  action and target (`handleListAudit` in `internal/api/teams_handlers.go`); the
  page (`web/src/pages/activity.tsx`) shows the last hundred events with no
  filter. Size S; no cluster.
- **New gap — a database's next major version.** Railway upgrades Postgres in
  place with a compatibility report, a backup either side and a rollback window.
  In Skifity a new major means a new database, a dump and `import` by hand.
  PostgreSQL could use CloudNativePG's own offline major upgrade; the other
  engines would go through a dump restored into a new database at the new
  version, then a relink. Size L; needs a cluster.
- **New gap — applying a fix that the catalogue already has.** Railway now
  patches images with known CVEs on its own. Skifity scans every running image
  nightly and says when a new critical appears (`internal/deploy/scan.go`), and
  offers a template's update with a backup first (Phase 102), but never applies
  one. An opt-in per app: "update automatically, after a backup, when the update
  fixes a critical vulnerability". Size M; the rule without a cluster, the update
  with one.
- **Lessons for `skifity.yaml`, from Railway's tracker.** `apply` must say what it
  did not converge rather than report success, a destructive step that was
  skipped must exit non-zero, and a migration must never change the builder
  silently. Worth a test each in `internal/blueprint`.

## Sources

All read 2026-09-30 unless marked.

- https://railway.com/pricing
- https://railway.com/features
- https://docs.railway.com/guides/variables
- https://docs.railway.com/reference/environments
- https://docs.railway.com/guides/environments
- https://docs.railway.com/reference/volumes
- https://docs.railway.com/reference/backups
- https://docs.railway.com/reference/cron-jobs
- https://docs.railway.com/reference/private-networking
- https://docs.railway.com/guides/public-networking
- https://docs.railway.com/reference/deployments
- https://docs.railway.com/deployments/pre-deploy-command (via search results)
- https://docs.railway.com/deployments/github-autodeploys (via search results)
- https://docs.railway.com/guides/autoscale-horizontally
- https://docs.railway.com/deployments/scaling and https://docs.railway.com/deployments/serverless (via search results)
- https://docs.railway.com/guides/observability
- https://docs.railway.com/guides/logs
- https://docs.railway.com/guides/metrics
- https://docs.railway.com/reference/templates
- https://docs.railway.com/templates/kickbacks
- https://docs.railway.com/config-as-code
- https://docs.railway.com/infrastructure-as-code
- https://docs.railway.com/ai/mcp-server
- https://docs.railway.com/cli
- https://docs.railway.com/reference/support
- https://docs.railway.com/overview/the-basics
- https://docs.railway.com/deployments/regions (via search results)
- https://docs.railway.com/reference/functions (via search results)
- https://docs.railway.com/databases/database-view (via search results)
- https://railway.com/changelog/2025-12-12-audit-logs
- https://railway.com/changelog/2026-04-17-remote-mcp (via search results)
- https://railway.com/changelog/2026-04-24-railway-agent
- https://railway.com/changelog/2026-08-14-access-groups
- https://railway.com/changelog/2026-08-20-railway-anon
- https://railway.com/changelog/2026-09-04-postgres-in-the-railway-cli
- https://railway.com/changelog/2026-06-19-railway-mobile-app-for-ios (via search results)
- https://blog.railway.com/p/1M-paid-to-developers-who-built-railway-templates (2025-12-05)
- https://blog.railway.com/p/incident-report-oct-28th-2025
- https://blog.railway.com/p/incident-report-november-25-2025
- https://blog.railway.com/p/incident-report-december-16-2025
- https://blog.railway.com/p/incident-report-february-11-2026
- https://blog.railway.com/p/incident-report-february-19-2026 (via search results)
- https://blog.railway.com/p/incident-report-may-19-2026-gcp-account-outage
- https://www.infoq.com/news/2026/05/railway-gcp-account-outage/ (2026-05-30)
- https://news.ycombinator.com/item?id=48201484 (via search results)
- https://siliconangle.com/2026/01/22/intelligent-cloud-infrastructure-startup-railway-gets-100m-simplify-application-deployment/
- https://www.techtarget.com/searchcloudcomputing/news/366637659/Upstart-cloud-provider-Railway-turns-heads-with-speed (2026-01-22)
- https://www.axios.com/pro/enterprise-software-deals/2026/01/22/software-deployment-railway-100-million (via search results)
- https://www.latent.space/p/railway (via search results)
- https://devtoolpicks.com/blog/railway-vs-render-vs-fly-io-solo-developers-2026 (2026-03-27)
- https://bex.co/blog/2026/07/30/railway-flat-pricing-vs-render-repricing (via search results)
- https://stackandsails.substack.com/p/is-railway-production-ready-in-2026 (via search results; competitor-adjacent)
- https://www.trustpilot.com/review/railway.com
- https://news.ycombinator.com/item?id=42743953 (quoted via search results; direct fetch refused)
- https://support.huntress.io/hc/en-us/articles/50042538711699-2026-March-Railway-Exploit (via search results; direct fetch refused)
- https://github.com/advisories/GHSA-cxm3-wv7p-598c (checked: unrelated to Railway)
- GitHub API via repository search, `org:railwayapp` (stars and open issues for cli, railpack, nixpacks, railway-mcp-server, railway-skills)
- Skifity: the files cited in the table, at commit `1ae3276`

Read 2026-11-08, for "Since 2026-09-30":

- https://railway.com/changelog/rss.xml (entries from 2026-09-11 to 2026-11-05), in particular:
  - https://railway.com/changelog/2026-09-11-one-click-postgres-major-version-upgrades
  - https://railway.com/changelog/2026-09-18-railway-sandboxes
  - https://railway.com/changelog/2026-09-25-free-vms-without-an-account
  - https://railway.com/changelog/2026-10-02-mongodb-high-availability
  - https://railway.com/changelog/railway-ios-auth-openrouter (2026-10-09)
  - https://railway.com/changelog/2026-10-16-activity-feed-filters
  - https://railway.com/changelog/2026-10-23-october-21-security-incident
  - https://railway.com/changelog/2026-10-30-automatic-security-updates
  - https://railway.com/changelog/2026-11-06-workspace-token-management
- https://blog.railway.com/ (index), and:
  - https://blog.railway.com/p/incident-report-sept-30-2026-routing-disruption
  - https://blog.railway.com/p/railway-authentication-identity-aware-proxy (2026-10-08)
  - https://blog.railway.com/p/security-disclosure-wednesday-october-21st
  - https://blog.railway.com/p/preliminary-report-october-security-incident (2026-10-22, updated 2026-10-26)
  - https://blog.railway.com/p/incident-report-march-30-2026-authenticated-user-data-cached
  - https://blog.railway.com/p/railway-cdn (2026-06-04)
- https://status.railway.com/incident/QH8L8B3E (the platform outage of 2026-10-21 to 23) and https://status.railway.com/incident/QBCLHG1V (network blocks, from 2026-11-04)
- https://railway.com/pricing
- https://docs.railway.com/config-as-code and https://docs.railway.com/infrastructure-as-code (the 2026-12-01 cutoff)
- https://docs.railway.com/networking/cdn
- https://docs.railway.com/guides/autoscale-horizontally (still no built-in autoscaler)
- GitHub search API: `org:railwayapp` repositories (stars, open issues); `repo:railwayapp/cli` issues and merged pull requests created since 2026-09-30 (#1266, #1270, #1289, #1290, #1304, #1318, #1327, #1331, #1333, #1334, #1335, and the RUSTSEC issues #1301–#1303)
- https://api.osv.dev/v1/query for `railwayapp` (crates.io), `@railway/cli` and `railway` (npm), and Railpack (Go): no advisories
- https://hn.algolia.com/api/v1/search?query=railway (stories since 2026-09-30) and https://hn.algolia.com/api/v1/items/50188228 (the disclosure thread)
- https://runtimewire.com/article/railway-cofounder-apologizes-after-employee-accused-of-trying-to-breach-primclou (2026-11-02, US Central time; from posts on X)
- Not reachable from this sandbox: GitHub's repository endpoints, Trustpilot, Security Boulevard, and web search
- Skifity: the files named under "Since 2026-09-30", read in this repository on 2026-11-08
