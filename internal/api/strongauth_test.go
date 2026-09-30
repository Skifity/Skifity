package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/auth"
	"skifity/internal/store"
)

// A team that requires a second factor or single sign-on.

func requireStrongAuth(t *testing.T, h *harness, teamID string) {
	t.Helper()
	team, err := h.db.GetTeam(t.Context(), teamID)
	if err != nil {
		t.Fatal(err)
	}
	team.RequireStrongAuth = true
	if err := h.db.UpdateTeam(t.Context(), &team); err != nil {
		t.Fatal(err)
	}
}

func turnOnTOTP(t *testing.T, h *harness, userID string) {
	t.Helper()
	if _, err := h.db.ExecContext(t.Context(), `UPDATE users SET totp_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
}

// A password alone is refused, whoever it belongs to; a second factor or the
// identity provider is let in.
func TestATeamThatRequiresASecondFactorRefusesAPasswordAlone(t *testing.T) {
	const password = "correct horse battery staple"
	h := newHarness(t)
	acme := h.newTenant("acme")
	person := h.person(t, "member@example.test", password)
	if err := h.db.AddMember(t.Context(), acme.team.ID, person.ID, store.RoleMember); err != nil {
		t.Fatal(err)
	}
	requireStrongAuth(t, h, acme.team.ID)
	projects := "/api/teams/" + acme.team.ID + "/projects"

	// A browser signed in with a password alone.
	b := h.signIn(t, "member@example.test", password)
	status, body := h.send(t, b, http.MethodGet, projects, "", nil)
	if status != http.StatusForbidden || !strings.Contains(body, "auth.strong_auth_required") {
		t.Fatalf("a password alone answered %d: %s", status, truncate(body, 200))
	}
	// Every route in the team, not only one: an app is as refused as the list.
	app := h.app(acme, "web")
	if status, _ := h.send(t, b, http.MethodGet, "/api/apps/"+app.ID, "", nil); status != http.StatusForbidden {
		t.Fatalf("an app in the team answered %d to a password alone", status)
	}
	// The account itself is still reachable, which is where two-factor is
	// turned on — and it says why the team refuses.
	status, body = h.send(t, b, http.MethodGet, "/api/me", "", nil)
	var me struct {
		StrongAuth bool `json:"strong_auth"`
	}
	_ = json.Unmarshal([]byte(body), &me)
	if status != http.StatusOK || me.StrongAuth {
		t.Fatalf("/me answered %d, strong_auth=%v", status, me.StrongAuth)
	}

	// With two-factor on, the same person is let in.
	turnOnTOTP(t, h, person.ID)
	if status, body := h.send(t, b, http.MethodGet, projects, "", nil); status != http.StatusOK {
		t.Fatalf("with two-factor on, answered %d: %s", status, truncate(body, 200))
	}

	// And somebody signed in through the identity provider is let in without it.
	sso := h.person(t, "sso@example.test", password)
	if err := h.db.AddMember(t.Context(), acme.team.ID, sso.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	session, err := h.auth.IssueSession(t.Context(), sso, "10.0.0.1", "test", auth.MethodSSO)
	if err != nil {
		t.Fatal(err)
	}
	ssoBrowser := browser{cookies: []*http.Cookie{h.auth.SessionCookie(session.Token, session.ExpiresAt)}}
	if status, body := h.send(t, ssoBrowser, http.MethodGet, projects, "", nil); status != http.StatusOK {
		t.Fatalf("a single sign-on session answered %d: %s", status, truncate(body, 200))
	}
}

// A token cannot say how it was minted, so it counts when its owner has
// two-factor on or signs in through the identity provider.
func TestATokenCountsByItsOwner(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	requireStrongAuth(t, h, acme.team.ID)
	projects := "/api/teams/" + acme.team.ID + "/projects"

	if status, _ := h.do(acme, http.MethodGet, projects, nil); status != http.StatusForbidden {
		t.Fatalf("the token of an owner with neither answered %d", status)
	}
	if err := h.db.LinkIdentity(t.Context(), acme.user.ID, "https://idp.example", "sub-1", acme.user.Email); err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(acme, http.MethodGet, projects, nil); status != http.StatusOK {
		t.Fatalf("the token of an owner who signs in through the provider answered %d: %s", status, truncate(body, 200))
	}
}

// Turning it on from a sign-in that would not pass it would lock the admin
// doing it out on the next request.
func TestTurningTheRequirementOnCannotLockOutWhoeverDidIt(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	path := "/api/teams/" + acme.team.ID

	status, body := h.do(acme, http.MethodPatch, path, map[string]any{"require_strong_auth": true})
	if status != http.StatusConflict || !strings.Contains(body, "team.strong_auth_self") {
		t.Fatalf("turning it on without a second factor answered %d: %s", status, truncate(body, 200))
	}
	if team, _ := h.db.GetTeam(t.Context(), acme.team.ID); team.RequireStrongAuth {
		t.Fatal("the requirement was stored anyway")
	}

	turnOnTOTP(t, h, acme.user.ID)
	status, body = h.do(acme, http.MethodPatch, path, map[string]any{"require_strong_auth": true})
	if status != http.StatusOK {
		t.Fatalf("turning it on with a second factor answered %d: %s", status, truncate(body, 200))
	}
	if team, _ := h.db.GetTeam(t.Context(), acme.team.ID); !team.RequireStrongAuth {
		t.Fatal("the requirement was not stored")
	}
	events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "team.strong_auth_required", "", 10)
	if len(events) != 1 {
		t.Fatalf("turning it on was not audited: %+v", events)
	}
}
