-- What a push has to be to deploy an app.
--
-- Every app deployed each push to its branch. Somebody who releases by
-- tagging wants the opposite: the branch moves all day and only a tag such as
-- v1.4.0 goes out. deploy_trigger is 'branch' — what every existing app did —
-- or 'tag', and tag_pattern is the path.Match pattern a pushed tag's name has
-- to match, 'v*' until somebody says otherwise.
ALTER TABLE apps ADD COLUMN deploy_trigger TEXT NOT NULL DEFAULT 'branch';
ALTER TABLE apps ADD COLUMN tag_pattern TEXT NOT NULL DEFAULT 'v*';
