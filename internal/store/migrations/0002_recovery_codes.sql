-- Recovery codes for two-factor authentication.
--
-- The panel generated eight of these, showed them once, and then threw them
-- away, so the only thing they ever recovered was the user's confidence. They
-- are stored hashed, exactly like a session token: the panel never needs to
-- show a code again, only to recognise one.
CREATE TABLE recovery_codes (
    id        TEXT PRIMARY KEY,
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    -- Set the moment a code is accepted, so each one works exactly once.
    used_at   TEXT
);

CREATE UNIQUE INDEX recovery_codes_hash ON recovery_codes(code_hash);
CREATE INDEX recovery_codes_user ON recovery_codes(user_id);
