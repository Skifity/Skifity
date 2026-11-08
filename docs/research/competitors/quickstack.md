# QuickStack

> **Status.** Written on 2026-11-08 against Skifity's code at `618dff2` and QuickStack's `main` at
> `90d872d` (merged the same day). Nothing named here has been changed in Skifity yet; the gaps
> below are proposals.

QuickStack (`biersoeckli/QuickStack`, quickstack.dev) is a self-hosted PaaS with the same pitch as
Skifity: one command turns a Linux server into a k3s cluster, and a web UI deploys apps and
databases onto it with "no knowledge of Kubernetes required". It is for solo developers, small
agencies and self-hosters who want Vercel- or Railway-style deploys on their own VPS, and who may
later add servers. **Architecture:** one pod in the `quickstack` namespace, bound to `cluster-admin`,
running a Next.js 15 / React 19 app behind a custom Node server (Socket.IO for the terminal), an
Elysia REST API under `/api/v1` with a generated OpenAPI document, Prisma 7 over **SQLite** at
`/app/storage/db/data.db`, next-auth 4 with JWT sessions, and the Kubernetes JavaScript client.
Underneath it installs k3s, **Longhorn on every install**, cert-manager (Helm), BuildKit with
Railpack, and an in-cluster Docker Distribution registry; Traefik is k3s's own. The UI is shadcn/ui,
with a React Flow canvas. The panel is served over plain HTTP on NodePort 30000 on every node, and
over HTTPS once it is given a domain; the NodePort stays open until an administrator turns it off
(`disableNodePortAccess`, default false). **Licence:** GPL-3.0. **Pricing:** none; "Free forever. Every
feature included." No hosted offer or paid tier was found. **Maturity:** first commit 2024-10-17;
tags 0.0.1 (2025-01-19) to 0.0.13 (2026-08-05), 0.1.0 (2026-09-13), **1.0.0 (2026-10-09)** and
**1.0.1 (2026-10-26)**. MCP, AI agent sandboxes and gVisor ship only on a **canary** update
channel. **Adoption** (read 2026-11-08): 365 GitHub stars, 36 forks, 19 open issues and pull
requests (GitHub search API); 19,956 pulls of `quickstack/quickstack` on Docker Hub (inflated:
every update and the setup job pull with `imagePullPolicy: Always`); 774 npm downloads of the SDK in
the month to 2026-11-07; a Show HN on 2025-01-28 with 6 points and 11 comments. **People:** it began
as a bachelor project at OST (Eastern Switzerland University of Applied Sciences) by `biersoeckli`
and `glueh-wyy-huet`; 741 of the 883 commits on `main` are `biersoeckli`'s (84%) and 85 are
`aperolschpritz`'s. achtpunkt GmbH of St. Gallen lists QuickStack among its products.

## Feature inventory

### What shipped, release by release

Dates are the tag commits' dates (`git ls-remote` and a fetch of each tag); Docker Hub's tag dates
agree for 0.1.0, 1.0.0 and 1.0.1. GitHub's release pages could not be read from this sandbox; the
1.0.1 notes come from `get.quickstack.dev/versioninfo.json`, which mirrors them.

