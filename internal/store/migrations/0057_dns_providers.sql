-- A team's DNS providers, and the records the panel creates at them.
--
-- dns_providers is one connection: Cloudflare, Hetzner, DigitalOcean or
-- Route 53, with its credentials sealed under a context naming the team and
-- the connection, and the zones it could see when it was last asked, as a JSON
-- array of {id, name}. The zones are kept so that deciding whether a hostname
-- is in one — which the Domains tab does as somebody types — asks nobody.
--
-- domain_dns is what the panel does about one domain's record: whether it
-- keeps it (the switch on the domain), and what happened the last time it
-- tried — created, elsewhere (somebody else's record already points here),
-- refused (a record somebody else made is in the way; problem names it), or
-- failed (the provider could not be reached; tried again). A domain with no
-- row is one nobody asked about.
--
-- dns_records is the panel's books: every record it created, with the id the
-- provider gave it and the value it was given. The panel only ever changes or
-- deletes a record that is in here and still says what it was left saying.
-- domain_id is set to NULL rather than the row deleted when the domain goes —
-- with its app, its environment or its project — so the record is still in
-- the books to be removed from the provider; the sync does that, and forgets
-- the row. keep is 0 once the domain's switch is turned off: the record is
-- left where it is from then on, and only forgotten — never deleted — when
-- its domain goes. A connection removed takes its rows with it: the records
-- stay at the provider, as they would if the connection had never existed.
CREATE TABLE dns_providers (
    id              TEXT PRIMARY KEY,
    team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('cloudflare','hetzner','digitalocean','route53')),
    name            TEXT NOT NULL,
    credentials_enc TEXT NOT NULL,
    zones           TEXT NOT NULL DEFAULT '[]',
    zones_listed_at TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (team_id, name)
);

CREATE TABLE domain_dns (
    domain_id   TEXT PRIMARY KEY REFERENCES domains(id) ON DELETE CASCADE,
    manage      INTEGER NOT NULL DEFAULT 1,
    state       TEXT NOT NULL DEFAULT '',
    problem     TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL
);

CREATE TABLE dns_records (
    id          TEXT PRIMARY KEY,
    team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL REFERENCES dns_providers(id) ON DELETE CASCADE,
    domain_id   TEXT REFERENCES domains(id) ON DELETE SET NULL,
    zone_id     TEXT NOT NULL,
    zone_name   TEXT NOT NULL,
    hostname    TEXT NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('A','AAAA','CNAME')),
    content     TEXT NOT NULL,
    remote_id   TEXT NOT NULL,
    proxied     INTEGER NOT NULL DEFAULT 0,
    keep        INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (provider_id, zone_id, hostname, type)
);

CREATE INDEX dns_records_domain ON dns_records(domain_id);

-- This panel's own name for itself, written into the note a record carries
-- where the provider keeps one: "managed by Skifity <id>". Made once, here,
-- and kept by a panel restored from a backup, so its records stay its own.
INSERT OR IGNORE INTO settings (key, value, encrypted, updated_at, updated_by)
VALUES ('maintenance.panel_id', lower(hex(randomblob(6))), 0, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), 'system');
