-- What an app used, minute by minute, and when to say something about it.
--
-- The panel showed what an app was using now and nothing about an hour ago,
-- so "it was slow at three" had nothing to look at, and the first sign of an
-- app running out of memory was the notification that it had stopped. The
-- watcher already reads every app once a minute; it now keeps what it read.
--
-- The peaks are the busiest instance as a share of its own limit, because
-- that is what gets an instance killed: three instances at a third each is
-- fine, one of them at its limit is not, and the total says the same thing
-- for both.
CREATE TABLE app_samples (
    app_id         TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    at             TEXT NOT NULL,
    cpu_m          INTEGER NOT NULL DEFAULT 0,
    memory_mb      INTEGER NOT NULL DEFAULT 0,
    cpu_peak_pct   INTEGER NOT NULL DEFAULT 0,
    memory_peak_pct INTEGER NOT NULL DEFAULT 0,
    ready          INTEGER NOT NULL DEFAULT 0,
    desired        INTEGER NOT NULL DEFAULT 0,
    restarts       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, at)
);

-- The thresholds an app is watched against. No row is the defaults; zero
-- turns one off. firing is which of them are going off now, so a threshold
-- crossed is said once, and its end is said too.
CREATE TABLE app_alerts (
    app_id         TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    memory_pct     INTEGER NOT NULL DEFAULT 90,
    cpu_pct        INTEGER NOT NULL DEFAULT 0,
    restarts       INTEGER NOT NULL DEFAULT 3,
    firing         TEXT NOT NULL DEFAULT ''
);

-- Old samples are forgotten by time, across every app at once.
CREATE INDEX idx_app_samples_at ON app_samples(at);
