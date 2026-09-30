# Fly.io

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

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
for new organisations: a trial of 2 VM-hours or 7 days, then per-second
billing (a `shared-cpu-1x` with 256 MB is about $1.90/month in Ashburn),
$0.15/GB-month for volumes, $0.08/GB-month for snapshots beyond 10 GB (billed
since 2026-01-01), $2/month for a dedicated IPv4 and Managed Postgres from
$38/month. It is mature (the `flyctl` repository dates from July 2019; releases
ship almost daily, v0.4.101 to v0.4.110 between 8 and 29 September 2026) and
well funded ($134M raised, most recently a $23.2M Series D on 2026-07-07 at a
reported $475M post-money, per Forge Global). No official customer count was
found; revenue figures online come from estimation sites and are not repeated
here. In September 2025 it cut its regions from 35 to 18, and in February 2026
it announced its GPU Machines end on 2026-07-31. (All figures read 2026-09-30;
sources at the end.)

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
  and other extensions.
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
  as an MCP server; `fly mcp launch` deploys MCP servers.
* **Sprites** (January 2026): persistent, checkpointable sandbox VMs for coding
  agents, idle after 30 seconds, MCP endpoint added March 2026.

### Notifications

* Email for billing and platform notices; the status page. No deploy-failed or
  app-down notifications to Slack/Discord/webhooks were found in the docs.

### Multi-server and networking

* 18 regions after the September 2025 consolidation (7 North America, 5
  Europe, 4 Asia-Pacific, 1 South America, 1 Africa).
* 6PN: every Machine in an organisation on one WireGuard IPv6 mesh, `.internal`
  DNS (`app.internal`, `iad.app.internal`), Flycast for private load-balanced
  addresses, custom private networks for tenant separation, and
  `fly wireguard create` to put a laptop on the network.

### Team and collaboration

* Organisations with Member/Admin, invitations, SSO enforcement, billing per
  organisation. The dashboard is secondary to the CLI.

### Developer experience and onboarding

* One command from a directory to a running app is the product's signature.
* Trial: 2 VM-hours or 7 days, 10 Machines, 20 GB of volumes; trial Machines
  stop after 5 minutes; nothing runs after the trial without a card.
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

| Capability | Fly.io | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy sources and builds | Local dir, image, buildpacks, Dockerfile; remote builders; push-to-deploy from GitHub only (Launch UI) or CI | Git (GitHub/GitLab/Gitea webhooks, `internal/gitsrc`), image, folder upload (`skifity up`, `internal/cli/up.go`, `PUT /api/apps/{app}/source`); Railpack/Nixpacks/Dockerfile in an in-cluster Job (`internal/builder`). Written | Parity |
| Pre-traffic checks and strategies | release_command, machine_checks, rolling/immediate/canary/bluegreen | `ReleaseCommand` (`internal/store/models.go`, run by `internal/deploy/run.go:130`), readiness/liveness/startup probes (`internal/kube/manifests.go:60-64`), rolling with `maxUnavailable: 0`, Recreate with a volume. No canary, blue-green or throwaway-instance check. Written | Behind |
| Rollback | Redeploys an old image; config and secrets **not** restored | New deployment carrying the old image **and** the settings it ran with; refuses when the image is gone (`docs/checklist.md` item 8). Written | Skifity ahead |
| Domains, TLS, routing | Anycast edge, wildcard certs, TCP/UDP services, dedicated IPs, statics at edge | cert-manager on first domain, `sslip.io` HTTP address (ADR-0015), path on a domain (`store.Domain.Path`), DNS target shown (`handleListDomains`), Cloudflare tunnel component; an app is reachable from outside only over HTTP(S) through its Ingress (`BuildIngress`, `internal/kube/manifests.go:251`), with no raw TCP/UDP exposure. Written | Behind on edge/TCP; see firewall below |
| Edge firewall and access | None found beyond network isolation | Per-app rules on IP, country, ASN, path, header (`internal/edgerules`, `internal/guard`); a username and password in front of an app (`internal/api/password_handlers.go`, commit `7865bc8`, 2026-09-30). Written, never through a live Traefik | Skifity ahead (unproven) |
| Databases and services | MPG: HA, PgBouncer, pgvector/PostGIS, from $38/month | PostgreSQL via CloudNativePG with odd-numbered HA instances; Redis and MySQL single instance (`internal/dbsvc/manifests.go:105-121`); no pooler or extension choice. Written | Parity in shape, behind on pooler/extensions |
| Storage and backups | Local NVMe volumes, daily snapshots, fork, auto-extend | PVCs (`internal/kube/manifests.go`), optional Longhorn replication (`internal/settings/settings.go` component `longhorn`), scheduled tar backups and `pg_dump` to the user's own S3 via presigned URLs (ADR-0012); no snapshots, no fork, no auto-extend. Written | Ahead on off-site backup, behind on snapshots/fork |
| Scaling and HA | Per-region counts, autostop/suspend, separate metric autoscaler | HPA on CPU/memory, KEDA scale-to-zero with a fixed 300 s idle (`internal/kube/scaletozero.go`), scaling readiness checker (`internal/deploy/scaling.go`), k3s rescheduling. Written | Parity (Skifity ahead on readiness check) |
| Placement / regions | 18 regions, choose per Machine | Servers carry location labels applied to the node (`internal/provision/provisioner.go:690`), but an app has **no field to select them** (no nodeSelector in `internal/kube/manifests.go`) | Behind |
| Observability | Managed Prometheus/Grafana ~15 days, logs ~7 days, log shipper, no alerting | Live tail, default 200 and max 10,000 lines, previous container (`internal/api/stream_handlers.go:177-188`); current usage from metrics-server (`internal/kube/client.go`); panel's own `/api/metrics`; full monitoring is an external Helm install (`internal/settings/settings.go` component `monitoring`). No history, no forwarding | Behind |
| Security, auth, roles, SSO, audit | Firecracker isolation, Member/Admin, SSO Google/GitHub only, macaroon tokens, write-only secrets with digests | Argon2id, TOTP, recovery keys, OIDC with PKCE against any IdP (`internal/auth`), three roles (`internal/store/models.go`), team-bound scoped tokens (`internal/auth/scopes.go`), audit log (`GET /api/teams/{team}/audit`), secrets sealed with context and write-only. **Works** (checklist item 11) | Parity; ahead on SSO, behind on token granularity per app and on a read-only human role |
| Preview environments | Only via a GitHub Action | Built in: namespace per PR or branch, an empty database of its own per linked database, fork previews get no app secrets, deleted on close (`internal/api/webhook_handlers.go`), commit status and PR comment (`internal/deploy/gitreport.go`); the last two landed on 2026-09-30. Written | Skifity ahead |
| Templates and catalogue | None; framework scanners | 282 one-click apps (`internal/templates/catalogue/*.yaml`) | Skifity ahead |
| CLI, API, IaC | `flyctl` covers everything; Machines API; `fly.toml`; `fly mcp server`; Terraform archived | 17 CLI commands, none for domains, databases, volumes, backups or jobs (`internal/cli/commands.go:34-71`); 15 MCP tools, none for those either (`internal/mcpserver/server.go`); `skifity.toml` holds ids only (`internal/cli/project.go:18-31`); `skifity export` **Works** | Behind on CLI coverage and config-as-code |
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
  plainly; Skifity already restores the settings a version ran with, and should
  keep treating that as a feature.
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

## Sources

All read 2026-09-30.

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
