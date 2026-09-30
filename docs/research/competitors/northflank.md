# Northflank

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Northflank is a closed-source platform, formed on 2019-04-01 per its security
page, that puts a
Heroku-like layer (services, jobs, databases, environments, previews) on top of
Kubernetes and sells it three ways: on Northflank's own managed cloud (17
regions; its security page and a 2025 incident report both point to Google
Cloud underneath), as **BYOC** where Northflank provisions and
operates a cluster inside the customer's AWS, GCP, Azure, Civo, Oracle,
CoreWeave or Nebius account, and as **BYOK** where it imports a Kubernetes
cluster the customer already runs. It is aimed at startups and platform teams
that want a developer self-service layer without building an internal platform,
and increasingly at AI workloads (GPUs, microVM sandboxes, "harnesses" for
coding agents). Architecture: a hosted control plane whose internals and state
store are not published, and a data plane per cluster in which Northflank
installs Istio, Envoy Gateway, Prometheus, Promtail, its own platform services
and a managed CoreDNS, on top of a required Cilium CNI, with Kata Containers
(microVMs) or gVisor as the workload runtime; for an imported cluster the
documentation says the control plane reaches the Kubernetes API with a
`cluster-admin` service account it creates. Pricing is
usage-based with no seat fees: a free Developer Sandbox (2 services, 1 database,
2 cron jobs, card required), then $0.01667 per vCPU-hour and $0.00833 per
GB-hour (the 1 vCPU / 2 GB plan is $24/month), $0.15/GB-month disk, $0.06/GB
egress, GPUs from $0.80/hour (L4) to $2.74/hour (H100); on BYOC the cloud bills
the customer directly and Northflank adds $0.01389 per vCPU-hour and $0.00139
per GB-hour. Adoption: $22.3M raised (a $16M Series A led by Bain Capital
Ventures plus a $6.3M seed led by Vertex, announced November 2024) and "more
than 30,000 developers", with Sentry, Writer and Chai Discovery named as
customers in the same announcement. The product ships very fast: its
bimonthly 2026 changelogs each list dozens of features. Its public GitHub
presence is small (the largest repository is an agent skill with 35 stars),
because the platform itself is not open source. (Figures read 2026-09-30;
sources at the end.)

**Why it matters most to Skifity:** it is the one competitor that is also
"Kubernetes underneath, hidden behind a product vocabulary", and it has spent
seven years deciding which Kubernetes concepts to expose (ports, probes, node
pools, network policies) and which to wrap (release flows, preview blueprints,
secret groups). Many of its answers can be copied almost literally; a few of
its costs are exactly what Skifity was built to avoid.

## Feature inventory

### Deploy sources and builds

* Three service kinds: **combined** (build from Git and deploy), **build**
  (build only, images consumed elsewhere) and **deployment** (run an image from
  a build service or any registry). **Jobs** are manual or cron, from Git, a
  build service or an external image.
* Git providers: GitHub, GitHub Enterprise, GitLab, Bitbucket, self-hosted
  Gitea (May-June 2026), Cursor Origin (August-September 2026), plus source
  bundles for non-Git sources.
* Dockerfile or Cloud Native Buildpacks; build rules on paths and
  commit-message ignore flags; build arguments; cross-project build services;
  BuildKit local disk cache (GA August-September 2026); builds can run on the
  customer's own cluster in BYOC.
* CI (build every commit) and CD (deploy the latest build) toggled per service
  and job.

### Domains, TLS and routing

* A public port gets `<port>--<service>--<random>.code.run` with TLS; custom
  domains and subdomains, wildcard certificates with TLS version controls,
  CDN caching presets and purge (August-September 2026).
* Ports are HTTP/1.1, HTTP/2 (gRPC) or TCP/UDP; only HTTP is public by default,
  TCP/UDP go through a self-service Layer 4 load balancer (January-February
  2026). Private VPC load balancers on BYOC; self-service static egress IPs.
* Security policies on ports and paths: IP allow/deny lists, basic-auth
  credentials, and **SSO access control** that makes a visitor sign in with the
  organisation's identity provider before reaching the service; organisation-
  wide ingress policies and AND/OR matching (2026).
* **Canary rollouts**: a team-level strategy attached to a service splits
  traffic by percentage or by header, and the operator promotes or rolls back.

### Databases and services

