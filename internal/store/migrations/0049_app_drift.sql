-- What the watcher last found when it compared an app's objects in the
-- cluster with what the panel applies, and whether to put them back by
-- itself.
--
-- The README tells people they can use kubectl, and nothing noticed when they
-- did: an edited Deployment ran as edited until the next deploy, and the app's
-- page described something that was no longer there. The watcher now compares
-- the two every few minutes.
--
-- A row is kept per app so a change is said once: fingerprint is what was
-- found, notified is what somebody was told about, and a notification goes
-- out only when the two differ. since is when the app stopped matching.
-- auto_repair is off unless somebody turns it on: putting something back that
-- a person changed on purpose, during an incident, is not a default.
--
-- applied is the objects the last apply of the app wrote, as a JSON object of
-- "Kind/name" to the fingerprint it carried. An object the panel would apply
-- and the cluster does not have was deleted by somebody only if it is in this
-- list; otherwise it is one the panel has yet to create — a domain added while
-- the cluster could not be reached — and not somebody else's doing. Empty is
-- an app last applied before this was kept.
CREATE TABLE app_drift (
    app_id      TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'in_sync',
    items       TEXT NOT NULL DEFAULT '[]',
    fingerprint TEXT NOT NULL DEFAULT '',
    notified    TEXT NOT NULL DEFAULT '',
    since       TEXT NOT NULL DEFAULT '',
    checked_at  TEXT NOT NULL DEFAULT '',
    auto_repair INTEGER NOT NULL DEFAULT 0,
    applied     TEXT NOT NULL DEFAULT ''
);
