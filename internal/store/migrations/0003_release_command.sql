-- The release command.
--
-- A command that runs after the image is built and before any traffic reaches
-- the new version. That is where a database migration belongs: run it too
-- early and the old code meets the new schema, run it too late and the new
-- code meets the old one, and run it by hand and somebody eventually forgets.
ALTER TABLE apps ADD COLUMN release_command TEXT NOT NULL DEFAULT '';
