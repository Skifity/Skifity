package deploy

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every scaling finding has words in all five languages.
//
// The readiness checker is the panel's best advice — eleven things that break
// when an app is scaled, each with what it found and what to do — and it was
// English on a Russian page. Same mechanism as the error catalogue: the server
// writes it once in English, which stays the fallback and what the CLI reads,
// and the panel looks it up by the finding's own code.
func TestEveryScalingFindingHasItsWords(t *testing.T) {
	codes := findingCodes(t)
	if len(codes) < 10 {
		t.Fatalf("only found %d findings, so this test has stopped reading the source", len(codes))
	}

	locales, err := filepath.Glob(filepath.Join("..", "..", "web", "src", "locales", "*.json"))
	if err != nil || len(locales) < 5 {
		t.Fatalf("find the locales: %v", err)
	}

	for _, path := range locales {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var locale struct {
			Scaling struct {
				Finding map[string]map[string]string `json:"finding"`
			} `json:"scaling"`
		}
		if err := json.Unmarshal(body, &locale); err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		language := strings.TrimSuffix(filepath.Base(path), ".json")

		for _, code := range codes {
			entry, ok := locale.Scaling.Finding[code]
			if !ok {
				t.Errorf("%s: the finding %q has no words: add scaling.finding.%s", language, code, code)
				continue
			}
			for _, field := range []string{"title", "detail", "fix"} {
				if strings.TrimSpace(entry[field]) == "" {
					t.Errorf("%s: scaling.finding.%s.%s is missing", language, code, field)
				}
			}
		}
		for code := range locale.Scaling.Finding {
			if !slices.Contains(codes, code) {
				t.Errorf("%s: scaling.finding.%s is not a finding the checker can report", language, code)
			}
		}
	}
}

// findingCodes reads every Code assigned to an api.ScalingFinding in this file.
func findingCodes(t *testing.T) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "scaling.go", nil, 0)
	if err != nil {
		t.Fatalf("read scaling.go: %v", err)
	}
	seen := map[string]bool{}
	ast.Inspect(parsed, func(node ast.Node) bool {
		composite, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := composite.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "ScalingFinding" {
			return true
		}
		for _, element := range composite.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := pair.Key.(*ast.Ident)
			if !ok || key.Name != "Code" {
				continue
			}
			if value, ok := pair.Value.(*ast.BasicLit); ok && value.Kind == token.STRING {
				if code, err := strconv.Unquote(value.Value); err == nil && code != "" {
					seen[code] = true
				}
			}
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for code := range seen {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}
