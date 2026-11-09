# bex

> **Status.** Written on 2026-11-08 against bex `main` at `348e099` (2026-11-08) and Skifity at
> `29147f1` the same day. Nothing in this file has been acted on yet; the gaps below are
> proposals.

bex (`bex-co/bex`, bex.co) calls itself "the open-source, AI-native alternative to Render": a
Git repository or an image becomes an HTTPS service, described with Render's `render.yaml`
Blueprint shape and operated through a dashboard, REST, GraphQL, MCP, the upstream Render CLI
and Kubernetes custom resources, all over one Go core. It is for developers who like Render and
want the same thing on machines they own, and for coding agents, which the project treats as
first-class users. **Architecture:** `bex-api` (Go) holds product intent in a control-plane
**PostgreSQL** and projects it into `App`, `Database` and `KeyValue` custom resources; a
database-free Go operator reconciles those into Deployments, Services, Ingresses and TLS, builds
source with in-cluster BuildKit workers and pushes to a **Zot** registry. **Cluster API** creates
the machines: Docker containers locally (CAPD), **Hetzner** servers in production (CAPH), with
the Cluster Autoscaler adding and removing them. The production cluster is kubeadm Kubernetes
1.34 with three control-plane machines, a tainted platform pool of at least two machines and a
tenant pool of at least one, Cilium with WireGuard, Traefik, cert-manager, CloudNativePG, Ory
Kratos and Hydra for identity, OpenFGA for authorization, OpenBao for tenant secrets, and Argo
CD installing the platform (bex ADR002, "bex architecture"). The dashboard is TanStack Start with
Apollo and shadcn; there is also an Expo mobile app. **Licence:** Apache-2.0, no CLA. **Pricing
(read 2026-11-08):** self-hosting has no software fee. Hosted bex.co is a paid public alpha
priced at 30% below Render's sheet of 2026-07-13: workspace plans Hobby $0, Pro $17.50, Scale
$349.30 a month; services from Free (0.1 CPU, 512 MiB, sleeps after 15 idle minutes) and
Starter $4.90 (0.5 CPU, 512 MiB) to Pro Ultra $315 (8 CPU, 32 GiB); Postgres $0, $4.90 and $14;
Key Value $0, $7 and $21; build minutes $0.0035, outbound traffic $0.015/GiB, used datastore
storage $0.21/GB-month. A payment method is required before any resource is created, free ones
included. Hosted bex runs in one Hetzner region (fsn1). **Company:** Stargately, Inc., which also
sells blockchain RPC ("bex router") and AI security scanning under the same brand. **Maturity:**
the README warns that bex "is in active development and is not ready for production workloads",
and there is no uptime SLA. The repository was created on 2024-04-30 for an earlier project; the
current code starts with "Initial bex MVP" on 2026-06-05. The GitHub search API counts about
6,640 commits since then (45 in June, 1,374 in July, 1,102 in August, 923 in September, 2,512
in October and 683 in 1–8 November), from one human author, Tian Pan, with agent mission tags
such as `(w9/m180)` in the subjects. Public signup opened on 2026-09-15. Platform services carry
no version numbers until 1.0, which is defined as a versioned self-host artifact with an upgrade
path (ADR058); only the CLI is tagged (`bex-cli/v0.1.0` to `v0.4.0`). There are 101 decision
records in its `docs` folder. **Adoption:** 576 stars, 65 forks and 8 open issues and pull requests
(GitHub search API, 2026-11-08); bex.co's own page said 419 stars on 2026-09-05.

## Feature inventory

### Deploy sources and builds

* **Sources:** a GitHub repository through a GitHub App (private repositories, push to deploy,
  "zero-config push"), a public Git URL, or an image (`runtime: image`) with registry
  credentials. GitLab and Bitbucket are marked partial and "untracked" in the parity ledger
  (ADR018).
* **Builds:** native buildpacks for Go, Node.js, Python, Ruby, Rust and Elixir, or a
  Dockerfile, built by BuildKit workers in the cluster and pushed to Zot. A queued deploy says
  why it waits. Build cache, a "clear cache" deploy, root directory and runtime inference from
  the repository (re-inferred when the root directory changes).
* **Build Filters** (path-scoped auto-deploy for monorepos) and **Build Sources**, one build
  that deploys several linked services, finished on production on 2026-11-08.
* **Pre-deploy command** that gates the rollout, health-check path as readiness,
  `maxShutdownDelaySeconds` per service, deploy modes (`deploy_only` for an image).
* **Deploys** are objects with an eleven-state lifecycle and transition timestamps, the commit
  and its author time; cancel; rollback to an earlier resolved image; deploy a chosen commit.
* **Deploy from CI:** `POST /v1/services/{id}/deploys {imageUrl}` and a secret deploy-hook URL.
  The documented GitHub Actions sample builds once, tests that image, pushes it, and deploys it
  by `@sha256:` digest; "Mutable tag-only refs are rejected before any POST"
  (bex.co/docs/platform/ci-image-deploy).
* Render's `autoDeployTrigger: checksPass` (deploy only after CI passes) is not supported and is
  refused by validation (bex.co/docs/platform/migrate-from-render).

### Domains, TLS and routing

* Custom domains with DNS instructions and cert-manager certificates; wildcard custom domains are
  refused. A platform address `<name>.onbex.co` under a wildcard certificate (ADR093), which a
  service can opt out of.
* Inbound IP allowlists per web service and static site; HTTP to HTTPS for every tenant host;
  edge caching (`cache.profile`) and cache purge for web services.
* **Static sites** are built and published to an object store and served by a static server,
  with header rules, redirects and rewrites, and a 301 to the trailing-slash form of a directory.
* **Maintenance mode** per service.
* An explanation of why a service has no public address, on every surface.

