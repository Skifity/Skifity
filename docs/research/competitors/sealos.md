# Sealos

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Researched 2026-09-30 against live sources (Sealos website and docs, the
`labring/sealos` repository through the GitHub API and GitHub pages, NVD,
Hacker News). Every number carries its source and the date it was read.

Sealos is a Kubernetes-based platform from Labring (Hangzhou) that began in 2018
as a one-command Kubernetes installer (`sealos run labring/kubernetes:v1.x`,
"cluster images", the `lvscare` IPVS health checker) and has since become a
multi-tenant "cloud operating system": a web desktop of apps — App Launchpad
(deploy an image), Database, DevBox (cloud development environments), Object
Storage, Cron Job, Terminal, App Store, Cost Center, AI Proxy — sold mainly as
the hosted **Sealos Cloud** and, secondarily, as a self-hosted distribution. In
2026 the pitch changed again: the repository now describes itself as "Deploy
real projects from GitHub or your AI coding agent, then keep them running with
AI-powered operations", and the homepage says "The AI-Native Cloud Platform -
Deploy Anything with a Prompt". It is for individual developers and small teams
on the hosted cloud (the audience it compares itself to is Railway, Render,
Vercel and Heroku), and for enterprises buying a private deployment — the
self-hosting page itself says large clusters and enterprise production should
use "the enterprise or custom edition". **Architecture:** Kubernetes plus a set
of Kubebuilder controllers (users, accounts and billing, DevBox, and others)
with Next.js/TypeScript frontends per app; the system design docs say user and
account data live in **CockroachDB** (replicated across availability zones),
per-region metering data in **MongoDB**, metrics in VictoriaMetrics, logs in
Loki, databases are run by the KubeBlocks operator, network isolation is
Cilium, block storage OpenEBS, and the gateway moved from ingress-nginx to
Higress (Envoy) in 2025 to reach ~2,000 tenants per cluster. **Licence:**
Apache 2.0 until 2025-07-25, when PR #5719 replaced it with the "Sealos
Sustainable Use License" (the README: internal business and personal
non-commercial use allowed, "providing cloud services to third parties"
prohibited) — source-available, not open source, although the homepage FAQ
still answers "Is Sealos open source?" with "Yes". **Pricing (hosted, read
2026-09-30):** fixed monthly resource packages — Starter $34 ($7 for a first
purchase), Hobby $70 ($25 first purchase), Standard $128, Pro $512, Team $2,030,
Enterprise $12,451 — plus a 7-day free trial with no card. **Maturity:** the
last stable release is v5.1.1 (2025-11-17); six release candidates of v5.1.2
followed, the latest (rc6) on 2026-08-16. **Adoption:** 18,367 stars, 2,481
forks and 103 open issues on GitHub (GitHub API, 2026-09-30); "87,000 users" on
Sealos Cloud (Sealos, Hacker News, 2025-05-21); "over 6,000 database instances
across four regions" (KubeBlocks case study, figures as of December 2024);
"200,000+ developers and teams" (Sealos homepage, 2026-09-30 — a marketing claim
with no method given). The repository dates from 2018 and was the installer for
its first four years, so much of that following likely predates the cloud
product — an inference from its history, not a measured split.

## Feature inventory

### Deploy sources and builds

- **Container image** through App Launchpad: image, command, ports, environment
  variables, config files mounted from a form, persistent storage, fixed
  instance count or autoscaling. The start-here tutorial deploys `nginx:latest`
  at 0.1 CPU and 128 MiB.
