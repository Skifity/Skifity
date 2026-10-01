-- A team's log drains: somewhere outside the cluster its apps' logs are
-- shipped to, by the collector the panel runs on every server.
--
-- settings is the drain's form as JSON, without its secrets: the address, a
-- username, a dataset. secrets_enc is the rest — a token, a password, an API
-- key — as one sealed JSON object, under LogDrainContext: the team, the drain
-- and the address it sends to, so a row whose address is changed by hand does
-- not send the credential somewhere new. The secrets are never answered by
-- any API.
--
-- scoped is the switch and log_drain_projects the list, for the reason the
-- notification channels have both (0043): with only the list, deleting the
-- last project a drain was limited to would quietly start sending it every
-- other project's logs. A scoped drain whose projects are all gone sends
-- nothing.
--
-- tested_at and test_error are the last test the panel sent through it; a
-- drain is not saved until a test has been taken.
CREATE TABLE log_drains (
    id             TEXT PRIMARY KEY,
    team_id        TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind <> ''),
    settings       TEXT NOT NULL DEFAULT '{}',
    secrets_enc    TEXT NOT NULL DEFAULT '',
    enabled        INTEGER NOT NULL DEFAULT 1,
    scoped         INTEGER NOT NULL DEFAULT 0,
    include_builds INTEGER NOT NULL DEFAULT 0,
    tested_at      TEXT NOT NULL DEFAULT '',
    test_error     TEXT NOT NULL DEFAULT '',
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    UNIQUE (team_id, name)
);
CREATE INDEX idx_log_drains_team ON log_drains(team_id);

CREATE TABLE log_drain_projects (
    drain_id   TEXT NOT NULL REFERENCES log_drains(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    PRIMARY KEY (drain_id, project_id)
);
CREATE INDEX idx_log_drain_projects_project ON log_drain_projects(project_id);
