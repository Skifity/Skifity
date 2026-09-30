# Porter

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Researched 2026-09-30 against live sources (Porter's docs, pricing page, blog
and homepage, the `porter-dev` GitHub organisation, Hacker News, NVD). Every
number carries its source and the date it was read. "Porter" is a crowded
name: this is Porter Technologies (porter.run, YC S20), not the CNAB tool
`getporter/porter` and not the Indian logistics company Porter, whose 2024 data
breach dominates search results.

Porter is a Kubernetes PaaS that "runs in your own AWS, GCP, or Azure account":
connect a cloud account, Porter provisions and then operates a managed cluster
there (EKS, GKE or AKS, with its VPC, load balancer and registry), and teams
deploy web services, workers and jobs to it from a hosted dashboard, a CLI, a
`porter.yaml` file or — since 2026 — an MCP server. It is for venture-backed
startups graduating from Heroku, Render or Vercel ("the fastest growing AI
startups", homepage) and explicitly not for small projects: "Porter is not
meant for small scale projects ... The base infrastructure Porter provisions
alone is around $300" (co-founder, Hacker News, 2023-06-14). **Architecture:**
a hosted control plane (`dashboard.porter.run`) that reaches into the
customer's account through an assumed IAM role (`porter-manager`, created by a
CloudFormation stack), a service principal, or Workload Identity Federation; in
the customer's account, three default node groups (System 2×t3.medium,
Monitoring 1×t3.large, Application from 1×t3.medium, autoscaled by Karpenter on
AWS), ingress-nginx (Porter maintains a build of Chainguard's fork since
January 2026), cert-manager, Prometheus (14-day retention) and Loki (7-day
retention). Builds run in the customer's GitHub Actions, not in the cluster.
Environment-group secrets are synced to the cloud's secret manager rather than
stored by Porter. In 2023 Porter replaced its Terraform-based provisioning with
its own reconciler "to immediately reconcile drifts" (co-founder, Hacker News,
2023-09-05). **Licence:** proprietary. The platform was MIT-licensed as
`porter-dev/porter` until 2024; it was renamed `porter-archive` around the May
2024 launch of Porter Cloud (Hacker News, 2024-05-23) and is **no longer public
as of 2026-09-30** — GitHub answers 404, and none of the organisation's public
repositories is the platform. The last mirrored release was v0.52.56 (SourceForge
mirror, last updated 2024-05-22). What remains public is the CLI tap, GitHub
Actions and `porter-charts` (25 stars). **Pricing (read 2026-09-30):** Standard
is pay-per-use on what apps request — $13 per vCPU-month ($0.019/hour) and $6
per GB-RAM-month ($0.009/hour) — on top of the cloud bill, which for the
default cluster Porter itself estimates at ~$201/month on AWS, ~$253 on GCP and
~$165 on Azure. Enterprise starts at 40 vCPU / 80 GB and adds SAML SSO,
advanced RBAC, custom alerts and on-prem installation. Startups get six months
free; non-profits 50% off. No free tier. **Maturity and adoption:** founded
2020; a $1.5M seed around YC Demo Day in 2021 (Porter blog, "Our Journey
Building Porter"); **$20M Series A led by FirstMark, announced 2026-01-27**
(Porter blog); "hundreds of the fastest-growing AI companies use Porter" and
customers scale "individual clusters ... to hundreds of machines and terabytes
of RAM" (same post). The hosted, eject-able Porter Cloud launched on Hacker News
on 2024-05-23 (258 points, 95 comments); `porter.run/porter-cloud` now redirects
to the homepage and neither the pricing page nor the docs mention it, so it
appears to have been withdrawn — Porter has not said so that I could find.

## Feature inventory

### Deploy sources and builds

- **GitHub:** install the Porter GitHub App; Porter opens a pull request adding
  a GitHub Actions workflow (branch `porter-stack`) that runs
  `porter apply -f porter.yaml` on every push. The first real deploy happens
  when that PR is merged; until then the app runs a placeholder image.
- **Any other CI** (CircleCI, GitLab, Travis) through the CLI's Docker image.
- **Container registries:** ECR, GAR, ACR, Docker Hub, any OCI registry.
- **Builds:** Dockerfile or Cloud Native Buildpacks (`method: pack`, e.g.
  `heroku/buildpacks:20`), with framework detection. Non-secret variables are
  piped into the build automatically; **secrets are not available at build
  time** (docs: "Secrets will not be made available to your build process").
- **One build, many apps:** a custom workflow builds one image and deploys it to
  several Porter apps.
- **Service types:** web, worker, job (cron or on demand) — an application is a
  group of services sharing one build and one set of variables.
- **Lifecycle:** `predeploy` job after the build and before the rollout (for
  migrations), `initialDeploy` job that runs only on the first deployment,
  `autoRollback` in `porter.yaml`, and rollbacks from the Activity tab or
  `porter app rollback`.
- **One-off and interactive:** `porter app run my-app -- python manage.py
  migrate`, `porter app run my-app -- bash` (interactive), with CPU/RAM
  overrides and `run cleanup` for leftover ephemeral replicas.

### Domains, TLS and routing

- Custom domains with Let's Encrypt issuance and renewal through cert-manager.
- NGINX ingress annotations exposed for timeouts, body size and WebSockets.
- AWS: switch from NLB to ALB for IP allowlisting, WAFv2 and ACM certificates.
- Cloudflare DNS in proxied or DNS-only mode, documented.
- Static egress IPs for third-party allowlists.
- Preview URLs by template: `{BRANCH}.my-domain.com` or
  `{PR_NUMBER}.my-domain.com` with a wildcard DNS record.

### Databases and services

- **Datastores (AWS only; GCP and Azure "on the roadmap"):** PostgreSQL as an
  in-cluster container (dev), a single Multi-AZ RDS instance, or an Aurora
  cluster with an optional read replica; Redis (ElastiCache). Provisioned in a
  separate VPC, peered, private subnets only. Connection details arrive as an
  environment group.
- `porter datastore connect` opens a local tunnel to a datastore for `psql`
  (Tailscale needed if the control plane is private).
- **Add-ons:** Datadog, New Relic, Grafana, Langfuse, Helicone AI Gateway,
  Mezmo, Metabase, Quivr, n8n, a persistent disk, and any public Helm chart.

### Storage and backups

- Persistent storage is Amazon EFS (AWS only), mountable by several services
  at once through `connections` in `porter.yaml`.
- Backups are the cloud provider's (RDS/Aurora automated backups); Porter's
  docs index has no backup or restore page of its own (read 2026-09-30).

### Scaling and high availability

- HPA on CPU and memory thresholds; KEDA-based autoscaling on custom
  Prometheus metrics; Temporal task-queue autoscaling.
- Karpenter "cost-optimized" node groups that pick instance types and bin-pack;
  fixed node groups for GPUs, Spot or special hardware; per-instance vCPU
  bounds to limit the blast radius of a Spot interruption.
- Sleep mode per service ("stopped and no instances will run").
- Managed cluster upgrades twice a year (end of Q1 and Q3), "leapfrogging"
  versions, done blue-green by node group — under a shared-responsibility model
  in which customers must run production with at least three replicas, health
  checks and graceful shutdown, or "an upgrade can cause significant
  disruption".
- Node image patches from the cloud provider, plus Porter-pushed patches for
  critical CVEs.

### Observability (logs, metrics, alerts, uptime)

- Logs retained 7 days in Loki in the customer's cluster, searchable, with time
  navigation; `porter app logs --search`, `--since`, `--from/--to`.
- Metrics retained 14 days in Prometheus in the cluster: CPU, RAM, network,
  NGINX error throughput.
- Cluster observability: pod status, node usage.
- Alerts on crash loops, out-of-memory and non-zero exits, to notification
  groups (Slack, email, PagerDuty) attached per app. Custom alerts are
  Enterprise.
- **DevOps Agent (alpha):** an in-dashboard AI debugger (`⌘+I`) that runs as a
  small container in the customer's cluster (50m CPU, 128 Mi) with read-only
  access, context-aware of the page being viewed, using the customer's own
  Anthropic or AWS Bedrock key (Claude Sonnet 4.6, Opus 4.6 or Haiku 4.5).

### Security, auth, roles, SSO, audit

- Cloud access through role assumption / workload identity; no static keys
  stored; revocable by deleting the role.
- Roles: Admin, Developer, Viewer. Invitations expire in 24 hours.
- SSO: requested through support; SAML-based SSO is an Enterprise line item;
  just-in-time provisioning with a configurable default role (Viewer by
  default).
- Audit logs (Admin only): every dashboard, CLI and API-token request with
  actor, method, status, resource and action; 30 days; filters with
  include/exclude; shareable filtered URLs; CSV/NDJSON export capped at 10,000
  rows.
- "One-click" SOC 2 and HIPAA configuration of the AWS account; ECR scanning,
  GuardDuty, private clusters, KMS encryption; Tailscale for private access.
- Secrets in environment groups are synced to AWS Secrets Manager, GCP Secret
  Manager or Azure Key Vault; Doppler and Infisical can be synced in read-only.
  Environment groups can carry files, mounted at `/etc/secrets/<group>`.

### Preview environments and branches

- A **preview template** per application: which add-ons (databases, Helm
  charts) to create alongside, and overrides for any setting including
  variables and resources — from the dashboard or a separate preview
  `porter.yaml`.
- Porter opens a PR adding the preview workflow; previews then appear for every
  pull request and are **destroyed when the PR is closed or merged**.
- **Environment groups and custom domains are stripped** from preview copies by
  default, "because environment groups are typically used to define values that
  are specific to a particular environment" — which is also where datastore
  credentials live.
- Several apps (even across repositories, matched by branch name) can share one
  preview environment.

### Templates and catalogue

- No app catalogue in the Coolify sense: the add-ons list above (about a dozen)
  plus any public Helm chart. Example repositories (Next.js, FastAPI, Strapi,
  Craft CMS) are starting points, not one-click installs.

### CLI, API, IaC, integrations

- CLI: `apply`, `app` (run, logs, build, push, update, rollback, manifests),
  `job`, `env`, `datastore connect`, `registry`, `target`, `clusters`,
  `cloud-accounts`, `auth` (browser, token for CI).
- `porter.yaml` v2: build or image, services, env, env groups, predeploy,
  initialDeploy, autoRollback, EFS storage; multi-app files with datastores and
  Helm add-ons via `porter apply`.
- **Remote MCP server** at `mcp.porter.run` (OAuth in a browser, so not usable
  in CI — the docs say to use the CLI there): read tools for projects, apps,
  revisions, metrics, logs (with bearer tokens and secret-shaped values
  redacted), notifications, jobs, nodes, pods, load balancers; write tools
  `create_app`, `update_application` (**dry run by default**),
  `redeploy_application`, `trigger_job_run`; `connect_github` returns the exact
  browser action a user must take. `curl -fsSL https://agents.porter.run | sh`
  installs the CLI, registers the MCP server and installs Porter's skills.
- **Sandboxes (AWS only):** isolated containers for untrusted or agent-written
  code, with Python and TypeScript SDKs, a warm pool for sub-second start,
  snapshots, volumes, TTLs and egress allowlists.
- GitHub Actions (`setup-porter`, `porter-cli-action`).

### Notifications

- Notification groups with Slack, email and PagerDuty channels, attached to
  individual apps; events are crash loop, OOM and non-zero exit. Deploy events
  are visible in GitHub Actions and the Activity tab.

### Multi-server and networking

- Node groups (default three), Karpenter autoscaling, GPU groups, private
  clusters, custom networking, VPC peering to existing VPCs, static egress IPs.
- Several clusters and clouds in one project; "Clone" copies an app to the same
  or another cluster (inline variables, env groups and domains are reset).
- Cluster provisioning takes 30–45 minutes.

### Team and collaboration

- Projects with collaborators, three roles, JIT provisioning, project-wide
  environment groups shared by apps, audit logs, notification groups.

### Developer experience and onboarding

- Quickstart from sign-up to URL, a "Kubernetes 101" page, a Heroku guide that
  converts a `Procfile` to `porter.yaml`, and hands-on migration help from the
  team (the most praised part — see below).
- `llms.txt` at `docs.porter.run` with explicit agent instructions.
- Credit card required; no free tier.

## What users love

- **Cost against Heroku at scale.** "even at ~$18k/mo on Heroku spend we're now
  spending less than half with Porter" (Hacker News, 2024-05-23); "the cost
  savings have been tremendous" (HomeLight engineer, same thread).
- **The team.** "the Porter team went above and beyond for us on this process
  and made it so easy for us" (second engineer on that migration, same thread);
  "they've helped us scale effortlessly" (Woflow, same thread).
- **Stability.** "the platform as stable as you can get" (HomeLight, same
  thread). The homepage testimonial from Toma cites "versioned rollbacks,
  observability out-of-the-box, and one-click inference deployments".
- **No lock-in by design.** "If you stop paying for Porter, Porter will stop
  managing your cluster" but "your servers will continue to run" (pricing FAQ);
  the eject idea was the most discussed part of the 2024 launch.
- **Cloud credits.** Infrastructure is billed by the cloud, so AWS/GCP/Azure
  startup credits pay for it (docs, pricing page).

## What users complain about

- **A floor of a few hundred dollars before any app runs.** "Their 'default
  infrastructure' is $300 a month set up in AWS plus usage costs on Porter. It's
  already 6x what I'm spending on Heroku" (Hacker News, 2023-06-14). Porter's
  own 2026 docs put the default cluster at ~$201/month on AWS, and the co-founder
  tells small users to go to Fly.io or Render instead.
- **Paying per CPU for CPUs Porter does not run.** "why would I pay usage costs
  per CPU to Porter if they're not running any CPUs?" (same thread). Flightcontrol
  makes the same point in its comparison: "your Flightcontrol cost doesn't
  increase as your traffic increases" (competitor-authored).
- **No free tier, card required.** "Credit card paywall" (Hacker News,
  2024-05-23); "the pricing seems high" (same thread).
- **More operations than Heroku, less mature than hoped.** From a customer who
  recommends it: "There is definitely still some more devops overhead compared
  to Heroku, and I wish the product was a bit more mature" (Hacker News,
  2024-05-23).
