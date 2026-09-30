package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/cli"
)

// llms.txt is the page an assistant is pointed at, and it prints a table of
// these tools. An assistant reads the table and calls what is in it: a tool
// that is not there does not read as a stale document, it reads as the product
// being broken, and the assistant keeps trying.
//
// So the table is checked against the server rather than against a list
// somebody keeps by hand — and in both directions, because a tool nobody
// documents is a tool nobody uses.
func TestEveryToolLlmsTxtPromisesExists(t *testing.T) {
	documented := toolsFromLlmsTxt(t)
	if len(documented) < 10 {
		t.Fatalf("only %d tools were read from llms.txt; this test is not reading the table", len(documented))
	}

	registered := map[string]bool{}
	for _, name := range New(cli.Config{PanelURL: "https://panel.example", Token: "skf_test"}).ToolNames() {
		registered[name] = true
	}

	for _, name := range documented {
		if !registered[name] {
			t.Errorf("llms.txt documents the tool %q, and the MCP server does not offer it", name)
		}
	}
	for name := range registered {
		if !contains(documented, name) {
			t.Errorf("the MCP server offers %q and llms.txt does not mention it", name)
		}
	}
}

// toolsFromLlmsTxt reads the tool column out of the document's MCP table.
var toolRow = regexp.MustCompile("^\\|\\s*`([a-z_]+)`\\s*\\|")

func toolsFromLlmsTxt(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "llms.txt"))
	if err != nil {
		t.Fatalf("read llms.txt: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if match := toolRow.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			out = append(out, match[1])
		}
	}
	sort.Strings(out)
	return out
}

func contains(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// listTools asks a server for its tools the way an assistant's client does.
func listTools(t *testing.T, s *Server) []*mcp.Tool {
	t.Helper()
	result, err := connect(t, s).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return result.Tools
}

// The protocol's defaults for a tool that says nothing are the worst case:
// not read-only, and destructive. A careful client asks the person before
// every such call, so an unannotated list_apps is a question nobody needed.
// And a tool that changes something without saying whether it can destroy
// something is one the client cannot weigh at all.
//
// The description is what the model reads to decide whether this is the tool
// at all, so a tool without a real one is a tool chosen by its name alone.
func TestEveryToolSaysWhatItDoes(t *testing.T) {
	tools := listTools(t, New(cli.Config{PanelURL: "https://panel.example", Token: "skf_test"}))
	if len(tools) < 30 {
		t.Fatalf("only %d tools", len(tools))
	}
	for _, tool := range tools {
		if len(strings.Fields(tool.Description)) < 12 {
			t.Errorf("%s does not say what it does, what it returns and when to use it: %q", tool.Name, tool.Description)
		}
		a := tool.Annotations
		if a == nil || a.Title == "" {
			t.Errorf("%s says nothing about what it does", tool.Name)
			continue
		}
		// The names already say which tools only look. The annotations must
		// agree with them, so a tool renamed or added is checked against
		// what it is called.
		looks := strings.HasPrefix(tool.Name, "list_") || strings.HasPrefix(tool.Name, "get_") ||
			strings.HasPrefix(tool.Name, "check_")
		if a.ReadOnlyHint != looks {
			t.Errorf("%s is read-only=%v, and its name says %v", tool.Name, a.ReadOnlyHint, looks)
		}
		if !a.ReadOnlyHint && a.DestructiveHint == nil {
			t.Errorf("%s changes something and does not say whether it can destroy anything", tool.Name)
		}
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s does not say that it only reaches this panel", tool.Name)
		}
	}
}

// The panel's own endpoint runs on the panel's server, where a tool that
// reads local files would read the panel's database and master key.
func TestTheRemoteServerCannotReadThePanelsFiles(t *testing.T) {
	local := New(cli.Config{PanelURL: "https://panel.example", Token: "skf_test"}).ToolNames()
	remote := NewRemote(cli.Config{PanelURL: "https://panel.example", Token: "skf_test"}, nil).ToolNames()
	if contains(remote, "deploy_folder") {
		t.Fatal("the remote server offers deploy_folder, which would read the panel's own disk")
	}
	for _, name := range local {
		if name != "deploy_folder" && !contains(remote, name) {
			t.Errorf("the remote server is missing %s", name)
		}
	}
}
