package deploy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// Everything the app records about how to build it has to reach the build.
//
// This is the shape of bug this repository keeps finding: a value is worked
// out, stored, shown in the interface, and then dropped on the floor one layer
// before it is used. StaticDir was the last one — detected from the
// repository, offered in the form, saved on the app, and never once read by
// the thing that builds the image, so every static build served the
// repository's source instead of what a build would have produced. Nothing
// failed; the page was simply blank.
//
// The rule: every field that exists on both store.App and builder.JobSpec has
// to be assigned where the JobSpec is built. A field on one and not the other
// is none of this test's business.
func TestEveryBuildSettingOnTheAppReachesTheBuild(t *testing.T) {
	root := repoRoot(t)

	appFields := structFields(t, filepath.Join(root, "internal", "store", "models.go"), "App")
	specFields := structFields(t, filepath.Join(root, "internal", "builder", "job.go"), "JobSpec")
	assigned := jobSpecLiteralFields(t, filepath.Join(root, "internal", "deploy", "build.go"))

	if len(appFields) < 10 || len(specFields) < 10 || len(assigned) < 5 {
		t.Fatalf("this test is not reading the source: %d app fields, %d spec fields, %d assigned",
			len(appFields), len(specFields), len(assigned))
	}

	// A field can be on both and mean two different things. Each one here
	// needs a reason, so that "it is exempt" is a decision somebody wrote down
	// rather than a name that happened to match.
	differentMeaning := map[string]string{
		"CPURequestM":  "the app's runtime CPU, not the build pod's",
		"MemRequestMB": "the app's runtime memory, not the build pod's",
		"MemLimitMB": "a 128 MB app can need 3 GB to build, which is why the " +
			"build has a limit of its own",
	}

	shared := 0
	for name := range specFields {
		if !appFields[name] {
			continue
		}
		if reason, exempt := differentMeaning[name]; exempt {
			if assigned[name] {
				t.Errorf("%s is exempt because %s, and it is being assigned anyway", name, reason)
			}
			continue
		}
		shared++
		if !assigned[name] {
			t.Errorf("store.App and builder.JobSpec both have %s, and the build never reads it: "+
				"assign it where the JobSpec is built in internal/deploy/build.go", name)
		}
	}
	if shared < 5 {
		t.Fatalf("only %d fields are shared; this test is not comparing what it thinks", shared)
	}
	t.Logf("%d build settings on the app, all carried into the build", shared)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the repository root")
	return ""
}

// structFields returns the field names of one struct in one file.
func structFields(t *testing.T, path, name string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != name {
			return true
		}
		structType, ok := spec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, field := range structType.Fields.List {
			for _, ident := range field.Names {
				out[ident.Name] = true
			}
		}
		return false
	})
	if len(out) == 0 {
		t.Fatalf("no fields were read from %s in %s", name, path)
	}
	return out
}

// jobSpecLiteralFields returns the fields set in the builder.JobSpec literal,
// including any assigned to the variable afterwards.
func jobSpecLiteralFields(t *testing.T, path string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			sel, ok := node.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "JobSpec" {
				return true
			}
			for _, element := range node.Elts {
				kv, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok {
					out[key.Name] = true
				}
			}
		case *ast.AssignStmt:
			// spec.CloneSecret = ... counts too: a field set after the literal
			// is just as delivered as one set inside it.
			for _, target := range node.Lhs {
				if sel, ok := target.(*ast.SelectorExpr); ok {
					out[sel.Sel.Name] = true
				}
			}
		}
		return true
	})
	return out
}
