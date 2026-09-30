package templates

import "testing"

// What a template can ask of the engine beyond an image and variables: a
// command, files and a database in pieces. Each is checked here, before an
// install finds out.

func TestEveryTemplateFileCanBeMounted(t *testing.T) {
	report(t, CheckFiles)
}

// A database's pieces arrive as variables, so each needs a name a container
// can carry, and two pieces under one name would leave one of them missing.
func TestEveryDatabasePieceArrivesAsAVariable(t *testing.T) {
	report(t, CheckDatabasePieces)
}
