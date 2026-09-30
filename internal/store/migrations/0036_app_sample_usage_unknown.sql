-- Whether metrics-server had nothing for an app's CPU and memory in a minute.
-- Such a minute is still kept, for the instances that were ready and the
-- restarts, which come from the pods themselves: dropping the whole minute, as
-- the watch did, meant a crash-looping app never raised its restarts alert
-- while metrics-server was down.
ALTER TABLE app_samples ADD COLUMN usage_unknown INTEGER NOT NULL DEFAULT 0;
