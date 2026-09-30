package builder_test

import (
	"testing"

	"skifity/internal/builder"
	"skifity/internal/dbsvc"
)

// The form offers to create what this panel can make and nothing else.
//
// The two lists live in different packages — detection cannot import the
// database service, which reaches it through the API — so this test, from
// outside both, is what keeps them the same. An engine offered and not makeable
// is a checkbox that fails after somebody ticked it; one makeable and never
// offered is a feature nobody finds.
func TestEveryProvidedEngineIsOneThePanelCanCreate(t *testing.T) {
	offered := map[string]bool{}
	for _, engine := range builder.ProvidedEngines {
		offered[engine] = true
		if _, ok := dbsvc.DefaultVersions[engine]; !ok {
			t.Errorf("%q is offered to be created and internal/dbsvc cannot make it", engine)
		}
	}
	for engine := range dbsvc.DefaultVersions {
		if !offered[engine] {
			t.Errorf("internal/dbsvc can make %q and detection never offers it", engine)
		}
	}
}
