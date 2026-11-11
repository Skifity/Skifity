-- What a failed deployment's problem said, whole, as JSON.
--
-- error_message and error_hint are the problem flattened into two English
-- strings, which is all a failed deployment kept. The panel could translate
-- the headline by the error's code and nothing else, and could not tell a
-- version that ran out of memory from one whose start command is not there:
-- the exit code, the lines the container printed before it stopped and the
-- values that go into the translated sentences were all thrown away.
ALTER TABLE deployments ADD COLUMN error_problem TEXT NOT NULL DEFAULT '';
