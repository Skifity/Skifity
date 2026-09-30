# DigitalOcean App Platform

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

App Platform is DigitalOcean's managed platform-as-a-service, launched on
2020-10-06 (HN 24698334): it builds from GitHub, GitLab, Bitbucket or a
container registry and runs the result as **components** of an app — services
(HTTP), workers (background), jobs (before or after a deploy, on failure, or on
a cron schedule), static sites and functions — described by one YAML **app
spec**. It is for developers and small teams already on DigitalOcean who want a
Heroku-like experience next to DO's Managed Databases and Spaces. It is a
closed, hosted product; DigitalOcean describes it as a Kubernetes-managed
container runtime (TechTarget, 2021), builds use Cloud Native Buildpacks or a
Dockerfile on a builder with 8 CPUs, 15 GiB of memory and a one-hour limit,
Cloudflare's CDN sits in front of every app, and a 2021 review traced its poor
file I/O to a gVisor sandbox (whether that is still the runtime is not
documented). Its own state lives in DigitalOcean's control plane; what a user
sees is the app spec, which can be downloaded, edited and re-applied. Pricing:
three static-site apps free; shared containers at $5 (512 MiB) and $10 (1 GiB)
fixed, $12/$25/$50 for sizes that can scale out; dedicated CPU from $29 to
$392/month; per-second billing with a one-minute minimum; egress beyond the
included allowance at $0.02/GiB; a $7 development database; a dedicated egress
IP up to $25/month; a scaled-to-zero service billed at 10%. Maturity: six
years old, with steady 2025-2026 releases (scheduled jobs GA 2025-09-30, VPC
GA 2025-09-09, request-based autoscaling GA 2026-05-20, scale to zero in
private preview since 2026-03-25). DigitalOcean does not publish App Platform
usage; the company reported $1,125M annual run-rate revenue for Q2 2026. The
CLI, `doctl`, is Apache-2.0 with 3,455 stars and 169 open issues; the Terraform
provider has 568 stars. (Read 2026-09-30; sources at the end.)

## Feature inventory

### Deploy sources and builds

* Sources per component: `github`, `gitlab`, `bitbucket` (GA 2025-01-21),
  plain `git` (public clone URL), or `image` (DOCR, Docker Hub, GHCR). Deploy on
  push per branch; monorepos through `source_dir`.
* Buildpacks for Node.js, Python, Go, PHP, Ruby, Hugo, .NET (2026-01-20), Rust
  (2026-02-06), Bun (2025-10-29) and static sites, or a Dockerfile; build and
  run commands overridable; an "XL build" size in private preview (July 2025).
* `doctl app dev build` runs the **same buildpack or Dockerfile build on a
  laptop** from the app spec, and can push to DOCR for CI use.
* No upload of a local folder without Git or a registry.

### Domains, TLS and routing

* A `*.ondigitalocean.app` starter domain (can be disabled since April 2025),
  up to 500 custom domains per app with managed TLS, subdomain routing (GA
  2025-06-27), path-based routes, rewrites and redirects, CORS, custom error
  pages, HTTP/2 on request.
* Cloudflare CDN built in, with cache controls, Layer 7 DDoS protection and
  email obfuscation (edge controls GA 2025-08-07); an external CDN is
  supported for custom rate limiting or bot rules.
* Services listen on HTTP; `internal_ports` for private HTTP between
  components (`http://web:3000`); workers and jobs cannot receive traffic; SSH,
  SFTP and SMTP ports cannot be opened.
* Dedicated egress IPs (up to $25/month per app); VPC attachment (GA
  2025-09-09) to reach Droplets and databases privately.

### Databases and services

* A `databases` block attaches DigitalOcean Managed Databases (PostgreSQL,
  MySQL, Redis/Valkey, MongoDB, Kafka, OpenSearch) or a $7 PostgreSQL
  **development database** that lives inside the app.
* Connection details arrive as **bindable variables** (see CLI, API, IaC)
  rather than copied strings.

### Storage and backups

* **No persistent storage at all.** "App Platform does not currently support
  volumes"; the local filesystem is ephemeral and capped at 4 GiB, and "if it
  is filled to capacity, the container is detected as unhealthy and replaced."
  Files go to Spaces (S3) and data to Managed Databases.
* Backups are a Managed Databases feature, outside App Platform.

