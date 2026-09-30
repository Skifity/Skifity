package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// `scale --max 8` on an app that autoscales sent nothing and exited 0.
func TestScaleLimitsAloneAreSent(t *testing.T) {
	var sent []map[string]any
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut && r.URL.Path == "/api/apps/app_1/scaling" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent = append(sent, body)
			_, _ = io.WriteString(w, `{"scaling":{},"warnings":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")

	var out bytes.Buffer
	if err := cmdScale(t.Context(), []string{"--app", "app_1", "--max", "8"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0]["max_replicas"] != float64(8) || sent[0]["autoscale"] != nil {
		t.Fatalf("sent %v", sent)
	}
	if err := cmdScale(t.Context(), []string{"--app", "app_1", "--instances", "2", "--max", "8"}, &out); err == nil {
		t.Fatal("a fixed number and an autoscaling limit together were accepted")
	}
}
