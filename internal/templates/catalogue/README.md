# The template catalogue

One file per template. Add one by writing a file here; nothing in Go needs to
change, and `make check` tells you whether it is right.

```yaml
id: example                      # a slug; it is in the URL
name: Example
description: One sentence, on a card.
category: productivity           # groups it on the Templates page
website: https://example.org     # https, and somewhere that explains the app
services:
  - name: example                # a slug; it becomes a Kubernetes object
    image: example/app:2.4.1     # a release, never `latest` or `2` — see below
    run_as_user: 65534           # only if the image's USER is a name: its uid
    needs_root: true             # only if the image starts as root; see below
    port: 8080
    public: true                 # gets a domain; a worker would not
    health_path: /healthz        # optional, and only if it really exists
    health_check: http           # optional; http, tcp or none — see below
    health_start_seconds: 600    # optional; how long its first start may take
    health_timeout_seconds: 10   # optional; how long one check may wait
    variables:                   # what the app needs, not what wires it up
      TZ: UTC
    volumes:
      - name: data
        mount_path: /var/lib/example
        size_gb: 5
    mem_request_mb: 128
    mem_limit_mb: 1024
    cpu_request_m: 50
    cpu_limit_m: 1000
    command: example serve --port 8080   # optional; replaces how the image starts
    gpu:                         # optional; a card it uses when a server has one
      vendor: nvidia             # nvidia, the default, amd or intel
      count: 1                   # installed without it on a cluster with none
    ports:                       # optional; connections that are not HTTP
      - port: 25565              # opened on every server at the same number
        protocol: tcp            # tcp, the default, or udp
    files:                       # optional; mounted read-only at each path
      - path: /etc/example/config.yml
        content: |
          listen: 0.0.0.0:8080
        executable: false        # true for a script the image runs at start
        secret: false            # true hides the content once installed
databases:                       # optional; created before the app starts
  - name: example-db
    engine: postgres             # postgres, mysql, mariadb, mongodb, redis, valkey,
                                 # dragonfly, clickhouse or memcached
    storage_gb: 5
    link_to: [example, worker]   # every service that needs it, not just one
    var_name: DATABASE_URL       # how the connection string arrives
    vars:                        # optional; the same connection in pieces
      host: DB_HOST
      port: DB_PORT
      name: DB_DATABASE
      user: DB_USERNAME
      password: DB_PASSWORD
inputs:                          # optional; asked for at install time
  - key: SECRET_KEY
    label: Secret key
    secret: true
    generate: true               # filled with a random value when left empty
notes: Anything that has to be done by hand afterwards.
```

## Several services in one template

A template may install more than one app — a web app and its worker, a service
and a search index. They land in the same environment and reach each other by
name, which is what makes the references between them work.

A worker is a service with **`port: 0`** and `public: false`. That is not a
placeholder: an app with no port gets no Service, no readiness probe and no
ingress, which is exactly what a Sidekiq or a Celery beat wants. Giving one the
web app's port instead produces a readiness check against something that never
answers, and an app that is "starting" forever.

`link_to` is a list because the web app and the worker usually share one
database, and linking only the first leaves the other without the variable it
cannot run without. List the applications, not the datastores: a ClickHouse, a
MinIO or a Meilisearch in the same template does not read a connection string
for the Postgres next to it.

A port has to come from somewhere. Where a template was converted, the order was:
the source's own domain marker, an `expose` or `ports` entry, the port a sibling
service dials (`ELASTICSEARCH_HOSTS=http://elasticsearch:9200` says one), a short
table of ports that are documented facts about an image, and last the port the
service's healthcheck talks to. Writing one because it is the usual one for that
kind of app is how a search index ends up with a dashboard's port.

One thing does not carry over: a volume shared between services. A Compose
volume is shared; a Skifity volume belongs to one app and is read-write-once, so
two apps mounting the same path get two different directories. Where that
matters, say so in `notes` and point people at object storage.

## A command, files, and a database in pieces

Three things software written for Compose takes for granted, and a template can
ask for:

* **`command`** replaces how the image starts, and is run by `/bin/sh -c`. It
  is for one image started two ways — a server and its worker — not for
  repeating the image's own default. An image with no shell cannot take one.
* **`files`** are mounted read-only at their paths, each replacing only that
  file: a `prometheus.yml`, a `Caddyfile`, a settings file the software reads
  and has no variable for. Configuration-sized: 256 KiB each, 900 KiB per
  service. A test checks every path can be mounted.
* **`vars`** under a database delivers its connection as a host, a port, a
  database name, a user and a password, for software that asks for those and
  has no setting for a URL. Each piece arrives under the name given; the
  password as a secret. They are written once at install, unlike `var_name`,
  which the panel keeps linked.

