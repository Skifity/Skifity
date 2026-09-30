package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// runPanel answers a one-off command for app_1 with the given exit status.
func runPanel(t *testing.T, exitCode int) {
	t.Helper()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/apps/app_1/run":
			_, _ = io.WriteString(w, `{"run":"web-run-abc"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/apps/app_1/runs/web-run-abc/logs":
			if r.URL.Query().Get("follow") != "true" {
				t.Errorf("the output was read without following it")
			}
			_, _ = fmt.Fprintf(w, `{"run":"web-run-abc","lines":["migrating","Error: relation exists"],"finished":true,"exit_code":%d}`, exitCode)
		default:
			http.Error(w, `{"error":{"code":"not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
}

// A CI step that runs a migration has to fail when the migration does. The
// command's exit status was never read, and `skifity run` exited 0.
func TestRunExitsWithTheCommandsStatus(t *testing.T) {
	runPanel(t, 3)
	var stdout, stderr bytes.Buffer
	code := Run(t.Context(), []string{"run", "--app", "app_1", "--", "npm", "run", "migrate"}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exited %d, want the command's 3\n%s%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Error: relation exists") || !strings.Contains(stderr.String(), "status 3") {
		t.Fatalf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}

	// --json waits for it too, and says how it ended.
	stdout.Reset()
	code = Run(t.Context(), []string{"run", "--json", "--app", "app_1", "--", "npm", "run", "migrate"}, &stdout, &stderr)
	var answer struct {
		Lines    []string `json:"lines"`
		ExitCode int      `json:"exit_code"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil || code != 3 || answer.ExitCode != 3 || len(answer.Lines) != 2 {
		t.Fatalf("--json exited %d with %q (%v)", code, stdout.String(), err)
	}
}

func TestRunSucceedsWhenTheCommandDoes(t *testing.T) {
	runPanel(t, 0)
	var stdout, stderr bytes.Buffer
	if code := Run(t.Context(), []string{"run", "--app", "app_1", "--", "true"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exited %d\n%s", code, stderr.String())
	}
}
