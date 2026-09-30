# Concepts

Skifity deliberately uses ordinary words. This page says what each one means,
and what it is underneath, so that nothing is a mystery when you need to look.

## The words

| Skifity says | It means | Underneath |
|---|---|---|
| **Team** | Who can see and change things | A row, not a Kubernetes object |
| **Project** | Things that belong together | A group of namespaces |
| **Environment** | Production, staging, a preview | A Namespace |
| **App** | One thing you deployed | A Deployment, a Service and an Ingress |
| **Instance** | One running copy of an app | A Pod |
| **Server** | A machine you own | A Node |
| **Domain** | An address that reaches an app | An Ingress rule and a Certificate |
| **Database** | A managed PostgreSQL, Redis or MySQL | A Cluster or a StatefulSet |
| **Variable** | Something your app reads from the environment | A key in a Secret |
| **Volume** | Storage that survives a restart | A PersistentVolumeClaim |
| **Deployment** *(the noun in the history)* | One attempt to run a new version | A build, then a rollout |

Every app's **Advanced** tab shows the exact objects the panel applies. Nothing
is hidden; it is only kept out of the way.

## Projects and environments

A project is a box. Inside it are environments, and inside those are apps and
databases.

Every project gets a **Production** environment when it is created. Add more for
staging, or let Skifity create one per pull request.

Environments are isolated from each other: each is a namespace with
default-deny networking, so an app in staging cannot reach production's database
by accident.

### How strictly an environment confines its apps

Each environment chooses one of two levels, and **Strictest** is the default.

**Strictest** refuses a container whose image starts as root. That is the right
answer for an app Skifity builds from your code, because the builder produces an
image that already runs as an ordinary user.

It is the wrong answer for a great many off-the-shelf images. WordPress,
Nextcloud, MediaWiki and phpMyAdmin all start as root and drop privileges
themselves, which is an ordinary and long-standing thing for a container to do,
and this level has no way to permit it. An app like that never starts, and the
panel says exactly that on the app's page rather than leaving you with the
kubelet's wording.

**Accepts root images** is the other level, chosen per environment under the
project. It permits that one thing and nothing else. Still refused, at both
levels:

* a privileged container;
* the server's network, process list or paths — an app cannot see or mount
  anything belonging to the machine it runs on;
* any capability beyond the set every container gets from the runtime;
* gaining privileges the process did not start with.

Two things stay true whichever level an environment is on. An app **Skifity
built from your code** is held to the strict rules either way: lowering an
environment so a third-party image can run is not a reason to stop checking the
one image whose contents are known. And the namespace keeps *recording* at the
strict level even when it stops *enforcing* it, so the cluster's own audit log
still lists everything the strict profile would have refused.

Changing the level changes the environment's namespace immediately, and reaches
each app the next time it is deployed.

## Variables, and why some rebuild and some do not

An app's variables become environment variables inside it. There are two kinds,
and the difference matters:

* A **runtime variable** is read by your app when it starts. Changing one
  restarts the app with the new value. That takes seconds, and no rebuild.
* A **build-time variable** is baked into the image, which front-end builds do
  with things like a public API URL. Changing one means the image is wrong, so
  the app is rebuilt.

Skifity works this out from a fingerprint of everything that actually affects
the image. The panel tells you which kind you are setting before you save it,
and says afterwards whether it rebuilt.

How a build-time variable reaches the build depends on how the app is built,
and none of them writes its value anywhere a person can read it afterwards —
not into the build's own Kubernetes objects, which anybody allowed to list them
could read:

* **Detected builds (Railpack)** receive each one as a BuildKit secret, which is
  what Railpack reads. The name goes into the build plan; the value does not.
* **A Dockerfile** receives each one as a build argument, for its `ARG` lines,
  and as a secret it can mount with `RUN --mount=type=secret,id=NAME`. Prefer
  the second for anything secret: a value an `ARG` uses in a `RUN` step is
  recorded in the image's history, which is how Docker works rather than
  something Skifity can change.
* **A front end served as a static site** (Vite, Astro and the like) sees them
  while it builds, which is when `VITE_API_URL` and friends are read. They are
  declared in the build stage only, and the image that is served does not keep
  them.

