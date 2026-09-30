# Kamal

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Kamal ("Deploy web apps anywhere", formerly MRSK) is 37signals' deployment tool:
a Ruby command-line program that runs on a developer's laptop or in CI, SSHes
into any Linux servers you list in `config/deploy.yml`, installs Docker on them,
builds the app's image with `docker buildx` (locally, on a remote builder, or on
Docker Build Cloud), pushes it to a registry, and boots containers behind
**kamal-proxy**, a small Go reverse proxy on each host that health-checks the
new container, switches traffic to it, drains the old one, and obtains Let's
Encrypt certificates. It is for developers — Rails developers first — who want
Capistrano-simple deploys of containers to their own servers with no platform in
between. **There is no control plane and no server-side database**: the system
of record is the YAML file and `.kamal/secrets` in the repository, plus what is
on each host (containers, env files, a lock directory, an audit log, the proxy's
state). **Licence:** MIT (Kamal and kamal-proxy). **Pricing:** free; there is no
commercial tier. **Maturity:** Kamal 2.0 shipped 2024-09-26 and replaced Traefik
with kamal-proxy; minor releases since then include 2.5.0 (2025-02-04), 2.6.0
(2025-05-13), 2.7.0 (2025-06-18), 2.8.0 (2025-10-19), 2.9.0 (2025-11-26), 2.10.0
(2025-12-15),
2.11.0 (2026-03-18) and **2.12.0 (2026-06-18)** (RubyGems); kamal-proxy is at
**v0.10.1 (2026-09-29)**, still 0.x (Docker Hub tags). **Adoption:** 14,617
GitHub stars, 754 forks, 153 open issues and pull requests on `basecamp/kamal`;
1,110 stars on `basecamp/kamal-proxy` (GitHub API, 2026-09-30); **27,509,053
RubyGems downloads** (2026-09-30), inflated by the fact that **every `rails new`
since Rails 8.0 (November 2024) includes Kamal 2** in its Gemfile with a
generated `config/deploy.yml` and Dockerfile. 37signals runs HEY and Basecamp on
it.

## Feature inventory

### Deploy sources and builds

* **A Dockerfile, built by the operator's machine** with `docker buildx`: local,
  a remote builder over SSH for another architecture (`builder.remote`), or
  Docker Build Cloud (`driver: cloud`). `builder.arch` takes `amd64` and/or
  `arm64` for multi-architecture images.
* **Cloud Native Buildpacks** through `pack` (2.7.0) as the alternative to a
  Dockerfile.
* Build arguments, **build secrets** mounted with `RUN --mount=type=secret`, SSH
  agent forwarding into the build, registry or GitHub Actions build cache,
  provenance attestations and SBOMs.
* The build context is **a clean git clone of the current commit** by default,
  so uncommitted files never ship; `context: .` opts out.
* Registries: Docker Hub, GHCR, ECR (ERB shelling out for a 12-hour token),
  Google Artifact Registry — or, since **2.8.0**, a **local registry** on the
  deploying machine that remote hosts pull from through an SSH port forward, so
  no registry account is needed.
* `kamal deploy`, `kamal redeploy` (skip bootstrap and proxy), **`kamal rollback
  VERSION`** — which only works while that version's container is still on the
  hosts (`retain_containers`, default 5).
* **Hooks** (`.kamal/hooks`): `docker-setup`, `pre-connect`, `pre-build`,
  `pre-deploy`, `post-deploy`, `pre-app-boot`, `post-app-boot`,
  `pre-proxy-reboot`, `post-proxy-reboot`, with `KAMAL_PERFORMER`,
  `KAMAL_VERSION`, `KAMAL_HOSTS` and friends in the environment. A non-zero exit
  aborts. Migrations are typically a Docker entrypoint or a hook, not a
  first-class step.
* **`asset_path`** bridges fingerprinted assets: the new container's asset
  directory also holds the previous version's files, so a page rendered by the
  old version can still load its CSS and JavaScript mid-deploy.

### Domains, TLS and routing

* kamal-proxy on ports 80 and 443 routes by **host** (`host`/`hosts`) and **path
  prefix** (`path_prefix`, stripped by default), so **several apps share one
  server** — the headline of Kamal 2.
* **Automatic Let's Encrypt** with `ssl: true`, which "requires that we are
  deploying to one server and the host option is set". With several web hosts,
  bring certificates yourself: `ssl.certificate_pem` / `private_key_pem` read
  from secrets (per role since 2.7.0), or terminate TLS at a load balancer.
