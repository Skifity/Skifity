# Configuration

Skifity has two kinds of configuration, and the split is deliberate.

**Settings you change while it is running** — domains, Git accounts, backup
storage, SMTP, the registry — live in the panel's database and are edited under
**Settings**. They are encrypted where they are secret, and changing one takes
effect without a restart.

**Settings that must be known before the database can be opened** — where the
database is, where the master key is, what to listen on — come from the
environment or a file. Those are the only ones on this page's first table.

## Startup configuration

Precedence, lowest to highest: built-in defaults, the configuration file, then
the environment. Every setting has a working default, so an empty configuration
is a valid one.

| Environment variable | Default | What it is |
|---|---|---|
| `SKIFITY_LISTEN` | `:8080` | The address to bind to. `127.0.0.1:8080` keeps it off the network behind a reverse proxy. |
| `SKIFITY_DATABASE_PATH` | `/var/lib/skifity/panel.db` | The SQLite file holding everything except the master key. |
| `SKIFITY_MASTER_KEY_PATH` | `/etc/skifity/master.key` | The key every stored secret is encrypted with. Back this up. |
| `SKIFITY_SETUP_TOKEN_PATH` | `/etc/skifity/setup-token` | The one-time token that allows the first account. Deleted once setup is done. |
| `SKIFITY_PUBLIC_URL` | derived from the request | How people reach the panel. Used for webhook URLs and links in notifications. |
| `SKIFITY_KUBECONFIG` | empty | Empty when running inside the cluster: the ServiceAccount is used instead. |
| `SKIFITY_NAMESPACE` | `skifity-system` | The namespace the panel itself runs in. |
| `SKIFITY_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `SKIFITY_LOG_FORMAT` | `json` | `json` or `text`. |
| `SKIFITY_TRUSTED_PROXY_COUNT` | `1` | How many reverse proxies sit in front. Only that many entries are trusted from `X-Forwarded-For`, so a client cannot forge its own address. |
| `SKIFITY_SESSION_TTL` | `168h` | How long a sign-in lasts without activity. |
| `SKIFITY_SHUTDOWN_GRACE` | `20s` | How long in-flight requests get when stopping. |
| `SKIFITY_DEV_MODE` | `false` | Serves the interface from a Vite dev server and relaxes cookie security. Development only. |
| `SKIFITY_DEV_FRONTEND_URL` | `http://127.0.0.1:5173` | Where that dev server is. |
| `SKIFITY_CLUSTER_TOKEN_PATH` | `/etc/skifity/cluster-token` | The k3s join token of the cluster the panel runs in, put there by the installer. A panel installed onto a server that already runs k3s cannot invent this, and a server added later has to join with it. |
| `SKIFITY_POD_NETWORK` | empty | The pod network the installer started this cluster with, `wireguard-native` or `vxlan`. Recorded the first time the panel starts and used for every server added afterwards, because nodes on different backends join without an error and then never reach each other. Leave empty on a cluster the installer did not create, and set it under **Settings → Cluster** instead. |
| `SKIFITY_CLI_DIR` | `/usr/local/share/skifity/cli` | The command line tool for the platforms the panel does not run on, as `skifity-<os>-<arch>[.exe].gz`. The image puts macOS, Windows and the other Linux architecture there, so somebody deploying from a Mac or a Windows laptop downloads a CLI that runs on it. |

`.env.example` in the repository is the same list, with comments.

A configuration file may be passed with `--config`, in a simple `key = value`
format. Its keys are the variable names without the prefix, lower-cased:

```toml
listen = ":8080"
log_level = "debug"
database_path = "/var/lib/skifity/panel.db"
```

## Settings in the panel

![The Git tab in Settings: connected accounts, then the GitHub App fields](images/settings-git.png)

These are stored encrypted where they are secret, and none of them is required
to get started.

