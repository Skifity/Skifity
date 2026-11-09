# Edka

> **Status.** Written on 2026-11-08 against Skifity's code at `23ec07f`. Edka's changelog
> was at 0.9.7 (2026-08-25), its documentation pages say "Last updated: Nov 5, 2026", and its
> CLI was at v0.10.0. Edka was read, not run: no account was created. Nothing named here has
> been changed in Skifity yet; the gaps below are proposals.

Edka (edka.io, "Apps, Databases & Agents on Infrastructure You Control") is a hosted control
plane that provisions and operates k3s clusters **inside the customer's own Hetzner Cloud or
AWS account**, then installs and runs what goes on them: deployments from an image or a
GitHub branch, cron jobs, managed PostgreSQL, MySQL, Valkey and ClickHouse, a catalogue of
apps, about twenty operators and controllers it calls add-ons, metrics, logs, alerts and
"agent runtimes" (Codex, Hermes, OpenClaw, T3 Code). It is for small teams that want
Kubernetes on cheap Hetzner hardware without a platform team; the founder's launch post
(Hacker News, 2025-08-15) says it was built "to make that part easy: spin up a production
ready cluster in ~2 minutes, then choose how low level or automated you want to go".
Since 0.9.6 (2026-08-12) a **satellite cluster** turns a server the customer already owns,
anywhere, into a single-node k3s cluster that Edka manages, by running one command on it.
**Architecture:** the control plane is Edka's own service (`console.edka.io`,
`api.edka.io`), closed source; at launch it was "TypeScript, React + Tailwind CSS,
PostgreSQL, Redis, BullMQ, Vault + AWS KMS to encrypted sensitive data" (HN, 2025-08-15), and
0.9.0 (2026-02-28) moved the cluster provisioner to Go. Each cluster is k3s with embedded
etcd, Flannel or Cilium, Debian 13 or Ubuntu 24.04/26.04 nodes, Envoy Gateway in front
(ingress-nginx is "deprecated" and kept for existing installs), cert-manager, and an
in-cluster `edka-agent` that reports health, evaluates alert rules, applies image updates and
feeds the cost inventory, plus an `edka-agent-node` DaemonSet that reads each node's package
list. Edka reaches a cluster it created through that agent, which "connects out to Edka",
and with tokens it signs that each API server accepts through an authentication-config
entry; a satellite is reached on its public port 6443 through an `edka-manager`
ServiceAccount. **Licence:** the platform is proprietary; the CLI (`edkadigital/cli`, Go) is
MIT and the community app catalogue (`edkadigital/apps`) Apache-2.0. **Pricing**
(edka.io/pricing, 2026-11-08; excluding VAT; infrastructure billed by the provider): Free —
one cluster of up to 24 vCPUs, one satellite, 5 apps, 4 databases, 2 agents, 5 deployments,
3 wildcard domains, 5 cron jobs, 5 alert rules, one user, daily cluster backups kept 7 days;
Standard €80 a month (€800 a year) — two clusters, five satellites, five users, HA control
plane, Metal pools, NAT gateway, 6-hourly and on-demand backups kept 30 days, a 99.5% SLA;
Pro €250 a month — four clusters, ten satellites, unlimited users, 90-day retention, chat
support; Enterprise from €500 a month — a dedicated Edka instance and **SSO/SAML**. Extra
clusters are €60 a month, extra satellites €10. Paid plans have a fair-use ceiling per
cluster (50 apps, 50 databases, 200 deployments, 100 alert rules). The prices changed since
launch: a commenter's arithmetic in August 2025 put Standard at €20 a month, and the FAQ now
explains "Legacy" plans that keep their old price. **Maturity:** 0.1 on 2024-12-14 ("Internal
testing and feedback"), still 0.x two years later, "beta" in the launch post; satellites are
labelled beta. **Company:** Edka Digital S.L., Spain; founder Camil Blanaru, who at launch ran
it "as a freelancer registered in Spain" and "in my spare time"; a co-founder posts on HN as
`victor_edka` (2026-08-10). **Adoption:** the Show HN reached 437 points and 130 comments
(2025-08-15); the homepage names two customers with vendor-reported savings (Aicole, 64% off
an Azure bill; Troi, 72%); SadServers wrote on HN (2025-10-05) that it moved to "Hetzner +
Edka and it's an order of magnitude cheaper". No user or cluster count is published. The
public repositories are small: `cli` 1 star, `apps` 1, `agent-profiles` 2, `startmeup` 2
(GitHub search API, 2026-11-08). The status page reports 100% for the website and the API and
99.995% for the console from August to November 2026.

## Feature inventory

### What shipped, release by release

Dates from edka.io/changelog. The changelog stops at 0.9.7; several features the
documentation describes on 2026-11-05 are newer, and their dates come from the pull requests
that added their routes to the CLI, which is said where it applies.

| Release | Date | What changed that matters for a comparison |
|---|---|---|
| 0.1 | 2024-12-14 | Initial release, internal testing |
| 0.2 – 0.3 | 2025-01-14 – 03-09 | Vault for secrets, the first add-ons, dark mode |
| 0.4 | 2025-05-04 | GitOps with GitHub, registry integration, the StartMeUp sample app |
| 0.5 | 2025-07-14 | Apps (HA PostgreSQL, Elasticsearch, Redis); deployments with autoscaling, automatic image updates and rollback |
| Show HN | 2025-08-15 | 437 points, 130 comments |
| 0.6 | 2025-12-10 | MySQL on the MOCO operator, failover, binlog point-in-time recovery |
| 0.7 | 2026-01-09 | **GitHub branch deployments built with Railpack, PR previews, wildcard TLS** |
| 0.9.0 | 2026-02-28 | Provisioner rewritten in Go; node pools to 500 nodes; secrets encrypted at rest; encrypted volumes; in-cluster Zot registry; IP allowlists for SSH and the API; cron jobs; persistent cluster events; organizations; passkeys and sign-in history |
| 0.9.1 – 0.9.2 | 2026-03-14 – 03-21 | Envoy Gateway; Tailscale exposure |
| 0.9.3 | 2026-04-14 | **Encrypted cluster backups** with in-place and new-cluster recovery; a databases section with pooling, PITR and insights; NATS queues; the cost inventory |
| 0.9.4 | 2026-04-29 | Organization-wide notifications; PromQL alert rules and packs; **maintenance windows and silencing**; email, Slack and webhook channels; Cloudflare Zero Trust; MetalLB |
| 0.9.5 | 2026-06-19 | **Hetzner Metal pools** over a vSwitch; NAT gateways with a stable egress IP; the **Explorer**; drift detection and repair; agent runtimes; **step-up passkeys for dangerous operations**; self-hosted GitHub Actions runners; per-node metrics; Longhorn; adopting existing CronJobs |
| 0.9.6 | 2026-08-12 | **Satellite clusters (beta)**; Codex environments and the Conductor; deployments with several domains, command overrides and **node pool pinning** |
| 0.9.7 | 2026-08-25 | One-click DNS records through Domain Connect |
| (CLI #1 – #46) | 2026-10-03 – 11-07 | The open-source CLI and the community catalogue; DNS-01 with a Cloudflare token (#23, 10-12); **node OS update rounds** (#33, 10-15) and **vulnerabilities** (#35, 10-16) reach the CLI's route catalogue; **GitHub Actions trusts** (#41, 10-23); sign-ins **bound to the device's TPM or Secure Enclave** (#43, #45, 11-03), v0.10.0 |

### Deploy sources and builds

* **Three workload kinds:** a container image, a "branch deployment" built from a GitHub
  branch or tag, and a Kubernetes CronJob. Branch deployments are "only supported for GitHub
  repositories at the moment" (docs, 2026-11-05); GitLab was promised in August 2025.
* Builds use **Railpack** or a Dockerfile, with build arguments and Railpack install, build
  and start overrides, and push to the cluster's default registry (the in-cluster Zot or
  GHCR, Docker Hub, ECR, GAR or another). Tags by commit SHA, branch, timestamp or a pattern.
  Builds run in the cluster (an `edka-builds` namespace); images Edka builds carry SBOMs.
* **Automatic image updates** for image deployments and cron jobs: the agent checks the
  registry from every minute to every 24 hours and applies a newer tag under a **version
  policy** — patch only, minor, major, all including pre-releases, the current tag pinned by
  digest (`repo:tag@sha256:…`), or a `glob:`/`regexp:` pattern. A suffix such as `-alpine` is
  kept, an update never moves down, a tag without an image for every node's architecture is
  skipped, and **Require approval** holds a new version until somebody applies or skips it.
  A Versions tab records each image change and who approved it; restoring an earlier one
  turns automatic updates off. The check runs inside the cluster, "so updates continue while
  Edka is unreachable".
* Variables and write-only secrets, imported from `.env`, JSON or YAML; Secrets that already
  exist in the namespace (from External Secrets, Helm or kubectl) can be added whole.
  **Restart pods when a Secret changes** rolls the deployment when a value changes.
* Rollback restores a revision from the deployment's menu; the CLI mentions failed changes
  "that Edka rolled back". No release command, health-check setting, worker process or
  scale-to-zero was found in the deployment documentation.
* `edka up --diff` compares a change with the deployment and sends nothing, listing variables
  added, changed and removed, secrets by name only, and fields that would be ignored or
  refused; `--expected-generation N` refuses the change if the deployment has moved on.

### Domains, TLS and routing

* Domains belong to a cluster: **wildcard** (`*.example.com`, optionally with the apex) or a
  single hostname, bound to a "traffic class" — a public or private Envoy Gateway class, a
  MetalLB private address, a Tailscale tailnet, or the legacy ingress.
* Certificates by cert-manager: HTTP-01 for a public hostname, or **DNS-01 by delegation** —
  a CNAME from `_acme-challenge.example.com` to `<id>.acme.edka.net`, so no DNS credential sits
  in the cluster — or with a Cloudflare API token, which Edka tests by writing and deleting a
  TXT record before accepting it. **One-click DNS** through Domain Connect (0.9.7) creates the
  routing and delegation records at a supporting provider such as Cloudflare.
* Per gateway class: a request timeout, a **local rate limit** and a **CIDR allowlist**; one
  Hetzner load balancer per class. **TCP ports** publish raw TCP (SMTP, MQTT) on a class's
  address, optionally with **PROXY protocol v2**; five ports per Hetzner load balancer.
* The Gateway page shows, per class and per route, requests per second, p50/p95/p99, 4xx and
  5xx shares, healthy backends and days to certificate expiry, and **searches Envoy's access
  log** by host, path and status, ranking paths by 5xx and explaining Envoy's response flags
  (`RL`, `UAEX`, `UT`, `UH`).
* Private reachability: Tailscale classes and per-workload tailnet hostnames, MetalLB VIPs on
  the private subnet routed to Cloudflare WARP users by a Cloudflare connector, and a **NAT
  gateway** (single or active/standby) that gives private workers one stable outbound IPv4
  for third-party allowlists, blocking cloud metadata and SMTP port 25 by default.

### Databases and services

* Four engines, each on an operator: **PostgreSQL** (CloudNativePG; 1, 3 or 5 instances with
  synchronous replication; a **PgBouncer pooler**; extensions from a catalogue and, on
  PostgreSQL 18 with Kubernetes 1.35, **extension images** such as PostGIS; import from any
  reachable PostgreSQL with `pg_dump` or a clone with `pg_basebackup`; minor upgrades by
  switchover and **major upgrades with a backup before and after**), **MySQL** (MOCO; 1, 3 or
  5 instances, switchover, binlog PITR), **Valkey** (standalone or replicas with Sentinel) and
  **ClickHouse** (Keeper, resource profiles).
* Tabs per database: Overview with a connection-string builder, Insights (PostgreSQL query
  statistics and sessions), Pooler, Logs, Users & DB, Backups, Settings. Deleting asks for
  `delete me` typed out.
* **External access** on a public load balancer, a private one or the tailnet, under a
  hostname Edka owns (`orders-0a1b2c3d.e2db.net`) with a Let's Encrypt certificate the
  database serves itself, so clients verify it with their system trust store
  (`sslmode=verify-full sslrootcert=system`).
* **NATS with JetStream** as a "Messaging" workspace: accounts, streams, consumers.

### Storage and backups

* On Hetzner, persistent volumes are **Hetzner Cloud Volumes** (`hcloud-volumes`, and an
  encrypted class); satellites use k3s local-path; Longhorn is an add-on. **Encrypted local
  storage** splits each node's disk and keeps a LUKS2 partition whose key Edka holds and
  sends after every boot, with a recovery key sealed to the customer's age key.
* **Database backups** go to the customer's S3, S3-compatible or GCS bucket: PostgreSQL base
  backups daily at 00:00 UTC plus continuous WAL (retention 1–35 days) through the Barman
  Cloud plugin, MySQL snapshots and binlogs, Valkey RDB snapshots, ClickHouse full snapshots.
  They are gzip-compressed and rely on the provider's server-side encryption; nothing found
  encrypts them on the client. **A restore always creates a new database**; a PostgreSQL
  restore needs the source database to still exist ("After you delete a database, its backups
  stay in the bucket but Edka can no longer restore them").
* **Cluster backups**: each set is an etcd snapshot, the k3s PKI bundle and restore metadata,
  AGE-encrypted before upload to Edka-managed storage, with a platform key or the customer's
  own public key (the private key is used for the operation and not stored). Daily on Free,
  every 6 hours and on demand on paid plans (hourly on Pro, per the 2026-08-07 blog), kept
  7/30/90 days. A catalogue across the organization flags clusters with no backups or an
  overdue one, and offers **Roll back** in place (also the recovery for a lost single control
  plane node), **Recover** a deleted cluster under its old name, and **Restore as new**. The
  blog post of 2026-08-07 deletes a control plane node from the Hetzner console and has the
  API back "about 90 seconds after the rollback starts". Not available on satellites.
* No backup of an app's own volumes was found outside the database engines and agent state.

### Scaling and high availability

* Deployments: replicas (0–100 from the CLI) and an HPA on CPU or memory. No scale to zero
  found.
* Clusters: a single or three-node control plane (one-way switch), an optional load balancer
  for the API, node pools with **cluster autoscaling** (also one-way), labels and taints, up
  to 50 nodes a pool on a private network and 500 with Cilium's host firewall. **Metal pools**
  attach Hetzner Robot dedicated servers as workers over a vSwitch (wiped on first provision,
  mirrored or separate disks); a pool of **existing Hetzner Cloud servers** is never deleted
  by Edka. AWS clusters run k3s on EC2 (not EKS) through a CloudFormation role.
* The Explorer's node view has **Cordon, Drain and Reboot**; Drain first lists the pods it
  will evict and **checks whether they fit on the other nodes** — requests, node selectors,
  required affinity, taints, node-bound volumes, anti-affinity, the pod limit — and a
  **Failover** figure says, for the worst node, how many pods would stay Pending if it went
  down.
* **Node OS updates** (docs, Settings → Maintenance; in the CLI from 2026-10-15): nodes
  install security updates daily, and **update rounds** drain, update, reboot and uncordon one
  host at a time — control plane first, a control plane node only while all are Ready, the
  active NAT gateway member last — waiting up to an hour for PodDisruptionBudgets, stopping
  the round when a node does not come back, resetting a node not Ready after 10 minutes once.
  Reboots are manual by default or automatic inside a maintenance window (Tuesday to Thursday
  03:00–06:00 in the location's time zone by default); a known-exploited CVE starts a round in
  the next window. Runs as Plans of Rancher's system-upgrade-controller. Not on satellites,
  "whose server you update yourself".

### Observability (logs, metrics, alerts, uptime)

* **Cluster overview**: health, a topology graph of gateways, pools, nodes and workloads with
  live traffic, capacity (used, requested, limits, allocatable), cost run rate, and up to four
  warnings each linking to its detail. **Diagnostics** reads the API directly: events,
  failing pods by reason, workloads not ready, every PVC fullest first (warning at 85%),
  **K3s signals** (certificate expiry, etcd snapshot failures, etcd leader, API latency) and
  **connectivity incidents** with what Edka found when it investigated.
* **Explorer** (the equivalent of Skifity's Advanced): live resources with a Diagnosis list
  per pod, workload, Service and PVC — a Service selector matching no pod, naming the one
  label that differs; a claim waiting for a pod — events, logs, YAML, Helm releases, and
  **exec or an ephemeral debug container** behind a passkey check.
* **Metrics and logs** in the cluster: VictoriaMetrics with a PromQL explorer (5 minutes to 7
  days) and VictoriaLogs with LogsQL, quick filters and an exclude filter, both installed and
  sized from the panel.
* **Resources**: every workload's CPU and memory over 7 days against its requests and limits,
  with **advised requests**, findings (OOMKilled, under-provisioned below 90% of p95, CPU
  throttled in 10% of periods, peak near limit, over-provisioned above 1.5 times the advice),
  and "apply" for Edka's own deployments.
* **Alerts**: PromQL rules with a threshold, a for-duration, "keep firing for", severity and
  routing labels, **tested against live data before saving**, evaluated by the in-cluster
  agent; **packs** for workloads, PostgreSQL, ClickHouse, Valkey, storage, cluster health, the
  gateway, host infrastructure, certificates, External Secrets and the NAT gateway.
* **Vulnerabilities** (CLI routes from 2026-10-16): CVEs in every node's OS packages, in the
  cluster's Kubernetes version, and in running images that carry an SBOM attestation, matched
  centrally against the Debian and Ubuntu trackers, the Kubernetes CVE feed, GHSA and OSV, with
  CISA KEV and EPSS; "Fixed by a reboot" is its own category.
* Inventory: every server, IP, volume and load balancer Edka created in the Hetzner account,
  with cost per month, current month and history.

### Security, auth, roles, SSO, audit

* Sign-in by email or Google, passkeys, sign-in history; **step-up passkey verification**
  lasting five minutes for dangerous operations — deleting a cluster, downloading root
  credentials, exec, Explorer changes, node operations, revealing secret values when the
  organization requires it. SSO/SAML only on Enterprise.
* Roles: organization owner, admin and developer; per cluster, a developer gets no access,
  read or write, and a **personal kubeconfig** to match (view, or view plus edit in listed
  namespaces). Changing someone's access revokes their kubeconfig on save. The cluster's SSH
  key and admin kubeconfig are "break-glass": owners only, passkey, downloadable once; **CA
  rotation** ends every admin kubeconfig downloaded before it.
* Secrets: write-only in forms; on the Secrets page owners and admins can **reveal values**,
  read from the cluster and audited per key. **External Secrets Operator** with stores for
  AWS Secrets Manager and Parameter Store, Google, Azure, Vault and OpenBao, Akeyless,
  1Password, Doppler, Infisical, Bitwarden, Scaleway and GitLab variables. Kubernetes Secrets
  are encrypted in etcd by default.
* What Edka holds: the cloud credentials (a Hetzner token with read and write on the project,
  per cluster or account-wide; an AWS role), the SSH key the nodes were created with, root on
  every node, and "the kubeconfig K3s wrote at install". The documentation describes how to
  cut Edka off: empty its entry in the API server's authentication config (Edka then stops
  acting and emails the owner), remove its SSH key from every node, rotate the CA.
* Agents get no Kubernetes API access by default; presets (read-only, write in namespaces,
  custom rules) bind built-in roles, and grants are audited.

### Preview environments and branches

* For a GitHub branch deployment: a preview per pull request targeting the branch, on a
  wildcard domain marked for previews, hostname `pr-{{pr_number}}-{{repo}}` by default,
  resource presets from 50m/64Mi, deleted after 1 to 720 hours (168 by default) and on close.
  `edka previews list|status|logs|open|delete`. Only the deployment is previewed; nothing about
  databases or other services of the environment was found.

### Templates and catalogue

* About fourteen apps (Buzz, Elasticsearch, Excalidraw, Langfuse, Meilisearch, Metabase,
  Multica, n8n, OpenSearch, Plausible, kube-prometheus-stack, Strapi, Typesense, Umami) plus
  the database engines and VictoriaMetrics/VictoriaLogs. Each declares the add-ons it needs,
  which Edka installs first.
* **Custom apps** (October 2026): a package is a `template.yaml`, a small Helm chart, a README
  and test answers. `edka apps init` writes one that passes validation; `edka apps validate`
  reports **file, line, rule and fix**; `publish` puts it in the organization's catalogue;
  versions are immutable; **Review update** lists what an update changes (images, kinds of
  objects, endpoints, storage, databases, secrets, settings). `edka apps share` opens a pull
  request to `edkadigital/apps`, which a chart check and an install on a fresh cluster gate;
  community apps must pin images by digest, stay in one namespace and meet the *baseline* Pod
  Security Standard, and are withdrawn when unmaintained or insecure.

### CLI, API, IaC, integrations

* `edka` (Go, MIT): `login` (browser OAuth with PKCE, read-only consent possible), `link`
  (writes `.edka.json` with IDs only), `up`, `build`, `logs`, `env`, `restart`, `scale`,
  `rollback`, `diagnose`, `doctor`, `clusters`, `nodepools`, `domains`, `apps`, `addons`,
  `databases`, `cronjobs`, `api` for any endpoint, `--json` with exit codes, completion,
  profiles. **`edka diagnose`** reads the rollout, pods, events, last revisions and the
  failing pod's previous log, and prints each problem with the commands to run next; apps,
  previews, databases and cron jobs have their own. Secrets are refused on the command line.
* Credentials go to the system keyring, and each sign-in is **bound to a key in the TPM or
  Secure Enclave** (DPoP, RFC 9449), so copied credentials work nowhere else.
* **GitHub Actions trusts**: a job's OIDC token, limited to a repository, refs, environments
  and a workflow file, is exchanged for a 15-minute token that may only publish and update one
  custom app; no secret is stored in the repository.
* **Remote MCP** at `https://api.edka.io/mcp/platform` with OAuth and a read/write consent,
  for claude.ai, Claude Code, Codex and others; it reads clusters, deployments, databases and
  notifications (without configuration values or credentials) and writes custom apps,
  installs and Codex environments. **`edka skill install`** writes Edka's skills for every
  coding agent it finds and refreshes them on upgrade.
* GitOps through the Flux Operator; self-hosted GitHub Actions runners (ARC scale sets).

### Notifications

* An in-app feed with severity (critical, warning, info) and category, per-user read state,
  action links ("Open diagnostics"), active and resolved history kept 7/30/90 days.
* Channels: **email, Slack and webhook**, each filtered by severity, category, cluster,
  triggered or resolved, and labels, with a test button.
* **Maintenance windows** hold deliveries for everything, some clusters or chosen alerts,
  15 minutes to a week, optionally by label; any active notification can be silenced alone.
* **Outage emails without a channel**: after an hour of an unreachable API, at 24 hours, and
  on reconnection, to the cluster's owner.

### Multi-server and networking

* Several clusters per organization (by plan), each in one Hetzner location (Nuremberg,
  Falkenstein, Helsinki, Ashburn, Hillsboro, Singapore) or AWS region; GCP, Scaleway,
  DigitalOcean and OVH are "coming soon". A private network by default (100 nodes), or
  public networking with Cilium's host firewall and encryption. SSH and the API are closed to
  everybody but Edka until the owner lists addresses.
* **Satellites**: Ubuntu 22.04–26.04 or Debian 12–13, x86_64 or arm64, 2 GB and 20 GB free,
  **a public IPv4 with 6443, 80 and 443 free and 6443 open to the internet**; the installer
  refuses a machine with another Kubernetes, probes pod egress and prints the fix for a
  stateless provider firewall. The script is static, published with its SHA-256, and takes a
  one-time token valid 60 minutes through environment variables. One server per satellite;
  no cluster backups, no managed k3s upgrades, no NAT gateway or node pools.

### Team and collaboration

* Organizations (several per account), invitations, owner/admin/developer, per-cluster
  access; one user on Free, five on Standard, unlimited on Pro. Login history and audit logs.

### Developer experience and onboarding

* Sign up, paste a Hetzner token, choose a location and sizes; the cluster is up "in about 2
  minutes" with progress events. Add-ons are mostly installed for the user when an app,
  database or gateway needs them. The documentation is long, precise and dated per page; the
  troubleshooting sections name kubectl commands. Site and docs are English only.

## What users love

* **Focus and a GUI over Terraform.** "Love how focussed this is" (HN, 2025-08-15). "Feels way
  smoother than Terraform templates or CLI tools" (HN, 2025-08-18), from a commenter who
  otherwise recommended a managed alternative.
* **Hetzner prices with somebody else's platform work.** "We at SadServers moved from big
  cloud managed K8s to Hetzner + Edka and it's an order of magnitude cheaper (obv some perks
  are missing)" (HN, 2025-10-05); Aicole's 64% and Troi's 72% savings are Edka's own figures.
* **Leaving is possible.** Workloads, databases and data stay in the customer's account: "Edka
  is not a hosted database service. Disconnect Edka and the databases keep running" (docs).
* **A responsive founder.** On launch day he fixed a typo, a bounced support address and the
  legal pages within hours, and answered most questions in the thread.
* Reddit could not be read from this sandbox, and no other discussion was found, so this list
  rests on one HN thread and a few later comments.

## What users complain about

All from the launch thread (HN, 2025-08-15 to 2025-08-23) unless dated otherwise.

* **Trust.** "I can't find this Spanish (?) company in the company register ... Not very
  trustworthy for a SaaS that stores your data and access keys"; "For me it does not pass the
  smell test. No physical address, no idea who is running it". The founder answered with his
  VAT number and updated the policies the next day.
* **Pricing that leaves out the servers.** "The pricing is misleading ... you still have to pay
  for the control plane nodes", and high availability needs three of them, not two.
* **Free tools do the same.** Several commenters pointed to `hetzner-k3s`, the
  `kube-hetzner` and `hcloud-k8s` Terraform modules, kops and Talos, and four recommended
  Syself instead ("I'm going to second the Syself recommendation").
* **Stuck provisioning with nothing to go on.** "The cluster status kept flipping between
  Failed and Creating with no clear way of troubleshooting it"; another user added, of Hetzner
  itself, "since July, 1-2 of 10 deployments will stay stuck in creating state". Hetzner had a
  partial outage that day.
* **A dependency on AWS** for secrets on a Hetzner product ("if you put secrets on AWS, you are
  STILL dependent on AWS"), **no self-hosted version** ("is there a selfhosted version of
  this ?" — still none outside the Enterprise "dedicated instance"), and **GitLab** asked for
  and promised; branch deployments are still GitHub only in November 2026.
* **Bare metal** was asked for three times; Metal pools answered it in 0.9.5 (2026-06-19).
* **Operating nodes was manual.** "For OS updates, you can replace the nodes" (founder,
  2025-08-15); update rounds arrived about fourteen months later.
* Satellites are beta and say what they do not do: no cluster backups, no managed k3s
  upgrades, and a server the user patches.

## Security record

* **No published vulnerability.** NVD returns nothing for "edka" (2026-11-08); OSV has nothing
  for `github.com/edkadigital/cli`. The GitHub Advisory Database could not be queried from
  this sandbox. The CLI has a `SECURITY.md` with private reporting; the platform is reported to
  `security@edka.io` with a PGP key and a safe-harbour statement.
* **The security page** (updated 2026-11-05) states a shared-responsibility model, AES-256 at
  rest for stored cloud credentials "in a dedicated secret storage system", TLS 1.3, EU data
  location, nine sub-processors (Hetzner, Cloudflare, Google Cloud, AWS, Paddle, Resend,
  Sentry, GitHub, Google Workspace), GDPR, and "ISO 27001:2022 — certification in progress".
  At launch the founder said "internal penetration tests were conducted".
* **Hardening that shipped in 2026:** passkeys and sign-in history (0.9.0), step-up passkeys
  for dangerous operations and "hardened sessions and tenant isolation" (0.9.5), "identity
  changes now require credential proof" (0.9.6), break-glass credentials downloadable once,
  CA rotation, CLI tokens bound to the device (v0.10.0, 2026-11-03), GitHub OIDC trusts
  instead of stored tokens, SSH and the API closed by default, and a documented way to cut
  Edka off a cluster.
* **Exposure by design.** A compromise of Edka's control plane is a compromise of every
  customer's cloud project: it holds read-and-write Hetzner tokens (or an AWS role), the SSH
  key that is root on every node, and the install-time admin kubeconfig, and it can sign
  tokens every API server accepts. A satellite keeps its Kubernetes API open to the internet
  so Edka can reach it. Encrypted local storage depends on Edka to unlock every node after a
  boot. Database backups are not encrypted on the client. These are the costs of the model
  `docs/research/competitors.md` ("Renting the panel out") describes as making the vendor "the
  highest-value target in the system".

## Against Skifity

Edka and Skifity sell the same thing to the same person — k3s on cheap servers, a panel
instead of a platform team — in opposite shapes: Edka is a service that operates clusters in
the customer's account, Skifity is the panel installed on the customer's own server. Skifity
statuses come from `docs/progress.md`: the one-server path runs on a real k3s on every push to
`main`, and two servers in the `Two servers` workflow. Edka was read, not run.

| Capability | Edka | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Sources | Image, GitHub branch or tag, CronJob; GitHub only | Git on GitHub, GitLab, Gitea and Bitbucket with webhooks, image, folder upload, a Compose file as a stack (`internal/gitsrc`, `docs/concepts.md:913`, Phase 101) | Skifity ahead |
| Builds | Railpack or Dockerfile, in the cluster, SBOMs | Railpack, Nixpacks or Dockerfile with BuildKit in the cluster (`internal/builder/detect.go:22-26`) | parity |
| Following new image versions | Policies, digest pin, approval, architecture check, history, notifications | None for image apps; a template's newer image arrives only with a panel release (`docs/templates.md:94-98`) | **behind** |
| A change shown before it is sent | `--diff`; `--expected-generation` refuses a stale change | `skifity up --dry-run`, `skifity plan` (`docs/cli.md:131,195`); no version on an app to refuse a stale save | parity; behind on stale saves |
| Release command, workers, health path | Not found | Release command, processes, health path (`docs/concepts.md:597`, Phase 106) | Skifity ahead |
| Rollback | A revision, automatic on a failed change | Old image and settings, refused when the image is gone (`docs/concepts.md:430-456`) | parity |
| **Domains, TLS and routing** | | | |
| Certificates | HTTP-01; DNS-01 by CNAME delegation or Cloudflare token; wildcards | HTTP-01, a team's own certificates, records at four DNS providers (`internal/tlscert`, `internal/dnsprov`); "Wildcard certificates ... That is not built" (`docs/concepts.md:421-426`) | **behind** on wildcards |
| DNS records | Domain Connect, or by hand | Created at Cloudflare, Route 53, Hetzner, DigitalOcean with a token (`internal/dnsprov`) | parity |
| Edge rules | Timeout, rate limit and CIDR allowlist per gateway class | Rules on IP, country, ASN, path and header, rate limits, maintenance page, a password in front of an app (`internal/edgerules`, `internal/guard`, Phases 75, 98) | Skifity ahead |
| Private apps | Tailscale, MetalLB with WARP, NAT gateway egress IP | Cloudflare tunnel (`internal/cluster/tunnel.go`); an app with a domain is public | behind |
| Raw TCP | TCP on a class address, PROXY protocol | TCP and UDP on every server, outside the firewall (`internal/kube/ports.go:13-21`) | parity, different |
| Traffic | Per class and route: rate, p50–p99, 5xx, backends, certificate days; access-log search | Requests, errors and response times per app from Traefik (`internal/traffic`); no per-request view | parity; behind on access logs |
| **Databases and services** | | | |
| Engines | PostgreSQL, MySQL, Valkey, ClickHouse on operators, 1/3/5 instances | Nine engines; PostgreSQL replicated through CloudNativePG, the rest single (`docs/databases.md:10-34`) | ahead on breadth |
| PostgreSQL depth | Pooler, WAL and PITR, extensions, import or clone, major upgrade with backups | Scheduled dumps, `skifity db import`, no pooler, PITR or version change (`docs/backups.md`, `docs/databases.md`) | **behind** |
| Restore | Always a new database | Replaces the live database (`docs/backups.md:165-171`) | behind |
| From outside | Public or private address, verified TLS under `e2db.net` | `skifity db connect` through the panel; "a database has no address outside the cluster" (`docs/databases.md:100-106`) | different; Skifity closed by default |
| **Storage and backups** | | | |
| Volumes | Hetzner Volumes, encrypted class, local-path, Longhorn, LUKS local storage | local-path, Longhorn optional (`internal/settings/settings.go:604`); a volume is tied to its server (`docs/adding-servers.md:365-375`) | behind on Hetzner |
| App and database backups | Databases only, provider-side encryption | Databases and volumes on schedules, sealed with a passphrase, verifiable (`internal/backup`, Phase 104) | Skifity ahead |
| Cluster state | etcd and PKI sealed off-site; roll back, recover, restore as new | Panel database to the bucket (Phase 84); etcd snapshots on the same disk, "Skifity does not do that for you" (`docs/backups.md:258-284`) | **behind** |
| **Scaling and high availability** | | | |
| Instances | HPA on CPU or memory | HPA, scale to zero through KEDA, a readiness checker (`internal/kube/scaletozero.go`, `internal/deploy/scaling.go`) | Skifity ahead |
| Servers | Node pools, cluster autoscaler, 500 nodes, Metal pools | Any server over SSH, Hetzner and DigitalOcean ordering, promote, three control planes (`internal/provision`, `internal/cloud`); no autoscaler | behind on cloud pools; ahead on any server |
| Taking a server out | Drain checks where the pods fit; Failover figure per node | Removal checks node-tied volumes only (`docs/adding-servers.md:365-399`); the summary has totals and an HA flag (`internal/api/ports.go:141-156`) | **behind** |
| OS patching | Daily updates; rounds of drain, update, reboot, uncordon in a window | Reports whether updates are automatic and a reboot is waiting (`internal/provision/hardening.go:65,85,254`); no action on a server (`internal/api/api.go:424-437`) | **behind** |
| Placement | Pin a deployment to node pools | No `nodeSelector` outside GPUs (`internal/kube/gpu.go`) | behind |
| **Observability** | | | |
| Metrics and logs | 7 days in VictoriaMetrics, PromQL; retained logs in VictoriaLogs | 72 hours of app and server samples (`internal/watch/usage.go:18`); live logs; eight log-drain kinds (`internal/logdrain`) | behind on retention; ahead on drains |
| Sizing | Advised requests from 7 days, OOM and throttling findings, apply | Requests set by hand (`internal/kube/manifests.go:592-617`); no advice | **behind** |
| Alerts | PromQL rules, tested before saving; packs | Thresholds per app and server (`internal/watch`, Phases 95, 99) | behind on custom rules |
| Cluster health | K3s signals, PVC fullness, connectivity incidents | Server usage and disk (Phase 99), drift (`internal/watch/drift.go`); nothing on k3s certificates, etcd or its snapshots (`internal/watch/`) | behind |
| Diagnosis | `edka diagnose` per object with next commands; Explorer diagnosis | `events --warnings` explained (`internal/kube/explain.go`), `status --explain` (`internal/cli/commands.go:146-154`) | behind (small) |
| Vulnerabilities | Node packages, Kubernetes version, images with an SBOM; KEV, EPSS | Trivy on every app's image, daily, a gate on fixable criticals (`internal/vulnscan`, `docs/concepts.md:697-790`) | ahead on images; behind on servers and k3s |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in | Email or Google, passkeys, step-up passkeys on dangerous operations; SSO only Enterprise | Argon2id, TOTP, passkeys, OIDC on every install (`internal/auth`); re-authentication only for account security and the master key (`internal/api/api.go:306-330,627-628`) | ahead on SSO; behind on step-up |
| Roles and kubectl | Owner, admin, developer; per-cluster read/write; personal kubeconfigs | Owner, admin, member, viewer, projects (Phases 85–86); no kubeconfig for members | different by decision |
| Secrets | Write-only forms; owners can reveal; 11+ stores through ESO | Write-only, sealed per row; four managers (`internal/secretmgr`) | ahead on secrecy; behind on providers |
| Who holds the keys | Edka: cloud tokens, node SSH key, admin kubeconfig | The owner's own server; the master key never leaves it (`docs/backups.md:286-293`) | Skifity ahead |
| CLI and CI sign-in | Browser OAuth, DPoP-bound; GitHub OIDC trusts | A pasted token, scoped and expiring (`docs/cli.md:38-75`, `internal/auth/scopes.go`) | **behind** |
| **Preview environments** | The deployment only, GitHub, presets, 1–720 hours | The whole environment, seeded, with a lifetime and fork rules (Phase 107, `docs/configuration.md:64`) | Skifity ahead |
| **Templates and catalogue** | About 14 apps; custom apps with validate, publish, community review | 374 templates (`internal/templates/catalogue`), team catalogues checked on load (`docs/templates.md:212-228`), updates with a backup first | ahead on size; behind on authoring tools |
| **CLI, API, IaC, integrations** | | | |
| CLI and IaC | Open-source CLI, `link`, `--json`, exit codes, `api`; Flux | One binary for panel, CLI and MCP; `skifity.yaml` plan and apply (`internal/cli`, `internal/blueprint`) | parity; ahead on IaC |
| MCP | Remote with OAuth, narrow tool set | 48 tools, stdio and `/api/mcp` with a bearer token (`internal/mcpserver`, `internal/api/mcp_handlers.go:35-61`) | ahead on tools; **behind** on sign-in |
| Agent skills | `edka skill install` | None | behind |
| **Notifications** | Email, Slack, webhook; feed; filters; silences and windows | Ten channels, fourteen events (`internal/notify/notify.go`); no silences | ahead on channels; **behind** on silences |
| **Multi-server and networking** | | | |
| Where it runs | Hetzner Cloud, Hetzner Robot, AWS; a satellite on any one server | Any Linux server over SSH, mixed, WireGuard between them (`internal/provision`) | ahead on mixed servers; behind on cloud integration |
| Several clusters | Per plan | One panel, one cluster, by decision (`docs/roadmap.md:155`) | different |
| **Team and collaboration** | Organizations; 1, 5 or unlimited users by plan | Teams, invitations, projects, SSO groups, no seat limit (Phases 85, 86, 92) | Skifity ahead |
| **Developer experience** | | | |
| First cluster | About two minutes from a Hetzner token; or one command on a server | One command on a server, a setup token, an `sslip.io` address (`docs/quick-start.md`) | parity |
| Requirements | Satellite: 2 GB, 20 GB, public IPv4, 6443 open | 2 GB, 8 GB of disk (`docs/quick-start.md:9-11`) | parity |
| Price and languages | Free tier, then €80–€500+ a month; English | Free; five languages (`web/src/locales`) | Skifity ahead |

### Claims in this repository that no longer hold

* `docs/research/competitors.md`, "Where Skifity is actually different", point 1: "None of
  them puts Kubernetes on one small server with one command." Edka's satellite does exactly
  that, on 2 GB, since 2026-08-12. The claim still holds for a **self-hosted** panel: the
  satellite is useless without Edka's service, which runs elsewhere and holds the access.
* The same file's "Renting the panel out" lists a hosted control plane with an agent that dials
  out as option 3, "more work, better reach", and none of it built. Edka is that option, built:
  an outbound agent on cloud clusters, signed per-cluster tokens, a documented way to revoke
  them. Its satellites show the cost the section predicts — a public 6443 — and its security
  page shows what a customer then has to trust.

## Gaps worth closing in Skifity

### P1 — Patch servers one at a time, inside a window

* **What:** on the Servers page, each server's pending updates and pending reboot (the
  hardening check already reads both), and **Update and reboot**, for one server or a round:
  cordon, drain, install updates, reboot, wait for Ready, uncordon, one server at a time, a
  control plane server only while the others are Ready, the panel's own server last, the round
  stopped when a server does not come back. Later, an optional window in which a round starts
  by itself.
* **Evidence:** raised in `heroku.md` (P1) and still half open there: the hardening report
  says whether automatic updates are on and whether a reboot is waiting
  (`internal/provision/hardening.go:65,85,254`), and nothing applies either — the server routes
  are get, rename, remove, retry, promote, usage, alerts and hardening (`internal/api/api.go:424-437`).
  Edka's rounds show the details worth copying: wait for disruption budgets (up to an hour),
  skip a server whose pods have nowhere to go and say which, reset a server once if it is not
  Ready after ten minutes, keep a server cordoned if it comes back without internet, and count
  the CVEs each reboot fixes. Its own users asked for this in August 2025 and got it fourteen
  months later.
* **Fit:** Edka runs its rounds as Plans of Rancher's system-upgrade-controller, which Skifity
  already installs to upgrade k3s (`internal/cluster/upgrades.go:33-38`,
  `internal/kube/k3supgrade.go`): a Plan with concurrency 1, `cordon`/`drain` and a script that
  runs the distribution's upgrade and reboots when `/var/run/reboot-required` exists. The panel's
  own server is the hard case — the panel stops while it reboots — so it goes last, and a single
  server says so before it starts, as the k3s upgrade already does
  (`internal/kube/k3supgrade.go:205`). The fit check in the next gap decides the skips.
* **Size:** L. **Without a cluster:** the ordering, skip and stop rules against the fake
  clientset; the reboot itself needs the `Two servers` workflow.

### P1 — The cluster's own state off the server, sealed, with a way back

* **What:** copy each etcd snapshot k3s takes, with the server token and the k3s CA files, to
  the backup bucket, sealed like every other backup; list them on Settings → Backup storage;
  and turn the restore in `docs/backups.md` into a command (`skifity admin restore-cluster`) that
  checks the copy and prints what it will do before it does it.
* **Evidence:** "They are a copy on the same disk ... they do not save you from losing the
  server, which is what copying that folder somewhere else is for. Skifity does not do that for
  you", and the restore "has not been run against a real cluster" (`docs/backups.md:258-284`).
  Raised as gap 12 in `zeabur-oss.md`. Edka uploads each set encrypted to a key the
  customer can own, restores in place or as a new cluster, and demonstrates a lost single
  control plane node back in about ninety seconds (blog, 2026-08-07). The panel database is
  already backed up off the server (Phase 84); without etcd and the CA, a restored panel on a
  new server faces an empty cluster, and the troubleshooting page's "If the old server is gone"
  (`docs/troubleshooting.md:604-608`) stops at the panel.
* **Fit:** the snapshots are already explicit (`internal/provision/scripts.go:568-577`,
  `installer/install.sh:1563-1575`). k3s can upload them to S3 itself (its `etcd-s3` settings),
  but that puts the bucket's keys in the cluster — in k3s's configuration or a Secret in
  `kube-system` — and uploads them unsealed; a Job on the control plane node using the
  existing presigned-URL path (`internal/backup`), sealed with the backup passphrase (Phase
  104), keeps Skifity's rule that storage credentials stay in the panel. Secrets inside a snapshot are encrypted
  (`--secrets-encryption`, `installer/install.sh:1672`), so the snapshot is useless without the
  server token and the encryption key: they are the sensitive part, sealed with the master key
  as well as the passphrase, and never uploaded apart from a snapshot.
* **Size:** M. **Without a cluster:** the upload and sealing yes; the restore needs the
  `Cluster` workflow to prove it, which is the point.

### P1 — Follow an image's new versions by a rule, with approval

* **What:** for an app that runs an image, and every template app, a policy — patch only,
  minor, major, a moving tag by digest, or a pattern — that finds a newer tag, shows "1.4.2 →
  1.5.0" on the app with **Apply** and **Skip**, and optionally applies it alone; template apps
  keep "back up first".
* **Evidence:** `portainer.md` P1, "Tell people when a newer image exists", still open
  ("noticing a newer image in the registry" in its since-section); template updates arrive "when
  an upgrade of the panel brings a newer version" (`docs/templates.md:94-98`). Edka goes further
  than Portainer's indicator: a suffix such as `-alpine` is kept, an update never moves down, a
  tag without an image for every server's architecture is skipped, approval is recorded, and a
  rollback turns the automation off so it does not undo the rollback.
* **Fit:** a tag lister beside `internal/registry` that reaches outside only through
  `internal/netguard`, using the app's pull credentials; the series logic of
  `hack/resolve_tags.py` ported to Go; a deploy through `internal/deploy` like any other, so a
  failed start rolls back and the lock (Phase 94) is respected; new events `image.available` and
  `image.updated` for `internal/notify`.
* **Size:** M. **Without a cluster:** yes, against a fake registry.

### P2 — Say where a server's instances would go, before it goes

* **What:** before removing, draining or patching a server, list the instances that would not
  fit anywhere else and why (memory, CPU, a volume on that server, a GPU); and on each server, a
  "losing this server" line: how many instances would be left waiting.
* **Evidence:** removal checks only for volumes tied to the server
  (`docs/adding-servers.md:365-375`); the cluster summary has totals and a high-availability flag
  (`internal/api/ports.go:141-156`), and `get_cluster_status` says whether the cluster "survives
  losing one" without saying which server matters. Edka's drain dialog checks requests, node
  selectors, taints, node-bound volumes, anti-affinity and the pod limit, and its Failover figure
  names the worst node.
* **Fit:** a pure function over nodes and pods in `internal/cluster` with first-fit by requests,
  shared by removal, the patch rounds above and the server page.
* **Size:** S. **Without a cluster:** yes.

### P2 — Advice on an app's requests, from what it used

* **What:** on the app's Overview, "reserves 1 GB, peaked at 180 MB" with an advised request
  and **Apply**, and findings for an instance killed for memory, CPU throttled, or no history yet.
* **Evidence:** Skifity keeps 72 hours of each app's usage (`internal/watch/usage.go:18`) and
  sets requests from the app's settings (`internal/kube/manifests.go:592-617`), and gives no
  advice. `sealos.md` (P2) and `porter.md` mention a right-sizing hint; Edka ships one with plain
  rules: under-provisioned below 90% of the p95, over-provisioned above 1.5 times the advice, no
  advice under an hour of history.
* **Fit:** a pure function over `app_samples`; Apply is a settings change, which does not
  rebuild (ADR-0007).
* **Size:** S. **Without a cluster:** yes.

### P2 — Watch k3s itself

* **What:** warn before the k3s certificates expire, when an etcd snapshot has not been taken on
  schedule, and when etcd has no leader; one line each on the Servers page and an event for the
  channels.
* **Evidence:** `internal/watch` reads app and server usage, team certificates, drift and
  traffic, and nothing about k3s. k3s's certificates last 365 days and are renewed only "every
  time K3s starts" within 120 days of expiry (90 before the May 2025 releases; docs.k3s.io,
  "certificate"), so a server that has run for most of a year without a restart is the case to
  catch. Edka's Diagnostics shows these "K3s signals".
* **Fit:** k3s itself records a `CertificateExpirationWarning` event on the node 120 days ahead,
  and lists every snapshot as a cluster-scoped `ETCDSnapshotFile` (docs.k3s.io,
  "etcd-snapshot"); both are reads the watcher's minute tick can make.
* **Size:** S. **Without a cluster:** the rules yes; the sources need one.

### P2 — Hold notifications during planned work

* **What:** a silence for a team — everything, one app or one event kind — for 15 minutes to a
  week, started now or later, ended early; what was held shown in Activity.
* **Evidence:** `internal/notify` has no silence; maintenance mode (Phase 98) changes what
  visitors see and still lets `app.unhealthy` and threshold alerts fire. Edka's maintenance
  windows and per-alert silence (0.9.4).
* **Size:** S. **Without a cluster:** yes.

### P2 — Wildcard certificates without a DNS credential in the cluster

* **What:** issue `*.example.com` by DNS-01 where the panel already holds the zone's credential.
* **Evidence:** "Wildcard certificates ... needs ... a credential of its own, per team, in the
  cluster. That is not built" (`docs/concepts.md:421-426`); raised in `coolify.md` and as gap 11
  of `zeabur-oss.md`. Edka avoids the credential with a CNAME from `_acme-challenge` to a
  name it answers for.
* **Fit:** a cert-manager webhook solver that asks the panel to write the TXT record with the
  credential `internal/dnsprov` already keeps sealed, so no token enters the cluster.
* **Size:** M. **Without a cluster:** the solver's API yes; issuance needs the ACME stand-in.

### P2 — Let an assistant sign in with OAuth

* **What:** `/api/mcp` as an OAuth protected resource with a consent page offering read or
  read-and-write, and the grants listed and revocable under Account.
* **Evidence:** raised in `canine.md`, `epinio.md` and `hakopod.md`; Edka is the fourth. The
  endpoint takes only a bearer token (`internal/api/mcp_handlers.go:35-61`), which a hosted
  assistant such as claude.ai cannot be given as a header.
* **Size:** M. **Without a cluster:** yes.

### P2 — CI that signs in with its own OIDC token

* **What:** a trust for a repository, refs, an environment and a workflow file, exchanged for a
  short-lived token that may deploy one app.
* **Evidence:** raised in `lucity.md`; CI today stores `SKIFITY_TOKEN` (`docs/cli.md:58-75`), and
  nothing in `internal/` knows `token.actions.githubusercontent.com`. Edka's trusts last 15
  minutes and are refreshed during `--wait`.
* **Fit:** the token model already has scopes, projects and expiry (`internal/auth/scopes.go`).
* **Size:** M. **Without a cluster:** yes.

### P2 — Hetzner Volumes when the servers are Hetzner Cloud machines

* **What:** when every server in a location is a Hetzner Cloud machine and a Hetzner project is
  connected, offer Hetzner Volumes as an app's disk, so the disk follows the app to another
  server.
* **Evidence:** a local-path volume is tied to its server, so removal refuses until "Remove
  anyway" and the app cannot move (`docs/adding-servers.md:365-375`); Longhorn is the only
  alternative (`internal/settings/settings.go:604`). Edka uses Hetzner Volumes by default.
* **Fit:** the Hetzner CSI driver as an optional component; the cost is a Hetzner token in the
  cluster, which can create and delete servers in that project — say so where it is turned on,
  and suggest a project of its own.
* **Size:** M. **Without a cluster:** no; it needs Hetzner.

### P2 — One `skifity diagnose`

* **What:** one command, and one MCP tool, that reads an app's rollout, instances, events, last
  deployments and the failing instance's previous log, and prints each problem with the command
  to run next; exit 1 on a problem, `--json` with the findings.
* **Evidence:** the pieces exist apart (`skifity status`, `events --warnings`, `logs --previous`,
  `status --explain`); `zeabur-oss.md` gap 14 and `olares.md` P3 ask for a doctor and a
  bundle. Edka's `diagnose` carries the context flags into every "Next:" line.
* **Size:** S. **Without a cluster:** yes.

### P2 — Restore a database beside the original

* **What:** "Restore as a new database", with the original left running until the person
  switches the app's link.
* **Evidence:** a restore "replaces everything currently in the database"
  (`docs/backups.md:165-171`); raised as gap 5 of `zeabur-oss.md`. Every Edka restore
  creates a new database and copies the source's settings.
* **Size:** M. **Without a cluster:** no.

### P3

* **Say when k3s or a server has a security fix waiting.** Read the Kubernetes CVE feed against
  the cluster's version beside the k3s upgrade offer (Phase 109), and the count of security
  updates the hardening check can read. S–M, no cluster.
* **Ask again before what cannot be undone.** Re-authentication covers account security and the
  master key (`internal/api/api.go:306-330,627-628`), not deleting a database, removing a server,
  restoring a backup or changing a team's members; Edka asks for a passkey on its equivalents.
  S, no cluster.
* **Domain Connect** for a zone whose provider supports it, so a record is approved at the
  provider without a token in the panel. M, no cluster.
* **`skifity templates check FILE`** with file, line and fix, the checks a private catalogue gets
  on load (`docs/templates.md:212-228`), and an agent skill that writes a template from a
  repository — skills raised in `sealos.md`, `qovery.md` and `olares.md`. S, no cluster.
* **Join a server by running a command on it**, with a one-time token, for a machine the panel
  cannot reach over SSH; Edka's script is static and published with its SHA-256. M, cluster.
* **One outbound address for an app**, for a partner's allowlist: depends on pinning an app to a
  server (`hakopod.md` 12, `hivepaas.md` 9). M, cluster.
* Already raised and still open, with Edka as one more example: PITR (`render.md` P1, `temps.md`
  P2), pinning an app to a server, logs kept after the pod (`hakopod.md` 6), requests one by one
  (`hakopod.md` 11), refusing a stale save (`zeabur-oss.md` 9).

## Things to deliberately not copy

* **Holding the customer's cloud account and root.** Edka keeps a read-and-write Hetzner token,
  the SSH key of every node and the install-time admin kubeconfig. Skifity's panel lives on the
  owner's server and its master key never leaves it; keep the "Renting the panel out" analysis
  as the reason, and if a hosted mode ever comes, start from option 3 with tokens scoped to the
  namespaces the panel created, not from cloud-wide credentials.
* **A Kubernetes API open to the internet so a vendor can reach it** (satellites). Skifity's
  installer leaves 6443 to the other servers.
* **Personal kubeconfigs with edit rights.** They make kubectl a second way to change an app;
  Skifity's drift detection would fight every such change. Edka's revoke-on-change and CA
  rotation are the right shape if this is ever done.
* **Exec and debug containers from the browser.** On the roadmap's "Not on this roadmap"
  (`docs/roadmap.md:149-154`).
* **Revealing secret values to owners.** Skifity's are write-only by design.
* **A database on the internet under a vendor's domain.** `e2db.net` puts every customer's
  database names in one public zone and every external connection behind Edka's DNS; `skifity db
  connect` is the safer default.
* **Certificates that renew only while a vendor answers.** A CNAME to `acme.edka.net` is elegant
  and makes every renewal depend on Edka; any delegation Skifity offers should point at a name
  the owner controls.
* **Limits per plan on apps, databases and alert rules, and SSO only at €500 a month.** Already
  on the not-copied list.
* **Twenty operators on a small server.** Envoy Gateway, MetalLB, Flux, Zot, Longhorn,
  VictoriaMetrics and VictoriaLogs each cost memory Skifity's 2 GB minimum does not have, and
  each puts Kubernetes back on the page.
* **Agent runtimes as a product line, and CI runners.** A different business; templates such
  as OpenClaw are enough.
* **Several clusters per panel.** One panel, one cluster, by decision (`docs/roadmap.md:155-157`).
* **Client-side-unencrypted database backups.** Edka relies on the bucket's encryption; Skifity
  seals backups with a passphrase and should keep doing so.

## Sources

All read 2026-11-08 (UTC, into the small hours of 2026-11-09).

* https://edka.io/ (navigation, version banner "V0.9.7", case studies, add-on list)
* https://edka.io/pricing/ (plans, comparison table, FAQ)
* https://edka.io/changelog/ (0.1 to 0.9.7)
* https://edka.io/security/ (updated 2026-11-05)
* https://edka.io/satellite/, /deployments/, /explorer/, /agents/, /hetzner-kubernetes/, /aws-kubernetes/, /enterprise/, /cloud-providers/
* https://edka.io/sitemap-0.xml (162 pages), and every page under /docs/ it lists, each "Last updated: Nov 5, 2026"; read closely: get-started/prerequisites, get-started/create-your-first-cluster, clusters/overview, clusters/create, clusters/satellite, clusters/access, clusters/backups, clusters/update, clusters/archive, clusters/delete, clusters/explorer, clusters/diagnostics, clusters/events, clusters/os-updates, clusters/secrets, clusters/metal-pools, clusters/existing-server-pools, clusters/aws, clusters/encrypted-local-storage, deployments/overview, deployments/branch-deployments, deployments/automatic-updates, cronjobs/overview, apps/overview, apps/custom-apps, add-ons/overview, databases/overview, databases/postgresql, observability/overview, observability/alerts, observability/logs, observability/notifications, observability/resources, observability/vulnerabilities, traffic/domains, traffic/gateway, traffic/tailscale, traffic/private-networking, traffic/nat-gateway, registry/overview, inventory/overview, messaging/overview, agents/overview, agents/security, agents/codex, agents/conductor, integrations/claude, integrations/codex, integrations/external-secrets, cli/overview, cli/deploy, cli/diagnose, cli/github-actions, cli/scripting
* https://edka.io/blog/ (three pages) and https://edka.io/blog/control-plane-failure-recovery/ (2026-08-07)
* https://status.edka.io/
* https://news.ycombinator.com/item?id=44915164 (Show HN, 2025-08-15, through the hn.algolia.com items API)
* hn.algolia.com search for "edka" with typo tolerance off: comments 45481634 (2025-10-05) and 49244261 (2026-08-10)
* GitHub search API: `org:edkadigital` repositories; pull requests of `edkadigital/cli` (#1 – #46) and `edkadigital/apps` (#1 – #12)
* https://raw.githubusercontent.com/edkadigital/cli/HEAD/README.md, SECURITY.md, docs/security.md, go.mod
* https://raw.githubusercontent.com/edkadigital/homebrew-tap/HEAD/Formula/edka.rb (v0.10.0)
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=edka (no results) and https://api.osv.dev/v1/query for `github.com/edkadigital/cli` (none)
* https://docs.k3s.io/cli/certificate and https://docs.k3s.io/cli/etcd-snapshot (renewal at 120 days, `CertificateExpirationWarning`, `ETCDSnapshotFile`, `etcd-s3`)
* Not readable from this sandbox: Reddit (blocked), the GitHub Advisory Database and the CLI's
  releases page (refused by the session's GitHub access), and web search (the search tool's
  credits ran out after the first query).
