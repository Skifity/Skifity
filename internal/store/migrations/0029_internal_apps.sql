-- An app can be internal: reachable by name from the other apps in its
-- environment, and from nowhere else. No automatic address, no Ingress.
--
-- A Compose file's database or cache is exactly this, and so is a worker's
-- admin port or an internal API. Every app used to get a public address on its
-- first deploy whether it served the public or not. Zero is every existing
-- app, which keeps what it has.
ALTER TABLE apps ADD COLUMN internal INTEGER NOT NULL DEFAULT 0;
