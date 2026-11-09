# Sailbox

> **Status.** Written on 2026-11-08 against Skifity's code at `29147f1` and Sailbox's `main` at
> `ad01f84` (v0.1.8, 2026-09-13, still the newest commit that day). Nothing named here has been
> changed in Skifity yet; the gaps below are proposals.

Sailbox (`sailboxhq/sailbox`, sailbox.dev) is a self-hosted PaaS that puts k3s on a server and
gives it a web panel for apps, databases and cron jobs. Its pitch is the opposite of Skifity's on
one point and the same on another: the same, that Coolify, Dokploy and CapRover are "Docker
Compose or Docker Swarm" underneath and Kubernetes is the better foundation; the opposite, that
Kubernetes should be shown rather than hidden — "Sailbox doesn't wrap Kubernetes — it **is**
Kubernetes", with Helm releases, DaemonSets, a cluster topology and a Traefik YAML editor in the
panel. It is for self-hosters and small teams on a VPS or a Raspberry Pi. **Architecture:** a Go
1.25 API (Gin, the Bun ORM over **PostgreSQL 18**, client-go v0.32.3) and a React 19 frontend
(TanStack Router and Query, Tailwind, Radix primitives, React Flow), built into one image in which
Caddy serves the frontend on port 3000 and proxies `/api` and `/ws` to the API on 8080. The panel
runs **outside the cluster**: `install.sh` installs Docker (get.docker.com) and k3s (get.k3s.io)
on the same host, then starts the panel and its PostgreSQL with Docker Compose, the panel in
`network_mode: host` with the k3s admin kubeconfig and `/var/run/docker.sock` mounted. Inside the
cluster it uses k3s's Traefik (Let's Encrypt through the TLS-ALPN challenge, set with a
`HelmChartConfig`), a `registry:2` in `sailbox-system` on NodePort 30500, a Kaniko Job per build
and a StatefulSet per database. A `cmd/worker` exists and contains only TODOs. The code is about
24,500 lines of Go and 20,400 of TypeScript, with one Go test file (`internal/auth/session_test.go`,
94 lines). **Licence:** AGPL-3.0-or-later with additional terms under Section 7(b): every copy and
derivative "must keep the 'Powered by Sailbox' notice visible in the user interface", in the
sidebar or footer, linked to the repository, "not be hidden, reduced in size, or made less
visible" (`NOTICE`). A `BrandGuard` React component checks every 30 seconds that the notice is in
the page and visible and logs a console warning if not; the API sets `X-Powered-By`; and
`.github/INSTRUCTIONS.md` tells AI assistants never to help remove it. **Pricing:** free. A
commercial licence without the attribution term is offered by email, with no price published;
GitHub Sponsors tiers are $5, $25 and $100 a month (sailbox.dev/sponsorships). No hosted offer was
found. **Maturity:** first commit 2026-03-27 (the whole product in one commit); tags v0.1.0
(2026-03-28) to v0.1.7 (2026-04-02), then **v0.1.8 (2026-09-13)** after five months without a
commit, and nothing since (56 days on 2026-11-08). Images `ghcr.io/sailboxhq/sailbox` 0.1.0 to
0.1.8 and `latest`. **Adoption** (read 2026-11-08): 17 GitHub stars, 9 forks, 2 open issues, 1
pull request ever (GitHub search API); no story or comment about it on Hacker News (Algolia); the
GHCR download count could not be read. **People:** every one of the 15 commits is by one author,
Alan Liu; the copyright line is "Sailbox Authors" and the contact `hello@sailbox.dev`. The site and
the README are in English and Chinese; the panel itself is English only.

## Feature inventory

### What shipped, release by release

Dates are the tag commits' dates (`git` in a clone; times are UTC+8). GitHub's release pages and
repository API could not be read from this sandbox; the release workflow publishes notes that
GitHub generates from commit titles.

| Release | Date | What changed that matters for a comparison |
|---|---|---|
| v0.1.0 | 2026-03-28 | The product as first committed the day before: Git and image apps, Kaniko builds, five database engines, cron jobs, cluster pages, alerts, four notification channels |
| v0.1.1–v0.1.3 | 2026-03-28 | Traefik management endpoints; Kubernetes name-conflict checks for apps and databases |
| v0.1.4–v0.1.6 | 2026-03-29 | Team invitations without an account, panel domain verification, self-upgrade through a one-shot upgrader container, domain and ingress fixes |
| v0.1.7 | 2026-04-02 | Error-handling refactor; infrastructure re-applied at startup |
| v0.1.8 | 2026-09-13 | Database CPU and memory limits editable; the PostgreSQL 18 mount fixed (PR #1); and, not in the commit message, a project-access guard on every by-ID route, admin-only infrastructure routes, credentials redacted from API answers and session revocation (81 files, +2,333/−436) — see "Security record" |

### Deploy sources and builds

* **Sources:** a Git repository through a GitHub App created with GitHub's manifest flow, or a
  GitHub or GitLab token stored as a "shared resource"; or a container image. Webhooks for GitHub
  (HMAC-checked) and GitLab, a branch filter, **watch paths** and an auto-deploy switch. No
  Bitbucket or Gitea, no folder upload, no Docker Compose (an `applications.compose_file` column
  exists and nothing reads it).
* **Builds:** one Kubernetes Job per build: `alpine/git:latest` clones the branch at depth 1, then
  `gcr.io/kaniko-project/executor:latest` builds the Dockerfile and pushes to the in-cluster
  registry with `--insecure --skip-tls-verify` (`orchestrator/k3s/build.go:74-188`). Requests
  500m and 512 MiB, limits 2 CPUs and 2 GiB, 30 minutes. **Kaniko's upstream repository was
  archived on 2025-06-03** ("no longer developed or maintained"); maintained forks exist
  (`chainguard-forks/kaniko`, `osscontainertools/kaniko`), and Sailbox uses neither.
* **Layer cache** in the registry (`<app>-cache`), a per-app **"no cache"** switch, and **Clear
  build cache**, a Job that deletes the cache repository's manifests. A Git deploy whose commit
  equals the last successful one reuses that image unless **Force Build** is pressed.
* **Nixpacks** is offered as "auto-detect language & build": an init container runs `nixpacks
  build` in `ghcr.io/railwayapp/nixpacks:latest`. That image is Nixpacks' Ubuntu-and-Nix base with
  no `nixpacks` command in it (its layer history, read 2026-11-08; Skifity found the same in Phase
  80), so the plan step cannot run. A `buildpacks` build type exists in the model with no code
  behind it. Read from the code, not run.
* **Build arguments and build environment variables** are passed as plain `--build-arg` flags and
  container environment on the Job.
* **Cancel** an in-flight build: the deploy goroutine's context is cancelled and the build Job is
  deleted. One deployment in flight per app, enforced by a unique index.
* **Rollout:** the Deployment is created or updated and the deployment is marked **success at
  once**; nothing waits for the new pods to become ready (`service/deploy_service.go:233-265`,
  `ProgressDeadlineSeconds: 300` is all). Per app: rolling with `maxSurge` and `maxUnavailable`, or
  Recreate, and a termination grace period.
* **Rollback** is a restart. `Rollback` annotates the *current* Deployment's pod template with
  `sailbox/rollback-time`, which rolls the same image again, and then records a new deployment with
  status success and the *old* image's name (`orchestrator/k3s/deploy.go:435-450`,
  `service/deploy_service.go:426-463`). The history then says the app is on the old version while
  it runs the new one. Read from the code, not run.

