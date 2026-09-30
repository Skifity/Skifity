-- Certificates a team brings instead of the ones Let's Encrypt issues: a
-- company CA's, an EV or OV certificate, a wildcard bought elsewhere, or one
-- for a hostname Let's Encrypt cannot reach. A hostname one of them covers is
-- served with it.
--
-- The chain is public — every visitor is sent it — and is kept in the clear.
-- The private key is sealed under a context naming the certificate, and no
-- query that lists certificates reads it.
--
-- hostnames is a JSON array of the leaf's DNS names, wildcards as written.
-- expiry_notified is the smallest threshold, in days before expiry, a warning
-- has been sent for: 0 for none yet, then 21, 7 and 1. Uploading a new
-- version puts it back to 0.
CREATE TABLE certificates (
    id              TEXT PRIMARY KEY,
    team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    chain_pem       TEXT NOT NULL,
    key_enc         TEXT NOT NULL,
    hostnames       TEXT NOT NULL,
    subject         TEXT NOT NULL,
    issuer          TEXT NOT NULL,
    not_before      TEXT NOT NULL,
    not_after       TEXT NOT NULL,
    fingerprint     TEXT NOT NULL,
    key_type        TEXT NOT NULL,
    self_signed     INTEGER NOT NULL DEFAULT 0,
    chain_length    INTEGER NOT NULL DEFAULT 1,
    expiry_notified INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (team_id, name)
);
