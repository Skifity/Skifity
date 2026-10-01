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

## Stopping and starting

**Stop**, on a database's **Manage** tab or `skifity db stop orders`, takes its
instances away and keeps its disk: the data stays where it is, and the database
uses no memory and no CPU until **Start** (`skifity db start orders`) brings it
back on the same disk. PostgreSQL is hibernated the way CloudNativePG does it;
every other engine's StatefulSet is scaled to nothing.

* The apps linked to it lose it until it is started again, so they are named
  first. The API refuses with `database.stop_linked` until the stop says it
  means it (`?force=true`, `skifity db stop --force`); the panel asks and lists
  them.
* While it is stopped its scheduled backups are recorded as **Skipped**, with
  the reason, rather than as failures: there is nothing new to copy. Only the
  newest skip is kept. A backup asked for by hand is refused, as for any
  database that is not running.
* Starting waits for it to accept connections with the same check a new
  database is waited for with; its status is *Starting* until then.

## Resizing

The **Size** card, `skifity db resize orders --memory-limit 2048 --storage 20`
or `PATCH /api/databases/{id}` changes what a database reserves (CPU in
millicores, memory in MB), what it may use (its limits; a CPU limit of 0 is
none, which is the default) and how big its disk is. Give only what changes.

* A change of CPU or memory replaces its instance, so it restarts. PostgreSQL's
  instances are replaced one at a time, the primary last. Dragonfly, MongoDB and
  Memcached size their caches to the memory limit, and are given the new sizes
  with it.
* Each engine has a least memory limit it starts with — ClickHouse and MongoDB
  1 GB, Dragonfly 384 MB, MySQL 512 MB — and a smaller one is refused.
* **A disk only grows.** A smaller size is refused (`database.storage_shrink`):
  a volume cannot shrink. Growing one needs a storage class that allows it, and
  that is checked before anything changes; the k3s default, `local-path`, does
  not ([more](troubleshooting.md#a-database-disk-cannot-grow)).
* The environment's quota is checked first. A resize the quota would refuse is
  refused here instead (`database.over_quota`), because Kubernetes would refuse
  the new instance only after the old one had gone.

## Changing the password

**Change password**, on the Manage tab, `skifity db password orders` or
`POST /api/databases/{id}/password`, gives a database a new password — a
generated one, or one you choose of 16 to 128 letters, digits, dots, dashes,
underscores or tildes — and every linked app the new connection string. It is
an administrator's, like reading the password, and it is in the activity log.

It runs in an order that leaves a working password whatever stops it:

1. The new credentials are stored, encrypted, beside the old ones.
2. The database is given the new password by a short job that signs in with
   the old one and checks the new one works before it ends. MySQL keeps the old
   one working beside it (`RETAIN CURRENT PASSWORD`), and so do Redis and Valkey
   (their default user can have two passwords), so nothing connected notices.
3. The database's Secret and the panel's copy are changed. If either fails, the
   job runs the other way and the old password is back.
4. Every linked app is rolled out with the new connection string.
5. Only then is the old password taken away, where there were two — and not at
   all if an app could not be rolled out.

The passwords reach the job through a Secret of its own and the clients'
environment variables, as the backup jobs' do: never a command line, and never
a log. Dragonfly and ClickHouse read their password only as they start, so
changing it restarts them, and the restart's own readiness check is the proof
it works. Memcached has no password to change.

`skifity db password orders --password-stdin` reads the one you choose from
standard input; `--show-password` prints the new one once it is changed, which
is a read of the password and is audited as one. The **History** tab shows each
step. If a change is interrupted after the database took the new password, the
panel keeps it; changing the password again without choosing one finishes that
change first.

## Importing a dump

**Import a dump**, on the Manage tab, or `skifity db import orders ./dump.sql.gz`
(`-` reads standard input), loads a dump you already have:

| Engine | What it takes | Made with |
| --- | --- | --- |
| PostgreSQL | SQL, or a custom-format archive | `pg_dump`, or `pg_dump --format=custom` |
| MySQL, MariaDB | SQL | `mysqldump` or `mariadb-dump` of one database |
| MongoDB | an archive | `mongodump --archive`; with `--gzip`, say `--format archive-gzip` |
| Redis, Valkey | a snapshot | `dump.rdb`, or `redis-cli --rdb` |

Each may be sent as it is or gzipped, up to 5 GB; larger ones are loaded with the
engine's own client through `skifity db connect`. It is an administrator's, like
a restore, and needs backup storage.

* The file is read to its end before anything changes: what it is comes from
  its first bytes, a gzipped one is decompressed to its end, and a dump for
  another engine — a MySQL dump sent to PostgreSQL, a pg_dumpall of a whole
  server, a tar archive — is refused with what it was.
* Then a backup of the database is taken and waited for. The import stops if it
  fails, and the backup, marked *Before an import*, is how an import is undone.
* The dump is loaded with the engine's own tool, in a job in the database's
  namespace, by the same machinery as a restore. PostgreSQL loads it in one
  transaction, stopping at the first error, so a dump that fails leaves nothing
  behind; a custom archive is loaded without its owners and privileges, since
  the roles of the server it came from do not exist here. A plain SQL dump made
  without `--no-owner --no-privileges` may name such roles and fail; make it
  with them, or use the custom format. A MongoDB archive's collections are put
  in this database whatever database they came from, and its `admin`, `config`
  and `local` are left out. A Redis snapshot goes through the same temporary
  server a restore replicates from.
* The file waits in the backup bucket, sealed when backups are, and is deleted
  when the import ends. The linked apps are restarted afterwards so their
  connections see what is there now.
