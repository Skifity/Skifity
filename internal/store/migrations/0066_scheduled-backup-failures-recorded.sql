-- A backup schedule goes with what it backs up, and a backup that did not
-- happen says why in a word the interface can translate.
--
-- backup_policies has no foreign key: target_id names a database or a volume,
-- two tables. Nothing deleted a schedule with its database, and deleting an
-- environment, a project or a team takes its databases and volumes by cascade
-- without asking any delete function, so a schedule for nothing came round
-- every night. These triggers run for a row deleted by a cascade as well as by
-- a DELETE, which is the reason they are triggers.
--
-- A table rebuilt the 0047 way drops its triggers with it: a migration that
-- rebuilds databases or volumes creates these again. The store's tests delete
-- through every cascade, so one that does not fails there.
CREATE TRIGGER databases_take_their_backup_policy AFTER DELETE ON databases
BEGIN
    DELETE FROM backup_policies WHERE target_type = 'database' AND target_id = OLD.id;
END;

CREATE TRIGGER volumes_take_their_backup_policy AFTER DELETE ON volumes
BEGIN
    DELETE FROM backup_policies WHERE target_type = 'volume' AND target_id = OLD.id;
END;

-- The schedules already left behind.
DELETE FROM backup_policies
    WHERE (target_type = 'database' AND target_id NOT IN (SELECT id FROM databases))
       OR (target_type = 'volume' AND target_id NOT IN (SELECT id FROM volumes));

-- Why a backup was skipped or could not start, as a code: the database was
-- stopped (database.stopped), something else was being done to it
-- (database.busy), or the problem's own code. error_message says it in
-- English for the API and the CLI; the panel says it in five languages.
ALTER TABLE backups ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
UPDATE backups SET error_code = 'database.stopped' WHERE status = 'skipped';