### Domains, TLS and routing

* A custom domain per app becomes an Ingress with Traefik's `letsencrypt` resolver; HTTP-to-HTTPS
  is a Traefik middleware. A generated name, `<app>-<id>.<base domain>`, where the base domain is
  set in Settings (for example `<ip>.sslip.io`); names on sslip.io, nip.io and traefik.me get no
  certificate.
* The panel can be given its own domain, with a check of DNS, reachability and the certificate's
  issuer. Certificate expiry is read by a TLS handshake.
* **Traefik configuration editor:** the YAML of k3s's packaged `traefik` HelmChart (or the
  HelmChartConfig when there is no HelmChart), editable and restartable by admins. k3s re-applies
  its packaged manifests when it starts, so edits to the HelmChart itself do not last; not run.
* No path routing (every rule is `/`), no uploaded certificates, no DNS-01 or wildcards, no
  redirects, no firewall, no basic auth, no maintenance page.

### Databases and services

* PostgreSQL 18, 17, 16, 15; MySQL 9.2, 9.0, 8.4, 8.0; MariaDB 11.7, 11.4, 10.11; Redis 8.0, 7.4,
  7.2; MongoDB 8.0, 7.0, 6.0 — official images by tag (`model/database_versions.go`). Each is a
  one-replica StatefulSet with a volume, probes and generated credentials in a Kubernetes Secret.
  Version and data cannot be changed after creation; CPU and memory can (v0.1.8).
* **External access** is a NodePort, chosen or assigned, on every server, with no address filter.
* No replication or failover, no linking to apps (the connection string is copied by hand), no
  operator.

### Storage and backups

* **App volumes:** ReadWriteOnce claims on the default class (k3s's local-path). Raising a size
  expands the claim on the next deploy; a cluster-wide Volumes page lists every claim with Expand
  and Delete.
* **Database backups:** a Job runs the engine's dump (`pg_dump` as SQL, `mysqldump` or
  `mariadb-dump`, `mongodump --archive`, `redis-cli --rdb`) and uploads it with
  `amazon/aws-cli:latest` to an S3 bucket; **restore** from a listed backup is a button. Backups
  are neither encrypted nor checked, and have no retention.
* **Scheduled database backups do not run.** The form stores a schedule and an on switch
  (`service/database_service.go:363-408`) and nothing reads them: the only caller of the backup is
  the "Backup now" route (`api/v1/database_handler.go:188`), and `cmd/worker` is the empty stub
  whose TODO says "Register task handlers (build, deploy, backup)". The README and the site both
  say "Automated S3 backups with schedule and retention". Read from the code, not run.
* No volume backups.
* **Panel backups:** `pg_dump -F c` of the panel's database to S3 on a cron schedule with
  retention (30 by default), and **restore from S3 on a fresh panel**, through
  `/api/v1/system/restore/*` with the `X-Setup-Secret` from `/opt/sailbox/.env`. The panel image
  is Alpine with `ca-certificates tzdata curl docker-cli docker-cli-compose` (`Dockerfile`), and
  both functions run `pg_dump`, `pg_restore` and `aws` as programs
  (`service/system_backup_service.go:205-227,401-498`), none of which is in it. Read from the
  Dockerfile, not run.
* **Self-upgrade with automatic rollback:** `upgrade-lib.sh` takes a dump of the panel's database
  (keeping the last five), pulls `latest`, restarts the panel, waits 120 seconds for `/healthz`, and
  on failure re-tags the previous image by digest, restores the dump and restarts again.

### Scaling and high availability

* Replicas, CPU and memory requests and limits, and a **Horizontal Pod Autoscaler** on CPU and
  memory percentages. Stop (zero replicas, autoscaler removed) and restart. No scale to zero.
* **Node pools:** an admin labels a server `sailbox/pool=<name>`, and an app with that pool gets a
  `nodeSelector` (`orchestrator/k3s/deploy.go:240-246`, `cluster.go:659-690`).