A variable a front end reads at build time ends up in the JavaScript sent to
every visitor, whatever it is marked. Keep secrets out of those.

This is the thing most often complained about in other panels, where changing
any setting triggers a ten-minute rebuild. Here it does not.

### What every app is given

Beside its own variables, every app can read these, and so can its scheduled
and one-off commands:

| Variable | What it holds |
|---|---|
| `PORT` | The port it should listen on, when it has one. |
| `SKIFITY_APP` | The app's name as a slug, such as `web`. |
| `SKIFITY_ENVIRONMENT` | The environment's, such as `production` or `pr-12`. |
| `SKIFITY_URL` | The address it is reached at: a domain of your own when it has one, otherwise the one it was given. A preview's changes with every pull request, which is what an app needs for a sign-in callback or a link in an email. |
| `SKIFITY_COMMIT_SHA` | The commit the running version was built from, for an app from a repository. |
| `SKIFITY_PREVIEW` | `true` in a preview, and unset otherwise. |
| `SKIFITY_PULL_REQUEST` | The pull request's number, in a preview of one. |
| `SKIFITY_PROCESS` | In one of the app's [processes](#processes), its name, such as `worker`; unset in the app itself. |

The `SKIFITY_` prefix is the panel's: a variable of your own with one of these
names is replaced by the panel's value. A value that is not known is left unset
rather than empty.

## Deployments and rollback

Each deployment records the image it produced *and* the settings it ran with:
the instance count and autoscaling, the resources, the port, the health check
and the start command.

Rolling back puts back that image and those settings. It does **not** put back
variables, domains or disks: those stay as they are now, because they are not
the version's to take back — a variable changed since may be a rotated key, a
domain added since may be one customers already use. If a variable change broke
the app, change the variable back.

Before anything happens, the rollback dialog shows what it would change: the
version running now, the one it goes back to, and each setting it would put
back, from what to what. `GET /api/apps/{app}/rollback/{deployment}/plan` is the
same answer for a script or an assistant.

**Locking deploys.** During an incident, a migration somebody is running by
hand, or a freeze before a launch, **Lock deploys** on the app's page — or
`skifity lock "why"` — stops every deploy and rollback of it, from the panel,
the CLI, an assistant or a push, until somebody unlocks it. The app's page says
who locked it, when and why, and a push to a locked app is skipped with that
reason rather than failed. Changing a variable or the instance count still
applies: those are not new code, and an incident is often when they are needed.

**How far back you can go.** The panel keeps a long list of deployments and the
registry keeps the images for the last ten of them; older images are removed so
the disk does not fill. The list says which versions can still be rolled back
to, and the older ones say "image removed" where the button would be. To go back
further, deploy that commit again — it builds the same code fresh.

### Promoting a version

An app of the same name in two environments of a project — `web` in staging
and `web` in production — is the same thing at two stages. **Promote** on a
version that deployed in one runs that exact image in the other, without
building it again: what was tested is what ships, and a dependency that would
have resolved differently on a second build does not get the chance.

The app it is promoted to keeps its own variables, domains, disks and scaling;
only the image and the commit it came from move. Its release command runs, as
for any deploy.

An image carries what it was built with, build-time variables included. If
the app it is promoted to would have built it differently — a
`NEXT_PUBLIC_API_URL` that is staging's in the image and production's in the
app — the promotion stops and says so, because the image would run code built
for the other environment. **Promote anyway** runs it as it was built; deploying
normally builds it with the app's own. Make those variables runtime ones, or
the same in both, and promotion is always the same build.

Only a version whose image is still kept can be promoted, the same ten as for a
rollback. `POST /api/apps/{app}/promote` with the version's `deployment_id` is
the same for a script; `GET` lists where an app's versions can go.

## Release command

A command that runs after the image is built and before any traffic reaches the
new version. It is where a database migration belongs.

```
npm run migrate
```

Set it on the app's **Settings** tab. Every deployment runs it, in the app's own
image with the app's own variables, while the previous version keeps serving. If
it fails, the deployment stops there: the new code never sees the old schema,
and nothing changed for your users.

