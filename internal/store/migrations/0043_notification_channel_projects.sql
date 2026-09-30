-- migrate: rebuilds a table
--
-- A notification channel can be limited to some of a team's projects, and it
-- can be any kind the panel sends, not only the four there were at the start.
--
-- kind carried a CHECK naming email, Telegram, Discord and a webhook. Slack,
-- Mattermost, ntfy, Pushover and every kind a plugin provides were added to
-- the panel after it, and every one of them was refused by the database the
-- moment somebody saved it: the form validated the channel, sealed its
-- settings, and then failed on a constraint nobody had widened. The kinds are
-- validated by internal/notify, which is the one place that knows them — a
-- plugin's kind is plugin:<plugin>/<provider> and cannot be listed here — so
-- the CHECK is gone rather than widened again. SQLite cannot drop a CHECK in
-- place, so the table is rebuilt; the migrator runs this with foreign keys off
-- (see rebuildMarker in store.go).
--
-- scoped is the switch and notification_channel_projects is the list, for the
-- same reason memberships have both (0021): with only the list, "no rows"
-- would have to mean "every project", and deleting the last project a channel
-- was limited to would quietly start sending it every other project's events.
-- With the switch, a limited channel whose projects are all gone hears only
-- what belongs to no project: the servers, and the panel itself.
CREATE TABLE notification_channels_new (
    id         TEXT PRIMARY KEY,
    team_id    TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind <> ''),
    name       TEXT NOT NULL,
    config_enc TEXT NOT NULL DEFAULT '',
    events     TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    scoped     INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO notification_channels_new (id, team_id, kind, name, config_enc, events, enabled, created_at, updated_at)
SELECT id, team_id, kind, name, config_enc, events, enabled, created_at, updated_at
FROM notification_channels;

DROP TABLE notification_channels;
ALTER TABLE notification_channels_new RENAME TO notification_channels;
CREATE INDEX idx_notification_channels_team ON notification_channels(team_id);

CREATE TABLE notification_channel_projects (
    channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    PRIMARY KEY (channel_id, project_id)
);
CREATE INDEX idx_notification_channel_projects_project ON notification_channel_projects(project_id);
