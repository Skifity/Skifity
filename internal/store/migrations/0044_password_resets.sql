-- A password forgotten, and reset through a link sent to the account's
-- address. The token itself is never stored, only its hash, so a copy of this
-- database resets nobody's password; each is good once, for half an hour.
CREATE TABLE password_resets (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT
);
CREATE INDEX password_resets_user ON password_resets (user_id);