For something you want to run once rather than on every deploy — a backfill, a
console, a look at the data — use a one-off command instead:

```
skifity run --app app_123 -- npm run backfill
```

That runs the same way, in the same image, with the same variables, and its
output comes back as it happens. It is a Job of its own rather than a shell into
a running instance: a migration usually needs to run when the app is not up,
which is exactly when there is nothing to attach to.

## Scheduled commands

A nightly report, an hourly cleanup, a weekly digest. Add one on the app's
**Console** tab with a name, a five-field cron schedule and a command:

```
nightly report    0 3 * * *    npm run digest
```

Schedules are in UTC, because a cluster's idea of local time is not something
anybody chose. Each runs in the app's image, with the app's variables, and is
applied with the app — so a nightly job always runs the version that is
deployed rather than whatever it was when the schedule was written.

Kubernetes does the scheduling, not the panel. A panel that is restarting at
three in the morning is not a reason for a job to be skipped. A job that is
still running when the next one is due does not start a second copy.

## What the panel works out for you

When you paste a repository address, **Check this repository** reads its file
list through your Git provider's API — two requests, no clone — and says what it
thinks it is: "This looks like Next.js", with the port it expects and the
reasons it decided that.

It fills in only the fields you have left empty, and only when the answer came
from a file the repository's author put there, like a `package.json` naming a
framework. A guess from a file extension is shown as a question and fills in
nothing. It never overwrites something you typed.

You do not have to use it. The zero-config builder does its own detection inside
the build, so a repository deploys whether or not you press the button; this is
about the panel being able to say something while you are still looking at the
form.

A private repository needs a connected Git account with access to it. The token
is only ever sent to the host that account is for.

### What your app needs to run

The same check reads what the app will reach for once it is running, and says so
before the first deploy rather than after it crashes.

**A database.** If the code uses a database — a `pg` or `mysql2` package, an
`asyncpg` in `requirements.txt`, a Prisma schema that says
`provider = "postgresql"` — the form offers to create one and connect it. It is
ticked already. Leave it ticked and the panel makes the database, gives your app
its address, and only then starts the first deploy, so the app never starts once
without it. The name it is given is the one your code reads: from the Prisma
schema, or from your `.env.example`, and otherwise the usual one for that
database. A new database takes about a minute to start, so the app may restart
once while it waits.

A database this panel does not run, such as MongoDB, is named rather than left
out: use a hosted one and set its address under Variables.

**Data kept in a file.** SQLite, `lowdb` and a committed `.sqlite` file all keep
data inside the container, and the next deploy replaces the container. Nothing
fails and nothing is logged — the data is simply gone. The form says so in
amber. Use a managed database instead, or add a volume for the folder the data
is in.

SQLite beside a real database is not warned about. That is almost always SQLite
for development and the real one in production, which is what a Rails
`Gemfile` does by default.

**Settings.** If the repository has a `.env.example`, the form lists what it
expects and shows which are still missing. Paste your `.env` into the box and
they are saved before the first deploy. Anything that looks like a key, a token
or a password — including a database address with a password in it — is stored
as a secret: it is never shown again, and it is not handed to the MCP server.

Everything this says comes with the file it read it from, so you can check it.
The panel reads `.env.example` for the names; it never reads `.env` itself.

### A repository written for Heroku

A `Procfile` and an `app.json` already say most of this, and are read:

* The Procfile's **`web`** line is the start command — over a guess from the
  framework, though not over a Dockerfile's own `CMD`.
