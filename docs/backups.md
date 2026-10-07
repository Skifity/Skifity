# Backups

Skifity backs up managed databases — PostgreSQL, MySQL, MariaDB, MongoDB,
Redis and Valkey — the volumes your apps write files to, and
[its own database](#the-panel-itself). Dragonfly, ClickHouse and Memcached are
not backed up, and [why](#what-is-not-backed-up) is below.

Nothing is backed up until you say where to put it.

## Storage

Backups go to S3, or to anything that speaks S3 — MinIO, Backblaze B2,
Cloudflare R2, Hetzner Object Storage. Set it up under **Settings → Backup
storage**: an endpoint, a region, a bucket, and a key pair.

Use a bucket and a key that can do nothing else. If the server is compromised,
the blast radius should stop at "the attacker can write backups".

**The credentials never leave the panel.** A backup runs as a Job inside the
cluster, and that Job is handed a URL that already carries a signature and
expires shortly afterwards. It can upload one object, to one path, for a few
minutes. It has no idea what the key is. The same is true in reverse for a
restore.

That is why backup storage is configured once, centrally, rather than per app:
there is nothing to hand out.

## Encryption

Whoever runs the bucket can read what is in it. Set a **Backup passphrase**
under **Settings → Backup storage** and every backup — databases, volumes and
the panel's own — is encrypted before it leaves the cluster, with a key the
bucket's owner never sees.

In a backup job, a step running the panel's own image seals the dump between
making it and uploading it; a restore opens it between downloading and
loading. The passphrase reaches the job the way the upload URL does, in a
Secret that exists for as long as the job — it belongs to the job, so it goes
with it even if the panel restarts in the middle. If the panel's image cannot be
found, the backup fails rather than going up unencrypted.

The format is AES-256-GCM in 64 KiB chunks, with a key derived from the
passphrase by Argon2id and a fresh salt for every file. Every chunk is
authenticated along with its position and whether it is the last, so a backup
that was changed, reordered or cut short is refused rather than restored as a
smaller database — and the header carries a check value, so the wrong
passphrase is told apart from damage.

**Keep the passphrase somewhere else.** The panel stores it encrypted and never
shows it again, and a sealed backup cannot be restored without it — which is
the point, and also what makes losing it final. Changing it affects backups
taken afterwards; restoring an older one asks for the passphrase it was sealed
with, and the panel checks that against the backup before stopping or
overwriting anything. Backups taken before a passphrase was set stay as they
were, and are restored as they were.

What sealing does not do is tie a file to its place. Every chunk is checked, but
a whole backup put where another one was — an older backup of the same database,
or one of another team's, sealed with the same passphrase — opens and restores.
Whoever can write to the bucket can do that, so give write access to it as
narrowly as read access.

## Verifying

A backup nobody has tried to restore is a hope. **Verify** beside a backup
downloads it, opens it when it is sealed, and reads the archive through to the
end in the background; the list then says when it was last read through, or why
it could not be — a wrong passphrase, a chunk missing or changed, an archive cut
short or not an archive at all. It proves everything a restore depends on
except whether the database accepts the dump, which only a restore does.
Verifications run one at a time — opening a sealed backup takes 64 MiB to derive
its key — and asking again while one is waiting or running is the same one.

## Schedules

Each database has its own schedule, set on its Backups tab, written as cron:

```
0 3 * * *      every day at 03:00
0 3 * * 0      every Sunday at 03:00
0 */6 * * *    every six hours
```

**Keep the last** is how many to hold. Older ones are deleted after a
successful new one, never before: a retention rule must not be able to leave you
with nothing.

It counts scheduled backups only. The ones you take yourself, and the ones
taken before a template update or an upgrade, are counted apart, and the last
ten of those are kept whatever the schedule keeps. Taking a few by hand in a
row therefore never deletes the scheduled copy from before whatever went wrong.

Times are UTC, not your local time.

A backup due while the panel was not running — an upgrade, a node rebooting
over 03:00 — is taken as soon as the panel is back, once, however many were
missed, and the team is told it was late with `backup.missed`. It looks back a
week. A backup started at or after the time it was due counts as taken, even
one that failed: that failure was already reported.

## What is backed up, and how

Each engine is dumped with its own client, from the database's own image, so
the tool is the server's version: never older than the data it reads, and
never newer than the server its output goes back into.

| Engine | Backup | Restore |
|---|---|---|
| PostgreSQL | `pg_dump --clean --if-exists` | `psql`, stopping at the first error |
| MySQL | `mysqldump --single-transaction` | `mysql` |
| MariaDB | `mariadb-dump --single-transaction` | `mariadb` |
| MongoDB | `mongodump --archive` of the app's database | `mongorestore --archive --drop`, each collection replaced |
| Redis, Valkey | a snapshot taken the way a replica takes one (`--rdb`) | see below |

Every dump is compressed, [sealed](#encryption) when there is a passphrase, and
uploaded in the same way whatever made it. No client is given its password as
an argument: each reads it from its own environment variable, and MongoDB's
tools, which read one from nowhere else, from a file only the job can open.

A Redis or Valkey snapshot is loaded by a server as it starts, and there is no
command that loads one into a server that is running. So the restore starts a
server of the database's own image beside it, on the snapshot, and makes the
database its replica: a replica's first synchronisation replaces everything it
holds with the other's data. Once the database has all of it — the number of
keys is compared — it is made a primary again and carries on with what it was
given, and its append-only file is rewritten with it. While that runs, the
database refuses writes. If anything fails part-way, it is made a primary again
before the restore stops; a restore killed outright cannot do that, and
restarting the database clears it, since nothing about it is saved.

## What is not backed up

**Dragonfly.** A backup of a live Redis is taken by asking the server for a
snapshot as a replica would (`SYNC`), and Dragonfly does not implement that
command. Its own snapshots are written to its own disk, which a backup job
cannot read while the database runs, and it can write them straight to a bucket
only with the bucket's keys inside the database. Dragonfly saves a snapshot
every five minutes and as it stops, so a restart loses nothing and a crash at
most five minutes; a lost disk loses everything.

**ClickHouse.** A consistent copy of ClickHouse is made by its own `BACKUP`
command, which writes to a bucket with the bucket's keys inside the database.
Skifity never hands storage credentials to an environment
([storage](#storage) says why), and a dump table by table through the client would be neither consistent nor,
with its materialized views, restorable in order. Run `BACKUP DATABASE app TO
S3(...)` yourself with a key of your own, or keep the tables rebuildable from
their source, which is how analytical data usually is.

**Memcached.** A cache keeps nothing on a disk, and whatever is in it is gone
when it restarts, by design. There is nothing to back up.

The panel says so rather than offering a button that fails: the database's
Backups tab explains it, and the API refuses a backup, a schedule or a restore
of one with `backup.not_offered`. For data you cannot lose, use an engine that
is backed up.

## Restoring

A restore replaces everything currently in the database with the contents of
the backup. There is no merge, and there is no undo.

Skifity asks you to confirm in words rather than with a button. A backup is
restored only into the database it was taken from, so its engine is always the
database's own; a Redis or Valkey restore is refused before anything is changed
when the database runs an older version than the one that would send it the
snapshot — a restore that half-works is worse than one that does not start.

While it runs, the database is unavailable and the apps connected to it will
report errors. That is expected and it says so before you begin.

Take a fresh backup first. The panel will offer.

## Failures

A failed backup is worth knowing about immediately, which is why
`backup.failed` and `backup.missed` are on by default in a new notification
channel — for volumes as well as databases. `backup.succeeded` is there for a
channel that wants every one; a channel with no list of events is not sent it.

**No storage configured.** Nothing has been backed up. Set it up under
Settings → Backup storage.

**Access denied, or the bucket does not exist.** The key pair cannot write to
that bucket. Check the bucket name and the region — a wrong region often
presents as a permission error rather than a missing bucket.

**The Job ran out of memory.** A dump of a large database needs room. The
message says how much it wanted.

**It succeeded but the file is tiny.** Almost always a database that is empty
because the app never wrote to the one you think it did. Check which database
the app is actually connected to on its Variables tab.

## The panel itself

The panel's own database — teams, apps, domains, settings, variables and
history — is copied to the same bucket, on its own schedule, once backup storage
is set up. It is a file on the first control plane server, and that server's
disk is the thing that fails; a copy beside it goes with it.

Under **Settings → Backup storage**:

* **Back up this panel** is when, as cron in UTC. Empty is every day at 03:17;
  `off` turns it off.
* **Panel backups to keep** is how many. Empty keeps 14. As with everything
  else, the old ones go after a new one succeeds, never before.

**Backups of this panel**, on the same page, lists what is in the bucket and
takes a copy now — which is what you want right before an upgrade. A copy that
fails is recorded there and sent to the notification channels of every team an
administrator belongs to, because it is not one team's problem.

The copies are in the bucket under `skifity/panel/`, named by date, compressed
with gzip. The secrets inside are sealed with the master key, which is never
uploaded: a bucket that leaks hands over ciphertext.

### Putting it back

Download the copy from the bucket to the panel's server, then:

```sh
sudo skifity admin restore-db ./20260930-031700-bak-9f2c-panel.db.gz
```

A sealed copy needs its passphrase in the environment:
`sudo SKIFITY_BACKUP_PASSPHRASE='…' skifity admin restore-db ./…panel.db.gz`.
The file is opened into a copy of its own and used only once every chunk has
been authenticated.

Without `--yes` it changes nothing. It checks that the file is a panel's
database, that SQLite finds it whole, that it has at least one account, and
whether its secrets open with the master key on this server — then prints what
it found and the three commands to run:

```sh
kubectl -n skifity-system scale deployment/skifity-panel --replicas=0
sudo skifity admin restore-db --yes ./20260930-031700-bak-9f2c-panel.db.gz
kubectl -n skifity-system scale deployment/skifity-panel --replicas=1
```

The database it replaces is not deleted: it is kept beside the new one as
`panel.db.before-restore-<time>`. A backup from an older version is brought up
to date the first time it is opened, the same way an upgrade would.

If it says the secrets do not open with the master key, put the key the backup
was taken with at `/etc/skifity/master.key` before starting the panel, or pass
the one you have with `--master-key`. A panel started with the wrong key comes
up with every secret unreadable.

## The cluster's own state

Everything Kubernetes knows — which apps exist, their configuration, the
certificates it issued — is in etcd, on the control plane server's disk. k3s
snapshots it by itself, and Skifity asks for a snapshot every six hours and
keeps the last twelve, which is three days:

```sh
ls /var/lib/rancher/k3s/server/db/snapshots/
```

They are a copy on the same disk. They save you from a bad upgrade or an object
somebody deleted; they do not save you from losing the server, which is what
copying that folder somewhere else is for. Skifity does not do that for you.

To put one back on a single-server cluster, stop k3s and reset the cluster from
the snapshot:

```sh
sudo systemctl stop k3s
sudo k3s server --cluster-reset --cluster-reset-restore-path=/var/lib/rancher/k3s/server/db/snapshots/<the one you want>
sudo systemctl start k3s
```

This has not been run against a real cluster by the people who wrote it. Read
[k3s's own page on it](https://docs.k3s.io/datastore/backup-restore) first, and
do it on a server you can rebuild.

## What is not backed up

The master key. A backup of anything — a database, a volume, the panel itself —
is useless without the key that decrypts the credentials stored alongside it,
and uploading it next to them would make the bucket the one thing an attacker
needs. Back it up somewhere else, once, and properly: the recovery key the panel
asked you to download at setup is the same secret in a form you can write on
paper. [Configuration](configuration.md#what-to-back-up) has the details.


## Volumes

A volume backup is a compressed tar of everything on the volume, taken while the
app keeps running. Take one from an app's **Storage** tab.

**A schedule and a restore** are both on the same tab, behind the clock icon
next to each disk: how often a copy is taken, how many to keep, and a button to
put one back.

The volume is mounted read-only for the copy. A backup that can write to the
thing it is copying is one bug away from being what destroyed it.

**It reads the files as the app does.** The copy and the restore run as the
user the app runs as, with the app's group. In an environment whose security
level lets a container run as root, they run as root with only what reading
and writing anybody's files takes, so a restore gives every file back to the
user it belonged to — `www-data` in the official WordPress image, for
instance. If an app writes files only its own user may read and the panel does
not know that user, the backup stops and says so: give the uid the app runs as
in its settings, and the backup uses it too.

**Restoring stops the app.** A volume is held by one server at a time and the
app has files open on it, so unpacking an archive underneath a running process
is how a restore makes things worse. Skifity scales the app to zero, waits for
the instance to actually be gone, unpacks, and starts it again at the size it
was — including when the restore fails, because an app left at zero instances
would be an outage caused by the thing that was meant to end one.

You are asked to confirm first, and told what it means: everything on the disk
now is replaced by what was in the archive. Take a copy of what is there if you
might want it.

Two things follow from a volume being ReadWriteOnce, which is what Kubernetes
calls a disk one server holds at a time:

* The backup runs on the same server as the app, and is scheduled there
  automatically. With the storage k3s ships this is already true of the volume
  itself, so nothing about it is visible.
* **A file being written while the copy runs may be caught half-written.** A
  tar is not a snapshot. For an upload directory or a cache that is fine. For
  something where a half-written file is worse than an old one — an embedded
  database on a volume, for instance — stop the app, take the backup, start it
  again, or keep that data in a managed database, where the dump is consistent
  by construction.

Restoring replaces the volume's contents entirely: it is "make it look like it
did", not "merge this over what is there". The archive is read through once
before anything is deleted, because unpacking a truncated archive over live data
leaves half the old files and half the new, which is worse than either.
