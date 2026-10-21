package errdoc

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEveryErrorHasItsWordsInTheInterface
//
// The server writes each failure once, in English: it has one language, and the
// API, the CLI and an assistant all read those strings. The panel has five and
// looks them up by the error's own code, falling back to the English when it
// finds nothing — which is what every error did in every language until this
// test existed. The settings page was the same until Phase 55.
//
// The codes are not in a list anywhere: they are the first argument to a
// hundred-odd errdoc.New calls spread across thirty files. So this reads them
// out of the source, which also means a new error cannot be added without a
// translation, and a code renamed in Go fails here rather than silently going
// back to English.
func TestEveryErrorHasItsWordsInTheInterface(t *testing.T) {
	problems := problemsInSource(t)
	if len(problems) < 100 {
		t.Fatalf("only found %d errdoc.New calls, so this test has stopped reading the source", len(problems))
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
			Errors struct {
				Catalogue map[string]map[string]string `json:"catalogue"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(body, &locale); err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		language := strings.TrimSuffix(filepath.Base(path), ".json")

		for _, problem := range problems {
			// i18next reads a dot as a level of nesting, so the code is written
			// the way the settings page writes its keys.
			key := strings.ReplaceAll(problem.Code, ".", "_")
			entry, ok := locale.Errors.Catalogue[key]
			if !ok {
				t.Errorf("%s: the error %q has no words: add errors.catalogue.%s",
					language, problem.Code, key)
				continue
			}
			for _, field := range []string{"title", "cause", "impact", "fix"} {
				if problem.Fields[field] == "" {
					continue
				}
				if strings.TrimSpace(entry[field]) == "" {
					t.Errorf("%s: errors.catalogue.%s.%s is missing, and the English says %q",
						language, key, field, truncate(problem.Fields[field]))
				}
			}
		}

		// A key nobody can reach is dead weight, and usually a code that was
		// renamed in Go and not here.
		//
		// PreflightFailed is the one code built at run time — "preflight." plus
		// the preflight problem's own code — so it cannot be read out of the
		// source here. TestEveryPreflightProblemAndStepMessageHasItsWords, in
		// internal/provision, enumerates those from where they are declared and
		// checks the same locales.
		for key := range locale.Errors.Catalogue {
			if strings.HasPrefix(key, "preflight_") || raisedByTheInterface[key] {
				continue
			}
			if _, ok := problems[strings.ReplaceAll(key, "_", ".")]; !ok {
				if !reachable(problems, key) {
					t.Errorf("%s: errors.catalogue.%s is not an error any code can raise", language, key)
				}
			}
		}
	}
}

// TestOneCodeSaysOneThing
//
// The interface has one translation per code. A code raised from two places
// with two different sentences is shown, in every language but English, with
// whichever one the locale happens to hold: "internal" was raised by From, by
// the request panic handler, by a deployment and by a server operation, and
// every uncatalogued failure — a SQLite error saving a variable — was shown as
// a server left half-configured, with the real cause only in the copied text.
//
// A second place with something different to say gets a code of its own; two
// places that mean the same thing share one constructor in the catalogue.
// There is no list of exceptions, so a new one cannot be waved through.
func TestOneCodeSaysOneThing(t *testing.T) {
	problems := problemsInSource(t)
	for code, problem := range problems {
		for _, field := range []string{"title", "cause", "impact", "fix"} {
			sentences := problem.Said[field]
			distinct := 0
			for sentence := range sentences {
				// What is built at run time cannot be compared from here.
				if sentence != "(built at run time)" {
					distinct++
				}
			}
			if distinct < 2 {
				continue
			}
			var said []string
			for sentence, where := range sentences {
				said = append(said, fmt.Sprintf("%q at %s", truncate(sentence), strings.Join(where, ", ")))
			}
			sort.Strings(said)
			t.Errorf("%s is raised with %d different sentences for its %s, and the interface can translate only one: %s. "+
				"Give each its own code, with its own words in the locales.",
				code, distinct, field, strings.Join(said, "; "))
		}
	}
}

// raisedByTheInterface are the codes the interface makes itself, for what the
// panel never answered, all in web/src/lib/api.ts and
// web/src/components/error-display.tsx: a response that was not the panel's
// JSON — a 502 while it restarts — a request the browser could not send at
// all, and anything else thrown in the browser. No Go raises them.
var raisedByTheInterface = map[string]bool{"network": true, "network_unreachable": true, "unexpected": true}

// reachable reports whether a locale key belongs to one of the codes found.
// The mapping is one-way — a code may contain an underscore of its own — so
// this compares the way the interface builds the key.
func reachable(problems map[string]problemInSource, key string) bool {
	for code := range problems {
		if strings.ReplaceAll(code, ".", "_") == key {
			return true
		}
	}
	return false
}

func truncate(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[:57] + "..."
}

type problemInSource struct {
	Code   string
	Fields map[string]string
	// Said holds every sentence a field was given, with where, for a code
	// raised from more than one place.
	Said map[string]map[string][]string
}

// problemsInSource reads every errdoc.New call in the repository, with the
// format strings of the sentences chained onto it.
func problemsInSource(t *testing.T) map[string]problemInSource {
	t.Helper()
	found := map[string]problemInSource{}
	fset := token.NewFileSet()

	root := filepath.Join("..", "..")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Vendored and generated trees have no errors of ours in them.
			// Nor does a directory Go itself ignores, one whose name starts
			// with a dot: a git worktree checked out under .claude/ is another
			// copy of this repository, with its own locales.
			name := info.Name()
			if name == "node_modules" || name == "dist" || (path != root && strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// A file this cannot parse is not one of ours to fail on: the walk
		// covers the whole repository, including generated and vendored trees.
		// Skipping it is the point, so the error is dropped on purpose.
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil //nolint:nilerr // skipping an unparsable file is deliberate
		}
		inPackage := strings.Contains(filepath.ToSlash(path), "internal/errdoc/")

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 || !isErrdocNew(call.Fun, inPackage) {
				return true
			}
			code, ok := literal(call.Args[0])
			if !ok {
				return true
			}
			// Newf carries its values separately, so its title is a literal
			// too. Anything else assembled from a variable still needs words
			// in the interface, and saying so beats skipping it silently —
			// which is how four errors stayed out of the catalogue and out of
			// this test's own list of what was missing.
			title, ok := literal(call.Args[1])
			if !ok {
				title = "(built at run time)"
			}
			entry, seen := found[code]
			if !seen {
				entry = problemInSource{Code: code, Fields: map[string]string{}, Said: map[string]map[string][]string{}}
			}
			fields := map[string]string{"title": title}
			collectSentences(file, call, fields)
			// The same code raised from two places keeps whichever sentences
			// either of them sets, which is what the interface has to cover.
			// What each of them said is kept too, for the test below.
			where := fset.Position(call.Pos()).String()
			for field, sentence := range fields {
				entry.Fields[field] = sentence
				if entry.Said[field] == nil {
					entry.Said[field] = map[string][]string{}
				}
				entry.Said[field][sentence] = append(entry.Said[field][sentence], where)
			}
			found[code] = entry
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("read the source: %v", err)
	}
	return found
}

func isErrdocNew(fun ast.Expr, inPackage bool) bool {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		return ok && pkg.Name == "errdoc" && (f.Sel.Name == "New" || f.Sel.Name == "Newf")
	case *ast.Ident:
		return inPackage && (f.Name == "New" || f.Name == "Newf")
	}
	return false
}

// collectSentences finds the With* calls chained onto one New call.
func collectSentences(file *ast.File, root *ast.CallExpr, fields map[string]string) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		var field string
		switch sel.Sel.Name {
		case "WithCause":
			field = "cause"
		case "WithImpact":
			field = "impact"
		case "WithFix":
			field = "fix"
		default:
			return true
		}
		if !chainedFrom(sel.X, root) {
			return true
		}
		if format, ok := literal(call.Args[0]); ok {
			fields[field] = format
		} else {
			// A sentence built from a constant still needs translating; the
			// English is not readable from here, so say that much.
			fields[field] = "(built at run time)"
		}
		return true
	})
}

// chainedFrom reports whether an expression is a chain of method calls whose
// root is this New call.
func chainedFrom(e ast.Expr, root *ast.CallExpr) bool {
	for {
		if e == root {
			return true
		}
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		e = sel.X
	}
}

// literal reads a string literal, including one written as "a" + "b".
func literal(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		left, ok1 := literal(v.X)
		right, ok2 := literal(v.Y)
		return left + right, ok1 && ok2
	case *ast.ParenExpr:
		return literal(v.X)
	}
	return "", false
}

var _ = sort.Strings
