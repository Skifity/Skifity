-- The GPUs an app's instances are given.
--
-- A table of its own rather than four more columns on apps: most apps never
-- have a row here, and a preview's copy of an app is made without one on
-- purpose (see previewCopy) — a preview holding the cluster's only card would
-- stop production's next rollout.
--
-- count cards of vendor's (nvidia, amd or intel) for every instance of the
-- workloads named: "web" is the app itself, anything else one of its
-- processes, comma-separated, and empty the app alone. product is a model to
-- prefer, as GPU feature discovery labels a server with one, NVIDIA-A10.
-- Runtime settings, all four: a change is a rollout and never a build
-- (ADR-0007). No row is no GPU.
CREATE TABLE app_gpus (
    app_id     TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    count      INTEGER NOT NULL CHECK (count >= 0),
    vendor     TEXT NOT NULL DEFAULT '',
    product    TEXT NOT NULL DEFAULT '',
    workloads  TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
);
