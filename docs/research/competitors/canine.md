# Canine

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Researched 2026-09-30 from the live repository and its history, the docs
repository, canine.sh, the issue tracker and Hacker News. Every number below
carries its source and the date it was read; where something could not be
confirmed it says so.

Canine (canine.sh, `CanineHQ/canine`, formerly `czhu12/canine`) is an
open-source "developer-friendly PaaS for your Kubernetes": a Heroku-style
interface — projects, services, add-ons, preview apps — over **a Kubernetes
cluster you provide**. It started for indie hackers on a $4 Hetzner box; by
2026 its author describes it as "mainly used in organizations with developers
who want to deploy to a corporate Kubernetes environment" (HN, December 2025).
Architecture: a **Rails 7.2** application (Hotwire, Tailwind, Avo admin) with a
**GoodJob** worker and **PostgreSQL 16** for its own state. It runs **outside
the cluster** — Docker Compose on a laptop or server, mounting the Docker socket
for builds ("local mode") — or, since 2026, **inside** one from a Helm chart
("cluster mode"); either way it reaches clusters through stored kubeconfigs and
can manage several. Apps are deployed from ERB-templated YAML or as Helm
releases; add-ons are Helm charts. Licence: **Apache-2.0 on GitHub, "MIT
licensed" on canine.sh/self-hosted** — a mismatch reported on HN in June and
December 2025 and still there on 2026-09-30. Pricing: **Canine Cloud** is free
for the first cluster, **$20 per additional cluster per month** (Pro adds
unlimited members, priority support, SSO/SAML); the code caps the free plan at
1 cluster and 5 members (`app/models/concerns/billable.rb`); self-hosting is
free. It is funded by sponsorship, the largest being **Portainer at "$5k+ / m"**
(author, HN, 2025-12-18), which lets him work on it "close to full time".
Maturity: very active — about **1,080 commits in the 12 months to September
2026, ~96% by Chris Zhu** — but **no tag or release has ever been cut** (the
Compose file runs `ghcr.io/caninehq/canine:latest`), and **docs.canine.sh
answered HTTP 503 "no available server" on 2026-09-30**, as it did when a user
reported it on HN on 2026-05-26. Adoption: **2,937 stars, 122 forks, 49 open
issues and PRs** (GitHub API, 2026-09-30); Show HN in June 2025 took **320
points and 123 comments**; the author reports "~1000 developers" (April 2026),
"2000 developers ... actively deploying" (May 2026, repeated September 2026)
and "2k apps on the cloud version" (January 2026), with Canine Cloud "still
able to run on a single 48GB Hetzner VPS" (April 2026). These are the author's
figures and could not be checked independently.

## Feature inventory

### Deploy sources and builds

- A **project** is one repository or one image; it has **services** of three
  types: `web_service`, `background_service`, `cron_job`
  (`app/models/service.rb`).
- Sources: GitHub, GitLab (including self-hosted), Bitbucket; images from Docker
  Hub, GHCR or GitLab's registry. Webhooks deploy on push.
- Build types: **Dockerfile** and **Cloud Native Buildpacks** (Paketo, Heroku
  builders) (`app/models/build_configuration.rb`). Build drivers: `docker`
  (the local Docker socket, local mode), `k8s` and `cloud` — a **Build Cloud**
  is a builder installed into the cluster's `canine-k8s-builder` namespace
  (defaults: 2 replicas, up to 2 CPU / 4 GiB each).
- Deployment methods `legacy` (templated YAML) and `helm`.
- Predeploy and postdeploy commands, predefined commands, one-off pods.
- "Redeploy" of an earlier build is the rollback
  (`app/controllers/projects/deployments_controller.rb`).

### Domains, TLS and routing

- Cluster packages installed on connect (`resources/helm/system_packages.yml`):
  **Traefik** (default) or ingress-nginx ("legacy"), **cert-manager** (the only
  pinned chart, v1.15.3), metrics-server, telepresence (private preview access),
  **cloudflared**, and since September 2026 **Fluent Bit**.
- Let's Encrypt certificates; **Cloudflare DNS** integration creates records
  automatically (architecture docs).
- An **internal auth proxy** (September 2026) puts Canine sign-in in front of a
  service or add-on endpoint.
