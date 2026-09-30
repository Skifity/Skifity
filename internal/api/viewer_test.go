package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"skifity/internal/store"
)

// The viewer role: everything a member can see, nothing a member can change.

// viewerMayNotRead is every GET a viewer is refused, and why. It is an
// allowlist in the other direction from openOnPurpose: any GET not named here
// must answer a viewer, and any route that is not a GET must refuse one. A
// route added next month is covered either way without anybody remembering to
// add it.
var viewerMayNotRead = map[string]string{
	"GET /api/teams/{teamID}/invitations":                    "an invitation link is a way in; administrators hand them out",
	"GET /api/teams/{teamID}/audit":                          "the audit log is for administrators, as it is for members",
	"GET /api/teams/{teamID}/export":                         "the whole team in one file is for administrators",
	"GET /api/teams/{teamID}/notifications/kinds":            "the form for adding a channel, which only administrators can do",
	"GET /api/apps/{appID}/advanced":                         "the Kubernetes objects are for administrators",
	"GET /api/databases/{databaseID}/credentials":            "a database's password is for administrators",
	"GET /api/teams/{teamID}/git-sources/{sourceID}/webhook": "a webhook's secret starts deploys; administrators set webhooks up",
	"GET /api/teams/{teamID}/git-sources/{sourceID}/repositories": "read with the team's token for creating an app, " +
		"which a viewer does not; it names private repositories",
	"GET /api/teams/{teamID}/git-sources/{sourceID}/branches": "the same, for one repository's branches",
}

// TestAViewerCanChangeNothing walks the whole router as a viewer of the team
// that owns every id in the path.
func TestAViewerCanChangeNothing(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	viewer := h.newMember(acme, "viewer", store.RoleViewer)

	ours := map[string]string{
		"teamID":     acme.team.ID,
		"projectID":  acme.project.ID,
		"envID":      acme.env.ID,
		"appID":      h.app(acme, "web").ID,
		"databaseID": h.database(acme, "shop-db").ID,
		"serverID":   h.node(acme, "node-1").ID,
	}

	reads, writes := 0, 0
	err := chi.Walk(h.api.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		pattern = strings.TrimSuffix(pattern, "/")
		if !strings.HasPrefix(pattern, "/api/") || !scoped(pattern, ours) {
			return nil
		}
		path := placeholder.ReplaceAllStringFunc(pattern, func(match string) string {
			if id, ok := ours[strings.Trim(match, "{}")]; ok {
				return id
			}
			return "id_00000000000000000000"
		})
		status, body := h.do(viewer, method, path, map[string]any{})

		if method != http.MethodGet {
			if status != http.StatusForbidden {
				t.Errorf("%s %s answered a viewer %d, want 403.\n%s", method, pattern, status, truncate(body, 200))
			}
			writes++
			return nil
		}
		if why, refused := viewerMayNotRead[method+" "+pattern]; refused {
			if why == "" {
				t.Errorf("%s %s is refused to viewers with no reason given", method, pattern)
			}
			if status != http.StatusForbidden {
				t.Errorf("%s %s answered a viewer %d, want 403: %s", method, pattern, status, why)
			}
			return nil
		}
		// Anything but a refusal: a 404 for an id in the path that does not
		// exist, a 503 for the cluster there is not, is the handler running.
		if status == http.StatusForbidden || status == http.StatusUnauthorized || status >= 500 && status != http.StatusServiceUnavailable {
			t.Errorf("%s %s answered a viewer %d; a viewer can read what a member can.\n%s",
				method, pattern, status, truncate(body, 200))
		}
		reads++
		return nil
	})
	if err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	if reads < 30 || writes < 50 {
		t.Fatalf("only %d reads and %d writes were checked, which cannot be the whole surface", reads, writes)
	}
	t.Logf("a viewer reads %d routes and is refused %d that change something", reads, writes)
}

// TestEveryRouteAViewerIsRefusedIsStillThere: as for openOnPurpose, an entry
// for a route that no longer exists is an entry nobody has read.
func TestEveryRouteAViewerIsRefusedIsStillThere(t *testing.T) {
	h := newHarness(t)
	live := map[string]bool{}
	if err := chi.Walk(h.api.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		live[method+" "+strings.TrimSuffix(pattern, "/")] = true
		return nil
	}); err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	for route := range viewerMayNotRead {
		if !live[route] {
			t.Errorf("%q is refused to viewers and is not a route this panel serves", route)
		}
	}
}

// The routes that name what they act on in the body rather than the path are
// not in the walk above, so they are asked for here.
func TestAViewerCannotActThroughTheBody(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	viewer := h.newMember(acme, "viewer", store.RoleViewer)

	status, body := h.do(viewer, http.MethodPost, "/api/templates/uptime-kuma/install",
		map[string]any{"environment_id": acme.env.ID})
	if status != http.StatusForbidden {
		t.Errorf("a viewer installing a template answered %d, want 403.\n%s", status, truncate(body, 200))
	}

	// A token, though, a viewer can have, and it reads and nothing more:
	// every request it makes is checked against the viewer's role.
	const password = "correct horse battery staple"
	person := h.person(t, "reader@example.test", password)
	if err := h.db.AddMember(t.Context(), acme.team.ID, person.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	b := h.signIn(t, "reader@example.test", password)
	status, body = h.send(t, b, http.MethodPost, "/api/me/tokens", b.csrf,
		map[string]any{"name": "dashboard", "team_id": acme.team.ID})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("a viewer could not make a token: %d\n%s", status, truncate(body, 200))
	}
	var created struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.Secret == "" {
		t.Fatalf("no token in %s", truncate(body, 200))
	}
	withToken := viewer
	withToken.token = created.Secret
	if status, body := h.do(withToken, http.MethodGet, "/api/projects/"+acme.project.ID, nil); status != http.StatusOK {
		t.Errorf("a viewer's token could not read: %d\n%s", status, truncate(body, 200))
	}
	if status, _ := h.do(withToken, http.MethodPatch, "/api/projects/"+acme.project.ID,
		map[string]any{"name": "renamed"}); status != http.StatusForbidden {
		t.Errorf("a viewer's token changed a project: %d", status)
	}
}

// A viewer is a role like any other: an administrator can give it, and an
// invitation can carry it.
func TestAViewerCanBeAddedAndInvited(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/invitations",
		map[string]string{"email": "client@example.test", "role": "viewer"})
	if status != http.StatusCreated {
		t.Fatalf("inviting a viewer answered %d.\n%s", status, truncate(body, 200))
	}

	other := h.newMember(acme, "bob", store.RoleMember)
	status, body = h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/members",
		map[string]string{"email": other.user.Email, "role": "viewer"})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("making a member a viewer answered %d.\n%s", status, truncate(body, 200))
	}
	membership, err := h.db.GetMembership(t.Context(), acme.team.ID, other.user.ID)
	if err != nil || membership.Role != store.RoleViewer {
		t.Fatalf("the membership is %+v, %v; want a viewer", membership, err)
	}
}