### Scaling and high availability

* Manual horizontal scaling up to 250 containers; vertical by changing size.
* **CPU autoscaling only on dedicated-CPU plans**; **request-based autoscaling**
  (requests per second per instance and P95 latency) on shared or dedicated
  plans, up to 100 containers (GA 2026-05-20).
* **Scale to zero** (`inactivity_sleep.after_seconds`, 600-86,400 s) for web
  services reachable through the app's hostname; internal traffic neither keeps
  it awake nor wakes it; cold start "typically takes several seconds"; billed
  at 10% while asleep; cannot be combined with request-based autoscaling;
  private preview since 2026-03-25.
* Readiness health checks (HTTP or TCP) and liveness probes (GA 2025-06-30).

### Observability (logs, metrics, alerts, uptime)

* Build and deploy logs kept 90 days; **runtime logs are not retained** unless
  forwarded; crash logs via the CLI only.
* Log forwarding to Managed OpenSearch, OpenSearch, Datadog, Better Stack (and
  Papertrail in the spec).
* Insights: CPU and memory percentage per component, also via the monitoring
  API.
* Alerts: deployment started/failed/cancelled/succeeded, domain failed/ok,
  failed to scale, failed job invocation; metric alerts on CPU, RAM, restart
  count, request rate and P95 duration; delivered by **email or Slack**.
  Failed-deployment and failed-domain email alerts are on by default.

### Security, auth, roles, SSO, audit

* DigitalOcean Teams: predefined roles (owner, biller, billing viewer, member,
  modifier, resource viewer) and **custom roles** from fine-grained
  permissions; SSO for teams with IdP attributes mapped to roles.
* Environment variables marked **Encrypt** are hidden from logs and stored in
  the app spec as ciphertext (`EV[1:…]`), so a spec can be exported and
  re-applied without exposing the value.
* A bug bounty on Intigriti; DigitalOcean's security pages cover the platform.

### Preview environments and branches

* **Not native.** The official `digitalocean/app_action` (156 stars) has a
  `deploy_pr_preview` mode (September 2024): one app per pull request, the spec
  "sanitized" of domains and alerts, branch references rewritten, a PR comment
  with the URL and logs, and a delete action on close. Databases in a preview
  are whatever the spec says.
* **Clone app** (2025-11-04) copies an app's configuration into a new app for
  staging or a feature branch, with the database configuration editable.

### Templates and catalogue

* Sample apps per language and a **"Deploy to DigitalOcean" button** that
  launches an app from a repository's spec. No catalogue of third-party
  self-hosted applications comparable to the Droplet Marketplace.

### CLI, API, IaC, integrations

* **The app spec is the source of truth**: download it from the panel, edit it
  in the panel's spec editor, keep it as `.do/app.yaml`, apply it with
  `doctl apps create|update --spec`, the API, Terraform (`digitalocean_app`)
  or the GitHub Action. Every setting in this document has a spec field.
* **Bindable variables**: `${APP_URL}`, `${APP_DOMAIN}`, `${_self.PRIVATE_URL}`,
  `${web.PUBLIC_URL}`, `${db.DATABASE_URL}` and others are resolved at deploy
  time, so references follow the resource instead of copying its value.
* App-level variables shared by all components; component variables with
  `RUN_TIME`, `BUILD_TIME` or `RUN_AND_BUILD_TIME` scope.
* A browser console into a running component (and `/exec` in the API).
* Remote MCP endpoints (2025-12-09) per DigitalOcean service, including
  `apps.mcp.digitalocean.com` for App Platform; the earlier local
  `digitalocean/digitalocean-mcp` repository is archived in favour of
  `digitalocean-labs/mcp-digitalocean` (139 stars).

### Notifications

* Email and Slack for app and metric alerts; failed deployment and failed
  domain configuration alert by default.

### Multi-server and networking

* An app lives in one of 13 listed regions (NYC, AMS, SFO, SGP, LON, FRA, TOR,
  BLR, SYD, ATL, RIC, MKC, MEM) and can change region; no multi-region app.
* Components of one app share a private network by name; VPC reaches the
  rest of the account.

### Team and collaboration

* DO Teams and Projects, with **environment tagging** on apps (November 2025),
  role-based access as above.

### Developer experience and onboarding

