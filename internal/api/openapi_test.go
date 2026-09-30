package api

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"sigs.k8s.io/yaml"

	"skifity/internal/version"
)

// The OpenAPI description is written by hand, so these tests are what keep it
// true. A description that has drifted is worse than none: a client generated
// from it calls a route that moved, and the person debugging that trusts the
// document over the panel.

// operationMethods are the keys of an OpenAPI path item that are operations.
var operationMethods = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}

// TestTheAPIDescriptionCoversEveryRoute walks the router the panel serves and
// holds the description to it in both directions: every route is described,
// and everything described is a route. A route added next month fails here
// until somebody describes it, which is the only way a hand-kept file stays
// complete.
func TestTheAPIDescriptionCoversEveryRoute(t *testing.T) {
	document := readOpenAPI(t)
	described := map[string]bool{}
	for route := range operations(t, document) {
		described[route] = true
	}

	served := map[string]bool{}
	h := newHarness(t)
	err := chi.Walk(h.api.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(pattern, "/api/") {
			return nil // the frontend and /docs are pages, not the API
		}
		// chi spells a route's own root "/apps/{appID}/"; the description,
		// and every client, call it without the slash, and chi answers both.
		served[method+" "+strings.TrimSuffix(pattern, "/")] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	if len(served) < 150 {
		t.Fatalf("the router reported %d routes under /api, which cannot be all of them", len(served))
	}

	for _, route := range slices.Sorted(maps.Keys(served)) {
		if !described[route] {
			t.Errorf("%s is served, and internal/api/openapi.yaml does not describe it: "+
				"add it under paths, with the {parameter} names the router uses", route)
		}
	}
	for _, route := range slices.Sorted(maps.Keys(described)) {
		if !served[route] {
			t.Errorf("internal/api/openapi.yaml describes %s, and nothing answers it: "+
				"correct its method or path, or remove it", route)
		}
	}
	if !t.Failed() {
		t.Logf("%d routes, and the description has every one of them", len(served))
	}
}

// TestTheAPIDescriptionIsWellFormed checks what a client generator would trip
// over: a reference to nothing, a path parameter that is not declared, two
// operations with one name, an operation with no answer.
func TestTheAPIDescriptionIsWellFormed(t *testing.T) {
	document := readOpenAPI(t)

	if v, _ := document["openapi"].(string); !strings.HasPrefix(v, "3.1.") {
		t.Errorf("openapi is %q; the description is written to OpenAPI 3.1", v)
	}

	// Every $ref, wherever it is, names something in this document.
	refs := 0
	walkRefs(document, func(ref string) {
		refs++
		if _, ok := resolvePointer(document, ref); !ok {
			t.Errorf("$ref %q does not resolve to anything in the description", ref)
		}
	})
	if refs < 100 {
		t.Errorf("only %d references were found, which means this is not reading the file", refs)
	}

	tags := map[string]bool{}
	for _, raw := range asList(document["tags"]) {
		if name, _ := asMap(raw)["name"].(string); name != "" {
			tags[name] = true
		}
	}
	schemes := asMap(asMap(document["components"])["securitySchemes"])

	ids := map[string]string{}
	for route, op := range operations(t, document) {
		id, _ := op.operation["operationId"].(string)
		switch {
		case id == "":
			t.Errorf("%s has no operationId", route)
		case ids[id] != "":
			t.Errorf("%s and %s are both called %q; an operationId names one operation", ids[id], route, id)
		default:
			ids[id] = route
		}
		if summary, _ := op.operation["summary"].(string); strings.TrimSpace(summary) == "" {
			t.Errorf("%s has no summary", route)
		}
		opTags := asList(op.operation["tags"])
		if len(opTags) == 0 {
			t.Errorf("%s has no tag, so it is listed under nothing", route)
		}
		for _, tag := range opTags {
			if name, _ := tag.(string); !tags[name] {
				t.Errorf("%s is tagged %v, which is not one of the tags the description declares", route, tag)
			}
		}

		// Every operation says what a failure looks like, and names at least
		// one answer of its own. That is usually a 2xx; a switch of protocols,
		// a redirect, and the 405 a stateless MCP server gives a GET are the
		// answers three of these routes exist to give.
		responses := asMap(op.operation["responses"])
		if _, ok := responses["default"]; !ok {
			t.Errorf("%s does not say what a failure looks like: give it default: $ref Error", route)
		}
		if len(responses) < 2 {
			t.Errorf("%s does not say what it answers when it works", route)
		}

		for _, requirement := range asList(op.operation["security"]) {
			for name := range asMap(requirement) {
				if _, ok := schemes[name]; !ok {
					t.Errorf("%s names the security scheme %q, which is not declared", route, name)
				}
			}
		}

		// The path's {parameters} and the declared ones are the same set: a
		// generator makes a function argument of each, and one missing from
		// either side is a client that cannot build the URL.
		inPath := map[string]bool{}
		for _, match := range placeholder.FindAllString(op.path, -1) {
			inPath[strings.Trim(match, "{}")] = true
		}
		declared := map[string]bool{}
		for _, raw := range append(asList(op.item["parameters"]), asList(op.operation["parameters"])...) {
			parameter := asMap(raw)
			if ref, ok := parameter["$ref"].(string); ok {
				resolved, _ := resolvePointer(document, ref)
				parameter = asMap(resolved)
			}
			if parameter["in"] == "path" {
				name, _ := parameter["name"].(string)
				declared[name] = true
				if parameter["required"] != true {
					t.Errorf("%s declares the path parameter %s without required: true", route, name)
				}
			}
		}
		for name := range inPath {
			if !declared[name] {
				t.Errorf("%s has {%s} in its path and does not declare it as a parameter", route, name)
			}
		}
		for name := range declared {
			if !inPath[name] {
				t.Errorf("%s declares the path parameter %s, which is not in its path", route, name)
			}
		}
	}
}

// TestTheAPIDescriptionSaysWhichRoutesAreOpen holds the description's
// `security: []` to openOnPurpose, which is itself held to the router. A route
// described as open that asks for a token sends a generated client out without
// one; a route described as closed that is open hides the one thing somebody
// with no account can do.
func TestTheAPIDescriptionSaysWhichRoutesAreOpen(t *testing.T) {
	document := readOpenAPI(t)
	for route, op := range operations(t, document) {
		security, overridden := op.operation["security"]
		open := overridden && len(asList(security)) == 0
		_, onPurpose := openOnPurpose[route]
		switch {
		case onPurpose && !open:
			t.Errorf("%s answers without credentials (openroutes_test.go), and the description says it needs them: give it security: []", route)
		case open && !onPurpose:
			t.Errorf("%s is described as needing no credentials, and the panel refuses it without them", route)
		}
	}
}

// TestTheAPIDescriptionIsServed asks for it the way a client generator would:
// with no credentials, as JSON, and with this binary's version in it.
func TestTheAPIDescriptionIsServed(t *testing.T) {
	h := newHarness(t)
	h.newTenant("acme") // past first-run setup, as a panel spends its life

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.server.URL+"/api/openapi.json", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /api/openapi.json: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/openapi.json answered %d with no credentials, want 200.\n%s", resp.StatusCode, truncate(string(body), 300))
	}
	if kind := resp.Header.Get("Content-Type"); !strings.HasPrefix(kind, "application/json") {
		t.Errorf("it is served as %q, want application/json", kind)
	}

	var served map[string]any
	if err := json.Unmarshal(body, &served); err != nil {
		t.Fatalf("the description served is not JSON: %v", err)
	}
	if got := asMap(served["info"])["version"]; got != version.Version {
		t.Errorf("the description says it is version %v, and this binary is %s", got, version.Version)
	}
	if got, want := len(asMap(served["paths"])), len(asMap(readOpenAPI(t)["paths"])); got != want {
		t.Errorf("the description served has %d paths, and openapi.yaml has %d", got, want)
	}
}

