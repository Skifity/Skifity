# Epinio

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Researched 2026-09-30 from the live repository and its history, the docs
repository and site, the Helm chart, the release blog, Krumware's pages and
NVD/OSV. Every number below carries its source and the date it was read; where
something could not be confirmed it says so.

Epinio is an "opinionated platform that runs on Kubernetes to take you from
Code to URL in one step": `epinio push` uploads a folder (or names a Git
revision or an image), a staging Job builds it with Paketo Cloud Native
Buildpacks — or, since 1.14.2, Kaniko from a Dockerfile — and the result is
deployed as a Helm release with a route on a wildcard domain. SUSE's Rancher
team built it from 2020 in the Cloud Foundry tradition (`cf push` for
Kubernetes) for **developers inside organisations whose platform team already
runs Kubernetes**, and the docs still speak to two personas: developers, who
need "no Kubernetes knowledge", and operators, who "work directly with
Kubernetes". Architecture: a Go API server, a web UI (standalone and as a
Rancher Dashboard extension) and a Go CLI, all installed by **one Helm chart**
that also brings Dex (OIDC), SeaweedFS (S3 for uploaded sources; MinIO until
1.13.10), a container registry, Reflector (kubed until 1.13.8) and a Helm
controller. It keeps **no database**: apps, app charts, catalog services and
builder images are CRDs, users are Kubernetes Secrets and roles are ConfigMaps.
Licence Apache-2.0. Pricing: free; commercial support subscriptions from
Krumware, price not published. **Is it still maintained? Yes — after nearly
dying.** Releases came every month or two through 2023 (v1.11.0,
2023-12-01), then stopped for twenty months (33 commits in all of 2024);
stewardship passed from SUSE to **Krumware**, a platform-engineering
consultancy and "Diamond SUSE Partner", beginning January 2025; v1.12.0
shipped 2025-08-26, and **12 tagged releases followed in the twelve months to
2026-09-30**, the latest **v1.14.2 on 2026-09-03**, with 696 commits in the
same year — 66% of the human ones by David Johnson, 30% by Pranay Sanghvi (git
history). Krumware publishes a support matrix (1.14.x: full support to
2027-03-31, end of life 2027-12-31). Adoption is small: **611 stars, 64 forks,
39 open issues and PRs** (GitHub API, 2026-09-30); Hacker News has one Epinio
story (2022, 1 point; Algolia search); no user counts have been published. Minimum
footprint per its own requirements: **2–4 vCPU and 8 GB of RAM**.

## Feature inventory

### Deploy sources and builds

- `epinio push` from a **local folder** (uploaded as a tarball to S3), a **Git
  URL and revision**, or a **container image**; `epinio.yml` manifests hold
  the settings (instances, routes, env, bindings, chart values).
- **Build modes**: `buildpack` (default; Paketo builder images, selectable per
  app, managed as a `BuilderImage` CRD from the UI/CLI since 1.14.1) and
  `dockerfile` (Kaniko `v1.23.2-debug`, since 1.14.2). In Dockerfile mode the
  app's variables reach the build only as `--build-arg`s the Dockerfile
  declares.
- Staging scripts live in a ConfigMap and can be replaced; custom builders and
  "staging config override" are documented operator tasks.
- Private Git: **git configs** per host; since 1.14.1 the config must be chosen
  explicitly and its credentials are only sent to the host it is scoped to.