* **One control plane.** k3s is installed without `--cluster-init`, so the first server keeps its
  state in SQLite. The "server" role for an added node runs `k3s server --server <url>`, which
  k3s's documentation says needs a first server running embedded etcd; read from the code, not run.
  The panel and its PostgreSQL run in Docker on the first host only.
* **Project quotas:** CPU and memory limits, pods, claims and storage as a ResourceQuota per
  project namespace.

### Observability (logs, metrics, alerts, uptime)

* Live pod logs over a WebSocket, build logs streamed into the deployment, per-pod events, node
  installation logs.
* **Web terminal** (`/bin/sh`) into a running container for project writers.
* A collector every 30 seconds writes node and per-app CPU and memory to PostgreSQL, kept **7
  days**, and Kubernetes warning events, kept **30 days** (`service/metrics_collector.go:18-22`).
* **Alerts:** eight built-in rules with fixed thresholds — node CPU over 90%, node memory over
  90%, node not ready, crash loop, OOM kill, image pull error, unschedulable, and recent warning
  events (evictions aggregated into "pods evicted due to DiskPressure"); auto-resolve, a 30-minute
  cooldown, an alert history with manual resolve. No thresholds of one's own, no per-app rules, no
  disk percentage, no uptime checks, no log drains.

### Security, auth, roles, SSO, audit

* Email and password (bcrypt, cost 10), optional TOTP without recovery codes. The first person to
  open `http://<ip>:3000` registers and becomes the owner; registration then closes. No setup
  token.
* **Roles:** owner, admin, member for the installation; per project, members get admin or viewer.
* JWT access tokens (HS256, 15 minutes) and refresh tokens (7 days) kept in `localStorage`; the
  WebSocket and SSE routes take the access token as `?token=`, and the request logger writes the raw
  query string to the log (`api/middleware/logger.go:15-30`). Since v0.1.8 a token is checked
  against the account on every request.
* **No SSO, no API tokens, no audit log, no passkeys.**
* **Secrets at rest:** app variables and app "secrets" are plain JSONB in PostgreSQL (secrets are
  write-only in the API and become a Kubernetes Secret; k3s runs without secrets encryption). SSH
  passwords for servers are AES-GCM with a key derived from `SETUP_SECRET`; SSH private keys, Git
  tokens and S3 keys are plain JSON in `shared_resources`, redacted in API answers since v0.1.8.
* Per project, an optional NetworkPolicy (off by default) that admits ingress from the same
  namespace and `kube-system`; no egress rules, no pod security settings.

### Preview environments and branches

* None. One branch per app; the GitHub App asks for `pull_request` events "for future CI".

### Templates and catalogue

* None. A `templates` table, a list route and a model with Compose YAML and an environment schema
  exist, nothing fills the table and nothing installs from it. The README's comparison table marks
  one-click templates and Compose as absent.

### CLI, API, IaC, integrations

* A REST API under `/api/v1` (RFC 9457-style errors), reachable only with a signed-in user's
  short-lived JWT, since there are no API tokens; no published reference beyond the source.
