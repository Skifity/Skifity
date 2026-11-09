# Fly.io

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail. Brought up to date on
> 2026-11-08: facts about Fly.io that changed are corrected in place, and "Since 2026-09-30"
> says what changed, with its sources.

Fly.io is a developer cloud that turns a container image into Firecracker
microVMs ("Machines") on its own bare-metal servers, puts an anycast proxy
(`fly-proxy`) in front of them, and joins every Machine in an organisation into
one private IPv6 WireGuard mesh ("6PN"). It is for developers who want to run
full, long-lived processes close to their users from a CLI, without owning
servers or learning Kubernetes. The platform is proprietary; the public parts
are `flyctl` (Go, Apache-2.0, 1,715 stars, 242 open issues, pushed 2026-09-29)
and infrastructure pieces such as Corrosion, the gossip-replicated SQLite store
Fly now uses for service-discovery state (Rust, 1,859 stars). Its API side runs
Sidekiq jobs, and its own incident log names Consul, Corrosion, Vault and a
Redis among the systems behind it. Pricing is pay-as-you-go with no free tier
for new organisations: a trial of $1 of credit for 7 days (by 2026-11-04; it
was 2 VM-hours or 7 days on 2026-09-30), then per-second billing (a
`shared-cpu-1x` with 256 MB is $2.19 per 30 days in Ashburn since Machine prices
rose on 2026-10-01; it was about $1.90),
$0.15/GB-month for volumes, $0.08/GB-month for snapshots beyond 10 GB (billed
since 2026-01-01), $2/month for a dedicated IPv4 and Managed Postgres from
$38/month. It is mature (the `flyctl` repository dates from July 2019; releases
ship almost daily, v0.4.101 to v0.4.110 between 8 and 29 September 2026) and
well funded ($134M raised, most recently a $23.2M Series D on 2026-07-07 at a
reported $475M post-money, per Forge Global). On 2026-07-24 its founder handed
the chief executive's job to Scott Johnston, formerly Docker's, and made
Sprites, "computers for agents", the company's focus (missed here on
2026-09-30). No official customer count was found; revenue figures online come
from estimation sites and are not repeated here. In September 2025 it cut its
regions from 35 to 18, and in late September 2026 it retired Mumbai, leaving 17;
in February 2026 it announced its GPU Machines end on 2026-07-31. (All figures
read 2026-09-30 unless marked; sources at the end.)

## Feature inventory

### Deploy sources and builds

* `fly launch` scans a directory, recognises the framework, writes a Dockerfile
  and a `fly.toml`, and creates the app; `fly deploy` builds and ships it. The
  build runs on a remote builder (Depot-backed Fly builders; a June 2026 incident
  was literally "Deploys defaulting to Fly-hosted Builders") or on local Docker.
* `[build]` in `fly.toml` accepts a Dockerfile, a multi-stage target, build
  args, a Cloud Native Buildpacks builder, or a prebuilt `image`.
* Git: since 2024-10-17 an app connected through the dashboard's Launch UI can
  auto-deploy on push to a chosen GitHub branch, built on Fly's builders; the
  early feedback was about missing build logs in that flow. GitHub only; for
  anything else the documented path is CI (GitHub Actions) running `flyctl`
  with a deploy token.
* `[deploy] release_command` runs a one-off task, typically a migration, in a
  temporary Machine "before any of your deployed Machines are created".
* Four strategies: `rolling` (default, with `max_unavailable`), `immediate`,
  `canary` (one Machine first, then rolling) and `bluegreen` (boot a full new
  set, switch traffic when all pass checks). Canary and bluegreen **cannot be
  used with volumes**.
* `machine_checks` boot a throwaway Machine from the new image, expose
  `FLY_TEST_MACHINE_IP`, run any command against it, and halt the deploy if it
  fails, before a single production Machine is touched.
* **Rollback is a redeploy of an old image** (`fly releases --image`, then
  `fly deploy --image …`). The docs are explicit: "Rollbacks don't undo config
  changes … Fly still uses your current `fly.toml`, along with whatever env vars
  and secrets are set now."

### Domains, TLS and routing

* Every app gets `<app>.fly.dev`. Custom domains via `fly certs add`, with
  Let's Encrypt; the first 10 single-hostname certificates per organisation are
  free, then $0.10/month each, and $1/month for a wildcard.
* Shared anycast IPv4 and IPv6 by default; a dedicated IPv4 is $2/month; static
  egress IPs are $0.005/hour (about $3.60/month).
* `[http_service]` for the common case (80/443, `force_https`), and
  `[[services]]` with `[[services.ports]]` and handlers for anything else,
  including raw TCP and UDP, TLS passthrough and PROXY protocol.
* `[[statics]]` serves files from the image or a Tigris bucket at the edge.
* Health checks in `http_service.checks` gate routing; top-level `[checks]`
  are observability only and never remove a Machine from rotation.

### Databases and services

* **Managed Postgres (MPG)**, announced in the January 2025 newsletter and live
  during 2025: Basic (shared-2x, 1 GB) $38/month, Starter $72, Launch
  (performance-2x, 8 GB) $282, Scale $962, Performance (performance-8x, 64 GB)
  $1,922; storage $0.28/GB-month, 1 TB maximum. HA with automatic failover,
  backups, PgBouncer on every plan, pgvector and PostGIS. The docs list what is
  still missing: version upgrades, customer-facing alerting, migration tooling
  and other extensions. That list is unchanged on 2026-11-08; since 2026-09-30 a
  cluster can be forked into a new one (`fly mpg fork`) and a user's password
  rotated from the CLI, and storage is billed on what is used (since
  2026-09-25).
* **The history matters.** Before MPG, "Fly Postgres" was a template that
  created a Postgres cluster *you* then operated; the page is still titled
  "This Is Not Managed Postgres". In April 2025 staff confirmed those templates
  and the `fly postgres` commands would be deprecated in favour of MPG. The
  Supabase-run "Fly Postgres managed by Supabase" was deprecated on 2025-04-11.
* Partner extensions: Upstash Redis, Tigris object storage (both billed through
  Fly).

### Storage and backups

* Volumes are a slice of one NVMe drive on one physical server: "A Machine can
  only mount one volume at a time and a volume can be attached to only one
  Machine." There is **no replication**; the docs tell you to "always provision
  at least two volumes per app" and to replicate in your application.
* Daily snapshots with 5-day default retention (1-60 configurable), but "the
  snapshots shouldn't be your primary backup method". Volumes can be forked,
  restored from a snapshot into a new volume, extended (never shrunk), up to
  500 GB, with `auto_extend_size_threshold` to grow automatically. Encrypted by
  default.
