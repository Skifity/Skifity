package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The chat services and push apps people already read, built in.

// captured is one request a service received.
type captured struct {
	path, contentType, auth string
	body                    []byte
}

// service is an httptest server that keeps what it is sent.
func service(t *testing.T) (*httptest.Server, *[]captured) {
	t.Helper()
	var got []captured
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, captured{r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Authorization"), body})
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server, &got
}

var failure = Message{
	Title: "Deployment failed: café-api", Body: "The build ran out of memory.", Level: "error",
	URL: "https://panel.example/apps/app_1", Fields: map[string]string{"commit": "abc1234", "app": "café-api"},
}

func TestSlackAndMattermostGetAColouredAttachment(t *testing.T) {
	for _, kind := range []string{"slack", "mattermost"} {
		server, got := service(t)
		if err := Send(context.Background(), kind, map[string]string{"webhook_url": server.URL + "/hooks/x"}, failure, nil); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		var payload struct {
			Attachments []struct {
				Color, Title, TitleLink, Text string
				Fields                        []struct{ Title, Value string }
			} `json:"attachments"`
		}
		if err := json.Unmarshal((*got)[0].body, &payload); err != nil || len(payload.Attachments) != 1 {
			t.Fatalf("%s sent %s", kind, (*got)[0].body)
		}
		a := payload.Attachments[0]
		if a.Color != "#e74c3c" || a.Title != failure.Title || a.Text != failure.Body || len(a.Fields) != 2 ||
			a.Fields[0].Title != "app" {
			t.Errorf("%s attachment is %+v", kind, a)
		}
		if !strings.Contains(string((*got)[0].body), `"title_link":"https://panel.example/apps/app_1"`) {
			t.Errorf("%s does not link back to the panel: %s", kind, (*got)[0].body)
		}
	}
}

// ntfy is published as JSON, because a title in a header can only be ASCII
// and an app called café-api is not.
func TestNtfyIsPublishedAsJSONWithItsToken(t *testing.T) {
	server, got := service(t)
	config := map[string]string{"server": server.URL + "/", "topic": "deploys", "token": "tk_test"}
	if err := Send(context.Background(), "ntfy", config, failure, nil); err != nil {
		t.Fatal(err)
	}
	request := (*got)[0]
	var payload map[string]any
	if err := json.Unmarshal(request.body, &payload); err != nil {
		t.Fatal(err)
	}
	if request.path != "/" || request.auth != "Bearer tk_test" || payload["topic"] != "deploys" ||
		payload["title"] != failure.Title || payload["priority"] != float64(4) || payload["click"] != failure.URL {
		t.Fatalf("ntfy got %s at %s with %q", request.body, request.path, request.auth)
	}
	if message, _ := payload["message"].(string); !strings.Contains(message, "commit: abc1234") {
		t.Errorf("the message leaves out the details: %q", message)
	}
}

func TestPushoverIsAFormWithBothKeys(t *testing.T) {
	server, got := service(t)
	previous := pushoverURL
	pushoverURL = server.URL + "/1/messages.json"
	t.Cleanup(func() { pushoverURL = previous })

	config := map[string]string{
		"app_token": strings.Repeat("a", 30), "user_key": strings.Repeat("u", 30),
	}
	if err := ValidateConfig(context.Background(), "pushover", config, nil); err != nil {
		t.Fatal(err)
	}
	if err := Send(context.Background(), "pushover", config, failure, nil); err != nil {
		t.Fatal(err)
	}
	form, err := url.ParseQuery(string((*got)[0].body))
	if err != nil || form.Get("token") != config["app_token"] || form.Get("user") != config["user_key"] ||
		form.Get("title") != failure.Title || form.Get("priority") != "1" || form.Get("url") != failure.URL {
		t.Fatalf("Pushover got %q (%v)", (*got)[0].body, err)
	}
}

// What the form catches before anything is stored.
func TestTheNewKindsRefuseWhatCannotWork(t *testing.T) {
	cases := []struct {
		kind   string
		config map[string]string
	}{
		{"slack", map[string]string{"webhook_url": "https://example.com/hook"}},
		{"mattermost", map[string]string{"webhook_url": "https://chat.example.com/"}},
		{"ntfy", map[string]string{"topic": ""}},
		{"ntfy", map[string]string{"topic": "x", "server": "ntfy.example.com"}},
		{"pushover", map[string]string{"app_token": "short", "user_key": strings.Repeat("u", 30)}},
	}
	for _, tc := range cases {
		if err := ValidateConfig(context.Background(), tc.kind, tc.config, nil); err == nil {
			t.Errorf("%s accepted %v", tc.kind, tc.config)
		}
	}
	good := []struct {
		kind   string
		config map[string]string
	}{
		{"slack", map[string]string{"webhook_url": "https://hooks.slack.com/services/T0/B0/x"}},
		{"mattermost", map[string]string{"webhook_url": "https://chat.example.com/hooks/abc123"}},
		{"ntfy", map[string]string{"topic": "deploys"}},
	}
	for _, tc := range good {
		if err := ValidateConfig(context.Background(), tc.kind, tc.config, nil); err != nil {
			t.Errorf("%s refused %v: %v", tc.kind, tc.config, err)
		}
	}
}
