# Netlify

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail. Brought up to date on
> 2026-11-08: facts about Netlify that changed are corrected in place, and "Since 2026-09-30"
> says what changed, with its sources.

Researched 2026-09-30 against live sources. Skifity's side of every comparison was
checked in the code at commit `7865bc8` (2026-09-30). Two features landed in
Skifity while this was being written — commit statuses and pull request comments
(`bb30e03`) and a password in front of an app (`7865bc8`) — and both are counted
below as what they are: Written, never run.
Much of the preview-workflow analysis overlaps with `vercel.md`; this file keeps
the evidence Netlify adds and cross-references the rest.

Netlify is the platform that coined "Jamstack" and made the deploy preview a
default: a pull request gets a live URL, every deploy is atomic and immutable, and
rolling back is publishing an earlier one. It is for web teams building static and
server-rendered sites on any framework — it deliberately treats Astro, Nuxt,
SvelteKit, Eleventy, Hugo and Next.js alike — and since 2025 it has repositioned
around "agent experience" (AX): AI tools such as Bolt.new deploy to it, and its own
dashboard runs coding agents. **Architecture:** a closed, proprietary control plane
in front of Netlify's CDN; compute is serverless functions (AWS Lambda-based, with
15-minute background and cron-triggered variants) and edge functions written
against a Deno-style runtime, which since September 2026 run in Unikraft MicroVMs
inside Netlify's own edge network instead of on a hosted execution service
(Netlify blog, 2026-09-29; this line said only "Deno-based" on 2026-09-30) —
**there is no way to run a container or a long-lived process**. State lives in
**Netlify Blobs** (key-value/object store), **Netlify Database** (managed Postgres
built with Neon, GA the week of 20 April 2026) and Forms. **Licence:** the service
is proprietary; the CLI (`netlify/cli`) is MIT. **Pricing:** since 4 September 2025
new accounts are on **credits** — Free (300 credits, hard cap, sites pause), Personal
$9/month (1,000), Pro from $20/month (3,000, **unlimited seats**, tiers up to 20,000
credits for $126/month since 2026-07-14, rollover on 5,000+), Enterprise custom (on
2026-11-08 the public pricing page also carries two test prices, $12 or $15 for
Personal and $20 or $25 for Pro; see "Since 2026-09-30"). A
production deploy costs 15 credits (about $0.10), compute 10 credits per GB-hour,
bandwidth 20 credits per GB (about $0.13), web requests 2 credits per 10K, AI
inference 180 credits per dollar; previews, branch deploys, failed deploys,
rollbacks and form submissions are free. Accounts from before that date keep their
legacy plan; switching is irreversible. **Maturity and adoption:** founded 2014,
launched 2015 by Mathias Biilmann and Christian Bach; $212M raised, last a $105M
Series D at a $2B valuation (November 2021); acquired Gatsby and Stackbit in 2023;
revenue estimated at $46.3M ARR for 2024 (Latka, an estimate). Netlify reported
crossing **10 million developers on 24 December 2025**, doubling from 5 million in a
year that it attributes to AI app builders; Bolt.new alone put 1 million sites on
Netlify between November 2024 and March 2025. On GitHub (API, 2026-09-30)
`netlify/cli` has 1,920 stars and 158 open issues; the `netlify-cli` npm package
shipped 70 stable releases in 2026 up to 27.10.2 on 2026-09-29.

## Feature inventory

### Deploy sources and builds

* Git: GitHub, GitLab, Bitbucket, Azure DevOps and Cursor Origin. The CLI
  (`netlify deploy`, and since 2026-03-27 `--allow-anonymous`, which deploys with no
  account and lets the project be claimed within an hour), the API, **Netlify
  Drop** (drag a folder onto a page), and **Agent Runners** (a prompt in the
  dashboard makes changes and a preview).
* Framework detection fills in the build command and publish directory for Angular,
  Astro, CRA, Eleventy, Express, Gatsby, Hugo, Jekyll, Next.js (through Netlify's
  own adapter on OpenNext and, from 2026, the Next.js Adapter API), Nuxt, React
  Router, Remix, SolidStart, SvelteKit, TanStack Start, Vite, VuePress and more; a
  Frameworks API for framework authors; build plugins that run during the build.
* **Build cache**: every `node_modules` in the repository is cached (plus
  language caches such as `~/.cargo/registry`); a deploy can be retried from the
  latest commit "with the clear cache option", which the docs recommend as the first
  thing to try when a build fails. On credit plans **build minutes are no longer
  metered** — the production deploy fee replaced them; Pro gets 3+ concurrent
  builds.
* **Monorepos**: a *base directory* (where dependencies install and the cache
  lives) and a *package directory* (where the site and its `netlify.toml` are,
  settable only in the UI). On import Netlify scans the repository and lists the
  sites it finds. By default **any change under the base directory rebuilds every
  connected site**; the documented fix is a custom `ignore` command in each site's
  `netlify.toml`.
* Build failures: **"Why did it fail?"** (AI diagnosis, since March 2024) explains a
  failed deploy and offers "Copy analysis for use in AI tools". New deploys of
  React/Next.js versions affected by React2Shell were **blocked** in December 2025.

### Domains, TLS and routing

* Automatic HTTPS, Netlify DNS, custom domains on every plan, branch subdomains.
  URLs: `deploy-preview-<n>--<site>.netlify.app` per pull request,
  `agent-<run id>--<site>.netlify.app` per agent run, and a **permalink per
  deploy** whose contents never change.
* `_redirects`/`_headers` or `netlify.toml` for redirects, rewrites and proxying;
  edge functions; the **Image CDN** (on-demand transforms); caching with
  stale-while-revalidate, cache tags, durable cache and a Cache API.
* High-Performance Edge and a 99.99% SLA on Enterprise.

### Databases and services

* **Netlify Database**: managed Postgres (with Neon), compute that scales to zero,
  storage free until 2026-07-01, billed in credits. A **built-in migration system**
  applies migrations committed to the repository during every production deploy and
  every deploy preview. **Every deploy preview and every agent run gets its own
  database branch "with a copy of the production data taken when the deploy preview
  is first created"**; changes never reach production until a production deploy.
  Netlify calls itself "the only platform that natively pairs Deploy Previews with
  completely isolated database branches".
* **Forms** (free and unlimited on credit plans, spam filtering), **Identity**
  (deprecated in February 2025 in favour of an Auth0 extension, then un-deprecated
  on 2026-02-19 after pushback), **AI Gateway** (GA 2025-12-16; model keys managed by
  Netlify, usage in credits, open models through OpenRouter), Async Workloads.
* Extensions (Netlify SDK) for Auth0, Neon, Supabase and headless CMSs.

### Storage and backups

* **Netlify Blobs**: site-wide stores (shared across deploys) and **deploy-scoped
  stores** that roll back with the deploy and are deleted with it; objects up to
  5 GB; eventual consistency (edge caches converge within 60 seconds) or strong
  consistency per store or per read; five regions; not available to Go functions.
