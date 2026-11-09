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

// Leaving is anybody's own, and a token made for the team stops working with
// the membership it stood on. The last owner cannot leave a team nobody could
// then administer.
func TestLeavingATeam(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	acme := h.newTenant("acme")
	member := h.newMember(acme, "member", store.RoleMember)
	path := "/api/teams/" + acme.team.ID

	if status, body := h.do(member, http.MethodPost, path+"/leave", nil); status != http.StatusOK {
		t.Fatalf("a member could not leave: %d %s", status, truncate(body, 200))
	}
	if _, err := h.db.GetMembership(ctx, acme.team.ID, member.user.ID); err == nil {
		t.Fatal("the member is still in the team")
	}
	if status, body := h.do(member, http.MethodGet, path, nil); status != http.StatusNotFound {
		t.Fatalf("their token made for the team still reaches it: %d %s", status, truncate(body, 200))
	}
	left, _ := h.db.ListAudit(ctx, acme.team.ID, "team.member_left", member.user.ID, 10)
	if len(left) != 1 {
		t.Fatalf("leaving was recorded %d times", len(left))
	}

	// A member limited to some projects is still a member, and may go.
	limited := h.limitedMember(acme, "contractor", store.RoleViewer, acme.project.ID)
	if status, body := h.do(limited, http.MethodPost, path+"/leave", nil); status != http.StatusOK {
		t.Fatalf("a limited viewer could not leave: %d %s", status, truncate(body, 200))
	}

	// The only owner may not; with a second one, either may.
	status, body := h.do(acme, http.MethodPost, path+"/leave", nil)
	if status != http.StatusConflict || !strings.Contains(body, "team.last_owner") {
		t.Fatalf("the last owner left: %d %s", status, truncate(body, 200))
	}
	h.newMember(acme, "partner", store.RoleOwner)
	if status, body := h.do(acme, http.MethodPost, path+"/leave", nil); status != http.StatusOK {
		t.Fatalf("an owner with another owner beside them could not leave: %d %s", status, truncate(body, 200))
	}
}

// Deleting a team is its owner's, is refused while it has a machine in the
// cluster or a deployment under way, and takes every environment's namespace
// out of the cluster before any record goes.
func TestDeletingATeam(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	staging := store.Environment{
		ProjectID: acme.project.ID, Name: "staging", Slug: "staging", Namespace: "acme-staging",
	}
	if err := h.db.CreateEnvironment(ctx, &staging); err != nil {
		t.Fatal(err)
	}
	other := h.newTenant("other")
	cluster := &namespaceRefuser{}
	h.withCluster(cluster)
	path := "/api/teams/" + acme.team.ID

	// An admin is not an owner.
	admin := h.newMember(acme, "admin", store.RoleAdmin)
	status, body := h.do(admin, http.MethodDelete, path, nil)
	if status != http.StatusForbidden || !strings.Contains(body, "auth.forbidden") {
		t.Fatalf("an admin deleted the team: %d %s", status, truncate(body, 200))
	}

	// A server is a machine every team's apps run on.
	server := h.node(acme, "web-1")
	status, body = h.do(acme, http.MethodDelete, path, nil)
	if status != http.StatusConflict || !strings.Contains(body, "team.has_servers") || !strings.Contains(body, "web-1") {
		t.Fatalf("a team with a server answered %d %s", status, truncate(body, 300))
	}
	if err := h.db.DeleteServer(ctx, server.ID); err != nil {
		t.Fatal(err)
	}

	// A deployment carries on after its namespace and puts it back.
	deployment := store.Deployment{AppID: app.ID, Status: store.DeployBuilding}
	if err := h.db.CreateDeployment(ctx, &deployment); err != nil {
		t.Fatal(err)
	}
	status, body = h.do(acme, http.MethodDelete, path, nil)
	if status != http.StatusConflict || !strings.Contains(body, "environment.deploying") {
		t.Fatalf("a team with a deployment under way answered %d %s", status, truncate(body, 300))
	}
	if len(cluster.removed()) != 0 {
		t.Fatalf("refusing removed namespaces: %v", cluster.removed())
	}
	if err := h.db.UpdateDeploymentStatus(ctx, deployment.ID, store.DeploySucceeded, "", "", ""); err != nil {
		t.Fatal(err)
	}

	if status, body := h.do(acme, http.MethodDelete, path, nil); status != http.StatusOK {
		t.Fatalf("deleting an empty team answered %d %s", status, truncate(body, 300))
	}
	removed := strings.Join(cluster.removed(), " ")
	for _, namespace := range []string{acme.env.Namespace, staging.Namespace} {
		if !strings.Contains(removed, namespace) {
			t.Errorf("the namespace %s was not removed; removed were %s", namespace, removed)
		}
	}
	if strings.Contains(removed, other.env.Namespace) {
		t.Errorf("another team's namespace was removed: %s", removed)
	}
	if _, err := h.db.GetTeam(ctx, acme.team.ID); err == nil {
		t.Error("the team's row is still there")
	}
	if _, err := h.db.GetProject(ctx, acme.project.ID); err == nil {
		t.Error("its project is still there")
	}
	if _, err := h.db.GetApp(ctx, app.ID); err == nil {
		t.Error("its app is still there")
	}
	if deleted, _ := h.db.ListAudit(ctx, acme.team.ID, "team.deleted", acme.team.ID, 10); len(deleted) != 1 {
		t.Errorf("the delete was recorded %d times", len(deleted))
	}
	// Somebody else's team is untouched.
	if _, err := h.db.GetTeam(ctx, other.team.ID); err != nil {
		t.Errorf("another team went with it: %v", err)
	}
}
