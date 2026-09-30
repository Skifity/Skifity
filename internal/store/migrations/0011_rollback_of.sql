-- The deployment number this deployment rolled back to, or zero.
--
-- A rollback used to record its own trigger as the English sentence
-- "rollback to #3". The panel ships in five languages, so that sentence was
-- never translated, and the deployments list matched the trigger against
-- "rollback", missed, and labelled the row "a person" instead. The number is
-- data, so it is stored as data and the sentence is built in the interface.
ALTER TABLE deployments ADD COLUMN rollback_of INTEGER NOT NULL DEFAULT 0;