* `ssl_redirect`, `forward_headers`, `response_timeout`, request/response
  **buffering** with a maximum body size (1 GB request by default), request
  header logging, custom **error pages** (`error_pages_path`).
* kamal-proxy itself can do more than Kamal configures: **on-demand TLS**
  approved by an HTTP endpoint (`--tls-on-demand-url`, for customer domains),
  `--canonical-host`, **percentage or allowlist rollouts**
  (`kamal-proxy rollout set --percent / --list`), `pause`/`resume`/`stop` with a
  message and a drain timeout, several targets per service with round-robin
  load balancing (PR #124), and read-target/writer-affinity routing flags.

### Databases and services

* **Accessories:** any image (MySQL, PostgreSQL, Redis, a search engine) on a
  chosen host or role, with files (ERB-rendered), directories, volumes, ports and
  env. "Accessories are managed separately from the main service — they are not
  updated when you deploy, and they do not have zero-downtime deployments."
* No managed databases, no credential generation, no linking, no replicas, no
  backups.

### Storage and backups

* Docker volumes and host directories on the host the container runs on.
* **Nothing for backups.** "Kamal does not set up backups for you, and most
  deployment tutorials mention this only in passing" (Ivan Turkovic,
  2026-02-06).

### Scaling and high availability

* Hosts are listed by hand per **role** (`web`, `job`, ...), each role the same
  image with its own `cmd`, options and env; hosts can be tagged for per-host env.
* **Rolling across hosts:** `boot.limit` (a count or a percentage) and
  `boot.wait`; roles on one host can boot in parallel (2.10.0).
* Zero downtime per host from kamal-proxy: start the new container, poll `/up`
  once a second until healthy (`deploy_timeout`), switch **all** traffic, wait
  for in-flight requests to drain (`drain_timeout`), then stop the old one
  (`stop_timeout`, configurable per role since 2.12.0).
* **No scheduler, no rescheduling on host failure, no autoscaling, and no load
  balancer across hosts.** A maintainer, July 2025: "Kamal currently only
  provides automatic TLS for individual hosts, and it doesn't currently load
  balance traffic between those hosts ... Most people doing this now are using
  either cloud load balancers ... or they use another reverse proxy as the load
  balancer (something like Caddy)."

### Observability (logs, metrics, alerts, uptime)

* `kamal app logs -f` (per role or host), `kamal proxy logs`, `kamal details`,
  `kamal app containers`.
* kamal-proxy exposes **Prometheus metrics** on `metrics_port`, with paths that
  can be excluded (health checks) so they do not skew latency figures.
* **Deploy output** can go to a file or an **OpenTelemetry** endpoint (2.12.0).
* No dashboards, alerts or uptime checks.

### Security, auth, roles, SSO, audit

* Access is **SSH keys** — as **root** unless `ssh.user` is changed — plus the
  registry password. Anyone with both can deploy; there are no users or roles.
* Secrets come from `.kamal/secrets`, a dotenv file evaluated with command
  substitution, with helpers for **1Password, LastPass, Bitwarden, Bitwarden
  Secrets Manager, AWS Secrets Manager, GCP Secret Manager, Doppler, Passbolt and
  Enpass** (`kamal secrets fetch` / `extract`). Secret env values are written to
  an env file on each host rather than passed on the command line — in
  plaintext, readable by anyone with SSH access.
* A **deploy lock** on the hosts (`kamal lock`, `--lock-wait` since 2.12.0) and an
  **audit log** of who ran what (`kamal audit`, one file per service on each
  host).
* `kamal config` is documented as "Show combined config (including secrets!)";
  in 2.12.0 its output passes through a redaction helper.

### Preview environments and branches

* **Destinations** (`deploy.staging.yml`, `-d staging`, `require_destination`)
  give named environments. There are no per-branch or per-pull-request previews.

### Templates and catalogue

* None. `rails new` generates a working `deploy.yml`, Dockerfile and Thruster
  setup for Rails apps.

### CLI, API, IaC, integrations

* The CLI is the product: `init`, `setup`, `deploy`, `redeploy`, `rollback`,
  `app exec` (with `--interactive --reuse` for a shell, `--raw` since 2.12.0),
  `app maintenance` / `app live`, `accessory`, `proxy`, `prune`, `lock`,
  `audit`, `secrets`, `server bootstrap`, `upgrade`; `--hosts` and `--roles`
  with wildcards; **aliases** (`kamal console`, `kamal shell`).
