-- A preview's copy of an app is deployed by the pull request or branch it
-- previews, never by a push of its own. Copies made before this was so kept
-- deploy-on-push on, with the branch set to the preview's head: a fork's pull
-- request from its own main redeployed the base repository's main on every
-- push, forever — and, being deployed all the time, was never idle long enough
-- for the preview reclaim to remove. Every copy that exists is switched off
-- here, so the ones the previous release made stop deploying and are reclaimed
-- like any other.
UPDATE apps SET auto_deploy = 0
WHERE environment_id IN (SELECT id FROM environments WHERE kind = 'preview');