- **DevBox** is the code path: a cloud development environment on Kubernetes,
  reached from Cursor, VS Code or JetBrains Gateway over SSH, with 20+
  pre-configured runtimes. "Release" packages the running environment as an OCI
  image with a tag and description; "Deploy" turns a release into an App
  Launchpad app with one click. The environment's state is kept by committing
  changes as image layers ("changes are packaged as image layers ... appended to
  the base image as commits"), merged periodically by a container shim.
- **Git repository (2026):** "Paste a repo. Get a running URL", branded "AI
  Pilot + Git + K8s" — framework auto-detection, build, expose. "No YAML. No
  Dockerfile. No CI/CD."
- **From an AI coding agent:** Sealos Skills (`labring/sealos-skills`, installed
  into Codex, Claude Code, Gemini and others) inspect a repository, score deploy
  readiness 0–12, reuse or build an image, generate a Sealos template, deploy,
  and leave reviewable files in `.sealos/` (`analysis.json`, `build-result.json`,
  `template/index.yaml`, `state.json`).
- **Migration:** a documented manual Docker Compose → Sealos translation guide,
  and a marketed tool that reads `vercel.json`, `render.yaml` or
  `docker-compose.yml` and "migrate[s] your entire cluster infrastructure
  cleanly in under 3 minutes" (a homepage claim; not verified here).

### Domains, TLS and routing

- Every public app gets a generated subdomain; a custom domain is a CNAME, with
  certificates issued by cert-manager.
- Bringing your own certificate is done by creating a Kubernetes TLS secret with
  `kubectl` in the Terminal app and editing the ingress — documented as shell
  commands, not a form.
- Several ports per app. TCP services are exposed through NodePorts, which the
  plans ration (4 on Starter, 8 on Hobby, 16 on Standard).
- Gateway: Higress (Envoy) since 2025, replacing ingress-nginx for reload
  stability and long connections at multi-tenant scale.

### Databases and services

- Managed databases through KubeBlocks: PostgreSQL, MySQL, MongoDB and Redis in
  the product and README; Kafka and Milvus also have connection guides. A
  replica count on the form gives "one-click HA".
- "Built-in DB studio (browse/import/backup)" on the homepage comparison.
- Auto environment injection: connecting a database to an app on the project
  canvas injects its connection details.
- Object storage (S3-compatible) is a first-class product with buckets and
  credentials.
- AI Proxy: an OpenAI-compatible gateway (open source as `labring/aiproxy`)
  that meters model calls against the workspace's "AI credits".

### Storage and backups

- Persistent volumes on apps (OpenEBS underneath).
- The overview promises databases and storage "fully managed with automated
  backups and high availability"; the mechanism is KubeBlocks': scheduled
  backups to object storage, with point-in-time recovery for PostgreSQL and
  MongoDB in KubeBlocks itself. The English docs have no backup or restore page
  of their own, so how much of that the Sealos UI exposes (PITR in particular)
  is not documented (the word "backup" appears four times in the whole English
  docs bundle, read 2026-09-30).
- No documented backup of an app's volume.

### Scaling and high availability

- Fixed instances or HPA on the average CPU or memory of running instances,
  with a min and max. The docs warn, correctly, that scaling suits stateless
  apps and that shared state must live outside the container.
- Self-hosted clusters: odd number of masters, IPVS-based API server HA via
  `lvscare`, nodes added with `sealos add`.
- Sealos Cloud runs multiple regions; account data is replicated across zones
  in CockroachDB.

### Observability (logs, metrics, alerts, uptime)

- Per-app CPU/memory monitoring and logs in App Launchpad; the 2026 canvas puts
  "Live CPU/Memory/Disk on every card".
- The platform's own monitoring is VictoriaMetrics, Loki, Grafana and
  PrometheusAlert with alerts to Feishu and WeChat — an operator stack for
  Sealos Cloud, not a user-facing alerting feature.
- 2026: "AI-powered operations" and "troubleshoot the live app" (Show HN,
  2026-07-24); an MCP "Observability" server exposes monitoring and logs to
  assistants.
- No documented user-facing uptime checks or per-app alert rules.

### Security, auth, roles, SSO, audit

- Multi-tenancy by namespace: every user gets a personal namespace; workspaces
  are further namespaces users can be invited into; three Kubernetes Roles are
  created per namespace and bound per member. Authentication inside the
  platform is a ServiceAccount token embedded in a per-user kubeconfig.
- Isolation claims: Cilium for network, OpenEBS for block storage, "Firecracker
  and Cloud Hypervisor" for runtime isolation (system design doc).
- The self-hosted installer prints default admin credentials
  (`admin` / `sealos2023`) in the documented example output.
- SSO/SAML/OIDC for the platform and an audit log: not documented in the
  English docs (read 2026-09-30).

### Preview environments and branches

- None documented. The homepage comparison has a "Preview deploys" row whose
  Sealos cell carries no text, and there is no docs page for previews
  (2026-09-30). DevBox is Sealos' answer to "a place to try a branch".

### Templates and catalogue

- App Store synced "in real-time" from `labring-actions/templates`
  (42 stars, 83 forks, 31 open issues, GitHub API 2026-09-30). Templates are
  YAML with GitHub-Actions-style variables such as `${{ SEALOS_NAMESPACE }}`.
- Size: "100+ pre-built templates" (docs overview) and "200+ More" (homepage),
  both read 2026-09-30; strongly AI-weighted (FastGPT, Dify, Lobe Chat, n8n,
  Supabase, Appsmith).
- DevBox has its own template market for runtimes.

### CLI, API, IaC, integrations

- The `sealos` CLI is the cluster installer (`run`, `add`, `delete`, `reset`,
  `build`, `save`/`load` of cluster images, registry sync); `sealctl` is its
  node-level helper. There is no app-level CLI comparable to `heroku`; the API
  is the Kubernetes API with Sealos CRDs, reached with the downloaded
  kubeconfig.
- MCP servers for DevBox, Database, Cost Center and Observability over
  StreamableHTTP. Authentication: copy the kubeconfig from the console,
  URL-encode it (the docs suggest an online URL encoder as "Method 1"), and put
  it in the MCP URL.
- Sealos Skills for coding agents (above).

### Notifications

- Billing notices driven by the debt controller (warning → approaching
  deletion → deletion states). No documented user-configurable channels for
  deploy failures or app health.

### Multi-server and networking

- Self-hosted: multi-master HA clusters, dual-stack, offline installs from
  cluster images, private registry sync. All nodes must reach each other and the
  script must run on the first master.
- Hosted: several regions; a workspace lives in one.

### Team and collaboration

- Workspaces with invitations and roles; per-workspace quotas; the Cost Center
  shows hourly bills per workspace.
- Billing lifecycle for a negative balance: normal → warning → approaching
  deletion (default 4 days) → immediate deletion (default 3 days) → final
  deletion, with workloads suspended and then removed.

### Developer experience and onboarding

- Web desktop ("use the cloud like a PC" was the 2023 tagline) and, in 2026, a
  project canvas where containers, databases and ingresses are nodes and drawing
  an edge wires them together.
- A built-in agent turns a request into "a structured form for you to approve,
  not an unverified wall of text".
- Self-hosting: one script (`install-v2.sh`) on the first master; a domain with
  a wildcard record (or `nip.io`); recommended **8 CPU, 16 GB RAM and 100 GB
  disk per node**, with system components taking about 2 CPU/2 GB per master
  and 1 CPU/1 GB per node. Sealos Cloud can only be installed on a
  Sealos-installed Kubernetes, and the installer's example output shows
  Kubernetes 1.28.15.
- The self-hosting pages exist only in Chinese: the English page carries the
  title and nothing else (read 2026-09-30).

## What users love

- **Databases with HA in a click, at scale.** One engineer runs 6,000+
  database instances across four regions on KubeBlocks without a DBA
  (KubeBlocks case study, December 2024 figures). "Users can start a
  MySQL/PostgreSQL/MongoDB highly available database in 30 seconds" is the
  recurring line in community write-ups (dev.to, 2024).
- **Cheap, predictable hosted plans.** The $7 and $25 first-purchase plans are
  what people recommend it for: "Sealos has a $7 and $25 plan and work with
  Next.js" (Hacker News, 2026-02-10). The pricing page is built around a fixed
  price against Railway's metered bill.
- **DevBox with a local IDE.** Coding in Cursor or VS Code against a cloud
  environment and shipping the same environment as a release is the product's
  most distinctive idea, and the one Sealos itself leads its docs with.
- **The installer.** "Sealos – run Kubernetes cluster in one command" (Hacker
  News, 2022): the one-command HA Kubernetes install, with offline "cluster
  images", was the whole project from 2018 to 2022 and is still maintained
  (`sealos run`, `sealos add`).
- **A testimonial worth reading carefully:** "Sealos replaced our entire
  staging infrastructure. Our DevOps ticket volume dropped by 80% and we cut our
  cloud bill in half" is attributed to the CTO of FastGPT (homepage,
  2026-09-30) — FastGPT is Labring's own product.

