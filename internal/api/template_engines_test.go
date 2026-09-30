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
	provisioned := map[string]bool{
		dbsvc.EnginePostgres: true,
		dbsvc.EngineMySQL:    true,
		dbsvc.EngineRedis:    true,
	}
	for _, tpl := range templates.All() {
		for _, db := range tpl.Databases {
			if !provisioned[db.Engine] {
				t.Errorf("the %s template asks for a %q database, which Skifity does not provision",
					tpl.ID, db.Engine)
			}
		}
	}
}