* Deploys are retained **30 days on free plans, 90 on paid, up to 365 on
  Enterprise**; the published deploy and the latest production and branch deploys
  are never deleted.
* No volumes, no user-facing database backup product beyond the managed Postgres.

### Scaling and high availability

* Functions and edge functions scale per request; the CDN is global. Functions time
  out quickly (scheduled functions 30 seconds); background functions run up to 15
  minutes and are credit/Enterprise only. There is nothing to scale by hand and
  nothing that can hold a connection open for long.
* Atomic deploys: a deploy is either fully live or not at all.

### Observability (logs, metrics, alerts, uptime)

* **Observability**: request data (status codes, methods, cache status, regional
  latency, user agents), function and edge-function logs with full-text search.
  Retention **24 hours on Free and Personal, 7 days on Pro, 30 days on Enterprise**.
  No alerting and no API to this data.
* Real User Metrics and Web Analytics as separate products; "30-day analytics &
  metrics" on Pro.
* **Log drains: Enterprise only** — Datadog, New Relic, Axiom, Azure Monitor, Sumo
  Logic, Splunk Observability Cloud, Logflare, Amazon S3 or any HTTP endpoint
  (JSON/NDJSON), for traffic, function, edge-function, **deploy** and WAF logs, live
  within about five minutes of configuration; function output truncated at 4 KB per
  invocation.
* No uptime monitoring or status page.

### Security, auth, roles, SSO, audit

* **Firewall Traffic Rules** on every plan: block or allow by IP/CIDR and by country
  or ISO 3166-2 subregion, with **separate rule sets for the published deploy and
  for unpublished ones** (previews, branch deploys). Limits: 2 rules / 3 IPs per rule
  on Free, 10 / 50 on Pro, 500 on Enterprise.
* **Rate limiting**: in code — a `rateLimit` block in a function's or edge
  function's `config`, or on a redirect in `netlify.toml` — on **every plan** (2
  rules on Free/Personal, 5 on Pro), aggregated per IP and domain, answering `429`
  or rewriting to a page; UI rules, per-domain aggregation and 100 rules need
  Enterprise with High-Performance Edge.
* **WAF**: a baseline subset of the OWASP Core Rule Set with anomaly scoring, active
  or passive mode, per-rule block/log/disable — **Enterprise with High-Performance
  Edge only**.
* **Access control for sites**: on credit plans, **project visibility** — public,
  private or (Pro only) a shared password, set separately for production and for
  previews. A private project is enforced with a Netlify login: on Free and
  Personal only the Team Owner can see it, on Pro any invited member. Teams created
  on or after 2026-07-28 start with new projects private, and previews stay
  private unless that is changed. Since 2026-10-07 functions and edge functions
  on a private or team-login project read the signed-in visitor as `context.user`
  (id, email, expiry). **Team login** (only members of the Netlify team, SSO
  through the IdP, strict SSO mode) remains Enterprise, which has no project
  visibility setting. On 2026-09-30 this line said credit plans called the
  password "project visibility"; visibility is a separate setting, and the
  private-by-default change was already two months old.
* **Secrets Controller**: a variable marked "contains secret values" is
  **write-only** in the UI, can be restricted further, and is covered by **secret
  scanning**: a build whose output contains a secret variable's value fails, and
  the deploy log says where. **Smart detection** (Personal and above) goes further
  and scans repository code and build output for anything that looks like a secret
  without being told which values to look for; false positives are safelisted with
  `SECRETS_SCAN_SMART_DETECTION_OMIT_VALUES`. A **sensitive variable policy** decides
  whether untrusted deploys (pull requests from forks of public repositories) get
  sensitive variables at all.
* Roles: Owner, Developer, Publisher, Internal Builder (edits through Agent
  Runners, cannot publish), Git Contributor, **Reviewer** (can open previews and
  leave feedback; Pro and above on credit plans), Billing Admin (Enterprise).
  **SSO and SCIM are Enterprise**; a team audit log records variable changes among
  others.

### Preview environments and branches

* **Deploy Previews** for every pull/merge request, rebuilt on every push, with a
  **status on the pull request** that updates as the deploy progresses and a
  **comment with the preview link** when deploy notifications are on; `@netlify
  /path` in the PR description sets the page the link opens.
* **Branch deploys** (all branches or a list) with their own subdomain; previews
  and branch deploys **cost no credits**.
* **Environment variables per deploy context**: Production, Deploy Previews, Branch
  deploys, Preview Server, Local development, and **a specific branch or wildcard
  pattern** (`release/*`), each with its own value; scopes (builds, functions,
  runtime, post-processing) on Pro and Enterprise; team-level shared variables;
  `netlify.toml` values override the UI's. Values up to 5,000 characters.
* **The Netlify Drawer** on every preview: comments, screenshots and video, device
  sizes, filing feedback into the pull request or an issue tracker; Reviewers can use
  it for free.
* **Rollback** is publishing an earlier deploy: "This doesn't trigger a new deploy
  but instead publishes a previous atomic deploy … Rollbacks are instantaneous."
  **Locked deploys** ("Lock to stop auto publishing") keep building new deploys but
  leave the published one in place until someone publishes another.
* Previews get their own **database branch** (above), and deploy-scoped Blobs stores
  keep data consistent with the code on rollback.

### Templates and catalogue

* Starter templates and "Deploy to Netlify" buttons, integrations with headless
  CMSs, the Visual Editor (from Stackbit). Like Vercel, the catalogue is of
  repositories to fork, not self-hostable services: nothing in it needs a disk.

### CLI, API, IaC, integrations

* `netlify` CLI (MIT) for deploys, variables (`env:import`, export as `.env`), logs,
  functions, `netlify dev` for local emulation; a REST API; build plugins;
  extensions through the Netlify SDK; outgoing webhooks.
* **Netlify MCP server**: remote at `https://mcp.netlify.com/mcp` (the address
  `https://netlify-mcp.netlify.app/mcp` named here on 2026-09-30 still works for
  existing connections) or local
  (`npx @netlify/mcp`), personal access token or OAuth; covers the CLI's commands,
  Git and manual deploys, variables, forms, extensions and logs; agent skills in
  `netlify/context-and-tools`.
* Anonymous CLI deploys that an agent can hand to a human to claim.

### Notifications

* Deploy notifications to email, Slack, outgoing webhooks and the Git host (commit
  statuses, pull request comments); form submission notifications; usage and credit
  notifications.

### Multi-server and networking

* Not applicable: there are no servers. Function regions can be chosen; High-
  Performance Edge and private connectivity are Enterprise.

### Team and collaboration