| Group | What it is for |
|---|---|
| **General** | The panel's own URL, the default builder, and an explicit switch for usage reporting — which is off, and has always been off, and exists so that its absence is visible rather than assumed. |
| **Cluster** | The k3s version a new cluster's first server is installed with (later servers join at the version the cluster runs), the pod network they join on, how long a preview environment lives, and how much history — deployments per app, activity log in days — is kept. |
| **Domains and HTTPS** | A wildcard domain so every app gets a free subdomain, the address domains should point at, the email Let's Encrypt sends expiry warnings to, a Cloudflare Tunnel token for a cluster with no public address, the proxies whose forwarded addresses are believed, and the country and network databases the firewall reads. |
| **Backup storage** | S3 or anything that speaks S3, including MinIO. Credentials never leave the panel: a backup Job is handed a presigned URL that expires. |
| **DNS** | A provider token, for wildcard certificates, which need a DNS challenge. |
| **Email** | SMTP, for notifications. |
| **Image registry** | Where built images go. An in-cluster registry is installed on first use if this is left empty. |
| **Image scanning** | Whether every image an app runs is scanned for known vulnerabilities with Trivy (on by default), when running apps are scanned again (daily), and whether a deploy is stopped by a critical vulnerability that has a fix (off by default). See [Scanning every image for vulnerabilities](concepts.md#scanning-every-image-for-vulnerabilities). |
| **Sign-in** | An OpenID Connect provider, so people sign in with the account they already have. See below. |
| **Plugins** | The store the Plugins page reads, and the public key its index has to be signed by. See [Writing a Skifity plugin](plugins.md). |

**Git** is a tab rather than a group of settings: a connection to GitHub, GitLab
or Gitea is a row you add, with a personal access token. There was a group here
for a GitHub App — an app id, a client id, a client secret, a private key — and
nothing ever read one of them, so it is gone until the code behind it exists.

**Notifications** is a tab rather than a group of settings: a channel is a row
you add, and Skifity sends to Telegram, Discord, Slack, Mattermost, Microsoft
Teams, ntfy, Pushover, Gotify, a webhook of your own, or email through the SMTP
settings above.

* **Slack** and **Mattermost** take an incoming webhook's address, and get the
  message as a coloured attachment with a link back to the panel.
* **Microsoft Teams** takes the address of a Workflows webhook: in the channel,
  open **Workflows**, choose *Send webhook alerts to a channel*, and paste the
  link it gives you. The message is an Adaptive Card — the title in the
  level's colour, the details as a list, and a button back to the panel.
  Office 365 connectors, which took the older MessageCard, are being retired
  by Microsoft; a connector that still works takes the same card.
* **ntfy** takes a topic, and optionally your own server (ntfy.sh otherwise) and
  an access token for a protected topic. A failure is sent at high priority, so
  it buzzes; a success is sent low, so it does not.
* **Pushover** takes an application token and your user or group key. Nothing
  is sent at emergency priority, which repeats until acknowledged.
* **Gotify** takes your server's address and an application token (Apps, in
  Gotify). The token is sent in a header rather than the address, so it is not
  written to the server's access log. A failure is priority 8, which
  interrupts; a success is 2, which does not.
* **A webhook** takes an address, and optionally a shared secret, sent in the
  `X-Skifity-Secret` header for your receiver to compare.

Every one of them is sent from the panel through the same guarded client as a
webhook, so none can be pointed at an address inside the cluster.

### Changing a channel

**Edit** on a channel opens the form it was added with. Everything can change
but its kind: a Telegram channel does not become a Discord one. Settings that
are not secret — a chat id, a topic, a list of recipients — are shown as they
are. A secret — a bot token, a webhook address that is itself the credential,
an access token — is never shown again once it is stored: its box says it is
there, and left empty it keeps it. Type a new one to replace it.

A channel can also be switched off there, which pauses it without forgetting
its settings.

### Limiting a channel to some projects

A channel hears about every project unless you limit it. Limited, it hears
about the projects you choose and no others. What belongs to a project is
decided by the event:

| Event | Belongs to |
|---|---|
| A deployment succeeded or failed | The app's project |
| An app stopped answering, or came back | The app's project |
| An app crossed a usage threshold | The app's project |
| A certificate could not be issued | The project of the app the domain is on |
| A backup failed, succeeded or was missed | The project of the database, or of the app whose volume it copied |
| A server joined, stopped responding or crossed a threshold | The whole team |
| This panel could not back itself up | The whole team |

An event that belongs to the whole team reaches every channel that asked for
it, limited or not: a server that stops answering takes every project's apps
with it, and a channel limited to one of them is still a channel somebody
reads. If you do not want those on a limited channel, leave the server events
unticked.

A channel limited to projects that have all since been deleted stays limited —
to none. It hears only the whole team's events, and never goes back to hearing
every project.

## Adding somebody to the team

**Settings → Members → invite by email.** Skifity gives you a **link** and shows
it once. Send it however you like; whoever opens it picks their own name and
password and lands in the panel, already signed in and already in the team.

It is a link rather than an email on purpose. SMTP is a setting most installs
have not filled in, and a panel that cannot add a colleague without a mail
server is a panel that cannot add a colleague. Configure SMTP if you want
notifications; you do not need it for this.

