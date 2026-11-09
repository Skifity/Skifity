package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/auth"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Teams: who may make one, and what happens to one when people leave it or it
// is deleted.

// signedInAs is a browser session for an account, the way the panel's own
// pages make their requests: a team-bound token cannot create a team at all,
// so what this is about has to be asked from a session.
func (h *harness) signedInAs(t *testing.T, user store.User) browser {
	t.Helper()
	session, _ := h.sessionFor(t, user, auth.MethodPassword)
	return session
}

// Making a team makes its owner, and an owner deploys onto the servers every
// team shares. Anybody signed in could, a viewer of somebody else's team
// included, which is a way onto the cluster nobody gave them.
func TestOnlyAPanelAdministratorCreatesATeamByDefault(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	viewer := h.newMember(acme, "viewer", store.RoleViewer)
	owner := h.signedInAs(t, acme.user)
	watcher := h.signedInAs(t, viewer.user)

	for name, who := range map[string]browser{"a team's owner": owner, "a viewer of another team": watcher} {
		status, body := h.send(t, who, http.MethodPost, "/api/teams", who.csrf, map[string]any{"name": "Side"})
		if status != http.StatusForbidden || !strings.Contains(body, "team.creation_restricted") {
			t.Errorf("%s who is not a panel administrator made a team: %d %s", name, status, truncate(body, 200))
		}
	}
	if teams, _ := h.db.ListTeamsForUser(t.Context(), viewer.user.ID); len(teams) != 1 {
		t.Fatalf("the viewer is in %d teams after being refused", len(teams))
	}

	// Opened to everybody, the viewer can.
	if err := h.db.SetSetting(t.Context(), settings.KeyTeamCreation, settings.TeamCreationEveryone, false, ""); err != nil {
		t.Fatal(err)
	}
	status, body := h.send(t, watcher, http.MethodPost, "/api/teams", watcher.csrf, map[string]any{"name": "Side"})
	if status != http.StatusCreated {
		t.Fatalf("with everyone allowed, a viewer was refused: %d %s", status, truncate(body, 200))
	}
	var made store.Team
	if err := json.Unmarshal([]byte(body), &made); err != nil || made.Role != store.RoleOwner {
		t.Fatalf("the new team is %s", body)
	}

	// And closed again, the panel administrator still can, whatever it says.
	if err := h.db.SetSetting(t.Context(), settings.KeyTeamCreation, settings.TeamCreationAdmins, false, ""); err != nil {
		t.Fatal(err)
	}
	admin := adminTenant(h, "ops")
	adminUser, err := h.db.GetUser(t.Context(), admin.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	session := h.signedInAs(t, adminUser)
	if status, body := h.send(t, session, http.MethodPost, "/api/teams", session.csrf, map[string]any{"name": "Platform"}); status != http.StatusCreated {
		t.Fatalf("the panel administrator was refused a team: %d %s", status, truncate(body, 200))
	}
}
