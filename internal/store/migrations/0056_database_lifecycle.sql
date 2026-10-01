-- migrate: rebuilds a table
--
-- Managing a database's life: stopping and starting it, resizing it, changing
-- its password and importing a dump into it.
--
-- Three columns on databases, which ALTER TABLE can add:
--
--   * mem_limit_mb and cpu_limit_m are what a database may use, beside what it
--     reserves. The memory limit was the engine's default and nowhere else, so
--     it could not be changed; every row gets its engine's default now, the
--     number its StatefulSet or cluster already carries. A CPU limit of 0 is
--     none, which is what every database has run with so far.
--   * credentials_next_enc holds the new credentials while a password change
--     is under way, sealed like credentials_enc. It is written before the
--     database is asked to take the new password, so the one copy of that
--     password is never only in the memory of a panel that might restart.
--
-- And one CHECK, which it cannot: a scheduled backup of a stopped database is
-- recorded as skipped, with the reason, rather than as a failure. So the
-- backups table is rebuilt the way 0047 rebuilt databases, with every column
-- it has gained since 0001 (encrypted, verified_at and verify_error, in 0031).

ALTER TABLE databases ADD COLUMN mem_limit_mb INTEGER NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN cpu_limit_m INTEGER NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN credentials_next_enc TEXT NOT NULL DEFAULT '';

UPDATE databases SET mem_limit_mb = CASE engine
    WHEN 'clickhouse' THEN 2048
    WHEN 'memcached' THEN 256
    ELSE 1024
END;

CREATE TABLE backups_new (
    id            TEXT PRIMARY KEY,
    target_type   TEXT NOT NULL,
    target_id     TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','skipped')),
    kind          TEXT NOT NULL DEFAULT 'scheduled',
    location      TEXT NOT NULL DEFAULT '',
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    finished_at   TEXT,
    encrypted     INTEGER NOT NULL DEFAULT 0,
    verified_at   TEXT,
    verify_error  TEXT NOT NULL DEFAULT ''
);

INSERT INTO backups_new (id, target_type, target_id, status, kind, location, size_bytes,
        error_message, created_at, finished_at, encrypted, verified_at, verify_error)
    SELECT id, target_type, target_id, status, kind, location, size_bytes,
           error_message, created_at, finished_at, encrypted, verified_at, verify_error
    FROM backups;

DROP TABLE backups;
ALTER TABLE backups_new RENAME TO backups;
CREATE INDEX idx_backups_target ON backups(target_type, target_id, created_at DESC);
