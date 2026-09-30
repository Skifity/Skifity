-- Scheduled commands.
--
-- A nightly report, an hourly cleanup, a weekly digest. Every product like
-- this needs them, and the alternative is a cron line on a server that nobody
-- remembers is there — which stops working the day the server is replaced, and
-- nothing says so.
--
-- Kubernetes does the scheduling; this table is what the panel applies from.
CREATE TABLE app_jobs (
    id         TEXT PRIMARY KEY,
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    schedule   TEXT NOT NULL,
    command    TEXT NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (app_id, name)
);

CREATE INDEX idx_app_jobs_app ON app_jobs(app_id);