- **The open-source repository disappeared.** "I noticed that the link to
  GitHub in the footer 404s however. I was hoping this was OSS" and "your github
  repo says it was archived" (same thread). As of 2026-09-30 it is not public at
  all.
- **Builds tied to GitHub Actions.** Builds need the Porter GitHub App and a
  workflow PR (or your own CI), and build steps cannot see secrets or private
  databases (Porter's docs; Flightcontrol's comparison makes it a selling point).
- **AWS-first.** Datastores, persistent storage and sandboxes are AWS-only;
  Azure setup is manual (docs, 2026-09-30).
- **Upgrades are partly the customer's job.** Fewer than three replicas, no
  health checks or no graceful shutdown means "significant disruption" during
  the twice-yearly upgrades (docs).
- I could not find Reddit or review-site discussion of Porter under its own
  name — web searches return the other Porters — so the public record above is
  thin and mostly from Hacker News launch threads (2023–2024).

## Security record

- **No CVEs found** for Porter Technologies' platform: NVD keyword searches for
  "porter-dev" and "porter.run" return 0 results (2026-09-30), and the platform
  repository is no longer public, so there is no GitHub advisories page to
  read. With the code closed, the absence of CVEs says little either way.
- **Name collisions to ignore:** the Porter (porter.in, logistics) data breach
  of September 2024 and `getporter/porter` (CNAB) are different organisations.
