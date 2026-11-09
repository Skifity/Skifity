# Research: Competing platforms

Researched 2026-09-16, re-researched against live sources 2026-09-17, and on
2026-09-30 researched again **one product at a time, in depth**: twenty-two
products, each in its own file under [`competitors/`](competitors/). Every file
has the same nine parts — what the product is and runs on, a feature inventory in
fourteen groups, what users love, what they complain about, its security record,
a capability-by-capability table against Skifity with the file each Skifity
claim was checked in, the gaps worth closing, the things not to copy, and dated
sources.

In November 2026 there was a further pass. Each of the twenty-two files gained a
"Since 2026-09-30" section, read against live sources on 2026-11-08, and fifteen
products not researched before got a file each in the same nine parts: thirty-seven
in all. What the first half found is under "What changed since 2026-09-30", the new
products are in the table, and the gaps both halves name are under "Gaps found in
November 2026".

This page is the summary. The numbers below come from those files, which carry
the sources and the dates they were read.

The three products Skifity is most often compared with — Coolify, Dokploy and
Kubero — were then cloned and read line by line against Skifity's own code.
That pass, what it found in Skifity, and a scorecard of all four are in
[source-audit.md](source-audit.md).

## The products

| Group | Product | What it is, in one line | File |
|---|---|---|---|
| Self-hosted, Docker | Coolify | The market leader: Laravel control plane, PostgreSQL, Redis; three minor releases in four weeks to v4.6.0 (2026-11-02), 354 templates | [coolify.md](competitors/coolify.md) |
| | Dokploy | The fast grower: Next.js on Docker Swarm, PostgreSQL; still pre-1.0 (v0.31.0, 2026-11-05), 602 templates | [dokploy.md](competitors/dokploy.md) |
| | CapRover | The oldest dashboard panel: Swarm, nginx, a JSON file for state; 1.16.0 (2026-11-03), 363 one-click apps | [caprover.md](competitors/caprover.md) |
| | Dokku | The original self-hosted Heroku over SSH; has shipped a **k3s scheduler** in core since 2024 | [dokku.md](competitors/dokku.md) |
| | Easypanel | Commercial, closed-source; Swarm, SQLite state, telemetry on by default that reports every change by name | [easypanel.md](competitors/easypanel.md) |
| | Kamal | 37signals' deploy tool: no control plane, a YAML file and kamal-proxy; no release since 2.12.0 (2026-06-18) | [kamal.md](competitors/kamal.md) |
| | Openship | A control plane on a laptop, a server or its own cloud; Docker by default, k3s across SSH servers since 0.8.0; 0.9.11 (2026-11-06) | [openship.md](competitors/openship.md) |
| | Temps | One Rust binary on Docker with error tracking, analytics, uptime monitors and status pages inside; every v0.1.0 build a prerelease | [temps.md](competitors/temps.md) |
| | HivePaaS | Docker Swarm with a feature list close to Skifity's; open core with node and user limits; in beta since 2026-10-03 | [hivepaas.md](competitors/hivepaas.md) |
| | Levelrail | Skifity's shape on Docker: one Go control plane, a CLI and an MCP server; v0.9.3 (2026-11-08), a release every 1.4 days | [levelrail.md](competitors/levelrail.md) |
| | Uncloud | Docker Compose across several machines with no control plane: a WireGuard mesh in about 150 MB a machine; v0.22.0 (2026-11-06) | [uncloud.md](competitors/uncloud.md) |
| Other people's apps | Cloudron | Packaged apps with maintained updates, backups before every update, SSO and mail; now writes some of its apps itself | [cloudron.md](competitors/cloudron.md) |
| | Portainer | Container and Kubernetes UI; 3.0 (2026-10-29) is "Kubernetes-first" and Business Edition only, and CE 2.45 is the last Community Edition | [portainer.md](competitors/portainer.md) |
| | Olares | A personal cloud OS on k3s for apps and local models; a vendor Olares ID to activate it; 4 cores, 8 GB, a 150 GB SSD | [olares.md](competitors/olares.md) |
| Self-hosted, k3s on your server | QuickStack | Skifity's pitch: one command puts k3s, Longhorn and a panel on a server; GPL-3.0, 1.0.1 (2026-10-26) | [quickstack.md](competitors/quickstack.md) |
| | Hakopod | "Kubernetes underneath, no YAML on top": K3s on one dedicated 4 GiB server; open core, 82 alphas since 2026-09-13 | [hakopod.md](competitors/hakopod.md) |
| | Sailbox | k3s with a panel beside it in Docker, Kubernetes shown rather than hidden; nothing since v0.1.8 (2026-09-13) | [sailbox.md](competitors/sailbox.md) |
| | Zeabur (open source) | Hosted Zeabur's rewrite, published as the service closes; keeps no state and acts with the user's own Kubernetes credential; v0.2.0 (2026-11-08) | [zeabur-oss.md](competitors/zeabur-oss.md) |
| Self-hosted, Kubernetes | Kubero | Heroku-style operator on your cluster; dormant since v3.1.1 (2025-09-17), with an unpatched critical CVE | [kubero.md](competitors/kubero.md) |
| | Canine | "Coolify for Kubernetes", in its author's words; Rails, outside the cluster, still never released | [canine.md](competitors/canine.md) |
| | Epinio | `cf push` for Kubernetes; stalled at SUSE, revived by Krumware in 2025; v1.14.3 (2026-10-16), still two people | [epinio.md](competitors/epinio.md) |
| | Sealos | A Kubernetes installer grown into a "cloud OS"; relicensed in 2025, 8 CPU / 16 GB per node; no stable release since 2025-11-17 | [sealos.md](competitors/sealos.md) |
| | Rainbond | An application platform since 2017, "No Kubernetes expertise required"; China first, alerts, audit and app backups in a paid edition | [rainbond.md](competitors/rainbond.md) |
| | Mortise | A Railway-style operator for any cluster, with no database of its own; a one-line k3s install; no MCP, SSO or second factor | [mortise.md](competitors/mortise.md) |
| | Lucity | The Railway mould on a cluster of three or more, with an Eject to a Helm chart; a hosted Lucity Cloud; one author | [lucity.md](competitors/lucity.md) |
| Kubernetes on your cloud | Porter | Operates EKS/GKE/AKS in your account; no longer open source; no changelog entry since 2026-06-12 | [porter.md](competitors/porter.md) |
| | Qovery | Internal developer platform on your cloud; dropped its single-server k3s offer in 2024, now has a Free plan on a cluster you run, k3s included | [qovery.md](competitors/qovery.md) |
| | Northflank | Kubernetes underneath, hosted or BYOC; environments, workflows, preview blueprints; secrets decrypted in a breach on 2026-10-30 | [northflank.md](competitors/northflank.md) |
| | Edka | A hosted control plane that runs k3s in your Hetzner or AWS account, and since 2026-08-12 on one server of yours | [edka.md](competitors/edka.md) |
| Managed | Vercel | The developer-experience bar: previews per pull request, instant rollback (30 days by default for new Pro teams); Daemons since 2026-10-15 | [vercel.md](competitors/vercel.md) |
| | Netlify | Deploy previews and atomic deploys; deploy contexts per branch | [netlify.md](competitors/netlify.md) |
| | Railway | Canvas and reference variables; no built-in horizontal autoscaler; a breach from 2026-10-20 to 22 | [railway.md](competitors/railway.md) |
| | Render | Service types and Blueprints (`render.yaml`), now planned and approved; its own Kubernetes on AWS | [render.md](competitors/render.md) |
| | Heroku | Defined the category; in "sustaining engineering" since 2026-02-06, though Postgres Advanced shipped on 2026-10-06 | [heroku.md](competitors/heroku.md) |
| | Fly.io | Firecracker Machines and flyctl; recurring reliability complaints; prices up on 2026-10-01, the focus moved to agents | [fly-io.md](competitors/fly-io.md) |
| | DigitalOcean App Platform | An app spec as the source of truth; still no volumes after five years | [digitalocean-app-platform.md](competitors/digitalocean-app-platform.md) |
| | bex | An open-source Render clone that reads `render.yaml`; a hosted alpha, and self-hosting takes Cluster API and six machines | [bex.md](competitors/bex.md) |