### Databases and services

* **Service types:** web, private (internal only), background worker, cron job, static site.
* **Managed PostgreSQL** through CloudNativePG: plans with storage floors, backups and
  point-in-time recovery (daily base backup plus continuous WAL, 30 days on paid plans), HA with
  a failover action and named read replicas, a PgBouncer pooler, users, an IP allowlist for
  public access, major-version upgrade, disk autoscaling, a supply of extensions (ADR092), logs,
  and query observability (live queries, top queries, sizes, table scans). Read-only SQL is an
  MCP tool; the dashboard has a SQL console with an explicitly confirmed write mode. Free
  Postgres has no backups, as on Render.
* **Key Value**: Valkey-compatible, with persistence modes, daily snapshots on paid plans, logs,
  and the deprecated `/redis` API spelling served since 2026-11-05.
* Public database access goes through SNI proxies on the edge load balancer's ports 5432 and
  6379.
* No other engines (MySQL, MongoDB and the rest were not found).

### Storage and backups

* **Persistent disks** from 10 GB, one per service, grow-only, with daily snapshots and
  full-disk restore (ADR082), built on its own encrypted pipeline because "Hetzner has no volume
  snapshots or backups at any level".
* **Platform backups** (ADR031, ADR050): the control-plane database (daily base backup plus WAL,
  7 days), etcd and OpenBao (daily snapshots, 7 kept), age-encrypted on the client side, written
  with credentials that cannot read the archive. Each has a scripted restore and the date of the
  last drill against production (2026-07-31 to 2026-09-01, alpha announcement).
* Render's object storage (early access) is listed as missing.

### Scaling and high availability

* Manual instance counts and autoscaling on CPU or memory targets with a five-minute scale-down
  window and a ceiling of 100 instances.
* **Idle sleep** for free web services only: after 15 minutes (adjustable up to 7 days) the
  service hibernates; the next request is answered by an activator with a 503, `Retry-After: 5`
  and an HTML page, and is not forwarded to the app (bex.co/docs/platform/idle-services).
* Suspend, resume, restart; a service that has crash-looped or failed its health check for 24
  hours is suspended as `stuck_crashlooping` and the workspace is emailed (ADR096, 2026-11-03).
* **Machines:** the Cluster Autoscaler and Cluster API add and remove Hetzner machines; platform
  and tenant workloads never share a node; node instance types per pool (ADR053).

### Observability (logs, metrics, alerts, uptime)

* Application logs with query and live tail; build logs; **request logs** with structured
  filters, including the 403s the edge returns for blocked addresses.
* Metrics: CPU, memory, instance count, HTTP requests, latency and bandwidth, plus disk, active
  connections and replication lag for datastores, drawn with **deploy markers**.
* A service events feed; hourly **usage metering** with a month-to-date cost estimate on every
  surface; a public status page (status.bex.co); an incident-report collector.
* External log and metric streams are a recorded non-goal. No threshold alerts on CPU or memory
  were found.

### Security, auth, roles, SSO, audit

* Identity is Ory Kratos (email, TOTP, passkeys, active sessions, "Sign in with GitHub"); Ory
  Hydra issues OAuth 2.1 tokens; OpenFGA decides permissions.
* **Roles** per workspace: viewer (no logs or sensitive fields), developer, admin, billing. A
  viewer-capability projection lets every client disable what the caller cannot do.
* **Protected environments** follow Render's rule: in a protected environment only admins may
  create, move or delete resources, suspend or resume, turn maintenance mode on, open a shell,
  or view or change variables and secret files (ADR032, 2026-10-22 and 2026-11-07).
* API keys are a workspace-scoped resource over the API; a machine exchanges one for a
  15-minute bearer token. Secrets live in OpenBao; reading one back needs `can_view_sensitive`.
* Audit log with an in-app viewer, and denied reads of logs or secrets are audited too.
* SSH into a running instance through a gateway with a pinned host key, and a browser shell.
* Account deletion with a preview of its consequences, refused while the account is a
  workspace's only admin (ADR086; the FAQ on bex.co/pricing).
* Enterprise SSO, SAML and SCIM are a deliberate non-goal (ADR027).

### Preview environments and branches

* None. "Preview-service creation remains a user-rejected non-goal" (ADR018); `--previews` is
  refused with a named 400. Projects and environments exist for staging and production.

### Templates and catalogue

* No catalogue. Twelve examples in `examples/` (hello-world apps in four languages, a cron
  demo, a static site, a multi-service stack, and a few others).

### CLI, API, IaC, integrations

* **REST** in Render's `/v1` shapes, validated against a pinned copy of Render's OpenAPI before
  any side effect; a guard test walks every Render operation and fails unless it is served or
  listed as omitted. Render's live API had 252 operations on 2026-11-02 (ADR018).
* **GraphQL** for the dashboard and mobile app, and the `App` custom resource
  (`kubectl get apps.app.bex.co` shows phase, revision and URL).
* **CLI**: the upstream Render CLI, pinned at v2.35.0, with bex's API origin and three extra
  commands; a per-command compatibility checklist; Sigstore-verified installer and a Homebrew
  tap.
* **MCP**: 179 tools counted by the project on 2026-09-15, served remotely at `/mcp` with the
  MCP authorization flow (RFC 9728 resource metadata, dynamic client registration, PKCE and a
  consent page naming the client and scopes `bex.read`, `bex.write`, `bex.sensitive`), and over
  stdio. A `deploy` tool takes a repository and a `render.yaml` and returns a URL.
* **Blueprints**: `render.yaml` with services, databases, environment groups, generated values,
  projects and environments; validate, plan, approve and sync, Git-connected with auto-sync,
  sync history and "detach" notices; fail-closed, so an unsupported field is a named error.