* Create an app by pointing at a repository; the spec is generated and shown.
* **Maintenance mode** takes an app offline behind a default or custom
  offline page (custom page since 2025-02-19); billing continues.
* **Archive** stops an app and its charges (except databases and egress IPs),
  keeps its configuration, serves an offline page, and restores later (GA
  2025-06-27).
* **Rollback** to any of the last ten successful deployments restores "code,
  configuration, and app spec" and shows a **diff** of source and settings first,
  with an option to switch off deploy-on-push "until the cause for the rollback
  is resolved".

## What users love

* **The clearest entry pricing** among the managed options: "the clearest
  low-end production entry point in this comparison" (Render's comparison,
  2026-07-20 — a competitor, but the point is DO's pricing page).
* **It fills the gap between Heroku and managing a server** (HN launch thread,
  2020: "the choice is either Heroku or manage your own server").
* **Everything in one account**: Managed Databases, Spaces and a CDN beside the
  app, and a spec that can be downloaded, versioned and re-applied.
* **Rollback that shows what it will change**, and per-second billing.

## What users complain about

* **No volumes, for five years.** The idea "Allow App Platform apps to use
  block storage volumes" has been open since 2021-09-10 (35 votes, "under
  review"): "this feature is already available on the DigitalOcean Kubernetes
  platform", "the 2gb disk limit makes it almost unusable for any project that
  allows users to upload large files". A 2024 question, "Deploying app wipes out
  sqlite DB", is the same complaint from the other end.
* **Slow and stuck builds.** "Deployment always takes about 20 minutes+ for a
  simple nestjs API" (2021) through "deployment is hanging for almost 1 hour on
  Build step" (2026-02-11); a Node.js buildpack failure broke builds globally
  for about 11.7 hours on 2026-02-26/27, and deployments degraded globally on
  2026-01-20 for 20 minutes.
* **Price above the entry tier.** Since the launch thread (bandwidth overage
  at the time, database minimums) through today's structure: CPU autoscaling
  requires a $29+ dedicated plan, a dedicated egress IP is $25/month per app,
  and larger sizes are "very expensive compared to competitors" (community
  and review sites).
* **Fragmentation.** Databases, storage and networking live in other DO
  products, which "creates cross-product glue work" (Render, 2026) and is why
  the lack of volumes bites.
* **Cold starts and slow first loads** after inactivity (community question
  "app platform slow startup"), and historically poor filesystem performance
  attributed to gVisor ("file opens are 216× slower", siipo.la, 2021).
* **Runtime logs disappear** unless forwarded to another service.

## Security record

* **No CVE or advisory specific to App Platform or `doctl` was found**
  (searched NVD/OpenCVE for "digitalocean" and the web, 2026-09-30).
  DigitalOcean-wide CVEs in the period concern other products: CVE-2026-24516
  (Droplet Agent through 1.3.2, command injection as root from metadata, CVSS
  8.8) and CVE-2025-59717 (the `@digitalocean/do-markdownit` package, CVSS 5.4).
  DigitalOcean listed App Platform "Not Affected" by regreSSHion
  (CVE-2024-6387).
* DigitalOcean runs a public bug bounty on Intigriti.

## Against Skifity

**Works** has a test that runs on every push; **Written** has never run on a
real cluster (ADR-0010, `docs/checklist.md`).

