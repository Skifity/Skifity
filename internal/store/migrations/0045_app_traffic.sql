-- The requests that reached an app, minute by minute, and a threshold on the
-- ones it failed.
--
-- The panel kept what an app used and nothing about what it was asked to do,
-- so "how many 500s since the deploy" had nothing to look at. k3s's Traefik
-- already counts every request per Ingress backend; the watcher reads those
-- counters on the same minute tick it reads usage on, and keeps the
-- difference between two reads here: how many requests, how they were
-- answered, and two percentiles of how long they took.
--
-- A table of its own rather than more columns on app_samples, because the two
-- come from different places and fail separately: a minute Traefik did not
-- answer is not a minute metrics-server did not, and a row written by one
-- must never draw zeros into the other's chart. They are kept for as long as
-- each other and forgotten together (PruneAppSamples).
--
-- p50_ms and p95_ms are estimated from Traefik's latency histogram, and are
-- NULL for a minute in which nothing was timed. partial marks a minute some
-- of whose traffic could not be counted — a copy of Traefik that did not
-- answer, or was read for the first time — so its numbers are a floor.
CREATE TABLE app_traffic (
    app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    at          TEXT NOT NULL,
    requests    INTEGER NOT NULL DEFAULT 0,
    status_2xx  INTEGER NOT NULL DEFAULT 0,
    status_3xx  INTEGER NOT NULL DEFAULT 0,
    status_4xx  INTEGER NOT NULL DEFAULT 0,
    status_5xx  INTEGER NOT NULL DEFAULT 0,
    p50_ms      REAL,
    p95_ms      REAL,
    partial     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, at)
);

CREATE INDEX idx_app_traffic_at ON app_traffic(at);

-- The share of requests answered with a server error (5xx) that is worth a
-- warning, as a percentage; 0 is off. On at 10 by default, like memory and
-- restarts and unlike CPU: an app failing one request in ten for three
-- minutes is failing the people using it. See DefaultAppAlerts.
ALTER TABLE app_alerts ADD COLUMN server_errors_pct INTEGER NOT NULL DEFAULT 10;
