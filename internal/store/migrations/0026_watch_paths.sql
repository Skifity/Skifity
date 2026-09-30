-- The paths an app watches, one pattern per line.
--
-- In a monorepo every push to the branch rebuilt every app built from it,
-- including the ones whose code the push never touched. When an app names
-- the paths it depends on, a push that changes none of them is skipped.
-- Empty means everything, which is what every existing app did.
ALTER TABLE apps ADD COLUMN watch_paths TEXT NOT NULL DEFAULT '';