| Capability | App Platform | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy sources and builds | GitHub, GitLab, Bitbucket, git URL, images; buildpacks or Dockerfile; local build with the same builder | GitHub, GitLab, Gitea (`internal/gitsrc/hooks.go`), images, **folder upload with no Git** (`internal/cli/up.go`, `PUT /api/apps/{app}/source`), Railpack/Nixpacks/Dockerfile in-cluster (`internal/builder`); no local build command. Written | Parity (Skifity ahead on no-Git upload) |
| Component model | Services, workers, jobs (pre/post/failed/scheduled), static sites, functions in one app | An app is one process (`store.App`); a worker is an app with no domain (`internal/kube/manifests.go:250`); static sites (`StaticDir`, `internal/builder/detect.go`); release command = pre-deploy (`internal/deploy/run.go:130`); scheduled commands (`internal/store/apps.go:676`); **no post-deploy or failed-deploy job**. Written | Behind |
| Domains, TLS, routing | Starter domain, 500 domains, CDN, rewrites/redirects, edge DDoS | cert-manager, `sslip.io` (ADR-0015), path per domain, HTTP-to-HTTPS redirect middleware for domains with a certificate (`internal/kube/manifests.go:279-282`), no rewrites or CDN, firewall on IP/country/ASN/path/header (`internal/edgerules`). Written | Behind on CDN/rewrites, ahead on firewall |
| Databases and services | Attach Managed Databases or a $7 dev DB; bindable variables | Postgres (CloudNativePG, HA), MySQL, Redis in the same cluster (`internal/dbsvc`); link copies the connection string into a secret variable (`internal/dbsvc/manager.go:347-353`). Written | Parity; behind on references |
| Storage and backups | **No volumes**; 4 GiB ephemeral disk | Volumes as PVCs, optional Longhorn replication, scheduled backups of volumes and databases to the user's S3 with restore (`internal/backup`, ADR-0012). Written | Skifity ahead |
| Scaling and HA | Manual to 250; CPU autoscale on dedicated; request-based autoscale; scale to zero (preview, idle 10 min-24 h) | HPA on CPU/memory, scale-to-zero through KEDA with a fixed 300 s idle (`internal/kube/scaletozero.go`), scaling readiness checker (`internal/deploy/scaling.go`). Written | Parity; behind on request-based and idle setting |
| Observability | Build/deploy logs 90 days, runtime logs only if forwarded, 4 destinations, CPU/memory insights, metric alerts | Build logs stored per build (last 20 kept, `docs/checklist.md` item 2); runtime tail from kubelet (`internal/api/stream_handlers.go`); current usage only; no forwarding, no metric alerts | Behind |
| Security, auth, roles, SSO, audit | Team roles incl. resource viewer, custom roles, SSO; encrypted spec values | Argon2id, TOTP, OIDC SSO, 3 roles, scoped team-bound tokens, audit, sealed write-only secrets, route-walk tenant test (**Works**); per-app password in front of an app (commit `7865bc8`, Written) | Parity; behind on custom/viewer roles |
| Preview environments | GitHub Action per PR; clone app | Built in per PR/branch, an empty database of its own per linked database, fork previews get no app secrets, cleanup on close, commit status and PR comment (`internal/api/webhook_handlers.go`, `internal/deploy/gitreport.go`). Written; one open defect, shared secrets reaching fork previews, is the P0 in `northflank.md` | Skifity ahead |
| Templates and catalogue | Sample apps, Deploy button | 282 one-click apps (`internal/templates/catalogue`) | Skifity ahead |
| CLI, API, IaC | App spec everywhere, `doctl`, Terraform, GitHub Action, remote MCP | API, 17-command CLI, MCP server with 15 tools, `skifity export` (**Works**), but `skifity.toml` stores only ids (`internal/cli/project.go:18-31`) and nothing reads a spec back in | Behind on config-as-code; ahead on MCP depth for deploy/debug |
| Notifications | Email, Slack | Telegram, Discord, webhook, email, plugin kinds (`internal/notify/notify.go`). Written | Parity |
| Multi-server and networking | One region per app, 13 regions, VPC | Several servers in one k3s cluster, namespaces with default-deny (`internal/kube/namespace.go`). Written | Different category |
| Team and collaboration | DO Teams, projects, environment tags | Teams, projects, environments, invitations, audit. Works | Parity |
| Developer experience | Spec generated on create, maintenance mode, archive, rollback with diff | Detection before first build, cause/impact/fix errors, five languages; rollback is one click with no preview of what changes (`web/src/components/app/deployments-tab.tsx:54-156`); no maintenance mode or archive; no release tagged | Behind |

## Gaps worth closing in Skifity

### P1 — A declarative app spec that lives in the repository

* **What.** An optional `skifity.yaml` (or a `[app]` section in the existing
  `skifity.toml`) describing one or more apps — source, build, start and
  release commands, port, health path, instances or autoscaling, volumes,
  scheduled commands, domains, linked databases — that `skifity deploy`
  applies, the panel can show and download for any app, and a deploy from Git
  reads if present. Secrets appear only as names (or as sealed values, the way
  DO stores `EV[1:…]`), never in clear text.
