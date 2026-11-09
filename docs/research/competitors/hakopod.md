# Hakopod

> **Status.** Written on 2026-11-08 against Hakopod's `main` at commit `8cb3f57`
> (tag `v0.1.0-alpha.82`, 2026-11-07) and against Skifity's working tree the same
> day. No gap named here has been closed yet.

Hakopod (`hakopod/hakopod`, hakopod.com, plus a hosted Hakopod Cloud in beta) is a
self-hosted application platform that installs K3s on one fresh Ubuntu or Debian
server and deploys apps onto it from Git, a container image, a pasted
`docker-compose.yml` or a template. Its pitch is Skifity's almost word for word:
"Kubernetes underneath, no YAML on top"; "You describe an application and its
services; Hakopod writes the manifests". It differs in where the control plane
lives and what it is made of. **Architecture:** a Go API and reconciler in one
process (two deployment workers, a 192 MiB soft memory target) and a TanStack
Start dashboard in a second, Node, process, which is the only public management
listener and forwards to the API; **PostgreSQL 17** holds specifications,
releases, authorization, audit and the durable work queue, either as a pod in K3s
or an existing server reached over verified TLS. Both management processes run as
systemd services on the host (`hakopod-k3s`, `hakopod-api`, `hakopod-dashboard`),
beside a root-owned `hakopod-maintenance` helper reached over a Unix socket.
Ingress is **HAProxy** (3.2), certificates come from cert-manager, and K3s runs
with its SQLite datastore. Every application and environment gets its own
namespace with deny-by-default NetworkPolicies, and a service is private unless
it is marked `public`. Source builds are not run on the server: Hakopod writes a
generated workflow into the repository and the build runs in GitHub Actions or
GitLab CI, or in Hakopod's own "Managed Actions" runner pools (Pro). Apps are
described in a strict, versioned TOML file. **Licence:** Apache-2.0, open core in
one repository: the Pro features are in the public code and unlocked by an
Ed25519-signed licence bound to one installation; the issuer and parts of the UI
library are private submodules. **Pricing** (hakopod.com/pricing, read
2026-11-08): self-hosted Free is $0 with one team and fixed project roles;
self-hosted **Pro** is $99 a year per installation, or $29 a month (the same page
says $19 a month in two other places), and adds unlimited members, more than one
team, Google, GitHub and GitLab sign-in, audit history with CSV export, Slack and
Managed Actions runners; OpenID Connect SSO needs an `enterprise_sso` entitlement
(`docs/paid-features.md`). Cloud "BYOD" plans, a hosted dashboard for your own
servers, are announced at $5 a month for one server and $15 for three, $4 for
each further server; "Paid checkout is not open yet". **Maturity:** self-described
alpha. The public repository was created on 2026-09-12 with history from
2026-03-12; 82 alpha tags followed between 2026-09-13 (`v0.1.0-alpha.1`) and
2026-11-07 (`alpha.82`), with no stable release. `docs/milestones.md` separates
what was verified on a real cluster from what was not, in the same spirit as
Skifity's `docs/progress.md`. **Adoption** (read 2026-11-08): 20 stars, 0 forks,
six issues (all opened by the maintainer, three open) and 252 pull requests, all
from the project's two maintainers (GitHub search API); 1,585 commits, 1,400 of
them by one person and 37 with an AI co-author trailer; `@hakopod/cli` on npm
downloaded 4,309 times in the 30 days to 2026-11-07 and 8,268 times since
2026-09-01; the Terraform provider 176 times. No Hacker News story or comment,
and no independent review, was found.

## Feature inventory

### What shipped, release by release

Dates are the tags' dates in a clone of the repository; what each carried is from
the pull-request titles and bodies (GitHub search API) and the version statements
in Hakopod's own documents. The release pages themselves could not be read from
here.

