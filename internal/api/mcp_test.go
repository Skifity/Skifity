package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/store"
)

// The panel's own MCP endpoint, driven by a real MCP client over HTTP.

// bearer adds a token to every request, which is how an assistant's client is
// configured for a remote server.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// connectMCP opens a client session against /api/mcp as a tenant.
func (h *harness) connectMCP(t *testing.T, as tenant) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:             h.server.URL + "/api/mcp",
		HTTPClient:           &http.Client{Transport: bearer{token: as.token, next: http.DefaultTransport}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatalf("connect to /api/mcp: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callText calls a tool and returns what it said, and whether it said it as
// an error.
func callText(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var out strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			out.WriteString(text.Text)
		}
	}
	if result.StructuredContent != nil {
		encoded, _ := json.Marshal(result.StructuredContent)
		out.Write(encoded)
	}
	return out.String(), result.IsError
}

func TestAnAssistantCanUseThePanelsOwnMCPEndpoint(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	other := h.newTenant("other")
	h.app(other, "their-app")
	app := h.app(acme, "web")

	session := h.connectMCP(t, acme)

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	if !names["list_apps"] || !names["set_variable"] {
		t.Fatalf("the endpoint offers %v", names)
	}
	if names["deploy_folder"] {
		t.Fatal("the endpoint offers deploy_folder, which would read the panel's own disk")
	}

	// A read goes through the API as the token's owner, and sees their team
	// and nobody else's.
	text, failed := callText(t, session, "list_apps", map[string]any{"environment_id": acme.env.ID})
	if failed || !strings.Contains(text, app.ID) || strings.Contains(text, "their-app") {
		t.Fatalf("list_apps answered (error=%v): %s", failed, text)
	}

	// A change goes through too, and is the same change the API would make.
	text, failed = callText(t, session, "set_variable",
		map[string]any{"app_id": app.ID, "key": "GREETING", "value": "hello", "is_secret": false})
	if failed {
		t.Fatalf("set_variable failed: %s", text)
	}
	variables, err := h.db.ListVariables(t.Context(), app.ID)
	if err != nil || len(variables) != 1 || variables[0].Key != "GREETING" {
		t.Fatalf("the variable was not set: %+v %v", variables, err)
	}

	// And another team's app is as invisible through the tools as it is
	// through the API.
	theirs := h.app(other, "secret-app")
	if text, failed := callText(t, session, "get_app_status", map[string]any{"app_id": theirs.ID}); !failed {
		t.Fatalf("another team's app answered through MCP: %s", text)
	}
}

// Every tool call is checked against the caller's role, because it is made
// with the caller's token.
func TestAViewersAssistantCanReadAndNotChange(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	viewer := h.newMember(acme, "viewer", store.RoleViewer)

	session := h.connectMCP(t, viewer)
	if text, failed := callText(t, session, "list_apps", map[string]any{"environment_id": acme.env.ID}); failed ||
		!strings.Contains(text, app.ID) {
		t.Fatalf("a viewer's assistant could not read: %s", text)
	}
	text, failed := callText(t, session, "set_variable",
		map[string]any{"app_id": app.ID, "key": "GREETING", "value": "hello", "is_secret": false})
	if !failed {
		t.Fatalf("a viewer's assistant changed a variable: %s", text)
	}
	if variables, _ := h.db.ListVariables(t.Context(), app.ID); len(variables) != 0 {
		t.Fatalf("a viewer's assistant changed a variable: %+v", variables)
	}
}

// The endpoint answers API tokens. No credentials is a 401 like everywhere
// else, and so is a browser session: a cookie is what another site can make a
// browser send.
func TestTheMCPEndpointWantsAToken(t *testing.T) {
	h := newHarness(t)
	h.newTenant("acme")

	status, body := h.do(tenant{}, http.MethodPost, "/api/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if status != http.StatusUnauthorized {
		t.Fatalf("an anonymous call answered %d: %s", status, truncate(body, 200))
	}

	const password = "correct horse battery staple"
	h.person(t, "browser@example.test", password)
	b := h.signIn(t, "browser@example.test", password)
	status, body = h.send(t, b, http.MethodPost, "/api/mcp", b.csrf,
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if status != http.StatusUnauthorized || !strings.Contains(body, "mcp.token_required") {
		t.Fatalf("a browser session answered %d: %s", status, truncate(body, 200))
	}
}

// A token limited to reading reaches the endpoint — which is a POST whatever
// the tool does — and its tools read, and cannot write: each request a tool
// makes is checked against the token's scope on its own.
func TestAReadOnlyTokensAssistantReads(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	_, token, err := h.auth.CreateAPIToken(t.Context(), acme.user.ID, acme.team.ID, "reader", "read", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reader := acme
	reader.token = token

	session := h.connectMCP(t, reader)
	if text, failed := callText(t, session, "list_apps", map[string]any{"environment_id": acme.env.ID}); failed ||
		!strings.Contains(text, app.ID) {
		t.Fatalf("a read-only token could not read: %s", text)
	}
	if text, failed := callText(t, session, "set_variable",
		map[string]any{"app_id": app.ID, "key": "GREETING", "value": "hello", "is_secret": false}); !failed ||
		!strings.Contains(text, "auth.token_scope") {
		t.Fatalf("a read-only token wrote: %s", text)
	}
}
