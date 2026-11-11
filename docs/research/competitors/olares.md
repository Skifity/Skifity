# Olares

> **Status.** Written on 2026-11-08 against Skifity's code at `23ec07f` and Olares's `main` at
> `4d83d35` (2026-11-07). The newest stable Olares release that day was 1.12.7 (2026-09-28).
> Nothing named here has been changed in Skifity yet; the gaps below are proposals.

Olares (`beclab/Olares`, olares.com; called Terminus until late 2024) is "an open-source personal
cloud OS you operate in plain language, built to run AI agents and LLMs on hardware you own"
(README). It installs Kubernetes on a machine at home or in an office and puts a desktop in the
browser on top: a Dock, a Launchpad, windows, and system apps called Files, Vault, Market,
Dashboard, Settings and Control Hub. It is for people who run apps rather than write them: the
documentation compares it with Synology, TrueNAS, CasaOS and Unraid, not with Coolify or Heroku,
and since 2026 it sells itself mostly as the place to run local models and agents. **Architecture:**
k3s by default (full Kubernetes optional; minikube on macOS), containerd, Calico, CoreDNS, etcd and
Envoy at the bottom; one PostgreSQL 16 shared by every app with an account each, KVRocks as the
Redis, NATS, Argo Workflows, JuiceFS on MinIO or S3 when there is more than one node, Prometheus,
and a Control Hub built on KubeSphere; Authelia and LLDAP for sign-in, Headscale and Tailscale for
the VPN, Seafile for sync, Infisical and a Padloc-based Vault for secrets, HAMi for GPU sharing,
Restic for backups (Olares's `developer/concepts/system-architecture.md`; the README's
acknowledgements). The platform's own services are Go (`app-service`, `bfl`, `system-server`, `olaresd`, `olares-cli`) and
the system apps Vue and Quasar. Apps are **Olares Application Charts**: a Helm chart plus an
`OlaresManifest.yaml` that declares entrances, permissions, middleware and resources. Every user
needs an **Olares ID** (`alice123@olares.com`, bound to a DID recorded on a blockchain) and the
**LarePass** phone app to activate a device, sign in with a second factor and use the VPN.
**Licence:** the README and the FAQ say AGPL-3.0, and the root holds an AGPL-3.0 `LICENSE`; the
same root also holds `LICENSE.md`, an "Olares License" that allows use "only for your own internal
business purposes or for non-commercial or personal use" and distribution only "free of charge for
non-commercial purposes", and the same file sits in `apps/packages/core`. Which code each one
covers is not stated in any file read. **Pricing:** the OS is free. Olares Space, the hosted side,
has a Free plan (2 GB a month through the Olares FRP relay at 5 Mbps, then 1 Mbps) and Basic at
$19.80 a month (50 GB of relay traffic at 50 Mbps, 50 GB of backup storage, $0.015 per extra GB)
(olares.com/space/plans; the billing page in the docs still says 50 GB of each is free, which the
plans page contradicts). Paid Market apps are bought with USDC or USDT from a browser wallet, plus
ETH for gas. Olares One, the company's own machine (Core Ultra 9 275HX, RTX 5090 Mobile with 24 GB,
96 GB of memory, 2 TB), raised $2,343,324 from 816 Kickstarter backers (one.olares.com,
2026-11-08); its retail price was not on the page read. **Maturity:** repository created
2024-04-29; stable tags from 1.3.0 to 1.12.7, about every two to four months in 2026; the FAQ says
"a major update approximately every 2 months" and the versioning page "monthly". **Adoption**
(2026-11-08): 5,353 GitHub stars, 334 forks, 126 open issues and 31 open pull requests (GitHub
search API); 219 issues ever opened; 212 pull requests merged between 2026-09-22 and 2026-11-08,
by about twenty authors in the clone's history; a Discourse forum with 65 topics, 187 posts and 63
users (`/forum/about.json`); four Hacker News stories, none above 4 points. The company signs its
blog as BEC LAB.

## Feature inventory

### What shipped, release by release

Tag commit dates come from a clone of the repository; announcement dates from the blog, the forum
and Olares's `manual/release-notes.md`. The GitHub releases pages could not be read from this
sandbox.

| Release | Date | What changed that matters for a comparison |
|---|---|---|
| 1.10.0 | 2024-11-08 | First tag under the Olares name; the Show HN of 2024-05-31 was still "Terminus" |
| 1.11.0 | 2024-12-24 | CUDA 12.4 and later. Moving from one minor version to the next was not supported: "users must manually uninstall the existing version before installing the newer version" (`developer/install/versioning.md`) |
| 1.12.0–1.12.2 | 2025-08-05 to 2025-11-06 | Cloud storage and team collaboration (1.12.1); simpler local access and several GPUs (1.12.2); install-time variables and system-wide variables need 1.12.2 |
| 1.12.3, 1.12.4 | 2026-01-06, 2026-01-19 | Easier local access |
| 1.12.5 | 2026-03-26 | DGX Spark support and GPU management; a missing header broke live updates in Chrome 148 over the VPN, fixed in 1.12.6 (`manual/help/known-issues.md`) |
| 1.12.6 | 2026-07-15, announced 2026-07-17 | Agent Skills in `olares-cli`; Model Console and four "Engine Base" apps (Ollama, vLLM, SGLang, llama.cpp); Settings → Accelerator with a CPU-or-GPU choice at install; shared apps rebuilt (old ones must be uninstalled and reinstalled, data moved by hand); Files archives, NFS and a Common folder for model weights; Overlay Gateway; the Studio app retired. "Over 700 pull requests" |
| 1.12.7 | 2026-09-22, released 2026-09-28 | Router, an AI gateway with its own API keys, scopes and quotas; Lares, the first-party agent; a wider model catalogue; five more interface languages, though the desktop and the new apps ship English and Chinese only; Ticket, a support channel that attaches logs; global search; `olares-cli node enable-juicefs` and `node join-command` for adding a server |
| 1.12.8, 1.12.9 | daily builds from 2026-09-29 and 2026-10-29 | 1.12.8 reached `beta.1` and was not stable on 2026-11-08, so the October security fixes below are in no stable release |

### Deploy sources and builds

* **No Git deploys and no builds.** Olares runs packaged apps. A developer packages an Olares
  Application Chart and submits it by pull request to `beclab/apps`, where a bot checks it; an
  update must bump the chart version, and "Olares Market does not support version rollbacks. If an
  issue occurs, you must submit a new version to fix it" (`developer/develop/manage-apps.md`).
* **Porting by agent.** Since 1.12.6 the `olares-chart` skill "turns a repository, Docker Compose
  project, or Helm chart into an Olares app and deploys it"; Lares can do the same from a
  repository or a Compose file and "iterate on issues it finds" (1.12.7 blog). Studio, the
  in-product development environment, was retired.
