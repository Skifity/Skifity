-- What a preview gets for a variable: the app's own value (the default), a
-- value of its own, or nothing.
--
-- A preview is a copy of an app for a pull request, and copying every value
-- is how a branch ends up charging real cards with the live payment key, or
-- sending mail to real customers. preview_value_enc is sealed like value_enc,
-- under a context of its own, so one cannot be swapped for the other.
ALTER TABLE app_variables ADD COLUMN preview_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE app_variables ADD COLUMN preview_value_enc TEXT NOT NULL DEFAULT '';