aaPanel was researched on 2026-09-17 and left out: it replaces cPanel, not a
Git-deploy PaaS. Komodo, Stackdome and Grua came up in the November sources as
alternatives and were not researched ([coolify.md](competitors/coolify.md),
[railway.md](competitors/railway.md), [portainer.md](competitors/portainer.md)).

## What changed since 2026-09-30

From the "Since 2026-09-30" sections and the fifteen new files, all read on
2026-11-08. What follows is what matters for Skifity; each file has the rest.

### Releases that closed a gap, and releases that opened one

* **What was Skifity's alone is now shared.** [Coolify](competitors/coolify.md)
  v4.5.1 (2026-10-27) skips a Compose build when the build inputs match, "so
  runtime-only changes no longer rebuild", and v4.6.0 (2026-11-02) limits members
  and API tokens to chosen projects, environments or single resources, further than
  Skifity's project limits. Counting what is merged and not yet released, Coolify
  also has secret managers, passkeys, team-required 2FA, image scanning, SSH
  host-key pinning, a backup before an upgrade and an MCP server that changes
  things. [Vercel](competitors/vercel.md)'s Daemons (public beta, 2026-10-15) run
  long-lived workers; [HivePaaS](competitors/hivepaas.md) has an autoscaler on
  requests and CPU; [Temps](competitors/temps.md),
  [Hakopod](competitors/hakopod.md) and [Levelrail](competitors/levelrail.md) scale
  to zero; image scanning arrived in [Portainer](competitors/portainer.md) 3.0.0
  (2026-10-29) and [Lucity](competitors/lucity.md) v26.9.1 (2026-09-21).
* **Rebuilding without the cache.** [Dokku](competitors/dokku.md) 0.38.34
  (2026-10-30) added `ps:rebuild --no-cache`, and
  [Easypanel](competitors/easypanel.md) 2.38.0 (2026-10-22) a rebuild without the
  Docker cache "to … pick up PHP security updates". Skifity's locale files carry
  "Rebuild from scratch" and no component uses it.
* **Newer database versions, and recovery to a moment.**
  [Railway](competitors/railway.md) upgrades a PostgreSQL major version in one click
  (2026-09-11) and [Qovery](competitors/qovery.md) with `blueprint upgrade-major`
  (2026-10-22). Point-in-time recovery shipped in Lucity (v26.9.1),
  [HivePaaS](competitors/hivepaas.md) (beta5, 2026-10-21) and Railway's MySQL
  (2026-10-01). [Heroku](competitors/heroku.md) made Postgres Advanced generally
  available on 2026-10-06, its largest launch since the sustaining-engineering
  announcement.
* **The servers themselves.** Coolify v4.5.0 (2026-10-23) patches servers with
  automatic security updates (beta), [Dokploy](competitors/dokploy.md) v0.31.0
  (2026-11-05) installs Fail2ban and lists pending OS updates, and
  [Edka](competitors/edka.md) drains, updates and reboots one node at a time (in its
  CLI from 2026-10-15). Skifity's hardening report says what to run and changes
  nothing but the SSH password setting.
* **A login in front of apps.** Railway Authentication (2026-10-08, at the
  $5,000-a-month tier), [Netlify](competitors/netlify.md)'s `context.user` for a
  signed-in visitor (2026-10-07), and Temps's sign-in in front of any address
  (2026-10-26). Skifity has one shared password per app.
* **Tokens that belong to the team.** Heroku's Team Authorizations (2026-09-29) and
  Railway's workspace tokens, which an admin can see and revoke (2026-11-05).
* **Logs and metrics out, without a licence.** Dokploy v0.31.0 ships logs and host
  and container metrics to Prometheus, Datadog, CloudWatch, Splunk and others;
  [Northflank](competitors/northflank.md) forwards audit events to S3.
