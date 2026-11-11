# Rainbond

> **Status.** Written on 2026-11-08 against Rainbond `main` (newest release
> v6.10.3-release, published 2026-11-06), its documentation repository at `1881a72`
> (2026-11-04), and Skifity at `29147f1` the same day. No gap named here has been
> closed yet; the gaps below are proposals.

Rainbond (`goodrain/rainbond`, rainbond.com in Chinese and rainbond.io in English,
plus a hosted Rainbond Cloud at run.rainbond.com) is a Kubernetes application
platform from Beijing Goodrain Technology Co., Ltd. Its README headline is "An
open-source container platform. No Kubernetes expertise required", the same
premise as Skifity's, and it has made that claim since 2017. It is for developers,
delivery teams and enterprise IT in China first: its own README lists "private
deployment, internal-network deployment, offline delivery, Xinchuang adaptation
[China's domestic CPU and OS programme], x86 to ARM migration validation" as what
it is "especially suitable for". The unit it manages is an **application**, a
group of **components** (services built from source, images, packages, Compose,
Helm or templates) drawn as a topology, with dependencies that inject connection
variables. The other half of the product is delivery: an application can be
published as a versioned template to an internal library or a public marketplace,
exported as an offline package with its images, installed elsewhere in one step,
and upgraded later. In 2026 the pitch added AI: an assistant in the console
(RainAgent, v6.8.0), agent skills for coding tools (RainSkills, May 2026), and
private model serving (v6.9.0).

**Architecture.** A console (`rainbond-console` and `rainbond-ui`; the v6.9.3 fixes
for `NameError` and a missing Python module show the console backend is Python,
though GitHub labels the repository JavaScript) talks to one or more **regions**,
each a Kubernetes cluster running Go services in `rbd-system`: `rbd-api` (the
region API), `rbd-chaos` (builds), `rbd-worker` (turns the application model into
Deployments, StatefulSets, Services, Ingresses, ConfigMaps and Secrets),
`rbd-gateway` (Apache APISIX 3.14.1 with its ingress controller 1.8.4 since
v6.9.8; an OpenResty gateway before that), `rbd-monitor` (Prometheus),
`rbd-eventlog`, `rbd-hub` (the built-in registry) and `rbd-db` (MySQL), installed
by `rainbond-operator`. A SQLite mode exists for small installs (fixes in v6.4.1
and v6.10.0). There are three ways in: a **quick install**, which runs the whole
platform as one `docker run --privileged` container from a `rainbond:<version>-k3s`
image with k3s inside (Linux, and macOS through OrbStack); a **host install**, a
web wizard that installs RKE2 on servers you register with a command; and a
**Helm install** onto an existing cluster (ACK, CCE, TKE and k3s have pages). One
console can manage several clusters.

**Licence.** The "Rainbond Open Source License": Apache 2.0 plus two conditions —
no multi-tenant SaaS on the software "without obtaining a separate commercial
license from Goodrain", and no removing or obscuring its logos — and contributors
agree Goodrain may use their contributions commercially. GitHub reports it as
`NOASSERTION`. `rainbond-ui` alone is plain Apache-2.0. The README says "Its core
capabilities are 100% open source"; the documentation marks nine of its eleven
platform plugins "enterprise" (see Security and Observability below).

**Pricing.** The community edition is free. The enterprise edition is "contact us"
with no published price (rainbond.com/enterprise_server, read 2026-11-08). Rainbond
Cloud gives "new users a free quota"; its prices were not found, and its terms say
fees are "as published". **Maturity:** 30 releases from v6.4.1 (2025-12-12) to
v6.10.3 (2026-11-06), with minors v6.5.0 (2025-12-28), v6.6.0 (2026-02-28), v6.7.0
(2026-04-08), v6.8.0 (2026-05-25), v6.9.0 (2026-06-07) and v6.10.0 (2026-10-27).
**Adoption:** 6,295 stars, 882 forks and 61 open issues and pull requests; created
2017-11-05, last pushed 2026-11-08 (GitHub search API, 2026-11-08). RainSkills: 12
stars, created 2026-05-13; the `rainskills` npm package was first published
2026-07-29, has 120 versions (latest 0.1.50) and 3,935 downloads in the 30 days to
2026-11-07. The homepage claims "100+ open-source apps" in the marketplace (not
counted here). Hacker News has two Rainbond stories in eight years, the larger with
3 points; its own forum (t.goodrain.com) had its newest thread on 2026-01-30.

## Feature inventory

### What shipped, release by release

Dates are when each release was published (GitHub releases, read through the
ungh.cc mirror, 2026-11-08). Notes from v6.7.3 onwards are mostly in Chinese and
are translated here.

| Release | Date | What changed that matters for a comparison |
|---|---|---|
| v6.5.0 | 2025-12-28 | Redesigned UI; the last log of a container that crashed or was killed for memory is collected and kept ("Previous Log"); quick-install image from 2 GB to about 300 MB with stargz lazy loading, "1 minute 30 seconds" to start; image metadata read to prefill variables and volumes |
| v6.5.2 | 2026-01-19 | BuildKit image cache; team permission fixes (#2488); multi-architecture image metadata (#2487) |
| v6.6.0 | 2026-02-28 | Node.js builds on Cloud Native Buildpacks with a framework picker (Vite, Vue CLI, React, Next.js, Nuxt, Docusaurus) and pnpm; top navigation; a separate App Store page with requirements per app; **Compose projects uploaded as an archive** with their referenced files |
| **v6.7.0** | 2026-04-08 | **Native Kubernetes resource management** (attach an existing namespace, raw YAML, Helm releases, StorageClass/PV/PVC pages); **CNB with Paketo the default** for new source components; **application snapshots** (a version timeline of the whole application, diff, export, publish, roll back); Helm charts from OCI |
| v6.7.2 | 2026-04-29 | Storage quota of a component volume can be changed; architecture filter in the marketplace fixed |
| **v6.8.0** | 2026-05-25 | **RainAgent** (an assistant in the console, any OpenAI-compatible model) and **RainSkills** |
| **v6.9.0** | 2026-06-07 | **Model serving**: a model repository (built in, ModelScope, URL, upload), vLLM instances on GPU or CPU, OpenAI-compatible API keys, service and GPU monitoring; virtual machines from qcow2, hot CPU and memory resize, VM applications published as templates |
| v6.9.1 | 2026-06-15 | The assistant can run one-off commands in a container, read the last crash log and config files, and profile a repository before deploying; Dockerfile-first builds; an upgrade whose ConfigMap failed no longer reports success |
| v6.9.2 | 2026-06-24 | Gateway **JWT and OIDC plugins**; Harbor push triggers builds; a global registry setting; a **toolbox debug container** in the web terminal; DaemonSet components; uploads proxied through port 7070 instead of 6060 |
| v6.9.3 | 2026-07-02 | Fixes for issues #2641–#2646 (region panics, raw errors from OpenAPI handlers, console initialisation) |
| v6.9.6 | 2026-07-31 | Create a component without building and starting it; node disk usage alert; platform components protected from eviction |
| **v6.9.8** | 2026-08-17 | API performance at scale; build and operation logs over SSE, shipped with the note that it "still has SSE proxy authentication, upstream status passthrough and duplicate-log-after-reconnect risks; we recommend fixing these before a formal release"; pre-install resource checks; **MCP device authorization**; APISIX 3.14.1; "fix CVE #2665" |
| v6.9.10 | 2026-09-14 | UDP ports; installer supports sudo users and checks disk I/O |
| v6.9.11 | 2026-10-12 | **Client-certificate (mTLS) checks per domain**; a cleanup of old build versions, images, build cache and uploads that protects what is in use; storage usage of the registry and caches |
| v6.10.0 | 2026-10-27 | Re-authorise a Git OAuth binding; SQLite activation fixes; 409 instead of 500 for a busy cleanup |
| v6.10.2 – v6.10.3 | 2026-10-30 – 11-06 | Build pod CPU and memory (`BUILD_POD_CPU`, `BUILD_POD_MEMORY`); release CI now tests a fresh standalone install before publishing |

### Deploy sources and builds

* **Sources:** Git over OAuth (GitHub, GitLab, Gitee, Gitea) or any Git URL, SVN,
  images from any registry (Harbor and Alibaba ACR named, with an architecture
  choice for multi-arch images), uploaded packages (jar, war, zip; resumable
  uploads of large image archives since v6.4.1), a **Compose project archive**,
  Helm charts, raw Kubernetes YAML converted into the application model, a
  template, or a virtual machine image.
* **Builds:** CNB with Paketo by default since v6.7.0 (Java, Python, PHP, .NET,
  Node.js), the older "slug" buildpacks for components made before it,
  Dockerfile (with a "Dockerfile first" choice since v6.9.1), Maven multi-module
  detection, BuildKit with a cache (v6.5.2). Builds run on chosen build nodes; the
  build pod's CPU and memory are settings since v6.10.2. Front-end builds fill in
  `.npmrc` and `.yarnrc` with Chinese mirrors by default (v6.7.4).
* **Automatic builds:** Git webhooks, registered on the repository when the source
  came through OAuth; **image registry webhooks** (Docker Hub, Alibaba, Harbor
  since v6.9.2) that rebuild an image component when its tag is pushed; and **"API
  auto-build"**: a URL and a secret key per component that any CI can `curl`, with
  Jenkins, GitLab CI and GitHub Actions examples in the docs.
* **Versions and rollback:** every build is a version with its commit; a
  component rolls back to one; versions are kept 30 days. Application snapshots
  (v6.7.0) record the whole application's components, configuration and
  dependencies, show the "current state" against the last snapshot (components
  added, changed, removed), and roll the application back to one. The docs say
  plainly that a snapshot holds no data: "not a replacement for business data
  backup or disaster recovery".
* **Rollouts:** stateless components start the new instance first and stop the
  old one after it is healthy; stateful ones stop and start in order. The docs
  warn that "several versions serve at once" and to use Restart if that is a
  problem.
* **Other workload kinds:** Job, CronJob (with retries, parallelism and a deadline)
  and DaemonSet (host network since v6.9.3).

### Domains, TLS and routing

* Default addresses under `nip.io`. HTTP routes by domain (wildcards allowed) and
  path, to one or **several components with weights** ("web-v1 90, web-v2 10"),
  with method filters, a source-address allowlist, **conditions on a header,
  cookie or query parameter** (equals, not equals, contains, regex), connect,
  read and send timeouts, and a WebSocket switch.
* **Route plugins** from APISIX: `limit-req`, `limit-count`, `limit-conn`,
  `proxy-rewrite`, `cors`, `real-ip`, `client-control`, `redirect`, `jwt-auth` and
  `openid-connect`, so an app can sit behind a sign-in without code. A client CA
  and mTLS per domain since v6.9.11.
* **Certificates:** uploaded PEM files with expiry shown; automatic issuance needs
  cert-manager, which the docs install with `kubectl apply` from the console's
  cluster command line, then an HTTP-01 ClusterIssuer; DNS-01 is described.
* **TCP and UDP** through NodePorts (30000–32767); a TCP policy "can only be
  deleted, not edited". LoadBalancer Services for clouds or MetalLB, with
  annotations as JSON.
* Getting a visitor's real address behind a CDN has been asked about since 2023
  (#1710, open).

### Databases and services

* **A database plugin (open source) on KubeBlocks:** MySQL, PostgreSQL, Redis and
  RabbitMQ; version, CPU, memory, instance count (replication, Sentinel for
  Redis), storage class fixed at creation, resize including disk, monitoring,
  parameters. Backups go to an S3-compatible "backup repository" on a schedule
  with retention or by hand; **a restore creates a new database component**
  rather than overwriting the old one.
* **Dependencies:** linking component A to B injects B's connection variables into
  A — `DB_HOST` and `DB_PORT` from a port alias, plus any variables B declares —
  and A waits in "waiting to start" until B runs. A built-in service mesh mode
  makes a dependency reachable on `127.0.0.1`.
* **Config groups** (variables shared by chosen components) and **config files**
  mounted from ConfigMaps.
* **Plugins on a component:** init containers or sidecars from a plugin market or
  an image, with their own variables.
* **Model serving** (v6.9.0) and **virtual machines** (amd64 only), both plugins.

### Storage and backups

* Local-path volumes by default, which pin a component to its node; temporary
  storage; any StorageClass found in the cluster; a volume can be mounted by
  another component ("shared storage"); files can be uploaded and downloaded from
  a volume in the UI; the size can be changed since v6.7.2.
* **The open-source edition backs up nothing but managed databases.** "Application
  backup and restore" is part of the enterprise "functional extension", and
  "disaster recovery" (S3, schedules, restore of platform metadata with volumes) is
  an enterprise plugin, whose page says of the open-source edition, for storage,
  schedules and restore: "none".
* The console's own database is backed up by hand from Platform Management and
  restored into a new console.

### Scaling and high availability

* Manual horizontal and vertical scaling (the 64 GB memory cap went in v6.9.8),
  and a horizontal autoscaler on CPU or memory usage or utilisation. No scale to
  zero was found.
* A highly available install is three nodes, an external load balancer or
  keepalived in front of several gateway nodes, and an external MySQL; the
  console can move from the quick-install container to pods in a cluster.
* An update that interrupted service for 2–5 seconds with one or two instances
  was reported in November 2025 (#2413, closed).

### Observability (logs, metrics, alerts, uptime)

* Live standard output per instance, operation event logs, build logs, the last
  crash log kept since v6.5.0, and log files downloaded through a volume.
* Resource charts per component from `rbd-monitor`; custom monitoring charts per
  component. Guides for SkyWalking, Pinpoint, Jaeger, Pyroscope and Arthas.
* **Enterprise only:** the Monitoring Center (gateway traffic, per-application
  availability percentage, error and latency rankings across teams and apps), the
  Alert Center ("open-source Rainbond has no alerting capability"; rules with
  severities, silences, email, DingTalk, Feishu, WeCom and webhooks), and the Log
  Center (open source: "only the current real-time log"; enterprise: history,
  search by cluster, service, level and keyword, download). v6.9.6 nevertheless
  lists a node disk usage alert.

### Security, auth, roles, SSO, audit

* Users, enterprise administrators and teams with "basic team roles". Roles down
  to the application or component, custom roles, the operation and sign-in audit
  log, and branding are enterprise.
* Personal access tokens for the OpenAPI. MCP uses a device authorization flow
  since v6.9.8; a user reported that the agent's session expired after one day
  and broke automation (#2577, open).
* Single sign-on to the console: not found in the open-source docs; LDAP has been
  asked for since 2022 (#1425, open). The OIDC plugin protects routes to apps.
* The **Security Center** (enterprise): image vulnerabilities, workload risks,
  network isolation by inbound and outbound allowlists per component, sign-in
  captcha and lockout.
* A web terminal into any container, with a toolbox debug container since
  v6.9.2. RainAgent asks before anything that changes state: "normal risk"
  actions can be auto-approved for the browser session, "high risk" ones are asked
  every time.
* Variables are shown and edited on the component's page; a write-only secret
  type was not found.

### Preview environments and branches

* No pull-request previews were found. Environments are teams bound to clusters;
  **"quick copy"** copies an application to another team or application and lets
  each component's branch or image tag be changed on the way; templates move a
  version between environments.

### Templates and catalogue

* The **Rainbond Application Model** (RAM, "v1.8.2" on the homepage) describes an
  application with its components, dependencies, configuration and plugins. A
  public marketplace (hub.rainbond.com) and a private "internal component
  library".
* **Any running application can be published** as a new template version to the
  internal library or the public store (an account made with a phone number and an
  SMS code), with its images synchronised. Installed applications are then
  **offered the upgrade**, with documented rules per property: components are
  added and updated but never deleted; environment variables, config groups,
  storage and HTTP policies are only added, never updated or removed; health
  probes are added, updated and removed.
* **Offline packages:** a template exported with all its images into a file,
  checked by SHA-256, carried into a network with no internet, and imported. Helm
  repositories can be attached as markets.
* The marketplace shows CPU and memory needs and supported architectures, checks
  cluster capacity before installing (v6.9.8), and hides apps for another
  architecture (fix in v6.7.2).

### CLI, API, IaC, integrations

* An OpenAPI at `/openapi/v1` with personal tokens; the docs have about 75
  endpoint pages (applications, components, gateway, teams, users, clusters).
* **No CLI of its own.** The CLI page lists `kubectl`, `helm` and `crictl`, run
  from a command line in the console or on the node.
* An MCP server used by RainAgent and RainSkills. **RainSkills** installs with
  `npx --yes rainskills` into Codex, Claude Code, Pi Agent, DeepSeek Harness,
  WorkBuddy and Hermes Agent, signs in through the browser ("no pasting JWTs,
  passwords or private keys into the chat"), keeps the credential in "protected
  local state", deploys the current project, reads build and runtime logs, and
  counts a deploy as done only after the page and API answer.
* No Terraform provider or configuration-as-code file was found.

### Notifications

* Not found in the open-source edition. The Alert Center (enterprise) sends email,
  DingTalk, Feishu, WeCom and webhooks. A notification after an automatic build
  has been asked for since 2023 (#1580, open).

### Multi-server and networking

* One console, many clusters: servers set up by the wizard (RKE2), existing
  clusters through Helm, and managed clouds. Teams are bound to clusters, and
  applications are copied or migrated between them. x86 and ARM servers in one
  cluster, and Chinese domestic CPUs and operating systems (Kunpeng, Kylin),
  which the enterprise comparison table marks as an enterprise feature.
* Gateway and build roles are chosen per node. Traffic between servers is the CNI
  of the chosen Kubernetes; nothing of Rainbond's own encrypts it.

### Team and collaboration

* Enterprise, teams (namespaces), applications, components; invitations; team
  resource quotas; a private template library shared across teams; copying an
  application across teams.

### Developer experience and onboarding

* One command (`curl -o install.sh https://get.rainbond.com && bash ./install.sh`)
  and a console on port 7070. The README recommends "2 CPU / 8GB RAM / 50GB disk";
  the installer refuses under 4 GB; host-installed nodes are "4 CPU, 8 GB+, 50 GB+"
  and run as root.
* The first application page shows a sample deployment (v6.9.8); a topology canvas
  where a dependency is drawn by dragging from one component to another.
* The documentation site is built for one locale, Chinese (`defaultLocale: 'zh'`,
  `locales: ['zh']`); the README and some release notes are in English.
* **The installer reports to Goodrain.** `send_warn` and `send_error` post each
  message with the server's external address, OS details and an install UUID to
  `https://log.rainbond.com/dindlog`; when the operator does not come up, pod
  diagnostics go to `/logCollect`. `DISABLE_LOG=true` turns off the second and
  not the first (read from the script on 2026-11-08, not run).

## What users love

Independent first-hand praise is scarce: the English-speaking forums have almost
nothing, and the Chinese forum is mostly support threads. What there is comes
from case studies hosted on Rainbond's own site, so it is the vendor's selection.

* **The application, not the cluster.** Talkweb's platform team chose it over
  other PaaS products because of "the application-centric design ... genuinely
  easy to use, hiding infrastructure concepts so the development team can focus
  on the business", and adds "Rainbond is easy enough that we don't need to
  explain how to build and manage components with it" (case study, translated).
* **Topology and local access.** The same team called the service-mesh mode,
  where a dependency is reached on localhost with no config change, "an
  unexpected pleasure", alongside "fast copying across clusters and teams" and
  "custom init and sidecar containers".
* **Gateway features without YAML.** A reading app with 15 million registered
  users used the gateway's grey release and A/B routing and the autoscaler,
  "simple to configure" (case study, translated).
* **Delivery to places the internet does not reach.** The offline package with
  images and checksums, and the publish-and-upgrade loop, are what the product
  leads with and what nobody else in Skifity's list does.
* **A steady cadence.** 30 releases in eleven months, each with upgrade steps for
  online and offline installs.

## What users complain about

* **Installing.** The most-discussed issues are install failures: CentOS 8 stuck
  initialising (#1265, 26 comments), `get.rainbond.com` resetting the TLS
  connection (#2006, 23), a Windows quick install that would not run (#2252, 20),
  Ubuntu 22.04 hosts (#1262, #1319), and forum threads about RKE2 gateways stuck in
  Pending (2025-03-19) and `rbd-api` restarting (2025-07-15).
* **Builds.** The two busiest forum threads are front-end Vue builds failing (23
  and 18 replies); Java could not build on v6.7.0 (#2547); upgraders asked how to
  use the new CNB builds offline (#2555).
* **Exporting and importing large applications.** Complex multi-component exports
  fail and big imports time out (#2469, open), Helm chart export fails (#2186,
  #2340, open), and a console restored from a backup could not export an offline
  model (#1965, 27 comments).
* **Its own weight.** A worker using abnormal resources (#2144, open since
  2025-02), a worker re-election that kept listing every pod (#2720, open), the
  registry volume growing without bound (forum, 2025-10-15), and a request for
  disk cleanup in the UI open since 2023 (#1819; v6.9.11 added one).
* **Architecture surprises.** On an Apple Silicon Mac every marketplace install
  says the app's architecture does not match the build node's (#2572, open).
* **Errors that are not errors.** A June 2026 batch of issues (#2610–#2646) lists
  panics, `NameError`s and raw backend errors reaching the user; several are still
  open. In SQLite mode a rollback fails on `FOR UPDATE` but the API answers success
  and the event waits until it times out (#2726, open).
* **Rolling updates that are not.** "Update (rolling) is not actually a rolling
  update; the business is visibly interrupted" for 2–5 seconds (#2413, 2025-11).
* **The paid line.** Alerts, the audit log, roles below the team, application
  backup, log search and notifications are enterprise, and in the community
  edition the AI assistant is "only for the first registered enterprise
  administrator".
* **Agents that sign out.** "The MCP session expires after one day, so online
  automation cannot keep running" (#2577, open).

## Security record

* **NVD:** one CVE. **CVE-2026-72741** (published 2026-08-13 by VulnCheck; CVSS 4.0
  8.6, CVSS 3.1 8.1): "Rainbond through 6.9.7 contains a broken access control
  vulnerability in the CheckToken function that allows authenticated attackers to
  access unauthorized enterprise resources by substituting another enterprise's
  tenant name in URL paths". The region API's `CheckToken` checked a token's
  validity period and class, never its enterprise, and `InitTenant` resolved any
  `tenant_name` in the path globally. It was reported on 2026-08-03 as a public
  issue with the whole chain (#2665) and fixed in v6.9.8 (2026-08-17).
* **Open on 2026-11-08: an unauthenticated file listener.** Issue #2723
  (2026-10-22) links a public write-up: `rbd-api` starts a second HTTP server on
  `0.0.0.0:6060` with a router made by `chi.NewRouter()` that never gets the token
  middleware, and mounts `/v2/file-operate` on it, whose download and upload
  handlers take the namespace, pod and container from the request — so whatever
  reaches port 6060 reads and writes files in any container in the cluster. The
  route's CORS wrapper reflects any `Origin` with credentials allowed. Read on
  `main` on 2026-11-08, `api/server/api.go` still builds that router the same way
  and `config/configs/websocket_config.go` still defaults to `0.0.0.0:6060`
  (read from the code, not run). How far it reaches depends on the install: the
  quick install publishes only 7070, 80, 443 and 30000–30010 on the host; the HA
  guide tells the load balancer to open 6060; and a tenant's pods share the
  cluster network with `rbd-api` unless a policy separates them, which is the
  enterprise Security Center's job.
* **Profiling on the default mux.** The same file imports `net/http/pprof` and
  serves `http.DefaultServeMux` on `:6789` with no authentication (read from the
  code, not run). Heap profiles of an API server can hold tokens and passwords.
* **Bundled components:** MinIO with CVE-2024-24747 was reported against v6.3.2
  (#2284, 2025-08-01, closed 2025-10-23); v6.9.11 fixed "illegal API requests that
  could make the service panic".
* **Disclosure:** no `SECURITY.md` on `main` (404); both reports above arrived as
  public issues with exploit detail. OSV has no entry for
  `github.com/goodrain/rainbond`; the GitHub advisory API was refused from this
  session.
* **Exposure by design:** a `--privileged` container for the quick install; a
  web terminal into every container; Kubernetes "advanced attributes" in the UI
  that include `privileged`, `serviceAccountName`, raw volumes and tolerations; an
  installer that reports errors with the server's address; network isolation and
  sign-in lockout only in the enterprise edition.

## Against Skifity

Skifity's statuses: much of it runs on a real single-server and two-server k3s on
every push (`docs/progress.md`, "A real cluster, at last"); a mixed-architecture
cluster and production traffic never have. Rainbond is released and used in
production, by its case studies. Line numbers are from Skifity's working tree on
2026-11-08.

| Capability | Rainbond | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Sources | Git (OAuth for four hosts), SVN, image, packages, Compose archive, Helm, YAML, template, VM | Git (GitHub, GitLab, Gitea, Bitbucket, generic), image, folder upload, Compose stack (`internal/gitsrc`, `internal/api/stack_handlers.go`); no Helm or raw YAML route in `internal/api/api.go` | parity for apps; Helm and YAML deliberately absent |
| Builders | CNB/Paketo default, slug, Dockerfile, BuildKit cache, build nodes and build pod size | Railpack default, Nixpacks, Dockerfile, static (`internal/builder/detect.go:26-28`), build queue (Phase 112) | parity |
| Deploy from CI | A URL and secret per component; registry webhooks (Docker Hub, Alibaba, Harbor) | An API token with the deploy scope; the deploy body has `commit_sha`, `force` and `accept_vulnerabilities`, no image (`internal/api/deploy_handlers.go:19-26`); only Git webhooks (`internal/api/api.go:285`) | **behind** (gap 2) |
| Rollback | A component to a build version (30 days); the whole application to a snapshot | One app to a deployment with its settings, shown first (Phase 94); promote between environments (`internal/api/api.go:495-496`) | ahead per app; **behind for the whole environment** (gap 3) |
| Compose | Archive with referenced files, start order from dependencies | A stack, built from the file's context, deployed in `depends_on` order (`deployOrder`, `internal/api/stack_handlers.go:322`) | parity |
| **Domains, TLS and routing** | | | |
| Certificates | Upload; cert-manager installed by hand with `kubectl` | cert-manager installed on the first domain (`internal/cluster/issuer.go`), a team's own certificates (`internal/tlscert`), DNS records at four providers (`internal/dnsprov`); no DNS-01 | **ahead** |
| Edge rules | Per route: address allowlist, methods, header/cookie/query conditions, rate and connection limits | Per app: address, country, ASN, host, path, method, user agent, headers, rate limits, maintenance (`internal/edgerules`, `internal/guard`) | ahead on rules |
| Sign-in in front of an app | `openid-connect` and `jwt-auth` route plugins | One shared password: "It is one shared account, not a sign-in system" (`docs/firewall.md:149-184`) | **behind** (gap 5) |
| Splitting traffic | Weights across components; header, cookie or query matches | None: no weighted service or canary anywhere in `internal/kube` | **behind** (gap 6) |
| Timeouts, CORS, mTLS | Per route timeouts, a CORS plugin, client CAs per domain | `Domain` has hostname, path, TLS and a redirect (`internal/store/models.go:433-458`) | behind (small, gap 13) |
| TCP and UDP | NodePorts, LoadBalancer Services | Public TCP and UDP ports (Phase 112) | parity |
| **Databases and services** | | | |
| Engines | Four in the open-source plugin (MySQL, PostgreSQL, Redis, RabbitMQ), with replicas | Nine (`internal/dbsvc/engine/engine.go`) | ahead on count |
| Restore | Into a new database component | In place, after a backup (`docs/backups.md`, `docs/databases.md`) | **behind** (gap 8, raised before) |
| Dependencies | Injected `X_HOST`/`X_PORT` and declared variables; start waits for dependencies | Database links give `DATABASE_URL`; apps reach each other by name and port (`docs/concepts.md:629-637`) | parity |
| Sidecars and init containers | Plugins with their own images | Processes from the same build (Phase 106); no sidecar of another image | different |
| **Storage and backups** | | | |
| App and volume backups | Enterprise only | Volume backup and restore, sealed and verifiable backups (Phase 104, `internal/backup`) | **ahead** |
| Volume size | Changeable since v6.7.2 | No resize for an app's volume; local-path never grows a database disk (see `hakopod.md` gap 9) | behind |
| Control-plane backup | Manual, from Platform Management | Scheduled panel backups to the bucket (Phase 84) | ahead |
| **Scaling and HA** | | | |
| Instances and autoscaling | Manual, HPA on CPU and memory | Instances, HPA, scale to zero through KEDA (`internal/kube/scaletozero.go`), readiness checker (`internal/deploy/scaling.go`) | ahead |
| Control plane HA | Three nodes, external MySQL, several gateway nodes | One panel on one node with SQLite | behind, by design |
| **Observability** | | | |
| Usage history and alerts | Charts; alerts are enterprise | CPU, memory and disk history with thresholds, `app.alert`, `server.alert`, `backup.missed` (Phases 95, 99; `internal/notify/notify.go`) | **ahead** |
| Requests | Enterprise Monitoring Center: SLA, error and latency rankings | Requests, errors and latency per app (`internal/traffic`, `web/src/components/app/traffic-charts.tsx`); nothing across apps on `web/src/pages/dashboard.tsx` | ahead per app; behind across apps (gap 14) |
| Logs | Live, last crash; history and search enterprise | Live, previous container (`internal/api/stream_handlers.go:427-431`), log drains (`internal/logdrain`) | parity |
| **Security, auth, roles, SSO, audit** | | | |
| Roles and audit | Basic team roles; finer roles and audit are enterprise | Owner, admin, member, viewer, members limited to projects, audit log (Phases 85–86, `internal/api/api.go:345`) | **ahead** |
| Console sign-in | Password; SSO not found; LDAP open since 2022 | Argon2id, TOTP, passkeys, OIDC with groups mapped to roles, team-required 2FA (`internal/auth`, Phases 91–92) | **ahead** |
| Secrets | Variables shown in the UI | Write-only, sealed to their context (`internal/crypto`) | ahead |
| Vulnerabilities | Security Center (enterprise) | Trivy on deploy and nightly, a deploy gate (`internal/vulnscan`) | ahead |
| Network isolation | Enterprise Security Center | Default-deny namespaces (`internal/kube/namespace.go`), the pod waits for its policy (`internal/netfence`) | ahead |
| Listeners | A second router with no auth on 6060; pprof on 6789 | Three servers, each with its own check: the panel (`internal/serverapp/serverapp.go:217`), the guard (`internal/guard/guard.go:337`), the registry guard (`internal/registryguard/registryguard.go:134`); no pprof in `internal/` or `cmd/` | ahead |
| Terminal | Web terminal with a toolbox container | None by decision; one-off commands in the app's own image (`docs/roadmap.md:149-153`, `internal/deploy/run.go:27`) | different (gap 7 for the toolbox) |
| AI assistant | RainAgent in the console with risk-tiered approvals; first admin only in community | MCP over HTTP and stdio with read-only annotations (`internal/api/api.go:610`, `internal/mcpserver`); nothing holds a change for a person | behind on approvals (gap 10) |
| **Preview environments** | | | |
| Previews | None found; copy an app with another branch | Per pull request with statuses, preview-only values and seeded databases (Phases 93, 107) | **ahead** |
| Cloning | Across teams and clusters, branch or tag per component | Within the project; data, domains and push deploys left behind (`internal/api/envclone_handlers.go:14-28`) | behind on reach |
| **Templates** | | | |
| Catalogue | Marketplace ("100+") and an internal library | 374 with pinned versions (`internal/templates/catalogue`); team catalogues from an https address (`internal/api/template_catalogues.go:293`) | ahead on count |
| Publish your own | Any running application becomes a versioned template | Nothing turns an environment into a template; team export is for leaving (`internal/api/api.go:348`) | **behind** (gap 1) |
| Upgrades | Offered, with add-only rules for most properties | Offered, backed up first, changed-by-hand images kept (Phase 102, `internal/api/api.go:484`) | ahead on safety |
| Architecture | Marketplace filtered by architecture, mismatches refused | No template records one; no `kubernetes.io/arch` in `internal/` | **behind** (gap 4) |
| Offline | Offline install and template packages with images and checksums | The installer downloads k3s, cert-manager and images (`installer/install.sh:140`); no bundle | **behind** (gap 9) |
| **CLI, API, IaC, integrations** | | | |
| CLI | None (`kubectl`, `helm`, `crictl`) | About 40 commands (`internal/cli/commands.go:36-119`) | **ahead** |
| API | OpenAPI, about 75 documented endpoints | OpenAPI described and tested against the router (`internal/api/openapi.go`) | ahead |
| Configuration as code | None found | `skifity.yaml` plan and apply (`internal/blueprint`); nothing writes one from an environment (`docs/cli.md:330-445`) | ahead; gap 3 adds the reverse |
| Agent skills | RainSkills, browser sign-in | None (`sealos.md`, "For Skifity"); tokens pasted (`docs/cli.md:41-48`) | **behind** (gap 11, raised before) |
| **Notifications** | Enterprise only | Ten channels, routed by project (`internal/notify`) | **ahead** |
| **Multi-server and networking** | | | |
| Clusters | Many per console | One, by decision (`docs/roadmap.md:155-156`) | deliberate difference |
| Servers | RKE2 by wizard, a registration command per node | k3s over SSH, Hetzner and DigitalOcean from the panel (`internal/provision`, `internal/cloud`) | parity |
| Between servers | The CNI's own | WireGuard flannel by default (`internal/settings/settings.go`) | ahead |
| **Team and collaboration** | | | |
| Quotas | Team resource quotas | A fixed ceiling per environment (`internal/kube/namespace.go:25-44`), read-only (`internal/api/api.go:457`) | behind (small, gap 12) |
| **Developer experience** | | | |
| Install | 4 GB minimum, 8 GB recommended, 50 GB; a privileged container | 2 GB, 8 GB disk (`README.md:104-108`), k3s on the host | **ahead** |
| Languages | Docs in Chinese only | Panel in five languages with a build gate (`web/src/locales`) | ahead |
| Telemetry | Installer reports errors with the server's address | None | ahead |
| GPUs | Whole cards; fractions are enterprise | Whole cards: "A GPU is not shared" (`docs/gpus.md:139`) | parity |

### Claims in this repository that no longer hold

* `docs/research/competitors.md` says "None of them puts Kubernetes on one small
  server with one command." Rainbond does put Kubernetes on one server with one
  command, as Hakopod does. What is still Skifity's is "small": Rainbond's
  installer refuses under 4 GB and recommends 8 GB, inside a privileged container.
* The same file's "Hiding Kubernetes well is still the whole bet" names Portainer
  as the product with the same pitch. Rainbond has made exactly this pitch since
  2017 — "No Kubernetes expertise required" — and since v6.7.0 has also gone the
  other way, adding raw YAML, Helm releases and namespace adoption beside its
  application model. It is the oldest evidence for and against the bet.
* The list of things not copied says "SSO, audit logs or roles behind a licence
  (Dokploy since January 2026, CapRover's paid two-factor)". Rainbond is a larger
  example: alerts, audit, roles below the team, application backup and log search
  are all enterprise.

## Gaps worth closing in Skifity

Each was checked against Skifity's code on 2026-11-08.

**P1**

1. **Save an environment as a template, and install it in another project or
   team.** *What:* a "Save as template" action on an environment that writes a
   template in the catalogue's own format — its apps with images pinned by
   digest, processes, files, schedules, health checks, database links,
   variables, and the names (never the values) of its secrets — into a catalogue
   the panel keeps for the team, with a version each time it is saved. It
   installs like any template into another project, or another team the person
   belongs to, asks for the secrets, and is offered later versions through the
   update path Phase 102 already has. *Evidence:* this loop — build it once, publish
   it, install it for the next customer, upgrade them all — is what Rainbond
   leads with, and its delivery and marketplace pages are built around it.
   Skifity's templates come from the built-in catalogue or from a team catalogue
   fetched from an https address (`internal/api/template_catalogues.go:293`,
   `docs/templates.md:133-165`), so a team that has a working environment must
   write the catalogue file by hand and host it somewhere. The clone stays inside
   the project (`internal/api/envclone_handlers.go:14-28`) and the team export is
   for leaving (`internal/api/api.go:348`). *Fit:* a generator from store rows to
   `templates.Template` beside `internal/templates`, a local source for
   `TemplateCatalogueRow`, the existing installer in
   `internal/api/template_install.go`. *Size:* M. *Without a cluster:* yes; the
   install that follows is the template path the Cluster workflow already runs.
2. **A deploy hook per app, for CI and for registries.** *What:* an app setting
   that creates a hook URL with its own secret (shown once, sealed, revocable),
   `POST /api/hooks/deploy/{hook}`, that deploys the app; with `{"image": "…"}`
   it deploys that image first, refused unless it is the app's own repository;
   it also accepts the push payloads of Docker Hub, Harbor and GitHub's package
   events, so pushing a tag redeploys an image app. The app page shows the `curl`
   line for GitHub Actions, GitLab CI and Jenkins. *Evidence:* Rainbond documents
   both, "API auto-build" with a secret key and registry webhooks (Harbor since
   v6.9.2). Skifity has only Git webhooks (`internal/api/api.go:285`), and the
   deploy body has no image (`internal/api/deploy_handlers.go:19-26`), so a CI job
   holds a whole API token and makes two calls. `kamal.md` and `hakopod.md` raised
   the image half; a hook is the smaller credential for it. *Fit:* the deploy
   handler, a sealed secret per hook, the audit entry as "hook", the image
   recorded on the deployment so rollback restores it. *Size:* S–M. *Without a
   cluster:* yes.

**P2**

3. **Versions of an environment: saved, compared, and rolled back together.**
   *What:* a version of an environment's settings, saved by hand and before
   anything that changes several apps (a template update, `skifity apply`, a
   promotion); the environment page says what changed since the last version
   (apps added, changed, removed); a version can be compared with now, written
   out as a `skifity.yaml`, and restored, which applies its settings and rolls
   each app back to the deployment it recorded. *Evidence:* Rainbond's
   application snapshots (v6.7.0), with the "current state" against the last
   snapshot on the version page. Skifity rolls back one app at a time (Phase 94);
   after changes to five apps and a link nothing returns the environment to how
   it was on Tuesday. `internal/blueprint`'s `Plan` already computes ordered
   differences between a file and an environment, and nothing writes a file from
   an environment (`docs/cli.md:330-445`). *Fit:* the generator of gap 1, a
   version table per environment, `Plan` for the diff, the existing rollback per
   app. *Size:* M. *Without a cluster:* saving and comparing yes; the rollback's
   rollout needs one.
4. **Templates and apps that know their architecture.** *What:* the catalogue
   check records which architectures each template's images offer (from the
   manifest list it already resolves); the template page marks one that cannot run
   on any server in the cluster and refuses to install it; a build is pinned to
   the architecture of the node it ran on. *Evidence:* Rainbond filters its
   marketplace by architecture and refuses mismatches (#2572 shows the message);
   Skifity supports amd64 and arm64 servers (`README.md:105`) and offers Hetzner's
   ARM machines, yet no template records an architecture and nothing in
   `internal/` mentions `kubernetes.io/arch`. Open since `kamal.md`. *Size:* S
   (metadata and refusal), M (pinning). *Without a cluster:* metadata and the
   refusal yes; placement needs a mixed cluster.
5. **The team's sign-in in front of an app.** *What:* on the Firewall tab, beside
   Password, "Sign in with this panel": the guard sends a visitor without a
   session for that app to the panel, which signs them in (password, passkey or
   the team's SSO) and returns a cookie scoped to the app's host; members of the
   team, or of chosen projects, get through, and the app receives the person's
   email in a header the guard sets after removing any the visitor sent.
   *Evidence:* Rainbond's `openid-connect` and `jwt-auth` route plugins (v6.9.2);
   Coolify points people to Authentik forward-auth. Skifity's password is "one
   shared account, not a sign-in system" (`docs/firewall.md:149-184`), so a staging
   site or an internal tool either shares a password or trusts the network. The
   guard is already Traefik's forward-auth for the app (`internal/guard/guard.go`).
   *Size:* M. *Without a cluster:* the guard and the panel's side yes; the
   Cluster workflow already sends requests through the live guard.
6. **Weighted and header-matched routing between two apps.** *What:* a domain
   that sends a share of requests, or requests with a given header or cookie, to a
   second app, for a canary or an A/B test, with the share changed from the
   Domains tab. *Evidence:* Rainbond's routes take weights across components and
   header, cookie and query conditions; `kamal.md` raised percentage rollouts.
   Skifity has nothing of the kind in `internal/kube`. *Fit:* a Traefik weighted
   service per domain, matchers on the router. *Size:* M. *Without a cluster:*
   rendered objects yes; the split needs one.
7. **A toolbox for one-off commands.** *What:* `skifity run --toolbox -- curl
   http://api:8080/health` and the same switch in the panel: the command runs in
   a small pinned image with `curl`, `dig`, `nc` and database clients, in the
   app's environment, with its variables and network policy, logged and audited
   like any run. *Evidence:* Rainbond added a toolbox container to its terminal
   (v6.9.2) after #2571 ("the common commands are not in the container, which
   makes troubleshooting hard"). Skifity's one-off command uses the app's own
   image (`internal/deploy/run.go:27`), so a distroless or slim image cannot test
   its own database connection. It keeps `docs/roadmap.md`'s reasoning: a command,
   not a shell. *Size:* S. *Without a cluster:* the Job yes.
8. **Restore a database into a new one.** *What:* restore a backup as a second
   database beside the first, to compare or to switch the link once it is
   checked. *Evidence:* Rainbond's database plugin restores "into a new database
   component, without overwriting the original"; Hakopod does the same, and
   `openship.md` gap 8 asked for it. Skifity restores in place after a backup
   (`docs/backups.md`). *Size:* M. *Without a cluster:* the plan and the API yes;
   the restore needs one.

**P3**

9. **An offline install and offline template bundles.** *What:* `skifity bundle`
   writes one file with k3s's air-gap images, the panel's image, cert-manager and
   the images of chosen templates, with a SHA-256 manifest; `install.sh --bundle
   FILE` installs from it with no internet, and the template installer reads
   images from it. *Evidence:* Rainbond's offline install and offline template
   packages are its main market (labs, factories, government networks). Skifity's
   installer downloads k3s, cert-manager (`installer/install.sh:140`) and images
   at install time. Worth doing only if that market is wanted. *Size:* L. *Needs:*
   a VM with no network.
10. **Hold an assistant's risky changes for a person.** *What:* risk levels on the
    MCP tools; "normal" changes can be approved for the rest of a session,
    "high" ones (delete, restore, a production deploy) are held until a team admin
    approves them in the panel. *Evidence:* RainAgent's approval cards, with
    session-only auto-approval that never covers high-risk actions; `hakopod.md`
    gap 16. Skifity's MCP tools carry read-only annotations
    (`internal/mcpserver`) and nothing holds a write. *Size:* M. *Without a
    cluster:* yes.
11. **Agent skills, and a browser sign-in for them.** Raised in `sealos.md` and as
    `hakopod.md` gap 4; Rainbond is a third product doing it, with numbers:
    RainSkills had 3,935 npm downloads in the 30 days to 2026-11-07 and 120
    versions in three months, signs in through the browser, and #2577 shows the
    trap — a one-day token breaks automation. Skifity's tokens are pasted
    (`docs/cli.md:41-48`) and take a time to live; keep a long one possible for
    automation. *Size:* M. *Without a cluster:* yes.
12. **Ceilings an admin can set per environment.** The quota exists and is fixed
    (`internal/kube/namespace.go:25-44`), readable through `/quota`
    (`internal/api/api.go:457`); Rainbond sets quotas per team. A form on the
    environment's settings for the CPU, memory and storage ceiling. *Size:* S.
    *Without a cluster:* yes.
13. **Per-domain timeouts, body size and CORS.** Rainbond sets connect, read and
    send timeouts per route and has a CORS plugin. Skifity's `Domain` has none of
    these (`internal/store/models.go:433-458`), so an upload or a long poll meets
    Traefik's defaults. *Size:* S. *Without a cluster:* rendered middleware yes.
14. **What is failing, across apps.** A dashboard card that ranks the team's apps
    by error rate and slowest response over the last hour, from the counts
    `internal/traffic` already keeps; Rainbond sells this as part of its Monitoring
    Center. `web/src/pages/dashboard.tsx` shows no traffic. *Size:* S. *Without a
    cluster:* yes, with seeded counts.

## Things to deliberately not copy

* **The basics behind "contact us".** Alerts, the audit log, roles below the
  team, application backup and log search are enterprise, and the community
  edition's assistant serves one person. Skifity's own list already says not to
  put SSO, audit or roles behind a licence; Rainbond widens that list to backups
  and alerts.
* **"100% open source" over a licence with extra conditions.** A no-SaaS clause
  and a logo clause on top of Apache 2.0 may be reasonable; the headline that
  hides them is not.
* **A second listener with its own router.** Port 6060 was a fresh `chi.Router`
  that never got the token middleware, and pprof sits on the default mux on 6789.
  Skifity's rule — one router per process, every route through the same
  authorization — is the answer; keep the route walk that enforces it.
* **Kubernetes in the UI.** `privileged`, `serviceAccountName`, tolerations, raw
  volumes, DaemonSets with host networking, raw YAML, Helm releases and
  namespace adoption make the product a Kubernetes console again. Skifity's
  Advanced tab stays read-only.
* **Upgrades that only ever add.** Rainbond's template upgrades never update or
  remove variables, config groups or storage, so an installed application drifts
  from its template without saying so. If Skifity versions environments
  (gaps 1 and 3), an upgrade should be a plan that names every change.
* **Multi-cluster, virtual machines, and a model platform inside the panel.**
  Each is a product of its own; `docs/roadmap.md` already declines the first, and
  GPUs plus templates (Ollama, Open WebUI, LiteLLM) cover model serving.
* **A privileged container as the install.** It makes a trial fast and leaves
  the production install inside one container with k3s in it. Skifity puts k3s
  on the host, with a preflight that says what is wrong.
* **Telemetry from the installer**, with the server's address, and an opt-out
  that covers half of it. Skifity sends nothing.
* **Security reports as public issues.** Both of Rainbond's 2026 reports were
  public, with exploit detail, because there is no `SECURITY.md`. Keep Skifity's.
* **Releasing a feature its own notes say is not ready.** v6.9.8 shipped SSE
  logs with "SSE proxy authentication ... risks; we recommend fixing these before
  a formal release".
* **A draggable topology editor.** Skifity's canvas is "deliberately not a
  draggable graph" so it stays readable on a phone
  (`web/src/pages/project-detail.tsx:553-558`); links are made from the app.
* **Documentation in one language** for a product sold abroad. Skifity's panel is
  in five; keep its documents and error catalogue in step.

## Sources

All read on 2026-11-08.

* GitHub search API: `search/repositories?q=repo:goodrain/rainbond` (stars, forks,
  open issues, dates, licence, topics), `q=org:goodrain` (83 repositories, among
  them `rainbond-console`, `rainbond-ui`, `rainbond-operator`, `rainskills`), and
  the four core repositories' languages
* GitHub search API for issues: `repo:goodrain/rainbond` open by reactions, newest
  40, and most-commented 30; the bodies of #2665, #2723, #2284, #2413, #2577, #2572
  and #2726, and the titles and states of #1265, #2006, #2252, #1262, #1319, #2547,
  #2555, #2469, #2186, #2340, #1965, #2144, #2720, #1819, #2571, #2610–#2646,
  #1425, #1580, #1710, #1273
* https://ungh.cc/repos/goodrain/rainbond/releases (a mirror of GitHub's releases
  API: the notes of v6.4.1-release to v6.10.3-release)
* https://raw.githubusercontent.com/goodrain/rainbond/HEAD/README.md and `/LICENSE`;
  `SECURITY.md`, `README_zh.md` and `CHANGELOG.md` (404)
* https://raw.githubusercontent.com/goodrain/rainbond/main/api/server/api.go,
  `/api/api_routers/websocket/websocket.go` and
  `/config/configs/websocket_config.go`
* https://github.com/goodrain/rainbond-docs, cloned at `1881a72` (2026-11-04):
  `docusaurus.config.js`; in `docs/`: `quick-start/introduction.md`,
  `quick-start/quick-install.mdx`, `quick-start/open-source.md`,
  `quick-start/architecture/architecture.md`, `installation/index.md`,
  `installation/install-with-ui/index.md`, `ops-guides/configuration/ha.md`,
  `ops-guides/cli-tools.md`, `ops-guides/management/console-recover.md`,
  `api/Intro.md` and the list of `api/`, `faq/index.md`, `ai/index.md`,
  `ai/rainagent/index.md`, `ai/rainskills/index.md`, every page in `plugins/`, and
  in `how-to-guides/`: `app-ops/app-snapshot.md`, `delivery/upgrade-app.md`,
  `app-ops/app-store-manage/share-app.md`, `app-ops/dependon.md`,
  `app-ops/environment-manage.md`, `app-ops/auto-build.md`,
  `app-deploy/gitops.md`, `app-ops/storage.md`, `app-ops/service-auto-scaling.md`,
  `app-ops/component-logs.md`, `app-ops/web-terminal.md`, `app-ops/lifecycle.md`,
  `app-ops/k8s-attribute.md`, `app-ops/app-sidecar.md`, `app-deploy/deploy-job.md`,
  `app-deploy/docker-compose.md`, `k8s-native-resource/index.md`,
  `app-gateway/http.md`, `app-gateway/lb.md`, `app-gateway/tcp.md`,
  `app-gateway/certificate.md`, `app-ops/cert-manager.md`,
  `scenario-tutorials/fully-offline-delivery.md`,
  `scenario-tutorials/multi-cluster-app-management.md`,
  `delivery/continuous/multi-env.md`; `tutorial/custom-gateway.md`;
  `src/pages/server.md` (Rainbond Cloud terms); `src/pages/case/talkweb.md` and
  `csgApp.md`, and the case-study list in `blog/2025`
* https://www.rainbond.com/ (the same page is served at `/pricing` and
  `/enterprise`), https://www.rainbond.com/enterprise_server (editions),
  https://www.rainbond.com/compare, https://www.rainbond.io/,
  https://run.rainbond.com/
* https://get.rainbond.com (the quick-install script, 147,865 bytes, read for its
  memory check, `docker run` line and reporting)
* https://hackmd.io/@hieuPenguinnn/rainbond-unauth-file-operate-cross-tenant-container-access (the report linked from #2723)
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=rainbond (one
  result, CVE-2026-72741); https://api.osv.dev/v1/query for
  `github.com/goodrain/rainbond` (no results)
* https://raw.githubusercontent.com/goodrain/rainskills/HEAD/README.md;
  https://registry.npmjs.org/rainskills and
  https://api.npmjs.org/downloads/point/last-month/rainskills
* https://hn.algolia.com/api/v1/search?query=rainbond and `search_by_date` (two
  stories: item 16330489, 2018-02-08, and item 37594036, 2023-09-21)
* https://t.goodrain.com/api/discussions, sorted by replies and by date
* Skifity files named in "Against Skifity" and the gaps, read in this repository
  at `29147f1`
* Not readable from here: the GitHub repository REST API and the advisory API
  (refused by this session), Reddit (HTTP 403), and web search (Firecrawl credits
  exhausted), so no third-party review outside GitHub, Hacker News and Rainbond's
  own forum was read
