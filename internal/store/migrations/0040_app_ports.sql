-- Ports an app takes connections on that are not HTTP: a game server's, an
-- MQTT broker's, a DNS server's. Each is opened on every server at its public
-- port, which is why a public port and protocol are the cluster's to hand out
-- once, not an app's.
CREATE TABLE app_ports (
    id          TEXT PRIMARY KEY,
    app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    port        INTEGER NOT NULL,
    protocol    TEXT NOT NULL CHECK (protocol IN ('tcp','udp')),
    public_port INTEGER NOT NULL,
    created_at  TEXT NOT NULL,
    UNIQUE (app_id, port, protocol),
    UNIQUE (public_port, protocol)
);
