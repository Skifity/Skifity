-- Commits a deployed push moved an app's branch past.
--
-- A Git host does not promise to deliver pushes in order, and a signed
-- delivery is good for ever, so a push that arrived late — or one sent again —
-- deployed an older commit over a newer one already running. A push is
-- skipped when its commit is one a later push already moved past, unless it
-- was forced, which is somebody going back on purpose.
CREATE TABLE app_passed_commits (
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL,
    passed_by  TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (app_id, commit_sha)
);
