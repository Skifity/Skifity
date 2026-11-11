package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/cli"
)

// get_app_logs reads every instance, says which wrote each line once there is
// more than one, and passes one instance and a time range on by name.
func TestAppLogsSayWhichInstanceWroteEachLine(t *testing.T) {
	var asked url.Values
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/apps/app_1/logs" {
			t.Errorf("asked for %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		asked = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"lines":["listening","out of memory"],
"instances":["web-5d8f-aaaaa","web-5d8f-bbbbb"],"instances_omitted":3,
"entries":[{"instance":"web-5d8f-aaaaa","at":"2026-11-09T10:00:01Z","line":"listening"},
{"instance":"web-5d8f-bbbbb","at":"2026-11-09T10:00:02Z","line":"out of memory"}]}`)
	}))
	t.Cleanup(panel.Close)

	session := connect(t, NewRemote(cli.Config{PanelURL: panel.URL, Token: "skf_test", TeamID: "team_1"}, nil))
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "get_app_logs",
		Arguments: map[string]any{"app_id": "app_1", "instance": "bbbbb", "since": "1h"},
	})
	if err != nil || result.IsError {
		t.Fatalf("get_app_logs: %v %+v", err, result)
	}
	if asked.Get("instance") != "bbbbb" || asked.Get("since") != "1h" {
		t.Fatalf("the panel was asked for %v", asked)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "[aaaaa] listening\n[bbbbb] out of memory") {
		t.Errorf("the lines do not say which instance wrote them: %q", text)
	}
	if !strings.Contains(text, "Read 2 of 5 instances") {
		t.Errorf("the instances left out are not said: %q", text)
	}

	encoded, _ := json.Marshal(result.StructuredContent)
	var out logsOutput
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 2 || out.Entries[1].Instance != "web-5d8f-bbbbb" || out.Entries[1].At == "" ||
		len(out.Lines) != 2 || out.InstancesOmitted != 3 {
		t.Errorf("the structured answer is %+v", out)
	}
}
