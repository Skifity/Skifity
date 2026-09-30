package settings

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A setting somebody fills in has to be read by something.
//
// Thirteen of forty-five were not. Six SMTP fields on a page headed "Email"
// that no email ever consulted; a registry username and password the build
// never turned into a credential, so an external registry broke the deploy at
// both ends; a default-builder choice that every app ignored; and three DNS
// fields for a feature that does not exist, one of which asked for an API
// token — a real credential, collected, sealed, and used for nothing.
//
// A form field is a promise. This is the check that the promise is kept: every
// Key constant declared here has to be named somewhere outside this package.
func TestEverySettingIsReadBySomething(t *testing.T) {
	// Written by the panel rather than filled in by a person, and read through
	// the same constants — they are covered by the walk below like any other.
	// Anything genuinely exempt goes here with the reason.
	exempt := map[string]string{
		"KeyTelemetryDisabled": "there is no telemetry to disable; the setting exists so its absence is visible, " +
			"which its own help text says",
	}

	declared := constants(t, "settings.go")
	if len(declared) < 20 {
		t.Fatalf("read %d settings constants; this test is not reading settings.go", len(declared))
	}

	used := map[string]bool{}
	root := filepath.Join("..", "..")
	for _, dir := range []string{"internal", "cmd"} {
		walk(t, filepath.Join(root, dir), func(path string, file *ast.File) {
			if strings.Contains(path, filepath.Join("internal", "settings")) {
				return
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "settings" {
					used[sel.Sel.Name] = true
				}
				return true
			})
		})
	}

	checked := 0
	for name := range declared {
		if reason, ok := exempt[name]; ok {
			if used[name] {
				t.Errorf("%s is exempt because %s, and something reads it now — remove the exemption", name, reason)
			}
			continue
		}
		checked++
		if !used[name] {
			t.Errorf("settings.%s is on the settings page and nothing reads it: "+
				"wire it up, or take the field away rather than collecting an answer nobody uses", name)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d settings were checked; this test is not comparing what it thinks", checked)
	}
	t.Logf("%d settings, each read by something", checked)
}

func constants(t *testing.T, path string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for _, name := range spec.Names {
			if strings.HasPrefix(name.Name, "Key") {
				out[name.Name] = true
			}
		}
		return true
	})
	return out
}

func walk(t *testing.T, dir string, visit func(path string, file *ast.File)) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // a directory that is not there is not this test's business
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return nil //nolint:nilerr // deliberately skipped: see above
		}
		visit(path, parsed)
		return nil
	})
}
