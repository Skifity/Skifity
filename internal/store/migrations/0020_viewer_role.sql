-- migrate: rebuilds a table
--
-- A fourth role, viewer: everything a member can see, nothing a member can
-- change. The role column carried a CHECK naming the three roles there were,
-- and SQLite cannot change a CHECK in place, so the table is rebuilt: made
-- anew, the rows copied across, the old one dropped, the new one renamed. The
-- migrator runs this with foreign keys off (see rebuildMarker in store.go).
--
-- team_invitations.role has no CHECK, so an invitation needs nothing here.
CREATE TABLE memberships_new (
    team_id    TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('owner','admin','member','viewer')),
    created_at TEXT NOT NULL,
    PRIMARY KEY (team_id, user_id)
);

INSERT INTO memberships_new (team_id, user_id, role, created_at)
    SELECT team_id, user_id, role, created_at FROM memberships;

DROP TABLE memberships;
ALTER TABLE memberships_new RENAME TO memberships;
CREATE INDEX idx_memberships_user ON memberships(user_id);
