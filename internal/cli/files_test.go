package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// filesPanel answers the files routes for app_1, and remembers what was
// saved.
type filesPanel struct {
	mu    sync.Mutex
	saved []map[string]any
}

func (p *filesPanel) serve(t *testing.T) {
	t.Helper()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/apps/app_1/files":
			_, _ = io.WriteString(w, `{"items":[
				{"id":"file_1","path":"/etc/nginx/nginx.conf","content":"worker_processes 1;\n","size":20},
				{"id":"file_2","path":"/app/secrets.yml","size":30,"is_secret":true}]}`)
		case r.Method == http.MethodPut && r.URL.Path == "/api/apps/app_1/files":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.mu.Lock()
			p.saved = append(p.saved, body)
			p.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"file": map[string]any{"path": body["path"], "size": 3}})
		default:
			http.Error(w, `{"error":{"code":"not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
}

// A file is read from disk or standard input, never typed on the command
// line, and it is only called secret when somebody said so: silence keeps a
// secret file secret on the panel's side.
func TestAFileIsSavedFromDiskOrStandardInput(t *testing.T) {
	panel := &filesPanel{}
	panel.serve(t)
	local := filepath.Join(t.TempDir(), "nginx.conf")
	if err := os.WriteFile(local, []byte("events {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cmdFiles(t.Context(), []string{"set", "/etc/nginx/nginx.conf", local, "--app", "app_1"}, &out); err != nil {
		t.Fatalf("set: %v\n%s", err, out.String())
	}
	filesStdin = strings.NewReader("#!/bin/sh\n")
	t.Cleanup(func() { filesStdin = os.Stdin })
	if err := cmdFiles(t.Context(), []string{"set", "/docker-entrypoint.d/10.sh", "-", "--app", "app_1", "--executable", "--secret"}, &out); err != nil {
		t.Fatalf("set from stdin: %v", err)
	}
	if len(panel.saved) != 2 {
		t.Fatalf("saved %v", panel.saved)
	}
	first, second := panel.saved[0], panel.saved[1]
	if first["content"] != "events {}\n" {
		t.Errorf("the file from disk was sent as %v", first)
	}
	if _, said := first["is_secret"]; said {
		t.Error("a file saved without --secret said it was not secret, which would turn a secret file ordinary")
	}
	if _, said := first["executable"]; said {
		t.Error("a file saved without --executable said it was not executable, which would stop a script running")
	}
	if second["content"] != "#!/bin/sh\n" || second["executable"] != true || second["is_secret"] != true {
		t.Errorf("the file from standard input was sent as %v", second)
	}
}

func TestCatPrintsAFileButNeverASecretOne(t *testing.T) {
	(&filesPanel{}).serve(t)
	var out bytes.Buffer
	if err := cmdFiles(t.Context(), []string{"cat", "/etc/nginx/nginx.conf", "--app", "app_1"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "worker_processes 1;\n" {
		t.Errorf("cat printed %q", out.String())
	}
	if err := cmdFiles(t.Context(), []string{"cat", "/app/secrets.yml", "--app", "app_1"}, &out); err == nil ||
		!strings.Contains(err.Error(), "secret") {
		t.Errorf("cat of a secret file answered %v", err)
	}
}
