-- An app's other processes: a worker, a clock, anything else its Procfile
-- names beside web. Each runs the app's own image, with its variables, under
-- a command of its own and a number of instances of its own; it has no port
-- and takes no traffic. Zero instances is a process that is stopped, kept so
-- starting it again is not remembering its command.
CREATE TABLE app_processes (
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    command    TEXT NOT NULL,
    instances  INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (app_id, name)
);
