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

// `skifity api spec` prints what the panel serves, and never sends the token:
// the description is open, and a token narrowed to some resources is refused
// it, so a scoped token would only make the command fail.
func TestAPISpecPrintsThePanelsDescription(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/openapi.json" {
			http.Error(w, `{"error":{"code":"not_found","title":"no"}}`, http.StatusNotFound)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("the description was asked for with a credential: %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"openapi":"3.1.0","info":{"title":"Skifity API","version":"v1.2.3"},"paths":{}}`)
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")

	var stdout, stderr bytes.Buffer
	if code := Run(t.Context(), []string{"api", "spec"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exited %d\n%s%s", code, stdout.String(), stderr.String())
	}
	var document map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil || document["openapi"] != "3.1.0" {
		t.Fatalf("printed %q (%v)", stdout.String(), err)
	}
	if !strings.Contains(stdout.String(), "\n  \"info\"") {
		t.Errorf("the description is not indented for a person to read:\n%s", stdout.String())
	}

	// Another panel's, by address, with nobody signed in at all.
	t.Setenv("SKIFITY_URL", "")
	t.Setenv("SKIFITY_TOKEN", "")
	stdout.Reset()
	if code := Run(t.Context(), []string{"api", "spec", "--url", panel.URL}, &stdout, &stderr); code != 0 {
		t.Fatalf("--url exited %d\n%s%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Skifity API") {
		t.Errorf("--url printed %q", stdout.String())
	}
}
