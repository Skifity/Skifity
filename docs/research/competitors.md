# Research: Competing platforms

Researched 2026-09-16, re-researched against live sources 2026-09-17. The second
pass changed the conclusions, so what follows is the second pass.

## The self-hosted panels

| Product | Orchestrator | Idle cost | Stars | State | Licence |
|---|---|---|---|---|---|
| Coolify | Docker (Swarm deprecated) | 500 MB – 1.2 GB RAM, 5–7% CPU | ~51–59k | v4.0.0 stable **April 2026**, after four years of beta | Apache 2.0 |
| Dokploy | Docker Swarm | ~350 MB RAM, 0.8–1.5% CPU | ~26–36k | v0.28.x, **still pre-1.0** | Apache 2.0 + proprietary directories |
| CapRover | Docker Swarm | light | ~13k | Stable since 2017, development slowed | Apache 2.0 |
| Dokku | Docker + herokuish | very light | — | Mature, single host | MIT |
| aaPanel | None (LAMP/Docker manager) | — | — | Mature | Free + paid Pro |

### What each one keeps its own state in

Researched 2026-09-17. This is the panel's *own* records — accounts, servers,
apps — not the databases it provisions for users, which is a different question
and the one most comparisons answer by mistake.

| Product | Its own state | What that costs to run |
|---|---|---|
| **cPanel / WHM** | Flat files: `/var/cpanel/users/*` per account, plus config under `/var/cpanel`. MySQL is for the *hosted* databases, not for cPanel's own records. | Nothing. It is text on disk. |
| **aaPanel** | SQLite, one file at `/www/server/panel/data/default.db`. | Nothing. |
| **CapRover** | Flat JSON, `config-captain.json`. | Nothing. |
| **Plesk** | MySQL/MariaDB, the `psa` database. | A database server. |
| **Coolify** | PostgreSQL 15 **and** Redis 7 **and** Soketi for websockets, beside a Laravel/PHP app and its queue workers. | Four processes before a single app of yours is running. This is most of the 500 MB – 1.2 GB. |
| **Dokploy** | PostgreSQL and Redis. | Two. |
| **Skifity** | SQLite in WAL mode, on the node's disk, through a pure-Go driver — no database server, no libc, no second process. | Nothing. This is most of the 35 MiB. |

The split is not about maturity. The two products with the largest installed
base in this list, cPanel and aaPanel, both keep their own state in files. A
control panel has one writer and a few thousand rows; the panel that brings a
PostgreSQL, a Redis and a websocket server to hold that is paying for a shape it
does not have.

Where an embedded store stops being right is a **hosted, multi-tenant** control
plane — many customers, horizontal scale, failover. SQLite is the correct answer
for one panel on one node and the wrong answer for that, which is worth knowing
before anybody designs the second thing (see "Renting the panel" below).

**Coolify** is the market leader and the reason is not technical: 280+ one-click
services, the largest Discord, the most tutorials. When a comparison recommends
it, "broader community, more one-click services, more tutorial content" is the
reason given. Its costs are a heavy runtime (Laravel, Postgres, Redis, Soketi
and workers, all running), no Kubernetes, and the security record below.

**Dokploy** is the fastest-growing, and is lighter than Coolify by roughly 3x. It
has **already adopted Railpack**, has **SSO/SAML**, which Coolify does not, and
ships an AI Docker Compose generator. Multi-node stops at Swarm, and scaling
stops at a Replicas field somebody sets by hand. Its licence is
Apache 2.0 with proprietary directories carved out.

**aaPanel is not in this category.** It is a cPanel/Plesk replacement — websites,
PHP, mail, a WordPress toolkit, a Docker manager — sold to agencies running
hundreds of client servers. Its users praise multi-user account isolation and
the WP Toolkit; its complaint is annual-only Pro pricing. It competes with
cPanel, not with a Git-deploy PaaS, and nothing here should be aimed at it.

### The security record, which is the most important fact in this table

On **8 January 2026 Coolify disclosed eleven critical CVEs in one day. Five carry
CVSS 10.0** — authenticated command injection ending in root on the host
(CVE-2025-66209 … 66213). One (CVE-2025-64420, also 10.0) let a low-privileged
user read the root SSH private key. More followed through spring: a Sentinel
token injection to host RCE (CVE-2026-34034), command injection during
deployment (CVE-2026-34038), and **an authorization bypass that let users reach
another team's servers** (CVE-2026-34592). Censys counted **52,890 publicly
reachable Coolify dashboards** at disclosure.