* **Plan before apply.** [Render](competitors/render.md)'s Blueprints gained plan
  and approve in the CLI (v2.35.0, 2026-11-02) and the MCP server (2026-11-04), the
  shape of `skifity plan` and `skifity apply`; [CapRover](competitors/caprover.md)
  1.16.0 (2026-11-03) has an installer that checks the server and, with `--check`,
  changes nothing.
* **The October patching month.** PHP2Shell (CVE-2026-109618 and CVE-2026-109619),
  the eMudhra CA compromise, a Node.js Brotli flaw (CVE-2026-111497) and a Linux
  kernel remote code execution (CVE-2026-98432) came in the second half of October.
  The hosted platforms patched their customers: Vercel what it manages, with WAF
  rules in front of the rest; Heroku by moving apps onto patched base images with no
  redeploy; Railway by redeploying services with a known CVE on a patched image
  (2026-10-30); [Cloudron](competitors/cloudron.md) by moving about 175 packages in
  two days. [DigitalOcean](competitors/digitalocean-app-platform.md) left apps built
  from a Dockerfile to their owners. On Skifity the runtime is a layer of each app's
  image and the kernel is the operator's: the scans say so, and nothing rebuilds.

### MCP and agents

* **OAuth for MCP became usual.** [Epinio](competitors/epinio.md) 1.14.3
  (2026-10-16) answers an unauthenticated call with protected-resource metadata and
  signs clients in through Dex with PKCE; Vercel accepts any client that publishes a
  Client ID Metadata Document (2026-11-02); [Fly.io](competitors/fly-io.md)'s hosted
  server (beta, 2026-10-23) has the person choose at consent which apps an agent may
  see; Hakopod added OAuth on top of its CLI's browser consent (alpha.82,
  2026-11-07); [Openship](competitors/openship.md), [bex](competitors/bex.md) and
  Edka sign clients in the same way. Skifity's `/api/mcp` takes a pasted bearer
  token. Northflank's hosted endpoint is the exception: a team API token and no
  OAuth (CLI 0.15.0, 2026-10-27).
* **Setting up an assistant became one command.** Northflank's `nf agent setup`
  (CLI 0.14.0, 2026-10-23), Fly.io's `fly setup agent` (v0.5.2, 2026-10-30), Temps's
  `mcp add`, Edka's `edka skill install`, the twelve skills
  [Olares](competitors/olares.md) compiles into its CLI,
  [Rainbond](competitors/rainbond.md)'s RainSkills (3,935 npm downloads in the 30
  days to 2026-11-07), and Levelrail's `init`, which writes an `AGENTS.md` and an MCP
  entry.
* **Agent writes held for a person.** Portainer deprecated its MCP server
  (2026-10-23) for Portainer-Command, where an agent reads through an expiring
  read-only session and proposes changes as pull requests that a named person
  approves. Hakopod's agent policies hold or block a key's requests (Pro,
  2026-11-06, not run on a cluster); HivePaaS's MCP server plans every change first
  and never deletes; Rainbond's assistant asks before every high-risk action.
* **Tool lists sized to the token.** Levelrail lists about 2,400 tokens of its 259
  tools by default and shows a read token no deploy tool. Measured the same way on
  2026-11-08, Skifity's 46 tools cost about 16,700 tokens in every session, and a
  read-only token is shown `deploy_app`.
* **Secrets in an assistant's context.** Lucity's MCP server masks secrets in logs
  (v26.9.2, 2026-09-29); [QuickStack](competitors/quickstack.md)'s returned them in
  plain text from 2026-10-03 until 2026-11-08; [Canine](competitors/canine.md)'s
  handed any cluster's kubeconfig to members whom teams restricted, until
  2026-10-16; [Porter](competitors/porter.md)'s documentation warns that a secret
  passed to a tool stays in the transcript. Skifity's tools return no secret, but its
  log and command tools return whatever the app printed.
* **Agents as the customer.** Fly.io wrote that "agents have overtaken humans as our
  most common customer species" (2026-10-23); Vercel counts agent-triggered
  deployments up 28 times in a year; DigitalOcean's releases in the period were for
  agents. Sandboxes for agents shipped at Render (public beta, 2026-11-04), Railway
  (2026-09-17), DigitalOcean (MicroVMs, 2026-10-09) and Porter, and Canine added agent
  computers (2026-10-18).

### Licence and business

* **Hosted Zeabur closes.** After two breaches its founder announced on 2026-10-17
  that purchases stop at once, shared-cluster services pause from 2026-12-01 and
  everything stops on 2026-12-31. The rewrite was published under Apache-2.0 on
  2026-10-28 ([zeabur-oss.md](competitors/zeabur-oss.md)).
