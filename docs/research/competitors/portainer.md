# Portainer

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail. Brought up to date on
> 2026-11-08: facts about Portainer that changed are corrected in place, and "Since 2026-09-30"
> says what changed, with its sources.

Portainer is a web UI and API for operating containers on Docker, Docker Swarm,
Podman, Kubernetes and Azure ACI, from one host to fleets of edge devices. It
is built by Portainer.io (New Zealand) for IT operations teams in companies that
run containers without a platform team, and has long been the default homelab
Docker GUI. Architecture: a single Portainer Server container (Go API, a
TypeScript/React front end) that keeps its own state in **BoltDB** (`bbolt`, in
the `portainer_data` volume, optionally encrypted with a secret at start-up);
it talks to each environment directly, through a stateless **Portainer Agent**,
or through an **Edge Agent** that dials out. For Kubernetes it acts as an
authenticating proxy in front of the API server and maps its own roles onto
Kubernetes RBAC. Licences: Community Edition (CE) is **zlib**, "no license key,
no telemetry"; Business Edition (BE) is proprietary. Pricing is per node:
BE is **free for 3 nodes** ("3 Nodes Free", "over 110,000 free Portainer
Business licenses active today" per the CEO on 2026-09-11); Starter $1,045/year
(5–15 nodes, ≤16 vCPU per node, community support); Scale $2,095/year (5–25
nodes, 9×5 support); Enterprise custom; a Home & Student plan. Maturity: since
2016; monthly STS releases and an LTS every four months — **2.45.1 LTS on
2026-09-17** (2.45.3 on 2026-10-21 is the latest LTS patch on 2026-11-08), 2.39
LTS maintained to Nov 2026, and **3.0.0 STS released on 2026-10-29 (3.0.1 on
2026-11-04) as a "Kubernetes-first codebase", with no CE build**: CE 2.45 is
the last Community Edition, and gets 2.45.x fixes until August 2027.
Adoption: `portainer/portainer` has 38,611 stars, 2,911 forks and 759 open
issues and PRs; `portainer/portainer-ce` has 1,553,607,356 Docker Hub pulls and
`portainer/agent` 1,603,165,141 (all as of 2026-09-30; the figures of
2026-11-08 are under "Since 2026-09-30").

## Feature inventory

### Deploy sources and builds

* **Docker/Swarm**: containers from a form; **stacks** (Compose) from a web
  editor, an upload, a URL or a **Git repository** with GitOps updates by
  polling or webhook (BE adds change windows, relative paths, stored
  credentials, and in 2.45 a central **Sources** view and a guided workflow with
  parallel batches and "automatic pause or rollback"); image build from an
  uploaded Dockerfile; webhooks to redeploy a service.
* **Kubernetes "Add with form"**: namespace, name, registry and image,
  environment variables, **ConfigMaps**, **Secrets**, persisted folders with an
  isolated/shared data-access policy, resource reservations, replicated or
  global deployment, an **HPA** toggle (min, max, target CPU), placement rules on
  node labels, and "Publishing the application" by choosing a **ClusterIP,
  NodePort or LoadBalancer** Service. Applications deployed from a form are
  edited with the same form.
* **Kubernetes from code**: a manifest in the web editor, from Git (GitOps) or a
  URL; **Helm** charts from a Helm repository with an editable `values.yaml`
  and a manifest preview, or (BE) from Git with GitOps updates and "Always
  upgrade Helm release to ensure compliance with source".
* **Rollback**: Kubernetes applications can be rolled back to a previous
  revision; the built-in Operator role exists to "redeploy or rollback
  applications".
* No source-code builds: no buildpacks, no language detection, no build
  pipeline. (Portainer-Run, below, adds this for Kubernetes.)
* Stacks created outside Portainer get **"Limited control"**: Portainer sees the
  containers but not the Compose file, env or history.

### Domains, TLS and routing

* Portainer does not issue certificates or run a proxy for apps. On Kubernetes
  it has an **Ingresses** UI (host, path, service) over whatever ingress
  controller the cluster runs; admins can allow ingress classes per namespace or
  restrict ingress creation to admins. On Docker, routing is the user's own
  Traefik/Caddy container.
* Portainer's own UI: its TLS certificate, "Force HTTPS only".

### Databases and services

* No managed databases. Databases are containers from app templates (MySQL,
  MariaDB, PostgreSQL, MongoDB, Redis, SQL Server, CockroachDB…) or Helm charts.
  No backups, credentials management or connection wiring.

### Storage and backups

* Docker volumes; Kubernetes **Persistent Volumes, PVCs and Storage Classes**
  (2.45: resize a PVC, change the reclaim policy, set the default class);
  per-namespace storage quotas.
* **Backups cover Portainer, not the apps**: "It does not back up what you have
  deployed on your environments (for example, containers, stacks, services,
  volumes, etc)." The backup (settings, users, roles, environments, stack
  files, Git credentials, custom templates…) goes to a browser download, S3 or,
  since 2.45, **Azure Blob** or a **scheduled local path** with retention.

### Scaling and high availability

* Swarm service replicas; Kubernetes replicas and HPA from the form; placement
  constraints; the cluster's own scheduling and rollout.
* Portainer Server is one instance with a local database; managed workloads keep
  running when it is down.
* BE provisions new clusters (**KubeSolo** and Talos) and imports kubeconfigs.
  KubeSolo is Portainer's single-node Kubernetes, "under 200MB of RAM".

### Observability (logs, metrics, alerts, uptime)

* Container logs, stats, console and attach; Kubernetes application logs,
  events, and a browser `kubectl` shell; node and cluster CPU/memory graphs
  when metrics-server is present; GPU view (2.45).
* **Alerting** (BE, admins only, GA in 2.45): rules in three categories
  (Portainer, Security, Environment) with multi-severity thresholds, new
  Kubernetes rules (etcd, API server, TLS certificate expiry, NotReady nodes),
  silences, and delivery to **Slack, Microsoft Teams, email or webhook**.