- **Relevant exposure:** Porter's clusters run ingress-nginx, which Kubernetes
  retired in March 2026 (announced 2025-11-11, best-effort maintenance until
  March 2026); Porter responded by building Chainguard's fork
  (`porter-dev/ingress-nginx`, created 2026-01-27). The IngressNightmare CVEs of
  March 2025 (CVE-2025-1974, CVSS 9.8) applied to the same controller; I found no
  Porter statement about them.
- **Structural points in its favour:** no static cloud keys (role assumption and
  workload identity), secrets in the customer's secret manager, logs and metrics
  kept in the customer's cluster, a read-only AI agent, secret redaction in MCP
  log output, and write tools that dry-run by default.

## Against Skifity

Skifity statuses follow `docs/checklist.md`: almost everything cluster-facing is
**Written, never run** (ADR-0010). Porter's column describes a product with
paying customers; Skifity's describes code. Read the verdicts with that in mind.

| Capability | Porter | Skifity (evidence) | Verdict |
|---|---|---|---|
| Where it runs | Hosted control plane; EKS/GKE/AKS in your cloud account | One binary on your own server, k3s installed over SSH (`internal/provision`, `installer/install.sh`) | Different category |
| Minimum cost to start | ~$165–253/month of cloud infrastructure plus Porter fees | A 1 GB VPS; panel 35 MiB idle (`docs/performance.md`); k3s not measured | Skifity ahead |
| Git → build → deploy | GitHub App + GitHub Actions workflow PR; other CI via CLI | In-cluster build Job (Railpack/Nixpacks/Dockerfile), GitHub/GitLab/Gitea webhooks (`internal/builder`, `internal/gitsrc`); Written | Parity (Skifity needs no CI and no GitHub) |
| Secrets during build | Not available to builds | Variables marked "needed while building" (`store.Variable.BuildTime`) | Skifity ahead |
| Config change without rebuild | Env change redeploys | Build fingerprint (ADR-0007) | Skifity ahead |
| Pre-deploy migration step | `predeploy` job | `ReleaseCommand` run as a Job before traffic (`internal/deploy/run.go:130`); Written | Parity |
| Run once on first deploy | `initialDeploy` | None | Behind |
| Automatic rollback on failure | `autoRollback` | Rollout waits 10 minutes and reports `RolloutTimedOut`; old instances keep serving via `maxUnavailable: 0`, but the Deployment is not reverted (`internal/deploy/deployer.go:378-394`, `internal/kube/manifests.go:181`) | Behind |
| One-click rollback | Activity tab, CLI | Rollback restores image and settings (`internal/api/deploy_handlers.go:126`); Written | Skifity ahead (settings too) |
| Service types | Web, worker, job | App with or without domains; scheduled commands per app (`/api/apps/{app}/jobs`) | Parity |
| One-off commands | `porter app run` including interactive `bash` | One-off command in the app's image, logged (`POST /api/apps/{app}/run`); no interactive shell by decision (`docs/roadmap.md`) | Different by design |
| Custom domains and TLS | cert-manager, ALB/ACM option | cert-manager on first domain, `sslip.io` default (ADR-0015), wildcard setting (`internal/settings/settings.go`); Written | Parity |
| Per-app firewall | ALB + WAFv2 on AWS | IP/country/ASN rules at the edge (`internal/edgerules`, `internal/guard`); Written | Skifity ahead for self-hosters |
| Managed databases | RDS, Aurora, ElastiCache (AWS only); in-cluster PG for dev | CloudNativePG PostgreSQL, Redis, MySQL (`internal/dbsvc/manager.go:160-165`); Written | Parity in kind (Porter's are cloud-managed) |
| Database from a laptop | `porter datastore connect` | None (no port-forward in `internal/cli`/`internal/api`) | Behind |
| Backups | Cloud provider's | Scheduled logical dumps and volume tars to S3, restore (`internal/backup`, ADR-0012); Written, never taken | Skifity ahead on paper |
| Persistent storage | EFS (AWS only), shared | PVC per volume, Recreate strategy, optional Longhorn (`internal/settings/settings.go` Components); Written | Parity |
| Autoscaling | HPA, KEDA custom metrics, Temporal queues | HPA on CPU/memory, KEDA scale-to-zero (`internal/kube/scaletozero.go`); no queue-depth scaling | Behind on event-driven; ahead on scale-to-zero |
| Scaling safety check | Docs on 3 replicas and graceful shutdown | `check_scaling_readiness` (`internal/deploy/scaling.go`), 5 s `preStop` | Skifity ahead |
| Cluster upgrades | Managed twice a year | k3s version applies to new servers only (`internal/settings/settings.go:121`, used only in `internal/provision/provisioner.go:829`) | Behind |
| Logs | 7 days, searchable, time ranges | Live stream and previous container (`internal/api/stream_handlers.go:177-181`) | Behind |
| Metrics | 14 days in Prometheus | Point-in-time from metrics-server (`internal/kube/client.go:319`); panel's own `/api/metrics` | Behind |
| Alerts | Crash loop, OOM, non-zero exit → Slack, email, PagerDuty per app | 7 events → Telegram, Discord, webhook, email, plugin kinds; team-wide channels (`internal/notify/notify.go:41-47`, `internal/store/models.go:502`); Written | Parity (Porter routes per app; Skifity has more channel kinds) |
| AI debugging | DevOps Agent in the dashboard (BYO key, read-only) | Every error has cause/impact/fix and a copy-for-assistant button; MCP `get_app_logs previous` (`internal/errdoc`, `internal/mcpserver`) | Different approach; parity in intent |
| Sign-in and SSO | SSO via support; SAML is Enterprise | OIDC with PKCE for everyone, TOTP, Argon2id (`internal/auth/oidc.go`); Works | Skifity ahead |
| Roles | Admin, Developer, Viewer | owner, admin, member — member can deploy; no read-only role (`internal/store/models.go:9-11`, `docs/configuration.md:98-100`) | Behind (no Viewer) |
| Audit log | 30 days, filters, CSV/NDJSON export | 365 days, filter by action/target (`internal/store/retention.go:42`, `internal/api/teams_handlers.go:255`); no export | Parity (longer retention, no export) |
| Secrets handling | Synced to cloud secret managers; Doppler/Infisical | Envelope encryption bound to context, write-only (`internal/crypto`); Works (stored) | Parity |
| Preview environments | Template with add-ons (databases) and overrides; env groups and domains stripped; multi-app; `{PR_NUMBER}` domains | One app per preview, copies all variables, forks get no secrets, commit status and PR comment, 7-day reclaim (`internal/api/webhook_handlers.go` `deployPreview`, `internal/deploy/gitreport.go`, `internal/watch/watch.go:136`); no databases, no overrides | Behind (see P0) |
| Templates | ~12 add-ons + Helm charts | 282 one-click apps, pinned versions (`internal/templates/catalogue`) | Skifity ahead |
| CLI | Full CLI, token auth for CI | Full CLI, `--json` on every command, env-var auth for CI (`internal/cli`) | Parity |
| Config as code | `porter.yaml` + `porter apply`, preview overrides file | `skifity.toml` only names the app (`internal/cli/project.go:25-35`); export is one-way (`internal/cli/export.go`) | Behind |
| MCP | Remote, OAuth, not usable in CI; dry-run default for updates | Local stdio, token auth, works in CI (`internal/mcpserver/server.go:74`); no dry-run, no read-only mode, no tool annotations | Mixed: Skifity ahead for CI and self-hosting; behind on safety defaults |
| Agent skills | Installed with the CLI | None | Absent in Skifity |
| Agent sandboxes | Porter Sandboxes (AWS) | None | Absent in Skifity (out of scope) |
| Multi-server | Node groups, Karpenter, GPUs, multi-cluster | Several servers in one k3s cluster, one panel per cluster (`docs/roadmap.md`); Written | Different scale |
| Team | Projects, env groups, notification groups | Teams, projects, environments, project-wide shared variables (`store.SharedVariable`) | Parity |
| Clone an app | To same or another cluster | None (grep `clone` in `internal/api`, `internal/store`: none) | Behind |
| Export / no lock-in | Stop paying, keep the cluster | `skifity export`: JSON plus each app's Kubernetes objects; Works | Parity |
| Open source | Closed since 2024 | Public repository | Skifity ahead |
| UI languages | English | Five, CI-enforced (`web/src/locales`) | Skifity ahead |

## Gaps worth closing in Skifity

**What fits and what does not.** Porter's business is operating EKS for
companies with cloud credits, and much of its surface — node groups,
Karpenter, RDS/Aurora wiring, SOC 2 "one click", static egress IPs, per-vCPU
billing — only exists because of that. What transfers to a single binary on a
VPS is the application layer Porter built on top: previews that do not touch
production, a config file you can commit, a safer MCP, metrics with a memory,
a Viewer role, and cluster upgrades that are somebody's job.

### P0 — Previews must not inherit production's database

- **What:** today a preview copies the app and **all** its variables
  (`copyPreviewVariables` in `internal/api/webhook_handlers.go`), and a
  database link is stored as an ordinary secret variable holding the full
  connection string (`internal/dbsvc/manager.go:342` `Link`), whose host is
  the production namespace's Service
  (`internal/dbsvc/manifests.go:144`, `<service>.<namespace>.svc.cluster.local`).
  The preview namespace's default-deny policy blocks traffic to other
  namespaces (`internal/kube/namespace.go:127-217`). So a same-repository
  preview of a database-backed app either cannot reach any database — its
  release command fails and the preview is broken — or, on a cluster whose CNI
  does not enforce NetworkPolicy, runs the pull request's migrations against
  production. Both are Written, never run.
- **Fix, in Porter's shape:** a per-app **preview template**: which linked
  databases get a fresh, empty (or seeded) copy in the preview environment,
  which are left out, and overrides for variables and resources. Variables
  written by a database link are never copied; the preview's own database link
  writes new ones. Cleanup already deletes the namespace, which takes the
  preview databases with it.
- **Evidence:** Porter creates add-ons (databases) per preview and strips
  environment groups — where datastore credentials live — "because environment
  groups are typically used to define values that are specific to a particular
  environment"; Qovery clones the databases into each preview. Vercel's preview
  workflow is the bar named in `docs/research/competitors.md`.
- **Fit:** `store.DatabaseLink` already records which variables came from a
  link (`internal/store/models.go:404`), so skipping them is a join; a small
  `preview_template` table; `internal/dbsvc` provisions into the preview
  namespace; a "Previews" section on the app's settings tab.
- **Size:** M.
- **Without a cluster:** the copy rules, template storage and API are unit
  tests against the `fakeCluster` that `internal/api/api_test.go:426` already
  uses; a database actually starting in a preview namespace needs a cluster.

### P1 — `skifity apply`: the app described in a file

- **What:** a committed `skifity.yaml` (or extend `skifity.toml`) describing
  source or image, build settings, start and release commands, port, health
  path, scaling, volumes, scheduled commands, non-secret variables, and the
  preview template above; `skifity apply` shows a diff and applies it through
  the existing API; `skifity export --app` writes the same format so the round
  trip is proven.
- **Evidence:** `porter.yaml` is Porter's configuration-as-code and its CI
  path; Qovery has a Terraform provider and exporter; Sealos templates are YAML.
  Skifity's `skifity.toml` only records which app a folder belongs to
  (`internal/cli/project.go:25-35`) and the JSON export cannot be applied back.
- **Fit:** `internal/cli` (parse, diff, apply), no new server routes needed
  beyond what `PATCH /api/apps/{app}` and friends already take.
- **Size:** M.
- **Without a cluster:** yes — parsing, diffing and API calls against the
  real panel in `make smoke`.

### P1 — MCP that is safe by default

- **What:** mark every tool with MCP `readOnlyHint`/`destructiveHint`
  annotations; give mutating tools a `dry_run` that defaults to true where the
  change is a setting (`set_variable`, `scale_app`, `rollback_app`), returning
  the resulting configuration and whether it rebuilds; and a
  `skifity mcp --read-only` mode that registers only read tools.
- **Evidence:** Porter's `update_application` "defaults to a dry run"; Qovery's
  MCP server is read-only unless `read_write=true` and a console setting are
  both set. Skifity's tools have neither (`internal/mcpserver/server.go`,
  tools at lines 314-391, no annotations). Skifity's scoped tokens
  (`internal/auth/scopes.go`) already make a read-only token possible; the MCP
  layer does not say so.