* **Unlimited seats** on the credit-based Pro plan ("Team member seats are unlimited
  and included starting in the base $20/month plan"); Reviewers for non-developers;
  the Drawer for feedback; the Visual Editor and the Publisher role for content
  editors; Internal Builders who change sites through Agent Runners without
  touching configuration.

### Developer experience and onboarding

* Drop a folder or connect a repository and get a URL; framework settings filled
  in; `netlify dev` locally; failed deploys explained by "Why did it fail?".
* **Agent experience**: Agent Runners (announced 2025-10-01) run Claude Code, Codex,
  Gemini and OpenCode against a live project, each run on its own branch with a
  deploy preview and an isolated database branch; the AI Gateway gives the app model
  access without keys; the MCP server and anonymous deploys serve outside agents;
  Bolt.new and other builders deploy to it with one button.

## What users love

* **Deploy previews, atomic deploys and instant rollback as defaults.** "Deploy
  previews, atomic deploys, and instant rollback as platform defaults" is how the
  Bejamas 2026 review sums up why teams stay; the Coolify thread on Hacker News
  (April 2025, 382 points) shows people moving to self-hosted panels specifically to
  keep "automatic preview branches from pull requests" and "wildcard subdomains".
* **Framework neutrality.** Netlify "treats Astro, Nuxt, SvelteKit, Eleventy, Hugo
  and Next.js as equal citizens" (Bejamas), which is why multi-framework agencies
  choose it over Vercel.
* **No seat tax on Pro.** "Flat-price Pro removes the seat tax" for teams where many
  non-developers approve previews (Bejamas); the credit Pro plan has unlimited seats.
* **Free static hosting and the simplest possible deploy.** The positive Trustpilot
  reviews (2.0/5 overall from 77) praise free hosting for simple sites and "tremendous
  value" for managing many static sites and dev environments (2026-08-21); Netlify
  Drop and Bolt.new's one-button deploy are why AI-built sites land there.
* **Deploy previews with a database of their own** is new (GA April 2026) and too
  recent for community sentiment; it is the feature most worth watching.

## What users complain about

* **Credit pricing, and paying per deploy.** The Netlify forum thread "Credit-based
  Billing is Terrible" (2025-12-10): 277.6 of 300 monthly credits gone in one day
  from 16 deploys and one AI request — "1 deploy is 15 CREDITS … now, I can't deploy
  anymore" — and another user: "The introduction of credit based pricing was also a
  price increase for most". No staff reply in the thread. Trustpilot (2026-06-17):
  "300 credits for a static website with only 1 visitor (me) in one day".
* **Everything pauses when credits run out.** Netlify's docs: "all of your web
  projects (sites/apps) are paused and visitors … will find a `Site not available`
  page". On Free there is no way to buy more.
* **A billing flag that disagrees with the balance.** Several forum threads from
  July to September 2026 report production deploys paused "despite 29.9/30 credits",
  "despite 28 credits remaining", matching "the known July/August 2026 credit-flag
  bug": the banner says credits are used up while Usage & Billing says otherwise.
* **Support and suspensions.** Trustpilot 2.0/5 from 77 reviews, 64% one-star:
  slow or absent support even on paid plans, a "five day" outage with little help
  (2026-09-05), accounts frozen without explanation.
* **The bill that defined the category's fear.** February 2024: $104,500 for 190 TB
  over four days on a free static site at $55 per 100 GB, cut to $5,225 by support
  and waived by the CEO only after it trended on Hacker News. It predates the credit
  model, which now pauses instead — the complaint above is the other side of that
  trade.
* **Next.js lags.** Netlify runs Next.js through its own runtime, and "adapter-based
  support structurally trails the framework" on ISR and image optimisation
  (Bejamas); the stable Next.js Adapter API (16.2, March 2026) is meant to close it.
* **Lock-in and whiplash.** "The moment Forms and Identity hold something the
  business needs, you've bought a dependency, not a feature" (Bejamas); Identity
  was deprecated in 2025 and reinstated in 2026, with CMS projects (Decap, Sveltia)
  telling users to stop relying on it in between.
* **Security gated to Enterprise.** WAF, log drains, UI rate-limit rules, team-login
  protection with SSO and SSO itself all need Enterprise. Keeping previews behind a
  Netlify login does not: credit plans have had private previews through project
  visibility since 2026-07-28, which this line missed on 2026-09-30.

## Security record

* **No breach of Netlify's own platform found for 2025–2026.** Searched Netlify's
  security changelog, news coverage, the GitHub Advisory Database and Hacker News
  (Algolia API, stories since 2025-01-01): the top Netlify-related stories are not
  about Netlify security at all.
* **Advisories in Netlify's own packages** are old: `netlify/gotrue` (critical,
  June 2021; moderate, February 2022), `@netlify/ipx` (moderate, September 2022),
  Netlify CMS (moderate, August 2023). The 2025–2026 advisories matching "netlify"
  are in `@astrojs/netlify` (moderate 2026-06-16, low 2026-07-20; maintained by
  Astro) and `nitro` (moderate 2026-05-06).