* **Migration tools** that run in the browser (bex.co/tools, read 2026-11-08): a `render.yaml`
  compatibility checker, a Render automation checker for scripts and CI files, and converters
  from Heroku (`Procfile`, `app.json`, `heroku.yml`), Docker Compose, Fly.io (`fly.toml`) and
  Railway (`railway.json`, `railway.toml`) to `render.yaml`; plus a Dockerfile checker, a build
  context inspector, a deployment-filter explainer, a cron checker, a DNS checker, a webhook
  signature verifier and an MCP config generator. Guides "Migrate from Render, Heroku, Docker
  Compose, Fly.io, Railway".
* Also: cloud coding-agent sessions, agent automations and E2B-compatible sandboxes on gVisor.

### Notifications

* Per-member email preferences (deploy started, succeeded, failed; failure only by default),
  per-service overrides, browser and native push, and an in-app inbox. Email "is best-effort".
* **Outbound webhooks**: an event catalogue (53 advertised event types once datastore events
  were added on 2026-09-08), Standard Webhooks signatures, a secret shown once, private and loopback
  destinations refused, redirects not followed, delivery history, resend, and endpoints disabled
  after repeated failures (bex.co/docs/platform/notifications).
* No Slack, Discord or Telegram channels were found; the docs point to webhooks.

### Multi-server and networking

* Machines come from Cluster API: Terraform makes the Hetzner network and load balancers, a
  disposable k3s bootstrap cluster creates the app cluster and hands Cluster API over to it.
  Only Hetzner (cloud and bare metal) is a production provider; there is no "add an existing
  server over SSH".
* A private network with internal service addresses; Cilium encrypts node traffic with
  WireGuard; a Hetzner firewall exposes only SSH and ICMP on nodes.

### Team and collaboration

* Workspaces, projects and environments; environment groups of variables and secret files that
  link to any service in a workspace; invitations; a getting-started checklist on an empty
  workspace; billing through Stripe with dunning.

### Developer experience and onboarding

* Hosted: sign up, verify email, add a card, connect GitHub or paste a `render.yaml`.
* Self-hosted: the quickstart needs Docker, Go 1.25+, `kubectl`, `kind` and `clusterctl`, boots a
  kind management cluster, Cluster API and an app cluster of Docker containers, and runs the
  operator from source (README). A production-shaped install follows `infra/README.md`
  (Terraform plus Cluster API on Hetzner). There is no versioned self-host artifact; that is the
  1.0 bar (ADR058).
* The dashboard ships English and Chinese (`dashboard/src/common/locales`); the website has eight
  languages.

## What users love

No independent user discussion was found. Hacker News (Algolia API) has no story or comment that
links to bex.co or mentions `bex-co/bex`; Reddit refused the request; the web search tool had no
credits left. The repository has one issue from an outside person (#38, a docs fix, 2026-07-18)
and three outside pull requests (#85 a dark-mode fix, 2026-10-08; #88 and #106 build image
digests, 2026-10-11 and 2026-10-23). Everything else is the project describing itself, and is
reported here as that:

* **A migration path, written down.** "Your app definition is a portable render.yaml. If bex
  disappoints you, leaving is a redeploy, not a migration project" (alpha announcement,
  2026-09-15). The parity ledger, the CLI checklist and the browser converters exist to make
  that checkable.
* **No dashboard-only actions.** "The same create is available from the dashboard form above,
  the REST API, GraphQL, the bex CLI, an MCP tool, or kubectl apply. There are no
  dashboard-only actions" (same post).
* **Honest about gaps.** The announcement has a second table, "Not in the alpha": previews, SAML
  and SCIM, Workflows, a second region, a self-host artifact, an SLA.
* **Price.** 30% under Render on compute and 90% under on bandwidth, with a public estimator.

## What users complain about

Not found, for the same reasons. What the repository shows instead:

* **Production alerts filed by the project's own monitors.** Every issue after #61 (2026-08-28)
  was opened by `github-actions[bot]`: "main is not reaching production" (#122, #123, #125, #129,
  2026-11-03 to 2026-11-06), "Tenant isolation matrix is red on production" (#66, 2026-09-14;
  #113, 2026-10-27, closed 2026-11-05), "Sandbox isolation matrix is red on production" (#67,
  #127, #135), "Tenant view is broken" (logs, metrics, events or wake; 13 issues between
  2026-09-12 and 2026-11-08), "SSH edge is down" (#97, 2026-10-16), "onbex.co fallback TLS is
  invalid or expiring" (#98, #119), and "CLI release is stale" (#111, open). The isolation
  probes create two disposable workspaces and run an adversarial matrix against production
  weekly; #67's failure was the probe's own port-forward, and the causes of the others were not
  read.
* **Not for production, and hard to self-host.** The README warning, no SLA, no version numbers
  for platform services, one hosted region, and an install that needs Terraform, Cluster API, a
  bootstrap cluster and at least six machines in its production shape.
* **A card before a free service.** "A bound payment method is required before any resource is
  created — including free-tier resources" (alpha announcement).
* **Free-tier cold starts answered with a 503.** The first request after idle sleep is not
  forwarded to the app, and a health probe on `/healthz` gets the activator's own 200
  (bex.co/docs/platform/idle-services).
* **Gaps the project lists itself:** no pull-request previews, no enterprise SSO, no log or
  metric streams, no `checksPass` deploys, GitLab and Bitbucket partial.

## Security record

**Where I looked (2026-11-08):** OSV for `github.com/bex-co/bex` (no entries), NVD keyword
`bex-co` (zero results), and the 22 security-review decision records in the repository. The
repository's advisory pages could not be read: GitHub's repository API refused this sandbox.

**The reviews.** ADR028 (2026-07-11) is a hand audit that found "No CRITICAL or HIGH
vulnerabilities" but two absent controls: no image signing or SBOM (fixed with cosign and an
SBOM in CI) and an in-cluster registry that accepted unauthenticated push and pull from tenant
build pods (fixed). Rounds 3 to 23 followed between 2026-07-20 and 2026-08-29, most of them
scans by an automated scanner (`codex-security`), plus a GLM harness (ADR081). The rounds that
state counts: round 12, 10 findings (1 high); 13, 9 (1 high); 14, 6 (1 high); 15, 8 (2 high);
16, 13 (1 high); 17, 15 (none high); 18, 10 (**1 critical**, 2 high); 19, 8 (2 high); 22, 17
(6 medium, 11 low); 23, 1 low. Across all the records, 33 findings are rated high or critical,
four of them rejected on review.

