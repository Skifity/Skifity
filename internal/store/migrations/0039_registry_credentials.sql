-- Credentials for the private registries a team pulls images from: a
-- ghcr.io package, a Docker Hub private repository, a company Harbor. One per
-- host; every app in the team's environments pulls with all of them, and the
-- kubelet picks the one for the image's host. The password is sealed.
CREATE TABLE registry_credentials (
    id           TEXT PRIMARY KEY,
    team_id      TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    host         TEXT NOT NULL,
    username     TEXT NOT NULL,
    password_enc TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE (team_id, host)
);