* **React2Shell (CVE-2025-55182 / CVE-2025-66478), December 2025.** Netlify deployed
  a platform patch at 14:00 UTC on 3 December ("all Netlify customers are not
  vulnerable"), added mitigations for variants on 6 December, **blocked deploys of
  affected versions**, and on 8 December advised rotating credentials for vulnerable
  deployments; it reported no evidence of exploitation.
* **CVE-2025-29927 (Next.js middleware bypass, March 2025)**: Netlify-hosted sites
  were not affected (routing is decoupled), unlike self-hosted `next start`.
* **2026 framework advisories** tracked in Netlify's changelog: an RSC denial of
  service (CVE-2026-23864, CVSS 7.5, January), nine Next.js vulnerabilities fixed in
  15.5.21/16.2.11 (July), two critical Next.js RCEs fixed in 15.5.24/16.3.3 (August)
  whose affected code paths Netlify says it does not run.

**What this means for Skifity.** As with Vercel, the platform's advantage was not
code quality but position: a managed edge can patch or block one vulnerable
framework version for every customer at once. A self-hosted panel gets that only if
it knows what versions it is deploying (see gap 6).

## Against Skifity

Skifity statuses follow `docs/checklist.md`: almost everything cluster-facing is
**Written, never run** (ADR-0010). "Parity" means the feature exists in code with
unit tests, not that it has been seen working.

The Netlify column was brought up to 2026-11-08. The Skifity column is as read on
2026-09-30, and "Since 2026-09-30" lists the rows it no longer describes.

| Capability | Netlify | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | Git (5 hosts incl. Azure DevOps), CLI, anonymous deploys, Drop, Agent Runners; no containers | Git (GitHub, GitLab, Gitea, generic) `internal/api/webhook_handlers.go`; images; folder upload from CLI or browser `internal/cli/up.go`, `internal/upload`; Railpack/Nixpacks/Dockerfile/static `internal/builder/job.go`. Written | Parity on sources; **Skifity ahead** on what can run (any long-lived container) |
| Framework detection | ~25 frameworks, fills build command and publish dir | Node/Python/Go/PHP/Ruby/Rust/Java frameworks `internal/builder/detect.go`, plus databases and on-disk data the app needs `internal/builder/needs.go` | Parity; Skifity ahead on needs |
| Build cache | `node_modules` and language caches; retry with the cache cleared | BuildKit registry cache `internal/builder/job.go`; no bypass | Behind (no bypass) |
| Monorepo | Base + package directory, detects sites, `ignore` command | `root_dir` per app; every app on the repo+branch rebuilds on every push | Behind |
| Build failure explanation | "Why did it fail?" (AI), copy for AI tools | Every failure has cause, impact and fix `internal/errdoc/catalogue.go`, with a copy-for-assistant button (README) | Parity (deterministic rather than generated) |
| Config change without rebuild | New deploy needed (and 15 credits if production) | Runtime variable change is a rollout (ADR-0007) | **Skifity ahead** |
| **Domains, TLS and routing** | Auto HTTPS, DNS, CDN, image CDN, redirects, edge functions | cert-manager, automatic `<app>-<env>` address `internal/kube/naming.go`, TLS only with a wildcard domain `internal/cluster/cluster.go`, Cloudflare tunnel `internal/cluster/tunnel.go`. Written | Behind on edge features; parity on domain + HTTPS |
| **Databases and services** | Netlify Database (Postgres), Forms, Identity, AI Gateway | PostgreSQL (CloudNativePG), Redis, MySQL linked as sealed variables `internal/dbsvc/manager.go`; any service as a container; 283 templates. Written | **Skifity ahead** on range; behind on preview branches |
| **Storage and backups** | Blobs (site- and deploy-scoped); deploys kept 30/90/365 days | Volumes and S3 backups for databases and volumes `internal/backup`; 10 images kept per app for rollback `internal/registry/registry.go`; no object store of its own | Different: ahead on disks/backups, behind on managed blob |
| **Scaling and HA** | Automatic, global CDN, 15-minute ceiling on work | HPA, scale-to-zero via KEDA `internal/kube/manifests.go`, readiness check `internal/deploy/scaling.go`; jobs and one-off commands with no 15-minute ceiling | Behind on global; **ahead** on long work. Written |
| Rollback | Publish an earlier atomic deploy, instant, free | New deployment with old image and old settings `internal/deploy/deployer.go`; a rolling update | Parity on intent; ahead on settings; behind on speed |
| Locked deploys | Build but do not publish until someone does | None: every push to the branch deploys (`dispatchGitEvent`) | Absent in Skifity |
| **Observability** | Requests, function logs, search; 24 h / 7 d / 30 d; drains on Enterprise | Live and previous-container logs `internal/api/stream_handlers.go`; instance usage; server metrics; panel metrics `internal/api/metrics_handlers.go`; no retention, search, request metrics or drains | Behind |
| Alerts / uptime | None in Observability; usage notifications | Deploy, health, server, backup, certificate events `internal/notify/notify.go`; no uptime checks | Parity (neither) |
| **Security, auth, roles, SSO, audit** | Roles incl. Reviewer; SSO/SCIM Enterprise; audit log | Argon2id, TOTP, OIDC free `internal/api/sso_handlers.go`, three roles `internal/store/models.go`, scoped tokens `internal/auth/scopes.go`, audit log; **Works** | **Skifity ahead** on SSO; behind on a reviewer role |
| Secrets | Write-only secret values, secret scanning of build output, sensitive-variable policy for forks | Sealed and write-only `internal/crypto`; secret by heuristic `logging.LooksSecret`; logs scrubbed `internal/deploy/build.go`; forks get no secrets `copyPreviewVariables`; **no scan of build output** | Ahead on storage; behind on output scanning |
| Firewall / WAF | IP and geo (incl. subregions) on all plans, separate rules for previews; code rate limits all plans; OWASP WAF Enterprise | Allow/block over address, country, ASN, host, path, method, UA, header `internal/edgerules`, `internal/guard`; no rate limits; not copied to previews; never run through Traefik | **Ahead** on rule expressiveness (ASN, headers, nesting); behind on rate limits and previews |
| Site access control | Password (Pro); private projects and private previews behind a Netlify login on every credit plan (since 2026-07-28), the visitor passed to code as `context.user` (since 2026-10-07); team login with SSO (Enterprise) | A username and password per app on the Firewall tab, Traefik basicAuth, bcrypt hash only, admin-only, inherited by previews `internal/api/password_handlers.go`, `internal/kube/password.go` (`7865bc8`); never run through Traefik; no login with panel accounts | Parity on the password; behind on a login, which Netlify now has on every plan, not only Enterprise |
| **Preview environments** | Per PR, status + comment, branch deploys, Drawer, context variables, DB branch, free | Per PR in its own namespace, fork gets no secrets, removed on close or after 7 idle days `internal/api/webhook_handlers.go`, `internal/watch/watch.go`; commit status + one edited PR comment `internal/deploy/gitreport.go` (`bb30e03`); the app's password carried over (`7865bc8`); no branch deploys, no context variables, no database of its own | Behind |
| Environments | Deploy contexts (prod, previews, branches, branch patterns) | Unlimited named environments per project, each a namespace; per-app variables; project shared variables apply to all environments | Ahead on real environments, behind on per-context values |
| Cron | Scheduled functions, 30 s limit, published deploy only | Scheduled commands in the app's image, per minute, no time limit `internal/cron`, `internal/kube/runjob.go` | **Skifity ahead**. Written |
| **Templates and catalogue** | Repositories to fork | 283 self-hosted services `internal/templates/catalogue` | Different category; Skifity ahead for services |
| **CLI, API, IaC** | CLI, REST API, build plugins, SDK extensions | CLI with `--json` `internal/cli/commands.go`, REST API, export to `kubectl apply` `internal/api/export_handlers.go`, plugins as containers `internal/plugins` | Parity |
| MCP / agents | Remote and local MCP, anonymous deploys, Agent Runners, AI Gateway | stdio MCP `internal/mcpserver/server.go` with `deploy_folder`; `llms.txt`; errors written for assistants | Behind on remote MCP; parity on agent deploys |
| **Notifications** | Email, Slack, webhooks, Git statuses, form alerts | Telegram, Discord, webhook, email, plugin kinds `internal/notify`; Git statuses and PR comment `internal/deploy/gitreport.go` | Parity. Written |
| **Multi-server and networking** | N/A | Servers added over SSH, k3s `internal/provision` | N/A (Skifity's category) |
| **Team and collaboration** | Unlimited seats, Reviewers, Drawer, Visual Editor | Teams, invitations, three roles; no reviewer role, no feedback on previews | Behind |
| **DX and onboarding** | Seconds to a URL, no server | A server and an unreleased installer (`docs/checklist.md`), then repository or folder to URL | Behind (the install) |
| Cost model | Credits; production deploys cost money; sites pause at zero | A server's fixed price; deploys free; running apps never paused by a quota | **Skifity ahead** |

### Reproducible on your own servers, and what is tied to an edge network

Netlify adds three things to the Vercel analysis (`vercel.md` has the full table):

| Netlify experience | On your own servers? | What it takes, or why not |
|---|---|---|
| A database branch per preview **with a copy of production data**, instantly | **Partly** | Neon branches in seconds because its storage is copy-on-write. On k3s's default local-path storage a copy is a full dump and restore (or a CloudNativePG cluster bootstrapped from a backup): minutes and a full disk copy per preview. Instant branching needs snapshot-capable storage (ZFS, LVM thin, Longhorn), which a 1 GB VPS does not have by default. An **empty or seeded** database per preview is cheap and realistic. |
| Migrations applied in the deploy lifecycle, previews first | **Yes** | Skifity already has a release command that runs before traffic moves (`ReleaseCommand`, `internal/store/models.go`), and previews copy it — it needs the preview to have a database (gap 1). |
| Instant rollback | **Yes for static sites, nearly for servers** | A static site is a small nginx image; keeping the previous one's pod warm costs a few MiB, so switching back can be a Service selector change. A server-rendered app needs its old pods started again. |
| Deploy contexts and branch-pattern variables | **Yes** | Control-plane logic. |
| Separate firewall rules for previews and production | **Yes** | Skifity's rules are per app; a preview is an app. |
| Secret scanning of build output | **Yes** | A step in the build Job. |
| Forms, Identity, Blobs | **Yes, as services** | Templates or plugins, not platform primitives — which is also how to avoid their lock-in. |
| Edge functions, Image CDN at the edge, High-Performance Edge, WAF with platform-wide attack data | **No** | These are the edge network. Put one in front (the Cloudflare tunnel) rather than imitate it. |
| Credit exhaustion pausing sites | **Not needed** | There is nothing to exhaust but the server. |

## Gaps worth closing in Skifity

Several of these are the same gaps as in `vercel.md`; where they are, the number
there is given and only Netlify's additional evidence is repeated. None is something
Skifity already has.

### P0

1. **A preview of an app with a database gets a database of its own** (`vercel.md`
   gap 1). Netlify's evidence is the strongest in the market: every deploy preview
   and agent run gets a database branch, and migrations run against it during the
   preview's deploy. In Skifity, `copyPreviewVariables` hands the preview production's
   `DATABASE_URL`, which names a Service in the production namespace that the
   preview's NetworkPolicy cannot reach (`internal/kube/namespace.go`), so the copied
   release command fails. Start with an **empty** database per linked one (M), then
   an opt-in **seed from the latest backup** using the restore path that already
   exists in `internal/backup` (L). Do not default to a copy of production data —
   see "Things to deliberately not copy". *Without a cluster:* the orchestration,
   yes; a database starting, no.

2. **Values per deploy context** (`vercel.md` gap 2). Netlify goes further than
   Vercel: a value for Deploy Previews, for Branch deploys, and for a **branch name
   or wildcard pattern**. Skifity's previews inherit production's values wholesale
   and shared variables are per project. A `context` on each variable
   (`production`, `preview`, later a branch pattern), resealed per app as today.
   *M; no cluster needed.*

### P1

3. **Locked deploys.** *What:* an app can be locked: pushes still build (and are
   listed as ready), but nothing goes live until someone publishes one — from the
   Deployments tab, `skifity deploy --publish <n>`, or the MCP. A rollback offers to
   lock at the same time. *Evidence:* Netlify's "Lock to stop auto publishing" is
   the simple half of Vercel's staged production; in Skifity a rollback lasts until
   the next push (`dispatchGitEvent` deploys every push to the branch). *Fit:* a
   `locked` flag on apps honoured by `internal/deploy` after the build step; the
   existing apply path publishes. Pairs with promotion across environments
   (`vercel.md` gap 4). *Size:* S–M. *Without a cluster:* yes.

4. **Scan build output for secret values.** *What:* after a build, look for the
   exact values of the app's secret variables in what was produced (a static site's
   output directory, `.next/static`, and similar client bundles) and fail the deploy
   with an `errdoc` entry naming the variable, not the value. *Evidence:* Netlify
   fails a build whose output contains a secret value, because a build-time secret
   ending up in a JavaScript bundle is the most common way secrets leak from
   frontend deploys. Skifity lets a variable be both secret and "needed while
   building" (`Variable.BuildTime`, `internal/store/models.go`), scrubs logs
   (`internal/deploy/build.go`), and never inspects the output. The same path has a
   second, related weakness worth fixing in the same change: build-time values reach
   the build as `railpack prepare --env KEY=value` and as BuildKit
   `--opt build-arg:KEY=value` (`internal/builder/job.go` `prepareContainer`,
   `buildScript`), which puts them in the build Pod's arguments, and Docker's own
   build check warns that secrets passed through `ARG`/`ENV` "persist in the final
   image" and should use secret mounts instead. *Fit:* build-time *secrets* move to
   BuildKit secret mounts; a final step in the build Job script compares output
   files against the secret values (passed as hashes so the check never prints
   them), held to the same shellcheck and stub-runner tests as the other generated
   scripts (`internal/shellgen`). *Size:* M. *Without a cluster:* the rendered Job
   and the script, yes (run against a fixture directory); a real build, no.

5. **Skip apps whose directory did not change** (`vercel.md` gap 5). Netlify's own
   default rebuilds every site on any change under the base directory and tells
   users to write an `ignore` command; Vercel made skipping the default. Skifity can
   do better than both with the changed-file lists already in the push payload.
   *S–M; no cluster.*

6. **Know when a deploy carries a framework version with a critical advisory**
   (`vercel.md` gap 6). Netlify blocked React2Shell-vulnerable deploys within days
   and was immune to CVE-2025-29927 by architecture; a self-hosted `next start` was
   not. *S–M; detection without a cluster.*

7. **Previews behind a login, and a reviewer role.** *What:* on by default for
   previews, with the panel's own accounts (and so its SSO) as the login, plus a
   **Reviewer** role that can open previews and nothing else. *Evidence:* Netlify's
   Reviewer role and team-login protection; Vercel's free Vercel Authentication.
   Skifity's three roles (`internal/store/models.go`) have no preview-only member,
   and the firewall is not copied to previews (`internal/store/firewall.go` is a
   separate table). The basicAuth password that landed in `7865bc8` covers a shared
   password and previews inherit it, but only from an app that has one — a public
   production site's previews stay public. This is what comes after it. *M; the flow
   without a cluster, Traefik with one.*

8. **Observability that outlives the pod** (`vercel.md` gap 7). Netlify keeps 7 days
   on Pro and searches function logs. *L; needs a cluster.*

9. **A remote MCP endpoint** (`vercel.md` gap 8). Netlify offers both remote and
   local servers; Skifity only stdio. *M; no cluster.*

### P2

10. **Firewall rules that differ between a preview and production.** Netlify keeps
    separate rule sets for the published deploy and for unpublished ones. In Skifity
    this falls out of copying the firewall to previews (gap 7) with an option to
    use a stricter set there. *S; no cluster for the logic.*
11. **Rate limits declared with the app.** Netlify lets code or `netlify.toml`
    declare a rate limit on every plan; Skifity's firewall only allows or blocks. A
    `rate_limit` action, also settable from `skifity.toml`. *M; engine without a
    cluster.*
12. **Clear the build cache on one deploy** (`vercel.md` gap 9). *S; no cluster.*
13. **Branch deploys** (`vercel.md` gap 12). *S–M; no cluster for the logic.*
14. **Log drains** (`vercel.md` gap 14). Netlify reserves them for Enterprise;
    offering them free is a cheap way to be ahead. *M; needs a cluster.*

## Things to deliberately not copy

* **Charging for a production deploy.** Fifteen credits per deploy made users ration
  deploys ("now, I can't deploy anymore"). Skifity's own working rule is "small and
  often"; a platform should make that free.
* **Pausing every running site when a quota runs out.** Netlify's credit exhaustion
  replaces the site with "Site not available". Skifity's quotas exist "to contain a
  mistake, not to ration" (`docs/checklist.md`): a quota may refuse a *new* deploy
  with an explanation; it must never stop an app that is already serving.
* **A status flag kept in sync with a balance.** The July–September 2026 credit-flag
  bug paused deploys for users with credits left — a stored flag that disagreed with
  the number it summarised. CLAUDE.md's "derive, do not synchronise" is the rule that
  prevents it.
* **Copying production data into previews by default.** Netlify's database branches
  start as a copy of production, and a preview URL is often shared with people who
  should not see customers' data. Skifity's preview database should start empty (or
  from a seed the user chose), with a production-backup seed as an explicit opt-in.
* **Proprietary primitives that become lock-in** (Forms, Identity) — and then
  deprecating one and reversing it a year later. Skifity's equivalents are templates
  and plugins that run as ordinary containers and leave with the export.
* **Security features on the Enterprise plan only**: WAF, log drains, team login,
  SSO. Skifity's are free and should stay free.
* **Anonymous deploys claimed within an hour.** A growth feature for a hosted
  service; on a self-hosted panel an endpoint that deploys without an account is
  simply a hole.
* **Hosted coding agents in the dashboard** (Agent Runners). They sell AI inference
  by the credit. Skifity's job is to be the best target for whatever agent the user
  already runs — the MCP server, `llms.txt`, errors with cause, impact and fix.

## Since 2026-09-30

Read on 2026-11-08. The changelog's feed, the blog, the docs, the pricing page,
the status page's API, the forum's JSON API, the npm registry and Hacker News
(Algolia API) were read; GitHub's REST API only through its search endpoint
(repository and advisory endpoints were refused from this sandbox), and
Trustpilot refused the request. A web search was not available.

### Product

* **Missed on 2026-09-30, from the days before it.** The project activity feed
  (2026-09-24): production versions, previews and agent runs in one list on the
  project's overview, on every plan. A Netlify-managed Git repository for every
  project that arrives without a Git host (blog, 2026-09-22; first called Netlify
  Source in August), because "in the last month, 93% of new projects" came from
  Drop, the API, the MCP or Agent Runners rather than a connected repository; it
  speaks the ordinary Git protocol and keeps its objects in S3. Edge Functions
  moved onto MicroVMs (2026-09-29): about 5–6 ms per warm invocation at the
  median, down from 25–40 ms, with each deploy in its own MicroVM. The Trust
  Center moved into the dashboard (2026-09-30).
* **Frameworks.** SvelteKit 3 on the day it shipped (2026-10-02); TanStack Start
  1.0 (2026-10-13), now what a new Agent Runners project starts with.
* **Who is visiting** (2026-10-07). On a private project or one behind team login,
  functions and edge functions get the signed-in Netlify user as `context.user`,
  set by Netlify rather than by a request header, so a visitor cannot forge it.
  Netlify's examples: internal dashboards, approval flows, audit trails inside an
  internal app.
* **Agents.** Comments on single lines of an agent's diff (2026-10-06); Agent
  Runners in the public API — start a run, send a follow-up, answer its
  questions, read the files it changed, with a deploy URL, but not yet open a
  pull request or publish (changelog, 2026-10-20); a connector for Grok
  (2026-10-23); subscriptions to deploy, failed-build and form events from ChatGPT
  (2026-10-07). The MCP server gained a deploys card for clients that render MCP
  Apps, refreshing while a deploy runs, and a tool through which an agent reports
  to Netlify where it got stuck (both 2026-11-03). New models in AI Gateway and
  Agent Runners almost weekly.
* **A schema for `netlify.toml`** (2026-11-04): a published JSON Schema, named
  with a `$schema` key, so an editor completes and checks the file and an agent
  can validate what it generated before deploying it.
* **Smaller.** The Support Hub, tickets inside the dashboard, for Enterprise
  (2026-10-16). Form notifications by email must go to a member of the team on
  every plan but Enterprise and Legacy Business (2026-11-03), with address
  verification promised.
* **Terms** (announced 2026-11-04, in force 2026-12-04). An organisation that
  proves it owns an email domain may be shown that accounts on that domain exist,
  their plan and their public sites, and an account may be moved into the
  organisation's own after a notice, unless its owner changes the email address.
* **CLI.** Fifteen releases from 27.11.0 (2026-10-05) to 27.16.2 (2026-11-02), MIT
  as before; 1,934 stars and 144 open issues (GitHub search API, 2026-11-08).

### Pricing

* Credit rates are as above on 2026-11-08: 15 credits per production deploy, 10
  per GB-hour, 20 per GB, 2 per 10,000 requests, Pro tiers of 3,000 to 20,000
  credits, Personal at 1,000, Free capped at 300.
* The pricing page's markup holds three variants of the paid prices, shown one
  per visitor: $9 Personal and $20 Pro (the prices above), $12 and $20, or $15
  and $25, with "See pricing in your dashboard" in their place for some
  visitors. That is a price test, not an announced change; the plan docs name no
  price at all. When it started could not be checked, since the Wayback Machine
  refused the request.

### Reliability

* Netlify's status page lists eleven incidents between 2026-10-05 and
  2026-11-04: errors reaching sites on the High-Performance Edge network for 55
  minutes (2026-10-05, 12:19–13:14 UTC); two major Agent Runner outages
  (2026-10-20, and 2026-10-27 with build errors); elevated build errors on
  2026-10-17 and 2026-11-04; build logs reaching the UI late on three occasions
  (2026-10-21, 2026-10-26, 2026-11-02); late function logs (2026-10-22); and two
  short error spikes recorded with no impact, in edge functions (2026-10-08) and
  AI Gateway (2026-10-06).

### What users say

* The forum changed its rules on 2026-09-29: new users wait before they may post,
  posts can sit in an approval queue, and account problems are to go to a support
  ticket. On 2026-11-08 the forum's list of newest topics shows two created since
  then, where the week before it had about twenty a day, most of them about
  credit flags, suspended accounts and domains held by another team. The public record
  of complaints this file relies on has thinned; that says nothing about the
  complaints.
* One of the two (2026-11-02) is the preview-database problem from outside
  Netlify Database: a team branching Neon per pull request cannot hand a Deploy
  Preview its branch's URL in time, because functions read variables as they were
  when the deploy was created, the first push builds before CI has the URL, and
  `POST /sites/{id}/builds` with a `review_id` answers 422 for GitHub
  repositories, so CI can neither start nor restart a preview. The team says it
  wanted Netlify Database but needed masked copies of production and one database
  shared by several projects, which it does not offer.
* On Hacker News the Edge Functions post had 230 points (2026-09-30); no story
  about Netlify itself since reached 20.

### Security

* No advisory against Netlify's own packages: OSV lists none for `netlify-cli`,
  `@netlify/plugin-nextjs`, `@netlify/functions`, `@netlify/edge-functions`,
  `@netlify/blobs` or `@netlify/identity` (2026-11-08). No breach was reported.
* Framework advisories Netlify relayed, each with what applies on Netlify:
  * 2026-09-22: remote code execution in `next/og` `ImageResponse` with untrusted
    input (CVE-2026-94545, critical), fixed in 15.5.26 and 16.3.6. On Netlify a
    crashed invocation, not code execution.
  * 2026-09-30: seven Next.js advisories (one high, an image-optimiser SSRF,
    CVE-2026-94483), fixed in 15.5.27 and 16.3.8 with adapter 5.16.1; and a
    critical reflected XSS in TanStack Start (CVE-2026-102989).
  * 2026-10-14: two critical remote code executions — a `use cache` function
    returning typed arrays (GHSA-673q-mjm6-r5xr) and SVG processing through sharp
    (CVE-2026-96889) — and a high denial of service in Server Actions, fixed in
    15.5.28 and 16.4.1.
  * 2026-11-03: seven medium ones, cache leaks between users and an HttpOnly
    cookie exposed on Edge routes among them, fixed in 16.4.5 and 15.5.29 with
    adapter 5.16.6. One, cross-client response injection (CVE-2026-119348),
    affects only self-hosted apps behind certain proxies.
* Several are "not affected on Netlify" because its Image CDN replaces Next.js's
  image optimiser and every request is its own invocation. A self-hosted
  `next start` has neither defence, so on a user's own server every one of them
  applies. Netlify adds each time that public previews and branch deploys stay
  vulnerable until they are deleted.

### For Skifity

Checked against the code on 2026-11-08.

* **Rows of "Against Skifity" that no longer describe Skifity.** Monorepo: a push
  deploys only the apps whose paths it touched (Phase 96). Databases: nine engines
  (`f4f9d09`), and a preview gets databases of its own and a seed command (`1ae3276`,
  Phase 107). Locked deploys (Phase 94). Observability: log drains (`3731416`),
  requests, errors and response times per app (`6fa2876`), usage history and
  alerts (Phases 95 and 99); still no log retention or search inside the panel.
  Roles: a viewer role and members limited to projects (Phases 85 and 86).
  Firewall: rate limits exist (`internal/edgerules`). Previews: a value per
  variable for previews (Phase 93), and a preview can be started through the API
  (`POST /api/apps/{app}/previews`). MCP: served by the panel over HTTP (Phase 87).
  Onboarding: v0.1.0 is released, and the `Cluster` workflow runs the installer on
  a real k3s on every push to `main`.
* **This file's gaps still open.** Scanning build output for secret values (gap 4;
  the build-argument half was fixed in `7123905`, nothing in
  `internal/builder/job.go` compares output with the values); clearing the build
  cache for one deploy (gap 12; `--import-cache` is always passed); branch deploys
  (gap 13); firewall rules for previews (gap 10: `previewCopy` in
  `internal/api/webhook_handlers.go` copies variables, files and the password, not
  the rules); previews behind a login (gap 7: the viewer role exists, a login in
  front of an app does not).