* Configuration is YAML with ERB, anchors and destinations: the config file is
  the infrastructure as code.
* **CI is trivial:** "A basic workflow that builds, pushes, and deploys on every
  merge to main takes about twenty lines of YAML" (Turkovic, 2026-02-06).
* No HTTP API, no MCP server.

### Notifications

* None built in. The `post-deploy` hook with the `KAMAL_*` variables is the
  documented place to post to Slack or fire a webhook.

### Multi-server and networking

* Any host reachable over SSH: cloud VMs, bare metal, IPv6-only machines with
  some Ansible (HN, 2026-04-22). Each host gets its own `kamal` Docker network;
  there is no overlay between hosts, so accessories are reached by host address.
* `kamal proxy reboot --rolling` upgrades the proxy host by host, with hooks to
  take each out of an upstream load balancer; a non-rolling reboot "is going to
  cause a small outage on each server".

### Team and collaboration

* The config lives in the repository, so review is a pull request; the deploy
  lock and audit log record the performer (`whoami`). Nothing else.

### Developer experience and onboarding

* `kamal init` then `kamal setup` turns fresh servers into app hosts "in under
  two minutes" for someone who already has SSH keys and a registry (Rails 8
  release coverage). For somebody who has never run a server, 2025–2026 posts
  describe hours of health-check, firewall and proxy debugging (below).

## What users love

* **No platform.** "The entire system of record is the YAML file on the
  operator's laptop and the containers actually running on each box" — no
  database, no control plane, no daemons (bex.co, 2026-07-10). "Kamal cares
  about your servers and little else — that's accurate."
* **Fresh deploys and CI.** "For a fresh application deployed to a clean server
  with no existing infrastructure to work around, Kamal is genuinely excellent";
  "Push your code, run `kamal deploy`, and your changes are live in minutes";
  "handles multiple applications on a single server surprisingly well"
  (Turkovic, 2026-02-06).
* **Declarative, documented.** "kamal is better for me, better documentation, i
  like the declarative way of setup my machines" (HN, 2025-01-21).
* **The Rails default.** Rails 8 "comes preconfigured with Kamal 2 for deploying
  your application anywhere" (Rails 8.0 release coverage, InfoQ and
  rubyonrails.org, November–December 2024), and 37signals' own production use
  is the reference customer.
* **kamal-proxy as a building block.** "Kamal proxy is good enough to sit behind
  a load balancer" (HN, 2025-04-05).

## What users complain about

* **Health checks fail for reasons unrelated to health.** Rails' `force_ssl`
  redirects the HTTP health check; host authorization rejects the internal
  hostname; Rack::Attack rate-limits the once-a-second `/up` probe; a cache that
  is not ready makes `/up` hang — all surface as a deploy timeout (Turkovic,
  2026-02-06). Issue #1083 ("Healthcheck bypass", 2024) asked for a way around
  the check altogether.
* **Multi-server is do-it-yourself.** No cross-host load balancing and no TLS
  across hosts (kamal-proxy discussion #142, 2025); "you can't set the 'default
  host'. This means load balancers can't work as '/up' never succeeds" (HN,
  2025-01-23).
