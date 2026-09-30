package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Microsoft Teams is sent an Adaptive Card, which is what a Workflows webhook
// posts to a channel: the Office 365 connectors that took a MessageCard are
// being retired.
func TestTeamsGetsAnAdaptiveCard(t *testing.T) {
	server, got := service(t)
	// The test server speaks plain HTTP, which the form refuses; sending is
	// what this is about.
	config := map[string]string{"webhook_url": server.URL + "/workflows/abc/triggers/manual/paths/invoke"}
	if err := Send(context.Background(), "teams", config, failure, nil); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Type        string `json:"type"`
		Attachments []struct {
			ContentType string `json:"contentType"`
			Content     struct {
				Type    string `json:"type"`
				Version string `json:"version"`
				Body    []struct {
					Type, Text, Color string
					Facts             []struct{ Title, Value string }
				} `json:"body"`
				Actions []struct{ Type, Title, URL string } `json:"actions"`
			} `json:"content"`
		} `json:"attachments"`
	}
	request := (*got)[0]
	if err := json.Unmarshal(request.body, &payload); err != nil {
		t.Fatalf("Teams got %s: %v", request.body, err)
	}
	if request.contentType != "application/json" || payload.Type != "message" || len(payload.Attachments) != 1 {
		t.Fatalf("Teams got %s as %q", request.body, request.contentType)
	}
	attachment := payload.Attachments[0]
	card := attachment.Content
	if attachment.ContentType != "application/vnd.microsoft.card.adaptive" || card.Type != "AdaptiveCard" ||
		card.Version == "" || len(card.Body) != 3 {
		t.Fatalf("the attachment is not an Adaptive Card: %s", request.body)
	}
	title, body, facts := card.Body[0], card.Body[1], card.Body[2]
	if title.Type != "TextBlock" || title.Text != failure.Title || title.Color != "Attention" ||
		body.Type != "TextBlock" || body.Text != failure.Body {
		t.Errorf("the card says %+v and %+v", title, body)
	}
	if facts.Type != "FactSet" || len(facts.Facts) != 2 || facts.Facts[0].Title != "app" ||
		facts.Facts[0].Value != "café-api" {
		t.Errorf("the details are %+v", facts)
	}
	if len(card.Actions) != 1 || card.Actions[0].Type != "Action.OpenUrl" || card.Actions[0].URL != failure.URL {
		t.Errorf("the card does not link back to the panel: %+v", card.Actions)
	}
}

// Gotify's token goes in a header, never in the query string a server writes
// to its log, and a failure is loud enough to interrupt.
func TestGotifyIsPushedWithItsTokenInAHeader(t *testing.T) {
	var path, query, key, contentType string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		key, contentType = r.Header.Get("X-Gotify-Key"), r.Header.Get("Content-Type")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	// A server under a path, with the slash people paste.
	config := map[string]string{"server": server.URL + "/gotify/", "app_token": "AbCdEfGhIjKlMnO"}
	if err := ValidateConfig(context.Background(), "gotify", config, nil); err != nil {
		t.Fatal(err)
	}
	if err := Send(context.Background(), "gotify", config, failure, nil); err != nil {
		t.Fatal(err)
	}
	if path != "/gotify/message" || query != "" || key != "AbCdEfGhIjKlMnO" || contentType != "application/json" {
		t.Fatalf("Gotify was sent %q to %s?%s with key %q", contentType, path, query, key)
	}
	var payload struct {
		Title    string `json:"title"`
		Message  string `json:"message"`
		Priority int    `json:"priority"`
		Extras   map[string]struct {
			ContentType string `json:"contentType"`
			Click       struct {
				URL string `json:"url"`
			} `json:"click"`
		} `json:"extras"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Gotify got %s: %v", body, err)
	}
	if payload.Title != failure.Title || payload.Priority != 8 ||
		!strings.Contains(payload.Message, "commit: abc1234") || !strings.Contains(payload.Message, failure.URL) {
		t.Errorf("Gotify got %s", body)
	}
	if payload.Extras["client::notification"].Click.URL != failure.URL ||
		payload.Extras["client::display"].ContentType != "text/plain" {
		t.Errorf("the extras are %+v", payload.Extras)
	}
}

func TestTeamsAndGotifyRefuseWhatCannotWork(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		config map[string]string
	}{
		{"teams", map[string]string{"webhook_url": ""}},
		{"teams", map[string]string{"webhook_url": "prod-12.westeurope.logic.azure.com/workflows/x"}},
		{"teams", map[string]string{"webhook_url": "http://prod-12.westeurope.logic.azure.com/workflows/x"}},
		{"gotify", map[string]string{"server": "gotify.example.com", "app_token": "AbCdEfGhIjKlMnO"}},
		{"gotify", map[string]string{"server": "https://gotify.example.com", "app_token": " "}},
	} {
		if err := ValidateConfig(context.Background(), tc.kind, tc.config, nil); err == nil {
			t.Errorf("%s accepted %v", tc.kind, tc.config)
		}
	}
	for _, tc := range []struct {
		kind   string
		config map[string]string
	}{
		{"teams", map[string]string{"webhook_url": "https://prod-12.westeurope.logic.azure.com:443/workflows/abc/triggers/manual/paths/invoke?api-version=2016-06-01&sig=x"}},
		{"teams", map[string]string{"webhook_url": "https://default0123.45.environment.api.powerplatform.com:443/powerautomate/automations/direct/workflows/abc/triggers/manual/paths/invoke"}},
		{"gotify", map[string]string{"server": "https://gotify.example.com", "app_token": "AbCdEfGhIjKlMnO"}},
	} {
		if err := ValidateConfig(context.Background(), tc.kind, tc.config, nil); err != nil {
			t.Errorf("%s refused %v: %v", tc.kind, tc.config, err)
		}
	}
}

// Every kind the panel offers has a form, and every form is for a kind the
// panel offers: the server decides which settings are secret, and a kind with
// no form would have every one of its settings treated as a secret for good.
func TestEveryBuiltInKindHasAForm(t *testing.T) {
	for _, kind := range BuiltIn {
		fields, known := FormOf(kind, nil)
		if !known || len(fields) == 0 {
			t.Errorf("%s has no form", kind)
		}
		required := false
		for _, field := range fields {
			required = required || field.Required
		}
		if !required {
			t.Errorf("%s asks for nothing it cannot do without", kind)
		}
	}
	for kind := range builtInForms {
		if !IsBuiltIn(kind) {
			t.Errorf("%s has a form and is not offered", kind)
		}
	}
}
