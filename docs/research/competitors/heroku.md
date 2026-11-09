# Heroku

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Researched 2026-09-30 against live sources. Skifity's side of every comparison was
checked in the code at commit `1ae3276` (2026-09-30). Uncommitted work seen in the
working tree that day is not counted.

Heroku is the platform-as-a-service that defined the category: `git push`, a
buildpack detects the language, a **Procfile** names the processes, **config vars**
hold the settings, **add-ons** attach databases and services, and a **release
phase** runs migrations before new code takes traffic. It was built from 2007 by
James Lindenbaum, Adam Wiggins and Orion Henry and bought by Salesforce on
2010-12-08 for $212 million in cash (Wikipedia). **Architecture:** two generations
side by side. **Cedar** (since 2011) runs dynos on Heroku's own dyno manager with
the Logplex log router and classic buildpacks, in the shared Common Runtime and in
Private Spaces. **Fir** (generally available 2025-04-02) is "powered by AWS
services like EKS, Intel/AMD, Graviton, and Global Accelerator" — Kubernetes
underneath, Cloud Native Buildpacks only, OCI images and OpenTelemetry — and is
available **only in Fir Private Spaces**; the Common Runtime is "To Be Added"
(Dev Center, "Heroku Generations"). Its own state is not documented publicly.
**Direction:** on **2026-02-06** Heroku announced it is "transitioning to a
sustaining engineering model focused on stability, security, reliability, and
support"; "Enterprise Account contracts will no longer be offered to new
customers", while credit-card customers see "no changes to pricing, billing, or
service", and investment moves to "enterprise-grade AI" (Heroku blog, Nitin T.
Bhat). Small platform changes continue — the Heroku-26 stack on Ubuntu 26.04 went
GA on 2026-05-20 with support to April 2031, trusted IPs for Fir Private Spaces on
2026-06-26 — but Fir's missing features (autoscaling, Heroku CI, internal routing,
the Common Runtime) are not expected: "We're waiting for general release but
sounds like that's just not going to happen now" (Hacker News via DevClass,
2026-02-09). **Licence:** proprietary platform; buildpacks and the CLI are public
(`heroku/cli`: 889 stars, 49 open issues; `heroku/heroku-buildpack-nodejs`: 1,347
stars; `heroku/logplex` and `heroku/heroku-pg-extras` archived — GitHub API,
2026-09-30). **Pricing (read 2026-09-30):** Cedar dynos Eco $5 (1,000 shared
hours, sleeps after 30 minutes), Basic $7, Standard-1X $25 (0.5 GB), Standard-2X
$50 (1 GB), Performance-M $250 (2.5 GB), Performance-L $500 (14 GB), up to
Performance-2XL $1,500; Private-S $125 to Private-2XL $1,500 and Shield 20% more;
Fir dynos from $25 (1 CPU, 0.5 GB) — up to $1,000 for 16 CPU/32 GB in the rows
read — all requiring a Private Space, whose own price is not on the pricing page.
Postgres Essential-0 $5 (1 GB), Essential-1 $9, Essential-2 $20, then Standard,
Premium, Private and Shield from $50 to $34,000/month; Postgres Advanced (GA
2026-10-06) from $150 (2 vCPU, 4 GB) to $40,000/month on the Common Runtime, 20%
more in Private Spaces and 40% more in Shield, with 100 GB-month of storage
included; Key-Value Store from $3; Kafka from $100. **Adoption:** no current user or app figures were found in the
sources read; the "An Update on Heroku" thread on Hacker News drew 525 points and
352 comments.

## Feature inventory

### Deploy sources and builds

