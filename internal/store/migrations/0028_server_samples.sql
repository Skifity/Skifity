-- What each server used, minute by minute, disk included, and when to say
-- something about it.
--
-- Phase 95 kept an app's history; a server's stayed "now" only, and its disk
-- was not shown at all. A full disk is how a self-hosted server usually dies:
-- images and logs pile up until the kubelet starts evicting everything on it.
-- disk_used_mb is capacity minus what is available, which is what the
-- kubelet itself measures its eviction threshold against.
CREATE TABLE server_samples (
    server_id          TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    at                 TEXT NOT NULL,
    cpu_m              INTEGER NOT NULL DEFAULT 0,
    cpu_capacity_m     INTEGER NOT NULL DEFAULT 0,
    memory_mb          INTEGER NOT NULL DEFAULT 0,
    memory_capacity_mb INTEGER NOT NULL DEFAULT 0,
    disk_used_mb       INTEGER NOT NULL DEFAULT 0,
    disk_capacity_mb   INTEGER NOT NULL DEFAULT 0,
    pods               INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (server_id, at)
);
CREATE INDEX idx_server_samples_at ON server_samples(at);

-- The thresholds a server is watched against. No row is the defaults; zero
-- turns one off; firing is which are going off now.
CREATE TABLE server_alerts (
    server_id  TEXT PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    disk_pct   INTEGER NOT NULL DEFAULT 85,
    memory_pct INTEGER NOT NULL DEFAULT 90,
    cpu_pct    INTEGER NOT NULL DEFAULT 0,
    firing     TEXT NOT NULL DEFAULT ''
);
