# HivePaaS

> **Status.** Written on 2026-11-08 against HivePaaS's `main` at commit `9779998`
> (2026-11-07; newest tag `v1.0.0-beta6`, 2026-11-05), its documentation site at
> `ce73465` and its template repository at `73015a4`, and against Skifity's
> working tree at `29147f1` the same day. No gap named here has been closed yet;
> they are proposals.

HivePaaS (`hivepaas/hivepaas`, hivepaas.com, docs.hivepaas.com) is a self-hosted
platform for running apps on your own servers, "an open-source, resource-efficient
alternative to Heroku, Render, and Coolify". You give it an image, a Git
repository, a Docker Compose file, a template from its app store or a function's
code, and it builds, deploys, routes and backs the app up. Its feature list is
close to Skifity's almost line for line; the difference is underneath.
**Architecture:** Docker Swarm, from one server to a cluster, with Traefik v3 in
front. On the first server, a manager, the installer deploys a `hivepaas` stack:
Traefik, **PostgreSQL 18** (all of HivePaaS's state), **Redis 8** (sessions,
locks, the task queue, rate limits), and HivePaaS's own app, worker and updater,
all one Go binary (Gin, Bun, lego) with a React 19 dashboard embedded. A Go
**agent** runs in global mode on every node and is reached over gRPC; it builds
images with BuildKit, runs commands and backups, and reads the node, and in the
project's own words HivePaaS "has root on your servers through its agent"
(`SECURITY.md`). Each app is a Swarm service; each project environment is its own
overlay network. Optional system apps, switched on from the dashboard, are
VictoriaLogs and vlagent (stored logs, metrics and autoscaling), OBI (eBPF
route and call metrics), Trivy (image scanning), zot (a registry for
multi-node builds) and Kopia (backups). **Licence:** Apache-2.0 for the
platform; the template catalogue (`hivepaas/app-templates`) is under the
**PolyForm Shield 1.0.0** licence, which is source-available, not open source.
**Pricing** (hivepaas.com/pricing and docs, read 2026-11-08): open core with
offline licence keys, sold through Paddle. Without a key an installation runs
**Bee**, free with no time limit: 2 nodes, 2 users, 20 apps, 20 functions and 2
pull-request previews at a time, and sign-in with GitHub, GitLab and Gitea only.
**Comb** $4.99 a month ($47.99 a year) for 5 nodes and 5 users; **Hive** $12.99
($124.99) for 10 and 10, with a 14-day trial and no card; **Colony** $24.99
($239.99) for 25 nodes and 30 users; **Apiary** quoted. Every paid plan lifts the
app, function and preview limits and adds Google, Microsoft and OpenID Connect
sign-in. Keys are checked offline against an Ed25519 and an ML-DSA-65 public key
compiled into the binary; with a key installed, a daily renewal request sends
the key and the installation ID, and can be turned off. The rule the docs repeat
is that "a limit only stops creating; it never stops what runs". **Maturity:**
beta. Six pre-releases, `v1.0.0-beta1` (2026-10-03) to `v1.0.0-beta6`
(2026-11-05), with `SECURITY.md` supporting the latest beta only. **Adoption**
(read 2026-11-08): 334 stars and 41 forks (GitHub search API); no GitHub issue
has ever been opened, and the seven "open issues" are Dependabot pull requests;
37 pull requests in all, six by the maintainer and the rest by Dependabot. The repository
was created on 2025-10-18; `main` holds 3,664 commits, 3,018 by one author and
1,538 with a Claude co-author trailer, and 1,315 of them are the dashboard's
own history, merged in on 2026-11-03. A Show HN on 2026-10-06 drew 3 points and
one comment, the author's. Three public read-only demos run in the EU, Asia
and the US, signed into with a published `demo` account. The same GitHub
organisation also publishes desktop tools (an SSH client with 151 stars, a
two-factor app, an archiver), all started in October 2026.

## Feature inventory

### What shipped, release by release

Dates are the GitHub release dates (read through the ungh.cc mirror of GitHub's
releases API); what each carried is from the release notes and, where those say
little, from the first tag that contains the feature's commits in a clone.

| Release | Date (2026) | What changed that matters for a comparison |
|---|---|---|
| v1.0.0-beta1 | 3 Oct | First tag. Already in it: Git and image deploys, functions, Compose, templates, Kopia backups, the MCP server with plan-then-apply, settings on trial (confirm-or-revert, from 7 Sep), signed release info (from 16 Sep), an API key limited to the capabilities it was given (a fix of 1 Oct) |
| v1.0.0-beta2 | 5 Oct | "Provision a project or a project env from Docker compose (file or directory)"; autoscaling improved; OBI route and call metrics |
| v1.0.0-beta3 | 7 Oct | More templates; the store sorted by popularity, trending and new |
| v1.0.0-beta4 | 10 Oct | End-to-end tests; bug fixes |
| v1.0.0-beta5 | 21 Oct | "Managed apps: Postgres, mariadb" (WAL-G archiving, point-in-time restore, replicas, promotion); plan limits and licence keys; Trivy image scanning; two security fixes with no advisory (see "Security record") |
| v1.0.0-beta6 | 5 Nov | The notes list six Dependabot pull requests. Of the 1,374 commits since beta5, 59 are new and 1,315 are the dashboard's history merged into the repository; the new ones include `go-jose` 4.1.6 for CVE-2026-116587, an installer reset, and Kopia rebuilt |

The release process (`docs/RELEASING.md`) is careful where the notes are not:
`release.json` names every image the release runs by tag and digest, is signed
offline with an Ed25519 and an ML-DSA-65 key, and the app and the installer
refuse it without both signatures. A release is tested by an installer run in
`docker:dind`, by an "Installer on distributions" workflow that boots cloud
images in QEMU, and by a Playwright suite whose level 2 puts plan limits in
force.

### Deploy sources and builds

* **Sources:** a Docker image (with registry credentials, Amazon ECR tokens
  renewed by a job); a Git repository through a **GitHub App** made by the
  manifest flow, an access token (GitHub, GitLab, Gitea, Bitbucket, Gogs) or an
  SSH deploy key; a Compose file; a template; a function's code.
* **Builds:** the repository's Dockerfile, one written in the dashboard, or one
  **generated** for Go, Ruby, Python, PHP, Elixir, Java, Rust, Next.js, Bun,
  Deno, Node.js, .NET, Dart, C/C++, Zig, Scala, Astro, Nuxt, R and static sites,
  with the version read from `.nvmrc`, `go.mod` and the like. Build variables
  that hold a secret reach the build as BuildKit secrets, and a Dockerfile that
  declares one with `ARG` is refused. Builds run on the node that the image
  stays on, or push to a registry; **Settings → Image Build** chooses build
  nodes, concurrency, CPU and memory, and the cache.
* **Deploys:** pre- and post-deployment commands; triggers `user`,
  `repo-webhook` and `api`; cancel. On a push, every app that builds that
  repository and branch deploys, "There is no switch per app".
* **Rollback:** "There is no rollback button: going back is deploying the
  earlier version again", by setting the earlier tag or commit hash
  (`deploying-apps/deployments.md`).