- **Size:** S.
- **Without a cluster:** yes — `internal/mcpserver/server_test.go` style tests.

### P1 — Metrics with a memory

- **What:** sample each instance's CPU and memory (already read from
  metrics-server) every minute into SQLite, downsample to 5-minute and 1-hour
  points, keep 14 days, and draw them on the app page and in
  `get_app_status`.
- **Evidence:** Porter keeps 14 days in Prometheus; Qovery sells Observe at
  $299 per cluster per month; a Qovery reviewer lists "integrated monitoring" as
  a missing key feature (G2/AWS Marketplace, 2025-06-10). Skifity shows only the
  current value (`internal/kube/client.go:319`), and the full Prometheus stack
  costs ~900 MB (`internal/settings/settings.go` Components) — too much for the
  1 GB VPS Skifity targets.
- **Fit:** the minute tick in `internal/serverapp`, a table and retention in
  `internal/store/retention.go`, a chart built from shadcn/ui on the app page.
- **Size:** M.
- **Without a cluster:** the sampler against a fake metrics client, the
  downsampling and retention in unit tests, the chart in Playwright with
  fixture data; real numbers need a cluster.

### P1 — A read-only Viewer role

- **What:** a fourth role below member that can read everything in a team and
  change nothing — for managers, support staff and auditors.
