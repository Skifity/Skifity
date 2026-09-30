package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

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
