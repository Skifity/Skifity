package plugins

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Every kind a plugin may provide has to be one the panel actually asks for.
//
// The same gate as TestEveryDeclaredEventIsActuallySent, pointed at the other
// half of the standard, and for a sharper reason. An event nobody sends is a
// plugin that never hears. A kind nobody asks for is worse: the manifest
// accepts it, the install screen lists it, an administrator agrees to it, and
// the plugin's container sits there for ever waiting for a request that no
// line of code anywhere makes. The author's only way to find out is to install
// it and watch nothing happen.
//
// It is also the check that keeps this vocabulary honest as it grows. Adding a
// kind is one line here and very tempting — a roadmap written in constants.
// This test makes it impossible: a kind that is not yet wired to anything
// fails the build, so the list can only ever say what is true today.
//
// So: for each kind in ProviderKinds, some file outside this package has to
// name its constant. That is not proof the call is in the right place, but it
// is proof that a caller exists.
func TestEveryProviderKindIsActuallyAskedFor(t *testing.T) {
	root := repoRoot(t)
	constants := prefixedConstants(t, filepath.Join(root, "provider.go"), "Provider")
	if len(constants) < len(ProviderKinds) {
		t.Fatalf("read %d provider constants for %d kinds; this test is not reading provider.go",
			len(constants), len(ProviderKinds))
	}

	used := map[string]bool{}
	callers := 0
	// Every package that could reasonably ask a plugin for something. A kind
	// wired somewhere not listed here is a kind this test cannot see, which is
	// why the count below is checked as well.
	for _, dir := range []string{"api", "backup", "cluster", "dbsvc", "deploy",
		"notify", "provision", "serverapp", "watch"} {
		walkGo(t, filepath.Join(root, "..", dir), func(_ string, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "plugins" {
					return true
				}
				if value, known := constants[sel.Sel.Name]; known {
					used[value] = true
					callers++
				}
				return true
			})
		})
	}

	if callers == 0 {
		t.Fatal("nothing in the panel names a provider kind; this test is not reading the repository")
	}
	for _, kind := range ProviderKinds {
		if !used[kind] {
			t.Errorf("the standard says a plugin can provide %q and nothing in the panel ever asks for it: "+
				"a plugin declaring it is installed, approved, and never called", kind)
		}
	}
	if !t.Failed() {
		t.Logf("%d provider kinds, each asked for from somewhere", len(ProviderKinds))
	}
}

// An action listed against a kind has to be one the panel really performs.
//
// The finer-grained half of the same promise. A kind can be wired for one of
// its actions and not the others, and then "a notify.channel is asked to
// validate and to send" is half true: a plugin author implements validate,
// nothing ever calls it, and a bad configuration is stored anyway.
func TestEveryProviderActionIsActuallyPerformed(t *testing.T) {
	root := repoRoot(t)
	constants := prefixedConstants(t, filepath.Join(root, "provider.go"), "Action")
	if len(constants) == 0 {
		t.Fatal("no action constants were read; this test is not reading provider.go")
	}

	used := map[string]bool{}
	for _, dir := range []string{"api", "backup", "cluster", "dbsvc", "deploy",
		"notify", "provision", "serverapp", "watch"} {
		walkGo(t, filepath.Join(root, "..", dir), func(_ string, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "plugins" {
					return true
				}
				if value, known := constants[sel.Sel.Name]; known {
					used[value] = true
				}
				return true
			})
		})
	}

	for kind, actions := range ProviderActions {
		for _, action := range actions {
			if !used[action] {
				t.Errorf("a %s is said to be asked to %q and nothing in the panel ever asks it: "+
					"a plugin author implements that and it is never called", kind, action)
			}
		}
	}
}

// Nothing may be listed as an action of a kind that is not a kind, which would
// be a plugin implementing something against a vocabulary entry that cannot
// exist.
func TestNoActionIsListedForAKindThatIsNotOne(t *testing.T) {
	for kind := range ProviderActions {
		if !KnownProviderKind(kind) {
			t.Errorf("%q has actions listed and is not a kind a plugin may provide", kind)
		}
	}
}

// prefixedConstants maps the Go constant name to the string it holds, for the
// constants in a file whose names start with prefix.
func prefixedConstants(t *testing.T, path, prefix string) map[string]string {
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
			if !strings.HasPrefix(name.Name, prefix) || i >= len(spec.Values) {
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
