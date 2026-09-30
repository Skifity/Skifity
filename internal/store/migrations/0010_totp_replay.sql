-- The counter of the last TOTP code that was accepted for this account.
--
-- RFC 6238 is explicit that a one-time password must be usable exactly once:
-- the panel accepts a code for the current thirty-second step and one either
-- side, so without this a code seen over somebody's shoulder, read off a
-- screen share or captured by a proxy stays valid for up to ninety seconds.
-- Zero means no code has been accepted yet.
ALTER TABLE users ADD COLUMN totp_last_counter INTEGER NOT NULL DEFAULT 0;
