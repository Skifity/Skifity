# The CLI, the API and AI assistants

Three ways in, one API behind all of them. Anything the panel can do, a terminal
or an assistant can do.

## Getting it

The installer puts it on the server it installs. Anywhere else, the panel
serves it — for your computer, not only for the server the panel runs on:

```sh
# macOS (Apple silicon; arch=amd64 for an Intel Mac)
curl -fsS "https://panel.example.com/api/cli/download?os=darwin&arch=arm64" -o skifity
chmod +x skifity

# Linux
curl -fsS "https://panel.example.com/api/cli/download?os=linux&arch=amd64" -o skifity
chmod +x skifity
```

```powershell
# Windows
curl.exe -fsS "https://panel.example.com/api/cli/download?os=windows&arch=amd64" -o skifity.exe
```

The panel's **New app → A folder on my computer** shows these with your panel's
address and your computer's platform already filled in.

With no platform named it is the binary the panel is itself running, which is
always the same version as the panel — a CLI a release behind its panel is a
confusing afternoon. The other platforms are built into the panel's image
beside it, from the same commit, kept gzipped and unpacked as they are
downloaded; they add about 90 MB to the image, and `docker build --build-arg
CLI_PLATFORMS=""` leaves them out. `/api/meta` lists what a panel can serve
(`cli_platforms`), and anything else is refused with a reason rather than
handed over as a file that will not run. The panel, the CLI and the MCP server
are one binary, so `make build` in the repository produces all three too.
## Signing in

```sh
skifity login
```

It asks for the panel's address and opens a browser to create a token, or takes
one you already have:

```sh
skifity login --url https://panel.example.com --token skf_...
```

The token is stored in your user configuration directory, never in a project, so
it cannot be committed by accident.

### Without signing in

For CI, a container, or an assistant's sandbox, set two environment variables
and nothing has to be signed in at all:

```sh
export SKIFITY_URL=https://panel.example.com
export SKIFITY_TOKEN=skf_...
skifity apps
```

They override a stored configuration, so setting them is never silently ignored.
If your account is in more than one team, add `SKIFITY_TEAM`; with one team the
CLI works it out.

Create tokens in the panel under **Account → API tokens**. A token cannot do
more than the person who created it.

## Deploying a folder

No repository, no Dockerfile, no settings. (The panel does the same without a
terminal: **New app → A folder on my computer**.) In the folder of an app — one
you wrote, or one an assistant wrote for you:

```sh
skifity up
```

The first time, that:

1. reads the folder the way the panel reads a repository, and says what it is:
   the framework, whether it is a website that has to be built first, and what
   it needs — a PostgreSQL, MySQL or Redis database, or data kept in a file
   that every deploy would erase;
2. creates the app, named after the folder, with the databases it needs already
   made and connected, so its first start finds them;
3. offers to set the values in your `.env` on the app. The file itself is never
   sent; its values are stored the way every variable is, and anything that
   looks like a key or a password is stored as a secret;
4. sends the folder, deploys it, shows the build as it happens, and prints the
   address.

It writes `skifity.toml` in the folder, so the next `skifity up` there updates the
same app. That one only sends and deploys. The same code sent twice is the same
upload, and the panel uses the image it already built instead of building again.
Running it from a subfolder sends the whole app, not the corner of it you are in.

### What is sent, and what is not

Left out, always: `.git`, `node_modules`, every `.env` file except the templates
(`.env.example`, `.env.sample`, `.env.template`), and `skifity.toml` itself.
Left out unless you say otherwise: caches and build environments such as
`.next`, `__pycache__`, `.venv` and `.cache`. Then whatever your `.gitignore`
lists, read the way git reads it, in every folder. A `.skifityignore` beside it
adds to it, for what is in git but should not be deployed:

```
# .skifityignore
fixtures/
*.psd
```

A line starting with `!` brings something back, except the ones that are always
left out. Links are never sent, and are named when they are skipped.

The panel takes up to 100 MB compressed and 1 GB unpacked. Source code is almost
never close; when a folder is, the command names its largest parts, which is
usually where a dependency folder or a database file is hiding.

### Options

```sh
skifity up ./my-app          # a folder other than this one
skifity up --dry-run         # say what would be sent and created, send nothing
skifity up --dotenv          # set the .env's values without asking
skifity up --dotenv=false    # and never offer
skifity up --no-database     # do not create the databases it seems to need
skifity up --name shop       # the app's name, the first time
skifity up --new             # a new app, even if this folder has one
skifity up --follow=false    # start the deploy and return
```

Without a terminal — in CI, or when an assistant runs it — nothing is asked, and
the `.env` is only sent with `--dotenv`. Assistants connected through
`skifity mcp` have the same thing as the `deploy_folder` tool, which tells them
to ask you before sending your `.env`.

## Deploying from a repository

In the directory of the thing you want to deploy:

```sh
skifity init      # writes skifity.toml
skifity deploy
```

`skifity.toml` records which app this directory is, so the commands below need
no arguments:

```toml
app = "app_06gaqcxybny593h2s52m"
name = "web"
environment = "env_06gaqcxy21mdqsdb1z7g"
```

