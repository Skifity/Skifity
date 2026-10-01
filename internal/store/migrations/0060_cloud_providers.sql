-- A team's connections to a cloud provider, and the servers the panel created
-- through one. See ADR-0024.
--
-- A connection is one provider project's API token, sealed under the
-- connection's own id. token_hint is the last four characters, so a person
-- can tell two connections apart without the panel ever showing the token.
CREATE TABLE cloud_providers (
    id          TEXT PRIMARY KEY,
    team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    name        TEXT NOT NULL,
    token_enc   TEXT NOT NULL,
    token_hint  TEXT NOT NULL DEFAULT '',
    checked_at  TEXT,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (team_id, name)
);

-- One row for each server the panel ordered from a provider. It is what makes
-- "delete the machine too" possible, and what keeps it from ever reaching a
-- machine the panel did not create: only a server with a row here, and a
-- machine id in it, is offered, and the machine must still carry the label
-- the panel gave it.
--
-- provider_id has no ON DELETE: a connection cannot go while a server it
-- created is still here, because deleting that server's machine needs it.
-- Deleting the whole team removes both in one statement, which SQLite checks
-- at the statement's end.
CREATE TABLE cloud_servers (
    server_id        TEXT PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    provider_id      TEXT NOT NULL REFERENCES cloud_providers(id),
    machine_id       TEXT NOT NULL DEFAULT '',
    location         TEXT NOT NULL,
    server_type      TEXT NOT NULL,
    image            TEXT NOT NULL,
    ssh_access       TEXT NOT NULL DEFAULT 'anywhere' CHECK (ssh_access IN ('anywhere','cluster')),
    firewall_id      TEXT NOT NULL DEFAULT '',
    ssh_key_id       TEXT NOT NULL DEFAULT '',
    host_key_rotated INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE INDEX cloud_servers_provider ON cloud_servers (provider_id);
