# Databases

Skifity runs nine kinds of database for your apps, in the same environment as
the apps that use them. Each is created from a project's page or the
**Databases** page, connected to an app with one click, and — for six of the
nine — [backed up](backups.md) on a schedule.

## The engines

| Engine | Versions offered | For | Port | Backed up |
|---|---|---|---|---|
| **PostgreSQL** | 18, **17**, 16 | Relational data; the one to pick when unsure | 5432 | Yes |
| **MySQL** | 9.7, **8.4** | Software that asks for MySQL by name | 3306 | Yes |
| **MariaDB** | 12.3, **11.8**, 11.4, 10.11 | MySQL-compatible; most PHP software is tested with it | 3306 | Yes |
| **MongoDB** | **8.0**, 7.0 | Documents rather than tables | 27017 | Yes |
| **Redis** | **7** | Caches, queues and sessions | 6379 | Yes |
| **Valkey** | 9.1, **9.0**, 8.1 | Redis's open-source continuation, used exactly like Redis | 6379 | Yes |
| **Dragonfly** | **1.40** | The Redis protocol on every core of the server | 6379 | [No](backups.md#what-is-not-backed-up) |
| **ClickHouse** | **26.8**, 26.3 | Analytics over large tables | 8123, and 9000 | [No](backups.md#what-is-not-backed-up) |
| **Memcached** | **1.6** | A cache and nothing more | 11211 | [No](backups.md#what-is-not-backed-up) |

The version in bold is the one a new database gets when you do not choose. Each
version is one exact release of the engine's official image, fetched from its
registry before it was offered, so the database you create today is the same
software as the one created next month. PostgreSQL and Redis are the two
exceptions: PostgreSQL runs CloudNativePG's image for the major version, which
CloudNativePG keeps patched, and Redis the official image's tag for its major.

PostgreSQL runs through CloudNativePG, which is installed the first time
somebody asks for one, and is the only engine that can run as several instances:
three survive losing a server. Every other engine runs as one instance with its
own disk. Asking for more is refused rather than quietly ignored — three copies
of a MySQL behind one address would be three different databases.

### MySQL and MariaDB

Until the nine engines, the panel offered one MySQL-compatible database, called
`mysql`, and it ran MariaDB. Those databases are now listed as **MariaDB**,
which is what they always were: their data, their address and the variable
their apps read are unchanged, and they are backed up with MariaDB's own tools
as before. `mysql` now means MySQL itself.

The difference matters for older software. MySQL's accounts sign in with
`caching_sha2_password`, which the Node package called `mysql` cannot do; an
app that uses it is offered MariaDB, and one that uses `mysql2` is offered
MySQL. A `skifity.yaml` written before the change that says `engine: mysql` for
an existing database gets a note from `skifity plan` that the database is a
MariaDB here; change the file to `engine: mariadb`.

## Connecting an app

Linking a database to an app gives it the connection string as a secret
variable and restarts it. When you do not name the variable, it is the one
software for that engine usually reads:

| Engine | Variable | Connection string |
|---|---|---|
| PostgreSQL | `DATABASE_URL` | `postgresql://app:…@host:5432/app?sslmode=disable` |
| MySQL, MariaDB | `MYSQL_URL` | `mysql://app:…@host:3306/app` |
| MongoDB | `MONGODB_URI` | `mongodb://app:…@host:27017/app?authSource=admin` |
| Redis, Valkey, Dragonfly | `REDIS_URL` | `redis://default:…@host:6379` |
| ClickHouse | `CLICKHOUSE_URL` | `http://app:…@host:8123/app` |
| Memcached | `MEMCACHED_URL` | `memcached://host:11211` |

A few of these deserve a word:

* **MariaDB's** URL says `mysql://`, because that is the scheme its drivers and
  ORMs read: Prisma, Sequelize, SQLAlchemy and Laravel know no `mariadb://`.
* **MongoDB's** user is the instance's root user, which MongoDB keeps in the
  `admin` database; `authSource=admin` says so, and the path is the database
  your collections go in. MongoDB creates it on the first write.
* **Valkey and Dragonfly** use `redis://` too. Both speak Redis's protocol, and
  every Redis client connects to them unchanged.
* **ClickHouse** answers on two ports. The connection string is its HTTP one,
  which most of its client libraries take — the official ones for JavaScript,
  Python (`clickhouse-connect`), Java and Rust speak only HTTP. The native
  protocol, for `clickhouse-client` and `clickhouse-go`, is on port 9000; the
  database's page shows that address too, and its credentials Secret carries it
  as `native_url`.
* **Memcached** has no user and no password. SASL would need a binary protocol
  most memcached clients no longer speak, so it is kept private the way every
  database is: a database has no address outside the cluster, and each
  environment's network policy lets in only its own apps, the panel (for
  `skifity db connect`, which needs an admin) and the proxies in front of apps,
  which route only to apps with a domain. An app in another environment, or anything
  on the internet, cannot reach it.

A template can also ask for the connection in pieces — host, port, name, user
and password — for software that has no setting for a URL; see
[templates](templates.md).

## From your computer

`skifity db connect` opens a local port that reaches a database through the
panel, for a desktop client or a migration ([more](cli.md#reaching-a-database)).
It listens on the engine's own port when that is free — 5432, 3306, 27017, 6379,
8123 or 11211 — so a client's defaults work. For ClickHouse that is the HTTP
port, which DBeaver and `curl` use; `clickhouse-client` needs the native one, so
run it inside the environment or connect with the HTTP interface.

## What each engine keeps, and where

* **Redis and Valkey** write every change to an append-only file on their disk,
  so a restart loses nothing.
* **Dragonfly** has no append-only file. It saves a snapshot every five minutes
  and another as it stops, and loads the newest when it starts: a crash loses at
  most the five minutes before it. It is sized to its container — three
  quarters of its memory limit for data, and one thread for every 256 MB of
  that, up to two.
* **MongoDB's** cache is a quarter of its memory limit, and at least 256 MB.
* **ClickHouse** gets 2 GB of memory and 10 GB of disk by default, since a
  query over a large table wants room.
* **Memcached** keeps three quarters of its memory limit for items, and nothing
  on a disk. A restart empties it, as a cache should.

## Passwords

Every password is generated by the panel, stored encrypted, and handed to the
database through a Kubernetes Secret — never written into a manifest and never
on a process's command line, where anything that can list the pod's processes
would read it. Redis and Valkey read theirs from a file the container writes
at start; ClickHouse's database is created without the image's own start-up
step, which would have passed the password to `clickhouse-client` as an
argument; the backup jobs give each client its password through its own
environment variable, or, for MongoDB's tools, a file.