* **Functions:** a handler in Node.js 24 (JavaScript or TypeScript), Bun 1,
  Python 3.13, PHP 8.5 or Go 1.27, written in the dashboard's editor or built
  from a repository directory, with a manifest's libraries installed at build.
  A **test run** tries the unsaved code in a throwaway container with the
  function's variables and secrets. Per-call timeout, concurrency and body size;
  autoscaling on calls; scheduled calls; call metrics from a log line per call.
  No scale to zero.
* **Docker Compose:** pasted, opened from a file or a whole folder read in the
  browser, into a new project or an existing environment. Variables whose names
  read as secrets become encrypted secrets; required `${VAR:?}` must be filled;
  each published port becomes a domain, a node port or nothing by a rule (web
  ports to domains, database ports unpublished); Traefik labels become domains;
  one-off services with `service_completed_successfully` run on each deploy.
  Left out: `build` (the image is deployed, or the service is not created),
  `depends_on` health ordering, and what Swarm cannot do (`privileged`,
  `devices`, `network_mode`).

### Domains, TLS and routing

* Traefik v3 routes by host. Certificates from Let's Encrypt, ZeroSSL, Google
  Trust Services (with EAB) or self-signed, EC P-256 by default or RSA, renewed
  from 30 days before expiry; HivePaaS waits 6 hours after a failed issuance so a
  redeploy loop cannot spend the rate limit. **DNS-01** through 14 providers
  (Cloudflare, Route 53, Google Cloud DNS, Azure, DigitalOcean, Hetzner,
  GoDaddy, Namecheap and others) and RFC 2136, which gives **wildcard
  certificates** and certificates before DNS points anywhere. Own certificates.
* Per domain: force HTTPS, **Redirect To**, TLS passthrough, and stacked
  configurations: basic auth, client limits with allowed IPs, rate limit
  (average, burst, in-flight), **headers**, **compression**, **path rewrite**
  (prefix, replace, regex), **circuit breaker**, WebSocket, load balancing; each
  can be applied to one path only.
* **TCP domains** with TLS and SNI, so several databases share one port; UDP by
  published port only. A project's **Allowed Domains** (such as
  `*.shop.example.com`) refuse any other domain to its apps.
* No firewall beyond allowed IPs and rate limits, and no maintenance mode for
  apps was found in the docs (maintenance means draining a node).

### Databases and services

* Databases are **templates that create ordinary apps**: PostgreSQL (and
  PostGIS, pgvector, TimescaleDB), MySQL, MariaDB, MongoDB, Redis, Valkey,
  ClickHouse, Kafka, RabbitMQ. A database shares `HIVEPAAS_HOST`, `_PORT`,
  `_DATABASE_NAME`, `_USER`, `_PASSWORD` and `_SSL_MODE`, which other apps
  reference as `${db.HIVEPAAS_PASSWORD}`; **Link to another app** writes a
  `DATABASE_URL`, and **Suggest Env** writes what an official image reads.
* **Managed PostgreSQL and MariaDB** (beta5, templates `postgres-managed`, 927
  lines, and `mariadb-managed`, 1,464): continuous WAL archiving with WAL-G,
  encrypted, to S3 or a volume, every 60 s at most; a nightly base backup kept
  7 times; an archive check every five minutes and a **weekly restore test**;
  restore into a new app at the latest moment, a time, a named restore point or
  a base backup (`hivepaas db` names restore points); streaming replicas and
  manual promotion with a stable address. "Managed databases are free on beta"
  (commit `94ff4d9`, 2026-10-21): on the stable channel they need a key, which
  `RELEASING.md` lists among the "Known gaps before 1.0.0" to decide.

### Storage and backups

* Volumes are bind directories, NFS shares, tmpfs or a Docker volume plugin,
  pinned to a node, a node label or "all nodes (shared storage)". An app gets a
  directory of its own; another app can mount it read-only or writable; a
  **Reset permissions** action changes owners.
* **Backups with Kopia:** encrypted with the repository's password,
  deduplicated, compressed, to S3, R2, B2, MinIO or a volume. A backup is either
  a command's output (`pg_dump …`, with its restore command) or a volume or a
  path in it. Retention keeps the last N and the last of each of N hours, days,
  weeks and months. A snapshot's **files can be browsed and downloaded**.
* **Restore** into the snapshot's own app or **another app**, such as a staging
  copy; a volume is restored by **Replace**, which moves the current directory
  aside as `….before-restore-…`, or **Overwrite**.
* **System backup** (off until set up): a `pg_dump` of HivePaaS's database and
  the configuration spec of the whole installation, its secrets omitted, in
  plain text, or encrypted with `age`. The app secret is in `hivepaas.toml`
  and must be kept apart.
* **Clone** an app into another environment with its settings, secrets,
  scheduled jobs and volumes, optionally their data, with post-clone command
  pipes (`pg_dump` of one into `pg_restore` of the other).

### Scaling and high availability

* Swarm modes: replicated, global, replicated job, global job; changing the mode
  recreates the service and stops the app meanwhile.
* **Autoscale** written by HivePaaS: every 15 s, on requests per instance
  (counted from Traefik's access log, so through domains only), on CPU, or
  both; out at once for a burst, in after a delay; paused when the logs it reads
  are not stored; a warning for an app that writes to a volume. No scale to
  zero ("one instance at least is up").
* **Placement:** constraints and preferences by node role, name or label;
  installation-wide rules that keep apps off managers and build nodes by default
  (from the second node on), require or exclude labels, and are refused when no
  node satisfies them. Nodes can be **Active, Paused or Drained**. GPUs (NVIDIA
  and AMD) as Swarm generic resources.
* Managers: 1, 3 or 5, promoted and demoted from the dashboard in an order that
  keeps the swarm up. The control plane itself runs on a manager with
  PostgreSQL on a local volume.

### Observability (logs, metrics, alerts, uptime)

* Live logs always; **stored logs** in VictoriaLogs once switched on, with
  search, a time range, level and stream filters, retention by days and disk
  use, and forwarding over HTTP. Secret values are filtered out of the logs the
  dashboard shows.
* **Metrics** from Traefik's access log (JSON, without the query string, client
  IP kept): requests by class, unreachable, p50/p95/p99, the 20 busiest **paths
  with numbers and ids collapsed** (`/users/:n`), requests per replica. CPU,
  memory, network and OOM kills per container from cgroups every 15 s.
* **Routes and calls** through OBI (OpenTelemetry eBPF Instrumentation) on
  chosen nodes: per-route counts and latency as the framework names the route,
  and each app's outgoing calls by peer (another app, `postgresql/shop`, an
  outside host), with no code change. Costs about 100–215 MiB per node and
  about 14 µs of CPU per request.
* **Health checks from outside**: REST or gRPC against a URL, status and body
  matching, a notification with a minimum interval between repeats. Container
  health checks too.
* **Home → Needs attention** and `hivepaas status`.

### Security, auth, roles, SSO, audit

* Passwords with a per-account backoff (10 failures, then 4 minutes, doubling);
  TOTP, required per user by an admin; **Trust this device** for 48 hours. No
  passkeys found.
* Sign-in with GitHub, GitHub App, GitLab and Gitea on every plan; Google,
  Microsoft and OpenID Connect on paid plans. Accounts are matched by an email
  **the provider verified** (since beta5); **Enforce SSO** per user turns their
  password off.
