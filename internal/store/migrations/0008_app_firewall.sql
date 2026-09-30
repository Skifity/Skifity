-- An app's firewall rules.
--
-- One row per app rather than a table of rules: the rules are an ordered list
-- of trees, and a schema that flattened them into rows would have to rebuild
-- the order and the nesting on every read, for no query anybody wants to run.
-- Nothing ever searches inside a rule; the whole set is read together, written
-- together, and rendered to the guard together.
--
-- enabled is separate from having no rules, because turning the firewall off
-- for ten minutes to check whether it is the cause of something is the first
-- thing anybody does, and deleting the rules to do it is not acceptable.
CREATE TABLE app_firewalls (
    app_id     TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    enabled    INTEGER NOT NULL DEFAULT 0,
    -- The RuleSet as edgerules writes it.
    rules      TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL,
    updated_by TEXT NOT NULL DEFAULT ''
);
