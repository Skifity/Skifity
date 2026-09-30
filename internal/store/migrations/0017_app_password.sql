-- A password in front of an app.
--
-- For a staging site, a preview or an internal tool: somebody who opens the
-- address is asked for a username and password before the app sees the
-- request. The check is Traefik's, on every request, so what is stored is what
-- Traefik reads — a bcrypt hash in htpasswd form — and never the password.
--
-- Its own table rather than two columns on apps, for the same reason as the
-- firewall: most apps have none, and a preview copies it explicitly rather
-- than by accident.
CREATE TABLE app_passwords (
    app_id        TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    username      TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    updated_by    TEXT NOT NULL DEFAULT ''
);