The link works for **seven days** and **once**. The token behind it is stored
hashed, like a session, so the panel cannot show it to you again — take a copy
when it appears, and if you lose it, withdraw the invitation and make another.
Pending invitations are listed under Members, and can be withdrawn there.

Somebody who already has an account here does not need a link. Add them by the
same form and they join directly.

Roles: **owner** administers the team and can delete it, **admin** can invite
and configure, **member** can deploy, and **viewer** can see everything a member
can — apps, deployments, logs, metrics, variables that are not secret — and
change nothing. Nobody can invite somebody to a role above their own.

A viewer is for the client who wants to watch their site's deployments, the
manager who wants the logs, or an assistant that should read and never act. A
viewer can make an API token, and the token is exactly as read-only as they
are: every request is checked against its owner's role in the team at that
moment, so changing somebody's role changes what their tokens can do too. The
only things a viewer cannot read are the ones a member cannot either, because
they are an administrator's: invitations, the audit log, the export, a
database's password, and the Kubernetes objects under Advanced.

What a member cannot *read*, a member can still *use*. Deploying is running
code of your choosing with the app's variables, secrets included, so a member
can print any of them from a deployment or `skifity run`, and a database they
link to an app hands its connection string — password and all — to that app.
The line between member and admin keeps a password off a member's screen and
out of their tokens; it does not keep it from somebody who can deploy. Give
the member role to people you would trust with the app's secrets, and viewer
to everybody else.

### Limiting somebody to some projects

A member or a viewer can be limited to some of the team's projects: tick
**Limit to some projects** when inviting them, or later under **Change access**
beside their name. It is how one team holds an agency and its clients, or a
company and a contractor who works on one product.

A limited member sees their projects and nothing else. Another project's apps,
databases and environments answer exactly as if they did not exist — the same
answer another team's get — so the limit does not tell them what else there is.
What belongs to the whole team is refused outright: servers, the cluster,
notification channels, creating a project. They can still see the team's name
and who is in it, pick a repository to create an app from, and make an API
token, which is limited exactly as they are.

Admins and owners cannot be limited: they look after the whole team by
definition. Deleting the last project somebody was limited to leaves them with
no projects, not with all of them. Changing someone's role without ticking the
box lifts the limit — the form always says the whole of what they can reach.

## Signing in

**Passwords** are hashed with Argon2id, twelve characters minimum, and checked
against the handful of passwords automated attacks try first. Failed attempts
pause sign-in: five for one account, twenty from one address, in a
fifteen-minute window. The two limits are separate so that somebody hammering
your account cannot lock you out by hammering it, and somebody spraying many
accounts from one machine is stopped anyway.

**Two-factor authentication** is TOTP, and a code is spent when it is used — the
window is ninety seconds wide, so a code read over a shoulder or out of a screen
share would otherwise work again. Eight recovery codes are shown when it is
turned on; each works once.

**Sessions** last seven days of inactivity by default (`session_ttl`) and thirty
days however much they are used. The second one is the ceiling: without it a
session used once a day renews forever, and a cookie stolen in January is still
good in December. Account → Sessions lists every device and signs out the ones
that are not this one.

**Three actions ask for your password again**, even though you are signed in:
turning two-factor off, reading the recovery codes, and creating an API token.
Each of them turns a session somebody borrowed into access they keep. Signing in
counts, so in practice this is one dialog a few minutes into a session. An API
token cannot take these actions at all — there is nobody at the keyboard for it
to ask.

**A team can require more than a password.** Under **Settings → Members**, an
admin or owner can require two-factor or single sign-on of everybody in the
team, themselves included. A sign-in with a password alone then sees nothing in
the team — every page and every API request in it answers
`auth.strong_auth_required` — while the account page stays reachable, which is
where two-factor is turned on; the panel says so at the top of every page. A
session counts when it was signed into through the identity provider, or its
person has two-factor on. An API token cannot say how it was minted, so it
counts when its owner has two-factor on or has linked the identity provider.
Turning the requirement on from a sign-in that would not pass it is refused
rather than locking out whoever did it.

**Put a domain on the panel.** The panel's cookies carry the `__Host-` prefix,
which a browser refuses to store if a cookie names a domain — so no page on a
sibling subdomain can write them. That prefix requires HTTPS. The default
install is plain HTTP on an sslip.io address (ADR-0015), and on plain HTTP the
prefix cannot be used at all. This matters more here than on most products,
because the applications this panel hosts can be on subdomains of the domain the
panel answers on. Adding a domain in Settings is what closes it.

