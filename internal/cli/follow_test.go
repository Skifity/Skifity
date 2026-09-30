package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// deployPanel starts a deploy that has already failed by the time anybody
// listens: its event stream stays silent, and only asking says how it went.
func deployPanel(t *testing.T) {
	t.Helper()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/apps/app_1/deploy":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"dep_1","app_id":"app_1","number":7,"status":"queued"}`)
		case r.URL.Path == "/api/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case r.Method == http.MethodGet && r.URL.Path == "/api/apps/app_1/deployments/dep_1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"dep_1","app_id":"app_1","number":7,"status":"failed",`+
				`"error_code":"build.failed","error_message":"npm ci exited 1"}`)
		default:
			http.Error(w, `{"error":{"code":"not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	was := followPoll
	followPoll = 20 * time.Millisecond
	t.Cleanup(func() { followPoll = was })
}

// A deploy that failed before the stream connected was never heard of, and
// `skifity deploy` waited for ever.
func TestADeployThatEndedUnheardIsStillReported(t *testing.T) {
	deployPanel(t)
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- Run(t.Context(), []string{"deploy", "--app", "app_1"}, &stdout, &stderr) }()
	select {
	case code := <-done:
		if code != 1 || !bytes.Contains(stderr.Bytes(), []byte("npm ci exited 1")) {
			t.Fatalf("exited %d\n%s", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the deploy is still being waited for")
	}
}

// Every command says it accepts --json. Following a deploy printed "Deployment
// #7 started.", the build and "succeeded" around it, which no parser reads.
func TestAFollowedDeployPrintsOneJSONDocument(t *testing.T) {
	deployPanel(t)
	var stdout, stderr bytes.Buffer
	code := Run(t.Context(), []string{"deploy", "--json", "--app", "app_1"}, &stdout, &stderr)
	var deployment struct {
		Number int    `json:"number"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &deployment); err != nil || deployment.Number != 7 || deployment.Status != "failed" {
		t.Fatalf("stdout is not the deployment as JSON: %q (%v)", stdout.String(), err)
	}
	if code != 1 {
		t.Fatalf("a failed deploy exited %d", code)
	}
}
