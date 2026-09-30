# Qovery

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Researched 2026-09-30 against live sources (Qovery's docs, pricing page,
changelog and `llms.txt`, the `Qovery` GitHub organisation through the GitHub
API, AWS Marketplace reviews syndicated from G2, Hacker News, NVD). Every number
carries its source and the date it was read.

Qovery (Paris, founded 2020, Techstars) sells itself in 2026 as "the Kubernetes
control plane for humans and AI agents" — an internal developer platform that
provisions and operates EKS, GKE, AKS or Scaleway Kapsule clusters **in the
customer's own cloud account**, or attaches to any existing Kubernetes 1.24+
cluster ("Bring Your Own Kubernetes"), and gives developers self-service
environments on top: applications, databases, cron jobs, lifecycle jobs, Helm
charts and Terraform services, grouped Organization → Project → Environment,
with preview environments per pull request. It is for engineering
organisations of roughly 10–200 people with no platform team (its buyer's
guides target "10 engineers and no platform team"), in regulated industries
(fintech, healthcare, insurtech) and, since 2025, for teams letting AI coding
agents deploy. **Architecture:** a hosted, proprietary control plane (console,
API, `api.qovery.com`); in each cluster a Qovery agent and a shell agent that
connect **outbound** to the API (only 80/443 inbound for apps), plus
ingress-nginx, cert-manager, external-dns and metrics-server if absent; the
open-source **Qovery Engine** (Rust, GPL-3.0, 2,466 stars on 2026-09-30)
executes deployments by combining Terraform, Helm and `kubectl`; managed
clusters autoscale nodes with Karpenter; observability (the paid "Observe"
add-on) is Prometheus + Thanos + Loki inside the customer's cluster. The
control plane can be self-hosted, air-gapped, from a Helm chart — on the
Enterprise plan only. **Licence:** engine and console GPL-3.0 (console 227
stars), CLI public (84 stars); the control plane is closed. **Pricing (read
2026-09-30):** Business **$2,999 per month billed yearly**, 20 users, up to 3
managed clusters (+$399 each), 10,000 build-and-deploy minutes then $0.16/min,
a 4 vCPU/8 GB builder, preview environments, 30-day audit log; Enterprise is
custom (BYOK, self-hosted control plane, custom roles, SSO, policy-as-code,
HIPAA/HDS/ISO 27001/DORA package); add-ons: Observe $299 per cluster per month,
a larger builder $150/month. 14-day trial, no free plan. AI agents "don't
consume user seats". **Maturity and adoption:** a changelog every two weeks
(latest 2026-09-23); "+200 companies" and "4.8 on G2 with +80 reviews" (pricing
page, 2026-09-30); 4.7/5 from 69 G2 reviews syndicated to AWS Marketplace (read
2026-09-30); 40+ case studies; **$13M Series A led by IRIS on 2025-09-30**
(Tech.eu), after a $4M seed in 2021. One piece of history matters for Skifity:
from 2022 to 2024 Qovery offered a single-EC2 **k3s** cluster for low-cost use
and decommissioned it in November 2024 because "most usage came from individual
developers looking for a simple way to deploy side projects with a very high
churn rate", the setup "lacked node autoscaling", and supporting it "consumed
significant resources" (Qovery changelog, 2024-11-05).

## Feature inventory

### Deploy sources and builds

- Git (GitHub, GitLab, Bitbucket) with auto-deploy on push, or container
  registries (ECR, GAR, ACR, Scaleway, GitHub and GitLab registries, Docker Hub,
  generic). Public Git repositories without a token since 2024.
- Built-in CI on the customer's infrastructure: Dockerfile builds; the Agent
  Skill generates a Dockerfile when a repository has none.
- **Build variables arrived only in September 2026:** "Environment variables
  only applied at runtime before this"; now `ARG NAME` bakes a value into the
  image and `RUN --mount=type=secret,id=NAME` mounts it for one step "never
  written to a layer" — the Dockerfile decides (changelog 2026-09-23).
- Service kinds: application, database, cron job, **lifecycle job** (runs on
  environment deploy, stop or delete, can output variables to other services),
  Helm chart, Terraform/OpenTofu service.
- **Deployment pipeline:** ordered stages within an environment (database
  first, then API, then workers), with skipped services.
- Deployment strategies: RollingUpdate with automatic rollback if the new
  version fails to start, or Recreate.
- Integrations with GitHub Actions, GitLab CI, CircleCI, Jenkins and Argo CD.

### Domains, TLS and routing

- Automatic HTTPS through Let's Encrypt; one CNAME per custom domain; wildcard
  domains for environments; custom preview URLs through the CLI.
- ALB controller on AWS with advanced settings; ingress-nginx elsewhere;
  documented rate limiting, IP-header authorization and egress filtering
  through a Squid proxy; static egress IPs on EKS and GKE.
