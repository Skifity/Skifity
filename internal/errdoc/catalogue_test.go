package errdoc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The catalogue is the product's central promise: every failure a user can see
// says what happened, why it matters and what to do about it. An entry missing
// one of those still compiles, still renders, and is exactly the bare "exit
// status 1" this package exists to prevent — it just takes longer to notice.
//
// So the file is read as source. Each function here builds its Problem with a
// chain of calls, and what that chain contains is checkable without calling it,
// which matters because these take a host, an exit code, a command: arguments a
// test would have to invent.
func TestEveryEntryExplainsItself(t *testing.T) {
	entries := catalogueEntries(t)
	if len(entries) < 20 {
		t.Fatalf("only %d entries were found; this test is not reading the catalogue", len(entries))
	}

	codes := map[string]string{}
	codeShape := regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9_]+)+$`)
	// A code built from an argument — "preflight." + check — still has to start
	// with a family the UI can group by.
	familyShape := regexp.MustCompile(`^[a-z0-9]+\.`)

	for name, entry := range entries {
		for _, part := range []string{"WithCause", "WithImpact", "WithFix"} {
			if !entry.calls[part] {
				t.Errorf("%s has no %s, so the panel shows a title and nothing else",
					name, strings.TrimPrefix(part, "With"))
			}
		}
		if entry.code == "" {
			t.Errorf("%s calls New with a code that has no text in it at all", name)
			continue
		}
		switch {
		case entry.codeComplete:
			if !codeShape.MatchString(entry.code) {
				t.Errorf("%s has the code %q; a code is lower-case and dotted, such as ssh.auth_failed",
					name, entry.code)
			}
			// The UI picks a translation by code, so two entries sharing one
			// are two different failures shown with the same words. A code
			// completed at runtime is a family and cannot collide this way.
			if other, clash := codes[entry.code]; clash {
				t.Errorf("%s and %s both use the code %q", other, name, entry.code)
			}
			codes[entry.code] = name
		case !familyShape.MatchString(entry.code):
			t.Errorf("%s builds the code from %q, which does not start with a family such as preflight.",
				name, entry.code)
		}

		if strings.TrimSpace(entry.title) == "" {
			t.Errorf("%s has no title, which is what the UI falls back to without a translation", name)
		} else if entry.titleEndsLiteral && strings.HasSuffix(entry.title, ".") {
			t.Errorf("%s ends its title with a full stop: %q; a title is a heading", name, entry.title)
		}
	}
}

type entry struct {
	code, title string
	// Complete is false when the value is built from an argument, so only the
	// literal part of it is here.
	codeComplete, titleComplete bool
	// titleEndsLiteral says the title's last piece was written here rather
	// than passed in, so how it ends can still be checked.
	titleEndsLiteral bool
	calls            map[string]bool
}

// catalogueEntries reads catalogue.go and returns, per function that builds a
// Problem, the code and title it starts from and the chain it applies.
func catalogueEntries(t *testing.T) map[string]entry {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "catalogue.go", nil, 0)
	if err != nil {
		t.Fatalf("parse catalogue.go: %v", err)
	}

	out := map[string]entry{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		found := entry{calls: map[string]bool{}}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch target := call.Fun.(type) {
			case *ast.Ident:
				if (target.Name == "New" || target.Name == "Newf") && len(call.Args) >= 2 {
					found.code, found.codeComplete = text(call.Args[0])
					found.title, found.titleComplete = text(call.Args[1])
					found.titleEndsLiteral = endsLiteral(call.Args[1])
				}
			case *ast.SelectorExpr:
				found.calls[target.Sel.Name] = true
			}
			return true
		})
		// A sentence set on the field rather than formatted through the
		// builder still counts. PreflightFailed assigns Cause and Fix directly,
		// because the preflight report has already rendered them and kept the
		// values that went into them.
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, target := range assign.Lhs {
				sel, ok := target.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				switch sel.Sel.Name {
				case "Cause":
					found.calls["WithCause"] = true
				case "Impact":
					found.calls["WithImpact"] = true
				case "Fix":
					found.calls["WithFix"] = true
				}
			}
			return true
		})
		// A helper such as itoa builds no Problem and is not an entry.
		if found.code == "" && len(found.calls) == 0 {
			continue
		}
		if found.code == "" && !found.calls["WithCause"] {
			continue
		}
		out[fn.Name.Name] = found
	}
	return out
}

// endsLiteral reports whether the rightmost piece of an expression is a string
// literal, which is what makes "how does this title end" a question about the
// code rather than about an argument.
func endsLiteral(node ast.Expr) bool {
	switch value := node.(type) {
	case *ast.BasicLit:
		return value.Kind == token.STRING
	case *ast.BinaryExpr:
		return value.Op == token.ADD && endsLiteral(value.Y)
	default:
		return false
	}
}

// text returns the literal text of an expression and whether all of it was
// literal. `"preflight." + check` gives ("preflight.", false), which is enough
// to check the part the author actually wrote.
func text(node ast.Expr) (string, bool) {
	switch value := node.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(value.Value)
		if err != nil {
			return "", false
		}
		return unquoted, true
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := text(value.X)
		right, rightOK := text(value.Y)
		return left + right, leftOK && rightOK
	default:
		return "", false
	}
}
