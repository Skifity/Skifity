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
| **Cluster** | The k3s version new servers are installed with, the pod network they join on, how long a preview environment lives, and how much history — deployments per app, activity log in days — is kept. |
| **Domains and HTTPS** | A wildcard domain so every app gets a free subdomain, the address domains should point at, the email Let's Encrypt sends expiry warnings to, a Cloudflare Tunnel token for a cluster with no public address, the proxies whose forwarded addresses are believed, and the country and network databases the firewall reads. |
| **Backup storage** | S3 or anything that speaks S3, including MinIO. Credentials never leave the panel: a backup Job is handed a presigned URL that expires. |
| **DNS** | A provider token, for wildcard certificates, which need a DNS challenge. |
| **Email** | SMTP, for notifications. |
| **Image registry** | Where built images go. An in-cluster registry is installed on first use if this is left empty. |
| **Sign-in** | An OpenID Connect provider, so people sign in with the account they already have. See below. |
| **Plugins** | The store the Plugins page reads, and the public key its index has to be signed by. See [Writing a Skifity plugin](plugins.md). |

**Git** is a tab rather than a group of settings: a connection to GitHub, GitLab
or Gitea is a row you add, with a personal access token. There was a group here
for a GitHub App — an app id, a client id, a client secret, a private key — and
nothing ever read one of them, so it is gone until the code behind it exists.

**Notifications** is a tab rather than a group of settings: a channel is a row
you add, and Skifity sends to Telegram, Discord, a webhook of your own, or email
through the SMTP settings above.

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
and configure, **member** can deploy. Nobody can invite somebody to a role
above their own.

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

Leave **Allowed email domains** empty only when the provider is yours. Against a
public provider an empty list means anybody with an account there can sign in.

An account created this way has **no password**. It cannot be signed into with
one, and the refusal is indistinguishable from a wrong password — so the sign-in
form does not become a way to find out which addresses use single sign-on. A new
account joins no team: an administrator adds it to one, the same as any other.

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

## What to back up

Two things, and they are not the same thing:

1. `/etc/skifity/master.key` — without it every stored secret and every database
   backup is unreadable. The panel can also print a recovery key that encodes
   the same secret in a form you can write on paper.
2. `/var/lib/skifity/panel.db` — the panel's own state: teams, apps, settings
   and history. Your applications' data is in their volumes and databases, and
   is backed up separately by the panel itself.

   Take this copy with `skifity admin backup-db <path>` rather than copying the
   file. The database runs in WAL mode, so a committed change can still be in
   `panel.db-wal` and not yet in `panel.db`; a plain copy loses it and says
   nothing. The command goes through SQLite, works while the panel is running,
   and writes one file.

Keep the key somewhere the database backup is not. Together they are everything;
apart, neither is enough.

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
