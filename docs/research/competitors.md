# Research: Competing platforms

Researched 2026-09-16, re-researched against live sources 2026-09-17, and on
2026-09-30 researched again **one product at a time, in depth**: twenty-two
products, each in its own file under [`competitors/`](competitors/). Every file
has the same nine parts — what the product is and runs on, a feature inventory in
fourteen groups, what users love, what they complain about, its security record,
a capability-by-capability table against Skifity with the file each Skifity
claim was checked in, the gaps worth closing, the things not to copy, and dated
sources.

This page is the summary. The numbers below come from those files, which carry
the sources and the dates they were read.

## The products

| Group | Product | What it is, in one line | File |
|---|---|---|---|
| Self-hosted, Docker | Coolify | The market leader: Laravel control plane, PostgreSQL, Redis, Soketi, 359 templates | [coolify.md](competitors/coolify.md) |
| | Dokploy | The fast grower: Next.js on Docker Swarm, PostgreSQL; still pre-1.0 (v0.30.8) | [dokploy.md](competitors/dokploy.md) |
| | CapRover | The oldest dashboard panel: Swarm, nginx, a JSON file for state, 360 one-click apps | [caprover.md](competitors/caprover.md) |
| | Dokku | The original self-hosted Heroku over SSH; has shipped a **k3s scheduler** in core since 2024 | [dokku.md](competitors/dokku.md) |
| | Easypanel | Commercial, closed-source; Swarm, SQLite state, telemetry on by default | [easypanel.md](competitors/easypanel.md) |
| | Kamal | 37signals' deploy tool: no control plane, a YAML file and kamal-proxy | [kamal.md](competitors/kamal.md) |
| Other people's apps | Cloudron | Packaged apps with maintained updates, backups before every update, SSO and mail | [cloudron.md](competitors/cloudron.md) |
| | Portainer | Container and Kubernetes UI; 3.0 is "Kubernetes-first", Portainer-Run deploys AI-built apps | [portainer.md](competitors/portainer.md) |
| Self-hosted, Kubernetes | Kubero | Heroku-style operator on your cluster; dormant, with an unpatched critical CVE | [kubero.md](competitors/kubero.md) |
| | Canine | "Coolify for Kubernetes", in its author's words; Rails, outside the cluster, never released | [canine.md](competitors/canine.md) |
| | Epinio | `cf push` for Kubernetes; stalled at SUSE, revived by Krumware in 2025 | [epinio.md](competitors/epinio.md) |
| | Sealos | A Kubernetes installer grown into a "cloud OS"; relicensed in 2025, 8 CPU / 16 GB per node | [sealos.md](competitors/sealos.md) |
| Kubernetes on your cloud | Porter | Operates EKS/GKE/AKS in your account; no longer open source | [porter.md](competitors/porter.md) |
| | Qovery | Internal developer platform on your cloud; dropped its single-server k3s offer in 2024 | [qovery.md](competitors/qovery.md) |
| | Northflank | Kubernetes underneath, hosted or BYOC; environments, workflows, preview blueprints | [northflank.md](competitors/northflank.md) |
| Managed | Vercel | The developer-experience bar: previews per pull request, instant rollback | [vercel.md](competitors/vercel.md) |
| | Netlify | Deploy previews and atomic deploys; deploy contexts per branch | [netlify.md](competitors/netlify.md) |
| | Railway | Canvas and reference variables; no built-in horizontal autoscaler | [railway.md](competitors/railway.md) |
| | Render | Service types and Blueprints (`render.yaml`); its own Kubernetes on AWS | [render.md](competitors/render.md) |
| | Heroku | Defined the category; in "sustaining engineering" since 2026-02-06 | [heroku.md](competitors/heroku.md) |
| | Fly.io | Firecracker Machines and flyctl; recurring reliability complaints | [fly-io.md](competitors/fly-io.md) |
| | DigitalOcean App Platform | An app spec as the source of truth; still no volumes after five years | [digitalocean-app-platform.md](competitors/digitalocean-app-platform.md) |

aaPanel was researched on 2026-09-17 and left out: it replaces cPanel, not a
Git-deploy PaaS.

## What the earlier passes got wrong

The 2026-09-17 conclusions were read back against the new files. Several did not
survive.