- **Evidence:** Porter (Admin/Developer/Viewer, and JIT users default to
  Viewer) and Qovery (Viewer, Billing Manager) both have it. Skifity's member
  "can deploy" (`docs/configuration.md:98-100`), so there is no safe role to
  give an observer.
- **Fit:** `internal/store/models.go` role rank, `authorizeTeam` and friends in
  `internal/api/api.go` already take a required role; the route-walk test
  already exercises every route.
- **Size:** S.
- **Without a cluster:** yes — the route walk proves every write refuses a
  viewer.

### P1 — Upgrade k3s on servers that already exist

- **What:** from Settings → Cluster, move every node to a newer k3s patch or
  minor release, control plane first, one node at a time with a drain, and
  refuse or warn when an app has one instance and would go down (the scaling
  readiness checker already knows which).
- **Evidence:** Porter upgrades clusters twice a year and publishes a
  shared-responsibility model; Qovery manages upgrades and stays one or two
  minors behind the provider; Sealos' open issues #5657 and #7326 show what
  happens when upgrades are left to users. In Skifity the k3s version setting
  only affects servers added later (`internal/settings/settings.go:121`, read
  only in `internal/provision/provisioner.go:829`), so a cluster installed today
  stays on today's Kubernetes until somebody SSHes in.