## What users complain about

- **Buzzwords over substance, and doubt about momentum.** The February 2026
  Hacker News thread for "Sealos – AI Native Cloud Operating System" (22 points)
  is almost entirely negative: "I am once again asking for a moratorium on
  calling something an 'operating system' ... you built a fancy GUI frontend to
  k8s"; "If Buzzword bingo evolved into an abandonware operating system"; "the
  development seems to crease. Last release 2025 and the issues are stalling"
  (a reply notes commits continue). The release record supports the last
  point: no stable release since 2025-11-17.
- **DevBox's design.** The October 2025 post "We reduced a container image from
  800GB to 2GB" (90 points, 84 comments) described an 11 GB log file copied
  into 271 committed layers. Commenters: "The real lesson they should learn is
  to not rely on running images and then using 'docker commit'"; "This does
  seem bonkers to me"; and a privacy objection — "Is it spooky that they said
  they looked inside a customer's image to fix this?"
- **Trust and opacity.** From the same thread: "There is practically no
  publicly available information about your company, other than that it appears
  to be held by a Chinese entity called Labring." Several commenters called the
  post LLM-written.
- **Self-hosted installs that do not come up.** Open issues in 2026 alone:
  #6741 "sealos-cloud failed to initialize admin user" (2026-03-01), #6847
  "cannot log in after a self-signed install with install-v2.sh" (2026-03-27),
  #6902 "sealos-cloud unreachable after install" (2026-04-22), #6591 install
  failed on WSL Ubuntu 24.04, #6424 `sealos run` hangs syncing images to the
  5050 registry. Upgrades: #5657 "upgrade from 1.30.x to 1.31.x fails" (open
  since 2025-06-19); #7326 "support k8s >1.33" (2026-09-13).
