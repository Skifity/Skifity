package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// Leaving.
//
// The export is the answer to "can I take my data with me", and its whole value
// is that it is complete and readable. These are the two ways that could go
// wrong: leaving something out, and putting something in that should never be
// shown again.

// exportOf runs the export and returns it decoded.
func exportOf(t *testing.T, h *harness, as tenant) map[string]any {
	t.Helper()
	status, body := h.do(as, http.MethodGet, "/api/teams/"+as.team.ID+"/export", nil)
	if status != http.StatusOK {
		t.Fatalf("the export answered %d\n%s", status, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("the export is not JSON: %v", err)
	}
	return out
}

func TestTheExportHasEverythingWorthTakingAway(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	ctx := t.Context()

	app := store.App{
		EnvironmentID: acme.env.ID, Name: "storefront", Slug: "storefront",
		SourceType: "git", RepoURL: "https://example.test/acme/shop", Branch: "main",
		Port: 3000, HealthPath: "/healthz", Replicas: 2,
		CPURequestM: 50, MemRequestMB: 128,
	}
	if err := h.db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("create app: %v", err)
	}
	domain := store.Domain{AppID: app.ID, Hostname: "shop.example.test", Path: "/", TLS: true}
	if err := h.db.CreateDomain(ctx, &domain); err != nil {
		t.Fatalf("create domain: %v", err)
	}

	status, body := h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/variables",
		map[string]any{"key": "PUBLIC_NAME", "value": "Storefront"})
	if status != http.StatusOK {
		t.Fatalf("setting a variable answered %d\n%s", status, body)
	}

	out := exportOf(t, h, acme)

	// The shape a person reads: team, then projects, then environments, then
	// apps, with the things an app needs to exist somewhere else.
	for _, want := range []string{
		"storefront", "shop.example.test", "https://example.test/acme/shop",
		"PUBLIC_NAME", "Storefront", acme.env.Namespace,
	} {
		if !strings.Contains(string(mustJSON(t, out)), want) {
			t.Errorf("the export does not mention %q, so it is not enough to rebuild anywhere else", want)
		}
	}

	// And it says what it is, so a file found in three years explains itself.
	if out["note"] == nil || out["secrets"] == nil || out["exported_at"] == nil {
		t.Errorf("the export does not describe itself: %v", out)
	}
}

// TestTheExportDoesNotUndoTheOnePromiseAboutSecrets: "once stored, a secret's
// value is never shown again" is a promise this panel makes in its own
// documentation, and a bulk download is exactly where such a promise quietly
// stops being true. The keys are listed; the values are not, and the document
// says where they already are — in the caller's own cluster.
func TestTheExportDoesNotUndoTheOnePromiseAboutSecrets(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	ctx := t.Context()

	app := store.App{
		EnvironmentID: acme.env.ID, Name: "storefront", Slug: "storefront",
		SourceType: "image", Image: "example.test/shop:1", Port: 3000,
	}
	if err := h.db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("create app: %v", err)
	}

	const secretValue = "sk_test_this_must_never_appear"
	status, body := h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/variables",
		map[string]any{"key": "STRIPE_KEY", "value": secretValue, "is_secret": true})
	if status != http.StatusOK {
		t.Fatalf("setting a secret answered %d\n%s", status, body)
	}
	status, body = h.do(acme, http.MethodPut, "/api/projects/"+acme.project.ID+"/variables",
		map[string]any{"key": "SHARED_KEY", "value": secretValue, "is_secret": true})
	if status != http.StatusOK {
		t.Fatalf("setting a shared secret answered %d\n%s", status, body)
	}

	status, raw := h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID+"/export", nil)
	if status != http.StatusOK {
		t.Fatalf("the export answered %d\n%s", status, raw)
	}
	if strings.Contains(raw, secretValue) {
		t.Fatal("the export contains a secret's value, which the panel promises never to show again")
	}
	// The key has to be there, or somebody rebuilding elsewhere does not know
	// what to set.
	if !strings.Contains(raw, "STRIPE_KEY") || !strings.Contains(raw, "SHARED_KEY") {
		t.Error("the export does not name the secrets that exist, so it is not a usable inventory")
	}
}

// TestAnExportIsNotAWayAroundTheTeamBoundary: the export is the widest read in
// the API — every app, domain and database in one answer — so it is the most
// attractive route to somebody else's data.
func TestAnExportIsNotAWayAroundTheTeamBoundary(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	other := h.newTenant("globex")

	status, body := h.do(acme, http.MethodGet, "/api/teams/"+other.team.ID+"/export", nil)
	if status != http.StatusNotFound {
		t.Fatalf("exporting another team's data answered %d, want 404\n%s", status, body)
	}

	// And a member cannot take the lot. Looking at one app is a different
	// thing from downloading every app, domain and database at once.
	member := h.newMember(acme, "member", store.RoleMember)
	status, body = h.do(member, http.MethodGet, "/api/teams/"+acme.team.ID+"/export", nil)
	if status != http.StatusForbidden {
		t.Fatalf("a member exported the whole team: answered %d, want 403\n%s", status, body)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return data
}