- Qovery patched ingress-nginx for CVE-2025-1974 across all managed clusters in
  about a day (changelog, 2025-03-26) and told self-managed users to upgrade
  themselves.

### Databases and services

- PostgreSQL, MySQL, MongoDB, Redis, in two modes: **container** (one instance
  with a volume, for development) or **managed** (RDS, Cloud SQL, Azure
  Database, Scaleway — the cloud's backups and maintenance windows apply).
- **Blueprints** (2026): a versioned catalogue (`Qovery/service-catalog`) of
  Terraform or Helm templates — RDS PostgreSQL 14–17, RDS MySQL, ElastiCache
  Redis/Valkey, Memorystore, Scaleway databases, MSK, RabbitMQ, S3, CloudFront,
  and external SaaS (MongoDB Atlas, PlanetScale, Timescale, Confluent, Aiven,
  Redpanda, Cloudflare DNS and Workers, Temporal Cloud, Bedrock access). A
  blueprint stays linked to its template and offers upgrades with a diff; since
  2026-09-23 an existing RDS instance can be adopted into a blueprint by
  Terraform import.
- Cloning a database service copies its configuration, "not the data".

### Storage and backups

- Persistent volumes for container services; object storage through
  blueprints (S3, Scaleway).
- Managed databases keep the provider's automated snapshots, and Qovery "does
  not delete automated snapshots and backups on deletion". Container-mode
  databases have no documented backup.
- A long disaster-recovery guide (RTO/RPO tiers, pilot light, warm standby,
  cross-region with the Terraform provider) — guidance rather than a feature.

### Scaling and high availability

- HPA on CPU (and memory); **KEDA** event-driven autoscaling on SQS, RabbitMQ,
  Kafka, Kinesis, databases, Prometheus, Datadog or cron.
- Karpenter node autoscaling on managed clusters ("new nodes ready in ~2
  minutes"), Spot support, multi-AZ node pools on AWS.
- **Deployment rules:** start and stop environments on a schedule, matched by
  wildcard (`dev-*`, `[PR]*` for every preview), per project or per
  environment, with a priority stack — "reduce your cloud costs up to 60%".
- Managed Kubernetes upgrades: production clusters upgraded on a published
  schedule, non-production clusters upgradable on demand first, staying "1 or 2
  minor versions below the last one offered by the cloud provider". BYOK users
  upgrade their own Kubernetes and dependencies.

### Observability (logs, metrics, alerts, uptime)

- Included: live application logs, live metrics, service status, deployment
  logs and history (clusters got the same history on 2026-09-23).
- **Observe (paid, $299 per cluster per month, "not yet self-service"):**
  Prometheus (7 days) + Thanos (15 days raw, 30 days downsampled), Loki with
  **12-week** log retention, Kubernetes events, P50/P90/P99 latency, 5xx/499
  rates per endpoint, automatic error-log counting, alerts. Data stays in the
  customer's cluster.
- Datadog and Kubecost integrations.

### Security, auth, roles, SSO, audit

- Five built-in roles — Owner, Admin, DevOps, Billing Manager, Viewer — plus
  custom roles scoped to projects and clusters (unlimited custom roles are
  Enterprise).
- SSO (SAML and OIDC) on Enterprise.
- API tokens with roles; **API Policy Tokens (beta)** that constrain a
  credential's allowed operations whatever client uses it.
- Audit logs of every API call with the tool used (console, Terraform, Git
  push), a JSON view of the post-change object, filters; retention 30 days on
  Business, custom on Enterprise (the docs still list 3 and 7 days for "User"
  and "Team" plans that the pricing page no longer sells).
- External secrets from AWS Secrets Manager, Parameter Store, GCP Secret
  Manager and Doppler; variables can be files.
- SOC 2 Type II and GDPR on Business; HIPAA, HDS, ISO 27001 and DORA packages
  on Enterprise.
- 2026-09-23: "Fixed an access-control gap where organization-wide resources
  could be reached beyond a user's actual permissions" (no CVE, no further
  detail published).

### Preview environments and branches

- A **blueprint environment** (a stopped, auto-deploy-off clone of a working
  environment) is copied for every pull request against the base branch:
  "The frontend, backend, PostgreSQL and Redis instances are cloned!" Variables
  and secrets are copied too.
- Per service you choose what goes into previews; databases are "clones or
  shared". Seeding is done by a lifecycle job (SQL scripts, production-like
  data; Qovery's own open-source Replibyte — "Seed your development database
  with real data", 4,415 stars on 2026-09-30 — is built for this).
- **Automatic** on every PR, or **manual**: a developer comments
  `/qovery preview` on the PR.
- Deleted when the PR is merged or closed, "after specified period of
  inactivity", or by hand; deployment rules can stop them at night.
- **Clone environment:** any environment to another project or cluster, with
  the choice of services; custom domains excluded.

### Templates and catalogue

- No one-click app store in the Coolify/Skifity sense. The catalogue is
  infrastructure — the blueprints above — plus any Helm chart from a
  configured Helm repository.
- Lifecycle-job examples repository (seed PostgreSQL, create RDS with
  Terraform).

### CLI, API, IaC, integrations

- One typed REST API behind every surface.
- **Terraform provider** (`qovery/qovery`) and a Terraform **exporter** that
  turns existing Qovery configuration into Terraform; Pulumi package; a
  CloudFormation integration.
- CLI: `application`, `container`, `database`, `cronjob`, `lifecycle`, `helm`,
  `environment` (clone, deploy, statuses, deployment explain), `env`, `log`,
  `shell` (interactive shell into a container), `port-forward`, `status`,
  `audit-log`, `terraform`, `cluster` (install, analysis cost-recommendation),
  `demo` (a local k3s-in-Docker cluster, "not intended for production").
- **AI (2025–2026):** a remote MCP server at `mcp.qovery.com/mcp`, OAuth,
  **read-only by default** (write needs `read_write=true` on the URL *and* a
  console setting); eight Agent Skills (`qovery`, `-onboard`, `-deploy`,
  `-troubleshoot`, `-optimize`, `-speedup`, `-preview`, `-terraform`) for 14
  named agent hosts; an AI Copilot (beta) in the console and in Slack; a
  "Securing AI agent access" guide with a Claude Code allowlist for the CLI;
  **Agent Tasks** (open beta 2026-09-23): Claude or Bedrock agents run as
  services in the customer's cluster, triggered by cron or webhook, in place or
  in a cloned environment (clones deleted after 3 days, 60 clones/hour cap);
  an open-source Heroku-to-AWS migration agent.

### Notifications

- Organisation webhooks on deployment started, succeeded, cancelled and
  failed, in Qovery's format or Slack's; Slack and email integrations; alerts
  from Observe; a red dot in the navigation for failing clusters, with email
  notifications for cluster failures since late 2024.

### Multi-server and networking

- Any number of clusters across AWS, GCP, Azure, Scaleway and on-premise
  (BYOK: vanilla, Rancher, OpenShift, k3s/k3d, EKS Anywhere, vSphere);
  environments can target clusters by deployment rule ("run development on
  Scaleway, production on AWS").
- VPC peering, deploy into an existing VPC, private endpoints, static IPs.

### Team and collaboration

- Organisations with members, invitations, roles; projects and environments;
  variables at project, environment and service scope with aliases and
  interpolation; labels and annotations groups; audit logs; shared Slack
  channel with Qovery's engineers on every plan.

### Developer experience and onboarding

- Two onboarding paths: "Install the Qovery Skill ... Say 'Deploy my
  application with Qovery'" (recommended for new users), or the console.
- Cluster creation on a connected cloud account (IAM permissions documented per
  cloud); BYOK needs 4 CPUs and 8 GB free and "is for Kubernetes experts only".
- Web shell into running containers from the console (since April 2024).
- A month (Business) or three (Enterprise) of onboarding with a solution
  engineer; migration funding "up to $300k credits" through cloud partners.

## What users love

- **Kubernetes without the Kubernetes.** "simple app deployment, no DevOps
  required" (G2 via AWS Marketplace, 2025-09); "zero to production in minutes"
  (2025-04); reviewers describe it as "a breath of fresh air" after Kubernetes
  and cloud consoles.
- **Support.** Several reviews single out the solution engineers (2025-04,
  2025-06); every plan includes a shared Slack channel.
- **Environments.** Cloning and preview environments are named as the features
  people use (2025-06).
- **Their own cloud.** Credits and committed-spend discounts apply; data stays
  in their VPC; Qovery is on AWS Marketplace so spend counts toward an EDP.
- **Engineering reputation.** "Rust in Production: Qovery" reached 127 points
  on Hacker News (2021); Replibyte (database seeding) is its most-starred
  repository.

## What users complain about

- **Price, and price changes.** "The frequent pricing changes, often trending
  upwards, are a bit frustrating" (G2 via AWS Marketplace, 2025-06-05). The
  history is visible: per-deployment pricing ($50 per 100 deployments) with a
  free plan until August 2022, then per active user "starting from
  $49/month/user" (Qovery blog, 2022-08-18), then User/Team/Business tiers, and
  in 2026 a single $2,999/month Business floor with no free plan — the
  audit-log docs still describe the retired "User" and "Team" plans.
- **Builds slower than Heroku.** "build times a little bit longer than that"
  compared with Heroku's sub-minute builds (2025-04-18).
- **Abstraction limits.** "trade offs if there are specific Kubernetes features
  you want to leverage that are not yet supported" (2025-06-24).
- **Gaps that took years to close.** "Some key features missing: running
  one-off containers, integrated monitoring (incoming)" (2025-06-10) —
  monitoring became a paid add-on that is "not yet self-service"; build-time
  variables only landed in September 2026.
- **Documentation for anything unusual.** "non-common OSS deployment
  methodologies are not documented well" (2025-06-20); "we can't define Env Vars
  in the UI for self-managed charts" (2025-06-06).
- **Setup is still technical.** "Some setup parts are technical" (2025-09-01);
  BYOK is labelled "For Kubernetes Experts Only".
- **A crowded, hard market.** On Hacker News the founder of Digger, a similar
  "DX platform for your cloud account", warned in 2023 that "20+ other
  passionate teams" had chased the same idea; Qovery survived it by moving up
  to enterprise pricing and governance.
- Public reviews are almost all 4–5 stars (88% five-star on the syndicated G2
  set), so complaints surface as the "dislikes" paragraphs of positive reviews
  rather than as negative ones. I found no substantial Reddit discussion.

## Security record

- **No CVEs:** NVD keyword search "qovery" returns 0 results; the
  `Qovery/qovery-cli` repository has no published advisories (both read
  2026-09-30).
- **Disclosed without a CVE:** the access-control fix of 2026-09-23 ("organization-wide
  resources could be reached beyond a user's actual permissions") — the same
  class as Coolify's cross-team bypass (CVE-2026-34592), and the class Skifity's
  route walk in `internal/api` exists to catch.
- **Supply chain handled well:** the ingress-nginx CVE-2025-1974 (CVSS 9.8) was
  patched on all managed clusters between 09:00 on 2025-03-24 and 2025-03-25,
  with a public timeline. Ingress-nginx itself was retired by Kubernetes in
  March 2026, and Qovery still installs it for BYOK clusters.
- **Posture:** SOC 2 Type II; CodeQL, secret scanning and Dependabot on their
  repositories (Qovery security page); agents reach out to the API rather than
  the API reaching into clusters; read-only-by-default MCP; the CLI install
  script's docs say plainly that it "does not verify the binary's integrity" and
  show how to check the checksum.

## Against Skifity

Skifity statuses follow `docs/checklist.md`: almost everything cluster-facing is
**Written, never run** (ADR-0010). Qovery's column describes a product with
paying customers; Skifity's describes code.

| Capability | Qovery | Skifity (evidence) | Verdict |
|---|---|---|---|
| Where it runs | Hosted control plane; your EKS/GKE/AKS/Kapsule or any K8s | One binary inside the k3s cluster it installs on your servers (ADR-0013) | Different category |
| Minimum cost | $2,999/month plus the cloud bill | Free; a 1 GB VPS; panel 35 MiB idle (`docs/performance.md`) | Skifity ahead |
| Git → build → deploy | Built-in CI, GitHub/GitLab/Bitbucket | In-cluster Railpack/Nixpacks/Dockerfile Job, GitHub/GitLab/Gitea (`internal/builder`, `internal/gitsrc`); Written | Parity (Bitbucket vs Gitea) |
| Build without a Dockerfile | Agent Skill writes one | Railpack detection, framework detection before the first build (`internal/builder/detect.go`) | Skifity ahead |
| Build-time variables | Since 2026-09: `ARG`, or secret mounts never written to a layer | Since earlier; but values, including secrets, go as plaintext build args into the build Job (`internal/builder/job.go:452`, `:552`) | Parity in reach; behind on secret handling (see P1) |
| Config change without rebuild | Env change redeploys | Build fingerprint (ADR-0007) | Skifity ahead |
| Lifecycle / release step | Lifecycle jobs on deploy/stop/delete with outputs | Release command before traffic (`internal/deploy/run.go:130`); scheduled commands | Behind on stop/delete hooks and outputs |
| Deployment ordering | Pipeline stages | Template installs link databases first, then deploy all services at once (`internal/api/template_install.go:23-25`, `:155-161`); no ordering setting | Behind |
| Rollback | Automatic on failed start; manual | Manual rollback restores image and settings (`internal/api/deploy_handlers.go:126`); failed rollout is reported, not reverted (`internal/deploy/deployer.go:378-394`) | Mixed |
| HTTPS and domains | Let's Encrypt, wildcard domains | cert-manager, wildcard setting, `sslip.io` default (ADR-0015); Written | Parity |
| Edge protection | Rate limiting / IP header guides, ALB | Per-app firewall on IP, country, ASN, path, header (`internal/edgerules`, `internal/guard`); Written | Skifity ahead |
| Databases | PG, MySQL, MongoDB, Redis; container or cloud-managed; blueprints | PG (CloudNativePG), Redis, MySQL (`internal/dbsvc/manager.go:160-165`); Written | Behind (no MongoDB, no cloud-managed mode) |
| Reach a database from a laptop | `qovery port-forward` | None (no port-forward in `internal/cli`, `internal/api`) | Behind |
| Backups | Cloud provider's for managed DBs; none documented for container DBs | Scheduled logical dumps and volume backups to S3 with restore (`internal/backup`, ADR-0012); Written, never taken | Skifity ahead on paper |
| Autoscaling | HPA, KEDA event sources, Karpenter nodes | HPA, KEDA scale-to-zero (`internal/kube/scaletozero.go`); no node autoscaling (servers are added by hand) | Behind on event-driven and nodes; ahead on scale-to-zero |
| Scaling safety check | — | `check_scaling_readiness` (`internal/deploy/scaling.go`) | Skifity ahead |
| Stop environments on a schedule | Deployment rules with wildcards | Per-app replicas can be 0 (`internal/api/deploy_handlers.go:302`); no environment stop, no schedule | Behind |
| Kubernetes upgrades | Managed, scheduled, non-prod first | New servers only (`internal/settings/settings.go:121`, `internal/provision/provisioner.go:829`) | Behind |
| Logs | Live; 12 weeks with Observe (paid) | Live stream plus previous container (`internal/api/stream_handlers.go:177-181`) | Behind |
| Metrics | Live; history, latency, error rates with Observe (paid) | Point-in-time usage (`internal/kube/client.go:319`) | Behind |
| Notifications | Webhooks and Slack on 4 deploy events | 7 events → Telegram, Discord, webhook, email, plugin kinds (`internal/notify/notify.go:41-47`); Written | Skifity ahead |
| Sign-in | SSO (SAML/OIDC) on Enterprise | OIDC with PKCE for everyone, TOTP, Argon2id, lockout (`internal/auth`); Works | Skifity ahead |
| Roles | 5 built-in + custom roles | owner, admin, member (`internal/store/models.go:9-11`) | Behind (no Viewer, no custom) |
| API tokens | Role-bearing tokens, Policy Tokens (beta) | Tokens bound to one team with resource-scoped `apps:read`-style scopes (`internal/auth/scopes.go`); Works | Parity |
| Audit | Every API call, tool used, post-change JSON; 30 days on Business | Team audit log, 365 days (`internal/store/retention.go:42`) | Parity (Qovery richer, Skifity longer) |
| Secrets | Encrypted; external secret managers; file variables | Envelope encryption bound to context, write-only everywhere (`internal/crypto`); no files, no external managers | Parity on storage; behind on files and sources |
| Variable scopes | Project, environment, service; aliases | Project-wide shared and per app (`store.SharedVariable`, `store.Variable`) | Behind (no environment scope) |
| Preview environments | Clone of a blueprint environment incl. databases; on demand via `/qovery preview`; inactivity expiry | One app per preview, all variables copied, no databases; forks get no secrets; commit status and PR comment; 7-day reclaim (`internal/api/webhook_handlers.go` `deployPreview`, `internal/deploy/gitreport.go`, `internal/watch/watch.go:136`); Written | Behind (see P0) |
| Clone an environment | Yes, across projects and clusters | None (grep `clone` in `internal/api`, `internal/store`) | Behind |
| Templates | Infrastructure blueprints (~25) + Helm | 282 one-click apps with pinned, verified versions (`internal/templates/catalogue`) | Skifity ahead for apps |
| Helm charts as services | Yes | No (only the optional monitoring stack is Helm, installed by the user: `internal/settings/settings.go` Components) | Behind (deliberate) |
| IaC | Terraform provider and exporter | Export to JSON + Kubernetes objects, one way (`internal/cli/export.go`) | Behind |
| CLI | Broad, including `shell` and `port-forward` | Broad, `--json` everywhere, CI by env vars (`internal/cli`); no shell by decision (`docs/roadmap.md`) | Parity |
| MCP | Remote, OAuth, read-only by default | Local stdio, token-scoped, errors with cause/impact/fix (`internal/mcpserver/server.go`); no read-only mode | Mixed |
| Agent skills | Eight skills for 14+ hosts | None | Absent in Skifity |
| In-cluster AI agents | Agent Tasks, AI Copilot | None; `llms.txt`, copy-for-assistant errors | Absent in Skifity (by fit) |
| Multi-cluster / multi-cloud | Yes | One panel, one cluster, by decision (`docs/roadmap.md`) | Different by design |
| Existing cluster (BYOK) | Yes, any 1.24+ | "Not yet" (`docs/faq.md:81-83`) | Behind |
| Team | Organisations, projects, environments, roles | Teams, projects, environments, invitations (`internal/api/api.go` routes) | Parity |
| Web shell | Console and `qovery shell` | One-off commands, logged; no interactive shell by decision | Different by design |
| UI languages | English | Five, CI-enforced (`web/src/locales`) | Skifity ahead |
| Open source | Engine and console GPL-3.0; control plane closed | Whole product public | Skifity ahead |

## Gaps worth closing in Skifity

**What fits and what does not.** Qovery's value to its buyers is governance
across many clusters and clouds — custom roles, policy tokens, blueprints of
cloud and SaaS resources, compliance packages, disaster-recovery playbooks,
agent tasks — sold at $2,999 a month. Almost none of that is a single-VPS
problem. What does fit is the **environment** as a unit you can copy, preview,
stop and seed, because Skifity already has environments as namespaces and
nothing more. Qovery's own k3s experiment is a warning worth keeping in view:
single-node k3s attracted "individual developers ... deploy[ing] side projects
with a very high churn rate". For a hosted vendor that was a reason to stop;
for a free self-hosted product it is the audience, and what it asks for is
fewer steps, not more governance.

### P0 — Previews must not inherit production's database

- **What:** a preview today copies one app and **all** its variables
  (`copyPreviewVariables` in `internal/api/webhook_handlers.go`), including the
  secret connection string a database link writes
  (`internal/dbsvc/manager.go:342`), which names a Service in the production
  namespace (`internal/dbsvc/manifests.go:144`). The preview's namespace is
  default-deny to other namespaces (`internal/kube/namespace.go:127-217`), so
  the preview of a database-backed app cannot reach a database and its release
  command fails — or, where NetworkPolicy is not enforced, the pull request's
  migrations run against production. Written, never run, in either case.
- **Fix, in Qovery's shape:** a preview is a copy of the **environment**, not of
  one app: every app whose repository matches (or a chosen set), and for each
  linked database a fresh one in the preview namespace — empty, or seeded by a
  command the user names (Qovery uses a lifecycle job; Skifity's release command
  and one-off run are the same idea). Variables written by database links are
  never copied; new links are made. Add Qovery's on-demand mode — a `/skifity
  preview` comment instead of every PR — which is what a 1 GB VPS actually
  wants, since each preview costs memory.
- **Evidence:** Qovery: "The frontend, backend, PostgreSQL and Redis instances
  are cloned!"; Porter creates databases per preview and strips credentials
  from copies. Preview-per-PR is named in `docs/research/competitors.md` as the
  best developer-experience feature in the industry.
- **Fit:** `store.DatabaseLink` (`internal/store/models.go:404`) already says
  which variables came from a link; `internal/dbsvc` creates databases in any
  environment; the webhook path already creates the environment and its
  namespace; the PR comment already exists (`internal/deploy/gitreport.go`).
- **Size:** M (L with seeding and on-demand mode).
- **Without a cluster:** the copy rules, which databases are created, the
  comment trigger and cleanup are unit tests against the `fakeCluster` in
  `internal/api/api_test.go:426`; a database starting in a preview namespace
  needs a cluster.

### P1 — Secret build variables must not travel as build arguments

- **What:** variables marked "needed while building" are decrypted and written
  in plaintext into the build Job's script — `--env KEY=VALUE` for Railpack and
  Nixpacks and `--opt build-arg:KEY=VALUE` for BuildKit
  (`internal/builder/job.go:452`, `:495`, `:552`), even when the variable is a
  secret (`internal/deploy/deployer.go:759-776` does not look at `IsSecret`).
  Anyone who can read Jobs in the build namespace can read them, and a
  Dockerfile `ARG` used by a `RUN` step is recorded in the image's history,
  which then sits in the registry. Pass secret build variables as BuildKit
  secrets (`--secret id=KEY,src=...` from a Kubernetes Secret mounted into the
  build pod) and keep build args for non-secret values — exactly the line
  Qovery drew on 2026-09-23. Tell the user in the variables form which one they
  get, and that a Dockerfile needs `RUN --mount=type=secret,id=KEY` to use it.
- **Evidence:** Qovery's changelog 2026-09-23 ("mounted for that one step only,
  never written to a layer"); Porter refuses secrets at build time entirely;
  Docker's own build check `SecretsUsedInArgOrEnv` warns that build arguments
  "persist in the final image" and recommends secret mounts.
  Skifity's promise is that a secret is never shown again by any surface
  (`llms.txt`, "Secrets are write-only"); a Job spec and an image history are
  surfaces.
- **Fit:** `internal/builder/job.go` (a Secret volume and `--secret` flags),
  `internal/deploy/build.go` (split args from secrets), the variables tab copy.
- **Size:** S–M.
- **Without a cluster:** yes for the guarantee that matters — a golden test that
  no secret value appears anywhere in the rendered Job; whether Railpack's
  frontend accepts secrets needs one real build.

### P1 — Clone an environment

- **What:** "Clone" on an environment: a new environment (namespace) with
  copies of its apps, settings and non-link variables, fresh databases (empty,
  or restored from the source's latest backup when one exists), no custom
  domains. The quickest way to get staging from production, or a sandbox for an
  assistant to break.
- **Evidence:** Qovery's clone across projects and clusters, and it is also how
  its blueprint environments and Agent Tasks' "Clone Environment" mode work;
  Porter clones apps. Skifity has neither (grep `clone` in `internal/api`,
  `internal/store`: nothing).
- **Fit:** shares nearly all of its machinery with the P0 preview work; an
  action on the project page and a `skifity env clone` command.
- **Size:** M (S once P0 exists).
- **Without a cluster:** the store copy and API yes; running the copy needs a
  cluster.

### P1 — Stop and start an environment, and on a schedule

- **What:** "Stop" scales every app in an environment to zero and "Start"
  restores the saved counts; a schedule ("weekdays 08:00–19:00") and an
  inactivity rule for previews.
- **Evidence:** Qovery's deployment rules, with `[PR]*` matching every preview,
  sold as "up to 60%" savings; Porter's sleep mode per service. On a VPS the
  saving is memory rather than money — a stopped staging environment is room
  for a build or two previews. Today Skifity can set one app's replicas to 0
  (`internal/api/deploy_handlers.go:302`) and nothing more.
- **Fit:** `internal/cron` already parses the schedules backups and scheduled
  commands use; the minute tick in `internal/serverapp`; stored counts on the
  environment in `internal/store`.
- **Size:** S–M.
- **Without a cluster:** yes for the schedule, the saved counts and the API;
  the scaling itself is the same `Sync` path as everything else.

### P1 — Agent skills and a read-only MCP mode

- **What:** ship skills with the binary (`skifity skills install`) and add
  `skifity mcp --read-only`, MCP tool annotations, and a documented Claude Code
  permission allowlist for the `skifity` CLI in `docs/cli.md`.
- **Evidence:** Qovery's eight skills and its "Securing AI Agent Access" page
  (read-only MCP by default, CLI allowlist, policy tokens) are the most complete
  treatment of agent safety of any product in this batch. Skifity already has
  the strongest primitive — tokens scoped to a resource and a direction
  (`internal/auth/scopes.go`) — and does not package it for agents.
- **Size:** S. **Without a cluster:** yes.

### P1 — A Viewer role

- **What:** a read-only role below member.
- **Evidence:** Qovery's Viewer and Billing Manager; Porter's Viewer. Skifity's
  member "can deploy" (`docs/configuration.md:98-100`).
- **Fit:** role rank in `internal/store/models.go`; the route walk in
  `internal/api` proves it.
- **Size:** S. **Without a cluster:** yes.

### P2 — Variables as files, and at environment scope

- **What:** a variable whose value is mounted as a file at a path (certificates,
  service-account JSON, a config file), and variables set on an environment that
  every app in it inherits (between project-wide and per-app).
- **Evidence:** Qovery supports both, with a scope hierarchy; Porter mounts
  environment-group files at `/etc/secrets/<group>`; Sealos has config files.
  Skifity has project-wide and per-app variables only (`store.SharedVariable`,
  `store.Variable`).
- **Size:** S–M. **Without a cluster:** manifests in golden tests.

### P2 — Environment hooks: on create, on stop, on delete

- **What:** commands that run when an environment (especially a preview) is
  created, stopped or deleted — seed a database, deregister a webhook, drop an
  external resource.
- **Evidence:** Qovery's lifecycle jobs; Porter's `initialDeploy`.
- **Fit:** the run-job machinery behind the release command
  (`internal/kube/runjob.go`).
- **Size:** M. **Without a cluster:** the triggering logic yes.

### P2 — Deploy order within an environment

- **What:** when several apps deploy together (a template install, a preview, a
  clone), let them declare an order — database, then API, then worker — rather
  than racing.
- **Evidence:** Qovery's deployment pipeline stages. Skifity's template
  installer creates and links databases first, then starts every service's
  deployment at once (`internal/api/template_install.go:23-25`, `:155-161`):
  there is no order among services and no wait for a database to be ready, so
  a service that migrates on start races its database.
- **Size:** S–M. **Without a cluster:** yes for ordering logic.

## Things to deliberately not copy

- **Governance as the product.** Custom roles, policy tokens, blueprints of
  external SaaS, compliance packages, 20-seat minimums — Qovery's move upmarket
  is what kept it alive in a market Digger's founder called crowded, and it is
  the wrong direction for a free panel on somebody's VPS.
- **Observability as a paid, "contact us" add-on.** Qovery's reviewers asked
  for integrated monitoring for years; the answer was a $299-per-cluster add-on
  that is not self-service. Skifity's version should be small, built in, and
  sized for 1 GB (see the metrics-history gap in `porter.md`).
- **Two database modes where one is "cloud-managed".** Wrapping RDS and Cloud
  SQL makes sense when the customer is on a hyperscaler. On a VPS, in-cluster
  databases with tested backups are the whole answer.
- **Frequent pricing changes.** The one complaint that recurs in otherwise
  five-star reviews. Skifity has no pricing; if it ever does, change it rarely.
- **A web shell into containers.** Qovery added one in 2024; Skifity decided
  against an interactive shell for reasons that still hold (`docs/roadmap.md`,
  "Not on this roadmap").
- **Agent Tasks.** Running Claude or Bedrock agents inside the customer's
  cluster, with model keys stored by the platform and cloned environments on a
  webhook, is a product of its own. Skifity's job is to be the best thing an
  assistant can drive from outside — MCP, skills, errors with a fix.
- **Marketing inside `llms.txt`.** Qovery's `llms.txt` carries dozens of
  "buyer's guides" deliberately kept off the human blog index. Skifity's
  `llms.txt` is a factual product description; keep it that way — assistants
  quote it.
- **A local "demo" cluster in Docker.** `qovery demo` runs k3s in Docker and is
  "not intended for production". Tempting as a try-it path, but it needs
  privileged containers (the reason ADR-0010 exists) and would give people a
  first impression of something that is not the product.
- **Chasing multi-cloud.** Qovery's value is one control plane across clouds;
  Skifity's roadmap already says one panel, one cluster.

## Sources

All read 2026-09-30.

- https://www.qovery.com/llms.txt and https://www.qovery.com/docs/llms.txt
- https://www.qovery.com/pricing
- https://www.qovery.com/changelog and https://www.qovery.com/changelog/2026-09-23
- https://www.qovery.com/changelog/2025-03-26/ (ingress-nginx CVE-2025-1974 response)
- https://www.qovery.com/changelog/2024-11-06 (EC2/k3s decommissioning; Kubernetes 1.29/1.30 upgrades)
- https://www.qovery.com/changelog/changelog-000029 (Single EC2 K3s offer, 2023-03-05)
- https://www.qovery.com/docs/getting-started/how-it-works.md and basic-concepts.md
- https://www.qovery.com/docs/configuration/integrations/kubernetes/byok.md
- https://www.qovery.com/docs/getting-started/installation/kubernetes.md
- https://www.qovery.com/docs/configuration/integrations/kubernetes/docker.md
- https://www.qovery.com/docs/getting-started/guides/use-cases/preview-environments.md
- https://www.qovery.com/docs/configuration/environment.md
- https://www.qovery.com/docs/configuration/deployment-rule.md
- https://www.qovery.com/docs/configuration/blueprints.md
- https://www.qovery.com/docs/configuration/database.md
- https://www.qovery.com/docs/configuration/lifecycle-job.md
- https://www.qovery.com/docs/configuration/deployment/strategies.md
- https://www.qovery.com/docs/configuration/environment-variables.md
- https://www.qovery.com/docs/configuration/organization/members-rbac.md
- https://www.qovery.com/docs/getting-started/security-and-compliance/audit-logs.md
- https://www.qovery.com/docs/configuration/integrations/observability/qovery-observe.md
- https://www.qovery.com/docs/getting-started/guides/qovery-101/optimize.md
- https://www.qovery.com/docs/configuration/integrations/webhooks.md
- https://www.qovery.com/docs/configuration/disaster-recovery.md
- https://www.qovery.com/docs/getting-started/useful-resources/faq.md
- https://www.qovery.com/docs/copilot/overview.md, mcp-server.md, securing-ai-access.md
- https://www.qovery.com/docs/configuration/agent-tasks/overview.md
- https://www.qovery.com/security (penetration tests, CodeQL, secret scanning, Dependabot, image scanning)
- https://github.com/Qovery/engine and https://github.com/Qovery/console (licences)
- https://github.com/Qovery/qovery-cli/security/advisories
- GitHub API organisation search `org:Qovery` (stars, forks, open issues)
- https://aws.amazon.com/marketplace/reviews/reviews-list/prodview-4jred4exomyak (pages 1–3; G2 reviews syndicated)
- https://tech.eu/2025/09/30/qovery-raises-13m-to-redefine-devops-automation/
- https://www.qovery.com/blog/new-pricing-that-will-give-you-peace-of-mind (published 2022-08-18: per-deployment to per-active-user pricing)
- https://www.qovery.com/blog/understanding-qovery-pricing-transparent-and-flexible-billing (published 2023-04-08: active-developer billing)
- https://news.ycombinator.com/item?id=28129159 (Rust in Production: Qovery, 2021)
- https://news.ycombinator.com/item?id=35277961 (Digger founder on the category, 2023)
- https://news.ycombinator.com/item?id=36330781 (Qovery CEO on per-user pricing, 2023)
- https://www.kubernetes.dev/blog/2025/11/12/ingress-nginx-retirement/
- https://docs.docker.com/reference/build-checks/secrets-used-in-arg-or-env/ (ARG/ENV secrets "persist in the final image")
- https://nvd.nist.gov (API keyword search "qovery")
- Skifity sources cited inline, read in this repository on 2026-09-30.