The analysis of why is worth quoting, because it describes a class rather than a
mistake: *"user input reaches a shell without enough sanitization, in many
independent code paths."*

That is the exact class `internal/shellsafe` exists for, and the exact bug found
in this repository in September 2026 — every generated script used Go's `%q`,
which is not shell quoting. The difference is not that Skifity avoided the
mistake. It is that there is now one place where quoting happens, a test that
runs a real shell against it, and no second path. Coolify's problem is that
there were many paths and no single place.

Dokploy has no publicly disclosed CVEs. It also has a fraction of the exposure.

## The managed platforms

| Product | What wins | What it costs | Why people leave |
|---|---|---|---|
| **Vercel** | The best PR preview workflow in the industry, image optimisation, edge middleware, native Next.js | Hobby free but explicitly not for production; Pro per-seat plus usage | A bill that is not a function of traffic you chose — one documented case is **$286 from a single morning of bot traffic** crossing a 100k-invocation line |
| **Railway** | Persistent containers, co-located databases, background jobs, genuinely liked DX | Hobby $5/mo incl. $5 credits, Pro $20/user/mo incl. $20, Enterprise from $2,000/mo | Usage pricing that is fine until it is not |
| **Heroku** | Predictability, buildpacks, nothing surprising | Performance dynos **$250–500/month each**, add-ons stacked separately | Price, and the free tier's removal — which is what created this entire category |

What all three sell is the same thing: **you push, and it is live, and you never
think about a server.** Vercel's preview-per-pull-request is repeatedly called
the best thing in the industry. That is the bar for developer experience, and it
is not a bar any self-hosted panel has reached.

## What we take from each

* **Vercel** — deploy from Git, one preview URL per branch or pull request, one-click rollback.
* **Railway** — a visual canvas of services, variables shared across a project.
* **Heroku** — detect the language and build with no Dockerfile.
* **Cloudflare Workers** — scale-to-zero for idle apps.
* **Coolify / Dokploy** — one-click templates, S3 backups, being genuinely self-hosted. Coolify's catalogue was the single most-cited reason people choose it, and at eight templates against 342 the gap was the largest we had. It is now 282, converted from that catalogue with every image resolved to a real version and verified against its registry, and the database wiring — which does not apply here — taken out. The catalogue is files rather than Go, which is how Coolify's got large in the first place.
* **Kubernetes** — self-healing, rolling updates, rollback, node failover, for free.

## Where we are actually different

Ordered by how well the evidence supports it.

1. **One place where user input becomes a shell command.** The leading product in
   this category shipped five CVSS-10.0 command injections in a day because it
   had many. This is the strongest claim Skifity has, and it is an architectural
   one: `shellsafe`, tested against a real `sh`, plus secrets sealed to the
   context they are stored in, plus one authorization layer that a handler
   cannot skip without it being visible — against a competitor whose 2026 CVE
   list includes a cross-team authorization bypass and a readable root SSH key.
2. **Footprint.** 35 MiB idle, measured, against Coolify's 500 MB–1.2 GB and
   Dokploy's ~350 MB. On the 1 GB VPS this category sells to, that is the
   difference between the panel being the problem and the panel being invisible.
3. **Scaling is a property of the system, not a runbook.** This is stronger than
   "multi-node that is not Swarm", and it is worth stating precisely because
   the leaders' own documentation states the other side of it.

   Coolify's scaling overview (read 2026-09-17) offers three paths: resize the
   server, deploy the same image to several standalone servers, or put a
   provider-managed load balancer in front. It says plainly: **"Coolify does not
   create or manage that external load balancer for you"**, and lists what
   remains the operator's: configuring the load balancer and its health checks,
   securing traffic to it, keeping sessions and uploads available to every
   instance, and **"monitoring capacity and deciding when to add or remove
   servers"**. Two further limits: **an application with persistent storage
   cannot use multi-server deployment at all**, and **Docker Swarm is now marked
   Deprecated** — "continue only as a temporary legacy setup". Load balancing in
   practice means hand-writing a Traefik file with the upstream IP addresses in
   it. Dokploy's equivalent is a **Replicas** field: "set the number of instances
   of your application that should be running".

   Neither has autoscaling. Skifity has a HorizontalPodAutoscaler on a CPU or
   memory target, scale-to-zero through KEDA with the ingress routed via its
   interceptor so a sleeping app is woken rather than 503'd, and the HPA
   suppressed when scale-to-zero is on so the two do not fight over one
   Deployment. Rescheduling, node failure and rolling updates come from k3s
   rather than from panel code.

   And the list Coolify tells an operator to work through by hand before adding
   a server — move sessions out of the container, share uploads, add a health
   endpoint — is the list `ScalingReadiness` reads out of the app's own
   configuration and reports, naming the variable that is wrong.

   **One line of Coolify's list applies here too, and it would be dishonest to
   let this section imply otherwise.** Skifity does not create or manage an
   external load balancer either. Every server runs the ingress, so an app
   answers on every server's address — but DNS names one, and if that server
   goes down the name is dead while the app is still running elsewhere. Round
   robin DNS, a floating IP or a provider's load balancer is the operator's
   choice, and `docs/adding-servers.md` says so. The difference from Coolify is
   narrower than the rest of this point: it is that the app above that front
   door genuinely reschedules, rolls and autoscales itself, not that the front
   door is solved.

   The honest limit is the same as everywhere else in this document: none of it
   has run on a real cluster.