* Managed "addons": PostgreSQL (Patroni failover, connection poolers,
  PostgreSQL 18), MySQL (standard and InnoDB Cluster HA), MongoDB, Redis
  (optionally Sentinel), MinIO, RabbitMQ, and S3-compatible buckets (May-June
  2026). Custom addon types from a Git repository. "External addons" provision
  RDS/Aurora, Cloud SQL or Memorystore through OpenTofu.
* Replicas with read-only connection strings, zonal redundancy, non-destructive
  replica scale-down, secret rotation without immediate invalidation.
* **Fork an addon** from any disk backup (PostgreSQL, MongoDB, MySQL), manually
  or inside a template; `backupId: latest` forks from the newest backup.

### Storage and backups

* Volumes in single read/write or multi read/write mode, per-replica volumes
  for stateful services, cross-namespace cloning, automatic resizing with a
  maximum, scheduled volume backups with failure alerts, container filesystem
  snapshots.
* Addon backups are either incremental **snapshots** or **dumps** (gzip or
  zstd, downloadable, with logs); up to three schedules (hourly, daily, weekly)
  with retention; import from a URL, an upload or a live database; "global
  backup" to the customer's own S3 with Object Lock.

### Scaling and high availability

* Horizontal autoscaling on CPU, memory, **requests per second**, or a
  **custom Prometheus metric** (gauge or counter) the app exposes; evaluated
  every 15 seconds, scale-down judged over a 5-minute window.
* Vertical plans from 0.1 vCPU/256 MB to 32 vCPU/256 GB.
* On BYOC: node pools with autoscaling, spot instances, GPU pools with time
  slicing, custom resource plans; "service expiry" to pause or delete a service
  on a schedule (May-June 2026).

### Observability (logs, metrics, alerts, uptime)

* Logs from builds, running and terminated containers, addons and job runs,
  **30 days of retention**, text or regex search, include/exclude, time ranges,
  service-mesh logs; tail over API and CLI.
* Metrics per pod and per container (CPU, memory, network, volume, GPU, probe
  latency, restart reasons), rebuilt in January-February 2026 with annotations.
* **Log sinks**: Datadog, Loki, Papertrail, Mezmo, Better Stack, Honeycomb,
  Logz.io, New Relic, Axiom, AWS S3 and custom HTTP, per project or account.
* **Infrastructure alerts**: container crash, eviction, CPU and memory spikes
  and sustained ≥90% for 5 minutes, platform and addon volumes at 75% or 90%,
  cluster problems; throttled per resource and sent through notification
  integrations.
* Liveness, readiness and startup probes over HTTP, TCP or a command.
* Audit logs at organisation, team, project and resource level, each event
  with its origin (UI, API, template run, Git, system), parent/child chain and
  a before/after diff of the resource spec.

### Security, auth, roles, SSO, audit

* RBAC with custom roles built from CRUD permissions per resource type,
  restricted to projects (with exclusion rules), marked UI-only, API-only or
  sensitive; organisation roles mapped from directory-sync groups; API tokens
  inherit a role.
* Personal sign-in with Google, GitHub, GitLab or Bitbucket OAuth plus TOTP;
  **organisation SSO over SAML or OIDC through WorkOS, with directory sync,
  unlocked by contacting sales** ("SSO only", approval queue for SSO sign-ups,
  invites restricted to the domain, just-in-time provisioning).
* **Secret groups**: typed as "secret" or "configuration" (different RBAC),
  scoped to build time, runtime or both, and **restricted to specific
  services, tags or environments**; global secrets shared across projects;
  secret files; linked addon credentials flow in as group entries.
* Workload identities (short-lived OIDC to cloud providers), SSH identities,
  customer KMS envelope encryption, network policies UI, Kata or gVisor
  isolation, SOC 2 Type 2, HIPAA with a BAA on Enterprise.

### Preview environments and branches