- **Fit:** a new operation kind in `internal/provision` (steps, retry, shown as
  they happen, like adding a server), scripts through `internal/shellsafe`.
- **Size:** M.
- **Without a cluster:** the generated scripts and the ordering against the
  in-process SSH server in `internal/sshx`; the upgrade itself needs a
  cluster — and is exactly the kind of thing `test/cluster/verify.sh` exists
  for.

### P2 — Revert automatically when a rollout fails

- **What:** when the new instances never become ready, roll the Deployment back
  to the previous revision and record the deployment as failed-and-reverted,
  instead of leaving the new ReplicaSet stuck beside the old one.
- **Evidence:** `autoRollback` in `porter.yaml`; Qovery's RollingUpdate
  "automatically rollback[s] if the new version fails to start". Skifity keeps
  serving (`maxUnavailable: 0`) but stops at `RolloutTimedOut`
  (`internal/deploy/deployer.go:378-394`).
- **Size:** S–M. **Without a cluster:** the decision logic yes; the revert
  needs a cluster.

### P2 — Scale workers on queue length

- **What:** KEDA is already installed for scale-to-zero; offer a Redis list or
  RabbitMQ queue length as a scaling signal for apps with no domain.
- **Evidence:** Porter (custom Prometheus metrics, Temporal queues) and Qovery
  (KEDA scalers) both sell it.