* Snapshots became billable on 2026-01-01 ($0.08/GB-month after 10 GB free);
  Fly said 98% of customers would see no change.

### Scaling and high availability

* `fly scale count N --region X` and `fly scale vm` set Machine count and size
  per region; Machines start in "well under a second" once created.
* **Autostop/autostart** is done by `fly-proxy`, not by a metrics autoscaler:
  per-Machine concurrency `soft_limit` decides whether a region has excess
  capacity; the stop loop "stops at most one Machine per region per pass";
  `min_machines_running` keeps a floor in the primary region only.
  `auto_stop_machines = "suspend"` snapshots memory and resumes in "a few
  hundred ms" instead of the "~2+ seconds" of a cold start, but only for
  Machines with at most 2 GB, no swap and no schedule, and a new deploy
  discards the snapshot.
* Metric-driven scaling is a separate app you deploy yourself
  (`fly-autoscaler`, 54 stars), reconciling every 15 seconds from Prometheus
  or Temporal and creating/destroying or starting/stopping Machines.
* Placement "can fail" for lack of capacity on a host; the recurring forum
  error is "could not reserve resource for machine: insufficient memory",
  reported again in January, February and March 2026. A Machine with a volume
  cannot simply move to another host.

### Observability (logs, metrics, alerts, uptime)

* Managed Prometheus (VictoriaMetrics) and Grafana at `fly-metrics.net`, free
  for now, about 15 days of retention; apps expose custom metrics through a
  `[metrics]` block, scraped every 15 seconds.
* Logs: `fly logs` tails, and logs are kept in a VictoriaLogs index for about 7
  days; shipping them anywhere else means deploying `fly-log-shipper`, which
  reads the organisation's NATS stream.
* **No built-in alerting**: "users must implement it themselves through
  Grafana alert rules or run their own Prometheus with Alertmanager."
* Public status page plus a detailed engineering **infra log**
  (fly.io/infra-log) with root causes. It paused after May 2025 and resumed on
  2026-03-02; it has entries on 70 separate days between then and 2026-09-15.

### Security, auth, roles, SSO, audit

* Tenants never share a kernel: every Machine is a Firecracker microVM.
  SOC 2 Type 2; critical vulnerabilities patched within 24 hours.
* Two organisation roles, Member and Admin; Members can deploy, manage
  secrets and volumes, but not billing or membership.
* Organisation SSO is limited to Google (a Workspace hosted domain) or GitHub
  (members of a GitHub organisation).
* Tokens are macaroons with attenuable caveats: org-wide, per-app deploy
  tokens, read-only "auditor" tokens, and tokens restricted to running one
  command on one app's Machines.
* Secrets are write-only: "The API servers can only encrypt; they cannot
  decrypt secret values." `fly secrets list` shows names and a **digest** per
  version; `fly secrets set` restarts Machines unless `--stage` defers it to the
  next deploy; secrets can also be mounted as files.

### Preview environments and branches

* Not built in. The documented path is the `superfly/fly-pr-review-apps`
  GitHub Action (206 stars), which creates one app per pull request at
  `pr-<n>-<owner>-<repo>.fly.dev` and destroys it on close. Databases and
  secrets for the preview are the user's problem.

### Templates and catalogue

* No one-click app catalogue. `fly launch` framework scanners (Rails, Django,
  Phoenix, Laravel and others) and documentation "blueprints" play that role.

### CLI, API, IaC, integrations

* `flyctl` covers the whole platform: apps, Machines, volumes, secrets, certs,
  IPs, WireGuard, tokens, Postgres, logs, SSH (`fly ssh console`), one-off
  consoles (`fly console`), SFTP and local tunnels (`fly proxy`).
* The **Machines API** is a plain REST API for creating, starting, stopping,
  leasing and destroying VMs; many products (including Sprites) are built on it.
* `fly.toml` is the per-app declarative file; there is no whole-organisation
  IaC. The Terraform provider was archived on 2024-03-01 and Fly's advice is
  to use `flyctl` or the Machines API.
* `fly mcp server` exposes `flyctl` to Claude, Cursor, VS Code, Zed and others
  as an MCP server; `fly mcp launch` deploys MCP servers. Since 2026-10-23 there
  is also a hosted one, `mcp.fly.io` (beta), signed into in the browser and
  limited to the apps chosen there, and since 2026-10-30 `fly skills` and
  `fly setup agent` install Fly's agent skills and that server into coding
  agents; see "Since 2026-09-30".
* **Sprites** (January 2026): persistent, checkpointable sandbox VMs for coding
  agents, idle after 30 seconds, MCP endpoint added March 2026.

### Notifications

* Email for billing and platform notices; the status page. No deploy-failed or
  app-down notifications to Slack/Discord/webhooks were found in the docs.

### Multi-server and networking

* 17 regions (7 North America, 5 Europe, 3 Asia-Pacific, 1 South America, 1
  Africa). There were 18 after the September 2025 consolidation; Mumbai (`bom`)
  was then retired and its customers told, and the docs dropped it on
  2026-09-25, so this line was one region out on 2026-09-30.
* Fly Kubernetes (FKS), a Kubernetes API over Machines that this file did not
  mention, was deprecated on 2026-10-15.
* 6PN: every Machine in an organisation on one WireGuard IPv6 mesh, `.internal`
  DNS (`app.internal`, `iad.app.internal`), Flycast for private load-balanced
  addresses, custom private networks for tenant separation, and
  `fly wireguard create` to put a laptop on the network.

### Team and collaboration

* Organisations with Member/Admin, invitations, SSO enforcement, billing per
  organisation. The dashboard is secondary to the CLI.

### Developer experience and onboarding

* One command from a directory to a running app is the product's signature.
* Trial (by 2026-11-04): $1 of credit for Machines, Sprites and Managed
  Postgres, for 7 days or until it is spent, with no card; up to 10 Machines,
  stopped ones included; trial Machines stop after 5 minutes; nothing starts
  after the credit is gone without a card. On 2026-09-30 it was 2 VM-hours or 7
  days, 10 Machines and 20 GB of volumes.
* Documentation is extensive and blunt about limits, as the quotes above show.

## What users love

* **The CLI and the one-command start.** HN threads going back to 2022 call Fly
  "the reclaimer of Heroku's magic"; the recurring praise is that `fly launch`
  then `fly deploy` works and that low-level primitives sit under a friendly
  surface (HN 31390506, 32951363).
* **Real VMs, not containers-with-caveats**, starting in under a second, and
  cheap enough to run small things "virtually for free" (Trustpilot, June 2025;
  "zero friction migrating our containerized workloads", June 2026).
