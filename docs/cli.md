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

It asks for the panel's address and for a token, which you create in the panel
under Account, then Tokens. Or give both at once:

```sh
skifity login --url https://panel.example.com --token skf_...
```

In more than one team, it asks which one to use, or takes `--team` with the
team's name, slug or id; with nobody to ask it stores none, and each command
says which to name.

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
   it needs — a PostgreSQL, MySQL, MongoDB or Redis database and the like, or
   data kept in a file that every deploy would erase;
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

It holds no secrets and belongs in version control. `panel = "https://..."`
says which panel the app is on; a command run in the folder while signed in to
another panel is refused rather than sent to an app id that means nothing there.

Without the file, a command works out the app from the team: its one project,
that project's production environment, and the one app in it. When any of those
is not one, it says so and asks for `--app` or `--env` rather than guessing.

`init` also says whether pushes will deploy the app. A repository on a host the
team has connected gets a webhook by itself. One read without a connection — a
public repository, the quickest start — has deploy on push on and no webhook,
and `init` says so, with the app's settings address, where a Git connection is
chosen once the account is connected under **Settings → Git**. With `--json`,
the one document printed carries it as `webhook.reason_code: "no_connection"`.
See [I pushed and nothing happened](troubleshooting.md#i-pushed-and-nothing-happened).

## Everything else

```sh
skifity status                    # running? which version is live? what URL?
skifity status --all              # what needs attention across the team; exits 1 if anything is broken
skifity logs --follow             # live output
skifity events --warnings         # what Kubernetes said went wrong, explained
skifity events --db orders        # the same for a database
skifity drift                     # what somebody changed with kubectl, and who
skifity drift --repair            # put it back; no build, no deployment
skifity env list                  # variables
skifity env set LOG_LEVEL=debug   # set one; says whether it rebuilds
skifity env set A=1 B=2 C=3       # several, rolled out once
skifity env set --secret API_KEY=... # stored encrypted, never shown again
skifity env import .env           # every KEY=value line in a file, rolled out once
skifity env set STRIPE_KEY --from company-vault:shop#stripe_key  # read from a secret manager, never stored
skifity env refresh               # read those again; roll out, or rebuild, only what changed
skifity env rm LOG_LEVEL          # remove one
skifity secrets connections list  # the team's secret managers; add, limit, test, remove
skifity secrets connections limit company-vault --allow-path shop --allow-project shop  # what it may be used for
skifity scale --instances 3       # a fixed number
skifity scale --auto --max 5      # or automatically
skifity gpus --count 1            # an NVIDIA GPU for each instance; see gpus.md
skifity processes set worker -- bundle exec sidekiq  # a worker beside the app, same build
skifity logs --process worker     # and its output
skifity plan                      # what skifity.yaml would change; see below
skifity apply                     # and change it
skifity rollback                  # back to the previous version; says what it puts back, then asks
skifity rollback --yes            # the same without the question, for a script
skifity restart                   # replace the instances one at a time; nothing is rebuilt
skifity apps set --port 3000      # change a setting; see below
skifity apps rm web               # delete an app and its disks; asks for its name
skifity deploy --cancel           # stop the deploy under way; --cancel 12 stops number 12
skifity promote --to production   # run the version this app runs in production, unbuilt
skifity lock "incident 42"        # no deploys or rollbacks until...
skifity unlock                    # ...this
skifity upgrade                   # ask which release of the panel is the newest; changes nothing
skifity upgrade --latest          # move the panel to it, when it is newer (an administrator's)
skifity scan                      # known vulnerabilities in the image it runs
skifity scan --now                # scan it again, and wait for the report
skifity deploy --accept-vulnerabilities  # past the check for fixable criticals, when it is on; audited
skifity maintenance on "Back at 14:00" --allow-me  # visitors see a page; you still see the app
skifity maintenance off           # visitors see the app again
skifity run -- npm run migrate    # run a one-off command in the app's image
skifity open                      # print the URLs
skifity apps                      # everything in this environment
skifity servers                   # the machines
skifity servers create web-2 --provider hetzner --location fsn1 --type cx22  # order one at Hetzner Cloud and join it
skifity servers create web-3 --provider digitalocean --location fra1 --type s-2vcpu-4gb  # or at DigitalOcean
skifity cloud providers add --token-file hcloud.token  # connect a Hetzner Cloud project; `-` reads stdin
skifity cloud providers add --kind digitalocean --token-file do.token  # or a DigitalOcean account
skifity cloud providers           # the team's cloud connections; test NAME, remove NAME
skifity cloud providers update eu --token-file new.token  # a new token, while its servers stay; `-` reads stdin
skifity certs                     # the team's own TLS certificates, and what uses each
skifity certs add wildcard --cert fullchain.pem --key privkey.pem  # upload one, or a new version
skifity certs remove wildcard     # its hostnames go back to Let's Encrypt
skifity domains                   # the app's domains, and where each one's DNS record stands
skifity domains add shop.example.com  # its record is created when the team connected the zone
skifity domains add shop.example.com --manage-dns=false  # make the record yourself
skifity domains remove shop.example.com  # and the record the panel created with it
skifity dns providers             # the team's DNS providers and their zones
skifity dns providers add --kind cloudflare --credentials token.txt  # or --credentials - from standard input
skifity dns providers add --kind route53 --credentials ~/.aws/credentials
skifity dns providers test Cloudflare  # sign in again and list its zones
skifity dns providers update Cloudflare --credentials new-token.txt  # new credentials; its records stay kept
skifity dns providers remove Cloudflare  # the records it made stay at the provider
skifity drains                    # where the team's logs are shipped, and the collector
skifity drains add Axiom --kind axiom --set dataset=apps  # asks for the token; a test line is sent first
skifity drains add Loki --kind loki --set url=https://logs.example.com --secret-file password=./token  # from a file
skifity drains test Axiom         # send its test line again
skifity drains remove Axiom       # nothing more is sent to it
skifity git                       # the team's Git connections
skifity git repos acme-github     # what one can read; --search narrows it
skifity git branches acme-github acme/shop  # one repository's branches
skifity templates                 # what the team can install; --search narrows it, ROOT marks an image that starts as root
skifity templates catalogues      # the team's own template catalogues; add, refresh, remove
skifity db                        # the managed databases
skifity db connect orders         # reach one from this computer; see below
skifity export --out ./leaving    # the whole team, as JSON and Kubernetes YAML
skifity api spec                  # the HTTP API, described in OpenAPI; see below
```

Any command takes `--app <id>` to act on something other than the current
directory's app, and `--json` for output a script can rely on. The human format
is not stable; the JSON is.

`status --all`, or `attention`, is the overview's list of
[what needs attention](concepts.md#what-needs-attention) across the team, each
with the panel page to deal with it on. Its exit status is the answer to "is
anything broken?": 1 when anything on it is an error — a deploy or a backup
that failed, an app with no instance running — and 0 for warnings alone or
nothing, so a cron job can run it and mail on failure.

`export` is the one worth knowing about before you need it. It writes
`skifity-export.json` — every project, app, domain, database, variable and
schedule — plus `manifests/<namespace>/<app>.yaml` per app: the Deployment,
Service, Ingress, autoscaler, disruption budget, volume claims and scheduled
commands as plain Kubernetes objects. `kubectl apply -f` them on any cluster and
the apps run there without this panel. Secret values are not included, and the
README it writes says where they already are.

### Changing an app's settings

`skifity apps set` changes what the settings tab changes, one flag per setting,
and sends only the flags given:

```sh
skifity apps set --port 3000 --start "node server.js" --health /healthz
skifity apps set --build "npm run build" --output-dir dist --root apps/web
skifity apps set --memory-limit 1024 --cpu-request 250
skifity apps set --internal              # only the environment's other apps reach it
```

The others are `--name`, `--branch`, `--builder`, `--dockerfile`, `--image`,
`--release`, `--health-check http|tcp|none`, `--health-start`,
`--health-timeout`, `--cpu-limit`, `--memory-request` and `--run-as-user`;
`skifity apps set --help` lists them. It says when each change takes effect:
what the running app reads — the port, the commands, the health check, the
resources — is rolled out there and then, without a rebuild, and what goes into
the image — the branch, the folder, the builder, the build command — waits for
the next `skifity deploy`, which builds with it. So does the release command,
which only runs in a deploy. An app that has never been deployed gets
everything at its first.

`skifity promote --to production`, run where the staging app is, deploys the
version it runs — or `--deployment 12` — to the app of the same name in
production, without building it again. When production would build that image
differently, because its build-time variables differ, it stops and says why;
`--force` says it again and promotes anyway. An environment with no app of the
same name is said too, with the ones the app can go to.

`skifity rollback` reads the rollback's plan first and prints it: the version
running now, the one it goes back to, and each setting it puts back, from what
to what. On a terminal it then asks; `--yes` skips the question, and a script
with no terminal is not asked. `skifity apps rm` deletes an app with its disks,
so it asks for the app's name to be typed, and with no terminal it does nothing
without `--yes`. Ctrl-C while a deploy is followed stops the following, not the
deploy, and prints how to stop that: `skifity deploy --cancel`.

## Reaching a database

A managed database has no address outside the cluster, and that is on purpose:
nothing on the internet can try its password. It also means the SQL client on
your laptop cannot reach it either. `skifity db connect` lends it the panel's
reach, one connection at a time:

```sh
$ skifity db connect orders
orders (postgres) is reachable on 127.0.0.1:5432.
Connect with: postgres://orders:<password>@127.0.0.1:5432/orders
Every connection goes through the panel and is in the team's activity log. Ctrl-C closes the tunnel.
```

The connection string it prints has the real password in it, since that is what
a client needs; the example above hides it. Do not run it where the output is
kept, like a CI log.

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
* An open tunnel is asked again every minute whether it still may be: revoke the
  token, remove its owner from the team or make them a member, and their open
  connections close within the minute. None lasts more than 12 hours; the
  client's next connection opens a new one, as after a dropped connection.
* A client that finishes sending — `nc -N`, or a file piped into `psql` — still
  reads the database's answer: the end of one direction is passed on alone.
* A tunnel is an upgraded HTTP/1.1 connection. A panel reached directly, or
  through the ingress it installs, carries it; a proxy in between that does not
  pass upgrades through is the one thing that stops it, and the command says so.
  Cloudflare documents passing on WebSocket upgrades and no others, so a panel
  reached only through a Cloudflare Tunnel should be expected to refuse one;
  that has not been tried. Run the command from somewhere that reaches the
  panel's own address instead.

## Managing a database

The rest of a database's life is under `db` too, each naming the database as
`db connect` does:

```sh
skifity db stop orders                 # refused while apps use it, unless --force
skifity db start orders --wait
skifity db resize orders --memory-limit 2048 --storage 20
skifity db password orders             # a generated one; --password-stdin to choose
skifity db import orders ./shop.dump   # or - for standard input
pg_dump --format=custom shop | skifity db import orders - --wait
```

* `resize` takes `--cpu-request` and `--cpu-limit` in millicores,
  `--memory-request` and `--memory-limit` in MB and `--storage` in GB, and
  changes only what is given. A disk only grows.
* `password` never prints the new password unless `--show-password` is given,
  which waits for the change to finish and then reads it, audited as any read of
  it is. A password you choose comes from standard input, never from an argument
  that the shell's history would keep.
* `import` detects the format; `--format archive-gzip` is the one it cannot, for
  `mongodump --archive --gzip`. A backup is taken before anything is loaded.
* `--wait` waits for a start, a password change or an import to finish, and a
  failure is the panel's own explanation. [Databases](databases.md) says what
  each does.

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
    engine: postgres        # postgres, mysql, mariadb, mongodb, redis, valkey,
                            # dragonfly, clickhouse or memcached
    version: "17"
    storage: 10             # GB

apps:
  web:
    repo: https://github.com/acme/shop
    git: acme-github        # the team's Git connection: reads a private repository,
                            # and registers the webhook; given to an existing app too
    branch: main
    root: apps/web
    start: npm start
    release: npm run migrate
    port: 3000
    health: /healthz
    watch: [apps/web/**, packages/**]
    deploy_trigger: branch  # or tag: deploy only pushed tags matching tag_pattern
    tag_pattern: "v*"
    instances: 2            # or autoscale: {min: 1, max: 5, cpu: 70}
    resources: {cpu: 100, memory: 256}   # reserved: millicores, MB
    variables:
      LOG_LEVEL: info
      # The long form says the build reads it, as a front end's public
      # settings need; left out, a variable keeps what it is.
      NEXT_PUBLIC_API_URL: {value: https://api.example.com, build: true}
    secrets:                # values not in the file; each one is required
      STRIPE_KEY: {from: company-vault:shop#stripe_key}  # read from a secret manager
      SESSION_SECRET: {}    # set in the panel or with `skifity env set --secret`
      SENTRY_DSN: {optional: true}  # the app runs without it
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

  blog:
    image: example/blog:4.2.0
    databases:
      # The long form also gives the connection in pieces, for software that
      # has no setting for a URL. See databases.md#in-pieces.
      main: {url: BLOG_DATABASE_URL, host: DB_HOST, port: DB_PORT, name: DB_NAME, user: DB_USER, password: DB_PASSWORD}

  auth:
    image: quay.io/keycloak/keycloak:26.0
    port: 8080
    # The long form of health: the check (http, tcp or none), the path, and
    # seconds to start and to answer one check. `health: /path` is the same
    # as {path: /path}.
    health: {check: http, path: /health/ready, start: 600, timeout: 5}
```

```sh
$ skifity plan
Reading /home/you/shop/skifity.yaml.

  ~ web: scaling — 2 → 3
  ~ web: variable LOG_LEVEL — "info" → "debug"
  · web: secret SENTRY_DSN — is not set; set it with `skifity env set --secret SENTRY_DSN=...`
  ! web: secret SESSION_SECRET — is required and not set; set it with `skifity env set --secret SESSION_SECRET=... --app app_06gaqcxybny593h2s52m`, or mark it optional

0 to add, 2 to change. `skifity apply` makes these changes.
web: SESSION_SECRET is required and not set: apply changes nothing until it is.
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
  `secrets:` names the ones that are, as a list or as a map. A secret with
  `from` is read from one of the team's secret managers, written as
  `env set --from` takes it, and the file sets it: by reference, so its value
  is in neither the file nor the panel. The others are set in the panel or with
  `skifity env set --secret`.
* **A secret the file lists is required.** While an app that is already in the
  environment lacks one, `apply` changes nothing at all and stops with
  `blueprint.secrets_missing`, naming each secret and its app: any change to
  the app would roll it out without it. An app the file makes is made with
  everything else and not deployed; set the secret, and the next `apply` gives
  it its first deploy. `optional: true` marks one the app runs without: the
  plan says it is not set, and nothing waits for it.
* **A database's engine, version and size are not changed from it** — each of
  those is a migration of somebody's data. The plan says when the file and the
  database disagree.
* **Nor is where an app comes from.** A repository other than the app's, or an
  image for an app built from source, is said in the plan and changed on the
  app's page. An app that runs an image and has a new one in the file is
  deployed with it.
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
  done stays done and the next plan shows what is left — a new app's first
  deploy included, when the apply stopped before it.
* **What the file says is read the way the panel stores it:** a schedule such as
  `0 9 * * 1-5` matches the one the panel keeps, a domain is compared lowercase
  and without `https://`, and a root without its leading slash. A plan straight
  after an apply is empty.
* **Which variables the build reads is said or kept.** `build: true` in a
  variable's or a secret's long form makes it one the build reads, and
  `build: false` one read when the app runs. Left out, a variable the
  environment has keeps what it is, whatever the panel says, and a new one is
  read when the app runs. A new value of one the build reads builds the app
  again; any other change to a variable only rolls it out.

The file is read from the current directory or the nearest one above it;
`--file` names another, and `--env` an environment other than the one this
directory's `skifity.toml` points at. `skifity.toml` is which app this folder
is, on this computer; `skifity.yaml` is what an environment should be, for
everybody.

A misspelt field is an error rather than a setting quietly ignored, and every
problem in the file is reported at once. Names are lowercase letters, digits and
single hyphens — what the panel would make of `my_app` is `my-app`, so the file
has to say that — and an app and a database in one file need different ones.

## Recovering access

These run on the panel's own server and read its database directly, so they work
when nobody can sign in — which is the one thing the API cannot help with. They
need root.

```sh
sudo skifity admin list-users
sudo skifity admin reset-password you@example.com
sudo skifity admin reset-2fa you@example.com
```

Resetting a password signs out every device that was signed in as that account.
Two-factor authentication stays on, so you will still need your authenticator
app or a recovery code. Passkeys stay too, and the command says how many the
account has; add `--remove-passkeys` to remove them as well, when the reason for
the reset is that somebody else got in. See [Passkeys](configuration.md#passkeys).

`reset-2fa` is for a lost phone with the recovery codes gone too. It turns the
account's two-factor authentication off, deletes its recovery codes and signs
out every device, so the password signs in on its own; the account turns
two-factor on again under Account. It names any team that requires a second
factor, which refuses the account until then. See
[I have lost my authenticator](troubleshooting.md#i-have-lost-my-authenticator).

When the panel's only administrator has gone, or somebody who left can still
sign in, the accounts themselves can be changed from the server too:

```sh
sudo skifity admin make-admin someone@example.com
sudo skifity admin disable leaver@example.com
sudo skifity admin enable leaver@example.com
sudo skifity admin revoke-admin leaver@example.com
```

`disable` signs the account out everywhere and stops its password, passkeys,
single sign-on and API tokens until `enable`. The last enabled administrator is
kept: make somebody else one before disabling or demoting them. The panel does
the same under [Settings → Accounts](configuration.md#accounts).

Each of these writes what it did to the activity log of every team the account
is in, as `skifity admin`.

Two more work on the database itself:

```sh
sudo skifity admin backup-db /root/panel-backup.db
sudo skifity admin restore-db ./20260930-031700-bak-9f2c-panel.db.gz
```

`backup-db` takes a consistent copy while the panel runs. `restore-db` puts one
back — a copy from `backup-db`, or one the panel uploaded to the backup bucket —
and without `--yes` only checks it and says what it would do. See
[Backups](backups.md#putting-it-back).

The master key has its own way back. The recovery key the panel showed at setup
is read from standard input, so it stays out of the process list and the shell's
history:

```sh
read -rs KEY; printf %s "$KEY" | sudo skifity admin restore-key
```

`restore-key` tries the key on a secret in the panel's database first, and writes
nothing if it does not open it. A master key that is already there is only
replaced with `--yes`, and is kept beside the new one.

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
read-only token — **Access: read only** when you create it under Account, Tokens
— reads. A browser session is refused: the endpoint wants a token.

One tool is missing there: `deploy_folder` reads files on the computer the
server runs on, and over HTTP that is the panel's server. Deploy a folder with
`skifity up`, or with the local MCP server.

Every tool says whether it only reads and, if not, whether it can destroy
something — a deploy replaces what is running, a variable is overwritten, a
command does whatever it does — so an assistant's client can ask before those
and not before `list_apps`.

An assistant can take an app the whole way: install it from a template or
create it, give it a database, set its variables and files, fix its port, start
command, health check or memory with `update_app_settings`, restart it, cancel
a deploy, promote a version to another environment with `promote_app`, put it
on a domain or open a port, back it up and lock its deploys.
`rollback_app` says which settings it puts back, and with `dry_run` says only
that. Deleting an app is not a tool: its disks go with it for good, so the
person does it in the panel or with `skifity apps rm`. Three more things it
cannot do, on purpose. It is never shown a database's password: `link_database` gives the app
its connection string as a secret variable, and a password that has been in an
assistant's context is in its provider's logs for good. It cannot restore a
backup: a restore replaces everything with an older copy and cannot be undone,
so it is done in the panel, where it asks for a person's confirmation in words.
The assistant can list the backups and say which one. And it cannot upload or
remove the team's own TLS certificates: uploading one means handing over its
private key, for the same reason as the password, so it is done in the panel or
with `skifity certs`. `list_domains` still says which certificate each domain is
served with.

Asked whether anything is wrong, an assistant starts with `get_attention`: the
same list the overview leads with, errors first, each with a link to the page
it is dealt with on.

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

The main endpoints are listed in [llms.txt](../llms.txt), and every one of them
is described in [the OpenAPI description](#the-openapi-description). Lists come
back as `{"items": [...], "total": n}`, and every error has the same shape:

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

### The OpenAPI description

Every route is described in OpenAPI 3.1 at `/api/openapi.json`: its parameters,
the body it takes, what it answers, and which answers are streams. It needs no
token, because it says what the routes are and holds none of anybody's data.
Hand it to a client generator, import it into Postman or Bruno, or read it:

```sh
curl -fsS https://panel.example.com/api/openapi.json -o skifity-openapi.json
skifity api spec > skifity-openapi.json   # the panel you are signed in to
```

It describes the panel that serves it, and `info.version` says which version
that is, so generate a client from the panel it will talk to. The few actions
that want a person rather than a token — creating a token, turning two-factor
off, reading the recovery key, installing a plugin — list only the session
cookie under `security`.

It is kept by hand, in `internal/api/openapi.yaml` beside the router, and a test
walks the router and fails when a route is added, moved or removed without it,
so the description cannot fall behind the panel it ships in.