- Open: an external domain cannot be removed once added (#535, February 2026);
  domains cannot be managed through the API (#696, August 2026).

### Databases and services

- **Add-ons are Helm charts.** Search Artifact Hub from the UI ("Over 10000 open
  source projects can be deployed via Canine"; "15k+ packages", author, 2025),
  fill in the chart's values schema, install. A curated list of nine
  (`resources/helm/charts.yml`): PostgreSQL, Redis, OpenClaw, Metabase,
  Elasticsearch, ClickHouse, Airbyte, Prometheus, Portainer.
- The default **PostgreSQL and Redis add-ons are Bitnami charts from
  `charts.bitnami.com` with no image override** (`resources/helm/charts.yml`) —
  after Broadcom moved versioned Bitnami images to the unmaintained
  `bitnamilegacy` repository in August–September 2025, that is either a pull of
  an image that is no longer updated or a pull that fails, depending on the
  chart version resolved.
- Add-on logs through the UI and MCP.

### Storage and backups

- Volumes per project (PVC, ReadWriteOnce or ReadWriteMany).
- **No backups** of add-ons or volumes were found in the code (every "backup"
  in `app/` is a two-factor backup code). Canine Cloud advertises "Managed
  backups" — of Canine's own data.
- **Log drain** (September 2026): Fluent Bit ships pod logs to an S3 bucket,
  configured per cluster.
- **Ejecting**: "Download All YAML Files" and the kubeconfig, commit them, run
  `kubectl apply` from CI (docs, "Ejecting from Canine").

### Scaling and high availability

- Manual replica count per service, CPU, memory and **GPU** limits, rolling
  updates with `maxUnavailable: 0` (`resources/k8/stateless/deployment.yaml`).
- **No HorizontalPodAutoscaler** anywhere in the code; the landing page's "Easy
  Autoscaling" means resizing servers or a managed cluster's node autoscaler.
- Multi-cluster per account; preview apps can target a separate cluster.
- HA of Canine itself: a Rails app, a worker and PostgreSQL — Canine's problem
  in local mode, the Helm chart's in cluster mode.

### Observability (logs, metrics, alerts, uptime)

- Build and pod logs, a "processes" view (pods), CPU/memory/storage samples per
  cluster and namespace kept in PostgreSQL (`app/models/metric.rb`, a metrics
  job) and drawn as charts.
- A service-health notifier; no HTTP uptime checks were found.
- A per-project deployment event feed (`app/models/event.rb`: create/update
  events linked to commits). No account-wide audit log was found, although the
  MCP page promises a "full audit trail".

### Security, auth, roles, SSO, audit

- Devise accounts with TOTP two-factor; sign-in with GitHub or GitLab.
- **SSO: SAML, OIDC and LDAP** (`ruby-saml`, `omniauth_openid_connect`,
  `net-ldap`), Pro on Canine Cloud.
- Account roles owner/admin/member (`app/models/account_user.rb`); **teams**
  that restrict which clusters and projects a user sees — "Once you create your
  first team, access control becomes active" (Teams docs).
- API tokens; Doorkeeper as an OAuth 2.0 provider for the MCP server.
- Variables are "config" (a ConfigMap) or "secret" (a Kubernetes Secret) in the
  cluster, but **stored in plain PostgreSQL columns** — as are kubeconfigs and
  Git tokens: no `encrypts` declaration exists in `app/models`, the schema has
  `environment_variables.value text` and `clusters.kubeconfig jsonb`, and the
  request to encrypt them is open (#557, March 2026, unanswered).

### Preview environments and branches

- **Preview apps**: per pull/merge request, a child project ("project fork") on
  a cluster you choose — the docs recommend a separate one — configured by a
  `canine.yml` in the repository: services, variables, volumes, notifiers and
  `predeploy`/`postdeploy`/`predestroy`/`postdestroy` scripts, with
  `<%= number %>`-style substitution (`app/services/canine_config/definition.rb`).
- A per-PR database is **the user's script**: the docs' example templates
  `DATABASE_URL` with the PR number and calls "/script-to-create-database" and
  "/script-to-drop-database".
- Cleanup: a job polls each fork's PR and deletes the child project when it is
  closed, merged or gone (`app/jobs/cleanup_closed_pr_projects_job.rb`).
- No commit status or PR comment was found.

### Templates and catalogue

- No app templates as such; the catalogue *is* Artifact Hub, plus the nine
  curated charts. Charts install at whatever version is selected or latest.

### CLI, API, IaC, integrations

- **CLI** in Rust (`CanineHQ/cli`, Homebrew): `auth login --token`, `projects
  list/deploy/run/logs/processes`, `clusters list/download-kubeconfig`, add-ons,
  `canine local start`. `projects run` **requires `kubectl`** on the laptop.
- **REST API** documented with Swagger at canine.sh/api-docs; an open request
  says it lacks parity with the UI (#636, June 2026).
- **MCP server** (announced in a Show HN on 2026-04-02) over **HTTP at `/mcp`
  with OAuth 2.0** (RFC 8414 metadata, RFC 7591 dynamic client registration,
  RFC 9728), so
  `claude mcp add --transport http canine https://canine.sh/mcp` is the whole
  setup. Tools include create cluster/project/service/add-on, deploy, restart,
  logs, set variables — and **`get_environment_variable_value`** and
  **`get_cluster_kubeconfig`** (`app/mcp/tools/`). The author calls the
  kubeconfig tool "the real killer feature ... which lets Claude then breakout
  and get the full Kubernetes API" (Show HN, April 2026).
- **Stack managers**: Portainer or Rancher can be linked to an account for
  cluster management and RBAC (`app/models/stack_manager.rb`).
- `canine.yml` is the only IaC.

### Notifications

- Slack, Discord, Microsoft Teams, Google Chat and email
  (`app/models/notifier.rb`), for builds, deployments and service health.

### Multi-server and networking

- **Canine does not install Kubernetes.** Three cluster types: `k8s` (paste a
  kubeconfig), `k3s` and `local_k3s` (`app/models/cluster.rb`). For a VPS the UI
  prints `curl -sfL https://get.k3s.io | sh -s - --disable traefik --tls-san
  <ip>`, tells you to SSH in and run it, to `sudo ufw allow 6443` so Canine can
  reach the API server **over the internet**, then to paste the output of
  `sudo cat /etc/rancher/k3s/k3s.yaml` — the cluster-admin credential — into a
  form (`app/views/clusters/cluster_types/instructions/_k3s.html.erb`, docs
  "VPS Hosted K3s", which recommends "At least 4GB RAM").
- Adding a second node to that VPS cluster is not supported; the author
  pointed to managed Kubernetes for multi-node (Show HN, 2025).
- Private access to previews through telepresence; Cloudflare tunnels.

### Team and collaboration

- Accounts, invitations, roles, teams scoped to clusters and projects, SSO
  groups via the providers above; Portainer RBAC mapping.

### Developer experience and onboarding

- **Canine Cloud** is the on-ramp: sign up, connect a cluster, no credit card.
- **Dev environments** (2026): a pod on your cluster built from your
  Dockerfile, the repository cloned in, a browser terminal (xterm.js, five
  sessions), a preview URL, file sync from the CLI — pitched for running Claude
  Code, Cursor or Codex against your own compute.
- Docs include a well-reviewed "Kubernetes crash course" — Canine explains
  Kubernetes rather than hiding it.
- The docs site has been unreachable for months (above).

## What users love

- **Heroku's workflow at Hetzner's price.** The launch post's table — Heroku
  $260, Fly.io $65, Render $85, Hetzner $4 for 4 GB — is the pitch, and the
  thread's warmest replies are about cost ("You deserve an award for building
  this"). Show HN, June 2025.
- **Any Helm chart is an add-on.** "Canine basically makes it trivial to host
  any helm chart" (author); a commenter: "building on top of helm charts makes
  me want to try it out" (Show HN).
- **The Kubernetes docs.** "Your docs on how K8s works look really good, and
  might be the most approachable docs I've seen on the subject" (Show HN).
- **It keeps shipping.** Preview apps, GitLab and Bitbucket (asked for at
  launch, #167, #173) arrived within weeks; MCP, dev environments, SSO, log
  drains followed. "Energy on this project" was the rebuttal when someone
  compared it to Kubero (Show HN).
- A Canine Cloud user in January 2026: it "simplified running a bunch of
  different services" on their infrastructure (#497).

## What users complain about

- **"It's still Kubernetes."** "If I need to know what Kubernetes is, Helm charts
  and whatnot, it's not really a Heroku alternative for me" (Show HN, 2025);
  "Kubernetes is really powerful, but IMHO, it is the wrong tool here ... the
  benefits of Heroku were precisely that it didn't need you to think about the
  guts" (HN, July 2026); "I wonder how much of K8s does Canine really abstract?
  Do I ever need to peek underneath the hood?" (Show HN).
- **The single-box path is unclear.** "Do I still have to create a cluster by
  putting in some managed DO K8s reference? ... I just want it to use the local
  VM" and "a big part of your audience will want to one-box it" (Show HN); "I
  think you do need to support being able to add more nodes to the Hetzner
  install" (Show HN).
- **Documentation and trust signals.** Docs 503 (HN, May 2026, and still on
  2026-09-30); the API reference link 404 (HN, December 2025); MIT vs Apache
  (HN, June and December 2025); "The README is confusing me" (Show HN).
- **Secrets at rest.** "Encrypted Environment Variables" requested (#557, open).
- **Gaps under the features.** No headless domain management (#696), cannot
  remove a domain (#535), API not at parity (#636), local setup failing
  silently (#463), a Cloud outage with no status page (#497). None of these had
  a maintainer reply on 2026-09-30 — the cost of one person shipping new
  features.

## Security record

- **No CVEs** (NVD keyword searches "canine.sh" and "CanineHQ": 0; OpenCVE: 0)
  and **no GitHub security advisories**; the repository has **no SECURITY.md**
  (GitHub security tab, 2026-09-30).
- **Cross-tenant cluster access, fixed silently on 2026-09-18** (commit
  33ee7ef, "Fix webhook signature verification and scope cluster access").
  `Clusters::BaseController#set_cluster` looked up `Cluster.find(params[:cluster_id])`
  without scoping to the signed-in account — the same unscoped line is in the
  file at 2024-09-30 and 2025-08-31 (git history). Controllers inheriting it at
  HEAD cover cluster metrics, build clouds and **installing or uninstalling
  cluster packages with arbitrary chart values**
  (`app/controllers/clusters/cluster_packages_controller.rb`). On Canine Cloud,
  a multi-tenant service, any signed-in user could address another account's
  cluster by numeric id. No advisory or changelog entry — there are no
  releases to carry one.
- **Webhook verification**, same commit: GitLab webhook tokens were not verified
  at all, and Bitbucket webhooks were accepted when no secret was configured.
- **Auth proxy authorization**, 2026-09-19 (commit 4b9a219): the proxy added on
  2026-09-11 did not check account membership before granting access. Live for
  eight days on main.
- **Plaintext secrets at rest**: variables, kubeconfigs and provider tokens in
  ordinary columns (verified in `db/schema.rb` and `app/models`; #557).
- **Credential hand-out by design**: the MCP tool `get_cluster_kubeconfig`
  returns the stored kubeconfig — for a `k3s` cluster, the admin file the user
  pasted — to the AI agent, guarded only by a description sentence ("only call
  this when explicitly requested by the user"). The CLI's `download-kubeconfig`
  does the same for a person.
- **API server on the internet**: the k3s instructions open port 6443 to the
  world so a Canine outside the cluster can reach it.
- A dependency report of a stored XSS in the Trix editor (#480, January 2026) is
  open without a reply.

## Against Skifity

### Why it has not become the Coolify of Kubernetes, and the lesson

Canine is the closest of the three — its author uses the phrase himself:
"Think about it like coolify is to a VPS as Canine is to Kubernetes" (HN, May
2026). It is growing, it is funded, it ships every day. It still is not it:

1. **The on-ramp stops at "paste your kubeconfig".** Coolify's install is one
   command on the server. Canine's is: install Docker, run Canine somewhere,
   SSH into the server, run k3s's installer by hand, open 6443 to the internet,
   copy an admin credential into a web form. Every step is where the Coolify
   audience leaves, and the one-box story never got better than that.
   **Lesson: Skifity's single command that installs k3s and the panel, and its
   SSH join, are exactly the missing piece — and neither has run on a real
   server yet (ADR-0010).**
2. **It chose the other audience.** "We were too big for the deploy to a VPS
   type options like coolify" (HN, March 2026); "mainly used in organizations
   with developers who want to deploy to a corporate Kubernetes environment"
   (HN, December 2025). Canine succeeds where Kubernetes already exists and a
   platform team wants a UI on it — which is the Epinio/Portainer market, not
   Coolify's.
3. **Kubernetes is the vocabulary.** Clusters, kubeconfigs, processes that are
   pods, Helm charts as add-ons, `kubectl` for `run`, a Kubernetes crash course
   as documentation, and an MCP whose best feature is escaping to the
   Kubernetes API. Honest, powerful, and the opposite of "you never think about
   a server".
4. **Trust signals a VPS user reads first.** No release, no changelog, docs down
   for months, two licences, no SECURITY.md, secrets in plain columns, a
   two-year-old cross-tenant bug fixed without a word. Coolify has the social
   proof to absorb bad news — tens of thousands of stars and the largest
   catalogue (`docs/research/competitors.md`); Canine, at under 3,000 stars,
   does not have that cushion yet.

**What it teaches Skifity**, beyond point 1: MCP is no longer unique — Canine
has had one since spring 2026, remote and OAuth-authenticated, which is the
easier setup; Skifity's is stdio only (`internal/mcpserver/server.go` runs
`mcp.StdioTransport`). What *is* still Skifity's is what the tools refuse to
do: no secret value is ever returned (`list_variables`), no kubeconfig is ever
handed out, and every error carries a fix. That is a better line to lead with
than "has MCP". And Canine Cloud is a live demonstration of the hosted control
plane `docs/research/competitors.md` warns about: one missing `where` and every
customer's cluster is reachable.

| Capability | Canine | Skifity (evidence) | Verdict |
|---|---|---|---|
| Install from a bare server | No. User installs k3s by hand, opens 6443, pastes the admin kubeconfig; 4 GB recommended | One installer for k3s and the panel (`installer/install.sh`), 1 GB minimum; servers added by IP and password over SSH (`internal/provision`). **Written, never run** | Skifity ahead (on paper) |
| Deploy sources and builds | GitHub/GitLab/Bitbucket, registries; Dockerfile or CNB buildpacks; local Docker, in-cluster Build Cloud | GitHub/GitLab/Gitea (`internal/gitsrc`), image, folder upload (`internal/upload`, `skifity up`); Railpack/Nixpacks/Dockerfile via BuildKit Job (`internal/builder`); no-rebuild variables (ADR-0007). No Bitbucket. Written | Parity; Skifity ahead on folder deploy and fingerprint, behind on Bitbucket |
| Domains, TLS and routing | Traefik/nginx, cert-manager, **Cloudflare DNS automation**, auth proxy, telepresence, cloudflared | cert-manager on first use, sslip.io address, DNS record helper with copy buttons (manual), app password (`internal/store/password.go`), edge firewall (`internal/edgerules`, `internal/guard`), Cloudflare tunnel. No DNS provider API (grep: none). Written | Parity; Canine ahead on automatic DNS, Skifity on the firewall |
| Databases and services | Any Helm chart; default PostgreSQL/Redis on unpinned Bitnami charts | PostgreSQL via CloudNativePG, Redis, MySQL, credentials sealed and linked (`internal/dbsvc`). Written | Canine wider, Skifity sounder |
| Storage and backups | Volumes; **no backups**; log drain to S3; eject | Volumes; S3 backups and restore for databases and volumes (`internal/backup`); export (`internal/api/export_handlers.go`). Written; no backup ever restored | Skifity ahead on backups; Canine ahead on log drain |
| Scaling and HA | Manual replicas, GPU limits; no HPA | HPA (`internal/kube/manifests.go`), KEDA scale-to-zero (`internal/kube/scaletozero.go`), scaling readiness check (`internal/deploy/scaling.go`), node failover. Written | Skifity ahead |
| Observability | Logs, pod view, CPU/memory/storage history charts, health notifier, log drain | Logs over SSE (`internal/api/stream_handlers.go`), current usage only (metrics.k8s.io in `internal/kube/client.go`), `/api/metrics`, 7 events (`internal/notify/notify.go`) | Canine ahead on history and log drain |
| Security, auth, roles, SSO, audit | TOTP, SAML/OIDC/LDAP, teams; **secrets in plain columns**; cross-tenant bug for ~2 years; MCP returns kubeconfigs | Argon2id, TOTP, recovery keys, OIDC with PKCE (`internal/auth`); envelope encryption bound to context (`internal/crypto`); route walk: 126 routes refuse anonymous, 91 refuse another team (`internal/api` tests); audit (`internal/store/misc.go`). No SAML or LDAP (SAML declined in `docs/research/competitors.md`). **Works** | Skifity ahead; Canine ahead on SAML/LDAP |
| Preview environments and branches | Forked projects, optional separate cluster, `canine.yml` with lifecycle scripts, polling cleanup | Namespace per PR/branch, fork PRs get no secrets, TTL, commit status + edited PR comment (`internal/api/webhook_handlers.go`, `internal/gitsrc/report.go`). No per-preview database; previews copy the source's linked database URL. Written | Skifity ahead on safety and feedback; both lack per-PR databases |
| Templates and catalogue | Artifact Hub search + 9 curated charts, unpinned | 282 templates, every image version checked (`internal/templates/catalogue`) | Different bets; Skifity ahead on reproducibility |
| CLI, API, IaC, integrations | Rust CLI (`run` needs kubectl), REST/Swagger, **remote MCP with OAuth**, Portainer/Rancher | CLI, API and stdio MCP in one binary (`internal/cli`, `internal/api`, `internal/mcpserver`); `skifity.toml`; `--json` everywhere; plugins (`internal/plugins`) | Parity; Canine ahead on remote MCP |
| Notifications | Slack, Discord, Teams, Google Chat, email | Telegram, Discord, webhook, email; others as plugin providers (`internal/notify/provider.go`, ADR-0021) | Parity by design |
| Multi-server and networking | Many clusters per account; no node management | One cluster, servers joined/promoted/removed over SSH, default-deny NetworkPolicy per environment (`internal/kube/namespace.go`). Multi-cluster declined (`docs/roadmap.md`). Written | Skifity ahead on nodes, Canine on clusters |
| Team and collaboration | Roles + teams scoped to clusters/projects; SSO groups | Teams, invitations, owner/admin/member, **team-wide only** (`internal/api/api.go` `authorize*`) | Canine ahead |
| Developer experience and onboarding | Free hosted tier, dev environments, K8s crash course; docs down; no releases | Docs served by the binary (`internal/docsite`), five languages with a build gate (`web/src/locales`), errors with cause/impact/fix (`internal/errdoc`); **no release, no hosted trial** | Canine ahead on the first ten minutes today |

## Gaps worth closing in Skifity

### P0

Nothing Canine does is more urgent than Skifity's own P0 — running
`test/cluster/verify.sh` once and cutting a tag (`docs/checklist.md`). Canine's
growth came from being tryable in minutes; Skifity is not tryable at all yet.

One P0 that Canine's preview design sharpens: Canine makes the user template
a per-PR `DATABASE_URL` and write create/drop scripts, which is clumsy but at
least never points a preview at production by default. Skifity's previews copy
the source app's linked database URL (`copyPreviewVariables` in
`internal/api/webhook_handlers.go`). See `kubero.md`, gap 2.

### P1

**1. A remote MCP endpoint.**
- *What.* Serve the same tools over MCP's Streamable HTTP transport at `/mcp`
  on the panel, authenticated first by the existing scoped API tokens
  (`Authorization: Bearer`), later by OAuth 2.1 with dynamic client
  registration so claude.ai and Cursor connectors work with no binary.
- *Evidence.* Canine's MCP is remote with OAuth ("claude mcp add --transport
  http canine https://canine.sh/mcp"), and its author calls MCP the feature
  that surprised him most (Show HN, April 2026); Epinio's is Streamable HTTP
  too. Skifity's needs the binary on the same machine as the assistant
  (`internal/mcpserver/server.go`, `mcp.StdioTransport`).
- *Fit.* `internal/mcpserver` gains an HTTP handler mounted in `internal/api`
  behind the same token middleware and `internal/auth/scopes.go`; the MCP
  tools keep calling the API, so authorization stays in one place. A line in
  `llms.txt` and `docs/cli.md`.
- *Size.* M with bearer tokens; L with OAuth and dynamic registration.
- *Without a cluster?* Yes — an HTTP MCP client test in Go and a step in
  `make smoke`.

**2. Project- and environment-scoped roles.**
- *What.* Let a member be limited to some projects, and let Production require
  admin to deploy.
- *Evidence.* Canine's teams restrict users to clusters and projects; Epinio's
  roles are namespace-scoped; Kubero added groups. Skifity's `authorizeApp`,
  `authorizeEnvironment` and `authorizeProject` resolve the team and compare one
  team-wide role (`internal/api/api.go`).
- *Fit.* `internal/store` (per-project grants), the `authorize*` helpers, the
  project settings page.
- *Size.* M.
- *Without a cluster?* Yes — extend the route walk.

### P2

**3. Create the DNS record for the user.**
- *What.* With a Cloudflare API token (scoped to one zone), adding a domain
  creates the A/CNAME record the Domains tab currently asks the user to type.
- *Evidence.* Canine automates Cloudflare DNS; Skifity spent Phases 57 and 58
  (`docs/progress.md`) making the manual instruction legible because it is where
  people get stuck.
- *Fit.* A small client in a new `internal/dnsprovider` package, dialled through
  `internal/netguard`; a token setting sealed like other secrets; a switch on
  the Domains tab; errdoc entries for a token without zone permission.
- *Size.* M.
- *Without a cluster?* Yes — a fake API server in tests.

**4. Usage history.** Canine keeps CPU, memory and storage samples per
namespace and charts them; Skifity shows only the current reading. See the
same gap in `kubero.md` for the fit. M; yes without a cluster.

**5. Log forwarding.**
- *What.* Ship app logs to an S3 bucket or an HTTP endpoint (Loki, a SIEM), as a
  component installed on first use.
- *Evidence.* Canine added a Fluent Bit drain to S3 in September 2026; Skifity's
  logs live only as long as the pod (grep for drain/loki/fluent: only node
  drains).
- *Fit.* `internal/cluster` renders a Fluent Bit or Vector DaemonSet with its
  cost stated before install (the pattern in `docs/performance.md`); settings
  for the destination, credentials sealed.
- *Size.* M.
- *Without a cluster?* The rendered objects, yes; delivery, no.

**6. Point Skifity at an existing cluster.** Canine's and Epinio's whole
audience brings its own cluster; Skifity's FAQ says "Not yet ... on the list"
(`docs/faq.md`). See `epinio.md` for the fit and the caveat. L; mostly needs
clusters.

## Things to deliberately not copy

- **Handing credentials to the agent.** `get_cluster_kubeconfig` and
  `get_environment_variable_value` make an MCP client — and every prompt
  injection that reaches it — a cluster admin. Skifity's rule that no surface
  returns a secret, including the MCP server, is the stronger product.
- **Asking the user to paste the cluster-admin kubeconfig and open 6443.** The
  panel living inside the cluster with a ServiceAccount is why Skifity never
  needs either.
- **Secrets in plain columns.** The encryption Canine's users are asking for
  (#557) is what `internal/crypto` already does.
- **"Any Helm chart" as the database story.** It makes the catalogue enormous
  and the defaults fragile — Canine's PostgreSQL and Redis still point at
  Bitnami's changed repository — and upgrades are, in its author's words,
  "still an unsolved problem" (Show HN). Skifity's operator-backed databases
  with backups and its pinned templates are the better default; charts, if
  ever, should be pinned and treated as templates.
- **Shipping without releases.** No tags means no changelog, no advisory, no
  way for a user to know a security fix landed. Canine fixed a cross-tenant bug
  and nobody was told.
- **A multi-tenant hosted control plane before the single-tenant one is proven.**
  Canine Cloud's two-year cluster-scoping bug is the risk
  `docs/research/competitors.md` describes, realised.
- **Dev environments and an in-browser terminal.** Real demand, but a second
  product; Skifity's roadmap already declines an interactive shell for good
  reasons (`docs/roadmap.md`).
- **Teaching Kubernetes as onboarding.** Canine's crash course is good
  writing, and it concedes the abstraction. Skifity's `docs/concepts.md` maps
  its words to Kubernetes for the curious, which is the right amount.

## Sources

All read 2026-09-30.

- https://github.com/CanineHQ/canine — repository metadata via the GitHub search API
- https://github.com/CanineHQ/canine/tags and https://github.com/CanineHQ/canine/releases (none)
- https://github.com/CanineHQ/canine/commits/main
- Git history of CanineHQ/canine, cloned (main at f7c7cc3, 2026-09-20): commits per month and author since 2025-10-01; commits 33ee7ef (2026-09-18) and 4b9a219 (2026-09-19); `app/controllers/clusters/base_controller.rb` at 2024-09-30 and 2025-08-31
- Source read at f7c7cc3: `README.md`, `docker-compose.yml`, `TODO.md`, `CLAUDE.md`, `Gemfile`, `db/schema.rb`, `app/models/*` (cluster, service, build_configuration, deployment_configuration, account_user, notifier, metric, event, project_fork, stack_manager, concerns/billable), `app/mcp/tools/*`, `app/jobs/cleanup_closed_pr_projects_job.rb`, `app/services/canine_config/definition.rb`, `app/controllers/clusters/*`, `app/views/clusters/cluster_types/instructions/_k3s.html.erb`, `resources/helm/charts.yml`, `resources/helm/system_packages.yml`, `resources/k8/stateless/deployment.yaml`, `install/install.sh`
- https://github.com/CanineHQ/canine-docs (b5543d0, 2026-07-13): installation/01-remote-k3s-vps.md, self-hosted/*, technical-details/01-canine-architecture.md, basics/01-clusters/02-build-clouds.md, basics/02-projects/04-preview-apps.md, ejecting/01-ejecting-from-canine.mdx, teams-and-permissions/01-teams.md, cli/*, mcp/index.md, resources/02-portainer-integration/index.md
- https://docs.canine.sh (HTTP 503, "no available server")
- https://canine.sh/ (pricing, "Why you should NOT use Canine")
- https://canine.sh/self-hosted ("MIT licensed")
- https://canine.sh/model-context-protocol
- https://canine.sh/dev-environments
- https://github.com/CanineHQ/canine/security (no SECURITY.md, no advisories)
- Issues: https://github.com/CanineHQ/canine/issues/167, /173, /188, /205, /463, /480, /497, /535, /557, /636, /694, /695, /696; issue lists sorted by comments and by date
- https://news.ycombinator.com/item?id=44292103 (Show HN, 2025-06-16, 320 points, 123 comments; thread read through the Algolia HN API)
- https://news.ycombinator.com/item?id=47614678 (Show HN: MCP server, 2026-04-02: "~1000 developers", 48 GB Hetzner VPS, kubeconfig tool)
- https://news.ycombinator.com/item?id=46308935 (2025-12-18: corporate Kubernetes, Portainer "$5k+ / m")
- https://news.ycombinator.com/item?id=46582211 (2026-01-12: "2k apps on the cloud version")
- https://news.ycombinator.com/item?id=48088294 (2026-05-10: "passed 2000 developers")
- https://news.ycombinator.com/item?id=49690857 (2026-09-14: "about 2000 active developers", dev containers)
- https://news.ycombinator.com/item?id=48271284 (2026-05-25: "coolify is to a VPS as Canine is to Kubernetes")
- https://news.ycombinator.com/item?id=47304015 (2026-03-09: "too big for ... coolify")
- https://news.ycombinator.com/item?id=48898495 (2026-07-13, and the reply by ryanisnan)
- https://news.ycombinator.com/item?id=48273569 (2026-05-26: docs 503)
- https://news.ycombinator.com/item?id=46343185 (2025-12-21: MIT vs Apache)
- https://news.ycombinator.com/item?id=46313514 (2025-12-18: API reference 404)
- NVD API keyword searches "canine.sh" and "CanineHQ" (0 results); https://app.opencve.io (no Canine entries)
- https://github.com/bitnami/charts/issues/35164 (Bitnami catalogue change)
- https://medium.com/@asierr/canine-sh-the-open-source-heroku-alternative-for-kubernetes-de73993d18a2 and https://blog.brightcoding.dev/2026/06/13/canine-the-revolutionary-paas-every-kubernetes-developer-needs (search-result summaries only; not relied on for figures)
- Skifity, for the comparison: `internal/mcpserver/server.go`, `internal/api/api.go`, `internal/api/webhook_handlers.go`, `internal/dbsvc/manager.go`, `internal/kube/*`, `internal/notify/*`, `internal/crypto`, `internal/store/password.go`, `docs/faq.md`, `docs/roadmap.md`, `docs/progress.md`, `docs/research/competitors.md`, `docs/checklist.md`
