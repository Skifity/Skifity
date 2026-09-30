package api

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The routes that answer without credentials, and why each one has to.
//
// Everything else must answer 401. This is an allowlist rather than a list of
// things to check, because the failure mode worth catching is a route added
// next month that nobody thought about: a list of paths to test grows only when
// somebody remembers to grow it, and the one that was forgotten is exactly the
// one that is wrong. TestNothingIsReachableWithoutCredentials used to name
// seven paths out of a hundred and twenty-three.
var openOnPurpose = map[string]string{
	"GET /api/health":       "a liveness probe runs before anybody has an account",
	"GET /api/ready":        "the same, for readiness",
	"GET /api/meta":         "the version and the feature flags the sign-in page needs",
	"GET /api/setup/status": "the panel has to be able to say it has not been set up yet",
	"POST /api/setup":       "first-run setup, which refuses once there is an account",
	"POST /api/auth/login":  "signing in is how credentials are obtained",
	"POST /api/auth/logout": "clearing a cookie nobody has is a no-op, not a secret",

	// The CLI is the same binary as the panel. There is nothing in it that is
	// not in the source, the installer fetches it before any account exists,
	// and requiring a token to download the thing you obtain tokens with is a
	// circle.
	"GET /api/cli/download": "a public binary, needed before there is anybody to authenticate",

	// A webhook is authenticated by the signature in its body against the
	// secret of the source it names, not by a session: the sender is GitHub,
	// which has no account here.
	"POST /api/webhooks/git/{sourceID}": "authenticated by its HMAC signature instead",

	// Both halves of single sign-on, for the same reason as /auth/login:
	// somebody with no account has no credentials to present. The callback is
	// guarded by a state this panel issued, in a cookie this browser was given,
	// used once — and by the ID token's signature, issuer, audience, expiry and
	// nonce. TestASingleSignOnCallbackRefusesWhatItDidNotIssue covers that.
	"GET /api/auth/sso/start":    "starting a sign-in is how credentials are obtained",
	"GET /api/auth/sso/callback": "guarded by a single-use state and a verified ID token, not by a session",

	// An invitation link, for somebody who has no account yet — which is the
	// whole point of one. The token in the URL is the credential: stored
	// hashed, single-use, expiring, and everything wrong with one answers the
	// same way, so it cannot be used to find out whether an address was
	// invited. TestAnInvitationCannotBeGuessedOrReused covers that.
	"GET /api/invitations/{token}":         "the link is the credential; the person holding it has no account yet",
	"POST /api/invitations/{token}/accept": "the same link, accepting it, which is what creates the account",
}

// placeholder fills a chi pattern with something shaped like an id. The value
// never matters: authentication is decided by middleware, before any handler
// looks at it.
var placeholder = regexp.MustCompile(`\{[^}]+\}`)

func TestEveryRouteRefusesAnAnonymousRequest(t *testing.T) {
	h := newHarness(t)
	// An account exists, so /api/setup is past its one open moment and the
	// panel is in the state it spends its life in.
	h.newTenant("acme")
	anonymous := tenant{}

	checked, skipped := 0, 0
	err := chi.Walk(h.api.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(pattern, "/api/") {
			return nil // the panel's own assets and /docs are public by design
		}
		pattern = strings.TrimSuffix(pattern, "/")
		if why, open := openOnPurpose[method+" "+pattern]; open {
			if why == "" {
				t.Errorf("%s %s is allowed through with no reason given", method, pattern)
			}
			skipped++
			return nil
		}

		path := placeholder.ReplaceAllString(pattern, "id_00000000000000000000")
		status, body := h.do(anonymous, method, path, map[string]string{})
		if status != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d with no credentials, want 401.\n%s",
				method, pattern, status, truncate(body, 200))
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	if checked < 100 {
		t.Fatalf("only %d routes were checked, which cannot be the whole surface", checked)
	}
	t.Logf("%d routes refuse an anonymous request, %d are open on purpose", checked, skipped)
}

// TestEveryOpenRouteIsStillThere: an allowlist that names a route which no
// longer exists is an allowlist nobody has read. It would go on excusing a
// route that came back later under the same name.
func TestEveryOpenRouteIsStillThere(t *testing.T) {
	h := newHarness(t)
	live := map[string]bool{}
	if err := chi.Walk(h.api.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		live[method+" "+strings.TrimSuffix(pattern, "/")] = true
		return nil
	}); err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	for route := range openOnPurpose {
		if !live[route] {
			t.Errorf("%q is excused from authentication and is not a route this panel serves", route)
		}
	}
}

// TestNoRouteReachesAnotherTeamsResource walks the whole router rather than a
// list somebody remembered to extend.
//
// TestOneTeamCannotReachAnother names thirty-three paths out of a hundred and
// twenty-three, and the one that matters is always the one nobody thought to
// add. Every route that takes the id of a team, a project, an environment, an
// app, a database or a server is asked for with another team's id here, by
// somebody with a perfectly good token of their own.
//
// The answer must be 404 throughout: a 403 confirms the thing exists, which is
// half of what somebody probing wants to know, and a 500 means the handler ran.
func TestNoRouteReachesAnotherTeamsResource(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	other := h.newTenant("other")

	theirs := map[string]string{
		"teamID":     other.team.ID,
		"projectID":  other.project.ID,
		"envID":      other.env.ID,
		"appID":      h.app(other, "web").ID,
		"databaseID": h.database(other, "shop-db").ID,
		"serverID":   h.node(other, "node-1").ID,
	}

	checked := 0
	err := chi.Walk(h.api.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		pattern = strings.TrimSuffix(pattern, "/")
		if !strings.HasPrefix(pattern, "/api/") || !scoped(pattern, theirs) {
			return nil
		}
		// Anything else in the path is an id of something that does not exist.
		// The route is refused before it gets that far, or this test has found
		// what it is looking for.
		path := placeholder.ReplaceAllStringFunc(pattern, func(match string) string {
			if id, ok := theirs[strings.Trim(match, "{}")]; ok {
				return id
			}
			return "id_00000000000000000000"
		})
		status, body := h.do(acme, method, path, map[string]any{})
		if status != http.StatusNotFound {
			t.Errorf("%s %s answered %d for another team's resource, want 404.\n%s",
				method, pattern, status, truncate(body, 200))
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	if checked < 60 {
		t.Fatalf("only %d team-scoped routes were checked, which cannot be all of them", checked)
	}
	t.Logf("%d team-scoped routes refuse another team", checked)
}

// scoped reports whether a pattern names a resource one team owns.
func scoped(pattern string, ids map[string]string) bool {
	for _, match := range placeholder.FindAllString(pattern, -1) {
		if _, ok := ids[strings.Trim(match, "{}")]; ok {
			return true
		}
	}
	return false
}
