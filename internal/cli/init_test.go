package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// A public repository with no Git connection deploys on push by default, and
// nothing was registered to make that true. init says so, and with --json the
// one document carries the reason's code for a script to act on.
func TestInitSaysWhenNoWebhookWasRegistered(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/api/environments/env_1/apps" {
			http.Error(w, `{"error":{"code":"resource.not_found","title":"no"}}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"app":{"id":"app_1","name":"site"},"webhook":{"registered":false,
			"reason":"no webhook was registered for this app, because it has no Git connection to register one with",
			"reason_code":"no_connection"}}`)
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")

	t.Chdir(t.TempDir())
	var out bytes.Buffer
	if err := cmdInit(t.Context(), []string{"--env", "env_1", "--repo", "https://github.com/acme/site", "--name", "site"}, &out); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out.String(), "no webhook was registered") || !strings.Contains(out.String(), "/apps/app_1?tab=settings") {
		t.Errorf("init printed:\n%s", out.String())
	}

	t.Chdir(t.TempDir())
	out.Reset()
	if err := cmdInit(t.Context(), []string{"--env", "env_1", "--repo", "https://github.com/acme/site", "--name", "site", "--json"}, &out); err != nil {
		t.Fatalf("init --json: %v", err)
	}
	var document struct {
		Webhook struct {
			ReasonCode string `json:"reason_code"`
		} `json:"webhook"`
	}
	if err := json.Unmarshal(out.Bytes(), &document); err != nil {
		t.Fatalf("--json printed something other than one document: %v\n%s", err, out.String())
	}
	if document.Webhook.ReasonCode != "no_connection" {
		t.Errorf("--json printed %s", out.String())
	}
}
