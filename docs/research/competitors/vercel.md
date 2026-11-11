# Vercel

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Researched 2026-09-30 against live sources. Skifity's side of every comparison was
checked in the code at commit `7865bc8` (2026-09-30). Two features landed in
Skifity while this was being written — commit statuses and pull request comments
(`bb30e03`) and a password in front of an app (`7865bc8`) — and both are counted
below as what they are: Written, never run.

Vercel is the managed platform that set the developer-experience bar this whole
category is measured against: push a branch, get a URL; merge, and it is live;
something breaks, and one click puts the last version back. It is for frontend and
full-stack JavaScript teams first (it maintains Next.js), and since 2025 it sells
itself as an "AI cloud" for agents as much as for websites. **Architecture:** a
closed, proprietary control plane in front of a global CDN (the edge network), with
compute on "Fluid compute" functions — request-scoped, scale to zero — and, since
30 June 2026, OCI container images run *as* those request-scoped functions. Since
15 October 2026 (public beta) containers can also run as **Daemons**: background
workers that pull their work from a queue or a database, kept at a chosen number
of instances per region and restarted when they exit; they serve no HTTP. State
lives in Vercel's own stores (Blob, Global Config —
renamed from Edge Config in July 2026, Queues, Workflow) and in Marketplace
partners (Neon, Supabase, Upstash, Prisma) since Vercel Postgres and KV were moved
to the Marketplace. **Licence:** the service is proprietary; the CLI
(`vercel/vercel`) is Apache 2.0 and Next.js is MIT. **Pricing (read 2026-09-30):**
Hobby is free and "non-commercial, personal use only"; Pro is $20 per developer seat
per month, each seat bringing $20 of usage credit (the credit model replaced fixed
allowances on 9 September 2025, with viewer seats made free and Spend Management
enabled by default at a $200 on-demand budget for new teams), then metered usage;
Enterprise is custom. **Maturity and adoption:** founded 2015 as ZEIT; $300M
Series F at a $9.3B valuation in September 2025; revenue estimated at $200M ARR
for 2025 (Latka) and ~$340M ARR by February 2026 (Sacra, an estimate, not a filing).
On GitHub (API, 2026-09-30) `vercel/next.js` has 142,908 stars and 3,528 open
issues; `vercel/vercel` (the CLI) has 16,322 stars and 866 open issues. The `vercel`
npm package shipped 247 stable releases in 2026 up to 61.1.0 on 2026-09-29 — the CLI
releases more than once a day.

## Feature inventory

### Deploy sources and builds

* Git: GitHub, GitLab, Bitbucket, with GitHub Enterprise Server only through GitHub
  Actions (`vercel build` then `vercel deploy --prebuilt`). Every push to every branch
  deploys by default; a newer push to the same branch queues and cancels the ones in
  between (`github.autoJobCancellation`).
* CLI deploys (`vercel`, `vercel --prod`), the REST API (`POST /files` then
  `POST /deployments`), and **Vercel Drop** (drag a folder or `.zip` onto
  vercel.com/drop, framework detected; guide dated 2026-06-11). Drop needed an
  account until 2026-10-19; since then a static site deploys without one and
  expires after an hour unless it is claimed, and since 2026-10-22 `vercel
  deploy` does the same from the CLI, on a separate `tmp.now` domain.
* Framework detection with presets for Next.js, SvelteKit, Nuxt, TanStack, Astro,
  Remix, Vite, CRA and many more; the Build Output API lets any framework target
  Vercel's primitives. Since 2026-06-30 a `Dockerfile`/`Containerfile` can be
  deployed as an HTTP server on Fluid compute (request-scoped, scales down after 5
  minutes idle in production and 30 seconds in preview, no persistent disk, no
  Compose). Since 2026-10-27 a plain root `Dockerfile` is used when no framework
  is detected; the container runtime is still a beta.
* Build machines: Hobby 2 vCPU / 8 GB / 32 GB disk; Pro 4 vCPU up to 30 vCPU / 60 GB
  ("Standard", "Enhanced", "Turbo", "Elastic"), billed per build minute from $0.007
  (Basic) to $0.105 (Turbo). `#VERCEL_BUILD_MACHINE=TURBO` in a commit message picks
  Turbo for one deploy. Builds time out at **45 minutes**.
* Build cache: up to **1 GB**, retained **one month**, keyed by team, project,
  framework preset, root directory, Node version, package manager and **Git branch**;
  a new branch starts from the last production cache. Failed builds do not modify
  the cache. Bypass it with the Redeploy dialog's "Use existing Build Cache"
  checkbox, `vercel --force`, `VERCEL_FORCE_NO_BUILD_CACHE=1`, or `forceNew=1` on the
  API. Turborepo/Nx remote cache is built in.
* Build logs streamed live and kept with the deployment; a system report
  (`VERCEL_BUILD_SYSTEM_REPORT=1`) explains OOM and disk exhaustion; when a build
  never starts (invalid `vercel.json`, a non-member committer, a Marketplace resource
  that failed to provision) the dashboard shows why instead of a log.
* **Monorepos:** one Vercel project per directory (Root Directory). **Skipping
  unaffected projects** is on by default for new monorepo projects: a project is
  rebuilt only if its source, an internal dependency, or a lockfile change that
  touches its dependencies changed (npm/yarn/pnpm/Bun workspaces, GitHub only), and a
  skipped project does not occupy a build slot. Otherwise an "Ignored Build Step"
  script (`turbo query`, `git diff`) decides. **Related Projects** inject a sibling
  project's matching preview/production URL (`VERCEL_RELATED_PROJECTS`, max 3).
  Consolidated commit status per monorepo, with "soft failure" projects.
* Security gates in the build: new deployments of Next.js versions with known
  critical vulnerabilities (React2Shell, December 2025) and of vulnerable
  `next-mdx-remote` are **blocked by default**.

### Domains, TLS and routing

* Every deployment gets an immutable URL; every branch gets a stable branch URL
  (`<project>-git-<branch>-<team>.vercel.app`); production domains are aliases that
  move on promotion. Automatic certificates, wildcard domains, redirects (bulk
  redirects at 1K per project included on Pro), rewrites, Routing Middleware.
* Global CDN, ISR, image optimisation (5K transformations/month on Hobby, from
  $0.05 per 1K after), Global Config (edge-replicated reads), Skew Protection (a
  client keeps talking to the backend of the deployment it loaded from).
* Static IPs ($100/project/month), Secure Compute, AWS PrivateLink; Microfrontends
  routing (2 projects included, $250 each after).

### Databases and services

* No first-party SQL any more: Postgres and KV were moved to the **Marketplace**
  (Neon, Supabase, Upstash, Prisma and others), provisioned from the dashboard,
  billed through Vercel, with variables injected per environment including custom
  environments. Claimed deployments carry Neon/Supabase/Prisma resources along.
* **Vercel Services** (beta, all plans): several independently built units per
  project, service-to-service "bindings" that inject the target's URL and stay on
  Vercel's network — the documented replacement for Docker Compose. Vercel's own
  guide (updated 2026-08-12) says plainly: *"You can't run your own Postgres or
  Redis container with local disk"*, and durable container storage "has not
  shipped".
* Queues, Workflow (durable steps), Sandbox (microVMs, 10K concurrent on Pro), AI
  Gateway.

### Storage and backups

* **Vercel Blob** ($0.023/GB-month on Pro, 1 GB free on Hobby), Global Config,
  Container Registry (VCR, $0.10/GB-month, launched summer 2026).
