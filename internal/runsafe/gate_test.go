package runsafe_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every goroutine the panel starts has to recover.
//
// Go ends the whole process when a goroutine panics, and this process is often
// the only way to reach the cluster: the deployment that crashed the panel also
// removed the thing you would use to find out why. The package comment here
// lists the goroutines that were fixed when that was first noticed — and it was
// a list, written by hand, so five files were missed. A stream of an app's own
// log output, the fan-out to a browser, an event posted to somebody else's
// plugin container, the SSH readers during provisioning, and the guard itself.
//
// This is that list, computed instead of remembered. A `go func` with nothing
// to catch a panic fails the build.
//
// It does not check that recovering is *correct*. A recover that leaves a
// channel nobody will ever send on is a hang rather than a crash, which is not
// obviously better; two of the SSH goroutines turn a panic into the failure
// their waiter is expecting for exactly that reason. That judgement stays with
// whoever writes the goroutine. This only makes sure it was made.
func TestEveryGoroutineInThePanelRecovers(t *testing.T) {
	checked := 0
	walkGo(t, panelRoot(t), func(path string, fset *token.FileSet, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			statement, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			checked++
			literal, ok := statement.Call.Fun.(*ast.FuncLit)
			if !ok {
				// `go someFunc()`: whatever it calls has to recover, and this
				// test cannot see inside it from here. runsafe.Go is the form
				// that keeps it visible, and is what such a call should be.
				return true
			}
			if !recovers(literal.Body, file.Name.Name == "runsafe") {
				t.Errorf("%s: a goroutine with nothing to catch a panic; "+
					"one panic here ends the whole panel",
					fset.Position(statement.Pos()))
			}
			return true
		})
	})

	if checked == 0 {
		t.Fatal("no goroutine was found anywhere; this test is not reading the repository")
	}
	if !t.Failed() {
		t.Logf("%d goroutines, each with something to catch a panic", checked)
	}
}

// recovers reports whether a function body defers something that recovers:
// runsafe.Recover, or a bare recover() in a deferred literal.
func recovers(body *ast.BlockStmt, inRunsafe bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		deferred, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		ast.Inspect(deferred, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				// "Recover" unqualified is this package calling its own, which
				// is what runsafe.Go does.
				if fun.Name == "recover" || (inRunsafe && fun.Name == "Recover") {
					found = true
				}
			case *ast.SelectorExpr:
				if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "runsafe" && fun.Sel.Name == "Recover" {
					found = true
				}
			}
			return true
		})
		return true
	})
	return found
}

// panelRoot is internal/, which is every package this binary is made of. Named
// as a whole rather than listed, so a package written tomorrow is covered.
func panelRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	return filepath.Dir(dir)
}

func walkGo(t *testing.T, dir string, visit func(path string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // a directory that is not there is not this test's business
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil //nolint:nilerr // deliberately skipped: see above
		}
		visit(path, fset, parsed)
		return nil
	})
}