It holds no secrets and belongs in version control.

## Everything else

```sh
skifity status                    # running? how many instances? what URL?
skifity logs --follow             # live output
skifity env list                  # variables
skifity env set LOG_LEVEL=debug   # set one; says whether it rebuilds
skifity env set A=1 B=2 C=3       # several, rolled out once
skifity env set --secret API_KEY=... # stored encrypted, never shown again
skifity env import .env           # every KEY=value line in a file, rolled out once
skifity env rm LOG_LEVEL          # remove one
skifity scale --instances 3       # a fixed number
skifity scale --auto --max 5      # or automatically
skifity processes set worker -- bundle exec sidekiq  # a worker beside the app, same build
skifity logs --process worker     # and its output
skifity plan                      # what skifity.yaml would change; see below
skifity apply                     # and change it
skifity rollback                  # back to the previous version
skifity lock "incident 42"        # no deploys or rollbacks until...
skifity unlock                    # ...this
skifity maintenance on "Back at 14:00" --allow-me  # visitors see a page; you still see the app
skifity maintenance off           # visitors see the app again
skifity run -- npm run migrate    # run a one-off command in the app's image
skifity open                      # print the URLs
skifity apps                      # everything in this environment
skifity servers                   # the machines
skifity db                        # the managed databases
skifity db connect orders         # reach one from this computer; see below
skifity export --out ./leaving    # the whole team, as JSON and Kubernetes YAML
```

Any command takes `--app <id>` to act on something other than the current
directory's app, and `--json` for output a script can rely on. The human format
is not stable; the JSON is.

`export` is the one worth knowing about before you need it. It writes
`skifity-export.json` — every project, app, domain, database, variable and
schedule — plus `manifests/<namespace>/<app>.yaml` per app: the Deployment,
Service, Ingress, autoscaler, disruption budget, volume claims and scheduled
commands as plain Kubernetes objects. `kubectl apply -f` them on any cluster and
the apps run there without this panel. Secret values are not included, and the
README it writes says where they already are.

## Reaching a database

A managed database has no address outside the cluster, and that is on purpose:
nothing on the internet can try its password. It also means the SQL client on
your laptop cannot reach it either. `skifity db connect` lends it the panel's
reach, one connection at a time:

```sh
$ skifity db connect orders
orders (postgres) is reachable on 127.0.0.1:5432.
Connect with: postgres://orders:...@127.0.0.1:5432/orders
Every connection goes through the panel and is in the team's activity log. Ctrl-C closes the tunnel.
```

Point `psql`, TablePlus, DBeaver or a migration tool at that address. Each
connection the client makes is carried through the panel over HTTPS to the
database and back; nothing else about the database changes, and when you stop the
command the port is gone.

* With one database in the environment, `skifity db connect` needs no name. With
  several it lists them; `skifity db` shows them too.
* The port is the database's own when that is free on your computer, so a
  client's defaults work, and any free one otherwise. `--port 6543` asks for one.
* It listens on `127.0.0.1` only. Somebody else on your network cannot use your
  tunnel.
* It needs an admin's token, the same as reading the password does, because a
  connection with that password can read and change everything in the database.
  A read-only token cannot open one. Each connection is recorded in the activity
  log as *Database tunnel opened*.
* A tunnel is an upgraded HTTP/1.1 connection. A panel reached directly, or
  through the ingress it installs, carries it; a proxy in between that does not
  pass upgrades through is the one thing that stops it, and the command says so.

## Describing an environment in a file

The settings that make an app work — its start command, its variables, the
worker beside it, the database it reads — can live in the repository, in a
`skifity.yaml`, and be reviewed in a pull request with the code that needs
them. `skifity plan` says what the file would change; `skifity apply` changes
it.

```yaml
# skifity.yaml
databases:
  main:
    engine: postgres        # postgres, mysql or redis
    version: "17"
    storage: 10             # GB

apps:
  web:
    repo: https://github.com/acme/shop
    branch: main
    root: apps/web
    start: npm start
    release: npm run migrate
    port: 3000
    health: /healthz
    watch: [apps/web/**, packages/**]
    instances: 2            # or autoscale: {min: 1, max: 5, cpu: 70}
    resources: {cpu: 100, memory: 256}   # reserved: millicores, MB
    variables:
      LOG_LEVEL: info
    secrets: [STRIPE_KEY]   # named here, set in the panel
    processes:
      worker: npm run worker
      clock: {command: npm run clock, instances: 1}
    domains: [shop.example.com]
    schedules:
      nightly: {schedule: "0 3 * * *", command: npm run report}
    databases:
      main: DATABASE_URL
    preview_seed: npm run seed

  cache:
    image: valkey/valkey:8
    port: 6379
    internal: true
```

```sh
$ skifity plan
Reading /home/you/shop/skifity.yaml.

  ~ web: scaling — 2 → 3
  ~ web: variable LOG_LEVEL — "info" → "debug"
  · web: secret STRIPE_KEY — is not set; set it with `skifity env set --secret STRIPE_KEY=...`

0 to add, 2 to change. `skifity apply` makes these changes.
```

What it does, and does not:

