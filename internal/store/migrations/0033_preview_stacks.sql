-- Previews of a whole environment, and the command that fills one with data.
--
-- preview_stack on an environment makes a pull request's preview a copy of
-- every app in it, not only the ones built from the pull request's repository:
-- the others run at the version they run here. preview_seed on an app is a
-- command run once in each new preview of it, after its first deploy — the
-- demo data an empty preview database needs — and seeded_at records that it
-- was, so a later push to the pull request does not seed a second time.
ALTER TABLE environments ADD COLUMN preview_stack INTEGER NOT NULL DEFAULT 0;
ALTER TABLE apps ADD COLUMN preview_seed TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN seeded_at TEXT NOT NULL DEFAULT '';
