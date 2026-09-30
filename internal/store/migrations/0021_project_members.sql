-- A member can be limited to some of a team's projects.
--
-- A team is one agency and many clients, or one company and many products, and
-- the person who works on one of them should not see the others. Until now a
-- membership was the whole team or nothing.
--
-- scoped is the switch and membership_projects is the list. They are separate
-- on purpose: with only the list, "no rows" would have to mean "the whole
-- team", and deleting the last project somebody was limited to would quietly
-- give them all of the others. With the switch, a scoped membership with no
-- projects left sees none.
--
-- Only members and viewers are ever scoped; the API refuses it for admins and
-- owners, who look after the whole team by definition.
ALTER TABLE memberships ADD COLUMN scoped INTEGER NOT NULL DEFAULT 0;

CREATE TABLE membership_projects (
    team_id    TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    PRIMARY KEY (team_id, user_id, project_id),
    FOREIGN KEY (team_id, user_id) REFERENCES memberships(team_id, user_id) ON DELETE CASCADE
);
CREATE INDEX idx_membership_projects_project ON membership_projects(project_id);

-- The same, carried by an invitation until it is accepted: a JSON array of
-- project ids, or empty for the whole team.
ALTER TABLE team_invitations ADD COLUMN projects TEXT NOT NULL DEFAULT '';
