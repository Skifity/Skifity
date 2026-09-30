package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"skifity/internal/store"
)

// Members limited to some of a team's projects.

// limitedMemberMayUse is every route that belongs to the whole team and that a
// member limited to some projects may still use, and why. Every other
// team-wide route refuses them: authorizeTeam does, unless a handler chose
// authorizeTeamMember, and this list is where that choice is written down.
var limitedMemberMayUse = map[string]string{
	"GET /api/teams/{teamID}":                                     "the team's name and their own place in it",
	"GET /api/teams/{teamID}/members":                             "who else is in the team; other people's limits name only projects they can see",
	"GET /api/teams/{teamID}/projects":                            "the projects they are limited to, and no others",
	"GET /api/teams/{teamID}/git-sources":                         "picking a repository is how an app is created in one of their projects",
	"GET /api/teams/{teamID}/git-sources/{sourceID}/repositories": "the repositories that picking one is made from",
	"GET /api/teams/{teamID}/git-sources/{sourceID}/branches":     "and the branches of the one picked",
	"POST /api/teams/{teamID}/detect":                             "looking at a repository creates nothing, and comes before creating an app",
	"POST /api/teams/{teamID}/detect-upload":                      "the same, for uploaded code",
}

// otherProject makes a second project in a tenant's team, with an environment,
// an app and a database in it, and returns the tenant pointed at it.
func (h *harness) otherProject(of tenant, name string) tenant {
	h.t.Helper()
	ctx := h.t.Context()
	project := store.Project{TeamID: of.team.ID, Name: name, Slug: name}
	if err := h.db.CreateProject(ctx, &project); err != nil {
		h.t.Fatalf("create project: %v", err)
	}
	env := store.Environment{
		ProjectID: project.ID, Name: "production", Slug: "production",
		Namespace: name + "-production",
	}
	if err := h.db.CreateEnvironment(ctx, &env); err != nil {
		h.t.Fatalf("create environment: %v", err)
	}
	out := of
	out.project, out.env = project, env
	return out
}

// limitedMember adds somebody to a team limited to the given projects.
func (h *harness) limitedMember(of tenant, name string, role store.Role, projects ...string) tenant {
	h.t.Helper()
	member := h.newMember(of, name, role)
	if err := h.db.SetMembership(h.t.Context(), of.team.ID, member.user.ID, role, projects); err != nil {
		h.t.Fatalf("limit member: %v", err)
	}
	return member
}

