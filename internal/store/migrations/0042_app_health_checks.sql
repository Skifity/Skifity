-- How an app's instances are checked, and how long they are given.
--
-- Every app had the same probes: an HTTP request to its health path, or a TCP
-- connect without one, and two minutes to start. A JVM warming up or an image
-- that migrates its database before it listens never became ready inside
-- that, and nothing could change it.
--
-- health_check is 'http', 'tcp' or 'none'. Existing apps get what they were
-- already rendered with, so upgrading changes no Deployment: 'http' where a
-- health path is set, 'tcp' where it is not. health_start_seconds is the time
-- a new instance may take to answer, and health_timeout_seconds how long one
-- check waits; both default to what the probes always used.
ALTER TABLE apps ADD COLUMN health_check TEXT NOT NULL DEFAULT 'tcp';
ALTER TABLE apps ADD COLUMN health_start_seconds INTEGER NOT NULL DEFAULT 120;
ALTER TABLE apps ADD COLUMN health_timeout_seconds INTEGER NOT NULL DEFAULT 3;
UPDATE apps SET health_check = 'http' WHERE health_path <> '';
