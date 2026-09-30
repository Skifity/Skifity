package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What an assistant is told about an app's vulnerabilities: the counts, the
// most severe findings and no more than it can use, that the report is of an
// image the app has moved on from, that deploys are being stopped — and that
// going past that is a person's call. It only reads.
func TestVulnerabilitiesAreRelayedAndOnlyRead(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_vulnerabilities", Arguments: map[string]any{"app_id": "app_1"},
	})
	if err != nil || result.IsError {
		t.Fatalf("get_vulnerabilities failed: %v %+v", err, result)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var out vulnerabilitiesOutput
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Scanned || out.Counts.Critical != 1 || out.Counts.Low != 60 || out.FixableCrit != 1 {
		t.Errorf("the counts were not passed on: %+v", out)
	}
	if len(out.Findings) != vulnerabilityFindingsShown || out.NotShown != 61-vulnerabilityFindingsShown {
		t.Errorf("passed on %d findings and said %d more, want %d and %d",
			len(out.Findings), out.NotShown, vulnerabilityFindingsShown, 61-vulnerabilityFindingsShown)
	}
	if first := out.Findings[0]; first.Package != "openssl" || first.FixedIn != "3.0.15" {
		t.Errorf("the most severe finding is not first, with its fix: %+v", first)
	}
	if out.Current || !out.DeploysBlocked {
		t.Errorf("current=%v blocked=%v; the report is of an older image and deploys are stopped", out.Current, out.DeploysBlocked)
	}
	for _, want := range []string{"another image than the one the app runs", "only a person should", "newest scan failed"} {
		if !strings.Contains(out.Note, want) {
			t.Errorf("the note does not say %q: %s", want, out.Note)
		}
	}

	for _, request := range panel.requests() {
		if request.Method != "GET" {
			t.Errorf("reading vulnerabilities sent %s %s", request.Method, request.Path)
		}
	}
	for _, tool := range listTools(t, New(panel.config())) {
		if tool.Name == "get_vulnerabilities" && !tool.Annotations.ReadOnlyHint {
			t.Error("get_vulnerabilities is not marked read-only")
		}
	}
}
