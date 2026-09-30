-- migrate: rebuilds a table
--
-- A Git connection can be to Bitbucket Cloud as well as GitHub, GitLab, Gitea
-- and plain Git.
--
-- kind carried a CHECK that named the kinds there were, and SQLite cannot
-- change a CHECK in place, so the table is rebuilt: made anew, the rows copied
-- across by name, the old one dropped, the new one renamed. The migrator runs
-- this with foreign keys off, which is what stops the drop from setting every
-- app's git_source_id to NULL through its ON DELETE SET NULL (see
-- rebuildMarker in store.go).
--
-- 'github_app' stays allowed. The panel no longer creates such connections,
-- but one made before that may still be in somebody's database, and a
-- migration that fails on it would stop the panel from starting.
CREATE TABLE git_sources_new (
    id          TEXT PRIMARY KEY,
    team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('github_app','github_pat','gitlab','gitea','bitbucket','generic')),
    name        TEXT NOT NULL,
    base_url    TEXT NOT NULL DEFAULT '',
    account     TEXT NOT NULL DEFAULT '',
    config_enc  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

INSERT INTO git_sources_new (id, team_id, kind, name, base_url, account, config_enc, created_at, updated_at)
SELECT id, team_id, kind, name, base_url, account, config_enc, created_at, updated_at
FROM git_sources;

DROP TABLE git_sources;
ALTER TABLE git_sources_new RENAME TO git_sources;
