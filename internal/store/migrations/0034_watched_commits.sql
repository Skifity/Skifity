-- The last push an app was checked against and skipped, because it touched
-- nothing the app watches, and the deployment it was running then.
--
-- A push's files are the difference from the commit before it. An app skipped
-- by one push still runs the commit before that push, so the next push — whose
-- "before" is the skipped one — could not be compared with anything the app
-- ran, and every monorepo app rebuilt on every other push. The skipped commit
-- stands in for the running one for as long as that deployment is the one
-- running.
CREATE TABLE app_watched_commits (
    app_id        TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    commit_sha    TEXT NOT NULL,
    deployment_id TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
