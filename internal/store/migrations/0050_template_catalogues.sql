-- A team's own template catalogues: a name and an https address that answers
-- a YAML or JSON index of templates, or a .tar.gz or .zip of them, in the
-- same schema as the catalogue built into the panel.
--
-- body is the last good copy, exactly as it was downloaded, and is replaced
-- only by a download that is a readable catalogue: a refresh that fails —
-- the host is down, the answer is an HTML error page, the archive does not
-- open — records why in last_error and leaves the templates people install
-- from as they were. It is kept as downloaded rather than as the templates
-- read from it so that every read checks it again with the rules of the
-- version of the panel doing the reading.
--
-- auth_header names the one header the address is asked with, for a private
-- Git host's raw files, and auth_value_enc is its value, sealed under
-- TemplateCatalogueContext: the team, the catalogue and its address, so a
-- row whose address is changed by hand does not send the token somewhere new.
-- The value is never answered by any API.
CREATE TABLE template_catalogues (
    id             TEXT PRIMARY KEY,
    team_id        TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    url            TEXT NOT NULL,
    auth_header    TEXT NOT NULL DEFAULT '',
    auth_value_enc TEXT NOT NULL DEFAULT '',
    body           BLOB,
    body_sha256    TEXT NOT NULL DEFAULT '',
    fetched_at     TEXT NOT NULL DEFAULT '',
    attempted_at   TEXT NOT NULL DEFAULT '',
    last_error     TEXT NOT NULL DEFAULT '',
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    UNIQUE (team_id, name),
    UNIQUE (team_id, url)
);

-- The logos of a catalogue's templates, fetched when the catalogue is and
-- served by the panel itself, so a browser is never sent to the catalogue's
-- host. content_type is what the bytes turned out to be, not what the host
-- said they were.
CREATE TABLE template_catalogue_icons (
    catalogue_id TEXT NOT NULL REFERENCES template_catalogues(id) ON DELETE CASCADE,
    template_id  TEXT NOT NULL,
    content_type TEXT NOT NULL,
    body         BLOB NOT NULL,
    PRIMARY KEY (catalogue_id, template_id)
);

-- Which catalogue an app's template came from: '' for the one built into the
-- panel. A template's id is only unique within its catalogue, so an update is
-- looked for where the app came from and nowhere else.
ALTER TABLE app_templates ADD COLUMN catalogue_id TEXT NOT NULL DEFAULT '';
