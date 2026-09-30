-- Variables whose values live in a secret manager the team already runs:
-- HashiCorp Vault or OpenBao, Infisical, Doppler, AWS Secrets Manager.
--
-- A connection is how the panel signs in to one. Its settings — an address, a
-- mount, a region — are not secret and are stored as they are. What it signs
-- in with is sealed, under a context naming the connection, so a row copied to
-- another connection does not open.
--
-- The periodic refresh is off unless somebody turns it on. When it is on,
-- next_refresh_at is when it is due, and refresh_failures counts the runs in a
-- row that failed, which is what the backoff doubles on.
CREATE TABLE secret_connections (
    id               TEXT PRIMARY KEY,
    team_id          TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    kind             TEXT NOT NULL,
    settings         TEXT NOT NULL DEFAULT '{}',
    credentials_enc  TEXT NOT NULL,
    refresh_minutes  INTEGER NOT NULL DEFAULT 0,
    next_refresh_at  TEXT NOT NULL DEFAULT '',
    refresh_failures INTEGER NOT NULL DEFAULT 0,
    last_refresh_at  TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    UNIQUE (team_id, name)
);

-- A variable can be a reference instead of a value: which connection, which
-- secret, and which key of it. None of the three is secret, and the value
-- itself is never stored here — value_enc stays empty for a reference.
ALTER TABLE app_variables ADD COLUMN ref_connection_id TEXT NOT NULL DEFAULT '';
ALTER TABLE app_variables ADD COLUMN ref_path TEXT NOT NULL DEFAULT '';
ALTER TABLE app_variables ADD COLUMN ref_key TEXT NOT NULL DEFAULT '';
ALTER TABLE shared_variables ADD COLUMN ref_connection_id TEXT NOT NULL DEFAULT '';
ALTER TABLE shared_variables ADD COLUMN ref_path TEXT NOT NULL DEFAULT '';
ALTER TABLE shared_variables ADD COLUMN ref_key TEXT NOT NULL DEFAULT '';

-- What each referenced variable held when it last reached an app, as a digest
-- sealed like everything else: enough to say which of them changed at the
-- next refresh, and nothing an offline guess at a short secret could be
-- checked against without the master key.
CREATE TABLE app_reference_digests (
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    digest_enc TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (app_id, key)
);