* **Evidence.** DO's app spec, Fly's `fly.toml` and Northflank's templates all
  make configuration a file. The most specific complaint against Fly was that
  it is "not declarative enough … you still have to run quite a few commands"
  (rdrn.me, 2024). Skifity's `skifity.toml` stores an app id, a name, an
  environment id and a panel URL and nothing else (`internal/cli/project.go`),
  and `skifity export` writes JSON that nothing imports.
* **Fit.** Skifity already has a declarative format for exactly this shape:
  the template catalogue YAML (`internal/templates/catalogue/*.yaml`, services
  with ports, health paths, variables, volumes, resources, plus databases with
  `link_to`). Reusing it as the user's spec means one parser and one set of
  validation errors. Apply is a diff against the store through the existing
  API, so authorization stays in `internal/api`; the build fingerprint
  (ADR-0007) decides whether applying the spec rebuilds.
* **Size.** L.
* **Without a cluster?** Yes: parse, validate, diff and apply-to-store are unit
  tests; the CLI path fits `make smoke`; the panel's "download spec" fits the
  Playwright test.

### P1 — Rollback that says what it will change, and pauses deploy-on-push

* **What.** Rolling back opens a dialog listing what differs between the
  running deployment and the target — image and commit, and each setting in the
  stored runtime spec — with a checkbox to turn off deploy-on-push until
  someone turns it back on.
* **Evidence.** DO's rollback shows "a comparison between the current
  deployment and the selected deployment" and offers to disable automatic
  deploys. Skifity's rollback is a single button that fires immediately
  (`web/src/components/app/deployments-tab.tsx:54-156`), and the next push to
  the watched branch redeploys the broken code the rollback just removed.
* **Fit.** `store.Deployment` already carries `Image`, `CommitSHA` and
  `RuntimeSpec`; a `GET /api/apps/{app}/deployments/{id}/diff` in
  `internal/api/deploy_handlers.go`, secret keys shown by name only, and
  `AutoDeploy` set false in the same request as the rollback.
* **Size.** S.
* **Without a cluster?** Yes, including a Playwright check of the dialog.

### P1 — Variables that reference, not copy

* **What.** Variable values may contain `${db-name.URL}`, `${APP_URL}`,
  `${other-app.PRIVATE_URL}`, resolved at deploy time from the current state.
  Linking a database writes the reference rather than the password.
* **Evidence.** DO's bindable variables; Northflank links addon credentials
  into secret groups; Railway's reference variables. Skifity copies the
  connection string into a secret variable at link time
  (`internal/dbsvc/manager.go:347-353`), so a rotated password leaves a stale
  copy behind. The copy is also why previews pointed at the production
  database until commit `1ae3276` (2026-09-30), whose fix had to teach
  `copyPreviewVariables` which variables a link wrote (`linked[row.Key]` in
  `internal/api/webhook_handlers.go`); every future feature that copies an app
  (clone, promote, spec export) has to remember the same exception unless the
  variable is a reference.
* **Fit.** Resolution in `runtimeVariables` (`internal/deploy/deployer.go:728`),
  a missing reference as an errdoc problem with a fix, the variables tab showing
  what a reference currently resolves to (never a secret value).
* **Size.** M.
* **Without a cluster?** Yes.

### P1 — Jobs after a deploy and after a failed one

* **What.** Besides the release command (before traffic), an app can run a
  command after a successful deploy (cache warm-up, a Slack post from the app,
  search reindex) and after a failed one (cleanup, a compensating migration).
* **Evidence.** DO's job kinds `PRE_DEPLOY`, `POST_DEPLOY` and
  `FAILED_DEPLOY`, chosen from one menu ("Before every deploy", "After every
  successful deploy", "After every failed deploy").
* **Fit.** Two optional fields beside `ReleaseCommand` in `store.App`, run
  through the existing one-off Job path (`internal/kube/runjob.go`) from
  `internal/deploy/deployer.go`; their output appears in the deployment log.
* **Size.** S-M.
* **Without a cluster?** Ordering and state handling yes; running them no.

### P1 — Clone an app or an environment

* **What.** "Clone to…" on an app or on a whole environment creates the same
  apps, variables (resealed), volumes (empty), scheduled commands and database
  definitions in another environment, with a form for the few things that
  should differ (branch, domain, size).
* **Evidence.** DO added clone app on 2025-11-04 for "a staging or testing
  instance based on a production app" and "parallel feature-branch instances";
  Northflank's team templates exist largely to stamp out environments.
  Skifity can create an environment but not fill it.
* **Fit.** The preview code already clones an app into a new environment,
  reseals its variables and gives the copy empty databases of its own instead
  of production's (`deployPreview`, `copyPreviewVariables` and
  `previewDatabases` in `internal/api/webhook_handlers.go`); promoting that into
  `POST /api/apps/{app}/clone` and `POST /api/environments/{env}/clone` reuses
  it, with a choice of size for the new databases.
