-- Whether a preview environment was made for a pull request from a fork.
--
-- Anybody can open one, and its code decides what runs in the container, so
-- nothing secret goes into it: not the app's own secret variables, which the
-- preview never copied, and not the project's shared ones, which it used to
-- be handed at every deploy because nothing recorded where it came from.
ALTER TABLE environments ADD COLUMN from_fork INTEGER NOT NULL DEFAULT 0;