- **Heavy for a small server.** 8 CPU/16 GB per node recommended, a wildcard
  domain required, and the Chinese-only self-hosting docs put it out of reach of
  the single-VPS user.
- **The licence change.** Apache 2.0 became a Sustainable Use License on
  2025-07-25 (PR #5719, tracked in issue #5722), while the homepage still says
  "Yes. Sealos is open source".
- **Billing edge cases (older, closed):** #3273 "delete database will not stop
  cost" (2023) and #4179 "cannot delete the application" on cloud.sealos.io
  (2023).

## Security record

Two CVEs, both from 2023, both in the multi-tenant control plane (NVD and the
repository's security advisories page, read 2026-09-30):

| ID | Published | Severity | What |
|---|---|---|---|
| CVE-2023-33190 / GHSA-74j8-w7f9-pp62 | 2023-06-29 | Critical (NVD 9.9; other scorers 9.8–10) | Improper RBAC configuration let a user obtain cluster control permissions — "could control the entire cluster deployed with Sealos, as well as hundreds of pods". Fixed in 4.2.1-rc4. |
| CVE-2023-36815 / GHSA-vpxf-q44g-w34w | 2023-07-03 | High (7.3) | Billing permission flaw: users controlled the `sealos.io/v1/Payment` resource and could recharge any amount for 1 RMB. |

Nothing since 2023 in NVD (keyword "sealos": 2 results) or the GitHub advisories
page. A `SECURITY.md` was only requested in August 2025 (issue #5846). Worth
noting for the "not copy" list: the documented MCP authentication puts a full
kubeconfig in a URL (and suggests pasting it into a public online encoder), and
the install docs show a fixed default admin password. Neither is a CVE; both are
the kind of thing that becomes one.

## Against Skifity

Skifity statuses follow `docs/checklist.md`: almost everything cluster-facing is
**Written, never run** (ADR-0010). A verdict of "Skifity ahead" on a Written
feature means ahead on paper.

| Capability | Sealos | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy from Git with detection | 2026 "AI Pilot + Git"; previously images and DevBox only | Railpack/Nixpacks/Dockerfile build Job in-cluster, GitHub/GitLab/Gitea webhooks (`internal/builder`, `internal/gitsrc`); Written | Parity (Skifity's older and more explicit) |
| Deploy a local folder | Via Sealos Skills from an agent | `skifity up` sends the folder, no repository (`internal/cli/up.go`, ADR-0022); Written | Parity |
| Config change without rebuild | Not a stated property | Build fingerprint separates image inputs from runtime (ADR-0007); build-time variables flagged (`store.Variable.BuildTime` in `internal/store/models.go`) | Skifity ahead |
| Cloud development environments | DevBox (SSH, IDE, release as image) | None (grep for `devbox` in `internal/`, `web/src`: 0 files) | Absent in Skifity (by fit, see below) |
| Compose / other-platform import | Guide plus marketed `vercel.json`/`render.yaml`/compose migration tool | Compose file detected; the form offers its services and creates one app per pick (`internal/builder/detect.go`, `internal/api/apps_handlers.go:257`) | Behind (one service at a time) |
| Automatic address and HTTPS | Generated subdomain, cert-manager for custom domains | `sslip.io` address over HTTP on purpose (ADR-0015), wildcard domain setting (`internal/settings/settings.go` `KeyWildcardDomain`), cert-manager on first domain; Written | Parity |
| Own TLS certificate | `kubectl` a TLS secret in Terminal | No upload path (grep for custom certificate: none) | Behind |
| TCP / non-HTTP exposure | NodePorts, rationed by plan | NodePorts and LoadBalancers forbidden by quota in app namespaces (`internal/kube/namespace.go:113-116`) | Behind (deliberate) |
| No-public-IP exposure | — | Cloudflare tunnel component (`internal/cluster/tunnel.go`); Written, never reached Cloudflare | Skifity ahead |
| Per-app firewall | — | IP/country/ASN rules at the edge (`internal/edgerules`, `internal/guard`); Written | Skifity ahead |
| Managed databases | PG, MySQL, MongoDB, Redis (+Kafka, Milvus) via KubeBlocks, HA replicas | PG via CloudNativePG, Redis, MySQL (`internal/dbsvc/manager.go:160-165`); Written | Behind (no MongoDB/Kafka) |
| Reach a database from a laptop | DB studio; NodePort external access | None: no tunnel, port-forward or external access (grep `port-forward`/`PortForward` in `internal/cli`, `internal/api`: none) | Behind |
| Object storage | Built-in S3 buckets | MinIO template only (`internal/templates/catalogue/minio.yaml`) | Behind |
| DB backups and restore | KubeBlocks scheduled backups to object storage, PITR in the operator | Scheduled/manual logical dumps to any S3 bucket, restore (`internal/backup`, ADR-0012); Written, never taken | Parity on paper; behind on PITR |
| Volume backups | Not documented | Tar of a volume to S3 and restore (`docs/backups.md`); Written | Skifity ahead |
| Autoscaling | HPA on CPU/memory average | HPA, plus scale-to-zero through KEDA (`internal/kube/scaletozero.go`); Written | Skifity ahead |
| Scaling safety check | Docs warning only | `check_scaling_readiness` reads the app for SQLite, local sessions, volumes (`internal/deploy/scaling.go`) | Skifity ahead |
| Multi-node, HA control plane | Mature installer, odd masters, lvscare | SSH provisioning, k3s join, promote to control plane (`internal/provision`); Written | Behind (maturity) |
| App metrics | Live CPU/mem/disk, history in App Launchpad | Point-in-time usage from metrics-server (`internal/kube/client.go:319`), no history | Behind |
| Logs | Per-app logs, Loki underneath | Live SSE stream plus previous container's log (`internal/api/stream_handlers.go`); no retention or search | Behind |
| Alerts / notifications | Billing states; operator alerts to Feishu/WeChat | Telegram, Discord, webhook, email and plugin-provided kinds on 7 events (`internal/notify/notify.go:41-47`); Written | Skifity ahead |
| Sign-in security | Platform login; SSO not documented | Argon2id, TOTP, recovery keys, OIDC with PKCE (`internal/auth`); Works | Skifity ahead |
| Roles | Three K8s Roles per workspace | owner/admin/member (`internal/store/models.go:9-11`); no read-only role | Parity |
| Audit log | Not documented | Team audit log, 365-day retention (`internal/store/retention.go:42`) | Skifity ahead |
| Tenant isolation | Namespace per workspace, Cilium, runtime sandboxing claims | Namespace per environment with default-deny policy, quota, LimitRange, restricted PSA (`internal/kube/namespace.go`); panel side Works, network Written | Parity (Sealos has more isolation layers, built for strangers sharing a cluster) |
| Preview environments | Not documented | Per-PR/branch preview env, forks get no secrets, commit status and PR comment, 7-day reclaim (`internal/api/webhook_handlers.go` `deployPreview`, `internal/deploy/gitreport.go`, `internal/watch/watch.go:136`); Written | Skifity ahead |
| Templates | "100+"/"200+", AI-heavy | 282, every image pinned to a verified version (`internal/templates/catalogue`, 282 YAML files) | Skifity ahead on count and pinning |
| CLI | Installer CLI; apps via kubectl | App CLI with `--json` everywhere (`internal/cli/commands.go`) | Skifity ahead |
| MCP | Remote, per-service, kubeconfig in URL | Local stdio, 15 tools, token auth, errors with cause/impact/fix (`internal/mcpserver/server.go:74`, tools at 314-391) | Skifity ahead on safety; behind on remote access |
| Agent skills | Sealos Skills for 9 agent hosts | None (no `SKILL.md` in the repository) | Absent in Skifity |
| IaC / declarative config | Everything is a CRD; templates are YAML | Export only (`internal/cli/export.go`); `skifity.toml` just points at an app (`internal/cli/project.go:25-35`) | Behind |
| Cost visibility | Cost Center, hourly bills, plan calculator | Components show their memory cost before install (`internal/settings/settings.go` `Components`); no per-app cost | Behind (different meaning of cost) |
| Team | Workspaces, invites, quotas | Teams, invitations, projects, environments, shared project variables (`store.SharedVariable`) | Parity |
| Canvas | Live canvas, draw an edge to wire | Project canvas of apps, databases and links (`internal/api/integrations_handlers.go:387`); no live usage on cards | Behind (slightly) |
| Footprint / minimum server | 8 CPU/16 GB/100 GB per node recommended | 1 GB minimum, panel 35 MiB idle (`docs/performance.md`); k3s not measured | Skifity ahead |
| Languages | Chinese and English (docs partly Chinese-only) | Five complete UI languages, CI-enforced (`web/src/locales`) | Skifity ahead |
| Licence | Source-available since 2025-07-25 | Repository's own licence; no phone-home (README) | Skifity ahead for self-hosters |
| Released and installable | Yes, since 2018 | No tag yet; installer refuses (`docs/checklist.md`) | Behind |

## Gaps worth closing in Skifity

**What fits and what does not.** Sealos is two products: a Kubernetes installer
(which Skifity also is, for k3s) and a multi-tenant public cloud (which Skifity
deliberately is not — see "Not on this roadmap" in `docs/roadmap.md`). Almost
everything that makes Sealos Cloud work — metering, balances, debt-driven
suspension, CockroachDB across zones, per-user kubeconfigs, runtime sandboxing
for strangers — exists because strangers share a cluster and pay by the hour.
None of it belongs in a single-binary panel on a VPS somebody owns. What
transfers is the user-facing surface: the database studio and external access,
agent skills, whole-stack import, and seeing what an app costs.

### P1 — Reach a database from a laptop (`skifity db connect`)

- **What:** `skifity db connect <db>` opens a local port that tunnels through
  the panel (authenticated with the same token) to the database's Service, so
  `psql`, TablePlus or a migration script works from a laptop. A panel button
  gives the one-line command.
- **Evidence:** Sealos sells a built-in DB studio and NodePort access in every
  plan; Porter ships `porter datastore connect`; Qovery ships `qovery
  port-forward`. Skifity's docs have no answer to "how do I open my database"
  (grep `port-forward`, `connect` in `docs/`: nothing relevant), and NodePorts
  are forbidden in app namespaces (`internal/kube/namespace.go:113-116`), so the
  only path today is SSH plus `kubectl port-forward`.
- **Fit:** a streaming endpoint in `internal/api` (authorized with the
  existing `authorizeDatabase` in `internal/api/api.go:498`, audited), a port-forward through the
  cluster client in `internal/kube`, and a CLI command in `internal/cli`. No
  NodePort, nothing opened on the server.
- **Size:** M.
- **Without a cluster:** the authorization, audit entry, CLI listener and
  byte-stream proxy can be tested against a fake upstream; the port-forward
  itself needs a cluster.

### P1 — Import a whole Compose file (and `render.yaml`) as one project

- **What:** today a Compose file is read and its services offered one at a time
  (`internal/builder/detect.go`, `internal/api/apps_handlers.go:257`). Offer
  "create all": every service becomes an app in the same environment, a
  `postgres`/`mysql`/`redis` service becomes a managed database linked by
  variable, named volumes become volumes, and everything the importer could not
  carry over is listed (the `ComposeWarnings` field already exists).
- **Evidence:** Sealos' homepage leads its migration pitch with "paste your
  vercel.json, render.yaml, docker-compose.yml"; Qovery's 2026 migration agents
  read `render.yaml` and `railway.json`. The audience Skifity courts arrives from
  Coolify and Dokploy with Compose files (see `docs/research/competitors.md`).
  38 of Skifity's own templates are already multi-app installs, so the "several
  apps land together and reach each other by name" machinery exists.
- **Fit:** extend the detector's Compose reader into a plan, reuse the template
  installer's multi-app path (`internal/templates`), a confirm screen on the new
  app page.
- **Size:** M.
- **Without a cluster:** yes — parsing, the plan and the created rows are unit
  tests; the confirm screen is covered by the Playwright test.

### P1 — Agent skills shipped in the binary

- **What:** `skifity skills install [--project]` writes a small skill pack
  (`SKILL.md` files) into `.claude/skills` or `.agents/skills`: deploy this
  folder, diagnose a failed deployment, add a database, check scaling
  readiness — each driving the CLI with `--json` and quoting `code`/`fix` from
  errors. Versioned with the binary, so a skill never describes commands the
  installed CLI does not have.
- **Evidence:** all three products in this batch shipped skills in 2025–2026
  (Sealos Skills for nine agent hosts; Qovery's eight skills; Porter's
  `agents.porter.run` installer registers MCP and installs skills). Skifity
  already has the hard parts — an MCP server, `llms.txt`, `detect-upload`,
  errors with a fix — and no packaging for agents that prefer skills to MCP.
- **Fit:** embedded files in `cmd/skifity` or `internal/cli`, one command.
- **Size:** S.
- **Without a cluster:** yes — a test that every command a skill names exists
  in `printUsage` and accepts `--json`.

### P2 — What an app costs, in money

- **What:** let the owner type each server's monthly price; show each app's and
  environment's share of it (requests against the node's allocatable capacity),
  plus "this app reserves 512 MiB and uses 90" as a right-sizing hint.
- **Evidence:** Sealos' Cost Center and its Railway calculator; Porter and
  Qovery both sell "only pay for what apps use" and right-sizing. For a
  self-hoster the bill is fixed, so the useful question is "which app is eating
  my VPS", which Skifity half-answers already (reserved vs used per instance,
  `docs/checklist.md` item 10).
- **Fit:** a server setting in `internal/store`, arithmetic in `internal/api`,
  a column on the servers and environment pages.
- **Size:** S.
- **Without a cluster:** yes for the arithmetic and the page; real usage needs
  metrics-server.

### P2 — MongoDB as a managed database

- **What:** a fourth engine beside PostgreSQL, Redis and MySQL.
- **Evidence:** Sealos and Qovery both list MongoDB as a first-class database;
  Skifity's `internal/dbsvc/manager.go:160-165` has three engines and the
  catalogue has no MongoDB template.
- **Fit:** a StatefulSet renderer in `internal/dbsvc/manifests.go` like the
  Redis and MySQL ones, `mongodump` in `internal/backup`.
- **Size:** M.
- **Without a cluster:** manifests and backup scripts yes (golden tests, the
  real-shell test in `internal/shellsafe`); running it needs a cluster.

### P2 — Upload a certificate you already have

- **What:** paste a certificate and key (or a CA-signed wildcard) for a domain
  instead of Let's Encrypt — for internal CAs, EV certificates, or hosts
  Let's Encrypt cannot reach.
- **Evidence:** Sealos documents it (as `kubectl` commands); enterprise and
  intranet users ask for it on every platform. Skifity has no path (grep for
  custom certificate/`tls.crt` in `internal/`: none).
- **Fit:** a sealed secret per domain (`keyring.Seal` with the domain as
  context), a TLS Secret rendered in `internal/kube`, a field on the domains
  tab.
- **Size:** S.
- **Without a cluster:** yes for storage, validation (key matches certificate,
  hostname covered, expiry) and manifests; serving it needs a cluster.

### P2 — Live usage on the project canvas

- **What:** the canvas already draws apps, databases and links
  (`internal/api/integrations_handlers.go:387`); put each instance's current CPU
  and memory on its card, and let drawing an edge create a database link.
- **Evidence:** it is the first item in Sealos' "eight things" list.
- **Size:** S. **Without a cluster:** the page yes, with fixture data.

## Things to deliberately not copy

- **Metering, balances and debt-driven deletion.** Sealos' billing controllers
  poll every namespace every minute and delete a workspace a few days after its
  balance goes negative. That is a hosting business. Skifity's roadmap already
  rules out a hosted version; a self-hosted panel that could delete a user's
  apps over a number would be the worst feature it could ship.
- **Two distributed databases for the panel's own state.** CockroachDB plus
  MongoDB is right for a multi-region public cloud and absurd for one panel;
  Skifity's SQLite is the correct answer for its shape (ADR-0004).
- **DevBox's "commit the running container" model.** Environments persisted as
  ever-growing image layers, an SSH daemon in every container, and engineers
  inspecting customer images to repair them: the Hacker News reaction was the
  right one. Skifity's one-off command in the app's own image
  (`web/src/components/app/console-tab.tsx`) is the defensible version of "let
  me run something in there".
- **A kubeconfig as the MCP credential, in a URL.** Skifity's scoped API tokens
  (`internal/auth/scopes.go`) are what an assistant should hold. Never document
  pasting a credential into a third-party web page.
- **A fixed default admin password in the install docs.** Skifity's one-time
  setup token is the right pattern; keep it.
- **"AI-native cloud operating system".** The February 2026 thread shows what
  that phrase costs with the exact audience Skifity wants. Skifity's
  "self-hosted apps, powered by Kubernetes" is plainer and better.
- **Changing the licence after the community built the stars.** And then
  telling people it is still open source.
- **An LLM gateway that resells tokens (AI Proxy).** It makes sense when you
  bill the tenant; a self-hosted panel has nobody to bill and no reason to sit
  between a user and their model provider.
- **8 CPU/16 GB per node and a mandatory wildcard domain.** The whole point of
  Skifity is the 1 GB VPS and an address that works with no DNS.
- **Chinese-only self-hosting docs behind an English title.** Skifity's rule
  that a missing translation fails the build is the fix, and it applies to
  documentation as much as to the UI.

## Sources

All read 2026-09-30.

- https://github.com/labring/sealos (repository page, README, licence section)
- GitHub API repository search for `repo:labring/sealos`, `org:labring`,
  `repo:labring-actions/templates` (stars, forks, open issues)
- https://github.com/labring/sealos/releases
- https://github.com/labring/sealos/pull/5719 and https://github.com/labring/sealos/issues/5722 (licence change)
- https://github.com/labring/sealos/security/advisories
- GitHub issues #6741, #6847, #6902, #6591, #6424, #5657, #7326, #5846, #3273, #4179 in labring/sealos
- https://sealos.io/ (homepage, FAQ, comparison table, testimonial)
- https://sealos.io/pricing
- https://sealos.io/sealos-skills
- https://sealos.io/llms.txt (full English docs: overview, quick start, DevBox guides and architecture, app deploy guides, databases, AI proxy, MCP, app store, system design)
- https://sealos.io/zh-cn/docs/self-hosting/install (requirements, install script, default credentials)
- https://sealos.io/zh-cn/docs/system-design/system-architecture
- https://sealos.io/zh-cn/docs/system-design/billing-system
- https://sealos.io/zh-cn/docs/system-design/user-system
- https://kubeblocks.io/blog/mangage-6k-db-instance-with-kubeblocks
- https://nvd.nist.gov (API keyword search "sealos", "labring")
- https://app.opencve.io/cve/?vendor=sealos_project and https://app.opencve.io/cve/?product=sealos&vendor=sealos
- https://news.ycombinator.com/item?id=46869024 (Sealos – AI Native Cloud Operating System, 2026-02)
- https://news.ycombinator.com/item?id=45719237 (We reduced a container image from 800GB to 2GB, 2025-10)
- https://news.ycombinator.com/item?id=44048976 (Envoy for 2k tenants; 87,000 users, 2025-05)
- https://news.ycombinator.com/item?id=49032248 (Show HN, 2026-07)
- https://news.ycombinator.com/item?id=46967757 (plan recommendation, 2026-02)
- https://news.ycombinator.com/item?id=32504992 (installer, 2022)
- https://dev.to/carsonyang/sealos-cloud-operating-system-simplicity-affordability-and-liberation-in-cloud-management-2ld8
- Skifity sources cited inline, read in this repository on 2026-09-30.
