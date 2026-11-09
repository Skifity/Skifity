# Zeabur (open source, zeabur-oss)

> **Status.** Written on 2026-11-08 against Zeabur's `main` at commit `89c2a15`
> (2026-11-08 15:35 UTC) and its v0.2.0 release, and against Skifity's tree at
> `29147f1` the same day. No gap named here has been closed yet.

Zeabur (`zeabur-oss/zeabur`, oss.zeabur.com) is a web interface, an HTTP API and a
command-line tool, `zeaburctl`, for running services on a Kubernetes cluster you own.
It shows each namespace labelled `zeabur.com/project: "true"` as a project, and works
out services from what is in it: owner references and selectors for the replicas,
Services, Ingresses and HTTPRoutes for the traffic, environment and volume references
for the configuration, operator objects for databases and backups, and Helm, Argo CD
and Flux labels for "somebody else manages this". It has **no database and no custom
resources of its own**: what it shows is read from the cluster each time, and what it
changes is written back to ordinary objects. Every read and write is made **with the
signed-in person's own token**, so kube-apiserver and RBAC decide; the chart's
ServiceAccount has no Role binding and replicas mount no token, with one exception,
the terminal namespace (below). A change is planned first, shown as the objects and
fields it writes, sealed in a ten-minute AES-256-GCM token, dry-run against
kube-apiserver, and refused if anyone changed the same object meanwhile
(`docs/reference/architecture.mdx`).

