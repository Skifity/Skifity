package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// eventsPanel answers the events and drift routes for app_1 and db_1, and
// remembers what it was asked.
type eventsPanel struct {
	mu   sync.Mutex
	asks []string
}

func (p *eventsPanel) serve(t *testing.T) {
	t.Helper()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.asks = append(p.asks, r.Method+" "+r.URL.RequestURI())
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		backoff := `{"type":"Warning","reason":"BackOff","kind":"Pod","name":"web-7d4f8b9c5-x2x9q",` +
			`"message":"Back-off restarting failed container web","count":14,` +
			`"first_seen":"2026-09-30T09:00:00Z","last_seen":"2026-09-30T09:10:00Z",` +
			`"explanation":"The app keeps stopping soon after it starts.","explanation_code":"crash_backoff"}`
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/apps/app_1/events":
			if r.URL.Query().Get("type") == "Warning" {
				_, _ = io.WriteString(w, `{"items":[`+backoff+`]}`)
				return
			}
			_, _ = io.WriteString(w, `{"items":[`+backoff+`,{"type":"Normal","reason":"Pulled","kind":"Pod",`+
				`"name":"web-7d4f8b9c5-x2x9q","message":"Container image already present","count":1,`+
				`"first_seen":"2026-09-30T09:00:00Z","last_seen":"2026-09-30T09:00:00Z"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/databases/db_1/events":
			_, _ = io.WriteString(w, `{"items":[]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/apps/app_1/drift":
			_, _ = io.WriteString(w, `{"status":"drifted","checked_at":"2026-09-30T09:00:00Z","auto_repair":false,"items":[`+
				`{"kind":"Deployment","name":"web","path":"spec.replicas","change":"changed","panel":"1","live":"4",`+
				`"changed_by":"kubectl","changed_at":"2026-09-30T08:55:00Z"},`+
				`{"kind":"Secret","name":"web-env","path":"data.API_KEY","change":"changed","hidden":true},`+
				`{"kind":"Service","name":"web","change":"deleted"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/apps/app_1/drift/repair":
			_, _ = io.WriteString(w, `{"status":"in_sync","checked_at":"2026-09-30T09:01:00Z","auto_repair":false,"items":[]}`)
		default:
			http.Error(w, `{"error":{"code":"not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
}

func runCommand(t *testing.T, command func(*testing.T, []string, *bytes.Buffer) error, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := command(t, args, &out); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func TestTheEventsCommandSaysWhatItMeansAndReadsAsJSON(t *testing.T) {
	panel := &eventsPanel{}
	panel.serve(t)
	events := func(t *testing.T, args []string, out *bytes.Buffer) error { return cmdEvents(t.Context(), args, out) }

	text := runCommand(t, events, "--app", "app_1")
	for _, want := range []string{"Warning", "BackOff", "Pod/web-7d4f8b9c5-x2x9q", "x14",
		"The app keeps stopping soon after it starts.", "Back-off restarting failed container web", "Pulled"} {
		if !strings.Contains(text, want) {
			t.Errorf("the events do not say %q:\n%s", want, text)
		}
	}

	var decoded []objectEvent
	if err := json.Unmarshal([]byte(runCommand(t, events, "--app", "app_1", "--warnings", "--json")), &decoded); err != nil ||
		len(decoded) != 1 || decoded[0].ExplanationCode != "crash_backoff" || decoded[0].Count != 14 {
		t.Fatalf("--warnings --json read %+v, %v", decoded, err)
	}
	if text := runCommand(t, events, "--db", "db_1"); !strings.Contains(text, "Nothing yet") {
		t.Errorf("a database with no events printed %q", text)
	}
	// An empty list is a list, for a script.
	if raw := strings.TrimSpace(runCommand(t, events, "--db", "db_1", "--json")); raw != "[]" {
		t.Errorf("no events as JSON is %q", raw)
	}

	panel.mu.Lock()
	defer panel.mu.Unlock()
	if !contains(panel.asks, "GET /api/apps/app_1/events?type=Warning") {
		t.Errorf("--warnings did not ask for the warnings: %v", panel.asks)
	}
}

func TestTheDriftCommandListsWhatChangedAndPutsItBack(t *testing.T) {
	panel := &eventsPanel{}
	panel.serve(t)
	drift := func(t *testing.T, args []string, out *bytes.Buffer) error { return cmdDrift(t.Context(), args, out) }

	text := runCommand(t, drift, "--app", "app_1")
	for _, want := range []string{"Deployment/web spec.replicas is 4, the panel applies 1", "by kubectl",
		"Secret/web-env data.API_KEY: a secret value differs", "Service/web was deleted", "drift --repair"} {
		if !strings.Contains(text, want) {
			t.Errorf("the drift does not say %q:\n%s", want, text)
		}
	}
	if text := runCommand(t, drift, "--app", "app_1", "--repair"); !strings.Contains(text, "Put back") {
		t.Errorf("putting it back printed %q", text)
	}
	var report driftReport
	if err := json.Unmarshal([]byte(runCommand(t, drift, "--app", "app_1", "--json")), &report); err != nil ||
		report.Status != "drifted" || len(report.Items) != 3 {
		t.Fatalf("--json read %+v, %v", report, err)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
