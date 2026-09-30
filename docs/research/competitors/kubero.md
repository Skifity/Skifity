# Kubero

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Researched 2026-09-30 from the live repository, documentation, issue tracker,
NVD/OSV and Hacker News. Every number below carries its source and the date it
was read; where something could not be confirmed it says so.

Kubero ("Kube Hero") is a self-hosted, Heroku-style PaaS that runs **on a
Kubernetes cluster you already have**. It is for a developer or a small team
who wants pipelines, review apps and one-click add-ons without writing
manifests. Architecture: a Kubernetes **operator** built with the Operator SDK's
Helm flavour (every app, pipeline and add-on is a custom resource reconciled by
a Helm chart) plus a **UI/API container** — NestJS/TypeScript backend, Vue 3 +
Vuetify frontend — and a separate Go CLI. State lives in two places: the
custom resources in etcd (`KuberoPipeline`, `KuberoApp`, one CRD per add-on),
and, since v3.0.0 (August 2025), a **SQLite file on a PersistentVolumeClaim**
for users, roles, tokens, notifications and audit (`server/prisma/schema.prisma`
declares `provider = "sqlite"`; the chart mounts `/app/server/db` from a PVC) —
the README's "all data is stored on your Kubernetes etcd without an extra
database" is out of date. Licence GPL-3.0. Pricing: none — no paid tier, cloud
or support offering was found on kubero.dev. Maturity: v3.1.1, released
2025-09-17, is the **latest release a year later**; the main branch has 6
commits in 2026 (a LibreDB Studio template with its fixes and merge, and one
dependency bump with its merge; the last on 2026-06-24) and 10 in the twelve months to 2026-09-30 and the operator has had **no commit since
2025-09-17**. Adoption: 4,428 stars, 212 forks, 127 open issues and pull
requests (GitHub API, 2026-09-30); its "Show HN" in August 2025 scored 3 points.
About 95% of the 2,206 commits on main are by one person, Gianni Carafa
(git history, 2026-09-30). **Status in one line: feature-rich, single-maintainer,
effectively dormant since September 2025, with an unpatched critical CVE.**

## Feature inventory

### Deploy sources and builds

- Two deployment strategies: **Docker** (run an existing image) and
  **GitOps** (build from a repository).
- Four build strategies for GitOps apps:
  - **Runpacks** (the default, formerly called "buildpacks"): three public
    images per language — a *fetch* init container clones the code, a *build*
    init container runs e.g. `npm install`, the *run* container starts it. No
    image is built. The docs say it plainly: "The code will be built for every
    pod ... becomes more inefficient with every replica." Runpacks are edited
    with `kubectl edit kuberoes kubero -n kubero`.
  - **Nixpacks**, **Dockerfile** and **buildpacks.io**, which push to a registry.
- The registry is either external (Docker Hub, GHCR) or a local one Kubero
  creates. The local one has two documented modes, both awkward: exposed on a
  public hostname behind basic auth, or cluster-internal, which "must be
  configured as an insecure registry on all nodes" (Configuration docs).
- Git providers: GitHub, GitLab, Bitbucket, Gitea/Forgejo and Gogs, hosted and
  self-hosted, via personal access tokens. Connecting a pipeline creates the
  webhook and deploy key "only if it is owned by you".
- Redeploy on push to a branch or tag. A Procfile's `web` and `worker`
  processes become two Deployments; only `web` gets an ingress.