* No CLI, no MCP server (issue #3, 2026-10-26, asks for one), no infrastructure-as-code format.
* Cluster pages: nodes and their metrics, every pod in every namespace, events, namespaces, claims,
  **Helm releases** (read from Helm's release Secrets) and **DaemonSets**, both read-only.
* A **clean-up page** with counts, then buttons: evicted pods, failed pods, completed pods, stale
  ReplicaSets, Jobs finished over an hour ago, orphaned Ingresses. After each successful deploy the
  app's Succeeded and Failed pods are deleted.

### Notifications

* Email (SMTP), Slack, Discord and Telegram channels, each with an on switch and a test button.
  Deploy succeeded, failed and cancelled, build timeout, backup succeeded and failed, member
  joined, database created and deleted, alert fired and resolved. Every channel gets every event;
  per-event filtering is marked "for future filtering UI" (`model/notification.go:42`).

### Multi-server and networking

* **Add Node** in the panel: SSH with a password or a stored key, host key trusted on first use,
  then `curl https://get.k3s.io | … K3S_TOKEN=… sh`, with the output streamed. The join token comes
  from the `k3s_token` setting, and in v0.1.8 **nothing can set it**: the installer does not, the
  settings form has no field, and the settings API accepts only `base_domain`, `panel_domain` and
  `https_email` (`model/setting.go:36-42`, `api/v1/setting_handler.go:61`). Every add then ends
  with "K3s token not configured. Set it in Settings → k3s_token". In v0.1.0–v0.1.7 the API wrote
  any key from any signed-in user, which is how it could be set. Read from the code, not run.
* Removing a node deletes its row; nothing drains or removes it from the cluster.
* Flannel uses WireGuard when the module is present on the first server, VXLAN otherwise; added
  "server" nodes are always told `wireguard-native`.
* **Topology:** a React Flow graph of servers, Deployments, pods, Services and Ingresses
  (`components/cluster-topology.tsx`), and per app a domain → ingress → service → pods strip.

### Team and collaboration

* One installation is one organisation. Invitations by email or link; project-level access for
  members. No comments, no approvals.

### Developer experience and onboarding

* `curl -sSL https://get.sailbox.dev | sudo sh`, a redirect to `install.sh` on `main` (not a tag);
  "< 2min", 2 CPUs and 2 GB of RAM. It installs Docker and k3s unpinned, opens the panel on plain
  HTTP at port 3000, and writes the k3s kubeconfig **world-readable**
  (`--write-kubeconfig-mode=644`). Upgrades follow `latest`.
* **Cmd+K** searches apps and databases by name and project. Dark and light themes.
* Docs: sailbox.dev/docs links to the README and says "A standalone documentation site … is coming
  soon".
* The site's comparison table says Coolify has no health probes and neither Coolify nor Dokploy
  has two-factor authentication; both have TOTP (`coolify.md`, `dokploy.md` in this directory).

## What users love

Independent discussion was not found. Hacker News has nothing about this product (Algolia,
2026-11-08); Reddit refused this sandbox; the web search tool had no credits left; GitHub search
finds no issue or pull request mentioning `sailboxhq` outside its own repository. What there is:

* **The thesis.** "really great initiative" (issue #3, 2026-10-26, from a Dokploy user). 17 stars
  and 9 forks in seven months.
* **An outside contributor** found and fixed the installer (PR #1, below).

## What users complain about

* **The installer did not work for five and a half months.** PR #1 (2026-04-25, `sandvale`):
  "The Sailbox installer still mounts `/var/lib/postgresql/data`, which causes `initdb` to fail
  and PostgreSQL never starts." The maintainer fixed it in v0.1.8 on 2026-09-13, 141 days later,
  and wrote in `upgrade-lib.sh`: "an install still on the old path has never had a working
  database — nothing to preserve." The installer had that path from the first commit.
* **No MCP** (issue #3, 2026-10-26): "Dokploy … recently released MCP capabilities, it really
  speeds up infra provision a lot!" Unanswered on 2026-11-08.
* **No Talos Linux** (issue #2, 2026-10-22), with a wish list: "observability, network
  connectivity (vpn?), storage (longhorn?/seaweedfs?), backup". Unanswered.
* What the code says users would hit next, none of it reported yet: a rollback that does not roll
  back, database backup schedules that never fire, panel backups that need programs the image does
  not have, a Nixpacks builder with no Nixpacks, a server that cannot be added, deploys marked
  successful before anything is ready, and cron commands split on spaces with no shell
  (`strings.Fields(cj.Command)`, `orchestrator/k3s/cronjob.go:65`), so `sh -c "a | b"` arrives as
  `["sh", "-c", "\"a", "|", "b\""]`. The cron "run history" records only manual runs
  (`service/cronjob_service.go:243-247`), and their "logs" are the line "Job created: <name>".

## Security record

**Where I looked (2026-11-08):** NVD keyword search "sailbox" (no results); OSV for the Go module
(none); the history from v0.1.0 to v0.1.8 in a clone; the repository has no `SECURITY.md` and no
security policy. GitHub's advisory pages could not be read from this sandbox. No CVE or advisory
was found.

* **For 169 days every signed-in user was an administrator, and could read every credential.**
  From v0.1.0 (2026-03-28) to v0.1.7, read from the tags:
  * `GET /api/v1/settings` returned every row, including `k3s_token` — "enough to take over the
    cluster", in v0.1.8's own comment — the GitHub App's private key and the SMTP password
    (`api/v1/setting_handler.go` at v0.1.7, lines 24-36), and `PUT /settings` wrote any key.
  * `GET /api/v1/resources` returned SSH private keys, Git tokens, S3 secret keys and Slack or
    Discord webhook URLs as stored (`resource_service.go` at v0.1.7, line 208).
  * Adding servers, editing Traefik, deleting any volume, cluster clean-up, notification and SMTP
    settings and panel backups had no role check (`server/router.go`, v0.1.7 to v0.1.8 diff).
  * `/deployments/:id`, `/cronjobs/:id`, `/domains/:id`, `/databases/:id` (credentials and restore
    included), deploys and domains under `/apps/:id`, and the log and terminal WebSockets checked
    the organisation, not the project, so a member made a viewer of one project could read every
    database's password and open a shell in any app.
  * v0.1.8 fixed all of it in a commit titled "feat: add resource limits update functionality for
    databases", whose message mentions none of it. There was no advisory and no note telling
    operators to rotate the k3s token or the credentials members could have read.
* **Still the case in v0.1.8:**
  * The **image registry is open to the internet**: `registry:2` with no authentication on
    NodePort 30500 on every server (`orchestrator/k3s/registry.go:15-30,142`), and the installer
    adds no firewall rule. Anyone who can reach a server can pull every app's image and push over
    it. Skifity closed the same hole with a host rule (ADR-0017, `installer/install.sh:1436-1452`).
  * **Database NodePorts** on every server with no address filter.
  * **First visitor becomes owner**, over plain HTTP on port 3000.
  * The panel container mounts the **Docker socket** and the k3s **admin kubeconfig**, which is
    world-readable on the host; it runs as root (the image creates a `sailbox` user and never
    switches to it).
  * Every member can list **every pod, event, namespace, claim, Helm release and DaemonSet** in the
    cluster and read Traefik's configuration (`server/router.go:255-271`, no role or project check).
  * Access tokens in WebSocket query strings, written to the log; WebSocket upgrades accept any
    Origin (`api/ws/logs.go:21`).
  * Plain-text app variables, Git tokens, SSH keys and S3 keys in PostgreSQL; unencrypted backups;
    build arguments on the Job.
  * Build images on `latest`, one of them (Kaniko) from an archived project; the installer and
    upgrades follow `main` and `latest`.
* **What was done well:** HMAC-checked GitHub webhooks; SSH host keys trusted on first use and
  then pinned; the unique index that makes two simultaneous deploys impossible; registration
  serialised with a lock; the setup secret required for restore; v0.1.8's redaction, which keeps a
  redacted value from overwriting the real one on save (`mergeRedactedConfig`).

## Against Skifity

Sailbox and Skifity make the same first bet (k3s, not Docker Compose) and the opposite second one:
Sailbox shows Kubernetes, Skifity hides it and keeps it under Advanced (`docs/faq.md:38`). Skifity
statuses come from `docs/progress.md`: the single-server path runs on a real k3s on every push to
`main`, and two servers in the `Two servers` workflow. Sailbox was read, not run.

| Capability | Sailbox | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git sources | GitHub App (manifest flow) or token, GitLab; signed GitHub webhooks; watch paths | GitHub, GitLab, Gitea, Bitbucket by token with signed webhooks and commit statuses (`internal/gitsrc`); watch paths (`internal/store/models.go:312-314`); folder upload; no GitHub App (`docs/configuration.md:78-80`) | Skifity ahead on hosts; **behind on connecting GitHub** |
| Builder | Kaniko `latest` (archived upstream), as root, `--insecure` | Rootless BuildKit per build, images pinned by digest (`internal/builder/job.go:32-33`, `internal/builder/images.go`) | Skifity ahead |
| Auto-detection | Nixpacks from an image without `nixpacks` | Railpack, Dockerfile, static; Nixpacks refused with `build.nixpacks_unavailable` (`internal/builder/job.go:222-225`, Phase 80) | Skifity ahead |
| Build without the cache | Per-app "no cache", Clear build cache | None: `force` skips image reuse, the build still imports the cache (`internal/deploy/deployer.go:185`, `internal/builder/job.go:877-878`); "Rebuild from scratch" strings unused (`web/src/locales/en.json:859-860`) | **behind** |
| Cancel a build | Yes | Yes (`internal/api/api.go:493`) | parity |
| Rollout | Marked success when the object is accepted | Waits for the rollout (`internal/deploy/deployer.go:491`) | Skifity ahead |
| Rollback | Restarts the current image, records the old one | Old image and settings, with a plan first (`internal/api/api.go:494-500`) | Skifity ahead |
| Rollout shape and drain | maxSurge, maxUnavailable, Recreate, grace per app | Fixed: rolling `maxSurge: 1`, Recreate with a volume, 30 s grace (`internal/kube/manifests.go:130,192-203`) | **behind** (open since `kamal.md`) |
| Compose and templates | None | Stacks from Compose (`internal/api/stack_handlers.go:66`), 374 templates with updates (`internal/templates/catalogue`) | Skifity ahead |
| **Domains, TLS and routing** | | | |
| Certificates | Traefik ACME TLS-ALPN, per domain | cert-manager, team certificates, DNS providers (`internal/api/api.go:382-391`) | Skifity ahead |
| Firewall, maintenance, redirects | None | `internal/edgerules`, `internal/guard`, `/maintenance`, domain redirects (`internal/api/api.go:476,486`) | Skifity ahead |
| Editing Traefik | Raw HelmChart YAML | None, by design | different |
| **Databases and services** | | | |
| Engines | 5, single instance | 9, PostgreSQL through CloudNativePG with several instances (`docs/databases.md:8-36`) | Skifity ahead |
| Reaching one from outside | Public NodePort, any address | No public address; `skifity db connect` through the panel (`internal/api/api.go:575`, `docs/databases.md:102-114`) | Skifity ahead |
| **Storage and backups** | | | |
| Database backups | Manual only (schedule stored, never run), plain, no retention | Scheduled, sealed with a passphrase, verified, missed-backup alerts (`internal/backup`, Phase 104, `internal/notify/notify.go:69`) | Skifity ahead |
| Volume backups | None | Per volume, with policy, verify and restore (`internal/api/api.go:545-550`) | Skifity ahead |
| Growing a volume | On deploy, and a cluster-wide Expand | No route (`internal/api/api.go:542-544`) | **behind** (open since `quickstack.md`) |
| Panel backup and restore | To S3, restore on a fresh panel in the UI — with binaries missing from the image | To the bucket, verified; restore with `skifity admin restore-db` (`internal/api/api.go:636`) | Skifity ahead on working; behind on restore steps |
| Panel upgrade | Health check, then automatic rollback of image and database | Snapshot first, and the rollback commands are handed to the operator (`internal/api/upgrade.go:25-36,100-120`) | **behind** |
| **Scaling and high availability** | | | |
| Instances | Replicas, HPA on CPU and memory | Same, plus scale to zero and a readiness checker (`internal/kube/manifests.go:458-468`, `internal/kube/scaletozero.go`, `internal/deploy/scaling.go`) | Skifity ahead |
| Choosing servers for an app | Node pools by label | GPU only (`internal/kube/gpu.go:383-511`); nothing else sets a selector | **behind** |
| Control plane | SQLite, one server; a second cannot join | Embedded etcd from the first install, promote (`installer/install.sh:1672`, `internal/api/api.go:429`) | Skifity ahead |
| **Observability** | | | |
| Usage history | 7 days, nodes and apps | 72 hours (`internal/watch/usage.go:18`) | behind (small) |
| Kubernetes events | Warning events kept 30 days | The last hour or so, live (`internal/cluster/events.go:1-2`) | **behind** |
| Alerts | 8 fixed rules, history, resolve | Thresholds per app and server, 5xx share, restarts (`internal/store/metrics.go:83-98`, `server_metrics.go:102-118`); what fires now, no history | Skifity ahead on rules; **behind on history** |
| Evicted and failed instances | Swept after deploys, clean-up page | Listed as instances until Kubernetes' own collector (`internal/kube/client.go:475-482`); nothing removes them | **behind** |
| Shell | Web terminal | None by decision (`docs/roadmap.md:149`); one-off commands | deliberate difference |
| Drift | `kubectl` changes silently replaced on the next deploy | Detected and offered back (`internal/watch/drift.go`, `internal/api/api.go:556-561`) | Skifity ahead |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | bcrypt, TOTP without recovery codes, first visitor is owner | Argon2id, TOTP with recovery codes, passkeys, OIDC, setup token (`internal/auth`, `internal/api/setup.go:52`) | Skifity ahead |
| API credentials | None | Scoped, expiring tokens (`internal/api/api.go:296-316`) | Skifity ahead |
| Secrets at rest | Mostly plain | Sealed per row, write-only (`internal/crypto`) | Skifity ahead |
| Audit | None | Team audit log | Skifity ahead |
| Isolation | Optional ingress policy, no pod security | Default-deny policies, restricted pod security by default (`internal/kube/namespace.go:52-69,147-223`) | Skifity ahead |
| Registry | Open NodePort | NodePort closed to all but the node, writes need a build's token (`internal/registryguard`, ADR-0017) | Skifity ahead |
| **Preview environments** | None | Per pull request, seeded (Phase 107) | Skifity ahead |
| **CLI, API, IaC, integrations** | REST with a session JWT only | CLI, API, 47-tool MCP, `skifity.yaml` (`internal/cli`, `internal/mcpserver`, `internal/blueprint`) | Skifity ahead |
| Scheduled jobs | Any image, time zone, manual trigger; history of manual runs only | In the app's image, UTC (`internal/kube/runjob.go:274`), run now with output and exit code (`internal/api/deploy_handlers.go:505-541`); no past runs shown, no failure notice | **behind** on history and failures |
| **Notifications** | 4 channels, every event to every channel | 10 channels (`internal/notify/provider.go:88-90`), events per channel | Skifity ahead |
| **Multi-server and networking** | | | |
| Adding and removing servers | SSH and a join token nothing can set; removal deletes a row | SSH from the panel, drain on removal, Hetzner (`internal/provision`) | Skifity ahead |
| What runs on a server | Topology graph | Instance count on the server page (`web/src/pages/server-detail.tsx:380`); the server of each instance on the app page (`web/src/pages/app-detail.tsx:536-561`) | behind (small) |
| **Team and collaboration** | One organisation, project access | Teams, roles, project limits | Skifity ahead |
| **Developer experience** | | | |
| Search | Cmd+K over apps and databases | Command palette over apps and pages, not databases (`web/src/components/command-palette.tsx:138-140`) | behind (small) |
| Languages | English panel | Five, with a build gate (`web/src/locales`) | Skifity ahead |
| Errors | Strings, some pointing at a setting that does not exist | Cause, impact, fix (`internal/errdoc`) | Skifity ahead |
| Maturity | v0.1.8, one author, 17 stars, quiet since September | v0.1.x, released; cluster workflows on every push | comparable age; Skifity tested |

### Claims in this repository that no longer hold

* `docs/research/competitors.md:159-163`, "Where Skifity is actually different", point 1: "None of
  them puts Kubernetes on one small server with one command." `quickstack.md` already said
  QuickStack does; Sailbox has since its first release (2026-03-28) — `curl | sh` on 2 CPUs and
  2 GB. What remains Skifity's is narrower: embedded etcd from the first install, servers added and
  removed over SSH that actually join, and a panel that runs inside the cluster it manages.

## Gaps worth closing in Skifity

### P1 — Scheduled commands: show their runs and say when one fails

* **What:** under each scheduled command, its last runs — when, how long, exit code, the output
  through the existing run-logs reader — and a `job.failed` notification when a scheduled run ends
  with a non-zero exit or hits its deadline. A failed run shows on the app page until a later run
  succeeds.
* **Evidence:** Sailbox advertises "Manual trigger, run history, real-time logs" (its own history
  holds only manual runs). Skifity keeps three successful and three failed Jobs per CronJob
  precisely so they can be read (`internal/kube/runjob.go:258-272`), and nothing reads them:
  `handleListAppJobs` returns the schedule rows only (`internal/api/deploy_handlers.go:611-623`),
  the console tab lists commands with "Run now" and no past runs
  (`web/src/components/app/console-tab.tsx:272-300`), and no event exists for a failed one
  (`internal/notify/notify.go:57-92`). `coolify.md` (gap 5) and `render.md` (P1) asked for the
  failure notice; it is still open.
* **Fit:** list Jobs labelled for the CronJob in `internal/kube`; `GET
  /api/apps/{app}/jobs/{job}/runs` answering with names that `/runs/{run}/logs` already reads; the
  minute tick in `internal/watch` remembers the newest finished Job per command and raises
  `job.failed` once; a row list in the console tab; an errdoc entry for "the output is gone"
  (Kubernetes collected the pod).
* **Size:** M. **Without a cluster:** yes for the API, the watcher and the page with a fake
  clientset; a CronJob actually firing is one more step for `test/cluster/verify.sh`.

### P1 — A build that ignores the cache, when asked

* **What:** "Rebuild without cache" on the deploy menu, `skifity deploy --no-cache`, and
  `no_cache` on the deploy request: the build runs without `--import-cache` (still exporting, so
  the next build is warm again), recorded on the deployment.
* **Evidence:** Sailbox has a per-app "no cache" and "Clear build cache"; Coolify, Dokploy and
  Railway have the same button, because a poisoned or stale layer (`apt-get update` cached last
  month, a broken dependency layer) is otherwise impossible to get past. Skifity's `force` only
  skips reusing an image (`internal/deploy/deployer.go:185`); the BuildKit command always imports
  the cache (`internal/builder/job.go:877-878`). The panel's own strings promise the feature —
  "Rebuild from scratch", "Ignores the cache and builds again even if nothing changed"
  (`web/src/locales/en.json:859-860`) — and no component uses them.
* **Fit:** a `NoCache` field from the deploy request through `internal/deploy` to the build spec in
  `internal/builder`; the DeployButton gains a second item; the CLI flag beside `--force`
  (`internal/cli/commands.go:699`). It must not change the build fingerprint (ADR-0007).
* **Size:** S. **Without a cluster:** yes for the rendered command and the API; that the layers are
  rebuilt needs the `Cluster` workflow.

### P2 — Keep what happened overnight

* **What:** Kubernetes warning events for each app and server, and every alert that fired with when
  it started and ended, kept for seven days and shown on the app's and server's pages, so "it
  restarted at 3 a.m." can be answered at 9.
* **Evidence:** Sailbox keeps warning events 30 days and an alert history with resolve
  (`service/metrics_collector.go:18-22`). Skifity's events are "what Kubernetes said about an app's
  objects in the last hour or so" (`internal/cluster/events.go:1-2`), and alerts are only what is
  firing now (`internal/store/metrics.go:96-97`); the notification channel is the only record.
* **Fit:** the minute tick already reads each app; store scrubbed warning events and alert
  transitions in two tables with a prune like `PruneAppSamples`; the Events card reads stored and
  live together. Messages pass through the same scrubbing `AppEvents` uses.
* **Size:** M. **Without a cluster:** yes.

### P2 — Sweep evicted and failed instances

* **What:** the watcher deletes an app's pods that were evicted or failed and have a newer ready
  replacement, after the app page has had a chance to show why (say, an hour), and the servers'
  page says how many it swept.
* **Evidence:** Sailbox deletes an app's finished pods after each deploy and has a clean-up page.
  Skifity lists every pod as an instance (`internal/kube/client.go:475-482`) and explains
  "evicted" (`internal/kube/explain.go:272,346`), but nothing removes them; Kubernetes collects
  terminated pods only past 12,500 by default, and nothing in `installer/` or `internal/provision`
  lowers that. After a disk-pressure night an app shows a list of dead instances for weeks.
* **Fit:** `internal/watch`, restricted to Skifity-managed namespaces; the latest one kept so the
  explanation survives; audit nothing (it is housekeeping). **Size:** S. **Without a cluster:** yes
  with a fake clientset; an eviction itself needs a cluster.

### P2 — Run an app on the servers you choose

* **What:** a group name on a server ("edge", "big-disk", "eu"), and on the app's Scaling tab "Run
  on: any server / group X". Required node affinity on the group label; refused with an errdoc entry
  when no ready server is in the group; the same choice for where builds run.
* **Evidence:** Sailbox's node pools; QuickStack #31 asked for it; `dokploy.md` gap 21 asks for
  build placement. Skifity sets a selector only for GPUs (`internal/kube/gpu.go:383-511`), so a
  database-heavy app and a public web app share whichever server the scheduler picks.
* **Fit:** a column on servers, a label applied over the API like the GPU label
  (`internal/api/gpu_handlers.go:399-403`), the affinity in `internal/kube/manifests.go`, the
  scaling readiness checker warning when a volume pins the app elsewhere. **Size:** M. **Without a
  cluster:** manifests and API yes; placement needs the `Two servers` workflow.

### P2 — A panel upgrade that undoes itself

* **What:** when the new panel is not ready within a few minutes, the upgrade puts the previous
  image and the database snapshot back on its own, and the next start says so.
* **Evidence:** Sailbox's `upgrade-lib.sh` health-checks for 120 seconds, then re-tags the previous
  image by digest and restores the pre-upgrade dump. Skifity takes the snapshot and hands the
  operator the commands (`internal/api/upgrade.go:25-36,100-120`), which is the right information
  for a person who is still there and no help to one who pressed Upgrade and closed the tab.
* **Fit:** a Job started before the image changes, with permission to patch only the panel's
  Deployment, which waits for readiness and on failure scales the panel to zero, copies the
  snapshot back on the same volume and restores the image (Recreate already guarantees one copy).
  **Size:** M. **Without a cluster:** the Job's logic yes; the rollback needs the `Cluster` workflow.

### P2 — Rollout shape and drain per app, and growing a volume

* Both already proposed and still open: a per-app shutdown grace and rollout shape (`kamal.md`, P2;
  `internal/kube/manifests.go:130` is still a fixed 30 s) and a larger size for an app volume
  (`quickstack.md`, P2; still no route, `internal/api/api.go:542-544`). Sailbox has both, which is
  a third and fourth competitor with them. **Size:** S each.

### P3 — What runs on a server

* **What:** on the server page, the instances on it with their app and status, and the same list in
  the removal dialog ("these will move").
* **Evidence:** Sailbox's topology graph answers it visually. Skifity shows a count
  (`web/src/pages/server-detail.tsx:380`) and, from the other side, the server of each instance on
  an app's page. **Size:** S. **Without a cluster:** yes.

### P3 — Databases in the command palette

* **What:** every database, and every server, as palette items beside apps.
* **Evidence:** Sailbox's Cmd+K merges apps and databases; Skifity's palette offers the Databases
  page only (`web/src/components/command-palette.tsx:138-140`). **Size:** S. **Without a cluster:**
  yes.

### P3 — A schedule in a time zone

* **What:** an optional time zone on scheduled commands (and backup schedules), shown next to the
  next run time.
* **Evidence:** Sailbox stores one per cron job and passes it to the CronJob. Skifity leaves
  `timeZone` unset on purpose so that a schedule means UTC (`internal/kube/runjob.go:274`), which is
  right as a default and awkward for "every weekday at 9" in a country with summer time.
* **Fit:** CronJob `timeZone` for commands; the panel's own cron for backups. **Size:** S.
  **Without a cluster:** yes.

### P3 — Connect GitHub as an app, and restore the panel at setup

* Already proposed and still open: a GitHub App (`dokploy.md`, `hakopod.md`) and restoring a panel
  backup from the first-run screen (`quickstack.md`, P3). Sailbox has both — its restore flow is
  sound in design even though its image lacks the programs it runs.

## Things to deliberately not copy

* **Showing Kubernetes as the product.** Helm release and DaemonSet lists, every pod in every
  namespace for every member, and a raw Traefik YAML editor whose edits k3s can put back. Skifity's
  Advanced view per app is enough for the person who needs it.
* **Security fixes inside feature commits.** v0.1.8 closed credential exposure and missing
  authorization to every member under a title about database limits, with no advisory and no advice
  to rotate. Skifity's progress log names its fixes; keep doing so.
* **A registry and databases on open NodePorts.** ADR-0017 and `skifity db connect` are the
  answers; do not regress for convenience.
* **First visitor becomes owner**, over HTTP. Skifity's setup token stays.
* **A panel outside the cluster with the Docker socket and a world-readable admin kubeconfig.**
  Two container runtimes on one host, and root on the host for anything that reaches the panel.
* **Archived and floating build images.** Kaniko on `latest`, Nixpacks from an image without it;
  Skifity pins by digest and refuses a builder it cannot run.
* **Features that are only a form.** A backup schedule nothing runs, a run history of manual runs,
  a rollback that restarts, an "Add Node" whose token cannot be set. Skifity's rule that nothing is
  called done until it ran is the lesson.
* **"Success" when the object is accepted.** Skifity waits for the rollout; keep it.
* **An attribution clause enforced in code.** A visible "Powered by" required by licence and
  checked by a script makes white-labelling a sale and a fork a negotiation; Skifity is Apache-2.0
  (`LICENSE`) and needs no such clause.
* **A comparison table that is wrong about competitors.** It costs credibility with exactly the
  people who have used them.
* **A web terminal.** Skifity's decision stands (`docs/roadmap.md:149`).

## Sources

All read on 2026-11-08.

* https://github.com/sailboxhq/sailbox — cloned (`main` at `ad01f84`, all 15 commits, tags v0.1.0
  to v0.1.8); metadata through the GitHub search API (stars, forks, open issues, licence, created
  and pushed dates). The repository API and the release pages were refused from this sandbox.
* GitHub search API: issues #2 and #3 and pull request #1 in `sailboxhq/sailbox`, bodies read
  (comments could not be read); issues, pull requests and code elsewhere mentioning `sailboxhq`
  (none outside the repository)
* Files read in the clone: `README.md`, `README_CN.md`, `NOTICE`, `CONTRIBUTING.md`,
  `.github/INSTRUCTIONS.md`, `.github/workflows/ci.yml` and `release.yml`, `install.sh`,
  `upgrade.sh`, `Dockerfile`, `docker-compose.yml`, `deploy/entrypoint.sh`, `deploy/Caddyfile`,
  `deploy/upgrade-lib.sh`; under `apps/api`: `go.mod`, `cmd/server/main.go`, `cmd/worker/main.go`,
  `internal/server/router.go`, `internal/api/middleware/*.go`, `internal/api/ws/logs.go` and
  `terminal.go`, `internal/api/v1/guard.go`, `setting_handler.go`, `webhook_handler.go`,
  `cluster_handler.go`, `database_handler.go`, `internal/auth/jwt.go`, `internal/config/config.go`,
  `internal/model/*.go`, `internal/orchestrator/k3s/build.go`, `deploy.go`, `cronjob.go`,
  `database.go`, `cleanup.go`, `cluster.go`, `helm.go`, `daemonset.go`, `ingress.go`,
  `networkpolicy.go`, `quota.go`, `registry.go`, `serviceaccount.go`, `traefik.go`,
  `internal/service/deploy_service.go`, `cronjob_service.go`, `database_service.go`,
  `node_service.go`, `auth_service.go`, `alert_evaluator.go`, `metrics_collector.go`,
  `system_backup_service.go`, `version_service.go`, `project_service.go`, `domain_service.go`,
  `template_service.go`; under `apps/web/src`: `components/brand-guard.tsx`, `app-search.tsx`,
  `cluster-topology.tsx`, the routes' file list and `_components/-topology-tab.tsx`
* `git diff v0.1.7 v0.1.8` of `router.go`, and `git show v0.1.7:` of `setting_handler.go`,
  `model/setting.go`, `model/shared_resource.go`, `resource_service.go`, `ws/terminal.go`,
  `auth_service.go`
* https://sailbox.dev/, https://sailbox.dev/docs, https://sailbox.dev/compare/coolify,
  https://sailbox.dev/sponsorships
* https://get.sailbox.dev (a 302 to `raw.githubusercontent.com/sailboxhq/sailbox/main/install.sh`,
  identical to the clone's)
* https://ghcr.io/v2/sailboxhq/sailbox/tags/list
* https://ghcr.io/v2/railwayapp/nixpacks/manifests/latest and the linux/amd64 image's config (layer
  history, created 2026-10-20)
* https://raw.githubusercontent.com/GoogleContainerTools/kaniko/main/README.md ("This project is
  archived"), and the GitHub search API for `GoogleContainerTools/kaniko` (archived, last push
  2025-06-03), `chainguard-forks/kaniko` and `osscontainertools/kaniko`
* https://raw.githubusercontent.com/railwayapp/nixpacks/main/README.md ("Maintenance Mode")
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=sailbox (no results)
* https://api.osv.dev/v1/query for `github.com/sailboxhq/sailbox/apps/api` (no results)
* https://hn.algolia.com/api/v1/search?query=sailbox, with and without typo tolerance and restricted
  to URLs (nothing about this product)
* https://www.reddit.com/search.json?q=sailbox (refused, 403). The web search tool was not
  available: its credits had run out.
* Skifity, at `29147f1`: `docs/progress.md` (Phases 80, 95, 99, 104, 107), `docs/decisions.md`
  (ADR-0007, ADR-0017), `docs/faq.md`, `docs/roadmap.md`, `docs/databases.md`,
  `docs/configuration.md`, `docs/research/competitors.md` and the per-product files named above,
  and the code files named in the table