* **Size.** M.
* **Without a cluster?** Yes for the store and API; the rollout needs one.

### P2 — Maintenance mode

* **What.** A switch that serves an offline page (default or a custom URL or
  HTML) for an app's domains while the app keeps running or is scaled down,
  for migrations that cannot be done live.
* **Evidence.** DO maintenance mode (custom page since 2025-02-19) and archive
  offline pages.
* **Fit.** The edge guard (`internal/guard`) already answers Traefik before
  the app; a maintenance flag in its ConfigMap answering 503 with a page is
  the smallest version. Scale-to-zero's interceptor path must be considered.
* **Size.** M.
* **Without a cluster?** The guard's answer yes; the Traefik path no.

### P2 — Archive an app or an environment

* **What.** Stop every instance, keep configuration, volumes and images, show
  it as archived, restore with one click.
* **Evidence.** DO archive (GA 2025-06-27) "for seasonal applications,
  temporary projects, and creating staging environments". On a self-hosted
  cluster the saving is memory, which is the resource that runs out first.
* **Fit.** Replicas 0 is already allowed (`internal/api/deploy_handlers.go:302`);
  archive is that plus a remembered previous state, a status the panel shows,
  and the registry garbage collector (`internal/registry`) told to keep the
  archived app's image.
* **Size.** S.
* **Without a cluster?** Mostly.

### P2 — Scale to zero: a configurable idle period

* **What.** Let the user choose how long an app may be idle before it sleeps.
* **Evidence.** DO allows 10 minutes to 24 hours; Fly lets the proxy decide
  from concurrency limits. Skifity hardcodes five minutes
  (`ScaleDownAfterSeconds = 300`, `internal/kube/scaletozero.go`).
* **Fit.** A field on the app, passed into the HTTPScaledObject's scaledown
  period; the scaling tab.
* **Size.** S.
* **Without a cluster?** Yes for the rendering; the behaviour needs one.

### P2 — Build on the laptop with the same builder

* **What.** `skifity build` runs the same detection and Railpack/Dockerfile
  build locally against Docker or BuildKit, so "it fails in the panel" can be
  reproduced in a terminal.
* **Evidence.** `doctl app dev build` exists for exactly this and for CI.
* **Fit.** `internal/builder` already produces the build plan; the CLI would
  call the local daemon instead of rendering a Job.
* **Size.** M.
* **Without a cluster?** Yes, but it needs Docker or BuildKit on the machine.

(Request-based autoscaling and metric alerts, where DO is also ahead, are
written up in `northflank.md` to avoid listing them twice.)

## Things to deliberately not copy

* **No persistent storage.** DO's biggest limitation for five years, and the
  reason SQLite apps lose their data on deploy. Skifity's volumes, and the
  scaling checker that warns about SQLite on a local disk, are an advantage to
  keep, not to trade for simplicity.
* **A preview workflow that lives in CI.** DO's previews exist only in a
  GitHub Action that the user wires up; Skifity's built-in previews are better
  and should stay in the panel.
* **Autoscaling as a paid tier.** DO ties CPU autoscaling to dedicated
  plans. On a self-hosted cluster there is no tier; the constraint is whether
  metrics exist, and the panel should say that instead.
* **Runtime logs that vanish unless shipped elsewhere.** A default that loses
  the evidence of a crash is the wrong default; if Skifity keeps logs, it should
  keep them without requiring a third-party account.
* **Splitting the product across many consoles.** Much of DO's friction is that
  databases, storage and networking are separate products. Skifity's single
  panel (apps, databases, volumes, backups, domains together) is the right
  shape.
* **A "spec must completely define all of your app's configurations" update
  model without a diff.** DO asks users to download the spec, edit it and
  submit it whole; a partial or stale spec silently removes things. A Skifity
  spec should always show what applying it will change first.

