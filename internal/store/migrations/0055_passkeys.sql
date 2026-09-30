-- Passkeys: WebAuthn credentials that sign an account in without a password.
--
-- A passkey is a key pair made by the person's device or security key. The
-- private half never leaves it; the panel keeps the public half and checks a
-- signature over a fresh challenge at every sign-in, so a copy of this table
-- signs nobody in. The public key is sealed all the same, bound to the account
-- and the credential: a row somebody writes into the database, or moves from
-- one account to another, does not open, and a passkey that does not open does
-- not sign anybody in.
--
-- credential_id is the authenticator's own id for the credential, base64url,
-- and unique: one credential is one account's. rp_id is the panel hostname it
-- was made for — a browser only offers it to that hostname, so it is kept to
-- say which passkeys belong to an address the panel no longer answers on.
-- sign_count is the authenticator's signature counter: one that goes
-- backwards is a sign the key was copied, and the sign-in is refused.
CREATE TABLE passkeys (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id      TEXT NOT NULL UNIQUE,
    public_key_enc     TEXT NOT NULL,
    rp_id              TEXT NOT NULL,
    sign_count         INTEGER NOT NULL DEFAULT 0,
    aaguid             TEXT NOT NULL DEFAULT '',
    transports         TEXT NOT NULL DEFAULT '',
    attestation_format TEXT NOT NULL DEFAULT '',
    user_verified      INTEGER NOT NULL DEFAULT 0,
    backup_eligible    INTEGER NOT NULL DEFAULT 0,
    backup_state       INTEGER NOT NULL DEFAULT 0,
    name               TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    last_used_at       TEXT
);
CREATE INDEX passkeys_user ON passkeys (user_id);

-- The challenge of a passkey ceremony, between its two halves.
--
-- Held here rather than handed to the browser, so it cannot be made up, and
-- taken out by the half that finishes the ceremony, so it works once. Keyed by
-- the hash of the challenge, which is what the browser signs and sends back.
-- binding is what the ceremony is tied to: the signed-in session that is adding
-- a passkey, or the hash of the cookie given to the browser that is signing in
-- with one. Five minutes, and expired rows are swept every hour.
CREATE TABLE passkey_challenges (
    challenge_hash TEXT PRIMARY KEY,
    kind           TEXT NOT NULL,
    user_id        TEXT NOT NULL DEFAULT '',
    binding        TEXT NOT NULL,
    ip             TEXT NOT NULL DEFAULT '',
    data           TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    expires_at     TEXT NOT NULL
);
CREATE INDEX passkey_challenges_ip ON passkey_challenges (kind, ip, expires_at);
