-- Who somebody is at the identity provider, bound to their account here.
--
-- Single sign-on used to find an account by email address on every sign-in.
-- An address is not an identity: some providers let a user set their own, and
-- one that does not send email_verified has not vouched for it. Whoever could
-- make an identity with the owner's address could sign in as the owner — the
-- class of bug behind CVE-2023-3128 in Grafana and CVE-2026-86117 in Coolify.
--
-- The issuer and subject never change and are never reused, so they are what
-- a returning sign-in is matched on. The email is kept only to show somebody
-- which of their provider accounts is linked.
CREATE TABLE user_identities (
    issuer     TEXT NOT NULL,
    subject    TEXT NOT NULL,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (issuer, subject)
);
CREATE INDEX user_identities_user ON user_identities(user_id);
