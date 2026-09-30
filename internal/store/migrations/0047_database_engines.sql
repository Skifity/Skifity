-- migrate: rebuilds a table
--
-- Nine database engines where there were three. The engine column carried a
-- CHECK naming postgres, redis and mysql, and SQLite cannot change a CHECK in
-- place, so the table is rebuilt: made anew, the rows copied across, the old
-- one dropped, the new one renamed. The migrator runs this with foreign keys
-- off (see rebuildMarker in store.go), so the links and backups of every
-- database survive it.
--
-- Every database recorded as mysql until now runs MariaDB: the panel offered
-- one MySQL-compatible engine under that name and rendered the mariadb image
-- for it. mysql now means MySQL itself, so those rows are renamed to what they
-- are. Nothing in the cluster changes — the StatefulSet, its volume, its
-- Secret and the apps' variables stay as they were — and a backup of one is
-- still taken and restored with MariaDB's own tools, which is what it needs.
CREATE TABLE databases_new (
    id              TEXT PRIMARY KEY,
    environment_id  TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL,
    engine          TEXT NOT NULL CHECK (engine IN ('postgres','mysql','mariadb','mongodb',
                                                    'redis','valkey','dragonfly','clickhouse',
                                                    'memcached')),
    engine_version  TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'creating',
    status_detail   TEXT NOT NULL DEFAULT '',
    instances       INTEGER NOT NULL DEFAULT 1,
    storage_gb      INTEGER NOT NULL DEFAULT 5,
    cpu_request_m   INTEGER NOT NULL DEFAULT 100,
    mem_request_mb  INTEGER NOT NULL DEFAULT 256,
    credentials_enc TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (environment_id, slug)
);

INSERT INTO databases_new (id, environment_id, name, slug, engine, engine_version, status,
        status_detail, instances, storage_gb, cpu_request_m, mem_request_mb, credentials_enc,
        created_at, updated_at)
    SELECT id, environment_id, name, slug,
           CASE engine WHEN 'mysql' THEN 'mariadb' ELSE engine END,
           engine_version, status, status_detail, instances, storage_gb, cpu_request_m,
           mem_request_mb, credentials_enc, created_at, updated_at
    FROM databases;

DROP TABLE databases;
ALTER TABLE databases_new RENAME TO databases;
