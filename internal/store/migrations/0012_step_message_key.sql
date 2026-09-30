-- What a finished step said, as a key the interface can translate.
--
-- The step's own name — "Checking the server", "Installing Kubernetes" — has
-- been translated since the panel shipped, from servers.steps.*. The sentence
-- underneath it never was: "Connected to 203.0.113.10", "Ubuntu 24.04, 4 cores,
-- 8192 MB memory, 40 GB free". A person watching a server being added in
-- Indonesian read a translated heading over an English line.
--
-- message stays: it is the English the panel wrote, the fallback for a row from
-- before this migration, and what the API and the CLI read.
ALTER TABLE operation_steps ADD COLUMN message_key TEXT NOT NULL DEFAULT '';
-- The values interpolated into that sentence, as a JSON array, in the order the
-- Go format string used them. See errdoc.Sprintf.
ALTER TABLE operation_steps ADD COLUMN message_args TEXT NOT NULL DEFAULT '';