**What they found**, in classes:

* **Tenant identity by name.** "Name-only cancellation terminates another tenant's
  builds/migrations"; "Name-only execution NetworkPolicy grants attacker build pods victim
  egress"; "Wildcard and concrete custom-domain claims treated as non-colliding" (all fixed).
  "Registry repo/user/ACL/digest/finalizer keyed only by App name" and static-site prefixes
  without tenant identity: the code landed on 2026-08-18 (ADR074), production cutover is
  runbook-gated, and the alpha announcement lists completing it as a 1.0 gate "before external
  tenants share registry and object-storage namespaces".
* **Authorization.** "OIDC-only consent grants a dynamic client the user's full permissions"
  (fixed with a required `bex.api` scope); "GraphQL writable SQL drops the sensitive
  capability"; "Revocation leaves positive caches usable to create durable replacement access"
  (both fixed).
* **Operations and supply chain.** Both critical findings are here. "Plaintext root
  credentials co-located in worktree `.env`" (round 18) is partially fixed: file mode 0600,
  rotation left to the operator. "Runner host compromise equals platform takeover (HCLOUD, SSH
  admin.conf, OpenBao root + Shamir quorum, auth bootstrap secrets, `contents: write` git push)"
  (round 20) is an accepted residual, "bounded by ADR080 environment gates + main-ref guards".
  Also: a workflow that ran an untrusted dispatched ref with the Hetzner token (fixed); unsigned
  installer checksums (fixed); mutable image references and unverified third-party executables
  in the production bootstrap (deferred or repeated).
* **Agent credentials.** Several high findings about model API keys reaching Git pushes or a
  caller-chosen endpoint, all fixed.

**Accepted residuals.** `onbex.co` is not on the Public Suffix List, so "a tenant can set a
parent-scoped cookie a sibling receives"; this was recorded as to be closed "before open signup"
and was still accepted when signup opened on 2026-09-15 (ADR084, ADR018; the list read on
2026-11-08 has no `onbex.co`). Also accepted: a routine operator credential that can exec into
any pod (ADR057, ADR084), persistent self-hosted CI runners (ADR083), and the runner-host
critical above.

**Shape.** No published advisory and a long internal record. The record shows real cross-tenant
defects found and fixed before outside users arrived, and a pace of change (about 2,500 commits
in October) that keeps producing them.

## Against Skifity

The Skifity column was checked in this repository on 2026-11-08 at `29147f1`. bex is a hosted
alpha that can also be self-hosted; Skifity is a released self-hosted panel (v0.1.0 on
2026-10-06, v0.1.1 on 2026-10-21) whose cluster-facing code runs on a real k3s on every push to
`main`.

