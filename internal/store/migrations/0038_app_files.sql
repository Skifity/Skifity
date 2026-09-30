-- Files an app's containers read: an nginx.conf, a Caddyfile, a settings.yml,
-- a script the image runs at start. Each is mounted read-only at its path in
-- every container the app runs — its instances, its processes, its commands.
--
-- The content is sealed like a variable's value, because a configuration file
-- is where a password ends up as often as an environment variable is. A
-- secret file's content is never sent back; any other is, so it can be
-- edited.
CREATE TABLE app_files (
    id          TEXT PRIMARY KEY,
    app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    path        TEXT NOT NULL,
    content_enc TEXT NOT NULL,
    size        INTEGER NOT NULL DEFAULT 0,
    is_secret   INTEGER NOT NULL DEFAULT 0,
    executable  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (app_id, path)
);
