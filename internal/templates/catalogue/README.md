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
    image: example/app:2.4       # a version, never `latest` — see below
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
databases:                       # optional; created before the app starts
  - name: example-db
    engine: postgres             # postgres, mysql or redis
    storage_gb: 5
    link_to: [example, worker]   # every service that needs it, not just one
    var_name: DATABASE_URL       # how the connection string arrives
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

## The two rules that are not obvious

**Name a version.** `latest` is not a version: two deploys of the same app would
run different software, a rollback would restore a tag rather than the thing
that worked, and an upstream release would arrive on a restart nobody asked for.
Use a series tag where upstream publishes one — `1`, `5-alpine`, `6-apache` —
and an exact version where it does not. A test refuses anything ending in
`latest`, `main`, `master`, `stable`, `edge`, `release` or `dev`, and the image
must exist: check before you commit.

Not every project ships semver, and that is fine. `19.1.8-ce.0`,
`2026.9.17-c49771992` and `version-2026-07-14c` each name one build exactly. An
architecture (`linux-arm-v7`), a runtime (`php8.3-apache`) or a build of
somebody's branch is not a version of the application, however precise it looks.

**Do not wire the database by hand.** A Compose file points services at each
other by name (`DB_HOST=mariadb`). There is no sibling container here — the
database is a managed one and arrives as the `var_name` above — so a variable
like `DB_HOST`, `REDIS_PORT` or `MB_DB_URL` points at nothing and the app will
crash-loop with a hostname nobody recognises. A test refuses those too.

## What the tests check

`go test ./internal/templates/` covers: unique ids that are already slugs, a
description and an https website, at least one service somebody can open, ports
in range, requests that do not exceed limits, variable names a container can
carry, absolute mount paths, every `link_to` naming a real service, every engine
one Skifity provisions, no floating tags, no service named for a worker that is
public, and no database wiring.

Run `make check` before opening a pull request. A template that fails is not a
template.
