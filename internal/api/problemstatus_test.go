package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every problem this package answers with has to say which status it is.
//
// An errdoc.Problem with no status is a 500, and a 500 is not a detail: the
// frontend renders it as "something went wrong" rather than as the thing the
// person just typed wrongly, the access log records it at ERROR where it
// drowns the real ones, and a client deciding whether to retry gets the wrong
// answer. A schedule of "every night please" was answering 500 until a test
// for volume backups happened to look at the code.
//
// Only this package. Below it, a package that returns a problem is usually
// describing something that genuinely went wrong on the server, and 500 is
// the right default there.
func TestEveryProblemThisPackageAnswersWithSaysItsStatus(t *testing.T) {
	// A problem that really is a server fault, with the reason it is exempt.
	serverFaults := map[string]string{
		"internal": "the panic handler: by definition nobody's input",
	}

	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the package: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// Walk the method chain from the outside in, looking for the
			// errdoc.New at its root and WithStatus anywhere along the way.
			code, hasStatus, found := chainRoot(call)
			if !found {
				return true
			}
			checked++
			if hasStatus {
				return false
			}
			if reason, exempt := serverFaults[code]; exempt {
				_ = reason
				return false
			}
			t.Errorf(`%s: errdoc.New(%q) does not say its status, so it answers 500. `+
				`Add WithStatus, or name it in serverFaults with the reason it is one.`, name, code)
			return false
		})
	}

	if checked < 20 {
		t.Fatalf("only %d problems were read; this test is not walking the package", checked)
	}
	t.Logf("%d problems, each one saying what it answers", checked)
}

// chainRoot reports the code an errdoc.New chain was built from, and whether
// WithStatus appears anywhere in it.
func chainRoot(call *ast.CallExpr) (code string, hasStatus, found bool) {
	for {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", false, false
		}
		// errdoc.New("code", ...) — the root.
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "errdoc" {
			if sel.Sel.Name != "New" && sel.Sel.Name != "Newf" {
				return "", false, false
			}
			if len(call.Args) == 0 {
				return "", false, false
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return "", false, false
			}
			return strings.Trim(literal.Value, `"`), hasStatus, true
		}
		if sel.Sel.Name == "WithStatus" {
			hasStatus = true
		}
		inner, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return "", false, false
		}
		call = inner
	}
}