* **Private networking that just works between apps**, and WireGuard to reach
  it from a laptop.
* **Candour.** The infra log and the "We were wrong about GPUs" post are
  praised even by critics; a December 2025 forum reply noted "Big Red Box days
  are mostly in the past now" and credited better certificate handling and docs.
* **Sprites** were received as solving agent sandboxing well (Simon Willison,
  2026-01-09).

## What users complain about

* **Reliability, over years, not once.** A 2023 Sydney host failure left
  single-node Postgres clusters down for 12+ hours with a green status page
  (HN 36808296, July 2023). "fly.io has a very bad reputation for reliability"
  (HN 42248279, November 2024). A December 2025 forum thread asked whether it
  had improved, citing ~25-30 status incidents a month. StatusGator counted 5
  major incidents in the 30 days to 2026-09-30, including a 2h40m state-database
  problem in GRU and a 7h25m partial Sprites outage. Fly's own infra log has
  written-up incidents on 70 days between 2026-03-02 and 2026-09-15, and for
  April-September 2026 attributes incidents to Corrosion schema mismatches,
  missing Consul rows, stale 6PN mappings, disk corruption in its secrets
  database, Vault failures that blocked certificate lookups, and a DNS cache
  that hung.
* **Postgres.** First "this is not managed", then a Supabase partnership that
  ended in April 2025, then MPG, which has had its own outages: Frankfurt on
  2026-05-16, an IAD control-plane incident on 2026-08-01, and a forum thread
  titled "URGENT production outage: all MPG clusters in ord unavailable"
  (August 2026), plus "Apparently MPG is not really 'high availability'".
