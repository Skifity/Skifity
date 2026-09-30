-- A team can require a second factor or single sign-on of everybody in it.
--
-- A session now records how it was signed into, because that is what the
-- requirement is about: a password alone, a password and a code, or the
-- identity provider. Sessions from before this say nothing, which counts as
-- a password alone.
ALTER TABLE sessions ADD COLUMN method TEXT NOT NULL DEFAULT '';
ALTER TABLE teams ADD COLUMN require_strong_auth INTEGER NOT NULL DEFAULT 0;
