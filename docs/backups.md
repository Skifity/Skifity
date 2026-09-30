# Backups

Skifity backs up managed databases — PostgreSQL, MariaDB and Redis — and the
volumes your apps write files to.

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

Times are the server's, not yours.

## Restoring

A restore replaces everything currently in the database with the contents of
the backup. There is no merge, and there is no undo.

Skifity asks you to confirm in words rather than with a button, and refuses
outright if the backup is from a different engine or a much newer version — a
restore that half-works is worse than one that does not start.

While it runs, the database is unavailable and the apps connected to it will
report errors. That is expected and it says so before you begin.

Take a fresh backup first. The panel will offer.

## Failures

A failed backup is worth knowing about immediately, which is why
`backup.failed` is on by default in a new notification channel.

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

## What is not backed up

The panel's own state and the master key. They are files on the control plane
server, and [Configuration](configuration.md) says which ones and why they
should not be kept together.

A backup of a database is useless without the master key that decrypts the
credentials stored alongside it, so back that up somewhere else, once, and
properly.


## Volumes

A volume backup is a compressed tar of everything on the volume, taken while the
app keeps running. Take one from an app's **Storage** tab.

**A schedule and a restore** are both on the same tab, behind the clock icon
next to each disk: how often a copy is taken, how many to keep, and a button to
put one back.

The volume is mounted read-only for the copy. A backup that can write to the
thing it is copying is one bug away from being what destroyed it.

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
