package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What needs attention reaches an assistant as the panel words it, counted,
// with a link a person can open, and asking for it changes nothing.
func TestAttentionIsRelayedWithLinksAndOnlyRead(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_attention", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("get_attention failed: %v %+v", err, result)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var out attentionOutput
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	if out.Errors != 1 || out.Warnings != 1 || len(out.Items) != 2 {
		t.Fatalf("counted %d errors and %d warnings in %d items", out.Errors, out.Warnings, len(out.Items))
	}
	first := out.Items[0]
	if first.Message != "Deployment #2 of web failed." || first.Project != "acme" || first.Since == "" {
		t.Errorf("the first item is %+v", first)
	}
	if !strings.HasPrefix(first.URL, panel.config().PanelURL) ||
		!strings.HasSuffix(first.URL, "/apps/app_1?tab=deployments&deployment=dep_2") {
		t.Errorf("the link is %q, want the panel's page for the deployment", first.URL)
	}

	for _, request := range panel.requests() {
		if request.Method != "GET" {
			t.Errorf("reading what needs attention sent %s %s", request.Method, request.Path)
		}
	}
	for _, tool := range listTools(t, New(panel.config())) {
		if tool.Name == "get_attention" && !tool.Annotations.ReadOnlyHint {
			t.Error("get_attention is not marked read-only")
		}
	}
}