- `git push heroku main`, GitHub integration with automatic deploys, the Container
  Registry and `heroku.yml` Docker builds (**Cedar only** — "Building Docker
  images with `heroku.yml` and the `container` stack are unsupported in Fir").
- **Buildpacks**: classic buildpacks by default on Cedar, with Cloud Native
  Buildpacks as an opt-in `cnb` stack since 2026-09-03; **Cloud Native Buildpacks
  required** on Fir. Stacks Heroku-22, -24 (default) and -26 (GA 2026-05-20).
- **Procfile** process types: `web`, `worker`, any named process, and `release`.
- **Release phase**: the `release` process runs in a one-off dyno "whenever a new
  release is created" — a build, a config var change, a pipeline promotion, a
  rollback, a new add-on — and "app dynos don't boot for a new release until the
  release phase finishes successfully". A non-zero exit fails the release;
  config var changes persist anyway; one-hour timeout, not extendable;
  `heroku releases:retry` retries without rebuilding.
- **Releases** are created by deploys, config var changes and add-on changes;
  **`heroku rollback`** restores the slug or image *and* config vars, but not
  add-on provisioning or any data, and the docs advise using it "only when
  absolutely necessary".
- **Preboot** (Standard and Performance on the Common Runtime, **off by default**):
  new web dynos start and take traffic about three minutes after a release before
  the old ones stop; Private Spaces use rolling deploys instead.
- Monorepo builds are not yet supported on Fir.

### Domains, TLS and routing

- `*.herokuapp.com` on the Common Runtime; custom domains with **Automated
  Certificate Management** (Let's Encrypt).
- The router ends any request not answered within **30 seconds**, "not
  configurable"; streaming gets a rolling 55-second window.
- Custom router error and maintenance pages on Cedar ("Router maintenance & error
  pages" are "To Be Added" on Fir); `heroku maintenance:on`.
- Private Spaces: network isolation, trusted IP ranges (on Fir for every routable
  process and for `heroku run`, 2026-06-26), internal routing, VPC peering and VPN
  on Cedar (all "To Be Added" on Fir).

### Databases and services

- **Heroku Postgres** by tier: Essential (1–32 GB, up to 4 hours of downtime a
  month, no rollback, no followers); Standard (rollback 4 days, followers, forks,
  under 1 hour); Premium, Private, Shield (rollback 7 days, **high availability**,
  under 15 minutes). Dataclips, `pg:psql`, `pg:backups`, `pg:diagnose`, pgvector.
- **Heroku Key-Value Store** (Valkey: Valkey 9 the default since 2026-08-19,
  Valkey 7 deprecated with end of life on 2027-02-19) from $3; **Apache Kafka on
  Heroku** from $100;
  **Heroku Connect** to sync with Salesforce; **AppLink** to call Heroku apps from
  Salesforce.
- **Elements Marketplace**: 200+ third-party add-ons (logging, APM, search, mail,
  object storage, queues), each provisioned by one command, billed through Heroku,
  and attached as config vars.
- **Heroku Managed Inference and Agents**: hosted models (Claude among them) billed
  per million tokens, plus hosting for MCP servers.

### Storage and backups

- **No persistent disks.** "Each dyno gets its own ephemeral filesystem ... Any
  files written get discarded the moment the dyno stops or restarts, including
  automatic restarts." Files go to object storage through add-ons.
- Postgres backups (`pg:backups`, scheduled logical) and continuous protection
  with rollback on Standard and above.

### Scaling and high availability

- `heroku ps:scale web=3 worker=2`; a formation per process type; vertical by
  dyno size.
- **Autoscaling** for web dynos only, on "Desired p95 Response Time", only for
  Performance, Private and Shield dynos, and "not yet available for Fir".
- **Dyno cycling**: every dyno is restarted "at least once per day ... once every
  24 hours (plus up to 216 random minutes)"; Fir can opt out with a labs flag.
- Eco dynos sleep after 30 minutes without traffic.

### Observability (logs, metrics, alerts, uptime)

- **Logs**: `heroku logs --tail`; Cedar keeps "the most recent 1,500 lines" (then
  one week); **Fir has no log history** and exports OTLP. **Drains**: syslog and
  HTTPS on Cedar; OpenTelemetry "telemetry drains" only on Fir ("Syslog drains are
  not available today").
- **Application Metrics** (all but Eco): response time median/p95/p99/max and
  throughput by status class for web dynos, memory, load (Cedar) or CPU (Fir),
  and events (deploys, config changes, restarts, errors); up to 7 days at 2-hour
  resolution, 2 hours at 1-minute; Basic gets 24 hours.
- **Threshold alerting** (Standard, Performance and Fir): p95 response time and
  5xx percentage, to email, PagerDuty or the dashboard.
- APM and log retention through add-ons (New Relic, Papertrail and others).

### Security, auth, roles, SSO, audit

- MFA; Heroku Teams with admin and member roles; fine-grained per-app and
  pipeline permissions **View, Deploy, Operate, Manage**, Enterprise-only until
  2026-08-20 and on every Heroku Team since; SSO and audit trails on Heroku
  Enterprise (no longer sold to new customers).
- **Heroku Shield**: HIPAA- and PCI-capable Private Spaces, dynos and Postgres at a
  20% premium.
- Private Spaces with trusted IPs and network isolation.

### Preview environments and branches

- **Pipelines**: Development, Review, Staging and Production stages; **promotion
  copies the build artifact** — "it is *not* rebuilt for the environment of the
  target app" — so production runs exactly what staging tested, "much faster
  than rebuilding". "Config vars, add-ons, and other environmental dependencies
  must be managed independently."
- **Review apps** (requires pipelines, GitHub and an `app.json`): created for
  every pull request or on demand, destroyed after 1, 2, 5, 14 or 30 days of
  inactivity; add-ons provisioned fresh per review app (with per-environment
  plans in `app.json`); "copying full database contents to a Review app is not
  currently supported"; a `postdeploy` script seeds once and `pr-predestroy`
  cleans up; pipeline-level "review app config vars" inject secrets;
  `HEROKU_APP_NAME`, `HEROKU_BRANCH`, `HEROKU_PR_NUMBER` are set; **pull requests
  from forks of public repositories are not built automatically**. Predictable
  review-app URLs are Cedar only, "not planned for Fir" because of subdomain
  takeover risk.
- **Heroku CI** runs tests in the pipeline ("currently unavailable for Fir").

### Templates and catalogue

- **Heroku Buttons**: "Deploy to Heroku" from any repository with an `app.json`
  (Cedar only; discontinued for Fir).
- The Elements Marketplace lists add-ons, buttons and buildpacks.

### CLI, API, IaC, integrations

- The **Heroku CLI** with plugins: `run` (one-off dynos, `run bash`, `run rails
  console`), `ps:exec` (Heroku Exec, Cedar only), `logs`, `config`, `pg:*`,
  `releases`, `rollback`, `pipelines:promote`, `maintenance`.
- The **Platform API**; `app.json` for review apps, CI and buttons; `heroku.yml`
  for Docker builds (Cedar); a Terraform provider.
- **MCP**: the Heroku MCP server (reported at 34 tools, including creating apps and
  add-ons and running one-off dynos) and a **remote MCP server** at
  `mcp.heroku.com` with OAuth (June 2025).

### Notifications

- Email when a release phase fails; threshold alerts by email and PagerDuty;
  **app webhooks** for releases, builds, dyno and add-on events; Eco-hour emails
  at 80% and 100%.

### Multi-server and networking

- No servers to manage. Common Runtime regions and more regions for Private
  Spaces; DNS service discovery and internal routing inside Cedar Private Spaces.

### Team and collaboration

- Personal accounts, Heroku Teams, Heroku Enterprise accounts (existing customers
  only after 2026-02-06); pipelines as the shared view of an app's stages.

### Developer experience and onboarding

- The conventions it invented are still the industry's vocabulary: Procfile,
  buildpacks, config vars, add-ons, release phase, review apps and the
  twelve-factor app.
- `heroku run rails console` against production data, from any laptop, is the
  thing its users describe missing most.
- Dev Center documentation is exhaustive and precise about limits.

## What users love

- **`git push` and done.** "There still really isn't in my mind a comparable
  offering to heroku's git push and go straight to a reasonable production"
  (Folcon, Hacker News, February 2026). "The 21st century answer to deploying a
  PHP site over FTP" (ljm, same thread).
- **Nothing else fills the spot.** "Nothing else exists to fill this spot. Fly and
  others offer varying degrees of easier hosting, but nobody offers true PaaS like
  Heroku" (Hacker News, quoted by DevClass, 2026-02-09).
- **Conventions that carry a whole app.** A Procfile with `web`, `worker` and
  `release`; `app.json` describing variables and add-ons; review apps that build
  a full copy with fresh add-ons; pipelines that promote the tested artifact.
- **Managed data done properly.** Even a competitor's founder concedes it: "we
  would all decide differently ... is how we did databases" (tptacek on Heroku's
  Postgres, same thread). Rollback, followers, forks and Dataclips.
- **The add-on marketplace.** One command to attach a logging, search, mail or
  queue service and get its config vars.

### Why they stay, and what of it travels to a user's own servers

Heroku's loyalty comes from conventions and from not having to think:

1. **Conventions in the repository** (Procfile, `app.json`). Pure file parsing —
   Skifity can read them.
2. **Release phase and one-off dynos** (`release:`, `heroku run`). Skifity already
   has a release command and one-off commands; what is missing is reading them from
   a Procfile and an interactive variant.
3. **Pipelines and review apps** (promote the tested artifact; a full copy per
   pull request). Namespaces and the build fingerprint make both natural.
4. **Add-ons** (attach a service, receive its variables). On a user's servers the
   service is a template in the same environment; the attachment is the part to
   copy.
5. **Not thinking about servers** (OS patching, restarts). This is the hard part
   on a user's own machines, and the part Heroku itself got wrong in June 2025.

What does not travel is the managed-data operation at Heroku's scale (a DBA team
behind every Postgres) and the Salesforce integrations.

## What users complain about

- **Price.** "Biggest complaint is always 'it's too expensive'" (glenngillen,
  Hacker News, February 2026); "biggest focus was to exit heroku as quickly as
  possible. The reason: Price" (eek2121). A 1 GB dyno is $50/month and
  autoscaling starts at $250/month dynos.
- **The free tier's removal.** "Starting November 28, 2022, we plan to stop offering
  free product plans" (Heroku blog, 2022-08-25) — the event that created most of the
  self-hosted PaaS category.
- **Stagnation under Salesforce**, now official. "Salesforce acquired them and just
  let it die, baffling" (bearjaws); "18 months between major user-facing platform
  launches" after the 2012 outages (bgentry, a former Heroku engineer). The
  2026-02-06 announcement froze features; Fir is stuck in Private Spaces without
  autoscaling, Heroku CI or the Common Runtime.
- **The 2025-06-10 outage**: about **24 hours**, beginning 06:00 UTC. "An automated
  operating system update ran on our production infrastructure when it should
  have been disabled"; a systemd refresh restarted networking, a legacy script did
  not restore routes, and Private Space dynos lost outbound connectivity. "Our
  internal tools and the Heroku Status Page were running on this same affected
  infrastructure", so customers were not told. Remediation: immutable
  infrastructure, an independent status channel, better diagnostics.
- **Platform limits that shape apps**: the fixed 30-second router timeout, daily
  dyno restarts, an ephemeral filesystem with no volumes, 1,500 lines of log
  history (none on Fir), a Scheduler that is "expected but not guaranteed" and
  only offers every 10 minutes, hourly or daily.
- **Fir's regressions** for anyone moving to it: no Docker builds, no syslog
  drains, no Heroku Exec, no Buttons, no autoscaling (Dev Center, "Heroku
  Generations").
- **Where they go**: Render ("this is very close to heroku"), Railway, Fly.io,
  Dokku, Coolify and Kamal on small VMs, Scalingo (summary of the February 2026
  Hacker News thread).

## Security record

- **April–May 2022, stolen OAuth tokens.** An attacker used a compromised token for
  an internal Heroku "machine account" to reach a database holding customers'
  GitHub integration OAuth tokens, downloaded them on 2022-04-07, and used them to
  download private repositories of dozens of organisations, npm among them. The
  same access later exposed hashed and salted customer passwords. On 2022-04-16
  Heroku revoked every GitHub integration token, which stopped deploys from GitHub
  through the dashboard and automation for weeks (BleepingComputer, The Record,
  The Stack). Criticism focused on slow, sparse communication.
- **No CVE against Heroku's platform** was found in the GitHub Advisory Database or
  NVD searches (2026-09-30); advisories in the `heroku` organisation concern
  individual buildpacks' dependencies.
- **The 2025-06-10 outage** was operational, not a breach, but the unplanned OS
  update is a change-control failure of the same family.
- **Sustaining engineering** promises security patches; it no longer promises new
  security features (for example Fir's missing VPN and VPC peering).

## Against Skifity

| Capability | Heroku | Skifity (evidence) | Verdict |
|---|---|---|---|
| Git push to deploy | `git push`, GitHub auto-deploy | Webhook deploy on push for GitHub, GitLab, Gitea; no Git remote to push to (`internal/api/webhook_handlers.go`, `internal/gitsrc/hooks.go`). Written | Parity (no `git push` remote) |
| Language detection | Buildpacks, CNB on Fir | Railpack, Nixpacks, Dockerfile, static front end; detection of framework and of needed databases before the first build (`internal/builder/detect.go`, `internal/builder/needs.go`) | Parity; Skifity ahead on detecting needs |
| Docker builds | Cedar only | Dockerfile wins when present (ADR-0008) | Skifity ahead of Fir |
| Procfile | `web`, `worker`, `release`, custom | Noted and used for the start command only: "The Procfile is used to start the app" (`internal/builder/detect.go:348-350`); `release` and `worker` lines are not read | Behind |
| `app.json` | Review apps, CI, Buttons: env, add-ons, scripts | Not read (no match for `app.json` in `internal/`) | Absent in Skifity |
| Release phase | On build, config change, promotion, rollback | Release command on deploy and rollback, before any traffic (`internal/deploy/run.go:124-170`, `internal/deploy/deployer.go:250`); not on a config change, which is a rollout (`Sync`, `internal/deploy/deployer.go:459-481`) | Parity |
| Rollback | Slug/image and config vars | Image and runtime settings (variables, scaling, resources, domains), refused when the image was collected (`internal/deploy/deployer.go:483-535`) | Skifity ahead |
| Zero-downtime deploys | Preboot, off by default; rolling in Private Spaces | `maxUnavailable: 0` and a five-second `preStop` by default; Recreate with a volume (`docs/concepts.md`, "Deploys and downtime"). Written | Skifity ahead (on by default) |
| Domains and TLS | ACM | cert-manager on the first domain; `sslip.io` HTTP address (ADR-0015). Written | Parity |
| Request timeout | 30 s fixed | Traefik defaults, no panel-imposed limit | Skifity ahead |
| Maintenance mode | Yes (Cedar) | None | Absent in Skifity |
| Managed Postgres | Tiers with rollback 4–7 days, followers, forks, HA | CloudNativePG with replicas; logical dumps to S3 (ADR-0012); no PITR, no forks, no followers (`internal/dbsvc/manifests.go:107-121`). Written | Behind |
| Redis | Key-Value Store | Redis 7 with AOF, single instance (`internal/dbsvc/manifests.go:277-283`). Written | Parity (single node) |
| Add-ons | 200+ marketplace, config vars injected | Databases linked by variable (`internal/dbsvc/manager.go:341-360`); 282 one-click templates in the same environment (`internal/templates/catalogue`); plugins that provide vendors (ADR-0021); no generic "attach this service to that app" | Behind on attachment, ahead on self-hosted catalogue |
| Persistent storage | None | Volumes with scheduled, restorable backups (`internal/api/api.go:309-316`, `internal/backup/volume.go`). Written | Skifity ahead |
| Scaling | Manual formation; autoscaling on $250+ dynos by p95, not on Fir | HPA on CPU/memory, scale to zero, readiness checker (`internal/kube/manifests.go:362`, `internal/kube/scaletozero.go`, `internal/deploy/scaling.go`). Written | Skifity ahead |
| Daily restarts | Every ~24 h | None imposed; restarts on failed probes only | Skifity ahead |
| Scheduled jobs | Scheduler: 10 min / hourly / daily, "not guaranteed" | Five-field cron, several per app, CronJobs with a 300 s starting deadline and no overlap (`internal/kube/runjob.go:237-256`). Written | Skifity ahead |
| One-off commands | `heroku run`, interactive | `skifity run -- cmd` in the app's image and variables, streamed, non-interactive (`internal/api/api.go:293-294`, `internal/kube/runjob.go`) | Behind (no interactive console) |
| Shell into a dyno | Heroku Exec (Cedar) | None, by decision (`docs/roadmap.md`) | Absent by decision |
| Logs | 1,500 lines / 1 week (Cedar), none on Fir; drains | Live stream plus the previous container's log (`internal/api/api.go:290`, `web/src/components/app/logs-tab.tsx`); no drains | Parity on history; behind on drains |
| Metrics | 7 days; response time and throughput per status class; threshold alerts | Current CPU and memory from metrics-server (`internal/kube/client.go:324-381`); no HTTP metrics, no history, no thresholds | Behind |
| Uptime of the platform itself | Status page went down with the platform (2025-06-10) | Apps do not depend on the panel (checklist item 14); but the panel is pinned to one node (`deploy/panel.yaml:91`) and is what notices a lost server (`internal/watch/watch.go:256`), so its own loss is silent | Parity on apps; gap on self-monitoring |
| OS patching | Heroku's job ("Automatic OS patching" on the pricing page) | Not handled: nothing in `installer/` or `internal/provision` touches unattended upgrades or reboots | Absent in Skifity |
| Sign-in, SSO | MFA; SSO on Enterprise | Argon2id, TOTP, recovery keys, OIDC for everyone (`internal/auth`) | Skifity ahead |
| Password in front of an app | None built in; add-ons or app code | HTTP basic auth through Traefik from a bcrypt hash in the app's namespace, inherited by previews, admin-only to change (`internal/api/password_handlers.go`, `internal/kube/password.go`, commit `7865bc8`). Written | Skifity ahead |
| Roles and permissions | Team roles, per-app and pipeline permissions | Owner, admin, member per team (`internal/store/models.go:5-20`); scoped API tokens (`internal/auth/scopes.go`) | Behind on granularity |
| Audit | Enterprise | Team audit log, 365 days (`internal/store/retention.go:42`) | Skifity ahead |
| Secrets | Config vars readable by collaborators | Write-only on every surface, sealed with context (`internal/crypto`) | Skifity ahead |
| Pipelines and promotion | Promote the tested slug without rebuilding | None: each environment's app builds its own image; no promotion (no match for promotion in `internal/deploy` or `internal/api`) | Absent in Skifity |
| Review apps | Full app with fresh add-ons, postdeploy seed, fork PRs skipped, 1–30 day expiry | One app per PR, fork PRs get no secrets and no database, an empty database of its own per linked one since `1ae3276`, 7-day reaper, commit status and PR comment (`internal/api/webhook_handlers.go:183-445`, `internal/watch/watch.go:136-190`, `internal/deploy/gitreport.go`); no `postdeploy`-style seed (see `render.md`, P1). Written | Behind (narrowing) |
| CI | Heroku CI (not on Fir) | None | Absent (and not wanted) |
| Buttons | Deploy to Heroku (Cedar) | Templates install from the catalogue only | Absent in Skifity |
| CLI | Rich, plugins | 17 commands, `--json` everywhere (`internal/cli/commands.go:34-71`) | Behind on breadth |
| API | Platform API | Same API for panel, CLI and MCP (`internal/api/api.go`), errors with cause/impact/fix (`internal/errdoc`) | Parity |
| IaC | `app.json` (partial), Terraform | None; export to JSON and Kubernetes YAML (`internal/api/export_handlers.go`) | Behind (see `render.md`, P1) |
| MCP | Local and remote (OAuth), ~34 tools | Local stdio, 15 tools, nothing destructive (`internal/mcpserver/server.go:314-391`) | Behind on breadth |
| Notifications | Email, PagerDuty, app webhooks | Telegram, Discord, webhook, email, plugin kinds; seven events (`internal/notify/notify.go:41-47`). Written | Parity |
| Multi-server and networking | None to manage; Private Spaces | Your servers join one k3s cluster over SSH with WireGuard between them (`internal/provision`, ADR-0003); default-deny between environments (`internal/kube/namespace.go:127-217`). Written | Different model |
| Team | Teams, Enterprise | Teams with invitations and roles (`internal/api/api.go:205-214`) | Parity |
| Documentation | Dev Center | Served from the binary, links checked by a test (`internal/docsite`) | Parity |
| Vendor risk | Feature freeze, no new Enterprise contracts | Apache 2.0, self-hosted, export works without the panel (checklist item 16) | Skifity ahead |

## Gaps worth closing in Skifity

### P0 — Honour the Procfile, and read `app.json`

**What.** When detection finds a Procfile, read every line: `web` sets the start
command (as now), **`release` sets the release command**, and each other process
(`worker`, `clock`, custom names) is offered as an additional app from the same
repository with no port, created in the same step. When it finds `app.json`,
read `env` (`required`, `value`, `description`, `generator: "secret"`) into the
variables form, `addons` into the databases to create (`heroku-postgresql` →
PostgreSQL, `heroku-redis` / Key-Value Store → Redis, `jawsdb`/`cleardb` → MySQL;
anything else named as unsupported, like MongoDB is today), `formation` into
instance counts, and `scripts.postdeploy` into a preview seed command (proposed
in `render.md`, P1; Skifity has none yet).

**Evidence.** Heroku froze features on 2026-02-06 and stopped selling Enterprise
contracts to new customers; its users are now choosing where to go, and Render
and Railway both publish Heroku migration guides (Render adds up to $10,000 in
migration credits). Today a typical Rails app with `release: bundle exec rails
db:migrate` and `worker: bundle exec sidekiq` deploys to Skifity with neither: the
detector notes the Procfile and uses it only to start the app
(`internal/builder/detect.go:348-350`), so migrations silently never run and jobs
silently never process. That is a correctness trap for the most likely migrating
user, not a missing nicety.

**Fit.** `internal/builder` (Procfile and `app.json` parsers beside `needs.go`,
whose findings already carry "the package and the file it came from"), the
new-app form and `skifity up` (both already create databases before the first
deploy), and the MCP `deploy_folder` tool. Multi-app creation reuses the template
installer, which already creates several services with shared links.

**Size.** M. **Without a cluster:** entirely — parsers, detection output, the
form (Playwright) and `skifity up` against the smoke-test panel.

### P1 — Promote an image between environments

**What.** "Promote to Production" on a staging app: deploy the exact image staging
runs into the matching app in another environment, with that environment's own
runtime variables, running its release command, without building. Refuse when
the two apps' build-time variables differ, because then the image is not the same
program (ADR-0007 already tells the two kinds apart).

**Evidence.** Heroku pipelines: "production contains the exact same code that you
tested in staging, and it's also much faster than rebuilding". Railway syncs
configuration between environments but rebuilds; Render has no promotion. Skifity
builds every environment's app from source separately.

**Fit.** A deployment with `Trigger: "promote"` carrying the source deployment's
image into `Deployer.run`, which already skips the build when `Image` is set
(`internal/deploy/deployer.go:198-212`); the image must be reachable from the
target namespace's pull path (`internal/kube/registryauth.go`); the registry sweep
must keep a promoted image (`internal/registry`). API
`POST /api/apps/{app}/promote`, a button, a CLI command and an MCP tool.

**Size.** M. **Without a cluster:** the matching, fingerprint check and records
yes; the pull from another namespace needs one.

### P1 — HTTP metrics and alerts from the ingress

**What.** Per app: requests per minute by status class and response time p50/p95/p99
for the last 7 days, with deployment markers, and threshold alerts on p95 latency
and 5xx rate through the existing channels.

**Evidence.** Heroku's metrics lead with response time and throughput for web
dynos and alert on them; Railway's metrics do not include HTTP latency; Render
sells HTTP request logs on Pro. Skifity has only instant CPU and memory
(`internal/kube/client.go:324-381`). The ingress (Traefik) already exports
per-service request counts and duration histograms in Prometheus format.

**Fit.** A minute-tick scraper of Traefik's metrics endpoint in `internal/serverapp`,
a small Prometheus text parser (the panel already writes the format in
`internal/metrics`), downsampled storage with pruning as in
`internal/store/retention.go`, rules in `internal/watch`. Pairs with the resource
history proposed in `railway.md` (P1).

**Size.** M. **Without a cluster:** parser, aggregation, storage, alert rules and
charts from fixtures yes; real traffic needs one.

### P1 — Keep servers patched without an outage

**What.** Show each server's pending OS security updates and whether a reboot is
required; apply them one server at a time — cordon, drain (respecting the
PodDisruptionBudgets the panel already renders), upgrade, reboot, wait Ready,
uncordon — on a schedule the owner picks, and never on two control-plane servers
at once. Tell the owner if unattended upgrades are enabled on a node, because an
uncoordinated upgrade is exactly what took Heroku down for a day.

**Evidence.** Heroku sells "Automatic OS patching" and suffered a 24-hour outage
from an unplanned OS update (2025-06-10). Skifity installs k3s on the user's
machines and then says nothing about them: nothing in `installer/` or
`internal/provision` touches unattended upgrades or reboots. "You never think about
a server" is the promise every managed platform sells.

**Fit.** `internal/provision` already runs commands over SSH through
`internal/shellsafe`; drain logic exists for server removal
(`internal/api/servers_handlers.go`); PDBs are in `internal/kube/manifests.go:404`.
A server page section and an operation with steps, like adding a server.

**Size.** L. **Needs a cluster** (and real VMs) to verify; the ordering and safety
rules are unit-testable with the fake clientset and the in-process SSH server.

### P2 — Attach any service to an app (add-ons, self-hosted)

**What.** Let a template declare the variables it exports (MinIO: endpoint, access
key, secret key, bucket; Meilisearch: URL and key; RabbitMQ: URL) and let any app
in the environment attach it, receiving those variables as a link does for a
database today. Show attachments on the canvas.

**Evidence.** The add-on model — attach a resource, receive config vars — is the
reason Heroku's marketplace worked; Railway's reference variables are the same
idea. Skifity links only managed databases (`internal/dbsvc/manager.go:341-360`)
and wires template services only at install time (`internal/templates/templates.go`,
`DatabaseSpec.LinkTo`).

**Size.** M. **Without a cluster:** yes. Shares a resolver with the reference
variables proposed in `railway.md` (P1).

### P2 — An interactive one-off console

**What.** `skifity run -it -- rails console` (and a panel terminal) that starts a
*new* one-off Job in the app's image with its variables and attaches to it — not a
shell into a running instance — recorded in the audit log with who, when and for
how long, admin-only on protected environments.

**Evidence.** `heroku run rails console` is the Heroku habit its users name; Render
added SSH into an *ephemeral* instance on 2026-06-02 and Railway has `railway ssh`.
Skifity's roadmap rejects a shell into a running instance because one-off commands
run in the app's image, leave a log and work when the app will not start
(`docs/roadmap.md`) — an interactive one-off keeps every one of those properties.
The cost is a bidirectional stream, which ADR-0005 avoided for the panel's events.

**Size.** M–L. **Needs a cluster** to verify attach; the CLI side and the audit
record are unit-testable.

### P2 — A heartbeat so the panel's own absence is noticed

**What.** An optional outbound heartbeat, every minute, to a URL the owner
configures (a healthchecks-style service, their own monitor), so that when the
node running the panel dies somebody is told.

**Evidence.** Heroku's status page lived on the infrastructure that failed. Skifity's
panel is pinned to one node (`deploy/panel.yaml:91`) and is itself what detects a
lost server (`internal/watch/watch.go:256`), so the loss of its own node is the one
failure it cannot report. A URL the owner chose keeps "never phones home" intact.

**Size.** S. **Without a cluster:** yes (the ticker and the request).

### P2 — Maintenance mode

Heroku's `maintenance:on` and Render's maintenance page; see `render.md` (P2).
Size S.

## Things to deliberately not copy

- **A new generation only on the most expensive tier.** Fir, Heroku's Kubernetes
  generation, was released only in Private Spaces and then frozen, stranding the
  long tail on Cedar. Skifity's Kubernetes base is the default for everybody; keep
  every capability available on a one-server install.
- **The 30-second router timeout and daily restarts.** Both exist to protect a
  shared fleet, and both shape apps around the platform. On a user's own servers
  there is no fleet to protect.
- **An ephemeral-only filesystem.** Heroku's answer to state is "use an add-on";
  Skifity's volumes, with the Recreate caveat and the scaling checker's warnings,
  are more honest.
- **A scheduler that is "expected but not guaranteed".** Skifity's CronJobs have a
  starting deadline and no overlap; keep scheduled commands precise and say what
  happens to a missed run.
- **Rollback that restores config but warns "only when absolutely necessary".**
  Skifity's rollback restores image and settings and refuses when it cannot; keep
  it the thing people reach for first.
- **Autoscaling and preboot as paid or opt-in extras.** Zero-downtime deploys are
  on by default in Skifity, and autoscaling does not depend on a tier.
- **Heroku CI.** CI belongs in the repository's host; waiting for its result (see
  `railway.md`, P2) is the useful half.
- **Marketplace billing and revenue share.** Add-ons worked because Heroku billed
  for them. Skifity has no billing; copy the attachment, not the commerce.
- **Status and tools on the infrastructure they report on.** Heroku's June 2025
  lesson. The heartbeat above is the cheap version of not repeating it.
- **Long-lived OAuth tokens in one database.** Heroku's 2022 breach was a table of
  every customer's GitHub token. Skifity seals each token with its own context
  (`internal/crypto`), sends it only to its own host, and supports GitHub through
  personal access tokens only (`internal/api/integrations_handlers.go:41-64`); when
  a GitHub App is added, prefer short-lived installation tokens over stored user
  tokens.

## Since 2026-09-30

Read on 2026-11-08: the Dev Center changelog (its first two pages, back to
2026-08-12) and the items named below, Heroku's blog feed and the posts named
below, the pricing page, the "Heroku Generations" page, the status page's
incidents API, and the git histories of `heroku/cli` and
`heroku/heroku-mcp-server`, cloned without file contents (tags and
`CHANGELOG.md` read). GitHub's API refused this session for repositories other
than Skifity's, so stars and open issues were not read again, and the web search
had no credits left. NVD and Hacker News were read through their APIs.

### Product

- **Heroku Postgres Advanced, generally available 2026-10-06.** A new tier on
  new infrastructure built with AWS: compute apart from storage (100 GB
  included, "200+ TB" possible), storage that grows and shrinks with the data,
  4 GB and 2 vCPU up to 1,536 GB and 192 vCPU, PostgreSQL 16 to 18, a leader pool
  and up to five named follower pools of 13 followers in all, each pool attachable
  to its own app, storage quotas with "Warning" and "Critical" thresholds, and
  scaling, high availability and credential changes "with zero app downtime".
  Prices are in the corrected pricing above. The changelog item lists logical
  replication, and CLI 11.11.0 (2026-10-01) manages it; the announcement puts
  "native logical replication", server-side pooling, autoscaling, sleep mode,
  global clusters and migration tooling on its roadmap. PostgreSQL 18 is now the
  default on every tier (blog, 2026-10-07). It is the largest launch since the
  sustaining-engineering announcement of 2026-02-06.
- **Team Authorizations (2026-09-29; blog 2026-09-30).** Team admins create,
  regenerate and revoke OAuth authorizations that act for a team rather than a
  person, for CI and integrations; CLI 11.12.0 (2026-10-13) added `--team`. The
  Platform API gained an OAuth Session resource for listing and deleting a
  user's sessions (2026-10-21).
- Shipped before 2026-09-30 and not recorded then (corrected above): per-app
  View, Deploy, Operate and Manage permissions on every Heroku Team (2026-08-20);
  Cloud Native Buildpacks on Cedar as an opt-in stack (2026-09-03); Valkey 9 on
  Key-Value Store (2026-08-19), with Valkey 7's end of life moved to 2027-02-19
  (2026-10-05); TLS 1.3 and HTTP/2 on `*.herokuapp.com` (2026-08-24).
- **Patching without redeploys.** The Heroku-22, -24 and -26 base images were
  updated on 2026-10-08, 2026-10-20 (twice), 2026-10-22 and 2026-10-28 and rolled
  out over 48 hours with no redeploy: Cedar lays the slug over the newest base
  image at each dyno start, and Fir rebases the built image onto it. Apps built
  from a Dockerfile have to be rebuilt.
- Otherwise maintenance: runtime releases throughout; Python 3.11 deprecated
  (2026-10-26); Kafka 2.8 and 3.7 at end of life (2026-10-28) and Kafka moving
  from ZooKeeper to KRaft from 2026-10-19; Claude Opus, Sonnet and Haiku 5.5 and
  Fable 5.1 in Managed Inference and Agents (2026-10-14); payment methods on a
  Stripe-hosted page (2026-11-04); SendGrid add-on users need a SendGrid
  account of their own (from 2026-10-16).
- **Fir is where it was.** The generations page, last updated 2026-09-11, still
  lists dyno autoscaling, the Common Runtime, Heroku CI, internal routing, VPC
  peering, VPN and router maintenance pages as "To Be Added". "We have a lot of
  great updates coming later this year", with KubeCon on 2026-11-10 to 12
  named (blog, 2026-10-07).
- CLI v11.11.0 to v11.12.1 (2026-10-01 to 2026-10-22); 11.12.1 bundles a patched
  Node.js. MCP server v1.2.10 to v1.2.13 (2026-10-01 to 2026-10-29): dependency
  updates and a REPL fix only.
- Unchanged on 2026-11-08: dyno, Essential Postgres, Key-Value Store and Kafka
  prices.

### Incidents

One on the status page since 2026-09-30: Node.js builds failing at elevated
rates because of an upstream dependency, 2026-10-28, from 10:55 to 15:59 UTC.

### Security

- No CVE names Heroku (NVD keyword "heroku", 2026-09-30 to 2026-11-08: none).
- **PHP CVE-2026-109618 and CVE-2026-109619**, critical remote code execution in
  PHP 5.4 through 8.5.11, published 2026-10-21. Heroku shipped PHP 8.2.35,
  8.3.36, 8.4.27 and 8.5.12 the same day, rebuilt the end-of-life PHP 8.1.34 on
  Heroku-22 with a backported fix, and asked PHP users to redeploy.
- **The eMudhra private key compromise.** Heroku updated `ca-certificates` in
  every stack with nothing for users to do, and told those whose libraries carry
  their own roots (Go, Node.js, Ruby's Excon) to update them; CLI users were told
  to update to 11.12.1 for its bundled Node (blog, 2026-10-29).

### What users said

- Quiet. On Hacker News since 2026-09-30, Heroku is mostly mentioned in passing:
  "Crazy that Heroku with its head start has become abandonware" (2026-10-01);
  one migration story, "From Heroku to Convox" (2026-11-03, 1 point). In the
  "Northflank Breached" thread (2026-10-30) one comment put the self-hosting case
  in a line: "Everyone knows Fort Knox has gold, and where it is, that's railway,
  heroku. No one knows where my personal safe is".

### For Skifity

Checked against the code on 2026-11-08.

- **eMudhra, in Skifity's terms.** The binary is built with Go 1.26.10
  (`go.mod`), released on 2026-10-19 with the crypto/x509 denylist. An app's image
  keeps the CA bundle and runtime it was built with until it is rebuilt. Every
  deployed image is scanned again with Trivy every 12 hours and `app.vulnerable`
  is sent (`internal/deploy/scan.go`, `internal/vulnscan`), which catches a
  vulnerable PHP where the image's package database records it; the build-time
  advisories cover Next.js and React only (`internal/builder/advisories.go`).
  Nothing moves an image onto a patched base without a build, which a buildpack
  platform can do and a Dockerfile one cannot; rebuilding without the cache is
  new gap 3 in `render.md`.
- **Postgres Advanced's storage quotas** warn before a database fills; Skifity
  warns about a server's disk only (`render.md`, failure notifications).

This file's gaps, against the code:

- P0, Procfile and `app.json`: **closed** (Phase 100; a Procfile's worker lines
  run as processes of the same build, Phase 106).
- P1, promote: **closed** (Phase 103, `POST /api/apps/{app}/promote`).
- P1, HTTP metrics and alerts: **closed.** Requests, errors and response times
  per app (`internal/traffic`), thresholds on them (`internal/watch/traffic.go`)
  and `app.alert`.
- P1, OS patching: **half.** The hardening report says whether automatic
  updates are on and whether a reboot is pending
  (`internal/provision/hardening.go`); nothing applies updates or reboots servers
  one at a time (still open in `coolify.md`).
- P2, attach any service: **open.** Only a managed database can be linked after
  it is made (`POST /api/databases/{id}/link`); a template's services are wired
  to each other at install time only.
- P2, an interactive one-off console: **open.** `skifity run` streams a one-off
  command's output and reads no input; `skifity db connect` (Phase 105) is
  Heroku's `pg:psql`, not its `run rails console`.
- P2, a heartbeat: **open.** No setting sends one (`internal/settings`,
  `internal/watch`).
- P2, maintenance mode: **closed** (Phase 98).

Rows of the table above that no longer describe Skifity:

- **Procfile** and **`app.json`:** read (Phases 100 and 106).
- **Maintenance mode:** answered by the guard (Phase 98).
- **Managed Postgres** and **Redis:** nine engines, Valkey among them; stop,
  resize, change the password, import a dump (Phase 112). Still no point-in-time
  recovery, forks or followers.
- **Add-ons:** 374 templates and a team's own catalogues (Phase 112).
- **Metrics:** usage history and alerts (Phases 95 and 99) and traffic per app
  (`internal/traffic`).
- **OS patching:** the hardening report above.
- **Roles and permissions:** a viewer role, members limited to projects, a team
  that requires two factors, teams that follow the identity provider's groups
  (Phases 85, 86, 91 and 92).
- **Pipelines and promotion:** Phase 103.
- **Review apps:** the whole environment, seeded, and started by hand (Phases 107
  and 112).
- **CLI:** about forty commands (`internal/cli/commands.go`).
- **IaC:** `skifity.yaml` with plan and apply (Phase 108).
- **MCP:** also served by the panel at `/api/mcp` (Phase 87), with more than
  forty tools, `run_command` and `rollback_app` among them (`internal/mcpserver`).

New gap:

1. **Tokens that belong to the team.** *What:* an API token owned by a team
   rather than a person, made, rotated and revoked by its admins, with a role and
   scopes of its own and its own name in the audit log, so that a pipeline keeps
   working when the person who made its token leaves and a member's departure
   never has to be weighed against a CI outage. *Evidence:* Heroku's Team
   Authorizations (2026-09-29); Render's new scoped keys are still a person's and
   "can never exceed the permissions you already have" (2026-10-27). Skifity's
   `APIToken` has a `UserID` (`internal/store/models.go`), a token acts through
   its owner's membership (`membershipIn`, `internal/api/api.go`), and removing a
   member deletes the membership only (`RemoveMember`,
   `internal/store/teams.go`), after which every token that person made for the
   team is answered as if the team did not exist. *Size:* M. *Without a
   cluster:* entirely.

Worth not copying, from this period: a database tier whose headline features
(native logical replication, pooling, migration tooling) are on the roadmap of
its own launch post; and patching that works only for apps built the platform's
way, leaving Dockerfile users to notice for themselves.

## Sources

All read 2026-09-30 unless marked.

- https://www.heroku.com/blog/an-update-on-heroku/ (2026-02-06)
- https://www.devclass.com/development/2026/02/09/heroku-future-in-doubt-as-salesforce-freezes-features-to-focus-on-ai/4090238
- https://news.ycombinator.com/item?id=46913903 ("An Update on Heroku", 525 points, 352 comments)
- https://simonwillison.net/2026/Feb/6/an-update-on-heroku/ (via search results)
- https://www.infoworld.com/article/4129430/salesforce-may-be-prepping-to-phase-out-heroku.html (via search results)
- https://www.heroku.com/blog/heroku-fir-generally-available-new-platform-capabilities/ (Fir GA, 2025-04-02)
- https://devcenter.heroku.com/articles/generations
- https://devcenter.heroku.com/changelog-items/3703 (Heroku-26, 2026-05-20)
- https://devcenter.heroku.com/changelog-items/3483 and https://devcenter.heroku.com/changelog-items/3735 (via search results)
- https://www.heroku.com/pricing/ (downloaded and read as text)
- https://www.qovery.com/blog/heroku-private-spaces-pricing-2026-real-cost
- https://devcenter.heroku.com/articles/private-spaces
- https://devcenter.heroku.com/articles/github-integration-review-apps
- https://devcenter.heroku.com/articles/pipelines
- https://devcenter.heroku.com/articles/release-phase
- https://devcenter.heroku.com/articles/releases
- https://devcenter.heroku.com/articles/preboot
- https://devcenter.heroku.com/articles/request-timeout
- https://devcenter.heroku.com/articles/dynos
- https://devcenter.heroku.com/articles/dyno-restarts
- https://devcenter.heroku.com/articles/eco-dyno-hours
- https://devcenter.heroku.com/articles/scaling
- https://devcenter.heroku.com/articles/autoscaling
- https://devcenter.heroku.com/articles/scheduler
- https://devcenter.heroku.com/articles/logging
- https://devcenter.heroku.com/articles/metrics
- https://devcenter.heroku.com/articles/heroku-postgres-plans
- https://www.heroku.com/elements/ and https://www.heroku.com/elements/addons/ (via search results, "200+ add-ons")
- https://www.heroku.com/blog/heroku-remote-mcp-server/ and https://github.com/heroku/heroku-mcp-server (via search results)
- https://www.heroku.com/blog/summary-of-june-10-outage/
- https://www.heroku.com/blog/next-chapter (2022-08-25, free plans ended 2022-11-28)
- https://en.wikipedia.org/wiki/Heroku
- https://www.bleepingcomputer.com/news/security/heroku-admits-that-customer-credentials-were-stolen-in-cyberattack/ (via search results)
- https://therecord.media/heroku-breach-salesforce-oauth-github (via search results)
- https://www.thestack.technology/heroku-outage-github-breach/ (via search results)
- https://www.darkreading.com/endpoint-security/heroku-cyberattacker-stolen-oauth-token-customer-account-credentials (via search results)
- GitHub API via repository search, `org:heroku` (stars and open issues for cli, buildpacks, logplex, heroku-pg-extras)
- Skifity: the files cited in the table, at commit `1ae3276`

Read 2026-11-08, for "Since 2026-09-30":

- https://devcenter.heroku.com/changelog and https://devcenter.heroku.com/changelog?page=2 (items 2026-08-12 to 2026-11-05)
- https://devcenter.heroku.com/changelog-items/3802 (Postgres Advanced GA, 2026-10-06)
- https://devcenter.heroku.com/changelog-items/3815 (team authorizations, 2026-09-29) and https://devcenter.heroku.com/changelog-items/3841 (OAuth Session resource, 2026-10-21)
- https://devcenter.heroku.com/changelog-items/3749 (fine-grained access for all Teams, 2026-08-20)
- https://devcenter.heroku.com/changelog-items/3803 (Cloud Native Buildpacks on Cedar, 2026-09-03)
- https://devcenter.heroku.com/changelog-items/3793 and https://devcenter.heroku.com/changelog-items/3794 (Valkey 9 GA and Valkey 7 deprecation, 2026-08-19, schedule updated 2026-10-05)
- https://devcenter.heroku.com/changelog-items/3796 (`*.herokuapp.com` TLS 1.3 and HTTP/2, 2026-08-24)
- https://devcenter.heroku.com/changelog-items/3829, 3840, 3842, 3847 and 3892 (stack updates, 2026-10-08 to 2026-10-28)
- https://devcenter.heroku.com/changelog-items/3845 (PHP CVE-2026-109618 and CVE-2026-109619, 2026-10-21)
- https://devcenter.heroku.com/changelog-items/3827, 3832, 3833, 3857, 3859, 3896 and 3899 (API docs, Kafka KRaft, Heroku AI models, Python 3.11, Kafka end of life, SendGrid, payments)
- https://devcenter.heroku.com/articles/generations (last updated 2026-09-11)
- https://www.heroku.com/blog/feed/ (posts to 2026-10-29)
- https://www.heroku.com/blog/new-era-managed-databases-announcing-heroku-postgres-advanced/ (2026-10-06)
- https://www.heroku.com/blog/october-2026-update-heroku-innovations-features/ (2026-10-07)
- https://www.heroku.com/blog/introducing-team-authorizations/ (2026-09-30)
- https://www.heroku.com/blog/securing-your-stack-recent-php-and-emudhra-updates-on-heroku/ (2026-10-29)
- https://www.heroku.com/pricing/ (dynos, Postgres Advanced, Key-Value Store, Kafka)
- https://status.heroku.com/api/v4/incidents (incident 2966, 2026-10-28)
- https://github.com/heroku/cli (git tags and `CHANGELOG.md`, v11.11.0 to v11.12.1)
- https://github.com/heroku/heroku-mcp-server (git tags and `CHANGELOG.md`, v1.2.10 to v1.2.13)
- https://services.nvd.nist.gov/rest/json/cves/2.0 (keyword "heroku", none; CVE-2026-109618)
- https://go.dev/doc/devel/release (go1.26.10, 2026-10-19)
- https://hn.algolia.com/api/v1/search and https://hn.algolia.com/api/v1/search_by_date (stories and comments since 2026-09-30; https://news.ycombinator.com/item?id=50346247 and comments 49917588 and 50310155)
- Skifity, for "For Skifity": `go.mod`, `internal/store/models.go`, `internal/store/teams.go`, `internal/api/api.go`, `internal/api/teams_handlers.go`, `internal/deploy/scan.go`, `internal/vulnscan`, `internal/builder/advisories.go`, `internal/provision/hardening.go`, `internal/traffic`, `internal/watch/traffic.go`, `internal/cli/commands.go`, `internal/mcpserver`, `docs/progress.md`