It runs three ways: **`zeabur-installer`**, a web flow on one Linux server that
installs K3s or RKE2 (KubeSolo for a "personal" install), Zeabur Identity, the
chosen components and Zeabur; a **Helm chart** for an existing cluster whose
kube-apiserver is configured to trust Zeabur Identity's issuer; and **`zeaburctl ui`**,
which serves the same interface on a laptop's loopback address with the person's
kubeconfig and installs nothing in the cluster. **Architecture:** a Rust server
(161,183 lines in `crates/`, 20 crates) with an embedded React interface (106,066
lines of TypeScript; components from `@zeabur-oss/design-system` 0.1.3, "on Base UI
and Tailwind CSS", which contains shadcn/ui's components and theme). Four sibling
projects carry what a platform with a database would hold: **Zeabur Identity**, an
OIDC issuer with passkeys whose users, credentials and sessions are Kubernetes
custom resources; **Zeabur Builder**, `Build` objects, a buildkitd per namespace and
a zot registry, with Keel rolling new images out; **Zeabur Catalog**, 28 component
files across 21 capabilities and 33 app templates, each installed and measured in CI;
and **Zeabur Proxy**, project-scoped reads of log and metric history from
VictoriaLogs and VictoriaMetrics. **Licence:** Apache-2.0, contributions under a DCO.
**Pricing:** free; no paid tier, support plan or hosted control plane is offered on
oss.zeabur.com (2026-11-08). **Maturity:** the repository was created on 2026-10-24;
its history starts on 2026-10-26 with "initial release from internal worktree";
v0.1.0 was withdrawn, **v0.1.1 (2026-10-28)** was the first published release,
four 0.2.0 betas followed, and **v0.2.0 (2026-11-08)** is called "the first stable
release". Only the latest stable release gets security fixes, and they are not
backported (`SECURITY.md`). Before 1.0.0 "a minor or patch release may break the
API" (`CHANGELOG.md`). **Adoption** (GitHub search API, 2026-11-08): 654 stars, 109
forks, 68 open issues and pull requests; 136 issues and 248 pull requests (201
merged) in all. Of the 262 commits on `main`, 234 are by one person (Yi-Jyun Pan),
27 by two bots and one by an outside contributor, whose issue #63 and pull request
#64 (the installer leaving the OAuth redirect address on an old host) are the only
ones from outside. The siblings have 10 (builder), 6 (identity) and 5 (catalog)
stars. Docs are in English, Traditional Chinese and Simplified Chinese; so is the
interface (`web/src/i18n/`).

**Why it exists.** Zeabur Inc. ran a hosted PaaS of the same name for about three and
a half years. On 2026-10-17 its founder announced that it is winding down after two
breaches (2026-08-27 and 2026-10-14/15): purchases stopped that day, shared-cluster
services pause and dedicated-server renewals stop from 2026-12-01, and everything
stops on 2026-12-31. The open-source product is the "Zeabur 2.0" rewrite the team had
been working on; the launch post says "users told us they hoped Zeabur would be
open-sourced". The shutdown notice and the migration guide were published in English,
Traditional and Simplified Chinese, Japanese, Spanish and **Bahasa Indonesia**, which
says where hosted Zeabur's users are.

## Feature inventory

### What shipped, release by release

From `CHANGELOG.md`, the tags and the release post (2026-11-08).

| Release | Date (2026) | What changed that matters for a comparison |
|---|---|---|
| v0.1.0 | 28 Oct | Withdrawn in every zeabur-oss repository; its contents ship in v0.1.1 |
| **v0.1.1** | 28 Oct | Projects and services read from the cluster; diagnoses with cause, fix and evidence; plan, confirm, apply for variables, Secrets, hostnames, restarts, rollback, scaling, probes, resources, volumes, YAML edits, NetworkPolicies, deletes and adopting a namespace; logs, container terminal, events, usage; backups through K8up, Velero and CloudNativePG; install suggestions from the catalog as `helm`, Flux, Argo CD or k3s `HelmChart`; English and zh-TW interface; `zeaburctl ui` and `doctor`; OpenAPI 3.1; template risks shown before deploying; IP limits; folder upload and `zeaburctl up`; the `zeabur-system-hosts` admission policy; signed and attested releases |
| 0.1.2-beta.0 – beta.2 | 1 – 3 Nov | Pre-releases |
| 0.2.0-beta.0 – beta.3 | 5 – 7 Nov | Pre-releases |
| **v0.2.0** | 8 Nov | Build memory and CPU per project; a failed build explained in one sentence, out-of-memory linking to the setting; ports suggested from the image's `EXPOSE`; "Add a private address"; an update notice from GitHub releases; an opt-out "experience program" (telemetry) from the installer; installer `plan` shows what changed since the last apply; kubelet `system-reserved`/`kube-reserved` and eviction thresholds; kube-bench CIS fixes; kube-rs response bodies no longer logged. **Breaking:** session and proxy flags removed, the installer's `preset` and old input names gone, `container-registry/zot` renamed, every API `operationId` renamed |

The release post lists what is next: live build steps, `zeaburctl connect` or a public
port for databases, API tokens for CI from the Members page, editing and turning off
database backups, force-deleting a stuck project, templates updated without upgrading
Zeabur, a project manager role between Developer and Admin, sign-in through any
OpenID Connect provider, and `HTTPS_PROXY` support. The features page lists 88
features, 21 of them marked "Not finished" with what is missing.

### Deploy sources and builds

* **An image**: name it and it runs, private registries included. The form reads the
  image's declared ports and its `User`: a numeric non-root user selects **Strict**
  (non-root, no privilege escalation, every capability dropped), anything else
  **Compatible**, with the reason shown and the choice left open
  (`docs/deploy/image.mdx`).
* **Git through Zeabur Builder**: Railpack or a Dockerfile, any folder of the
  repository, build variables passed as BuildKit secrets so they stay out of the
  image. GitHub through **one GitHub App per cluster**, created with GitHub's
  manifest flow, from which members pick repositories ("No deploy keys"); other hosts
  by HTTPS token or deploy key. Builder **checks the branch every minute** unless a
  Git host webhook is wired to it (`docs/deploy/git.mdx`).
* **Going live**: Keel rolls a new build out automatically, or "Deploy newest" by hand.
* **A folder**: dropped into the page as a folder or `.zip`, or sent with
  `zeaburctl up`; packed with `.gitignore` and `.dockerignore`, pushed to the registry
  as an OCI artifact and built by a `Build`. Railpack services default to port 8080.
* **Compose import**: paste `compose.yaml` and the `.env`/`env_file` contents; each
  database image (PostgreSQL, MongoDB, MySQL, MariaDB, Redis/Valkey, MinIO) can become
  an operator-managed service, with connection strings in the other services
  rewritten; every changed or skipped setting is listed. Build-only services, UDP
  ports and host paths are skipped; named volumes start empty at 1 GiB; Zeabur keeps
  no copy of the file (`docs/deploy/compose.mdx`).
* **Plain YAML** for any kind the cluster serves, custom resources included.
* **Builds** (Builder 0.2.1): 1 CPU and 1 GiB each by default, one at a time per
  namespace and in the cluster, raised per project; an out-of-memory build is
  recorded as `OutOfMemory` and the card says so. buildkitd runs in a user namespace,
  which needs Kubernetes 1.34.
* **Rollback** to a ReplicaSet revision after comparing two pod templates. It restores
  only the pod template: "It doesn't restore the previous contents of config files and
  secrets that the containers read", and the review says so
  (`docs/configure/restart-rollback.mdx`).
* **Cron jobs** from a service's image in a chosen time zone, with run now, pause and
  history.
* **Objects managed by Helm, Argo CD or Flux** can be restarted and nothing else; other
  edits are refused with a pointer to the values file or the Git repository.

### Domains, TLS and routing

* Adding a domain shows the DNS records to set and whether they point here;
  cert-manager over HTTP-01; **wildcard certificates over DNS-01** with Cloudflare,
  Route 53, Google Cloud DNS, DigitalOcean or Azure DNS; an uploaded PEM checked for
  dates, names and key.
* Redirects (301 or 302, path kept), a visitor password, **maintenance mode** (a
  second Ingress Traefik ranks first, or HTTPRoute weights) and **custom 502, 503 and
  504 pages** from a page-server template. The first and last three are Traefik only.
* Edits to a domain's certificate, redirect, password and maintenance go into **one
  draft** with Undo per row, reviewed and applied together ("Apply N changes").
* **Gateway API** HTTPRoutes beside Ingresses; a TCP port opened without a domain.
* **IP limits**: an inbound allow list as a Traefik `ipAllowList` Middleware, and an
  **outbound allow list per service** as an egress NetworkPolicy.
* **Connection check** per domain and private address, hop by hop with "Expected" and
  "Found": DNS, entry address, ingress controller ready, certificate `Ready`, route
  admitted, Service exists and selects the replicas, `targetPort` matches the
  container, a ready replica; and whether the person's browser reaches it
  (`docs/cluster/diagnosis.mdx`).
* The `zeabur-system-hosts` admission policy keeps projects off Zeabur's and Zeabur
  Identity's own hostnames. Other domains are not protected between projects: "Two
  projects can configure the same domain. Traefik merges these routes"
  (`docs/reference/security.mdx`).

### Databases and services

* PostgreSQL (CloudNativePG), MongoDB (Percona), MySQL (MOCO), MariaDB
  (mariadb-operator) and Dragonfly, each run by its operator; **MongoDB and MySQL with
  1, 3 or 5 instances**.
* "Connect a database": tick services and they get `DATABASE_URL` and friends from a
  Secret.
* **Restore into a new database** from a backup, check it, then point services at it;
  the old one keeps running. PostgreSQL WAL is archived to S3 continuously, but a
  restore picks a backup, not a point in time.
* **Download a dump** in the browser: `pg_dump --format=custom`, `mongodump`,
  `mysqldump`, `mariadb-dump`, run in the database container with the person's own
  `pods/exec` right.
* Object storage: SeaweedFS buckets with the endpoint and keys added to a service
  (beta, one copy of the data).

### Storage and backups

* Volumes: mount, local or replicated (Longhorn) storage class, grow, read-only.
* Database backups to S3 **daily or on Sundays at 03:00 UTC**, nothing else; no
  backup-now for MySQL or Dragonfly; MySQL and Dragonfly keep every backup unless the
  bucket's lifecycle rules delete them; "CloudNativePG (PostgreSQL) writes backup
  files to the bucket in plaintext" (`docs/deploy/databases.mdx`). Editing or
  turning off a schedule is listed as next.
* Volume backups with K8up (restic) to S3 with retention; files from a backup can be
  downloaded; a Developer may restore in place in their own project
  (`docs/reference/permissions.mdx`), although the features page says "An admin
  restores by hand".
* **Cluster backups with Velero**, for the whole cluster or chosen projects, restore
  of a namespace from the interface by admins only, encryption keys kept outside the
  cluster.
* **Shared S3 destinations**, registered once for all three kinds of backup; rotating
  a key updates every copy.
* **Browse files** in a container or a volume: upload, download, preview, rename,
  delete.

### Scaling and high availability

* Replicas, CPU, memory and readiness, liveness and startup checks by hand. **No
  autoscaling form**: KEDA and the Vertical Pod Autoscaler are catalog components,
  and an HPA written in YAML is shown as what changed the replica count.
* Pause every service in a project (replicas to 0, with **Undo** in the toast) or
  restart them in turns.
* Nodes: added with a short-lived token and one command on the new machine
  (installer clusters only); maintenance moves replicas off first, honours
  PodDisruptionBudgets and lists what cannot move; removal gives the cleanup command.
  RKE2 takes three control planes, K3s only workers.

### Observability (logs, metrics, alerts, uptime)

* **What's wrong** at the top of a service: one sentence of cause, one of what to do,
  buttons that open the fix (Open logs, Change memory, Health checks), Evidence and
  Raw error folded, **Copy diagnosis**. It recognises crash loops by the kubelet's own
  back-off rule, out-of-memory exits (137), failing probes, failed Job runs, image
  pulls, missing ConfigMaps, Secrets or keys, scheduling, ResourceQuota and Pod
  Security refusals, and a Service that selects nothing or the wrong port. With no
  known cause it says where the first unready replica is stuck. "Without AI", the
  features page adds.
* **What needs you** on Home groups problems by cause. A cluster cause — Traefik or
  ingress-nginx, cert-manager, CoreDNS or metrics-server with no ready replica, a node
  not ready — is shown once with "Caused by" and "Affects N services, domains and
  projects".
* **Missing setup**: no requests or memory limit, no readiness check, data written
  with no disk mounted, a disk with no backup, a project with no connection rules, a
  route to a missing Service, a retired ingress controller; each with a Fix, Install
  or Set up backups button.
* **Needs attention** on the cluster: a Kubernetes version past its support date (from
  kubernetes/website's release data), kubelet skew, node memory or disk pressure,
  outdated components, Warning events of the last hour, Falco alerts.
* Logs with filter, exclude, previous run and download; **log history** of replaced
  replicas with VictoriaLogs; usage against limits, with OOM kills and restarts marked
  on the history (VictoriaMetrics).
* `zeaburctl doctor` (cluster, Identity and components, text or JSON) and
  `zeabur-installer doctor`, which runs the catalog's own health checks per component.

### Security, auth, roles, SSO, audit

* Sign-in through Zeabur Identity with **passkeys**; GitHub or Google can be linked by
  regular accounts. **Admin accounts are separate and passkey-only**, their tokens
  last 5 minutes, and an optional AAL3 setting accepts only device-bound passkeys.
  Regular tokens last 10 minutes; a session ends after 1 hour idle and 12 hours in
  all, and lives only in the server's memory, so a restart signs everybody out.
  kubectl signs in the same way through kubelogin.
* Roles: **Viewer** (no logs, no Secrets) and **Developer** per project, bound as
  RoleBindings to `zeabur:<user>`; Admin is `cluster-admin`. A Developer reads and
  writes every Secret in the project and can run as any ServiceAccount in it, which
  the docs warn about. "Zeabur has no concept of teams": "everyone who can sign in is
  a trusted user", and people who do not trust each other should use separate
  clusters.
* A refused request lists the missing RBAC rules and the original Kubernetes answer;
  the cluster page can copy the ClusterRole to send to an admin.
* Secrets are listed by name, type and key count; values are fetched only on an
  explicit reveal; a masked value is never written back.
* Terminals: a container shell (a debug container for shell-less images, left in the
  pod until it is replaced); a kubectl and helm terminal as the person, in a tools pod
  that **Zeabur's own ServiceAccount** creates in `zeabur-terminals`, the one place it
  holds a right; and a **root shell on a node** for admins, recorded as an Event on
  the node.
* Installer hardening: CIS profile on K3s and RKE2, Secrets encrypted at rest, a
  kube-apiserver audit policy, baseline NetworkPolicies, host checks for root,
  systemd, clock, swap, crash-kernel memory, ports, disk space and disk encryption.
* No audit view in the interface: who changed something comes from events,
  `managedFields` and the kube-apiserver audit log.

### Preview environments and branches

* None in the code, docs or the 88-feature list.

### Templates and catalogue

* 33 templates (n8n, Dify, WordPress, Ghost, Open WebUI, Vaultwarden, Uptime Kuma and
  others). All 63 `image:` lines in their manifests carry a digest; each sets requests
  and limits and runs under Restricted Pod Security where the app allows.
* CI installs and tests every template and component and records idle memory and
  CPU (`measurements/templates/*.json`; Ghost with MySQL idles at 402 MiB on kind);
  components get a full run on each distribution every week (0.2.0 post). A
  component counts as tested only while the file that passed is the current one.
* Each template carries **`security.summary` and `security.risks` with mitigations**
  in three languages, shown under "Before you deploy". Ghost's first: "If Ghost has
  not been set up yet, the first visitor can create the site owner", answered by a
  setup Job that creates the owner from the form's email within seconds.
* Templates name the components they need; missing ones are offered as install
  commands.

### CLI, API, IaC, integrations

* HTTP API under `/api/v1` with an OpenAPI 3.1 document; every refusal is
  `application/problem+json`.
* `zeaburctl`: `ui`, `doctor`, `up`, `components export` (Helm, HelmChart, Flux or
  Argo CD). Linux and Apple-silicon macOS; Windows through WSL.
* **Every documentation page ends with "Do the same with kubectl"**, and the objects
  Zeabur writes keep working without it.
* Zeabur itself can be deployed by Argo CD or Flux; secrets synced from OpenBao,
  Vault, 1Password or AWS Secrets Manager through External Secrets.
* **No API tokens yet** (a ServiceAccount and binding by hand; a form is planned) and
  **no MCP server** in the code or docs, though the v0.1.0 changelog names "MCP
  answers" among the places secret values never appear.
* An install page for coding agents: one message pasted into Claude Code or Codex
  downloads and verifies the installer, writes the config, shows the plan, waits for
  "go ahead", applies, runs `doctor`, and hands back the first admin's registration
  link without opening it (`docs/get-started/ai-agent.mdx`).

### Notifications

* Channels per cluster or project: Slack, Discord, Telegram, email, webhook and ntfy
  in the docs, and Gotify on the features page. Events include node not ready,
  Falco, crash loop, deploy and build failed or succeeded, out of memory, failed
  backups and expiring certificates. Delivery goes through VictoriaMetrics'
  Alertmanager, so notifications need the metrics component; a test is a Job the
  alert rules notice (`docs/cluster/notifications.mdx`).

### Multi-server and networking

* Any systemd Linux on amd64 or arm64; Calico on K3s, Canal on RKE2; workers join over
  TCP 6443 (K3s) or 9345 (RKE2), which the docs say to keep off the internet.
  kube-apiserver stays reachable on 6443 by design.
* A new project gets a NetworkPolicy `zeabur-project` that admits only its own pods,
  platform namespaces and `kube-system`; "Outgoing connections aren't restricted".
  A Developer may delete it for their own project. On KubeSolo (personal installs)
  NetworkPolicies are not enforced at all.

### Team and collaboration

* Members invited by one-time links valid for 7 days; removing someone clears their
  passkeys and sessions; roles per project. No teams; no comments, approvals or
  audit view.

### Developer experience and onboarding

* The installer's web flow is five steps (server checks, access, components, review,
  progress) with a memory estimate that disables Install when too little is left,
  sslip.io names and a local CA for a test server, and `plan`, `apply`, `doctor` and a
  config file for repeat runs. **A team install needs at least 2,700 MiB on K3s, and
  3,900 MiB with Builder**, so a 4 GiB server gets no Git builds by default; a
  personal install runs KubeSolo from 1.5 GiB with no members and no network
  isolation (`docs/install/personal.mdx`).
* The installer pins K3s, RKE2, KubeSolo, the catalog, Identity and Zeabur, and checks
  each download's SHA-256.
* **Telemetry on by default from the installer** (opt out on its Options page,
  `--no-telemetry` or `DO_NOT_TRACK=1`; off by default with Helm): one event per
  person per UTC day and per install start, finish or failure, through PostHog. The
  server asks GitHub for a newer release at most once an hour unless turned off
  (`docs/reference/telemetry.mdx`).
* Docs, interface and template texts in English, zh-TW and zh-CN.

## What users love

Almost nothing has been said about the open-source product by people outside the
company. What there is:

* **The launch, as news.** The only long-form material found is Zeabur's own posts,
  and the stars arrived with the shutdown story: 654 in the two weeks after 2026-10-24. No Hacker
  News story about the open-source release exists; the Zeabur stories of October and
  November 2026 are about the breaches and the shutdown, with one "Zeabur is closing.
  Where should you go?" (4 points, 2026-11-03) (hn.algolia.com, 2026-11-08).
* **What the company itself leads with**, and the design choices most worth noting:
  no platform state, the person's own rights, review before apply, and a documented
  way out ("Remove Zeabur and sites still answer"). The zero-trust post (2026-11-07)
  argues it against the October 2026 platform breaches: a platform that holds no
  master key cannot lose one.
* **Hosted Zeabur's users liked "push and it's deployed"**, which the migration guide
  names as one of the three things people want to keep, and which the open-source
  version keeps through Builder and Keel.

Reddit could not be read from here (HTTP 403), and the one web search available was
refused for lack of credits, so forums beyond Hacker News were not checked.

## What users complain about

No complaint from a user of the open-source version was found: the issue tracker has
two issues from anyone but the main author, one of them Renovate's dashboard. The
limits below are from Zeabur's own documents.

* **Unfinished by its own count.** 21 of 88 features are marked "Not finished":
  Compose skips build-only services, UDP and host paths; Git builds, folder upload and
  the GitHub App need Builder; auto-deploy needs Keel; redirects, passwords, error
  pages and domain IP limits are Traefik only; no backup-now for MySQL and Dragonfly;
  no point-in-time restore; object storage is beta with one copy; volume restores and
  cluster backups are for admins; log and usage history need extra components; node
  joins only on installer clusters.
* **Heavier than it looks.** 2,700 MiB for a team install, 3,900 MiB with Builder, and
  Builder needs Kubernetes 1.34. Notifications need VictoriaMetrics.
* **Trust is cluster-wide.** No teams, Developers read every Secret in their project,
  domains are not protected between projects, and the docs send people who do not
  trust each other to separate clusters.
* **Breaking in its first stable release**, and fixes only for the latest one.
* **Everybody signs in again after a restart**: sessions are in one replica's memory
  and the chart uses `Recreate`.
* **No path for CI yet**: no API tokens; Builder polls Git once a minute without a
  webhook receiver.
* **Backups that can only be daily or weekly at 03:00 UTC**, cannot yet be edited or
  turned off, and for PostgreSQL are written unencrypted.
* **The people it is for are leaving a breached service.** Zeabur's migration guide
  says the two commonest migration failures are images left in Zeabur's registry and
  injected variables nobody carried over, and asks people not to paste a Zeabur token
  into a chat with an agent.

## Security record

**The open-source code.** NVD's keyword search for "zeabur" returns no CVE
(2026-11-08). The repository's advisory page could not be read from here (HTTP 403),
so whether a GitHub advisory exists is not known. The changelog names fixes the
project found itself: in v0.2.0, kube-rs logged unparseable Kubernetes response
bodies and some refusal messages at `WARN`, "Such a body can be a Secret", so both
binaries now drop kube-rs's log lines; and the form's YAML, which can hold registry
passwords and secret values, no longer passes through `sessionStorage`, "which a
browser may write to disk". Supply chain: binaries, the image and the chart are
signed with Sigstore by a shared release workflow and attested; the chart pins the
image by digest; the installer checks RKE2 against SHA-256 digests compiled into it
and every catalog chart and manifest against the catalog's digests; new crate and
npm releases must be seven days old; aws-lc-rs is the only cryptographic backend.
Exposure by design: an admin can open a root shell on a node from the browser;
debug containers stay in a pod until it is replaced; kube-apiserver stays on 6443;
buildkitd ran privileged before Builder 0.2.1 put it in a user namespace; the
terminal namespace is the one place Zeabur's ServiceAccount holds rights.

**The company's hosted platform** (zeabur.com reports, read 2026-11-08):

* **2026-08-27.** An attacker used CVE-2026-7644 in a customer's NextChat v2.16.1 on the
  shared cluster, crossed its internal network, took a team administrator's Legacy
  API Key from a routing cache, found a long-lived AWS root key in an internal
  service's settings, and read project environment variables (names and values) from
  the production database.
* **2026-10-14/15.** Shared-cluster admin tokens "stored in plaintext in a private
  Zeabur configuration repository", with **no expiry**, were used against an admin
  interface "open to the public internet" in nine regions. The attacker ran commands
  in 865 database services, signed in as root to 101 GCP dedicated servers whose root
  passwords sat in plaintext in their initialisation settings, read about 53
  production secrets including the GitHub App private key, is assessed to have forged
  sign-ins with the key that signs them, and read the platform database: **3,218,965 environment
  variable records and 321,810 accounts**. Hetzner and DigitalOcean tokens used to
  manage customers' servers leaked with it. "Zeabur's daily cluster backups store all
  secrets": a key rotated at 15:40 was captured by the 16:00 backup and used at 20:30.
  The report's root cause: "long-lived admin credentials were stored in plaintext and
  could be used from the public internet."
* **2026-10-17**, the shutdown announcement.

The open-source design answers that history point by point — no platform credential,
no stored secrets, tokens of minutes, passkey-only admins — and the zero-trust post
places it beside the Railway and Northflank breaches of the same month.

## Against Skifity

The Skifity column is the tree at `29147f1` on 2026-11-08. Most of it runs on a real
single-server and two-server k3s on every push (`docs/progress.md`, "A real cluster,
at last"). Zeabur's own tests run on Ubuntu 26.04 virtual machines with K3s, RKE2 and
KubeSolo, and the catalog's weekly runs.

| Capability | Zeabur (open source) | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Connecting GitHub | A GitHub App per cluster through the manifest flow; other hosts by token or deploy key; polls every minute | Personal access token, GitLab, Gitea, Bitbucket, generic; webhooks and commit statuses; the `github_app` kind was removed because "none of that was ever written" (`internal/api/integrations_handlers.go:77-86`) | ahead on hosts and push; **behind on connecting GitHub** |
| Image config | `User` picks Strict or Compatible, declared ports fill the form | Only the registry prefix is checked (`checkImageReference`, `internal/api/apps_handlers.go:2186-2207`); ports from a Dockerfile's `EXPOSE` or a table of known images (`internal/builder/detect.go:449-451, 645-670`); a root image is explained after the pod is refused (`internal/kube/client.go:532-533`) | **behind** |
| Build failures and build size | One sentence; OOM links to the project's build memory; 1 GiB by default, raised per project | `build.failed` with the stage and log tail (`internal/deploy/build.go:321-328`, `internal/errdoc/catalogue.go:1373-1381`); every build 3,072 MiB, not settable (`internal/builder/job.go:165-168`, `internal/settings/settings.go:310`) | **behind** |
| Compose | Database images can become operator-managed, connection strings rewritten | One stack of apps; a database service is an app with a 5 GB disk (`internal/api/stack_handlers.go:299-307`) | **behind** on databases |
| Rollback | Pod template only; config files and Secrets not restored | Image and settings, with a plan first (`/rollback/{deploymentID}/plan`, `internal/api/api.go`) | **ahead** |
| Review before a change | Every change is a reviewed plan, dry-run, refused if the object changed | Saved at once; "Saved. This value is baked into the image, so the app is being rebuilt." (`web/src/locales/en.json:948`); `PATCH /apps/{id}` takes no version (`internal/api/apps_handlers.go:714-`); plans for rollback and `skifity.yaml` only (`internal/blueprint`) | **behind** in the panel |
| Going live | Keel, or Deploy newest | Push, tag, watch paths, locked deploys, promote (Phases 94, 96, 103) | ahead |
| **Domains, TLS and routing** | | | |
| Certificates | HTTP-01, DNS-01 wildcards with five providers, uploaded PEM | HTTP-01 and a team's own certificates (`internal/tlscert`); wildcard DNS-01 "is not built" (`docs/concepts.md:423-426`) | **behind** |
| Domain check | Hop by hop: DNS, entry, controller, certificate, route, Service, port, replica, browser | DNS against the cluster's addresses (`DNSCheck`, `internal/api/domain_dns.go:86-99`) and certificate state (`web/src/locales/en.json:997-1036`) | **behind** |
| Redirects, passwords, maintenance, error pages | All four, Traefik only for three | Redirects (`internal/api/domain_redirects.go`), password, maintenance in the guard (`internal/guard`); no custom 502/503/504 pages | parity; behind on error pages |
| Inbound rules | Traefik `ipAllowList` | Address, country, ASN, path and header rules and rate limits (`internal/edgerules`, `internal/guard`) | **ahead** |
| Between apps and outbound | Per-service connection rules; per-service egress allow list; egress open by default | Every pod of an environment reaches every other; egress closed to other namespaces, private ranges, metadata and nodes (`internal/kube/namespace.go:147-224`, `internal/kube/egress.go`); no per-app rules | ahead on the default; behind per app |
| Domains between tenants | Two projects can claim one domain | Refused when another app has it (`internal/api/apps_handlers.go:1717-1722`) | **ahead** |
| **Databases and services** | | | |
| Engines | Five, through operators | Nine (`docs/databases.md:10-20`) | ahead |
| Several instances | PostgreSQL; MongoDB and MySQL with 1, 3 or 5 | PostgreSQL only (`docs/databases.md:28-33`) | behind |
| Restore and export | Restore beside, then repoint; dump download; WAL archive | Restore replaces in place (`docs/backups.md:167-174`); dump import (`docs/databases.md:221`); `skifity db connect` (Phase 105) | **behind** on restore-beside and download |
| Object storage | SeaweedFS buckets (beta) | A MinIO template only | behind |
| **Storage and backups** | | | |
| Database backups | Daily or Sunday 03:00 UTC; PostgreSQL unencrypted | Any schedule, sealed with a passphrase, verifiable, `backup.missed` (`docs/backups.md`, `internal/notify/notify.go:62-70`) | **ahead** |
| Volume backups | K8up, file download | Backup, verify and restore (`/volumes/{volumeID}/...`, `internal/api/api.go`); no file download | parity |
| Cluster state | Velero, a project restored from the interface | The panel's database to S3 (Phase 84); etcd snapshots "a copy on the same disk" (`docs/backups.md:258-271`) | **behind** |
| Browsing files | Container and volume, upload and download | Config files only (`internal/api/files_handlers.go`) | behind |
| **Scaling and HA** | | | |
| Instances and autoscaling | By hand; no autoscaler form | HPA, KEDA scale to zero, readiness checker (`internal/kube/scaletozero.go`, `internal/deploy/scaling.go`) | **ahead** |
| Pausing a project | Pause all or restart all, with Undo | Per app only; no environment-wide action (`internal/api/api.go:453-464`) | behind (small) |
| Server maintenance | Drain honouring PDBs; three RKE2 control planes | Apps moved off by eviction (`internal/provision/move.go:24-45`); promote a server | parity |
| **Observability** | | | |
| Naming the cause | Kubelet's back-off rule, exit 137, probe kinds, missing keys, quotas, wrong port; one button to the fix | Events explained one by one (`ExplainEvent`, `internal/kube/explain.go:293-347`); the instance shows the current waiting reason only, never `LastTerminationState` (`internal/kube/client.go:524-541`); `app.crash_loop` lists "the usual three" (`internal/errdoc/catalogue.go:1418-1428`) | **behind** |
| Cluster health | Controller, cert-manager, DNS, metrics-server readiness with what each affects; node pressure; Kubernetes support dates | Reachable, node count, ready nodes, usage (`ClusterSummary`, `internal/api/ports.go:140-156`); no watcher for component readiness (`internal/watch`); k3s releases without dates (`internal/cluster/upgrades.go:41-42`) | **behind** |
| Setup that will hurt later | A list per project and cluster with a Fix button | Scattered: a warning for engines not backed up, a missing health path refused (`web/src/locales/en.json:1108,1558`) | behind |
| Logs | Live, previous, history with VictoriaLogs | Live, previous, drains (`internal/logdrain`) | behind on history; ahead on drains |
| Usage and traffic | CPU and memory against limits, OOM and restarts marked | Usage history with alerts, requests and latency from Traefik (`internal/traffic`, Phases 95, 99) | **ahead** |
| Changes made outside | Nothing to drift | Drift found, attributed and repaired (Phase 112, `internal/watch/drift.go`) | different |
| **Security, auth, roles, SSO, audit** | | | |
| Whose rights | The person's own token; ServiceAccount bound to nothing but the terminal namespace | `cluster-admin`, token mounted (`deploy/panel.yaml:39-62`, ADR-0014) | **behind** on blast radius |
| Sign-in and sessions | Passkeys; GitHub, Google; admin accounts passkey-only, AAL3; 1 h idle, 12 h max | Argon2id, TOTP, passkeys, OIDC with groups, team-required strong sign-in; 7 days idle (`internal/config/config.go:133`), 30 days max (`internal/auth/service.go:80`) | ahead on providers; **behind on session length** |
| Roles and tenants | Viewer, Developer, Admin; no teams; Developers read Secrets | Teams; owner, admin, member, viewer; members limited to projects (Phases 85, 86); write-only sealed secrets (`internal/crypto`) | **ahead** |
| API tokens | None yet | Scoped, expiring, with a network allow list (`internal/auth/scopes.go`) | **ahead** |
| Audit | kube-apiserver's log, on the server | Team audit log in the panel | **ahead** |
| Terminals | Container, kubectl, root on a node | None, by decision (`docs/roadmap.md:149-153`) | deliberate difference |
| Supply chain | Signed binaries, image and chart; digests for RKE2, charts and every template image | Provenance on binaries; image tagged `latest` among others and unsigned (`.goreleaser.yaml`, `dockers_v2`); k3s from `get.k3s.io` on the `stable` channel (`installer/install.sh:97,1671`); component manifests by URL, "defaults, not pins" (`internal/cluster/components.go:19-33`); 0 of 436 template `image:` lines with a digest | **behind** |
| **Preview environments** | None | Per pull request and branch, whole environments, seeded databases (Phases 76, 93, 107) | **ahead** |
| **Templates** | 33, digests, installed by CI, idle memory measured, risks and mitigations in three languages | 374 (`internal/templates/catalogue`), exact tags; checked statically (`internal/templates/validate_test.go`) and one installed on a cluster (`test/cluster/verify.sh:2887`); a free-text `notes` in 143 | ahead on count; **behind on proof and stated risks** |
| **CLI, API, IaC** | `ui`, `doctor`, `up`; OpenAPI; kubectl steps on every page; no MCP, no tokens | About 40 commands (`internal/cli/commands.go:35-115`), OpenAPI, MCP at `/api/mcp`, `skifity.yaml` plan and apply; no `doctor` | **ahead**; behind on a doctor |
| **Notifications** | Six or seven channels through Alertmanager; needs VictoriaMetrics | Ten channels, fourteen events (`internal/notify/notify.go:96-99`) | **ahead** |
| **Multi-server and networking** | Join by token and a command on the machine | Added from the panel over SSH, WireGuard, Hetzner and DigitalOcean (`internal/provision`, `internal/cloud`) | **ahead** |
| **Team and collaboration** | Members, no teams | Teams, invitations, roles, environment cloning (`/environments/{envID}/clone`) | **ahead** |
| **Developer experience** | | | |
| Where it runs | One server (2,700 MiB team, 3,900 with builds), an existing cluster, or a laptop | One server from 2 GB with builds (`docs/quick-start.md:9-11`, `installer/install.sh:1024-1045`); "Can I use my existing Kubernetes cluster? Not yet." (`docs/faq.md:90-93`) | ahead on small servers; **behind on existing clusters** |
| Installer | Web flow, memory estimate, plan, apply, doctor, pinned downloads | `install.sh` with host checks and kubelet reservations (`installer/install.sh:1024-1045, 1558-1560`) | different; behind on plan and doctor |
| Languages | Interface and docs in en, zh-TW, zh-CN | Interface in en, id, hi, ru, zh-CN with a build gate; docs in English only (`internal/docsite/page.go:18`) | ahead on the interface; **behind on docs** |
| Phoning home | Telemetry on by default from the installer; hourly update check | Nothing sent (`internal/settings/settings.go:222-223`); updates checked on request (`internal/api/upgrade_check.go`) | **ahead** |

### Claims in this repository that no longer hold

* `docs/research/competitors.md:161-162` says none of the Kubernetes competitors
  "puts Kubernetes on one small server with one command". Zeabur's installer puts K3s
  or RKE2 on one server from a web flow started by one command, and KubeSolo on 1.5
  GiB for one person. What is still Skifity's is a multi-user panel with builds on a
  2 GB server; Zeabur needs 3,900 MiB for that.
* The same file (lines 194-195) puts off "installing into somebody's existing cluster
  before the one-server install is proven". The one-server install now runs on a real
  k3s on every push and has been released, so the condition it named has been met;
  `docs/faq.md:90-93` still says "Not yet".
* The same file's "Unpinned images in the catalogue" (line 193) holds only in the sense of exact
  tags: no template image carries a digest, and Zeabur's every one does.

## Gaps worth closing in Skifity

**P1**

1. **Name the cause of a crash or a failed rollout, and open the fix.** *What:* when an
   app crash-loops or a rollout times out, decide which case it is from what the
   kubelet recorded — `LastTerminationState.Terminated` with `OOMKilled` or exit 137
   (memory), probe failures from `Unhealthy` events (refused connection: the port; a
   404: the health path; a timeout: the start time), `CreateContainerConfigError` with
   the missing key, a quota or Pod Security refusal — and say that one sentence with
   one button (Memory, Health check, Port, Variables), the evidence folded under it,
   and "Copy diagnosis". *Evidence:* Zeabur's "What's wrong" does this "without AI".
   Skifity reads only the current waiting reason (`internal/kube/client.go:524-541`),
   explains events one at a time (`internal/kube/explain.go:293-347`), and its
   `app.crash_loop` and `deploy.rollout_timeout` fixes list "the usual causes"
   (`internal/errdoc/catalogue.go:1396-1428`). A container killed for memory shows as
   `CrashLoopBackOff` with nothing about memory, because the kubelet writes no pod
   event for a cgroup OOM kill. *Fit:* a classifier beside `explain.go`, errdoc codes
   per cause, the alert at the top of the app's Overview, the same text in
   `skifity status` and the MCP `app_status` answer. *Size:* M. *Without a cluster:*
   yes, from recorded pod statuses and events; one forced OOM on the Cluster workflow
   to check what a real kubelet writes.
2. **Say why a build failed, and let a build have the memory it needs.** *What:* read
   the build pod's termination reason and the log tail and name the case — out of
   memory, clone refused, push refused, no lockfile, a language version that cannot be
   installed — with the fix; make build memory a per-app setting with the cluster's
   largest server as the ceiling. *Evidence:* Zeabur 0.2.0 leads the build card with
   one sentence and links out-of-memory to the project's limit, after a build on a 4
   GB test server "took other services down". Skifity gives every build 3,072 MiB
   (`internal/builder/job.go:165-168`) on servers it supports from 2 GB, and reports
   every failure as `build.failed` with the stage and exit code
   (`internal/deploy/build.go:321-328`). *Size:* S. *Without a cluster:* the
   classification and the setting, yes; a real OOM on the Cluster workflow.
3. **Refuse a stale save, and show what a save will do before it does it.** *What:* a
   version on apps, variables and domains that a save must match ("changed by Ana two
   minutes ago — review again"), and for a save that restarts or rebuilds, a review
   naming the fields, whether it rebuilds (ADR-0007 already decides), which apps
   restart, and any data at risk (a disk detached, a database unlinked). *Evidence:*
   Zeabur binds every apply to object versions and dry-runs it. Skifity's
   `PATCH /apps/{id}` takes no version (`internal/api/apps_handlers.go:714-`), so two
   people saving the same app overwrite each other, and the rebuild is announced after
   the save (`web/src/locales/en.json:948`). The planner exists: rollback has a plan
   endpoint and `skifity.yaml` has plan and apply (`internal/blueprint`). *Fit:*
   `updated_at` as an `If-Match`, refused with an errdoc `conflict.stale`; the
   blueprint planner reused for one app. *Size:* M. *Without a cluster:* yes.
4. **Connect GitHub with a GitHub App the panel creates for itself.** *What:* "Connect
   GitHub" creates a GitHub App for this panel through GitHub's manifest flow,
   installs it on the repositories the person picks, and uses short-lived
   installation tokens for clones, webhooks and commit statuses; the personal access
   token stays as the fallback. *Evidence:* Zeabur's per-cluster App ("No deploy
   keys"); Coolify has had one for years. Skifity removed the `github_app` kind
   because it was never implemented (`internal/api/integrations_handlers.go:77-86`),
   so connecting GitHub means making a token with the right scopes, tied to one
   person and usually able to read every repository they can. Zeabur's October report
   is the reason to keep the App per panel: the hosted platform's single App key
   opened every customer's repositories. *Fit:* `internal/gitsrc` (the JWT and
   installation-token exchange), the key sealed with context like any secret, an
   integration form. *Size:* M. *Without a cluster:* yes, against an `httptest`
   GitHub.

**P2**

5. **Read the image before the first deploy.** *What:* fetch the image's config from
   its registry with the team's credentials, and use it: `User` (offer the
   environment's baseline level or "run as user" before the pod is refused),
   `ExposedPorts` (fill the port), `Volumes` (offer a disk at each), and the platforms
   (record the architecture). Do the same for an image the panel built. *Evidence:*
   Zeabur picks Strict or Compatible from `User` and suggests `EXPOSE` ports. Skifity
   checks only the registry prefix (`internal/api/apps_handlers.go:2186-2207`) and
   learns that an image runs as root when the pod is refused
   (`internal/kube/client.go:532-533`). *Fit:* a small registry client behind
   `netguard` next to `internal/registry`, called from the create and update
   handlers. *Size:* M. *Without a cluster:* yes, against an `httptest` registry.
6. **One cause, many apps.** *What:* watch the readiness of what every app depends on —
   Traefik, cert-manager, CoreDNS, metrics-server, the registry, KEDA — and node
   `MemoryPressure` and `DiskPressure`; show a broken one first on the dashboard with
   the apps and domains it affects, fold those apps' own alerts under it, and send one
   notification instead of one per app. *Evidence:* Zeabur's "Caused by" and
   "Affects". Skifity's `ClusterSummary` has reachability and node counts
   (`internal/api/ports.go:140-156`); nothing in `internal/watch` checks components,
   so Traefik with no ready pod looks like every domain failing at once, and there is
   no notification event for it (`internal/notify/notify.go:96-99`). *Size:* M.
   *Without a cluster:* yes, with fake clients.
7. **Check a domain hop by hop.** *What:* extend **Check DNS** into a connection check
   that the panel, inside the cluster, can make: DNS, Traefik ready, the certificate
   `Ready`, the Service has ready endpoints, the app answers on its port, and an HTTP
   request to the domain through Traefik; mark the first hop that fails with its fix.
   *Evidence:* Zeabur's connection check with "Expected" and "Found". Skifity checks
   DNS and shows certificate state (`internal/api/domain_dns.go:86-99`); an app
   listening on the wrong port, or on 127.0.0.1, is diagnosed only as a failed health
   check. *Size:* M. *Without a cluster:* the logic with fakes; the probes on the
   Cluster workflow.
8. **Compose databases as managed databases.** *What:* when a stack holds a recognised
   database image, offer a managed database of that engine (backed up, linkable),
   rewrite the other services' connection variables to it, and list what differs
   (version, command, volume starts empty). *Evidence:* Zeabur's Compose import does
   this per database; Skifity turns `postgres` into an app with a 5 GB disk and no
   backup (`internal/api/stack_handlers.go:299-307`), while its own FAQ says such a
   service "is better created as a managed database". *Size:* M. *Without a cluster:*
   the conversion and API, yes.
9. **Restore beside the old database, and download a dump.** *What:* "Restore as a new
   database", then relink the apps after a look; "Download a dump" streamed through
   the panel with the engine's own tool. *Evidence:* Zeabur has both; Skifity restores
   only in place — "A backup is restored only into the database it was taken from"
   (`docs/backups.md:171`). *Size:* M. *Needs a cluster* for the jobs.
10. **Pin and check what gets installed.** *What:* the installer installs one k3s
    release checked against a SHA-256 compiled in, instead of the `stable` channel;
    component manifests are checked against a digest before they are applied; the
    panel image is signed with cosign and installed by digest; template images carry
    digests. *Evidence:* Zeabur pins and checks each of these. Skifity takes whatever
    `stable` is that day (`installer/install.sh:97,1671`), applies manifests that are
    "defaults, not pins" (`internal/cluster/components.go:19-33`), publishes a
    `latest` tag, and no template image has a digest. *Size:* S–M. *Without a
    cluster:* the checks, yes; one installer run on the Cluster workflow.
11. **A page for people leaving hosted Zeabur, before 2026-12-31.** *What:* a docs page
    and an `llms.txt` section written for a coding agent, mapping hosted Zeabur to
    Skifity — Git services to apps with Railpack, `*.zeabur.internal` names to app
    names, injected variables to linked databases and shared variables, daily
    database backups to a backup policy, `*.zeabur.app` to the automatic address —
    and, for people whose own server ran Zeabur's k3s, what to do with it. Offer it
    in Indonesian and Simplified Chinese, which Skifity's interface already speaks.
    *Evidence:* 321,810 accounts were told to move by 2026-12-31; Zeabur's guide is
    written for agents and names images and injected variables as the commonest
    failures; it was published in Bahasa Indonesia and Chinese among others. *Size:*
    S. *Without a cluster:* yes.
12. **Wildcard certificates by DNS-01.** Still open from `coolify.md`; Zeabur issues
    them with five DNS providers, and Skifity already keeps DNS provider credentials
    per team (`internal/dnsprov`). `docs/progress.md` calls it "A decision before
    code". *Size:* M. *Needs a cluster and a zone.*
13. **The cluster's own state off the server, sealed.** *What:* copy k3s's etcd
    snapshots to the team's bucket, sealed with the backup passphrase (Phase 104), the
    k3s token kept apart, with a documented restore. *Evidence:* Zeabur offers Velero
    with keys outside the cluster; Skifity's snapshots "do not save you from losing the
    server" (`docs/backups.md:258-271`). Zeabur's October report is the warning about
    how: "daily cluster backups store all secrets", and a rotated key was read from the
    next backup. *Size:* M. *Needs a cluster.*

**P3**

14. **Template risks, and templates proven on a cluster.** A `risks` list with
    mitigations per template, shown before installing (for apps whose first visitor
    becomes the administrator, the route password of Phase 75 on until the admin
    exists); and a scheduled job that installs every template on k3s, waits for it to
    answer, and records idle memory to show in the catalogue. Skifity checks templates
    statically and installs one on a real cluster (`test/cluster/verify.sh:2887`). L;
    needs a cluster.
15. **`skifity doctor`**, run on the server or against the panel: k3s and components
    ready, storage class, certificates, Secrets encryption, clock, disk, the panel
    reachable, exit codes for scripts. Nothing like it exists
    (`internal/cli/commands.go:35-115`, `internal/cli/admin.go:51-63`). S–M; fakes
    without a cluster.
16. **Shorter sessions, and stricter ones for administrators.** 7 days idle and 30 days
    in all for everyone (`internal/config/config.go:133`, `internal/auth/service.go:80`);
    Zeabur has 1 hour and 12 hours, and admin accounts that sign in only with a
    passkey. A shorter default for owners and admins, and an option to require a
    passkey for them. S; no cluster.
17. **Pause or restart a whole environment, with Undo.** Scale every app to zero and
    back from the stored counts, or restart them in turns. S; no cluster for the API.
18. **Per-app connection rules and outbound allow lists.** "Only `api` may reach the
    database" and "this worker may reach only these addresses", as NetworkPolicies
    beside the environment's default-deny (`internal/kube/namespace.go:147-224`). M;
    needs a cluster to prove enforcement.
19. **Documentation in the interface's languages**, starting with the quick start,
    troubleshooting and the Zeabur migration page; Skifity's docs are English only
    while the interface ships five languages. L; no cluster.
20. **Schedules in a time zone**, kept explicit in the form: scheduled commands are UTC
    by design (`internal/kube/runjob.go:274`). S; no cluster.
21. **Custom 502/503/504 pages** from the guard that already serves the maintenance
    page. S–M; needs a cluster for Traefik's errors middleware.
22. **Install into an existing cluster**, now that the stated precondition has been met.
    XL; needs clusters other than k3s.

## Things to deliberately not copy

* **The person's own Kubernetes token as the only authority.** It is the right design
  for people who already think in RBAC, and it costs an identity service that
  kube-apiserver must trust, RBAC bindings per person, and Developers who can read
  every Secret their pods mount. Skifity's users do not see Kubernetes; its write-only
  sealed secrets and teams are the answer for them. What is worth taking from it is
  smaller, and is gap 16 and the blast-radius question ADR-0014 already records.
* **No teams, and the cluster as the trust boundary**, with two projects free to claim
  one domain. Skifity's teams, per-environment namespaces with closed egress and
  domain uniqueness are the product.
* **An egress-open project policy.** Hosted Zeabur's August breach crossed the shared
  cluster's internal network; Skifity closes egress to other namespaces, private
  ranges, metadata and nodes by default. Keep it.
* **A root shell on a node and kubectl in the browser.** Logged and admin-only, but
  still the host one click from a session; `docs/roadmap.md`'s reasons apply twice.
* **Telemetry on by default from the installer, and an hourly call to GitHub.**
  Skifity sends nothing and checks for updates when asked.
* **Polling Git every minute.** Verified webhooks are cheaper and faster.
* **Backups written unencrypted** and schedules limited to two choices. Skifity's
  sealed, verifiable backups on any schedule are ahead; keep them.
* **Breaking changes in the first stable release, with fixes only for the latest.**
  Skifity's support policy (Phase 90) is better.
* **What hosted Zeabur did**: admin tokens with no expiry stored in a repository,
  admin interfaces on the internet, root passwords in initialisation settings, cloud
  provider tokens in code and environment variables, one GitHub App key for every
  customer, and backups that hold every secret next to the key that reads them.
  Skifity seals cloud and Git tokens with context and leaves the master key out of
  panel backups (`docs/backups.md`); keep both, and keep them true for any etcd copy
  that leaves the server (gap 13).

## Sources

All read on 2026-11-08.

* https://github.com/zeabur-oss/zeabur, cloned at `89c2a15` (262 commits; tags v0.1.1
  to v0.2.0 with their dates): `README.md`, `CHANGELOG.md`, `SECURITY.md`,
  `web/package.json`, `web/src/i18n/`, `crates/` (line count, `zeabur-plan`,
  `zeabur-gaps`), and the English docs `get-started/how-it-works`,
  `get-started/quickstart`, `get-started/ai-agent`, `reference/architecture`,
  `reference/permissions`, `reference/security`, `reference/telemetry`,
  `cluster/diagnosis`, `cluster/notifications`, `deploy/image`, `deploy/git`,
  `deploy/compose`, `deploy/databases`, `configure/domains`,
  `configure/restart-rollback`, `install/personal`, and the headings of every other
  page under `docs/`
* https://github.com/zeabur-oss/catalog, cloned at `13d38db`: `README.md`,
  `templates/` (33), `templates/ghost/template.yaml`, `templates/*/manifests.yaml`
  (digests), `measurements/templates/ghost.json`, `components/`
* GitHub search API: `search/repositories?q=org:zeabur-oss` (stars, forks, open
  counts, dates, licences); `search/issues` for issue and pull-request totals and for
  issues and pull requests by authors other than the main author (#63, #64). The
  repository, releases and advisories endpoints refused this session (HTTP 403)
* https://oss.zeabur.com/zeabur/ (the 88 features and the 21 marked not finished)
* https://oss.zeabur.com/blog/ and the posts https://oss.zeabur.com/blog/rebuilding-zeabur
  (2026-10-28), https://oss.zeabur.com/blog/zero-trust-platform (2026-11-07) and
  https://oss.zeabur.com/blog/zeabur-0-2-0 (2026-11-08)
* https://zeabur.com/blogs/zeabur-is-shutting-down (2026-10-17)
* https://zeabur.com/blogs/august-2026-security-incident (2026-09-07)
* https://zeabur.com/blogs/october-2026-security-incident (preliminary report,
  2026-10-19)
* https://zeabur.com/blogs/migrate-from-zeabur (2026-10-20)
* https://hn.algolia.com/api/v1/search_by_date?query=zeabur (items 50347421, 50198025,
  50198048, 50309243, 50127877)
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=zeabur (no results)
* Not reachable: https://github.com/zeabur-oss/zeabur/security/advisories (HTTP 403),
  Reddit's search (HTTP 403), and web search (no credits)
* Skifity files named in "Against Skifity" and the gaps, read in this repository at
  `29147f1`