// describedOperation is one operation, with the path item around it: path
// parameters are usually declared on the item rather than on each method.
type describedOperation struct {
	path      string
	item      map[string]any
	operation map[string]any
}

// operations lists every operation the description has, keyed as the router
// is walked: "GET /api/apps/{appID}".
func operations(t *testing.T, document map[string]any) map[string]describedOperation {
	t.Helper()
	paths := asMap(document["paths"])
	if len(paths) == 0 {
		t.Fatal("the description has no paths")
	}
	out := map[string]describedOperation{}
	for path, raw := range paths {
		item := asMap(raw)
		for _, method := range operationMethods {
			if operation, ok := item[method]; ok {
				out[strings.ToUpper(method)+" "+path] = describedOperation{
					path: path, item: item, operation: asMap(operation),
				}
			}
		}
	}
	return out
}

// readOpenAPI parses the embedded file the way the panel does.
func readOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(openAPISource, &document); err != nil {
		t.Fatalf("internal/api/openapi.yaml does not parse: %v", err)
	}
	return document
}

// walkRefs calls visit with every $ref in a parsed document.
func walkRefs(node any, visit func(string)) {
	switch value := node.(type) {
	case map[string]any:
		if ref, ok := value["$ref"].(string); ok {
			visit(ref)
		}
		for _, child := range value {
			walkRefs(child, visit)
		}
	case []any:
		for _, child := range value {
			walkRefs(child, visit)
		}
	}
}

// pointerEscape undoes JSON Pointer's two escapes, in the order RFC 6901 says.
var pointerEscape = strings.NewReplacer("~1", "/", "~0", "~")

// resolvePointer follows a local reference such as #/components/schemas/App.
// A reference to another file is not something this description uses, so it
// does not resolve.
func resolvePointer(document map[string]any, ref string) (any, bool) {
	pointer, local := strings.CutPrefix(ref, "#/")
	if !local {
		return nil, false
	}
	var node any = document
	for _, segment := range strings.Split(pointer, "/") {
		next, ok := asMap(node)[pointerEscape.Replace(segment)]
		if !ok {
			return nil, false
		}
		node = next
	}
	return node, true
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}
