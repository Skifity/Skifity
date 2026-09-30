package api_test

import (
	"testing"

	"skifity/internal/dbsvc"
	"skifity/internal/templates"
)

// The catalogue names engines as strings, and internal/dbsvc decides which
// engines exist. Neither package can import the other — dbsvc imports
// internal/api, and internal/api imports templates — so this file is an
// external test package, which can import both without a cycle.
//
// A template naming an engine nobody provisions fails at install time, after
// its apps have been created, and leaves half a template behind.
func TestEveryTemplateAsksForAnEngineSkifityProvisions(t *testing.T) {
	provisioned := map[string]bool{}
	for engine := range dbsvc.DefaultVersions {
		provisioned[engine] = true
	}
	for _, tpl := range templates.All() {
		for _, db := range tpl.Databases {
			if _, ok := dbsvc.DefaultVersions[db.Engine]; !ok {
				t.Errorf("the %s template asks for a %q database, which Skifity does not provision",
					tpl.ID, db.Engine)
			}
		}
	}

	// templates.Engines is what a team's own catalogue is checked against
	// when it is read, so it has to be exactly the engines that exist: one
	// more is a template that fails after its apps were created, one fewer
	// is a good template refused.
	for engine := range templates.Engines {
		if !provisioned[engine] {
			t.Errorf("templates.Engines accepts %q, which Skifity does not provision", engine)
		}
	}
	for engine := range provisioned {
		if !templates.Engines[engine] {
			t.Errorf("Skifity provisions %q and templates.Engines refuses it", engine)
		}
	}
}
