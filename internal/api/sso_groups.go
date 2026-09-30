package api

import (
	"errors"
	"fmt"
	"net/http"
	"slices"

	"skifity/internal/settings"
	"skifity/internal/store"
)

// Teams that follow the identity provider's groups.
//
// An organisation that keeps people in groups at its provider wants the panel
// to follow them, rather than somebody remembering to add and remove the same
// person in two places — and the one they forget is the person who left.
//
// A team named in the groups-to-teams setting is the provider's to decide for
// anybody who signs in through it: at every sign-in, their role there becomes
// the highest their groups give, and they leave the team when no group gives
// one. A team not named there is left alone, whatever the groups say. The
// team's last owner is never removed or demoted by this, because a team
// nobody can administer is not a thing a group change should be able to make.

// syncGroupRoles brings a person's memberships of the mapped teams in line
// with their groups.
func (s *Server) syncGroupRoles(r *http.Request, user store.User, groups []string) error {
	raw, _, err := s.db.GetSetting(r.Context(), settings.KeySSOGroupRoles)
	if err != nil {
		return err
	}
	mappings, err := settings.ParseGroupRoles(raw)
	if err != nil {
		// Refused when it was saved, so this is a database edited by hand:
		// following half of it would be worse than saying so.
		return fmt.Errorf("the groups-to-teams setting does not read: %w", err)
	}
	if len(mappings) == 0 {
		return nil
	}

	// The highest role each mapped team gives this person; "" for none.
	want := map[string]store.Role{}
	for _, mapping := range mappings {
		if _, seen := want[mapping.Team]; !seen {
			want[mapping.Team] = ""
		}
		role := store.Role(mapping.Role)
		if slices.Contains(groups, mapping.Group) && role.AtLeast(want[mapping.Team]) {
			want[mapping.Team] = role
		}
	}

	for slug, role := range want {
		team, err := s.db.TeamBySlug(r.Context(), slug)
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("the groups-to-teams setting names a team that does not exist", "team", slug)
			continue
		}
		if err != nil {
			return err
		}
		if err := s.syncTeam(r, user, team, role); err != nil {
			return fmt.Errorf("team %s: %w", slug, err)
		}
	}
	return nil
}

// syncTeam makes one membership what the groups say.
func (s *Server) syncTeam(r *http.Request, user store.User, team store.Team, want store.Role) error {
	current, err := s.db.GetMembership(r.Context(), team.ID, user.ID)
	member := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if !member && want == "" || member && current.Role == want {
		return nil
	}

	if member && current.Role == store.RoleOwner && want != store.RoleOwner {
		owners, err := s.db.CountOwners(r.Context(), team.ID)
		if err != nil {
			return err
		}
		if owners <= 1 {
			s.log.Warn("the groups would remove or demote a team's last owner; they were not followed",
				"team", team.Slug, "user", user.ID)
			return nil
		}
	}

	from := "none"
	if member {
		from = string(current.Role)
	}
	to := "none"
	switch {
	case want == "":
		err = s.db.RemoveMember(r.Context(), team.ID, user.ID)
	case member && current.Scoped && (want == store.RoleMember || want == store.RoleViewer):
		// A limit to some projects was somebody's decision, and a group that
		// only changes the role does not undo it.
		projects := current.Projects
		if projects == nil {
			projects = []string{}
		}
		err = s.db.SetMembership(r.Context(), team.ID, user.ID, want, projects)
		to = string(want)
	default:
		err = s.db.SetMembership(r.Context(), team.ID, user.ID, want, nil)
		to = string(want)
	}
	if err != nil {
		return err
	}
	s.audit(r.WithContext(withUser(r, user)), team.ID, "team.member_synced", "user", user.ID,
		fmt.Sprintf("%s: %s → %s", user.Email, from, to))
	return nil
}