Whether cookies are marked `Secure` follows `SKIFITY_PUBLIC_URL` rather than the
build, because a `Secure` cookie is never stored over plain HTTP: marking them
Secure on an HTTP panel does not make anything safer, it makes signing in
impossible. Set that variable to the address people actually open.

## Your own registry, and your own mail server

Two settings groups that hold credentials, and both are read now.

**Registry.** Leave the address empty and images go to the registry inside the
cluster, which needs nothing. Give it an address and a username and password,
and Skifity places those credentials as a Kubernetes Secret in two places: the
namespace builds run in, so the push works, and each app's namespace, so the
kubelet can pull. Both were missing before, so an external registry broke a
deploy at each end with nothing but a Kubernetes error about a Secret that was
never created.

**Email.** Fill in the SMTP server once here, and an email notification channel
only needs the recipients. A channel that names its own server still wins, for
the case where one alert goes somewhere else. Before this, every channel
carried its own copy of the SMTP password and this page configured nothing at
all.

## Single sign-on

Skifity speaks **OpenID Connect**: Okta, Entra ID, Authentik, Keycloak, Zitadel,
Google Workspace, or anything else that publishes a discovery document. There is
no SAML, and there is not going to be — it is a second protocol and a second
class of signature bug, and every provider a self-hosted panel meets speaks
OIDC.

Register an application with your provider as a **confidential client** using the
authorization code flow, and give it this redirect URI:

```
https://panel.example.com/api/auth/sso/callback
```

The host has to be the **Panel URL** setting exactly. Skifity builds the redirect
URI from that setting rather than from the request, so a request cannot name its
own redirect target — which also means single sign-on does not start until Panel
URL is set.

Then, under **Settings → Sign-in**:

| Setting | What it is |
|---|---|
| Single sign-on issuer | The provider's issuer URL. Skifity reads `/.well-known/openid-configuration` under it. |
| Client ID | From the application you registered. |
| Client secret | Stored encrypted and never shown again. |
| Sign-in button text | What the button says. Empty gives a generic label. |
| Allowed email domains | Comma-separated. Only verified addresses in these domains may sign in. |
| Create an account on first sign-in | Off by default: somebody the provider knows and this panel does not is refused. |
| Groups claim | The ID token claim that lists a person's groups. Empty is `groups`. |
| Groups to teams | One per line, `group = team:role`. See below. |

Leave **Allowed email domains** empty only when the provider is yours. Against a
public provider an empty list means anybody with an account there can sign in.

An account created this way has **no password**. It cannot be signed into with
one, and the refusal is indistinguishable from a wrong password — so the sign-in
form does not become a way to find out which addresses use single sign-on. A new
account joins no team: an administrator adds it to one, the same as any other —
unless the provider's groups say which.

### Teams that follow the provider's groups

**Groups to teams** maps a group at the provider to a team and a role, one per
line, by the team's slug:

```
platform-admins = acme:admin
developers      = acme:member
clients         = acme:viewer
```

A team named there follows the groups for everybody who signs in through the
provider, at every sign-in: they get the highest role their groups give, and
leave the team when no group gives one. So taking somebody out of a group at
the provider takes them out of the team the next time they sign in, and nobody
has to remember to do it twice. A team not named is left alone, and a team's
last owner is never removed or demoted by it. A member who was limited to some
projects keeps the limit when a group only changes their role between member
and viewer.

The groups are read from the verified ID token, so the provider has to put them
there — most need a mapper or a scope for that. A sign-in whose teams cannot be
brought in line is refused rather than let through on yesterday's groups, and
every change is in the team's audit log as `team.member_synced`. A session
already open keeps its access until it ends or is signed out; to remove
somebody at once, remove them under Members as well.

### How a returning person is recognised

By who the provider says they are — its issuer and its permanent id for them —
and not by their email address. An address is not an identity: some providers
let a user type their own, and Entra ID does not say whether it verified one.
Matching on the address alone would let whoever can make an account at the
provider with the owner's address sign in as the owner.

So an address only finds an **existing** account here the first time, and only
when that cannot hand somebody else's account over:

* the account has **no password**, so it only ever existed through single
  sign-on; or
* the provider says, in its `email_verified` claim, that it **verified** the
  address.