- Open build bugs filed in 2026 and unanswered: a Procfile without `build:`
  produces an empty build script silently (#795), an image reference becomes
  `repo:tag:tag` (#798), SSH clone URLs are mangled by an unanchored regex
  (#797), editing an app resets its branch to the default (#737), GitLab
  webhooks are ignored (#740).

### Domains, TLS and routing

- One ingress per app, TLS from cert-manager and a `letsencrypt-prod`
  ClusterIssuer the installer creates.
- **Built for ingress-nginx.** The install guide: "Kubero is designed to run
  with Nginx Ingress Controller ... Some features like Metrics and Proxy
  configurations (CORS, Bodysize, ...) may not work" on another controller. App
  basic auth is implemented with `nginx.ingress.kubernetes.io/auth-*`
  annotations (`helm-charts/kuberoapp/templates/ingress.yaml` in the operator),
  so on k3s's default Traefik it silently does nothing. ingress-nginx itself
  was retired by Kubernetes SIG Network in March 2026 — no further releases or
  security fixes.
- Cloudflare Tunnels only through a third-party operator add-on.
- Open bug: editing an app races into "domain already taken" (#738).

### Databases and services

- "Built-in" add-ons shipped with the operator: MySQL, PostgreSQL, Redis,
  MongoDB, RabbitMQ (moved to groundhog2k charts in v3.1.0 after Broadcom's
  Bitnami catalogue change), CouchDB and a Haraka mail server. The old Bitnami
  add-ons (also Elasticsearch, Kafka, Memcached) are marked deprecated. The
  README says the built-ins "are not High Availability (HA) ready".
- Operator-backed add-ons that each need their operator installed separately
  (through OLM): CloudNativePG, Crunchy Postgres, Percona MongoDB, Redis Cluster
  (Opstree), CockroachDB, ClickHouse (Altinity), MinIO.
- The docs warn: "Deleting or renaming an add-on instance will delete the add-on
  from your app and all data will be lost. For ever."

### Storage and backups

- Extra volumes (PVCs) per app.
- **No backups.** Kubero's own Heroku comparison table leaves "DB-Backups"
  empty for Kubero. The only backup in the code is the Crunchy Postgres add-on's
  pgBackRest section, which writes to a 1 GiB PVC in the same cluster
  (`server/src/addons/plugins/postgresCluster.ts`); the CloudNativePG add-on
  sets `backup: null`. Nothing goes off-site, nothing is restored from the UI.

### Scaling and high availability

- Pod sizes from a configurable list (`podSizeList`), replica counts for web and
  worker, HorizontalPodAutoscaler on CPU/memory.
- **Sleeping containers** through the zeropod runtime class (CRIU
  checkpoint/restore, `runtimeClassName: zeropod` in the app chart), which must
  be installed on every node first; marked beta and off for review apps.
- Multi-cluster: `KUBECONFIG_BASE64` may hold several contexts and a pipeline
  phase can target a context.
- HA of the platform itself: two Deployments; the v3 SQLite file pins the UI to
  one PVC.

### Observability (logs, metrics, alerts, uptime)

- Live application logs and a restart button in the UI.
- Current CPU/memory from metrics-server; long-term charts need the optional
  Prometheus stack (`kubero install -c monitoring`, which also requires
  ingress-nginx metrics). A Prometheus endpoint for Kubero itself arrived in
  v3.0.0.
- **Vulnerability scans** with Trivy (`aquasec/trivy:latest`), scheduled or on
  demand, results in the UI.
- Audit log, optional, on its own PVC with a 1,000-entry limit.
- No HTTP uptime checks were found.

### Security, auth, roles, SSO, audit

- v3.0.0 replaced the `KUBERO_USERS` base64 environment variable with a user
  database: roles `admin`, `member`, `guest`, permissions such as `app:write`,
  `pipeline:read`, `user:write`, `token:write`, `security:read`, user groups that
  filter which pipelines a user sees, API tokens, profile editing and an admin
  reset command (`yarn cli:reset-admin` inside the pod).
- SSO: GitHub OAuth restricted to one **public** organisation ("Private
  Organisations are not supported") and a generic OAuth2 provider — which a user
  reported in June 2026 as not enforced at all (#755, unanswered).
- Web console (exec into a pod), off unless `KUBERO_CONSOLE_ENABLED` is set.
- Basic auth for apps (nginx-only, above). Read-only mode and a banner.
- See "Security record" for what is wrong with all of this.

### Preview environments and branches

- A pipeline has up to four phases — review, test, stage, production — each a
  namespace (`<pipeline>-<phase>`), each app configured per phase.
- **Review apps**: opening a pull request builds and starts an app in the
  review phase; closing it removes it. What the app gets is thin — in
  `createPRApp` (`server/src/apps/apps.service.ts`) it is `addons: []`,
  `extraVolumes: []` with the comment "TODO Not sure how to handlle extra
  Volumes on PR Apps", `containerPort: 8080` hardcoded, the first pod size, the
  phase's default variables and the runpack build. **No per-PR database.**
- No promotion of a built image from one phase to the next was found in the
  server code; each phase builds its own.

### Templates and catalogue

- 173 templates in `services/*/app.yaml` (the README says 164+, the Show HN
  said 170+), plus a "frameworks" catalogue; catalogues are JSON indexes on
  GitHub and more can be configured.
- **149 of the 173 pin `tag: latest`** (grep of `services/*/app.yaml` at main,
  2026-09-30) — rolling back one of them restores a tag, not a version.

### CLI, API, IaC, integrations

- Go CLI (`kubero install`, `kubero debug`, app and pipeline commands). It can
  create a cluster only on GKE, DigitalOcean, Linode, Scaleway or local Kind
  (`kubero-cli` source); there is no bare-VPS or k3s path.
- REST API with Swagger; everything is also a CRD, so `kubectl apply` of a
  `KuberoPipeline` and `KuberoApp` is the IaC story, and ArgoCD works on them.
- Operator on OperatorHub/OLM.

### Notifications

- Slack, Discord and generic webhooks, per pipeline and event. The API that
  stores them was the subject of CVE-2026-92720 (below).

### Multi-server and networking

- Whatever the cluster already is. Kubero does not add, join or remove
  servers, and does not install Kubernetes on a server.

### Team and collaboration

- Users, roles, groups and team views since v3; no invitations flow was found
  beyond an admin creating accounts.

### Developer experience and onboarding

- Install is `kubero install`, which walks through installing ingress-nginx,
  metrics-server, cert-manager, optionally Prometheus, the operator and the UI
  on an existing cluster. The manual path is five `kubectl apply` commands and a
  Secret with `openssl rand` values.
- UI in several languages since v3.0.0 (Portuguese among them); README in
  English, Chinese and Japanese.
- A public demo at demo.kubero.dev (up on 2026-09-30).
- Troubleshooting is `kubectl` all the way down: the troubleshooting page asks
  the user to list CRDs, read operator logs and inspect `certificaterequests`.

## What users love

- **Everything Heroku had, for free, on Kubernetes.** Pipelines, review apps,
  add-ons, cron jobs, sleeping pods, vulnerability scans, a web console, SSO:
  the feature list is the longest of the Kubernetes-native peers, and the
  project's own comparison table ticks nearly every Heroku Enterprise box
  (kubero.dev/docs/comparison-heroku).
- **No lock-in by construction.** Apps are CRDs and Helm releases; delete the UI
  and the operator still reconciles them (Goals and Concepts page).
- **Git provider breadth**: GitHub, GitLab, Bitbucket, Gitea/Forgejo, Gogs,
  hosted or self-hosted.
- **"Flattening the learning curve"** is how the r/selfhosted-derived
  summaries describe it for people who want multi-server HA without learning
  Kubernetes (search summary of LinuxLinks and dev.to write-ups; no first-hand
  Reddit thread could be retrieved).

## What users complain about

- **Installation.** The top-voted open issues are about getting it running:
  "Need proper installation documentation with clear images and steps" (#351,
  open since June 2024), "Failed to wait for Kubero UI to become ready" (#646,
  May 2025), and two reports that the operator manifest references
  `gcr.io/kubebuilder/kube-rbac-proxy:v0.11.0`, an image that no longer exists,
  so **a fresh operator install fails** (#735, March 2026; #744, May 2026). Both
  unanswered as of 2026-09-30; the reporter's workaround is to swap in
  `quay.io/brancz/kube-rbac-proxy`.
- **Dependency rot.** Bitnami add-ons had to be replaced after Broadcom's
  catalogue change (#682, the most-reacted open issue), ingress-nginx — the
  controller Kubero is designed around — was retired in March 2026, and the
  kube-rbac-proxy image above disappeared. Each is an upstream change a
  maintained project absorbs in a release; none has shipped since.
- **Editing is lossy.** The UI overwrites the whole `KuberoApp` spec and drops
  fields it does not manage (#796); editing resets the branch (#737) and races
  on domains (#738).
- **Traction.** On the Canine Show HN a commenter wrote that Canine "is
  identical to kubero.dev — the problem is that kubero, Idk they did not gain
  any traction. maybe most user want simple tools like coolify" (HN, June 2025).
- **Silence.** The 2026 issue list — build bugs, security reports, broken
  installs — has no maintainer replies.

## Security record

| Date | Identifier | What | Status on 2026-09-30 |
|---|---|---|---|
| 2026-09-16 | **CVE-2026-92720**, CVSS 3.1 **9.1** / CVSS 4.0 **9.3** (VulnCheck, CWE-306) | "Kubero through 3.1.1 fails to apply authentication guards to the notifications API endpoints" — unauthenticated `GET/POST/PUT/DELETE /api/notifications` returns webhook secrets and Slack/Discord URLs in plaintext and lets anyone register a webhook subscribed to every event or delete alerting | **No fixed release.** Filed publicly as #753 (June 2026), unanswered; the controller at main still has no guard. OSV's record lists a fix commit that is in fact the v3.1.1 tag commit, while NVD says "through 3.1.1" — the NVD wording matches the code. |
| 2026-08-26 | #786, no CVE | A **hardcoded JWT fallback secret** ("DO NOT USE THIS VALUE ...") in `auth.module.ts`, `jwt.strategy.ts` and `auth.service.ts`; if `JWT_SECRET` is unset anyone can mint an admin token with arbitrary role and permission claims | Open, unanswered; the string is still in main (verified by grep). |
| 2026-08-26 | #787, no CVE | The console endpoint execs into any `podName` the caller names, without checking it belongs to the app; everything in a pipeline phase shares a namespace, so `app:write` on one app is a shell in every other app of that phase | Open, unanswered. |
| 2026-06-24 | #755, no CVE | OAuth2 login configured per the docs is not enforced | Open, unanswered. |

- The project's `SECURITY.md` tells reporters to "open a Issue" — so every one of
  these was disclosed publicly before any fix — and lists 2.x as the supported
  line, a year after 3.0 shipped. No GitHub security advisories are published.
- Sources checked: NVD keyword search "kubero" (1 result), OpenCVE
  vendor/product page (1), OSV, GitHub security tab, the issue tracker.

## Against Skifity

### Why it did not become the Coolify of Kubernetes, and the lesson

Kubero had the features. It lost on everything around them:

1. **It starts after the hard part.** The Coolify user has a $5 VPS and nothing
   else. Kubero's first requirement is a cluster, then ingress-nginx,
   metrics-server, cert-manager and a registry that every node trusts; its CLI
   can only create *managed* clusters or a local Kind. The person who would
   type `curl | sh` into a fresh VPS never gets in. **Lesson: Skifity's
   installer and "add a server with an IP and a password" are the product —
   and both are Written, never run.**
2. **It owned nothing underneath and still inherited all of it.** Bitnami,
   ingress-nginx, kube-rbac-proxy: three upstream decisions in twelve months,
   each a broken install until somebody ships a release. A Kubernetes PaaS is a
   distribution; distributions need releases. **Lesson: Skifity installs *more*
   of the stack than Kubero does (k3s, cert-manager, CloudNativePG, KEDA,
   Longhorn, BuildKit, a registry) and today has no way to upgrade any of it
   once installed (`EnsureComponent` in `internal/cluster/components.go`
   returns as soon as a component is "installed"; the k3s version setting only
   applies to new servers).**
3. **One maintainer, and he stopped.** 95% of commits, then six in 2026. No
   succession, no second reviewer — the security reports sit unanswered.
4. **Kubernetes leaks at every edge.** Pipelines are namespaces, runpacks are
   edited with `kubectl edit`, troubleshooting is `kubectl get crd`. A
   developer who does not know Kubernetes is fine until the first thing goes
   wrong.
5. **Security by feature count.** A console, notifications and SSO were each
   added; the authorization around them was not. The same class of bug —
   an endpoint that forgot the guard — that Skifity's route walk exists to catch.

| Capability | Kubero | Skifity (evidence) | Verdict |
|---|---|---|---|
| Install from a bare server | No. Needs an existing cluster; CLI creates GKE/DO/Linode/Scaleway/Kind only | `installer/install.sh` installs k3s and the panel; servers join over SSH (`internal/provision`). **Written, never run** (ADR-0010) | Skifity ahead (on paper) |
| Deploy sources and builds | Image or Git; runpacks (build in every pod), Nixpacks, Dockerfile, buildpacks.io; 5 Git hosts incl. Bitbucket and Gogs | Image, Git (GitHub, GitLab, Gitea: `internal/gitsrc`), or a folder (`internal/upload`, `skifity up`); Railpack, Nixpacks, Dockerfile in a cluster Job (`internal/builder`); build fingerprint so variables do not rebuild (ADR-0007). No Bitbucket (grep, 0 hits). Written | Parity; Skifity ahead on folder deploy and no-rebuild config, behind on Bitbucket |
| Domains, TLS and routing | cert-manager; tied to retired ingress-nginx; basic auth nginx-only | cert-manager on first domain (`internal/cluster/components.go`), sslip.io address, DNS record helper, per-app password (`internal/store/migrations/0017_app_password.sql`) and IP/country/ASN firewall (`internal/edgerules`, `internal/guard`) on Traefik, Cloudflare tunnel. Written | Skifity ahead |
| Databases and services | 7 built-ins (not HA) + 8 operator add-ons, each operator installed by hand | PostgreSQL (CloudNativePG), Redis, MySQL, operator installed on first use, credentials sealed and injected (`internal/dbsvc`). Written | Kubero wider, Skifity safer; parity overall |
| Storage and backups | PVC volumes; no backups (own comparison table) | Volumes, scheduled/manual S3 backups for databases and volumes, restore (`internal/backup`). Written, never restored | Skifity ahead |
| Scaling and HA | Replicas, HPA, zeropod sleep; multi-cluster contexts | HPA (`internal/kube/manifests.go` `BuildHPA`), KEDA scale-to-zero (`internal/kube/scaletozero.go`), readiness checker (`internal/deploy/scaling.go`), node failover via k3s. Written | Parity; Skifity ahead on the readiness check |
| Observability | Logs, metrics-server + optional Prometheus charts, Trivy scans, audit | Logs over SSE, per-instance usage from metrics.k8s.io, `/api/metrics`, watch loop with 7 notification events (`internal/watch`, `internal/notify`), audit (`internal/store/misc.go`). No usage history, no vulnerability scan (grep "trivy", 0) | Behind on history and scans |
| Security, auth, roles, SSO, audit | admin/member/guest + permissions, groups, tokens, GitHub/OAuth2 SSO (reported broken), **CVE-2026-92720 unpatched**, hardcoded JWT fallback | Argon2id, TOTP, recovery keys, OIDC with PKCE/nonce, owner/admin/member, scoped team-bound tokens (`internal/auth`), route walk refusing anonymous and cross-team access (`internal/api`), sealed secrets (`internal/crypto`). Works (Go tests, `make smoke`) | Skifity ahead |
| Preview environments and branches | 4-phase pipelines; review apps with no add-ons, no volumes, port 8080 hardcoded | PR/branch previews in their own namespace, fork PRs get no secrets, TTL, commit status and PR comment (`internal/api/webhook_handlers.go`, `internal/gitsrc/report.go`). No promotion between environments, no per-preview database. Written | Parity; Kubero ahead on staged pipelines |
| Templates and catalogue | 173, 149 on `latest` | 282, every image a checked version (`internal/templates/catalogue`) | Skifity ahead |
| CLI, API, IaC, integrations | Go CLI, REST, CRDs (kubectl/ArgoCD) | One binary: CLI, API, stdio MCP (`internal/mcpserver/server.go`), `skifity.toml`, export as JSON + Kubernetes objects (`internal/api/export_handlers.go`). No CRDs, no Terraform | Skifity ahead on MCP/export; Kubero ahead on GitOps-by-CRD |
| Notifications | Slack, Discord, webhook (the CVE'd API) | Telegram, Discord, webhook, email built in; Slack via a plugin provider (`internal/notify/provider.go`, `internal/plugins`). Written | Parity |
| Multi-server and networking | Whatever the cluster is | Add, join, promote, remove servers over SSH (`internal/provision`, `internal/sshx`); default-deny NetworkPolicy per environment (`internal/kube/namespace.go`). Written | Skifity ahead |
| Team and collaboration | Users, roles, groups, pipeline filtering by group | Teams, invitations, three roles; roles are team-wide (`authorizeApp` etc. in `internal/api/api.go` check the team role only) | Parity; Kubero's groups can hide pipelines, Skifity cannot scope a member to one project |
| Developer experience and onboarding | Multi-language UI; kubectl-first troubleshooting; install broken on fresh clusters (#735) | Five complete languages with a build gate (`web/src/locales`), docs served by the binary (`internal/docsite`), every error with cause/impact/fix (`internal/errdoc`). Works for the panel; the cluster path is Written | Skifity ahead |

## Gaps worth closing in Skifity

### P0

**1. Keep the platform underneath current: component and k3s upgrades.**
- *What.* Record the version each component was installed at; on a panel
  upgrade whose defaults moved (cert-manager, CloudNativePG, KEDA, KEDA HTTP,
  Longhorn, BuildKit, registry), show "cert-manager 1.21.2 → 1.22.x" with the
  upstream notes link and apply it on request; for k3s, render
  system-upgrade-controller `Plan` objects to roll servers one at a time,
  control plane first.
- *Evidence.* Kubero is the counter-example: three upstream changes in a year
  (Bitnami #682, ingress-nginx retirement, kube-rbac-proxy #735/#744) and a
  fresh install no longer works. Epinio spent two releases replacing kubed
  (1.13.8) and MinIO (1.13.10). Skifity today: `EnsureComponent`
  (`internal/cluster/components.go`) never revisits an installed component,
  `UpgradePanel` changes only the panel image, and `cluster.k3s_version`
  (`internal/settings/settings.go`) is read only by the provisioner for new
  servers (`internal/provision/scripts.go`).
- *Fit.* `internal/cluster` (a `components.installed_version` column in
  `cluster_components`, an `UpgradeComponent`), a new `internal/kube` renderer
  for upgrade Plans, `POST /api/teams/{team}/cluster/components/{name}/upgrade`,
  the Components section of the Settings page; an errdoc entry per failure.
- *Size.* L.
- *Without a cluster?* Partly: the version comparison, the plan, the rendered
  Plan objects (golden tests) and the panel page are testable here; whether an
  upgrade leaves a running cluster healthy needs one, and belongs in
  `test/cluster/verify.sh` as a phase.

**2. A preview must never be handed the production database.**
- *What.* Today `copyPreviewVariables` (`internal/api/webhook_handlers.go`)
  copies every variable of the source app, and a linked database *is* a secret
  variable (`dbsvc.Manager.Link`, `internal/dbsvc/manager.go`). A same-repository
  pull request's preview therefore receives the source environment's
  connection string — production's, if previews are on for a production app —
  and runs code nobody has merged against it. Either give each preview its own
  small database (a CloudNativePG cluster, or a database in a shared one,
  created with the namespace and deleted with it), or at minimum drop
  linked-database variables from the copy and say so on the preview.
- *Evidence.* None of the three Kubernetes peers solves it: Kubero's review
  apps get `addons: []`; Canine tells users to template `DATABASE_URL` and write
  their own create/drop scripts in `canine.yml`; Epinio has no previews.
  "PR previews with per-PR databases" is what Vercel/Neon, Railway and Render
  sell. The environment's default-deny NetworkPolicy
  (`internal/kube/namespace.go`) blocks egress to other namespaces and should
  stop the connection — but that policy has never been enforced by a real CNI
  (checklist item 12, Written), so today it is the only thing standing between
  an unmerged migration and the production database, and it is unproven. P0
  because the minimal fix is small and the failure is not recoverable.
- *Fit.* `internal/api/webhook_handlers.go` (skip or re-provision links),
  `internal/dbsvc` (create/delete per preview), the preview's page in the panel.
- *Size.* S for "do not copy links, say why"; M for per-preview databases.
- *Without a cluster?* The copy rule and the rendered database manifests, yes
  (unit tests); the database coming up in a preview namespace, no.

### P1

**3. Scope a member to a project or protect production.**
- *What.* A role per project or per environment ("can deploy staging, can only
  read production"), checked in the same `authorize*` helpers.
- *Evidence.* Kubero added user groups that filter pipelines in v3 and an open
  request asks for private apps per group (#605); Epinio has namespace-scoped
  roles built from dozens of fine-grained actions; Canine scopes teams to
  clusters and projects.
  Skifity's helpers resolve to the team and compare one team role
  (`internal/api/api.go`).
- *Fit.* A `memberships` extension or a `project_roles` table in
  `internal/store`, `authorizeProject/Environment/App` consult it, the project
  settings page lists who can do what.
- *Size.* M.
- *Without a cluster?* Yes — the route walk that already refuses cross-team
  access can be extended to cross-project access.

### P2

**4. Image vulnerability scan.**
- *What.* Scan each built image (Trivy or Grype as a Job after the build), store
  the counts on the deployment, show them on the deployment and optionally
  refuse "critical" findings.
- *Evidence.* It is one of Kubero's most-shown screenshots; Skifity's pitch leans
  on security, and has no scan (grep for trivy/grype/scan: nothing).
- *Fit.* `internal/builder` (a scan Job after the build Job), a column on
  `deployments`, the deployment log page, an errdoc code for a refused deploy.
- *Size.* M.
- *Without a cluster?* The Job manifest and the parsing of the scanner's JSON,
  yes; a real scan, no.

**5. Usage history.**
- *What.* Sample each app's CPU/memory every minute (the watch loop already
  ticks), keep a week in SQLite with the existing retention job, draw it on the
  app page.
- *Evidence.* Kubero ships long-term charts (via Prometheus); Canine stores CPU,
  memory and storage samples per cluster and namespace. Skifity shows only the
  current value from metrics.k8s.io (`internal/kube/client.go`) and refuses to
  install Prometheus itself.
- *Fit.* `internal/watch`, a `usage_samples` table and `internal/store/retention.go`,
  a chart on `web/src/pages/app-detail.tsx`. There is no chart component in
  `web/src/components/ui` yet; shadcn/ui's own brings in Recharts, which is a
  decision to take against the one-component-library rule — a small SVG
  sparkline avoids it.
- *Size.* M.
- *Without a cluster?* Yes, with the fake metrics client and the Playwright test.

**6. Bitbucket.**
- *What.* A fourth Git host in `internal/gitsrc` (clone, webhook, commit status).
- *Evidence.* Kubero and Canine both support it; Skifity has GitHub, GitLab and
  Gitea only.
- *Fit.* `internal/gitsrc/hooks.go`, `webhook.go`, `report.go`; Settings → Git.
- *Size.* M.
- *Without a cluster?* Yes — the other three hosts are tested against a fake host.

## Things to deliberately not copy

- **Building in every pod.** Runpacks trade a registry for a rebuild on every
  replica start. It makes scaling slow exactly when scaling matters and makes
  a rollback depend on the build still working. Skifity builds once and runs
  an image.
- **A cluster-internal registry that nodes must trust as insecure, or one
  exposed publicly behind basic auth.** Both are Kubero's documented options.
- **Coupling features to one ingress controller's annotations.** Kubero's basic
  auth, metrics and proxy settings only work on ingress-nginx — now retired.
  Skifity's firewall and app password are Traefik middleware; keep them behind
  one renderer so a future controller change is one package.
- **`latest` in templates.** 149 of Kubero's 173.
- **A web console bolted on without per-pod authorization.** Kubero's console
  proves Skifity's roadmap decision (`docs/roadmap.md`, "An interactive shell
  into a running instance"): it became a cross-app shell (#787).
- **Security reports as public issues.** Kubero's `SECURITY.md` asks for them;
  every critical bug above was public before a fix. Skifity has no
  `SECURITY.md` at all today — it should have one that points at GitHub's
  private vulnerability reporting before the first tag.
- **A secret with a published default.** If a signing key is missing, refuse to
  start; do not fall back to a string in the source.
- **Editing by overwriting the whole object.** Kubero's UI replaces the CR spec
  and loses fields (#796). Skifity's rollback restores settings from the
  deployment record; any "edit YAML" feature must merge, not replace.

## Sources

All read 2026-09-30.

- https://github.com/kubero-dev/kubero — repository metadata via the GitHub search API (stars, forks, open issues, licence, created/pushed dates)
- https://github.com/kubero-dev/kubero/tags and https://github.com/kubero-dev/kubero/releases
- https://github.com/kubero-dev/kubero/releases/tag/v3.0.0, /v3.1.0, /v3.1.1
- https://github.com/kubero-dev/kubero/commits/main
- Git history of kubero-dev/kubero (main at 36a5046, 2026-06-24) and kubero-dev/kubero-operator (ea8327a, 2025-09-17), cloned: commit counts per year and per author, tags
- Source read at those commits: `server/prisma/schema.prisma`, `server/src/auth/*`, `server/src/notifications/notifications.controller.ts`, `server/src/apps/apps.service.ts`, `server/src/addons/plugins/*`, `server/src/database/database.service.ts`, `services/*/app.yaml`; operator `deploy/operator.yaml`, `helm-charts/kuberoapp/templates/*`, `helm-charts/kubero/templates/deployment.yaml`; kubero-cli (f009221) provider list
- https://raw.githubusercontent.com/kubero-dev/kubero/main/README.md
- https://www.kubero.dev/ (features, no pricing)
- https://www.kubero.dev/docs/goals-and-concepts
- https://www.kubero.dev/docs/Getting-Started/prerequisites
- https://www.kubero.dev/docs/Getting-Started/Installation/installation-vanilla
- https://www.kubero.dev/docs/Getting-Started/configuration
- https://www.kubero.dev/docs/Getting-Started/repositories
- https://www.kubero.dev/docs/Getting-Started/upgrade
- https://www.kubero.dev/docs/Getting-Started/troubleshooting
- https://www.kubero.dev/docs/Usermanual/features
- https://www.kubero.dev/docs/Usermanual/addons
- https://www.kubero.dev/docs/Usermanual/runpacks
- https://www.kubero.dev/docs/Usermanual/deployment
- https://www.kubero.dev/docs/comparison-heroku
- https://demo.kubero.dev (HTTP 200)
- Issues: https://github.com/kubero-dev/kubero/issues/351, /605, /646, /682, /735, /737, /738, /740, /744, /753, /755, /786, /787, /795, /796, /797, /798; issue list sorted by reactions and by date
- https://github.com/kubero-dev/kubero/security (policy, no advisories)
- https://nvd.nist.gov/vuln/detail/CVE-2026-92720 (via the NVD API; references VulnCheck's advisory https://www.vulncheck.com/advisories/kubero-through-3.1.1-unauthenticated-notifications-api-access, not read directly)
- https://osv.dev/vulnerability/CVE-2026-92720
- https://app.opencve.io/cve/?product=kubero&vendor=kubero-dev
- https://news.ycombinator.com/item?id=44873057 (Kubero Show HN, 3 points, 2 comments, 2025-08-12; HN API)
- https://news.ycombinator.com/item?id=44292103 (Canine Show HN thread, comment by tonyhart7 on Kubero's traction; Algolia HN API)
- https://github.com/bitnami/charts/issues/35164 (Bitnami catalogue change, effective 2025-08-28)
- https://www.kubernetes.dev/blog/2025/11/12/ingress-nginx-retirement/ (ingress-nginx retirement, March 2026)
- https://www.linuxlinks.com/kubero-self-hosted-paas/ and https://dev.to/shoksuno/kubero-vs-coolify-3po0 (search-result summaries only)
- Skifity, for the comparison: `internal/cluster/components.go`, `internal/settings/settings.go`, `internal/provision/scripts.go`, `internal/api/webhook_handlers.go`, `internal/dbsvc/manager.go`, `internal/kube/namespace.go`, `internal/api/api.go`, `internal/notify/provider.go`, `internal/store/migrations/0017_app_password.sql`, `internal/mcpserver/server.go`, `docs/roadmap.md`, `docs/faq.md`, `docs/checklist.md`