* **What the forum thread asks for, Skifity has.** CI can start a preview through
  the API, a variable can hold a value for previews alone, and each preview gets
  databases of its own and a seed command after its first deploy. A
  copy of production data, masked or not, stays out on purpose ("Things to
  deliberately not copy"); masking belongs in the seed command.
* **New gap — previews private by default, behind the panel's own login, with the
  visitor's identity passed on.** Netlify keeps new projects and all previews
  private behind its login on every credit plan and gives the code `context.user`;
  Railway put services and whole environments behind its login on 2026-10-08
  (`railway.md`). Skifity has a shared basicAuth password per app
  (`internal/kube/password.go`), carried to previews only when production has one;
  the guard is already Traefik's forward-auth for a protected app
  (`internal/guard`) but knows nothing of panel sessions. *Fit:* the guard sends a
  visitor without a valid cookie to the panel, which checks the session and the
  person's access to the project and returns a short-lived signed assertion for
  that one host; the guard sets a cookie on the app's own host, strips any
  incoming identity headers and adds `X-Skifity-User` and `X-Skifity-Email`; an
  environment setting "Previews require signing in to the panel", on by default
  for new environments. Size L; the flow and the signatures without a cluster,
  Traefik with one.
* **New gap — a schema for `skifity.yaml`.** The file is read strictly
  (`DisallowUnknownFields` and `UnmarshalStrict` in `internal/blueprint/blueprint.go`),
  so a typo fails, but only when `skifity plan` runs against a panel; nothing an
  editor or an agent can check against before that. Generate a JSON Schema from
  the blueprint types, publish it in `docs/` and from the panel, test that every
  field of the types is in it, and put the `# yaml-language-server: $schema=`
  line in the documentation's examples. Size S; no cluster.
* **New gap — the pre-build advisory list falls behind.** `internal/builder/advisories.go`
  holds CVE-2025-29927 and CVE-2025-55182 and nothing since; three Next.js
  disclosures with a critical or high advisory came in four weeks, and the
  defences Netlify cites for some of them do not exist on a self-hosted
  `next start`. The nightly image scan
  (`internal/vulnscan`, Trivy, which can stop deploys with fixable criticals)
  reports them once the image exists and Trivy's database has them; the hole is
  the warning before the build. Add the critical ones, and make keeping the list
  a step of `docs/releasing.md`. Size S; no cluster.

## Sources

All read 2026-09-30.

* https://www.netlify.com/pricing/
* https://docs.netlify.com/manage/accounts-and-billing/billing/billing-for-credit-based-plans/credit-based-pricing-plans/
* https://docs.netlify.com/manage/accounts-and-billing/billing/billing-for-credit-based-plans/how-credits-work/
* https://www.netlify.com/changelog/2026-07-14-pro-plan-credit-tiers/
* https://docs.netlify.com/deploy/deploy-types/deploy-previews/
* https://docs.netlify.com/deploy/manage-deploys/manage-deploys-overview/
* https://docs.netlify.com/build/environment-variables/overview/
* https://docs.netlify.com/build/configure-builds/monorepos/
* https://docs.netlify.com/build/configure-builds/manage-dependencies/
* https://docs.netlify.com/manage/security/secret-scanning/
* https://docs.docker.com/reference/build-checks/secrets-used-in-arg-or-env/
* https://docs.netlify.com/build/frameworks/overview/
* https://docs.netlify.com/start/core-concepts/primitives/
* https://docs.netlify.com/build/functions/scheduled-functions/
* https://docs.netlify.com/build/data-and-storage/netlify-blobs/
* https://docs.netlify.com/build/data-and-storage/netlify-db/
* https://www.netlify.com/blog/netlify-database/
* https://www.netlify.com/changelog/2026-04-13-netlify-db-ga-coming-soon/
* https://docs.netlify.com/manage/monitoring/observability/overview/
* https://docs.netlify.com/manage/monitoring/log-drains/
* https://docs.netlify.com/manage/security/secure-access-to-sites/traffic-rules/
* https://docs.netlify.com/manage/security/secure-access-to-sites/rate-limiting/
* https://docs.netlify.com/manage/security/secure-access-to-sites/web-application-firewall/
* https://docs.netlify.com/manage/security/secure-access-to-sites/password-protection/
* https://docs.netlify.com/manage/accounts-and-billing/team-management/roles-and-permissions/
* https://docs.netlify.com/build/build-with-ai/netlify-mcp-server/
* https://docs.netlify.com/build/build-with-ai/agent-runners/overview/ and https://finance.yahoo.com/news/netlify-launches-ai-agent-runners-140000633.html
* https://www.netlify.com/changelog/2026-03-27-create-and-deploy-anything-netlify-clis-improved-ax/
* https://www.netlify.com/changelog/2025-12-16-ai-gateway-ga/
* https://www.netlify.com/blog/unfailify/ and https://www.infoworld.com/article/2336223/netlify-ai-analyzes-failed-deployments.html
* https://answers.netlify.com/t/netlify-identity-is-staying-feb-2026-reversal-what-changed-whos-affected-and-how-to-proceed/162733 and https://www.netlify.com/blog/auth0-extension-identity-changes/
* https://www.netlify.com/changelog/2025-12-03-react-security-vulnerability-response/
* https://www.netlify.com/changelog/tag/security/ (July 2026, August 2026, January 2026 entries)
* https://github.com/advisories?query=netlify
* https://www.netlify.com/blog/10-million-developers/
* https://www.netlify.com/press/bolt-netlify-1-million-ai-generated-websites/
* https://en.wikipedia.org/wiki/Netlify
* https://getlatka.com/companies/netlify
* https://bejamas.com/stack/hosting/netlify
* https://answers.netlify.com/t/credit-based-billing-is-terrible/158457
* https://answers.netlify.com/t/production-deploys-paused-despite-29-9-30-credits-matches-known-operational-credits-bug/168093
* https://answers.netlify.com/t/production-deploys-paused-despite-28-credits-remaining-free-plan/170299
* https://www.trustpilot.com/review/www.netlify.com
* https://news.ycombinator.com/item?id=39520776 and https://news.ycombinator.com/item?id=39521986 ($104k bill, CEO reply)
* https://news.ycombinator.com/item?id=43555996 (Coolify thread)
* https://hn.algolia.com/api/v1/search (queries "netlify", stories since 2025-01-01)
* https://github.com/netlify/cli (licence) and the GitHub search API (stars, open issues)
* https://registry.npmjs.org/netlify-cli (release cadence)
* https://securitylabs.datadoghq.com/articles/nextjs-middleware-auth-bypass/ (CVE-2025-29927 scope)

Read 2026-11-08, for "Since 2026-09-30":

* https://www.netlify.com/changelog/feed.xml (entries from 2026-09-22 to 2026-11-04), in particular:
  * https://www.netlify.com/changelog/2026-09-24-new-activity-feed/
  * https://www.netlify.com/changelog/2026-09-30-trust-center-moves/
  * https://www.netlify.com/changelog/2026-10-01-sveltekit-3/
  * https://www.netlify.com/changelog/2026-10-06-agent-runners-diff-line-comments/
  * https://www.netlify.com/changelog/2026-10-07-context-user-in-functions/
  * https://www.netlify.com/changelog/2026-10-07-subscribe-to-netlify-events-from-chatgpt/
  * https://www.netlify.com/changelog/2026-10-13-tanstack-start-1-0/
  * https://www.netlify.com/changelog/2026-10-16-support-tickets-in-the-netlify-ui/
  * https://www.netlify.com/changelog/2026-11-05-agent-runners-api/ (dated 2026-10-20 in the feed)
  * https://www.netlify.com/changelog/2026-10-23-netlify-connector-for-grok/
  * https://www.netlify.com/changelog/2026-11-03-check-netlify-deploys-from-mcp/
  * https://www.netlify.com/changelog/2026-11-03-agents-can-send-netlify-feedback/
  * https://www.netlify.com/changelog/2026-11-03-new-email-requirements-for-form-notifications/
  * https://www.netlify.com/changelog/2026-11-04-json-schema-for-netlify-toml/
  * https://www.netlify.com/changelog/updates-to-our-terms-and-privacy-statement/ (2026-11-04)
  * https://www.netlify.com/changelog/2026-07-28-start-with-private-project-urls/
  * https://www.netlify.com/changelog/2026-09-22-nextjs-imageresponse-vulnerability/
  * https://www.netlify.com/changelog/2026-09-30-nextjs-react-security-vulnerabilities/
  * https://www.netlify.com/changelog/2026-09-30-tanstack-start-security-vulnerability/
  * https://www.netlify.com/changelog/2026-10-14-nextjs-react-security-vulnerabilities/
  * https://www.netlify.com/changelog/2026-11-03-nextjs-security-vulnerabilities/
* https://www.netlify.com/blog/ (no post after 2026-09-29), https://www.netlify.com/blog/edge-functions-firecracker-microvms/ (2026-09-29) and https://www.netlify.com/blog/how-we-built-git-storage-for-millions/ (2026-09-22)
* https://www.netlify.com/press/ (no release after 2026-03-18)
* https://www.netlify.com/pricing/ (prices, the variants in the page's markup, the plan table)
* https://docs.netlify.com/manage/accounts-and-billing/billing/billing-for-credit-based-plans/credit-based-pricing-plans/
* https://docs.netlify.com/manage/security/secure-access-to-sites/project-visibility/
* https://docs.netlify.com/manage/security/secure-access-to-sites/password-protection/
* https://docs.netlify.com/build/build-with-ai/netlify-mcp-server/ (the new remote address)
* https://www.netlifystatus.com/api/v2/incidents.json (incidents since 2026-09-30)
* https://answers.netlify.com/latest.json?order=created, https://answers.netlify.com/t/172503 (moderation rules, 2026-09-29) and https://answers.netlify.com/t/172726 (per-PR database URL, 2026-11-02)
* https://registry.npmjs.org/netlify-cli (releases 27.11.0 to 27.16.2) and the GitHub search API, `repo:netlify/cli` (stars, open issues)
* https://api.osv.dev/v1/query for `netlify-cli`, `@netlify/plugin-nextjs`, `@netlify/functions`, `@netlify/edge-functions`, `@netlify/blobs` and `@netlify/identity` (no advisories)
* https://hn.algolia.com/api/v1/search?query=netlify (stories since 2026-09-30), including https://news.ycombinator.com/item?id=49912444
* Not reachable from this sandbox: GitHub's repository and advisory endpoints, Trustpilot, the Wayback Machine, and web search
* Skifity: the files named under "Since 2026-09-30", read in this repository on 2026-11-08
