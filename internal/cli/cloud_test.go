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

const fakeCloudToken = "hcloud-cli-test-token-not-real-0000"

// cloudPanel answers the cloud routes for one team, and remembers what it was
// sent.
type cloudPanel struct {
	mu      sync.Mutex
	added   []map[string]string
	ordered []map[string]any
	polls   int
}

func (p *cloudPanel) serve(t *testing.T) {
	t.Helper()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p.mu.Lock()
		defer p.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/me":
			_, _ = io.WriteString(w, `{"user":{"id":"usr_1"},"teams":[{"id":"team_1","name":"Acme"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/teams/team_1/cloud-providers":
			_, _ = io.WriteString(w, `{"items":[{"id":"cld_1","kind":"hetzner","title":"Hetzner Cloud","name":"eu",
				"token_hint":"0000","servers":1}],"total":1}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/teams/team_1/cloud-providers":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.added = append(p.added, body)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"cld_2","kind":"hetzner","title":"Hetzner Cloud","name":"`+body["name"]+`","token_hint":"0000"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/teams/team_1/servers/cloud":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.ordered = append(p.ordered, body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"op_1","kind":"server.create","status":"pending","target_id":"srv_1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/operations/op_1":
			p.polls++
			status := "running"
			if p.polls > 1 {
				status = "succeeded"
			}
			_, _ = io.WriteString(w, `{"id":"op_1","kind":"server.create","status":"`+status+`","target_id":"srv_1",
				"steps":[{"key":"cloud-create","status":"succeeded","message":"Ordered web-1"}]}`)
		default:
			http.Error(w, `{"error":{"code":"not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")
	was := followInterval
	followInterval = time.Millisecond
	t.Cleanup(func() { followInterval = was })
}

// The token comes from a file or standard input, never the command line, and
// every answer is JSON when asked for.
func TestACloudTokenIsReadFromAFileOrStandardInput(t *testing.T) {
	panel := &cloudPanel{}
	panel.serve(t)
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(fakeCloudToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := cmdCloud(t.Context(), []string{"providers", "add", "--name", "eu", "--token-file", tokenFile, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var added map[string]any
	if err := json.Unmarshal(out.Bytes(), &added); err != nil || added["id"] != "cld_2" {
		t.Errorf("--json printed %q (%v)", out.String(), err)
	}

	cloudStdin = strings.NewReader(fakeCloudToken)
	t.Cleanup(func() { cloudStdin = os.Stdin })
	out.Reset()
	if err := cmdCloud(t.Context(), []string{"providers", "add", "--token-file", "-"}, &out); err != nil {
		t.Fatal(err)
	}

	for name, args := range map[string][]string{
		"the token itself": {"providers", "add", "--token-file", fakeCloudToken},
		"no token":         {"providers", "add"},
	} {
		if err := cmdCloud(t.Context(), args, &out); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	out.Reset()
	if err := cmdCloud(t.Context(), []string{"providers", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var listed []map[string]any
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Errorf("the list printed %q (%v)", out.String(), err)
	}

	panel.mu.Lock()
	defer panel.mu.Unlock()
	if len(panel.added) != 2 {
		t.Fatalf("%d connections reached the panel", len(panel.added))
	}
	for _, body := range panel.added {
		if body["token"] != fakeCloudToken || body["kind"] != "hetzner" {
			t.Errorf("the panel was sent %v", body)
		}
	}

}

// `servers create --provider hetzner` finds the team's one Hetzner connection,
// orders, and follows the operation to one JSON document.
func TestServersCreateOrdersAndFollows(t *testing.T) {
	panel := &cloudPanel{}
	panel.serve(t)

	var out bytes.Buffer
	err := cmdServers(t.Context(), []string{"create", "web-1", "--provider", "hetzner", "--location", "fsn1",
		"--type", "cax11", "--json"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	var finished map[string]any
	if err := json.Unmarshal(out.Bytes(), &finished); err != nil || finished["status"] != "succeeded" {
		t.Errorf("--json printed %q (%v); want one document, the finished operation", out.String(), err)
	}
	panel.mu.Lock()
	defer panel.mu.Unlock()
	if len(panel.ordered) != 1 {
		t.Fatalf("%d orders", len(panel.ordered))
	}
	order := panel.ordered[0]
	if order["provider_id"] != "cld_1" || order["name"] != "web-1" || order["server_type"] != "cax11" ||
		order["image"] != "ubuntu-24.04" || order["ssh_access"] != "anywhere" {
		t.Errorf("ordered %v", order)
	}
}