* **A field left out is left alone.** The file says what it wants, not
  everything there is: an app with only `instances: 3` has its instances
  changed and nothing else.
* **Nothing is deleted.** An app, a database or a process the environment has
  and the file does not is listed with a `·` and left as it is. Removing a line
  from a file is a smaller act than removing a database from a cluster, and the
  two are not one step.
* **Variables in it are not secrets**, because the file is in a repository.
  `secrets:` names the ones that are; their values are set in the panel or with
  `skifity env set --secret`, and the plan says which are missing.
* **A database's engine, version and size are not changed from it** — each of
  those is a migration of somebody's data. The plan says when the file and the
  database disagree.
* **The order is worked out:** databases before the apps linked to them, an app
  before its variables, and a new app's first deploy last, so it never starts
  without what the file gives it. A change to an existing app rolls out the way
  the same change in the panel does.
* **An app needs a `repo` or an `image` to be made from the file.** A folder sent
  with `skifity up` has neither: send it once, and the file describes it from
  then on.
* **It goes through the API**, with your token: a viewer can `plan` and cannot
  `apply`, every change is checked as it would be in the panel, and each is in
  the activity log under your name. If one fails, apply stops there; what was
  done stays done and the next plan shows what is left.

The file is read from the current directory or the nearest one above it;
`--file` names another, and `--env` an environment other than the one this
directory's `skifity.toml` points at. `skifity.toml` is which app this folder
is, on this computer; `skifity.yaml` is what an environment should be, for
everybody.

A misspelt field is an error rather than a setting quietly ignored, and every
problem in the file is reported at once.

## Recovering access

These run on the panel's own server and read its database directly, so they work
when nobody can sign in — which is the one thing the API cannot help with. They
need root.

```sh
sudo skifity admin list-users
sudo skifity admin reset-password you@example.com
```

Resetting a password signs out every device that was signed in as that account.
Two-factor authentication stays on, so you will still need your authenticator
app.

Two more work on the database itself:

```sh
sudo skifity admin backup-db /root/panel-backup.db
sudo skifity admin restore-db ./20260930-031700-bak-9f2c-panel.db.gz
```

`backup-db` takes a consistent copy while the panel runs. `restore-db` puts one
back — a copy from `backup-db`, or one the panel uploaded to the backup bucket —
and without `--yes` only checks it and says what it would do. See
[Backups](backups.md#putting-it-back).

## AI assistants

The same binary is an MCP server:

```sh
skifity mcp
```

Point your assistant at it. In Claude Code:

```sh
claude mcp add skifity -- skifity mcp
```

It needs credentials the same way the CLI does: either `skifity login` first, or
`SKIFITY_URL` and `SKIFITY_TOKEN` in its environment.

### Without installing anything

The panel serves the same MCP server itself, at `/api/mcp`, for an assistant
that cannot run the binary — a hosted one, or a teammate's editor. Create an API
token under Account and give it as a header:

```sh
claude mcp add --transport http skifity https://panel.example.com/api/mcp \
  --header "Authorization: Bearer skf_..."
```

It is streamable HTTP and stateless: every request carries the token and is
checked on its own, like any other API request, and each tool call is the same
API requests the CLI would make with that token. So a viewer's token reads and
changes nothing, a token limited to one project reaches that project, and a
token scoped to `read` reads. A browser session is refused: the endpoint wants a
token.

One tool is missing there: `deploy_folder` reads files on the computer the
server runs on, and over HTTP that is the panel's server. Deploy a folder with
`skifity up`, or with the local MCP server.

Every tool says whether it only reads and, if not, whether it can destroy
something — a deploy replaces what is running, a variable is overwritten, a
command does whatever it does — so an assistant's client can ask before those
and not before `list_apps`.

The tools are listed in [llms.txt](../llms.txt). They are named for what someone
would ask for — `get_app_logs`, `check_scaling_readiness`, `rollback_app` — and
every error comes back as a cause, an impact and a suggested fix rather than a
status code, because an assistant acting on "the build ran out of memory; add a
server or raise the build memory limit" does something useful, and one acting on
"500" guesses.

### What to give an assistant

Point it at `llms.txt` on your panel, at `/llms.txt`. It describes the whole
product on one page: the model, the tools, the endpoints, and the handful of
things that are not obvious, such as which variables cause a rebuild.

## The HTTP API

`https://<panel>/api`, with `Authorization: Bearer <token>`.

The endpoint list is in [llms.txt](../llms.txt). Lists come back as
`{"items": [...], "total": n}`, and every error has the same shape:

```json
{
  "error": {
    "code": "deploy.build_failed",
    "title": "The build failed",
    "cause": "...",
    "impact": "...",
    "fix": "...",
    "retryable": true
  }
}
```

Live updates come from server-sent events:

```sh
curl -N -H "Authorization: Bearer $SKIFITY_TOKEN" \
  "$SKIFITY_URL/api/events?topics=team:team_06gaqcvks6teywnwmw1p"
```

Topics are `team:<id>`, `operation:<id>` and `deployment:<id>`. The browser
reconnects and resumes from the last event it saw, so a dropped connection does
not lose the middle of a build log.
