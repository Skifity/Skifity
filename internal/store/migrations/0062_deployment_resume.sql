-- Whether a deployment has been picked up again after a panel restart.
--
-- A deployment the panel was running when it stopped used to be failed on the
-- next start, with "deploy again" as the fix: a push that landed during an
-- upgrade of the panel was simply lost. It is now started again from where it
-- stood (internal/deploy/resume.go), once. A deployment interrupted a second
-- time is failed, so a panel that keeps restarting during one cannot be made
-- to run it for ever.
ALTER TABLE deployments ADD COLUMN resumed INTEGER NOT NULL DEFAULT 0;