* **Volumes pinned to one host.** Capacity errors ("could not reserve resource
  for machine") recur in 2026 threads, and the fix is to move the volume,
  which is a fork and a new Machine.
* **Not declarative enough, and coarse permissions.** A team that moved to GKE
  wrote "you still have to run quite a few commands to get your app to the state
  you want" and "you can't invite a read-only user (check logs, metrics) or even
  a billing user" (rdrn.me, August 2024).
* **Things taken away or metered.** 17 regions deprecated (September 2025; HN
  45380634: "it seems a bit sad"), GPUs deprecated after a blog post said they
  were not going away (forum, February 2026), snapshot billing from January
  2026, cross-region private transfer to MPG metered from February 2026.
* **Billing surprises** appear in Trustpilot reviews (2.8/5, but only 20
  reviews; e.g. September 2025, April 2026). The sample is small; treat it as
  a signal, not a finding.

## Security record

* **2026-10-16 to 17 — the whole fleet rebooted for a kernel bug** (PwnEscape,
  CVE-2026-99999), by which a guest could bypass the host's firewall and read
  32 bytes of host memory at a time, possibly a neighbouring VM's packets. Fly
  found no evidence of exposure and asked customers to rotate anything sent
  unencrypted between 15 and 17 October. "Since 2026-09-30" has the detail.
* **2026-11-04 — a Firecracker escape** (CVE-2026-122192, CVSS 7.5): mitigated
  across the fleet the same day, with Machine restarts to follow.
* **No CVE or GitHub security advisory for `flyctl`**: the repository's
  advisories page says "There aren't any published security advisories"
  (checked 2026-09-30). NVD searches for Fly.io and flyctl returned no
  platform CVEs.
* No public customer-data breach disclosure was found in searches of the web,
  HN and Fly's own security pages. Fly publishes SOC 2 Type 2, a 24-hour
  critical-patch commitment, and no bug bounty.
* The documented residual risk is honest and applies to everyone: "People with
  deploy access **can** deploy code that reads secret values and prints them."

## Against Skifity

Skifity's status words are from `docs/checklist.md`: **Works** has a test that
runs; **Written** means code and unit tests exist but it has never run on a
real cluster (ADR-0010).

The Fly.io column was brought up to 2026-11-08. The Skifity column is as read on
2026-09-30, except the rollback row, which overstated the code that day and is
corrected; "Since 2026-09-30" lists the rows it no longer describes.

| Capability | Fly.io | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy sources and builds | Local dir, image, buildpacks, Dockerfile; remote builders; push-to-deploy from GitHub only (Launch UI) or CI | Git (GitHub/GitLab/Gitea webhooks, `internal/gitsrc`), image, folder upload (`skifity up`, `internal/cli/up.go`, `PUT /api/apps/{app}/source`); Railpack/Nixpacks/Dockerfile in an in-cluster Job (`internal/builder`). Written | Parity |
| Pre-traffic checks and strategies | release_command, machine_checks, rolling/immediate/canary/bluegreen | `ReleaseCommand` (`internal/store/models.go`, run by `internal/deploy/run.go:130`), readiness/liveness/startup probes (`internal/kube/manifests.go:60-64`), rolling with `maxUnavailable: 0`, Recreate with a volume. No canary, blue-green or throwaway-instance check. Written | Behind |
| Rollback | Redeploys an old image; config and secrets **not** restored | New deployment carrying the old image **and** its runtime settings (instances, autoscaling, resources, port, health check, start command); variables, domains and disks stay as they are, which Phase 94 found this cell's earlier "the settings it ran with" overstated; refuses when the image is gone (`docs/checklist.md` item 8). Written | Skifity ahead |
| Domains, TLS, routing | Anycast edge, wildcard certs, TCP/UDP services, dedicated IPs, statics at edge | cert-manager on first domain, `sslip.io` HTTP address (ADR-0015), path on a domain (`store.Domain.Path`), DNS target shown (`handleListDomains`), Cloudflare tunnel component; an app is reachable from outside only over HTTP(S) through its Ingress (`BuildIngress`, `internal/kube/manifests.go:251`), with no raw TCP/UDP exposure. Written | Behind on edge/TCP; see firewall below |
| Edge firewall and access | None found beyond network isolation | Per-app rules on IP, country, ASN, path, header (`internal/edgerules`, `internal/guard`); a username and password in front of an app (`internal/api/password_handlers.go`, commit `7865bc8`, 2026-09-30). Written, never through a live Traefik | Skifity ahead (unproven) |
| Databases and services | MPG: HA, PgBouncer, pgvector/PostGIS, from $38/month | PostgreSQL via CloudNativePG with odd-numbered HA instances; Redis and MySQL single instance (`internal/dbsvc/manifests.go:105-121`); no pooler or extension choice. Written | Parity in shape, behind on pooler/extensions |
| Storage and backups | Local NVMe volumes, daily snapshots, fork, auto-extend | PVCs (`internal/kube/manifests.go`), optional Longhorn replication (`internal/settings/settings.go` component `longhorn`), scheduled tar backups and `pg_dump` to the user's own S3 via presigned URLs (ADR-0012); no snapshots, no fork, no auto-extend. Written | Ahead on off-site backup, behind on snapshots/fork |
| Scaling and HA | Per-region counts, autostop/suspend, separate metric autoscaler | HPA on CPU/memory, KEDA scale-to-zero with a fixed 300 s idle (`internal/kube/scaletozero.go`), scaling readiness checker (`internal/deploy/scaling.go`), k3s rescheduling. Written | Parity (Skifity ahead on readiness check) |
| Placement / regions | 17 regions, choose per Machine | Servers carry location labels applied to the node (`internal/provision/provisioner.go:690`), but an app has **no field to select them** (no nodeSelector in `internal/kube/manifests.go`) | Behind |
| Observability | Managed Prometheus/Grafana ~15 days, logs ~7 days, log shipper, no alerting | Live tail, default 200 and max 10,000 lines, previous container (`internal/api/stream_handlers.go:177-188`); current usage from metrics-server (`internal/kube/client.go`); panel's own `/api/metrics`; full monitoring is an external Helm install (`internal/settings/settings.go` component `monitoring`). No history, no forwarding | Behind |
| Security, auth, roles, SSO, audit | Firecracker isolation, Member/Admin, SSO Google/GitHub only, macaroon tokens, write-only secrets with digests | Argon2id, TOTP, recovery keys, OIDC with PKCE against any IdP (`internal/auth`), three roles (`internal/store/models.go`), team-bound scoped tokens (`internal/auth/scopes.go`), audit log (`GET /api/teams/{team}/audit`), secrets sealed with context and write-only. **Works** (checklist item 11) | Parity; ahead on SSO, behind on token granularity per app and on a read-only human role |
| Preview environments | Only via a GitHub Action | Built in: namespace per PR or branch, an empty database of its own per linked database, fork previews get no app secrets, deleted on close (`internal/api/webhook_handlers.go`), commit status and PR comment (`internal/deploy/gitreport.go`); the last two landed on 2026-09-30. Written | Skifity ahead |
| Templates and catalogue | None; framework scanners | 282 one-click apps (`internal/templates/catalogue/*.yaml`) | Skifity ahead |
| CLI, API, IaC | `flyctl` covers everything; Machines API; `fly.toml`; `fly mcp server` and, since 2026-10-23, the hosted `mcp.fly.io` (beta); agent skills; Terraform archived | 17 CLI commands, none for domains, databases, volumes, backups or jobs (`internal/cli/commands.go:34-71`); 15 MCP tools, none for those either (`internal/mcpserver/server.go`); `skifity.toml` holds ids only (`internal/cli/project.go:18-31`); `skifity export` **Works** | Behind on CLI coverage and config-as-code |
| Notifications | Email/status page only | Telegram, Discord, webhook, email and plugin-provided kinds on 7 events (`internal/notify/notify.go:41-47`). Written | Skifity ahead |
| Multi-server and networking | Global anycast, 6PN mesh, `.internal` DNS, Flycast, WireGuard for laptops | k3s nodes joined over SSH with WireGuard flannel (ADR-0003), a namespace per environment with default-deny (`internal/kube/namespace.go`); no laptop access to private services | Behind (different category) |
| Team and collaboration | Organisations, 2 roles | Teams, 3 roles, invitations, audit (`internal/api/api.go` team routes). Works | Parity |
| Developer experience | `fly launch`; trial | Installer + first-run setup, detection before first build, `llms.txt`, five languages; **no release tagged**, so no one-command install today (`docs/checklist.md`) | Behind until a release exists |

## Gaps worth closing in Skifity

Ordered by what a Fly user would miss first. None of these repeats something
Skifity has.

### P1 — CLI and MCP coverage for everything the panel can do

* **What.** `skifity domains add|list|rm`, `skifity db create|list|credentials|backup|restore|link`,
  `skifity volumes …`, `skifity jobs …`, `skifity env-list/projects`, and the
  same set as MCP tools (`add_domain`, `create_database`, `backup_database`,
  `list_jobs`, …).
* **Evidence.** `flyctl` is the whole product and `fly mcp server` hands all of
  it to an assistant. `llms.txt` promises "anything the panel can do, the CLI
  and the MCP server can do", but the CLI has no command for domains or
  databases and the MCP server has 15 tools, none of them for data. An
  assistant told to "add a Postgres and point the app at it" cannot do it
  today.
* **Fit.** The API routes already exist (`internal/api/api.go`); this is
  `internal/cli/commands.go` and `internal/mcpserver/server.go`, plus `--json`
  on each command, plus errdoc problems for each failure.
* **Size.** M (many small commands).
* **Without a cluster?** Yes. The smoke test already drives the CLI against a
  real panel; each command is a request whose refusal paths are testable with
  no cluster configured.

### P1 — `skifity proxy`: reach a private database or port from a laptop

* **What.** `skifity proxy <database|app> [--port]` opens a local port that
  tunnels through the panel's API to the Service in the cluster, authorized like
  logs, audited, and closed when the command exits.
* **Evidence.** `fly proxy`, `fly mpg connect` and `fly wireguard` are how Fly
  users run `psql` or a migration against production; Northflank has
  `northflank forward` for the same reason. Skifity's credentials card hands
  out a host of the form `name.namespace.svc.cluster.local`
  (`internal/dbsvc/manifests.go:144`) that nothing outside the cluster can
  resolve.
* **Fit.** `internal/kube/client.go` already exposes the REST config "which exec
  and port-forward need"; a WebSocket endpoint in `internal/api` behind
  `authorizeDatabase`/`authorizeApp`; the CLI side in `internal/cli`. No
  interactive shell (the roadmap rules that out); a byte tunnel to one port is a
  different and smaller thing.
* **Size.** M.
* **Without a cluster?** Partly: authorization, audit and the CLI's local
  listener can be tested; the tunnel itself needs a cluster.

### P1 — A read-only "viewer" role

* **What.** A fourth role below member that can read apps, logs, deployments
  and metrics, and change nothing.
* **Evidence.** The most specific Fly complaint in the research: "You can't
  invite a read-only user (check logs, metrics) or even a billing user"
  (rdrn.me). Fly now has read-only auditor *tokens*; Northflank has custom roles
  restricted to projects; DigitalOcean has a Resource Viewer role. Skifity's
  scoped tokens can already be read-only (`internal/auth/scopes.go`), so only
  the human half is missing.
* **Fit.** `store.Role` in `internal/store/models.go`, `authorizeTeam` in
  `internal/api/api.go`; the route-walk test that proves 91 team-scoped routes
  refuse another team's id is exactly the harness to prove a viewer is refused
  every write.
* **Size.** S.
* **Without a cluster?** Yes, entirely.

### P1 — Process groups: one build, several processes

* **What.** An app declares `web`, `worker`, `scheduler` processes sharing one
  image and one set of variables, each with its own command, instance count and
  whether it gets a domain.
* **Evidence.** `fly.toml` `[processes]` is the standard Fly shape; DigitalOcean
  splits services, workers and jobs from one repository. In Skifity a worker
  from the same repository is a second app and **a second build**: the image is
  named after the app (`internal/deploy/build.go:58`), so the fingerprint cannot
  be shared.
* **Fit.** A `processes` list on the app in `internal/store`, one Deployment
  per process in `internal/kube/manifests.go`, the scaling tab per process, the
  scaling checker per process. Templates that already install "a web app and
  its worker" as two apps could collapse into one.
* **Size.** M.
* **Without a cluster?** The rendering is covered by golden manifest tests
  without a cluster; running it needs one.

### P2 — Put an app in a location

* **What.** An app setting "run on servers labelled `location=eu`" that becomes
  a node affinity.
* **Evidence.** Choosing a region is the second thing a Fly user does. Skifity
  already asks for a server's location and writes it onto the node "so apps can
  be steered without anyone touching kubectl" (`internal/provision/provisioner.go:690`),
  but nothing on the app side reads it.
* **Fit.** A field on `store.App`, a `nodeAffinity` in `internal/kube/manifests.go`,
  a select on the scaling tab; the "why can't it start" explainer
  (`internal/kube/explain.go`) should name the label when nothing matches.
* **Size.** S.
* **Without a cluster?** Yes for the manifests and the panel; scheduling
  needs one.

### P2 — A check against the new version before it takes traffic

* **What.** An optional "check command" that runs against a throwaway instance
  of the new image (address in an environment variable) and aborts the deploy on
  a non-zero exit, before the rolling update starts.
* **Evidence.** Fly's `machine_checks` catch bad deploys with integration tests
  against the real image; readiness probes only answer "is it up", not "does it
  work".
* **Fit.** A step in `internal/deploy/deployer.go` between the release command
  and the rollout, reusing the one-off run Job (`internal/kube/runjob.go`).
* **Size.** M.
* **Without a cluster?** Ordering and failure handling can be unit-tested;
  the check itself needs a cluster.

### P2 — Secret digests and staged changes

* **What.** Show a short digest next to each secret so two people can confirm
  they set the same value without seeing it, and let several variable changes be
  saved and applied in one rollout.
* **Evidence.** `fly secrets list` shows a digest per version; `--stage` avoids
  one restart per secret.
* **Fit.** A keyed hash (never the raw hash of a low-entropy value) computed at
  seal time in `internal/crypto`, returned by `GET /api/apps/{app}/variables`;
  a "save without restarting" flag on `PUT /variables`.
* **Size.** S.
* **Without a cluster?** Yes.

## Things to deliberately not copy

* **Rollback that leaves the configuration where it was.** Fly's docs say it
  plainly; Skifity restores the runtime settings a version ran with (not its
  variables, domains or disks, as Phase 94 found) and shows what it will change
  first, and should keep treating that as a feature.
* **Local volumes as the silent default.** Fly's volume model pushes
  replication onto the user and produces "could not reserve resource" when a
  host fills. Skifity's default storage class on k3s is also node-local; the
  lesson is to keep saying so where the volume is created and to keep Longhorn
  one click away, not to pretend otherwise.
* **"This is not managed Postgres."** Shipping a template that looks like a
  product and then explaining that the user operates it cost Fly years of
  trust. Skifity's database page should say exactly what CloudNativePG does and
  does not do for Redis and MySQL, which run as single instances.
* **An IaC integration nobody maintains.** Fly archived its Terraform
  provider. If Skifity adds config-as-code, it should be its own file format,
  owned and tested here, not a provider for someone else's tool.
* **Features that cannot be sustained.** GPUs were launched, then written off,
  then deprecated with five months' notice. A self-hosted panel's equivalent is
  a component that is installable but unmaintained; the `Beta` flag on KEDA in
  `internal/settings/settings.go` is the right instinct.
* **Metering creep.** Not applicable to a product with no billing, but the
  underlying lesson applies: a new cost (snapshots, cross-region transfer)
  should be visible where it is incurred, which for Skifity means disk and
  memory use shown before a component is installed, as it already does.

## Since 2026-09-30

Read on 2026-11-08. Fly's blog feed, the Fresh Produce category of its forum
(Fly's changelog) and the forum's most-read topics, the infra log, the status
page's API, the docs (pricing, free trial, regions, Managed Postgres) and
`mcp.fly.io` were read; the docs' and flyctl's merged pull requests and flyctl's
issues through the GitHub search API; OSV and NVD; Hacker News through Algolia.
GitHub's repository endpoints (releases, advisories) were refused from this
sandbox, and a web search was not available.

### The company

* **Missed on 2026-09-30.** On 2026-07-24 ("Turn And Face The Strange") Kurt
  Mackey stepped down as chief executive for Scott Johnston, Docker's former
  chief executive, announced a fundraise without figures, and made "Computers
  for Agents", Sprites, the company's focus: "Fly Machines and our Platform As
  A Service features aren't going anywhere", but Sprites had been "the product
  of a tiny skeleton crew" and no longer are. The post opens with a video in
  which Theo Browne named Fly the provider he was least sure would be around at
  the end of the year.
* On 2026-10-23 Fly wrote that "agents have overtaken humans as our most common
  customer species". Nearly everything it shipped in this period was for agents.
* Fly Kubernetes (FKS) was deprecated on 2026-10-15 and its documentation
  removed on 2026-10-30.

### Pricing

* **Machine prices rose on 2026-10-01**, with customers told beforehand. The
  per-second rates went from $0.00000075 to $0.0000008465 for a shared vCPU
  (+13%), from $0.00001196 to $0.000012732 for a performance vCPU (+6%), and
  from $0.00000193 to $0.000002316 for a GB of RAM (+20%). In Ashburn a
  `shared-cpu-1x` with 256 MB is $2.19 per 30 days (it was $1.94), 512 MB $3.69,
  1 GB $6.70, a `performance-1x` $33.00, and RAM beyond a preset $6.00 per
  GB-month. Volumes, snapshots, IPv4 and data transfer did not change. Corrected
  in place above.
* **The trial** became $1 of credit for Machines, Sprites and Managed Postgres,
  for 7 days or until spent, without a card; the docs were rewritten for it on
  2026-11-04, "the $1 trial credit that replaced the Machines trial". Corrected
  in place above.
* Managed Postgres storage is billed on what is used rather than what is
  provisioned (docs, 2026-09-25; clusters on the first version stay on
  provisioned). Builders and registry storage are free, with "ample notice"
  promised before that changes (pricing page, 2026-10-27).
* On 2026-11-06 Fly fixed dashboard invoices that showed amounts from prices
  hard-coded before October's change, and truncated rather than rounded; what
  Stripe charged was right.

### Product

* **`mcp.fly.io`** (beta, 2026-10-23): one hosted MCP server for apps, Machines,
  volumes, secrets and Sprites, with Managed Postgres "coming soon". It is
  added to Claude Code, Codex, Cursor, VS Code, Claude or OpenCode by its
  address and signed into in the browser, where the person chooses the
  organization, the apps the agent may work on and what it may do with Sprites;
  there is no token to paste. IP addresses and certificates are readable but
  not writable, and discovery returns only the apps that were approved, "so
  your agent won't even know the apps you didn't select exist". On the first day
  the consent page's Authorize button was disabled for some combinations of
  organizations and apps. A `submit_feedback` tool followed on 2026-11-05.
* **flyctl 0.5.** `fly skills` (v0.5.1) installs Fly's agent skills into Claude
  Code, Codex, Cursor, OpenCode, Copilot, Factory Droid and a universal
  `~/.agents` directory, and updates them without overwriting files the person
  edited; `fly setup agent` (v0.5.2, 2026-10-30) installs the skills, writes an
  MCP entry for `mcp.fly.io` and signs the person in. `FLY_NO_PROMPT`
  (2026-10-08) stops every prompt, because agent harnesses give a command a
  pseudo-terminal and it then waited for an answer nobody would give. `fly
  feedback` and a feedback API let an agent report a bug against a request id
  (2026-11-04). The install endpoint served v0.5.5 on 2026-11-08; 80 pull
  requests were merged between 2026-09-30 and then, and the repository has
  1,718 stars and 233 open issues.
* **Managed Postgres.** `fly mpg fork` makes a new cluster from another's current
  data, on the same plan or another (merged 2026-10-16), and `fly mpg users
  rotate-password` rotates a user's password, optionally ending its sessions
  (v0.4.113, 2026-10-07). Managed Postgres in the Machines API (2026-09-15) was
  missed here.
* The Machines API's list endpoints paginate (2026-10-01); flyctl warns when an
  organization uses 80% of its Machines limit (2026-10-24); `fly scale count`
  applies a count to each region rather than in total (2026-11-04).
* On 2026-11-05 Fly described its own deploys: the default rolling
  `max_unavailable` of a third overloaded the remaining Machines of its API, and
  a worker app with no health check rolled every Machine in about a minute,
  because without a check flyctl counts a Machine as deployed after 10 seconds.

### Incidents and security

* **PwnEscape (CVE-2026-99999), 2026-10-15 to 17.** A Linux kernel bug in IPv4
  checksum offload let a guest rewrite two bytes of an IPv4 header after the
  host had routed and filtered the packet: a firewall bypass, and an
  out-of-bounds read of 32 bytes of host memory that "can contain host packets,
  kernel data structures, and packets belonging to the VM next door". It was
  published on 2026-10-15 with a write-up of the exploit. Fly rebooted every
  host into a patched kernel between 16:22 UTC on 2026-10-16 and 05:15 UTC on
  2026-10-17, each host down for 15 to 20 minutes, in waves planned so that no
  Managed Postgres cluster had both nodes in one wave (17 waves in `iad`);
  single-Machine apps went down whatever was done. Some hosts did not come back:
  a four-node chassis in `gru` lost its power supply with no spare on site, and
  its Machines and volumes were down until 2026-10-20. Fly found no evidence
  that data was read and asked anybody running between 15 and 17 October to
  rotate credentials sent unencrypted, "including unencrypted traffic over your
  6PN". The status notice said maintenance and did not name the CVE; customers
  objected, and incident communication now belongs to Fly's executives (blog,
  2026-11-06).
* What it was like, from the forum thread "GRU: Machines and attached volumes
  remain unreachable during rolling host reboots" (2026-10-16, 29 posts):
  single-node databases on local volumes holding "~14 hours of production data
  newer than our last snapshot" that "exists nowhere else", a volume that could
  not be deleted because it still showed as attached, and "disappointed in the
  comms".
* **CVE-2026-122192**, a Firecracker escape (versions 0.23.0 to 1.16.1, CVSS
  7.5, published 2026-11-04): a guest's root could read or change the VMM
  process's memory on a microVM restored from a snapshot with the File memory
  backend. Fly deployed mitigations to the whole fleet the same day and will
  restart Machines gradually, "spacing out restarts within each app" (forum,
  2026-11-05).
* On 2026-10-23 Fly's head of engineering listed the week: the fleet reboot, CA
  certificates patched after eMudhra's weak keys, no PHP found internally, Node
  updated everywhere, and a search for anomalies "since learning about the
  compromise and attacks at other platform companies".
* The status page has 25 incidents from 2026-10-01 to 2026-11-06. Critical:
  hosts offline in AMS and the Managed Postgres control plane there unavailable
  (2026-10-03, about an hour); Sprite creation failing (2026-10-08). Major: LHR
  unreachable for about 15 minutes after new edges went live without
  advertising their BGP routes (2026-10-08); the fleet reboot; `fly-metrics.net`
  down during it; emergency network maintenance in IAD (2026-11-02). Low
  capacity stopped Machines starting in GRU for seven hours on 2026-10-20.
* The infra log has entries for four days since 2026-09-30: the LHR BGP outage
  and an oversized Sprites image (2026-10-08), an expired ECDSA certificate that
  took `*.sprites.app` down for 75 minutes (2026-10-12), lock contention in
  Petsem, Fly's SQLite secrets store, failing half of its writes (2026-10-15),
  and the Managed Postgres control plane in IAD going read-only for a couple of
  hours when etcd reached its 2 GiB quota, the databases serving throughout
  (2026-10-25). From the week before 2026-09-30, after this file's window:
  private networking broken for some Machines by gateway routes and reused host
  ids (2026-09-23), and GRU routing degraded for two and a half hours by a
  Corrosion metadata format (2026-09-24).
* OSV has no advisory for `flyctl` or `fly-go`, and NVD no CVE naming Fly.io or
  flyctl published since 2026-09-30 (2026-11-08).

### What users say

* A Wired story of 2026-10-28 on surveillance of activists (Hacker News, 1,201
  points, 1,048 comments) led commenters to a court filing that lists, from
  Fly.io, SSH session logs, outbound connection records and an account's signup
  IP among the records agents used. Commenters asked whether Fly had challenged
  the request; a former Fly employee wrote on 2026-11-07 that he never saw Fly
  hand over data it was not legally obliged to, and that Fly "had no facility we
  could have used to provide 'SSH session logs'". No statement from Fly was
  found on its blog or forum by 2026-11-08.
* "State of fly.io and IaC in 2026" (forum, 2026-10-05): still no Terraform
  provider, by Fly's choice; a community Pulumi provider is "in the works"; and
  "bootstrapping a machine or app still involves manual steps, so setting up a
  new environment for an existing app cannot be automated easily".
* flyctl's tracker since 2026-09-30: a canary deploy waits out its timeout when
  autostop stops a Machine during the deploy (#5326); `fly storage destroy`
  leaves the bucket's Tigris access key active, and a failed `fly storage
  create` makes one (#5280); a machine-exec token cannot list secrets' names
  (#5363); `fly launch --db mpg` fails without an `=` (#5387).

### For Skifity

Checked against the code on 2026-11-08.

* **Rows of "Against Skifity" that no longer describe Skifity.** Rollback: the
  cell overstated it and is corrected; Phase 94 also added a dialog that says
  what a rollback will change before it runs. Domains: public TCP and UDP ports
  (`62d2adb`) and hostname redirects (`6308052`). Databases: nine engines
  (`f4f9d09`), still no pooler (nothing in `internal/dbsvc`). Observability:
  three days of each app's usage with thresholds (Phase 95), servers' usage and
  disks (Phase 99), log drains (`3731416`); still nothing kept in the panel
  beyond the container's own log. Security: a viewer role (Phase 85), members
  limited to projects (Phase 86), teams that follow the provider's groups
  (Phase 92), passkeys (`6c3eee5`), tokens limited to networks (`977b982`).
  Previews: the whole environment, seeded (Phase 107). Templates: 374 catalogue
  files. CLI, API, IaC: `db`, `domains`, `dns`, `ports`, `drains`, `processes`,
  `files`, `certs`, `plan` and `apply` among the CLI's commands
  (`internal/cli/commands.go`), 46 MCP tools also served by the panel at
  `/api/mcp` (Phase 87), and `skifity.yaml` (Phase 108). Multi-server: `skifity
  db connect` reaches a database from a laptop (Phase 105). Notifications:
  Slack, Mattermost, ntfy, Pushover, Teams and Gotify too (Phase 88,
  `faeecc1`). Developer experience: v0.1.1 is released and installs from the
  latest release (`docs/progress.md`, "The first release").
* **This file's gaps.** Closed: CLI and MCP coverage (Phases 87 and 105, Phase
  112); a way into a private database (Phase 105; an app's private port still
  has none); the viewer role (Phase 85); processes from one build (Phase 106).
  Still open: putting an app in a location (nothing in
  `internal/kube/manifests.go` reads a server's location); a check against the
  new version before it takes traffic (no such step in `internal/deploy`); and
  secret digests with staged changes (`internal/deploy/references.go` keeps
  digests only of values read from secret managers).
* **Already there, against this period's releases.** Rotating a database's
  password gives every linked app the new connection string in a rollout
  (`docs/databases.md`, "Changing the password"), which `fly mpg users
  rotate-password` leaves to the user. A rollout keeps `maxUnavailable: 0`
  (`internal/kube/manifests.go`), the opposite of the default that overloaded
  Fly's own API. Each environment's quota is shown with what is used of it
  (`GET /api/environments/{env}/quota`), though nothing warns before a limit
  the way flyctl now does at 80%.
* **Corroborated, not new.** The hosted MCP's sign-in, with the apps chosen at
  consent and the rest invisible, is gap 1 of `openship.md` (OAuth for
  `/api/mcp`, which takes a pasted token today, `docs/cli.md`); Skifity's
  tokens can already be limited to a project and to reading. `fly skills` and
  `fly setup agent` are the agent skills of `sealos.md` (P1), with the MCP entry
  and the sign-in written in the same command. `fly mpg fork` is the "restore
  into a new database" that `zeabur-oss.md` found missing: a restore
  replaces the database it was taken from (`POST
  /api/databases/{id}/restore/{backupID}`).
* **PwnEscape and the servers Skifity looks after.** The hardening report knows
  when a server needs a reboot and says "with more than one server, one at a
  time" (`internal/provision/hardening.go`), and does nothing about it; applying
  updates one server at a time is already proposed in `heroku.md` (P1) and
  `openship.md` (9). Fly's week adds three requirements to that proposal: plan
  the order so that no database's instances are down together (Fly needed 17
  waves in one region), name the apps with one instance that will go down, and
  say in the notice why, even when the reason is a security fix. Apps on a
  Skifity server are containers sharing its kernel, with no microVM between
  them, so a kernel bug of this class matters for code the owner did not write,
  such as a fork's preview, and the answer is the same patched kernel.
* **New gap — a switch that stops the CLI prompting.** Whether to ask is decided
  only by `term.IsTerminal` (`isInteractive` in `internal/cli/up.go`, and
  `internal/cli/commands.go`, `dns.go`, `secrets.go`), so an agent harness that
  gives the command a pseudo-terminal leaves `skifity up` waiting at ".env has N
  settings ... [Y/n]" for an answer that never comes — the case `FLY_NO_PROMPT`
  was made for. `SKIFITY_NO_PROMPT` making `isInteractive` false everywhere,
  each prompt then failing with an errdoc problem that names the flag to give
  instead, and the variable in `llms.txt` and `docs/cli.md`. Size S; no
  cluster.

## Sources

All read 2026-09-30 unless marked.

* https://docs.fly.io/about/pricing/
* https://docs.fly.io/about/free-trial/
* https://docs.fly.io/mpg/
* https://fly.io/docs/postgres/getting-started/what-you-should-know/ (title "This Is Not Managed Postgres")
* https://gist.github.com/karlhorky/f80217c1e908e5c3bbdaffb002c55eb9 (January 2025 newsletter)
* https://community.fly.io/t/future-of-legacy-postgres/24609
* https://supabase.com/changelog/33413-deprecation-of-fly-io-postgres-managed-by-supabase-on-april-11-2025
* https://docs.fly.io/launch/autostop-autostart/
* https://docs.fly.io/reference/suspend-resume/
* https://docs.fly.io/launch/autoscale-by-metric/
* https://github.com/superfly/fly-autoscaler
* https://docs.fly.io/volumes/overview/
* https://community.fly.io/t/we-are-going-to-start-charging-for-volume-snapshots-from-january-2026/26202
* https://community.fly.io/t/auto-deploy-on-push-to-github/22306 (2024-10-17)
* https://docs.fly.io/reference/configuration/
* https://docs.fly.io/blueprints/seamless-deployments/
* https://docs.fly.io/blueprints/rollback-guide/
* https://docs.fly.io/apps/secrets/
* https://docs.fly.io/machines/overview/
* https://docs.fly.io/networking/private-networking/
* https://docs.fly.io/monitoring/metrics/
* https://fly.io/docs/monitoring/exporting-logs/ and https://github.com/superfly/fly-log-shipper
* https://docs.fly.io/security/security-at-fly-io/
* https://fly.io/docs/security/org-roles-permissions/
* https://fly.io/docs/security/sso/
* https://fly.io/docs/security/tokens/ and https://fly.io/blog/macaroons-escalated-quickly/
* https://fly.io/docs/mcp/flyctl-server/
* https://fly.io/docs/app-guides/review-apps-guide/ and https://github.com/superfly/fly-pr-review-apps
* https://github.com/fly-apps/terraform-provider-fly (archived 2024-03-01) and https://news.ycombinator.com/item?id=38362118
* https://github.com/superfly/flyctl (stars, open issues, licence via GitHub search API)
* https://github.com/superfly/flyctl/releases
* https://github.com/superfly/flyctl/security/advisories
* https://github.com/superfly/corrosion
* https://fly.io/blog/the-region-consolidation-project/ (2025-09-09)
* https://news.ycombinator.com/item?id=45380634 (regions, 2025-09-25)
* https://fly.io/blog/wrong-about-gpu/
* https://community.fly.io/t/gpu-migration-fly-io-gpus-will-be-deprecated-as-of-july-31-2026/27110
* https://simonwillison.net/2026/Jan/9/sprites-dev/
* https://fly.io/infra-log/
* https://status.flyio.net/ and https://statusgator.com/services/flyio
* https://isdown.app/status/fly-io/incidents/612731-deploys-defaulting-to-fly-hosted-builders
* https://community.fly.io/t/how-is-reliability/26615
* https://news.ycombinator.com/item?id=42248279
* https://news.ycombinator.com/item?id=36808296
* https://news.ycombinator.com/item?id=31390506 and https://news.ycombinator.com/item?id=32951363
* https://community.fly.io/t/anyone-else-having-a-managed-postgres-outage-in-fra-or-elsewhere/27890
* https://community.fly.io/t/urgent-production-outage-all-mpg-clusters-in-ord-unavailable-postgres-nodes-unreachable-behind-healthy-pgbouncer/28522 (title only; the thread needs a sign-in)
* https://community.fly.io/t/apparently-mpg-is-not-really-high-availability/27060 (title only; needs a sign-in)
* https://pulsetic.com/status/fly-io/incidents/6109/ (MPG v2 control plane, IAD, 2026-08-01)
* https://community.fly.io/t/insufficient-capacity-in-iad/27499 and https://community.fly.io/t/failed-to-launch-vm-insufficient-memory/27090
* https://rdrn.me/why-we-left-fly/ (2024-08-07)
* https://www.trustpilot.com/review/fly.io
* https://forgeglobal.com/flyio_ipo/ (funding rounds)
* https://bex.co/blog/2026/09/07/fly-io-2026-billing-lines-vs-flat-rate-fleet (a competitor's blog; used only for the date of the cross-region MPG transfer charge)

Read 2026-11-08, for "Since 2026-09-30":

* https://fly.io/blog/feed.xml, in particular:
  * https://fly.io/blog/kurt-scott-money-sprites/ (2026-07-24)
  * https://fly.io/blog/sprites-block-device/ (2026-10-22)
  * https://fly.io/blog/feng-shui-for-robots/ (2026-10-23)
  * https://fly.io/blog/well-that-wasnt-fun/ (2026-11-06)
* https://community.fly.io/c/fresh-produce/27.json (topics from 2026-09-01 to 2026-11-06), in particular:
  * https://community.fly.io/t/28640 (Managed Postgres in the Machines API, 2026-09-15)
  * https://community.fly.io/t/28736 (pagination, 2026-10-01)
  * https://community.fly.io/t/28769 (MPG password rotation, 2026-10-07)
  * https://community.fly.io/t/28906 (New Fly MCP, 2026-10-23)
  * https://community.fly.io/t/28959 (fly skills and fly setup agent, 2026-10-30)
  * https://community.fly.io/t/28996 (feedback from agents, 2026-11-04)
  * https://community.fly.io/t/29003 (deploy failures, 2026-11-05)
  * https://community.fly.io/t/29020 (invoice display, 2026-11-06)
* https://community.fly.io/top.json?period=quarterly and ?period=monthly, and the topics https://community.fly.io/t/28843 (GRU during the reboots), https://community.fly.io/t/28899 (the head of engineering's reply, 2026-10-23), https://community.fly.io/t/29004 (CVE-2026-122192, 2026-11-05) and https://community.fly.io/t/28753 (IaC, 2026-10-05)
* https://fly.io/infra-log/ (entries from 2026-09-15 to 2026-10-25)
* https://status.flyio.net/api/v2/incidents.json (incidents from 2026-10-01 to 2026-11-06)
* https://fly.io/docs/about/pricing/, https://fly.io/docs/about/free-trial/, https://fly.io/docs/reference/regions/ and https://fly.io/docs/mpg/
* https://mcp.fly.io/ (the setup guide)
* https://api.fly.io/app/flyctl_releases/linux/x86_64/latest (v0.5.5)
* GitHub search API: `repo:superfly/flyctl` (stars, open issues; pull requests merged since 2026-09-30, among them #5285, #5295, #5305, #5320, #5333, #5339, #5341, #5344, #5373; issues #5280, #5326, #5363, #5387) and `repo:superfly/docs` pull requests #2488, #2507, #2508, #2525, #2601, #2610, #2619
* https://services.nvd.nist.gov/rest/json/cves/2.0 (CVE-2026-99999; CVE-2026-122192; keyword searches for Fly.io and flyctl since 2026-09-30) and https://api.osv.dev/v1/query (`github.com/superfly/flyctl`, `github.com/superfly/fly-go`: no advisories)
* https://hn.algolia.com/api/v1/search_by_date (stories and comments since 2026-09-30) and https://hn.algolia.com/api/v1/items/50272749 (the Wired story's thread)
* Not reachable from this sandbox: GitHub's repository endpoints (releases, security advisories) and web search
* Skifity: the files named under "Since 2026-09-30", read in this repository on 2026-11-08