* Its **`release`** line is the [release command](#release-command): it runs
  after each build and before the new version takes traffic, which is what
  Heroku's release phase does, so a migration that ran on every Heroku deploy
  runs on every deploy here.
* Any other line — a `worker`, a `clock` — becomes one of the app's
  [processes](#processes), ticked in the form and started with the first
  deploy. Heroku allows capitals and underscores in those names and a process
  here does not, so `Celery_Beat` becomes `celery-beat`.
* **`app.json`'s add-ons** are databases: `heroku-postgresql`, `heroku-redis`,
  `jawsdb`, `cleardb` and the like are offered like any other database, under
  the variable Heroku would set (`DATABASE_URL`, `REDIS_URL`, or the one its
  `as` names).
* **Its `env`** fills the settings box: values filled in, a fresh random secret
  wherever it says `"generator": "secret"`, descriptions as comments, and the
  required ones with no value listed as missing. `skifity up` sets the same on
  the app it creates.
* **Its `postdeploy` script** is named, with the `skifity run` command that runs
  it once.

### A Compose file

A repository with a `compose.yaml` or `docker-compose.yml` is offered as what it
describes: **Create all as a stack** makes every service an app in the
environment at once, and deploys them, the ones a service `depends_on` first.
Or pick one service and the form fills in from it, as before.

Each service carries over the way Compose runs it:

* **Where it comes from.** A service with an `image` runs that image; one with
  a `build` is built from the repository, from its context and with its
  `dockerfile` — Compose always builds with a Dockerfile, so this does too.
* **How the others reach it.** By name and port, the way they do in Compose:
  `postgres://app:secret@db:5432/app` from another service reaches the `db`
  app. The port is the first one the service publishes, else the first it
  `expose`s, else the one its image is known for — 5432 for `postgres`, 6379
  for `redis`, and so on for the usual ones.
* **Who else reaches it.** A service Compose publishes with `ports` gets an
  address like any app; the rest are **internal**: reachable from their own
  environment and nowhere else, with no public address. Any app can be made
  internal, or public again, in its Settings.
* **Its settings.** `environment` becomes variables, secret-looking ones as
  secrets. `${NAME:-default}` takes the default, since the shell Compose read
  it from is not here; a `${NAME}` with none is kept and named, to be set under
  Variables.
* **Its data.** A named volume becomes a disk at the same path, 5 GB for a
  database and 1 GB otherwise. A folder from the machine that ran Compose —
  `./init.sql:/docker-entrypoint-initdb.d/init.sql` — has no equivalent and is
  named; put those files in the image instead.
* **Its command.** `command` becomes the start command.

Nothing is created unless everything can be: a name already taken in the
environment, a build with no repository, or two services that would end up
with the same name, refuses the whole stack. A service name with capitals or
underscores is reached by its lower-case, hyphenated form, and the panel says
so.

A database in a stack is an app running the database's image with a disk, as
it was in Compose, not a managed database with [backups](backups.md). For
production, create a managed one and point the app's variable at it instead.

## How your code becomes an image

Four ways, in the order the panel picks them:

| | When | What happens |
|---|---|---|
| **Dockerfile** | The repository has one | It is built as written. Your decision, not overridden. |
| **Front end** | A build script and no server: Vite, Create React App, Angular | The build runs, and what it writes is served by Caddy. No Node process in the image. |
| **Static** | The repository already holds `index.html` | The files are served as they are. |
| **Zero-config** | Everything else | [Railpack](https://railpack.com) works out the language, the versions and how to start it, inside the build. |

The zero-config builder is the answer to "does it support my stack": it is not a
list Skifity maintains, it is Railpack's, and it covers Node, Python, Go, PHP,
Ruby, Rust, Java, Deno, Elixir and more. Nixpacks is not offered: its makers
replaced it with Railpack, and its command line is not published as an image a
build can run. And anything at all builds with a Dockerfile, which
is the escape hatch that never runs out — if a stack is not supported, that
sentence means "you write four lines of Dockerfile", not "you cannot deploy it".

**A front end needs two things named**, and the panel fills both in when it
recognises the framework:

* **Build command** — what produces the files, usually `npm run build`. The
  package manager comes from whichever lockfile is in the repository, so pnpm
  and Yarn work without being told.
* **Output directory** — where that command writes them. `dist` for Vite,
  `build` for Create React App, `dist/<project>` for Angular.

Get the second one wrong and the site comes up empty; both are editable under
the app's **Settings**.

Nothing from the repository's own `.git` directory ever reaches a served image.

### Versions with a known critical vulnerability

When a version of a framework can be taken over from the internet — the
React Server Components hole of December 2025, CVE-2025-55182, was a remote
code execution in every affected Next.js app — hosted platforms stopped
deploying it within a day, and people running their own servers found out from
the news. Skifity checks for the same short list:

| Package | Advisory | Fixed in |
|---|---|---|
| `next` | [CVE-2025-29927](https://github.com/vercel/next.js/security/advisories/GHSA-f82v-jwr5-mffw), middleware authorization bypass | 12.3.5, 13.5.9, 14.2.25, 15.2.3 |
| `next` | [CVE-2025-55182](https://github.com/vercel/next.js/security/advisories/GHSA-9qr9-h5gf-34mp), remote code execution | 15.0.5, 15.1.9, 15.2.6, 15.3.6, 15.4.8, 15.5.7, 16.0.7 |
| `react-server-dom-webpack`, `-parcel`, `-turbopack` | [CVE-2025-55182](https://github.com/facebook/react/security/advisories/GHSA-fv66-9v8q-g76r) | 19.0.1, 19.1.2, 19.2.1 |

The version is read from the lockfile — `package-lock.json`, `pnpm-lock.yaml`,
`yarn.lock` or `bun.lock` — because that is what gets installed; from
`package.json` only when it names one exact version. Canary and other
pre-release versions are not checked.

It is said in three places: the new-app form, `skifity up`, and the build log of
every deploy of a commit that installs one, as a line starting with `Warning:`.
It never stops a deploy: the version may be the step on the way to the fix,
and a panel that refuses to deploy during an incident is worse than one that
says so. The list is kept short on purpose, for holes that turn an app into
somebody else's server; everything else is what `npm audit` and Dependabot are
for.

## Deploying a folder instead

Not every app has a repository. One an assistant wrote is usually a folder on
somebody's computer, and there are two ways to deploy it as it is. In the panel,
**New app → A folder on my computer** and pick the folder; later versions go up
with **Send a new version** on the app's page. From a terminal, `skifity up` in
the folder, and again for every version after. See
[the CLI](/docs/cli#deploying-a-folder).

Both read the folder with the same detection the panel uses on a repository,
create the app with the databases it needs, and send the folder without its
`.env`, `node_modules` or anything its `.gitignore` lists. They are two
implementations of one rule about what is sent, and the build checks that they
agree, case by case, so a folder never deploys differently depending on which
one sent it. A `.env`'s values are offered as variables — shown in the form, or
asked about in the terminal — and never uploaded as a file. A pasted address
for a database the panel is creating with the app is left out: the new database
sets it.

From there it is an ordinary app. The build is the same four ways above, a
rollback goes back to the image an earlier upload built, and the same code sent
twice builds once. What it does not have is anything that needs a repository: a
branch, deploy on push, or previews for pull requests. When the app outgrows a
folder on a laptop, push it to a Git host and create an app from the repository.

## Deploying when you push

Connect a Git account under **Settings → Git**, and when you create an app from
a repository Skifity registers the webhook on it for you. After that a push to
the app's branch builds and deploys it, and a pull request gets a preview of
its own if you turned those on.

If the token cannot manage that repository's webhooks — a read-only token is
the right token for somebody who deploys by hand — the panel says so and gives
you the URL to add yourself. It never fails creating the app over it.

Only the app's own branch deploys. A push to any other branch is read, matched
against nothing, and ignored.

### Only the paths an app watches

In a monorepo several apps are built from one repository, and by default a push
to the branch deploys every one of them. Give an app **Watch paths** in its
settings and a push deploys it only when the push changed one of them. One per
line, starting at the repository's root — not at the app's root directory,
because what an app is built from is often outside it:

```
apps/web
packages/ui/**
!**/*.md
```

A plain path covers everything under it. `*` and `?` match inside one
directory, `**` across any number of them. A line starting with `!` leaves
matching files out, a later line wins over an earlier one, and a list of only
`!` lines means "everything except these". Lines starting with `#` are
comments. When the app has a root directory and no watch paths, the settings
offer to watch the root directory.

When Skifity cannot be sure which files a push changed, it deploys: the first
push of a branch, a force push, a push of twenty or more commits (where hosts
stop listing them), and a commit that changes no files — which is how
`git commit --allow-empty` asks for a redeploy. GitLab and Gitea do not say
when a push was forced, so a force push there that only takes changes away from
an app's paths is not seen; press **Redeploy** for that. Pull requests are not
filtered: their webhooks do not list files, and every one gets its preview.

A skipped app is named in the webhook's answer, which your Git host shows in
its delivery log: `web (nothing it watches changed)`.

### What the pull request sees

Every deploy of a commit from a connected repository is reported back to it.
The commit gets a status — building, live or failed — named
`skifity/<environment>/<app>`, whose link opens the app when it is live and the
deployment's log when it is not. A preview's pull request also gets one comment
with the preview's address, the commit it is running and a link to its log. The
comment is edited on every push rather than added to, and two apps built from
one repository get a comment each.

Every preview reports under the same name, `skifity/preview/<app>`, so a branch
protection rule can require a working preview before anything is merged.

This needs a token that can write commit statuses and comments: on GitHub a
fine-grained token with *Commit statuses* and *Pull requests* set to read and
write, on GitLab the `api` scope, on Gitea one that can write to the
repository. A token that cannot is not an error. The deploy goes ahead, and its
log says once why nothing appeared on the pull request.

Two things are deliberately not reported. A rollback goes back to a commit that
was already reported when it first went out, and saying "live" on it again
would read as a fresh test. A plain Git connection has no host API to report
to.

### A preview's databases

A preview starts with the app's variables, and with **a database of its own**
for each one the app is linked to: the same engine and version, empty, one
instance and 1 GB, in the preview's environment, under the same variable name.
It goes when the preview does.

It is never production's. A pull request's code does not get the address of
the database your customers' data is in, so a migration on a branch runs
against something it is allowed to break. If the panel cannot make the
preview's database, the preview starts without the variable and fails where
you can see it, rather than quietly connecting somewhere it should not.

A pull request from a fork gets no database and no secret variables, since
anybody can open one.

Two apps linked to one database — an API and its admin, a web app and a worker
that is an app of its own — share the preview's copy of it, rather than each
getting an empty one of its own that the other never sees.

### Seeding a preview

An empty database is a preview with nothing to click on. An app's **Seed for
previews** (under Settings, once previews are on) is a command run once in each
new preview, after its first deploy — `npm run seed`, `bin/rails db:seed`,
`python manage.py loaddata demo` — which is what Heroku's `postdeploy` script
does for a review app. It runs after the release command, so against the schema
the pull request migrated to, in the preview's own image and variables.

Once, even when it fails: a seed that half ran and runs again is duplicated
rows. A failed seed does not fail the deploy, which has already happened; its
output is in the deployment's log, with the `skifity run` command that runs it
again. It never runs outside a preview.

### Previewing the whole environment

By default a pull request's preview has the apps its repository builds. A front
end whose API is another repository gets a preview that calls nothing, and a
Compose stack's database admin or cache is not there. Turn on **Previews copy
the whole environment** on the project page, beside the environment's
confinement level, and a new preview gets a copy of every app in it:

* The apps the pull request's repository builds are built from the pull
  request, as before.
* Every other app runs the version it runs in the environment it came from —
  the same image, nothing rebuilt — and stays at that version: it does not
  deploy on push, and it has no preview of its own.
* Each gets what any preview copy gets: the variables (as each says), databases
  of its own (shared, as above), its password, its processes at one instance,
  and one instance of itself. An internal app stays internal, reached by the
  same name as here.
* An app that has never been deployed is left out, having nothing to run.

It applies to previews made after it is turned on. The preview goes with its
pull request, all of it.

### A preview's variables

A preview starts with the app's variables — unless the app says otherwise, one
variable at a time, with the pull-request icon beside it on the **Variables**
tab. A preview can get:

* **the same value**, which is the default;
* **a value of its own** — the payment provider's test key rather than the live
  one, a sandbox mail server, a staging API; or
* **nothing**, for a variable a preview should not have at all.

A value of its own is sealed like the variable itself, and a secret's is as
hidden as the secret. It is read when a preview is made, so previews already
running keep what they were given and the next one made gets the new setting.
The app itself never changes when you set these.

## Usage and warnings

The panel reads every running app once a minute and keeps what it read for
three days: CPU and memory, instances ready, restarts. The **Usage** card on
the app's Overview draws the last hour, six hours, day or three days, as the
busiest instance's share of its own limit — the number that gets an instance
throttled at the CPU limit or killed at the memory limit, which a total across
instances hides.

Three thresholds are watched, and each is sent to the team's notification
channels (as **An app crossed a usage threshold**) once when it is crossed and
once when it is over:

* **memory** — the busiest instance at 90% of its memory limit or more for three
  minutes running, by default;
* **CPU** — the same for the CPU limit, off by default, since a busy app is using
  what it was given;
* **restarts** — three restarts in ten minutes, by default, counted across
  instances that are replaced.

Each is set on the same card; 0 turns one off. They are not checked while a
deploy is in progress, when new instances starting and old ones stopping are
expected. `GET /api/apps/{app}/metrics?range=6h` and `/api/apps/{app}/alerts`
are the same for a script.

### A server's usage, and its disk

Servers are read the same way: CPU, memory, instances and the disk, once a
minute, kept for three days, drawn on the server's page. The disk is measured
as the kubelet measures it — capacity minus what is available, reserved blocks
included, which is why it can read a few points higher than `df` — because at
90% by that measure Kubernetes starts stopping instances on the server to free
space. A full disk is the way a self-hosted server usually dies: images and
logs pile up quietly until it does.

Three thresholds, sent as **A server crossed a usage threshold** once when
crossed and once when over:

* **disk** — 85% full for three minutes, by default, five points before
  Kubernetes acts;
* **memory** — 90% used, by default;
* **CPU** — off by default.

An admin sets them on the server's page. `GET /api/servers/{server}/usage` and
`/api/servers/{server}/alerts` are the same for a script.

## Deploys and downtime

A deploy starts the new instance, waits for it to answer its readiness check,
moves traffic, and only then stops the old one. Nothing is dropped: the old
instance also pauses for five seconds before it is told to stop, so every proxy
has seen it leave before it goes.

**Except when the app has a disk.** One disk can only be written by one
instance at a time, so the old instance has to stop before the new one starts,
and there are a few seconds when nothing answers. The panel says so on the
app's **Storage** tab, because it is the disk that changes this rather than
anything you did to the deploy.

Two other things stop an app on purpose, and both say so first: restoring a
volume backup, and scale to zero, where the first request after an idle period
waits for an instance to start.

## Maintenance

When the work needs the app to be left alone — a migration run by hand, a data
fix, a move to another database — put it into maintenance from its
**Settings** tab or with `skifity maintenance on "Back at 14:00"`. Visitors to
every one of its domains get a page with your message and nothing else, with a
`503` and `Retry-After`, which is what tells a browser and a search engine that
it is temporary: a crawler keeps the page indexed rather than dropping it.

The message is shown exactly as written, so write it in the language your
visitors read. The addresses you list — one address or range per line, and
the form offers the one you are using — still reach the app, so you can check
the work before anybody else sees it. `skifity maintenance on --allow-me` does
the same from the command line.

The app keeps running the whole time; nothing about it is stopped or
redeployed, so ending maintenance is immediate. The page is answered by the
firewall's guard, which already stands in front of an app for its rules, and it
is installed the first time an app is put into maintenance. An app with
firewall rules keeps them: somebody the rules refuse is refused, not shown the
page. An app with no domain has nowhere to show a page and cannot be put into
maintenance.

Maintenance is not a [deploy lock](#deployments-and-rollback): a push still
deploys while visitors see the page, which is usually the point. Lock deploys as
well if it should not.

## Instances and scaling

An app runs one instance by default. You can set a fixed number, or let Skifity
add and remove instances based on CPU or memory.

Before you scale, the panel checks whether the app *can* run more than once. It
looks for the things that break: a SQLite file on a local disk, sessions kept in
memory, a background job that assumes it is the only one. Each finding says what
would go wrong and how to fix it.

A CPU or memory target is a percentage of what the app *reserves*, not of the
server. A new app reserves 50m of CPU and 128Mi of memory, so a 70% target adds
an instance above 35m and 90Mi — which many frameworks are over before they have
served a request. If autoscaling pins your app at its maximum, that is almost
always why, and the fix is to raise the reservation under the app's settings to
what the app really uses. The panel says so on the scaling tab rather than
leaving you to work it out.

An app can also scale to zero: with no traffic for five minutes it stops, and
the next request starts it again while the request waits. That request is held
by KEDA's interceptor, so nobody sees a 503 — they see a slow first page. It
needs a hostname, because the interceptor decides what to wake from the address
that was asked for.

Once anything is scaling an app — a target or scale to zero — the panel stops
writing an instance count of its own. It would otherwise put the app back to its
minimum every time you changed a variable.

Instances are spread across servers where possible, so losing a server does not
take an app down.

## Processes

An app is usually more than its web server: a worker taking jobs off a queue, a
clock enqueueing them, a consumer reading a stream. Each of those is a
**process** of the app, listed under Scaling:

* It runs the app's own image — the same build — with the app's variables, under
  a command of its own. A worker cannot run last week's code against this week's
  schema, because it is deployed with the app, and rolled back with it.
* It has no port, no address and no health check. Nothing sends it traffic; it
  is running when its container is.
* It has its own number of instances. `0` stops it and keeps its command, for a
  queue you want drained later. It is not autoscaled: the web's CPU says nothing
  about how long a queue is.
* It does not mount the app's volumes. A volume can be attached to one server at
  a time, and two writers on one file are how data is corrupted; keep what a
  worker shares with the web in the database, or in object storage.
* It reads `SKIFITY_PROCESS`, its own name, beside everything else the app is
  given.

Its logs are the app's logs tab with the process picked, or
`skifity logs --process worker`. A deploy waits for every process as it waits
for the app, so a worker that cannot start fails the deploy with the reason
rather than crash-looping unnoticed.

```sh
skifity processes                                # what runs beside the app
skifity processes set worker -- bundle exec sidekiq
skifity processes set worker --instances 3
skifity processes set worker --instances 0       # stopped, command kept
skifity processes rm worker
```

A name is lowercase letters, digits and hyphens, up to 20, starting with a
letter: `worker`, `clock`, `celery-beat`. `web` and `release` are taken — they
are the app itself and its [release command](#release-command). An app can have
ten; more than that is usually several apps sharing a repository.

## Servers

The first server runs the control plane: the Kubernetes API, the panel and its
database. Others join it.

With one control plane server, a reboot means a few minutes of downtime for the
cluster's control — your apps keep running. With three, losing one changes
nothing.

A worker can be promoted to a control plane server, if it is big enough for one:
2 GB of memory and 20 GB of disk, against the 1 GB and 8 GB a worker needs,
because etcd and the API server live there too. The check runs before anything
is touched. It did not always: promotion drains the node, removes it from the
cluster and uninstalls Kubernetes before reinstalling it, so a machine that
turned out to be too small used to be discovered at the end, with the apps
already moved off and nothing left running on it.

## Secrets

Every secret Skifity stores — database passwords, Git tokens, your apps'
variables — is encrypted with a key of its own, and that key is wrapped with a
master key kept outside the database.

A leaked database backup is therefore useless on its own. The flip side is that
losing the master key loses every secret, which is why the panel makes you
download a recovery key before it stops nagging.

Once stored, a secret's value is never shown again, by the panel, the CLI, the
API or an AI assistant.

## Components

A fresh install is small: k3s, the panel, and nothing else. Heavier pieces are
installed the first time you need them.

| Component | Installed when | Roughly |
|---|---|---|
| cert-manager | You add a domain | 120 MB |
| Image registry | You first deploy from source | 60 MB |
| Builder | You first deploy from source | 200 MB |
| PostgreSQL operator | You create a PostgreSQL database | 150 MB |
| Scale to zero | You turn it on | 180 MB |
| Cross-node storage | You turn it on | 700 MB per server |
| Full monitoring | You turn it on | 900 MB |

Settings shows what is installed and what each one costs before you agree to it.