## How it is checked, and how long it has

An app installed from a template is checked as any app is, and gets what any
app gets unless its template says otherwise: a check that follows
`health_path`, two minutes to start, three seconds to answer. Three fields say
otherwise, each only for software that needs it:

* **`health_start_seconds`**, from 10 to 1800, is how long a new instance may
  take before it has to answer. Software that migrates its database, installs
  itself or warms up a JVM before it opens its port needs longer than two
  minutes on its first start, and without it the startup probe restarts it
  part-way, again and again. Give it as long as the slowest first start takes,
  with room for a database on another server: 300 for most that migrate or
  install themselves first, 600 where that takes a few minutes, 900 for GitLab.
  A test asks for more than the default from every template whose notes say
  the first start takes minutes.
* **`health_timeout_seconds`**, from 1 to 60, is how long one check waits for
  an answer. Raise it for a health path that asks the database on every check,
  such as WordPress's `/wp-admin/install.php`. An app and its database can land
  on different servers: on two servers 40 ms and some lost packets apart, that
  answer took longer than three seconds, the instance was killed at the end of
  its start budget and the install's deploy failed. Ten is plenty. Read the
  image's source for what its health path does rather than guessing from the
  name; `/healthz` is as often a constant as a query.
* **`health_check`** is `http`, `tcp` or `none`, and is best left out: it is
  `http` when there is a `health_path` and `tcp` when there is not. `http`
  needs a `health_path`, and `none` means nothing checks the app at all.

A worker has no port and so no checks, and a test refuses any of these on one.
They are the app's own health settings once it is installed, changed under its
**Settings** like any app's, and runtime settings: they never rebuild anything,
and an update of the template moves only the image.

## Which variables a database arrives as

A database arrives as one connection string, under `var_name`. That is right for
software that reads a URL, and for nothing else: an image that reads a host, a
user and a password instead is given a variable it never looks at, and the
database is created beside an app that cannot reach it. Read the image's own
documentation for what it reads, and when it is pieces, ask for them with
`vars` — and set whatever a piece cannot say, such as which kind of database it
is, in `variables`:

```yaml
services:
  - name: gitea
    image: gitea/gitea:1.27.3
    variables:
      GITEA__database__DB_TYPE: postgres
databases:
  - name: gitea-db
    engine: postgres
    storage_gb: 5
    link_to: [gitea]
    var_name: DATABASE_URL
    vars:
      host: GITEA__database__HOST
      name: GITEA__database__NAME
      user: GITEA__database__USER
      password: GITEA__database__PASSWD
```

`internal/templates/known_images.go` lists what a few dozen well-known images
read — WordPress, Ghost, the official Nextcloud, Gitea, Forgejo, Joomla, Matomo,
BookStack, n8n, Grafana and others, each from its project's documentation or
source — and a test holds every template on one of them to it, as the panel
holds a team's own catalogue. One that sets `files` or `command` is left alone,
since it may configure itself another way. An image whose installer asks for its
database in a browser (Drupal, MediaWiki, FreshRSS) reads nothing from its
environment, so its template's notes have to say what to type there. An image
that takes no database setting at all, such as linuxserver's Nextcloud, cannot
be given a database.

### Templates that hand over only a URL

These link a database as a connection string and nothing else. The ones whose
images have been looked up, and do read a URL or ask in their installer:

<!-- url-only:audited -->
`docuseal-with-postgres`, `drupal-with-postgresql`, `freshrss-with-mariadb`,
`freshrss-with-mysql`, `freshrss-with-postgresql`, `pocket-id-with-postgresql`,
`unleash-with-postgresql`.
<!-- /url-only:audited -->

The ones nobody has looked up yet. Many of their images read a URL, and some
will not; each one checked either moves to the list above, with its image added
to `known_images.go`, or gets the `vars` its image reads. A test keeps both lists
to what is in this directory.