| Capability | bex | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git providers | GitHub App; GitLab and Bitbucket partial | GitHub, GitLab, Gitea and Bitbucket webhooks and commit statuses (`internal/gitsrc/webhook.go`, `bitbucket_webhook.go`, `report.go`) | Skifity ahead |
| Builders | Buildpacks for six languages or a Dockerfile; BuildKit; Zot | Railpack, Nixpacks, Dockerfile, static directory; BuildKit Job; in-cluster registry (`internal/builder/detect.go`, `job.go`, `internal/registry`) | parity |
| Deploy without a repository | Image | Image, or a folder with `skifity up` (`internal/cli/up.go`, `internal/upload`) | Skifity ahead (folder) |
| Reading other platforms' files | `render.yaml` natively; browser converters from Heroku, Compose, Fly.io and Railway files | Compose as a stack (`POST /api/environments/{env}/stack`, Phase 101); `Procfile` and `app.json` (`internal/builder/heroku.go`); nothing reads `render.yaml`, `fly.toml` or `railway.json` (no match in `internal/` or `web/src`) | **behind** |
| CI deploy of a built image | `imageUrl` on the deploy call, digest-only sample workflow, deploy hooks | Deploy body is `commit_sha`, `force`, `accept_vulnerabilities` (`internal/api/deploy_handlers.go`); no deploy-hook route (`internal/api/api.go`); no CI section in `docs/cli.md`; `skifity apply` with a new `image:` works | **behind** |
| Monorepo filters | Build Filters | `watch` paths (`internal/gitsrc/paths.go`, `internal/blueprint/blueprint.go`) | parity |
| Step before rollout | Pre-deploy command | Release command (`internal/blueprint/blueprint.go` `Release`; `Procfile` `release`) | parity |
| Workers and cron | Service types | Processes from the same build (`internal/kube/process.go`); scheduled jobs (`/api/apps/{app}/jobs`, `internal/cron`) | parity |
| Rollback, cancel, promote, lock | Rollback, cancel | Rollback with a plan, cancel, promote between environments, deploy locks (`internal/api/api.go`, `internal/api/promote_handlers.go`) | Skifity ahead |
| Shutdown drain | `maxShutdownDelaySeconds` per service | Fixed 30 s grace and a preStop (`internal/kube/manifests.go`) | behind |
| **Domains, TLS and routing** | | | |
| Certificates | cert-manager; wildcard custom domains refused | HTTP-01, DNS-01 through connected DNS providers, the team's own certificates, redirects from one hostname to another (`internal/dnsprov`, `internal/tlscert`, `internal/api/domain_redirects.go`) | Skifity ahead |
| Shared app domain | `*.onbex.co`, not on the Public Suffix List (accepted) | `sslip.io` or `domains.wildcard`, one label per app under one parent (`internal/kube/naming.go` `autoHostname`); `sslip.io` is not on the list either; the panel's own cookies are `__Host-` (ADR-0019), apps' are not discussed (`docs/configuration.md`) | same residual, unsaid in Skifity |
| Edge rules | Inbound IP allowlist | IP, country and ASN rules, rate limits, password protection (`internal/edgerules`, `internal/guard`) | Skifity ahead |
| Maintenance mode | Yes | Yes (`internal/api/maintenance_handlers.go`) | parity |
| Static sites | Object-store origin; header rules, redirects, rewrites; cache purge | Caddy with a fixed `Caddyfile`, `try_files {path} /index.html` (`internal/builder/job.go`); no headers, redirects or 404 page | **behind** |
| **Databases** | | | |
| Engines | PostgreSQL, Valkey | Nine (`internal/dbsvc/engine/engine.go`) | Skifity ahead |
| PostgreSQL recovery | Daily base backup plus WAL, point-in-time recovery, drilled | Scheduled engine dumps, sealed and verifiable (`internal/backup`, `docs/backups.md`); no WAL archive | **behind** |
| PostgreSQL HA | CloudNativePG HA, failover action, named read replicas | CloudNativePG instances (`docs/databases.md`); only the `-rw` service is used (`internal/dbsvc/manifests.go`) | behind (no read endpoint) |
| Pooling, upgrades, extensions | PgBouncer, major upgrade, extensions, disk autoscaling | None in `internal/dbsvc`; CloudNativePG 1.29 is installed (`internal/cluster/components.go`) | behind |
| Reaching data | Public SNI proxy with IP allowlist; SQL console; query insight | Private; `skifity db connect` tunnel, import (`/api/databases/{db}/tunnel`, `/import`) | different by design |
| **Storage and backups** | | | |
| Disks | One per service, daily snapshots | Volumes with scheduled backups, verify and restore (`/api/apps/{app}/volumes/...` in `internal/api/api.go`) | parity |
| Panel backup | Control plane, etcd, OpenBao; age-encrypted; write-only credentials; drill dates | Panel backups to S3 with passphrase seal and verify (`/api/admin/panel/backups`); jobs get presigned URLs, never the key (`docs/backups.md`) | parity |
| **Scaling and HA** | | | |
| Instances and autoscaling | Manual, CPU or memory targets, ceiling 100 | Instances, HPA, readiness check that names what breaks (`internal/deploy/scaling.go`) | Skifity ahead |
| Idle sleep | Free tier; first request gets a 503 | Any app; KEDA's interceptor holds the request until an instance is ready (`internal/kube/scaletozero.go`) | Skifity ahead |
| Crash loops | Suspended after 24 h, email | `app.unhealthy` and a cause with a fix (`internal/notify/notify.go`, `internal/errdoc/catalogue.go` `CrashLoop`) | different |
| Machines | Cluster Autoscaler adds and removes Hetzner machines | Hetzner and DigitalOcean servers created on request (`internal/cloud`); nothing adds or removes one by itself | **behind** |
| **Observability** | | | |
| Logs | Query, tail, build logs | Live over SSE, previous container, build logs (`/api/apps/{app}/logs`, `/deployments/{id}/logs`) | parity |
| Request logs | Per request, filtered by status and path, edge 403s included | Totals, errors and latency from Traefik's metrics (`internal/traffic`); no per-request record | **behind** |
| Charts | Deploy markers on CPU and memory | Usage and traffic charts without deploys (`web/src/components/app/usage-card.tsx`, `traffic-charts.tsx`) | behind (small) |
| Alerts | Deploy and crash-loop email; status page | App and server thresholds, missed backups, expiring certificates (`/api/apps/{app}/alerts`, `/api/servers/{s}/alerts`, `internal/notify/notify.go`) | Skifity ahead |
| Log forwarding | Non-goal | Eight drain kinds (`internal/logdrain`) | Skifity ahead |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | Kratos: TOTP, passkeys, sessions, GitHub login | Argon2id, TOTP, passkeys, sessions (`internal/auth`, `/api/me/sessions`, `/api/me/passkeys`) | parity |
| SSO | Enterprise SSO, SAML, SCIM a non-goal | Generic OIDC with groups mapped to teams (`internal/api/sso_handlers.go`, Phase 92) | Skifity ahead |
| Roles | Viewer, developer, admin, billing | Owner, admin, member, viewer; memberships limited to projects (`internal/store/models.go`, Phases 85 and 86) | parity |
| Protected environments | Admin-only destructive and secret actions | None: `Environment` has no such field (`internal/store/models.go`) | **behind** |
| Account lifecycle | Self-service deletion with a preview | `users.disabled` is refused at every sign-in (`internal/auth/service.go`, `passkey.go`, `internal/api/sso_handlers.go`) but only tests ever set it; no delete; `skifity admin` lists users and resets passwords only (`internal/cli/admin.go`) | **behind** |
| Secrets | OpenBao; readable with `can_view_sensitive` | Sealed with context, write-only everywhere (`internal/crypto`) | Skifity ahead |
| Shell | SSH gateway, browser shell | None by decision; one-off `run` (`docs/roadmap.md`) | deliberate difference |
| Audit | In-app viewer, denied reads recorded | Team audit log (`/api/teams/{team}/audit`) | parity |
| **Preview environments** | Refused | Whole-environment previews, seeded, preview-only values, fork rules (`internal/api/preview_manual.go`, Phases 93 and 107) | Skifity ahead |
| **Templates** | 12 examples | 375 catalogue files, updates offered (`internal/templates/catalogue`, Phase 102) | Skifity ahead |
| **CLI, API, IaC, integrations** | | | |
| API | Render-shaped REST pinned to Render's OpenAPI, GraphQL, custom resources | One REST API with OpenAPI (`/api/openapi.json`) | different |
| MCP | 179 tools; remote with the MCP authorization flow; stdio | About 45 tools; stdio and `/api/mcp` with a pasted API token (`internal/mcpserver`, `docs/cli.md`) | behind on sign-in and breadth |
| Config as code | `render.yaml`, Git-connected, auto-sync | `skifity.yaml` plan and apply from the CLI (`internal/blueprint`); a push never applies it | behind (small) |
| Secret managers | Not found | Vault, AWS Secrets Manager, Doppler, Infisical (`internal/secretmgr`) | Skifity ahead |
| **Notifications** | | | |
| Channels | Email per member, browser and native push, inbox | Ten channel kinds, limited by project (`internal/notify/provider.go`) | Skifity ahead |
| Events for machines | Typed events, Standard Webhooks signatures, history, resend, auto-disable | Body `{title, body, level, url, fields}` with no event name; the shared secret itself in `X-Skifity-Secret`; two retries, no record (`internal/notify/notify.go` `sendWebhook`, `dispatcher.go`) | **behind** |
| **Multi-server and networking** | | | |
| Machines | Cluster API on Hetzner only | Any Linux server over SSH, or created at Hetzner or DigitalOcean (`internal/provision`, `internal/cloud`) | Skifity ahead |
| Between servers | Cilium WireGuard | Flannel WireGuard by default (`internal/settings/settings.go`) | parity |
| Tenant isolation | Namespace per workspace, default deny; probed against production weekly | Namespace per environment, default deny, network policy before code runs (`internal/kube/namespace.go`, `internal/netfence`) | parity |
| **Team and collaboration** | | | |
| Shared variables | Environment groups linked to any service | Per project (`/api/projects/{project}/variables`) | behind (small) |
| Clone | Not found | Clone an environment (`internal/api/envclone_handlers.go`) | Skifity ahead |
| **Developer experience** | | | |
| Install | No versioned artifact; kind plus Cluster API from source; six machines in production shape | One installer and one binary (`installer/install.sh`) | Skifity ahead |
| Languages | Dashboard in English and Chinese | Five, with a build gate (`web/src/locales`) | Skifity ahead |
| Before signing up | Fifteen browser tools | Detection inside the panel (`POST /api/teams/{team}/detect`), DNS check per domain | different |

