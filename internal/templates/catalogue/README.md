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
    port: 8080
    public: true                 # gets a domain; a worker would not
    health_path: /healthz        # optional, and only if it really exists
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
carry, absolute mount paths, every `link_to` naming a real service, every engine
one Skifity provisions, no floating or major-only tags, no service named for a
worker that is public, no database wiring, and no URL pointing at a host the
template does not install.

Run `make check` before opening a pull request. A template that fails is not a
template.