| Release | Date | What changed that matters for a comparison |
|---|---|---|
| 0.0.1 | 2025-01-19 | Git or image deploys, Kaniko builds, Longhorn volumes, S3 volume backups, logs, web terminal |
| 0.0.3 | 2025-03-24 | Users and groups (#19) |
| 0.0.5 | 2025-07-26 | BuildKit replaces Kaniko (#34) |
| 0.0.6–0.0.9 | 2025-12-04 – 2026-03-20 | Separate database backup jobs (#51), Kubernetes network policies (#53), self-update from the UI (#56); shareable RWX volumes (#60), health checks (#61), k3s upgrades from the UI (#62); security context (#70), suggested reservations (#72), privileged mode (#75), cert-manager through Helm (#77) |
| 0.0.10 | 2026-04-06 | Build placement on a chosen node (#85), build optimisations |
| 0.0.11–0.0.13 | 2026-04-22 – 2026-08-05 | Railpack (#91), Git over SSH with per-app deploy keys (#92), NodePorts (#94), the REST API (#101), Agent Sandbox integration (#102) |
| 0.1.0 | 2026-09-13 | SSO providers (#107), extended per-app network policies (#109), cluster add-ons (#116), parallel builds 1–20 (#124), rollback for Git apps (#125) |
| 1.0.0 | 2026-10-09 | Build arguments (#130), framework presets (#132), backup before a deploy (#140), gVisor add-on (#143, canary), every app setting in the Project Canvas drawer (#144), remote MCP (#145, canary), Longhorn as the default class (#146) |
| 1.0.1 | 2026-10-26 | **Pod terminal authentication fixed** (#154), command execution through the API (#151) |
| `main` since 1.0.1 | to 2026-11-08 | Multipart S3 uploads for large backups (#156), MCP read-only mode and secret redaction (#152), per-app CPU/RAM/volume history (#161) |

### Deploy sources and builds

* **Sources:** Git over HTTPS (public, or username and token), Git over SSH with a deploy key
  generated per app, or a container image from any registry with optional credentials. No GitHub or
  GitLab App, no commit statuses, no folder upload.
* **Build methods:** **Railpack** (default), **Framework** presets for Next.js, Nuxt and SvelteKit
  (Node server on 3000) and React, Angular and Astro (static, served on 80) with editable install,
  build and run commands, root and output directory and Node version, or a **Dockerfile** built by
  BuildKit.
* **Build arguments** are Dockerfile-only, stored in plain text and passed as `--build-arg` (ADR
  0008). Railpack and Framework builds get no build-time values at all: issue #44 ("Environment
  Variables Completely Missing", `NEXT_PUBLIC_*` undefined at build) has been open since 2025-11-19.
* **Settings are staged:** every edit is saved and applied only by **Deploy**. In the UI, Deploy on a
  Git app clones the branch head and builds; **Rebuild** rebuilds the current commit. The REST API
  separates a Deploy Action (no forced build) from Build-And-Deploy.
* **Rollback** (#125, 2026-09-06): Git images are tagged with the short commit hash and `latest`;
  rollback redeploys the commit's image, or rebuilds the commit if the image was purged. "Rollback
  only reverts code (git commit); app config (env, ports, domains, replicas) comes from the current
  DB state." Image apps have no rollback.
* **Backup before deployment** (#140, 1.0.0): a volume or database backup schedule can be marked to
  run before every deploy of the app **and of every app one network-policy hop away**, sequentially,
  logged into the deployment log; a failure aborts the deploy unless "Fail silently" is on. It covers
  UI deploys, rollbacks, webhooks and build-watch deploys.
* **Builds:** a global **Max Parallel Builds** (1–20, default 1) with queued builds shown as
  PENDING; a **Build Node** of Auto (the node with most free resources), "k3s native" with CPU and
  memory limits, or a named node. A global Builds page lists every build with Stop.
* Container command and arguments, run-as user and group, fsGroup, and a per-app **Privileged
  Mode** (#75).
* No Docker Compose: #12 (2025-02-07, 10 comments) is open.

### Domains, TLS and routing

* Traefik ingress with cert-manager HTTP-01 certificates per domain; HTTPS and HTTP-to-HTTPS
  redirect are per-domain switches. Domains take effect on the next Deploy.
* **quickstack.me:** `<prefix>.<hex-ip>.quickstack.me` resolves to the server's public IPv4 and gets
  its own Let's Encrypt certificate, so a fresh app has HTTPS with no DNS work. It replaced
  traefik.me in December 2025 (#43, #48). `quickstack.me` is not on the Public Suffix List (checked
  2026-11-08), so every install shares one "50 certificates per registered domain per week" budget;
  the docs mention the limit, no failure reports were found.
* HTTP basic auth per app as a Traefik middleware (credentials stored in plain text).
* **Node Ports** (30001–32767) for TCP and UDP on every node, bypassing Traefik.
* No DNS-01 or wildcard certificates (#11, 2025-02-04, the most-reacted open issue), no custom
  certificates, no redirects, no firewall or WAF, no maintenance page.

### Databases and services

* Five **database templates**: PostgreSQL (`postgres:18-alpine`), MySQL (`mysql:9`), MariaDB
  (`mariadb:11`), MongoDB (`mongo:7`), Redis (`redis:8-alpine`). Each is an app running one
  container with a volume and generated credentials in environment variables; there is no operator,
  replication or failover. Internet egress is denied by default.
* The **DB Access** tab shows name, user, password, internal host, port and connection URL with copy
  buttons, and toggles **DbGate**, **pgAdmin** or **phpMyAdmin** as separate deployments "publicly
  accessible (via a domain)"; the docs say to disable them after use.
* Native dumps for PostgreSQL, MariaDB and MongoDB; MySQL and Redis throw "Database backup for …
  is not yet implemented" (`database-backup.service.ts`) and can only be archived as a volume.

### Storage and backups

* **Volumes:** size in MB (grow only), storage class `local-path` or `longhorn` (Longhorn is the
  default since #146, 2026-10-08), access mode RWO or **RWX** fixed at creation. An RWX Longhorn
  volume can be **shared with other apps in the project** (#60). Any RWO volume forces one replica
  and the Recreate strategy.
* **File mounts:** text files stored in the database, mounted read-only.
* **File browser:** stops the app, mounts the volume in a temporary `filebrowser` pod on a domain with
  a generated password. A running volume can be downloaded as an archive.
* **Volume backups:** cron and retention per schedule to an S3 target. The panel copies the files
  out of the **running** app pod with a tar over exec into its own disk, then uploads from there
  (`backup.service.ts` `runBackupForVolume`); backups over about 5 GB failed with `EntityTooLarge`
  until multipart upload landed on `main` on 2026-10-29 (#155, #156), after 1.0.1.
* **Restore:** "No in-UI restore for volume and database backups yet." Volumes are restored by
  uploading the archive through the file browser; databases through pgAdmin, phpMyAdmin or DbGate.
* **System backups:** the SQLite file, daily to S3 once a target is set, plus **Run Backup Now**,
  download, and **restore by uploading a `.tar.gz` in the UI**, which replaces everything and
  restarts the panel. A password reset is a `curl | sh` script on the master.

### Scaling and high availability

* Manual replica count, CPU and memory reservations and limits, with a one-click suggested
  reservation from current usage (#72). "QuickStack currently has no horizontal autoscaling based on
  CPU or memory, and no time-based scaling." No scale to zero.
* Rolling updates (`maxSurge: 1`, `maxUnavailable: 0`) when no RWO volume is attached, Recreate
  otherwise; the docs say zero downtime needs a readiness probe.
* **One control-plane node.** `setup.sh` installs k3s without `--cluster-init`, so the first server
  keeps its state in SQLite; the UI adds workers only. An unreferenced
  `setup/setup-worker-master.sh` (2026-08-09) carries "TODO: Exact server version has to be an
  input, otherwise adding new master fails". #98 (multiple masters) and #115 (Traefik replicas) are
  open.
* Longhorn replicates volumes across nodes, so a pod with a Longhorn volume can restart on another
  node; this is the main argument the maintainers give for k3s over Docker (HN, 2025-01-28).

### Observability (logs, metrics, alerts, uptime)

* Live container logs per pod (last 1,000 lines), build logs with Kubernetes events streamed into the
  deployment log, admin-only panel logs.
* **Web terminal** (sh or bash) into a running container for users with write access.
* Live CPU and memory per app (every 10 s); node CPU, RAM and disk with 80% and 90% colour
  thresholds; Longhorn volume usage (the page fails without Longhorn, #105, open).
* **History** (#161, merged 2026-11-08): CPU, RAM and volume usage samples per app, 24 hours by
  default, retention configurable.
* "No built-in real-time telemetry or alerting integration." No alerts, uptime checks or log drains.

### Security, auth, roles, SSO, audit

* Email and password (bcrypt, cost 10), per-account TOTP; no way to require it.
* **SSO** (#107, 2026-08-21): generic OIDC, Google, Entra ID, GitHub; a default group per provider.
  Every provider is configured with `allowDangerousEmailAccountLinking: true`, `email_verified` is
  never read, and Entra logins fall back to `preferred_username` or `upn` for the address
  (`src/server/utils/auth-options.ts`). The docs warn: "A provider that lets users set an arbitrary
  email could otherwise be used to take over an existing account." SSO logins skip TOTP.
* **Groups and permissions:** a fixed Admin group plus custom groups with Read, Write, Create and
  Delete per project, optionally overridden per app or agent.
* **REST API keys:** owned by a user with that user's permissions, optional expiry, stored as an
  HMAC-SHA-256 hash; **API-only users** that cannot sign in to the UI.
* **Secrets at rest:** agent variables, LLM admin keys and SSH private keys are encrypted; app
  environment variables, Git tokens, registry passwords, S3 secret keys and basic-auth passwords are
  plain columns (`prisma/schema.prisma`).
* Deny-by-default network policies (below). No audit log: "No user audit log (login history,
  change history) is implemented."

### Preview environments and branches

* None. One branch per app; the webhook deploys that branch whatever was pushed.

### Templates and catalogue

* **16 app templates** in the picker (WordPress, n8n, Nextcloud, MinIO, Uptime Kuma, Gitea, Docmost,
  NGINX, Adminer, draw.io, Duplicati, Open WebUI with Ollama, Apache Tika, Libredesk, Chisel,
  LiteLLM), 5 database templates and 6 agent templates. **25 more** app templates exist but are
  commented out as "aren't tested yet" (`src/shared/templates/all.templates.ts`). Multi-service
  templates create several apps and wire their network rules and connection variables. No template
  updates; #32 ("How I contribute a new template?") is open.

### CLI, API, IaC, integrations

* **REST API** at `/api/v1` for projects, apps (POST upsert with full-schema writes), deployments,
  logs, agents and, since 1.0.1, `POST /apps/{id}/commands` (a shell command in a running pod).
  Errors are RFC 9457 problem details. OpenAPI UI and JSON can be made public by an admin.
* **SDK:** `@quickstackdev/sdk` 1.0.2 for JavaScript and TypeScript, generated from the OpenAPI.
* **No CLI.** #97 ("AI-Friendly CLI / MCP Interface", 2026-05-21) is open.
* **MCP (canary):** `/api/mcp` over Streamable HTTP with a REST API key, generated from the REST
  routes as three meta-tools — `quickstack_search_operations`, `quickstack_execute_read_operation`,
  `quickstack_execute_operation` — so every new route appears without new code. Since #152
  (2026-11-08): modes **Off / Read only / Read & write**, and fields marked `secret` are redacted in
  MCP responses unless **Send secrets in MCP responses** is turned on. The docs repository (last
  commit 2026-10-09) still says reads "can return secrets in plaintext".
* **Agent sandboxes (canary):** long-lived workspaces for AI coding agents (OpenCode, Gemini CLI,
  Copilot CLI, Claude Code, DeepSeek) built on the Kubernetes `agents.x-k8s.io` Sandbox controller,
  with suspend and resume, per-sandbox or per-tag volumes, a LiteLLM gateway that issues one virtual
  key per sandbox, and an optional gVisor `RuntimeClass` installed by a privileged DaemonSet that
  edits each node's k3s configuration and cannot be removed (ADRs 0009–0015).
* No IaC format, no Terraform provider, no cloud server provisioning.

### Notifications

* None. No email, chat or webhook notifications for deploys, backups or nodes were found in the
  code or the docs.

### Multi-server and networking

* Adding a worker: copy a `curl … setup-worker.sh | K3S_URL=… JOIN_TOKEN=… sh -` command from the UI
  and run it on the new server. Removing one: drain in the UI, then `kubectl delete node` by hand.
* Node traffic is k3s's default flannel VXLAN; the docs recommend a private network. Ports 80 and
  443, and 30000 for the UI unless switched off, are open on every node.
* **Network policies:** every app has ingress and egress rules per peer app, port and protocol, and
  an Internet Access switch; adding a rule on one side writes the mirror rule on the other; rules can
  cross projects. Without rules, apps cannot reach each other or the internet. The **Project
  Canvas** draws them as a graph and creating an edge by dragging creates the rules.
* Cluster add-ons with install, update and status: Longhorn, cert-manager, and on canary Agent
  Sandbox and gVisor. k3s upgrades through the System Upgrade Controller, masters first, workers one
  at a time with cordon and drain, on a chosen minor channel.
* Maintenance actions: purge registry images, clean build jobs, temporary files, old logs, orphaned
  pods, force-restart the registry, **delete all network policies**.

### Team and collaboration

* One installation is one organisation: users, groups and per-project or per-app permissions. No
  teams or tenants, no invitations by link, no comments.
* The **Project Canvas** is the main view: apps, databases, agents and the internet as nodes with
  live status, a drawer with Deployments, DB Access, Logs, Stats, Backups and Settings tabs, deep
  links (`?drawerAppId=&drawerTab=`), right-click menus (Deploy, Rebuild, Start, Stop, logs,
  settings, internet on or off, delete), and node positions saved per project.

### Developer experience and onboarding

* `curl -sfL https://get.quickstack.dev/setup.sh | sh -`, which asks for a network interface, then
  installs open-iscsi, nfs-common, jq and Helm, disables rpcbind and multipathd, loads `dm_crypt`,
  installs k3s, Longhorn and cert-manager, and starts a setup Job from `quickstack/quickstack:latest`.
  A non-interactive mode exists (#59). Minimum **2 CPU, 4 GB RAM, 40 GB disk**, Ubuntu 24.04+ or
  Debian 13+, public IPv4; the platform itself is budgeted at 0.5–1 GB of RAM.
* The installer fetches the versions from `get.quickstack.dev`. On 2026-11-08 the production install
  pins **k3s v1.31.3** (Kubernetes 1.31 reached end of life on 2025-11-11) and **Longhorn v1.7.2**
  (end of life 2025-09-04, endoflife.date); PR #82 to move them has been open since 2026-04-03.
* Updates from the UI on `latest` or `canary`. The update check calls
  `get.quickstack.dev/versioninfo.json?instanceId=<uuid>` up to every 15 minutes.
* Docs at quickstack.dev/docs: 54 pages of tutorials, how-to guides, concepts and reference, an
  `llms.txt`, a DeepWiki badge. English-only UI (no i18n library in `package.json`).
* Uninstall is "uninstall k3s" (maintainer on HN, 2025-01-28).

## What users love

Independent discussion is thin: one Show HN, one r/selfhosted launch thread (2025-02, read only as
a search excerpt), a Serbian r/programiranje thread listing it beside Dokku, Coolify, CapRover and
Dokploy, and a few blog round-ups. The GitHub issues carry most of the signal.

* **Kubernetes without Kubernetes, across servers.** The reason given by the maintainers and
  repeated in comparisons: "if you use k3s with Longhorn, volumes attached to a container are
  automatically replicated across multiple VPS nodes … If a VPS node goes down, the database can
  seamlessly run on another node" (HN, 2025-01-28). A commenter: "That was very helpful, thanks!
  Might give it a shot."
* **In production and happy.** "I am using QuickStack in production and overall it works really
  well" (#79, 2026-03-22); "great work!" (#22); "Thank you for your great efforts and great
  application!" (#23).
* **Free and complete.** GPL-3.0, no tiers, one command, a UI that looks like Dokploy's (HN asked
  whether it was a fork; both use shadcn/ui).
* **Fast, visible development.** 155 commits (merges excluded) in September 2026 and 57 in October;
  outside contributors fixed rollouts (#103), S3 endpoints (#112), cert-manager's install (#77) and
  the phpMyAdmin image (#153).

## What users complain about

* **Installation.** Stuck at "Waiting for all pods" on AWS EC2 (#58); UI unreachable on port 30000
  after a successful install (#40, #41); `jq` not found (#29); NFS utilities missing (#10); kubeconfig
  permissions (#49); Oracle Cloud public IP not found (#28).
* **Build-time variables.** `NEXT_PUBLIC_*` undefined for Railpack builds (#44, open, 7 comments);
  build arguments exist only for Dockerfiles.
* **Builds on small nodes.** A Next.js build OOMed on the small master while a larger worker sat idle
  (#80), which led to the Build Node setting; parallel builds were asked for (#99) and shipped as a
  global limit.
* **Rollouts.** A restart redeployed old builds "ad infinitum" (#100, fixed in #103 and #137,
  September 2026).
* **Backups.** Large volumes failed (#155), `lost+found` broke backups (#68), registry S3 options for
  Hetzner were missing (#106); restore is manual.
* **Single points of failure.** One control plane (#98), one Traefik replica (#115), "High
  Availability and Autoscaling" (#22, open since 2025-03-12).
* **Missing pieces.** Docker Compose (#12), DNS-01 (#11), cross-project networking and
  post-deploy hooks (#79), StatefulSets (#27), pinning an app to a node (#31), automatic clean-up of
  images and jobs (#46), clearer Deploy versus Rebuild buttons (#23).

## Security record

**Where I looked (2026-11-08):** NVD keyword search "quickstack" (one unrelated 2013 OpenStack CVE),
the code and history on `main`, the 1.0.1 release notes, `SECURITY.md`. GitHub's advisory pages could
not be read from this sandbox; no CVE was found anywhere.

* **The pod terminal was unauthenticated for 23 months.** The Socket.IO namespace `/pod-terminal`
  was added on 2024-11-27 (`65abd04e`) and called `k8s.Exec` with whatever namespace, pod and
  container the client sent, with no session check (`src/socket-io.server.ts` and
  `terminal.service.ts` before `879153ba`). The panel's ServiceAccount is bound to `cluster-admin`
  and the panel listens on NodePort 30000 on every node by default (and on its domain), so anyone
  who could reach it could open a shell in any pod, including the panel's own, which holds a
  cluster-admin token. PR #154 (2026-10-26) added session, expiry, Origin and per-workload checks,
  rate limits, and revalidation every 120 s. 1.0.1 says "An issue in the previous implementation of the Terminal WebSocket allowed
  connections without proper authentication and authorization … Update as soon as possible", credits
  ucsurf, and has no CVE or advisory that could be found.
* **SSO account linking by email** with `allowDangerousEmailAccountLinking`, no `email_verified`
  check and an Entra UPN fallback — the class of Coolify's CVE-2026-86117 and Skifity's own Phase
  77. Documented as a warning, not fixed.
* **Plain-text secrets** in SQLite (app variables, Git tokens, registry and S3 credentials,
  basic-auth passwords), and a system backup is that file. Build arguments are plain text by
  decision (ADR 0008).
* **Unsigned deploy webhook:** `/api/v1/webhook/deploy?id=` accepts GET or POST, checks only the
  ID, and builds the configured branch on any push.
* **MCP read operations returned secrets** in plain text from 2026-10-03 (canary) until #152
  (2026-11-08) made redaction the default — with a switch to turn it off.
* **Public helpers:** DbGate, pgAdmin, phpMyAdmin and the file browser run on public domains with
  generated passwords.
* `/api/volume-data-download` checks that a session exists but not what it may read; files are named
  after a volume id or an S3 key and share one directory with downloaded system backups. Whether a
  non-admin can fetch someone else's archive this way was not tested. `/api/print-schedules-jobs`
  needs no authentication (it only writes to the log). Next.js middleware matches `/` only; routes
  check sessions themselves.
* **Out-of-date base:** production installs pin an end-of-life Kubernetes and Longhorn (above).
* `SECURITY.md` (email to the maintainer, fixes on "the current release line" only) exists; there is
  no support window.

## Against Skifity

Skifity statuses come from `docs/progress.md`: the single-server path runs on a real k3s on every
push (the `Cluster` workflow), and adding, promoting and losing a second server in the `Two servers`
workflow. Longhorn appears in neither, nor anywhere in `docs/progress.md`.

| Capability | QuickStack | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git sources | HTTPS with token, SSH deploy key; unsigned ID webhook | GitHub, GitLab, Gitea, Bitbucket with signed webhooks and commit statuses (`internal/gitsrc/webhook.go`, `report.go`), folder upload (`internal/cli/up.go`) | Skifity ahead |
| Build methods | Railpack, six framework presets, Dockerfile | Railpack, Nixpacks, Dockerfile, static; root dir, build and start commands (`internal/store/models.go:274-287`) | parity |
| Build-time variables | Dockerfile only (#44 open) | Build variables reach Railpack (Phase 79) | Skifity ahead |
| Docker Compose | None (#12) | Stacks from Compose (`internal/api/stack_handlers.go`) | Skifity ahead |
| Rollback | Code only, today's settings | Image and settings, with a plan first (`internal/api/api.go:498`, `internal/deploy/deployer.go`) | Skifity ahead |
| Backup before a deploy | Opt-in per schedule, app plus apps one hop away, aborts on failure | Only before a template update (`internal/api/template_updates.go:182`) or a dump import (`internal/backup/importdump.go:440`) | **behind** |
| Build concurrency and placement | 1–20 slots; Auto, k3s native or a named node | Fair slots per team (`internal/deploy/buildslots.go`); no node choice (no affinity in `internal/builder`) | behind (small) |
| **Domains, TLS and routing** | | | |
| Certificates | HTTP-01 per domain; no DNS-01, no own certificates | HTTP-01, team certificates (`/teams/{team}/certificates`), DNS records at the provider (`/dns-providers`) | Skifity ahead |
| Address without a domain | quickstack.me with HTTPS, one shared Let's Encrypt budget | sslip.io over HTTP by design (ADR-0015) | different: theirs easier, ours does not share a rate limit |
| Firewall, maintenance | None | Rules by address, country, network, path, header (`internal/edgerules`, `internal/guard`), maintenance (`internal/guard/maintenance.go`) | Skifity ahead |
| Non-HTTP ports | NodePorts on every node | Public ports through ServiceLB (`internal/kube/ports.go`) | parity |
| **Databases and services** | | | |
| Engines | 5 single-container templates | 9 engines, PostgreSQL through CloudNativePG (`internal/dbsvc`, Phase 112) | Skifity ahead |
| Database high availability | None | CloudNativePG instances | Skifity ahead |
| Native dumps | PostgreSQL, MariaDB, MongoDB | Those plus MySQL, Redis, Valkey (`docs/databases.md`) | Skifity ahead |
| Admin UI for a database | DbGate, pgAdmin, phpMyAdmin per database, on a public domain | `skifity db connect` (Phase 105); Adminer, pgAdmin, phpMyAdmin only as templates (`internal/templates/catalogue/`) | behind on convenience |
| **Storage and backups** | | | |
| Replicated volumes | Longhorn on every install, default class, host packages installed | Optional component (`internal/settings/settings.go:604`, `internal/cluster/components.go:33`); nothing installs or checks open-iscsi (`internal/provision`, `installer/`); never run | **behind** |
| Volume options | Class per volume, RWO or RWX, shared between apps, grow | RWO always (`internal/kube/manifests.go:550`), no class (`internal/api/apps_handlers.go:1874`), no resize route (`internal/api/api.go:542-550`) | **behind** |
| File mounts | Plain text in the database | Sealed, one Secret per app (`internal/kube/files.go`) | Skifity ahead |
| Backup restore | Manual for volumes and databases | Both in the panel, verified, sealed with a passphrase (`internal/api/api.go:547-548`, `internal/sealed`) | Skifity ahead |
| Panel backup | Daily to S3, restore by upload in the UI | Daily to the bucket, restore with `skifity admin restore-db` on the server (Phase 84) | parity; behind on restore steps |
| Browse or download a volume | File browser, live download | None | behind |
| **Scaling and high availability** | | | |
| Instances | Manual replicas | HPA, scale to zero, readiness checker (`internal/kube/apply.go`, `scaletozero.go`, `internal/deploy/scaling.go`) | Skifity ahead |
| Control-plane HA | One master, SQLite datastore | Embedded etcd from the first install (`installer/install.sh`), promote (`internal/api/api.go:429`) | Skifity ahead |
| Sizing help | Suggested reservation from usage | Usage history and thresholds, no suggestion (Phase 95) | behind (small) |
| **Observability** | | | |
| Logs and events | Live, 1,000 lines, events in deploy log | Live, previous container, events (`internal/cluster/events.go`) | parity |
| Usage history | 24 h per app (since today), node gauges | Three days per app, servers (Phases 95, 99) | Skifity ahead |
| Alerts, log drains | None | `app.alert` and notifications, log drains (`internal/logdrain`) | Skifity ahead |
| Shell | Web terminal | None by decision (`docs/roadmap.md:149`); one-off commands | deliberate difference |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | bcrypt, optional TOTP, SSO bypasses TOTP | Argon2id, TOTP, passkeys, team-required two-factor (Phase 91) | Skifity ahead |
| SSO | Four provider types, email linking | Identity bound by issuer and subject (Phase 77), groups to teams (Phase 92) | Skifity ahead |
| Permissions | Groups, per project and per app | Owner, admin, member, viewer; project limits (`internal/store/models.go`, Phase 86) | parity (theirs finer per app) |
| API credentials | User keys, expiry, API-only users | Scoped, team-bound, expiring tokens owned by a user (`internal/store/models.go` `APIToken`) | parity; behind on a non-person identity |
| Secrets at rest | Mostly plain text | Sealed per row, write-only (`internal/crypto`) | Skifity ahead |
| Audit | None | Team audit log (`/teams/{team}/audit`) | Skifity ahead |
| Isolation inside a project | Deny by default between apps; internet off per app | Every pod in an environment reaches every other on any port, and all have internet egress (`internal/kube/namespace.go:194,213,222`) | **behind** |
| **Preview environments** | None | Per pull request, seeded databases (`internal/api/webhook_handlers.go`, Phase 107) | Skifity ahead |
| **Templates** | 16 apps offered, 25 commented out as untested, no updates | 374 with updates (`internal/templates/catalogue`, Phase 102) | Skifity ahead |
| **CLI, API, IaC, integrations** | | | |
| CLI | None | `internal/cli` | Skifity ahead |
| API and SDK | REST, OpenAPI, JS SDK | REST, `internal/api/openapi.yaml`; no SDK | parity; behind on SDK |
| MCP | Generated from REST, three meta-tools, modes, redaction by default (canary) | 47 tools, read-only by scope or role, secrets never returned (`internal/mcpserver`, Phase 87) | Skifity ahead on secrets; theirs covers every route |
| Config as code, cloud servers | None | `skifity.yaml` plan and apply (`internal/blueprint`), Hetzner (`internal/provision/cloud.go`) | Skifity ahead |
| AI agent sandboxes | Canary | None | absent in Skifity |
| **Notifications** | None | Channels and events (`internal/notify`) | Skifity ahead |
| **Multi-server and networking** | | | |
| Adding and removing servers | Paste a join command; remove with kubectl | Over SSH from the panel, promote, remove with drain (`internal/provision`) | Skifity ahead |
| Between servers | Unencrypted VXLAN | WireGuard (`internal/settings/settings.go` `FlannelWireGuard`) | Skifity ahead |
| k3s and component upgrades | From the UI; production pinned to end-of-life versions | From the UI, stable channel (Phase 109) | parity; Skifity current |
| **Team and collaboration** | | | |
| Tenancy | One organisation | Teams (`internal/api/api.go`) | Skifity ahead |
| Visual map of a project | Project Canvas | None (no graph component in `web/src`) | **behind** |
| **Developer experience** | | | |
| Languages | English | Five, with a build gate (`web/src/locales`) | Skifity ahead |
| Errors | RFC 9457 problem details | Cause, impact, fix (`internal/errdoc`) | Skifity ahead |
| Maturity | 1.0.1, ~20,000 image pulls | v0.1.0 (Phase 112) | behind |

### Claims in this repository that no longer hold

* `docs/research/competitors.md`, "Where Skifity is actually different", point 1: "None of them
  puts Kubernetes on one small server with one command." QuickStack has done that since 0.0.1
  (2025-01-19); today one command installs k3s, Longhorn and cert-manager on a 2-CPU, 4 GB server.
  Dokku's k3s scheduler is in the same file. What is still Skifity's is narrower: embedded etcd from the first install,
  servers added, promoted and removed over SSH from the panel, WireGuard between them, teams, and
  sealed secrets.
* `internal/deploy/scaling.go:48` and `:177` tell a user that turning on cross-node storage lets a
  volume "be shared" by several instances. Every app volume is ReadWriteOnce
  (`internal/kube/manifests.go:550`), and Longhorn attaches an RWO volume to one node at a time;
  sharing needs ReadWriteMany. `docs/faq.md` ("install Longhorn") and `docs/troubleshooting.md`
  point at a component that nothing prepares the servers for and no test has run.

## Gaps worth closing in Skifity

### P1 — Cross-node storage that works, and advice that is true

* **What:** (1) Now: correct the two scaling findings so they no longer promise sharing from
  Longhorn. (2) Then make Longhorn a supported path: install and check its host prerequisites
  (open-iscsi with `iscsid` running, `nfs-common` for RWX, multipathd not claiming the disks) when a
  server is added or when the component is turned on, with a preflight that names the server that
  lacks them; let a volume choose "this server" or "replicated" (and RWX for one that several
  instances or apps share); offer to move an existing volume onto replicated storage through the
  backup and restore Skifity already has; and run it in the `Two servers` workflow — write, switch the
  server off, read on the other.
* **Evidence:** QuickStack's setup installs open-iscsi and nfs-common, disables multipathd and loads
  `dm_crypt` on every node, makes Longhorn the default class (#146), and offers RWX volumes shared
  between apps (#60). Longhorn's own documentation requires open-iscsi on every node. In Skifity,
  `internal/provision` and `installer/` mention neither iSCSI nor NFS, `handleCreateVolume`
  (`internal/api/apps_handlers.go:1874`) sets no class, every claim is RWO, and Longhorn has no
  phase in `docs/progress.md` and no test beyond a comment in `test/cluster/verify.sh`. Survival of
  a server loss is the reason to run Kubernetes at all (`docs/faq.md`, "Why Kubernetes
  underneath?").
* **Fit:** `internal/deploy/scaling.go` (message), `internal/provision/scripts.go` and
  `preflight.go`, `internal/kube/manifests.go` volume claims, a `storage_class` and access mode on
  the volume (`internal/store/apps.go` already has the column), the volume form, `test/cluster`.
* **Size:** S for the message; L for the rest. **Without a cluster:** the message, the preflight
  parsing, manifests and API yes; whether a volume survives a server needs the two-server workflow.

### P1 — Back up before a deploy

* **What:** a switch per app, "back up before each deploy", that backs up the app's volumes and the
  databases linked to it before the release command runs, waits, and stops the deploy with the
  reason if a backup fails, unless "deploy anyway" is also set. The same for a rollback. The backups
  are kept as "before deploy" alongside the hand-taken ones.
* **Evidence:** QuickStack's "Backup before deployment" (#140) covers the app and every app one
  network hop away and aborts by default. Skifity already does this for template updates
  (`internal/api/template_updates.go:182`, which computes the targets and waits) and dump imports,
  but not for an ordinary deploy, which is when a release command runs migrations.
* **Fit:** the target list from `template_updates.go`, called from `internal/deploy` before the
  release step; the backup kinds in `internal/backup/manager.go`; a toggle on the app's Backups tab
  and in `skifity.yaml`.
* **Size:** M. **Without a cluster:** ordering, abort and audit yes against the fake backup manager;
  the backups themselves already run in the `Cluster` workflow.

### P2 — Databases reachable only by the apps linked to them

* **What:** derive network policy from what Skifity already knows: a managed database accepts
  connections only from apps linked to it (and from the panel, its operator and its backups), and has
  no internet egress; an app gets a "no internet" switch. No rule editor: the link is the rule.
* **Evidence:** QuickStack denies traffic between apps and to the internet by default and draws the
  allowed paths (ADR 0007). In Skifity the environment's allow policy admits every pod from every
  other pod in it on every port, and gives every pod internet egress
  (`internal/kube/namespace.go:194,213,222`), so a compromised app in an environment can try every
  database in it.
* **Fit:** `internal/kube/namespace.go` and `databasePolicy`, `internal/dbsvc` links, a switch on
  the app's settings; keep "derive, do not synchronise".
* **Size:** M. **Without a cluster:** rendered policies and `EgressAllows`-style tests yes;
  enforcement needs the `Cluster` workflow.

### P2 — Grow an app's volume

* **What:** a larger size on an existing app volume, refused below the current size and when the
  class cannot expand, with the errdoc entry databases already use.
* **Evidence:** QuickStack resizes volumes upward; Skifity grows database disks
  (`docs/databases.md`, `database.storage_shrink`) but app volumes have create and delete only
  (`internal/api/api.go:542-550`).
* **Fit:** a `PATCH` on the volume, the quota check in `internal/api/ports.go`, the existing
  `DatabaseStorageNotExpandable` reasoning. **Size:** S. **Without a cluster:** API and manifests
  yes; the resize itself needs one.

### P2 — A map of the project

* **What:** a read-only view of an environment: apps, databases, links, domains, public ports,
  status on each node, a click opening the app. Later, linking a database by dragging to it.
* **Evidence:** QuickStack's Project Canvas is the main screen (React Flow and ELK) and its README's
  third feature. Skifity has lists only; there is no graph component in `web/src`.
* **Fit:** a new tab on `web/src/pages/project-detail.tsx`, drawn as SVG the way the usage charts
  are (Phase 95), from data the project and link APIs already return. **Size:** M. **Without a
  cluster:** yes, with Playwright.

### P2 — Open a database in a browser

* **What:** "Open in a browser" on a managed database: a short-lived Adminer (or pgAdmin) pod in the
  database's environment, connected with the database's credentials, reached through the panel
  with the user's session and removed after an idle period. Nothing on a public domain.
* **Evidence:** QuickStack's DB Access tab starts DbGate, pgAdmin or phpMyAdmin per database, but
  "publicly accessible (via a domain)". Skifity has `skifity db connect` and the same tools as
  unconnected templates.
* **Fit:** `internal/dbsvc`, the run-Job machinery, the panel's proxy. **Size:** M. **Without a
  cluster:** API and rendered objects yes; the session through the proxy needs one.

### P2 — Suggested limits

* **What:** next to memory and CPU limits, "use 1.3 times the peak of the last three days", from the
  samples Phase 95 keeps.
* **Evidence:** QuickStack's one-click suggestion (#72). **Fit:** `GET /api/apps/{app}/metrics`, the
  Scaling tab. **Size:** S. **Without a cluster:** yes.

### P3 — Restore the panel from a backup at first-run setup

* **What:** on a fresh install's setup screen, "restore from a backup": upload a panel backup and the
  recovery key, run the checks `restore-db` runs, restart.
* **Evidence:** QuickStack restores system backups by upload in the UI. Skifity's restore needs a
  shell on the server (`internal/cli/admin.go`, Phase 84). **Size:** M. **Without a cluster:** yes.

### P3 — Choose where builds run

* **What:** a build server setting: any, a named server, or the one with most free memory.
* **Evidence:** QuickStack #80 and #85; Coolify's build servers. **Fit:** `internal/builder` Job,
  `internal/settings`. **Size:** S. **Without a cluster:** manifests yes; placement needs two servers.

### P3 — Look inside a volume

* **What:** list and download a volume's files (or the latest backup of it) from the panel, read-only,
  without stopping the app.
* **Evidence:** QuickStack's file browser and live download. **Size:** M. **Without a cluster:** no.

### P3 — An identity for CI that is not a person

* **What:** a team-owned token (or an API-only member) that survives the person who created it
  leaving.
* **Evidence:** QuickStack's API-only users; Skifity's `APIToken` has a `UserID`. **Size:** S.
  **Without a cluster:** yes.

### P3 — An administrator switch for the remote MCP endpoint

* **What:** Off, Read only, or Read and write for `/api/mcp`, panel-wide.
* **Evidence:** QuickStack's `mcpServerMode`. Skifity's endpoint is always served to any token
  holder (Phase 87); scopes cover read-only per token. **Size:** S. **Without a cluster:** yes.

### P3 — A sandboxed runtime for apps you did not write

* **What:** an optional gVisor `RuntimeClass` per environment, installed over SSH when a server is
  prepared rather than by a privileged DaemonSet.
* **Evidence:** QuickStack's gVisor add-on (ADR 0009). Skifity has the NVIDIA runtime class pattern
  (`internal/kube/gpu.go`). **Size:** L. **Without a cluster:** no.

## Things to deliberately not copy

* **A browser terminal on an unauthenticated socket.** Twenty-three months of shell access to every
  pod for anyone who reached the panel, behind a cluster-admin ServiceAccount. Skifity's decision
  against a shell (`docs/roadmap.md:149`) stands; a one-off command in the app's image is enough.
* **SSO linking by email.** `allowDangerousEmailAccountLinking` with no `email_verified` and a UPN
  fallback is the nOAuth class; Skifity fixed it in Phase 77 and should not regress.
* **Secrets in plain columns and a switch that sends them to a model.** Keep them sealed and
  write-only; there should be no "Send secrets in MCP responses".
* **A webhook that accepts GET with an ID** and builds on any push.
* **A shared magic domain with a certificate per app.** quickstack.me is not on the Public Suffix
  List; every install shares one Let's Encrypt budget. ADR-0015 is the reason Skifity does not.
* **Admin tools and file browsers on public domains** with generated passwords.
* **Privileged mode as an app setting.** Skifity's floor is baseline (`internal/kube/podsecurity.go`).
* **Floating `latest` and `canary` panel images**, and a version check that sends an install id.
* **Pinning installs to end-of-life Kubernetes and Longhorn** while the update waits in a pull
  request.
* **A second product inside the panel.** Agent sandboxes, a LiteLLM gateway and per-sandbox virtual
  keys passed as plain claim environment (ADR 0014) are a different business; warm pools are already
  disabled to make them fit (ADR 0015).
* **Host changes from a privileged DaemonSet that cannot be undone** (the gVisor add-on). Skifity
  changes servers over SSH, in steps it can check.
* **Databases as single containers with no restore.** Skifity's managed engines and in-panel restore
  are the answer.

## Sources

All read on 2026-11-08.

* https://github.com/biersoeckli/QuickStack — cloned (`main` at `90d872d`, history to the first
  commit), tags via `git ls-remote`; metadata via the GitHub search API (stars, forks, open issues,
  licence, created and pushed dates)
* GitHub search API: every issue and pull request in `biersoeckli/QuickStack` (56 issues, 106 pull
  requests), bodies read; comments could not be read from this sandbox
* Files read in the clone: `README.md`, `GLOSSARY.md`, `SECURITY.md`, its ADRs 0004 to 0015,
  `setup/setup.sh`, `setup/setup-canary.sh`, `setup/setup-worker.sh`, `setup/setup-worker-master.sh`,
  `prisma/schema.prisma`, `package.json`, `src/middleware.ts`, `src/server.ts`,
  `src/socket-io.server.ts` (before and after `879153ba`), `src/server/services/terminal.service.ts`,
  `src/server/utils/auth-options.ts`, `src/app/api/v1/webhook/deploy/route.ts`,
  `src/app/api/mcp/route.ts`, `src/app/api/volume-data-download/route.ts`,
  `src/app/api/print-schedules-jobs/route.ts`, `src/shared/templates/all.templates.ts`,
  `src/server/services/qs.service.ts`, `qs-update.service.ts`,
  `src/server/adapter/qs-versioninfo.adapter.ts`, `hostname-dns-provider.service.ts`,
  `ingress.service.ts`, `pvc.service.ts`, `standalone-services/backup.service.ts`,
  `system-backup.service.ts`, `database-backup.service.ts`, `app-monitoring-history.service.ts`
* https://github.com/biersoeckli/QuickStack-Docs — cloned (last commit 2026-10-09), all 54 pages under
  `content/docs` read
* https://quickstack.dev/ and https://quickstack.dev/docs/mcp-server (live site)
* https://get.quickstack.dev/versioninfo.json (1.0.1 release notes), `k3s-versions.json`,
  `longhorn-versions.json`
* https://hub.docker.com/v2/repositories/quickstack/quickstack/ and its tags
* https://registry.npmjs.org/@quickstackdev%2Fsdk and https://api.npmjs.org/downloads/point/last-month/@quickstackdev/sdk
* https://hn.algolia.com/api/v1/items/42849917 (Show HN, 2025-01-28) and the Algolia search for "quickstack"
* https://www.reddit.com/r/selfhosted/comments/1igktsv/alternative_to_caprover_and_coolify_quickstack/
  and https://www.reddit.com/r/programiranje/comments/1qiejz1/ (search excerpts only; Reddit refused
  direct reads)
* https://achtpunkt.ch/produkte/quickstack
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=quickstack
* https://publicsuffix.org/list/public_suffix_list.dat
* https://endoflife.date/api/kubernetes.json and https://endoflife.date/api/longhorn.json
* Skifity, at `618dff2`: `docs/progress.md` (Phases 73, 77, 79, 84, 86, 87, 90, 91, 92, 95, 99, 101,
  102, 105, 107, 108, 109, 112), `docs/decisions.md` (ADR-0015), `docs/faq.md`,
  `docs/troubleshooting.md`, `docs/databases.md`, `docs/roadmap.md`, `docs/research/competitors.md`,
  and the code files named in the table above
