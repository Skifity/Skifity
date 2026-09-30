package api

import (
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// What a preview gets for each variable: the app's own value, a value of its
// own, or nothing.

func TestAPreviewGetsItsOwnValueOrNothingWhereTheAppSaysSo(t *testing.T) {
	p := withLinkedDatabase(t)
	for key, value := range map[string]string{"MAIL_TO": "customers@example.test", "LOG_LEVEL": "info"} {
		sealed, err := p.keyring.Seal([]byte(value), variableContext(p.app.ID, key))
		if err != nil {
			t.Fatal(err)
		}
		variable := store.Variable{AppID: p.app.ID, Key: key}
		if err := p.db.SetVariable(t.Context(), &variable, sealed); err != nil {
			t.Fatal(err)
		}
	}
	base := "/api/apps/" + p.app.ID + "/variables/"

	// The live payment key stays out of previews; they get the test one.
	status, body := p.do(p.acme, http.MethodPut, base+"STRIPE_KEY/preview",
		map[string]string{"mode": "value", "value": "sk_test_for_previews"})
	if status != http.StatusOK {
		t.Fatalf("setting a preview value answered %d: %s", status, body)
	}
	// Previews send no mail to customers at all.
	if status, body := p.do(p.acme, http.MethodPut, base+"MAIL_TO/preview", map[string]string{"mode": "none"}); status != http.StatusOK {
		t.Fatalf("leaving a variable out of previews answered %d: %s", status, body)
	}
	if status, _ := p.do(p.acme, http.MethodPut, base+"NOT_THERE/preview", map[string]string{"mode": "none"}); status != http.StatusNotFound {
		t.Fatalf("a variable that does not exist answered %d", status)
	}
	if status, _ := p.do(p.acme, http.MethodPut, base+"LOG_LEVEL/preview", map[string]string{"mode": "sometimes"}); status != http.StatusBadRequest {
		t.Fatalf("a mode that is not one answered %d", status)
	}

	preview := p.openPullRequest(t, 7, false)
	if got, _ := p.value(t, preview.ID, "STRIPE_KEY"); got != "sk_test_for_previews" {
		t.Fatalf("the preview's STRIPE_KEY is %q, want the preview value", got)
	}
	if _, found := p.value(t, preview.ID, "MAIL_TO"); found {
		t.Fatal("the preview was given MAIL_TO, which the app said previews do not get")
	}
	if got, _ := p.value(t, preview.ID, "LOG_LEVEL"); got != "info" {
		t.Fatalf("the preview's LOG_LEVEL is %q, want the app's own", got)
	}
	// The app itself is unchanged.
	if got, _ := p.value(t, p.app.ID, "STRIPE_KEY"); got != "sk_live_production" {
		t.Fatalf("the app's own STRIPE_KEY became %q", got)
	}

	// A secret's preview value is as hidden as the secret; a plain one's is shown.
	status, body = p.do(p.acme, http.MethodGet, "/api/apps/"+p.app.ID+"/variables", nil)
	if status != http.StatusOK || strings.Contains(body, "sk_test_for_previews") ||
		!strings.Contains(body, `"preview_mode":"none"`) || !strings.Contains(body, `"preview_mode":"value"`) {
		t.Fatalf("the list says %s", body)
	}
}