* **Observability policy** (BE, 2.45) connects namespaces to a OneUptime
  instance for logs and metrics.
* No HTTP uptime check of apps.

### Security, auth, roles, SSO, audit

* Internal authentication (minimum password length, default 12), **LDAP** and
  **OAuth** in CE; **Active Directory**, provider presets, automatic user
  provisioning, **claim-to-team mapping** and admin-by-group in BE. 8-hour
  sessions by default.
* **RBAC** (BE): Administrator; Environment administrator; Edge administrator;
  **Operator** (redeploy/rollback, no create/delete); **Helpdesk** (read-only,
  no console); **Namespace Operator** (Operator scoped to namespaces);
  **Standard User** (full control of what they or their team deployed);
  **Read-Only User**; Team Leader. Roles are assigned per environment or group;
  an **Effective access viewer** shows what a user can do. On Kubernetes these
  map onto native RBAC, and 2.45 adds a native Kubernetes permission model.
* **Activity and authentication logs** (BE), including `kubectl exec`, container
  exec, node shell and pod-exec sessions; CSV export; syslog export.
* **Policies** (BE, fleet-wide, re-applied on drift by the agent): RBAC,
  Security (e.g. restricting privileged containers), Setup, Registry
  (allowed registries per namespace), **Pod Security Standards** per namespace
  in enforce/audit/warn modes, **Kubernetes Network Security** (NetworkPolicy
  presets), Docker image cleanup, and a banner with a change-confirmation
  prompt (all 2.45).
* Docker **environment security settings** keep non-admins from using
  privileged mode, host PID, device mapping, capabilities, sysctls,
  security-opt and bind mounts through Portainer — the settings behind several
  2026 CVEs below. Portainer itself has called them hiding rather than blocking
  since November 2026: they "can't block a request that never goes through
  Portainer" (blog, 2026-11-06).
* Cluster settings: restrict the default namespace, hide Secret contents from
  non-admins ("UI only"), node shell for admins (off by default), resource
  over-commit off, kubeconfig expiry.
* **SSRF allowlist** for proxied destinations (2.45, enforce/audit/off);
  **vulnerability scanning** with trivy-operator in 3.0 STS (released
  2026-10-29).
* Automatic **patch updates of Portainer itself** (opt-in; patches only).

### Preview environments and branches

* None. GitOps can deploy a branch or reference, but there is no per-PR
  environment.

### Templates and catalogue

* **App templates**: a JSON list at a configurable URL. The default v3 list has
  **74 templates**, mostly infrastructure (registries, web servers, databases,
  CI, agents, and about 25 industrial-IoT vendors); **33 of its 37 single-
  container templates use `latest` or no tag** (my count of
  `portainer/templates@v3`, last commit 2026-09-30).
* **Custom templates** for Docker/Swarm stacks and (BE, since 2.10) Kubernetes
  manifests, with variables; created from an existing stack.
* **Helm**: the default chart repository is still **Bitnami** ("the Bitnami
  repository we include by default", settings docs, read 2026-09-30), whose free
  catalogue Broadcom froze into `bitnamilegacy` from 2025-08-28 with
  latest-only free images.
* **Add-ons** (BE, 2.45): a Helm-based catalogue of Portainer's own tools; the
  first is Portainer-Run.
* **Image update indicator** (since 2.14): compares the local digest with the
  registry digest for the same tag and shows a coloured dot on containers,
  stacks and services; BE registry management adds "image update
  notifications".

**What Portainer does about running other people's apps.** It gives an
*expert* the controls and leaves the *app* to whoever deploys it. The expert
sets policies (pod security, network policy, registry allowlist, quotas,
no privileged containers, no bind mounts), roles and change windows; the user
then deploys a template, chart or Compose file inside those walls. Keeping the
app current is GitOps (polling a repo someone maintains) or noticing the
image-update dot and redeploying; there is no backup before an update, no data
rollback, no curated catalogue with a maintainer per app, and the default
sources — a template list of `latest` images and the Bitnami repository —
make the problem worse rather than better. Portainer's own answer for
non-experts is new: **Portainer-Run** (live by September 2026) lets "non-developer
business teams" drop the files an AI tool produced, and it detects the runtime,
commits to a sanctioned Git repository, deploys through Portainer's GitOps with
"sane resource requests and limits", offers scale, restart and roll back, and
exposes an **MCP server** so it can be driven from Claude Code. That is the
closest any competitor has come to Skifity's `skifity up` + MCP pitch — sold to
enterprises, on top of Portainer BE.

### CLI, API, IaC, integrations

* Full REST API with access tokens; the UI is a client of it.
* **Terraform provider** (official, 2.45) for environments, users, teams,
  stacks and more.
* **`portainer/portainer-mcp`** (official MCP server, 236 stars on 2026-09-30)
  was deprecated on 2026-10-23 in favour of **Portainer-Command**, an MCP
  gateway sold as a BE add-on (3 Nodes Free included) that gives agents
  "expiring, read only roles" and forces every change through GitOps.
* Kubeconfig download per user (expiry configurable), Docker/Kubernetes API
  proxying, webhooks. No first-party CLI.

### Notifications

* CE: none. BE: alerting to Slack, Teams, email and webhook (above).

### Multi-server and networking

* Any number of environments per server: Docker, Swarm, Podman, Kubernetes,
  ACI; environment groups and tags; Edge Agent for devices behind NAT (one
  customer runs "over 125,000 Docker devices"); edge stacks, edge jobs, edge
  configurations; BE provisions KubeSolo/Talos. 3.x adds **Portainer-D2K**,
  a Docker API translator that runs Compose on Kubernetes.

### Team and collaboration

* Users and teams; resource ownership (private, team, public); OAuth-driven team
  membership; per-environment role assignment; "Require a note on
  applications"; banners and change confirmation policies.

### Developer experience and onboarding

* One `docker run` (or a Helm chart) to start; admin created on first visit,
  within a five-minute setup window; docs on GitBook with Markdown copies of
  every page and an `llms.txt`; a Recommendations view (2.45) that lists
  configuration gaps with a link to fix each.

## What users love

* **The easiest way to see and poke containers.** Review-site summaries say it
  lowers the learning curve of Docker, is more intuitive than the Docker CLI
  and suits beginners (TrustRadius and G2, 2025–2026; paraphrased from the
  summaries a web search returned — both pages answered 403 to a direct read).
* **One pane for many environments.** "The de facto standard in managing
  containers, not only in home labs, but for those getting their feet wet
  running containers in production" (virtualizationhowto, 2026-09-23).
* **Swarm support.** "Portainer was one of the few friendly web interfaces for
  Docker Swarm" (botmonster.com, summarising r/selfhosted and r/homelab,
  September 2026).