* **"Nobody else in the category has an MCP server" is false.** Coolify has had a
  remote one at `/mcp` since v4.1.0 (May 2026); Dokploy's generated one has 508
  tools; Easypanel shipped one in 2.33.0; Canine, Epinio, Porter, Qovery and
  Sealos all have one. What still sets Skifity's apart is narrower: its tools
  never return a secret or a kubeconfig. Canine's hands the stored admin
  kubeconfig to agents, and Sealos puts a kubeconfig in its MCP URL.
* **Dokploy's security record is not clean.** The earlier page said it had no
  disclosed CVEs. It has 60 published GitHub advisories — 34 critical, 42 of them
  published on 21 July 2026 — mostly command injection and cross-organisation
  access, including a CVSS 10.0 admin takeover through a hardcoded auth secret
  (CVE-2026-45631). Its current installer runs PostgreSQL only, not Redis.
* **Dokku is not a single-server tool any more**, and not clean either. Its k3s
  scheduler — cert-manager, KEDA with the HTTP add-on, Longhorn volumes — makes it
  Skifity's closest architectural relative. It published six advisories in 2026,
  five critical, all user input reaching Bash.
* **"A config change does not rebuild" is a narrower edge than claimed.** Coolify's
  documentation now sends runtime-only changes to Restart, which reuses the
  image. The difference left is that Skifity decides, where Coolify asks the user
  to pick the right button.
* **The footprint claim is about the panel, not the stack.** 35 MiB is measured;
  k3s's own documented minimum is 2 cores and 2 GB, the same as Coolify's, and
  Skifity's whole-stack footprint has never been measured.
* **Coolify's catalogue is 359 templates, not 342**, and 45% name `latest`.
  CapRover's 360 mostly do not, so "more than half ship latest" is about Coolify
  only.

## The security record across the category

| Product | What was disclosed, 2025–2026 | The class |
|---|---|---|
| Coolify | 75 CVEs in NVD, 70 GitHub advisories | 36 command injection, 14 missing authorization, 10 cross-team; an OAuth sign-in matching accounts by email (CVE-2026-86117) |
| Dokploy | 60 advisories, 34 critical | Command injection, cross-organisation access, a hardcoded auth secret |
| Dokku | 6 advisories, 5 critical (CVSS 9.0) | Input reaching Bash `eval`, heredocs and `tar` on the host |
| Portainer | 11 CVEs in 2026 | Authorization bypasses in its Docker/Kubernetes API proxy |
| Kubero | CVE-2026-92720 (9.1), unpatched | Unauthenticated API returning webhook secrets |
| Canine | A cross-account cluster lookup, fixed silently 2026-09-18 | Tenant isolation |
| Vercel | A platform breach, 2026-04-19 | Environment variables not marked sensitive were decrypted |

The pattern is the one the 2026-09-17 page named: user input reaching a shell in
many places, and authorization checked in many places. Skifity has one quoting
function tested against a real `sh` (`internal/shellsafe`) and one authorization
layer a route walk proves every route goes through. That is a reason to expect
fewer of the first two classes. It is not evidence: nobody outside has looked,
and this pass found three security defects in Skifity's own code by reading it,
listed below.

## What the research found in Skifity, and what was done about it

Reading every competitor's feature against Skifity's code found defects, not only
gaps. Each was fixed the same day, with a test that fails when the fix is taken
out, and is written up in `docs/progress.md`.

| Found by | What was wrong | Commit |
|---|---|---|
| Coolify | Single sign-on matched existing accounts by email, and treated a missing `email_verified` as verified: an identity at the provider carrying the owner's address signed in as the owner | `043d60a` |
| Northflank | A fork's preview was handed the project's shared secret variables at every deploy | `b2a0607` |
| Netlify, Qovery | Every build variable's value was written into the build Job, readable by anybody who can list Jobs; and Railpack was never given them, so they never reached the build | `cae0a4b` |
| Dokploy, Kubero, Porter, Qovery, Vercel, Netlify | Every preview copied production's `DATABASE_URL`: refused by the network policy, so previews with a database never worked, or a branch's migration against production where the policy is not enforced | `1ae3276` |
| Dokploy | Every build image followed `latest`/`master` — and pinning them showed `ghcr.io/railwayapp/railpack` is not a public image (every Railpack build would have failed to pull) and the Nixpacks image has no `nixpacks` in it | `7a909ca` |
| Dokku | A server added later joined at whatever the stable channel had moved to, possibly newer than the control plane | `15c20cc` |
| Dokploy | Apps were given `Skifity_APP` in mixed case and nothing else about themselves | `03d9b37` |
| Vercel, Netlify, Coolify | A preview's address was only in the panel; nothing reached the pull request | `bb30e03` |
| CapRover, Vercel, Netlify, Coolify, Dokploy | No way to put a password in front of a staging site or a preview | `7865bc8` |

