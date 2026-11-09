# Zeabur (open source)

> **Status.** Written on 2026-11-08 against Zeabur's `main` at commit `89c2a15`
> (2026-11-08) and release v0.2.0, and against Skifity's working tree at `618dff2`
> the same day. No gap named here has been closed yet.

Zeabur (`zeabur-oss/zeabur`, oss.zeabur.com) is a web interface and API for running
services on a Kubernetes cluster you own. It shows each namespace as a project and
groups Deployments, StatefulSets, Services, Ingresses, Secrets and volumes into
services by following owner references, selectors, routes and env references. Its
pitch is the opposite of a platform with a database: **it keeps no state of its own**
(no database, no custom resources of its own; sessions live in the server's memory)
and **acts with the signed-in person's own Kubernetes credential**, so kube-apiserver
and RBAC decide every read and write and the chart's ServiceAccount is bound to no
Role. A change is planned first, shown as the objects and fields it will write,
carried in a 10-minute AES-256-GCM token, dry-run, and refused if somebody changed
the object meanwhile. Objects that Helm, Argo CD or Flux manage can only be
restarted. It runs three ways: **`zeabur-installer`** puts K3s, RKE2 or KubeSolo,
Zeabur Identity and Zeabur on one Linux server through a web flow reached over an
SSH tunnel; a **Helm chart** installs it into an existing cluster whose
kube-apiserver trusts Zeabur Identity; and **`zeaburctl ui`** serves it on a
laptop's loopback with the person's kubeconfig. **Architecture:** a Rust server
(about 161,000 lines in `crates/`) with an embedded React interface (about 106,000
lines of TypeScript, TanStack Router and Query, shadcn/ui through
`@zeabur-oss/design-system`), plus four sibling projects: **Zeabur Identity** (an
OIDC issuer with passkeys whose users are Kubernetes custom resources), **Zeabur
Builder** (`Build` and `BuildRun` resources, a buildkitd per namespace, a zot
registry, Keel to roll new images out), **Zeabur Catalog** (28 cluster components and
33 app templates, installed and measured in CI, every image pinned by digest) and
**Zeabur Proxy** (namespace-scoped reads of log and metric history from VictoriaLogs
and VictoriaMetrics). **Licence:** Apache-2.0 with a DCO. **Pricing:** free; no paid
tier, support plan or hosted control plane was found on oss.zeabur.com
(2026-11-08). **Maturity:** the repository was created on 2026-10-24 and its public
history starts on 2026-10-26 with "initial release from internal worktree"; v0.1.0
was withdrawn, **v0.1.1 (2026-10-28)** was the first published release, the
"pre-alpha" notice was removed on 2026-11-07, and **v0.2.0 (2026-11-08)** is called
the first stable release. Only the latest stable release gets security fixes
(`SECURITY.md`). **Adoption** (GitHub search API, 2026-11-08): 654 stars, 109 forks,
136 issues (45 open), 248 pull requests (201 merged, 23 open). One issue and one pull
request came from somebody other than the main author and the bots; 234 of the 262
commits on `main` are by one person. The sibling repositories have 10 (builder), 6
(identity) and 5 (catalog) stars.

**Why it exists.** Zeabur Inc. ran a hosted PaaS of the same name for about three and
a half years. After two breaches, on 2026-08-27 and on 2026-10-14/15, its founder
announced on **2026-10-17** that the hosted service is winding down: purchases
stopped that day, shared-cluster services pause and dedicated servers stop renewing
from **2026-12-01**, and everything stops on **2026-12-31**. The open-source version
is the rewrite ("Zeabur 2.0") the team had been working on, published on 2026-10-28
because "users told us they hoped Zeabur would be open-sourced". The breaches are under
"Security record"; they are the company's hosted platform, not this code.

## Feature inventory

### What shipped, release by release

From `CHANGELOG.md`, the tags and the release blog.

| Release | Date (2026) | What changed that matters for a comparison |
|---|---|---|
| v0.1.0 | 28 Oct | Never published; its contents ship in v0.1.1 |
| **v0.1.1** | 28 Oct | Projects and services read from the cluster; diagnoses in plain words; plan, confirm, apply for variables, Secrets, hostnames, scaling, probes, resources, volumes, YAML edits, NetworkPolicies, deletes and adopting a namespace; logs, a container terminal, events, usage, backups (K8up, Velero, CloudNativePG), nodes and Kubernetes version support; install suggestions from the catalog as `helm`, Flux, Argo CD or k3s `HelmChart`; English and Traditional Chinese interface; `zeaburctl ui`, `doctor`, `up`; OpenAPI 3.1; template risks shown before deploying; IP limits; the `zeabur-system-hosts` admission policy; folder upload; signed and attested releases |
| 0.1.2-beta.0 – beta.2 | 1 – 3 Nov | Pre-releases |
| 0.2.0-beta.0 – beta.3 | 5 – 7 Nov | Pre-releases |
| **v0.2.0** | 8 Nov | Build memory and CPU per project; failed builds explained in a sentence (out of memory links to the setting); ports suggested from the image's `EXPOSE`; "Add a private address" form; an update notice; an opt-out "experience program" (telemetry) from the installer; `plan` shows changes since the last apply; kubelet `system-reserved`, `kube-reserved` and eviction thresholds; team minimum of 2,700 MiB on K3s; kube-bench CIS fixes; kube-rs response bodies no longer logged. Breaking: removed flags, installer `preset` removed, `container-registry/zot` renamed, API operation IDs renamed |

The v0.2.0 post lists what comes next: live build steps, `zeaburctl connect` or a
public port for databases, API tokens for CI from the Members page, editing and
turning off database backups, force-deleting a stuck project, templates updated
without upgrading Zeabur, a project manager role, sign-in through any OpenID Connect
provider, and `HTTPS_PROXY` support.

### Deploy sources and builds

* **Image**: name it and it runs; private registries; the form reads the image's
  `User` and picks **Strict** (non-root, no escalation, all capabilities dropped,
  RuntimeDefault seccomp) for a numeric non-root user and **Compatible** otherwise.
* **Git** through Zeabur Builder: Railpack or a Dockerfile, a root directory, build
  variables passed as BuildKit secrets. GitHub through one **GitHub App per cluster**
  created with GitHub's manifest flow and granted to projects; any other host with an
  HTTPS token or a deploy key, with host keys pasted and fingerprints shown. Builder
  **checks the branch once a minute** unless a webhook receiver is configured.
* **Auto-deploy new builds** (Keel, on by default) or **Deploy newest** by hand.
* **Folder upload**: drop a folder or `.zip` in the page, or `zeaburctl up`; packed
  with `.gitignore` and `.dockerignore`, pushed to the registry as an OCI artifact,
  built by a `Build`.
* **Compose import**: paste `compose.yaml` and an env file, see each setting that is
  changed or skipped, choose per database whether it moves to an operator; build-only
  services, UDP and host paths are skipped.
* **Plain YAML** for any kind the cluster serves, custom resources included.
* Builder 0.2.1 limits each build to 1 CPU and 1 GiB by default, one at a time per
  namespace and cluster; a project raises it. A build that runs out of memory is
  recorded as `OutOfMemory` and the card says so. After a build, the image's
  declared ports are recorded and offered on the Networking tab.
* **Rollback** to one of the ReplicaSet revisions the workload keeps (10 by default),
  after comparing two pod templates. It restores the pod template only: "It doesn't
  restore the previous contents of config files and secrets that the containers read."
* **Cron jobs** from an existing service's image and command, in a chosen time zone,
  with concurrency, history, run now and pause.

### Domains, TLS and routing

* A domain shows the DNS records to add and whether it points here; cert-manager
  HTTP-01; **wildcard certificates by DNS-01** with Cloudflare, Route 53, Google Cloud
  DNS, DigitalOcean or Azure DNS; an uploaded PEM checked for dates, names and key.
* Redirects (301/302, path kept), visitor passwords, **maintenance mode** (a second
  Ingress Traefik ranks first, or HTTPRoute weights) and **custom 502/503/504 pages**
  — Traefik only.
* **Gateway API** HTTPRoutes beside Ingresses; a TCP port opened without a domain.
* **IP limits**: an inbound allow list as a Traefik `ipAllowList` middleware, and an
  outbound allow list per service as an egress NetworkPolicy.
* **Connection check** per domain and private address, hop by hop with "Expected" and
  "Found": DNS, entry address, ingress controller ready, certificate `Ready`, route
  admitted, Service exists and selects the pods, `targetPort` matches, a ready
  replica; plus whether the person's browser reaches it.

### Databases and services

* Five engines, each through its operator: PostgreSQL 16–18 (CloudNativePG), MongoDB
  7.0/8.0 (Percona), MySQL 8.4 (MOCO), MariaDB 11.8/12.3 (mariadb-operator), Dragonfly
  1.34. **MongoDB and MySQL with 1, 3 or 5 instances.**
* Connect a database by ticking services; they get `DATABASE_URL` and friends from a
  Secret.
* **Restore into a new database** from a backup, check it, then **Point services here**;
  the old one keeps running. **PostgreSQL WAL archived to S3** continuously, though a
  restore picks a backup, not a point in time. **Download a dump** (`pg_dump`,
  `mongodump`, `mysqldump`, `mariadb-dump`) in the browser.
* **Object storage**: SeaweedFS with buckets and keys added to a service (beta, one
  copy of the data).

### Storage and backups

* Volumes: mount, local or replicated (Longhorn) storage class, grow, read-only.
* Database backups to S3, **daily at 03:00 UTC or Sundays** only; CloudNativePG
  backups "are compressed but not encrypted"; no backup-now for MySQL or Dragonfly,
  and a schedule cannot yet be edited or turned off (#178).
* Volume backups with K8up (restic-encrypted) to S3 with retention; a Developer
  restores in place in their own project; files from a backup can be downloaded.
* **Cluster backups with Velero** for the whole cluster or chosen projects, restore a
  namespace from the interface (admins only), Kopia and SSE-C encryption with keys the
  docs tell you to save outside the cluster.
* **Shared S3 destinations** registered once for all three kinds; a key rotation
  updates every copy.

### Scaling and high availability

* Replicas from 0 to 1000 by hand; pause or restart every service in a project, with
  an Undo toast. **No autoscaling form**: KEDA and the Vertical Pod Autoscaler are
  installable components, and an HPA written in YAML shows up in the timeline as what
  changed the replica count.
* Nodes: join with a short-lived token and one command on the new machine; K3s takes
  workers, RKE2 three control planes; maintenance drains honouring
  PodDisruptionBudgets and lists what cannot move; remove a node with its cleanup
  command.

### Observability (logs, metrics, alerts, uptime)

* **What's wrong** on a service: one sentence of cause, one of what to do, buttons
  that open the fix (Change memory, Health checks, Open logs), Evidence and Raw error
  folded, **Copy diagnosis**. Recognised: crash loops by the kubelet's own back-off
  rule, OOM (137), failing probes, failed Job runs, image pulls, missing ConfigMaps,
  Secrets or keys, scheduling, ResourceQuota and Pod Security refusals, a Service that
  selects nothing or the wrong port.
* **What needs you** on Home groups problems by cause; a cluster cause (Traefik,
  cert-manager, CoreDNS or metrics-server with no ready replica, a node not ready) is
  shown with "Caused by" and "Affects" N services, domains and projects.
* **Missing setup**: no requests or memory limit, no readiness check, a known database
  image writing to no persistent volume, a disk with no backup, a project with no
  connection rules, a route to a missing Service, a retired ingress controller.
* **Needs attention** on the cluster: Kubernetes version past its support date (from
  kubernetes/website's release data), kubelet version skew, node memory or disk
  pressure, outdated components; Warning events of the last hour; Falco alerts.
* Logs with filter, exclude, previous run and download; **log history** of replaced
  replicas with VictoriaLogs; usage against limits with OOM kills and restarts marked
  on the history (VictoriaMetrics).
* `zeaburctl doctor` and `zeabur-installer doctor`, exit codes 0 to 3.

### Security, auth, roles, SSO, audit

* Every request carries the person's own token from Zeabur Identity; the change token
  binds user, groups, project, request and object versions. Secrets never appear in
  listings, YAML, plans or logs and are shown only on an explicit reveal.
* Sign-in: **passkeys**, with GitHub or Google linkable for regular accounts. **Admin
  accounts are separate, passkey-only**, tokens of 5 minutes, optional AAL3
  (device-bound passkeys only). Regular tokens last 10 minutes; sessions end after 1
  hour idle and 12 hours in all. kubectl signs in the same way through kubelogin.
* Roles: **Viewer** (no logs, no Secrets) and **Developer** per project, plus Admin
  (`cluster-admin`). A Developer reads and writes every Secret in the project and can
  run as any ServiceAccount in it. "Zeabur has no concept of teams": "everyone who can
  sign in is a trusted user".
* Terminals: a container shell with a debug container for shell-less images, a
  kubectl/helm terminal as the person, and a **root shell on a node** for admins,
  recorded as an Event on the node.
* Installer: CIS hardening on K3s and RKE2, Secrets encrypted at rest (`aescbc`), a
  kube-apiserver audit policy (readable by root on the server), host checks for root,
  clock, swap, crash-kernel memory, ports, disk space and disk encryption.
* No audit view in the interface: who changed a deployment comes from events and
  `managedFields`.

### Preview environments and branches

* None found in the code or docs.

### Templates and catalogue

* 33 templates in Zeabur Catalog (n8n, Dify, WordPress, Ghost, Open WebUI and others),
  every image pinned by digest, resources set, Restricted Pod Security where the app
  allows, installed and tested in CI per distribution, idle memory measured and shown.
* Each template carries a **security summary and risks with mitigations** in three
  languages ("If Ghost has not been set up yet, the first visitor can create the site
  owner", and how the template closes that window), shown under "Before you deploy".
* Templates name the components they need; missing ones are offered as install
  commands.

### CLI, API, IaC, integrations

* HTTP API under `/api/v1` with an OpenAPI 3.1 document; errors as
  `application/problem+json`.
* `zeaburctl`: `ui`, `doctor`, `up`, `components export` (Helm, HelmChart, Flux or Argo
  CD output). Linux and Apple-silicon macOS builds; Windows through WSL.
* **Every documentation page ends with "Do the same with kubectl"**, and the objects
  Zeabur writes keep working without it.
* Zeabur itself can be deployed by Argo CD or Flux; secrets synced from OpenBao, Vault,
  1Password or AWS Secrets Manager through External Secrets.
* **No API tokens yet** (a ServiceAccount and binding by hand; a form is planned) and
  **no MCP server** found in the code or docs, although the 0.1.0 changelog lists "MCP
  answers" among the places secret values never appear.

### Notifications

* Cluster and per-project channels: Slack, Discord, Telegram, email, webhook, ntfy
  (and Gotify per the feature list), through VictoriaMetrics' Alertmanager, with
  resolved messages. Events: node not ready, Falco, crash loop, deploy and build
  failed or succeeded, out of memory, failed backups, expiring certificates. Needs
  VictoriaMetrics.

### Multi-server and networking

* Any systemd Linux on amd64 or arm64; Calico on K3s, Canal on RKE2; workers join over
  TCP 6443 (K3s) or 9345 (RKE2), which the docs say to keep off the internet.
* A new project gets a NetworkPolicy `zeabur-project` that admits only its own pods,
  platform namespaces and `kube-system`; **outgoing connections aren't restricted**.
  Developers can delete it for their project. Two projects can claim the same domain
  and Traefik merges the routes.

### Team and collaboration

* Members invited by one-time links valid for 7 days; removing someone clears their
  passkeys and sessions; roles per project. No teams.

### Developer experience and onboarding

* Installer web flow in five steps (Server, Access, Components, Review, Install) with a
  memory estimate rated Enough, Nearly full or Not enough and Install disabled below
  it; sslip.io names and a local CA for a test server; `plan`, `apply`, `doctor` and a
  config file for repeat installs. A team install needs **4 GiB** (2,700 MiB of
  requests on K3s, 3,900 MiB with Builder, so a 4 GiB server gets no Builder); smaller
  servers get a personal install on KubeSolo.
* An **install guide for coding agents**: paste one message into Claude Code or Codex,
  answer two questions, open the registration link yourself.
* Docs in English, Traditional and Simplified Chinese; the interface in the same three.
* The installer **joins the telemetry programme by default** (opt out on its review
  page, `--no-telemetry`, `DO_NOT_TRACK=1`); the Helm chart does not. The server asks
  GitHub for a newer release at most once an hour unless turned off.

## What users love

Little has been said about the open-source product by people outside the company.
What there is:

* **The architecture, as news.** Taiwanese and Chinese developers on Threads reposted
  the launch as "Zeabur actually open-sourced itself", and the stars came in one pulse:
  about 450 between 2026-10-28 and 2026-10-31, then 5 to 18 a day (stringers.io,
  2026-11-06, from daily star counts). Its analysis credits the shutdown news, not the
  product, and notes a 17% fork rate.
* **No platform state and the person's own rights.** The one third-party description
  that goes past the README picks out the same two things the README leads with: no
  stored configuration, and RBAC deciding each request (stringers.io).
* Hacker News has no story about the open-source release; the only Zeabur stories in
  October and November 2026 are about the breaches and the shutdown (hn.algolia.com,
  2026-11-08). A YouTube "fit check" video and faun.dev comparison pages exist; their
  content was not read.

## What users complain about

No complaint from a user of the open-source version was found: the issue tracker holds
one issue from outside the main author (#63, the installer leaving the OAuth redirect
on an old host, fixed). The limits below come from Zeabur's own documents.

* **Unfinished, by its own list.** Of 88 listed features, the site marks Compose (skips
  build-only services, UDP and host paths), redirects, passwords and error pages
  (Traefik only), WAL archiving (no point-in-time restore), volume restores (admin by
  hand), cluster backups (admins only), object storage (beta, one copy), log history
  and usage history (need extra components) and node joins (installer clusters only)
  as not finished.
* **Heavier than it looks.** A team install needs 4 GiB, and Builder alone needs 3,900
  MiB on K3s, so Git deploys are not installed on a 4 GiB server; Builder needs
  Kubernetes 1.34 or later.
* **Trust is cluster-wide.** No teams; a Developer reads every Secret in the project;
  domains are not protected between projects; the docs say to use separate clusters
  for people who do not trust each other.
* **Breaking already.** v0.2.0, the first stable release, removed flags and an
  installer field, renamed a component and renamed every API operation ID; only the
  latest stable release gets fixes.
* **Everyone signs in again after a restart.** Sessions live in the single replica's
  memory and the chart uses `Recreate`.
* **No CI path yet.** No API tokens; Builder polls Git once a minute without a
  webhook receiver.
* **The users it is for are leaving a breached service.** People leaving hosted Zeabur
  were told to "treat your secrets as exposed", rotate everything, and back up before
  1 December (neatread.net, 2026-10-20), and Zeabur's own guide says the two commonest
  migration failures are images left in its registry and injected variables nobody
  carried over.

## Security record

**The open-source code.** NVD's keyword search for "zeabur" returns nothing, and
GitHub's advisory pages for the repository could not be read from here (HTTP 403), so
whether any advisory exists is not known. The changelog lists fixes the project found
itself: in v0.2.0, kube-rs logged unparseable Kubernetes response bodies and some
refusals at `WARN`, "such a body can be a Secret", so both binaries now drop kube-rs's
log lines; and YAML holding registry passwords and secret values passed through
`sessionStorage`, which a browser may write to disk. Issue #40 ("Debug logs can include
Secret values from apiserver refusals") was closed on 2026-10-29. Supply chain:
releases built by a shared workflow and signed with Sigstore, with provenance; the
chart pins the image by digest; the installer checks RKE2 against SHA-256 digests
compiled into it and every catalog chart and manifest against the catalog's digests;
crates and npm packages must be seven days old; aws-lc-rs is the only cryptographic
backend. Exposure by design: Zeabur Builder runs buildkitd privileged ("a build that
escapes its sandbox reaches the node", quickstart), though Builder 0.2.1 runs it in a
user namespace; an admin can open a root shell on a node from the browser; debug
containers stay in a pod until it is replaced; kube-apiserver stays reachable on 6443.

**The company's hosted platform** (zeabur.com incident reports):

* **2026-08-27.** An attacker used an RCE in a customer's public NextChat
  (CVE-2026-7644), crossed the shared cluster's internal network ("The shared cluster
  allowed cross-project internal connections"), took a team administrator's Legacy
  API Key from a routing cache, found a long-lived **AWS root key** in an internal
  service's environment, and reached the production database, reading project
  environment variables (names and values).
* **2026-10-14/15.** Shared-cluster admin tokens created in January 2024, **with no
  expiry, never rotated, stored in plaintext** in a private configuration repository,
  were used against an admin interface "open to the public internet": nine regions'
  Secrets, commands in 865 database services, root on 101 GCP dedicated servers whose
  root passwords sat in plaintext in their initialisation settings, and the platform
  database: **3,218,965 environment variable records and 321,810 accounts**. "Cluster
  backups contained all secrets", and a key rotated during the incident was captured
  by the next backup and used.
* **2026-10-17**, the shutdown announcement. Railway (an attacker in the build
  infrastructure took CI credentials and decrypted some stored variables) and
  Northflank (secrets in its control plane decrypted) were breached the same month,
  as Zeabur's own "zero-trust PaaS" post (2026-11-07) recounts.

The open-source design answers that history point by point: no long-lived platform
credential, no stored secrets, tokens of minutes, admin accounts passkey-only.

## Against Skifity

Skifity's statuses: much of it runs on a real single-server and two-server k3s on
every push (`docs/progress.md`, "A real cluster, at last"); a real S3 bucket, a
mixed-architecture cluster and production traffic never have. Zeabur's own tests run
on Ubuntu 26.04 virtual machines with K3s, RKE2 and KubeSolo (v0.2.0 post). Line
numbers are from the working tree on 2026-11-08.

| Capability | Zeabur (open source) | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git providers | GitHub App per cluster; any host by token or deploy key; Builder polls once a minute | GitHub (token), GitLab, Gitea, Bitbucket, generic, with webhooks and commit statuses (`internal/gitsrc`); no GitHub App (`internal/api/integrations_handlers.go:77-81`) | ahead on providers and push; **behind on connecting GitHub** |
| Why a build failed | One sentence; out of memory links to the project's build memory | `build.failed` with the log tail and "Read the build log below" (`internal/errdoc/catalogue.go:1373-1381`, `internal/deploy/build.go:324-328`); a fixed 3,072 MiB per build (`internal/builder/job.go:165-168`) | **behind** |
| Reading the image | `User` picks Strict or Compatible before creation; `EXPOSE` offered as a port | A table of known image ports (`internal/builder/detect.go:645-670`); a root image is explained after the pod is refused (`internal/kube/client.go:529-533`) | **behind** |
| Going live | Keel auto-deploy, or "Deploy newest" | Push, tag, watch paths, locked deploys, promote between environments (Phases 94, 96, 103) | ahead |
| Rollback | Pod template from ReplicaSet revisions; Secrets and config files not restored | Image and settings, with a plan first (`internal/api/api.go:494-498`) | ahead |
| Compose | Databases can move to an operator; changed and skipped settings listed | A stack of apps; database services become apps with a disk (`internal/api/stack_handlers.go:299-307`) | **behind** on databases |
| Review before a change | Every change shown as fields, dry-run, refused if stale | Saved at once; "rebuilt" said after the save (`web/src/locales/en.json:948`); no version check on `PATCH /apps/{id}` (`internal/api/apps_handlers.go:680-714`); plans only for rollback and `skifity.yaml` (`internal/blueprint`) | **behind** in the panel |
| **Domains, TLS and routing** | | | |
| Certificates | HTTP-01, DNS-01 wildcards with five providers, uploaded PEM | HTTP-01, a team's own certificates (`internal/tlscert`); wildcard DNS-01 "is not built" (`docs/concepts.md:423-426`) | **behind** |
| Redirects, passwords, maintenance | Yes (Traefik only); custom 502/503/504 pages | Redirects (`internal/api/domain_redirects.go`), passwords, maintenance in the guard (`internal/guard`); no custom error pages | parity; behind on error pages |
| Inbound rules | Traefik `ipAllowList` | Address, country, ASN, path, header rules and rate limits (`internal/edgerules`, `internal/guard`) | ahead |
| Outbound | Per-service allow list; project policy leaves egress open | Egress closed to other namespaces, private ranges, metadata and nodes for every environment (`internal/kube/namespace.go:139-225`); no per-app allow list | ahead on the default; behind on per-app lists |
| Domain checks | Hop by hop, Expected and Found, plus the browser | DNS statuses (`internal/api/domain_dns.go:43-66`) and certificate events | **behind** |
| Domains between tenants | Two projects can claim one domain | Refused when another app has it (`internal/api/apps_handlers.go:1718-1722`) | ahead |
| **Databases and services** | | | |
| Engines | Five, through operators | Nine (`docs/databases.md:10-20`) | ahead |
| Several instances | PostgreSQL; MongoDB and MySQL with 1, 3 or 5 | PostgreSQL only (`docs/databases.md:28-34`) | behind |
| Restore and export | Restore into a new database, then point services there; dump download; WAL archive | Restore replaces the database in place (`docs/backups.md:167-171`); dump import (`internal/api/api.go:572`); `skifity db connect` (Phase 105) | **behind** on restore beside and download; ahead on import and tunnel |
| Object storage | SeaweedFS buckets (beta) | None; a MinIO template | behind |
| **Storage and backups** | | | |
| Database backups | Daily or weekly at 03:00 UTC; CloudNativePG backups unencrypted; no edit or off yet | Any schedule, sealed with a passphrase, verifiable, `backup.missed` (`docs/backups.md`, `internal/notify/notify.go:56-60`) | ahead |
| Volume backups | K8up, restore in place, file download | Backup and restore from the panel (`internal/api/api.go:545-550`) | parity |
| Whole-cluster backup | Velero, restore a project from the interface | Panel database to S3 (Phase 84); etcd snapshots stay on the same disk (`docs/backups.md:262-271`) | **behind** |
| **Scaling and HA** | | | |
| Instances and autoscaling | Replicas by hand; no autoscaler form | Instances, HPA, KEDA scale to zero, readiness checker (`internal/kube/scaletozero.go`, `internal/deploy/scaling.go`) | **ahead** |
| Control planes and drains | RKE2 with three servers; drains honour PDBs | Promote a server (`docs/adding-servers.md:289`); apps moved off a server (`internal/provision/move.go`) | parity |
| **Observability** | | | |
| Saying what is wrong | Cause, what to do, buttons, evidence, copy | Instance states and event explanations (`internal/kube/explain.go`); `deploy.rollout_timeout` and `app.crash_loop` list the usual causes (`internal/errdoc/catalogue.go:1396-1428`); "Copy error for AI" | **behind** on naming the cause |
| Cluster health | Ingress controller, cert-manager, DNS, metrics-server readiness with what they affect; node pressure; Kubernetes support dates; Warning events | Cluster unreachable on the dashboard (`web/src/pages/dashboard.tsx:76-102`); `server.lost`, `server.alert`; components are installed or absent (`internal/api/settings_handlers.go:210-218`); k3s releases without support dates (`internal/cluster/upgrades.go:41-43`) | **behind** |
| Logs | Live, previous, history with VictoriaLogs | Live, previous (`internal/api/stream_handlers.go:427-431`), drains (`internal/logdrain`) | behind on history; ahead on drains |
| Usage and requests | CPU and memory against limits, OOM and restarts marked | Usage history with thresholds and alerts, requests and latency from Traefik (`internal/traffic`) | ahead |
| Changes made outside | Nothing to drift: the cluster is the record | Drift found, attributed and repaired (`internal/api/api.go:558-560`, Phase 112) | different |
| **Security, auth, roles, SSO, audit** | | | |
| Whose rights | The person's own token; the ServiceAccount has none | The panel's ServiceAccount is `cluster-admin` with a mounted token (`deploy/panel.yaml:39-66`, ADR-0014) | **behind** on blast radius |
| Sign-in | Passkeys; GitHub or Google linked; admin accounts passkey-only, AAL3; 1 h idle, 12 h max | Argon2id, TOTP, passkeys, OIDC with groups, team-required 2FA (`internal/auth`); 7 days idle (`internal/config/config.go:133`), 30 days max (`internal/auth/service.go:80`) | ahead on providers; behind on session length |
| Roles and tenants | Viewer, Developer, Admin; no teams; Developers read Secrets | Teams, owner, admin, member, viewer, members limited to projects, write-only secrets (`internal/store/models.go`, `internal/crypto`) | **ahead** |
| API tokens | None yet | Scoped, expiring, network allow list (`internal/auth/scopes.go`, `internal/api/middleware.go:318`) | ahead |
| Audit | kube-apiserver's log, root on the server | Team audit log in the panel | ahead |
| Terminals | Container, kubectl and node root shells | None, by decision (`docs/roadmap.md:149-153`) | deliberate difference |
| Host checks | Clock, swap, disk encryption, CIS, audit policy, Secrets encrypted | Secrets encrypted (`internal/provision/scripts.go:412`), kubelet reservations (`installer/install.sh:1558-1560`), hardening report; no clock or disk-encryption check (`internal/provision/preflight.go`) | behind (small) |
| Supply chain | Signed binaries, image and chart; digests for RKE2, charts and every catalog image | SLSA provenance on binaries (`.github/workflows/release.yml:92-112`); image by moving tag, unsigned (`.goreleaser.yaml:79-89`); k3s from `get.k3s.io` by channel (`installer/install.sh:1656-1673`); component manifests by URL, "defaults, not pins" (`internal/cluster/components.go:19-33`) | **behind** |
| Build isolation | buildkitd per namespace, privileged before Builder 0.2.1 | Rootless BuildKit, one per build (`internal/builder/job.go:32-36`) | ahead |
| **Preview environments** | None | Per pull request and branch, seeded databases (Phases 76, 93, 107) | **ahead** |
| **Templates** | 33, digests, CI-tested, idle memory measured, risks and mitigations | 374 (`internal/templates/catalogue`), versions by tag (0 of 436 `image:` lines carry a digest), a free-text `notes` field | ahead on count; **behind on pinning and stated risks** |
| **CLI, API, IaC** | `zeaburctl ui`, `doctor`, `up`; OpenAPI; kubectl steps on every page; no MCP; no tokens | About 40 commands (`internal/cli/commands.go:35-115`), OpenAPI, MCP at `/api/mcp` (`internal/api/api.go:610-612`), `skifity.yaml` plan and apply; no `doctor` | ahead; behind on a doctor |
| **Notifications** | Six or seven channels through Alertmanager; needs VictoriaMetrics | Ten channels, fourteen events, project-limited channels (`internal/notify/notify.go:56-60`) | ahead |
| **Multi-server and networking** | Join by token and a command on the machine; any systemd Linux | Added from the panel over SSH, WireGuard, Hetzner and DigitalOcean (`internal/provision`, `internal/cloud`); Ubuntu and Debian tested (`internal/provision/preflight.go:136-156`) | ahead on adding servers |
| **Team and collaboration** | Members, no teams | Teams, invitations, roles, environment cloning | ahead |
| **Developer experience** | | | |
| Where it runs | One server (4 GiB), an existing cluster, or a laptop | One server from 2 GB (`docs/quick-start.md:9-11`); not an existing cluster (`docs/faq.md:90-93`) | ahead on small servers; **behind on existing clusters** |
| Installer | Web flow with checks, memory estimate, components; plan and doctor | `install.sh` piped from `curl`, with checks and one or two questions (`installer/install.sh:1-60`) | different |
| Languages | English, Traditional and Simplified Chinese | en, id, hi, ru, zh-CN with a build gate (`web/src/locales`) | ahead on count; no zh-TW |
| Phoning home | Telemetry on by default from the installer; hourly update check | Nothing sent; updates checked when an admin presses the button (`internal/api/upgrade_check.go`) | ahead |

### Claims in this repository that no longer hold

* `docs/research/competitors.md` (lines 159-163) says no competitor "puts Kubernetes on
  one small server with one command". Zeabur's installer puts K3s or RKE2 on one server
  from a web flow started by one SSH command. What remains Skifity's is size: Zeabur
  needs 4 GiB for a team install and 3,900 MiB for Builder, Skifity documents 2 GB with
  builds.
* The same file (lines 194-195) declines "installing into somebody's existing cluster
  before the one-server install is proven". The one-server install now runs on a real
  k3s on every push and has two releases, so the reason given has lapsed. Zeabur shows
  what the other path costs: an identity provider kube-apiserver trusts, and RBAC for
  every person.
* `docs/faq.md` (lines 145-149) says Skifity does not "import the whole file in one
  action" for Compose. Phase 101 made a Compose file one stack
  (`internal/api/stack_handlers.go`).

## Gaps worth closing in Skifity

**P1**

1. **Name the cause of a failed rollout or a crashing app, and open the fix.**
   *What:* when a rollout times out or an app keeps restarting, decide which case it is
   from what Kubernetes recorded — last termination reason and exit code (OOMKilled or
   137 is memory), probe failures in `Unhealthy` events (connection refused is the
   port, a 404 is the health path), `CreateContainerConfigError` (a missing secret or
   key), `FailedScheduling`, a quota or Pod Security refusal — and say that one, with a
   button to the setting (memory, health check, port, variables) and the evidence
   folded under it. *Evidence:* Zeabur's "What's wrong" does exactly this. Skifity has
   the readings — `internal/kube/explain.go` already explains scheduling, pulls, quotas
   and Pod Security, and the instance list knows `OOMKilled` — but
   `deploy.rollout_timeout` and `app.crash_loop` still list "the usual causes"
   (`internal/errdoc/catalogue.go:1396-1428`), and the app page says "The last log lines
   usually say why" (`web/src/locales/en.json`, `apps.detail.crashing`). Errors that
   say what to do are Skifity's own pitch. *Fit:* a classifier beside `explain.go`, new
   errdoc codes with fixes, an alert at the top of the app's Overview, the same text in
   `skifity status --explain` and MCP. *Size:* M. *Without a cluster:* yes, with fake
   clients and recorded pod statuses; which reasons a real kubelet writes, once on the
   Cluster workflow.
2. **Say why a build failed, and let a build have more memory.** *What:* read the
   build pod's termination reason and the log tail: out of memory (with "raise this
   app's build memory"), a clone refused, a push refused, a lockfile missing, a
   language version not installable. Make the build memory a per-app or per-project
   setting. *Evidence:* Zeabur 0.2.0 leads the build card with one sentence and links
   out-of-memory to the project's limit. Skifity reports every failure as
   `build.failed` with a log tail (`internal/deploy/build.go:324-328`) and gives every
   build 3,072 MiB (`internal/builder/job.go:165-168`) while documenting 2 GB servers
   (`docs/quick-start.md:9-11`), so the commonest failure on a small server reads like
   any other. *Size:* S–M. *Without a cluster:* the classification yes; a real OOM on
   the Cluster workflow.
3. **Read the image before the first deploy.** *What:* for an image app, fetch the
   image's config from its registry (with the team's registry credentials) and use it:
   the user (offer "run as user" or the environment's confinement before the pod is
   refused, not after), the `EXPOSE` ports (fill the port), the `VOLUME`s (offer a disk
   at each, and warn when a known database image would write to the container), and
   the platforms (record the architecture for the mixed-architecture gap in
   `kamal.md` and `openship.md`). Do the same for a built image after the build.
   *Evidence:* Zeabur picks Strict or Compatible from the image's `User`, suggests the
   `EXPOSE` port, and flags database images with no persistent volume. Skifity guesses
   ports from a table (`internal/builder/detect.go:645-670`) and learns that an image
   runs as root only when the pod is refused (`internal/kube/client.go:529-533`).
   *Fit:* a small registry client next to `internal/registry` behind `netguard`, called
   from the create and update handlers in `internal/api/apps_handlers.go`. *Size:* M.
   *Without a cluster:* yes, against an `httptest` registry.
4. **A page for people leaving hosted Zeabur, before 2026-12-31.** *What:* a
   `docs/` page and a section of `llms.txt` written for a coding agent, mapping what
   hosted Zeabur did to Skifity — a Git service to an app with Railpack, a template to
   a template, `*.zeabur.internal` names to internal app names, injected variables to
   linked databases and shared variables, daily database backups to a backup policy,
   `*.zeabur.app` to the automatic address or a domain — and, for people whose own
   server ran Zeabur's k3s, whether `--skip-k3s` is safe there. *Evidence:* 321,810
   accounts were told to move by 31 December; Zeabur's migration guide is written for
   agents and names images left in its registry and injected variables as the two
   commonest failures; its users are mostly in Taiwan, mainland China and Japan
   (stringers.io), and Skifity ships `zh-CN`. *Size:* S. *Without a cluster:* yes.

**P2**

5. **Restore beside the old database, and download a dump.** *What:* "Restore as a new
   database" that creates a database from a backup, shows it, and then relinks the
   apps with a reviewed deploy; and "Download a dump" through the panel. *Evidence:*
   Zeabur has both; `openship.md` raised the first as gap 8 and it is still open:
   "A backup is restored only into the database it was taken from"
   (`docs/backups.md:167-171`). *Fit:* `internal/dbsvc`, the restore jobs in
   `internal/backup`, `link_database`; a dump streamed through the same path as
   `skifity db connect`. *Size:* M. *Without a cluster:* API and UI yes; the jobs need
   one.
6. **One cause, many apps.** *What:* read the live readiness of what every app depends
   on — Traefik, cert-manager, CoreDNS, metrics-server, the registry, KEDA — and node
   `Ready`, `MemoryPressure` and `DiskPressure`; show a broken one first on the
   dashboard with the apps and domains it affects, and fold those apps' own problems
   under it. *Evidence:* Zeabur's Home groups problems by cause with "Caused by" and
   "Affects". Skifity's dashboard shows only a cluster that is missing or unreachable
   (`web/src/pages/dashboard.tsx:76-102`), and a component is "installed" or "absent"
   (`internal/api/settings_handlers.go:210-218`), so Traefik with no ready pod shows as
   every app's domain failing at once. *Size:* M. *Without a cluster:* yes with fake
   clients.
7. **Compose databases as managed databases.** *What:* when a stack contains a
   recognised database image, offer to create a managed database of that engine
   instead (backed up, linkable), wire its variables into the services that used it,
   and keep "run it as an app" as the other choice. *Evidence:* Zeabur's Compose import
   asks how to run each database; the Coolify file's gap 1 planned this; Skifity's stack
   turns `postgres` into an app with a 5 GB disk and no backup
   (`internal/api/stack_handlers.go:299-307`). *Size:* M. *Without a cluster:* the
   conversion and API yes.
8. **What will hurt later.** *What:* a list per project and on the dashboard of things
   that have not failed yet: a database with backups off or no backup storage, a disk
   never backed up, an app running a database image with no disk, a single-instance
   database holding the only copy, a domain not pointing here, a k3s minor past its
   upstream support date, each with a button to fix it. *Evidence:* Zeabur's "Missing
   setup" and "Needs attention". Skifity's pieces are scattered: a warning only for
   engines it cannot back up (`web/src/locales/en.json`, `databases.noBackupsTitle`),
   `backup.missed` only once a schedule exists, k3s releases without support dates
   (`internal/cluster/upgrades.go:41-43`). *Size:* M. *Without a cluster:* yes.
9. **Refuse a stale save, and show what a save changes.** *What:* a version on apps,
   variables and domains that a save must match ("changed by Ana two minutes ago;
   review again"), and, for saves that restart or rebuild, a review listing the fields,
   the apps that restart, whether it rebuilds (ADR-0007 already decides) and any data
   at risk. *Evidence:* Zeabur binds every apply to object versions and shows the diff
   first. Skifity's `PATCH /apps/{id}` takes no version (`internal/api/apps_handlers.go:680-714`),
   so two people saving the same app overwrite each other, and the rebuild is announced
   after the save (`web/src/locales/en.json:948`). *Fit:* the store's `updated_at` as
   an `If-Match`, the `internal/blueprint` planner reused for one app. *Size:* M.
   *Without a cluster:* yes.
10. **Pin what the installer and the panel install.** *What:* the installer installs
    one k3s release checked against a SHA-256 compiled into it instead of
    `get.k3s.io` and a channel; each component manifest is checked against a digest
    before it is applied; the panel image is signed with cosign and installed by
    digest; template images carry digests. *Evidence:* Zeabur pins and checks all of
    these. Skifity installs whatever the channel holds that day
    (`installer/install.sh:1656-1673`), applies manifests from URLs that are "defaults,
    not pins" (`internal/cluster/components.go:19-33`), pushes a moving `latest` tag
    (`.goreleaser.yaml:79-89`), and none of 436 template `image:` lines has a digest.
    *Size:* S–M. *Without a cluster:* yes for the checks; one installer run on the
    Cluster workflow.
11. **Wildcard certificates by DNS-01.** Still open from `coolify.md` and
    `openship.md`; Zeabur issues them with five DNS providers, and `internal/dnsprov`
    already holds tokens for four. *Size:* M. *Needs a cluster and a zone.*
12. **The cluster's own state off the server, sealed.** *What:* copy k3s's etcd
    snapshots to the team's bucket, sealed with the backup passphrase (Phase 104), with
    the k3s token kept apart, and a documented restore of one namespace. *Evidence:*
    Zeabur offers Velero with keys kept outside the cluster; Skifity's snapshots "do not
    save you from losing the server" (`docs/backups.md:262-271`). Zeabur's October
    report is the warning about how: "Cluster backups contained all secrets", and a
    rotated key was captured by the next backup. *Size:* M. *Needs a cluster.*

**P3**

13. **Template risks before deploying.** A `risks` list with mitigations in each
    template, shown under "Before you deploy", and for apps whose first visitor becomes
    the administrator, the route password (Phase 75) on until the admin account exists.
    About 25 of 374 templates say so in their `notes` today (Baserow, Coder, Kavita,
    Teable, XWiki and others). S–M, no cluster.
14. **A `skifity doctor` on the server**: k3s, components ready, storage class,
    certificates, Secrets encryption, clock, the panel reachable, exit codes 0 to 3, as
    Zeabur's `doctor` does. Nothing like it exists (`internal/cli/commands.go:35-115`).
    S–M; with fake clients, no cluster.
15. **Schedules in a time zone.** Scheduled commands are UTC on purpose
    (`internal/kube/runjob.go:274-275`) and backup times say "Times are UTC"; Zeabur
    takes a time zone. S, no cluster.
16. **Shorter sessions for administrators.** 7 days idle and 30 days in all for
    everyone (`internal/config/config.go:133`, `internal/auth/service.go:80`); Zeabur
    has 1 hour and 12 hours, and admin accounts that sign in only with a passkey. S, no
    cluster.
17. **Log history in the panel** for instances that are gone, reading back from a
    drain Skifity runs itself. M; needs a cluster.
18. **Traditional Chinese (zh-TW)**, the language most of hosted Zeabur's users read.
    M, no cluster.
19. **Several instances of MySQL and MongoDB** through their operators. L; needs a
    cluster.
20. **Install into an existing cluster**, now that the stated precondition holds. XL;
    needs clusters other than k3s.

## Things to deliberately not copy

* **Telemetry on by default and an hourly call to GitHub.** Zeabur's installer joins
  its programme unless unticked, and its server asks GitHub for releases every hour.
  Skifity "never phones home"; keep the button.
* **A root shell on a node, and kubectl in the browser.** Zeabur limits the node shell
  to admins and logs it, but it is still the host, one click from a session.
  `docs/roadmap.md`'s reasons for having no interactive shell apply twice over.
* **Developers who can read every Secret.** Kubernetes RBAC cannot let someone deploy
  without letting them read what the pods mount; Skifity's write-only, sealed secrets
  avoid that, and Zeabur's own docs warn about it.
* **The cluster as the trust boundary, with no teams.** Zeabur tells people who do not
  trust each other to use separate clusters, and lets two projects claim one domain.
  Skifity's teams, namespaces with closed egress and domain uniqueness are the product.
* **An egress-open default.** Zeabur's project policy restricts only inbound traffic;
  hosted Zeabur's August breach crossed "cross-project internal connections". Keep
  Skifity's closed egress.
* **A separate identity service that kube-apiserver must trust, as the price of
  signing in.** It is the right design for people who want kubectl; Skifity's users do
  not see Kubernetes, and one binary is the point.
* **Polling Git every minute.** Webhooks with verified signatures are cheaper and
  faster.
* **Breaking changes in the first stable release, and fixes only for the latest.**
  Skifity's support policy (Phase 90) is better than that; keep it.
* **What hosted Zeabur did.** Long-lived admin tokens with no expiry, admin interfaces
  on the internet, root passwords in initialisation settings, backups holding every
  secret beside the key that reads them. Skifity's panel backups leave the master key
  out (`docs/backups.md`, "What is not backed up"); keep that, and apply it to any
  etcd snapshot that leaves the server (gap 12). The panel's own `cluster-admin`
  token (ADR-0014) is the same class of credential, which is why keeping the panel off
  the internet (`openship.md` gap 10) matters.

## Sources

All read on 2026-11-08.

* https://github.com/zeabur-oss/zeabur, cloned at `89c2a15` (262 commits, tags v0.1.1 to v0.2.0); files read: `README.md`, `CHANGELOG.md`, `SECURITY.md`, `docs/` (English pages: `index`, `get-started/how-it-works`, `get-started/quickstart`, `get-started/ai-agent`, `reference/architecture`, `reference/permissions`, `reference/security`, `reference/releases`, `reference/telemetry`, `reference/zeaburctl`, `cluster/diagnosis`, `cluster/notifications`, `cluster/backups`, `cluster/github-app`, `cluster/nodes`, `cluster/components`, `configure/domains`, `configure/maintenance`, `configure/restart-rollback`, `configure/service-settings`, `configure/variables`, `configure/volumes`, `configure/logs-terminal`, `deploy/git`, `deploy/compose`, `deploy/databases`, `deploy/cron-jobs`, `deploy/object-storage`, `deploy/templates`, `leave/uninstall`), `crates/zeabur-gaps/src/rules.rs` and `durability.rs`, `web/src/i18n/`, `web/package.json`, commit `e946e5c`
* https://github.com/zeabur-oss/catalog, cloned at `13d38db`: `README.md`, `components/`, `templates/`, `templates/ghost/template.yaml`, `measurements/`
* GitHub search API: `search/repositories?q=repo:zeabur-oss/zeabur` and `org:zeabur-oss` (stars, forks, dates, licence); `search/issues` for issue and pull-request counts, the issues by reactions, and those by authors other than the main author and the bots (#63, #64). The repository API, releases and advisory pages refused this sandbox (HTTP 403)
* https://oss.zeabur.com/zeabur/ (the 88-feature list)
* https://oss.zeabur.com/blog/rebuilding-zeabur (2026-10-28), https://oss.zeabur.com/blog/zero-trust-platform (2026-11-07), https://oss.zeabur.com/blog/zeabur-0-2-0 (2026-11-08)
* https://zeabur.com/blogs/zeabur-is-shutting-down (2026-10-17)
* https://zeabur.com/blogs/august-2026-security-incident (2026-09-07)
* https://zeabur.com/blogs/october-2026-security-incident (preliminary report, 2026-10-19)
* https://zeabur.com/blogs/migrate-from-zeabur (2026-10-20)
* https://hn.algolia.com/api/v1/search_by_date?query=zeabur and items 50198025, 50198048, 50347421, 50127877, 50309243
* https://stringers.io/github/gh_zeabur-oss__zeabur.html (2026-11-06; star history and where the stars came from)
* https://neatread.net/zeabur-is-closing-where-should-you-go (2026-10-20)
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=zeabur (no results)
* Web search result summaries only, not opened: a YouTube "fit check" video (`youtube.com/watch?v=t2Oa-Dj2Z-w`) and faun.dev's generated comparison pages
* Skifity files named in "Against Skifity" and the gaps, read in this repository at `618dff2`