## Sources

All read 2026-09-30.

* https://www.digitalocean.com/pricing/app-platform
* https://docs.digitalocean.com/products/app-platform/details/pricing/
* https://docs.digitalocean.com/products/app-platform/details/limits/
* https://docs.digitalocean.com/products/app-platform/details/availability/
* https://docs.digitalocean.com/release-notes/app-platform/
* https://docs.digitalocean.com/products/app-platform/reference/app-spec/
* https://docs.digitalocean.com/products/app-platform/how-to/scale-app/
* https://docs.digitalocean.com/products/app-platform/how-to/scale-to-zero/
* https://docs.digitalocean.com/products/app-platform/how-to/manage-jobs/
* https://docs.digitalocean.com/products/app-platform/how-to/manage-deployments/ (rollback)
* https://docs.digitalocean.com/products/app-platform/how-to/maintenance-mode/
* https://docs.digitalocean.com/products/app-platform/how-to/archive-restore/
* https://docs.digitalocean.com/products/app-platform/how-to/clone-app/
* https://docs.digitalocean.com/products/app-platform/how-to/build-locally/
* https://docs.digitalocean.com/products/app-platform/how-to/console/
* https://docs.digitalocean.com/products/app-platform/how-to/use-environment-variables/
* https://docs.digitalocean.com/products/app-platform/how-to/manage-internal-routing/
* https://docs.digitalocean.com/products/app-platform/how-to/manage-health-checks/
* https://docs.digitalocean.com/products/app-platform/how-to/store-data/
* https://docs.digitalocean.com/products/app-platform/how-to/view-logs/
* https://docs.digitalocean.com/products/app-platform/how-to/forward-logs/
* https://docs.digitalocean.com/products/app-platform/how-to/view-insights/
* https://docs.digitalocean.com/products/app-platform/how-to/create-alerts/
* https://docs.digitalocean.com/platform/teams/roles/predefined/ and https://www.digitalocean.com/blog/introducing-custom-roles
* https://docs.digitalocean.com/platform/teams/how-to/configure-sso/
* https://www.digitalocean.com/blog/github-actions-for-app-platform (2024-09-26) and https://github.com/digitalocean/app_action
* https://www.digitalocean.com/blog/remote-mcp-server (2025-12-09)
* https://github.com/digitalocean/digitalocean-mcp (archived) and https://github.com/digitalocean-labs/mcp-digitalocean
* https://github.com/digitalocean/doctl and https://github.com/digitalocean/terraform-provider-digitalocean (via GitHub search API)
* https://news.ycombinator.com/item?id=24698334 (launch, 2020-10-06)
* https://www.techtarget.com/searchcloudcomputing/tip/Dive-into-DigitalOcean-Droplets-and-App-Platform (2021-07-30)
* https://siipo.la/blog/digitalocean-app-platform-its-a-promising-service-with-a-one-really-big-problem (2021-12-26)
* https://ideas.digitalocean.com/app-platform/p/allow-app-platform-apps-to-use-block-storage-volumes
* https://www.digitalocean.com/community/questions/deploying-app-wipes-out-sqlite-db-where-can-i-put-the-db-so-it-persists (2024-04-23)
* https://www.digitalocean.com/community/questions/why-app-platform-is-very-slow-in-deploying-app (2021-07-18)
* https://www.digitalocean.com/community/questions/app-platform-deployment-is-hanging-for-almost-1-hour-on-build-step (2026-02-11)
* https://www.digitalocean.com/community/questions/app-platform-slow-startup
* https://isdown.app/status/digitalocean/incidents/543201-app-platform-deployments (February 2026) and https://statusgator.com/services/digitalocean/outage-history?page=2
* https://render.com/articles/railway-vs-digitalocean-app-platform-pricing-reliability-production-risk (2026-07-20; a competitor's article)
* https://capterra.com/p/205055/DigitalOcean/reviews/
* https://app.opencve.io/cve/?vendor=digitalocean
* https://www.digitalocean.com/blog/regresshion-vulnerability-recommended-action
* https://app.intigriti.com/programs/digitalocean/digitalocean
* https://investors.digitalocean.com/news/news-details/2026/DigitalOcean-Announces-Second-Quarter-2026-Financial-Results/default.aspx