* **Install-time variables** (`envs`, 1.12.2): typed as `int`, `bool`, `url`, `ip`, `domain`,
  `email`, `string` or `password` and validated, with a list of choices, a default, `required`
  and `editable` after install. **System variables** (`OLARES_USER_*`, Settings → Advanced) are
  set once, for example an OpenAI key, and referenced by any app.
* **Dependencies and conflicts** between apps, and a minimum OS version, are declared in the
  manifest and checked before install.
* **Clone:** an app marked `allowMultipleInstall` can be installed again under another name with its
  own variables (`manual/olares/market/clone-apps.md`).
* **A newer image by hand:** when the Market lags, the documented route is editing the Deployment
  in Control Hub, which "the Market version will overwrite" at the next update
  (`manual/update-app-image.md`).

### Domains, TLS and routing

* **An HTTPS address for every app without a domain.** Each entrance is
  `https://{routeID}.{local-name}.olares.com`; community apps get a random eight-character route ID
  plus the entrance's index, and a memorable one can be set. Olares registers the names and the
  certificates on the user's behalf; the FAQ says this is why an Olares ID is required.
* **Remote access through a relay:** Olares Tunnel (FRP, metered as above), a self-run FRP server,
  or Cloudflare Tunnel; a device with a public address skips the relay. The relay node is chosen
  during activation.
* **Entrances and access levels:** each entrance is **Public** (no sign-in), **Private** (sign-in
  everywhere) or **Internal** (no sign-in from the VPN or the LAN, sign-in otherwise), with the
  mode System, One Factor or Two Factor. **Sub-policies** apply another mode to paths matched by a
  regular expression, and a manifest can ship them (`options.policies`, with a validity period). The
  default for an entrance is `private` (`manual/olares/settings/manage-entrance.md`, manifest).
