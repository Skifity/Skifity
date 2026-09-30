<div align="center">

<img src="docs/images/logo.svg" width="72" height="72" alt="">

# Skifity

**Self-hosted apps, powered by Kubernetes.**

You give it a server. It gives you URLs.

</div>

---

```sh
curl -fsSL https://raw.githubusercontent.com/Skifity/Skifity/<version>/installer/install.sh | sudo sh
```

That is the whole installation. It checks the server, installs Kubernetes,
starts the panel, and prints a URL and a one-time token. Open the URL, create
your account, paste a Git repository, and you have an app on the internet. Add
a domain you own and HTTPS is turned on for it automatically; the free address
every app starts with is plain HTTP on purpose, for the reason in ADR-0015.

**There is no `<version>` to put in that URL yet**, because no release has been
tagged. The installer refuses before it changes anything rather than half
installing — see [Status](#status) for the two commands that do work today.

<p align="center">
  <img src="docs/images/overview-dark.png" alt="The overview, on a fresh install with no servers yet" width="820">
</p>

## What it is

Coolify and Dokploy run Docker, and their multi-server story is Docker Swarm,
which is in maintenance. Skifity runs Kubernetes, so several servers are one
pool: an app that wants three instances gets them wherever there is room, and an
app whose server dies is restarted elsewhere without anyone being woken up.

The panel itself costs **35 MiB of memory**. Coolify idles at 500 MB to 1.2 GB
and Dokploy at around 350 MB, because they run a web framework, a database, a
cache and a websocket server to be a panel. Skifity is one process.

What it does not do is make you learn Kubernetes. The panel talks about **Apps**,
**Instances**, **Servers**, **Domains** and **Databases**. The Kubernetes objects
are one click away under **Advanced**, and `kubectl` works normally — they are
simply not in the way.

## Adding a server is an IP address and a password

<p align="center">
  <img src="docs/images/add-server.png" alt="The add server form: address, username, password" width="820">
</p>

Skifity connects, checks the machine, installs a key of its own, configures the
firewall, verifies the network in both directions, installs k3s and joins the
cluster — seven steps, each one shown as it happens.

Your password is used exactly once, to install that key, and is never stored. A
test asserts it: every column of the database is scanned for it.

## What is different about it

**Changing a setting does not rebuild your app.** The single most common
complaint about panels of this kind. Skifity separates what goes into the image
from what the container reads at start-up, so changing a variable is a rollout
in seconds. The panel tells you which kind you are setting before you save, and
says afterwards whether it rebuilt.

**Rolling back restores the settings too**, not just the image. If a variable
broke the app, rolling back puts the old variable back.

**It checks before you scale.** Before running a second instance, Skifity looks
for what would break — SQLite on a local disk, sessions in memory, a cron job
that assumes it is alone — and says what would go wrong and how to fix it.

**Errors are written to be acted on.** Every failure, in the panel, the CLI, the
API and the MCP server, says what happened, what it means and how to fix it,
with a button that copies the whole thing for an AI assistant.

**It never phones home.** No licence key, no telemetry, no update check, no call
to anything of ours — there is nothing of ours to call. It does reach the
internet for things you asked for: Let's Encrypt for a certificate, GitHub for
the component you just turned on, your Git provider, your S3 bucket. The one
exception worth knowing is that the preflight asks a public-IP service what the
server's address is, because a server behind NAT cannot tell you itself.

## Built for the terminal and for assistants

One binary is the panel, the CLI and an MCP server.

```sh
skifity deploy                    # deploy this directory
skifity logs --follow             # watch it
skifity env set LOG_LEVEL=debug   # change something, without a rebuild
skifity rollback                  # undo it
```

```sh
claude mcp add skifity -- skifity mcp
```

[`llms.txt`](llms.txt) describes the whole product on one page, and the panel
serves it at `/llms.txt`. Every route of the HTTP API is described in OpenAPI
3.1 at `/api/openapi.json`, for a client generator, and a test fails the build
when a route is added without it. See [the CLI guide](docs/cli.md).

## Five languages, properly

<p align="center">
  <img src="docs/images/overview-russian.png" alt="The same overview in Russian" width="820">
</p>

English, Indonesian, Hindi, Russian and Simplified Chinese, all complete. A
build fails if any string is missing from any language, if a plural form a
language needs is absent — Russian needs one, few and many — or if a translation
drops a placeholder. Dates, numbers and relative times are formatted by the
language, not translated around.

## Sign in with the account people already have

OpenID Connect, against Okta, Entra ID, Authentik, Keycloak, Zitadel or Google:
PKCE, a verified ID token, a nonce per sign-in and a single-use state. Restrict
it to your own email domains, and decide whether a first sign-in creates an
account or is refused until somebody invites them.

An account made this way has no password, cannot be signed into with one, and is
refused in a way that does not tell an anonymous visitor which addresses use
single sign-on.

## What you need

A server with Ubuntu 24.04 or Debian 12, 1 GB of memory, 8 GB of free disk, and
root over SSH. Any VPS will do. 2 GB is comfortable.

A fresh install is deliberately small: Kubernetes and the panel, and nothing
else. The panel is **35 MiB of resident memory idle**, measured, and does not
drift. Certificates, the builder, the PostgreSQL operator and the rest install
themselves the first time you use them, and each one says what it costs first.

## 339 one-click applications

WordPress, Ghost, Gitea, GitLab, n8n, Vaultwarden, Metabase, BookStack, MinIO,
Chatwoot, Uptime Kuma and about three hundred and thirty more, grouped by what
they are for. Forty of them install more than one app — a web app and its
worker, a service and its search index — which land in the same environment and
reach each other by name.

Every one of them **names a version**, and every version was fetched from its
registry to prove it exists. That sounds like a detail until you roll back: an
image on `latest` is not a version, so restoring it restores a tag rather than
the thing that worked. More than half of the largest competing catalogue ships
`latest`.

A template is a file in [`internal/templates/catalogue`](internal/templates/catalogue),
not Go, so adding one is a pull request anybody can write and `make check` tells
you whether it is right.

## Documentation

Also served by the panel itself, at `/docs`, from inside the binary — which is
where you want it when the cluster is broken and the server has no browser and
no way out to the internet.

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

Those eleven are the ones the panel serves. The rest are for people working on
Skifity rather than running it, so they stay in the repository:

| | |
|---|---|
| [The checklist](docs/checklist.md) | The eighteen things a self-hosted platform is judged on, and which of them have a test that runs |
| [Architecture](docs/architecture.md) | How it fits together |
| [Decisions](docs/decisions.md) | Why it is like this |
| [Progress](docs/progress.md) | Where the work stands, including what has never run |
| [Contributing](CONTRIBUTING.md) | The rules that are load-bearing |
| [Security](SECURITY.md) | How to report a vulnerability, what is supported, where the lines of trust are |

## Building it yourself

```sh
make build     # the frontend, then one binary with it inside
make check     # every linter, then the tests
make smoke     # end to end against a real panel
```

Go 1.26 and Node 22. `CGO_ENABLED=0` everywhere, so a release binary is static
and runs on anything.

## Status

Honest version, two parts.

**Nothing is published.** No release has been tagged, so nothing has been
pushed to `ghcr.io/skifity/skifity` and there is no version to put in the
one-line install above. The installer knows: it names the release it installs in
its own source, and refuses before touching the machine while that is empty.
`plugins.skifity.com` does not resolve either, so the panel's plugin store reads
an index that is not there yet; a plugin is installed by giving its address.

What works today is two commands:

```sh
make image
sudo SKIFITY_IMAGE=ghcr.io/skifity/skifity:$(git describe --tags --always --dirty) \
  sh installer/install.sh
```

Run from inside the clone, the installer reads `deploy/*.yaml` from disk, so
nothing is fetched. [Quick start](docs/quick-start.md) has the rest.

**Nothing has run against a real cluster.** The environment this was built in
refuses privileged containers, so k3s could never be started in it. Everything
that needs a cluster is tested against a fake API server, golden manifests, a
real in-process SSH server and shell syntax checks — which exercises the same
code path and is not the same thing as having run.
[`docs/progress.md`](docs/progress.md) says exactly what has and has not been
executed.

Treat it as something to try on a spare VPS, not something to move production
onto this afternoon.

## Licence

Apache 2.0. See [LICENSE](LICENSE).
