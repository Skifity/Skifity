-- The domain the panel was put on from Settings, and where moving it stands.
--
-- There is one panel, so one row. Moving the panel is slow, and its last step
-- restarts the panel: the row is what the panel reads when it starts again to
-- know whether it now answers where it was asked to, and what it keeps
-- answering on for the webhooks registered at its earlier addresses.
CREATE TABLE panel_domain (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    domain      TEXT NOT NULL,
    -- The hosts the panel answered on before, as a JSON array, oldest first.
    previous    TEXT NOT NULL DEFAULT '[]',
    -- moving, restarting, live or failed.
    state       TEXT NOT NULL,
    -- Why the move stopped, as the Problem the API answered with, when it did.
    problem     TEXT NOT NULL DEFAULT '',
    started_by  TEXT NOT NULL DEFAULT '',
    started_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
