-- An app can be put into maintenance: visitors get a page saying so, with a
-- 503 and a Retry-After, while the addresses listed in allow still reach the
-- app, so whoever is doing the work can check it.
--
-- The firewall's guard answers for it. It already stands in front of an app's
-- Ingress as Traefik's forward-auth, and whatever it answers other than a 2xx
-- is what the visitor gets — so no second proxy, and nothing about the app's
-- own Deployment changes. One row is one app in maintenance.
CREATE TABLE app_maintenance (
    app_id     TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    message    TEXT NOT NULL DEFAULT '',
    allow      TEXT NOT NULL DEFAULT '',
    started_by TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL
);
