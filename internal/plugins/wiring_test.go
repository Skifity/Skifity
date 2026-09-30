package plugins

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every event the standard declares has to be one the panel actually sends.
//
// This is the third time this repository has found the same shape of bug: a
// thing that is declared, validated, documented and shown in the interface,
// and then never delivered. Seven hub events nobody listened for (Phase 61).
// A build setting detected and dropped before the build (Phase 66). And this
// one, which is worse than both because it is a promise to somebody else's
// code: a plugin subscribes to "backup.failed", the manifest accepts it, the
// install screen says the plugin will be told, and the panel never tells it.
// Nothing fails. The plugin simply never hears, and its author has no way to
// find out except by waiting for a backup to fail and watching nothing happen.
//
// So: for each event in Events, some file outside this package has to name its
// constant. That is not proof the call is in the right place, but it is proof
// that a call exists, and it is the check that would have caught eight of ten.
func TestEveryDeclaredEventIsActuallySent(t *testing.T) {
	root := repoRoot(t)
	constants := eventConstants(t, filepath.Join(root, "manifest.go"))
	if len(constants) < len(Events) {
		t.Fatalf("read %d event constants for %d events; this test is not reading manifest.go",
			len(constants), len(Events))
	}

	used := map[string]bool{}
	dispatchers := 0
	for _, dir := range []string{"api", "backup", "cluster", "dbsvc", "deploy", "provision", "serverapp", "watch"} {
		walkGo(t, filepath.Join(root, "..", dir), func(path string, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				method, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (method.Sel.Name != "Notify" && method.Sel.Name != "Ask") {
					return true
				}
				for _, arg := range call.Args {
					sel, ok := arg.(*ast.SelectorExpr)
					if !ok {
						continue
					}
					pkg, ok := sel.X.(*ast.Ident)
					if !ok || pkg.Name != "plugins" {
						continue
					}
					if value, known := constants[sel.Sel.Name]; known {
						used[value] = true
						dispatchers++
					}
				}
				return true
			})
		})
	}

	if dispatchers == 0 {
		t.Fatal("no plugin event is sent from anywhere; this test is not reading the repository")
	}

	for _, event := range Events {
		if !used[event] {
			t.Errorf("the standard declares %q and nothing in the panel ever sends it: "+
				"a plugin subscribing to it is told it will hear, and never does", event)
		}
	}
	t.Logf("%d events, each sent from somewhere", len(Events))
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	return dir
}

// eventConstants maps the Go constant name to the string it holds.
func eventConstants(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "Event") || i >= len(spec.Values) {
				continue
			}
			literal, ok := spec.Values[i].(*ast.BasicLit)
			if !ok {
				continue
			}
			out[name.Name] = strings.Trim(literal.Value, `"`)
		}
		return true
	})
	return out
}

func walkGo(t *testing.T, dir string, visit func(path string, file *ast.File)) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // a directory that is not there is not this test's business
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return nil //nolint:nilerr // deliberately skipped: see above
		}
		visit(path, file)
		return nil
	})
}