* **Free where it counts.** 3 Nodes Free gives the full BE feature set; the CEO
  cites 110,000 active free licences (2026-09-11).
* **A Kubernetes UI non-specialists can use.** Portainer's own framing: a tool
  "to empower non-experts to deploy and manage container-based applications
  within enterprise Kubernetes", while "allowing EXPERTS to set the rules of the
  game" (Portainer blog, 2022, still its positioning).

## What users complain about

* **The 3.0 pivot.** CE freezes on 2.x, 3.x is Kubernetes-first and closed.
  The top r/selfhosted comment: "there is no money for us with you selfhosting
  guys" (205 votes); "Switched to Komodo a long time ago anyway" (103 votes);
  commenters counted Dockhand 16, Komodo 14, Arcane 13 as where they went
  (botmonster.com, September 2026). It's FOSS: "Portainer Cuts the Cord Between
  Its Free and Paid Editions" (2026-09-21).
* **Upsell inside the free product.** One user "already runs a fork that strips
  out the Business Edition ads" (botmonster.com); RBAC, AD, audit logs,
  alerting, registry management and Kubernetes YAML editing are BE-only.
* **"Limited control" over stacks.** Stacks created outside Portainer, or after
  switching agent types or upgrading, lose their editor and history; the
  workaround is to delete and recreate them, which risks downtime (GitHub issues
  #6631, #12348, #12614, #13156; discussions #9722, #12270).
* **Kubernetes depth.** Reviewers report that advanced Kubernetes work —
  complex manifests, CRDs — outgrows the UI and sends them back to `kubectl`,
  and that newcomers feel lost among the options (G2 review summaries,
  paraphrased as above; not verified against the page itself).
* **Security churn in 2026** (below): eleven CVEs published in 2026, several
  of them authorisation bypasses that give a low-privileged user the host.

## Security record

31 NVD results for "portainer"; 29 are Portainer's own (two are Vasion Print
products that embed Portainer). The 2025–2026 ones, all from NVD, read
2026-09-30:

| CVE | Published | CVSS | What |
|---|---|---|---|
| CVE-2025-49593 | 2025-06-17 | 6.8 | Registry auth headers or session tokens could leak to a malicious registry; fixed 2.31.0 / 2.27.7 LTS |
| CVE-2026-33590 | 2026-05-28 | 8.5 | **Insecure defaults** let non-admin users read host files and get root-equivalent access |
| CVE-2026-44848 | 2026-05-28 | 8.8 | Docker `/plugins/*` endpoints had no handler, so standard users could install and enable Docker plugins |
| CVE-2026-44849 | 2026-05-28 | 8.8 | Non-admin restrictions (privileged, host PID, devices, capabilities, sysctls, security-opt, bind mounts) not applied on the Swarm service API |
| CVE-2026-44850 | 2026-05-28 | 8.5 | "Disable bind mounts" checked `HostConfig.Binds` but not `HostConfig.Mounts` |
| CVE-2026-44881 | 2026-05-28 | 9.9 | Git-backed stacks: a symlinked `docker-compose.yml` returned any host file through the stack-file endpoint |
| CVE-2026-44882 | 2026-05-28 | 8.1 | Kubernetes proxy middleware wrote a 403 but did not `return`, forwarding the request anyway |
| CVE-2026-44883 | 2026-05-28 | 7.5 | JWTs accepted in `?token=` (used by exec/attach), leaking into logs and Referer |
| CVE-2026-44884 | 2026-05-28 | 6.5 | Any user could read any custom template file by enumerating IDs |
| CVE-2026-44885 | 2026-05-28 | 5.5 | Backup restore tar extraction path traversal |
| CVE-2026-55761 | 2026-07-08 | 5.9 | `/api/restore` and admin init reachable unauthenticated during the setup window |
| CVE-2026-72533 | 2026-08-11 | 8.8 | Docker proxy authorisation bypassed through non-canonical URL paths, "root-level access to the underlying Docker host" (CE through 2.44.0) |

Older history includes an unauthenticated websocket exec (CVE-2018-12678, 9.8),
cleartext LDAP credentials via the API (CVE-2018-19466), four access-control
bugs in 1.22.1 (CVE-2019-16872/74/77, up to 9.9), and bind-mount checks done
only in the browser (CVE-2020-24264, 9.8). The pattern repeats for a decade:
**Portainer proxies a raw Docker or Kubernetes API and authorises by inspecting
paths and request bodies**, so every new endpoint, field or path spelling is a
new bypass. Fixes ship quickly and in both streams (2.33.8, 2.39.2, 2.41.0 for
the May batch).

## Against Skifity

"Written" = code and unit tests exist, never run against a real cluster
(ADR-0010, `docs/checklist.md`).

