<div align="center">

<img src="docs/images/logo.svg" width="80" height="80" alt="">

# Skifity

**Self-hosted apps, powered by Kubernetes.**<br>
You give it a server. It gives you URLs.

[![Release](https://img.shields.io/github/v/release/Skifity/Skifity?sort=semver&label=release)](https://github.com/Skifity/Skifity/releases)
[![CI](https://github.com/Skifity/Skifity/actions/workflows/ci.yml/badge.svg)](https://github.com/Skifity/Skifity/actions/workflows/ci.yml)
[![CodeQL](https://github.com/Skifity/Skifity/actions/workflows/codeql.yml/badge.svg)](https://github.com/Skifity/Skifity/actions/workflows/codeql.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/Skifity/Skifity)](go.mod)
[![Licence](https://img.shields.io/badge/licence-Apache%202.0-blue)](LICENSE)

[Install](#install) · [Features](#features) · [Screenshots](#screenshots) · [CLI and MCP](#the-terminal-and-ai-assistants) · [Documentation](#documentation)

</div>

<p align="center">
  <img src="docs/images/overview-dark.png" alt="The overview in dark mode: projects, apps, cluster health and recent activity" width="880">
</p>

Skifity turns the servers you rent into a platform for your own apps. Push a Git
repository and it builds it, deploys it, gives it a URL with a certificate, and
keeps it running. Add more servers and they become one pool: apps spread across
them, and an app whose server dies comes back on another.

The panel talks about **Apps**, **Instances**, **Servers**, **Domains** and
**Databases**. Kubernetes runs underneath and stays out of sight, one click away
under **Advanced**.

One binary is the panel, the CLI and an MCP server, and it idles at **35 MiB**
of memory.

## Install

On a fresh Ubuntu 24.04 or Debian 12 server:

```sh
curl -fsSL https://github.com/Skifity/Skifity/releases/latest/download/install.sh | sudo sh
```

With a domain already pointed at the server, HTTPS is set up during the install:

```sh
curl -fsSL https://github.com/Skifity/Skifity/releases/latest/download/install.sh | sudo sh -s -- --domain panel.example.com
```

The link is always the newest release's installer, and that installer installs
its own release: the image and the Kubernetes objects of the same version, so
the two can never disagree.

That is the whole installation. The installer:

1. **Checks the server** — system, memory, disk, ports, clock — and stops
   before changing anything if one of them will not do.
2. **Opens the host firewall** for HTTP, HTTPS and the cluster's own traffic,
   and nothing else, whether it is ufw, firewalld or plain iptables.
3. **Installs Kubernetes** (k3s), with an encrypted pod network where the
   kernel supports it.
4. **Starts the panel** with the Kubernetes objects from the same release, so
   an image and its Deployment never come from different versions.
5. **Checks the panel answers at its own address**, through the ingress, the
   way your browser will reach it.
6. **Prints a link** with a one-time setup token already in it. Open it, create
   your account, and you are in.
7. **Puts `skifity` and `skifity-uninstall` on the PATH**, from the same release.

Run in a terminal without `--domain`, it offers to take one. Without a domain
the panel answers at a free `sslip.io` address over plain HTTP, on purpose
([ADR-0015](docs/decisions.md)); add a domain in Settings later and HTTPS is
turned on for it automatically. Behind NAT — AWS, Google Cloud, Oracle, a home
router — it finds the address the internet reaches the server on.

**Running it again is safe, and it is how you repair or upgrade.** It keeps the
address, the domain and the certificate the panel already has, and after setup
it prints where to sign in rather than a setup link that no longer works. Two
copies cannot run at once, and an interrupted install finishes when you start
it again. Everything is logged to `/var/log/skifity-install.log`, readable by
root alone.

<details>
<summary><b>What the server needs</b></summary>

| | Minimum | Comfortable |
|---|---|---|
| Operating system | Ubuntu 24.04 or Debian 12 | |
| Architecture | amd64 or arm64 | |
| Memory | 1 GB | 2 GB, or 1 GB with swap |
| Free disk | 8 GB | |
| Access | root over SSH | |
| Open ports | TCP 80 and 443 | |

A fresh install is deliberately small: Kubernetes and the panel, nothing else.
Certificates, the builder, the PostgreSQL operator and the rest install
themselves the first time you use them, and each one says what it costs first.

If your provider has a firewall of its own in front of the server, allow TCP 80
and 443 there too; the installer cannot see it.

</details>

<details>
<summary><b>Installer options</b></summary>

Options go after `sh -s --` when the installer is piped from `curl`, or after
`install.sh` when it is run from a clone. Each one has an environment variable
that does the same, for provisioning tools that prefer those.

| Option | Variable | What it does |
|---|---|---|
| `--domain NAME` | `SKIFITY_DOMAIN` | The domain the panel answers on, with HTTPS from Let's Encrypt |
| `--email ADDRESS` | `SKIFITY_ACME_EMAIL` | The contact address registered with Let's Encrypt |
| `--staging` | `SKIFITY_ACME_STAGING=1` | Let's Encrypt's staging server, for trying things out |
| `--public-ip ADDRESS` | `SKIFITY_PUBLIC_IP` | The address the server is reached on, when the one found is wrong |
| `--version TAG` | `SKIFITY_VERSION` | The release to install; defaults to the installer's own, and `latest` asks GitHub for the newest |
| `--image REFERENCE` | `SKIFITY_IMAGE` | A full image reference, for an image you built yourself |
| `--pod-network NAME` | `SKIFITY_POD_NETWORK` | `wireguard-native` or `vxlan`; chosen from the kernel by default |
| `--channel NAME` | `SKIFITY_CHANNEL` | The k3s channel; default `stable` |
| `--skip-k3s` | `SKIFITY_SKIP_K3S=1` | Use the k3s already installed here as it is |
| `--skip-firewall` | `SKIFITY_SKIP_FIREWALL=1` | Leave the host firewall alone |
| `--yes` | `SKIFITY_ASSUME_YES=1` | Never ask anything |

`--help` lists them without needing root. Every value is checked before
anything changes, so a typo is one line now rather than a broken certificate
later.

**Unattended**, from cloud-init, Terraform or a CI job, pass `--yes`. Without a
terminal and without `--yes`, the one question the installer may ask — whether
to carry on on a system it is not tested on — is answered no, not guessed:

```sh
# As root, from cloud-init's runcmd or a provisioning script:
curl -fsSL https://github.com/Skifity/Skifity/releases/latest/download/install.sh \
  | sh -s -- --yes --domain panel.example.com --email ops@example.com
```

When the server's own address is a private one, the installer asks
`api.ipify.org` (or `icanhazip.com`) once for the public one. `--public-ip`
skips that.

</details>

<details>
<summary><b>Upgrading and uninstalling</b></summary>

**Upgrade** from Settings: type the release to move to. The panel copies its
database first, off the server too when a backup bucket is set, and tells you
the one command that goes back. Your apps keep running throughout. Running the
install command again does the same from a shell — it fetches the newest
installer — and keeps everything the panel already has.

**One release exactly**, rather than the newest, is that release's own
installer: `https://github.com/Skifity/Skifity/releases/download/<tag>/install.sh`.

**Uninstall** with `skifity-uninstall`, which is conservative by default:

```sh
sudo skifity-uninstall            # remove the panel, keep k3s and your data
sudo skifity-uninstall --all      # remove k3s too, and everything on it
sudo skifity-uninstall --purge    # also delete the database and the master key
sudo skifity-uninstall --dry-run  # print what would happen, change nothing
```

`--purge` asks for a typed confirmation: without the master key, every stored
secret and every existing backup is unreadable for ever.

</details>

> [!NOTE]
> **Skifity has not yet been run against a real cluster**: the environment it
> was built in refuses privileged containers, so k3s could never start there. Everything that needs a cluster is tested against a
> fake API server, golden manifests and a real in-process SSH server instead,
> and the installer is run from start to finish against stand-ins for k3s,
> kubectl and the network. Try it on a spare VPS before you move production onto
> it, and [open an issue](https://github.com/Skifity/Skifity/issues) with
> whatever you find. More in [Status](#status).

## Features

**Several servers are one pool.** An app that wants three instances gets them
wherever there is room, and an app whose server dies is restarted on another
without anyone being woken up. Three control plane servers survive losing one.

**Adding a server is an IP address and a password.** Skifity connects, checks
the machine, installs a key of its own, configures the firewall, verifies the
network in both directions, installs k3s and joins the cluster — seven steps,
each shown as it happens. The password is used exactly once and never stored;
a test scans every column of the database for it. Or let it order the server
for you at Hetzner Cloud or DigitalOcean.

**Changing a setting does not rebuild your app.** What goes into the image is
kept apart from what the container reads at start-up, so changing a variable is
a rollout in seconds, and the panel says which kind you are changing before you
save.

**Rolling back restores the settings too**, not just the image. If a variable
broke the app, rolling back puts the old variable back.

**It checks before you scale.** Before a second instance, Skifity looks for
what would break — SQLite on a local disk, sessions in memory, a cron job that
assumes it is alone — and says how to fix it.

**Errors are written to be acted on.** Every failure, in the panel, the CLI, the
API and the MCP server, says what happened, what it means and how to fix it,
with a button that copies the whole thing for an AI assistant.

**Builds run unprivileged.** Images are built by rootless BuildKit in a
namespace of their own: no privileged container, no Docker socket, nothing that
hands a build the server it runs on.

**Secrets are sealed to where they are stored.** Envelope encryption with a
master key you can rotate without downtime, a recovery key shown once, and a
ciphertext that will not open if it is copied to another row. SSH host keys are
pinned the first time a server is seen.

**It never phones home.** No licence key, no telemetry, no update check. It
reaches the internet only for what you asked for: Let's Encrypt, your Git
provider, your backup bucket.

<table>
<tr>
<td width="33%" valign="top">

**Build and deploy**<br>
Dockerfile, Railpack or a prebuilt image; Compose files as a stack; web and
worker processes from one build; deploy on push, on a tag, or from a folder;
autoscaling and scale to zero.

</td>
<td width="33%" valign="top">

**Previews and environments**<br>
A preview per pull request, or of the whole environment with its own
databases and a seed step; promote the exact image from staging to production;
`skifity.yaml` with plan and apply.

</td>
<td width="33%" valign="top">

**Databases and backups**<br>
Nine engines, from PostgreSQL to ClickHouse; high-availability PostgreSQL;
encrypted backups verified by reading them back; `skifity db connect` to reach
one from your laptop.

</td>
</tr>
<tr>
<td valign="top">

**Domains and the edge**<br>
Automatic HTTPS, your own certificates, DNS records created at your provider,
redirects, a password in front of an app, maintenance mode, and a firewall by
address, country and network.

</td>
<td valign="top">

**Watching**<br>
CPU, memory, requests and errors over time; threshold alerts to ten kinds of
channel; log drains; image vulnerability scans; and changes made behind the
panel's back, noticed and put right.

</td>
<td valign="top">

**People and access**<br>
Roles down to read-only, members limited to some projects, single sign-on,
passkeys and two-factor, scoped API tokens, an audit log, external secret
managers.

</td>
</tr>
</table>

## Screenshots

Every picture is captured by a test against the real binary, not drawn.
[The tour](docs/tour.md) walks through them in order.

### Getting started

<table>
<tr>
<td width="33%"><img src="docs/images/setup.png" alt="First run: the setup token, an email address, a name, a password and a team name"><br><b>First run.</b> Only the token the installer printed can create the first account.</td>
<td width="33%"><img src="docs/images/recovery-key.png" alt="The recovery key, shown once, with a checkbox confirming it has been saved"><br><b>The recovery key</b>, shown once. It opens the encrypted settings if the master key is lost.</td>
<td width="33%"><img src="docs/images/sign-in.png" alt="The sign-in page with email, password and a single sign-on button"><br><b>Signing in</b>, with single sign-on when an identity provider is set.</td>
</tr>
</table>

### The overview, in any theme and five languages

<table>
<tr>
<td width="50%"><img src="docs/images/overview.png" alt="The overview: projects, apps, cluster health and recent activity"><br><b>Overview.</b> What is running, what changed, and what needs attention.</td>
<td width="50%"><img src="docs/images/overview-dark.png" alt="The same overview in dark mode"><br><b>Dark</b>, which is what most people will see.</td>
</tr>
<tr>
<td><img src="docs/images/overview-indonesian.png" alt="The overview in Indonesian"><br><b>Indonesian.</b></td>
<td><img src="docs/images/overview-russian.png" alt="The overview in Russian"><br><b>Russian.</b> A missing string fails the build rather than falling back to English.</td>
</tr>
</table>

### Projects and apps

<table>
<tr>
<td width="33%"><img src="docs/images/projects.png" alt="The projects list"><br><b>Projects</b> hold environments; environments hold apps and databases.</td>
<td width="33%"><img src="docs/images/project.png" alt="A project's canvas, showing its apps and how they connect"><br><b>A project</b>, drawn: what is linked to what.</td>
<td width="33%"><img src="docs/images/new-app.png" alt="The new app form: a Git repository, a branch and a builder"><br><b>A new app</b>, from Git, an image or a folder.</td>
</tr>
</table>

### One app, tab by tab

<table>
<tr>
<td width="33%"><img src="docs/images/app-overview.png" alt="An app's overview: its status, domains and recent deployments"><br><b>Overview.</b></td>
<td width="33%"><img src="docs/images/app-deployments.png" alt="The deployments list with commits and outcomes"><br><b>Deployments</b>, each with its commit and a one-click rollback.</td>
<td width="33%"><img src="docs/images/app-logs.png" alt="Live logs"><br><b>Logs</b>, streamed.</td>
</tr>
<tr>
<td><img src="docs/images/app-console.png" alt="The console: run a command once in the app's image, and scheduled commands"><br><b>Console.</b> A command run once in the app's image, and scheduled ones.</td>
<td><img src="docs/images/app-variables.png" alt="Environment variables, with secret values hidden"><br><b>Variables.</b> Changing one does not rebuild the image.</td>
<td><img src="docs/images/app-domains.png" alt="Domains, with certificate status"><br><b>Domains</b>, with a certificate as soon as DNS points here.</td>
</tr>
<tr>
<td><img src="docs/images/app-firewall.png" alt="The firewall tab: rules by address, country, network, path or header"><br><b>Firewall.</b> Rules by address, country, network, path or header.</td>
<td><img src="docs/images/app-scaling.png" alt="The scaling tab: autoscaling, targets, scale to zero, and what to fix first"><br><b>Scaling</b>, and what would break with a second instance.</td>
<td><img src="docs/images/app-storage.png" alt="Volumes and their backups"><br><b>Storage.</b> Volumes, and their backups.</td>
</tr>
<tr>
<td><img src="docs/images/app-settings.png" alt="App settings: resources, health path, builder"><br><b>Settings.</b> Resources, health checks, the builder.</td>
<td><img src="docs/images/app-advanced.png" alt="The Kubernetes objects this app becomes"><br><b>Advanced</b>, the only place the word Kubernetes appears.</td>
<td></td>
</tr>
</table>

### Databases, servers, templates and plugins

<table>
<tr>
<td width="33%"><img src="docs/images/databases.png" alt="The databases list"><br><b>Databases.</b> Nine engines, provisioned and backed up by the panel.</td>
<td width="33%"><img src="docs/images/servers.png" alt="The servers list"><br><b>Servers.</b></td>
<td width="33%"><img src="docs/images/add-server.png" alt="Adding a server: an IP address, a user and a key or password"><br><b>Adding one</b> is an address and a way in. The rest is automatic.</td>
</tr>
<tr>
<td><img src="docs/images/templates.png" alt="The template catalogue, grouped by category"><br><b>Templates.</b> 374 applications, every one on a version checked to exist.</td>
<td><img src="docs/images/plugins.png" alt="The plugins page: installed plugins, the store, or one from an address"><br><b>Plugins</b>, each in a container of its own with only the permissions it declared.</td>
<td><img src="docs/images/plugins-store.png" alt="An error explaining that the plugin store could not be reached, with what it means and how to fix it"><br><b>Errors explain themselves.</b> Here, the plugin store, which is not published yet.</td>
</tr>
<tr>
<td><img src="docs/images/activity.png" alt="The activity log"><br><b>Activity.</b> Who did what, and when.</td>
<td><img src="docs/images/account.png" alt="The account page: password, two-factor, sessions and API tokens"><br><b>Account.</b> Two-factor, sessions, and API tokens for the CLI.</td>
<td></td>
</tr>
</table>

### Settings

<table>
<tr>
<td width="33%"><img src="docs/images/settings-general.png" alt="General settings, grouped"><br><b>General</b>: domains, storage, DNS, email, the registry, the cluster, single sign-on.</td>
<td width="33%"><img src="docs/images/settings-git.png" alt="Git accounts and the GitHub App fields"><br><b>Git.</b> Connected accounts, or a GitHub App for an organisation.</td>
<td width="33%"><img src="docs/images/settings-notifications.png" alt="Notification channels"><br><b>Notifications.</b> Email, Slack, Discord, webhooks and more.</td>
</tr>
<tr>
<td><img src="docs/images/settings-components.png" alt="Cluster components and their status"><br><b>Components</b>, installed on first use rather than up front.</td>
<td><img src="docs/images/settings-plugins.png" alt="Plugin store settings: the index address and its signing key"><br><b>Plugins.</b> The store's address and the key its index is signed with.</td>
<td><img src="docs/images/settings-members.png" alt="Team members and their roles"><br><b>Members</b> and their roles.</td>
</tr>
<tr>
<td><img src="docs/images/settings-security.png" alt="Security: the recovery key and key rotation"><br><b>Security.</b> Rotating the master key without downtime.</td>
<td><img src="docs/images/settings-audit.png" alt="The audit log"><br><b>Audit log.</b></td>
<td><img src="docs/images/settings-indonesian.png" alt="The settings page in Indonesian"><br><b>Pengaturan</b> — the same page in Indonesian.</td>
</tr>
</table>

## The terminal and AI assistants

One binary is the panel, the CLI and an MCP server, all against the same API.

```sh
skifity login                     # your panel's address and a token
skifity deploy                    # deploy this directory
skifity logs --follow             # watch it
skifity env set LOG_LEVEL=debug   # change something, without a rebuild
skifity rollback                  # undo it
skifity db connect                # a private database, on localhost
```

```sh
claude mcp add skifity -- skifity mcp
```

The installer puts the CLI on the server. For your own computer, your panel
serves the one that matches it, for macOS, Linux and Windows:

```sh
# os=darwin, linux or windows; arch=arm64 or amd64
curl -fsS "https://panel.example.com/api/cli/download?os=darwin&arch=arm64" -o skifity
chmod +x skifity && sudo mv skifity /usr/local/bin/
```

Or take it from [the newest release](https://github.com/Skifity/Skifity/releases/latest),
where every binary comes with SLSA provenance that says which workflow built
it from which tag.

[`llms.txt`](llms.txt) describes the whole product on one page, and the panel
serves it at `/llms.txt`. Every route of the HTTP API is described in OpenAPI
3.1 at `/api/openapi.json`, and a test fails the build when a route is added
without it. See [the CLI guide](docs/cli.md).

## Five languages, properly

English, Indonesian, Hindi, Russian and Simplified Chinese, all complete. The
build fails if any string is missing from any language, if a plural form a
language needs is absent — Russian needs one, few and many — or if a
translation drops a placeholder. Dates, numbers and relative times are
formatted by the language, not translated around.

## 374 one-click applications

WordPress, Ghost, Gitea, GitLab, n8n, Vaultwarden, Metabase, BookStack, MinIO,
Chatwoot, Uptime Kuma and about three hundred and sixty more. Forty-six of them
install more than one app — a web app and its worker, a service and its search
index — which reach each other by name.

Every one of them **names a version**, fetched from its registry to prove it
exists. That matters the day you roll back: `latest` is not a version, so
restoring it restores a tag rather than the thing that worked. Installed apps
are offered their template's updates, backed up first.

A template is a YAML file in
[`internal/templates/catalogue`](internal/templates/catalogue), so adding one is
a pull request anybody can write, and `make check` says whether it is right.

## Documentation

Also served by the panel itself, at `/docs`, from inside the binary — which is
where you want it when the cluster is broken and the server has no way out to
the internet.

| | |
|---|---|
| [A look at the panel](docs/tour.md) | Every screen, captured from the real binary |
| [Quick start](docs/quick-start.md) | Empty server to a running app |
| [Concepts](docs/concepts.md) | What the words mean, and what they are underneath |
| [Adding servers](docs/adding-servers.md) | The seven steps, and what to do when one fails |
| [Templates](docs/templates.md) | What a one-click install does, and what it does not |
| [Backups](docs/backups.md) | What is backed up, where it goes, and restoring |
| [The CLI and AI assistants](docs/cli.md) | Terminal, API, MCP |
| [Troubleshooting](docs/troubleshooting.md) | When something is wrong |
| [Questions](docs/faq.md) | Including the ones with awkward answers |
| [Configuration](docs/configuration.md) | Every setting, and what to back up |
| [What it costs](docs/performance.md) | Memory and size, measured |

For people working on Skifity rather than running it:

| | |
|---|---|
| [Architecture](docs/architecture.md) | How it fits together |
| [Decisions](docs/decisions.md) | Why it is like this |
| [Progress](docs/progress.md) | Where the work stands, including what has never run |
| [The checklist](docs/checklist.md) | The eighteen things a self-hosted platform is judged on |
| [Releasing](docs/releasing.md) | Cutting a release, and what the tag does |
| [Contributing](CONTRIBUTING.md) | The rules that are load-bearing |
| [Security](SECURITY.md) | Reporting a vulnerability, and where the lines of trust are |

## Building it yourself

```sh
make build     # the frontend, then one binary with it inside
make check     # every linter, the tests, the vulnerability scan
make smoke     # end to end against a real panel
make image     # the panel's container image
```

Go 1.26 and Node 22. `CGO_ENABLED=0` everywhere, so a release binary is static
and runs on anything. To install an image you built yourself, run the installer
from inside the clone, which reads the manifests from disk:

```sh
sudo sh installer/install.sh --image ghcr.io/skifity/skifity:$(git describe --tags --always --dirty)
```

## Status

Two things, plainly.

**It has never met a real cluster.** Every release has static binaries for
Linux, macOS and Windows and a multi-architecture image at
`ghcr.io/skifity/skifity`. But the environment Skifity was built in refuses
privileged containers, so k3s could never start there. Everything that needs a
cluster is tested against a fake API server, golden manifests, a real
in-process SSH server and shell syntax checks — the same code paths, and not
the same thing as having run. [`docs/progress.md`](docs/progress.md) says
exactly what has and has not been executed, and
[`test/cluster/verify.sh`](test/cluster/README.md) is the run that would change
that. Try it on a spare VPS first.

**The plugin store is not published.** `plugins.skifity.com` does not resolve,
so the Store tab says it could not reach anything. A plugin is installed by
giving its address or pasting its manifest.

## Licence

Apache 2.0. See [LICENSE](LICENSE).
