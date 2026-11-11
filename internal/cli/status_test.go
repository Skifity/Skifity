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
	"time"
)

// statusPanel answers an app's status mid-rollout, and a history whose
// newest deployment is still building and whose live one is a rollback.
func statusPanel(t *testing.T) {
	t.Helper()
	finished := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/apps/app_1/status":
			_, _ = io.WriteString(w, `{"phase":"updating","desired_replicas":2,"ready_replicas":2,
"urls":["https://web.example.test"],"instances":[
{"name":"web-new","status":"Running","ready":true,"node":"s1","deployment":6},
{"name":"web-old","status":"Running","ready":true,"node":"s2","deployment":5,"restarts":3,
 "last_exit":{"reason":"OOMKilled","exit_code":137,"finished_at":"`+finished+`"}}]}`)
		case "/api/apps/app_1/deployments":
			_, _ = io.WriteString(w, `{"items":[
{"id":"dep_7","number":7,"status":"building","commit_sha":"9f8e7d6c5b4a","commit_author":"Someone Else"},
{"id":"dep_6","number":6,"status":"succeeded","trigger":"rollback","rollback_of":4,
 "commit_sha":"abc1234def5678","commit_author":"Ada Lovelace","finished_at":"`+finished+`"},
{"id":"dep_5","number":5,"status":"succeeded","commit_sha":"0123456789ab"}]}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
}

// `skifity status` says which version is live — the newest that succeeded,
// not the newest — and which instances are still on another one.
func TestStatusSaysWhichVersionIsLive(t *testing.T) {
	statusPanel(t)
	var out bytes.Buffer
	if err := cmdStatus(t.Context(), []string{"--app", "app_1"}, &out); err != nil {
		t.Fatal(err)
	}
	printed := out.String()
	if !strings.Contains(printed, "Live: #6 abc1234 by Ada Lovelace, 3h ago") {
		t.Errorf("the live line is missing or wrong:\n%s", printed)
	}
	if strings.Contains(printed, "#7") {
		t.Errorf("a deployment still building was named as running:\n%s", printed)
	}
	for _, line := range strings.Split(printed, "\n") {
		switch {
		case strings.Contains(line, "web-old") && !strings.Contains(line, "last ended: out of memory (exit code 137), 3h ago"):
			t.Errorf("an instance killed for its memory does not say so: %q", line)
		case strings.Contains(line, "web-old") && !strings.Contains(line, "#5"):
			t.Errorf("an instance on the version before does not say so: %q", line)
		case strings.Contains(line, "web-new") && strings.Contains(line, "#6"):
			t.Errorf("an instance on the live version repeats it: %q", line)
		}
	}

	out.Reset()
	if err := cmdStatus(t.Context(), []string{"--app", "app_1", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var answer struct {
		LiveDeployment struct {
			Number int `json:"number"`
		} `json:"live_deployment"`
		Instances []struct {
			Deployment int `json:"deployment"`
		} `json:"instances"`
	}
	if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
		t.Fatalf("--json printed something that is not JSON: %v\n%s", err, out.String())
	}
	if answer.LiveDeployment.Number != 6 || len(answer.Instances) != 2 || answer.Instances[1].Deployment != 5 {
		t.Errorf("--json printed %s", out.String())
	}
}
