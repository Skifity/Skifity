-- Deploys to an app can be locked, with a reason, the way `kamal lock` does.
--
-- During an incident, a migration somebody is running by hand, or a freeze
-- before a launch, a push to the branch should not ship. A lock stops every
-- deploy and rollback of the app — from the panel, the CLI, an assistant or a
-- webhook — until somebody unlocks it, and says who locked it and why.
-- Changing a variable or the instance count still applies: those are not new
-- code, and an incident is often exactly when they are needed.
CREATE TABLE app_deploy_locks (
    app_id    TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    reason    TEXT NOT NULL,
    locked_by TEXT NOT NULL DEFAULT '',
    locked_at TEXT NOT NULL
);