<!-- url-only:unaudited -->
`activepieces`, `alexandrie`, `argilla`, `authorizer`, `baserow`, `bugsink`,
`casdoor`, `castopod`, `chaskiq`, `chatwoot`, `cloudreve`, `coder`, `codimd`,
`docmost`, `documenso`, `dolibarr`, `easyappointments`, `engram`, `faraday`,
`fider`, `firefly`, `flagsmith`, `focalboard`, `formbricks`, `ghostfolio`,
`glitchtip`, `glpi`, `grimmory`, `grist`, `hedgedoc`, `hoppscotch`, `inngest`,
`invoice-ninja`, `joplin`, `keila`, `kener`, `keycloak-with-postgres`, `kimai`,
`langflow`, `langfuse`, `leantime`, `librechat`, `linkwarden`, `listmonk`,
`litellm`, `logto`, `matrix-synapse-with-postgresql`, `mattermost`, `metabase`,
`metamcp`, `mindsdb`, `miniflux`, `mixpost`, `monica`, `moodle`, `newapi`,
`nitropage-with-postgresql`, `nocobase`, `nodebb`, `observium`, `odoo`,
`open-archiver`, `openpanel`, `orangehrm`, `outline`, `owncloud`, `passbolt`,
`paymenter`, `pgbackweb`, `photoview`, `piwigo`, `planka`, `plausible`, `plunk`,
`portabase`, `postiz`, `pydio-cells`, `rallly`, `reactive-resume`, `redash`,
`redmine`, `rybbit`, `ryot`, `seafile`, `sequin`, `sftpgo`, `strapi`,
`superset-with-postgresql`, `sure`, `tandoor-recipes`, `teable`, `tolgee`,
`traccar`, `twenty`, `typebot`, `umami`, `usesend`, `wakapi`, `weblate`,
`wikijs`, `zipline`.
<!-- /url-only:unaudited -->

## Which user an image starts as

Every environment starts at the strictest confinement level, which refuses an
image that starts as root and one whose `USER` is a name, because the kubelet
cannot see that a name is not root. So a template says which of the three its
images are:

* **A number other than 0** needs nothing.
* **A name** — `USER node`, `USER nobody` — needs `run_as_user` with that name's
  uid, and then runs at the strictest level.
* **Root**, or no `USER` at all, needs `needs_root: true` on the service, and a
  sentence in `notes` saying the environment needs the baseline pod security
  level; a test checks the two agree. The install dialog then says so before
  anything is made, and an owner or admin can let the environment accept root
  images in the same step. Try `run_as_user` first where the image documents
  running as an ordinary user — many PHP images do — because an image that runs
  without root runs everywhere.

`python3 hack/resolve_tags.py --users` asks each image's registry which user it
starts as and prints every service whose template does not say so yet. It is run
by hand, like the rest of `hack/`; nothing in `make check` asks a registry. Seven
images would not answer the last time it ran — codimd, convex, openobserve,
rocketchat, unstructured and weaviate — and are not marked either way.

## The two rules that are not obvious

**Name a version.** `latest` is not a version: two deploys of the same app would
run different software, a rollback would restore a tag rather than the thing
that worked, and an upstream release would arrive on a restart nobody asked for.
A tag that names only a major version — `1`, `v2`, `5-alpine`, `6-apache` — is
the same thing with a fence around it: it moves on every minor and patch
release. Name the release instead, `5.130.6-alpine` rather than `5-alpine`, and
stay on the major the template was written for: a database's data directory
from one major does not always open in the next, so moving up is a change of
its own. A test refuses anything ending in `latest`, `main`, `master`, `stable`,
`edge`, `nightly`, `release` or `dev`, and any tag that is only a major version,
and the image must exist: check before you commit.

Not every project ships semver, and that is fine. `19.1.8-ce.0`,
`2026.9.17-c49771992` and `version-2026-07-14c` each name one build exactly. An
architecture (`linux-arm-v7`), a runtime (`php8.3-apache`) or a build of
somebody's branch is not a version of the application, however precise it looks.

**Do not wire the database by hand.** A Compose file points services at each
other by name (`DB_HOST=mariadb`). There is no sibling container here — the
database is a managed one and arrives as the `var_name` above, or in pieces
through `vars` — so a variable like `DB_HOST`, `REDIS_PORT` or `MB_DB_URL`
written by hand points at nothing and the app will crash-loop with a hostname
nobody recognises. A test refuses those too, and it reads values as well as
names: a URL whose host is a bare name (`PAPERLESS_REDIS=redis://redis:6379`)
must name a service in the same template or one of its databases. Where an app
wants its Redis or its database under a name of its own, that name is the
`var_name`.

## What the tests check

`go test ./internal/templates/` covers: unique ids that are already slugs, a
description and an https website, at least one service somebody can open, ports
in range, requests that do not exceed limits, variable names a container can
carry, absolute mount paths and no volume over one of the server's own files,
every `link_to` naming a real service, every engine one Skifity provisions, no
floating or major-only tags, no service named for a worker that is public, no
database wiring, no URL pointing at a host the template does not install, a
well-known image given the database variables it reads, `needs_root` and the
notes agreeing, health settings an app could have and none on a worker, and a
start budget above the default wherever the notes say the first start takes
minutes.

Run `make check` before opening a pull request. A template that fails is not a
template.

The checks are functions in `internal/templates/validate.go`, not loops in the
tests, because a team's own catalogue is held to exactly the same ones when the
panel reads it: a template of theirs that fails one is listed with why and
cannot be installed. The format above is also what such a catalogue is written
in; see the private catalogues section of `docs/templates.md`.
