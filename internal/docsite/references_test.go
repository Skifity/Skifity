package docsite

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryPathTheDocumentsNameExists: a document that describes this
// repository can be wrong about it, and nothing noticed. `test/smoke/panel.sh`
// announced "the four cluster smoke tests in this directory" and ADR-0010 said
// they lived there and that the progress log recorded which had been run. There
// were none, there never had been, and the claim survived every reading of
// those files because reading is not checking.
//
// So the paths are checked. Not the prose — no test can check prose — but the
// part of it that is a fact about the filesystem.
func TestEveryPathTheDocumentsNameExists(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("locate the repository root: %v", err)
	}

	documents := documentsIn(t, root)
	if len(documents) < 10 {
		t.Fatalf("only %d documents were found; this test is not reading them", len(documents))
	}

	checked := 0
	for _, document := range documents {
		body, err := os.ReadFile(document)
		if err != nil {
			t.Fatalf("read %s: %v", document, err)
		}
		relative, _ := filepath.Rel(root, document)
		for _, path := range repositoryPaths(string(body)) {
			checked++
			if _, err := os.Stat(filepath.Join(root, path)); err != nil {
				t.Errorf("%s names %s, which is not in the repository", relative, path)
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d paths were checked; the pattern is not matching what the documents write", checked)
	}
}

// documentsIn returns the Markdown files that describe the repository.
func documentsIn(t *testing.T, root string) []string {
	t.Helper()

	var out []string
	for _, pattern := range []string{"*.md", "docs/*.md", "docs/research/*.md"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		out = append(out, matches...)
	}
	return out
}

// backticked finds `code spans`, which is how this repository writes a path.
var backticked = regexp.MustCompile("`([^`\n]+)`")

// repositoryPaths returns the code spans that are paths into this repository.
//
// The test is only as good as this is careful: a code span is usually a command
// or a flag, and calling one of those a missing file would make the test
// something people turn off. So a span counts only when it starts at a
// directory this repository has at its root, or is a bare file at the root that
// looks like a file.
func repositoryPaths(body string) []string {
	roots := []string{
		"cmd/", "internal/", "web/", "docs/", "deploy/", "installer/", "test/", "scripts/", ".github/",
	}
	files := map[string]bool{
		"go.mod": true, "go.sum": true, "Makefile": true, "Dockerfile": true,
		"CLAUDE.md": true, "README.md": true, "LICENSE": true, ".env.example": true,
		"llms.txt": true, ".golangci.yml": true,
	}

	seen := map[string]bool{}
	var out []string
	for _, match := range backticked.FindAllStringSubmatch(body, -1) {
		span := strings.TrimSpace(match[1])
		// A command, a flag, a shell line, a URL or a Go expression is not a
		// path, whatever else it contains.
		if span == "" || strings.ContainsAny(span, " \t|$(){}<>*\"'") || strings.Contains(span, "://") {
			continue
		}
		span = strings.TrimSuffix(span, "/")

		isPath := files[span]
		for _, prefix := range roots {
			if strings.HasPrefix(span, prefix) {
				isPath = true
				break
			}
		}
		if !isPath || seen[span] {
			continue
		}
		seen[span] = true
		out = append(out, span)
	}
	return out
}