// TestAMemberLimitedToAProjectReachesNothingElse walks the whole router as a
// member limited to one project, three times over: with the ids of another
// project in the same team, which must answer exactly as another team's do;
// with the ids of the team itself, which must refuse unless the route is named
// in limitedMemberMayUse; and with the ids of their own project, which must
// never be refused for being outside the limit.
func TestAMemberLimitedToAProjectReachesNothingElse(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	secret := h.otherProject(acme, "secret")
	member := h.limitedMember(acme, "contractor", store.RoleMember, acme.project.ID)

	theirs := map[string]string{
		"projectID":  secret.project.ID,
		"envID":      secret.env.ID,
		"appID":      h.app(secret, "hidden").ID,
		"databaseID": h.database(secret, "hidden-db").ID,
	}
	// A connection of the team's own, so a route that lists through one is
	// asked about something that exists. A plain one: it is answered without
	// the panel asking a Git host anything.
	plain := store.GitSource{TeamID: acme.team.ID, Kind: "generic", Name: "plain"}
	if err := h.db.CreateGitSource(t.Context(), &plain); err != nil {
		t.Fatal(err)
	}
	teamWide := map[string]string{
		"teamID":   acme.team.ID,
		"serverID": h.node(acme, "node-1").ID,
		"sourceID": plain.ID,
	}
	// Their own project's app and database are made afresh for every route:
	// a member may delete an app, and the walk does.
	fresh := 0
	ours := func() map[string]string {
		fresh++
		return map[string]string{
			"projectID":  acme.project.ID,
			"envID":      acme.env.ID,
			"appID":      h.app(acme, fmt.Sprintf("web-%d", fresh)).ID,
			"databaseID": h.database(acme, fmt.Sprintf("db-%d", fresh)).ID,
		}
	}
	fill := func(pattern string, ids map[string]string) string {
		return placeholder.ReplaceAllStringFunc(pattern, func(match string) string {
			if id, ok := ids[strings.Trim(match, "{}")]; ok {
				return id
			}
			return "id_00000000000000000000"
		})
	}

	hidden, refused, reached := 0, 0, 0
	err := chi.Walk(h.api.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		pattern = strings.TrimSuffix(pattern, "/")
		if !strings.HasPrefix(pattern, "/api/") || strings.HasPrefix(pattern, "/api/webhooks/") {
			return nil // a webhook is authenticated by its signature, not by a team
		}
		route := method + " " + pattern

		if scoped(pattern, theirs) {
			status, body := h.do(member, method, fill(pattern, theirs), map[string]any{})
			if status != http.StatusNotFound {
				t.Errorf("%s answered %d for another project's resource, want 404.\n%s", route, status, truncate(body, 200))
			}
			hidden++

			own := ours()
			status, body = h.do(member, method, fill(pattern, own), map[string]any{})
			if strings.Contains(body, "auth.scoped_to_projects") ||
				status == http.StatusNotFound && containsAny(body, own) {
				t.Errorf("%s refused a member their own project: %d.\n%s", route, status, truncate(body, 200))
			}
			reached++
			return nil
		}
		if scoped(pattern, teamWide) {
			status, body := h.do(member, method, fill(pattern, teamWide), map[string]any{})
			if why, allowed := limitedMemberMayUse[route]; allowed {
				if why == "" {
					t.Errorf("%s is open to limited members with no reason given", route)
				}
				if status == http.StatusForbidden || status == http.StatusNotFound {
					t.Errorf("%s answered a limited member %d: %s.\n%s", route, status, why, truncate(body, 200))
				}
				return nil
			}
			if status != http.StatusForbidden {
				t.Errorf("%s answered a member limited to a project %d, want 403.\n%s", route, status, truncate(body, 200))
			}
			refused++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	if hidden < 60 || refused < 15 {
		t.Fatalf("only %d project routes and %d team routes were checked, which cannot be all of them", hidden, refused)
	}
	t.Logf("%d routes hide another project, %d team routes refuse, %d reach their own project", hidden, refused, reached)
}

func containsAny(body string, ids map[string]string) bool {
	for _, id := range ids {
		if strings.Contains(body, id) {
			return true
		}
	}
	return false
}

// TestEveryRouteALimitedMemberMayUseIsStillThere: an allowlist naming a route
// that no longer exists is one nobody has read.
func TestEveryRouteALimitedMemberMayUseIsStillThere(t *testing.T) {
	h := newHarness(t)
	live := map[string]bool{}
	if err := chi.Walk(h.api.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		live[method+" "+strings.TrimSuffix(pattern, "/")] = true
		return nil
	}); err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	for route := range limitedMemberMayUse {
		if !live[route] {
			t.Errorf("%q is open to limited members and is not a route this panel serves", route)
		}
	}
}

// What a limited member is shown: their projects, and in the team list, which
// ones they are.
func TestALimitedMemberSeesOnlyTheirProjects(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	secret := h.otherProject(acme, "secret")
	member := h.limitedMember(acme, "contractor", store.RoleViewer, acme.project.ID)

	status, body := h.do(member, http.MethodGet, "/api/teams/"+acme.team.ID+"/projects", nil)
	if status != http.StatusOK || !strings.Contains(body, acme.project.ID) || strings.Contains(body, secret.project.ID) {
		t.Fatalf("the project list for a limited member is %d:\n%s", status, body)
	}

	status, body = h.do(member, http.MethodGet, "/api/teams/"+acme.team.ID, nil)
	var team store.Team
	if status != http.StatusOK || json.Unmarshal([]byte(body), &team) != nil ||
		!team.Scoped || len(team.Projects) != 1 || team.Projects[0] != acme.project.ID {
		t.Fatalf("the team does not say the member is limited: %d %s", status, body)
	}

	// And the owner is not limited by any of this.
	status, body = h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID+"/projects", nil)
	if status != http.StatusOK || !strings.Contains(body, secret.project.ID) {
		t.Fatalf("the owner lost a project: %d %s", status, body)
	}
}

