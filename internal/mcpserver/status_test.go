package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/cli"
)

// get_app_status names the version that is live: the newest deployment that
// succeeded, which after a rollback and during the next build is neither the
// highest number nor the newest code.
//
// An assistant that deployed and then asked for the status was told the app
// was running, and nothing told it whether that was its deploy or the one
// before.
func TestAppStatusNamesTheLiveDeployment(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/apps/app_1/status":
			_, _ = io.WriteString(w, `{"phase":"running","desired_replicas":1,"ready_replicas":1,
"instances":[{"name":"web-1","status":"Running","ready":true,"node":"s1","deployment":6,"restarts":2,
"last_exit":{"reason":"OOMKilled","exit_code":137,"finished_at":"2026-11-01T09:00:00Z"}}]}`)
		case "/api/apps/app_1/deployments":
			_, _ = io.WriteString(w, `{"items":[
{"id":"dep_7","number":7,"status":"building","commit_sha":"9f8e7d6"},
{"id":"dep_6","number":6,"status":"succeeded","trigger":"rollback","rollback_of":4,
 "commit_sha":"abc1234","commit_message":"Fix the checkout","commit_author":"Ada Lovelace",
 "image":"registry.local/web:abc1234","finished_at":"2026-11-01T10:00:00Z"}]}`)
		default:
			t.Errorf("asked for %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)

	session := connect(t, NewRemote(cli.Config{PanelURL: panel.URL, Token: "skf_test", TeamID: "team_1"}, nil))
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_app_status", Arguments: map[string]any{"app_id": "app_1"},
	})
	if err != nil || result.IsError {
		t.Fatalf("get_app_status: %v %+v", err, result)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "#6 is live") {
		t.Errorf("the summary does not say which version is live: %q", text)
	}
	if !strings.Contains(text, "web-1's previous container ended 2026-11-01T09:00:00Z: OOMKilled, exit code 137") {
		t.Errorf("the summary does not say how the instance last ended: %q", text)
	}

	encoded, _ := json.Marshal(result.StructuredContent)
	var out statusOutput
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	live := out.LiveDeployment
	if live == nil || live.Number != 6 || live.ID != "dep_6" || live.RollbackOf != 4 ||
		live.Commit != "abc1234" || live.CommitAuthor != "Ada Lovelace" || live.LiveSince != "2026-11-01T10:00:00Z" {
		t.Errorf("live_deployment is %+v", live)
	}
	if len(out.Instances) != 1 || out.Instances[0].Deployment != 6 {
		t.Errorf("the instance does not say which deployment it runs: %+v", out.Instances)
	}
	if exit := out.Instances[0].LastExit; exit == nil || exit.Reason != "OOMKilled" || exit.ExitCode != 137 {
		t.Errorf("the instance does not say how its last container ended: %+v", exit)
	}
}

// Nothing live is not a guess at the newest.
func TestAppStatusBeforeAnythingWentLiveNamesNoVersion(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/apps/app_1/status" {
			_, _ = io.WriteString(w, `{"phase":"not_deployed","desired_replicas":0,"ready_replicas":0}`)
			return
		}
		_, _ = io.WriteString(w, `{"items":[{"id":"dep_1","number":1,"status":"failed"}]}`)
	}))
	t.Cleanup(panel.Close)

	session := connect(t, NewRemote(cli.Config{PanelURL: panel.URL, Token: "skf_test", TeamID: "team_1"}, nil))
	text, isError := call(t, session, "get_app_status", map[string]any{"app_id": "app_1"})
	if isError || strings.Contains(text, "live_deployment") || strings.Contains(text, "is live") {
		t.Errorf("an app with nothing live answered %s", text)
	}
}
