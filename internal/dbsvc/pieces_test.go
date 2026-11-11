package dbsvc

import (
	"context"
	"slices"
	"testing"

	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/templates"
)

// An app installed from a template before a link kept its pieces has them
// as plain variables the link knows nothing about. The step at startup gives
// the link those pieces, once, so the next password change reaches them.
func TestATemplateAppsPiecesAreAttachedToItsLinkOnce(t *testing.T) {
	m, db, app, orders, _ := linkHarness(t)
	ctx := t.Context()
	deployer := &syncRecorder{fail: map[string]bool{}}
	m.deployer = deployer

	worker := store.App{EnvironmentID: app.EnvironmentID, Name: "worker", Slug: "worker", Replicas: 1}
	if err := db.CreateApp(ctx, &worker); err != nil {
		t.Fatal(err)
	}
	tpl := templates.Template{ID: "notes", Databases: []templates.DatabaseSpec{{
		Name: "notes-db", Engine: EnginePostgres, LinkTo: []string{"web", "worker"}, VarName: "NOTES_DATABASE_URL",
		Vars: templates.DatabaseVars{Host: "DB_HOST", Port: "DB_PORT", Name: "DB_NAME", User: "DB_USER", Password: "DB_PASSWORD"},
	}}}
	// As an install used to leave them: the link, and the pieces beside it,
	// the password one holding a password the database has since lost.
	for _, installed := range []struct {
		app     store.App
		service string
		pieces  map[string]string
	}{
		{app, "web", map[string]string{"DB_HOST": "orders-rw", "DB_PORT": "5432", "DB_NAME": "app", "DB_USER": "app", "DB_PASSWORD": "a-password-it-no-longer-has"}},
		// Somebody removed one of the worker's: it is left as it is.
		{worker, "worker", map[string]string{"DB_HOST": "orders-rw", "DB_PORT": "5432", "DB_NAME": "app", "DB_USER": "app"}},
	} {
		if err := db.RecordAppTemplate(ctx, store.AppTemplate{AppID: installed.app.ID, TemplateID: "notes", Service: installed.service}); err != nil {
			t.Fatal(err)
		}
		if err := m.Link(ctx, orders.ID, installed.app.ID, "NOTES_DATABASE_URL", nil); err != nil {
			t.Fatal(err)
		}
		for key, value := range installed.pieces {
			sealed, err := m.keyring.Seal([]byte(value), "variable:"+installed.app.ID+":"+key)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.SetVariable(ctx, &store.Variable{AppID: installed.app.ID, Key: key, IsSecret: key == "DB_PASSWORD"}, sealed); err != nil {
				t.Fatal(err)
			}
		}
	}
	deployer.synced = nil
	find := func(_ context.Context, installed store.AppTemplate) (templates.Template, bool) {
		return tpl, installed.TemplateID == "notes"
	}

	attached, err := m.AttachTemplatePieces(ctx, find)
	if err != nil || attached != 1 {
		t.Fatalf("attached %d (%v), want the web app's one link", attached, err)
	}
	links, _ := db.ListLinksForApp(ctx, app.ID)
	want := store.LinkPieces{Host: "DB_HOST", Port: "DB_PORT", Name: "DB_NAME", User: "DB_USER", Password: "DB_PASSWORD"}
	if len(links) != 1 || links[0].Pieces != want {
		t.Fatalf("the web app's link is %+v", links)
	}
	if values, _ := variableValues(t, m, app.ID); values["DB_PASSWORD"] != "pw-of-orders" {
		t.Errorf("the stale password piece is still %q", values["DB_PASSWORD"])
	}
	if synced := deployer.all(); !slices.Equal(synced, []string{app.ID}) {
		t.Errorf("rolled out %v, want the web app, whose password was wrong", synced)
	}
	if links, _ := db.ListLinksForApp(ctx, worker.ID); len(links) != 1 || len(links[0].Pieces.Pairs()) != 0 {
		t.Errorf("the worker, missing a piece, was given %+v", links)
	}

	// A second start changes nothing: it has run.
	before, _ := db.ListVariables(ctx, app.ID)
	if attached, err := m.AttachTemplatePieces(ctx, find); err != nil || attached != 0 {
		t.Errorf("a second start attached %d (%v)", attached, err)
	}
	// Nor would it if it ran again: the links it gave pieces to are skipped.
	if err := db.DeleteSetting(ctx, settings.KeyTemplatePiecesAttached); err != nil {
		t.Fatal(err)
	}
	if attached, err := m.AttachTemplatePieces(ctx, find); err != nil || attached != 0 {
		t.Errorf("running it again attached %d (%v)", attached, err)
	}
	after, _ := db.ListVariables(ctx, app.ID)
	for i := range before {
		if before[i].Key != after[i].Key || before[i].Sealed != after[i].Sealed {
			t.Errorf("%s was written again", before[i].Key)
		}
	}
	if synced := deployer.all(); len(synced) != 1 {
		t.Errorf("rolled out again: %v", synced)
	}
}