* **Databases are your problem.** Accessories boot without ordering guarantees
  ("Kamal does not guarantee that accessories ... are fully ready before your
  application container starts"), have no zero-downtime deploys, and nothing
  backs them up.
* **Docker bypasses the firewall.** "Docker operates on the NAT table of
  iptables, while UFW operates on the filter table", so a published Postgres
  port is public despite UFW; the fix (`127.0.0.1:5432:5432`) "is not covered in
  Kamal's documentation" (Turkovic, 2026-02-06).
* **Secrets on the hosts in plaintext**, and a rotated secret needs a redeploy or
  `env push` to take effect.
* **Hard for beginners.** "Kamal Deployment: The Newest Form of Self-Torture"
  (alec-c4.com, April 2025; the Kamal book author replied that it is simple "if
  you understand a bit about DevOps"); "I've always given up after an hour or so
  of unsuccessful tinkering around" (HN, 2025-01-21); a Rails forum thread,
  "Struggling with Kamal 2 deploy".
* **Builds on small machines and across architectures.** Building on a 2 GB VPS
  swaps past the deploy timeout; Apple Silicon to AMD64 needs a remote builder
  or emulation (Turkovic; Honeybadger).
* **A young proxy.** "It's brittle, doesn't allow for horizontal scaling of your
  proxy instances ... doesn't have issuer fallback, doesn't do rate limit
  avoidance, doesn't respect ARI ... someone could spoof their IP by setting
  X-Forwarded-For" (a Caddy maintainer on HN, 2024-09-21 — before on-demand TLS,
  load balancing and several releases; how much still holds was not verified).

## Security record

* **GitHub Security Advisories:** none published for `basecamp/kamal` or
  `basecamp/kamal-proxy` (read 2026-09-30). Reports go through GitHub's private
  reporting or 37signals' HackerOne programme.
* **NVD:** a keyword search for "kamal" returns three CVEs, none of them about
  this Kamal (queried 2026-09-30).
* **One silent fix:** 2.9.0 (2025-11-26) "Prevent OS Command Injection in
  Passbolt adapter (CWE-78)" (#1697), with no advisory. The secrets adapters
  build shell commands on the operator's machine, so the blast radius is the
  developer's laptop or CI runner.
* **Exposure by design:** root SSH by default; secret values in plaintext env
  files on every host; `.kamal/secrets` is a shell-evaluated file; accessory
  ports published by Docker bypass UFW unless bound to 127.0.0.1; before Kamal
  2 the proxy was Traefik with Docker labels.
* Small attack surface is real: no web UI, no API and no long-running control
  plane means nothing listening except kamal-proxy and the apps.

## Against Skifity

Kamal is a different category: a deploy tool, not a platform. Where Kamal has
nothing, that is a design choice, and the verdicts below say "Skifity ahead"
only in the sense of what a user gets. Skifity statuses: almost everything
cluster-facing is **Written, never run** (ADR-0010).

| Capability | Kamal | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy sources and builds | Dockerfile or CNB built by the operator (local, remote, cloud), multi-arch, build secrets, local registry over SSH | Git with webhooks, image, folder upload; Railpack/Nixpacks/Dockerfile/static detection; builds in-cluster with BuildKit (`internal/builder/`); **no multi-arch builds**; **no one-command deploy of a prebuilt image** (`internal/cli/commands.go:468-476`, `internal/api/deploy_handlers.go:17-20`) | Skifity ahead for "no Docker knowledge"; **behind for CI-built images and architectures** |
| Zero-downtime switching | Health-check, switch all traffic, drain, stop; `asset_path` bridging; configurable drain and stop timeouts | Rolling update, `maxUnavailable: 0`, `maxSurge: 1`, 5 s preStop, fixed 30 s grace (`internal/kube/manifests.go:72-86,132,176-186`); old and new versions serve side by side during a rollout; nothing for asset skew. Written, never run | **Behind** on version-skew handling and tunable drain |
| Rollback | `kamal rollback VERSION` if the container is still on the host; env is today's | A new deployment carrying the old image **and its settings**; refuses when the image was collected (`docs/checklist.md` item 8, `internal/deploy/deployer.go`) | Skifity ahead |
| Domains, TLS and routing | Host and path routing; Let's Encrypt on single-host deploys only; custom certs; buffering; error pages; on-demand TLS, canonical host and canary in kamal-proxy | Domains with path (`internal/store/models.go` Domain), cert-manager HTTP-01 on any number of servers (`internal/cluster/components.go:130-170`), sslip.io default, HTTPS redirect; **no custom certificates, no canary, no maintenance page** | Skifity ahead on multi-server TLS; **behind on proxy features** |
| Databases and services | Accessories: any image, no backups, no zero-downtime | Managed PostgreSQL/MySQL/Redis with sealed credentials and linking (`internal/dbsvc/`), or any image as an app | Skifity ahead |
| Storage and backups | Host volumes; no backups | PVCs; scheduled S3 backups and restore for databases and volumes (`internal/backup/`) | Skifity ahead |
| Scaling and high availability | Hosts by hand; roles; rolling by batches; no rescheduling, autoscaling or cross-host LB | Scheduler places instances; HPA; scale-to-zero via KEDA; readiness checker; k3s reschedules on node loss (`internal/kube/manifests.go:362`, `internal/kube/scaletozero.go`, `internal/deploy/scaling.go`); no cross-host LB either (`docs/research/competitors.md`) | Skifity ahead (Written) |
| Observability | Logs via SSH; proxy Prometheus metrics; deploy logs to OTel | Live logs over SSE incl. previous container; per-instance usage; `/api/metrics`; optional Prometheus/Grafana | Parity; behind on per-app request metrics |
| Security, auth, roles, SSO, audit | SSH keys as root; no users; secrets plaintext on hosts; lock and audit log | Argon2id, TOTP, OIDC, roles, route-walk authorization tests; secrets sealed per row and write-only (`internal/crypto`, `internal/auth`); audit log. Works | Skifity ahead |
| Preview environments | Destinations only | Per-PR previews (`internal/api/webhook_handlers.go:184-280`). Written, never run | Skifity ahead |
| Templates and catalogue | None | 282 templates | Skifity ahead |
| CLI, API, IaC | Excellent CLI; YAML+ERB config in the repo; hooks; aliases; external secret managers; no API | CLI, API and MCP on one surface (`internal/cli`, `internal/mcpserver`); `skifity.toml` links a folder to an app but does not describe its settings; **no hooks, no secret-manager integration, no CI recipe** (`docs/cli.md`) | Mixed: Skifity ahead on API/MCP; **behind on config-as-code and CI** |
| Notifications | Hooks only | Four channels, seven events, plugin channels (`internal/notify/notify.go:41-47`) | Skifity ahead |
| Multi-server and networking | Any SSH host; no overlay; no cross-host routing | k3s over SSH, WireGuard or VXLAN between servers (`internal/settings/settings.go:236-245`), Cloudflare tunnel; **no architecture awareness in a mixed amd64/arm64 cluster** (preflight accepts both, `internal/provision/preflight.go:216-228`; no `kubernetes.io/arch` anywhere in `internal/kube`) | Skifity ahead on networking; **behind on mixed architectures** |
| Team and collaboration | Git review; lock; audit | Teams, invitations, roles, audit (`internal/api/api.go:201-216`) | Skifity ahead |
| Developer experience and onboarding | Rails default; minutes for DevOps-literate users; painful for beginners | Panel, sslip.io URL, folder deploy, errors with a fix; **not released yet** | Skifity ahead for beginners (on paper) |

## Gaps worth closing in Skifity

### P1 — Deploy an image built in CI with one command, and document the CI recipe

* **What:** `skifity deploy --image ghcr.io/acme/web:<sha>` (and the same in the
  API and MCP `deploy_app`) that sets the image and deploys in one call, plus a
  GitHub Actions and a GitLab CI example in `docs/cli.md`.
* **Evidence:** Kamal's whole model is "build in CI, deploy from CI" in about
  twenty lines of YAML; CapRover has `caprover deploy --imageName`; Dokku has
  `git:from-image` and an official GitHub Action. In Skifity today an image app
  needs `PATCH /api/apps/{app}` with a new `image` and then `POST .../deploy`,
  there is no CLI flag for it (`internal/cli/commands.go:468-476`), the deploy
  body has only `commit_sha` and `force` (`internal/api/deploy_handlers.go:17-20`),
  and `docs/cli.md` has no CI section.
* **Fit:** an `image` field on the deploy request recorded on the deployment (so
  rollback still restores exactly what ran), the CLI flag, a docs page.
* **Size:** S. **Without a cluster:** yes (API tests, `make smoke` drives the
  CLI up to the deploy).

### P2 — Stop serving two versions at once during a deploy

* **What:** an option (default on for apps with a web port) to switch traffic
  all at once: bring the new version up in full, move the Service's selector to
  it, then drain and remove the old one — instead of a rolling update that sends
  requests to both.
* **Evidence:** kamal-proxy switches all traffic to the new container and adds
  `asset_path` bridging precisely because fingerprinted assets break when a page
  from one version asks the other for its JavaScript; Dokku fixed the same bug
  for Traefik in January 2026 (#8282: "HTML page loads from the new container
  ... requests new.js ... balanced to the retiring container"). Skifity's
  `maxSurge: 1` rolling update (`internal/kube/manifests.go:176-186`) and 5 s
  preStop leave a window where both versions are ready even with one instance.
* **Fit:** a version label on pods and a selector flip in `internal/deploy`
  after readiness, with the old ReplicaSet scaled down after the drain; the
  scaling readiness checker already reasons about what breaks with two
  instances.
* **Size:** M. **Without a cluster:** the rendered objects and the state machine
  yes; the traffic behaviour needs one (Phase 1 of `verify.sh` already counts
  failed requests during a rollout and could count mixed versions).

### P2 — Maintenance mode

* **What:** a switch that answers every request to an app with a 503 and a
  message (or the app's own `503.html`), optionally letting chosen addresses
  through.
* **Evidence:** `kamal app maintenance --message "..."` and `kamal app live`;
  kamal-proxy `stop --message`; Dokku's official maintenance plugin, surfaced in
  the Dokku Pro 1.4.0 UI. Skifity has none, and it is what people reach for
  during a long migration.
* **Fit:** the firewall guard already sits in front of apps and already answers
  503 "This site is not accepting requests right now" (`internal/guard/guard.go:272`);
  maintenance is a rule set whose default is that answer, with the existing
  allow rules for the operator's own address. A switch on the app page.
* **Size:** S–M. **Without a cluster:** the guard's behaviour yes (its tests run
  without one); Traefik forwarding to the guard is already "Written, never run".

### P2 — A drain time that can be set per app

* **What:** a per-app shutdown grace (drain and stop timeout) for apps with long
  requests, uploads or websockets.
* **Evidence:** Kamal has `drain_timeout` and, since 2.12.0, `stop_timeout` per
  role; kamal-proxy `--drain-timeout`; Dokku's `wait-to-retire` (60 s default)
  and `stop-timeout-seconds`. Skifity hardcodes a 5 s preStop and a 30 s
  `terminationGracePeriodSeconds` (`internal/kube/manifests.go:84-86,132`).
* **Size:** S. **Without a cluster:** yes (golden manifests, the Scaling tab).

### P2 — Mixed-architecture servers

* **What:** record the architecture an image was built for (or that a pulled
  image offers) and pin the app's pods to matching nodes; later, build for both
  when both are present.
* **Evidence:** Kamal takes `builder.arch: [amd64, arm64]` and a remote builder
  for the other one; cross-architecture failures are a recurring Kamal complaint.
  Skifity's preflight accepts amd64 and arm64 servers
  (`internal/provision/preflight.go:216-228`) but nothing in `internal/kube` or
  `internal/builder` mentions `kubernetes.io/arch`, so an amd64 build can be
  scheduled onto an arm64 server and fail with an exec format error.
* **Fit:** a node selector from the build Job's node in `internal/deploy`, a
  refusal with an `errdoc` entry when no node matches, BuildKit `--platform`
  later.
* **Size:** S (pinning), M (multi-arch builds). **Without a cluster:** pinning
  logic and manifests yes; builds need one.

### P2 — Upload your own TLS certificate

* **What:** attach a certificate and key to a domain (a Cloudflare origin
  certificate, a corporate CA, a certificate bought elsewhere).
* **Evidence:** Kamal reads `ssl.certificate_pem` / `private_key_pem` from
  secrets (per role since 2.7.0) because Let's Encrypt only works on single-host
  Kamal deploys; kamal-proxy's README names the Cloudflare origin case; Dokku has
  `certs:add`. Skifity only issues through cert-manager's HTTP-01 solver
  (`internal/cluster/components.go:160-166`), which also rules out servers that
  are not reachable on port 80.
* **Fit:** a TLS Secret in the app's namespace, sealed at rest in the panel like
  any secret, referenced by the Ingress instead of the issuer annotation
  (`internal/kube/manifests.go:280-281`); expiry shown on the Domains tab and
  fed to the existing `certificate.failed` notification.
* **Size:** S. **Without a cluster:** yes for storage and manifests; serving it
  needs one.

### P2 — Percentage rollouts

* **What:** send a share of traffic, or requests carrying a cookie, to the new
  version before switching everyone.
* **Evidence:** kamal-proxy's `rollout set --percent` / `--list` (not yet
  exposed in Kamal's config); a common ask once apps have real users.
* **Fit:** Traefik weighted services between two Deployments, managed by
  `internal/deploy`; depends on the "switch at once" work above.
* **Size:** M. **Without a cluster:** manifests yes; behaviour needs one.

## Things to deliberately not copy

* **No control plane.** Kamal's elegance is that state lives on a laptop and in
  containers; the cost is no rescheduling, no health reconciliation, no
  multi-user access, no API. Skifity's panel is the point — but keep what Kamal
  gets right about it: apps keep running when the tool is gone (checklist item
  14).
* **Secrets in plaintext files on the servers** and a shell-evaluated secrets
  file. Skifity seals each value with context and never returns it; the Passbolt
  command injection in 2.9.0 is what building shell commands out of secret
  lookups costs.
* **Root over SSH as the default, forever.** Skifity uses the password once to
  install its own key and never stores it; keep that.
* **TLS only on single-host deploys.** Skifity's cert-manager works on every
  server; do not regress to per-host ACME caches.
* **A home-made proxy.** kamal-proxy exists because Traefik-on-Docker-labels
  was awkward for Kamal; on Kubernetes, Traefik's ingress is maintained by
  somebody else and handles ACME, HTTP/3 and forwarded headers. Add features as
  middleware, not a proxy.
* **Accessories as databases.** Containers with a volume and no backups are
  what Skifity's managed databases exist to replace.
* **Rollback that depends on an old container still lying on the host and
  reuses today's env.** Skifity's rollback carries the old settings and refuses
  cleanly when the image is gone; keep both.
* **ERB inside the deployment config** (`<%= %x(aws ecr get-login-password) %>`).
  Executable configuration is powerful and makes a config file a script.
* **Assuming DevOps literacy.** The recurring Kamal complaints are health checks,
  firewalls and headers that a beginner cannot diagnose; Skifity's errors with a
  cause and a fix are the answer to exactly that.

## Sources

All read 2026-09-30.

* https://github.com/basecamp/kamal (stars, forks, open issues via the GitHub search API)
* https://github.com/basecamp/kamal-proxy (stars via the GitHub search API)
* https://rubygems.org/api/v1/versions/kamal.json and https://rubygems.org/api/v1/gems/kamal.json (release dates, downloads, licence)
* https://rubygems.org/downloads/kamal-2.12.0.gem (source read for defaults: SSH user, retain_containers, config docs, secrets adapters, lock, audit)
* https://hub.docker.com/v2/repositories/basecamp/kamal-proxy/tags (kamal-proxy versions and dates)
* https://raw.githubusercontent.com/basecamp/kamal-proxy/main/README.md
* https://raw.githubusercontent.com/basecamp/kamal-proxy/main/MIT-LICENSE
* https://raw.githubusercontent.com/basecamp/kamal-proxy/main/internal/cmd/deploy.go, rollout_set.go, stop.go (flags)
* https://github.com/basecamp/kamal/releases and https://github.com/basecamp/kamal/releases/tag/v2.9.0
* https://github.com/basecamp/kamal/security and https://github.com/basecamp/kamal-proxy/security
* https://github.com/basecamp/kamal-proxy/issues/15
* https://github.com/basecamp/kamal-proxy/discussions/142
* https://github.com/basecamp/kamal/issues/1083 (via GitHub issue search)
* https://kamal-deploy.org/docs/configuration/proxy/
* https://kamal-deploy.org/docs/configuration/cron/
* https://kamal-deploy.org/docs/configuration/docker-registry/
* https://kamal-deploy.org/docs/configuration/builders/
* https://kamal-deploy.org/docs/configuration/accessories/
* https://kamal-deploy.org/docs/configuration/roles/
* https://kamal-deploy.org/docs/configuration/environment-variables/
* https://kamal-deploy.org/docs/configuration/booting/
* https://kamal-deploy.org/docs/commands/view-all-commands/
* https://kamal-deploy.org/docs/commands/app/
* https://kamal-deploy.org/docs/commands/proxy/
* https://kamal-deploy.org/docs/commands/secrets/
* https://kamal-deploy.org/docs/hooks/overview/
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=kamal
* https://www.ivanturkovic.com/2026/02/06/honest-take-kamal-rails-deployment/
* https://bex.co/blog/2026/07/10/kamal-2-kamal-proxy-cluster-api-simplicity
* https://www.honeybadger.io/blog/new-in-kamal-2/
* https://discuss.rubyonrails.org/t/struggling-with-kamal-2-deploy/87780 (via search result)
* https://rubyonrails.org/2024/11/7/rails-8-no-paas-required and https://www.infoq.com/news/2024/12/rails-8-released/ (via search summary)
* https://news.ycombinator.com/item?id=41608350 (via hn.algolia.com API)
* https://news.ycombinator.com/item?id=42782854 (via hn.algolia.com API)
* https://news.ycombinator.com/item?id=43639026 (via hn.algolia.com API)
* https://news.ycombinator.com/item?id=43591098 (via hn.algolia.com API)
* https://news.ycombinator.com/item?id=47864425 (story title via hn.algolia.com API)
* https://github.com/dokku/dokku/issues/8282