* **Roles:** admin, or member with **Read, Write, Execute, Delete** per module
  (projects, settings, cluster, users, system), **per project and per
  environment**, two capabilities given apart (**Reveal Secrets**, **Create API
  keys**), and **Access Expiration** at a date.
* **Secrets** are masked by default; revealing one takes a server switch
  (**Return Secrets via API**, off by default), the capability, and leaves an
  audit entry.
* **API keys:** per user, at most a year, with module permissions and an
  optional reveal capability; only a hash is kept.
* **Settings on trial:** a change to the dashboard's routing, to the proxy
  settings in front of it, or to Traefik's startup command is applied, counted
  down for 3 minutes, and undone unless confirmed **through the new
  configuration**; confirming is refused for the first 30 seconds (two minutes
  when HivePaaS restarts itself), the countdown lives on the server, and a
  change Swarm already rolled back cannot be confirmed. "There is deliberately
  no way to ask for no trial at all" (`docs/recovery.md`).
* **Privileged apps** (Docker socket, host directories) need an operator switch,
  off by default; apps that drive Docker get a filtering **Docker API proxy**.
  A **web terminal** into containers with file upload and download.
* Audit logs per installation, project and environment, kept 90 days by
  default.

### Preview environments and branches

* A preview is an app `pr-42` under the app, at `pr-42-<domain>`, with one
  replica and **empty storage of its own**; secrets only if marked
  **Inheritable**, and the dashboard names the ones it goes without.
* Created by a comment, **`/hivepaas deploy`** (with `subdomain=`, `clonedb`,
  `nowait`, `nostart`), or from the dashboard; removed by `/hivepaas cancel` or
  when the pull request closes. **Only someone who can write to the repository
  can run a command**, checked per provider; a push by anybody else is not
  deployed again until a writer comments.
* **DB Apps to Clone**, with or without data, and commands run in the parent app
  before the preview starts, with `HIVEPAAS_PREVIEW_*` variables.

### Templates and catalogue

* 393 templates in `index.json` at `73015a4` (2026-11-02): 348 in "webapps", 68
  in "databases"; 153 create dependency apps; 339 were added in September 2026
  and 54 in October. Moving tags such as `latest` or `17` are refused; each
  version names an exact image. An installation reads the catalogue only
  through the signed release info, which pins its commit and the SHA-256 of
  `index.json`, which pins every template file. Templates declare whether they
  need elevated privileges or the Docker API.

### CLI, API, IaC, integrations

* **REST API** with an OpenAPI description and an **API level**: a CLI built for
  an older level gets 426 `ERR_CLI_OUTDATED` on writes, because it would erase
  fields it does not know.
* **CLI** (`hivepaas-cli`, v1.0.0-beta2): contexts, the secret in the OS
  keychain, `login` reading the secret from stdin, `link` writing
  `.hivepaas.json`, `deploy --image|--commit` that waits up to 30 minutes and
  fails with the deployment, `logs --search --level --since`, `env`, `secret
  set` prompting for the value, `job run`, `exec`, `cp`, `app scan --fail-on`
  (exit code 11), `db`, `preview`, `template`, `compose`; documented exit codes;
  a self-update that accepts only archives in a signed list.
* **MCP server** at `/api/mcp` over streamable HTTP, switched on in **System →
  AI**, with an **Allow changes** switch. Every change is **planned first**:
  the plan tool answers what would happen and a token; the plan is sealed,
  kept 10 minutes, used once, only by the key that made it, and refused if what
  it checked has moved. **Nothing is ever deleted** through it. Prompts
  (`debug_app`, `install_app`, `deploy_image`, `deploy_function`,
  `connect_app_to_database`, `expose_database`) and guides as resources. A
  **Recent calls** list. OAuth is "not offered yet".
* **Export and import** of the installation, projects, environments or apps as
  a configuration spec, secrets excluded, `age`-encrypted or plain; an import
  shows **what it would do** first, then leaves what exists or makes it match.
* No secret-manager integration and no cloud server provisioning were found.

### Notifications

* Email (SMTP, or an HTTP sending API with field mappings), Slack, Discord,
  Telegram and Lark. **Notification targets** combine channels and recipients
  (project members, owners, admins, addresses). Deployments, health checks,
  scheduled jobs and data backups, clones, certificates and HivePaaS's own jobs
  each choose a target for success and one for failure; **Min Send Interval**
  holds back repeats. The pricing page also lists "webhook"; the channels page
  does not.

### Multi-server and networking

* Nodes join with the `docker swarm join` command HivePaaS shows; the agent
  starts on its own. Ports 2377, 7946 and 4789 between nodes.
* Traffic between nodes rides Docker overlay networks. The installer creates
  `hivepaas_net`, which carries every request from Traefik to an app, without
  Docker's `encrypted` option (`deployment/release/install.sh`, the
  `docker network create --driver overlay` at line 1740), so it crosses the
  wire unencrypted; the per-environment networks were not checked.