4. **A config change does not rebuild.** The top Coolify complaint, answered by
   the build fingerprint (ADR-0007).
5. **Built for assistants.** An MCP server in the same binary, and every failure
   carrying cause, impact and fix. Nobody else in the category has this.

## Where we are not different, and the honest reading

* **Railpack is no longer a differentiator.** Dokploy already ships it.
* **SSO: closed.** This was the one straight absence on the list rather than a
  trade-off. OpenID Connect is in, with PKCE, a verified ID token, a per-sign-in
  nonce and a single-use state — Okta, Entra, Authentik, Keycloak, Zitadel,
  Google. SAML is not, and is not planned: a second protocol and a second class
  of signature bug, for providers that all speak OIDC anyway.
* **Templates: closed, but not by being better at templates.** 282 against 342, and ours are converted from theirs. What is genuinely ours is that every image names a version that was checked to exist, where more than half of Coolify's ship `latest`.
* **Kubernetes is a category mismatch, not only an advantage.** Comparison sites
  exclude k3s and Rancher from "self-hosted PaaS" as *"a different abstraction
  layer entirely"*, and one of the most-read 2026 guides is titled *"Best
  Self-Hosted PaaS to Replace Heroku (**No Kubernetes**)"*. The audience is
  actively selecting away from the thing Skifity is built on. Hiding Kubernetes
  well is therefore not a bonus feature; it is the entire bet.
* **The security advantage is architectural, not demonstrated.** Coolify has
  eleven critical CVEs because 52,890 people run it and researchers look.
  Skifity has none because nobody has looked. Better structure is a reason to
  expect fewer, not evidence of fewer. Claiming otherwise in public would be the
  same kind of unearned statement this project keeps finding in its own
  documentation.

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

- https://hostzero.com/articles/is-coolify-safe-2026-cves
- https://thehackernews.com/2026/01/coolify-discloses-11-critical-flaws.html
- https://github.com/coollabsio/coolify/security/advisories/GHSA-qqrq-r9h4-x6wp
- https://www.sentinelone.com/vulnerability-database/cve-2026-34058/
- https://www.virtua.cloud/learn/en/concepts/coolify-vs-dokploy-self-hosted-paas
- https://massivegrid.com/blog/dokploy-vs-coolify-vs-caprover/
- https://contabo.com/blog/self-hosted-paas-replace-heroku/
- https://lumadock.com/tutorials/coolify-alternatives
- https://justinmckelvey.com/blog/railway-vs-vercel
- https://resources.rework.com/tools/dev-tools/best-vercel-alternatives
- https://medium.com/@allahverdiyev.tural/your-paas-bill-lied-to-you-the-real-cost-of-railway-render-fly-io-and-vercel-in-2026-8b74074014ce
- https://www.aapanel.com/ and https://www.trustpilot.com/review/aapanel.com
- https://coolify.io/docs/core/infrastructure/scaling/overview (read 2026-09-17)
- https://coolify.io/docs/core/networking/proxy/traefik/load-balancing
- https://docs.dokploy.com/docs/core/applications/advanced
- https://northflank.com/blog/dokploy-vs-coolify
- https://coolify.io/docs/get-started/introduction and https://next.coolify.io/docs/start-with-cloud (read 2026-09-17, for Coolify Cloud's BYOS model)
- https://deepwiki.com/coollabsio/coolify (Coolify's own control-plane stack: Laravel, PostgreSQL, Redis, Soketi)
- https://www.aapanel.com/forum/ and https://fornex.com/help/database-aapanel/ (aaPanel's SQLite store)
- https://www.logicweb.com/knowledge-base/cpanel-tutorials/cpanel-directory-structure/ and https://gist.github.com/irazasyed/6488963 (cPanel's flat files under /var/cpanel)