Otherwise the sign-in is refused with a message saying what to do: sign in with
the password, open **Account → Single sign-on**, and link the provider there.
Linking asks for the password again, and the browser that comes back from the
provider has to still be signed in as the same account. After that, the account
is found by the provider's id even if the address changes, and it can be
unlinked from the same card as long as it still has a password.

Nothing is lost if the provider goes away. The first administrator account still
has a password, and API tokens keep working.

## The CLI

The CLI keeps its own configuration in your user configuration directory, so a
token cannot be committed to a repository by accident.

| Environment variable | What it is |
|---|---|
| `SKIFITY_URL` | The panel's address. |
| `SKIFITY_TOKEN` | An API token, created under **Account → API tokens**. |
| `SKIFITY_TEAM` | Only needed when your account is in more than one team. |
| `SKIFITY_CONFIG` | A different path for the stored configuration. |

With `SKIFITY_URL` and `SKIFITY_TOKEN` set, nothing has to be signed in first,
which is how the CLI and the MCP server are meant to be used from CI, a
container or an assistant's sandbox. They override anything stored.

### API tokens only from

A token is a password that does not end when somebody leaves, and it tends to
end up in CI logs and dotfiles. **Settings → Sign-in → API tokens only from**
takes addresses and networks, one per line (`203.0.113.7`, `10.0.0.0/8`); once
it is set, a token used from anywhere else is refused with
`auth.token_network`, valid or not — the CLI, CI jobs and the MCP endpoint
alike. Signing in to the panel in a browser is not limited, because that is a
password and a second factor from wherever its owner happens to be.

The address checked is the one the panel sees, through as many proxies as
`SKIFITY_TRUSTED_PROXY_COUNT` says sit in front of it. Leave the list empty to take
tokens from anywhere, which is the default.

## What to back up

Two things, and they are not the same thing:

1. `/etc/skifity/master.key` — without it every stored secret and every database
   backup is unreadable. The panel can also print a recovery key that encodes
   the same secret in a form you can write on paper.
2. `/var/lib/skifity/panel.db` — the panel's own state: teams, apps, settings
   and history. Your applications' data is in their volumes and databases, and
   is backed up separately by the panel itself.

   Once backup storage is set, the panel copies this to the bucket every day
   by itself: see [Backups](backups.md#the-panel-itself), which also says how
   to put a copy back with `skifity admin restore-db`.

   To take a copy by hand, use `skifity admin backup-db <path>` rather than
   copying the file. The database runs in WAL mode, so a committed change can
   still be in `panel.db-wal` and not yet in `panel.db`; a plain copy loses it
   and says nothing. The command goes through SQLite, works while the panel is
   running, and writes one file.

Keep the key somewhere the database backup is not. Together they are everything;
apart, neither is enough.

## Upgrading

The panel does not check for new versions: it contacts no server of its own,
so a release is something you hear about from the releases page. Upgrading is
one API call as a panel administrator — the panel changes the image of its own
Deployment and Kubernetes rolls it out:

```sh
curl -X POST https://panel.example.com/api/upgrade \
  -H "Authorization: Bearer skf_..." -d '{"version":"v1.2.0"}'
```

Your apps keep running throughout; the panel itself is down for the moment it
takes the new version to start.

**Before anything changes, the panel copies its database** beside itself, as
`panel.db.before-upgrade-<time>-to-<version>`, and keeps the last three. If
backup storage is set up it also puts a copy in the bucket. An upgrade that
cannot take the local copy does not start.

That copy is the other half of going back. The new version migrates the
database when it starts, and a version refuses a database that a later one has
migrated — reading columns it does not know and rules it does not keep is how
data is lost quietly — so rolling the image back is not enough on its own. The
answer to the upgrade request carries the exact commands, with the copy's path
filled in:

```sh
kubectl -n skifity-system scale deploy/skifity-panel --replicas=0
skifity admin restore-db --yes /var/lib/skifity/panel.db.before-upgrade-...
kubectl -n skifity-system rollout undo deploy/skifity-panel
kubectl -n skifity-system scale deploy/skifity-panel --replicas=1
```

`restore-db` puts a copy back exactly as it was taken; it never migrates it, so
any version of the binary can restore a copy for any other. Anything you
changed in the panel between the upgrade and going back is lost with it —
which is another reason to go back soon or not at all.

Migrations only go forward. A version skipped is fine: the one you upgrade to
applies every migration it has that the database does not.

### Upgrading components

The components the panel installs — HTTPS certificates, PostgreSQL, scale to
zero, the builder, the registry, cross-node storage, the Cloudflare tunnel —
each record the version they were installed at, under **Settings → Components**.
A panel release that ships a newer version of one says so beside it, with an
**Upgrade to …** button: the newer release's manifest is applied over the one
that is running, which is how each of these projects documents its own upgrade.
The component restarts; the apps using it do not. A version you chose yourself,
by pointing a component's manifest setting at another address, is the version
it is compared with.

A component installed before versions were recorded shows no version and is
offered the current one. Only a newer version is offered — clearing a manifest
setting that pointed at a newer release does not turn into a downgrade — and a
component upgraded from a manifest goes one minor version at a time: Longhorn
refuses more, and cert-manager advises against it. When the panel's version is
further ahead, the refusal names the manifest setting to point at the release in
between first. An upgrade that fails leaves the component as it was, marked
installed, with the reason beside it; one the panel restarted in the middle of
is marked installed at its old version, to be upgraded again.

### Upgrading k3s

Kubernetes supports each minor version for about fourteen months, and the
installer put one on every server that nothing moved since. **Settings →
Components → Kubernetes (k3s)** lists each server's version, offers the latest
release of each minor version from k3s's own release channels (or takes one
typed, for a panel that cannot reach them), and **Check** shows the plan before
anything happens:

* The order: control-plane servers first, then workers, one at a time — a
  worker newer than the API it talks to is the one version skew Kubernetes does
  not allow.
* Every reason not to start: a version that skips a minor version (1.35 to 1.37
  has to go through 1.36), a server that already runs something newer, one that
  is not ready, upgrade plans already in the cluster that the panel did not
  write — the `server-plan` and `agent-plan` of the k3s documentation, applied by
  hand, would upgrade the same servers beside the panel's — and **no backup of
  the panel's own database from the last day**. A version typed without its
  `+k3s` part is read as the first k3s release of it: `v1.31.4` is
  `v1.31.4+k3s1`.
  The upgrade does not start until the list is empty; starting checks it again
  rather than trusting the one on screen.
* What is worth knowing: with one control plane, the Kubernetes API is away for
  about a minute while it restarts. Apps keep serving; the panel reconnects.

**Upgrade** hands the work to Rancher's system-upgrade-controller, which is how
k3s documents upgrading from inside a cluster: it is installed the first time
(it appears under Components as *Kubernetes upgrades*), and given two plans for
the version — the control plane, one server at a time, then the workers, one at
a time once the control plane is done. Each server is cordoned, so nothing new
is scheduled on it, while its k3s binary is replaced and restarted; running
containers keep running through a k3s restart, so it is not drained. The
servers' versions change on the Servers page as each is done.

It cannot be undone from the panel: k3s is not downgraded. The backup the plan
insisted on is what the panel is restored from if an upgrade takes its own
server with it.

## Monitoring the panel

The panel watches the cluster; this is how you watch the panel. `GET
/api/metrics` returns the Prometheus text format.

It is behind the same authentication as the rest of the API, because it says how
many apps and servers exist and how the process is getting on, which is not
something to hand to anyone who can reach the port. Scrape it with an API token
from an account with administrator rights:

```yaml
scrape_configs:
  - job_name: skifity
    metrics_path: /api/metrics
    authorization:
      credentials: skf_your_token_here
    static_configs:
      - targets: ["panel.example.com"]
```

Create the token under your account, then **API tokens**.

What is there:

| Metric | What it tells you |
| --- | --- |
| `skifity_http_requests_total` | Requests by method, route and status. The route is the pattern, not the path, so an id never becomes a label. |
| `skifity_http_request_duration_seconds` | A histogram of how long they took. |
| `skifity_deployments_total` | Deployments that finished, by result. |
| `skifity_deployments_in_flight` | Deployments that have not. A number that only climbs means something is stuck. |
| `skifity_apps`, `skifity_servers` | How many exist; servers are split by status. |
| `skifity_cluster_reachable` | 1 when the Kubernetes API answered. |
| `skifity_event_clients` | Open event streams. Climbing and never falling is a subscription that is not being closed. |
| `skifity_database_bytes` | The size of `panel.db`. |
| `skifity_goroutines`, `skifity_memory_heap_bytes` | The two numbers that say the panel is leaking. |
| `skifity_uptime_seconds`, `skifity_build_info` | How long it has been up, and which version. |

Three alerts are worth having: `skifity_cluster_reachable == 0` for more than a
few minutes, `skifity_deployments_in_flight` above zero for an hour, and
`skifity_goroutines` climbing steadily over a day.
