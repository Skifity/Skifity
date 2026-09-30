package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// Getting a second person onto the panel.
//
// Until invitations existed there was no way: adding a member refused any
// address that had never signed in, with the advice "ask them to create an
// account first", and nothing could create an account except first-run setup.
// A team of one was the only team possible.
//
// The link is the credential, so these are mostly about what it refuses.

// invite creates an invitation and returns the URL it was given once.
func (h *harness) invite(t *testing.T, as tenant, email, role string) (string, string) {
	t.Helper()
	status, body := h.do(as, http.MethodPost, "/api/teams/"+as.team.ID+"/invitations",
		map[string]string{"email": email, "role": role})
	if status != http.StatusCreated {
		t.Fatalf("inviting %s answered %d\n%s", email, status, body)
	}
	var out struct {
		Invitation store.Invitation `json:"invitation"`
		URL        string           `json:"url"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode the invitation: %v", err)
	}
	token := out.URL[strings.LastIndex(out.URL, "/")+1:]
	if len(token) < 20 {
		t.Fatalf("the invitation URL has no token in it: %q", out.URL)
	}
	return token, out.Invitation.ID
}

func TestAnInvitationCreatesAnAccountAndAMembership(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	anonymous := tenant{}

	token, _ := h.invite(t, acme, "new@example.test", "member")

	// Whoever opens the link is told which team and which address, so they
	// know what they are accepting before they type a password.
	status, body := h.do(anonymous, http.MethodGet, "/api/invitations/"+token, nil)
	if status != http.StatusOK {
		t.Fatalf("looking the invitation up answered %d\n%s", status, body)
	}
	if !strings.Contains(body, "new@example.test") || !strings.Contains(body, "acme") {
		t.Errorf("the lookup does not say who it is for or which team:\n%s", body)
	}

	status, body = h.do(anonymous, http.MethodPost, "/api/invitations/"+token+"/accept",
		map[string]string{"name": "New Person", "password": "a reasonable passphrase"})
	if status != http.StatusOK {
		t.Fatalf("accepting answered %d\n%s", status, body)
	}

	user, err := h.db.GetUserByEmail(t.Context(), "new@example.test")
	if err != nil {
		t.Fatalf("no account was created: %v", err)
	}
	membership, err := h.db.GetMembership(t.Context(), acme.team.ID, user.ID)
	if err != nil {
		t.Fatalf("the account is in no team: %v", err)
	}
	if membership.Role != store.RoleMember {
		t.Errorf("joined as %q, want member", membership.Role)
	}
	if user.PasswordHash == "" {
		t.Error("the account has no password, so it cannot sign in again")
	}
}

func TestAnInvitationCannotBeGuessedOrReused(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	anonymous := tenant{}

	token, id := h.invite(t, acme, "once@example.test", "member")

	// Used once.
	if status, body := h.do(anonymous, http.MethodPost, "/api/invitations/"+token+"/accept",
		map[string]string{"name": "Once", "password": "a reasonable passphrase"}); status != http.StatusOK {
		t.Fatalf("the first accept answered %d\n%s", status, body)
	}
	// And not again — otherwise a link in a chat history is a second account.
	if status, _ := h.do(anonymous, http.MethodPost, "/api/invitations/"+token+"/accept",
		map[string]string{"name": "Again", "password": "a reasonable passphrase"}); status != http.StatusNotFound {
		t.Errorf("the same link worked twice: %d", status)
	}
	if status, _ := h.do(anonymous, http.MethodGet, "/api/invitations/"+token, nil); status != http.StatusNotFound {
		t.Errorf("a spent link still describes the team: %d", status)
	}

	// A token nobody issued, a short one, an empty one, and the invitation's
	// own id — which is not the credential and must not work as one.
	for _, guess := range []string{
		"", "x", "0000000000000000000000000000000000000000000", id, strings.Repeat("a", 64),
	} {
		if status, _ := h.do(anonymous, http.MethodGet, "/api/invitations/"+guess, nil); status == http.StatusOK {
			t.Errorf("the token %q was accepted", guess)
		}
	}

	// A revoked invitation stops working.
	second, secondID := h.invite(t, acme, "revoked@example.test", "member")
	if status, body := h.do(acme, http.MethodDelete,
		"/api/teams/"+acme.team.ID+"/invitations/"+secondID, nil); status != http.StatusOK {
		t.Fatalf("revoking answered %d\n%s", status, body)
	}
	if status, _ := h.do(anonymous, http.MethodPost, "/api/invitations/"+second+"/accept",
		map[string]string{"name": "Nope", "password": "a reasonable passphrase"}); status != http.StatusNotFound {
		t.Errorf("a revoked link still works: %d", status)
	}
}

// TestAnInvitationIsForTheAddressItNames: the account is created with the email
// from the invitation, never from the request. Otherwise a link meant for one
// person makes an account for another — and if that address is an
// administrator's, it makes their account.
func TestAnInvitationIsForTheAddressItNames(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	token, _ := h.invite(t, acme, "intended@example.test", "member")

	// Sending an address at all is refused before anything is read: decodeJSON
	// rejects a field the endpoint does not declare, which is a stronger answer
	// than ignoring it, because ignoring it is a line somebody deletes later.
	if status, body := h.do(tenant{}, http.MethodPost, "/api/invitations/"+token+"/accept",
		map[string]string{
			"name":     "Somebody Else",
			"password": "a reasonable passphrase",
			"email":    "owner@example.test",
		}); status != http.StatusBadRequest {
		t.Errorf("an accept carrying its own email answered %d, want it refused\n%s", status, body)
	}

	// And the account it does create is for the invited address.
	status, body := h.do(tenant{}, http.MethodPost, "/api/invitations/"+token+"/accept",
		map[string]string{"name": "Intended", "password": "a reasonable passphrase"})
	if status != http.StatusOK {
		t.Fatalf("accepting answered %d\n%s", status, body)
	}
	if _, err := h.db.GetUserByEmail(t.Context(), "intended@example.test"); err != nil {
		t.Errorf("the account was not created for the address the invitation named: %v", err)
	}
	if !strings.Contains(body, "intended@example.test") {
		t.Errorf("the answer is not for the invited address:\n%s", body)
	}
}

// TestOnlyAnAdminInvites, and never to a role above their own.
func TestOnlyAnAdminInvitesAndNeverAboveTheirOwnRole(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	member := h.newMember(acme, "member", store.RoleMember)
	admin := h.newMember(acme, "admin", store.RoleAdmin)

	if status, _ := h.do(member, http.MethodPost, "/api/teams/"+acme.team.ID+"/invitations",
		map[string]string{"email": "x@example.test", "role": "member"}); status != http.StatusForbidden {
		t.Errorf("an ordinary member could invite somebody: %d", status)
	}
	if status, body := h.do(admin, http.MethodPost, "/api/teams/"+acme.team.ID+"/invitations",
		map[string]string{"email": "y@example.test", "role": "owner"}); status != http.StatusForbidden {
		t.Errorf("an admin could invite an owner, outranking themselves: %d\n%s", status, body)
	}
	if status, _ := h.do(admin, http.MethodPost, "/api/teams/"+acme.team.ID+"/invitations",
		map[string]string{"email": "z@example.test", "role": "member"}); status != http.StatusCreated {
		t.Errorf("an admin could not invite a member: %d", status)
	}

	// And not into somebody else's team.
	other := h.newTenant("other")
	if status, _ := h.do(acme, http.MethodPost, "/api/teams/"+other.team.ID+"/invitations",
		map[string]string{"email": "q@example.test", "role": "member"}); status != http.StatusNotFound {
		t.Errorf("one team invited somebody into another: %d", status)
	}
}