- **No push-to-deploy webhooks.** The documented route is Rancher Fleet's
  GitJob CRD, with the user's `~/.config/epinio/settings.yaml` — which holds the
  base64 password — uploaded as a Kubernetes Secret (docs, "Pushing with a Git
  job"). "Track changes on branch when origin is git repo" is an open request
  from March 2022 (#1269).
- `epinio app restage`, redeploy of a locally sourced app (fixed in 1.14.2).
- **App watch** (experimental, 1.14.1): syncs local changes into the running
  pod through a supervisor injected as PID 1.
- **No rollback**: the word does not appear in the docs (grep, 0 files).

### Domains, TLS and routing

- Requires a **wildcard domain** pointed at the ingress controller; each app
  gets `<app>.<domain>`, plus custom routes.
- TLS through cert-manager with four issuers; the **default is `epinio-ca`, a
  private CA** (`global.tlsIssuer: "epinio-ca"` in the chart's `values.yaml`),
  so a default install serves certificates browsers do not trust; Let's
  Encrypt is opt-in. Routing secrets for bring-your-own certificates.
- Any ingress controller with a default IngressClass (Traefik recommended);
  **Gateway API** support since 1.14.1; optional separate workload ingress.
- On bare metal the docs point to MetalLB for an external IP.

### Databases and services

- A **service catalog** of Helm-chart-based service classes, extendable by the
  operator with any chart (the docs suggest cloud operators or Crossplane).
- The five defaults — MySQL, PostgreSQL, Redis, RabbitMQ, MongoDB — are
  **Bitnami charts with a `-dev` suffix** because "they might not be suitable
  for production usage". PostgreSQL is chart **12.1.6 / PostgreSQL 15.1.0**
  from `bitnamilegacy`, the repository Broadcom stopped updating in 2025
  (`templates/service-catalog/postgresql-dev-service.yaml` in epinio/helm-charts).
- **Configurations** — named sets of key/values — and service bindings reach
  the app **as files under `/configurations/<name>/`**, not as environment
  variables; the request to expose them as variables has been open since
  July 2022 (#1647).
- `epinio service port-forward` opens a tunnel to a service from the laptop.

### Storage and backups

- A stateful app chart (`application-stateful`, a StatefulSet) and PVCs;
  `epinio app delete --delete-pvc` since 1.14.2 (data kept by default).
- A default StorageClass is required and **ReadWriteMany is preferred**.
- **No backups** of services or volumes. The storage page covers sizing the
  S3 store for uploaded sources.
- **Export**: an app's image and a Helm chart for it can be pushed to an OCI
  registry "for pickup by, and use with, `helm`".

### Scaling and high availability

- An instance count per app. **No autoscaling for apps** — the chart's
  `autoscaling` block is for the API server.
- HA is the cluster's; the platform pieces (server, UI, Dex, registry,
  SeaweedFS) are ordinary Deployments with their own replica settings.

### Observability (logs, metrics, alerts, uptime)

- Runtime and staging logs in the CLI and UI; deployment progress in a UI
  notifications panel (1.14.0); a downloadable cluster metrics report
  (1.13.10); request tracing for the server.
- metrics-server is a prerequisite; no usage history, no alerts, no uptime
  checks, **no outbound notifications** of any kind were found.

### Security, auth, roles, SSO, audit

- TLS between components; Basic auth or **OIDC through Dex**, with connectors
  (GitHub, LDAP, Entra ID, Rancher SSO, AWS IAM are documented) configured by
  editing the `dex-config` Secret, and a **`rolesMapping` from external groups
  to Epinio roles**.
- **Roles**: `admin` and `user` by default, plus `view_only`,
  `application_developer`, `application_manager`, `system_manager` since
  1.13.10; custom roles assemble fine-grained actions (`app_scale`,
  `app_update_env`, `app_exec`, `service_portforward`, `builderimage_write` ...)
  and can be **namespace-scoped**. A user who creates a namespace becomes its
  admin.
- **Administration is kubectl**: a user is a `BasicAuth` Secret with the
  `epinio.io/api-user-credentials` label and a bcrypt hash you generate; a role
  is a ConfigMap with the `epinio.io/role` label (Authorization reference).
- **Default credentials**: API users `admin` and `epinio`, both `password`;
  Dex local users `admin@epinio.io` and `epinio@epinio.io`; registry password
  `changeme` in the chart defaults — the docs say to override them "in a
  production setup".
- Signed images (cosign, keyless via GitHub OIDC) and SBOMs; air-gapped
  installs through `global.cattle.systemDefaultRegistry`.
- No audit log was found.

### Preview environments and branches

- **None.** Namespaces are the only separation; no per-PR or per-branch
  environments are documented or found.

### Templates and catalogue

- No one-click app catalogue. **App charts** — operator-supplied Helm chart
  templates an app can choose (standard, stateful, Gateway API, and an
  `standard-elevated` one for the MCP server) — and the five `-dev` services.
- Example apps in the docs.

### CLI, API, IaC, integrations

- Go CLI (`brew install epinio`) covering apps, env, configurations, services,
  catalog, app charts, builder images, git configs, namespaces, exec,
  port-forward, export; REST API with Swagger; manifests as IaC; the CRDs are
  the GitOps surface.
- **MCP server** (`epinio/mcp`, beta since 1.14.1, "feature parity with the
  CLI" in 1.14.2): Streamable HTTP, deployed onto the cluster as an Epinio app;
  **core tools act only through the Epinio REST API as the calling user**; an
  opt-in **elevated tier** reaches into Kubernetes to *adopt* an existing
  kubectl-managed Deployment into Epinio's view (`adopt_app`, `reconcile_app`,
  `release_app`). When a request carries no `Authorization` header the server
  "falls back to the credentials it was configured with (default `admin` /
  `password`)" (MCP reference). A downloadable **agent skill** maps the tools
  to CLI commands where MCP cannot run.
- **Rancher Dashboard extension** with one-click install (1.13.10); an older
  Docker Desktop extension.

### Notifications

- None to email, chat or webhooks.

### Multi-server and networking

- Whatever the cluster is. Epinio installs nothing on servers and adds none.
  Supported platforms per Krumware: RKE2, RKE1, K3s, AKS, EKS, GKE — Kubernetes
  1.31–1.33 on the support page, while the docs' system requirements say
  1.34–1.36.

### Team and collaboration

- Namespaces as the tenancy unit, namespace-scoped roles, SSO groups mapped to
  roles. No invitations or team objects; users are created by an operator.

### Developer experience and onboarding

- A developer's path is short once the platform exists: `epinio login`,
  `epinio push`, a URL. Krumware's line: "the 'aha' moment hits when that first
  app goes from zero to live in under a minute" (sponsored post, The New Stack,
  2026-07-09).
- The operator's path is long: a cluster, an ingress controller with a default
  IngressClass, cert-manager, metrics-server, a default (preferably RWX)
  StorageClass, a wildcard DNS record, then the chart — and on a local cluster
  hostPort tricks or MetalLB.
- The UI was rebuilt on Krumware's "Trailhand" design system in 1.14.0.
- The docs are versioned, current (last commit 2026-09-28) and split by persona.

## What users love

- **One command from source to URL on a cluster the company already runs.**
  "I like Epinio ... It is backed by Suse and lightweight compared to KNative ...
  I still prefer k8s due to the vast ecosystem of mature solutions. And I can
  still run everything on a single box, it just needs to be a bit bigger" (HN,
  June 2024). "`epinio push --name myapp` feels similar enough" to `git push`
  (same thread).
