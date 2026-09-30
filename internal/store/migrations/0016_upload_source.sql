-- migrate: rebuilds a table
--
-- An app can now come from an uploaded folder as well as a repository or an
-- image: `skifity up` sends the code of somebody who has no repository at all,
-- which is most people whose first app an assistant wrote.
--
-- source_type carried a CHECK that named the three sources there were, and
-- SQLite cannot change a CHECK in place, so the table is rebuilt: made anew,
-- the rows copied across by name, the old one dropped, the new one renamed.
-- The migrator runs this with foreign keys off, which is what stops the drop
-- from cascading into every table that references an app (see
-- rebuildMarker in store.go).
--
-- 'compose' stays allowed. The panel no longer creates such apps, but one made
-- before that may still be in somebody's database, and a migration that fails
-- on it would stop the panel from starting.
CREATE TABLE apps_new (
    id              TEXT PRIMARY KEY,
    environment_id  TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL,
    source_type     TEXT NOT NULL DEFAULT 'git' CHECK (source_type IN ('git','image','upload','compose')),
    git_source_id   TEXT REFERENCES git_sources(id) ON DELETE SET NULL,
    repo_url        TEXT NOT NULL DEFAULT '',
    branch          TEXT NOT NULL DEFAULT '',
    root_dir        TEXT NOT NULL DEFAULT '',
    builder         TEXT NOT NULL DEFAULT 'auto',
    dockerfile_path TEXT NOT NULL DEFAULT '',
    image           TEXT NOT NULL DEFAULT '',
    port            INTEGER NOT NULL DEFAULT 0,
    health_path     TEXT NOT NULL DEFAULT '',
    start_command   TEXT NOT NULL DEFAULT '',
    replicas        INTEGER NOT NULL DEFAULT 1,
    autoscale       INTEGER NOT NULL DEFAULT 0,
    min_replicas    INTEGER NOT NULL DEFAULT 1,
    max_replicas    INTEGER NOT NULL DEFAULT 3,
    cpu_target      INTEGER NOT NULL DEFAULT 75,
    memory_target   INTEGER NOT NULL DEFAULT 0,
    scale_to_zero   INTEGER NOT NULL DEFAULT 0,
    cpu_request_m   INTEGER NOT NULL DEFAULT 50,
    cpu_limit_m     INTEGER NOT NULL DEFAULT 1000,
    mem_request_mb  INTEGER NOT NULL DEFAULT 128,
    mem_limit_mb    INTEGER NOT NULL DEFAULT 512,
    auto_deploy     INTEGER NOT NULL DEFAULT 1,
    preview_deploys INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'created',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    release_command TEXT NOT NULL DEFAULT '',
    build_command   TEXT NOT NULL DEFAULT '',
    static_dir      TEXT NOT NULL DEFAULT '',
    UNIQUE (environment_id, slug)
);

INSERT INTO apps_new (
    id, environment_id, name, slug, source_type, git_source_id, repo_url, branch,
    root_dir, builder, dockerfile_path, image, port, health_path, start_command,
    replicas, autoscale, min_replicas, max_replicas, cpu_target, memory_target,
    scale_to_zero, cpu_request_m, cpu_limit_m, mem_request_mb, mem_limit_mb,
    auto_deploy, preview_deploys, status, created_at, updated_at,
    release_command, build_command, static_dir
)
SELECT
    id, environment_id, name, slug, source_type, git_source_id, repo_url, branch,
    root_dir, builder, dockerfile_path, image, port, health_path, start_command,
    replicas, autoscale, min_replicas, max_replicas, cpu_target, memory_target,
    scale_to_zero, cpu_request_m, cpu_limit_m, mem_request_mb, mem_limit_mb,
    auto_deploy, preview_deploys, status, created_at, updated_at,
    release_command, build_command, static_dir
FROM apps;

DROP TABLE apps;
ALTER TABLE apps_new RENAME TO apps;