None of these had been seen, for the reason `docs/checklist.md` gives: nothing
here has run on a cluster. Three of them — the Railpack image, the build
variables and the preview database — would have been the first thing a real run
hit.

## Gaps still open, ranked

Ranked by how many of the twenty-two products win on it and how much a person
loses without it. "Without a cluster" says whether it can be built and proven
here, or needs `make verify` on a real server.

| # | Gap | Cited by | Size | Without a cluster |
|---|---|---|---|---|
| 1 | **Closed in Phases 95 and 99.** Usage history and charts for apps and servers, the disk included; memory, CPU, restart and disk alerts | Coolify, Dokploy, Railway, Render, Heroku, Northflank, Vercel, Netlify, Canine, Kubero, Easypanel, Cloudron | M | The storage, API and charts; real numbers need one |
| 2 | **Closed in Phase 84.** Scheduled off-site backup of the panel's own database, and a restore command | Coolify, Dokploy, CapRover, Cloudron, Portainer | S–M | Yes |
| 3 | **Closed in Phases 85 and 86.** A read-only role, and members limited to projects | Coolify, Fly, Portainer, Dokploy, Easypanel, Kubero, Canine, Netlify | M | Yes |
| 4 | **Closed in Phase 87.** A remote MCP endpoint over HTTP, and read-only/destructive annotations on the tools | Coolify, Canine, Vercel, Netlify, Dokploy, Easypanel, Portainer | M | Yes |
| 5 | The environment described in a file in the repository (`skifity.yaml`), with plan and apply | Render, DigitalOcean, Porter, Railway, Portainer | L | Yes |
| 6 | **Closed in Phase 101.** A Compose file deployed as one stack of apps, reaching each other by name and port, with internal apps | Coolify, CapRover, Sealos, Render | M | Yes, except running it |
| 7 | **Closed in Phase 103.** Promote the exact image from one environment to the next | Heroku, Northflank, Vercel, Render | M | Mostly |
| 8 | **Half closed in Phase 93** (preview-only values). Still open: previews of the whole stack with a seed step | Vercel, Netlify, Railway, Render, Coolify | M | Yes |
| 9 | **Closed in Phase 100.** A Procfile's `web` and `release` lines, its other lines named, and `app.json`'s add-ons and settings | Heroku | M | Yes |
| 10 | Several processes (web, worker) from one build; service types in the new-app form | Fly, Dokku, Render | M–L | Manifests yes |
| 11 | `skifity db connect`: reach a private database from a laptop | Coolify, Fly, Sealos, Epinio | M | Partly |
| 12 | Upgrade what the installer installed: components and k3s | Kubero, Epinio | L | The plan yes; the upgrade needs one |
| 13 | **Closed in Phase 102.** Track which template an app came from, offer its updates, back up first | Cloudron | M | Yes |
| 14 | **Closed in Phase 88.** More notification channels built in: Slack, Mattermost, ntfy, Pushover | Coolify, Dokploy | S | Yes |
| 15 | **Closed in Phases 91 and 92.** SSO groups mapped to roles, and 2FA or SSO required per team | Portainer, Epinio, Dokploy | S–M | Yes |
| 16 | **Closed in Phase 104.** Encrypted, verifiable backups | Cloudron, Dokploy, Dokku | M | The crypto yes |
| 17 | **Closed in Phase 89.** Set several variables in one rollout, and import a `.env` | Dokku | S | Yes |
| 18 | **Closed in Phases 96 and 97.** Watch paths, so a push deploys only the apps it touched; a warning for framework versions with critical advisories | Vercel, Netlify, Railway, Render, Coolify | S–M | Yes |
| 19 | **Closed in Phases 94 and 98.** Locked deploys; a rollback that shows what it will change; maintenance mode with a page and an allow list | DigitalOcean, Kamal, Easypanel, Netlify | S–M | Yes |
| 20 | **Closed in Phase 90.** `SECURITY.md` and a support and upgrade policy before the first release | Epinio | S | Yes |