- **Cloud Foundry without Cloud Foundry.** The positioning SUSE launched it with
  and Krumware keeps: buildpacks, bindings, a platform team that sets the
  guardrails (SUSE blog "Meet Epinio"; The New Stack, 2026).
- **Operator control.** App charts, builder images, a service catalog and
  fine-grained roles let a platform team decide what "a deploy" means — the
  feature Krumware sells as "golden paths".
- **It is alive again.** Twelve releases in a year, a support matrix and
  current documentation after a year and a half of silence.

## What users complain about

- **Resources.** "being kubernetes based still requires more resources than
  dokku or Piku" (HN, 2024); the project's own minimum is 8 GB.
- **No `git push`.** "Epinio does admittedly not support git push" (HN, 2024);
  push-on-commit needs Rancher Fleet (docs) and branch tracking is an open
  request since 2022 (#1269).
- **Configuration as files.** Configurations cannot be environment variables
  (#1647, open since 2022) — every twelve-factor app needs glue.
- **Long-standing gaps left open.** The open list sorted by reactions is
  dominated by 2021–2023 issues: an events hook (#593, 2021), offline builders
  (#1820), "Admin as Task" (#1681), s3gw quota errors (#2105). The revival has
  worked through releases, not through that backlog.
- **Churn in the parts underneath.** 1.13.8 replaced kubed and asked users to
  uninstall it by hand; 1.13.10 replaced MinIO and asked internal-MinIO users
  to **back up and restore their source blobs** across the upgrade; 1.14.1
  changed CRDs, added a `kubectl` hook Job that "blocks" the upgrade, and broke
  dashboard deploys for custom roles missing `builderimage_read` (Upgrading
  page). 1.13.10 also fixed "service bindings being wiped out when an
  application is redeployed" (release blog).
- **Thin release notes.** The GitHub release pages list merge commits; the
  substance is on the blog and the upgrading page.

## Security record

- **No CVEs and no GitHub security advisories** found: NVD keyword search
  "epinio" (0), OSV for `github.com/epinio/epinio` (0), OpenCVE (0), the
  repository's advisories page (none published; private reporting enabled).
- What stands out instead is **defaults**: `admin`/`password` and
  `epinio`/`password` API users, Dex users with the same password, a registry
  password of `changeme`, a private CA as the default TLS issuer, and an MCP
  server that — when a request has no `Authorization` header — acts with the
  credentials it was configured with, which the install guide fills with
  `admin`/`password`, on a route like `https://epinio-mcp.<ip>.sslip.io`. None
  of these is a vulnerability in the code; each is a finding waiting for the
  install that skipped the paragraph that says to change it.
- Pushing with GitJob requires storing the user's settings file, password
  included, as a Secret (docs).
- The absence of advisories should be read as low exposure (611 stars, mostly
  corporate installs behind a firewall), not as proof.

## Against Skifity

### Why it did not become the Coolify of Kubernetes, and the lesson

Epinio never tried to be. It was SUSE's answer to "Cloud Foundry, but on the
Rancher clusters you already bought", and every decision follows from that:

1. **It begins where a platform team already is.** A cluster, an ingress
   controller, cert-manager, a StorageClass, wildcard DNS, 8 GB. The person with
   a VPS and an afternoon is not the customer and was never going to find it.
2. **It shipped the developer loop and stopped.** Push, build, route, bind. No
   push-to-deploy, no previews, no rollback, no backups, no notifications, no
   templates — the things Heroku, and then Coolify, made table stakes. What
   Epinio adds instead (app charts, builder images, catalogs, roles) serves the
   operator.
3. **It hid Kubernetes from half its users.** Developers never see YAML;
   operators create users as Secrets and roles as ConfigMaps with `kubectl`,
   and configure SSO by editing a Secret. In a one-person install those are the
   same person.
4. **Corporate stewardship is a dependency too.** SUSE's priorities moved;
   2024 had 33 commits. A consultancy picked it up, which kept it alive and
   pointed it further toward enterprise "golden paths" and "AI readiness" —
   Krumware's own framing in a sponsored post — and further from self-hosters.

**What it teaches Skifity.** Epinio's two personas are Skifity's Apps and
Advanced, and the lesson is that the operator half must be hidden too:
Skifity already does users, roles, SSO and servers in the panel
(`internal/auth`, `internal/api`), which is the right side of that line. Its
release discipline since 2025 — a support matrix, an upgrading page per
version, blog notes a person can read — is the model Skifity needs before its
first tag, because Epinio's own upgrade notes show what a platform that owns
components faces every few months (kubed, MinIO, CRDs). And its MCP design —
API-only as the calling user, with anything that touches Kubernetes directly
behind an explicit opt-in — is the same instinct as Skifity's, spoiled only by
a credential fallback Skifity must never have.

| Capability | Epinio | Skifity (evidence) | Verdict |
|---|---|---|---|
| Install from a bare server | No. Needs a prepared cluster (ingress, cert-manager, metrics-server, StorageClass), wildcard DNS, 8 GB | One installer for k3s and the panel, 1 GB minimum (`installer/install.sh`, `docs/performance.md`), servers joined over SSH (`internal/provision`). **Written, never run** | Skifity ahead (on paper) |
| Deploy sources and builds | Folder, Git revision or image; Paketo or Kaniko; no webhooks; app watch | Folder (`internal/upload`, `skifity up`), Git with webhooks for GitHub/GitLab/Gitea (`internal/gitsrc`), image; Railpack/Nixpacks/Dockerfile (`internal/builder`); variables do not rebuild (ADR-0007). Written | Skifity ahead |
| Domains, TLS and routing | Wildcard domain, private CA by default, Let's Encrypt opt-in, Gateway API | Free sslip.io address, cert-manager and Let's Encrypt when a domain is added, DNS helper, app password and edge firewall (`internal/store/password.go`, `internal/edgerules`), Cloudflare tunnel. Ingress only, no Gateway API. Written | Skifity ahead |
| Databases and services | Catalog of Helm services; defaults are frozen Bitnami `-dev` charts; bindings as files | PostgreSQL via CloudNativePG, Redis, MySQL, connection string injected as a variable (`internal/dbsvc`). Written | Skifity ahead |
| Storage and backups | PVCs, stateful chart, OCI export; no backups | Volumes; S3 backups and restore for databases and volumes (`internal/backup`); export (`internal/api/export_handlers.go`). Written, never restored | Skifity ahead |
| Scaling and HA | Instance count only | HPA (`internal/kube/manifests.go`), KEDA scale-to-zero (`internal/kube/scaletozero.go`), scaling readiness (`internal/deploy/scaling.go`). Written | Skifity ahead |
| Observability | Logs, staging logs, metrics report download; no alerts | Logs over SSE, current usage, `/api/metrics`, watch loop and 7 notification events (`internal/watch`, `internal/notify`). Written | Skifity ahead |
| Security, auth, roles, SSO, audit | Dex OIDC with group→role mapping, namespace-scoped fine-grained roles; users/roles via kubectl; default passwords; no audit | Argon2id, TOTP, recovery keys, OIDC with PKCE, owner/admin/member team-wide, scoped tokens (`internal/auth`); route walk (`internal/api` tests); sealed secrets (`internal/crypto`); audit (`internal/store/misc.go`); one-time setup token instead of a default password. No group mapping (grep "groups" in `internal/auth/oidc.go`: none), no project scoping (`internal/api/api.go`). **Works** | Skifity ahead on safety, Epinio ahead on role granularity |
| Preview environments and branches | None | Per-PR/branch namespaces, fork PRs without secrets, commit status and PR comment (`internal/api/webhook_handlers.go`). Written | Skifity ahead |
| Templates and catalogue | App charts and 5 dev services; no app catalogue | 282 pinned templates (`internal/templates/catalogue`) | Skifity ahead |
| CLI, API, IaC, integrations | CLI, REST, manifests, CRDs, OCI export, MCP (HTTP, beta) with adopt tier, agent skill, Rancher extension | CLI/API/stdio MCP in one binary (`internal/cli`, `internal/api`, `internal/mcpserver`), `skifity.toml`, export, plugins. No remote MCP, no adoption of existing workloads | Parity |
| Notifications | None | Telegram, Discord, webhook, email; more via plugins (`internal/notify`). Written | Skifity ahead |
| Multi-server and networking | Any conformant cluster, RKE2/K3s/AKS/EKS/GKE | k3s only; add/remove/promote servers (`internal/provision`); existing clusters "Not yet" (`docs/faq.md`); default-deny NetworkPolicy (`internal/kube/namespace.go`). Written | Different bets: Epinio ahead on reach, Skifity on nodes |
| Team and collaboration | Namespaces, scoped roles, SSO groups; no invitations | Teams, invitations, roles (`internal/api/invitation_handlers.go`, `teams_handlers.go`) | Parity |
| Developer experience and onboarding | Short developer path, long operator path; versioned persona-split docs; support matrix | Docs in the binary (`internal/docsite`), five languages (`web/src/locales`), errors with cause/impact/fix (`internal/errdoc`); **no release, no support policy** | Skifity ahead on the product, Epinio ahead on release discipline |

## Gaps worth closing in Skifity

### P0

**1. Upgrade what the installer installed.** Epinio's upgrade page is a year of
component swaps (kubed → Reflector, MinIO → SeaweedFS, a CRD hook Job); Skifity
installs k3s, cert-manager, CloudNativePG, KEDA, Longhorn, BuildKit and a
registry and has no path to upgrade any of them once installed
(`EnsureComponent` in `internal/cluster/components.go`; k3s version applies to
new servers only, `internal/provision/scripts.go`). Full write-up, fit and size
in `kubero.md`, gap 1. L; plan and manifests testable here, the upgrade itself
needs a cluster.

### P1

**2. A published support and security policy with the first release.**
- *What.* A `SECURITY.md` pointing at GitHub's private vulnerability reporting
  and an embargo promise; a support table in `docs/releasing.md` (which
  releases get fixes, for how long); an "Upgrading" section per release listing
  anything that needs a person.
- *Evidence.* Epinio's Krumware-era support matrix (full support and
  end-of-life dates per minor) and per-version upgrading page are what made the
  revival credible; Kubero's `SECURITY.md` routes reports to public issues and
  lists a stale supported version; Canine has neither a policy nor a release.
  Skifity has no `SECURITY.md` (checked at the repository root and `.github/`).
- *Fit.* Repository files and `docs/releasing.md`; the release workflow can
  refuse a tag with no upgrading entry, the way it refuses a mismatched
  installer today.
- *Size.* S.
- *Without a cluster?* Yes.

**3. Map SSO groups to roles.**
- *What.* Read the ID token's `groups` claim (configurable name) and map groups
  to a team and role, applied at each sign-in; unmapped users follow today's
  invite-or-refuse setting.
- *Evidence.* Epinio's Dex `rolesMapping`; SSO buyers expect access to follow
  the directory rather than per-user clicks. Skifity's OIDC
  (`internal/auth/oidc.go`, `internal/api/sso_handlers.go`) has no group handling.
- *Fit.* `internal/auth/oidc.go` (claim parsing), a settings key for the map,
  `internal/api/sso_handlers.go` (apply on sign-in, audit the change), the SSO
  section of Settings.
- *Size.* S–M.
- *Without a cluster?* Yes — the existing OIDC tests use a fake provider.

### P2

**4. Connect to a managed database from the laptop.**
- *What.* `skifity db connect <database>` opens a local port tunnelled through
  the panel to the database Service, so `psql` or a GUI works without exposing
  the database.
- *Evidence.* Epinio's `service port-forward` and `app port-forward` (and
  matching `service_portforward` role action). Skifity's CLI has no command
  for a managed database — `admin backup-db` copies the panel's own SQLite file
  (`internal/cli/admin.go`) — while `internal/kube/client.go` already keeps the
  REST config "which exec and port-forward need".
- *Fit.* An authorized WebSocket endpoint in `internal/api`
  (`authorizeDatabase`), the tunnel in `internal/kube`, the command in
  `internal/cli`, one line on the database page showing the command.
- *Size.* M.
- *Without a cluster?* The CLI, the authorization and the framing, yes; the
  tunnel reaching a real database, no.

**5. Install onto an existing cluster.**
- *What.* Run the panel on a cluster Skifity did not build: a preflight that
  reads what is there (default StorageClass, IngressClass and whether it is
  Traefik, cert-manager, a NetworkPolicy-enforcing CNI, metrics-server) and
  says which features will not work, then the same manifests.
- *Evidence.* All three Kubernetes peers work only this way; Canine's author
  says its users are mostly organisations with a corporate cluster; Epinio
  lists RKE2, K3s, AKS, EKS and GKE. Skifity's FAQ: "Not yet ... on the list"
  (`docs/faq.md`).
- *Fit.* `internal/cluster` (a capability probe), `installer/` or a `skifity
  admin install` path, the Settings → Components page showing what is missing.
  The firewall and app password render Traefik middleware, so a non-Traefik
  cluster must turn them off with an explanation rather than silently.
- *Size.* L.
- *Without a cluster?* The probe against a fake clientset, yes; the claim that
  it works on EKS or AKS, only on those clusters. **Caveat:** this is the
  audience every peer already fights over, and the one that did not make any of
  them the Coolify of Kubernetes; it should not take a day from the one-server
  install.

## Things to deliberately not copy

- **Default passwords.** `admin`/`password`, `changeme`, and an MCP server that
  uses them when a request brings no credentials. Skifity's one-time setup
  token and "no request without a token" are the right defaults.
- **Administration through `kubectl`.** Users as Secrets and roles as
  ConfigMaps are elegant for GitOps and hostile to the person running a small
  install. Keep them in the panel; export them if anyone wants YAML.
- **Configuration only as files.** Epinio's bindings under `/configurations`
  have waited four years for environment variables (#1647). Twelve-factor apps
  read the environment.
- **A private CA as the default certificate.** A default install whose URLs
  browsers warn about teaches users to click through warnings. Skifity's plain
  HTTP on sslip.io with a real certificate on a real domain (ADR-0015) is more
  honest.
- **Frozen `-dev` databases as the default service.** A PostgreSQL 15.1 chart
  from a repository nobody updates is worse than no default; Skifity's
  operator-backed databases with backups are the product.
- **Push-to-deploy through another product's CRD.** Requiring Rancher Fleet and
  a stored password for a feature every PaaS has is the cost of building for
  an ecosystem instead of a user.
- **Operator-authored app charts as the main extension point.** Powerful for a
  platform team, and a second product for everyone else; Skifity's
  templates-as-files and plugins cover the need without exposing Helm.
- **Release notes that are a list of merge commits.** The GitHub release pages
  for 1.14.0 and 1.14.1 say "Development updates"; the real notes are on a
  blog. One place, readable, per release.

## Sources

All read 2026-09-30.

- https://github.com/epinio/epinio — repository metadata via the GitHub search API
- Git history of epinio/epinio, cloned (main at 9d60f60, 2026-09-23): tags and dates, commits per year and month, authors and e-mail domains since 2025-10-01; `go.mod`; `README.md`
- https://github.com/epinio/epinio/releases/tag/v1.14.0 and /v1.14.1 (release pages)
- https://github.com/epinio/epinio/issues (open issues sorted by reactions: #593, #940, #1269, #1397, #1647, #1681, #1820, #1896, #2105, #2141, #2205, #2244)
- https://github.com/epinio/epinio/discussions
- https://github.com/epinio/epinio/security/advisories (none published)
- https://github.com/epinio/docs, cloned (e77e305, 2026-09-28), including `versions.json` and the release blog posts `blog/2025-09-24-epinio-1-13-0.md` … `blog/2026-09-01-epinio-1-14-2.md`
- https://docs.epinio.io/ (introduction)
- https://docs.epinio.io/getting-started/system-requirements
- https://docs.epinio.io/getting-started/install-epinio
- https://docs.epinio.io/how-to/operator/cluster-prerequisites
- https://docs.epinio.io/reference/security/authorization
- https://docs.epinio.io/reference/security/authentication_oidc
- https://docs.epinio.io/reference/concepts/services
- https://docs.epinio.io/reference/concepts/catalog
- https://docs.epinio.io/reference/concepts/configurations
- https://docs.epinio.io/reference/concepts/build_modes
- https://docs.epinio.io/reference/customization/dockerfile-builds
- https://docs.epinio.io/reference/upgrading
- https://docs.epinio.io/reference/mcp
- https://docs.epinio.io/getting-started/install-mcp
- https://docs.epinio.io/reference/cli/agent-skill
- https://docs.epinio.io/how-to/operator/networking/gitjob_push
- https://docs.epinio.io/how-to/developer/concepts/app_watch
- https://docs.epinio.io/how-to/developer/concepts/export/export_to_oci_registries
- https://docs.epinio.io/blog/epinio-1-13-10, /epinio-1-14-0, /epinio-1-14-1, /epinio-1-14-2
- https://github.com/epinio/helm-charts, cloned (1fcdcc8, 2026-09-03): `chart/epinio/values.yaml`, `chart/epinio/templates/service-catalog/*.yaml`, chart templates list
- https://www.krum.io/products/epinio
- https://www.krum.io/products/epinio/support (support matrix)
- https://thenewstack.io/krumware-epinio-kubernetes-mcp/ ("Develop like you deploy", Katie Tincello, 2026-07-09 — marked "Krumware sponsored this post")
- https://www.suse.com/c/rancher_blog/meet-epinio-the-application-development-engine-for-kubernetes/ (SUSE's launch post; search-result summary)
- https://news.ycombinator.com/item?id=33094402 ("Epinio: Kubernetes PaaS from SuSE", 2022-10-05, 1 point; Algolia HN API)
- https://news.ycombinator.com/item?id=40632046 and https://news.ycombinator.com/item?id=40635431 (2024-06-10 comments on Epinio)
- NVD API keyword search "epinio" (0), OSV API query for `github.com/epinio/epinio` (0), https://app.opencve.io (0)
- https://github.com/bitnami/charts/issues/35164 (Bitnami catalogue change)
- Skifity, for the comparison: `internal/cluster/components.go`, `internal/provision/scripts.go`, `internal/auth/oidc.go`, `internal/api/sso_handlers.go`, `internal/api/api.go`, `internal/kube/client.go`, `internal/cli`, `internal/mcpserver/server.go`, `docs/faq.md`, `docs/performance.md`, `docs/releasing.md`, `docs/checklist.md`