* **Preview blueprints** (the 2026 name for the older "preview environment
  templates"): a template run per pull request or branch, with triggers for PR
  opened/updated, push, tagged release, check-suite success, **PR label**, cron
  or webhook.
* A preview can contain any node: build, services, **a fresh database or one
  forked from a backup**, a seed job, secret links, generated domains, and
  lightweight mocks chosen by condition nodes.
* Naming by PR number, branch or custom expression; **smaller resource plans
  than production**; **expiry after a set lifetime**, optionally reset on each
  push; the blueprint file can be read **from the preview's own branch**;
  **teardown specs** for external resources; a manual "create" to test a
  blueprint without a PR; a message node can comment on the pull request.

### Templates and catalogue

* **Templates are the IaC primitive**: team templates (anything, including
  cloud integrations and clusters), workflow templates and preview blueprints,
  edited in a visual node editor or as JSON, with arguments, references to
  other nodes' outputs and functions (`fn.if`, `concat`). Run history, version
  history with rollback and per-node run diffs (August-September 2026), drafts
  for review (Enterprise).
* **Bidirectional GitOps**: a template file in a repository is synced both
  ways; edits in the UI are committed, pushes update the template, and a
  template can run on every change.
* One-click "stack templates" exist (Dify, Docuseal, Notifuse and others were
  added in January-February 2026); the total count is not published.

### CLI, API, IaC, integrations

* REST API over every resource, the `@northflank/cli` npm package, a
  JavaScript client, `northflank forward` for local tunnels to private services
  and addons, exec/terminal and file copy, a GitHub Action.
* OpenTofu nodes inside templates for external infrastructure (AWS, GCP,
  Azure, Akamai), including destroy nodes.
* An official agent skill repository (`northflank/skills`, created April
  2026). No first-party MCP server was found; a third-party one exists via
  Composio.

### Notifications

* Notification integrations to Slack, Discord, Microsoft Teams Workflows and
  webhooks, for events such as build starts, backup failures, job runs and
  billing, plus infrastructure alerts; filtered by event type, project and
  resource tag; organisation-level integrations for billing and cluster
  provisioning errors. Slack, webhook and pull-request-comment message nodes
  inside workflows.

### Multi-server and networking

* 17 managed regions; BYOC clusters in any region the provider offers;
  multi-project networking to allow private traffic between projects;
  Tailscale integration; internal DNS; "consistent replica" routing for
  internal traffic (January-February 2026).
* BYOK requirements: Kubernetes 1.34 or 1.35, Cilium with L7 proxy enabled, a
  CSI driver, CoreDNS named `kube-dns` (replaced by Northflank's own), an API
  server reachable from Northflank's control plane, and public `LoadBalancer`
  IPs. Istio, Envoy Gateway, Prometheus and Promtail must **not** be installed,
  because Northflank installs them.

### Team and collaboration

* Teams and organisations, invitations, directory sync, team-to-organisation
  conversion, project-level activity logs, command menu.

### Developer experience and onboarding

* A real-time UI (changes appear without refreshing), a project dashboard
  reorganised around environments in May-June 2026, redesigned documentation
  with Markdown versions of every page for agents.
* **Environments and workflows** replaced "pipelines and release flows" as the
  primary model during 2026: an environment column holds services, jobs and
  addons, and workflows (visual node graphs: build, deploy/promote, run job,
  back up addon, wait for condition, **approval**, message) run on Git, cron or
  webhook triggers with a concurrency policy of allow, queue or cancel-previous.
  The older pipeline pages remain in the docs.

## What users love

* **Support and onboarding**, repeatedly, on G2 (4.5-4.9/5 depending on the
  snapshot, from about 10-11 reviews): "exceptional customer support … proactive
  assistance … during onboarding and migration".
* **Speed and the real-time UI**: "Everything is incredibly fast and
  streamlined. You can deploy new versions in literally seconds" (Trustpilot).
* **Kubernetes power without Kubernetes work**, and the same experience in
  their own cloud account. Sentry's David Cramer is quoted on Northflank's BYOC
  page: "It's the ideal platform to deploy containers in our cloud account"
  (vendor-published quote).
* **Cheaper than Heroku** for the same shape of workload (G2; HN threads where
  the co-founder answers Heroku-migration questions).

## What users complain about

Review volume is small (6 Trustpilot reviews, about 10 on G2), so these are
patterns across few voices rather than statistics.

* **Support that goes quiet** for self-serve customers: "I have to send
  multiple messages to get an answer, and most of the time I no answer at all"
  (Trustpilot, June 2025); Trustpilot is 3.4/5 and split between 5 and 1 stars.
  Pay-as-you-go support is officially "best effort"; the SLA is Enterprise.
* **A confusing UI and a learning curve**, "the UX is not really good and
  confusing" (Trustpilot, March 2026); G2 lists "learning curve" and "smaller
  community, less ecosystem" as cons. The breadth of the product (templates,
  workflows, node graphs, pipelines and environments coexisting in the docs)
  is the likely cause.
* **The free tier needs a credit card** (Trustpilot, September 2025).
* **Networking limits** (VPC, load balancing, VPN) were a G2 con; several of
  these were addressed in 2026 (L4 load balancers, VPC load balancers,
  Tailscale).
* **Incidents** are fewer than Fly's on the public record, but exist: a
  platform partial outage of about an hour on 2025-05-21 (a database migration
  triggered rescheduling that overloaded the underlying GCP nodes; four
  databases needed manual recovery), a logs-and-metrics outage on 2025-08-05,
  and a 50-minute London incident on 2026-05-13.
* **BYOK carries sharp edges by Northflank's own account**: "Do not import
  clusters that run production workloads … there is currently no full
  deinstallation process", and deleting an imported cluster by default deletes
  every PVC and every StatefulSet in every namespace, including ones Northflank
  did not create.

## Security record

* **No CVEs or published advisories found.** Searched NVD and the web for
  Northflank, and GitHub for advisories; the platform is closed source, so there
  is no advisory database to check beyond that (2026-09-30).
* Northflank's security page states "Northflank has not experienced a
  security breach since the company's formation on April 1st, 2019", SOC 2
  Type 2 and HIPAA; it also says "We plan to begin penetration tests on a
  recurring basis over the next year" and lists no bug bounty.
* Architecturally, workloads run under Kata Containers (microVMs) by default,
  with gVisor as the fallback, and Cilium network policies between project
  namespaces. The BYOK path needs a kubeconfig with `cluster-admin`, from which
  Northflank creates a permanent `cluster-admin` service account in
  `kube-system`.

## Against Skifity

**Works** has a test that runs on every push; **Written** has never run on a
real cluster (ADR-0010, `docs/checklist.md`).

| Capability | Northflank | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy sources and builds | Git (5 providers + bundles), images, Dockerfile/buildpacks, build rules, shared build services | Git (GitHub/GitLab/Gitea, `internal/gitsrc`), images, folder upload (`internal/cli/up.go`), Railpack/Nixpacks/Dockerfile (`internal/builder`), build fingerprint so a config change never rebuilds (ADR-0007). Written | Parity (Skifity ahead on no-rebuild config) |
| Release process | Environments + workflows: build, promote, run job, back up, approval, message; concurrency policy | Release command before rollout (`internal/deploy/run.go:130`); environments exist (`POST /api/projects/{id}/environments`) but **no promotion** between them and no multi-step release | Behind |
| Domains, TLS, routing | code.run domains, wildcard certs, CDN, TCP/UDP load balancers, canary traffic split, port security policies | cert-manager, `sslip.io` HTTP address (ADR-0015), path routing, edge firewall on IP/country/ASN/path/header (`internal/edgerules`, `internal/guard`), a password in front of an app (commit `7865bc8`), HTTP only. Written | Behind on protocols, canary and SSO-at-the-edge; parity-plus on edge rules |
| Databases and services | Postgres, MySQL HA, Mongo, Redis Sentinel, MinIO, RabbitMQ, buckets, external addons, fork from backup | Postgres HA via CloudNativePG, Redis and MySQL single-instance (`internal/dbsvc/manifests.go:105-121`); restore only into the same database (`handleRestoreBackup`, `internal/api/data_handlers.go:394`). Written | Behind |
| Storage and backups | Volumes RWO/RWX, per-replica, cloning, snapshots and dumps, three schedules, import, own-S3 with Object Lock | PVCs, optional Longhorn, scheduled tar and `pg_dump`/`mysqldump`/`redis-cli --rdb` to the user's S3 through presigned URLs (ADR-0012), restore in place. Written; "Backup and restore, proven" is explicitly not claimed (`docs/checklist.md` item 7) | Behind on snapshots/fork/import; parity on off-site |
| Scaling and HA | Autoscale on CPU, memory, RPS or a custom Prometheus metric; node-pool autoscaling on BYOC | HPA on CPU/memory, KEDA HTTP scale-to-zero (`internal/kube/scaletozero.go`), scaling readiness checker (`internal/deploy/scaling.go`). Written | Behind on RPS/custom metrics; ahead on readiness check |
| Observability | 30-day logs with regex search, per-container metrics history, 11 log sinks, threshold alerts, audit with diffs | Kubelet log tail, max 10,000 lines, client-side text filter (`internal/api/stream_handlers.go:182`, `web/src/components/app/logs-tab.tsx:75`); point-in-time usage (`internal/kube/client.go`); no history, no sinks, no threshold alerts; audit events without diffs (`store.AuditEvent`) | Behind |
| Security, auth, roles | Custom RBAC per project; SAML/OIDC SSO and directory sync only after contacting sales; secret groups restricted by environment; KMS; workload identity; Kata/gVisor | Argon2id, TOTP, OIDC SSO for every install (`internal/auth`), 3 fixed roles, team-bound scoped tokens, sealed write-only secrets, route-walk tenant tests (**Works**); `restricted` Pod Security per namespace (`internal/kube/podsecurity.go`); project shared variables apply to **every** environment (`internal/deploy/deployer.go:731`) | Behind on RBAC and secret scoping; ahead on SSO availability and panel-side proof |
| Preview environments | Blueprints with fresh or forked DBs, seed jobs, TTL, small plans, label triggers, teardown | PR/branch namespace, one copy of each app watching the repo, an **empty database of its own per linked database** (1 GB, one instance, not for forks; `previewDatabases`, commit `1ae3276`, 2026-09-30), fork previews get no app secrets, the app's password carried over (commit `7865bc8`), deleted on close, commit status and PR comment (`internal/api/webhook_handlers.go`, `internal/deploy/gitreport.go`). No TTL, no seeding, shared secrets still reach forks (P0 below). Written | Behind on lifecycle; close on the core |
| Templates and catalogue | Templates as IaC with GitOps; a small one-click stack list | 282 one-click apps as YAML (`internal/templates/catalogue`); not user-authored, not synced to Git | Ahead on catalogue, behind on IaC |
| CLI, API, IaC | API, CLI, JS client, `forward`, GitOps, OpenTofu | API, 17-command CLI, 15-tool MCP server (`internal/mcpserver/server.go`), `skifity export` (**Works**); `skifity.toml` holds ids only (`internal/cli/project.go`) | Behind on IaC and tunnels; ahead on MCP |
| Notifications | Slack, Discord, Teams, webhooks, PR comments, alerts | Telegram, Discord, webhook, email, plugin-provided kinds; 7 events (`internal/notify/notify.go:41-47`); PR comment for previews. Written | Parity |
| Multi-server and networking | Managed regions, BYOC anywhere, BYOK import, multi-project networking, Tailscale | Servers added over SSH into one k3s cluster (`internal/provision`), WireGuard flannel (ADR-0003), namespace per environment with default-deny (`internal/kube/namespace.go:127`). One panel, one cluster by decision (`docs/roadmap.md`). Written | Different by design |
| Team and collaboration | Teams, orgs, directory sync | Teams, invitations, 3 roles, audit. Works | Behind on org/directory |
| Developer experience | Real-time UI, environment-first dashboard, sandbox tier | SSE-driven panel (ADR-0005), project canvas (`handleProjectCanvas`), errors with cause/impact/fix, five languages; 35 MiB panel vs Northflank's in-cluster Istio + Prometheus stack. No release tagged | Ahead on footprint; behind until released |

## Gaps worth closing in Skifity

### P0 — A preview from a fork must not receive the project's shared secrets

* **What.** Previews built from a fork withhold the *app's* secret variables
  (`copyPreviewVariables`, `internal/api/webhook_handlers.go:336`), and since
  commit `1ae3276` (2026-09-30) they get no database and never production's
  connection string. But at deploy time `runtimeVariables` merges the
  **project's shared variables**, secret or not, into every environment of the
  project, previews included (`internal/deploy/deployer.go:731`), and nothing in
  `internal/deploy` or on the preview's environment records that it came from a
  fork. So a shared secret — a payment key, an SMTP password — is handed to a
  container running code that anyone on the internet could have written, which
  is exactly the case the comment on `copyPreviewVariables` says must not
  happen. Found by reading the code; not observed on a cluster.
* **Evidence.** Northflank restricts secret groups to chosen services, tags or
  environments, so a preview blueprint gets only what it is given; GitHub
  Actions draws the same line for forks that Skifity's own comment cites.
* **Fit.** Record `from_fork` on the preview environment in `deployPreview`;
  in `runtimeVariables`, skip secret shared variables when the environment is a
  fork preview; say so once in the deployment log. The P1 "project variables
  that differ per environment" below is the general form of the same fix.
* **Size.** S.
* **Without a cluster?** Yes: which variables reach which app is store logic,
  testable with the fake keyring and store the preview-database tests
  (`internal/api/previewdb_test.go`) already use.

### P1 — Promote a version from one environment to the next

* **What.** "Promote" on a deployment in staging creates a deployment of the
  matching app in production with the **same image**, no rebuild, and optionally
  the same build-time inputs; production keeps its own runtime variables.
* **Evidence.** Promotion is the core of Northflank's pipelines and of its
  2026 workflows ("in the production stage you can only promote an image from a
  deployment in the staging stage"). Skifity has environments but a user who
  wants staging-then-production today deploys the same commit twice and gets
  two builds.
* **Fit.** `POST /api/apps/{app}/promote` in `internal/api`, matching by app
  slug across environments of one project; the deployer already knows how to
  deploy an existing image (rollback does it, `internal/deploy/deployer.go`),
  and the build fingerprint says whether the target's build inputs differ.
  A button on the environment view and on the deployment row.
* **Size.** M.
* **Without a cluster?** Mostly: the API, authorization, deployment records
  and the Playwright test can run without one; the rollout needs one.

### P1 — Project variables that differ per environment

* **What.** A project variable can be set for all environments or overridden
  per environment (staging's `STRIPE_KEY` is not production's), and marked
  "not for previews".
* **Evidence.** Northflank secret groups are restricted by service, tag or
  environment (the environment restriction shipped in August-September 2026);
  on DigitalOcean each environment is a separate app with its own app-level
  variables. In
  Skifity a shared variable is per project and reaches every environment,
  previews included (`internal/deploy/deployer.go:731`).
* **Fit.** An optional `environment_id` on shared variables in
  `internal/store`, resolution order project → environment → app in
  `runtimeVariables`, sealed with a context that includes the environment.
* **Size.** S.
* **Without a cluster?** Yes.

### P1 — Preview lifecycle: expiry, a cap, and a smaller size

* **What.** Previews expire after N hours without a push, a project caps how
  many exist at once, and preview apps get a smaller CPU/memory request than
  their source. A "create preview for branch" button for testing.
* **Evidence.** Northflank: preview expiry with reset-on-update, resource
  limits for previews, service expiry. Skifity's code comments already name the
  risk ("Previews that are never cleaned up are how a self-hosted cluster
  quietly runs out of memory") but cleanup depends on a close event arriving.
* **Fit.** The minute tick in `internal/serverapp`, a `last_pushed_at` on the
  preview environment, a setting in `internal/settings`.
* **Size.** S.
* **Without a cluster?** Yes for the policy and the tick; namespace deletion
  is already written.

### P1 — Threshold alerts through the channels that already exist

* **What.** Crash-looping, OOM-killed, CPU or memory ≥90% for 5 minutes, and a
  volume 75% / 90% full, each a notification event with a per-resource
  cooldown.
* **Evidence.** Northflank's infrastructure alerts are exactly this list;
  DigitalOcean has CPU/RAM/restart alerts; Fly's lack of built-in alerting is a
  documented gap. Skifity's seven events stop at "app unhealthy"; a full disk
  is how a database dies quietly.
* **Fit.** `internal/watch` already watches app health; new events in
  `internal/notify`; volume usage from the kubelet stats summary through
  `internal/kube/client.go`.
* **Size.** M.
* **Without a cluster?** The rules, cooldowns and delivery can be tested with
  fake readings; the volume numbers need a cluster.

### P1 — Some history for metrics, and logs that outlive the pod

* **What.** A small per-instance CPU/memory/restarts history (for example
  one sample a minute for 7 days) kept in the panel's SQLite, and runtime logs
  that survive a pod being replaced, with forwarding to Loki, an HTTP endpoint or
  S3.
* **Evidence.** Northflank keeps 30 days of logs and full metric history and
  ships to 11 sinks; DigitalOcean forwards to four destinations; Skifity shows
  what is true now and the last 10,000 lines of a pod that still exists.
* **Fit.** Metrics: a sampler on the minute tick, a table with retention in
  `internal/store/retention.go`, charts from `web/src/components/ui`. Logs: a
  log sink is a textbook "provides" plugin (ADR-0021) — the panel owns "where
  do logs go", the plugin owns Loki or Datadog — but collection needs an
  in-cluster shipper (Fluent Bit or Vector as a DaemonSet), which costs memory
  on every node and must say so like the other components.
* **Size.** M for metrics, L for logs.
* **Without a cluster?** Metrics storage and charts yes (fake clientset,
  Playwright); log collection no.

### P2 — "Back up first" as a release step

* **What.** An app option: before the release command runs, take a backup of
  every linked database and wait for it.
* **Evidence.** Northflank's recommended migration workflow is "back up →
  run migration → deploy"; its release flows exist largely to encode that
  order.
* **Fit.** `internal/deploy/run.go` before the release command, using
  `internal/backup`.
* **Size.** S-M.
* **Without a cluster?** Ordering and failure handling yes; the backup no.

### P2 — Seed a preview's database

* **What.** An optional "seed command" on the app, run once against a
  preview's fresh database after it is created and before the release command,
  so reviewers do not open an empty application.
* **Evidence.** Northflank's "full stack with seed data" pattern runs a job
  after the preview database is ready, or forks from a snapshot. Skifity's
  preview databases are empty on purpose, and the reason given (no customer
  data in pull requests) is right; a seed script the team owns keeps that
  property.
* **Fit.** A field on `store.App`, run through the one-off Job path
  (`internal/kube/runjob.go`) from `deployPreview` after `previewDatabases`.
* **Size.** S.
* **Without a cluster?** Ordering yes; the run needs a cluster.

### P2 — Canary rollouts

* **What.** Send N% of traffic (or requests with a header) to the new version,
  promote or roll back.
* **Evidence.** Northflank canary strategies; Fly canary and blue-green.
* **Fit.** Traefik weighted services in `internal/kube/manifests.go`, two
  Deployments per app during a rollout; interacts with scale-to-zero and the
  firewall, which is why it is not small.
* **Size.** L.
* **Without a cluster?** Manifests yes; behaviour no.

### P2 — Autoscale on request rate or a custom metric

* **What.** Scale on requests per second (the KEDA HTTP add-on Skifity already
  installs can report it) or on a Prometheus metric the app exposes.
* **Evidence.** Northflank RPS and custom-metric autoscaling; DigitalOcean made
  request-based autoscaling GA on 2026-05-20.
* **Fit.** `internal/kube/scaletozero.go` and the HPA rendering; the scaling
  tab.
* **Size.** M.
* **Without a cluster?** Manifests yes; scaling no.

### P2 — Sign in with the panel to see a preview

* **What.** An app or a whole preview environment can require the visitor to
  be signed in to the panel (or to its OIDC provider) before the request
  reaches the app, so a preview of an internal tool is not public by address.
* **Evidence.** Northflank port security policies offer IP lists, basic-auth
  credentials and SSO access control. Skifity gained a username and password
  in front of an app on 2026-09-30 (commit `7865bc8`, Traefik basicAuth from a
  bcrypt hash, inherited by previews), which covers the simple case; a login
  against the team's identity provider is the case teams ask for, because a
  shared password outlives the people who knew it.
* **Fit.** The edge guard (`internal/guard`) already answers Traefik's
  forward-auth for every protected app and deliberately holds no credentials;
  it would verify a short-lived cookie signed by the panel after an OIDC or
  password sign-in (`internal/auth`), using a public key the panel writes into
  the guard's ConfigMap next to the rules (`internal/cluster/edgeguard.go`).
* **Size.** M.
* **Without a cluster?** The guard's decision and the cookie are unit-testable;
  the path through Traefik needs a cluster.

### P2 — Audit entries that say what changed

* **What.** Store the before/after of a changed setting (never a secret value)
  on the audit event, and link an event to the one that caused it.
* **Evidence.** Northflank audit logs show a spec diff and parent/child
  events; Skifity's `AuditEvent` has a free-form `Metadata` string only
  (`internal/store/models.go:486`).
* **Size.** S. **Without a cluster?** Yes.

## Things to deliberately not copy

* **A visual node-graph language for releases.** Workflows, preview
  blueprints, templates, functions, references and teardown specs are powerful
  and are also why reviewers call the UX confusing. Skifity should offer a few
  fixed, named steps (promote, back up first, release command, check) rather
  than a programming language drawn in boxes.
* **Bidirectional GitOps.** A UI edit that becomes a commit, and a commit that
  silently re-runs a template against production, is two sources of truth
  with a sync between them. If Skifity grows a config file, the file should win
  and the panel should show the difference.
* **The in-cluster stack.** Istio, Envoy Gateway, Prometheus and Promtail on
  every cluster, plus a Cilium requirement, is the price of Northflank's
  features and the opposite of a panel that idles at 35 MiB on a 1 GB server.
  Add observability as optional components that state their memory, as
  `internal/settings/settings.go` already does.
* **Import that cannot be undone.** BYOK takes `cluster-admin` forever,
  replaces CoreDNS, has "no full deinstallation process", and by default
  deletes every PVC in every namespace when the cluster is removed. Skifity's
  dry-runnable uninstaller is the better instinct; keep it.
* **A hosted control plane holding customers' `cluster-admin`.** It is the
  business model, and it is also the highest-value target in the system;
  `docs/research/competitors.md` already records why Skifity is not doing this
  today.
* **Gating identity and review features behind a sales call.** Organisation
  SSO and directory sync must be unlocked through support, and template drafts
  are Enterprise-only. Skifity ships OIDC to every install; keep it that way.
* **A free tier that needs a card.** Not applicable to self-hosting, but a
  reminder that the first minute is where people leave.

## Sources

All read 2026-09-30.

* https://northflank.com/pricing
* https://northflank.com/blog/northflank-vs-porter (BYOC fee, dated 2026-09-15)
* https://northflank.com/features/bring-your-own-cloud
* https://northflank.com/docs/v1/application/bring-your-own-cloud/byoc-and-byok-requirements
* https://northflank.com/docs/v1/application/bring-your-own-cloud/import-an-existing-cluster-byok
* https://northflank.com/docs/v1/application/bring-your-own-cloud/use-other-cloud-providers-with-northflank
* https://northflank.com/docs/v1/application/release/set-up-environments
* https://northflank.com/docs/v1/application/release/configure-workflows
* https://northflank.com/docs/v1/application/release/set-up-preview-blueprints
* https://northflank.com/docs/v1/application/release/canary-rollouts
* https://northflank.com/docs/v1/application/release/run-migrations
* https://northflank.com/docs/v1/application/release/pipeline/create-a-pipeline-and-release-flow
* https://northflank.com/docs/v1/application/infrastructure-as-code/infrastructure-as-code
* https://northflank.com/docs/v1/application/infrastructure-as-code/gitops-on-northflank
* https://northflank.com/docs/v1/application/observe/view-logs
* https://northflank.com/docs/v1/application/observe/configure-log-sinks
* https://northflank.com/docs/v1/application/observe/set-infrastructure-alerts
* https://northflank.com/docs/v1/application/observe/configure-health-checks
* https://northflank.com/docs/v1/application/observe/audit-logs
* https://northflank.com/docs/v1/application/scale/autoscale-deployments
* https://northflank.com/docs/v1/application/secure/use-role-based-access-control
* https://northflank.com/docs/v1/application/secure/single-sign-on-multi-factor-authentication
* https://northflank.com/docs/v1/application/collaborate/manage-an-organisation
* https://northflank.com/docs/v1/application/observe/configure-notification-integrations
* https://northflank.com/docs/v1/application/secure/manage-secret-groups
* https://northflank.com/docs/v1/application/databases-and-persistence/configure-addons-for-high-availability
* https://northflank.com/docs/v1/application/databases-and-persistence/fork-an-addon
* https://northflank.com/docs/v1/application/databases-and-persistence/backup-restore-and-import-data
* https://northflank.com/docs/v1/application/databases-and-persistence/add-a-volume
* https://northflank.com/docs/v1/application/network/configure-ports
* https://northflank.com/docs/v1/application/network/add-security-policies-for-ports
* https://northflank.com/docs/v1/application/run/run-an-image-once-or-on-a-schedule
* https://northflank.com/docs/v1/application/run/deploy-to-a-region
* https://northflank.com/docs/v1/application/run/access-services-with-ssh
* https://northflank.com/docs/v1/api/forwarding
* https://northflank.com/docs/sitemap.xml (to find the current docs structure)
* https://northflank.com/changelog/january-and-february-2026
* https://northflank.com/changelog/march-and-april-2026
* https://northflank.com/changelog/may-and-june-2026
* https://northflank.com/changelog/august-and-september-2026
* https://northflank.com/security
* https://status.northflank.com/cmay5h4pg0052zbome4c6m5q6 (2025-05-21)
* https://status.northflank.com/cmdy7qu4001ym91lf2txeit0h (2025-08-05)
* https://statusgator.com/services/northflank (2026-05-13 London incident)
* https://www.businesswire.com/news/home/20241111904885/en/Northflank-Raises-$22M-to-Simplify-App-and-Database-Deployment-in-Your-Cloud (via search summary; the page refused a direct fetch)
* https://northflank.com/blog/northflank-raises-22m-to-make-kubernetes-work-for-your-developers-ship-workloads-not-infrastructure
* https://www.g2.com/products/northflank/reviews
* https://www.trustpilot.com/review/northflank.com
* https://news.ycombinator.com/item?id=35047937 and https://news.ycombinator.com/item?id=28840668 (co-founder's HN comments)
* https://github.com/northflank/skills and the `northflank` GitHub organisation (via GitHub search API)
* https://www.npmjs.com/package/@northflank/cli
* https://composio.dev/toolkits/northflank (third-party MCP)