| Release | Date (2026) | What changed that matters for a comparison |
|---|---|---|
| alpha.1 – alpha.6 | 13–14 Sep | First public alphas: the installer on amd64 and arm64, TOML, plan and deploy with an expected revision, scoped machine keys, HAProxy, private networking by default |
| alpha.15 | 18 Sep | The MCP endpoint over Streamable HTTP, served by the dashboard |
| alpha.37 – alpha.56 | 28 Sep – 8 Oct | Managed PostgreSQL and Redis (alpha.37), MongoDB (47), MySQL (50), ClickHouse (52), Vitess (55), DuckDB through MyDuck and Oracle Database Free (56) |
| alpha.57 – alpha.61 | 8–10 Oct | Stateful sandbox sessions under gVisor (#185), predefined job invocations (#183), runtime profiles (#179), reviewed recovery workflows (#188) |
| alpha.62 – alpha.65 | 16–23 Oct | Private egress bindings (#198), node profiles (#201), sidecar containers (#202) |
| alpha.67 – alpha.68 | 28 Oct | **Application logs kept after the pod is gone** (#207); running apps checked against a signed advisory feed and OSV (#209) |
| alpha.72 – alpha.74 | 30 Oct | Dashboard redesign and **staged changes**: one draft per application, reviewed as a plan before deploy (#220); **private management access** — an allowlist, a host firewall, WireGuard, Tailscale, OpenVPN and IPsec connectors and a built-in WireGuard server (#221–#229); Pro checkout |
| alpha.75 – alpha.78 | 2–5 Nov | Managed Actions runner pools that scale to zero (#242); Plausible, Headscale and monitoring presets |
| alpha.79 – alpha.81 | 6–7 Nov | **Agent policies** that block or hold a machine key's requests for approval (#252); a per-service ingress timeout (#256); Kubernetes patches behind an installation switch (#258); a fix for client starvation that failed deploys (#259) |
| alpha.82 | 7 Nov | SaaS custom hostnames with DNS proof (#261); **OAuth sign-in for the MCP endpoint**, so a client connects without a pasted key (#262) |

### Deploy sources and builds

* **Sources:** an image (resolved to a digest once, before anything is applied);
  a GitHub or GitLab repository through a named "Git connection" — a GitHub App
  created with GitHub's manifest flow, so "No personal token, App ID,
  installation ID, private key or webhook secret needs to be copied"
  (`docs/git-connections.md`) — or a pasted Compose file. A TOML file in the
  repository can be bound to an application and synchronised.
* **Builds** detect the framework, or use a Dockerfile or Cloud Native
  Buildpacks, and run **in the user's own CI**: Hakopod writes a generated
  workflow into the repository, which "requires explicit browser administrator
  review", and the run reports back an image digest. Managed Actions (Pro, beta)
  runs those GitHub jobs on runner pools inside the cluster, one job per replica,
  each in gVisor.
* **Every deployment is a plan first.** `POST /plan` validates the canonical
  specification and returns a diff without touching the cluster;
  `POST /deployments` needs the expected revision and an idempotency key, and a
  duplicate returns the original operation. Accepted work is durable in
  PostgreSQL and resumes after a management restart (milestone 1 recorded a
  kill during a deploy, then 97 public requests with zero errors). Since
  alpha.72 the dashboard stages edits into a shared draft per application by
  default; **Review N changes** shows the diff and its authors, then deploys or
  discards, and an out-of-date draft is rebased three ways.
* A failed rollout tries to restore the last healthy group of services; an
  explicit **rollback is a new, audited revision** built from stored digests.
  Neither reverses database changes or secret values.
* Deployment jobs (run before a release), scheduled jobs, predefined job
  invocations, file mounts, sidecars, several named HTTP endpoints per service,
  public TCP and UDP listeners, and a per-service ingress timeout.
* **Serverless HTTP:** a service can sleep at zero replicas and be woken by an
  activation gateway on `node_ip:8082` (idle 30 s to 24 h, a 60 s cold-start
  wait by default, at most 128 requests in flight per installation). JavaScript
  and Python function starters exist. Not combinable with volumes, GPUs or HPA.
* **Sandbox sessions** keep a worker's memory between calls, for notebook
  kernels, under gVisor ("development-qualified on Linux amd64").

### Domains, TLS and routing

* HAProxy ingress; cert-manager HTTP-01 for a verified custom domain, or an
  uploaded certificate; backend certificates for services that want TLS from
  the proxy. Wildcard domains and DNS-01 are an open "Future" issue (#2).
* **Hakopod Edge** (installation administrators only): ordered rules on exact
  hostnames and path prefixes, IPv4 and IPv6 allow and deny lists, per-client
  rate limits on HAProxy's one-second counter, and country rules. Countries come
  only from `CF-IPCountry` or `CloudFront-Viewer-Country` sent by a trusted
  proxy: "Hakopod does not install or download a GeoIP database". On a host with
  rules, **non-canonical paths are refused with 400**: "Unreserved percent
  escapes, repeated slashes, dot segments, encoded separators, semicolon path
  parameters, nested percent encoding and malformed escapes" (`docs/haproxy.md`).
  At most 32 rules.
* **SaaS custom hostnames** (alpha.82) for a product's own customers: a hostname
  that reaches the installation only by address also needs a TXT record at
  `_hakopod-challenge.<hostname>` holding a token issued for that hostname, and a
  hostname is unique from the moment it is attached.
* DNS records written at Cloudflare, the only provider, inside a zone list that
  is the enforced boundary.

### Databases and services

* Managed databases, each with its own revision history and operations:
  PostgreSQL (CloudNativePG), Redis, MongoDB, MySQL 8.4, ClickHouse, Vitess,
  MyDuck and Oracle Database Free; the website calls the set "Preview". Neon,
  Supabase and public endpoints "remain unavailable". Several engines run from
  patched operators the project builds itself (`patches/oracle-operator`,
  `release/managed-vitess`).
* Database bindings put connection settings into an app and roll them out when
  a password or CA changes; a binding can be tested from inside an application
  pod. A SQL query page and a private-access path for a person.
* Compose import (see "Developer experience").

### Storage and backups

* Volumes are retained when a service or application is removed, and reclaimed
  only when someone asks; a **volume can be grown or shrunk** by an offline copy
  into a new volume, verified by hashes, with the original kept until a person
  confirms (`docs/volume-resizing.md`).
* Backups to S3-compatible storage, **encrypted with age**; a recovery key is
  shown once when the destination is created; a manifest beside each object lets
  it be recovered without the management database. **Restores go into fresh
  database names**, and a review that has expired, a replaced pod or an existing
  database name is refused. One backup or restore runs at a time. Milestones
  record 1,200 PostgreSQL rows, two MySQL rows and a management account restored.
* "A management backup covers the logical database, not the whole host or
  Kubernetes cluster"; the K3s datastore and `/etc/hakopod` are the operator's
  to back up (`installer/README.md`).

### Scaling and high availability

* Replicas per service (at most 20) and HPA; rolling updates that keep the old
  pod when the new one never becomes ready; scale to zero for HTTP services.
* One control-plane node, one PostgreSQL: "One control-plane node and local
  PostgreSQL are not highly available" (`docs/architecture.md`). HAProxy or its
  node failing stops public traffic.
* Each application's namespace has one quota (8 CPU and 8 GiB of requests,
  16 and 16 of limits, 64 pods) that its services share.

### Observability (logs, metrics, alerts, uptime)

* **Logs that outlive the pod** (alpha.67): the API process tails kubelet's
  files on its own node and polls `pods/log` elsewhere, keeps 7 days and 512 MiB
  per application in compressed segments, records why each pod ended (OOM,
  evicted, replaced) and shows the gaps it could not collect. Lines are **masked
  for the app's own secret values at ingest**. Search uses a bounded SQL-like
  filter: `severity >= ERROR AND message ILIKE '%timeout%'`, `json.status >= 500`.
* **Requests:** HAProxy writes one record per completed request — method, host,
  path, status, bytes, TLS version, five timings, retries, backend — collected
  into PostgreSQL; client addresses are masked to /24 or /48 before logging, and
  query strings, headers and bodies are never kept.
* Metrics are live only: "Service charts retain at most 24 actual samples while
  open"; no Prometheus and no history.
* An **alarm inbox** in the dashboard: application and service readiness, and
  node `NotReady`, `DiskPressure`, `MemoryPressure` and `PIDPressure`, with
  optional email. No CPU or memory thresholds were found.
* Security advisories: once an hour each running app's image and runtime
  version (Node, Python, Go, PHP, Ruby, Java) is matched against a signed Hakopod
  feed and OSV; an affected app gets a banner, a notification, a one-step update
  and a **Copy fix prompt** for a coding agent. "They don't inspect OS packages,
  your dependencies or your source code."

### Security, auth, roles, SSO, audit

* Passwords with bcrypt, TOTP with recovery codes, passkeys, all in Free. Google,
  GitHub and GitLab sign-in (Pro), OIDC SSO (`enterprise_sso`). The CLI signs in
  through a **browser consent page** (a device flow) and gets a 12-hour session.
* Team roles owner, administrator, member; project roles administrator,
  developer, viewer. Custom roles and organisation-wide MFA are documented as
  implemented behind Pro (`docs/pro-access.md`) and as "Planned" on the pricing
  page.
* **Machine keys** are scoped to a project and environment, optionally one
  application, with named permissions such as `deployments:read` and
  `logs:read`, an expiry, and rotation with a 15-minute overlap; since #228
  (2026-10-29) they may also never expire. Pod exec and SQL need permissions of their
  own, and the audit log keeps a hash of each command.
* **Management network access** (alpha.72–74, Pro): an allowlist of networks for
  the whole management surface — dashboard, sign-in page, API, CLI, MCP — with
  signed Git webhooks still let through. A save that changes it is
  **provisional and reverts after five minutes** unless confirmed from an allowed
  network; a save that would block the network in use is refused unless ticked;
  `hakopod-server access-reset` on the host turns it off. An optional root agent
  closes the dashboard port and the K3s API in nftables, and joins the server to
  WireGuard, Tailscale or Headscale, OpenVPN or IPsec, or runs a WireGuard server
  whose device keys are generated in the browser. "Admit only people on the VPN"
  turns it into a VPN-only panel.
* **Agent policies** (Pro, 2026-11-06): conditions on team, key, operation,
  project and environment that block a machine key's request or hold it for a
  person's approval, in monitor or enforced mode. "Not yet verified on a real
  cluster."
* Container and host terminals with explicit grants; Kubernetes patches behind
  an installation switch with a refusal list; a "container daemon" grant that
  gives a service mutual-TLS access to a Docker daemon elsewhere.
* Audit log in Free; actor history and CSV export (1,000 rows) in Pro.

### Preview environments and branches

* **Manual previews from an already-built image**: up to four small services,
  one replica each, 5 GiB of fresh storage, a lifetime of 1 to 72 hours, three
  per project. Nothing from production is copied. No previews per pull request.

### Templates and catalogue

* The shared catalogue (`hakopod/templates`, pinned as a submodule at
  `95da89d`, 2026-11-05) has **103 entries: 68 deployable templates and 35
  migration guides** for Dokploy blueprints, which the deploy API refuses
  (`docs/templates.md` still says 78 entries and 43 presets). Every template
  image is **pinned by digest** (60 of 60 with an `image` field).
* Each entry records its own verification. By its own words, **about 15 of the
  68 have run on Kubernetes** (PostgreSQL, Valkey, Redis, MySQL, CockroachDB,
  ClickHouse, Uptime Kuma, Gitea, Bytebase, Dagu, CouchDB, Blinko, Baserow,
  Mathesar, and Xem in part); the rest record "registry manifests checked;
  runtime acceptance pending", or a container smoke test in GitHub Actions
  "not tested on Kubernetes".
* A deployed template goes through the same plan; **Edit TOML** changes the
  generated configuration before the first deploy.

### CLI, API, IaC, integrations

* An OpenAPI contract (`api/openapi.json`) with an agent category on every
  operation; a generated TypeScript SDK in the repository (not on npm); the CLI
  on npm for six platforms.
* **CLI:** `login`, `validate`, `plan`, `deploy --wait`, `key-create`,
  `logs --history`, `exec`, `terminal`, `database query`, `preview-create`,
  `api operations`.
* **MCP** at `/api/v1/mcp?project=…&environment=…`, Streamable HTTP, with
  read tools (applications, runtime, domains, provenance, logs) and a `plan`
  tool; deploying needs `allow_deploy=true`, and exec, SQL and terminals need
  their own options and an API key. Since alpha.82 a client can sign in with
  **OAuth** (PKCE, dynamic client registration, protected-resource metadata)
  through the CLI's consent page, for a 12-hour session with no refresh token.
  "The flow has not been tried end to end with Claude in a real browser" (#262).
* **Provenance:** each accepted image digest is mapped to the build commit,
  repository and run that produced it, or to a commit a CI job reported.
* **A GitHub Action** (`hakopod/deploy`, on the Marketplace) deploys selected
  services with an image digest and waits for the result.
* **A Terraform provider** on the Terraform Registry (`hakopod/hakopod` 0.1.1,
  2026-10-10, 176 downloads) for projects, applications, secrets and virtual
  networks; it sends the same TOML to the same API.
* External secrets from HashiCorp Vault, OpenBao and Infisical; AWS workload
  identity for services.

### Notifications

* Up to five destinations **per application** — email, Slack (Pro), Discord, a
  signed webhook — for deployment success, failure and cancellation; the alarm
  inbox and SMTP for runtime alarms; security advisories to every channel.

### Multi-server and networking

* Workers join from the dashboard with an **expiring K3s bootstrap credential**
  run on the worker, so the panel never needs SSH to it; cordon and drain; node
  profiles an operator approves; a service can be **pinned to a node by name**,
  and the plan refuses a cordoned node, a different architecture, known
  overcommit and a conflict with a volume's node. "Physical Linux workers and
  control-plane/ingress HA remain separate tests."
* Named virtual networks, internal-only egress, and egress to a private database
  only through a binding. "There is no claim of encrypted east-west application
  traffic" (`docs/threat-model.md`).

### Team and collaboration

* One team with invitations and fixed project roles in Free; more teams and
  unlimited members in Pro; a private personal workspace per verified account.
  Deployment approvals are a planned Cloud Team feature, not self-hosted.

### Developer experience and onboarding

* `curl -fsSL https://hakopod.com/scripts/installer.sh | sudo sh` on a **fresh,
  dedicated** Ubuntu 24.04/26.04 or Debian 12/13 server with **at least 4 GiB of
  memory and 30 GiB of free disk**; it refuses an existing K3s or Kubernetes, has
  `--dry-run`, prints a plan and asks the operator to type `install`. "No
  uninstaller or automatic data deletion is provided." Each release runs the
  installer on fresh amd64 and arm64 GitHub runners before it is published.
  Upgrades follow declared source versions ("alpha.34 can reach alpha.38 through
  alpha.36").
* A first install queues a labelled, removable sample shop. Guided empty states
  on every list page.
* **Compose import:** paste or upload, review "the conversion differences", edit
  or download the generated TOML, then the normal plan. Images only — `build` is
  not executed — and host bind mounts, privileged containers, devices, Compose
  healthchecks, profiles and labels fail with a field-specific message; `.env`
  files can be attached and sensitive values become secret references.
* The dashboard is **English only** (no translation library in `web/package.json`);
  the README is in six languages.

## What users love

No independent review, forum thread or Hacker News comment about Hakopod was
found (Hacker News search for "hakopod" with typo tolerance off returns nothing;
a web search found only its own site, two GitHub lists and a pinned, unranked
listing on boringproducts.xyz). Reddit could not be read from here. What there is:

* **Somebody runs it in production and reports back.** SyneHQ "ran production
  deploys with CLI alpha.66 and reported 18 problems" (#251, 2026-11-06) and sent
  a second report on 2026-11-07 (#259); what was still a bug was fixed within
  a day (eight of the first 18; two had been fixed in alpha.67). SyneHQ's
  own product is also in the catalogue (`synehq-oos`).
* **Installs of the tools around it.** 4,309 CLI downloads from npm in 30 days
  and 176 of the Terraform provider.
* **What the project leads with**, which is what it expects people to value:
  "See the change before it ships", "Bring your Compose file", "Give agents a
  key, not your cluster", "Private by default", and a comparison table that
  says "if you want the most mature option today, Coolify has the largest
  community" (README).

## What users complain about

All from SyneHQ's two reports, as the maintainers summarised them in pull
requests #251 and #259; nothing else was found.

* **The CLI and MCP did not accept names.** `hakopod exec <app>` sent the name as
  an ID and answered `not_found`; MCP tools "only took an application ID", so
  `shop` returned "resource not found"; two tools refused a valid call because
  of an optional `_meta` field.
* **A plan that passed and a deploy that failed.** "`plan` passes, `deploy`
  fails on another app's secret"; secrets are per application, so sharing one
  meant an external secret provider.
* **A diff that hid everything.** "Plan redacts every env value", so a reviewer
  could not tell which variables changed.
* **Log search that missed errors.** `severity >= ERROR` missed plain-text lines
  that start with `ERROR`; logs had no retention until alpha.67 (#13, #14 in the
  report); the `logs` MCP tool "always returned `data: null`" until alpha.82
  (#262).
* **Deploys that failed while nothing was wrong.** `deploy --wait` failed
  rollouts that worked; and, the one marked critical, deploys failed with
  "managed database is not healthy" while every database was healthy, because one
  Kubernetes client at 10 requests a second served deploys, a 15-second resync
  and database observation for about 30 apps: "A restart cleared the backlog for
  about two hours" (#259). The fix splits the client and, as a trade-off, a
  rotated database password now takes up to 10 minutes to reach running pods.
* **Sessions that expire mid-work.** The 12-hour CLI session was kept; a 401 now
  says to sign in again.
* Visible in the repository rather than reported: documents that disagree with
  each other — 68 templates in the README against 78 entries and 43 presets in
  `docs/templates.md`; $29 and $19 a month for Pro on one page; custom roles
  "planned" on the pricing page and documented as implemented in
  `docs/pro-access.md`; an installation verification record
  (`docs/prebuilt-installation-verification.md`) last written for alpha.4.

## Security record

**Where I looked (2026-11-08):** `SECURITY.md`, `docs/threat-model.md`,
`security-advisories.json`, the 1,585-commit history, the issue and pull-request
search, the NVD API and OSV. The repository's advisory pages and the GitHub
advisory API are not reachable from here, so **whether any GHSA has been
published is not verified**.

* **NVD:** a keyword search for "hakopod" returns no CVEs. **OSV:** nothing for
  `github.com/hakopod/hakopod`.
* **What the project says about itself:** "This is an early, single-organization
  development milestone. It is not a production-hardened service or a hostile
  multi-tenant isolation boundary." There is no security mailbox: "use its
  private vulnerability reporting feature once the maintainer has enabled it",
  and "The project currently makes no response time or supported-release
  guarantee" (`SECURITY.md`). The threat model says namespaces and
  NetworkPolicies "are not a guarantee against hostile tenants" and that there is
  no encrypted traffic between services.
* **History:** no commit or pull request names a vulnerability it fixes. The
  security-themed work is hardening of features as they were added (isolation of
  MyDuck, ClickHouse tenant access, Neon cleanup ownership). Its own advisory feed
  holds one entry, about Node.js runtimes in deployed apps, not about Hakopod.
* **Exposure by design:** container and **host** terminals; a root agent that
  rewrites nftables and VPN interfaces every ten seconds and is downloaded from a
  GitHub release on demand; a root maintenance helper behind a Unix socket with
  fixed operations; a service grant to a remote Docker daemon ("effective root on
  its own host"); Kubernetes patches from people who hold `kubernetes:patch`;
  machine keys that may never expire (#228); build workflows written into the
  user's repository with a shared installation GitHub token.
* **Done well:** canonical paths enforced before edge rules match; trusted-proxy
  headers read from the right and only from listed peers; secret values masked in
  stored logs; client addresses masked before request logs are written; image
  resolution that refuses private and link-local registries and checks the
  architecture; egress that excludes metadata ranges; a provisional allowlist that
  reverts on its own; one-use OAuth codes bound to the user, with PKCE; a signed
  advisory feed; SBOMs and build-provenance attestations on releases; digest-pinned
  template images; and documents that say what has not been run.

## Against Skifity

Skifity's statuses: much of it runs on a real single-server and two-server k3s on
every push (`docs/progress.md`, "A real cluster, at last" and "What one throwaway
server could not show"); a mixed-architecture cluster and production traffic never
have. Hakopod's milestones are in the same position by its own account. Line
numbers are from the working tree on 2026-11-08.

| Capability | Hakopod | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Git connection | GitHub App by manifest flow, GitLab | GitHub personal token, GitLab, Gitea, Bitbucket, generic (`internal/gitsrc`); the GitHub App was removed because it was never finished (`internal/api/integrations_handlers.go:77-81`) | ahead on providers; **behind on connecting GitHub** |
| Where builds run | The user's GitHub Actions or GitLab CI, through a workflow written into the repository; Managed Actions (Pro) | In-cluster BuildKit Jobs with Railpack, Nixpacks, Dockerfile or static (`internal/builder`) | different; Skifity needs no CI and no write access to the repository |
| Plan before a change | Every deploy is a plan with an expected revision; drafts staged in the dashboard and reviewed as one diff | `skifity.yaml` plan and apply from the CLI (`internal/blueprint`), a rollback plan (`GET /api/apps/{app}/rollback/{deployment}/plan`, Phase 94), a Compose plan; a saved setting in the panel rolls out at once (`docs/concepts.md:124-125`); no expected-revision or idempotency check in `internal/api` | **behind** in the panel |
| Rollback | A new audited revision from stored digests | A new deployment with the old image and runtime settings, shown first (Phase 94, `internal/deploy`) | parity |
| CI deploy of a built image | GitHub Action with digests, scoped keys, CI-reported provenance | The deploy body has `commit_sha`, `force`, `accept_vulnerabilities` and no image (`internal/api/deploy_handlers.go:19-26`); `docs/cli.md` has no CI section | **behind** |
| Scale to zero | Serverless HTTP through its own activation gateway, single replica, no volumes | KEDA's HTTP add-on (`internal/kube/scaletozero.go`), run on the Cluster workflow | parity |
| **Domains, TLS and routing** | | | |
| Certificates | HTTP-01 after verification, uploaded certificates; no DNS-01 (#2 open) | HTTP-01 (`internal/cluster/issuer.go`), a team's own certificates (`internal/tlscert`); no DNS-01 (`docs/concepts.md:423-426`) | parity |
| Edge rules | Installation-wide, exact hosts and path prefixes, IP lists, rate limits, countries from a CDN header only, 32 rules | Per app, address, country and ASN from a bundled database, host, path, method, user agent and headers, nested and/or (`internal/edgerules/edgerules.go`, `internal/geoip`), maintenance mode (`internal/guard/maintenance.go`) | ahead |
| Path canonicalisation | Non-canonical paths answered 400 on hosts with rules | The path judged as the visitor encoded it (`pathOf`, `internal/guard/guard.go:424-433`; nothing decodes in `internal/edgerules`) | **behind** (gap 1) |
| Domain ownership | TXT proof when a hostname reaches the installation only by address | Hostname unique per panel, first come first served (`internal/store/migrations/0001_init.sql:202`); the only cross-team check is against another team's certificate (`internal/api/certificates_handlers.go:264-277`); the Ingress is applied at once (`internal/api/apps_handlers.go`, `handleAddDomain`) | **behind** for panels with several teams |
| DNS providers | Cloudflare | Cloudflare, Hetzner, DigitalOcean, Route 53 (`internal/dnsprov`) | ahead |
| **Databases and services** | | | |
| Engines | Eight, "Preview", several from patched operators | Nine (`docs/databases.md:10-20`, `internal/dbsvc`) | parity in count; different sets |
| Restore | Into a fresh database name | In place, with a backup taken first (`docs/backups.md`, `docs/databases.md` "Importing a dump") | **behind** (open since `openship.md` gap 8) |
| Compose | Images only, an editable TOML review, imports into an existing app | A stack of apps that may build from the file's context (`internal/api/stack_handlers.go`, Phase 101) | ahead on builds; behind on editing before creating |
| **Storage and backups** | | | |
| Backup encryption | age, a recovery key shown once, a manifest per object | Sealed with a passphrase and verifiable (`docs/backups.md`, Phase 104) | parity |
| Volume size | Grow or shrink by verified copy, original kept | No resize route for an app's volume (`internal/api/api.go:542-550`); a database disk grows only where the storage class allows, which local-path never does (`web/src/locales/en.json`, `database_storage_not_expandable`) | **behind** |
| Control-plane backup | Operator's job (`pg_dump`, `/etc/hakopod`, the K3s data) | Scheduled panel backups to the bucket (Phase 84), `restore-db` (`docs/backups.md`), etcd snapshots | ahead |
| **Scaling and HA** | | | |
| Instances and autoscaling | Replicas, HPA | Instances, HPA (`internal/kube/apply.go`), readiness checker (`internal/deploy/scaling.go`) | parity |
| Placement | Pin a service to a node, with plan checks | Spread and anti-affinity only (`internal/kube/manifests.go:166-180`); no `kubernetes.io/arch` anywhere in `internal/` | **behind** |
| **Observability** | | | |
| Log history | 7 days and 512 MiB per app after the pod is gone, with gaps shown | Kubernetes' own logs, at most 10,000 lines, and the previous container (`internal/api/stream_handlers.go:427-449`); history only through a log drain (`internal/logdrain`) | **behind** |
| Log search | SQL-like filter on severity, JSON fields, pod, time | A substring over the lines on screen (`web/src/components/app/logs-tab.tsx:105-116`) | **behind** |
| Secrets in logs | Masked at ingest | Not masked; a viewer reads logs (`authorizeApp(..., store.RoleViewer)`, `internal/api/stream_handlers.go:412`) | **behind** (gap 2) |
| Requests | One record per request, addresses masked | Counts, status classes and latency from Traefik (`internal/traffic`) | **behind** (open since `openship.md` gap 6) |
| Usage history and thresholds | 24 live samples, no history; readiness and node-condition alarms | CPU, memory and disk history with thresholds and `app.alert`, `server.alert`, `backup.missed` (`internal/notify/notify.go`, Phases 95 and 99) | ahead |
| Vulnerabilities | Runtime versions against a signed feed and OSV, hourly, one-step update | Trivy on every deploy and nightly, a deploy gate (`internal/vulnscan`), framework advisories (Phase 97) | ahead on coverage |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | bcrypt, TOTP, passkeys; social sign-in and OIDC are paid | Argon2id, TOTP, passkeys, OIDC with groups mapped to roles, team-required 2FA (`internal/auth`), all free | ahead |
| CLI sign-in | Browser consent, 12-hour session | A token created in the panel and pasted (`docs/cli.md:41-48`) | **behind** |
| Machine keys | Project, environment and application scope; rotation with overlap | Scopes by resource type, expiry, network allowlist (`internal/auth/scopes.go`); no project on a token (`internal/api/auth_handlers.go:495-500`) | **behind** (open since `openship.md` gap 4) |
| Management network | Allowlist for every management request, provisional save, reset command, host firewall, four VPN connectors and a VPN server (Pro) | An allowlist for API tokens only: "A browser session is not limited" (`internal/api/middleware.go:313-317`); the panel is always published (`deploy/ingress.yaml`) | **behind** (gap 3) |
| Agent approvals | Policies that block or hold a key's request (Pro, unverified) | Locked deploys (Phase 94); nothing holds a request for a person | behind (small) |
| Terminals | Container and host terminals | None, by decision (`docs/roadmap.md:149-152`) | deliberate difference |
| Audit | Free log; history and CSV export in Pro | Team audit log purged after 90 days, no export (`internal/api/api.go:345`, `:969`) | behind (small) |
| **Preview environments** | | | |
| Previews | Manual, from a built image, 1–72 h, three per project | Per pull request with statuses and a comment, preview-only values, whole-environment previews with a seed (`internal/api/webhook_handlers.go`, `internal/deploy/gitreport.go`, Phases 93 and 107) | **ahead** |
| **Templates** | | | |
| Catalogue | 68 deployable, digest-pinned, each with a recorded verification | 374 with checked versions (`internal/templates/catalogue`); nothing records whether one ran (`internal/templates/templates.go:148-171`) | ahead on count; **behind on saying what was run** |
| **CLI, API, IaC, integrations** | | | |
| MCP | Streamable HTTP, a `plan` tool, OAuth sign-in (alpha.82) | Streamable HTTP and stdio, about 47 tools written by hand (`internal/mcpserver`), Bearer token only (`internal/api/mcp_handlers.go:47-60`) | **behind** on sign-in |
| IaC | TOML in the repository, a Terraform provider, a TypeScript SDK | `skifity.yaml` plan and apply (`internal/blueprint`), OpenAPI (`internal/api/openapi.go`), team export | parity; no Terraform |
| Secret managers | Vault, OpenBao, Infisical | Vault, AWS Secrets Manager, Doppler, Infisical (`internal/secretmgr`) | parity |
| Server provisioning | None self-hosted; AWS, GCP and Azure in a private Cloud repository | Hetzner and DigitalOcean from the panel (`internal/cloud`) | ahead |
| **Notifications** | | | |
| Channels and routing | Email, Slack (Pro), Discord, webhook, per application; an in-panel inbox | Ten channels, limited to projects (`internal/notify`, migration 0043); no inbox | ahead on channels; behind on an inbox |
| **Multi-server and networking** | | | |
| Joining servers | A one-time command on the worker, no SSH from the panel | SSH with a password once, then the panel's key (`internal/provision`) | different |
| Traffic between servers | Not encrypted, by its own account | WireGuard flannel by default (`installer/install.sh`, `internal/settings/settings.go`) | ahead |
| k3s upgrades | "doesn't upgrade K3s for you" | From the panel (Phase 109) | ahead |
| **Team and collaboration** | | | |
| Teams | One team free, more in Pro | Teams, invitations, roles, project limits, all free (`internal/api`, Phases 85–86) | ahead |
| **Developer experience** | | | |
| Install | 4 GiB, 30 GiB disk, a dedicated server; dry run and a printed plan; amd64 and arm64 tested per release | 2 GB, 8 GB disk (`README.md`); no `--dry-run` in `installer/install.sh`; the Cluster workflow runs on `ubuntu-24.04` only (`.github/workflows/cluster.yml:45`) | ahead on footprint; behind on dry run and arm64 |
| Languages | English dashboard; README in six | Five with a build gate (`web/src/locales`) | ahead |
| Release | 82 alphas in 56 days, upgrades along declared paths | v0.1.0 and v0.1.1 | different |

### Claims that no longer hold

* `docs/research/competitors.md` says of installing the cluster: "None of them puts
  Kubernetes on one small server with one command." Hakopod does put K3s on one
  server with one command, as Openship's "Enable scaling" does on one or two. What
  is still Skifity's is "small": Hakopod asks for 4 GiB and 30 GiB of disk on a
  dedicated machine, Skifity for 2 GB and 8 GB. The same paragraph's "Written,
  never run" is out of date.
* The same file's "Hiding Kubernetes well is still the whole bet" names Portainer
  as the product with the same pitch. Hakopod is a closer one — "Kubernetes
  underneath. Buttons on top." on its home page — with a Go control plane, K3s
  from the first server, generated manifests, default-deny namespaces and MCP on
  scoped keys.
* "Secrets that stay secret" is narrower than it reads: Skifity's values are
  write-only on every surface, but an app that prints one puts it in front of
  anybody who can read its logs, viewers included. Hakopod masks them.
* `docs/templates.md:72` says "None of these has been deployed by us, because
  nothing in this product has been deployed by us yet". The Cluster workflow
  installs Homer from the catalogue on every push (`test/cluster/verify.sh:2887`).

## Gaps worth closing in Skifity

**P1**

1. **Judge the path the app will see.** *What:* in the guard, decode the path
   once before any rule looks at it; answer 400 to a path that does not decode,
   that holds an encoded `/`, `\`, `.` segment or NUL, a `;` parameter or a
   second level of encoding; collapse repeated slashes and remove dot segments
   the way a router would; test each form. *Evidence:* the Kamal page found the
   bypass on 2026-11-08 (`/%61dmin` steps round a rule on `/admin`) and it is
   still open: `pathOf` returns `X-Forwarded-Uri` as sent and lowercases it
   (`internal/guard/guard.go:424-433`), and nothing in `internal/edgerules`
   decodes. Hakopod's edge refuses exactly this list with 400 on hosts with rules
   (`docs/haproxy.md`), so the design is already worked out. A rule that can be
   stepped round is worse than none, because people trust it. *Fit:*
   `internal/guard`, with the decoded path passed to `edgerules`. *Size:* S.
   *Without a cluster:* yes; the guard's tests run without one.
2. **Mask an app's own secrets in what the panel shows of its logs.** *What:*
   before a log line leaves the API, replace every value of that app's secret
   variables, linked database passwords and connection strings with a marker,
   including their URL-encoded and base64 forms, as the lines stream. *Evidence:*
   Hakopod masks at ingest and calls it standard. Skifity stores secrets
   write-only, yet `handleAppLogs` streams raw lines to anyone with the viewer
   role (`internal/api/stream_handlers.go:412`), so a framework that prints its
   connection string on start shows the password to people who can never open
   Variables. *Fit:* one masker built from the app's resolved secrets in
   `internal/api/stream_handlers.go`; the MCP server and the CLI read logs
   through the same router (`internal/api/mcp_handlers.go`), so they inherit it;
   log drains ship from Vector on the nodes (`internal/logdrain/vector.go`) and
   need a redact transform with the same values, or a note on the drain form that
   they carry logs as written. *Size:* S–M. *Without a cluster:* yes, against a
   fake log stream.
3. **Put signing in behind the same allowlist as tokens.** *What:* a setting that
   limits the whole panel — sign-in page, sessions, API, MCP — to listed
   networks, with Git webhooks and health checks let through; a save that would
   lock out the network in use is refused unless confirmed; every change is
   provisional and reverts after five minutes unless confirmed from an allowed
   network; `skifity admin access-reset` on the server turns it off and is
   audited. Later, a Tailscale connector. *Evidence:* Hakopod shipped this, a host
   firewall and four VPN connectors in one week (#221–#229); Openship's maintainer
   told beta users to stay off the network, and Coolify reviewers' first hardening
   advice is a VPN or an allowlist. Skifity's allowlist binds API tokens only, by
   design ("A browser session is not limited", `internal/api/middleware.go:313-317`),
   and the panel is always on its public Ingress. `openship.md` gap 10 asked for a
   private panel; this is the cheaper first half. *Fit:* `tokenNetworkAllowed`
   becomes a check for every request except the webhook routes, the trusted-proxy
   count already in `internal/api/middleware.go`, `internal/cli/admin.go`, the
   Sign-in settings group. *Size:* S–M (allowlist); M (Tailscale). *Without a
   cluster:* yes for the allowlist, with `make smoke`.
4. **Sign the CLI, and assistants, in through the browser.** *What:* `skifity
   login` opens a consent page in the panel (a device code shown in both places),
   the person approves the team, projects and scopes, and the CLI receives a
   short-lived session token; the MCP endpoint then speaks OAuth 2.1 on top of the
   same consent (metadata, dynamic client registration, PKCE), so `claude mcp add
   --transport http` works without a pasted secret. *Evidence:* Hakopod built
   exactly this in that order — device consent first, OAuth for MCP on it in
   alpha.82 (#262), "so there is no second approval system". Skifity asks for a
   token made by hand and pasted (`docs/cli.md:41-48`) and its MCP refuses anything
   but a Bearer token (`internal/api/mcp_handlers.go:47-60`). `openship.md` gap 1
   asked for the OAuth half; Hakopod shows the order that makes it small. *Fit:*
   `internal/auth` (a device-code store, sealed), `internal/api` routes, the
   existing scoped tokens as the grant, a consent page in `web/src/pages`. *Size:*
   M. *Without a cluster:* yes, end to end against the real binary.
5. **Deploy an image built in CI in one call, once.** *What:* an `image` (by tag
   or digest) on `POST /api/apps/{app}/deploy`, recorded on the deployment;
   `skifity deploy --image … --wait`; an `Idempotency-Key` header so a retried CI
   step returns the first deployment instead of starting a second; a GitHub
   Actions and a GitLab CI example in `docs/cli.md`. *Evidence:* the Kamal page's
   P1, still open: the deploy body has no image (`internal/api/deploy_handlers.go:19-26`)
   and the CLI guide has no CI section. Hakopod has a Marketplace Action that
   deploys selected services by digest and waits, idempotency keys on every
   deployment, and CI-reported commits for each digest. *Fit:* the deploy handler,
   `internal/deploy`, `internal/cli/commands.go`. *Size:* S. *Without a cluster:*
   yes.

**P2**

6. **Logs kept after the pod is gone, with a search that understands them.**
   *What:* the panel copies each app's logs while its pods run and keeps a
   bounded window (days and MiB per app, set by an administrator) after they are
   replaced, crash or are evicted; the Logs tab gains past instances, a time
   range and a filter on level, text and JSON fields; gaps the collector missed
   are shown, never hidden. *Evidence:* Hakopod's alpha.67 (#207), after its one
   production user listed log retention among 18 problems. Skifity reads what
   Kubernetes still has — at most 10,000 lines and one previous container
   (`internal/api/stream_handlers.go:427-449`) — and filters on screen with a
   substring (`web/src/components/app/logs-tab.tsx:105-116`); after a rollout the
   crashed instance's last lines are gone unless a drain sent them elsewhere.
   *Fit:* compressed segments on the panel's volume with an index in SQLite, fed
   from `pods/log` with a time cursor (the panel does not run on every node), the
   masking of gap 2 applied on the way in, a retention sweep on the minute tick.
   *Size:* M–L. *Without a cluster:* the collector, storage and search against a
   fake client yes; behaviour across rollouts needs one.
7. **Prove a domain before routing it, when the panel has more than one team.**
   *What:* adding a hostname that is not under a DNS zone the team has connected
   issues a token and asks for a TXT record at `_skifity-challenge.<hostname>`;
   the Ingress and the certificate wait until it resolves. A panel with one team
   can switch it off. *Evidence:* Hakopod's threat model names the problem —
   "every application shares the ingress addresses, so a domain whose A or AAAA
   records point at the installation could otherwise be claimed by whichever
   application asked first" — and alpha.82 requires the TXT proof. In Skifity a
   hostname is unique per panel, first come first served
   (`internal/store/migrations/0001_init.sql:202`), the only cross-team check is
   against another team's uploaded certificate (`internal/api/certificates_handlers.go:264-277`),
   and `handleAddDomain` applies the Ingress at once, so HTTP-01 succeeds for any
   name whose DNS already points at the panel. *Fit:* `internal/api/domain_dns.go`
   (it already resolves records for "Check again"), a column for the token,
   `internal/dnsprov` to write the record when the zone is connected. *Size:* S–M.
   *Without a cluster:* yes, with a fake resolver.
8. **Stage changes and apply them as one reviewed rollout.** *What:* first, an
   expected revision on every write to an app, so two people editing it are told
   instead of overwriting each other; then a "Review changes" bar on the app page
   that collects edits to variables, scaling, domains and processes, shows the
   diff (names of changed secrets, never values), says whether it restarts or
   rebuilds, and applies them in one rollout. *Evidence:* Hakopod plans every
   deployment, refuses a stale revision, and since alpha.72 stages edits into a
   shared draft reviewed as one diff (#220); its user's complaint that the diff
   "redacts every env value" shows the detail that matters. Skifity has plans for
   `skifity.yaml`, a rollback and a Compose stack, but a saved setting in the panel
   rolls out at once (`docs/concepts.md:124-125`), and nothing in `internal/api`
   checks a revision or an `If-Match`. *Fit:* `internal/blueprint`'s `Plan` already
   computes ordered differences; a draft row per app; the fingerprint that already
   decides restart or rebuild (ADR-0007). *Size:* S (revision check), M (drafts).
   *Without a cluster:* yes.
9. **Grow or shrink a volume by copying it.** *What:* a Resize action on a volume
   and a database disk that stops the app, copies the data into a new volume of
   the chosen size with a helper Job, verifies it, switches the app, and keeps
   the original until somebody deletes it. *Evidence:* Hakopod does this with
   hash verification, a journal that survives restarts, and Cancel and Retry
   (`docs/volume-resizing.md`). Skifity has no resize for an app's volume
   (`internal/api/api.go:542-550`), and a database disk grows only where the
   storage class allows expansion, which "k3s's own local-path storage never
   allows" (`web/src/locales/en.json`, `database_storage_not_expandable`) — so on
   the default install a full disk has no way out but a new database and an
   import. `quickstack.md` raised growing a volume; copying is how it works on
   local-path. *Fit:* `internal/backup` already scales an app to zero and back
   for a volume restore; the copy is the same Job shape. *Size:* M. *Without a
   cluster:* the plan, the Job and the state machine yes; the copy needs one.
10. **Say which templates have run, and run them.** *What:* a `verified` block
    per template — date, architecture, what was checked (started, answered on
    its URL, survived a restart) — written by a scheduled workflow that installs
    a rotating batch of templates on the throwaway k3s each night, and shown on
    the template card; fix the stale sentence in `docs/templates.md:72`.
    *Evidence:* Hakopod records a verification line for every one of its 68 and
    says plainly that about 15 have run on Kubernetes. Skifity has 374 templates,
    installs one (Homer, `test/cluster/verify.sh:2887`), and has no field for it
    (`internal/templates/templates.go:148-171`), while its document still says
    nothing has been deployed. *Fit:* `internal/templates`, `.github/workflows`,
    the template card. *Size:* M. *Without a cluster:* the field and the card yes;
    the runs need the Cluster workflow's machine.
11. **Requests one by one.** `openship.md` gap 6, with Hakopod's privacy rules as
    the design: per request the time, method, host, path without the query,
    status, bytes and duration, client addresses cut to /24 or /48 before they are
    stored, nothing from headers or bodies, readable with the logs permission.
    Skifity counts requests in minute buckets (`internal/traffic/collector.go`)
    and keeps none. M; the parser and storage without a cluster, real traffic
    with one.

**P3**

12. **Pin an app to a server.** A server picker on Scaling that sets a node
    selector, refused when the server is cordoned, of another architecture,
    without the memory asked for, or where a volume is bound elsewhere — Hakopod's
    checks (`docs/node-placement.md`). Skifity spreads instances and cannot place
    one (`internal/kube/manifests.go:166-180`). S; no cluster for the plan, one to
    prove it.
13. **Keep the audit log longer and export it.** A retention setting instead of
    the fixed 90 days (`internal/api/api.go:969`) and a CSV or JSON download on
    the activity page; Hakopod sells this as Pro. S; no cluster.
14. **An installer dry run.** `install.sh --dry-run` runs the preflight and prints
    what it would install, open and write, then stops; Hakopod's installer also
    makes the operator type `install`. S; no cluster.
15. **Run the Cluster workflow on arm64 too.** Hakopod installs every release on
    fresh amd64 and arm64 runners; Skifity's Cluster workflow runs on
    `ubuntu-24.04` only (`.github/workflows/cluster.yml:45`), though arm64 is a
    supported server and Hetzner's ARM machines are offered from the panel. S;
    needs the runner.
16. **Hold an assistant's changes for a person.** An option on a token that
    turns its deploys and deletions into requests a team admin approves in the
    panel; Hakopod's agent policies do this (Pro, unverified). Locked deploys
    (Phase 94) cover the freeze, not the approval. M; no cluster.

## Things to deliberately not copy

* **Security features behind a licence.** Hakopod charges for OIDC SSO, Google,
  GitHub and GitLab sign-in, more than one team, audit history and agent
  policies, enforced by a signed licence in an Apache-2.0 codebase that anybody
  can patch. Skifity's list already says not to put SSO, audit or roles behind a
  licence; Hakopod is one more reason.
* **Builds in the user's CI, through a workflow written into their repository.**
  It needs write access to the repository and a shared installation GitHub token,
  and a project without Actions minutes cannot build. Skifity's in-cluster
  BuildKit Jobs need neither.
* **A second management process and a database server.** The Node dashboard and
  PostgreSQL are why Hakopod asks for 4 GiB and 30 GiB of disk on a dedicated
  machine. One binary and SQLite are why Skifity asks for 2 GB.
* **Host and container terminals, a remote Docker daemon grant, and user patches
  to generated objects.** Each comes with a refusal list or a paragraph on what
  the operator accepts; the Docker daemon is "effective root on its own host".
  Skifity's one-off commands and read-only Advanced tab stay.
* **A root agent rewriting the host firewall every ten seconds.** Hakopod's VPN
  agent is downloaded on demand and keeps nftables and tunnels in line with the
  panel. If Skifity closes ports for the panel, it should be once, at install or
  from a provisioning step through `internal/shellsafe`, not a resident loop.
* **Country rules that need a CDN.** Hakopod reads countries only from Cloudflare
  or CloudFront headers; Skifity's bundled GeoIP works on a bare server.
* **Manual, short-lived previews instead of pull-request previews.** Skifity's per
  pull request previews with statuses and seeds are ahead; do not trade them for
  the simpler model.
* **Many engines at "Preview" built from patched operators.** Oracle Free, Vitess
  and MyDuck each carry a patched operator or runtime image the project now
  maintains. Skifity's nine engines from upstream images are enough to keep
  verified.
* **Upgrades along declared version pairs.** "alpha.34 can reach alpha.38 through
  alpha.36" is honest for 82 alphas, and it is a chore for the person upgrading.
  Skifity should keep upgrading from any recent release in one step.
* **Keys that never expire** (#228). Skifity's tokens take a time to live; keep a
  maximum.
* **Documents that disagree.** Template counts, prices and feature status differ
  between Hakopod's README, its pricing page and its docs. Skifity's equivalent is
  the stale sentence in `docs/templates.md`; the habit to keep is fixing the
  document in the same change as the code.

## Sources

All read on 2026-11-08.

* https://github.com/hakopod/hakopod, cloned (blobs on demand) at `8cb3f57`; 1,585 commits and 86 tags read; files: `README.md`, `SECURITY.md`, `AGENTS.md`, `.gitmodules`, `security-advisories.json`, `installer/README.md`, and in `docs/`: `milestones.md`, `architecture.md`, `threat-model.md`, `paid-features.md`, `pro-access.md`, `templates.md`, `compose-import.md`, `management-network-access.md`, `http-mcp.md`, `idle-http-runtime.md`, `serverless.md`, `sandbox-sessions.md`, `haproxy.md`, `requests.md`, `application-logs.md`, `observability.md`, `alarms.md`, `deployment-notifications.md`, `dns-providers.md`, `git-connections.md`, `agent-policies.md`, `backups.md`, `volume-resizing.md`, `retained-storage.md`, `node-placement.md`, `managed-databases.md`, `engine-workflows.md`, `source-build-onboarding.md`, `installation-maintenance.md`, `prebuilt-installation-verification.md`, `terraform.md`; the list of `web/src/routes` and `web/package.json`
* https://github.com/hakopod/templates, cloned at `95da89d` (2026-11-05): `catalog.json` (103 entries)
* GitHub search API: `search/repositories?q=repo:hakopod/hakopod` and `q=org:hakopod` (stars, forks, dates, licence, the organisation's seven repositories); `search/issues` for the six issues and 252 pull requests, with the bodies of #207, #209, #220, #251, #259 and #262
* https://hakopod.com/, https://hakopod.com/pricing, https://hakopod.com/security/, https://hakopod.com/self-hosted/, https://hakopod.com/compare/
* https://registry.npmjs.org/-/v1/search?text=hakopod, https://api.npmjs.org/downloads/point/last-month/@hakopod/cli and `/range/2026-09-01:2026-11-07/@hakopod/cli`
* https://registry.terraform.io/v1/providers/hakopod/hakopod
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=hakopod (no results); https://api.osv.dev/v1/query for `github.com/hakopod/hakopod` (no results)
* https://hn.algolia.com/api/v1/search?query=%22hakopod%22&typoTolerance=false (no results)
* One web search (Firecrawl) for "hakopod": results were Hakopod's own pages, `github.com/topics/coolify-alternative`, `lusky3/awesome-stars`, `upstar.dev` and `boringproducts.xyz`
* Not readable from here: `github.com/hakopod/hakopod/releases` and `/security/advisories` (HTTP 403), the repository REST API and the GitHub advisory API (refused by this session), Reddit (HTTP 403)
