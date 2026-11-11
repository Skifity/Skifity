# Mortise

> **Status.** Written on 2026-11-08 against Mortise's `main` at commit `8a274c0`
> (2026-10-30: v1.2.0 and one commit after it) and against Skifity's working tree at
> `23ec07f`. Nothing in this file has been acted on yet; the gaps below are proposals.

Mortise (`mortise-org/mortise`, mortise.me) is "a self-hosted, Railway-style deploy
platform for Kubernetes": a Git repository or an image becomes an app with builds,
domains, TLS, variables, volumes, previews and "service bindings", on a cluster the
user already has. It is for homelabbers on k3s or Talos and small teams "when there
is no platform team" (`SPEC.md` §2–3). **Architecture:** one Go operator built with
controller-runtime, which runs the controllers, the REST API, SSE streams and an
embedded SvelteKit interface, and keeps **no database**: deploy history lives in the
`App` custom resource's status (twenty records), users and sessions are Kubernetes
Secrets in `mortise-system`, audit lines go to stdout, and a project's activity is a
ConfigMap. The state is seven custom resources (`Project`, `App`,
`PreviewEnvironment`, `PlatformConfig`, `GitProvider`, `ProjectMember`, `BuildRun`).
Each project gets a control namespace `pj-{project}` and one namespace per environment
`pj-{project}-{env}`. Beside the operator, the umbrella Helm chart installs Traefik,
cert-manager, metrics-server, one shared BuildKit, a Distribution registry with a
node-local proxy on a host port, and `mortise-observer`, a second Go binary that keeps
logs, metrics and traffic in SQLite on a volume. Builds use Railpack as a library or a
Dockerfile. Its design rule is "Mortise is a deploy target, not a platform": SSO,
backups, monitoring, log aggregation and secret managers are "integration recipes"
for upstream projects (Authentik, Velero, kube-prometheus-stack, Loki, External
Secrets Operator), not code (`SPEC.md` §6.1). The README's own headline is "It tells
you what is actually running": named conditions on the `App` for states a platform
usually reports as success. **Runs on:** any Kubernetes through Helm (EKS, GKE, AKS,
Talos, k3s), or a one-line installer that puts k3s on Linux and k3d (k3s in Docker
Desktop) on macOS and Windows. The documented cluster minimum is 2 cores, 2 GB and
10 GB of disk, 4 cores and 4 GB recommended (`docs/cluster-setup.md`); the stack takes
"~1 CPU + 1.5Gi RAM", and the operator requests 512 MiB with a 2 GiB limit since
v1.1.0 (`docs/install.md`). **Licence:** Apache-2.0. **Pricing:** none; there is no
hosted or paid edition, and `mortise.me/pricing` is a 404 (2026-11-08). **Maturity:**
twelve releases between 2026-04-22 (v0.1.0) and 2026-10-30 (v1.2.0, tagged on
2026-10-29 local time); v1.0.0 was called general availability on 2026-05-09. The
repository was created on 2026-04-14 and has 1,266 commits on `main`: 368 in April,
458 in May, 1 in June, 5 in July, 270 in August, 114 in September, 50 in October and
none in November up to the 8th. Two people write almost all of it, James Sprow and Chase Allen
(`MC-Meesh`); 157 commits carry agent author names under Chase Allen's address
(`mayor`, `crew1` to `crew3`, `quality_audit`, `chrome`, `mutant`, `nitro`, `rust`,
`raider`), some pull requests are titled `[codex]`, and several issues credit a "Fable 5
quality audit". **Adoption:** 31 stars, 5 forks, 33 open issues and one open
pull request; 321 issues and 336 pull requests in all (GitHub search API, 2026-11-08).
No discussion of it was found on Hacker News or anywhere else outside its own
repository (see "What users love").

## Feature inventory

### What shipped, release by release

Dates are when the GitHub release was published, in UTC (ungh.cc mirror of the
releases API).

| Release | Date (2026) | What changed that matters for a comparison |
|---|---|---|
| v0.1.0 | 22 Apr | Project and App resources, Git and image sources, BuildKit builds, environments, previews, cert-manager TLS, variables, bindings, the observer, GitHub/GitLab/Gitea, native login with admin and member roles |
| v0.1.1 – v0.1.4 | 26 Apr – 6 May | Integration tests in CI, observer and webhook fixes, domain editing, environment clone, `llms.txt`, a local-only recipe |
| **v1.0.0** | 9 May | `BuildRun` resource for durable builds; previews converge against the forge's open pull requests instead of trusting webhooks; single-use SSE tokens; pull credentials per app; environment clone API; certificate status on each environment; a viewer role; "redeploy stale" for environments with unapplied variables; cosign-signed images with SBOM and provenance |
| v1.0.1 | 12 May | "All 49 commits are stability, security, and correctness fixes": stack creation rolled back on partial failure, viewers could list deploy tokens, variable values redacted in read responses, SSE tokens revalidated after a password change |
| v1.0.2 – v1.0.3 | 19 – 20 May | Previews became real project environments `pr-{N}`; a `Degraded` phase when the newest build failed and an older image still serves; crash-loop false positives during startup fixed |
| v1.0.4 | 26 Jul | Chart hardening; webhook registration refuses to guess an address or register without a secret |
| **v1.1.0** | 21 Sep (tag 18 Sep) | `MORTISE_REVISION`, `MORTISE_IMAGE` and `MORTISE_REPLICAS` in every container; conditions `EnvRolledOut`, `SpecEnvApplied`, `EnvKeysRetained`, `EnvKeysReserved`, `PlaintextCredentials`, `EnvironmentJoined`, `WebhookSignature`; `mortise diff`; `mortise admin reset-password`; Prometheus endpoints and a Grafana dashboard; login rate limit; opt-in `securityProfile: restricted`; build retries with adoption of an interrupted build's pushed image; the plaintext `last-spec-env` annotation replaced by digests, which makes a downgrade unsafe |
| **v1.2.0** | 30 Oct (tag 29 Oct) | `RolloutStalled`, `CertificateNotReady` with the CertificateRequest's failure, `VolumeRetained`, `MembersReady`, `CRDsCurrent`; image apps read their port from the image's configuration; one database binding also gives `DATABASE_URL`; rollback refused for Git apps; a missed push repaired hourly; webhooks re-verified hourly; image paths scoped per project (every Git app rebuilds once) and required Secret references (every app restarts once) |

On `main` after v1.2.0, unreleased: the operator takes a leader-election lease, because
during the v1.2.0 rollout the outgoing operator "adopted" a build the incoming one was
running and overwrote its digest. The changelog's upgrade note for it says some apps
deployed by a pre-1.2.0 operator are pinned to digests "the registry never stored",
enter ImagePullBackOff after the upgrade, and are healed one by one with
`kubectl annotate app <name> … mortise.dev/rebuild-requested-at=…`.