* No volume or database backup product of its own: backups are the Marketplace
  provider's (Neon point-in-time restore etc.). Deployments themselves are retained
  so a production deployment can be rolled back to ("Deployment Storage keeps your
  deployments rollback-ready") — for 30 days by default on Pro since October 2026:
  new Pro teams default to 30-day retention (2026-10-09), existing Pro teams'
  deployments older than 30 days are deleted from 2026-10-23 unless they opt out,
  Deployment Storage is billed at $0.10 per GB-month, and "deleted deployments
  cannot be restored or used for rollback".
* **Vercel Drives** (GA 2026-10-15): persistent storage that mounts in a Sandbox
  only, forkable, 1 TiB by default (1 GiB on Hobby) and up to 16 TiB.

### Scaling and high availability

* Functions scale per request with no configuration; Fluid compute runs many
  requests per instance and bills **Active CPU** ($0.128/hour) plus provisioned
  memory ($0.0106/GB-hour) and invocations ($0.60/1M) on Pro. Functions run up to
  300 s by default, 800 s configurable, 1,800 s in beta.
* Multi-region by construction for the CDN; function regions are configurable.
* **Rolling Releases** (GA; Pro can use it on one project): staged percentages,
  per-stage metrics comparison, automatic or manual advance, abort = Instant
  Rollback, cookie-based bucketing, REST API and CLI. Deployment Checks hold a
  production deployment until chosen GitHub checks pass.

### Observability (logs, metrics, alerts, uptime)

* Runtime logs: **1 hour** on Hobby, **1 day** on Pro, 30 days with Observability
  Plus ($1.20 per 1M events). Tracing (1M span units on Hobby, $0.50/1M after;
  generally available since 2026-11-06, with traces kept 3 days on Hobby and 14 on
  Pro and Enterprise), session tracing, request metrics, the Firewall's live
  traffic view. Custom monitors since 2026-10-26: a metric or a ratio of two, a
  fixed threshold or anomaly detection, with Observability Plus.
* **Drains** (Pro and Enterprise, **$0.50/GB** measured as uncompressed JSON):
  logs (runtime, build, static), OpenTelemetry traces, Speed Insights, Web Analytics
  and Connect events to any HTTP endpoint or native integrations; audit-log drains
  to S3, Splunk, Datadog, Panther on Enterprise.
* Web Analytics (50K events free, $3 per 100K after), Speed Insights (real-user Core
  Web Vitals, 10K events free; Plus $10/project/month), Firewall alerts.
* **Vercel Agent Investigation** (beta, Pro/Enterprise with Observability Plus): an
  agent that investigates production anomalies; 10 investigations per cycle
  included.
* No uptime monitoring or status page product.

### Security, auth, roles, SSO, audit

* **Deployment Protection**, set per project or as a team default: *Vercel
  Authentication* (only team members with access; free on every plan including
  Hobby), *Password Protection* ($20 per protected project per month on Pro,
  included on Enterprise), *Trusted IPs* and *Passport* (your IdP in front of the
  app) on Enterprise. Scopes: Standard (everything except production domains), All
  Deployments, or production-only via Trusted IPs. Bypass: shareable links,
  "Protection Bypass for Automation" secrets (several at once), exceptions.
  Protected source maps.
* **Environment variables**: encrypted at rest; since 2026-08-24 each is either
  *Config* or *Secret*, and a Secret is **write-only** — *"members cannot view or
  retrieve it after saving"*. A "Separate Production Secret Values" team policy
  replaced "Enforce Sensitive Environment Variables". This change followed the April
  2026 breach, in which variables *not* marked sensitive were decrypted (see
  Security record).
* **Vercel Firewall**: platform DDoS mitigation on every plan; WAF custom rules (3
  on Hobby, 40 on Pro, 1,000 on Enterprise) over IP, country, ASN, path, headers,
  user agent, cookies, JA3/JA4 TLS fingerprints; actions log, deny, **challenge**,
  **rate limit**, redirect, bypass; IP blocking (3/100/1,000); **rate limiting**
  (fixed window on all plans, token bucket on Enterprise; keys IP and JA4 digest,
  plus user agent and arbitrary headers on Enterprise; 10 s to 10 min windows,
  1 hour on Enterprise; counters are *per region*); managed rulesets — **OWASP Core
  Ruleset**, **Bot Protection** (JavaScript challenge), **AI Bots**; **Attack
  Challenge Mode**; **BotID** ($1 per 1,000 deep checks); rules from natural
  language; configuration by REST API or Terraform. Rules propagate globally on
  publish.
* Roles: Owner, Member, Developer, Billing, Viewer Pro (free), Security, project
  administrators, access groups; project-level RBAC on Enterprise. **SAML SSO costs
  $300/month on Pro**, SCIM and audit logs are Enterprise. HIPAA BAA $350/month on
  Pro. Git fork protection: a pull request from a fork waits for a team member's
  authorisation before it is deployed, so its code never sees the project's
  variables or OIDC token.
* OIDC federation: deployments receive a Vercel-issued OIDC token to reach AWS/GCP
  without static keys.

### Preview environments and branches

* A preview deployment for **every push to every non-production branch** and every
  pull request (GitHub, GitLab, Bitbucket), each with a **commit URL** (immutable)
  and a **branch URL** (moves with the branch). Previews are not indexed by search
  engines (a `noindex` header).
* The Vercel bot **comments on the pull request** with the URLs and updates the
  comment; **commit statuses** per project (consolidated for monorepos); GitHub
  `deployment_status` events and `repository_dispatch` events
  (`vercel.deployment.success`, `.error`, `.promoted`, `.skipped`, …) so a GitHub
  Actions workflow can run end-to-end tests against the preview URL.
* **Comments on previews** through the Vercel Toolbar (viewers can comment for
  free), feature-flag overrides from the toolbar.
* **Environment variables per environment**: Production, Preview, Development and
  **Custom Environments** (1 per project on Pro, 12 on Enterprise, none on Hobby),
  with **branch-specific Preview values** that override only the keys they name.
  `vercel env pull` writes Development values to `.env.local`; `vercel pull
  --environment=staging` for custom environments.
* **Promotion**: a production-branch build can be held ("staged production" by
  turning off auto-assignment of production domains), checked at its own URL, and
  **promoted without rebuilding**. `vercel promote <deployment>`.
* **Instant Rollback**: points production domains back at an earlier production
  deployment instantly — no rebuild, and **environment variables are not changed**
  (the old deployment keeps the values it was built with, which Vercel warns "may
  become stale"). Cron jobs revert with it. Hobby can roll back one deployment; Pro
  and Enterprise to any deployment that was ever aliased to production and is still
  retained, which on Pro now means the last 30 days by default (see Storage and
  backups). After a rollback, auto-assignment is **turned off** so the next push
  does not silently undo it; "Undo Rollback" re-enables it.

### Templates and catalogue

* A large template gallery (framework starters, AI chatbots, commerce), "Deploy"
  buttons that clone a repository and provision Marketplace resources, v0-generated
  apps. Templates are Git repositories, not a catalogue of self-hostable services:
  there is no one-click WordPress, Gitea or n8n, because none of them fit
  request-scoped compute with no disk.

### CLI, API, IaC, integrations

* `vercel` CLI (deploy, env, pull, promote, rollback, rolling-release, crons,
  logs, vcr, curl with protection bypass), a full REST API and `@vercel/sdk`, an
  official Terraform provider, Marketplace integrations with deployment actions and
  checks, OIDC federation, webhooks, Vercel Connect (managed third-party API
  connectors).
* **Vercel MCP** (public beta): a **remote** server at `https://mcp.vercel.com`,
  Streamable HTTP with MCP OAuth, until 2026-11-02 only for reviewed clients
  (Claude Code, Claude.ai, ChatGPT, Codex, Cursor, VS Code, Devin, Gemini CLI and
  others); since then any client that publishes a Client ID Metadata Document may
  ask to connect, and the consent screen names the host serving it; searches docs,
  manages projects and deployments, reads logs, queries Web Analytics, manages
  rolling releases and drains; changelog entries say it **can now deploy code** and
  **supports purchases**. A "Vercel plugin" for coding agents
  (`npx plugins add vercel/vercel-plugin`).
* **Claim Deployments**: an agent or platform deploys through the API into its own
  team and hands the user a claim URL (code valid 24 hours) that transfers the
  project — and, since a later change, its Neon/Supabase/Prisma resources — to the
  user's account. A `vercel-deploy-claimable` agent skill does this from Claude.ai.

### Notifications

* Dashboard, email and (for Spend Management) SMS notifications; Slack via the
  GitHub app and integrations; deployment webhooks; spend webhooks at 50/75/100% of
  budget; Firewall alerts.

### Multi-server and networking

* Not applicable in the self-hosted sense: there are no servers to add. Regions for
  functions, the global edge for everything else, Secure Compute (dedicated network
  with VPC peering) and static egress IPs for reaching private backends.

### Team and collaboration

* Developer seats $20/month; **Viewer seats free** since September 2025 (view,
  comment on deployments, see analytics). Comments on previews, Vercel Toolbar,
  activity log, access groups. Vercel Agent code review on pull requests ($0.25 per
  million tokens plus provider cost, no seat licence).

### Developer experience and onboarding

* Import a repository, framework detected, first deployment is always production,
  preview per branch thereafter — minutes from sign-up to URL, no configuration.
* `vercel dev` runs functions locally; `vercel env pull` hands a developer the
  environment's variables; `vercel link --repo` links a whole monorepo.
* **AI and agents**: **v0** (v0.app; Free with $5 of credit and 7 messages a day,
  Plus $30/user/month, Business $100/user/month, token-metered models from $0.20
  to $10 per million input tokens) generates apps and deploys them to Vercel; **AI
  SDK** (27,043 stars) and **AI Gateway**; **Vercel Agent** for code review and
  production investigation; **Sandbox** microVMs for untrusted agent code;
  Claimable deployments for agent-built apps; the MCP server above; docs pages carry
  "For AI agents" link maps and a `.graph.md` per page.
* Weak spots in the same experience: Hobby forbids commercial use, cron on Hobby
  runs at most daily with ±59 minutes of jitter (Cron Jobs; their successor,
  Vercel Schedules, entered public beta on 2026-11-05 with 5,000 runs a month
  included on Hobby), and runtime logs vanish after an hour on Hobby.

## What users love

* **Preview deployments per pull request.** This is the feature every review and
  comparison names first: "every pull request gets its own live, shareable URL that
  mirrors production, allowing designers, product managers, and clients to click a
  link and see the exact change before it merges" (LuckyMedia review 2026;
  community.vercel.com thread "Why Developers Prefer Using Preview Deployments on
  Vercel"). PandaStack's 2026 survey calls Vercel's previews "what 'preview
  environment' means by default for a huge slice of the frontend world". The
  earlier Skifity research reached the same conclusion
  (`docs/research/competitors.md`).
* **Zero configuration.** Framework detection, automatic HTTPS and a URL on the
  first push; the Hacker News discussion of the pricing page (April 2026, 191
  points), mostly hostile, still concedes "Vercel's superior developer experience
  remains a selling point".
* **Instant rollback and promotion without rebuild**, because every deployment is
  immutable and kept: the recovery path is a pointer move, not a new build.
* **Next.js on the platform that writes Next.js**: ISR, image optimisation, Skew
  Protection and middleware work on day one, and both CVE-2025-29927 and the
  React2Shell RCE were mitigated for hosted apps before most self-hosters had read
  the advisory (see Security record).
* **The free tier for hobby projects**: even on Trustpilot, where the score is 1.7/5
  from 122 reviews, the positive reviews are about the free plan ("unbelievable the
  amount of value", 2026-09-10).

## What users complain about

* **Bills that are not a function of traffic you chose.** Documented cases in 2026
  include a ~$23,000 bill after a multi-day DDoS and a $3,200 bill for a student,
  both billed at the ordinary bandwidth rate because nothing distinguished attack
  traffic from customers (bex.co, 2026-07-08; the earlier Skifity research has a
  $286 morning of bot traffic). Vercel's own docs confirm the mechanism critics
  describe: Spend Management checks usage "every few minutes", "setting a spend
  amount does not stop usage on its own", and the stop, when enabled, **pauses the
  production deployment of every project on the team** until each is resumed by
  hand — a circuit breaker that turns a bill into an outage.
* **Defaults that cost money.** A Vercel Community thread (2026-04-01) documents
  Turbo build machines becoming the default for Pro accounts without notice; one
  user was billed $659.72 in build minutes, another $140+, and the refund needed
  escalation past automated support.
* **Opaque, escalating pricing and lock-in.** On Hacker News (April 2026): "their
  pricing is intentionally opaque", and an enterprise customer: "the bill was
  $40,000. When our management went back to negotiate the second year, Vercel
  wanted $120,000!" Security features are sold separately (SAML SSO $300/month,
  password protection $20 per project per month).
* **Next.js outside Vercel.** "Next.js 15.1 is unusable outside of Vercel" (Hacker
  News, June 2025, 155 points) is the recurring shape; the stable Adapter API in
  Next.js 16.2 (March 2026), built with Netlify, Cloudflare and OpenNext, is the
  response.
* **Support and account actions.** Trustpilot (1.7/5, 122 reviews, 79% one-star)
  repeats: charges after cancellation, accounts disabled without explanation
  ("disabled" within 30 minutes of deployment, 2026-01-08), and "five days and
  multiple urgent tickets" of generic replies (2026-08-15). A biased sample, but a
  consistent one.
* **Trust after April 2026.** The breach (867 points on Hacker News) and, ten days
  earlier, the Vercel plugin for Claude Code sending full bash commands and prompts
  as telemetry by default (280 points; the reply that it was "anonymous" and could be
  disabled with `VERCEL_PLUGIN_TELEMETRY=off` was received as dismissive).

## Security record

* **2026-04-19, Vercel's own breach.** A third-party AI tool used by an employee
  (Context.ai) had its Google Workspace OAuth app compromised; the attacker took
  over the employee's Workspace account, then their Vercel account, pivoted into
  Vercel's environment and **enumerated and decrypted environment variables that
  were not marked sensitive** for "a limited subset of customers", later expanded by
  "a small number of additional accounts". Sensitive variables, stored so they
  cannot be read back, showed no sign of access; npm packages were verified clean.
  Vercel's response: stronger defaults, team-wide variable oversight, deeper
  activity logs — and, in August 2026, the write-only Secret type. (Vercel bulletin;
  GitGuardian analysis; Hacker News 47824463.)
* **2025-12-18, supply chain through documentation hosting.** Researchers showed
  that Mintlify, which hosted docs for Vercel, Discord, X and Cursor, would serve an
  uploaded SVG with script from the customer's own domain — session theft on
  vercel.com's docs host. Fixed by the vendor; bounty $4–5K (Hacker News 46317098,
  1,167 points).
* **2025-12-03, React2Shell (CVE-2025-55182, CVSS 10.0; CVE-2025-66478 for
  Next.js).** Unauthenticated RCE in React Server Components. Vercel deployed WAF
  rules to every project for free, blocked new deployments of vulnerable versions,
  ran a $1M hacker challenge against its mitigations, and reported blocking over 6
  million exploit attempts (2.3 million in one 24-hour peak).
* **2025-03-21, CVE-2025-29927 (CVSS 9.1), Next.js middleware authorization
  bypass** via the `x-middleware-subrequest` header. **Vercel- and Netlify-hosted
  apps were not affected** because routing runs in a separate system; **self-hosted
  `next start` was** (Vercel postmortem; Datadog Security Labs).
* **2026 Next.js advisories.** The GitHub Advisory Database lists nine `next`
  advisories on 2026-07-22 (SSRF, a middleware authorisation bypass, DoS, cache
  disclosure) and two **critical** ones on 2026-09-08 (GHSA-2xp9-vwfh-vxw4,
  GHSA-p293-qw3h-jr36). A search for "vercel" returns 118 advisories, most of them
  in ecosystem packages rather than Vercel's platform.
* Where I looked: Vercel's bulletins and changelog, the GitHub Advisory Database
  (queries "vercel" and "next"), Hacker News via the Algolia API (2025-01-01 to
  2026-09-30, stories over 150 points), GitGuardian, Datadog Security Labs.

**What this means for Skifity.** Two of these land on a self-hosted panel harder
than on Vercel: CVE-2025-29927 only hit self-hosted Next.js, and React2Shell was
mitigated on Vercel by a platform WAF rule and a deploy block, neither of which a
self-hoster gets unless the panel provides them. The breach is the opposite lesson,
and one Skifity already follows: a variable readable by the platform is a variable
an attacker inside the platform can read, which is why `internal/crypto` seals every
secret with its own key and nothing returns a secret's value.

## Against Skifity

Skifity statuses follow `docs/checklist.md`: almost everything cluster-facing is
**Written, never run** (ADR-0010). A "parity" verdict below means the feature exists
in code with unit tests; it does not mean it has been seen working.

The Skifity column below, and the gaps after it, are Skifity on 2026-09-30. Since
then it has been released (v0.1.0 on 2026-10-06, v0.1.1 on 2026-10-21), the
`Cluster` workflow installs it on a real k3s on every push to `main`, and most of
those gaps are closed; "Since 2026-09-30" says which.

| Capability | Vercel | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | Git (GitHub/GitLab/Bitbucket), CLI, API, Drop (.zip), Dockerfile as request-scoped function | Git (GitHub, GitLab, Gitea, generic) via signed webhooks `internal/api/webhook_handlers.go`; image; folder upload (`skifity up`, browser folder picker) `internal/cli/up.go`, `internal/upload`; Railpack/Nixpacks/Dockerfile/static builds `internal/builder/job.go`. Written, never run | Parity (Skifity runs long-lived containers that serve traffic; Vercel's long-running Daemons, a beta since 2026-10-15, serve none) |
| Framework detection | Presets for ~all JS frameworks, Build Output API | Node (Next, Nuxt, Nest, Remix, SvelteKit, Astro, Vite, Angular…), Python, Go, PHP, Ruby, Rust, Java `internal/builder/detect.go`; plus what the app *needs* (databases, SQLite on disk) `internal/builder/needs.go` | Parity; Skifity ahead on detecting needs |
| Build cache | 1 GB, 1 month, per branch; redeploy without cache; remote cache | BuildKit registry cache at `<image>:buildcache` `internal/builder/job.go`; **no way to bypass it** | Behind (no cache bypass) |
| Build logs | Live, kept with deployment, OOM/disk report | Live over SSE, scrubbed of secrets `internal/deploy/build.go`, last 20 builds per app `internal/deploy/deployer.go` | Parity |
| Monorepo | Root directory, skip unaffected projects by default, related projects | `root_dir` per app `internal/store/models.go`; every app on the repo+branch rebuilds on every push (`dispatchGitEvent`; fingerprint includes the commit SHA, `internal/kube/spec.go`) | Behind |
| Config change without rebuild | Env changes need a new deployment | Runtime variable change is a rollout, not a build (ADR-0007) | **Skifity ahead** |
| **Domains, TLS and routing** | Global CDN, auto TLS, wildcard, redirects, middleware, ISR, image CDN | cert-manager on first domain; automatic `<app>-<env>.<wildcard>` or sslip.io address `internal/kube/naming.go`, TLS only with a wildcard domain `internal/cluster/cluster.go`; Cloudflare tunnel `internal/cluster/tunnel.go`. Written | Behind on edge features (by design); parity on "a domain and HTTPS" |
| **Databases and services** | Marketplace (Neon, Supabase, Upstash); no databases with local disk | PostgreSQL (CloudNativePG), Redis, MySQL, linked as a sealed variable `internal/dbsvc/manager.go`; long-running services of any kind. Written | **Skifity ahead** for stateful apps |
| **Storage and backups** | Blob, Global Config; backups are the provider's | Volumes (PVC) and S3 backups of databases and volumes `internal/backup`; no object-storage product (MinIO etc. as templates) | Different: Skifity ahead on disks/backups, behind on managed blob |
| **Scaling and HA** | Automatic per request, multi-region, rolling releases | HPA, KEDA scale-to-zero `internal/kube/manifests.go`, scaling readiness check `internal/deploy/scaling.go`; single cluster, no canary | Behind on canary/multi-region; parity on autoscale (Written) |
| Instant rollback | Pointer move, no rebuild, env unchanged, auto-assign paused afterwards | New deployment with the old image **and old settings** `internal/deploy/deployer.go` `Rollback`; refuses if image collected (10 kept, `internal/registry/registry.go`); a rolling update, not instant; the next push deploys over it | Parity on intent, Skifity ahead on restoring settings, behind on speed and on holding production |
| Promotion without rebuild | `vercel promote`, staged production, Deployment Checks | None: staging and production are separate apps and each builds the commit (`FindDeploymentByFingerprint` is per app, `internal/store/deployments.go`) | Absent in Skifity |
| **Observability** | Runtime logs 1 h/1 d/30 d, request metrics, tracing, Web Analytics, Speed Insights, drains | Live and previous-container logs from the kubelet `internal/api/stream_handlers.go`, no retention or search; instance CPU/memory; server metrics; panel's own Prometheus endpoint `internal/api/metrics_handlers.go`; no per-app request metrics, no drains, no analytics | Behind |
| Alerts / uptime | Firewall alerts, spend alerts; no uptime product | `app.unhealthy`, `deploy.failed` etc. `internal/notify/notify.go`; no uptime checks | Parity (neither has uptime/status pages) |
| **Security, auth, roles, SSO, audit** | Vercel Auth, SSO $300/mo, RBAC, audit on Enterprise | Argon2id, TOTP, OIDC SSO free `internal/api/sso_handlers.go`, three roles `internal/store/models.go`, scoped team-bound tokens `internal/auth/scopes.go`, audit log route in `internal/api/api.go`. **Works** (checklist row 11) | **Skifity ahead** (SSO and audit are not paywalled) |
| Secrets | Write-only Secret type since 2026-08; breach exposed readable vars | Sealed per value with context, write-only everywhere, secret by heuristic `internal/crypto`, `logging.LooksSecret` | **Skifity ahead** (it was the default from the start) |
| Firewall / WAF | Custom rules, IP/geo/ASN/JA4, challenge, rate limit, OWASP, bot and AI-bot rulesets, DDoS | Ordered allow/block rules over address, country, ASN, host, path, method, UA, header `internal/edgerules`, enforced by a forwardAuth guard `internal/guard`; no rate limit, no challenge, no managed rules; never run through Traefik | Behind |
| **Preview environments** | Per push and per PR, commit + branch URLs, PR comment, statuses, protection, per-environment variables | Per PR (opened/synchronize/reopened), own namespace, copy of the app, fork gets no secrets, removed on close or after 7 idle days `internal/api/webhook_handlers.go`, `internal/watch/watch.go`; commit status + one edited PR comment on GitHub/GitLab/Gitea `internal/deploy/gitreport.go` (added in `bb30e03`). A preview inherits the app's basicAuth password if it has one (`internal/api/webhook_handlers.go`, `internal/kube/password.go`, `7865bc8`). No branch deploys, no per-commit URLs, no preview-specific values, **no preview database** (see gaps), no protection by default and no sign-in with a panel account | Behind |
| Environments | Production, Preview, Development, custom (1 on Pro) | Any number of environments per project, each a namespace `internal/api/api.go` (`/projects/{id}/environments`); variables per app, shared variables per project across *all* environments (`SharedVariable`, `internal/store/models.go`) | Ahead on count, behind on per-environment values |
| Cron | Hobby daily ±59 min; Pro per minute; 100 per project; HTTP GET to a path. Since 2026-11-05 Schedules (beta): a private function, a path or a queue topic, also created at runtime | Scheduled commands in the app's image, per minute, `internal/cron`, `app.Post("/jobs")`; Kubernetes CronJob `internal/kube/runjob.go` | **Skifity ahead** (a real command, no plan limit). Written |
| **Templates and catalogue** | Framework starters and AI apps | 283 one-click self-hosted services `internal/templates/catalogue` | Different category; Skifity ahead for self-hostable services |
| **CLI, API, IaC** | CLI, REST, SDK, Terraform, Marketplace | CLI with `--json` everywhere `internal/cli/commands.go`, REST API, export to `kubectl apply` `internal/api/export_handlers.go`; no Terraform provider | Behind on IaC |
| MCP / agents | Remote MCP with OAuth that deploys; claimable deploys; v0; Agent | MCP over **stdio only** `internal/mcpserver/server.go` (15 tools, nothing destructive), `deploy_folder`, errors with cause/impact/fix `internal/errdoc/catalogue.go`, `llms.txt` | Behind on remote MCP; parity on agent deploys |
| **Notifications** | Email, SMS (spend), webhooks, Slack via integrations | Telegram, Discord, webhook, email + plugin-provided kinds, seven events `internal/notify` | Parity. Written |
| **Multi-server and networking** | N/A (no servers) | Add a server over SSH, k3s joins `internal/provision` | N/A (Skifity's category) |
| **Team and collaboration** | Free viewers, comments on previews, toolbar | Teams, invitations, three roles; no viewer role, no comments on previews | Behind |
| **DX and onboarding** | Minutes to first URL, no servers | Needs a server and an install that has never been released (`docs/checklist.md`); after that, repository or folder to URL with detected databases created | Behind (the install) |
| Cost model | Seats + metered usage, spend cap pauses production | A server's fixed price; no metering, no telemetry ("It never phones home", README) | **Skifity ahead** |

### Reproducible on your own servers, and what is tied to an edge network

The key question for this product. Each row says whether a self-hosted panel on k3s
can give the same experience, and what it costs.

| Vercel experience | On your own servers? | What it takes, or why not |
|---|---|---|
| A URL per pull request, commented on the PR, status on the commit | **Yes** — Skifity has it (Written) | A wildcard DNS record and the Git host's API. Nothing edge-specific. |
| A URL per *commit*, kept forever | **Only at a cost** | Vercel can keep every deployment addressable because idle functions and CDN assets cost nothing. On a VPS each addressable version is pods (or at least images plus scale-to-zero). A per-PR URL with a TTL is the right trade. |
| Protection in front of previews | **Yes** — Skifity has a shared password (Written, `7865bc8`) | A basicAuth or forwardAuth middleware in Traefik; for "sign in with your account", the panel as identity provider. |
| Per-environment and per-branch variables | **Yes** | Pure control-plane logic. |
| Promotion without rebuild | **Yes** | The image is in the in-cluster registry; promotion is applying it with another app's settings. |
| *Instant* rollback | **Nearly** | Vercel's rollback is an alias swap onto something already serving. On Kubernetes it is a rolling update: seconds while the old image starts, unless the previous ReplicaSet is kept warm (blue/green), which doubles the app's memory on a 1 GB server. |
| Build cache, build logs, monorepo skipping, framework detection | **Yes** | BuildKit, the Git push payload's changed-file list, the detector Skifity already has. |
| Rolling releases / canary | **Yes, with work** | Traefik weighted services and a sticky cookie; comparing canary with stable needs per-version request metrics first. |
| Logs with retention, request metrics, drains | **Yes, at a memory cost** | Traefik already exports Prometheus metrics per router; a log store (Loki/VictoriaLogs) or a Vector DaemonSet costs RAM the 35 MiB panel does not. Make it an opt-in component, like cert-manager. |
| Web Analytics / real-user metrics | **Yes** | An analytics template (Umami, Plausible-style) already fits the catalogue; it is app-level, not platform-level. |
| IP/country/ASN rules | **Yes** — Skifity has it (Written) | |
| Rate limiting | **Yes, per node** | Traefik's RateLimit middleware or the guard; counters are per replica unless shared through Redis — Vercel's are per region, the same compromise. |
| OWASP rules, JS challenge | **Partly** | Coraza (a WAF engine) as a Traefik plugin, a proof-of-work challenge; both cost CPU on the same server as the app. |
| Bot reputation, JA4 intelligence, AI-bot lists | **No** | Their value is data aggregated across millions of sites; one server sees only its own traffic. |
| Volumetric DDoS absorption | **No** | A single uplink saturates before any software runs. The self-hosted answer is to put an edge network in front — which Skifity's Cloudflare tunnel does. |
| Global CDN, ISR at the edge, edge middleware, image cache near users, Global Config reads in ~ms worldwide | **No** | These *are* the edge network. A self-hosted panel can serve them from one region; it cannot make them close to every user. |
| Spend management | **Not needed** | A server has a fixed price. What an attack costs on your own server is downtime, not an invoice — worth saying in the product, because it is the most common reason people leave Vercel. |
| Remote MCP, agent deploys | **Yes** | The panel is already an HTTP server with scoped tokens. |
| v0, Vercel Agent, AI Gateway, Sandbox | **Not the product** | These are AI products sold alongside hosting, not hosting features. |

The honest summary: **the preview-and-promote workflow — the part people name when
they say "Vercel DX" — is control-plane logic and fully reproducible**. What is not
reproducible is latency near every user and absorbing attacks, and for those the
right design is to sit behind an edge network rather than imitate one.

## Gaps worth closing in Skifity

Ordered by how much a person loses without it. None of these is something Skifity
already has; the evidence for Skifity's side is in the table above.

### P0

1. **A preview of an app with a database gets a database of its own.**
   *What:* when a pull request's preview is created, create an empty database of the
   same engine for every database linked to the app, link it under the same
   variable name in the preview environment, and let it go with the namespace.
   Optionally seed it from the production database's latest backup (off by default,
   because it copies personal data to a URL anybody may open).
   *Evidence:* today `copyPreviewVariables` (`internal/api/webhook_handlers.go`)
   copies the production app's `DATABASE_URL`, which `dbsvc.Link` wrote as a secret
   variable naming `<db>.<production namespace>.svc.cluster.local`
   (`internal/dbsvc/manager.go`, `internal/dbsvc/manifests.go`); the preview's
   namespace has a default-deny policy that only allows egress inside its own
   namespace (`internal/kube/namespace.go`), so the preview cannot reach it and a
   release command (migrations) fails. Since Phase 73 detection creates a database
   with most apps, this is the common case, not an edge case. Netlify pairs every
   deploy preview with an isolated database branch (GA April 2026); Vercel's
   Marketplace Neon integration branches per preview.
   *Fit:* `deployPreview` reads `ListLinksForApp`, calls `dbsvc.Manager` to create
   and `Link` in the preview environment, and skips those variable names when
   copying. The panel's preview list shows the databases it made.
   *Size:* M for empty databases, L for seeded ones.
   *Without a cluster:* the orchestration (what is created, what is skipped, what is
   removed) is unit-testable against the fake clientset and the store; the database
   actually starting needs a cluster (`make verify`).

2. **Preview values for variables.**
   *What:* a variable can have a production value and a preview value (and later a
   value per environment and per branch pattern); a preview uses the preview value
   when there is one. `skifity env set --preview KEY=value`, a column in the
   Variables tab, a `preview` flag on the MCP `set_variable` tool.
   *Evidence:* a same-repository pull request's preview currently runs the PR's code
   with production's secrets (`copyPreviewVariables` copies every row), so a preview
   talks to live payment, email and webhook endpoints, and there is no way to say
   "use the test key here". Vercel has Preview values with branch-specific
   overrides; Netlify has deploy contexts and branch patterns. Shared variables are
   per project and apply to every environment (`SharedVariable`,
   `internal/store/models.go`), so they cannot express it either.
   *Fit:* a `context` column on `variables` (production/preview), resealed under
   the preview app's context exactly as today; the fork rule is unchanged.
   *Size:* M. *Without a cluster:* yes — store, API, CLI tests and the Playwright
   test.

### P1

3. **Protect previews by default, with the panel's own accounts.**
   *What:* a preview is not open to whoever guesses `<app>-pr-<n>.<wildcard>`
   (`internal/kube/naming.go` makes it predictable) just because production is
   public. Skifity now has a per-app basicAuth password that previews inherit
   (`7865bc8`, `internal/kube/password.go`) — but only when the production app has
   one, which a public site does not. What remains: **protection on for previews by
   default**, **"sign in with your panel account"** (Vercel Authentication, and so
   the panel's OIDC) rather than a shared password that crosses plain HTTP on the
   sslip.io address, and a **bypass token** for CI end-to-end tests.
   *Evidence:* Vercel ships team-member authentication on every plan including
   Hobby and lets a team make it the default for new projects; Netlify gates
   previews by password (Pro) or team login (Enterprise). Firewall rules are stored
   per app (`internal/store/firewall.go`) and, unlike the password, are not copied
   to previews.
   *Fit:* a guard action in `internal/guard` that redirects to the panel, which
   checks `authorizeApp` and issues a short-lived token bound to that host; cookies
   on the preview host, never the panel's `__Host-` cookie. *Size:* M.
   *Without a cluster:* the guard decision and the panel flow, yes; Traefik
   forwarding, no — the same gap the firewall already has.

4. **Promote an image; hold production until promoted.**
   *What:* `POST /api/apps/{app}/promote` deploys another app's successful
   deployment (same team, same repository) with the target app's own settings, no
   build; an app setting "build on push, wait for promotion"; and after a rollback,
   auto-deploy pauses until someone promotes or re-enables it.
   *Evidence:* Vercel's promote, staged production and "Undo Rollback"; Netlify's
   locked deploys. In Skifity a staging app and a production app build the same
   commit twice (fingerprints are looked up per app), and a rollback is undone by the
   next push to the branch.
   *Fit:* `internal/deploy` gains `Promote`; `dispatchGitEvent` honours a hold flag;
   the Deployments tab gets "Promote to…". *Size:* M.
   *Without a cluster:* yes for the logic; the rollout is the existing path.

5. **Skip apps whose directory did not change.**
   *What:* read the changed paths from the push payload (GitHub, GitLab and Gitea
   list added/modified/removed files per commit) and skip an app whose `root_dir`
   (plus optional extra watch paths) is untouched, saying so in the webhook result
   and the app's history.
   *Evidence:* Vercel skips unaffected projects by default for new monorepos;
   Netlify documents an ignore command for the same reason. Skifity rebuilds every
   app bound to the repository on every push.
   *Fit:* `gitsrc.PushEvent` gets `ChangedPaths`; `dispatchGitEvent` filters.
   *Size:* S–M. *Without a cluster:* yes, entirely.

6. **Know when the framework being deployed has a critical advisory.**
   *What:* detection reads dependency versions (at least `next`, `react`,
   `react-server-dom-*`) from the lockfile, compares them with a small advisory
   table compiled into each release (so nothing phones home), and warns — or refuses
   with an override — naming the fixed version; the ingress strips
   `x-middleware-subrequest` by default.
   *Evidence:* CVE-2025-29927 affected exactly the self-hosted `next start` that
   Skifity builds, while Vercel and Netlify were immune; for React2Shell both
   platforms blocked vulnerable deploys and shipped WAF rules within hours.
   `internal/builder/detect.go` reads `engines.node` and nothing else about
   versions. *Fit:* `internal/builder` plus an `errdoc` entry; a Traefik headers
   middleware in `internal/kube`. *Size:* S–M.
   *Without a cluster:* detection yes; the header strip needs Traefik.

7. **Request metrics and logs that outlive the pod.**
   *What:* per-app request rate, status codes and latency on the app's overview,
   from Traefik's Prometheus metrics sampled on the minute tick into a bounded table;
   optional log retention and search as a component installed on first use.
   *Evidence:* Vercel's observability (1 day of runtime logs on Pro) and Netlify's
   (7 days on Pro) are the first place users look; Skifity shows only live logs and
   the previous container's (`internal/api/stream_handlers.go`), and cannot answer
   "how many 500s since the deploy". Also a prerequisite for canaries.
   *Size:* L. *Without a cluster:* no (the parser and storage are testable, the
   numbers are not).

8. **A remote MCP endpoint on the panel.**
   *What:* the same tools over Streamable HTTP at `/api/mcp`, authenticated with the
   existing scoped tokens (OAuth later), so Claude.ai, ChatGPT and hosted agents can
   use Skifity without the binary on their machine.
   *Evidence:* Vercel's MCP is a remote server with OAuth and can deploy; Netlify
   offers remote and local. Skifity's server runs on `mcp.StdioTransport`
   (`internal/mcpserver/server.go`), which a web connector cannot use.
   *Size:* M. *Without a cluster:* yes — Go tests and the smoke test.

### P2

9. **Redeploy without the build cache.** A checkbox on Deploy, `skifity deploy
   --no-cache`, an MCP flag; the build Job omits `--import-cache`
   (`internal/builder/job.go`). Vercel and Netlify both have it because a poisoned
   cache is a real failure. *S; no cluster (golden Job test).*
10. **A rate-limit action in the firewall.** Vercel moved rate limiting down to
    Hobby; Netlify has code-based limits on every plan; Skifity's rules can only allow
    or block (`docs/firewall.md`). Counters per guard replica, documented as such.
    *M; the engine without a cluster, enforcement with one.*
11. **One wildcard certificate for automatic and preview addresses.** Each automatic
    hostname gets its own certificate when a wildcard domain is set
    (`internal/cluster/cluster.go`); Let's Encrypt allows 50 new certificates per
    registered domain per 7 days, which a busy repository with several apps and
    previews can exhaust. DNS-01 needs a DNS provider — the plugin kind ADR-0021
    already names as missing. *M; needs a cluster and real DNS.*
12. **Branch deploys.** A push to a branch with no pull request is ignored
    (`dispatchGitEvent`); Vercel previews every branch, Netlify has branch deploys.
    Opt-in per app, with the same TTL as previews. *S–M; no cluster for the logic.*
13. **Deploy hooks scoped to one app.** Tokens are bound to a team, not an app
    (`internal/auth/scopes.go`), so a CI token that may deploy one app may deploy all
    of them. An app-scoped token or an unguessable per-app hook URL. *S; no cluster.*
14. **Log drains.** Vercel sells them on Pro ($0.50/GB), Netlify on Enterprise only.
    A plugin kind (`log.destination`) or a Vector component forwarding to HTTP/OTLP.
    *M; needs a cluster.*
15. **Canary releases.** Traefik weighted services between two Deployments with a
    sticky cookie, after item 7 exists to judge them. *L; needs a cluster.*

## Things to deliberately not copy

* **Metered billing and a spend cap that takes production down.** Vercel's own
  documentation describes a check every few minutes and a stop that pauses every
  project on the team. Skifity's answer to bill shock is having no bill; keep "no
  billing anywhere" (roadmap, "A hosted version") and say so where people compare.
* **Paywalled security.** SAML at $300/month and password protection at $20 per
  project per month are the complaints; Skifity's OIDC, TOTP and audit log are free
  and should stay that way, including any preview protection.
* **Opt-out telemetry in developer tooling.** The Vercel plugin for Claude Code
  collected bash commands and prompts by default and lost trust over it. Skifity
  "never phones home"; an advisory table shipped in the release (gap 6) keeps that
  promise where an online lookup would break it.
* **Readable-by-default variables.** The April 2026 breach decrypted exactly the
  variables that were not marked sensitive, and Vercel spent the next four months
  converging on what Skifity already does. Do not add a "show value" mode for
  secrets, and keep `logging.LooksSecret` deciding when the user did not.
* **Every deployment addressable forever.** It is cheap on serverless and expensive
  on a VPS; per-PR URLs with a TTL (`internal/watch/watch.go`) are the right
  shape here.
* **Edge-only products**: Global Config, ISR at the edge, an image CDN, edge
  middleware. On one region they are complexity without the benefit; point people at
  an edge network in front instead (the Cloudflare tunnel exists).
* **Framework-shaped infrastructure.** "Next.js 15.1 is unusable outside of Vercel"
  is what coupling a platform to one framework's internals produces. Skifity runs
  containers and should keep treating every framework the same.
* **Silently changing a default that costs resources** (Turbo build machines became
  the Pro default without notice). The self-hosted equivalent is quietly raising
  build or preview resource limits; the checklist's "generous and visible" rule for
  quotas is the right one.
* **A 0% canary anyone can force** (`vcrrForceCanary=true` is settable by any
  visitor, as Vercel's docs warn). If canaries arrive, an unreleased version should
  not be one query parameter away.
* **Daily-only cron and hour-level jitter on the free tier**: an artefact of
  metering, irrelevant on your own server.

## Since 2026-09-30

Read on 2026-11-08: Vercel's changelog feed (155 entries dated 2026-09-30 to
2026-11-07; about half announce models on AI Gateway and were not read further),
its security bulletins, the pricing page, the npm registry for `vercel`, the OSV
database for `next`, shields.io for GitHub counts, and Hacker News through the
Algolia API. GitHub's API refused this session for repositories other than
Skifity's, so stars and issues came from shields.io, which rounds.

### Backends, at Ship 2026

- **2026-10-15, Vercel Ship.** Python became generally available: FastAPI,
  Flask and Django on CPython with uv, which Vercel says 150,000 teams already
  run. Container support in Functions counted 5,000 teams and 150,000 container
  deployments since its beta. **Daemons** entered public beta: containers run
  continuously as workers that pull work (queue consumers, Celery, BullMQ,
  Temporal workers, self-hosted CI runners), at an instance count per region,
  restarted when they exit or crash. They gained HTTP liveness checks and
  service bindings on 2026-10-30; their CPU and memory metrics need
  Observability Plus.
- **Drives** became generally available the same day (corrected in place
  above), for Sandbox only. **Bring Your Own Cloud on AWS** became generally
  available for Enterprise: Node.js and Python functions run in the customer's
  AWS account, while Vercel keeps deployments, routing and static assets.
- **Smaller changes.** WebSockets in Functions became generally available
  (2026-10-20; 15 million connections a day). A plain `Dockerfile` deploys when
  no framework is detected (2026-10-27). `vercel.toml` joined `vercel.json` and
  `vercel.ts` (2026-11-02). Node.js 26 became the default (2026-10-29).
  Monorepo projects that are skipped no longer count against deployment limits
  (2026-10-21).
- **Vercel CI** (beta, 2026-10-28) runs only the Turborepo tasks a push affects,
  each on its own machine, and reports a GitHub check; since 2026-11-03 it runs
  on private GitHub repositories with no Vercel project.
- **Vercel Schedules** (blog 2026-11-04, public beta 2026-11-05) is the
  successor to Cron Jobs: one-time and recurring schedules, created in
  `vercel.json` or at runtime through the SDK, CLI or API, aimed at a private
  function, an HTTP path or a queue topic, run on demand, testable in previews.
  $5 per million runs and $10 per million creations on Pro; 500 creations and
  5,000 runs a month on Hobby.

### Pricing, retention and terms

- **Rollback history now has a price and a horizon.** Deployment Storage and
  Functions Storage are billed at $0.10 per GB-month for existing Pro teams;
  new Pro teams keep 30 days of deployments by default (2026-10-09); older
  deployments of existing Pro teams are deleted from 2026-10-23 unless they opt
  out; deleted deployments are removed for good sooner (2026-10-16). Corrected
  in place under "Storage and backups" and "Instant Rollback".
- **Data Cache becomes Runtime Cache** (2026-10-30). Reads and writes that were
  not billed are charged from $0.40 per million read units and $4.00 per million
  write units, varying by region. Hobby teams move on 2026-11-29 with a million
  reads and 200,000 writes a month free; Pro teams are told their date.
- **Spend Management in the CLI** (2026-10-23): `vercel budgets`. The changelog
  repeats that "setting a budget does not stop usage on its own"; `--pause`
  pauses production deployments of every project at 100%.
- **Terms of Service** for Hobby and Pro, effective 2026-10-19: plan credits
  expire at the end of the term and purchased credits after a year; a company
  can manage accounts on its email domain, with notice; UK and EU consumers may
  cancel for free after a change that harms their use.
- The pricing page on 2026-11-08 still shows Hobby at $0 and Pro at $20 a month
  with $20 of usage credit and free viewer seats.

### Observability, firewall and security products

- **Custom monitors** (2026-10-26, corrected in place above); Vercel Agent can
  check an alert before it pages (2026-10-28). **Tracing** became generally
  available (2026-11-06).
- **AI traffic** in Observability (2026-10-28), and the AI Bots managed
  ruleset now treats crawlers (indexing and training) and assistants (fetching
  for a user) separately, on every plan (2026-10-29). IP geolocation became
  city-level for nearly all requests (2026-10-29), which puts some visitors in
  a different country, most often those on VPNs, hosting providers and leased
  ranges.
- **A Security Dashboard**, described in "Vercel for Enterprise" (2026-10-15),
  "flags risks across your team and apps, including missing MFA, unprotected
  preview deployments, long-lived credentials, and framework vulnerabilities",
  each linked to the setting that fixes it.
- **Security Review** (public beta 2026-11-03, Pro and Enterprise): Vercel
  Agent reads the diff of every push to a connected GitHub repository against
  the previous deployment, reports findings in the dashboard, a GitHub check and
  pull request comments, takes team policies written in plain language, and can
  open a fix. Billed at Agent rates; free until 2026-11-10.
- **Builds refuse blocked npm packages** (2026-11-02): a list of package
  versions blocked for malware or vulnerabilities is checked against the
  lockfile, including one restored from the build cache, and against downloads
  during installation; a hit fails the deployment and invalidates the cache.
- **Leaked tokens are revoked** (2026-11-04): Truffle Security reports Vercel
  tokens found in public GitHub, GitLab, Docker images, npm, PyPI, Hugging Face,
  Android apps and Firefox add-ons, and a scanner reads each monthly Common Crawl.
- **"Death to long-lived static credentials"** (2026-10-22): Marketplace vendors
  that do not accept Vercel's OIDC tokens get a "Not secure" label by 2026-11-30
  and are delisted in January 2027.

### Agents and the MCP

- The MCP server accepts any client with a Client ID Metadata Document
  (2026-11-02, corrected in place above), shows deployments as an interactive
  card in ChatGPT, its first MCP App (2026-11-02), and has a `submit_feedback`
  tool (2026-10-23). The Vercel plugin reached OpenCode (2026-10-28) and Google
  Antigravity (2026-10-29).
- The CLI gained `vercel doctor`, which checks the install, the login, the team,
  the project link and `vercel.json`, never prompts, and prints the command that
  fixes each problem (2026-11-03); `--delete-project-after` for projects that
  delete themselves after an hour to 30 days (2026-11-06); deploys with no account
  (2026-10-22); and domain purchases an agent can prepare but only a person can
  confirm (2026-10-09).
- At Ship, Vercel said agents can use "over 200 CLI commands", and that
  agent-triggered deployments grew 28 times in a year.

### Security record

No breach of Vercel was disclosed in the period. Its bulletins were about
software its customers run, and in each one Vercel patched what it manages and
put WAF rules in front of everything else:

- **Node.js CVE-2026-111497** (bulletin 2026-10-21): an out-of-bounds write in
  the bundled Brotli decoder, with memory disclosure and command execution shown
  on 22.x, 24.x and 26.x. WAF rules went out the same day, outbound Brotli was
  turned off for unpatched runtimes, and every new deployment used a patched
  Node.js from 2026-10-28. Node.js 20 is past end of life and gets no fix.
- **php2shell, CVE-2026-109618 and CVE-2026-109619** (2026-10-21): two critical
  remote code executions in PHP 5.4 and later; WAF rules were in place before
  disclosure.
- **eMudhra** (bulletin 2026-10-27): the private keys of eight intermediate CAs
  could be recovered (found 2026-10-17). Vercel stopped trusting the roots in its
  builds, functions and sandboxes; Node.js 22.23.4, 24.22.1 and 26.12.1 and
  certifi 2026.10.19 drop them.
- **CVE-2026-98432** (bulletin 2026-11-01): remote code execution in the Linux
  kernel through crafted IPv4 TCP packets, shown by Anthropic against a
  Multipath TCP listener on 2026-10-27. Vercel patched hosts and guest kernels
  across builds, functions, sandboxes, daemons, CI and its control plane.
- **Name.com** (2026-10-31): a domain takeover campaign at the registrar
  touched nine domains that originated on Vercel, since restored.
- **Better Auth** (2026-11-03): twelve advisories, one critical; WAF rules.
- **The Sandbox challenge** (results 2026-10-14): 1,285 reports, 57 valid,
  $242,000 paid. A Linux kernel networking zero-day leaked host memory from
  inside a microVM, shown against production sandboxes, and gave code execution
  on the host, shown on the researcher's own Firecracker machines. A
  cross-tenant snapshot swap worked because the host trusted a storage location
  reported by the guest; it was fixed within a week. An intern found recycled
  disk blocks readable across sandboxes.
- **Next.js**, per OSV, since 2026-09-30: critical remote code executions in
  `next/og` (GHSA-vcvr-r3jv-pc5j, 2026-09-30, 16.2.0 to before 16.3.6), in Image
  Optimization with SVG files (GHSA-2464-fhr4-w9xw, 2026-10-22, every version
  from 10.0.0 before 15.5.28, and 16.x before 16.4.1) and in `use cache`
  functions returning typed arrays (GHSA-673q-mjm6-r5xr, 2026-10-22, 16.x before
  16.4.1); a high SSRF in Image Optimization (CVE-2026-94483, 2026-10-07); a
  high denial of service through Server Actions (CVE-2026-94482, 2026-10-22);
  and cache poisoning "in self-hosted applications" (CVE-2026-94543, moderate,
  2026-10-07).

### Numbers on 2026-11-08

- The `vercel` CLI reached 65.12.1 on 2026-11-06: 33 stable releases since
  2026-09-30, and 280 in 2026.
- shields.io: `vercel/next.js` about 143,000 stars, `vercel/vercel` about
  17,000. Its open-issue counts leave out pull requests, so they cannot be
  compared with the API's on 2026-09-30.
- Hacker News: no story about Vercel itself above 100 points in the period.
  One comment (2026-10-13) describes acquaintances whose Vercel bill "goes
  through the roof" after a launch on Hacker News drew bot traffic, "even with
  bot protection from CF".

### For Skifity

Checked against the code on 2026-11-08.

- **The pattern of this period's bulletins is the case for a self-hosted panel
  being told, not the case against it.** For Node.js, PHP, the CA roots and the
  kernel, Vercel's customers were patched by Vercel. On Skifity the language
  runtime is inside each app's image and the kernel belongs to the operator.
  Skifity warns about the framework versions in its table (Phase 97), scans
  images nightly (`internal/vulnscan`, `app.vulnerable`), and its hardening
  report says when updates are off or a reboot is pending
  (`internal/provision/hardening.go`).
- **Long-lived workers are no longer Skifity's alone.** Daemons are Vercel's
  answer to processes from one build (Phase 106), corrected in the table's
  first row. Vercel still has no disk for them and no databases with a disk;
  Drives mount only in a Sandbox.
- **Rollback now has a horizon on both sides.** Skifity keeps ten images per
  app (`registry.KeptPerApp`) and refuses a rollback to one it has collected;
  Vercel Pro now keeps 30 days by default and bills the storage.
- **MCP.** Vercel's OAuth with Client ID Metadata Documents lets any assistant
  connect after consent. Skifity's `/api/mcp` takes a pasted bearer token only
  (`internal/api/mcp_handlers.go`); OAuth is gap 1 of `openship.md`, still open.

This file's gaps, against the code:

1. A preview's own database: **closed** (Phase 76), seeded if asked (Phase 107).
2. Preview values for variables: **closed** (Phase 93).
3. Previews protected by default, signing in with the panel's accounts:
   **open**. A preview still only inherits the app's password
   (`internal/kube/password.go`), and the guard holds no session.
4. Promote, and hold production: **mostly closed** — promotion (Phase 103),
   locked deploys and a rollback plan (Phase 94).
5. Skip apps a push did not touch: **closed** (watch paths, Phase 96).
6. Framework advisories: **closed** (Phase 97), but the table has fallen behind;
   see new gap 1.
7. Request metrics and logs that outlive the pod: **closed** (`internal/traffic`,
   `internal/logdrain`).
8. A remote MCP endpoint: **closed** (Phase 87), without OAuth.
9. Redeploy without the build cache: **open**. `skifity deploy --force` rebuilds,
   but every build imports the BuildKit cache (`--import-cache`,
   `internal/builder/job.go`); see new gap 2.
10. A rate-limit action in the firewall: **closed** ("Limit the rate",
    `internal/guard/limiter.go`, `docs/firewall.md`).
11. One wildcard certificate: **open**; DNS-01 is still absent, though records
    can now be kept at four DNS providers (`internal/dnsprov`).
12. Branch deploys: **partly**; a preview of a branch can be started by hand
    (Phase 112), not on every push.
13. App-scoped deploy tokens: **open**. `APIToken` (`internal/store/models.go`)
    has scopes, an expiry and a network allowlist, and no app.
14. Log drains: **closed** (`internal/logdrain`).
15. Canary releases: **open**.

Rows of the table above that no longer describe Skifity: Monorepo (watch
paths); Promotion without rebuild (Phase 103); Observability (requests, errors
and response times per app, usage history, log drains); Alerts (`app.alert`,
`server.alert`, server-error thresholds, `backup.missed`,
`certificate.expiring`); Firewall (a rate-limit action); Preview environments
(their own databases, preview values, whole-environment previews, a preview by
hand); CLI, API, IaC (`skifity.yaml` with plan and apply, Phase 108); MCP (served
by the panel); Notifications (ten kinds); Team (a viewer role, Phase 85); DX (two
releases, and a real cluster on every push).

New gaps:

1. **Keep the advisory table current.** *What:* add this period's critical
   Next.js advisories to `knownAdvisories`, let `ID` hold a GHSA where there is no
   CVE yet, and make reading the advisory databases a step of every release in
   `docs/releasing.md`, so the table never ships older than the release.
   *Evidence:* `internal/builder/advisories.go` lists CVE-2025-29927 and
   CVE-2025-55182 only. It misses GHSA-2xp9-vwfh-vxw4 (2026-09-08) and the three
   critical remote code executions since 2026-09-30 above; GHSA-2464-fhr4-w9xw
   alone affects every Next.js from 10.0.0 before 15.5.28 and 16.x before
   16.4.1, so nearly every Next.js app deployed today would warn.
   (GHSA-p293-qw3h-jr36 is Windows-hosted servers only and does not apply.)
   *Size:* S. *Without a cluster:* yes.
2. **Rebuild without the cache, and every app at once.** *What:* a "Rebuild
   from scratch" on the app and `skifity deploy --no-cache`, which leave out
   `--import-cache`; and a team action that rebuilds, without the cache, every
   app whose detected runtime is named (Node.js, PHP), for the day a runtime is
   patched. *Evidence:* Vercel moved every new deployment to a patched Node.js
   within a week of CVE-2026-111497, and its builds now refuse a blocked
   package even when the lockfile came from the cache. On Skifity the runtime
   is a layer of each app's image, and a forced rebuild can take that layer from
   the BuildKit cache. *Size:* S–M. *Without a cluster:* the Job rendering and
   the selection, yes; a build, no.
3. **One page of what is weak, per team.** *What:* members without a second
   factor where the team does not require one, tokens with no expiry or unused
   for months, apps with a fixable critical vulnerability or a framework
   advisory, previews open to anyone, servers whose hardening report warns —
   each linked to the place that fixes it. *Evidence:* Vercel's Security
   Dashboard. Skifity has every piece separately — team-required two factors
   (Phase 91), token expiry (`APIToken`), the image scan per app
   (`web/src/components/app/security-tab.tsx`), advisories in the build log,
   the hardening report per server — and Settings → Security holds only the
   recovery key and key rotation. *Size:* M. *Without a cluster:* yes.
4. **A ready-made rule for AI crawlers.** *What:* a firewall condition "known
   AI crawler", from a list compiled into each release (no lookup, so nothing
   phones home), kept apart from assistants fetching a page for a person.
   *Evidence:* Vercel's AI Bots ruleset (2026-10-29). Skifity's rules can match
   the `User-Agent` header (`docs/firewall.md`), so each team writes and
   maintains its own list. *Size:* S. *Without a cluster:* the rule engine, yes.

## Sources

All read 2026-09-30.

* https://vercel.com/pricing
* https://vercel.com/blog/new-pro-pricing-plan
* https://vercel.com/changelog/included-pro-usage-is-now-credit-based
* https://vercel.com/docs/plans/hobby
* https://vercel.com/docs/spend-management
* https://vercel.com/docs/deployments/environments
* https://vercel.com/docs/deployment-protection
* https://vercel.com/docs/git/vercel-for-github
* https://vercel.com/docs/instant-rollback
* https://vercel.com/docs/rolling-releases
* https://vercel.com/docs/deployment-checks
* https://vercel.com/docs/environment-variables
* https://vercel.com/changelog/environment-variables-now-use-config-and-secret-types
* https://vercel.com/docs/monorepos
* https://vercel.com/docs/deployments/troubleshoot-a-build
* https://vercel.com/docs/frameworks
* https://vercel.com/docs/vercel-firewall
* https://vercel.com/docs/vercel-firewall/vercel-waf/rate-limiting
* https://vercel.com/docs/vercel-firewall/vercel-waf/managed-rulesets
* https://vercel.com/docs/drains
* https://vercel.com/docs/cron-jobs
* https://vercel.com/docs/cron-jobs/usage-and-pricing
* https://vercel.com/changelog/bring-your-dockerfile-to-vercel-functions
* https://vercel.com/i/7-ways-to-use-docker-containers-on-vercel
* https://vercel.com/kb/guide/docker-compose-concepts-on-vercel
* https://vercel.com/kb/guide/bolt-vercel-drop
* https://vercel.com/docs/agent-resources/vercel-mcp
* https://vercel.com/docs/deployments/claim-deployments and https://vercel.com/changelog/claimed-deployments-now-include-third-party-resources
* https://vercel.com/docs/agent/pricing and https://vercel.com/docs/agent
* https://v0.app/pricing
* https://vercel.com/kb/bulletin/vercel-april-2026-security-incident
* https://blog.gitguardian.com/vercel-april-2026-incident-non-sensitive-environment-variables-need-investigation-too/
* https://vercel.com/kb/bulletin/react2shell and https://vercel.com/changelog/cve-2025-55182
* https://vercel.com/blog/our-million-dollar-hacker-challenge-for-react2shell
* https://vercel.com/blog/postmortem-on-next-js-middleware-bypass
* https://securitylabs.datadoghq.com/articles/nextjs-middleware-auth-bypass/
* https://github.com/advisories?query=vercel
* https://nextjs.org/blog/nextjs-across-platforms (Adapter API) and https://www.netlify.com/blog/the-next-js-adapter-api-just-shipped-here-s-what-comes-next/
* https://community.vercel.com/t/vercel-build-minutes-billing-enabled-by-default-causing-unexpected-charges/37434
* https://community.vercel.com/t/why-developers-prefer-using-preview-deployments-on-vercel/28450
* https://bex.co/blog/2026/07/08/vercel-no-spending-cap
* https://www.luckymedia.dev/insights/vercel
* https://www.pandastack.ai/blog/best-preview-environment-platforms-2026/
* https://www.trustpilot.com/review/vercel.com
* https://news.ycombinator.com/item?id=47824463 (April 2026 incident)
* https://news.ycombinator.com/item?id=47704881 (Vercel plugin telemetry)
* https://news.ycombinator.com/item?id=47967508 (Vercel's pricing page)
* https://news.ycombinator.com/item?id=46317098 (Mintlify supply chain)
* https://news.ycombinator.com/item?id=44255911 (Next.js 15.1 outside Vercel)
* https://hn.algolia.com/api/v1/search (queries "vercel", stories since 2025-01-01)
* https://sacra.com/c/vercel/ and https://getlatka.com/companies/vercel
* https://github.com/vercel/vercel (licence) and the GitHub search API (stars, open issues)
* https://registry.npmjs.org/vercel (release cadence)
* https://letsencrypt.org/docs/rate-limits/

Read 2026-11-08, for "Since 2026-09-30":

* https://vercel.com/atom (the changelog and blog feed; entries dated 2026-09-30 to 2026-11-07, read from their content). Among them:
  * https://vercel.com/blog/vercel-ship-2026-recap, https://vercel.com/blog/backends-on-vercel-python-containers-and-daemons and https://vercel.com/changelog/vercel-daemons-is-now-in-public-beta (2026-10-15)
  * https://vercel.com/changelog/vercel-drives-is-now-generally-available and https://vercel.com/changelog/bring-your-own-cloud-on-aws-is-now-generally-available (2026-10-15)
  * https://vercel.com/blog/vercel-for-enterprise (2026-10-15, the Security Dashboard)
  * https://vercel.com/changelog/new-pro-teams-now-default-to-30-day-deployment-retention and https://vercel.com/changelog/deployment-storage-pricing-expands-to-existing-teams (2026-10-09)
  * https://vercel.com/changelog/deleted-deployments-are-permanently-removed-sooner (2026-10-16)
  * https://vercel.com/changelog/deploy-with-vercel-drop-without-an-account (2026-10-19) and https://vercel.com/changelog/deploy-without-an-account-using-vercel-cli (2026-10-22)
  * https://vercel.com/changelog/updates-to-hobby-and-pro-terms-of-service (2026-10-19)
  * https://vercel.com/changelog/websocket-support-is-now-generally-available (2026-10-20)
  * https://vercel.com/changelog/skipped-monorepo-deployments-no-longer-count-against-limits (2026-10-21)
  * https://vercel.com/blog/death-to-long-lived-static-credentials (2026-10-22)
  * https://vercel.com/changelog/vercel-cli-adds-spend-management and https://vercel.com/changelog/send-feedback-from-vercel-mcp (2026-10-23)
  * https://vercel.com/changelog/create-custom-monitors-in-vercel-observability (2026-10-26)
  * https://vercel.com/changelog/deploy-a-container-from-a-plain-dockerfile (2026-10-27)
  * https://vercel.com/blog/introducing-vercel-ci, https://vercel.com/changelog/ai-traffic-insights-now-available-in-vercel-observability, https://vercel.com/changelog/vercel-agent-can-now-validate-alerts-before-paging-you and https://vercel.com/changelog/vercel-plugin-now-available-in-opencode (2026-10-28)
  * https://vercel.com/changelog/block-ai-crawlers-and-assistants-independently-with-managed-rules, https://vercel.com/changelog/improved-ip-geolocation-data, https://vercel.com/changelog/node-js-26-lts-is-now-generally-available-on-vercel and https://vercel.com/changelog/vercel-plugin-is-now-available-in-antigravity (2026-10-29)
  * https://vercel.com/changelog/data-cache-is-moving-to-runtime-cache, https://vercel.com/changelog/add-liveness-checks-to-your-daemons and https://vercel.com/changelog/vercel-daemons-can-now-call-services-with-bindings (2026-10-30)
  * https://vercel.com/changelog/vercel-mcp-now-supports-client-id-metadata-documents, https://vercel.com/changelog/monitor-vercel-deployments-in-chatgpt, https://vercel.com/changelog/projects-can-now-be-configured-with-vercel-toml and https://vercel.com/changelog/blocking-malicious-npm-packages-before-and-during-installation (2026-11-02)
  * https://vercel.com/blog/vercel-security-review, https://vercel.com/changelog/vercel-security-review-public-beta, https://vercel.com/changelog/vercel-cli-now-diagnoses-setup-problems-with-vercel-doctor and https://vercel.com/changelog/vercel-ci-no-longer-requires-a-linked-vercel-project (2026-11-02 and 2026-11-03)
  * https://vercel.com/blog/introducing-vercel-schedules, https://vercel.com/changelog/vercel-schedules-is-now-in-public-beta and https://vercel.com/changelog/vercel-token-scanning-expands-to-more-public-sources (2026-11-04 and 2026-11-05)
  * https://vercel.com/changelog/vercel-tracing-is-now-generally-available and https://vercel.com/changelog/vercel-cli-can-now-set-new-projects-to-expire (2026-11-06)
  * https://vercel.com/changelog/agents-can-now-buy-domains-with-the-vercel-cli (2026-10-09)
  * https://vercel.com/blog/findings-from-the-1-million-dollar-vercel-sandbox-challenge (2026-10-14)
* https://vercel.com/kb/bulletin and its bulletins: node-js-security-advisory (2026-10-21), php2shell-security-bulletin (2026-10-21), emudhra-security-advisory-compromised-intermediate-ca-keys (2026-10-27), name-com-domain-takeover-notice (2026-10-31), linux-kernel-security-advisory-cve-2026-98432 (2026-11-01), better-auth-november-2026-security-release (2026-11-03)
* https://vercel.com/pricing
* https://api.osv.dev/v1/query (package `next`, npm: advisories published since 2026-09-01, with affected ranges)
* https://registry.npmjs.org/vercel (release dates)
* https://img.shields.io/github/stars/vercel/next.js.json and https://img.shields.io/github/stars/vercel/vercel.json
* https://hn.algolia.com/api/v1/search (stories and comments mentioning Vercel since 2026-09-30); https://news.ycombinator.com/item?id=50078478 (bot traffic and a Vercel bill, 2026-10-13)
* Skifity, for "For Skifity": `internal/builder/advisories.go`, `internal/builder/job.go`, `internal/deploy/deployer.go`, `internal/registry/registry.go`, `internal/api/mcp_handlers.go`, `internal/api/webhook_handlers.go`, `internal/kube/password.go`, `internal/guard`, `internal/store/models.go`, `internal/vulnscan`, `internal/provision/hardening.go`, `internal/dnsprov`, `internal/traffic`, `internal/logdrain`, `internal/cli/commands.go`, `web/src/components/app/security-tab.tsx`, `web/src/pages/settings.tsx`, `docs/firewall.md`, `docs/releasing.md`, `docs/progress.md`, `docs/research/competitors/openship.md`
