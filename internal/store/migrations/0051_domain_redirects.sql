-- A hostname that sends its visitors to another of the same app's: www to
-- the bare domain, or an old name to the new one. Empty, what every existing
-- domain has, serves the app.
ALTER TABLE domains ADD COLUMN redirect_to TEXT NOT NULL DEFAULT '';
