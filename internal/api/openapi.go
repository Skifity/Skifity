package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sync"

	"sigs.k8s.io/yaml"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// The API, described for machines.
//
// openapi.yaml is written by hand, beside the router it describes, rather than
// generated from the handlers. A generator reads Go types; it cannot say what a
// route is for, which of its answers is a stream, or that a token is refused
// where a person is wanted — which is most of what somebody writing a client
// needs to know. What keeps a hand-written description true is openapi_test.go:
// it walks the router and fails on every route the file does not describe, and
// on every route the file describes that nothing answers.

//go:embed openapi.yaml
var openAPISource []byte

var (
	openAPIOnce sync.Once
	openAPIBody []byte
	openAPIErr  error
)

// openAPIDocument is the description as JSON, converted once. YAML is what a
// person edits and JSON is what every client generator reads, so the panel
// serves the form it does not keep.
//
// The version is this binary's own. The file cannot know it, and a description
// that says which panel it describes is what lets somebody tell whether the
// client they generated last month still matches.
func openAPIDocument() ([]byte, error) {
	openAPIOnce.Do(func() {
		var document map[string]any
		if err := yaml.Unmarshal(openAPISource, &document); err != nil {
			openAPIErr = err
			return
		}
		if info, ok := document["info"].(map[string]any); ok {
			info["version"] = version.Version
		}
		openAPIBody, openAPIErr = json.Marshal(document)
	})
	return openAPIBody, openAPIErr
}

// handleOpenAPI serves the description.
//
// Open, like /api/meta. It names the routes and the shapes of their answers,
// which is what the source code says to anybody who reads it, and none of any
// team's data. A client generated before there is an account to sign in with
// has no token to present.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	body, err := openAPIDocument()
	if err != nil {
		writeError(w, r, errdoc.APIDescriptionUnreadable(err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