## Gaps worth closing in Skifity

**P1**

1. **Import `render.yaml`, `fly.toml` and `railway.json` into `skifity.yaml`.** *What:*
   `skifity import render.yaml` (and `fly.toml`, `railway.json`, `railway.toml`) writes a
   `skifity.yaml` and a list of what did not carry over, and the new-app page and detection read
   the same files in a repository. *Why:* bex's whole on-ramp is that a Render user's file
   already works, and its tools page converts four more platforms' files. Skifity already reads
   Compose (Phase 101) and Heroku's `Procfile` and `app.json` (Phase 100), and has a file format
   of its own with plan and apply, but someone leaving Render, Fly.io or Railway retypes their
   settings. *Sketch:* a converter per format in `internal/blueprint` returning `File` plus
   notes as codes. For Render: `web` to an app, `pserv` to an internal app, `worker` to a
   process or an app without a port, `cron` to a job, static sites to the static builder;
   `buildCommand`, `startCommand` and `preDeployCommand` to `build`, `start` and `release`;
   `healthCheckPath`, `numInstances` and `scaling`; `buildFilter.paths` to `watch`; plain
   `envVars` to variables, `generateValue` and `sync: false` to secrets (generated or required),
   `fromDatabase` to a database link, `envVarGroups` to project variables; `databases` to
   PostgreSQL and `keyvalue` to Valkey; `disk` to a volume; `domains`; `plan` and `region`
   named and dropped. The same in the CLI (`internal/cli`), an MCP tool, and `detect`. *Size:*
   M. *Without a cluster:* yes.
2. **Deploy a CI-built image in one call, a deploy hook, and the CI recipe.** Still open from
   the Kamal research (`kamal.md`, "Since 2026-09-30"). *Why:* bex has `imageUrl` on the deploy
   call, a secret deploy-hook URL, and a downloadable GitHub Actions workflow that refuses
   anything but a digest. Skifity's deploy body has no image (`internal/api/deploy_handlers.go`),
   there is no hook route, and `docs/cli.md` has no CI section, so CI rewrites `skifity.yaml`
   and runs `skifity apply`. *Sketch:* `image` on `POST /api/apps/{app}/deploy`, recorded on the
   deployment so rollback restores it, with a warning or refusal for a tag that is not a digest;
   `skifity deploy --image`; a per-app hook token stored as a hash, shown once, regenerated from
   Settings, rate-limited, audited, and refused while deploys are locked; a GitHub Actions and a
   GitLab CI example in `docs/cli.md`. *Size:* S–M. *Without a cluster:* yes, and `make smoke`
   drives the CLI.
