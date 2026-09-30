-- How strictly an environment's pods are confined.
--
-- Every environment namespace carried the "restricted" Pod Security Admission
-- label with no way to change it, and the panel's own rendered pod spec went
-- further: it forced uid 1000 on every container, whatever the image said it
-- ran as. Measured against the one-click catalogue, of the 124 images whose
-- config could be read from their registries, 120 do not run as uid 1000 and 87
-- declare no non-root user at all — which "restricted" refuses outright. The
-- catalogue that is the product's front page mostly could not start.
--
-- So the level is the environment's, defaulting to the strict one. A team that
-- runs only what Skifity builds never touches it. Existing rows get
-- 'restricted', which is the level they were already running at.
ALTER TABLE environments ADD COLUMN pod_security TEXT NOT NULL DEFAULT 'restricted'
    CHECK (pod_security IN ('restricted','baseline'));
