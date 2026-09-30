-- Getting a second person onto the panel.
--
-- Until this table existed there was no way. Adding somebody to a team looked
-- them up by email and refused an address it had never seen, with the advice
-- "ask them to create an account first" — and nothing could create an account
-- except first-run setup, which happens once, or a single sign-on provider,
-- which most installs do not have. A team of one was the only team possible,
-- and the dead end was written into the error message.
--
-- An invitation is a link, not an email. SMTP is a setting most people have not
-- filled in, and a product that cannot add a colleague without a mail server is
-- a product that cannot add a colleague. The link is shown once, the same way
-- the recovery key and the setup token are, and whoever opens it sets their own
-- password.
CREATE TABLE team_invitations (
    id          TEXT PRIMARY KEY,
    team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    role        TEXT NOT NULL,
    -- The token is hashed, like a session and an API token: the panel's own
    -- database is not a place a working credential should sit in the clear.
    token_hash  TEXT NOT NULL UNIQUE,
    invited_by  TEXT REFERENCES users(id) ON DELETE SET NULL,
    expires_at  TEXT NOT NULL,
    accepted_at TEXT,
    created_at  TEXT NOT NULL
);

-- One pending invitation per address per team. Inviting the same person twice
-- should replace the first link rather than leave two that both work.
CREATE UNIQUE INDEX team_invitations_pending
    ON team_invitations (team_id, email) WHERE accepted_at IS NULL;

CREATE INDEX team_invitations_team ON team_invitations (team_id);
