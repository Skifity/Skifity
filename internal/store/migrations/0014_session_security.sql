-- Two things a browser session needs to hold, and did not.
--
-- csrf_hash makes the CSRF check a property of the session rather than of two
-- cookies agreeing with each other. Double-submit compares a cookie with a
-- header, and both are things a page on a sibling subdomain can influence:
-- this panel hosts other people's applications, some of them on subdomains of
-- the domain the panel itself answers on, so "another site" is not a stranger's
-- server — it may be an app somebody deployed here this morning. With the
-- token's hash on the session, the header is checked against something only
-- this panel and this browser have ever held.
--
-- reauth_at is when the person last proved who they are, rather than merely
-- holding a cookie that says they once did. Turning two-factor off, reading
-- the recovery codes and minting an API token are the three actions that
-- convert a borrowed session into permanent access, and each of them now asks
-- again. Empty means never, which is what an old session gets.
ALTER TABLE sessions ADD COLUMN csrf_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN reauth_at TEXT NOT NULL DEFAULT '';
