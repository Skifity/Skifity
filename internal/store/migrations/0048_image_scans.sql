-- What Trivy found in the image an app runs.
--
-- One row per scan, whether it worked or not: a scan that could not download
-- its database is worth showing as such, rather than as an app nobody looked
-- at. The counts are per severity over every finding; findings holds at most
-- the first two hundred, most severe first, as JSON, and omitted says how many
-- more there were — a report for an old full-fat image runs to thousands of
-- rows and megabytes, and the panel's database is one file on one disk.
--
-- deployment_id is the deployment whose image was scanned, when the scan came
-- from one. It is kept loose (SET NULL) because deployments are pruned and a
-- scan of the image an app still runs is worth keeping past its deployment's
-- record.
--
-- announced is a report whose critical findings the team has been told about,
-- or had nothing new to be told. The newest such report is what the next one
-- is compared with, so a critical is sent once; a report on an image that a
-- deploy was stopped for never went out, and is never announced.
CREATE TABLE image_scans (
    id               TEXT PRIMARY KEY,
    app_id           TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    deployment_id    TEXT REFERENCES deployments(id) ON DELETE SET NULL,
    image            TEXT NOT NULL,
    digest           TEXT NOT NULL DEFAULT '',
    trigger          TEXT NOT NULL CHECK (trigger IN ('deploy','schedule','manual')),
    status           TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed')),
    critical         INTEGER NOT NULL DEFAULT 0,
    high             INTEGER NOT NULL DEFAULT 0,
    medium           INTEGER NOT NULL DEFAULT 0,
    low              INTEGER NOT NULL DEFAULT 0,
    unknown          INTEGER NOT NULL DEFAULT 0,
    fixable          INTEGER NOT NULL DEFAULT 0,
    fixable_critical INTEGER NOT NULL DEFAULT 0,
    findings         TEXT NOT NULL DEFAULT '[]',
    omitted          INTEGER NOT NULL DEFAULT 0,
    scanner_version  TEXT NOT NULL DEFAULT '',
    os               TEXT NOT NULL DEFAULT '',
    error_code       TEXT NOT NULL DEFAULT '',
    error_message    TEXT NOT NULL DEFAULT '',
    error_hint       TEXT NOT NULL DEFAULT '',
    announced        INTEGER NOT NULL DEFAULT 0,
    requested_by     TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    started_at       TEXT,
    finished_at      TEXT
);
CREATE INDEX idx_image_scans_app ON image_scans(app_id, created_at);

-- A deploy somebody let through although its image has a critical
-- vulnerability with a fix, when the panel is set to stop those. Recorded on
-- the deployment, beside who created it, so the history says which versions
-- went out that way; the activity log says it too.
ALTER TABLE deployments ADD COLUMN accepted_vulnerabilities INTEGER NOT NULL DEFAULT 0;