- **Size:** M. **Without a cluster:** the rendered `ScaledObject` in golden
  tests; the scaling needs a cluster.

### P2 — Smaller items Porter has and Skifity does not

- **Run once on the first deploy** (`initialDeploy`) for seeding. S, no
  cluster for the logic.
- **Route notifications per project or app**, as Porter's notification groups
  do; Skifity's channels are team-wide (`internal/store/models.go:502`). S, no
  cluster.
- **Export the audit log** as CSV/NDJSON with the current filter. S, no
  cluster.
- **Clone an app** into another environment, without its secrets and domains.
  S, no cluster for the store part.

## Things to deliberately not copy

- **A fixed infrastructure floor.** Porter's three default node groups — two
  for the system, one just for monitoring — are why it costs ~$200/month before
  an app runs. Skifity's optional components that "say what they cost first"
  (`internal/settings/settings.go` Components) are the opposite, and the right
  one for a 1 GB VPS.
- **Charging per vCPU of the user's own hardware.** It is Porter's business
  model and the most repeated complaint about it; a self-hosted panel has no
  business charging for anything.
- **Builds that live in GitHub Actions.** Requiring a GitHub App, a workflow PR
  merged before the first deploy, and builds that cannot see secrets are all
  consequences of not having a builder in the cluster. Skifity's in-cluster
  build Job supports GitLab, Gitea and a plain folder.
