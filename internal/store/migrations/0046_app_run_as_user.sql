-- The uid an app's container runs as, for an image that names its user.
--
-- An image whose USER is a name — prom/prometheus's nobody, SonarQube's
-- sonarqube — cannot be checked for root by the kubelet, which refuses it at
-- the strict confinement level with "image has non-numeric user". Giving the
-- number here pins it, and the image runs without lowering the environment.
-- 0 is what every existing app has: the image decides.
ALTER TABLE apps ADD COLUMN run_as_user INTEGER NOT NULL DEFAULT 0;
