package kube

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// BuildInputs is what saving an app's settings reads to say which changes wait
// for the next deploy, and what a deploy records it built with. If it fell
// behind the fingerprint, a setting that goes into the image would be said to
// have rolled out, and a deploy that rebuilt for it would look like it did
// nothing. So every parameter of BuildFingerprint is named by an entry, and
// every entry names a parameter there is, read from the source itself.
func TestEveryBuildFingerprintParameterIsABuildInput(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "spec.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var parameters []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "BuildFingerprint" {
			continue
		}
		for _, field := range function.Type.Params.List {
			for _, name := range field.Names {
				parameters = append(parameters, name.Name)
			}
		}
	}
	if len(parameters) < 5 {
		t.Fatalf("found the parameters %v; this is not reading BuildFingerprint", parameters)
	}

	named := map[string]bool{}
	settings := map[string]bool{}
	for _, input := range BuildInputs {
		if settings[input.Setting] {
			t.Errorf("%s is in BuildInputs twice", input.Setting)
		}
		settings[input.Setting] = true
		if input.Parameter == "" {
			// The one setting with no parameter: an image app's image is its
			// fingerprint, and nothing is built.
			if input.Setting != "image" {
				t.Errorf("%s names no BuildFingerprint parameter", input.Setting)
			}
			continue
		}
		if !slices.Contains(parameters, input.Parameter) {
			t.Errorf("%s says it reaches the build as %s, which BuildFingerprint does not take", input.Setting, input.Parameter)
		}
		named[input.Parameter] = true
	}
	for _, parameter := range parameters {
		if !named[parameter] {
			t.Errorf("BuildFingerprint takes %s, and no entry in BuildInputs says which setting it comes from", parameter)
		}
	}
	if !IsBuildInput("build_command") || IsBuildInput("port") || IsBuildInput("start_command") {
		t.Error("IsBuildInput does not read BuildInputs")
	}
}
