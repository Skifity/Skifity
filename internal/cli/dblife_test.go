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
	"time"
)

// `db stop|start|resize|password|import`, against a panel that answers as
// the real one does and remembers what it was sent.

type lifePanel struct {
	mu       sync.Mutex
	requests []string
	bodies   map[string]string
	polls    int
}

const lifeDatabase = `{"id":"db_1","name":"orders","slug":"orders","engine":"postgres","status":"running",
	"cpu_request_m":100,"cpu_limit_m":0,"mem_request_mb":256,"mem_limit_mb":2048,"storage_gb":20}`

func (p *lifePanel) serve(t *testing.T) {
	t.Helper()
	p.bodies = map[string]string{}
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		route := r.Method + " " + r.URL.Path
		p.mu.Lock()
		p.requests = append(p.requests, route+"?"+r.URL.RawQuery)
		p.bodies[route] = string(body)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch route {
		case "GET /api/environments/env_1/databases":
			_, _ = io.WriteString(w, `{"items":[`+lifeDatabase+`],"total":1}`)
		case "POST /api/databases/db_1/stop", "PATCH /api/databases/db_1":
			_, _ = io.WriteString(w, lifeDatabase)
		case "POST /api/databases/db_1/start":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, lifeDatabase)
		case "POST /api/databases/db_1/password", "POST /api/databases/db_1/import":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"op_1","kind":"database.password","status":"pending"}`)
		case "GET /api/operations/op_1":
			p.mu.Lock()
			p.polls++
			done := p.polls > 1
			p.mu.Unlock()
			if done {
				_, _ = io.WriteString(w, `{"id":"op_1","status":"succeeded"}`)
			} else {
				_, _ = io.WriteString(w, `{"id":"op_1","status":"running"}`)
			}
		case "GET /api/databases/db_1/credentials":
			_, _ = io.WriteString(w, `{"engine":"postgres","password":"the-new-password-from-the-panel"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"test.unanswered","title":"no"}}`)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")
}

func (p *lifePanel) body(route string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bodies[route]
}

func runDB(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := cmdDB(t.Context(), append(args, "--env", "env_1"), &out); err != nil {
		t.Fatalf("db %s: %v", strings.Join(args, " "), err)
	}
	return out.String()
}

func TestTheDatabaseLifeCommandsSayWhatTheyDidInJSON(t *testing.T) {
	panel := &lifePanel{}
	panel.serve(t)
	previous := operationPoll
	operationPoll = time.Millisecond
	t.Cleanup(func() { operationPoll = previous })

	var database map[string]any
	if err := json.Unmarshal([]byte(runDB(t, "stop", "orders", "--force", "--json")), &database); err != nil || database["id"] != "db_1" {
		t.Fatalf("stop --json: %v %v", database, err)
	}
	if !strings.Contains(strings.Join(panel.requests, " "), "POST /api/databases/db_1/stop?force=true") {
		t.Errorf("--force was not passed on: %v", panel.requests)
	}
	if err := json.Unmarshal([]byte(runDB(t, "start", "orders", "--json")), &database); err != nil {
		t.Fatalf("start --json: %v", err)
	}

	runDB(t, "resize", "orders", "--memory-limit", "2048", "--storage", "20", "--json")
	var resize map[string]int
	if err := json.Unmarshal([]byte(panel.body("PATCH /api/databases/db_1")), &resize); err != nil ||
		len(resize) != 2 || resize["mem_limit_mb"] != 2048 || resize["storage_gb"] != 20 {
		t.Errorf("resize sent %v (%v): only what was given", resize, err)
	}

	// Without --show-password, the password is nowhere in the answer.
	out := runDB(t, "password", "orders", "--json")
	if strings.Contains(out, "the-new-password") || !strings.Contains(out, "op_1") {
		t.Errorf("password --json: %s", out)
	}

	// A chosen one comes from standard input; --show-password waits and
	// prints the new one.
	dbStdin = strings.NewReader("chosen-password-from-stdin\n")
	t.Cleanup(func() { dbStdin = os.Stdin })
	out = runDB(t, "password", "orders", "--password-stdin", "--show-password", "--json")
	var shown map[string]any
	if err := json.Unmarshal([]byte(out), &shown); err != nil || shown["password"] != "the-new-password-from-the-panel" {
		t.Errorf("password --show-password --json: %s", out)
	}
	if body := panel.body("POST /api/databases/db_1/password"); !strings.Contains(body, `"password":"chosen-password-from-stdin"`) {
		t.Errorf("the chosen password was sent as %s", body)
	}

	// An import from a file, then from standard input.
	file := filepath.Join(t.TempDir(), "shop.sql")
	if err := os.WriteFile(file, []byte("CREATE TABLE t (id int);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var op map[string]any
	if err := json.Unmarshal([]byte(runDB(t, "import", "orders", file, "--format", "sql", "--json")), &op); err != nil || op["id"] != "op_1" {
		t.Fatalf("import --json: %v %v", op, err)
	}
	if body := panel.body("POST /api/databases/db_1/import"); body != "CREATE TABLE t (id int);\n" {
		t.Errorf("the import sent %q", body)
	}
	dbStdin = strings.NewReader("PGDMP-from-a-pipe")
	runDB(t, "import", "orders", "-", "--json")
	if body := panel.body("POST /api/databases/db_1/import"); body != "PGDMP-from-a-pipe" {
		t.Errorf("an import from standard input sent %q", body)
	}
	if !strings.Contains(strings.Join(panel.requests, " "), "import?format=sql") {
		t.Errorf("--format was not passed on: %v", panel.requests)
	}
}

func TestAResizeOfNothingIsRefusedBeforeThePanelIsAsked(t *testing.T) {
	panel := &lifePanel{}
	panel.serve(t)
	var out bytes.Buffer
	if err := cmdDB(t.Context(), []string{"resize", "orders", "--env", "env_1"}, &out); err == nil {
		t.Fatal("a resize of nothing was sent")
	}
	if err := cmdDB(t.Context(), []string{"import", "orders", "--env", "env_1"}, &out); err == nil {
		t.Fatal("an import of nothing was sent")
	}
	for _, request := range panel.requests {
		if strings.HasPrefix(request, "PATCH") || strings.Contains(request, "/import") {
			t.Errorf("the panel was asked %s", request)
		}
	}
}
