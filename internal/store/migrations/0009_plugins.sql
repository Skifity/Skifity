-- Installed plugins.
--
-- The manifest is kept as it was at install rather than re-fetched, because it
-- is what an administrator read and agreed to. A publisher who changes their
-- manifest changes what the plugin may do, and that has to be an upgrade
-- somebody approves — not a fact that quietly becomes true.
--
-- token_id is the API token issued to this plugin, so that removing the plugin
-- revokes it rather than leaving a token nobody can trace to anything.
CREATE TABLE plugins (
    id           TEXT PRIMARY KEY,
    -- The manifest as published, verbatim, for showing what was agreed to.
    manifest     TEXT NOT NULL,
    version      TEXT NOT NULL,
    -- Where it came from, so an upgrade can be looked for in the same place.
    source_url   TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'installing'
                 CHECK (status IN ('installing','running','failed','disabled')),
    status_detail TEXT NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    token_id     TEXT NOT NULL DEFAULT '',
    -- A secret shared with the plugin, so it can tell an event from the panel
    -- from a request anything in the cluster could have made. Sealed.
    hmac_sealed  TEXT NOT NULL DEFAULT '',
    installed_at TEXT NOT NULL,
    installed_by TEXT NOT NULL DEFAULT ''
);

-- A plugin's own settings, sealed when the manifest said so.
CREATE TABLE plugin_settings (
    plugin_id TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    key       TEXT NOT NULL,
    value     TEXT NOT NULL,
    encrypted INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (plugin_id, key)
);