3. **Disable and delete accounts.** *Why:* `users.disabled` is checked at every way in
   (`internal/auth/service.go`, `passkey.go`, `reset.go`, `internal/api/sso_handlers.go`) and
   nothing outside tests sets it; there is no delete, and `skifity admin` only lists users and
   resets passwords (`internal/cli/admin.go`). An instance administrator can remove somebody
   from teams but cannot stop the account signing in, and any signed-in account can create a
   team (`POST /api/teams`). bex deletes an account with a preview and refuses while it is a
   workspace's only admin (ADR086). *Sketch:* an admin users page and `PATCH
   /api/admin/users/{id}` (disable revokes sessions and tokens at once), `DELETE` refused while
   the user is the only owner of a team, `skifity admin disable-user`, a self-service "Delete my
   account" on Account with the same rule, and audit entries. *Size:* S. *Without a cluster:*
   yes.

**P2**

4. **Signed, typed events for machines, with a delivery record.** *Why:* Skifity's webhook body
   is a chat message (`title`, `body`, `level`, `url`, `fields`) with no event name, id or time,
   and the shared secret is sent as itself in `X-Skifity-Secret`, so anybody who sees one
   request can forge the next (`internal/notify/notify.go` `sendWebhook`). Failures are retried
   twice and logged, with nothing for the operator to look at (`dispatcher.go`). bex signs with
   Standard Webhooks, publishes the event catalogue, keeps a delivery history with resend, and
   disables an endpoint that keeps failing. *Sketch:* add `type`, `id`, `timestamp` and resource
   ids to the webhook body; send `webhook-id`, `webhook-timestamp` and `webhook-signature`
   (HMAC-SHA256 over `id.timestamp.body`); keep `X-Skifity-Secret` for one release and say so;
   keep the last deliveries per channel with status and a resend button. *Size:* S–M.
   *Without a cluster:* yes.
5. **Point-in-time recovery for PostgreSQL.** *Why:* a nightly dump loses up to a day of
   writes; bex keeps a base backup plus WAL for 30 days and has drilled the restore. Skifity
   already runs PostgreSQL through CloudNativePG (`internal/dbsvc/lifecycle.go`) but backs up
   with dumps (`internal/backup`). *Sketch:* CloudNativePG's WAL archive to the team's bucket
   under a per-database prefix, a "restore to a time" action that creates a new database, and
   the existing verify. One design point first: WAL archiving needs a credential inside the
   cluster for as long as the database runs, which `docs/backups.md` promises never happens
   ("The credentials never leave the panel"). It needs a key limited to that prefix where the
   store can issue one, and a sentence in the docs that changes the promise for this case.
   *Size:* L. *Without a cluster:* manifests only; archiving and recovery need one.
6. **Protected environments.** *Why:* a member who may deploy staging may also change
   production's variables or delete its database in the same project; Skifity limits members by
   project (Phase 86), not by environment, and `Environment` has no protection field
   (`internal/store/models.go`). bex follows Render: in a protected environment only admins
   create, move or delete resources, change variables and files, or turn maintenance on.
   *Sketch:* a `protected` flag on an environment (production by default for new projects),
   checked in `authorizeApp`, `authorizeEnvironment` and `authorizeDatabase` for a fixed list of
   actions, with a coded `errdoc` refusal, the route walk extended, and controls disabled in the
   interface with the reason. Deploys and rollbacks stay open, as in bex. *Size:* S–M.
   *Without a cluster:* yes.
7. **Per-request logs.** *Why:* "which path returns 500" needs requests, not totals; Skifity
   draws requests, errors and latency from Traefik's metrics (`internal/traffic`) and has no
   per-request record. bex filters request logs by status and path and includes the edge's own
   refusals. *Sketch:* Traefik's JSON access log, read by the panel per app, with status, path
   and time filters on an app tab; the guard's refusals in the same list; a short retention.
   *Size:* M. *Without a cluster:* the parser and filters, yes; Traefik's output needs one.
8. **The MCP authorization flow.** *Why:* `/api/mcp` takes a pasted API token as a header
   (`docs/cli.md`, "Without installing anything"). A client that only implements the MCP
   authorization flow cannot connect, and every other client keeps a long-lived token in its
   configuration. bex answers an unauthenticated call with a 401 and RFC 9728 metadata, lets the
   client register itself, runs PKCE through its own sign-in, and shows a consent page with the
   client's name and scopes. *Sketch:* the two `.well-known` documents, dynamic client
   registration with strict redirect rules, an authorize page for a signed-in user that picks
   read or write and projects, and a token endpoint that issues the existing scoped tokens
   (`internal/auth/scopes.go`) with an expiry and refresh, listed and revocable under Account.
   *Size:* M. *Without a cluster:* yes.
9. **Headers, redirects and a real 404 for static sites.** *Why:* the static builder writes one
   `Caddyfile` for everybody, `try_files {path} /index.html` (`internal/builder/job.go`), so
   every missing path answers 200 with the home page, and nobody can set a cache or security
   header or a redirect. bex has header rules, redirects and rewrites. *Sketch:* read
   `_redirects` and `_headers` (the format many static generators already write) and a
   `static:` block in `skifity.yaml`; render them into the `Caddyfile`; a single-page-app switch
   that defaults on for the frameworks that need it and serves `404.html` otherwise. *Size:*
   S–M. *Without a cluster:* the rendering and tests, yes.
10. **Say what a shared app domain allows.** *Why:* every app's automatic address is one label
    under the same parent (`internal/kube/naming.go`), and neither `sslip.io` nor a typical
    operator's wildcard domain is on the Public Suffix List, so an app can set a cookie that
    another team's apps receive. ADR-0019 protects the panel's cookies only. bex accepted the
    same risk and wrote it down. *Sketch:* a paragraph in `docs/configuration.md` beside the
    wildcard setting, the same in the setting's help, advice to apps to use `__Host-` cookies,
    and optionally a wildcard domain per team for panels that host teams who do not trust each
    other. *Size:* S. *Without a cluster:* yes.

**P3**

11. **Deploys on the charts.** Draw each deployment as a marker with its number and commit on
    the usage and traffic charts (`web/src/components/app/usage-card.tsx`,
    `traffic-charts.tsx`), from the deployments list the app page already reads. S, no cluster.
12. **A read-only address, a pooler and major upgrades for PostgreSQL.** Give an app with a
    multi-instance database the CloudNativePG `-ro` service as a second variable; offer a
    CloudNativePG `Pooler`; move a database to a new major version with CloudNativePG's
    in-place upgrade, after a backup. `internal/dbsvc`. M; needs a cluster.
13. **Apply `skifity.yaml` on a push that changes it**, after showing the plan on the
    deployment and only for the environment the branch deploys. bex's Blueprints sync from Git;
    today it is CLI-only (`internal/blueprint`). M; mostly without a cluster.
14. **A server pool that grows and shrinks.** When instances stay unschedulable for lack of CPU
    or memory, create a server from a saved provider and type (the path in `internal/cloud`
    exists); remove a server that has run no app for a set time. bex does this with the Cluster
    Autoscaler. L; needs a cluster and a provider account.
15. **A drain time per app**, still open from the Kamal research: the grace is a fixed 30 s
    (`internal/kube/manifests.go`). S, no cluster.
16. **Variable groups shared across projects**, linked to apps by name, beside project
    variables. `internal/api`, `internal/deploy`. S–M, no cluster.

## Things to deliberately not copy

* **Another product's API as the top principle.** bex's rule is "Render parity first", above
  "the platform's earlier tenant-hardening choices"; "ADR022's drop-ALL hardening" was the
  first decision amended to match Render (ADR008). Reading another platform's files is a door;
  wearing its API ties every decision to someone else's roadmap.
* **A platform that needs a platform to install.** Terraform, a bootstrap cluster, Cluster API,
  Argo CD, Ory, OpenFGA, OpenBao and six machines are reasonable for a hosted service and the
  opposite of one binary on one small server.
* **An interactive shell into instances.** bex built an SSH gateway and a browser shell and then
  had to restrict both in protected environments. Skifity's one-off `run` reasoning in
  `docs/roadmap.md` still holds.
* **Secrets that can be read back**, even behind a capability. Keep them write-only.
* **Sharing an app domain among strangers without saying so.** bex wrote the risk down and
  opened signup with it unresolved; Skifity should at least write it down (gap 10).
* **Answering the first request after sleep with a 503.** KEDA's interceptor holds the request;
  keep that.
* **Suspending an app that crash-loops.** On a hosted platform it saves money; on a server the
  owner pays for, it stops something the owner may be debugging. A notification is enough, and
  Skifity has one.
* **Change faster than anyone reads it.** About 6,600 commits in five months from one person
  with agents; 22 review records, largely from an automated scanner, rated 33 findings high or
  critical in seven weeks. Skifity's roadmap asks for a review by somebody who did not write the
  code; a scanner reading what an agent wrote is not that.
* **Several products under one name.** Hosting, sandboxes, coding-agent sessions, a mobile app,
  blockchain RPC and security scanning share a brand and a repository. Skifity is one thing.

## Sources

All read on 2026-11-08.

* https://github.com/bex-co/bex (repository metadata through the GitHub search API:
  `search/repositories?q=repo:bex-co/bex`; the repository API refused this sandbox)
* A blobless clone of https://github.com/bex-co/bex.git at `348e099`: `README.md`, `AGENTS.md`,
  `CONTRIBUTING.md`, and in its `docs` folder `ADR002-architecture.md`, `ADR008-vision.md`,
  `ADR018-render-parity.md`, `ADR024-members.md`, `ADR026-github-integration.md`,
  `ADR027-sso.md`, `ADR028-security-review.md` and every `ADR0*-security-review-*.md`,
  `ADR032-environments.md`, `ADR058-release-engineering.md`, `ADR074-workspace-scoped-artifact-identity.md`,
  `ADR082-persistent-disks.md`, `ADR096-crashloop-suspension.md`, `bex-cli.md`,
  `lego/cli/UPSTREAM_RENDER_CLI.md`, `infra/README.md`,
  `infra/clusterapi/overlays/hetzner-caph/cluster.yaml`, `examples/`,
  `dashboard/src/common/locales/`, `git log` of the last 50 commits, `git ls-remote --tags`
* GitHub search API: commit counts per month (`search/commits?q=repo:bex-co/bex+committer-date:…`),
  the first commits (2026-06-05), issues and pull requests (`search/issues?q=repo:bex-co/bex…`),
  and the bodies of #67, #113, #127 and #135
* https://bex.co (home page)
* https://bex.co/pricing
* https://bex.co/changelog
* https://bex.co/blog/2026/09/15/bex-public-alpha-open-source-render-alternative
* https://bex.co/tools
* https://bex.co/docs (index), https://bex.co/docs/platform/migrate-from-render,
  https://bex.co/docs/platform/ci-image-deploy, https://bex.co/docs/platform/idle-services,
  https://bex.co/docs/platform/notifications
* https://api.osv.dev/v1/query for `github.com/bex-co/bex` (no entries)
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=bex-co (zero results)
* https://publicsuffix.org/list/public_suffix_list.dat (neither `onbex.co` nor `sslip.io` listed)
* https://hn.algolia.com/api/v1/search and /search_by_date for `bex.co`, `bex-co/bex` and
  `bex.co` restricted to URLs (no relevant hits)
* https://www.reddit.com/search.json (refused with 403); the web search tool was out of credits,
  so no other third-party discussion was looked for