* **Own domains** go through Olares Space as an "organisation": a TXT record, then two NS records
  that delegate a subdomain "to Olares's Cloudflare"; the docs warn not to use an apex because "all
  DNS management" moves to Olares Space. It has to be set up before a device is activated — an
  activated device must be factory-reset — and "once an organization is created for a domain, the
  domain cannot be removed from Olares Space" (`reusables/custom-domain.md`). A third-party
  domain per app with a CNAME was described on the forum (2026-06-30); its documentation was
  removed on 2026-10-30 (#4348), and whether the feature remains was not established.
* **On the LAN:** `http://<entrance>.<user>.olares.local` names and, since 1.12.7, host mappings
  written by LarePass Desktop on Windows and macOS.
* **Ports that are not HTTP** (`ports.exposePort`, TCP and UDP) are reachable "on the local network
  or through a VPN" only, and can be added to the Tailscale ACL. Minecraft and Palworld guides use
  them.
* **No firewall, rate limit or WAF** was found in the docs; public entrances have "basic security
  through Cloudflare".

### Databases and services

* **Middleware an app declares** (`middleware` in the manifest): PostgreSQL (the shared instance,
  an account and database per app, extensions and init scripts), Redis (KVRocks), MongoDB, MinIO,
  RabbitMQ, MariaDB, MySQL, and Elasticsearch and NATS in the integration guides. Connection details
  arrive as Helm values. "MongoDB, MySQL, MariaDB, MinIO, and RabbitMQ must first be installed by
  an admin from the Market." On several nodes PostgreSQL runs on Citus, "though its production
  readiness is still under evaluation".
* Control Hub's **Middleware** page lists instances, connection details and metrics.
* **Shared apps** (rebuilt as "V3" in 1.12.6): one instance installed by an administrator, used by
  every member through their own URL — model servers, ComfyUI, Dify.
* No database per app, no replicas or failover, and no database dumps were found in the docs.

### Storage and backups

* **Folders by permission:** an app gets `Data` (cluster storage), `Cache` (node-local), `Common`
  (shared between apps, mainly model weights), chosen `Home` subfolders, and `External` — USB disks
  and SMB or NFS mounts — only with `externalData`, off by default since manifest 0.12.0.
* **Files** browses, edits, compresses and shares; mounts SMB and NFS shares and Google Drive,
  Dropbox, S3 and Tencent COS; syncs devices through Seafile.
* **Backups** of chosen folders and of applications — "Currently, only the **Wise** application is
  supported" — to a local path (a USB disk or an SMB share), Olares Space, AWS S3 or Tencent COS;
  daily, weekly or monthly; the first run full, later ones incremental; protected by a password;
  pausable; "Snapshot now" (`manual/olares/settings/backup.md`). The engine is Restic
  (`olares-cli backups download` "downloads the Restic dependency tool"). A restore from S3 takes a
  presigned URL of the backup folder, pasted into Settings.
* Several nodes need **JuiceFS**: `olares-cli node enable-juicefs` stops Olares and migrates its
  local file system, after a warning to back up first.
* No backup of the system itself, of middleware databases, or of an arbitrary app's data was found
  in the user manual; the architecture page lists "backups for directories, applications, and
  clusters".

### Scaling and high availability

* **Instances per workload are fixed by the chart** (`workloadReplicas`, mandatory since manifest
  0.12.0). No autoscaling was found.
* **Several machines:** one master and workers joined over SSH (below). No second master is
  documented. "Olares does not support mixed-architecture clusters", and apps on the Overlay Gateway
  must run on the master.
* **GPUs pooled across nodes** (Settings → Accelerator), each card in one of three modes: **time
  slicing** (apps take turns, each sees the whole card), **memory slicing** (a fixed share of VRAM
  per app, adjustable at launch) and **exclusive**. Before starting an app in time slicing, Olares
  checks node memory as "app memory + the card's VRAM" against 90% of the node. Accelerators listed:
  NVIDIA, NVIDIA GB10, Apple M-series, AMD Strix Halo, and CPU as the fallback; AMD GPUs are "not
  covered in this version" (`manual/olares/settings/gpu-resource.md`).
* **Under pressure Olares stops apps itself:** "If an application consumes excessive resources ...
  the system might automatically pause it", and starting it again needs free resources that the
  user has to make by stopping others (`manual/olares/market/market.md`).

### Observability (logs, metrics, alerts, uptime)

* **Dashboard:** CPU (with temperature and load), memory and swap, disks (model, serial,
  temperature, power-on time, data written, and an "occupancy analysis" per file system), pods per
  node, GPUs (VRAM, power, and per-task use), network interfaces, fans on Olares One; history over
  a chosen range; the top five apps by CPU and memory; each user's quota.
* **Control Hub:** namespaces, pods, workloads, jobs, storage, network policies and custom
  resources, with logs, events and charts; YAML can be edited; and **a terminal on the host**.
* **Market operation logs** per app, downloadable; system logs exported from Settings or with
  `olares-cli logs`, attached to a Ticket since 1.12.7.
* **No alert rules and no external channels** were found: notifications appear on the desktop.

### Security, auth, roles, SSO, audit

* **Identity:** an Olares ID made in LarePass is required to activate a device; the account goes
  through a DID stage and is then bound on-chain. Losing the mnemonic loses the identity and the
  paid apps bound to it (`manual/olares/market/purchase-paid-apps.md`).
* **Sign-in:** a password plus a one-time code generated in LarePass for "sensitive operations such
  as login" (`developer/concepts/account.md`). Authelia checks every request to a private
  entrance; `bfl` adds the user's identity in `X-BFL-USER` and the app trusts it.
* **SSO into apps:** a built-in OpenID Connect provider an app enables in its manifest, receiving a
  client ID, secret and issuer as values; LLDAP serves LDAP.
* **Roles:** Super Admin (the first user), Admin and Member. Members install ordinary apps and use
  shared ones; Admins manage users, shared apps, GPUs and updates.
* **Sandbox:** a namespace per app per user, network policies admitting only the user's own and
  system namespaces, no `hostNetwork` or NodePort for community apps, and an option forcing UID
  1000 (`runAsUser`). The network page describes an Envoy sidecar on every entrance pod; pull
  requests merged on 2026-08-07 (#3882, #3883) retire those sidecars for an L4 proxy, with a Linkerd
  mesh for shared traffic (#3785).
* **VPN controls:** enforce VPN-only access, SSH over the VPN, ACL ports.
* Vault, a password manager for people; Infisical inside the platform.
* **No audit log** was found in the docs.

### Preview environments and branches

* None. Olares has no Git integration.

### Templates and catalogue

* **Market:** 397 app directories in `beclab/apps` on 2026-11-08 (`0e22946`), 113 of whose names
  end in a version suffix such as `v2` or `v3` (models and their engine variants). Categories
  include Agents, Models, Workspace, Homelab and Developer. Official sources for the world and for
  China; **custom sources** can be added and switched.
* **Before install**, each accelerator option shows the CPU, memory, disk and VRAM it needs;
  installs fail with "Insufficient system CPU/memory", "Insufficient disk space" or "Available
  CPU/memory insufficient".
* **Updates** in one panel with "Update all". Uninstall keeps or deletes the app's data.
* **Engine Base** template apps (`templateOnly`) are cloned into model instances with their own
  console, a GPU-residency check and a one-click time-to-first-token test.
* **Paid apps** with licences bound to the Olares ID; restoring one also "requires the developer's
  service to be online".
* Updates lag upstream: "for each upgrade, we still manually verify that the change will not affect
  user data. This makes our app update process slower than we would like" (staff, forum #123,
  2026-07-28).

### CLI, API, IaC, integrations

* **`olares-cli` on the host** installs, upgrades, adds nodes, manages GPUs, users and backups,
  checks prerequisites and collects logs, with root and the kubeconfig.
* **`olares-cli` for agents** (`npx @olares/cli@latest install`, then `profile login`) with **twelve
  Agent Skills compiled into the binary**: shared, chart, files, market, settings, dashboard,
  cluster, doctor, router, knowledge, search, publish. `olares-cli skills install` writes them to
  `~/.agents/skills` and links them into the skill folders of Cursor, Claude Code, Codex and
  OpenClaw where those exist; the CLI warns when the installed skills came from another build
  (`developer/cli-agent-skills.md`). Output is JSON.
* **Router** (1.12.7) gives apps inside Olares models and tools without a key, and outside clients
  keys with model scopes, expiry and quotas. **Lares** turns a goal into CLI and skill calls.
* **No public REST API reference** and no declarative file for a whole setup were found.

### Notifications

* Desktop notifications only. No email, chat or webhook channel was found.

### Multi-server and networking

* **Adding a worker** (1.12.7): enable JuiceFS on the master, run `olares-cli node join-command`,
  give it the master's SSH password, and paste the command it prints on the worker. The master must
  allow password SSH, and the docs warn that "`MASTER_AUTH_INFO` is Base64-encoded, not encrypted.
  Anyone who has the command can recover the master's SSH credentials"
  (`manual/best-practices/install-olares-multi-node.md`).
* **LarePass VPN:** Headscale and Tailscale, with subnet routes. Issue #2535 (2026-02-12, open)
  reports the bundled Tailscale is 1.48.1 and subnet routing broken.
* **Overlay Gateway** (1.12.6): selected apps — Home Assistant, Jellyfin — get a second interface
  on the LAN so casting, DLNA and device discovery work; wired Ethernet, a Linux host and the master
  node only.
* `.local` names over mDNS; local-first use once activated, but activation needs the internet.

### Team and collaboration

* Users of one device share its domain; an administrator creates each with a role and a CPU and
  memory quota (at least one core and 3 GB) and hands over an activation URL and a one-time
  password (`manual/olares/settings/manage-team.md`).
* Shared apps, shared Vault items and Files sharing. Organisations and their members are managed in
  Olares Space. No teams or projects inside a device.

### Developer experience and onboarding

* **Requirements:** 4 cores, 8 GB of memory and 150 GB on an SSD — "Installation on an HDD will
  fail" — on Ubuntu 22.04 to 25.04 or Debian 12 or 13; NVIDIA GPUs from Turing on are optional.
* **Install paths:** `curl -fsSL https://olares.sh | bash -`, an ISO for x86-64, Docker Compose,
  and guides for macOS, Windows, Proxmox, LXC and the Raspberry Pi; Olares One and DGX Spark have
  their own.
* **Activation:** create an Olares ID in LarePass, enter it at the end of the install, open the
  wizard URL with the one-time password, pick a relay node, then scan a QR code with the phone "on
  the same network". `olares-cli` can activate without the app, but the ID is still made in
  LarePass.
* **Updates** are started from LarePass on a phone, in steps: "If you are running Olares 1.12.5 or
  earlier, update to 1.12.6 first" (`manual/update-guides/1.12.7.md`); some AI apps had to be
  reinstalled after 1.12.6.
* **Interface:** a desktop in the browser with windows, widgets synced across devices, and a
  mobile layout. Seven languages are offered; coverage "rolls out application by application".
* Documentation in English and Chinese, with troubleshooting pages written per symptom.

## What users love

Independent praise was thin in what could be read: Reddit refused this sandbox and web search was
unavailable, and several of the warmest Hacker News comments are from people who say they work on
Olares ("Open Source Olares (biased)", 2026-07-15; "That fragmentation is exactly why we started
building Olares", 2026-03-13).

* **Local AI without assembling it.** The pitch that lands is one-click models on one's own GPU:
  "Running Qwen3-30B-A3B-Instruct-2507-AWQ-4bit on an Olares One ... Plenty fast for coding work and
  for sharing with my OpenClaw setup" (HN, 2026-06-17); "Qwen3.8 flash-next runs at 110 t/s on the
  Olares One using Strata" (HN, 2026-10-29). Model Console's residency check and time-to-first-token
  test answer the question people otherwise script by hand (1.12.6 blog).
* **The device and the money behind it.** $2,343,324 from 816 backers; "was lucky enough to snag
  the Olares One from Kickstarter just before this whole AI induced memory chip price gouging
  started" (HN, 2026-07-10).
* **Momentum.** #1 on GitHub Trending during the 1.12.6 cycle (blog, 2026-07-17), 5,353 stars, and
  about 200 merged pull requests in the six weeks after 1.12.7.
* **A responsive team on a small forum.** Staff answered every thread read within days, and a user
  closed with "I appreciate what you have created and continue to grow" (forum #123, 2026-07-28).

## What users complain about

* **The vendor account.** "Why I have to get olares id to run olares? ... Why I can't use my own
  domain at zero point?" (#719, 2024-12-05); "is it possible to self host olares without using
  larapass if i have my own domain? ... i cant separate my instance fully from your network" (#2069,
  2025-11-14, open, the most-reacted issue). The FAQ promises activation "options that make the
  Olares ID optional" and standalone relay, DID and Market services "in future updates".
* **Web3.** "It would be nice to have an image that does not include the super sus AI and web3
  'features'"; "the word blockchain turned me off as well" (HN Show HN thread for Terminus,
  2024-05-31). The founder answered that the blockchain is used "only in limited aspects related to
  DNS and CA".
* **Locked out by activation.** A custom-domain activation failed at the password step with a 530
  from the relay, and then neither reset nor SSH worked: "no recovery path (same as #2266)" (#3017,
  2026-05-05, open). Clearing LarePass's data lost the super-admin identity while a browser session
  still worked (#2764, 2026-03-26, open).
* **The OS gets in the way on a good machine.** "It comes with Olares OS which for my purposes is
  not all that useful, I settled finally on a good Ubuntu 24.04 LTS configuration" (an Olares One
  buyer, HN, 2026-03-26).
* **Install failures off the beaten path:** DietPi (#742, 28 comments), Proxmox (#366), Unraid
  (#914), WSL (#1908), macOS with minikube (#2800); an uninstall that left Docker's iptables chains
  broken for every other container on the host (#850, 2025-01-10).
* **Catalogue lag and no way back.** Market updates are verified by hand and trail upstream (forum
  #123); an upstream author deleted a model file and "breaks Ornith 1.0 35B from market. Do you plan
  to implement some rollback feature?" (forum #116, 2026-07-15).
* **GPU sharing breaks some workloads.** HAMi's injected library segfaulted CUDA 13 apps on the
  RTX 5090 Mobile (#2687, closed 2026-03-16) and on WSL2 (#2350, open); a GPU-lock setting caused "a
  severe performance regression" (#3213, open).
* **Fragile state.** A failed user creation leaves a `User` object that makes the proxy on port 443
  hang at every boot, so "Olares becomes completely unreachable" (#2944, 2026-04-24, open). Upgrades
  go one minor at a time, and 1.12.6 needed some apps reinstalled with data migrated by hand.
* **Disks fill up with models.** "My disk is almost completely full ... a lot of hidden disk space
  waste from old, discarded, or partially downloaded AI models" (forum #107, 2026-06-26); listing
  unused images arrived in the CLI in 1.12.6.
* **Hardware and the mobile desktop.** Freezes and a Thunderbolt NIC lost after a BIOS update on
  Olares One (forum #122, #113); icons cut off on a phone, so "I had to open the settings app, find
  the entrance endpoint url and copy it into the browser" (forum #46, 2026-03-18).

## Security record

**Where I looked (2026-11-08):** NVD keyword searches for "olares", "beclab" and "beclab terminus"
(none); OSV for the Go modules `github.com/beclab/Olares`, `/cli`, `/framework/app-service`,
`github.com/beclab/app-service` and `github.com/beclab/bfl` (none); GitHub's issue and pull-request
search for security terms (283 pull requests, 24 issues). The repository's own advisory pages could
not be read, because GitHub's API refused the repository from this sandbox, so "none found" is not
"none".

* **Policy.** `SECURITY.md` arrived on 2026-03-18 (#2713): reports by email to `hi@olares.com`, only
  the latest version supported, "no formal bug bounty program", rewards at the company's discretion.
  On 2026-11-01 a researcher asked in a public issue whether the bounty "is still active" after
  emailing a report (#4375, open).
* **Fixes without advisories.** #3077 (2026-05-13, into 1.12.6) in Files: "close ffprobe RCE, stop
  CORS origin reflection, tighten share path matching, allowlist ORDER BY inputs, fail-closed on
  token RNG failure". #3981 (2026-08-20): on public entrances the proxy had set `X-BFL-USER` to the
  owner, so "anonymous callers" were "presented to backends as the zone owner". #4359 (2026-10-31,
  "High Risk"): on shared routes a caller-supplied identity header could reach the upstream; it is
  now stripped and the verified user sent as `X-CALLER-USER`. #4361 (2026-10-31): "remove probe
  bypass", the signed User-Agent paths #3981 mentions. #4319 (2026-10-28): the Market now confines
  source paths, limits who administers sources and bounds remote fetches. #2983–#2986 (2026-04-29):
  vulnerable dependencies bumped. The October fixes target 1.12.8, which was not released, so the
  stable 1.12.7 does not have them.
* **Identity by header.** Apps behind an entrance trust `X-BFL-USER`, which is why two of the fixes
  above are about who may set it.
* **Exposure by design.** A root terminal on the host in Control Hub's web page; a worker join
  command that carries the master's SSH password, recoverable from Base64, with password SSH
  required on the master; a Tailscale 1.48.1 binary in the system image (#2535, open); for default
  installs, DNS and the relay are run by the vendor (NS delegation to "Olares's Cloudflare", FRP
  nodes), so the vendor is on the path of every remote request. Whether TLS ends on the device or
  at the relay was not established.
* **Reputation.** Symantec Bluecoat classified olares.com and its subdomains as "Malicious Outbound
  Data/Botnets" in 2025 (#1709, closed the same day as a reclassification request).

## Against Skifity

Olares and Skifity make the same bet — Kubernetes underneath, out of sight on top — for different
people: Olares for a household or a small office that installs apps and models, Skifity for people
who run apps they or others wrote. Skifity statuses come from `docs/progress.md`: the one-server
path runs on a real k3s on every push to `main`, and two servers in the `Two servers` workflow.
Olares was read, not run.

| Capability | Olares | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Your own code | None; a chart by pull request, or an agent porting a repository | Git with webhooks, image, folder upload, a Compose file as a stack, Railpack and Dockerfile builds (`internal/gitsrc`, `internal/builder`, `internal/api/stack_handlers.go`) | Skifity ahead |
| Packaging | Helm chart plus `OlaresManifest.yaml` | An app is settings; the panel renders the objects (`internal/kube/manifests.go`) | different; Skifity simpler |
| Install-time inputs | Typed (`email`, `url`, `int`, ...) with choices, validated | Key, label, help, default, secret, required, generate; no type or choices (`internal/templates/templates.go:299`) | **behind** (small) |
| Rollback | None in the Market; an image edited by hand is overwritten | Old image and settings, with a plan first (`docs/concepts.md:442`) | Skifity ahead |
| **Domains, TLS and routing** | | | |
| First address | HTTPS on `olares.com` at once, through the vendor | `sslip.io` over plain HTTP until a domain is added (`docs/quick-start.md:158-163`, ADR-0015) | behind on first-run HTTPS; ahead on independence |
| Own domain | A subdomain delegated to the vendor's Cloudflare, before activation, never removable | Any domain, cert-manager, DNS providers, own certificates (`internal/dnsprov`, `internal/tlscert`) | Skifity ahead |
| Sign-in in front of an app | Every private entrance behind Authelia, one or two factors, rules per path | One shared password per app (Phase 75, `docs/firewall.md` "A password instead") and address rules | **behind** |
| Firewall, rate limits, maintenance | None found | Rules on IP, country, ASN, path, header; limits; maintenance (`internal/edgerules`, `internal/guard`) | Skifity ahead |
| Behind NAT | Vendor FRP (2 GB free), own FRP, Cloudflare Tunnel, VPN | Cloudflare Tunnel (`internal/cluster/tunnel.go`) | behind (fewer ways) |
| Ports that are not HTTP | TCP and UDP, LAN and VPN only | TCP and UDP on every server, open to everyone; "The firewall does not apply to them" (`internal/kube/ports.go:13-21`) | parity; **behind** on who may connect |
| **Databases and services** | | | |
| Databases | One shared PostgreSQL and KVRocks; others installed from the Market; an account per app | Nine engines, one instance per database, PostgreSQL with replicas, `skifity db connect` (`docs/databases.md`, `internal/dbsvc`) | Skifity ahead |
| **Storage and backups** | | | |
| Data elsewhere on the network | SMB and NFS mounts; apps get them with `externalData` | None: app pods are kept off every private range (`internal/kube/egress.go:28-37`); no NFS or SMB volume in `internal/kube` | **behind** |
| Backup destinations | Local path or USB, SMB share, S3, COS, Olares Space | S3-compatible only (`docs/backups.md:12`) | behind |
| Backup method | Restic, incremental after the first, password | A full dump or a full compressed tar each time (`docs/backups.md:298`), sealed with a passphrase, verifiable (Phase 104) | **behind** on incremental; ahead on verification |
| What is backed up | Folders, and one app (Wise) | Every database and volume, on schedules, with restore (`internal/backup`) | Skifity ahead |
| The system itself | Not documented | The panel to the bucket on a schedule, `skifity admin restore-db` (Phase 84) | Skifity ahead |
| **Scaling and high availability** | | | |
| Instances | Fixed by the chart | Autoscaling, scale to zero, readiness checker (`internal/kube/scaletozero.go`, `internal/deploy/scaling.go`) | Skifity ahead |
| Several servers | One master, workers; no mixed architectures | Embedded etcd from the first install, promote, drain on removal (`internal/provision`) | Skifity ahead |
| GPUs | Pooled; time slicing, memory slicing, exclusive; VRAM quota per app | Whole cards: "A GPU is not shared" (`docs/gpus.md:139`); NVIDIA, AMD, Intel | **behind** on sharing |
| Resource ceilings | CPU and memory per user, set by an admin | A fixed 8 cores, 16 GB and 100 GB per environment, readable, not settable (`internal/kube/namespace.go:45-47`, `internal/api/teams_handlers.go:675`) | behind (small) |
| Under pressure | Stops heavy apps itself | Kubernetes evicts; thresholds alert (Phases 95, 99) | different |
| **Observability** | | | |
| Usage history | Dashboard with ranges; disk health, temperatures, fans, GPU per task | Apps and servers for 72 hours, disk, thresholds (`internal/watch/usage.go:18`, Phase 99) | parity; behind on hardware health |
| Looking underneath | Control Hub: browse, edit YAML, a host terminal | Advanced, read-mostly, drift detected (`internal/watch/drift.go`); no shell by decision (`docs/roadmap.md:149`) | different; Skifity safer |
| Alerts and channels | Desktop only | Ten channels, events per channel (`internal/notify`) | Skifity ahead |
| Logs for a bug report | Export from Settings, `olares-cli logs`, Ticket | Log drains (`internal/logdrain`); no diagnostics bundle (`internal/cli/admin.go:51-63`) | mixed |
| **Security, auth, roles, SSO, audit** | | | |
| Sign-in to the system | Password plus a code from the vendor's phone app; vendor ID required | Argon2id, TOTP, passkeys, OIDC, a setup token, no outside account (`internal/auth`, `internal/api/setup.go`) | Skifity ahead |
| Apps signing in with it | Built-in OIDC provider, LLDAP | None | **behind** |
| Roles | Super Admin, Admin, Member | Owner, admin, member, viewer; members limited to projects (Phases 85–86) | Skifity ahead |
| Audit | Not found | Team audit log | Skifity ahead |
| Secrets | Vault for people; app variables' handling not established | Sealed per row, write-only, external managers (`internal/crypto`, `internal/secretmgr`) | Skifity ahead |
| Isolation | Namespace per app and user, policies, proxy-checked identity header | Namespace per environment, default-deny both ways, restricted Pod Security (`internal/kube/namespace.go`) | parity, different layer |
| **Preview environments** | None | Per pull request, seeded (Phase 107) | Skifity ahead |
| **Templates and catalogue** | | | |
| Size | 397 app directories, many of them model variants | 374 (`internal/templates/catalogue`) | parity; different focus |
| Requirements before install | CPU, memory, disk and VRAM shown; refused when short | GPUs checked (`internal/api/template_install.go:136`); memory and CPU neither shown nor checked (`web/src/pages/templates.tsx`) | **behind** |
| Other sources | Custom sources | Team catalogues by URL (`internal/templates/remote`) | parity |
| Updates | One panel, "Update all", trailing upstream | Offered per app, with a backup first (`internal/api/template_updates.go`) | parity; Skifity safer |
| **CLI, API, IaC, integrations** | | | |
| Agents | Twelve skills in the CLI binary, installed into agents' folders; Lares; Router | 48 MCP tools over stdio and from the panel (`internal/mcpserver`, `internal/api/api.go:610`); no skills | ahead on MCP; **behind** on skills |
| Declarative setup | None | `skifity.yaml` with plan and apply (`internal/blueprint`) | Skifity ahead |
| **Notifications** | Desktop | Ten channels | Skifity ahead |
| **Multi-server and networking** | | | |
| Joining a server | Master's SSH password in a Base64 command | Password used once, the panel's own key installed (`internal/provision`) | Skifity ahead |
| A VPN for people | Headscale and Tailscale built in (an old Tailscale, #2535) | WireGuard between servers only | behind |
| A presence on the LAN | Overlay Gateway for chosen apps | None; restricted Pod Security refuses host networking (`internal/kube/podsecurity.go:18`) | behind (niche) |
| **Team and collaboration** | Users of one device, quotas; organisations in Olares Space | Teams, projects, invitations, SSO groups (Phases 85, 86, 92) | Skifity ahead |
| **Developer experience** | | | |
| Requirements | 4 cores, 8 GB, 150 GB SSD | 2 GB of memory, 8 GB of disk (`docs/quick-start.md:9-11`) | Skifity ahead |
| Install targets | Script, ISO, Docker Compose, macOS, Windows, Proxmox, LXC, Raspberry Pi | Script on a systemd Linux; "An unprivileged LXC or a Docker container" will not work (`docs/quick-start.md:20-23`) | behind on breadth |
| Upgrades | From a phone, one minor at a time, some apps reinstalled | From the panel with a snapshot (`internal/api/upgrade.go`, Phase 109) | Skifity ahead |
| Languages | Seven offered; desktop and new apps in two | Five, every key present, a build gate (`web/src/locales`) | Skifity ahead on completeness |
| Local AI | Router, Lares, Model Console | Templates with a GPU (Ollama), nothing more | different category |

### Claims in this repository that no longer hold

* `docs/research/competitors.md`, "Where Skifity is actually different", point 5 calls hiding
  Kubernetes well "the whole bet", and its list of things not copied ends with "the thing nobody
  else does". Olares makes the same bet for people who are not developers — Control Hub is its
  "Advanced" — with 5,353 stars and a crowdfunded device behind it. Point 1 still holds, but only
  because of the word "small": Olares installs k3s with one command too, on 4 cores, 8 GB and a
  150 GB SSD.

## Gaps worth closing in Skifity

### P1 — Only the team can open this app, with exceptions by path

* **What:** an access setting per app beside the password: "Anyone" (today) or "Signed-in members
  of this team", optionally with a second factor, and path rules on top — `/api/` open for a phone
  client that cannot follow a sign-in page, `/admin` needing the second factor.
* **Evidence:** in Olares an entrance is `private` unless its manifest says otherwise, with One
  Factor or Two Factor and regex sub-policies (`manual/olares/settings/manage-entrance.md`,
  `options.policies`). It is what makes a self-hosted app without its own login — a file browser,
  ComfyUI, a monitoring page — safe to put on the internet. `cloudron.md` raised this as P1 and it
  is still open: Skifity has a shared basic-auth password (Phase 75) and address rules
  (`internal/edgerules/edgerules.go:56-67` has allow, block and limit, nothing about identity).
* **Fit:** the guard is already a forwardAuth in every namespace (`BuildGuardMiddleware`,
  `internal/kube/namespace.go:452`). On a request without a valid cookie it redirects to the
  panel, which checks the session and team, and sends the browser back with a short-lived token
  signed for that app's domain; the guard verifies it with a key from a Secret and sets a cookie on
  the domain. Path rules reuse the `edgerules` path test, after the path is decoded once — the
  open defect `kamal.md` names (`pathOf` in `internal/guard/guard.go:424` still compares the path
  as sent). An `errdoc` entry for a member of another team.
* **Size:** M. **Without a cluster:** the decision, the token and the redirect yes; Traefik
  forwarding to the guard is already exercised by the `Cluster` workflow's firewall checks.

### P1 — Share one GPU between several apps

* **What:** a mode per server's cards — "whole card" (today) or "shared by up to N apps" — on the
  Servers page; the app page says which apps share its card and that sharing divides time, not
  memory.
* **Evidence:** Olares's Settings → Accelerator offers time slicing, memory slicing and exclusive
  per card, and 1.12.6's Model Console is built on it; the household case is Ollama, ComfyUI,
  Immich's machine learning and Jellyfin transcoding on one consumer card. In Skifity "A GPU is not
  shared. An instance given one has it to itself" (`docs/gpus.md:139`), so a one-card server runs
  one GPU app; the Ollama template takes the card. Olares's memory slicing goes through HAMi's
  injected library, which segfaulted CUDA 13 apps (#2687), and a GPU-lock setting in its sharing
  caused a severe slowdown (#3213): start with the device plugin's own time slicing.
* **Fit:** Skifity renders NVIDIA's device plugin itself (`BuildNVIDIADevicePlugin`,
  `internal/kube/gpu.go:496`). Time slicing is that plugin's config file —
  `sharing.timeSlicing.resources` with `replicas` — from a ConfigMap, after which a card is
  advertised as N `nvidia.com/gpu` and the node is labelled with the replica count. `gpuFits`
  (`internal/api/gpu_handlers.go:267`) learns that a shared card counts N times, and the refusal of
  scale to zero for GPU apps can be lifted for shared cards, since a sleeping app no longer holds a
  card somebody else needs.
* **Size:** M. **Without a cluster:** the rendered config, the fit logic and the interface yes;
  that two pods really share a card needs a runner with a GPU, which CI does not have.

### P1 — Let an app reach the home or office network, and mount an NFS share

* **What:** on an app's settings, addresses on one's own network it may reach (a host or a range,
  and ports), and a volume kind "NFS share" (server, export, read-only or not).
* **Evidence:** Olares mounts SMB and NFS shares in Files and hands them to apps that ask for
  `externalData`; its Jellyfin and Plex tutorials read media from them, and a Home Assistant user
  edits a policy to reach `192.168.1.141` (#1453). Skifity keeps every app pod off `10.0.0.0/8`,
  `172.16.0.0/12`, `192.168.0.0/16` and `100.64.0.0/10` (`internal/kube/egress.go:28-37`), and opens
  an exception only for a build's Git host (Settings → Git, `docs/progress.md:6968`) and for the
  backup jobs' bucket (`BackupStoragePolicy`, `internal/kube/storagepolicy.go:88`). No NFS or SMB
  volume exists in `internal/kube`. So the Jellyfin, Immich, Home Assistant, Syncthing and
  Nextcloud templates cannot use a NAS or a device on the LAN, which is half of why people run them.
* **Fit:** a per-app NetworkPolicy shaped like `BackupStoragePolicy` (the app's pods, the resolved
  addresses, the ports), with the metadata addresses never opened (`isNeverOpened`); NFS as a
  PersistentVolume with an `nfs` source and a claim the panel creates, which restricted Pod
  Security allows because the pod only names a claim; the add-server preflight checks for the NFS
  mount helper. Admin only, audited, an `errdoc` entry for an unreachable export.
* **Size:** M. **Without a cluster:** the policies and objects yes; a mount needs a cluster and an
  NFS server, which `test/cluster/verify.sh` could start in a pod.

### P2 — Back up to a disk or a share, and send only what changed

* **What:** a backup destination that is a disk on one of the servers or an NFS share, and volume
  backups that upload only changed data.
* **Evidence:** Olares backs up to a local path, a USB disk, an SMB share, S3, COS or its own
  storage, the first run full and the rest incremental with Restic
  (`manual/olares/settings/backup.md`, `developer/install/cli/backups.md`). Skifity writes to an
  S3-compatible bucket only (`docs/backups.md:12`), and every volume backup is "a compressed tar of
  everything on the volume" (`docs/backups.md:298`): a 400 GB photo library is 400 GB a night.
* **Fit:** the constraint is Skifity's rule that "the credentials never leave the panel" — a Job
  gets presigned URLs, and Restic or Kopia need a key. A small S3 proxy in the panel that signs each
  request for one backup's prefix, the way `internal/registryguard` gates the registry, keeps the
  rule. The repository password comes from the existing backup passphrase. A disk destination runs
  the Job in the panel's namespace on that server, since restricted Pod Security refuses host paths
  in an app's.
* **Size:** L. **Without a cluster:** the proxy, the keys and the scheduling yes; a backup and a
  restore need one, as every backup does today.

### P2 — Say what a template needs, and whether it fits, before installing

* **What:** the install dialog adds up the template's memory and CPU reservations, disks and GPUs,
  compares them with what the cluster has free and with the environment's ceiling, and refuses with
  the shortfall named, or offers to install it stopped.
* **Evidence:** Olares's Market shows CPU, memory, disk and VRAM per accelerator and refuses
  "Insufficient system CPU/memory" (`manual/olares/market/market.md`). Skifity checks only GPUs at
  install (`internal/api/template_install.go:136`); the templates page shows no resources
  (`web/src/pages/templates.tsx`); a template that does not fit installs, and its instances stay
  pending until the scheduler's reason is explained after the fact (`ExplainUnschedulable`,
  `internal/kube/explain.go:35`; "No server has enough free CPU or memory",
  `docs/troubleshooting.md:267`).
* **Fit:** templates already carry `mem_request_mb`, `cpu_request_m` and volume sizes per service;
  the cluster summary has allocatable figures and `QuotaUsage` the environment's. One function, one
  `errdoc` entry, locale keys.
* **Size:** S. **Without a cluster:** yes.

### P2 — Template inputs with a type and choices

* **What:** an input `type` (`email`, `url`, `domain`, `int`, `bool`) checked in the dialog and in
  the API, and a list of `options` rendered as a select.
* **Evidence:** Olares's `envs` have eight types and choices (manifest reference). Skifity's
  `Input` has key, label, help, default, secret, required and generate
  (`internal/templates/templates.go:299`), so a mistyped admin address becomes an app that fails
  later.
* **Fit:** two fields, `validate.go` in `internal/templates`, the install form. **Size:** S.
  **Without a cluster:** yes.

### P2 — Which architectures a template runs on

* **What:** record the platforms each template image publishes, refuse to install on a cluster
  without a matching server, and pin the app to matching servers.
* **Evidence:** Olares's manifest has `supportArch` and refuses mixed clusters. Skifity accepts
  x86-64 and arm64 servers, the Raspberry Pi included (`docs/quick-start.md:23`), but no template
  records an architecture and nothing in `internal/` mentions `kubernetes.io/arch`, the gap
  `kamal.md` raised for builds. An amd64-only image on a Pi fails with an exec format error.
* **Fit:** `hack/resolve_tags.py` already reads each image from its registry; write the platforms
  into the template; a node selector in `internal/kube`; a refusal at install. **Size:** S–M.
  **Without a cluster:** yes, except the scheduling itself.

### P2 — A public port for some addresses only

* **What:** an allow list on each public port, so a game server or MQTT broker can be limited to
  the LAN or to friends' addresses.
* **Evidence:** Olares exposes non-HTTP ports on the LAN and the VPN only (`ports.exposePort`).
  Skifity opens them to everyone and "The firewall does not apply to them"
  (`internal/kube/ports.go:20`).
* **Fit:** the NetworkPolicy that already admits anybody to those ports takes `ipBlock` entries
  instead; the Service needs `externalTrafficPolicy: Local` so the policy sees the visitor's
  address, which has to be shown on a cluster. **Size:** S–M.

### P2 — Set an environment's ceiling

* **What:** an admin changes an environment's CPU, memory and storage ceiling, and sees it on the
  environment page.
* **Evidence:** Olares sets CPU and memory per user. Skifity gives every environment 8 cores, 16 GB
  and 100 GB (`internal/kube/namespace.go:45-47`) and only reads it back
  (`internal/api/teams_handlers.go:675`), so a large model or database in one environment cannot be
  allowed, and a small team cannot be held to less.
* **Fit:** three columns, `PUT /api/environments/{env}/quota` (admin, audited), applied through
  `EnsureNamespace`. **Size:** S. **Without a cluster:** yes.

### P2 — Agent skills in the binary (raised in `sealos.md`)

* **What, and what Olares adds:** `sealos.md` raised skills as P1 and they are still absent. Olares
  shows the shape worth copying: the skills are compiled into the CLI, `skills install` links one
  copy into each agent's folder that exists, and the CLI warns when the installed skills came from
  a different build (`developer/cli-agent-skills.md`). Twelve skills, one per area, plus a "doctor"
  that walks a failed install to its cause, the job Skifity's `internal/kube/explain.go` already
  does for the panel.
* **Size:** S. **Without a cluster:** yes.

### P2 — Installed apps signing in with the panel (raised in `cloudron.md`)

* Olares's manifest turns on its OpenID Connect provider with `oidc.enabled` and hands the app a
  client ID, secret and issuer as values; with the identity wall above, it is how a household gets
  one sign-in for every app. Still absent in Skifity: no authorization or discovery route in
  `internal/api/api.go`. **Size:** L.

### P3 — A diagnostics bundle for a bug report

* **What:** `skifity admin diagnose` writes one archive: the panel's log (already redacted by
  `internal/logging`), cluster events, node conditions, component versions, never a variable.
* **Evidence:** Olares exports system logs from Settings and with `olares-cli logs`, and attaches
  them to a Ticket. Skifity's `admin` commands are reset-password, list-users, backup-db,
  restore-db, restore-key, plugin-key and plugin-sign (`internal/cli/admin.go:51-63`);
  `test/cluster/collect.sh` gathers the same for CI only. **Size:** S.

### P3 — Private access without a public address

* Olares's "internal" level skips sign-in for people on its VPN, and its VPN can be enforced. A
  firewall preset "only from my Tailscale network" (`100.64.0.0/10` is already a range Skifity
  knows) and a page on running Tailscale on the servers would cover most of it without building a
  VPN; Olares's own shows the cost of shipping one (#2535). **Size:** S.

### P3 — Hardware health on the server page

* Olares shows disk model, temperature, power-on hours and data written, CPU temperature, and GPU
  memory and power per task. Skifity shows CPU, memory and disk use (Phase 99). A failing disk is
  how a home server dies. **Size:** M; it needs `smartctl` on the servers.

### P3 — A presence on the LAN for apps that discover devices

* Olares's Overlay Gateway gives Home Assistant and Jellyfin an interface on the LAN for casting,
  DLNA and discovery. Skifity's restricted Pod Security refuses host networking
  (`internal/kube/podsecurity.go:18`). A macvlan interface needs Multus and a second network; it is
  L, and worth it only once the home segment is a target.

## Things to deliberately not copy

* **A vendor identity to use one's own machine.** An Olares ID, a DID on a blockchain and a phone
  app before the first sign-in; a lost mnemonic loses the identity and the apps bought with it. It
  is the most-voiced complaint (#719, #2069). Skifity's setup token and local accounts are the
  answer.
* **DNS and remote access through the vendor.** Delegating a subdomain's NS records to the vendor's
  Cloudflare, an organisation that cannot be removed, a domain that can only be chosen before
  activation, and a relay metered after 2 GB. Skifity's `sslip.io` start and any-domain
  cert-manager keep the user in charge; close the first-run HTTPS gap without a vendor in the path.
* **Paid apps settled in stablecoins with ETH for gas,** and licences that need the developer's
  server online to restore.
* **A desktop with windows in the browser.** It makes a phone a poor client (forum #46) and is a
  second operating system to maintain; Skifity's pages work at phone width.
* **A root terminal on the host in a web page, and editable YAML as the everyday tool.** Skifity's
  stance — no shell, Kubernetes under Advanced, drift detected — is the safer one.
* **A join command that carries the master's SSH password.** Skifity uses the password once and
  installs its own key.
* **Stopping apps behind the user's back** when the system is loaded. Skifity alerts on thresholds
  and lets Kubernetes evict by priority; a person decides what to stop.
* **Identity in a header the app trusts.** Two of Olares's 2026 fixes are about who may set
  `X-BFL-USER`. If Skifity puts a sign-in in front of apps, pass a signed token or strip and set the
  header in one place, tested against a client that sends its own.
* **Upgrades one minor at a time, with apps reinstalled by hand.** Skifity's panel upgrade, which
  changes the panel's image with a snapshot first and never touches the apps
  (`internal/api/upgrade.go`), is worth keeping that way as releases accumulate.
* **GPU sharing by injecting a library into every container.** Memory slicing through HAMi
  segfaulted CUDA 13 workloads; the device plugin's own time slicing is the step to take.
* **Two licences in one root.** An AGPL `LICENSE` beside a non-commercial `LICENSE.md` leaves users
  unsure what they may do; Skifity's single licence should stay single.
* **Minimums of 4 cores, 8 GB and a 150 GB SSD.** Skifity's 2 GB start is a reason to choose it.

## Sources

All read 2026-11-08. GitHub's repository API refused `beclab/Olares` from this sandbox, so the
repository was read through a `git clone` (blobless, sparse), its tags with `git ls-remote`, its
issues and pull requests through GitHub's search API, and single files through
raw.githubusercontent.com. Reddit answered 403, and web search was unavailable (the Firecrawl
allowance was used up), so user opinion comes from GitHub, Hacker News and the Olares forum.

* GitHub search API: `repo:beclab/Olares` (stars, forks, open issues, licence, created and pushed
  dates); `org:beclab` repositories; issues by reactions; open issues (126), open pull requests
  (31), pull requests merged 2026-09-22..2026-11-08 (212); issues and pull requests for security
  terms
* `git clone https://github.com/beclab/Olares` at `4d83d35` (2026-11-07): README.md, LICENSE,
  LICENSE.md, SECURITY.md, `cli/skills/`, and under `docs/`: `manual/what-is-olares.md`,
  `manual/feature-overview.md`, `manual/help/olares.md`, `manual/help/faqs.md`,
  `manual/help/known-issues.md`, `manual/get-started/install-olares.md`,
  `manual/get-started/activate-olares.md`, `manual/get-started/reusables.md`,
  `manual/olares/settings/{backup,roles-permissions,manage-team,update,manage-entrance,custom-app-domain,remote-access,gpu-resource,developer}.md`,
  `manual/olares/market/{market,shared-apps,clone-apps,purchase-paid-apps}.md`,
  `manual/olares/controlhub/index.md`, `manual/olares/resources-usage.md`,
  `manual/olares/desktop.md`, `manual/olares/files/mount-cloud-storage.md`,
  `manual/manage-system-env.md`, `manual/update-app-image.md`,
  `manual/collect-diagnostic-information.md`, `manual/best-practices/set-custom-domain.md`,
  `manual/best-practices/install-olares-multi-node.md`, `manual/space/{billing,host-domain}.md`,
  `manual/update-guides/1.12.7.md`, `manual/release-notes.md`,
  `developer/concepts/{system-architecture,architecture,network,application,account,olares-id}.md`,
  `developer/develop/{index,manage-apps,app-sys-injected-variables}.md`,
  `developer/develop/package/manifest.md`, `developer/install/versioning.md`,
  `developer/install/cli/backups.md`, `developer/cli-overview.md`, `developer/cli-agent-skills.md`
* `git show 37d9247` (#4348, per-app custom-domain docs removed, 2026-10-30)
* `git ls-remote --tags https://github.com/beclab/Olares` and the commit dates of tags 1.10.0 to
  1.12.7
* https://raw.githubusercontent.com/beclab/Olares/main/docs/reusables/custom-domain.md
* https://raw.githubusercontent.com/beclab/Olares/main/apps/packages/core/LICENSE.md
* https://raw.githubusercontent.com/beclab/Olares/main/docs/one/spec.md
* `git clone https://github.com/beclab/apps` at `0e22946` (2026-11-08): app directories with an
  `OlaresManifest.yaml`
* https://www.olares.com/blog/olares-1-12-7/ (2026-09-28) and https://www.olares.com/blog/olares-1-12-6/ (2026-07-17)
* https://www.olares.com/space/plans
* https://one.olares.com/ (Kickstarter total and backers)
* https://www.olares.com/forum/ — `about.json`, `categories.json`, `top.json`, and topics 33, 46,
  107, 109, 113, 116, 122, 123
* GitHub issues #719, #742, #850, #914, #1453, #1709, #1908, #2069, #2225, #2350, #2535, #2687,
  #2764, #2800, #2944, #3017, #3213, #4375; pull requests #2713, #2983–#2986, #3024, #3077, #3785,
  #3882, #3883, #3981, #4319, #4331, #4332, #4348, #4359, #4361 (bodies through the search API)
* https://hn.algolia.com/api/v1/search?query="olares" (stories and comments) and
  https://hn.algolia.com/api/v1/items/40535279 (Show HN: Terminus, 2024-05-31)
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=olares, `=beclab`,
  `=beclab terminus` (no results)
* https://api.osv.dev/v1/query for `github.com/beclab/Olares`, `/cli`, `/framework/app-service`,
  `github.com/beclab/app-service`, `github.com/beclab/bfl` (no results)
* Skifity at `23ec07f`: the files named in "Against Skifity" and in the gaps, and
  `docs/research/competitors/{kamal,coolify,cloudron,sailbox,sealos}.md`