// Deleting the last project somebody was limited to must leave them with
// nothing, not with everything.
func TestALimitedMemberWhoseProjectIsDeletedReachesNothing(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	secret := h.otherProject(acme, "secret")
	doomed := h.otherProject(acme, "doomed")
	member := h.limitedMember(acme, "contractor", store.RoleMember, doomed.project.ID)

	if err := h.db.DeleteProject(t.Context(), doomed.project.ID); err != nil {
		t.Fatal(err)
	}
	status, body := h.do(member, http.MethodGet, "/api/projects/"+secret.project.ID, nil)
	if status != http.StatusNotFound {
		t.Fatalf("with their only project gone, a limited member reached another: %d %s", status, body)
	}
	status, body = h.do(member, http.MethodGet, "/api/teams/"+acme.team.ID+"/projects", nil)
	if status != http.StatusOK || strings.Contains(body, acme.project.ID) || strings.Contains(body, secret.project.ID) {
		t.Fatalf("with their only project gone, a limited member lists %d %s", status, body)
	}
}

// Limits are for members and viewers, name projects of this team, and are set
// and lifted by the same form that changes a role.
func TestLimitingAMember(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	other := h.newTenant("other")
	person := h.newMember(acme, "bob", store.RoleMember)
	path := "/api/teams/" + acme.team.ID + "/members"

	cases := []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"an admin cannot be limited", map[string]any{"email": person.user.Email, "role": "admin",
			"projects": []string{acme.project.ID}}, http.StatusBadRequest},
		{"a limit needs a project", map[string]any{"email": person.user.Email, "role": "member",
			"projects": []string{}}, http.StatusBadRequest},
		{"another team's project is not one of these", map[string]any{"email": person.user.Email, "role": "member",
			"projects": []string{other.project.ID}}, http.StatusNotFound},
	}
	for _, tc := range cases {
		status, body := h.do(acme, http.MethodPost, path, tc.body)
		if status != tc.status {
			t.Errorf("%s: answered %d, want %d.\n%s", tc.name, status, tc.status, truncate(body, 200))
		}
	}
	if m, _ := h.db.GetMembership(t.Context(), acme.team.ID, person.user.ID); m.Scoped {
		t.Fatal("a refused limit was stored")
	}

	status, body := h.do(acme, http.MethodPost, path,
		map[string]any{"email": person.user.Email, "role": "viewer", "projects": []string{acme.project.ID}})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("limiting a member answered %d.\n%s", status, truncate(body, 200))
	}
	if m, _ := h.db.GetMembership(t.Context(), acme.team.ID, person.user.ID); !m.Scoped ||
		len(m.Projects) != 1 || m.Role != store.RoleViewer {
		t.Fatalf("the limit was not stored: %+v", m)
	}

	// The same form without projects is the whole team again.
	status, _ = h.do(acme, http.MethodPost, path, map[string]any{"email": person.user.Email, "role": "member"})
	if m, _ := h.db.GetMembership(t.Context(), acme.team.ID, person.user.ID); status != http.StatusOK && status != http.StatusCreated || m.Scoped {
		t.Fatalf("lifting the limit answered %d and left %+v", status, m)
	}
}

// An invitation carries its limit to the membership it becomes.
func TestAnInvitationCarriesItsLimit(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	h.otherProject(acme, "secret")

	status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/invitations",
		map[string]any{"email": "client@example.test", "role": "viewer", "projects": []string{acme.project.ID}})
	if status != http.StatusCreated {
		t.Fatalf("inviting a limited viewer answered %d.\n%s", status, truncate(body, 200))
	}
	var created inviteResponse
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	token := created.URL[strings.LastIndex(created.URL, "/")+1:]

	status, body = h.do(tenant{}, http.MethodPost, "/api/invitations/"+token+"/accept",
		map[string]string{"name": "Client", "password": "correct horse battery staple"})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("accepting answered %d.\n%s", status, truncate(body, 200))
	}
	user, err := h.db.GetUserByEmail(t.Context(), "client@example.test")
	if err != nil {
		t.Fatal(err)
	}
	m, err := h.db.GetMembership(t.Context(), acme.team.ID, user.ID)
	if err != nil || !m.Scoped || len(m.Projects) != 1 || m.Projects[0] != acme.project.ID || m.Role != store.RoleViewer {
		t.Fatalf("the membership is %+v, %v; want a viewer limited to one project", m, err)
	}
}
