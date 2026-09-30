-- Whether a backup was sealed with the backup passphrase, and when it was last
-- shown to open and read through.
--
-- A backup nobody has tried to restore is a hope. Verifying one downloads it,
-- opens it when it is sealed — every chunk authenticated, none missing — and
-- reads the archive to its end, which is everything a restore does short of
-- writing to a database.
ALTER TABLE backups ADD COLUMN encrypted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE backups ADD COLUMN verified_at TEXT;
ALTER TABLE backups ADD COLUMN verify_error TEXT NOT NULL DEFAULT '';
