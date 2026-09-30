package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

// processPanel answers the processes routes for app_1, which has a worker,
// and remembers what was written.
type processPanel struct {
	mu   sync.Mutex
	puts []map[string]any
}

func (p *processPanel) serve(t *testing.T) {
	t.Helper()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/apps/app_1/processes":
			_, _ = io.WriteString(w, `{"items":[{"name":"worker","command":"bundle exec sidekiq","instances":2}]}`)
		case r.Method == http.MethodPut && r.URL.Path == "/api/apps/app_1/processes/worker":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.mu.Lock()
			p.puts = append(p.puts, body)
			p.mu.Unlock()
			body["name"] = "worker"
			_ = json.NewEncoder(w).Encode(body)
		default:
			http.Error(w, `{"error":{"code":"not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
}

func TestAProcessesCommandIsTheWorkersOwn(t *testing.T) {
	panel := &processPanel{}
	panel.serve(t)
	run := func(args ...string) {
		t.Helper()
		var out bytes.Buffer
		if err := cmdProcesses(t.Context(), args, &out); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
	}

	// After `--`, the flags are the worker's: --concurrency is not ours, and
	// --instances there is part of its command.
	run("set", "worker", "--app", "app_1", "--instances", "3", "--",
		"bundle", "exec", "sidekiq", "--concurrency", "5", "--instances", "x")
	// Only the count: the command it has is kept, not emptied.
	run("set", "worker", "--instances", "0", "--app", "app_1")
	// Neither: nothing changes but the command is still sent.
	run("set", "worker", "--app", "app_1")

	if len(panel.puts) != 3 {
		t.Fatalf("%d writes", len(panel.puts))
	}
	first, second, third := panel.puts[0], panel.puts[1], panel.puts[2]
	if first["command"] != "bundle exec sidekiq --concurrency 5 --instances x" || first["instances"] != float64(3) {
		t.Errorf("the first write was %v", first)
	}
	if second["command"] != "bundle exec sidekiq" || second["instances"] != float64(0) {
		t.Errorf("changing the count wrote %v", second)
	}
	if third["command"] != "bundle exec sidekiq" || third["instances"] != float64(2) {
		t.Errorf("changing nothing wrote %v", third)
	}

	var out bytes.Buffer
	if err := cmdProcesses(t.Context(), []string{"set", "clock", "--app", "app_1", "--instances", "1"}, &out); err == nil {
		t.Fatal("a new process with no command was sent")
	}
}
