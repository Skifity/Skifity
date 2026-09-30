package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// builtFrontend is the shape `npm run build` leaves in web/dist: an entry
// point and fingerprinted chunks under assets/. The tests serve this rather
// than the embedded build, which only exists after the frontend is built.
func builtFrontend() http.Handler {
	return filesHandler(fstest.MapFS{
		"index.html":             {Data: []byte("<!doctype html><div id=root></div>")},
		"assets/index-AbC123.js": {Data: []byte("console.log(1)")},
		"favicon.svg":            {Data: []byte("<svg/>")},
	})
}

// A missing file must be a 404, and a route must be the app.
//
// Falling back to index.html for everything is the usual shortcut and it hurts
// where it is hardest to diagnose: a browser holding a cached page asks for a
// chunk a new deploy has renamed, gets HTML with a 200, and the JavaScript
// parser fails on `<!doctype html>`. What the person sees is a white page and a
// syntax error in a file they did not write.
func TestAMissingFileIsNotTheSinglePageApp(t *testing.T) {
	handler := builtFrontend()

	for _, path := range []string{
		"/assets/index-DEADBEEF.js",
		"/assets/style-0000.css",
		"/nothing.js",
		"/deep/path/missing.css",
		"/missing.svg",
		"/missing.json",
	} {
		response := get(t, handler, path)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s answered %d with %q, want 404 — a browser asking for a "+
				"renamed chunk must be told it is gone, not handed HTML",
				path, response.Code, response.Header().Get("Content-Type"))
		}
	}

	// A route inside the app has no extension, and reloading on one has to
	// work: that is what the fallback is for.
	for _, path := range []string{
		"/",
		"/apps/app_06gax1hb07nr6yx4edmn",
		"/settings",
		"/projects/proj_1/environments",
	} {
		response := get(t, handler, path)
		if response.Code != http.StatusOK {
			t.Errorf("GET %s answered %d, want the app", path, response.Code)
		}
		if !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
			t.Errorf("GET %s answered %q, want HTML", path, response.Header().Get("Content-Type"))
		}
	}
}

// TestAFingerprintedAssetIsCachedAndTheEntryPointIsNot: an asset's name
// contains its own hash, so it can be cached forever; index.html must not be,
// or an upgrade would not take effect until every browser gave up its copy.
func TestAFingerprintedAssetIsCachedAndTheEntryPointIsNot(t *testing.T) {
	handler := builtFrontend()

	if cache := get(t, handler, "/").Header().Get("Cache-Control"); !strings.Contains(cache, "no-cache") {
		t.Errorf("index.html is served with %q; an upgrade would not take effect", cache)
	}
	if cache := get(t, handler, "/assets/index-AbC123.js").Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Errorf("a fingerprinted chunk is served with %q, want it cached hard: the name contains its hash", cache)
	}
	if response := get(t, handler, "/favicon.svg"); response.Code != http.StatusOK {
		t.Errorf("a file that exists answered %d", response.Code)
	}
}

// Without a build the binary still starts and says what is missing, rather
// than failing to compile or serving an empty page with a 200.
func TestABinaryWithNoFrontendSaysSo(t *testing.T) {
	response := get(t, filesHandler(fstest.MapFS{}), "/")
	if response.Code != http.StatusInternalServerError ||
		!strings.Contains(response.Body.String(), "not available in this build") {
		t.Errorf("with no frontend, / answered %d %q", response.Code, response.Body.String())
	}
}

func get(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