## Where Skifity is actually different

Ordered by how well the evidence supports it, after this pass.

1. **It installs the cluster.** Kubero, Canine and Epinio all need a cluster
   somebody already runs; Porter and Qovery operate one in a cloud account;
   Sealos installs one but asks for 8 CPUs and 16 GB per node. None of them puts
   Kubernetes on one small server with one command. That opening is real — and,
   like everything else here, Written, never run.
2. **Scaling is the system's, not a runbook.** Coolify has no replicas until v5,
   by its maintainer's own account; Dokploy has a replica count; Railway has no
   built-in horizontal autoscaler. Skifity has an autoscaler, scale to zero
   through KEDA, and a readiness checker that names what breaks with several
   instances.
3. **One place where input becomes a shell command, one place for
   authorization.** See the security table above. A structural reason to expect
   fewer of the category's commonest CVEs, and not yet a demonstrated one.
4. **Secrets that stay secret.** Write-only variables sealed to where they are
   stored — the design Vercel moved to in August 2026 after its breach — MCP tools
   that never return one, and, since this pass, build variables that never touch
   a Job.
5. **Hiding Kubernetes well is still the whole bet.** Portainer's pivot to a
   Kubernetes-first 3.0 with a product for deploying AI-built apps is the same
   pitch sold to enterprises; the "No Kubernetes" guides are the audience the
   bet is against.

## Things deliberately not copied

Collected from the per-product files, where each says why.

* A web terminal into the host or a container (two CVSS 9.9 CVEs in Coolify).
* Shell strings built from user input in more than one place.
* Metered billing, or a quota that pauses a running site.
* SSO, audit logs or roles behind a licence (Dokploy since January 2026, CapRover's
  paid two-factor).
* Telemetry on by default (Easypanel, and Vercel's Claude Code plugin in April 2026).
* Copying production data into previews.
* An admin kubeconfig in an MCP URL or an agent's hands.
* Unpinned images in the catalogue or the build.
* Installing into somebody's existing cluster before the one-server install is
  proven — it pulls effort away from the thing nobody else does.

## Renting the panel out, with the customer's own servers

A question worth writing down because the answer is architectural rather than
commercial. Coolify Cloud is the reference implementation of the model:
**their control plane, your servers**. It runs on Hetzner in Falkenstein,
connects out to customer machines over SSH from published IP addresses, and
requires nothing installed on the customer's side but Docker and sshd. The
customer keeps the operating system, the network and the workloads.

Skifity cannot do that today, and the reason is one sentence: **the panel runs
inside the cluster it manages.** It is a Deployment in `skifity-system`, pinned
to the node holding its SQLite file, using an in-cluster ServiceAccount. A
hosted control plane is the opposite shape — outside, holding credentials for
many clusters.

Three ways to get there, in order of what they cost:

1. **One panel per customer.** Install it on their server, sell support,
   updates and the catalogue. Almost everything needed exists; what does not is
   licensing, update orchestration across a fleet, and any view of that fleet.
   This is what most "sell a panel" businesses actually are, and it is the only
   one of the three that does not put customer credentials in your building.
2. **Hosted control plane, customer's servers** — Coolify Cloud's model. For
   Skifity this means: the panel manages N clusters instead of living in one,
   SQLite becomes PostgreSQL because it is now multi-tenant with failover, and
   accounts become a layer above teams. The security shape changes completely:
   your infrastructure holds every customer's cluster credentials and becomes
   the highest-value target in the system.
3. **Hosted control plane with an agent that dials out.** Same as (2) without
   needing inbound access to the customer's network, which is what makes home
   servers and strict corporate networks possible. More work, better reach.

Where Skifity is genuinely better placed than Coolify for (2) and (3): the thing
it would talk to is a **Kubernetes API server**, not a Docker socket over SSH.
That means a scoped ServiceAccount token with RBAC and a short expiry, rather
than root over SSH — a hosted control plane that is compromised loses much less.
Coolify Cloud holds SSH access to every customer's machine; the equivalent here
could hold a token that may only touch the namespaces it created.

None of that is built. It is written here so the choice is a decision rather
than a drift.

## Sources

Each file under [`competitors/`](competitors/) lists its own, with the date each
was read. The 2026-09-17 sources for this page's earlier version are in the
repository history.