- **Closing the source after launch.** Porter's MIT repository became an archive
  and then disappeared; the 2024 launch thread noticed immediately.
- **OAuth-only remote MCP.** Porter's own docs say it "cannot be used in
  headless environments such as CI pipelines". Skifity's token-based stdio MCP
  works in CI and in an assistant's sandbox, which is where agents actually run.
  A remote endpoint, if ever added, should take the same tokens.
- **SSO as an enterprise upsell.** Skifity gives OIDC to everyone; keep it that
  way.
- **"Run three replicas or upgrades will hurt you" as policy.** On a one-server
  install that is impossible. Skifity's version should instead name the
  single-instance apps that will blink during an upgrade, before it starts.
- **An in-dashboard AI agent that needs the user's API key.** Porter's DevOps
  Agent is well-designed (read-only, context-aware, runs in the customer's
  cluster), but it means the panel stores a model key and calls a model
  provider. Skifity's "never phones home" promise and its MCP-first design put
  the assistant on the user's side, where the key already is. If this is ever
  wanted, it is a plugin.
- **Agent sandboxes.** A different product for a different buyer.

## Sources

All read 2026-09-30.

- https://docs.porter.run/llms.txt (full docs index)
- https://docs.porter.run/getting-started/introduction.md and https://docs.porter.run/getting-started/concepts.md
- https://docs.porter.run/cloud-accounts/overview.md (default node groups, ~$201/$253/$165 per month)
- https://docs.porter.run/cloud-accounts/connecting-a-cloud-account.md
- https://docs.porter.run/cloud-accounts/node-groups.md
- https://docs.porter.run/cloud-accounts/cluster-upgrades.md
- https://docs.porter.run/applications/deploy/builds.md
- https://docs.porter.run/applications/deploy/duplicating-apps.md
- https://docs.porter.run/applications/configure/basic-configuration.md (sleep mode)
- https://docs.porter.run/applications/configure/autoscaling.md
- https://docs.porter.run/applications/configure/environment-groups.md
- https://docs.porter.run/applications/configuration-as-code/reference.md
- https://docs.porter.run/applications/observability/logging.md, monitoring.md, alerts.md, devops-agent.md
- https://docs.porter.run/preview-environments/overview.md and defining-overrides.md
- https://docs.porter.run/addons/overview.md and datastores.md
- https://docs.porter.run/security-and-compliance/role-based-access-control.md, audit-logs.md, soc2-hipaa.md
- https://docs.porter.run/sandboxes/overview.md
- https://docs.porter.run/mcp/tools.md
- https://docs.porter.run/standard/cli/command-reference/porter-app.md
- https://www.porter.run/pricing
- https://www.porter.run/ (homepage; `/porter-cloud` redirects here)
- https://www.porter.run/blog/effortless-app-infrastructure-in-any-cloud-porters-20m-series-a (dated 2026-01-27)
- https://www.porter.run/blog/our-journey-building-porter ($1.5M seed, 2021)
- https://github.com/porter-dev/porter (404) and https://github.com/orgs/porter-dev/repositories
- GitHub API organisation search `org:porter-dev` (public repositories and stars)
- https://sourceforge.net/projects/porter.mirror/ (mirror of porter-dev/porter, last updated 2024-05-22, v0.52.56)
- https://news.ycombinator.com/item?id=40456959 (Show HN: Porter Cloud, 2024-05-23)
- https://news.ycombinator.com/item?id=36327347 (Heroku to EKS via Porter discussion, 2023-06-14)
- https://news.ycombinator.com/item?id=37396676 (moved away from Terraform, 2023-09-05)
- https://www.flightcontrol.dev/porter-alternatives (competitor-authored)
- https://www.qovery.com/blog/porter-alternatives (competitor-authored; describes Porter as open-source and self-hosted, which is out of date)
- https://www.kubernetes.dev/blog/2025/11/12/ingress-nginx-retirement/
- https://nvd.nist.gov (API keyword searches "porter-dev", "porter.run")
- Skifity sources cited inline, read in this repository on 2026-09-30.
