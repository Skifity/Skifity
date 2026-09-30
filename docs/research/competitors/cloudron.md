# Cloudron

> **Status.** Written on 2026-09-30 against the code as it was that day. Several gaps and
> defects named here were closed the same day; [the summary](../competitors.md#what-the-research-found-in-skifity-and-what-was-done-about-it)
> lists them with their commits, and `docs/progress.md` has the detail.

Cloudron is a self-hosted platform for running *packaged* open-source web apps
on one server you own, sold by Cloudron UG (Germany) to individuals, families,
non-profits and small businesses who want SaaS-style updates, backups, single
sign-on and email without doing system administration. It is the closest thing
in this category to an app store with a maintainer behind every app.
Architecture: a single Ubuntu 26.04 x64 server (KVM only; "Cloudron does not
support ARM, LXC, Docker, or OpenVZ"; minimum 2 GB RAM and 20 GB disk). The
platform ("box") is Node.js — Express, dockerode, `oidc-provider`, `ldapjs` —
and keeps its own state in **MySQL** (`db-migrate-mysql`, `mysql2` in its
`package.json`); apps are Docker containers with a **read-only root
filesystem**, an AppArmor profile and a non-root user behind an nginx reverse
proxy; shared "addon" services (MySQL 8.4, PostgreSQL 18, MongoDB 8.3, Redis,
a Haraka/Dovecot mail server, TURN, an OIDC provider and an LDAP server) are
provisioned per app with isolated credentials. Licence: **source-available**
("The Cloudron Subscription license": production use requires a subscription;
app packages are open source on git.cloudron.io). Pricing: Free (two apps), Pro
€15/month billed yearly or €30 billed monthly, Max €25/month yearly or €50
monthly (Max adds user groups and roles, directory server, multiple backup
sites, VPN-only app access); "All plans include: app updates, per-app backups,
firewall, email solution and Single Sign-On". A cancelled subscription keeps
running but stops receiving platform and app updates. Maturity: App Store
packages date back to July 2015; 9.0.0 shipped 2025-10-02, 10.0.0 on 2026-08-12
(Ubuntu 26.04), 10.1.0 on 2026-09-28. Adoption: the site says "Thousands of
organizations use Cloudron" and publishes no install count; the App Store has
**196** approved packages; the `platform/box` repository on git.cloudron.io has
33 stars; `cloudron/base` has 648,488 Docker Hub pulls (all as of 2026-09-30).

## Feature inventory

### Deploy sources and builds

* **App Store**: 196 packages, each a Cloudron-specific Docker image built on
  `cloudron/base` plus a `CloudronManifest.json`. Installing asks for a
  location, access control and (for some apps) optional services.
* **Community apps** (9.1.0, 2026-02-25; directory in 10.0.0): any developer can
  publish a `CloudronVersions.json` catalogue at an HTTPS URL; pasting the URL
  installs the app and it "receives updates automatically when the developer
  publishes new versions". Community apps are "not reviewed by Cloudron", and
  10.0.0 raises a notification if a versions URL goes missing.
* **Custom apps** via the `cloudron` CLI: `cloudron install`/`cloudron update`
  upload the source and build on the server from `Dockerfile.cloudron`
  ("source builds", 9.1.0). There is no Git-push deploy, no buildpack
  detection, no preview; a GitHub Action guide covers CI.
* **App proxy**: a Cloudron-managed HTTPS front (DNS, certificate, aliases,
  CSP, up/down notifications) for an app hosted elsewhere; **external links**
  put a bookmark on the dashboard.
* **Install a specific version** with `?version=<package version>` in the App
  Store URL. Package version (semver, drives updates) is separate from
  upstream version (display only).

### Domains, TLS and routing

* 20 DNS providers automated (Bunny, Cloudflare, deSEC, DigitalOcean,
  DNSimple, Gandi, GoDaddy, Google Cloud DNS, Hetzner Cloud, Infomaniak (10.0),
  INWX, Linode, Name.com, Namecheap, Netcup, OVH, Porkbun, Route 53, Vultr)
  plus wildcard, manual and no-op.
* Let's Encrypt (EC certificates, ARI renewal since 9.1.0), wildcard
  certificates to keep app names out of CT logs, custom certificates, CAA,
  HSTS and HSTS preload, "A+ rating from SSL Labs".
* Apps are relocatable at any time without data loss; secondary domains;
  aliases for multi-domain apps (wildcards allowed); 301 redirections; TCP/UDP
  port bindings; custom CSP and `robots.txt` per app; `.well-known` handling.
* Every app gets its own subdomain, never a sub-path, "to prevent XSS
  vulnerabilities in one app from compromising other apps".

### Databases and services

* Addons declared in the manifest: MySQL, PostgreSQL (with extensions incl.
  pgvector), MongoDB, Redis, LDAP, OIDC, proxyAuth, sendmail, recvmail, email,
  scheduler, TLS, TURN, SCIM (9.2.0) and a restricted Docker API. Across the
  App Store: localstorage 192, sendmail 116, **oidc 97**, postgresql 73, redis
  49, mysql 46, scheduler 30, ldap 26, proxyAuth 9 (my count of
  `api.cloudron.io/api/v1/apps`, 2026-09-30).
* One shared server per engine, one isolated database and credential per app.
  Credentials arrive as environment variables that "are subject to change every
  time the app restarts", so packages read them at runtime.
* The web terminal has buttons that open the app's database shells; guides
  cover importing and connecting over an SSH tunnel.

### Storage and backups

* **Per-app backups** of `/app/data` plus a logical dump of each addon database
  — no code, no OS — so each app can be restored, cloned or migrated alone.
* **Backup sites**: one on Pro, several on Max, each with its own provider,
  format, schedule, retention and scope (everything / exclude apps / only some
  apps). About 25 providers: S3, B2, R2, GCS, DO Spaces, Hetzner, Wasabi, IDrive,
  OVH, Scaleway, Synology C2, Vultr, MinIO, CIFS, NFS, SSHFS, EXT4/XFS disks,
  a user-managed mount. 9.1.6 removed the "local disk" provider; a Filesystem
  provider remains, with a warning when it is on the same disk.
* Formats: **tgz** (one tarball) or **rsync** (incremental, remote copies,
  hardlinks on filesystems).
* **Encryption**: AES-256-CBC with an HMAC per file, keys derived by scrypt from
  a password Cloudron does not store; filenames optionally encrypted.
* **Integrity** (9.0/9.1): a `.backupinfo` with SHA-256 per file, signed in the
  database, and a "Check integrity" action that re-downloads and verifies.
* **Retention**: keep-within or N daily/weekly/monthly; the latest backup of an
  installed app is always kept; **a backup taken before an app update is kept
  for three weeks** "because an update broke something and it took you some
  time to figure that out"; labels and "preserve" for backups kept forever.
* **Restore reverts code and data together** — "the current version of the
  app may not be able to handle old data". Clone an app from a backup to a new
  location; import an app backup from another server; **restore or move the
  whole server** from one backup; a **dry-run restore** to a new server driven
  by `/etc/hosts` before switching DNS.
* Packages can declare `backupCommand`/`restoreCommand` and `persistentDirs`
  (9.1.0) for data that needs a logical dump.
* Volumes (NFS, CIFS, SSHFS, disks) can be mounted into apps; mounts are *not*
  backed up, an app data directory moved to a volume *is*.

### Scaling and high availability

* One server. No replicas, no second node, no failover; moving servers is a
  backup-and-restore. Per-app memory limit (package default, admin-adjustable,
  OOM restarts the app and notifies), optional CPU limit, device passthrough,
  unlimited swap. Services start lazily (9.1.4).

### Observability (logs, metrics, alerts, uptime)

* Per-app log viewer (10 MB plus one rotation, 14 days), system and service
  logs, downloadable.
* **Graphs**: live and historical CPU, disk, network and memory per app and for
  the system (the service was renamed from graphite to "metrics" in 9.1.0).
* **Notifications** for: app down / back online, **app ran out of memory**, app
  updated, auto update failed, backup failed, backup config issues,
  certificate renewal failed, domain config check failed, **low disk space**,
  mail status, manual update needed, platform update available/failed, server
  reboot required, Ubuntu update available. In the dashboard and by email only.
* A server health URL (`/api/v1/cloudron/status`) for an external monitor.
* An **event log** of app usage and configuration changes (searchable by IP
  since 10.0.4).

### Security, auth, roles, SSO, audit

* Isolation: read-only rootfs, AppArmor, non-root apps ("only in exceptional
  cases" root), `NET_RAW` dropped, authenticated addon access, per-app
  subdomains, managed iptables, rate limits on every login surface.
* Sign-in: PBKDF2 passwords, TOTP with backup codes (10.0), **mandatory 2FA**,
  **passkeys and passwordless login** (9.1.x), app passwords with expiry and
  last-used time, OIDC Device Authorization Grant.
* **Cloudron is the identity provider**: an OIDC provider and a read-only LDAP
  server for its apps (and, with client credentials or an IP allowlist, for
  outside apps), SCIM for user listing, an external LDAP/AD connector.
  Packages "prefer OIDC over LDAP" because users get true SSO, dashboard-managed
  sessions and 2FA, and "apps never see the user's password".
* **Roles**: User, User manager, Mail manager, Admin, Superadmin; groups;
  per-app access lists ("allow all users" or specific users/groups); per-app
  **operators** who can maintain but not uninstall or move an app; an "App
  access" view that says *why* a user can reach each app (10.0).
* **proxyAuth**: an authentication wall in front of apps that have no login of
  their own, with path include/exclude rules.
* **VPN-only apps** (10.0, Max): an app is reachable only through the installed
  VPN app, independent of the app's own login.
* Signed platform releases (GPG, keys kept offline); Ubuntu unattended security
  upgrades; per-app **security checklists** created by the package
  ("changing default credentials or reviewing registration settings") whose
  completion is recorded with user and date.
* 10.1.0 (2026-09-28) hardening: only hashes of API and login tokens stored,
  admins can no longer set another user's password, invite and reset links
  expire after 24 h, impersonation removed.

### Preview environments and branches

* None; not a developer platform.

### Templates and catalogue

* **196 App Store packages**, every one with a `healthCheckPath` (it is a
  required manifest field); **134** declare a post-install checklist; **126**
  integrate SSO through OIDC, LDAP or proxyAuth, 84 of them optionally.
* **Update cadence**: 138 of the 196 packages published a new version in
  September 2026, 164 since 1 July, 188 in the last twelve months (from the
  `creationDate` of each package's latest version, 2026-09-30). The site
  promises "the latest releases within days and security fixes within 24h".
  When n8n shipped 2.9.3 with SQL-injection and SSO-bypass fixes on 2026-02-25,
  a Cloudron engineer confirmed the package was out the same evening.
* A manifest also carries `minBoxVersion`/`maxBoxVersion`, `memoryLimit`,
  `optionalSso`, `postInstallMessage`, `tcpPorts`/`udpPorts`, `multiDomain`,
  `runtimeDirs`, `persistentDirs` and `capabilities` (only `net_admin`, `mlock`,
  `ping`).

**What Cloudron does about running other people's apps.** This is the whole
product, and it is a chain rather than one feature:

1. *A maintainer per app.* Every App Store package is rebuilt by Cloudron on its
   own base image, so the upstream's Dockerfile, root user and writable
   filesystem are replaced by a known shape (read-only rootfs, `/app/data` the
   only persistent path, non-root).
2. *Declared integration.* The manifest says which databases, mail, auth and
   ports the app needs; the platform provisions them, so nothing is wired by
   hand and nothing is left at a default credential — and the checklist says
   what still has to be done by a person.
3. *Tested, staged updates* delivered through the App Store, with an update
   policy (disabled / apps only / platform and apps) and a schedule of days and
   hours.
4. *A backup before every update*, and "if backup creation fails, the update is
   not applied". The pre-update backup is kept three weeks.
5. *Rollback is restore*: code and data go back together, so a migration that
   ran cannot strand the old version against a new schema.
6. *Notifications when an update fails*, when an app runs out of memory, when
   the disk fills.

The price of that chain is the catalogue size (196 after eleven years) and the
packaging requirement for everything else.

### CLI, API, IaC, integrations

* REST API (275 documented endpoints, API tokens; impersonation tokens for
  admins in 10.0.5), the `cloudron` CLI (install, update, logs, exec, backup,
  versions), AI-agent "skills" for packaging and server operations. MCP servers
  exist only as third-party projects. No Terraform provider.

### Notifications

* Dashboard and per-user email, selectable per event (list above). HTML email
  (9.2.0). No Slack, Discord, Telegram or webhook channel.

### Multi-server and networking

* Single server per licence. IPv4/IPv6 configuration, dynamic DNS, trusted IPs
  and a blocklist, home-server and intranet install guides, VPN app with
  per-app routing options. Another Cloudron can be used as an external LDAP
  directory.

### Team and collaboration

* Users, invitations, groups, roles and per-app operators; a **full mail
  server** (mailboxes, aliases, catch-all, forwarding, shared mailboxes,
  vacation, full-text search, DKIM/SPF/DMARC, ARC, relay tokens) with webmail
  apps; branding; admin notes per app; the dashboard is translated through
  Weblate (`translate.cloudron.io`; Czech added in 9.1.2).

### Developer experience and onboarding

* `cloudron-setup` on a fresh server, then a browser wizard for domain, DNS
  provider and admin; marketplace images on AWS, DigitalOcean, Hostinger,
  Linode, Time4VPS, Vultr; a public demo (`cloudron`/`cloudron`). A domain with
  working DNS is required before the dashboard is usable. **Recovery mode**
  starts an app paused with a writable filesystem so a broken plugin can be
  fixed; a web terminal, a file manager and SFTP (port 222, operators only).

## What users love

* **Day-2 operations are done for you.** "Helper scripts automate day 0.
  Cloudron automates day 2+. Install ≠ operate" (HN, 2025-12-17). "Well worth
  1.00 a day! Handles the entire stack (backups, monitoring, dns, ssl,
  updates)" (HN, 2026-01-11).
* **Updates that do not break things.** "Rock-solid updates. App updates are
  tested and staged. I've never had an update break a running app" (dev.to
  comparison, March 2026 — written by a vendor of a competing managed service,
  so read as an opinion). Security packages arrive within hours (n8n thread,
  forum, 2026-02-25).
* **Portable, restorable servers.** "Pretty rock solid have migrated a complete
  Cloudron installation from one server to another a couple of times without
  issues" (HN, 2026-09-06).
* **Attention to what real apps need.** "Backups, DB management or mail
  sending, but also addressing niche apps' needs like .well-known or custom
  ports, UDP, server certificates available to the app" (HN, 2026-09-07).
* **Polish.** "Cloudron spoiled me so much everything else looks half baked"
  (HN, 2025-04-19); "Best admin UX. No contest" (dev.to, March 2026). Long
  tenure: "Paying customer for 6 years" (HN, 2025-08-09).
* **SSO and email built in**, which every comparison lists as the thing the
  Docker panels lack (dev.to, March 2026; Easypanel's own comparison page:
  "Cloudron wins for all-in-one app hosting").

## What users complain about

* **Price for home use and the two-app free tier.** "Paying 30 bucks per month
  for me is really much … 5 bucks for 5 apps, 10 for 10 apps" (forum "pricing
  too high", 40 posts, 2023); "the free tier limits you to 2 apps — barely
  enough to test a real setup" and "1 server per license" (dev.to, 2026).
  Staff say lower prices would not cover a single support ticket (same thread).
* **The catalogue is small and closed to arbitrary apps.** 196 apps against
  845 (Easypanel) or 280+ (Coolify). Running anything else means packaging it.
  "My biggest pain point … I cannot work with docker compose apps … I need to
  run a second server" (forum, 2024-04-21; the founder answered that a generic
  Compose runner is "quite a different product"). On HN: Cloudron is "'limited'
  in the amounts of apps they can provide with support and quality path for
  upgrades" (2025-05-31).
* **Platform updates occasionally break a server.** 35 forum topics have "update
  broke" in the title across the years; in November 2025 an automatic update to
  9.0.11 left MySQL, PostgreSQL, MongoDB and the metrics service not starting for
  48 hours, and the user restored a disk image and changed host (forum,
  2025-11-23).
* **x86 KVM only, 2 GB minimum, a domain first.** No ARM ("all of them have to
  be repackaged … for each update", staff, forum 2023-09-11), no LXC; wildcard
  DNS "tripped me up and added 15 minutes of troubleshooting" (dev.to, 2026).
* **Not open source.** "Outside of the stated requirements because its not fully
  open source" (HN, 2025-07-19).
* **One server, no HA.** Recurring forum requests to spread apps across
  servers ("Distribute applications on multiple servers?", "Multiple servers,
  one pool") have no product answer.

## Security record

* **NVD**: one CVE, **CVE-2021-40868** — reflected XSS through the login page's
  `returnTo` parameter in Cloudron 6.2, CVSS 6.1, published 2021-09-21.
* **Fixed without a CVE (from the platform changelog)**: 10.0.0 (2026-08-12)
  "db: fix sql injection when some REST APIs are passed arbitrary fields",
  "ui: sanitize HTML and markdown inputs", "applinks: do not allow server
  internal IPs to be added" (an SSRF-shaped fix), "security: fix 2fa
  enforcement with the external ldap connector"; 9.1.0 "security: remove cors";
  10.1.0 token hashing and removal of impersonation and admin-set passwords. I
  found no advisory text or CVE for the SQL injection.
* **App-level response**: the forum shows Cloudron tracking upstream CVEs in
  packaged apps (n8n CVE-2025-68613 on 2025-12-23: the store version was
  already unaffected; n8n 2.9.3 on 2026-02-25: package out the same day;
  Keycloak 26.5.3 for CVE-2026-1609/1529/1486).
* Where I looked (2026-09-30): NVD keyword search "cloudron", the `platform/box`
  CHANGES file, the forum search and security threads, web search. The source is
  available for review but the project is small (33 stars), so a thin CVE list
  mostly means few outside researchers.

## Against Skifity

"Written" = code and unit tests exist, never run against a real cluster
(ADR-0010, `docs/checklist.md`).

| Capability | Cloudron | Skifity (evidence) | Verdict |
|---|---|---|---|
| Deploy your own code | Package + CLI build on server, no Git push | Git push with webhook, image, folder upload, detection (`internal/gitsrc`, `internal/builder`, `internal/api/detect_handlers.go`); Written | Skifity ahead |
| Run an arbitrary upstream image | Only after packaging | Any image; multi-service templates (`internal/api/template_install.go`) | Skifity ahead |
| Rollback | Restore from backup (code + data) | New deployment with old image + settings, no data (`internal/api/deploy_handlers.go` `handleRollback`); Written | Cloudron ahead for stateful apps |
| Config change without rebuild | n/a (packaged images) | Build fingerprint, ADR-0007 | Skifity ahead |
| Domains and TLS | 20 DNS APIs, wildcard, relocation, aliases, redirects, CSP | cert-manager, sslip.io, wildcard domain, DNS token (`internal/settings/settings.go`); Written | Cloudron ahead |
| Managed databases | Shared MySQL/Postgres/Mongo/Redis, per-app isolation | Per-database Postgres (CNPG), MariaDB, Redis instances (`internal/dbsvc/manifests.go`); Written | parity (different shape) |
| App email | sendmail addon in 116/196 apps, full mail server | SMTP only for the panel's own notifications (`internal/settings/settings.go` `email.smtp_*`); 2/282 templates mention SMTP | absent in Skifity |
| Backups: scope | Per app: data + DB dumps together | Per database and per volume, separately (`internal/backup`); never run | Cloudron ahead |
| Backups: destinations | ~25 providers | S3-compatible, presigned URLs (`docs/backups.md`) | behind on breadth, ahead on credential handling |
| Backups: encryption | AES-256 client-side, filenames too | None beyond the bucket's own (`internal/backup` has no encryption step) | absent in Skifity |
| Backups: integrity | SHA-256 manifest, signed, "Check integrity" | None | absent in Skifity |
| Backup before update | Always; update refused if it fails; kept 3 weeks | None (`grep -ri "backup before\|pre-deploy" internal`: nothing) | absent in Skifity |
| Whole-server restore / migrate | One backup restores everything; dry run | `skifity export` (JSON + objects, no data); `skifity admin backup-db` local file (`docs/configuration.md`) | Cloudron ahead |
| Scaling / HA | Single server | k3s multi-node, HPA, scale-to-zero, readiness checker; Written | Skifity ahead |
| Metrics history | Graphs per app and system | Live values only (`internal/api/servers_handlers.go`; no chart in `web/src`) | behind |
| Alerts | App down, OOM, disk, backup, cert, update failed | deploy ok/failed, app unhealthy, server added/lost, backup failed, cert failed (`internal/notify/notify.go`); no OOM, no disk | behind |
| Notification channels | Dashboard + email | Telegram, Discord, webhook, email + plugin kinds (`internal/notify`, ADR-0021) | Skifity ahead |
| Sign-in security | PBKDF2, TOTP, passkeys, mandatory 2FA | Argon2id, lockout, TOTP, recovery keys (`internal/auth`); Works | parity (Cloudron has passkeys) |
| SSO into the panel | Its own directory; external LDAP/AD | OIDC client with PKCE/nonce/state (`internal/auth/oidc.go`); Works | parity |
| SSO into the apps | OIDC provider + LDAP + SCIM, 126/196 apps wired | None; Skifity is not an identity provider | absent in Skifity |
| Auth wall for apps without login | proxyAuth (panel identity) | Basic auth per app, added in `7865bc8` (`internal/kube/password.go`); firewall by IP/country/ASN (`internal/edgerules`, `internal/guard`) | behind (no identity wall) |
| Roles | 5 roles, groups, per-app ACL, per-app operators | owner/admin/member per team (`internal/store/models.go`) | behind |
| Audit | Event log | Team audit log, 90 days, Activity page (`internal/api/api.go`, `web/src/pages/activity.tsx`) | parity |
| App isolation | Read-only rootfs, AppArmor, non-root, per-app subdomain | Namespace per environment, default-deny NetworkPolicy, `restricted` PSA by default, `baseline` opt-in (`internal/kube/namespace.go`, `podsecurity.go`); Written | parity, different layer |
| Root images | Repackaged to non-root | Refused under `restricted`; explained after the fact (`internal/kube/explain.go` `ExplainImageRunsAsRoot`) | Cloudron ahead for catalogue apps |
| Per-app firewall | Trusted IPs/blocklist server-wide; VPN-only apps | Per-app rules over IP, country, ASN (`internal/edgerules`) | Skifity ahead |
| Preview environments | None | Per-PR previews (`internal/api/webhook_handlers.go`); Written | Skifity ahead |
| Catalogue size | 196 | 282 | Skifity ahead |
| Catalogue maintenance | 138/196 updated in Sept 2026 | Versions pinned at import; moved forward by hand (`docs/templates.md` "Versions") | Cloudron ahead |
| Health checks in catalogue | 196/196 | 5/282 declare `health_path`; the rest get a TCP probe (`internal/kube/manifests.go` `probeHandler`) | behind |
| Post-install checklist | 134/196, completion tracked | `notes` on 49/282, shown once (`web/src/pages/templates.tsx`) | behind |
| Update of installed apps | Automatic, scheduled, policy | None; "a template does not update itself" (`docs/templates.md`) | absent in Skifity |
| Third-party catalogues | CloudronVersions.json, auto-updating | Plugin store with signed index exists (`internal/pluginstore`), not for templates | absent in Skifity |
| API / CLI / MCP | REST, CLI; MCP third-party only | REST, CLI with `--json`, MCP in the binary (`internal/mcpserver`); Works | Skifity ahead |
| Phones home | Update checks and licence to api.cloudron.io | Never (`internal/api/integrations_handlers.go:454`, `docs/faq.md`) | Skifity ahead |
| Recovery tools | Recovery mode, web terminal, file manager, SFTP | One-off command in the app's image, previous-container logs (`/apps/{id}/run`, `logs?previous=true`); no shell by decision (`docs/roadmap.md`) | different by design |
| Multi-server | No | Yes, seven-step SSH join (`internal/provision`); Written | Skifity ahead |
| Team | Users, groups, invitations, mail | Teams, invitations (`internal/api/invitation_handlers.go`) | behind (no groups) |
| Onboarding | Domain first, 2 GB, x86 | Works on an IP with sslip.io, 1 GB, x86-64 and arm64 (`installer/install.sh`, `docs/quick-start.md`, ADR-0015); no release tagged | Skifity ahead on requirements, behind on being installable |
| Price / licence | Source-available, €15–50/month | Free, no licence key (`docs/faq.md`) | Skifity ahead |

## Gaps worth closing in Skifity

**P0 — Know which template an app came from, and offer its update.**
*What:* record `template_id`, the catalogue version and the image each service
was installed with; when the catalogue (in a newer panel) names a newer image
for that service, show "Update available: 1.4 → 1.5" with the upstream release
notes link, and apply it as an ordinary deployment. *Evidence:* this is
Cloudron's core promise (138/196 packages updated in September 2026, "security
fixes within 24h", auto-update policies); Portainer shows an image-update
indicator since 2.14; Skifity's own docs say "a template does not update itself.
Moving one forward is a change to Skifity, and you move your own installation
forward by changing the image" (`docs/templates.md`), and nothing links an app
to its template (`grep -ri template_id` finds nothing). Without this, the 282
templates are 282 apps that silently fall behind on security fixes. *Fit:* a
migration adding the columns to `apps`; `installTemplate` in
`internal/api/template_install.go` writes them; a `version` field per template
file checked by `internal/templates/templates_test.go`; a comparison function in
`internal/templates`; `GET /api/apps/{id}/update` and a banner on the app page;
an MCP tool. The catalogue is compiled in with `//go:embed catalogue/*.yaml`
(`internal/templates/templates.go`), so updates reach a panel with the panel's
own upgrade — which keeps "never phones home" intact. (`docs/templates.md` says
the catalogue "can keep growing without a release"; it means without Go
changes — a binary still has to ship.) *Size:* M. *Without a cluster:* yes —
store, API, comparison and UI are unit- and Playwright-testable; the update
itself is the existing deploy path.

**P0 — Back up before an update, and roll back data with code.**
*What:* before a deployment that changes the image of an app with linked
databases or volumes, take a backup of each, record the backup ids on the
deployment, and refuse the deployment if the backup fails (with an explicit
"deploy without a backup" override); keep those backups past normal retention
for a set time; when rolling back past that deployment, offer to restore them
too. *Evidence:* Cloudron: "A backup is created before each app update. If
backup creation fails, the update is not applied", the pre-update backup is kept
three weeks, and restore reverts code and data because "the current version of
the app may not be able to handle old data". Easypanel's docs tell users to run
a manual backup "before a destructive migration or major version upgrade", and
its issue #114 is a Chatwoot update stuck in `db:migrate` with no way back.
Skifity's rollback restores image and settings only (`handleRollback`), so
rolling back an app whose release command or startup migrated the schema puts
old code on a new schema. *Fit:* `internal/deploy` already has a release phase
before traffic moves (`runRelease` in `internal/deploy/run.go`); a pre-release
step calls `internal/backup.Manager.Run` for each linked database and volume;
a `deployment_backups` table; retention in `internal/backup` skips pinned
backups; the rollback dialog lists what would be restored. *Size:* M–L. *Without
a cluster:* the ordering, refusal, retention and API are unit-testable against
the fake clientset; the backups themselves are Written, never run, like every
backup in Skifity today.

**P1 — Templates that say what they need to be safe.** *What:* three template
fields, each enforced by a test: `health_path` required (or an explicit `tcp`),
`runs_as_root: true` for images that start as root, and `checklist` items
whose completion is recorded per app with user and date. The install dialog
refuses to put a `runs_as_root` template into a `restricted` environment and
offers to lower that environment, instead of the app failing on first start.
*Evidence:* Cloudron's manifest requires `healthCheckPath` (196/196) and ships
checklists (134/196); Skifity has health paths on 5/282 and notes on 49/282.
Skifity's own `internal/kube/podsecurity.go` says the official WordPress,
Nextcloud, MediaWiki and phpMyAdmin images start as root, the default level is
`restricted`, and `ExplainImageRunsAsRoot` exists precisely because this
surprises people — yet the catalogue has no field for it, so `wordpress.yaml`
installs into an environment that will refuse it. *Fit:*
`internal/templates/templates.go` struct fields, a data pass using the image
config's `User` (the registry is already queried by `hack/resolve_tags.py`),
checks in `handleInstallTemplate`, a small `app_checklist` table, locale keys.
*Size:* M. *Without a cluster:* yes.

**P1 — Out-of-memory detection and alert.** *What:* when a container's last
termination reason is `OOMKilled`, say so on the app page with the limit and a
"raise to …" fix, and send an `app.out_of_memory` notification. *Evidence:*
Cloudron notifies "App ran out of memory" and restarts; Skifity's
`internal/kube/explain.go` explains unschedulable pods, pull failures and root
refusals but not OOM, and `internal/watch/watch.go` only notices when *no*
instance is ready — an app that OOMs and restarts every hour never trips it.
Template memory limits are guesses converted from Compose files. *Fit:*
`internal/cluster` status already reads pods; a new `errdoc` entry, a new event
in `internal/notify/notify.go`, locale keys. *Size:* S. *Without a cluster:*
yes — fake clientset pod statuses.

**P1 — Encrypted, verifiable backups.** *What:* encrypt each dump or tar in the
Job with a per-backup data key sealed by the panel's keyring, write a SHA-256
next to it, and add "Verify" (download, check hash, decrypt a header).
*Evidence:* Cloudron encrypts with AES-256 and has a signed integrity manifest
plus "Check integrity" (9.0/9.1); `docs/checklist.md` item 7 says "proven is
exactly the word this cannot claim" for Skifity backups. A verify action is the
cheapest step toward being able to claim it. *Fit:* `internal/backup/jobs.go`
builds the Job; the key goes in as a one-off Secret; `internal/crypto` seals it
with context like every other secret. *Size:* M. *Without a cluster:* the
crypto and the verification are unit-testable; the Job needs one.

**P1 — Scheduled backup of the panel itself to the same bucket.** *What:*
`panel.db` (via SQLite's backup API, as `skifity admin backup-db` already does)
on a schedule to the configured S3 bucket, plus a documented restore that also
needs the master key or recovery key. *Evidence:* Cloudron restores a whole
server from one backup and offers a dry run; Portainer added scheduled S3,
Azure and local backups of its config in 2.45. Skifity's panel state is "a file
on one node" and its backup is a manual local command (`docs/configuration.md`
"What to back up"). *Fit:* the minute tick in `internal/serverapp`, the S3
client in `internal/backup/storage.go`. *Size:* S–M. *Without a cluster:* yes —
it is the panel process and a bucket (MinIO in a test).

**P1 — An identity wall in front of an app.** *What:* "Only people in this team
can open this app": the edge guard asks the panel whether the request carries a
signed-in session for the app's team, and redirects to sign-in otherwise.
*Evidence:* Cloudron's `proxyAuth` addon (and VPN-only apps) exist because many
self-hosted tools have no login or a weak one; basic auth, which Skifity is
adding, gives a shared password rather than a person. *Fit:* the guard is
already a Traefik forwardAuth in every namespace (`internal/kube/namespace.go`
`BuildGuardMiddleware`, `internal/guard`); add a rule kind to
`internal/edgerules` that checks a session cookie scoped to the app's domain.
*Size:* M. *Without a cluster:* the decision logic yes; Traefik behaviour needs
one (the firewall has the same open issue in `docs/progress.md`).

**P2 — Let installed apps sign in with the panel.** *What:* Skifity as an OIDC
provider for the apps it hosts; a template declares `sso: oidc` and gets client
credentials injected. *Evidence:* 97 Cloudron packages use its OIDC addon and 26
its LDAP; users repeatedly name SSO as the thing Docker panels lack. *Fit:* a
new package next to `internal/auth`; per-template wiring is data. *Size:* L.
*Without a cluster:* yes for the provider; the per-app wiring needs real apps.

**P2 — Wire email into apps.** *What:* an `smtp` input kind that fills a
template's SMTP variables from the panel's email settings (or a per-app relay),
so password resets and invitations work on day one. *Evidence:* 116 of 196
Cloudron packages use its sendmail addon; only 2 of 282 Skifity templates
mention SMTP. *Fit:* `Input` in `internal/templates/templates.go`,
`installTemplate`. *Size:* S. *Without a cluster:* yes.

**P2 — Third-party template feeds.** *What:* add a template catalogue by URL,
signed like the plugin store index, with its own update stream. *Evidence:*
Cloudron's community apps (9.1.0/10.0.0) with automatic updates and a warning
that they are unreviewed; Portainer's custom template URL. *Fit:* reuse
`internal/pluginstore`'s signed-index verification. *Size:* M. *Without a
cluster:* yes.

**P2 — Update policy and window.** *What:* once P0 exists, per-environment "apply
template updates automatically on these days and hours", off by default.
*Evidence:* Cloudron's three policies with a schedule; Portainer's GitOps change
windows. *Fit:* `internal/cron`, the minute tick. *Size:* S after P0. *Without a
cluster:* yes.

## Things to deliberately not copy

* **Updates behind a subscription.** Cloudron stops platform and app updates
  when the subscription ends; security fixes become a paid feature. Skifity's
  catalogue updates should ship with the panel, free.
* **Repackaging every app.** Cloudron's safety comes from rebuilding each
  upstream on its own base image, which is why it has 196 apps after eleven
  years and no ARM. Copy the *metadata* (health path, root flag, checklist,
  backup hooks), not the rebuild; Skifity runs upstream images.
* **A built-in mail server.** Haraka, Dovecot, spam training, DKIM, IP
  reputation: a product of its own, and Cloudron's docs warn that "many email
  providers block emails from VPS and home network IPs". Wire apps to a relay
  instead.
* **Admins who can sign in as anyone.** Cloudron let admins "log in to any app"
  and impersonate users; 10.1.0 removed impersonation. Skifity's secrets are
  write-only on every surface; keep that stance for identities too.
* **Credentials that change on every restart.** Cloudron's addon variables can
  change at restart, which only works because every package reads them at
  runtime. Upstream images cache them; Skifity's stable connection strings are
  right for images it did not build.
* **One shared database server for all apps.** Efficient on 2 GB, but one
  noisy or corrupted instance takes every app down; Skifity's per-database
  instances are the safer default, and the memory cost should be shown instead.
* **Requiring a domain and wildcard DNS before the dashboard works.** Skifity's
  sslip.io first run (ADR-0015) is the better on-ramp.
* **Single server as the only shape.** The forum has asked for years; the
  architecture cannot answer. Skifity's reason to exist is the other answer.

## Sources

All read 2026-09-30.

* https://www.cloudron.io/pricing.html (and its HTML for which price is monthly vs yearly)
* https://www.cloudron.io/ — "security fixes within 24h", "Thousands of organizations"
* https://docs.cloudron.io/installation/ — requirements, Ubuntu 26.04, no ARM/LXC
* https://docs.cloudron.io/updates — update policies, backup before update, cancelled subscription
* https://docs.cloudron.io/apps — community apps, app proxy, access control, operators, checklists, recovery mode, versions
* https://docs.cloudron.io/backups — sites, providers, formats, encryption, integrity, retention, restore, clone, import, dry run
* https://docs.cloudron.io/security — isolation, signed releases, rate limits, passwords
* https://docs.cloudron.io/user-management and https://docs.cloudron.io/user-directory — roles, groups, OIDC provider, LDAP, external directory
* https://docs.cloudron.io/notifications — events and email
* https://docs.cloudron.io/domains, /email, /network, /docker, /server — DNS providers, mail, VPN protection, registries
* https://docs.cloudron.io/packaging/, /packaging/manifest, /packaging/addons, /packaging/versions — manifest fields, addons, CloudronVersions.json
* https://docs.cloudron.io/sitemap.xml — 275 API reference pages, 219 package pages
* https://api.cloudron.io/api/v1/apps — 196 packages; addon, checklist, health path and update-date counts computed locally
* https://git.cloudron.io/platform/box/-/raw/master/CHANGES — 9.x and 10.x changes
* https://git.cloudron.io/api/v4/projects/platform%2Fbox/repository/tags and /projects/platform%2Fbox — release dates, 33 stars
* https://git.cloudron.io/platform/box/-/raw/master/LICENSE, /README.md, /package.json — licence, stack, MySQL
* https://hub.docker.com/v2/repositories/cloudron/base/ — 648,488 pulls
* https://forum.cloudron.io/topic/8453 (pricing too high), /topic/11129 (free plan), /topic/11580 (docker compose), /topic/10015 (ARM), /topic/14598 (9.0.11 broke services), /topic/14788 (n8n CVE-2025-68613), /topic/15119 (n8n 2.9.3), /topic/13214 (Keycloak updates); forum search API for "update broke" and "multiple servers"
* https://news.ycombinator.com/item?id=46302397, 46580919, 49588240, 49598316, 43735056, 44848503, 44612687, 44146751, 46285874 (via hn.algolia.com)
* https://dev.to/vikasprogrammer/coolify-vs-cloudron-vs-caprover-in-2026-i-self-hosted-apps-on-all-three-46mg (March 2026)
* https://easypanel.io/alternatives/cloudron — competitor's comparison, verified by that vendor 2026-09-29
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=cloudron — CVE-2021-40868
* https://github.com/serenichron/mcp-cloudron — third-party MCP server