* Updates from the dashboard with an **update plan** per component and a
  `blockMajorUpgrade` list (PostgreSQL's major is moved only by the installer);
  release channels stable and beta.

### Team and collaboration

* Projects with up to ten environments, settings at four scopes (global,
  project, environment, app) marked "Available in Projects" or "in Apps", env
  vars inherited down the scopes, command templates and command pipes between
  apps. Users belong to the installation; Bee allows two, invitations included.

### Developer experience and onboarding

* `curl -fsSL https://get.hivepaas.com -o install.sh && sudo bash install.sh`
  asks for the admin, the dashboard's domain, the root domain, data
  directories, the timezone and the app secret, then takes nine steps: it
  installs or upgrades Docker to 29 (restarting running containers), turns off
  Docker's SELinux labelling where it is on, adds a 2 GB swap file, sets
  swappiness to 10, installs **earlyoom**, makes a swarm and deploys. Ten
  distribution families; 4 CPUs, 8 GB and 100 GB recommended, "HivePaaS runs on
  less". Silent install from a settings file; `--redeploy`; an earlier
  database can be kept.
* Under **sudo-rs before 0.2.14** — Ubuntu 25.10 and 26.04 — `curl … | sudo
  bash` stopped forever after the password (sudo-rs #1527); since beta5 the
  installer detects it and refuses with the two-line form.
* Docs: 105 pages on Docusaurus; "Upgrade" and "Diagnostics" say "This page is
  being written". The dashboard is in English and Vietnamese.

## What users love

Not found. No GitHub issue has been opened, the Show HN's only comment is the
author's, Reddit refused the request, and the one web search allowed was
refused for lack of credits. What exists is attention, not opinion:

* 334 stars and 41 forks in the month after the Show HN of 2026-10-06.
* A link in a weekly newsletter (`mfahlandt/lwcn`, week 42 of 2026).
* A developer of another project compared his own against "HivePaaS's feature
  list on 2026-10-06 by reading the code", calling it "the nearest current shape
  of what VISION.md calls 'FJS's Coolify'" (`frontierJS/frontierjs`,
  `IDEAS/paas-parity.md`).

## What users complain about

Not found, for the same reason. What HivePaaS's own documents say is unfinished
or awkward, which is what users would meet first:

* **No rollback:** going back means typing the earlier tag or commit.
* **Autoscaling needs stored logs**, cannot reach zero, takes 20 s to a minute
  for a new instance, and does not see a long request or a WebSocket until it
  ends.
* **Compose loses** `build`, health-ordered `depends_on`, and anything Swarm has
  no field for; one project volume on the node HivePaaS runs on until each app
  is given a cluster volume.
* **MySQL and MariaDB cannot be exposed** by domain; their port is published
  on every node.
* **The upgrade and diagnostics pages are unwritten**, and release notes are
  thin: beta6's list only Dependabot pull requests, and beta5's two security
  fixes appear as "Bug fixes".
* **The documents disagree with each other.** The pricing page says "Every plan
  has every feature … Nothing else" apart from limits and sign-in providers,
  while managed databases need a key on the stable channel (`RELEASING.md`,
  "Known gaps before 1.0.0"); the pricing page lists webhook notifications,
  the channels page does not.
* **Weight:** PostgreSQL, Redis, the app, worker, updater and an agent per node,
  plus VictoriaLogs and OBI when switched on; 8 GB recommended.

## Security record

**Where I looked (2026-11-08):** NVD (`keywordSearch=hivepaas`: none), OSV
(`github.com/hivepaas/hivepaas`, Go: none), the repository's advisory page
(HTTP 403 from here; the GitHub advisory API is not open to this session),
`SECURITY.md`, and every commit whose message names a security class.

**No CVE and no published advisory were found.** `SECURITY.md` asks for private
reports through GitHub, supports the latest beta only, and promises an advisory
once a fix is released. The commit history shows fixes that got none:

| Commit | Date | What it fixed | First release with the fix |
|---|---|---|---|
| `4cdd5bb` | 2026-10-01 | An API key carried its owner's capabilities whole, so "even a read-only key of an admin read every password and private key in the clear"; renewing a key's session dropped the key's limits | beta1 (never released vulnerable) |
| `f1831c5` | 2026-10-16 | A Dockerfile path with `..`, or a repository holding a link named `Dockerfile`, wrote the Dockerfile content anywhere the build could, and "The build runs as root, beside the Docker socket" | beta5; beta1–beta4 affected |
| `26143d2` | 2026-10-17 | SSO matched an account by whatever email the provider reported, verified or not, including Microsoft's tenant-set `mail` ("nOAuth") | beta5; beta1–beta4 affected |
| `dedb305` | 2026-11-03 | `go-jose` 4.1.6 for CVE-2026-116587 (a denial of service through lego) | beta6 |

The second and third are the kind that would carry an advisory elsewhere; the
beta5 notes say "Bug fixes". The fixes themselves are careful (`os.Root` for the
build's files; each provider's verified-email rule written out).

**Exposure by design:** an agent with root on every node through the Docker
socket; a web terminal and file transfer into containers; secrets that can be
revealed with a capability and a switch; privileged apps behind an operator
switch; Traefik's access log keeps client addresses; the overlay network that
carries app traffic between nodes is created without encryption. Against that:
releases signed offline with two algorithms so "a compromised CI can build
images but cannot make an installation run them", digest-pinned images and
templates, Trivy blocking a release with a fixable HIGH or CRITICAL, the
verified-email rule, MCP that plans before it changes and never deletes, and
settings on trial.

## Against Skifity

Skifity's statuses: much of it runs on a real single-server and two-server k3s
on every push (`docs/progress.md`, "A real cluster, at last"); production traffic
never has. HivePaaS is a beta of six weeks on Swarm. Line numbers are from
Skifity's working tree at `29147f1`.

| Capability | HivePaaS | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Sources | Image, Git (GitHub App, tokens, SSH keys), Compose, template, function code | Image, Git through tokens for GitHub, GitLab, Gitea, Bitbucket (`internal/gitsrc`), folder upload (`internal/upload`), Compose stack (`internal/api/stack_handlers.go`), template; the GitHub App was removed unfinished (`internal/api/integrations_handlers.go:77-81`) | parity; behind on a GitHub App |
| Builds | Own or generated Dockerfile for 20 languages, BuildKit on chosen build nodes, root beside the Docker socket | Railpack, Dockerfile or static in rootless BuildKit Jobs (`internal/builder/job.go:32-36`); no build-node pinning | ahead on isolation |
| Deploy an image from CI | `hivepaas deploy --image`, waits, exit codes, a 500-line CI page | No image on the deploy body (`internal/api/deploy_handlers.go:19-26`), no CI section in `docs/cli.md` | **behind** (open since `kamal.md`) |
| Rollback | None: deploy the earlier tag or commit | A new deployment with the old image and its settings, shown first (Phase 94) | ahead |
| Functions | Five runtimes, an editor, test runs, autoscale on calls | None in `internal/` | behind, see "not copy" |
| **Domains, TLS and routing** | | | |
| Certificates | Three CAs, EC or RSA, DNS-01 with 14 providers and RFC 2136, wildcards | HTTP-01 through cert-manager, any ACME directory (`internal/settings/settings.go:396-412`), own certificates (`internal/tlscert`); "Wildcard certificates … That is not built" (`docs/concepts.md:423-426`) | **behind** |
| Request shaping | Headers, compression, path rewrite, circuit breaker, rate limit, IPs, per path | Middlewares are redirects (`internal/kube/hostredirect.go`), forward auth to the guard (`internal/kube/namespace.go:464`) and basic auth (`internal/kube/password.go:69`) | behind on HTTP shaping |
| Firewall | Allowed IPs and rate limits per domain or path | Rules by address, country, ASN, path, method and headers, rate limits, maintenance mode (`internal/edgerules`, `internal/geoip`, `internal/guard/maintenance.go`) | **ahead** |
| Domains a project may use | **Allowed Domains** patterns | None; a hostname is unique per panel, first come (`internal/store/migrations/0001_init.sql:202`) | behind |
| Databases from outside | TCP domains by SNI; MySQL by published port | `skifity db connect` tunnel (`internal/cli/dbconnect.go`), public ports | different |
| **Databases and services** | | | |
| Engines | Templates as ordinary apps; managed PostgreSQL and MariaDB (a key on stable) | Nine managed engines (`docs/databases.md:10-20`, `internal/dbsvc`) | ahead |
| High availability | Replicas with manual promotion | CloudNativePG with automatic failover, odd instance counts (`internal/dbsvc/lifecycle.go`) | ahead |
| Point-in-time recovery | WAL-G every 60 s, restore to a time or restore point into a new app, a weekly restore test | Dumps only (`internal/backup/jobs.go:368`); **Verify** reads the archive "except whether the database accepts the dump" (`docs/backups.md:77-78`) | **behind** |
| **Storage and backups** | | | |
| Backup format | Kopia: encrypted, deduplicated, compressed; files browsable | Dumps and volume archives sealed with a passphrase and verifiable (Phase 104, `internal/sealed`) | parity on encryption; behind on dedup |
| Retention | Last N, and hourly, daily, weekly, monthly | "Keep the last" (`docs/backups.md:92`) | behind (small) |
| Restore target | The app or another app; Replace keeps the old directory | "restored only into the database it was taken from" (`docs/backups.md:171`) | **behind** |
| Control-plane backup | Database and encrypted spec, off by default | Scheduled panel backups (Phase 84), `restore-db`, a copy before each upgrade (`internal/api/upgrade.go`) | ahead |
| Clone | An app, with volume data if asked | An environment, without data, by decision (`internal/api/envclone_handlers.go:14-28`) | different |
| **Scaling and HA** | | | |
| Autoscaling | Own loop on requests and CPU every 15 s; needs stored logs; no zero | HPA on CPU and memory (`internal/kube/manifests.go:464-467`), scale to zero through KEDA (`internal/kube/scaletozero.go`), readiness checker (`internal/deploy/scaling.go`) | ahead on zero; behind on a request target |
| Placement and nodes | Labels, constraints, rules off managers and builders, Active/Pause/Drain | A server can only be renamed (`internal/api/servers_handlers.go:216-235`); cordon and drain only on removal (`internal/provision/remove.go:71-79`); no app `nodeSelector` outside GPUs (`internal/kube/gpu.go`) | **behind** |
| **Observability** | | | |
| Log history | VictoriaLogs, search, levels, time range, retention | What Kubernetes keeps, 10,000 lines and the previous container (`internal/api/stream_handlers.go:427-449`), a substring filter (`web/src/components/app/logs-tab.tsx:113-115`); eight drains (`internal/logdrain`) | behind on history; ahead on drains |
| Secrets in logs | Filtered from shown logs | Not masked; a viewer reads them (`internal/api/stream_handlers.go:411-412`) | **behind** (open since `hakopod.md`) |
| HTTP metrics | Classes, unreachable, percentiles, top paths with ids collapsed, per replica | Requests, status and latency per app (`internal/traffic`) | behind on paths |
| Routes and calls | eBPF per route and per peer | None | behind |
| Usage and alerts | Per-container CPU, memory, network, OOM kills; no thresholds found | History with thresholds, `app.alert`, `server.alert`, `backup.missed` (`internal/notify/notify.go:56-93`) | ahead |
| Uptime from outside | REST and gRPC checks with body match | `app.unhealthy` from readiness inside the cluster (`internal/watch/watch.go:400-418`) | behind |
| Scheduled jobs | Runs recorded as tasks, retries, success and failure targets, triggers on deploy and health, sequences, pipes | CronJobs (`internal/kube/runjob.go:221`); the list returns definitions only (`internal/api/deploy_handlers.go:611-623`); no job event (`internal/notify/notify.go:56-93`) | **behind** |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | TOTP, trusted device; OIDC, Google, Microsoft paid; no passkeys | Argon2id, TOTP, passkeys (`internal/auth/passkey.go`), OIDC with groups to roles (Phase 92), team-required 2FA (Phase 91), all free | **ahead** |
| Roles | Module × R/W/X/D, per project and environment, capabilities, expiry, SSO-only per user | Owner, admin, member, viewer; members limited to projects; no expiry (`internal/store/models.go:65-77`) | behind on environment scope and expiry |
| Secrets | Masked; revealable with a capability, a switch and an audit entry | Write-only on every surface, sealed to their context (`internal/crypto`) | ahead |
| Self-lockout | Routing, proxy and Traefik changes on trial, reverted unless confirmed | Few such settings exist (the panel's domain and proxy count are install-time); an upgrade that does not start leaves "no panel left to fix it" (`internal/api/upgrade.go:35`) | **behind** on upgrades |
| Release integrity | Every image by digest in a release file signed offline with Ed25519 and ML-DSA-65; installer and app verify; signed CLI updates | SLSA provenance on binaries (`.github/workflows/release.yml:92-112`); the panel image is tagged `latest` too (`.goreleaser.yaml:86-89`) and pulled by tag (`installer/install.sh:81`); the update check follows GitHub's `/releases/latest` redirect (`docs/configuration.md:727`) | **behind** |
| Vulnerability scanning | Trivy after deploys and daily, ignores with reason and expiry, SBOM, its own images; never blocks a deploy | Trivy on each deploy and nightly, a deploy gate (`internal/vulnscan/policy.go:20`) accepted per deploy (`internal/api/deploy_handlers.go:25`), framework advisories (Phase 97) | ahead on the gate; behind on ignores, SBOM, own images |
| Terminal and privileged apps | Web terminal; Docker socket and host mounts behind a switch; Docker API proxy | None, by decision (`docs/roadmap.md`); restricted Pod Security (`internal/kube/namespace.go`) | deliberate difference |
| **Preview environments** | | | |
| Previews | By a writer's comment or the dashboard; clone database apps with data or without; 2 at once on Bee | Automatic per pull request with statuses and comments, preview-only values, whole-environment previews with a seed (Phases 93, 107); a fork's pull request is built and run automatically, no secrets, three at once, with internet egress (`internal/api/webhook_handlers.go:482-487`, `internal/kube/namespace.go:222`) | ahead on automation; **behind on who may start a stranger's** |
| **Templates** | | | |
| Catalogue | 393, exact images, digest-pinned through signed release info, PolyForm Shield | 374, checked versions (`internal/templates/catalogue`), Apache-2.0 | parity on count; ahead on licence |
| **CLI, API, IaC, integrations** | | | |
| CLI | Contexts, keychain, deploy waits, logs search, exec, cp, scan gate with exit code, signed self-update | About 40 commands (`internal/cli/commands.go`), token in a 0600 file (`internal/cli/client.go:115-127`), CLI served by the panel itself (`internal/api/cli_handlers.go`) | mixed |
| MCP | HTTP; plan-then-apply; never deletes; prompts and guides; Allow changes; Recent calls | HTTP and stdio, 46 tools with read and destructive annotations (`internal/mcpserver/server.go:360-370`) | ahead on breadth; behind on plan-then-apply |
| Configuration as code | Export and import of any scope with a plan | `skifity.yaml` plan and apply (`internal/blueprint`), but nothing writes one; team export with no import (`internal/api/export_handlers.go:82`) | mixed |
| Secret managers, cloud servers | None found | Vault, AWS, Doppler, Infisical (`internal/secretmgr`); Hetzner and DigitalOcean (`internal/cloud`) | ahead |
| **Notifications** | | | |
| Channels and events | Five channels, targets per thing, repeats held back | Ten kinds (`internal/notify/notify.go:127-186`) and fourteen events | ahead, except job outcomes |
| **Multi-server and networking** | | | |
| Between servers | Overlay without `encrypted` | WireGuard flannel by default (`internal/settings/settings.go`) | ahead |
| Upgrades | An update plan for its stack; Docker by the installer | Panel, components and k3s from the panel (Phase 109) | ahead |
| **Team and collaboration** | | | |
| Teams and users | One installation's users, two on Bee | Teams, invitations, roles, project limits, all free (`internal/api`) | ahead |
| **Developer experience** | | | |
| Install | Ten distribution families tested in VMs; refuses piped sudo-rs; 8 GB recommended | Ubuntu and Debian (`installer/install.sh:1007-1012`), 2 GB; piped into `sudo sh` in the README | ahead on footprint; **behind on distributions and sudo-rs** |
| Languages | English, Vietnamese (`web/src/i18n/dictionaries`) | Five with a build gate (`web/src/locales`) | ahead |
| Demo | Three public read-only demos | None | behind |

### Claims that no longer hold

* `docs/research/competitors.md` gives "Scaling is the system's, not a runbook"
  as a difference, naming Coolify, Dokploy and Railway. HivePaaS has an
  autoscaler on requests and CPU and warns about apps that write to a volume.
  What is still Skifity's is scale to zero and a readiness checker that names
  the variable that breaks.
* "Secrets that stay secret" is narrower than it reads, as `hakopod.md` found:
  HivePaaS, which can reveal secrets, filters them out of the logs it shows, and
  Skifity, which never reveals them, does not.

## Gaps worth closing in Skifity

**P1**

1. **The one-line install on Ubuntu 26.04.** *What:* make the documented
   `curl … | sudo sh` either work or refuse at once under a sudo-rs older than
   0.2.14, with the two-line form (`-o install.sh`, then `sudo sh install.sh`)
   in the message and in the README; add Ubuntu 26.04 to what the installer is
   tested on. *Evidence:* HivePaaS's `ac37a08` (2026-10-21): with stdin a pipe,
   sudo-rs runs the script in a terminal of its own outside its foreground
   process group, and "the first command to touch that terminal … is stopped
   with its process group … so the installer waits forever" (sudo-rs #1527);
   Ubuntu 25.10 and 26.04 ship sudo-rs 0.2.13. Skifity's README and quick start
   give that exact form (`README.md:41`), and a default run asks for the domain
   by reading `/dev/tty` (`installer/install.sh:1339-1344`), which is what a
   background process group is stopped for. The installer says it is tested on
   Ubuntu 24.04 and Debian 12 (`:1007-1012`). Read from the code and HivePaaS's
   account, not run. *Fit:* compare the process group with the terminal's
   foreground group from `/proc/self/stat`, as HivePaaS does, before the first
   question; `test/smoke`'s installer checks for the message. *Size:* S.
   *Without a cluster:* the check yes; proving the hang and the fix needs an
   Ubuntu 26.04 VM, not k3s.
2. **Sign what a release runs, and refuse what is not signed.** *What:* a
   release file naming the panel image by digest and the version of every
   component the panel installs, signed on an offline machine with Ed25519;
   the installer, **Check for updates** and `upgradePanel` verify it and pull by
   digest, never by tag; later a second ML-DSA-65 signature, as HivePaaS has,
   once Skifity is on Go 1.27 (`crypto/mldsa` is not in Go 1.26.10, which
   `go.mod` names). *Evidence:* HivePaaS's `RELEASING.md`: "a compromised CI
   can build images but cannot make an installation run them". Skifity's panel
   image is pushed by the release workflow's own token under `latest` and the
   version (`.goreleaser.yaml:86-89`), the installer pulls
   `${IMAGE_REPO}:${VERSION}` (`installer/install.sh:81`), the update check
   trusts GitHub's `/releases/latest` redirect (`docs/configuration.md:727`),
   and `upgradePanel` sets whatever tag it is given (`internal/api/upgrade.go:37`).
   `kamal.md` noted the unsigned image on 2026-11-08. *Fit:* the detached
   Ed25519 verification already written for the plugin store
   (`internal/pluginstore/store.go:5-6`), a key in `internal/version`,
   `openssl pkeyutl -verify -rawin` in the POSIX installer, a signing step in
   `docs/releasing.md`. *Size:* M. *Without a cluster:* verification, refusal and
   the installer's test yes; the swap is what `test/cluster/upgrade.sh` already
   runs.
3. **An upgrade that puts itself back.** *What:* before the panel changes its
   own image it starts a one-shot Job, on the old image, that waits a set time
   (five minutes) for the new panel to be ready; if it is not, the Job scales
   the panel to zero, restores the database copy the upgrade took, rolls the
   image back and starts it again, and records why; a **Keep this version**
   button in the new panel ends the wait early. The same mechanism carries
   `hakopod.md` gap 3's panel-wide allowlist, which should be on trial in the
   same way, and a save of the token allowlist should say when it leaves out
   the address in use. *Evidence:* HivePaaS restores its own services through
   Swarm's `failure_action: rollback` and reverts routing and proxy changes that
   nobody confirms "through the new configuration" (`docs/recovery.md`);
   "there is deliberately no way to ask for no trial at all". Skifity's panel
   uses the Recreate strategy, so "If the new image does not come up there is
   no panel left to fix it" (`internal/api/upgrade.go:30-36`), and going back is
   four `kubectl` and `restore-db` commands typed on the server
   (`docs/configuration.md`, "Upgrading"). *Fit:* `internal/api/upgrade.go`
   already takes the copy and knows the previous image; the Job runs the
   existing `admin restore-db`. *Size:* M. *Without a cluster:* the Job's
   rendering and its decisions yes; a broken image being put back needs the
   Cluster workflow, which can push one on purpose.
4. **A stranger's pull request waits for somebody who can write.** *What:* a
   pull request whose author cannot write to the repository gets no build and
   no preview until a person with write access approves it, by a comment
   (`/skifity preview`) or a button on the app's previews list; each later push
   by that author needs approval again; the pull request is told so in the
   comment Skifity already posts. *Evidence:* HivePaaS runs a preview only on a
   writer's command, checks who wrote it per provider (GitHub's webhook says;
   GitLab and Gitea are asked with the app's credentials), and "A pull request
   from anybody else is not deployed again". Skifity builds and runs a fork's
   pull request on the event, without secrets, up to three per project
   (`internal/api/webhook_handlers.go:482-487`); every environment, a fork's
   included, is allowed out to the internet (`internal/kube/namespace.go:222`),
   so anybody who can open a pull request on a public repository runs a build
   and a container of their choosing on the team's servers. *Fit:* GitHub's
   `author_association` in the pull-request payload, `internal/gitsrc` for the
   other providers, a pending state beside `FromFork` on the environment, the
   existing pull-request comment (`internal/deploy/gitreport.go`). *Size:* M.
   *Without a cluster:* yes, against recorded webhooks and an `httptest`
   provider.

**P2**

5. **Say when a scheduled command fails.** *What:* record each run of an app's
   scheduled commands from its Kubernetes Job (start, end, exit, the last lines),
   show the last runs beside each schedule, return the last result in the API,
   CLI and MCP, and send a `job.failed` event on the first failure, at most once
   a day while it keeps failing, and once on recovery. *Evidence:* HivePaaS
   records every run as a task with logs and retries and gives each job a target
   for success and for failure. `coolify.md` gap 5 asked for three alerts; the
   missed backup and the disk are done, the failing command is not. Skifity's
   jobs list returns only what was saved (`internal/api/deploy_handlers.go:611-623`)
   and `internal/notify/notify.go:56-93` has no job event, so a nightly report
   that has failed for a month says nothing. *Fit:* `internal/watch` reads Jobs
   by their labels, a `job_runs` table pruned like deployments, the Console
   tab. *Size:* S–M. *Without a cluster:* yes with a fake Job lister; real
   CronJobs need one.
6. **Point-in-time recovery for PostgreSQL, and a restore that is tested.**
   *What:* switch on CloudNativePG's continuous WAL archiving to the team's
   backup bucket; restore into a **new** database at a chosen time; and, on a
   schedule, restore the latest backup into a throwaway database, run a check
   query, record the result beside **Verify**, and raise `backup.failed` when it
   fails. Restoring a dump into a new database, rather than only over the one it
   came from, comes with it for every engine. *Evidence:* HivePaaS's managed
   PostgreSQL archives at most 60 s behind, takes a nightly base backup, checks
   the archive every five minutes and restores weekly to prove it
   (`templates/postgres-managed.yaml`). Skifity backs PostgreSQL up with
   `pg_dump` (`internal/backup/jobs.go:368`), its Verify proves "everything a
   restore depends on except whether the database accepts the dump"
   (`docs/backups.md:77-78`), and a backup is "restored only into the database
   it was taken from" (`docs/backups.md:171`). CloudNativePG, which Skifity
   already runs (`internal/dbsvc/lifecycle.go`), does the archiving and the
   recovery itself. *Fit:* `internal/dbsvc` manifests, `internal/backup`'s
   bucket settings, the database's Backups tab. *Size:* L. *Without a cluster:*
   the manifests and the state yes; archiving and recovery need one.
7. **Set a vulnerability aside, with a reason and an end; an SBOM; the panel's
   own images.** *What:* ignore one finding for an app, an environment, a
   project or the team, for one package or any, with a required reason and an
   expiry of 30 days to a year; ignored findings leave the counts, the badge,
   `app.vulnerable` and the deploy gate. A CycloneDX download per image.
   Scanning of the panel's and the components' images in a tab of their own,
   fixed by upgrading. `skifity scan --fail-on critical` with an exit code for
   CI. *Evidence:* HivePaaS has each of these. Skifity's gate refuses a deploy on
   a fixable critical (`internal/vulnscan/policy.go:16-20`), and the only way
   past one that does not apply is `accept_vulnerabilities` on every deploy
   (`internal/api/deploy_handlers.go:21-25`); no SBOM is produced anywhere in
   `internal/`, and the scanner reads apps' images only. *Fit:* a
   `scan_ignores` table read by `Blocking`, Trivy's CycloneDX output from the
   same Job, the Security tab, `internal/cli/scan.go`. *Size:* M. *Without a
   cluster:* the rules and the UI yes; scans need one.
8. **Write `skifity.yaml` from what runs, and import a team export.** *What:*
   `skifity export --yaml -p shop -e production` writes the environment as a
   `skifity.yaml` that `skifity plan` on the same environment reports as no
   change; a panel form that reads a team export and shows what it would create,
   as `skifity plan` does, before anything is made. *Evidence:* HivePaaS exports
   any scope and imports with "What the import would do" first. Skifity reads
   `skifity.yaml` and never writes one (`internal/cli/blueprint.go`), and its team
   export (`internal/api/export_handlers.go:82`) has no import, so the file is a
   way out and not a way to another panel. *Fit:* `internal/blueprint` already
   computes the differences both ways. *Size:* M. *Without a cluster:* yes.
9. **Servers that can be paused, labelled and chosen.** *What:* pause a server
   (no new instances), drain it for maintenance and bring it back, without
   removing it; labels on servers; an app pinned to servers by name or label;
   panel-wide rules that keep apps off the control-plane servers once there is
   more than one server, refused when no server satisfies them. *Evidence:*
   HivePaaS has Active, Pause and Drain, labels, constraints, and placement
   rules that are on by default from the second node. A Skifity server can only
   be renamed (`internal/api/servers_handlers.go:216-235`); cordon and drain
   exist only inside removal (`internal/provision/remove.go:71-79`); no app gets
   a node selector outside GPUs (`internal/kube/gpu.go`). `hakopod.md` gap 12
   (pin) and the Kamal page's architecture gap are parts of this. *Fit:*
   `moveSoleInstances` (`internal/provision/move.go`) already moves an app's
   only instance first; a selector in `internal/kube/manifests.go`; the
   server page. *Size:* M. *Without a cluster:* manifests and refusals yes; a
   drain and back needs one.
10. **Domains a team may use.** *What:* a panel administrator gives a team the
    patterns its hostnames must match (`*.client-a.example.com`); a domain or a
    certificate outside them is refused. *Evidence:* HivePaaS's project
    **Allowed Domains**. In Skifity a hostname belongs to whichever team asks
    first (`internal/store/migrations/0001_init.sql:202`), and `hakopod.md`
    gap 7's TXT proof is the stronger check for panels that host strangers;
    this is the cheap one for panels that host clients. *Fit:* a team setting,
    `handleAddDomain` in `internal/api/apps_handlers.go`, the certificate
    upload. *Size:* S. *Without a cluster:* yes.
11. **Plan, then apply, for an assistant's changes.** *What:* every changing MCP
    tool answers a plan and a token first; a second call applies exactly that
    plan, once, within ten minutes, by the token that planned it, and is refused
    if the app has moved since; a panel switch that lets assistants read only;
    a list of the assistants' recent calls. *Evidence:* HivePaaS seals each plan
    with a random key, binds it to the user and key, and compares a stored check
    before applying (`hivepaas_app/interface/mcp/plans.go`); "Nothing is ever
    deleted through the MCP server". Skifity's 46 tools mark themselves read-only
    or destructive (`internal/mcpserver/server.go:360-370`), which a client may
    honour or not, and include `delete_variable`, `remove_domain`,
    `close_port`, `stop_database` and `rollback_app`. *Fit:* the rollback and
    `skifity.yaml` plans already exist (`internal/blueprint`, Phase 94); the
    plan store can live in memory, like the device codes `hakopod.md` gap 4
    proposes. *Size:* M. *Without a cluster:* yes.

**P3**

12. **A public demo.** A panel anybody can look round without signing up,
    seeded with apps that hold nothing real. Not a published password: Skifity
    locks an account after five failures in 15 minutes
    (`internal/auth/service.go:59-60`), so anybody could lock a shared demo
    account; a **Look around** button that issues a viewer session
    (`RoleViewer`, `internal/store/models.go`) does not, and the route walk
    already proves a viewer changes nothing. Logs must be masked first (the
    open `hakopod.md` gap 2). HivePaaS runs three. S–M; hosting needs a server.
13. **Access that ends on a date.** An expiry on a membership and an
    invitation, for a contractor; HivePaaS has **Access Expiration**.
    `Membership` has none (`internal/store/models.go:65-77`). S, no cluster.
14. **Keep backups by day, week and month.** Beside "Keep the last"
    (`docs/backups.md:92`), the last of each of N days, weeks and months, which
    is how a month-old mistake is still recoverable without keeping thirty
    copies. S, no cluster.
15. **Headers, compression and path rewriting per domain.** Traefik's
    `headers`, `compress` and `replacePathRegex` middlewares, as HivePaaS offers
    them per domain and per path; Skifity renders only redirects, forward auth
    and basic auth. M; manifests without a cluster.
16. **Check an app from outside.** A request to an app's public address on the
    panel's tick, with an expected status and text, and `app.unhealthy` when it
    fails while the instances say they are ready (DNS gone, a certificate
    refused, the guard answering 503). HivePaaS's health checks do this;
    Skifity reads readiness only (`internal/watch/watch.go:400-418`). S–M; the
    logic without a cluster.
17. **Say before a deploy that the environment has no room.** A rolling update
    starts one instance more than runs, and the environment's ResourceQuota
    counts it; at the ceiling the rollout waits until it fails with
    `replicas_quota` (`internal/kube/explain.go:100`). The usage is already read
    (`QuotaUsage`, `internal/api/ports.go:228`) and only shown. Saying so before
    the deploy or the scale-up keeps HivePaaS's rule that a limit stops creating
    and says so at once. S, no cluster for the arithmetic.

**Already proposed elsewhere and still open, with HivePaaS's version as a
design note:** an image deploy from CI (`kamal.md` P1; HivePaaS's `deploy
--image` waits up to 30 minutes and its CLI has documented exit codes); secrets
masked in logs (`hakopod.md` gap 2); stored logs with search (`hakopod.md`
gap 6; HivePaaS makes them an opt-in system app with retention by days and disk
use, and reads its autoscaling from them); traffic by path (`coolify.md` new
gap 3; HivePaaS drops the query string where the log is written and collapses
numbers and ids into one path); DNS-01 and wildcard certificates (`coolify.md`
gap 13); access per environment (`coolify.md` new gap 1); a TXT proof for a
domain (`hakopod.md` gap 7); and the CLI's token kept in the OS keychain rather
than `config.json` (`internal/cli/client.go:115-127`), which belongs with
`hakopod.md` gap 4's browser sign-in.

## Things to deliberately not copy

* **Limits on users and nodes, and sign-in providers behind a key.** Bee allows
  two users and two nodes, and Google, Microsoft and OpenID Connect sign-in need
  a paid plan, in an Apache-2.0 codebase. Skifity's list already says not to put
  SSO, audit or roles behind a licence. What is worth keeping from HivePaaS's
  scheme is its rule, which matches Skifity's "a quota that pauses a running
  site" refusal: a limit stops creating, never what runs.
* **A catalogue under a different licence from the product.** The templates are
  PolyForm Shield, so a fork or a competitor cannot reuse them. Skifity's 374 are
  in the repository under its own licence; keep it that way.
* **Docker Swarm, and an autoscaler of one's own.** HivePaaS had to write its own
  autoscaler, read from logs, because Swarm has none, and its overlay carries app
  traffic unencrypted unless asked. Kubernetes gives Skifity HPA, KEDA and
  WireGuard; spend the effort on hiding them, not replacing them.
* **A web terminal, file transfer into containers, the Docker socket and host
  directories for apps.** Each is a switch HivePaaS has to guard; the agent is
  root on every node. Skifity's one-off commands and restricted Pod Security
  stay.
* **Secrets that can be revealed.** A capability, a server switch and an audit
  entry are three gates, and every authorization bug is still a secret leak, as
  HivePaaS's own fix of 2026-10-01 shows: a read-only admin key read every
  secret. Keep them write-only.
* **Functions as a second runtime layer.** Five language runtimes, an editor,
  test containers and a call-counting autoscaler are a product of their own to
  keep patched. A folder deploy with Railpack already turns a few files into an
  app; if anything is taken, it is the test run before saving.
* **No rollback.** Typing an earlier tag back in loses the settings it ran with;
  Skifity's rollback carries them.
* **Previews that copy production's data.** HivePaaS's preview databases can be
  cloned with their data. Skifity's seeded previews are the safer default.
* **Broad changes to the host at install.** Upgrading Docker restarts what runs,
  turning off Docker's SELinux labelling weakens the host, and earlyoom kills
  the largest process. Kubernetes evicts by its own rules; Skifity's installer
  advises swap and changes the host little.
* **Security fixes as "Bug fixes".** Two fixes in beta5 that would carry an
  advisory anywhere else went into notes of two lines, and beta6's notes list
  only Dependabot. Skifity's `SECURITY.md` and `docs/progress.md` already say
  what changed; keep release notes doing the same.
* **Documents that disagree.** "Every plan has every feature" next to a
  `RELEASING.md` that keeps managed databases for keys on the stable channel.
  Fix the document in the same change as the code.

## Sources

All read on 2026-11-08.

* https://github.com/hivepaas/hivepaas, cloned without blobs at `9779998`; 3,664 commits and 12 tags read; files: `README.md`, `SECURITY.md`, `AGENTS.md`, `CLAUDE.md`, `release.json`, `release.signed.json`, `docs/ARCHITECTURE.md`, `docs/RELEASING.md`, `docs/recovery.md`, `docs/DEVELOPMENT.md` (headings), `deployment/release/hivepaas.yaml`, `deployment/release/install.sh`, `hivepaas_app/pkg/releasesig/releasekeys/README.md`, `hivepaas_app/pkg/releasesig/releasesig.go`, `hivepaas_app/interface/mcp/plans.go`, `hivepaas_app/cmd/internal/updater.go`, the file lists of `hivepaas_app`, `services`, `web/src/i18n` and `.github/workflows`; commit bodies of `4cdd5bb`, `f1831c5`, `26143d2`, `88fcf1c`, `3305c96`, `dedb305`, `94ff4d9`, `ddc9f2f`, `ac37a08`, `0e050cf`
* https://github.com/hivepaas/hivepaas-website, cloned at `ce73465`: the 105 pages of `docs/docs/` (installation, deploying apps, configuring apps, databases, domains and TLS, backups, cluster, notifications, administration, integrations, CLI, plans and licenses, troubleshooting) and `landing/src/components/pricing/`
* https://github.com/hivepaas/app-templates, cloned at `73015a4`: `README.md`, `LICENSE`, `index.json` (393 entries, analysed locally), `templates/postgres-managed.yaml`, `templates/mariadb-managed.yaml` (line counts and the replica sections)
* https://ungh.cc/repos/hivepaas/hivepaas/releases (a mirror of GitHub's releases API: the six betas' dates and notes)
* GitHub search API: `search/repositories?q=repo:hivepaas/hivepaas` and `q=org:hivepaas` (stars, forks, dates, licence, the organisation's 17 repositories), `search/issues` for issues (none) and pull requests (37), `search/code?q=hivepaas -org:hivepaas` (third-party mentions)
* https://hivepaas.com/pricing and https://hivepaas.com/ (text extracted locally); https://demo1.hivepaas.com/ (answers; a single-page app)
* https://hn.algolia.com/api/v1/search?query=hivepaas and `/items/49977257` (the Show HN, 3 points, one comment)
* https://raw.githubusercontent.com/mfahlandt/lwcn/HEAD/website/content/newsletter/2026-week-42-articles.md and https://raw.githubusercontent.com/frontierJS/frontierjs/HEAD/IDEAS/paas-parity.md
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=hivepaas (no results); https://api.osv.dev/v1/query for `github.com/hivepaas/hivepaas` (no results)
* Not readable from here: the repository REST API and the GitHub advisory API (refused by this session), `github.com/hivepaas/hivepaas/security/advisories` (HTTP 403), Reddit (HTTP 403), Brave Search (HTTP 429); Bing returned unrelated results; the one Firecrawl web search was refused because the account's credits were used up
* Skifity files named in "Against Skifity" and the gaps, read in this repository at `29147f1`
