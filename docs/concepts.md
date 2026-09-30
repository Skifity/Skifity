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