* **Portainer's Community Edition ends at 2.45.** 3.0 is Business Edition only, and
  CE 2.45 gets fixes until August 2027 (Portainer's post of 2026-11-06).
* **Sealos took apps out of its public repository**: the App Store (2026-10-08), App
  Launchpad and the Database frontend (2026-10-14), while its homepage still says
  "Source-available core 100%" ([sealos.md](competitors/sealos.md)).
* **Prices.** Fly.io raised Machine prices on 2026-10-01 (13% for a shared vCPU, 20%
  for memory) and cut the trial to $1 of credit; Vercel bills deployment storage,
  keeps 30 days of deployments for new Pro teams (2026-10-09), and has started to
  bill Runtime Cache; Qovery has five plans, from Free on a cluster you already run to
  Scale at $2,999 a month; Netlify's pricing page shows different visitors different
  prices; Temps sells its Premium edition at $399 per organisation a month.
* **Open core with limits.** HivePaaS's free plan stops at two nodes and two users;
  Hakopod's Pro ($99 a year) adds teams, social sign-in and audit history; Rainbond
  keeps alerts, audit and application backup for an edition with no published price;
  Edka puts SSO in its €500-a-month plan.
* **The hosted control plane, built.** Edka is the third option under "Renting the
  panel out" below — a hosted control plane with an agent that dials out — and its
  satellites show the cost that section predicts: a Kubernetes API open to the
  internet.
* **Smaller.** DigitalOcean ended its Open Source Credits programme (2026-10-05);
  Railway paused new signups after its breach (still paused on 2026-10-26).

### Died, dormant or quiet

* [Kubero](competitors/kubero.md): no release since v3.1.1 (2025-09-17), its server
  code unchanged since but for a lockfile, and CVE-2026-92720 still unfixed on
  2026-11-08.
* Canine: still no tag and no release, and nothing landed between 2026-10-24 and
  2026-11-08.
* Sealos: no stable release for 356 days on 2026-11-08 (v5.1.1, 2025-11-17).
* [Kamal](competitors/kamal.md): no release since 2.12.0 (2026-06-18); kamal-proxy
  v0.10.2 (2026-10-06) is not what a default install runs.
* [Sailbox](competitors/sailbox.md): nothing after v0.1.8 (2026-09-13), which itself
  came after five months without a commit.
* Porter: no changelog entry since 2026-06-12 and no blog post since 2026-01-27;
  three CLI releases without notes.
* Fly.io changed chief executive on 2026-07-24 to put agents first, deprecated Fly
  Kubernetes on 2026-10-15 and removed its documentation on 2026-10-30.
* Heroku: Fir's list of features "To Be Added" has not changed since 2026-09-11.
* DigitalOcean App Platform: one dated release note in the period, a buildpack update
  (2026-10-22), and no blog post; still no volumes.
* Epinio: the 1.15 its support matrix promised for the third quarter of 2026 has no
  tag.
* Hosted Zeabur, above, stops on 2026-12-31.

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
| Coolify | 76 CVEs in NVD (CVE-2026-121587 added on 2026-11-03) and 70 GitHub advisories on 2026-09-30; PHP2Shell (CVE-2026-109618, CVE-2026-109619) left every unpatched v4 dashboard exposed on 2026-10-21 | 36 command injection, 14 missing authorization, 10 cross-team; an OAuth sign-in matching accounts by email (CVE-2026-86117); a webhook lookup that listed every team's app names |
| Dokploy | 60 advisories, 34 critical, on 2026-09-30; two more CVEs since, CVE-2026-111331 (2026-10-15) and CVE-2026-116068 (2026-10-25, CVSS 4.0 8.7); 59 CVE records in CIRCL | Command injection, cross-organisation access, a hardcoded auth secret |
| Dokku | 6 advisories, 5 critical (CVSS 9.0); none new by 2026-11-08 | Input reaching Bash `eval`, heredocs and `tar` on the host |
| Portainer | 11 CVEs in 2026, none after 2026-09-30; a GitOps bypass to host root in CE 2.45.0 (written up 2026-10-09) and three more ways round the bind-mount setting, fixed in 2.45.2 and 2.45.3 without a CVE | Authorization bypasses in its Docker/Kubernetes API proxy; settings that hide rather than block |
| Kubero | CVE-2026-92720 (9.1), still unpatched on 2026-11-08 | Unauthenticated API returning webhook secrets |
| Canine | A cross-account cluster lookup, fixed silently 2026-09-18; a second one, and an MCP server that ignored teams, fixed 2026-10-16; owner sign-in without a password, and PostgreSQL published with the password `password`, in the self-hosted Compose file from 2025-09-20 to 2026-10-19 | Tenant isolation; insecure defaults |
| Vercel | A platform breach, 2026-04-19; none since; 57 valid reports in its Sandbox challenge (2026-10-14) | Environment variables not marked sensitive were decrypted |
| Railway | A breach, 2026-10-20 to 22: a subset of variables decrypted in "at most 1.85% of all workspaces", about 100,000; every Railway token revoked | A hosted platform holding every customer's secrets |
| Northflank | A breach, 2026-10-30: "Assume all environment variables have been decrypted"; no root cause published by 2026-11-08 | The same |
| Zeabur (hosted) | Two breaches, 2026-08-27 and 2026-10-14/15: 3,218,965 environment variable records and 321,810 accounts; the service closes on 2026-12-31 | The same, with admin tokens that never expired, kept in plain text |
| Fly.io | PwnEscape (CVE-2026-99999, 2026-10-15), patched by rebooting every host; a Firecracker escape (CVE-2026-122192, 2026-11-04, CVSS 7.5) | Isolation between tenants on shared hosts |
| Kamal | Two kamal-proxy advisories, a path-based routing bypass (GHSA-g5gj-723m-2f9x) and no client timeouts (GHSA-g8jf-9mcc-x4f2, quoted at CVSS 7.5), fixed in v0.10.1 and v0.10.2; a default Kamal install still runs v0.9.2 | A routing bypass; slow clients |
| Cloudron | CVE-2026-79363 (2026-10-09, CVSS 3.1 5.9); a user's OIDC token accepted as their mail password, fixed but not released on 2026-11-08 | Stored XSS from an admin setting; token scope |
| Easypanel | No CVE; 2.37.0 (2026-10-14) stopped Traefik trusting any visitor's forwarded headers, on new installs only and without saying so | Trusted headers |
| Sealos | No new CVE; a privilege escalation through a User named like a platform ServiceAccount, fixed 2026-11-02 without an advisory | A name that spells a platform identity |
| Qovery | No CVE; until 2026-11-04 secret values were written to audit logs and to stored job output | Secrets in logs |
| Rainbond | CVE-2026-72741 (2026-08-13, CVSS 4.0 8.6), fixed in v6.9.8; an unauthenticated file listener on port 6060, reported in public on 2026-10-22 and still in the code on 2026-11-08 | Cross-tenant access; a second router without the token check |
| QuickStack | No CVE; a pod terminal with no authentication from 2024-11-27 until 1.0.1 (2026-10-26), behind a cluster-admin ServiceAccount | Missing authentication |
| Sailbox | No CVE; every signed-in user an administrator for 169 days, from v0.1.0 (2026-03-28) to v0.1.8, fixed in a commit titled for database limits; its registry still open on a NodePort | Missing authorization; an open registry |
| Temps | No CVE; an assessment of 2026-10-20 with 33 findings, 1 critical and 2 high, and twelve more on 2026-10-22, fixed in public issues | Cross-project reads |
| Levelrail | Its own list, LRSA-2026-0001 to -0013, all found by internal audit before 0.9; three High | Ids in request bodies not authorized; one rate-limit budget for every visitor |
| HivePaaS | No CVE; a Dockerfile path traversal (2026-10-16) and SSO matching unverified email addresses (2026-10-17), fixed in beta5 under "Bug fixes" | Path traversal; email matching |
| Mortise | No CVE; an unauthenticated, privileged BuildKit and a writable registry with no NetworkPolicy, an open issue since 2026-07-05 | Build infrastructure open to tenants |
| Lucity | Sixteen public reports on 2026-03-18, a cross-tenant takeover chain among them, closed the next day without advisories | Tenant isolation in builds and internal services |
| bex | 22 internal review records, with 33 findings rated high or critical, four of them rejected | Tenant identity by name; operations and supply chain |
| Olares | No CVE; fixes to who may set the identity header apps trust, the latest "High Risk" (2026-10-31), in no stable release on 2026-11-08 | Identity in a header |
| Openship | No CVE; 0.9.9 called a "Critical security update" (2026-11-03); the Docker socket in the API container is an open issue | Host root through the control plane |
| Uncloud | No advisory; an embedded registry that takes writes from inside the mesh, read from a TODO in its code | An open registry |

Nothing was found against Hakopod, Edka or Zeabur's open-source code, nor against
CapRover, Epinio, Porter, Render, Heroku, Netlify or DigitalOcean since 2026-09-30.
Each file says where it looked; several could not read GitHub's advisory pages.

The pattern is the one the 2026-09-17 page named: user input reaching a shell in
many places, and authorization checked in many places. Skifity has one quoting
function tested against a real `sh` (`internal/shellsafe`) and one authorization
layer a route walk proves every route goes through. That is a reason to expect
fewer of the first two classes. It is not evidence: nobody outside has looked,
and this pass found three security defects in Skifity's own code by reading it,
listed below.

The November pass adds a third class: a hosted platform that holds every
customer's secrets, which Railway, Northflank and hosted Zeabur showed within one
month ([railway.md](competitors/railway.md), [northflank.md](competitors/northflank.md),
[zeabur-oss.md](competitors/zeabur-oss.md)). It also shows how often a fix arrives
with no advisory at all — Sailbox, HivePaaS, QuickStack, Sealos, Olares and
Portainer each shipped such fixes in 2026 — so "no CVE" says little about a product
here. What the November files found in Skifity's own code is in the rows of "Gaps
found in November 2026".

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

## The gaps, ranked, and the phase that closed each

Ranked by how many of the twenty-two products win on it and how much a person
loses without it. "Without a cluster" says whether it can be built and proven
here, or needs `make verify` on a real server. All twenty are closed; each
phase in `docs/progress.md` says what was tested and, plainly, what could not
be run without a cluster.

| # | Gap | Cited by | Size | Without a cluster |
|---|---|---|---|---|
| 1 | **Closed in Phases 95 and 99.** Usage history and charts for apps and servers, the disk included; memory, CPU, restart and disk alerts | Coolify, Dokploy, Railway, Render, Heroku, Northflank, Vercel, Netlify, Canine, Kubero, Easypanel, Cloudron | M | The storage, API and charts; real numbers need one |
| 2 | **Closed in Phase 84.** Scheduled off-site backup of the panel's own database, and a restore command | Coolify, Dokploy, CapRover, Cloudron, Portainer | S–M | Yes |
| 3 | **Closed in Phases 85 and 86.** A read-only role, and members limited to projects | Coolify, Fly, Portainer, Dokploy, Easypanel, Kubero, Canine, Netlify | M | Yes |
| 4 | **Closed in Phase 87.** A remote MCP endpoint over HTTP, and read-only/destructive annotations on the tools | Coolify, Canine, Vercel, Netlify, Dokploy, Easypanel, Portainer | M | Yes |
| 5 | **Closed in Phase 108.** The environment described in a file in the repository (`skifity.yaml`), with plan and apply | Render, DigitalOcean, Porter, Railway, Portainer | L | Yes |
| 6 | **Closed in Phase 101.** A Compose file deployed as one stack of apps, reaching each other by name and port, with internal apps | Coolify, CapRover, Sealos, Render | M | Yes, except running it |
| 7 | **Closed in Phase 103.** Promote the exact image from one environment to the next | Heroku, Northflank, Vercel, Render | M | Mostly |
| 8 | **Closed in Phases 93 and 107.** Preview-only values; previews of the whole environment, with a seed step | Vercel, Netlify, Railway, Render, Coolify | M | Yes |
| 9 | **Closed in Phase 100.** A Procfile's `web` and `release` lines, its other lines named, and `app.json`'s add-ons and settings | Heroku | M | Yes |
| 10 | **Closed in Phase 106.** Several processes (web, worker) from one build; service types in the new-app form | Fly, Dokku, Render | M–L | Manifests yes |
| 11 | **Closed in Phase 105.** `skifity db connect`: reach a private database from a laptop | Coolify, Fly, Sealos, Epinio | M | Partly |
| 12 | **Closed in Phase 109.** Upgrade what the installer installed: components and k3s | Kubero, Epinio | L | The plan yes; the upgrade needs one |
| 13 | **Closed in Phase 102.** Track which template an app came from, offer its updates, back up first | Cloudron | M | Yes |
| 14 | **Closed in Phase 88.** More notification channels built in: Slack, Mattermost, ntfy, Pushover | Coolify, Dokploy | S | Yes |
| 15 | **Closed in Phases 91 and 92.** SSO groups mapped to roles, and 2FA or SSO required per team | Portainer, Epinio, Dokploy | S–M | Yes |
| 16 | **Closed in Phase 104.** Encrypted, verifiable backups | Cloudron, Dokploy, Dokku | M | The crypto yes |
| 17 | **Closed in Phase 89.** Set several variables in one rollout, and import a `.env` | Dokku | S | Yes |
| 18 | **Closed in Phases 96 and 97.** Watch paths, so a push deploys only the apps it touched; a warning for framework versions with critical advisories | Vercel, Netlify, Railway, Render, Coolify | S–M | Yes |
| 19 | **Closed in Phases 94 and 98.** Locked deploys; a rollback that shows what it will change; maintenance mode with a page and an allow list | DigitalOcean, Kamal, Easypanel, Netlify | S–M | Yes |
| 20 | **Closed in Phase 90.** `SECURITY.md` and a support and upgrade policy before the first release | Epinio | S | Yes |

## Gaps found in November 2026

The November files name new gaps against Skifity, each checked against its code on
2026-11-08. Where several files name the same thing it is one row, and the rows are
ranked by how many of the thirty-seven products have it and how much a person loses
without it. Sizes are the files' own. `docs/progress.md` records what this branch
built of them.

| # | Gap | Cited by | Size |
|---|---|---|---|
| 1 | **A second database beside the first.** Restore a backup into a new database, or move to a newer major version into one, and switch the app's link once it is checked; download a dump | Openship, Zeabur, Rainbond, Lucity, Hakopod, Edka, Temps, HivePaaS, Fly.io, Qovery, Railway, Cloudron, Levelrail, bex | M; M–L for major versions |
| 2 | **Get fixes into running apps.** Rebuild without the build cache, from the panel as well as the CLI; rebuild the apps a runtime fix needs; keep the pre-build advisory table current; deploy an image by its digest, follow its new versions by a rule with approval, and deliver security fixes for catalogue apps without waiting for a panel release | Dokku, Easypanel, Sailbox, Render, Vercel, DigitalOcean, Netlify, Heroku, Edka, Portainer, Cloudron, Railway, Mortise | S for the rebuild; M for the rest |
| 3 | **Deploy from CI without a person's token.** An image on the deploy call, recorded so a rollback restores it; a token or a hook that can deploy one app and nothing else; a CI page with GitHub Actions and GitLab CI examples; CI that signs in with its own OIDC token | Kamal, Hakopod, bex, Rainbond, Mortise, Temps, Uncloud, HivePaaS, Levelrail, Lucity, Edka, Vercel | S–M; M for OIDC |
| 4 | **Assistants and the CLI sign in through the browser.** `/api/mcp` as an OAuth 2.1 protected resource with a consent page that picks scopes and projects, and a device code for the CLI's login, so no token is pasted | Openship, Hakopod, Epinio, Canine, Vercel, Fly.io, bex, Edka, Sealos, Levelrail, Rainbond | M |
| 5 | **A sign-in in front of an app.** Members of the team pass and the app is told who they are; previews private by default; a stranger's pull request held until somebody who can write approves it | Netlify, Railway, Rainbond, Temps, Olares, Cloudron, Vercel, Northflank, HivePaaS | M–L |
| 6 | **Say when something stops.** A scheduled command's runs and a `job.failed` event; pushes that stop arriving; the public address checked from outside, a status page and a heartbeat out; k3s certificates and etcd snapshots watched; silences for planned work | Dokku, Railway, Sailbox, HivePaaS, Render, Temps, Mortise, Canine, Levelrail, Edka | M |
| 7 | **Change control.** Protected environments where only admins change variables or delete, access set per environment, a second person's approval before production, freezes on a schedule, a stale save refused, edits staged and applied as one rollout | bex, Mortise, Levelrail, Render, Coolify, HivePaaS, Hakopod, Zeabur, Edka | S–M; M for approvals and drafts |
| 8 | **Point-in-time recovery for PostgreSQL**, from CloudNativePG's WAL archive, restored into a new database | bex, Temps, HivePaaS, Lucity, Render, Edka, Levelrail | L |
| 9 | **Keep secret values out of what people and assistants read.** Mask an app's secrets in its logs and a command's output, keep them out of the audit log, show the password `skifity db connect` prints only when asked, and mark text an app wrote as data for an assistant | Hakopod, Lucity, HivePaaS, Qovery, Levelrail, Railway | S–M |
| 10 | **Judge requests as the app sees them.** The firewall decodes a path once before any rule; trusted proxies reach Traefik; the panel and the firewall find the client address the same way; the backup endpoint is dialled through the outbound guard; registry reads are the servers' alone and a replaced registry credential reaches running apps; the route walk fills ids in request bodies | Kamal, Hakopod, Easypanel, Portainer, Lucity, Canine, Levelrail | S each; M for registry reads |
| 11 | **Server upkeep from the panel.** Install OS updates and reboot one server at a time, inside a window, the panel's own server last; Fail2ban | Coolify, Openship, Heroku, Edka, Fly.io, Dokploy | M–L |
| 12 | **Domains and certificates.** Wildcards by DNS-01 with no DNS credential in the cluster; a certificate for the server's IP address, so the first sign-in is over HTTPS; a TXT proof or allowed patterns before a hostname routes on a panel with several teams | Openship, Zeabur, Edka, HivePaaS, Uncloud, Coolify, Vercel, Cloudron, Hakopod, Lucity | M; S–M for the proof |
| 13 | **Placement and capacity.** Run an app on chosen servers or one on each; pause and drain a server without removing it; place pods by the image's architecture; say where a server's instances would go and whether a deploy fits; advise on requests from what an app used; a disruption budget that never blocks on an instance already down | Sailbox, Hakopod, HivePaaS, Uncloud, Edka, QuickStack, Openship, Temps, Rainbond, Olares, Kamal, Mortise, Porter | S–M |
| 14 | **Logs kept after the pod is gone**, searchable by time, level and field, with the gaps shown | Hakopod, Lucity, Mortise, Zeabur, HivePaaS, Temps, Levelrail, Edka | M–L |
| 15 | **Say what is wrong, and what will be.** The cause of a failed rollout, build or certificate from what Kubernetes and cert-manager recorded; a crashing version failed in seconds with its last log lines; build memory per app; one diagnose command and a report bundle; a page of what is weak or will hurt later | Zeabur, Uncloud, Qovery, Mortise, Edka, Olares, CapRover, Vercel | S–M |
| 16 | **Install and upgrade safely.** A signed release, installed and upgraded by digest; an installer that can check without changing anything, does not hang under sudo-rs on Ubuntu 26.04, and leaves a result a script can read; an upgrade that checks its target before the panel stops and puts the old one back when the new one does not start | HivePaaS, Levelrail, Mortise, Zeabur, Kamal, Sailbox, CapRover, Hakopod, Temps | S–M; M for the revert |
| 17 | **Accounts and tokens a team manages.** Tokens limited to a project or an app, owned by the team, listed and revoked by its admins, paused and rolled; access that ends on a date; accounts that can be disabled and deleted | Coolify, Openship, Hakopod, Railway, Heroku, QuickStack, HivePaaS, bex | S–M |
| 18 | **GitHub as an App**, made by the manifest flow, with Checks and Deployments, and an automatic deploy that waits for the commit's checks | Openship, Hakopod, Zeabur, Sailbox, HivePaaS, Mortise, Dokploy, Coolify, Levelrail | M |
| 19 | **Moving in and out.** Importers from Coolify, Dokploy, CapRover, Dokku, Kamal and a Kubernetes namespace; converters from `render.yaml`, `fly.toml` and `railway.json`; a page for people leaving hosted Zeabur before 2026-12-31; an environment written out as `skifity.yaml`, as a template another team installs, or as an export another cluster runs | Temps, Openship, Levelrail, bex, Zeabur, Rainbond, HivePaaS, Lucity | M a source, L in all; S for the page |
| 20 | **Backups that are proved and can go elsewhere.** Each verified when it is taken and a restore drilled weekly; a download; more than one destination; SFTP or a disk; incremental; kept by day, week and month | Levelrail, HivePaaS, Dokploy, Coolify, Openship, Olares | S–M; L for incremental |
| 21 | **Requests one by one, and traffic by path, country and referrer**, with client addresses shortened before they are stored | Coolify, Openship, Hakopod, bex, HivePaaS, Edka | M |
| 22 | **Set an assistant up in one command**, with agent skills and a page in the panel that shows its first call; a CLI that never waits at a prompt under a pseudo-terminal and never passes on a terminal escape | Northflank, Fly.io, Temps, Olares, Rainbond, Levelrail, Edka, Sealos, Render | S–M |
| 23 | **Assistants propose, people approve.** Changing MCP tools plan first and apply once; deletes and production deploys held for a person; `skifity.yaml` planned and applied from MCP; a switch for read-only assistants; a tool list cut to what the token may do | Portainer, Hakopod, Rainbond, HivePaaS, Render, QuickStack, Levelrail | S–M; M for approvals |
| 24 | **Send more out.** Host and app metrics to Prometheus or Datadog; CloudWatch and Splunk drains; the audit log forwarded, exported, kept longer and marked with where an action came from; signed, typed webhooks with a delivery record; error tracking wired to an app | Dokploy, Coolify, Northflank, Hakopod, Levelrail, Porter, bex, Railway, Canine, Temps | S–M |
| 25 | **Volumes that grow, move and are shared.** Resize by a verified copy; ReadWriteMany on Longhorn, with its host packages installed and the scaling advice corrected; Hetzner Volumes on Hetzner servers | QuickStack, Hakopod, Sailbox, Openship, Edka, Rainbond | S–L |
| 26 | **Network rules from what the panel knows, and a mesh it can see.** A database reachable only by the apps linked to it; outbound rules per app; a public port for some addresses only; a way to the LAN or an NFS share; servers whose public address is not on their interface; each server's WireGuard peers shown | QuickStack, Porter, Zeabur, Olares, Uncloud, Openship | M |
| 27 | **Templates that say what they need and risk.** Typed inputs with choices; resources checked before install; architectures recorded; risks with mitigations; no default on a secret input; an install that removes what it made when it fails; a record of which templates have run | Olares, Zeabur, Hakopod, Levelrail, CapRover, Kubero | S–M |
| 28 | **Look inside a database.** A table browser, a read-only query runner, metrics and the slowest queries, through the panel rather than on a public domain | Lucity, QuickStack, Temps, Levelrail, Edka | M |
| 29 | **Watch a release after it takes traffic**, and roll it back on restarts or 5xx; weighted and header-matched routing for a canary | Levelrail, Rainbond, Vercel, Northflank, Kamal | M |
| 30 | **The cluster's own state off the server.** etcd snapshots, the k3s token and CA copied to the bucket and sealed; a restore command; the panel brought back on a surviving control-plane server | Zeabur, Edka, Uncloud | M |

Gaps that only one or two files name — sharing a GPU between apps (Olares), an
offline install (Rainbond), more panel languages (Openship, Zeabur) among them — are
left in their files.

## Where Skifity is actually different

Ordered by how well the evidence supports it, after the November pass.

1. **It installs the cluster, on 2 GB.** The September wording, "None of them
   puts Kubernetes on one small server with one command", does not survive the
   November files. [QuickStack](competitors/quickstack.md) has done it since
   2025-01-19 (k3s and Longhorn, 2 CPUs and 4 GB); [Sailbox](competitors/sailbox.md)
   since 2026-03-28 on 2 CPUs and 2 GB, with its panel in Docker beside the cluster
   and, read from its code, added servers that cannot join in v0.1.8;
   [Mortise](competitors/mortise.md)'s one-line installer on a 2 GB minimum;
   [Hakopod](competitors/hakopod.md) on a dedicated 4 GiB server;
   [Zeabur](competitors/zeabur-oss.md)'s installer at 4 GiB for a team;
   [Rainbond](competitors/rainbond.md) in a privileged container from 4 GB;
   [Olares](competitors/olares.md) from 8 GB; [Openship](competitors/openship.md) as a
   second step after its Docker install, unproven on production hosts by its own
   account; and [Edka](competitors/edka.md)'s satellite on 2 GB since 2026-08-12,
   run from Edka's hosted service. What is still Skifity's is narrower: a
   self-hosted panel inside the cluster it manages, on 2 GB — measured now, k3s takes
   748–780 MiB and the whole install about 1,000 MiB
   ([dokku.md](competitors/dokku.md), [levelrail.md](competitors/levelrail.md)) — with
   embedded etcd from the first install, servers added, promoted and removed over
   SSH, WireGuard between them, teams and sealed secrets. It is no longer "Written,
   never run": the `Cluster` workflow installs every push to `main` on a real k3s
   ([kubero.md](competitors/kubero.md), [epinio.md](competitors/epinio.md)). The last
   line of "Things deliberately not copied" calls this "the thing nobody else does",
   which no longer holds as written; [zeabur-oss.md](competitors/zeabur-oss.md) argues
   that the reason given there has lapsed, and [mortise.md](competitors/mortise.md)
   keeps it. And [Uncloud](competitors/uncloud.md) gets
   several servers, WireGuard and rolling deploys in about 150 MB a machine with no
   Kubernetes at all; what k3s buys is rescheduling, autoscaling and isolation between
   teams.
2. **Scaling is the system's, not a runbook — and no longer only Skifity's.**
   Coolify v4.5.0 refuses Compose `replicas` above one and still promises replicas for
   v5; [Temps](competitors/temps.md) "does not auto-scale"; Uncloud has no
   rescheduling by design; QuickStack and Mortise have neither an autoscaler nor scale
   to zero. But Hakopod, Rainbond, Sailbox, Lucity, Edka and [bex](competitors/bex.md)
   have an autoscaler on CPU or memory, [HivePaaS](competitors/hivepaas.md) wrote its
   own on requests and CPU and warns about apps that write to a volume, and Temps,
   Hakopod and [Levelrail](competitors/levelrail.md) scale to zero. What is left is the
   readiness checker that names the setting that breaks with several instances, and
   all three in one panel on a small server.
3. **One place where input becomes a shell command, one place for
   authorization.** See the security table above. A structural reason to expect
   fewer of the category's commonest CVEs, and not yet a demonstrated one. The
   November files add a warning: Levelrail's two worst advisories and both of
   [Canine](competitors/canine.md)'s cross-tenant bugs were ids taken from a request
   body or its parameters and never authorized, and Skifity's route walk sends every
   route an empty body, so such an id is checked by its handler alone.
4. **Secrets that stay secret, in the panel.** Write-only variables sealed to where
   they are stored — the design Vercel moved to in August 2026 after its breach — MCP
   tools that never return one, and build variables that never touch a Job. Narrower
   than it reads: an app that prints a secret shows it to anybody who can read its
   logs, viewers and assistants included, where Hakopod, HivePaaS and
   [Lucity](competitors/lucity.md) mask it. The October breaches at Railway,
   Northflank and hosted Zeabur, each a platform holding every customer's keys, are
   the case for one organisation's panel holding one organisation's secrets; the panel
   still decrypts each into its app's Secret, so a compromised panel exposes its
   team's, and the blast radius is one installation, not none
   ([northflank.md](competitors/northflank.md), [railway.md](competitors/railway.md)).
5. **Hiding Kubernetes well is still the bet, and others make it.** Hakopod's
   "Kubernetes underneath, no YAML on top" is the closest pitch; Rainbond has made it
   since 2017 and, since v6.7.0, also shows raw YAML and Helm releases; Olares makes it
   for households, with Control Hub as its Advanced; Portainer's Kubernetes-first 3.0
   shipped on 2026-10-29. Sailbox bets the other way and shows Kubernetes. On the
   "No Kubernetes" side, Levelrail says so in its first line, and Uncloud is the
   strongest case in these files against choosing Kubernetes at all.

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
* A vendor in the path of a server the user owns: an account to activate it, DNS
  delegated to the vendor, a relay, a hosted DNS name, or certificates that renew
  only while the vendor answers (Olares, Uncloud, Edka).
* Certificates for a shared name such as sslip.io, quickstack.me or uncld.dev. None
  of them is on the Public Suffix List, so every install shares one Let's Encrypt
  limit (QuickStack, Temps, Uncloud).
* Secrets that can be read back, behind a capability, a switch or the right to edit,
  or sent to a model on request (HivePaaS, Mortise, Lucity, bex, Edka, Zeabur's
  open-source version, QuickStack).
* An assistant inside the panel holding credentials of its own, or AI tools that
  change things without asking (Levelrail's LRSA-2026-0007, Temps, Openship, Lucity).
* A second product inside the panel: agent sandboxes, coding-agent harnesses, AI
  gateways, functions, error tracking and analytics, a mail server, model serving or
  virtual machines (Temps, Openship, QuickStack, Levelrail, HivePaaS, Rainbond,
  Canine, Porter).
* The Docker socket, or root on every host, in the control plane's hands (Openship,
  Sailbox, HivePaaS, Levelrail).
* Build infrastructure open to tenants: a registry anybody inside can write to, or
  one shared BuildKit for everybody (Mortise, Sailbox, Uncloud, Lucity).
* Kubernetes or proxy configuration as free text in the panel: raw YAML, Helm
  releases, a Traefik editor, privileged mode as an app setting, a Caddyfile per app
  (Rainbond, Sailbox, QuickStack, Olares, Uncloud).
* Security fixes without an advisory: inside a feature commit, under "Bug fixes", or
  as a public issue fixed overnight (Sailbox, HivePaaS, Lucity, Rainbond, Olares).
* Features that are only a form, and claims ahead of the code: a backup schedule
  nothing runs, a rollback that restarts, a CDN, a promotion or an export the code
  does not have, counts that differ between a project's own pages (Sailbox,
  Openship, Lucity, Levelrail, Hakopod).
* Upgrades that turn themselves on, go one minor version at a time, or need every
  machine at once (Levelrail, Olares, Uncloud, Hakopod).
* Stopping a user's app for them: suspending one that crash-loops, or pausing heavy
  ones under load (bex, Olares).
* Backups, alerts, the firewall, secret managers or a second team behind a licence,
  and limits on users or nodes (Rainbond, Temps, HivePaaS, Hakopod).
* A licence that says less than the headline: "100% open source" over extra
  conditions, two licences in one root, an attribution clause checked by code, a
  catalogue under another licence, apps moved out of the public repository
  (Rainbond, Olares, Sailbox, HivePaaS, Sealos).
* Holding a customer's cloud account and root on their nodes, as a hosted control
  plane does (Edka); "Renting the panel out" below says why.
* CI runners on the production servers, or builds through a workflow written into
  the user's repository (Openship, Hakopod).

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