### Deploy sources and builds

* **Sources:** Git (`git`), an image (`image`), or `external`, a facade over a service
  Mortise does not run (an ExternalName Service plus credentials Mortise holds). GitHub
  connects by the OAuth device flow with per-user tokens stored as Secrets; GitLab and
  Gitea take a personal access token. Bitbucket is not supported.
* **Builds:** one shared BuildKit Deployment in `mortise-deps`, fed a Dockerfile or a
  Railpack plan converted to LLB inside the operator. Build arguments per environment,
  monorepo `source.path` and `watchPaths` (one webhook fans out to the apps whose paths
  changed), build logs over SSE and kept in ConfigMaps (ten per app environment).
  Since v1.1.0 a build lost to an operator restart is retried with backoff up to five
  times, and an image the interrupted build already pushed is adopted.
* **Kinds:** `service` (a Deployment) and `cron` (a CronJob with `concurrencyPolicy`).
* **Deploys and history:** a record per image or variable-hash change, twenty kept,
  marked "Confirmed" only once the environment's pods ran it (CAI-501); built images are
  pinned by digest, an image app's tag is run as written;
  **promote** between environments without a rebuild; **rollback** for image apps
  only — since v1.2.0 a Git app's rollback is refused: "the controller always deploys
  the freshly built image, so an image rollback would be silently reverted. Redeploy
  the target revision instead" (Mortise's `internal/api/rollback.go`). Variables are
  not restored by a rollback (#215, open since 2026-05-07).
* **External CI:** `POST /api/projects/{p}/apps/{a}/deploy` with `{"image": …}` and a
  **deploy token scoped to one app and one environment**, created with
  `mortise token create web production`, shown once, revocable, several per app;
  GitHub Actions and GitLab CI examples in `docs/recipes/external-ci.md`.
* **Variables don't roll out by themselves.** With `Project.spec.autoRedeploy` false,
  the default, a saved variable changes the Secret and not the pods; the app shows
  "Redeploy to apply" and the `EnvRolledOut=False / RedeployPending` condition until
  someone redeploys.
* **Compose:** a pasted file becomes one app per service (image, ports, environment,
  `depends_on` ordering, volumes, health checks, resources); services with
  `restart: "no"` are skipped (Mortise's `internal/api/compose.go`).

### Domains, TLS and routing

* An automatic hostname per app and environment from a Go template,
  `{app}-{project}[-{env}].{domain}`; collisions are refused with a `DomainCollision`
  condition. Custom domains per environment.
* TLS from cert-manager; the chart creates a ClusterIssuer when `tls.acme.email` is
  set. Per environment a different ClusterIssuer or an existing TLS Secret.
* `CertificateNotReady` (v1.2.0) is set while a public environment's pods are Ready but
  its certificate is issuing or failed, with the newest CertificateRequest's failure
  as the message ("Failed: …", or `Denied`/`InvalidRequest`), and the interface's
  Open button is disabled when the certificate failed (Mortise's
  `internal/controller/app_controller.go`, `latestCertRequestFailure`).
* DNS records only through an ExternalDNS the user installs; Cloudflare Tunnel is a
  recipe. No redirects, basic auth, firewall or maintenance page; an
  `environments[].annotations` map is merged onto every object, "footguns … not
  guardrailed", for nginx rate limits, Linkerd or IRSA (`SPEC.md` §5.2a).
* Without a domain, the interface's **Open** proxies to the pod through the API, and
  `mortise proxy <app>` opens a local reverse proxy.

### Databases and services

* No managed databases. A database is an image app (`postgres:16`, `redis:7-alpine`)
  with a volume and a `credentials` list; the "New Postgres" button pre-fills one.
  CloudNativePG or managed services are a recipe, bound through `external`.
* **Bindings:** binding to an app injects `{APP}_HOST`, `{APP}_PORT`, `{APP}_URL` and
  its declared credentials; with exactly one relational database the conventional
  `DATABASE_URL`, `DATABASE_HOST` and `DATABASE_PORT` too (v1.2.0);
  `valueFrom.fromBinding` projects one key under another name. Rotated credentials
  reach binders since v1.2.0.
* `secretMounts` mounts an existing Kubernetes Secret as files; `configFiles` mounts
  text files from a ConfigMap.

### Storage and backups

* Named volumes per app with a size, storage class and access mode; RWO volumes switch
  the Deployment to `Recreate`. Removing a volume keeps its claim, reported as
  `VolumeRetained` since v1.2.0. PVC usage is collected by the observer.
* **No backups.** `docs/recipes/backup.md` points to k3s etcd snapshots for the
  objects and Velero for volumes; `mortise export` is "post-v1" (#163, open).

### Scaling and high availability

* `replicas` per environment. No autoscaling of any kind and no scale to zero (no HPA
  or KEDA object anywhere in the code). Requests are set equal to limits (#489).
* `MORTISE_REPLICAS` tells an app how many copies were asked for, "so an app that holds
  process-local state … can refuse to run scaled".
* A manual `kubectl scale` to zero is reverted within minutes with nothing saying so
  (#490, open; pull request #658 of 2026-11-06 would surface it).
* Placement, multi-cluster and node management are out of scope; the cluster is the
  user's.

### Observability (logs, metrics, alerts, uptime)

* **The observer:** logs kept 48 hours, metrics 72 hours (raw for 24 h, then 5-minute
  averages), traffic 48 hours from Traefik's access log, PVC usage 72 hours, all in
  SQLite on a volume, with live SSE streams. Responses carry a `coverage` array so
  charts draw gaps instead of interpolating, and `GET /v1/health/collectors` reports
  each collector's last tick and error.
* Prometheus endpoints on the observer and the operator (`mortise_builds_total`,
  `mortise_build_duration_seconds`, `mortise_app_status_phase`) with an importable
  Grafana dashboard.
* **Conditions as the product:** `RolloutStalled` (pods that never start past the
  120 s progress deadline, with "pod unschedulable: insufficient memory",
  `ImagePullBackOff` or stuck `ContainerCreating`), `CertificateNotReady`,
  `EnvRolledOut`, `SpecEnvApplied` (an out-of-band edit is overriding the spec),
  `EnvKeysRetained`, `EnvKeysReserved` (a declared `PORT` or `MORTISE_*` loses to the
  platform's), `EnvVarInvalid`, `PlaintextCredentials` (a literal under a name like
  `*_KEY` or `*_PASSWORD`), `RegistryTargetValid`, `WebhookConfigured`,
  `WebhookSignature`, `CRDsCurrent`, `ConfigApplied=RestartRequired`. Each message names
  "the way out".
* **`mortise diff APP`** compares the `App`'s variables, the Secret the pods mount and
  the env hash the workload started with, and classifies each difference
  (`spec-differs-from-secret`, `user-override`, `retained`, `missing-from-secret`, …).
  It prints names and 12-character digests salted per run, never values, reads the
  cluster through the kubeconfig and never writes; `-f` is a dry run against a file.
* No alerts, uptime checks or notification channels.

### Security, auth, roles, SSO, audit

* Native accounts only: bcrypt, users as Secrets, a JWT valid 24 hours kept in the
  browser's `localStorage` and refreshable up to 7 days after it expired. Login is
  rate-limited (5 a minute, a 15-minute lockout after 10 failures) since v1.1.0. **No
  two-factor, no passkeys, no SSO**: SSO is "a front-door auth proxy (no OIDC login
  flow yet)" (README); OIDC is #152, open since 2026-04-27.
* Platform roles admin, member, viewer; project roles owner, developer, viewer through
  `ProjectMember`; **restricted environments** (`environments[].restricted: true`) are
  read-only for developers, and a preview may not clone from one. The last admin and
  last owner cannot be removed.
* Secrets are Kubernetes Secrets in the app's namespace. The variables API returns
  values in plain text to anyone who may update the app and redacts them for others
  (Mortise's `internal/api/env.go`, `envResponseVar`); `mortise env pull > .env` exists.
* A per-project activity log names who changed what (variable names, never values);
  write handlers stamp the actor.
* An exec API runs one command in a running pod (output capped at 10 MiB); there is no
  shell in the interface (#426, open).
* `spec.securityProfile: restricted` makes an app admissible under the restricted Pod
  Security profile; it is opt-in because "root-running images cannot satisfy
  `runAsNonRoot`".

### Preview environments and branches

* Per project: a pull request adds a `pr-{N}` environment cloned from a source
  environment (staging by default), including its Secrets, and every app in the
  project fans out into it, crons included — "real side-effects if the cron hits
  external systems" (`SPEC.md` §5.8a). A backing-service app gets a fresh, empty copy
  (copying volumes is #368, open). `SPEC.md` §5.8 says the URL is "posted as PR
  comment" and the architecture diagram has commit statuses, but at `8a274c0` there is
  no comment call, and `PostCommitStatus` is implemented for GitHub, GitLab and Gitea
  and called by nothing (Mortise's `internal/git/`).
* Previews converge every 10 minutes against the forge's open pull requests, so a
  missed webhook does not leave one behind or missing. `botPR` defaults to true. No
  handling of pull requests from forks was found (no match for "fork" in Mortise's
  `internal/`), and the preview branch is taken by name from the base repository
  (`p.PullRequest.Head.Ref`, Mortise's `internal/webhook/handler.go`).
* Two repositories in one project collide on the same `pr-{N}` (#435, P1, open).

### Templates and catalogue

* Five built into the interface (Postgres 16, Redis 7, Paperless-ngx 2.20.15,
  Vaultwarden 1.36.0, Mealie v3.17.0) and one Compose stack, Supabase in seven
  services, served by `GET /api/templates`. A community preset repository is "not
  Mortise's scope" (`SPEC.md` §6.5).

### CLI, API, IaC, integrations

* About 109 REST routes with an OpenAPI 3.0 file, served live at `/api/openapi.yaml`.
* A CLI: `login`, `project`, `app create|update|delete|list` (`--storage` since
  v1.2.0), `deploy`, `promote`, `rollback`, `logs`, `env list|set|unset|import|pull`
  (with `@secret:` and `@from:` shortcuts), `secret`, `domain`, `token`, `proxy`,
  `open`, `status`, `diff`, `git-provider`, `repo`, `platform`, `admin`, `version`,
  and `up|down|destroy|install` for the local cluster.
* The custom resources are the infrastructure as code; Argo CD and Flux are expected
  to manage them, with `ignoreDifferences` on variables, which the interface writes
  (`SPEC.md` §12).
* No MCP server (#130, open since 2026-04-26), no Terraform provider. External secret
  managers through External Secrets Operator.

### Notifications

* An in-app notification list fed by the activity log. No email, chat or webhook
  channel was found in the code.

### Multi-server and networking

* Whatever the cluster has. Mortise adds no servers, configures no network between
  them and installs no NetworkPolicy: none exists in its charts or its controllers
  (`8a274c0`). The registry proxy listens on host port 30500 on every node.

### Team and collaboration

* Projects with members by email of an existing account; no email invitations.
  Project-level shared variables. Environment clone. The activity rail filters
  deploys, changes and members.

### Developer experience and onboarding

* `curl -fsSL https://mortise.me/install | bash` installs k3s (Linux, sudo) or k3d
  (macOS and Windows, Docker Desktop) and Helm, and port-forwards the interface to
  `localhost:8090`; the quickstart promises "Zero to deployed app in 10 minutes", and an
  installation through Helm reaches the interface with `kubectl port-forward` until a
  domain is configured (`docs/quickstart.md`).
* A first-run admin form, a getting-started checklist, a canvas of the project's apps
  with a drawer per app, and an "Extensions" page listing upstream tools with links.
* English only.

## What users love

Nothing was found outside the repository. Hacker News has no story or comment about
this Mortise (Algolia search by text and by URL, 2026-11-08); Reddit refused the
request from this sandbox; the web search tool had no credits left. GitHub's search
finds no issue or pull request in another repository that mentions
`mortise-org/mortise`.

Inside the repository, three people other than the authors appear. IIRoan reported
and diagnosed a reconcile loop (#631) and was thanked for "the report, diagnosis, and
measurement"; lmmendes reported a variables bug (#560); LeonardoFranco28 contributed
`--storage` ("their first open source contribution", v1.2.0) and has #658 open. What
the project itself offers as its reasons is the README's "It tells you what is
actually running" and Railway's model of bindings and previews on one's own cluster.

## What users complain about

Most issues are filed by the two authors from their own production use ("production
postlab-api", "five production pods ran this way"), so these are an operator's
complaints more than a crowd's.

* **Upgrades break things.** The v1.0.4 → v1.1.0 upgrade failed in production three
  ways: `helm upgrade --reuse-values` hit a nil dereference, a renamed RoleBinding's
  immutable `roleRef` stopped the apply, and a registry now running as uid 1000 could
  not write data an older root registry owned, so "every build on the platform failed
  at push" (CAI-310, -311, -313). The v1.2.0 rollout pinned apps to digests the
  registry never stored, and the fix is unreleased and needs `kubectl annotate` per app.
  v1.1.0 rolled every workload once; v1.2.0 restarts every app and rebuilds every Git
  app; a downgrade past v1.1.0 is "unsafe".
* **Memory is stranded.** Requests equal limits, so "10936Mi reserved vs 2850Mi
  actually used — 26% utilisation, ~8Gi stranded", and a production deploy could not be
  scheduled "on a cluster with several gigabytes genuinely free" (#489, open,
  2026-08-12).
* **A one-instance app cannot be updated on a full cluster.** The default rolling
  update needs room for a second full copy, and there is no strategy field (#474,
  open).
* **Silent reversions.** A manual scale to zero comes back "silently, and re-consumed
  the memory — extending the outage while it looked like the mitigation simply hadn't
  worked" (#490, open). Variable changes to an image app were "never rolled out" while
  the app said Ready (#471, open).
* **Probes that destroy data.** The default TCP liveness probe restarted PostgreSQL
  during its first boot and left "a permanently unusable volume that reported Ready"
  (#469; the v1.1.0 changelog removes the default probe for apps with storage).
* **A small server working hard for nothing.** "On my small k3s server, this kept k3s
  busy all day and the fans running nonstop" — the operator and the PV controller
  rewrote one PVC annotation about 40 times a second (#631, fixed in v1.2.0). An HTTP
  probe caused a no-op Deployment update every 2.7 s per app (CAI-71, fixed in v1.1.0).
* **Saving variables with a database bound failed** with "cannot overwrite …: managed
  by binding" for a variable nobody touched (#560, fixed in v1.2.0).
* **Releases lag the code.** "main sat 163 commits past the last tag for a month", and
  production "ran an untagged build 153 commits behind `main` for weeks and nothing
  said so" — the reasons for a weekly release-lag job and for `mortise version`.
* **The large pieces are missing:** OIDC (#152), an MCP server (#130), export (#163),
  backup tooling (#73), an interactive shell (#426), start-command overrides (#427),
  startup ordering for stacks (#167), all open.

## Security record

**Where I looked (2026-11-08):** the repository's security advisories page (refused
from this sandbox, 403), OSV for the Go module `github.com/mortise-org/mortise` (none),
NVD's keyword search for "mortise" (no results), the changelog, the repository's
issues, and the code at `8a274c0`. **No published advisory or CVE was found.**
`SECURITY.md` supports only the latest release, promises acknowledgement in 48 hours
and a fix within 14 days for critical issues, through GitHub's private reporting.

**Fixed, by the project's own audits** (changelog and issues):

* An inverted role check let a platform viewer list every user (CAI-445, fixed
  2026-09-30). Earlier: platform viewers could list all projects and activity (#316),
  project viewers could list deploy tokens (#328), variables and shared variables were
  returned in plain text by read APIs (#332), an SSE token stayed valid after a
  password change (#318), a reflected `Host` in the OG image (v1.0.0).
* Unauthenticated GitProvider creation through the device flow plus SSRF through an
  unvalidated host (#439, fixed 2026-08-09).
* Workload updates and garbage collection without ownership checks, so Mortise could
  change or delete objects it did not create (#437).
* Every resolved variable value, including ones moved into a Secret, sat in plain text
  in an annotation on the derived Secret until v1.1.0 (CAI-168).
* Bot pull requests bypassed `botPR: false` and received the source environment's
  secrets; a preview could clone another pull request's preview (CAI-450, -452).
* Two projects' apps with the same name shared one image repository, and an
  interrupted build could adopt the other project's image and "ship the wrong code"
  (CAI-454, v1.2.0).
* Login had no throttling and leaked usernames by timing; webhooks told unknown
  providers from bad signatures; an app without a `providerRef` accepted any provider's
  signed webhook (fixed in v1.1.0).

**Open, and confirmed in the code at `8a274c0`:**

* **#440 (P1, opened 2026-07-05): "unauthenticated privileged BuildKit + writable
  registry, no NetworkPolicies".** BuildKit listens on `tcp://0.0.0.0:1234` with
  `privileged: true` by default (`charts/mortise/values.yaml`, `buildkit.yaml`); the
  registry has no authentication and `REGISTRY_STORAGE_DELETE_ENABLED=true`, and its
  proxy listens on host port 30500 on every node (`registry.yaml`,
  `registry-proxy.yaml`); there is no NetworkPolicy anywhere in the charts or the
  operator. In the issue's words, any user code "can dial it, which is effectively root
  on the node", and "a tenant can push or delete images the platform then deploys".
  v1.1.0 made rootless BuildKit possible through values and hardened the registry's
  pod; the defaults are unchanged.
* **#441 (P2):** the operator holds cluster-wide read of Secrets and ConfigMaps, and
  `rolebindings create` cluster-wide (`charts/mortise-core/templates/rbac.yaml`).
  Skifity's panel holds more, cluster-admin, and says so (ADR-0014), so this is not a
  point in Skifity's favour.
* **#444 (P3):** the 7-day refresh window on an expired token, among others.

**By design:** no second factor; tokens in `localStorage`; variable values readable
back by anyone who can edit an app; preview environments that copy the source
environment's Secrets with bot pull requests on by default.

## Against Skifity

The Skifity column was checked in this repository on 2026-11-08 at `23ec07f`. Skifity is
a released self-hosted panel (v0.1.0 on 2026-10-06, v0.1.1 on 2026-10-21) whose
cluster-facing code runs on a real k3s on every push to `main` (`docs/progress.md`, "A
real cluster, at last"). Mortise is released, installs on any cluster, and runs its
integration suite on k3d for every pull request.

| Capability | Mortise | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git providers | GitHub by device flow, GitLab and Gitea by token | GitHub, GitLab, Gitea and Bitbucket by token, webhooks registered for you (`internal/gitsrc/webhook.go`, `bitbucket_webhook.go`, `hooks.go`) | Skifity ahead on providers; behind on connecting GitHub without a pasted token |
| Builders | Shared privileged BuildKit; Railpack or Dockerfile | A rootless BuildKit per build, a registry only the build's own token writes to (ADR-0026, `internal/builder/job.go`, `internal/registryguard`); Railpack, Nixpacks, Dockerfile, static (`internal/builder/detect.go`) | Skifity ahead |
| Deploy without a repository | Image, external service | Image, or a folder with `skifity up` (`internal/cli/up.go`, `internal/upload`) | Skifity ahead (folder) |
| Compose | One app per service from a pasted file | A stack from a file, building from its context (`internal/api/stack_handlers.go`) | Skifity ahead |
| Rollback | Image apps only; variables not restored (#215) | A new deployment with the old image and its runtime settings, shown as a plan first (`Rollback` in `internal/deploy/deployer.go`, `handleRollbackPlan` in `internal/api/deploy_handlers.go`) | Skifity ahead |
| What an image tag means | Built images pinned by digest; an image app runs its tag as written (`app.Spec.Source.Image` in Mortise's `app_controller.go`); a deploy record is "Confirmed" only once its pods ran it | The tag is recorded and deployed as written, with `IfNotPresent` (`Image` on `Deployment` in `internal/store/models.go`; `internal/kube/manifests.go`), so a rollback to `:main` runs whatever `main` is now; a deployment succeeds only once its rollout finished (`WaitForRollout` in `internal/kube/client.go`) | parity; neither resolves a tag (gap 4) |
| Port of an image app | Read from the image's configuration (v1.2.0) | 8080 unless typed (`DefaultAppPort` in `internal/kube/naming.go`, `defaultInt(req.Port, …)` in `internal/api/apps_handlers.go`); the known-image table is used only for Compose services (`KnownImagePort` in `internal/builder/detect.go`) | **behind** (gap 5) |
| CI deploy of a built image | `image` in the deploy call with a token for one app and environment, CI recipes | The deploy body is `commit_sha`, `force`, `accept_vulnerabilities` (`internal/api/deploy_handlers.go:19-26`); tokens belong to a user and a team, with no app (`APIToken` in `internal/store/models.go`); no CI section in `docs/cli.md` | **behind** (gap 3) |
| A saved variable reaches the pods | Not by default (`autoRedeploy` off); a condition says so | Saving rolls the app out, without a build when only runtime inputs changed (ADR-0007, `docs/faq.md`) | Skifity ahead |
| **Domains, TLS and routing** | | | |
| Certificates | cert-manager; any ClusterIssuer the cluster has, or an existing Secret, per environment | HTTP-01 from the panel's issuer (`internal/cluster/issuer.go`), a team's own uploaded certificates (`internal/tlscert`); no DNS-01 or wildcard issuance (`docs/concepts.md`, "What is not here") | parity (Mortise takes any issuer; Skifity takes uploads without kubectl) |
| Why a certificate is missing | The newest CertificateRequest's failure, at once | The Certificate's Ready message only, called failed after 15 minutes (`CertificateStatus` in `internal/cluster/cluster.go`; `checkCertificate` and `stale` in `internal/watch/watch.go`) | **behind** (gap 1) |
| Redirects, firewall, maintenance | None; annotations | Redirects between hostnames, IP/country/ASN rules, password protection, maintenance (`internal/api/domain_redirects.go`, `internal/edgerules`, `internal/guard`, `internal/api/maintenance_handlers.go`) | Skifity ahead |
| DNS records | ExternalDNS, installed by the user | Cloudflare, Hetzner, DigitalOcean, Route 53 (`internal/dnsprov`) | Skifity ahead |
| **Databases and services** | | | |
| Databases | An image app with a volume; no backups | Nine managed engines with sealed credentials and links (`internal/dbsvc/engine/engine.go`) | Skifity ahead |
| Wiring apps together | `{APP}_URL`, `{APP}_HOST`, `{APP}_PORT` per binding; `DATABASE_URL` for one database | `DATABASE_URL` from a database link; other apps by name and port with nothing injected (`docs/concepts.md`, "How the others reach it") | behind (small; `railway.md` P1) |
| A database Mortise does not run | `external` source with bindable credentials | Project variables (`/api/projects/{project}/variables` in `internal/api/api.go`) | behind (small, gap 15) |
| Files from secrets | `secretMounts`, `configFiles` | Files, secret or not, sealed (`docs/concepts.md`, "Files") | parity |
| **Storage and backups** | | | |
| Volumes | Size, class, access mode; claim kept on removal | Volumes with scheduled backups, verify and restore (`/api/apps/{app}/volumes/…` in `internal/api/api.go`, `internal/backup`) | Skifity ahead |
| Panel backup | Recipes for k3s snapshots and Velero | Scheduled panel backups and `skifity admin restore-db` (`docs/backups.md`) | Skifity ahead |
| **Scaling and HA** | | | |
| Instances | `replicas` per environment, requests equal limits | Instances, HPA, scale to zero through KEDA, requests apart from limits and no CPU limit by default (`internal/kube/scaletozero.go`, `buildResources` in `internal/kube/manifests.go`) | Skifity ahead |
| Can it run scaled | `MORTISE_REPLICAS` for the app to check itself | The scaling readiness check names the variable that breaks (`internal/deploy/scaling.go`) | Skifity ahead |
| **Observability** | | | |
| Rollout that never starts | `RolloutStalled` with the blocking reason | The rollout fails with `deploy.rollout_timeout`, `deploy.image_pull_failed` or `app.crash_loop`, with scheduler messages explained (`internal/errdoc/catalogue.go`, `internal/kube/explain.go`) | parity |
| Changed outside the panel | `mortise diff` on variables across spec, Secret and pods | Drift over every object the panel applies, who changed it, repair, `app.drifted`, `skifity drift` (`internal/deploy/drift.go`, `internal/kube/drift.go`) | Skifity ahead |
| What an app knows about itself | `MORTISE_REVISION`, `MORTISE_IMAGE` (with the digest for a built image), `MORTISE_REPLICAS` | `SKIFITY_APP`, `_ENVIRONMENT`, `_COMMIT_SHA`, `_URL`, `_PREVIEW`, `_PULL_REQUEST`, `POD_NAME`; no image or deployment (`buildPlainEnv` in `internal/kube/manifests.go`) | behind (small, gap 4) |
| A variable the platform overrides | `EnvKeysReserved` names it | Accepted and silently ignored: `SanitiseEnvKey` refuses only GPU device names (`internal/kube/naming.go`), and the platform's `PORT` and `SKIFITY_*` beat the app's Secret | **behind** (gap 7) |
| A secret typed as a plain value | `PlaintextCredentials` reports it | Stored as a secret automatically (`secretness` in `internal/api/apps_handlers.go`, `LooksSecret` in `internal/logging/logging.go`) | Skifity ahead |
| Log history | 48 hours in the observer, pods that are gone included | Kubernetes' own logs, at most 10,000 lines, and the previous container (`internal/api/stream_handlers.go:427-449`); history through a drain (`internal/logdrain`) | **behind** (gap 8) |
| Metrics and traffic | 72 h of metrics, 48 h of traffic from the access log, gaps drawn | Usage and traffic charts with gaps drawn (`web/src/components/app/usage-card.tsx`, `internal/traffic`) | parity |
| Alerts | None | App and server thresholds, missed backups, certificates, drift, ten channels (`internal/notify/notify.go`) | Skifity ahead |
| **Git webhooks** | | | |
| A refused delivery | `WebhookSignature` on the provider | A log line (`internal/api/webhook_handlers.go:53`); nothing on the connection (`GitSource` in `internal/store/models.go`) | **behind** (gap 2) |
| A hook that is gone or incomplete | Re-verified hourly; event set checked | Ensured when an app is created or starts deploying tags, never re-checked; any hook with the panel's URL counts, active or not, with or without `pull_request` (`ensureGitHubHook` in `internal/gitsrc/hooks.go`) | **behind** (gap 2) |
| A push that never arrived | Branch head re-read hourly, repaired | Nothing reads a branch head ("Nothing else in the panel polls", `internal/watch/watch.go`) | **behind** (gap 2) |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | bcrypt, JWT in `localStorage`, no 2FA | Argon2id, TOTP, passkeys, sessions with `__Host-` cookies (`internal/auth`, ADR-0019) | Skifity ahead |
| SSO | None | Generic OIDC with groups mapped to teams (`internal/api/sso_handlers.go`) | Skifity ahead |
| Roles | Platform and project roles; restricted environments | Owner, admin, member, viewer; members limited to projects (`internal/store/models.go`); no protected environment (`Environment` in the same file) | behind (gap 6) |
| Secrets | Readable back by editors; `env pull` | Write-only, sealed with context (`internal/crypto`) | Skifity ahead |
| Isolation | No NetworkPolicy; privileged BuildKit open to tenants | Default-deny per environment, pods wait for their policy, restricted Pod Security by default (`internal/kube/namespace.go`, `internal/netfence`, ADR-0027) | Skifity ahead |
| Release signing | cosign keyless, SBOM and provenance on both images | SLSA provenance on the binaries; the panel image is unsigned and also tagged `latest` (`.github/workflows/release.yml`, `.goreleaser.yaml`) | **behind** (gap 9) |
| **Preview environments** | | | |
| Previews | Whole project per PR, Secrets cloned, empty databases, no fork rule found | Per PR or whole environment, preview-only values, fork previews get no secrets, seeded databases (`internal/api/webhook_handlers.go` `copyPreviewVariables`, `internal/api/preview_manual.go`, Phases 93 and 107) | Skifity ahead |
| Telling the Git host | Promised in the spec; nothing calls the commit-status code | Commit statuses and a pull-request comment (`internal/gitsrc/report.go`, `internal/deploy/gitreport.go`) | Skifity ahead |
| **Templates** | Five and a Supabase stack | 374 (`internal/templates/catalogue`) | Skifity ahead |
| **CLI, API, IaC, integrations** | | | |
| API | About 109 routes, OpenAPI | About 270 routes, OpenAPI (`internal/api/api.go`, `internal/api/openapi.go`) | Skifity ahead |
| MCP | None (#130) | stdio and `/api/mcp`, about 45 tools (`internal/mcpserver`) | Skifity ahead |
| Config as code | The custom resources, with Argo CD or Flux | `skifity.yaml` plan and apply (`internal/blueprint`) | different |
| Version | `mortise version` prints the CLI's and the operator's | The CLI's own only (`internal/cli/commands.go:119-121`); the panel's is in `/api/health` (`internal/api/health.go`) | behind (small, gap 10) |
| Reaching a private app | `mortise proxy`, Open through the API | `skifity db connect` for databases only (`internal/cli/dbconnect.go`) | behind (small, gap 12) |
| **Notifications** | In-app list | Ten channel kinds, fourteen events (`internal/notify/provider.go`, `notify.go`) | Skifity ahead |
| **Multi-server and networking** | The user's cluster | Servers over SSH or created at Hetzner or DigitalOcean, WireGuard between them (`internal/provision`, `internal/cloud`) | Skifity ahead |
| **Team and collaboration** | Projects, members, shared variables, clone | Teams, invitations, project variables, environment clone (`internal/api/envclone_handlers.go`) | Skifity ahead |
| **Developer experience** | | | |
| Install | Helm on any cluster, or k3s/k3d from one line, also on a laptop | One installer on a Linux server (`installer/install.sh`, `docs/quick-start.md`) | different |
| Languages | English | Five with a build gate (`web/src/locales`) | Skifity ahead |

## Gaps worth closing in Skifity

**P1**

1. **Say why HTTPS is not working, from cert-manager's latest attempt.** *Why:* Skifity
   reads only the Certificate's `Ready` condition (`CertificateStatus`,
   `internal/cluster/cluster.go`) and calls a domain failed only once it is 15 minutes
   old (`stale`, `internal/watch/watch.go`). While cert-manager backs off, that message
   is usually some form of "issuing", which says nothing about the cause; the cause — a
   challenge answered with 404, a record that points elsewhere, a rate limit — is on the
   CertificateRequest, the Order and the Challenge. Mortise added this in v1.2.0 after
   "the link was dead but the platform claimed success", and disables Open when the
   certificate failed. *Sketch:* in `CertificateStatus`, when not Ready, read the newest
   CertificateRequest by the `cert-manager.io/certificate-name` label and revision, then
   the Challenges for the hostname (`acme.cert-manager.io/v1`, `status.reason`, `state`);
   classify the common reasons (wrong status code, DNS lookup failed or pointing
   elsewhere, connection refused on port 80, rate limited, CAA) into coded explanations
   in all five languages, with the record Skifity expects where DNS is the cause; mark
   the domain failed on a terminal failure instead of waiting 15 minutes; show "HTTPS
   is not working" on the app's header and keep its link from promising `https://`.
   *Size:* S. *Without a cluster:* the reading and classification against fake objects,
   yes; the real error texts need one, and the Cluster workflow's Pebble server can
   produce a failing challenge.
2. **Know when push-to-deploy has stopped working.** *Why:* deploy on push is the core
   loop and every way it breaks is silent. A delivery with a bad signature is a log line
   (`internal/api/webhook_handlers.go:53`) and nothing on the connection, which has no
   delivery fields (`GitSource`, `internal/store/models.go`). The hook is ensured when an
   app or a stack is created and when an app starts deploying tags (`ensureWebhookFor`,
   `internal/api/apps_handlers.go`), never after, and any hook with the panel's URL
   counts as "already there", even a disabled one or one without `pull_request`, so
   previews quietly never happen (`ensureGitHubHook`, `internal/gitsrc/hooks.go`). A
   hook deleted on the Git host, or a push the host never sent, is never noticed.
   Mortise has `WebhookSignature` (v1.1.0), re-verifies hooks hourly and checks their
   events (v1.2.0), and re-reads the branch
   head hourly after "GitHub generated no push event for a squash merge" left an app on
   an old commit while it said Ready. *Sketch:* keep on each connection the last verified
   delivery and the last refused one, by class, written at most once a minute because the
   endpoint is public; record the `ping` a host sends when a hook is created, and say
   within a minute that "the Git host could not reach the panel at …" when it does not
   come; once a day re-list each hooked repository's hooks, re-create a missing one and
   fix `active` and the events; once an hour, for each app that deploys on push, read
   the branch head (the four branch APIs `internal/gitsrc/repos.go` already calls; add
   the commit to `Branch`) and when it is ahead of the watched commit
   (`internal/store/watched.go`) for a full interval, deploy it as a push would, or only
   say so if the team prefers; a `git.webhook_failing` event and a line on the
   connection and the app pages. *Size:* M. *Without a cluster:* yes, against
   `httptest` Git hosts.
3. **Deploy a CI-built image with a token that can do nothing else.** Still open from
   `kamal.md` and `bex.md`; Mortise is the third product with it. *Why:* a CI job today
   rewrites `image:` in `skifity.yaml` and runs `skifity apply`, or makes two API calls
   with a user's team-wide token (`deployRequestBody`, `internal/api/deploy_handlers.go`;
   `APIToken`, `internal/store/models.go`). Mortise's deploy token belongs to one app and
   one environment and is shown once, the call takes the image, and its docs carry the
   GitHub Actions and GitLab CI jobs (`docs/recipes/external-ci.md`). *Sketch:* `image`
   on `POST /api/apps/{app}/deploy`, recorded on the deployment; a deploy token per app
   (stored as a hash, shown once, revocable, audited, refused while deploys are locked)
   that can only call that route; the GitHub Actions and GitLab CI snippets on the token
   dialog and in `docs/cli.md`; `skifity deploy --image`. *Size:* S–M. *Without a cluster:* yes.

**P2**

4. **Run, record and roll back to the digest, not the tag.** *Why:* an image app's
   deployment records the tag as written (`Image`, `internal/store/models.go`), the
   Deployment pulls `IfNotPresent` (`internal/kube/manifests.go`), and a rollback deploys
   `previous.Image` (`Rollback`, `internal/deploy/deployer.go`). With `:main` or `:latest`
   a rollback is not a rollback, and two servers can run different contents of one tag in
   one deployment. Mortise does not solve this for image apps either — it runs the tag as
   written — but it pins its own builds by digest, puts the reference in `MORTISE_IMAGE`,
   and its CAI-1141 is the trap to avoid: a manifest `HEAD` without an `Accept` header can
   return a digest the registry cannot serve. Skifity's docs say "Each deployment records
   the image it produced" (`docs/concepts.md`, "Deployments and rollback"), which for a
   tag is not true. *Sketch:* at deploy, resolve the tag through the registry API with
   the team's credentials
   (`internal/store/registries.go`), declaring every pullable manifest media type; write
   `image@sha256:…` into the Deployment; record tag and digest on the deployment and show
   both on the Deployments tab; roll back to the digest; `SKIFITY_IMAGE` and
   `SKIFITY_DEPLOYMENT` beside `SKIFITY_COMMIT_SHA`. A registry that cannot be read leaves
   the tag in place and says so. *Size:* M. *Without a cluster:* yes against an `httptest`
   registry; pulls on the Cluster workflow.
5. **Take an image's port from the image.** *Why:* `nginx:1.27` created from the form gets
   port 8080 and fails its health check unless somebody types 80 (`DefaultAppPort`,
   `internal/kube/naming.go`); the table of known ports exists but only Compose services
   use it (`KnownImagePort`, `internal/builder/detect.go`). Mortise's quickstart example
   crash-looped "out of the box" for exactly this before v1.2.0. *Sketch:* with the
   registry client from gap 4, read the image configuration's `ExposedPorts` and take the
   lowest TCP port, else the known-image table; fill the form as a suggestion, and use it
   in the API, CLI, MCP and `skifity.yaml` when no port is given. Best effort, one
   bounded attempt per image reference, never a refusal. *Size:* S. *Without a cluster:*
   yes.
6. **Protected environments.** Also gap 6 of `bex.md`. *Why:* a member who may deploy
   staging may change production's variables or delete its database in the same project;
   `Environment` has no such field (`internal/store/models.go`). Mortise's
   `restricted: true` makes an environment read-only for developers and forbids cloning
   previews from it. *Sketch:* a `protected` flag (on for production in a new project),
   checked in `authorizeApp`, `authorizeEnvironment` and `authorizeDatabase` for
   variables, files, domains, deletion and maintenance, with a coded refusal and the route
   walk extended; deploy and rollback stay open. *Size:* S–M. *Without a cluster:* yes.
7. **Refuse or explain the names the platform sets.** *Why:* `PORT`, `POD_NAME` and
   anything `SKIFITY_*` can be saved as variables (`SanitiseEnvKey`,
   `internal/kube/naming.go`), and the platform's own value, set on the container, wins
   over the app's Secret (`buildPlainEnv`, `internal/kube/manifests.go`), so the saved
   value does nothing and nothing says so. Mortise reports `EnvKeysReserved`. *Sketch:*
   refuse `SKIFITY_*` and `POD_NAME` with an `errdoc` entry; for `PORT`, refuse with "set
   the port under Settings", and in a `.env` import, a Compose file or `skifity.yaml` take
   it as the app's port and say so in the result. *Size:* S. *Without a cluster:* yes.
8. **Logs of instances that are gone.** Also in `hakopod.md`. *Why:* after a rollout,
   an eviction or a crash the old pod's lines are gone; Skifity reads the kubelet and the
   previous container only (`internal/api/stream_handlers.go:427-449`), so a beginner
   without a drain loses what explains last night. Mortise's observer keeps 48 hours,
   departed pods included. *Sketch:* the same Vector the drains use
   (`internal/logdrain`) writing to a small store the panel reads, a day or two by
   default, with time and pod filters on the Logs tab and gaps shown. *Size:* M. *Without
   a cluster:* the store and queries yes; the shipping needs one.
9. **Sign the panel's image.** *Why:* the binaries carry SLSA provenance, but the image
   the installer and `skifity upgrade` pull is unsigned and also published as `latest`
   (`.github/workflows/release.yml`, `.goreleaser.yaml`). Mortise signs both of its
   images with cosign keyless and attaches SBOM and provenance since v1.0.0. *Sketch:*
   `cosign sign` by digest in the release workflow with GitHub's OIDC identity, an SBOM
   attestation, the installer and the upgrade pinning the digest, and the verify command
   in `docs/releasing.md`. *Size:* S. *Without a cluster:* yes.

**P3**

10. **`skifity version` names the panel's version too**, and says when the CLI is older
    or newer. Today it prints `version.Full()` alone (`internal/cli/commands.go:119-121`)
    though `/api/health` carries the panel's (`internal/api/health.go`). Mortise added it
    after production ran a build 153 commits behind for weeks unnoticed. S, no cluster.
11. **Each instance says which deployment it runs.** `Instance` has no revision or image
    (`internal/kube/client.go`), though pods carry the revision annotation; during a
    stuck rollout the Instances list cannot tell old from new. S, no cluster (fake
    clients).
12. **Reach a private app from a laptop.** `skifity connect <app> [--port]`, the tunnel
    `skifity db connect` already uses (`internal/cli/dbconnect.go`), for internal apps
    such as an admin interface. Mortise has `mortise proxy`. M; the tunnel needs a
    cluster.
13. **A deploy that does not fit beside the running instance says so.** When a rollout is
    unschedulable only because the old instance holds the room — one instance, rolling
    update, insufficient CPU or memory — the failure should say that and offer to replace
    it in place for that deploy; today the explanation is "No server has enough free
    memory for this instance" (`internal/kube/explain.go`). Mortise's #474 is the open
    version of this. S–M; needs a cluster.
14. **Everything an app receives, and from where.** A read-only list on the Variables tab
    of what the container gets: its own variables, the project's, the database link's,
    secret-manager references, and the platform's (`PORT`, `SKIFITY_*`), each with its
    source and which one wins. Mortise shows source badges. S, no cluster.
15. **An outside database that links like a managed one.** Host, port and credentials
    sealed once, linked to apps with the same `DATABASE_URL` and rotation as a managed
    database; today it is a project variable. Mortise's `external` source. S–M, no
    cluster.

## Things to deliberately not copy

* **Secrets that come back.** Mortise returns variable values to anyone who can edit an
  app and has `env pull`. Keep Skifity's write-only sealing.
* **Shared build infrastructure open to tenants.** A privileged BuildKit on an open port,
  a registry anyone can write and delete, and no NetworkPolicy (#440, open four months)
  is the arrangement ADR-0026 and ADR-0027 replaced. Keep a builder per build, the
  registry guard and default-deny.
* **Two writers for one value.** Mortise lets the interface edit the Secret while the
  custom resource holds the spec, then needs `SpecEnvApplied`, `EnvKeysRetained`,
  `mortise diff` and an Argo CD `ignoreDifferences` to explain the result. Skifity's panel
  writes and the cluster follows; drift is about kubectl, not about the product
  disagreeing with itself.
* **A saved setting that is not live** (`autoRedeploy` off by default). A condition that
  explains a footgun is still the footgun.
* **Rollback that refuses Git apps and forgets variables.** Skifity's rollback restores the
  image and the runtime settings of the version.
* **`MORTISE_REPLICAS`.** Under autoscaling or scale to zero the number is wrong the moment
  it is read; the scaling readiness check is the right place for this.
* **Requests equal to limits** (#489) and a default liveness probe on a stateful app
  (#469). Skifity already keeps requests apart from limits.
* **Everything else as a recipe.** Backups through Velero, SSO through a proxy, alerts
  through Prometheus: a product for teams "when there is no platform team" that asks them
  to install five platforms. Skifity's built-in backups, SSO and alerts are the point.
* **Unvalidated annotations onto every object** ("these footguns are not guardrailed").
* **Installing onto any existing cluster** before the one-server install is proven, already
  on the list in `docs/research/competitors.md`.
* **Upgrades that restart or rebuild everything, and one-way migrations.** v1.2.0 restarts
  every app and rebuilds every Git app; past v1.1.0 a downgrade is unsafe; the fix for the
  last upgrade's broken digests is unreleased and needs `kubectl`.
* **Previews that copy the source environment's secrets with no rule for forks.**
* **Tokens in `localStorage`, and no second factor.**

## Sources

All read on 2026-11-08 (the evening, UTC, running into 2026-11-09).

* https://github.com/mortise-org/mortise (metadata through the GitHub search API,
  `search/repositories?q=repo:mortise-org/mortise`; the repository API refused this
  sandbox)
* A blobless clone of https://github.com/mortise-org/mortise.git at `8a274c0`:
  `README.md`, `CHANGELOG.md`, `ARCHITECTURE.md`, `SPEC.md`, `SECURITY.md`, `RELEASING.md`,
  `REVIEW.md`, `llms.txt`; `docs/quickstart.md`, `docs/install.md`,
  `docs/cluster-setup.md`, `docs/recipes/external-ci.md`, `docs/recipes/backup.md`;
  `charts/mortise/values.yaml` and `charts/mortise/templates/buildkit.yaml`,
  `registry.yaml`, `registry-proxy.yaml`, `charts/mortise-core/templates/rbac.yaml`;
  `api/v1alpha1/project_types.go`, `app_types.go`; `internal/api/server.go`, `env.go`,
  `exec.go`, `compose.go`, `rollback.go`; `internal/controller/app_controller.go`;
  `internal/webhook/handler.go`; `internal/auth/jwt.go`; `cmd/cli/` (`diff.go`,
  `diff_report.go`, `proxy.go`, the command list); `cmd/observer/traffic_collector.go`;
  `ui/src/lib/templates.ts`, `ui/src/lib/api.ts`, `ui/src/routes/extensions/+page.svelte`;
  `internal/templates/supabase/docker-compose.yml`; `.github/workflows/ci.yml`,
  `release.yml`, `release-lag.yml`; `git log` and `git shortlog` for dates and authors
* https://ungh.cc/repos/mortise-org/mortise/releases and
  https://ungh.cc/repos/mortise-org/mortise (release dates and notes, stars, forks)
* GitHub search API: `repo:mortise-org/mortise` issues and pull requests, open and
  closed (321 and 336), the bodies of #130, #152, #215, #440, #441, #444, #469, #471,
  #474, #489, #490, #560 and #631, open pull request #658; mentions of
  `mortise-org/mortise` outside the repository (none)
* https://mortise.me/ and https://mortise.me/pricing (404)
* https://hn.algolia.com/api/v1/search?query=mortise (by text, and with
  `restrictSearchableAttributes=url`) and `search_by_date` since April 2026: nothing about
  this Mortise
* https://www.reddit.com/search.json?q=mortise%20kubernetes (refused, 403)
* https://api.osv.dev/v1/query for `github.com/mortise-org/mortise` (no record)
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=mortise (no result)
* https://github.com/mortise-org/mortise/security/advisories (refused, 403)
* Skifity files named in "Against Skifity" and the gaps, read in this repository at
  `23ec07f`