| Capability | Portainer | Skifity (evidence) | Verdict |
|---|---|---|---|
| Build from source | None in Portainer; Portainer-Run (BE add-on) detects and builds for Kubernetes | Railpack/Nixpacks/Dockerfile build Job, detection before first build (`internal/builder`, `internal/api/detect_handlers.go`); Written | Skifity ahead |
| Deploy an image / Compose | Containers, Compose stacks, K8s forms, manifests, Helm | Image apps, multi-service templates; no Compose, no Helm, no raw manifests (`internal/templates`) | behind on formats |
| GitOps / push to deploy | Polling or webhook, change windows (BE) | Webhook deploy on push, per-PR previews, commit status reporting (`internal/gitsrc`, `internal/api/webhook_handlers.go`); Written | parity (different model) |
| Rollback | K8s revision rollback | New deployment with old image *and* settings; refuses a garbage-collected image (`internal/api/deploy_handlers.go`); Written | Skifity ahead |
| Kubernetes vocabulary | Namespaces, ConfigMaps, ClusterIP/NodePort/LoadBalancer in the main form | Apps, Instances, Servers, Domains, Databases; objects under Advanced (`/apps/{id}/advanced`, `llms.txt`) | Skifity ahead for non-experts |
| TLS and domains | Ingress UI only; no certificates | cert-manager on first domain, sslip.io free address, DNS record help (`docs/checklist.md` #3, ADR-0015); Written | Skifity ahead |
| Managed databases | None | Postgres (CNPG), MariaDB, Redis with generated, sealed credentials and linking (`internal/dbsvc`); Written | Skifity ahead |
| App data backups | None ("does not back up … volumes") | Database and volume backups to S3 with restore (`internal/backup`); never run | Skifity ahead |
| Backup of the control plane | Download, S3, Azure, scheduled local | `skifity admin backup-db` to a local file only (`docs/configuration.md`) | behind |
| Autoscaling | HPA from the form | HPA, KEDA scale-to-zero with interceptor, readiness checker (`internal/kube/scaletozero.go`); Written | Skifity ahead |
| Multi-cluster / fleet | Unlimited environments, Edge Agent | One panel, one cluster, by decision (`docs/roadmap.md` "Multi-cluster") | behind by design |
| Adding servers | Not Portainer's job (BE provisions KubeSolo/Talos) | SSH, firewall, k3s join, promote (`internal/provision`); Written | Skifity ahead for one cluster |
| Logs | Container/pod logs, kubectl shell | Live and previous-container logs; no shell by decision (`docs/roadmap.md`) | parity |
| Metrics and alerts | Graphs; alert rules to Slack/Teams/email/webhook (BE) | Live usage; seven events to Telegram/Discord/webhook/email + plugins (`internal/notify`) | behind on metrics, parity on channels |
| Roles | 8 built-in roles incl. Operator, Helpdesk, Read-Only, per environment (BE) | owner/admin/member, team-wide (`internal/store/models.go`) | behind |
| SSO | LDAP, OAuth (CE), AD, claim-to-team mapping (BE) | OIDC with PKCE, nonce, domain allowlist, auto-create (`internal/auth/oidc.go`, `internal/api/sso_handlers.go`); no LDAP, no group mapping | behind on mapping |
| Audit | Activity + auth logs, exec sessions, CSV, syslog (BE) | Team audit log, 90 days, Activity page (`internal/api/api.go`, `web/src/pages/activity.tsx`) | behind on export |
| Workload guardrails | PSS, NetworkPolicy, registry, security policies with drift repair (BE) | Per-environment PSA `restricted`/`baseline`, default-deny NetworkPolicy, ResourceQuota, LimitRange, always on (`internal/kube/namespace.go`, `podsecurity.go`); Written | parity; Skifity's are defaults, not options |
| Per-app edge firewall | None | IP/country/ASN rules (`internal/edgerules`, `internal/guard`) | Skifity ahead |
| Authorization design | Proxies raw Docker/K8s APIs, checks paths and bodies | Domain endpoints only; `authorizeApp`/`authorizeTeam`; route-walk test over every route (`internal/api`, `docs/checklist.md` #11); Works | Skifity ahead |
| Tokens in URLs | Accepted `?token=` until CVE-2026-44883 | Bearer header or cookie + CSRF header only (`internal/api/middleware.go`) | Skifity ahead |
| First-run takeover | Five-minute unauthenticated setup window (CVE-2026-55761) | One-time setup token printed by the installer, rate-limited (`internal/api/setup.go`) | Skifity ahead |
| Secrets | Kubernetes Secrets, "UI only" hiding | Sealed with context, write-only on every surface (`internal/crypto`, `docs/checklist.md` #4) | Skifity ahead |
| Preview environments | None | Per-PR (`internal/api/webhook_handlers.go`); Written | Skifity ahead |
| Catalogue | 74 default templates (33/37 `latest`), Bitnami Helm | 282 pinned, verified templates (`internal/templates`) | Skifity ahead |
| Image update awareness | Digest-compare indicator (2.14+) | None | absent in Skifity |
| API | Full REST | Full REST, same as CLI and MCP (`internal/api`) | parity |
| CLI | None first-party | `skifity` with `--json` (`internal/cli`); Works | Skifity ahead |
| MCP | Official server deprecated 2026-10-23 for the Portainer-Command gateway (BE add-on); Portainer-Run MCP | In the binary, errors with cause/impact/fix (`internal/mcpserver`) | parity |
| IaC | Official Terraform provider | `skifity export` to `kubectl apply`-able objects (`internal/api/export_handlers.go`); no Terraform | behind |
| Notifications | BE only | Built in (`internal/notify`) | Skifity ahead vs CE |
| Team | Teams, ownership, notes, change confirmation | Teams, invitations (`internal/api/teams_handlers.go`) | behind |
| Onboarding | One container | One script (no release tagged), in-panel docs, five languages (`web/src/locales`) | behind today, ahead on docs/i18n |
| Licence | CE zlib (frozen on 2.x), BE proprietary | Open repository, no licence key (`docs/faq.md`) | Skifity ahead |

## Gaps worth closing in Skifity

**P1 — Tell people when a newer image exists.** *What:* for image-sourced apps
(including every template app), check the image's own registry for the same tag
at a new digest, and for a pinned version (`1.4`, `2026.9.17`) for a newer tag
in the same series; show it on the app and in `get_app_status`. *Evidence:*
Portainer has shipped a digest-compare indicator since 2.14 and sells update
notifications in BE; Cloudron's whole product is timely updates. Skifity names
a version for every template and then has no way to say it is old. *Fit:* the
series logic already exists as `hack/resolve_tags.py`; port it to Go next to
`internal/registry`, go through `internal/netguard` like every other outbound
request, cache per image, and trigger only for apps that exist (the registry is
somewhere the user already pulls from, so this is not phoning home). *Size:*
S–M. *Without a cluster:* yes — against a fake registry in tests.

**P1 — A read-only role and an operator role.** *What:* "viewer" (sees apps,
logs, deployments; changes nothing) and "operator" (deploy, restart, roll back,
scale; cannot create, delete or change domains and secrets). *Evidence:*
Portainer's Helpdesk, Read-Only, Operator and Namespace Operator roles; Cloudron's
per-app operators. Skifity's three roles are ordered and the lowest, member, can
do everything inside a project (`internal/store/models.go`), which is too much
for a client, an intern or an on-call contractor. *Fit:* `roleRank` in
`internal/store/models.go` and the required-role argument already passed to
`authorizeApp` and friends; the route walk in `internal/api` tests proves every
route; API tokens already have read/write scopes (`internal/auth/scopes.go`) to
reuse. *Size:* M. *Without a cluster:* yes.

**P1 — Map SSO groups to teams and roles.** *What:* a claim name and a table
"group → team, role", applied at each sign-in. *Evidence:* Portainer's
automatic team membership by claim with regex and admin-by-group; Cloudron syncs
from LDAP/AD. Skifity's OIDC has a domain allowlist and auto-create only
(`internal/api/sso_handlers.go`), so every SSO user still has to be placed by
hand. *Fit:* `internal/auth/oidc.go` already verifies the ID token; add claim
reading and a settings table. *Size:* S. *Without a cluster:* yes (the SSO tests
run a fake provider).

**P1 — Scheduled panel backup to S3.** *What:* the SQLite backup that
`skifity admin backup-db` already takes, on a schedule, to the backup bucket,
with retention. *Evidence:* Portainer 2.45 added scheduled local and Azure
destinations next to S3; Cloudron restores a whole server from one backup.
Skifity's panel state is one file on one node (`docs/configuration.md`). *Fit:*
`internal/serverapp` minute tick, `internal/backup/storage.go`. *Size:* S–M.
*Without a cluster:* yes.

**P2 — A "recommendations" page.** *What:* one list of what is wrong or risky
now, each with a fix link: backups not configured, databases never backed up,
apps with no health path, apps on `baseline`, templates with a newer image,
servers near their disk or memory, the recovery key not saved. *Evidence:*
Portainer 2.45's Recommendations view; Skifity already computes most of these in
separate places (scaling readiness, quota headroom, the recovery-key gate) and
has the cause/impact/fix language to phrase them (`internal/errdoc`). *Fit:* a
`GET /api/teams/{team}/recommendations` that aggregates existing checks; a panel
page. *Size:* S–M. *Without a cluster:* yes.

**P2 — Export audit events.** *What:* CSV download and a syslog/webhook stream
of the audit log, beyond 90 days if the operator wants. *Evidence:* Portainer BE
sells syslog export and CSV as compliance features. *Fit:* `handleListAudit` and
`RecordAudit` (`internal/api/teams_handlers.go`, `internal/store/misc.go`); a stream could ride the
plugin event delivery (ADR-0021), which today carries ten events.
*Size:* S. *Without a cluster:* yes.

**P2 — Image vulnerability scanning as an optional component.** *What:* install
trivy-operator on request (like cert-manager and CNPG are installed on first
use) and show findings per app. *Evidence:* Portainer 3.0 STS ships exactly this
as a policy; running other people's images makes their CVEs your CVEs. *Fit:*
`admin.Get("/components")` already manages optional cluster components.
*Size:* M. *Without a cluster:* the component manifests and the report parsing
yes; findings need a cluster.

**P2 — A Terraform provider or at least an import path.** *What:* manage teams,
projects, apps, domains and variables as code. *Evidence:* Portainer shipped an
official provider in 2.45. Skifity's export produces Kubernetes objects, not
Skifity objects. *Fit:* a separate repository against the existing REST API.
*Size:* L. *Without a cluster:* yes.

## Things to deliberately not copy

* **Kubernetes words in the main form.** Portainer's "Add with form" asks for a
  namespace, ConfigMaps, Secrets and whether the Service is ClusterIP, NodePort
  or LoadBalancer. Portainer's own answer for non-experts, Portainer-Run, is a
  separate console that hides all of it — the clearest evidence that Skifity's
  bet (Apps, Instances, Domains; objects under Advanced) is the right one.
* **A pass-through API proxy with path-based authorisation.** Most of
  Portainer's 2026 CVEs (44848, 44849, 44850, 44882, 72533) come from forwarding
  raw Docker or Kubernetes calls and trying to recognise the dangerous ones.
  Skifity should never add a generic "Kubernetes API through the panel"; keep
  domain endpoints behind `authorizeApp`/`authorizeTeam`.
* **Credentials in query strings** for websockets or SSE (CVE-2026-44883).
* **Protections that are "UI only" or client-side** (the Secret-hiding setting;
  CVE-2020-24264). Every refusal belongs in `internal/api`.
* **An unauthenticated setup window** (CVE-2026-55761); Skifity's installer
  token is the better design.
* **Supporting every runtime.** Portainer carried Docker, Swarm, Podman,
  Kubernetes and ACI in one codebase and has now said that "every new
  capability … has had to be built three times"; it is dropping to
  Kubernetes-first. Skifity's single substrate is an advantage to keep.
* **Splitting the community off.** Freezing CE on 2.x and steering home users to
  a closed, node-capped licence cost goodwill overnight. Skifity has no editions;
  keep it that way.
* **Unpinned defaults.** A default template list where 33 of 37 container
  templates float, and a default Helm repository whose free tier stopped being
  maintained, hand the user an update problem on day one.
* **Losing track of what you deployed.** "Limited control" stacks are what
  happens when the panel is not the source of truth for what it runs; Skifity's
  store-first model and `skifity export` avoid it.

## Since 2026-09-30

Read on 2026-11-08. GitHub's API refused this sandbox, so releases and
repository figures were read through the ungh.cc mirror of GitHub's API; the
3.0 release notes, the lifecycle policy and the requirements page from
docs.portainer.io; CVEs from NVD and the OSV database. Issue counts were not
read again.

### Releases

* **2.45.2 LTS (2026-10-08).** Security: "Hide bind mounts for
  non-administrators" could be got round by a Compose file whose `configs`,
  `secrets` or `env_file` pointed outside the stack directory, and `include` is
  now disabled for non-admins; a refused or dry-run namespace deletion still
  removed Portainer's access policies, stack records and registry bindings;
  in-cluster HTTPS connections to the Kubernetes API skipped certificate
  verification during stack deploy and removal. Also a node-group column for
  EKS, GKE, AKS, Karpenter and kops node pools.
* **2.45.3 LTS (2026-10-21).** Breaking: certificates issued by eMudhra are no
  longer trusted after the compromise of eight of its intermediate CAs
  (CVE-2026-111943 in the notes), so an OAuth, LDAPS, registry or Git host that
  presents one fails, and "only the initial admin will be able to sign in" when
  it is the SSO provider. Security: `volumes_from` refused for non-admins under
  the bind-mount setting, one more way to inherit a host mount; Go 1.26.10 and
  `golang.org/x/net` 0.60.0, two critical CVEs among those fixed; kubeconfig
  tokens accepted only on the Kubernetes proxy route and issued per
  environment. A GitOps poll still running now makes the next one skip, rather
  than piling up redeploys on a slow repository.
* **3.0.0 STS (2026-10-29)**, the first Portainer 3 release, Business Edition
  only (the CE repository has no 3.x release on 2026-11-08):
  * **Vulnerability scanning**: a policy attached to an environment group
    deploys trivy-operator, an Image Vulnerabilities view lists workloads by
    worst severity with CVE detail, and an alert rule reports critical and high
    findings.
  * A **reconciliation engine** for GitOps (beta): server-side apply of
    manifests or Helm charts, readiness, pruning of what was removed from the
    source, drift reported or corrected; a workflow can deploy several
    artifacts with dependencies between them. Kubernetes stacks now keep an
    inventory and prune resources dropped from the manifest.
  * **D2K environments** (the Docker API served from a Kubernetes namespace);
    **KubeSolo installed on many machines over SSH**, each tested first with
    failures grouped by cause and a suggested fix, offline bundles for
    air-gapped machines; **automatic namespace provisioning**, a personal
    namespace and role per user; a Resource Quotas view with the effective
    ceiling; Gateway API CRUD; scoped kubeconfigs that can be listed and
    revoked; a user detail screen; an encrypted key-value store for add-ons;
    drift detection for Swarm stacks.
  * Security fixes in the same release: an admin-only route returned a GitOps
    source's stored Git password; a user with read access to a source could
    test its connection against an attacker's URL and send the stored
    credentials there; the last administrator could be deleted or demoted; the
    login rate limiter and the authentication log took the rightmost
    `X-Forwarded-For` entry without asking whether it was itself a proxy, so
    behind a chained load balancer one client could use up the sign-in
    attempts of everybody, and entries are now walked from the right across
    every header line; the S3 backup settings returned the stored secret key,
    and the S3 host is now checked against the SSRF allow list; cached
    service-account tokens and Kubernetes clients outlived their sessions; the
    GPU summary counted namespaces the caller could not see.
  * Breaking API changes: the stack Git webhook answers 202 with a body, and
    409 only while a deploy is already running; `addonsAvailable` became a
    string.
* **3.0.1 STS (2026-11-04)**: add-ons were unavailable on every Kubernetes
  deployment after an upgrade to 3.0.0.
* The lifecycle policy (read 2026-11-08) plans 3.1 STS in early November, 3.2
  in late November and **3.3 LTS in December 2026**, and still shows 2.45 LTS
  supported until May 2027.

### Community Edition and the Portainer 3 position

On 2026-11-06 the CEO published "Portainer 3 is Kubernetes-first, not
Kubernetes-only", which "clarifies our earlier announcement" because "many
people read it as 'Portainer is dropping Docker.'" Docker Standalone, Swarm
and Podman stay in Portainer 3 with bug and security fixes and no new features.
**CE 2.45 is the last Community Edition**: it gets the same 2.45.x fixes as BE
2.45 LTS, and none after August 2027 ("We are extending its support", which is
three months later than the lifecycle page). Moving to Portainer 3 means 3 Nodes
Free, unchanged and with more than 110,000 licences active, or a paid licence.
The post also renames the Docker security settings: they hide options in
Portainer and "can't
block a request that never goes through Portainer. We used to describe those
settings as blocking, and we renamed them to hiding because that is what they
actually do." Its case for Kubernetes is that every request passes the API
server, where a policy can refuse it whichever tool sent it.

### Portainer-Command and the AI products

`portainer/portainer-mcp` was deprecated in its 2.45.3 release (2026-10-23): "no
longer the supported way to use an LLM with Portainer", kept "without support,
warranty or SLA", and the repository "may be archived". The supported way is
**Portainer-Command**, listed as "Available now" and included with BE, 3 Nodes
Free too. An agent reads through an expiring read-only session (one hour by
default, eight at most) opened with a stated rationale, never holds a credential
that can change a cluster, and proposes changes as pull requests against
Portainer-Command's own GitOps repository that a named person approves.
Administrators allow or refuse agents per environment, with Secret access a
separate switch that is off by default; one click halts an environment or one
user's agents; model spend is shown per workspace; a built-in OpenCode workspace
runs unprivileged with its egress limited by NetworkPolicy. portainer.ai also
lists Portainer-Run (available, BE 2.44 or later), Portainer-AiGrid (beta) and
Portainer-AiSecure, a separately licensed product built on MisterShell (all read
2026-11-08).

### Numbers and pricing on 2026-11-08

* 38,791 stars and 2,934 forks (38,611 and 2,911 on 2026-09-30);
  `portainer/portainer-ce` 1,585,356,935 Docker Hub pulls and `portainer/agent`
  1,629,517,271 (1,553,607,356 and 1,603,165,141); `portainer-mcp` 241 stars
  (236).
* Pricing is unchanged: Starter $1,045/year, Scale $2,095/year (up to 24 vCPUs
  per node), Enterprise custom. Industrial and IoT pricing has moved to
  portainer.industries.

### Security

* NVD still returns 31 results for "portainer" (2026-11-08); none was published
  after 2026-09-30. The Go vulnerability database added GO-2026-6619 on
  2026-10-01 for CVE-2024-29296, a username enumeration by response timing fixed
  in 2024.
* Vulnetic.ai published "Portainer GitOps Bypass Enables Host Takeover" (on
  Hacker News on 2026-10-09). In CE 2.45.0 a standard user who could change a
  stack's Git repository got a privileged container with the host's root
  mounted: a manual redeploy checked the Compose file against the environment's
  security settings, and redeploys triggered by a webhook or a poll did not. The
  write-up says 2.45.2 added the check; 2.45.2's notes do not name it.
* Everything else fixed in the period came without a CVE (Releases, above):
  three more ways round the bind-mount setting in two weeks (`configs`,
  `secrets` and `env_file`; `include`; `volumes_from`), stored Git credentials
  returned or sent elsewhere through the GitOps source routes, and TLS
  verification skipped inside the cluster. The pattern named under "Security
  record" held, and Portainer's November post concedes that the setting can
  only hide.

### Users

* The November post is the measure of the reaction to 3.0: it was written
  because the first announcement was read as dropping Docker. Hacker News
  comments in the period are few and mild: "Personally I use docker with
  portainer to handle the stacks/containers, have done for many years. I like
  it" (2026-10-15). Grua, a new self-hosted PaaS, was introduced there by
  somebody who had used "portainer, easypanel, coolify, etc" and still found "a
  lot of work to setup and maintain those" (2026-11-06).

### For Skifity

Checked against the code on 2026-11-08.

* Already there: image vulnerability scanning (`internal/vulnscan`, `ca539aa`),
  which can also stop a deploy on a critical finding that has a fixed version
  (`Blocking` in `internal/vulnscan/policy.go`), where Portainer 3.0 reports and
  alerts; drift detection with an `app.drifted` event
  (`internal/api/drift_handlers.go`, `internal/notify/notify.go`); a team's last
  owner cannot be removed or demoted (`errdoc.LastOwner`,
  `internal/api/teams_handlers.go`); API tokens with resource scopes, an expiry
  and a network allowlist (`internal/auth/scopes.go`, `api_tokens.expires_at`);
  Go 1.26.10 in `go.mod`, the toolchain 2.45.3 moved to.
* The GitOps bypass does not carry over. A push deploys through the same
  `s.deployer.Deploy` as a deploy from the panel
  (`internal/api/webhook_handlers.go`), and what a pod may do is decided by its
  namespace's Pod Security level (`internal/kube/podsecurity.go`), which nothing
  in a repository changes. That is the write-up's own advice, to enforce at the
  orchestration boundary, and the argument of Portainer's November post.
* Of this file's gaps, closed since: a viewer role (Phase 85), SSO groups mapped
  to teams (Phase 92), the panel backed up to the bucket on a schedule (Phase
  84), Compose files as stacks (Phase 101), image vulnerability scanning. Still
  open: an operator role (the roles are owner, admin, member and viewer,
  `internal/store/models.go`), noticing a newer image in the registry (template
  updates still arrive only with a panel release, `docs/templates.md`
  "Updates"), a recommendations page, audit export, Terraform.
* New gaps:
  * **The panel and the firewall decide differently who is asking.** The address
    the panel uses for the sign-in rate limit, the flood guard and the API token
    network allowlist (`clientIP` in `internal/api/middleware.go`) is found by
    counting `SKIFITY_TRUSTED_PROXY_COUNT` entries back from the right of the
    *first* `X-Forwarded-For` line. The guard (`internal/edgerules/clientip.go`)
    walks every line from the right against trusted ranges, and its own comments
    say why both shortcuts are wrong: a proxy that adds a second line leaves the
    client's line first, and a hop count is wrong "the moment somebody adds a
    load balancer". Portainer fixed the same class in its sign-in limiter in
    3.0.0. Using the guard's function for the panel closes it. Size S.
  * **The backup storage endpoint is dialled without the outbound guard.** The
    S3 client is built on minio-go's default transport
    (`internal/backup/storage.go`), while `internal/netguard` exists for every
    address an administrator gives the panel to dial, and refuses the metadata
    service and loopback while allowing private ranges, so a MinIO on the LAN
    still works. Portainer 3.0.0 routed its S3 backup connections through its
    SSRF allow list. Size S.
  * **Agent changes that a person approves.** The MCP endpoint (`/api/mcp`,
    Phase 87) acts at once within the token's scopes. There is no queue of
    proposed changes, no per-environment switch for agents and no single control
    that stops every agent token (no approval code anywhere in `internal`).
    Portainer-Command turns every agent write into a proposal a named person
    approves. `skifity.yaml` plan and apply (Phase 108) already produce the diff
    such an approval would show. Size M.

## Sources

All read 2026-09-30.

* https://www.portainer.io/pricing — plans, node definition, trials
* https://www.portainer.io/blog/portainer-community-edition-ce-vs-portainer-business-edition-be-whats-the-difference (2026-01-05) — CE/BE differences, zlib, no telemetry in CE
* https://www.portainer.io/blog/portainer-3-0-is-coming (2026-09-11, with clarification) — 3.0, CE on 2.x, 110,000 free licences, Portainer-Run/IDP/Command/Operations/AiGrid, KubeSolo, D2K
* https://www.portainer.io/blog/portainer-a-kubernetes-management-platform-for-newbies-and-experts (2022-06-28) — positioning
* https://docs.portainer.io/whats-new.md — 2.45 LTS features (add-ons, policies, GitOps, alerting GA, backups, SSRF, Terraform)
* https://docs.portainer.io/3.0-sts/whats-new.md — 3.0 STS, vulnerability scanning
* https://docs.portainer.io/start/lifecycle.md — STS/LTS cadence, planned 3.x releases
* https://docs.portainer.io/start/requirements-and-prerequisites.md — release dates and tested versions
* https://docs.portainer.io/user/kubernetes/applications/add.md, /applications/edit.md, /applications/manifest/helm.md, /templates.md, /namespaces/add.md, /cluster/setup.md
* https://docs.portainer.io/user/docker/templates/application.md — app templates
* https://docs.portainer.io/admin/user/roles.md — RBAC roles
* https://docs.portainer.io/admin/settings/authentication.md, /authentication/oauth.md — auth, claim mapping
* https://docs.portainer.io/admin/logs/activity.md — activity logs
* https://docs.portainer.io/admin/settings/general.md — app template URL, auto patch updates, Bitnami default Helm repo, Portainer backup
* https://docs.portainer.io/faqs/getting-started/what-does-portainers-backup-include.md and /what-is-portainers-architecture.md
* https://docs.portainer.io/faqs/troubleshooting/stacks-deployments-and-updates/how-does-the-image-update-notification-icon-work.md
* https://docs.portainer.io/user/alerting.md and https://docs.portainer.io/admin/add-ons.md
* https://docs.portainer.io/advanced/db-encryption.md — BoltDB
* https://docs.portainer.ai/ — Portainer-Run
* GitHub search API: `repo:portainer/portainer` (stars, forks, open issues, zlib), `org:portainer` (portainer-mcp, kubesolo, d2k, portainer-run, templates)
* `git clone --filter=tree:0 https://github.com/portainer/portainer` — tag dates; `go.mod` for `go.etcd.io/bbolt`
* `git clone -b v3 https://github.com/portainer/templates` — 74 templates, `latest` count computed locally
* https://hub.docker.com/v2/repositories/portainer/portainer-ce/, /portainer-ee/, /agent/ — pull counts
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=portainer — CVE list and descriptions
* https://github.com/portainer/portainer/security/advisories (GHSA-rrmm-9v76-h3p4, GHSA-5fxq-qcf3-244w, GHSA-7fw3-x4r2-g7wc, GHSA-rpgq-m5fp-32wr, GHSA-mgq6-4x29-88r3, GHSA-jvp4-q659-95mj, GHSA-cqpq-2fgr-8mvc, GHSA-m8fg-67j7-cx4v) as referenced by NVD
* https://github.com/portainer/portainer/issues/6631, /12348, /12614, /13156; https://github.com/orgs/portainer/discussions/9722, /12270 — "limited control" stacks (via web search)
* https://itsfoss.com/news/portainer-community-edition-freeze/ (2026-09-21)
* https://www.virtualizationhowto.com/2026/09/portainer-3-0-changes-direction-what-does-it-mean-for-docker-home-labs/ (2026-09-23)
* https://botmonster.com/self-hosting/portainer-3-0-is-the-push-your-homelab-needed-to-switch/ — r/selfhosted and r/homelab reaction, vote counts
* https://dev.to/selfhostpilot/portainer-30-drops-the-community-edition-what-self-hosters-should-run-instead-22h8
* https://www.trustradius.com/products/portainer/reviews and https://www.g2.com/products/portainer/reviews?qs=pros-and-cons — review summaries (via web search)
* https://github.com/bitnami/charts/issues/35164 — Bitnami catalogue changes from 2025-08-28

Read 2026-11-08, for "Since 2026-09-30":

* https://ungh.cc/repos/portainer/portainer/releases — 2.45.2 (2026-10-08) and 2.45.3 (2026-10-21) release notes; no 3.x release in the CE repository
* https://ungh.cc/repos/portainer/portainer, /repos/portainer/portainer-mcp, /repos/portainer/portainer-mcp/releases, /orgs/portainer/repos — stars, forks, the portainer-mcp 2.45.3 release (2026-10-23)
* https://raw.githubusercontent.com/portainer/portainer-mcp/main/README.md — the deprecation notice
* https://docs.portainer.io/portainer-documentation/sts/release-notes.md — 3.0.0 (2026-10-29) and 3.0.1 (2026-11-04)
* https://docs.portainer.io/3.0-sts/whats-new.md — what's new in 3.0
* https://docs.portainer.io/start/lifecycle.md — planned 3.1, 3.2, 3.3 LTS; 2.45 LTS to May 2027
* https://docs.portainer.io/start/requirements-and-prerequisites.md — release dates of 2.45.2 and 2.45.3
* https://www.portainer.io/blog/portainer-3-is-kubernetes-first-not-kubernetes-only (2026-11-06) — CE 2.45 the last CE, fixes to August 2027, settings renamed to hiding
* https://www.portainer.io/blog/the-docker-api-ceiling (2026-09-21) — the reasoning behind 3.0
* https://www.portainer.io/pricing — plans unchanged
* https://portainer.ai/products/portainer-command and https://portainer.ai/pricing — Portainer-Command, the add-ons, Portainer-AiSecure
* https://www.vulnetic.ai/blog/portainer-gitops-bypass-enables-host-takeover (on Hacker News 2026-10-09, item 50020686)
* https://hub.docker.com/v2/repositories/portainer/portainer-ce/, /agent/ — pull counts
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=portainer — 31 results, none after 2026-09-30
* https://api.osv.dev/v1/query for `github.com/portainer/portainer` — GO-2026-6619 (2026-10-01)
* https://hn.algolia.com/api/v1/search_by_date, query "portainer" after 2026-09-30 — items 50110782, 50392771, 50020686
