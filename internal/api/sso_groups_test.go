package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"skifity/internal/settings"
	"skifity/internal/store"
)

// Teams that follow the identity provider's groups.

func TestTeamsFollowTheProvidersGroups(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	beta := h.newTenant("beta")
	person := h.newMember(beta, "person", store.RoleMember).user
	mappings := "# the platform team\ndevs = acme:member\nleads = acme:admin\nops = ghost:viewer\n"
	if err := h.db.SetSetting(t.Context(), settings.KeySSOGroupRoles, mappings, false, "test"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/auth/sso/callback", nil)
	role := func(teamID string) store.Role {
		m, err := h.db.GetMembership(t.Context(), teamID, person.ID)
		if err != nil {
			return ""
		}
		return m.Role
	}
	sync := func(groups ...string) {
		t.Helper()
		if err := h.api.syncGroupRoles(r, person, groups); err != nil {
			t.Fatal(err)
		}
	}

	// The highest role any of their groups gives.
	sync("devs", "leads", "unrelated")
	if role(acme.team.ID) != store.RoleAdmin {
		t.Fatalf("in devs and leads, the role in acme is %q, want admin", role(acme.team.ID))
	}
	// A team not named in the mappings is left alone, and a team named that
	// does not exist is skipped.
	if role(beta.team.ID) != store.RoleMember {
		t.Fatalf("the role in beta, which no mapping names, became %q", role(beta.team.ID))
	}

	sync("devs")
	if role(acme.team.ID) != store.RoleMember {
		t.Fatalf("out of leads, the role in acme is %q, want member", role(acme.team.ID))
	}

	// Out of every mapped group, out of the team.
	sync()
	if role(acme.team.ID) != "" {
		t.Fatalf("in no mapped group, still %q in acme", role(acme.team.ID))
	}
	events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "team.member_synced", "", 10)
	if len(events) != 3 {
		t.Fatalf("%d changes were audited, want 3: %+v", len(events), events)
	}
}

// A limit to some projects was somebody's decision; a group that changes only
// the role keeps it.
func TestAGroupKeepsAMembersProjectLimit(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	person := h.limitedMember(acme, "contractor", store.RoleMember, acme.project.ID).user
	if err := h.db.SetSetting(t.Context(), settings.KeySSOGroupRoles, "clients = acme:viewer", false, "test"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if err := h.api.syncGroupRoles(r, person, []string{"clients"}); err != nil {
		t.Fatal(err)
	}
	m, err := h.db.GetMembership(t.Context(), acme.team.ID, person.ID)
	if err != nil || m.Role != store.RoleViewer || !m.Scoped || len(m.Projects) != 1 {
		t.Fatalf("the membership is %+v, %v", m, err)
	}
}

// A team nobody can administer is not a thing a group change may make.
func TestGroupsNeverRemoveTheLastOwner(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	if err := h.db.SetSetting(t.Context(), settings.KeySSOGroupRoles, "devs = acme:member", false, "test"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	for _, groups := range [][]string{nil, {"devs"}} {
		if err := h.api.syncGroupRoles(r, acme.user, groups); err != nil {
			t.Fatal(err)
		}
		if m, err := h.db.GetMembership(t.Context(), acme.team.ID, acme.user.ID); err != nil || m.Role != store.RoleOwner {
			t.Fatalf("with groups %v the last owner became %+v, %v", groups, m, err)
		}
	}
}
