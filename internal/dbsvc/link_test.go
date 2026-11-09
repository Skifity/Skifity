package dbsvc

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"skifity/internal/api"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// linkHarness is an app and two databases in one environment, with no
// cluster: linking is rows and a sealed variable.
func linkHarness(t *testing.T) (*Manager, *store.DB, store.App, store.Database, store.Database) {
	t.Helper()
	ctx := t.Context()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key, _ := crypto.GenerateKey()
	keyring, err := crypto.NewKeyring("k1", key)
	if err != nil {
		t.Fatal(err)
	}
	team := store.Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := store.Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := store.Environment{ProjectID: project.ID, Name: "Production", Slug: "production", Kind: "standard", Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	app := store.App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}
	database := func(name string) store.Database {
		record := store.Database{EnvironmentID: env.ID, Name: name, Slug: name, Engine: EnginePostgres, Status: "running"}
		if err := db.CreateDatabase(ctx, &record); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(api.DatabaseCredentials{URL: "postgres://app:pw@" + name + ":5432/app",
			Host: name + "-rw", Port: 5432, Database: "app", Username: "app", Password: "pw-of-" + name})
		sealed, err := keyring.Seal(raw, credentialsContext(record.ID))
		if err != nil {
			t.Fatal(err)
		}
		record.CredentialsEnc = sealed
		if err := db.UpdateDatabase(ctx, &record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	m := New(db, keyring, nil, nil, nil, slog.New(slog.DiscardHandler))
	return m, db, app, database("orders"), database("analytics")
}

func variableKeys(t *testing.T, db *store.DB, appID string) map[string]bool {
	t.Helper()
	rows, err := db.ListVariables(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, row := range rows {
		keys[row.Key] = true
	}
	return keys
}

// Linking a database again under another name moved the link and left the
// old variable — the full connection string — behind, belonging to no link:
// kept by every preview, and by the app after the database was unlinked.
func TestLinkingUnderANewNameTakesTheOldVariableAway(t *testing.T) {
	m, db, app, orders, _ := linkHarness(t)
	ctx := t.Context()
	if err := m.Link(ctx, orders.ID, app.ID, "DATABASE_URL", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.Link(ctx, orders.ID, app.ID, "ORDERS_URL", nil); err != nil {
		t.Fatal(err)
	}
	if keys := variableKeys(t, db, app.ID); keys["DATABASE_URL"] || !keys["ORDERS_URL"] {
		t.Fatalf("the app's variables after renaming the link: %v", keys)
	}
}

// A second database linked under the first one's name overwrote its
// connection string, and unlinking either took the other's away.
func TestTwoDatabasesCannotShareOneVariable(t *testing.T) {
	m, db, app, orders, analytics := linkHarness(t)
	ctx := t.Context()
	if err := m.Link(ctx, orders.ID, app.ID, "DATABASE_URL", nil); err != nil {
		t.Fatal(err)
	}
	err := m.Link(ctx, analytics.ID, app.ID, "DATABASE_URL", nil)
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "database.variable_taken" {
		t.Fatalf("a second database under the same name: %v", err)
	}
	links, _ := db.ListLinksForApp(ctx, app.ID)
	if len(links) != 1 || links[0].DatabaseID != orders.ID {
		t.Fatalf("the links are %+v", links)
	}
	if err := m.Link(ctx, analytics.ID, app.ID, "ANALYTICS_URL", nil); err != nil {
		t.Fatalf("under a name of its own: %v", err)
	}
}

// variableValues opens an app's variables, and says which are secret.
func variableValues(t *testing.T, m *Manager, appID string) (map[string]string, map[string]bool) {
	t.Helper()
	rows, err := m.db.ListVariables(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	values, secret := map[string]string{}, map[string]bool{}
	for _, row := range rows {
		plain, err := m.keyring.Open(row.Sealed, "variable:"+appID+":"+row.Key)
		if err != nil {
			t.Fatalf("%s did not open for its app: %v", row.Key, err)
		}
		values[row.Key], secret[row.Key] = string(plain), row.IsSecret
	}
	return values, secret
}

var everyPieceAs = store.LinkPieces{Host: "DB_HOST", Port: "DB_PORT", Name: "DB_DATABASE", User: "DB_USERNAME", Password: "DB_PASSWORD"}

// A link with pieces writes each as a variable of the app — the password a
// secret like the URL it is part of — and keeps them on the link, so that
// unlinking takes all of them away.
func TestALinkDeliversItsPiecesAndTakesThemAway(t *testing.T) {
	m, db, app, orders, _ := linkHarness(t)
	ctx := t.Context()
	pieces := everyPieceAs
	if err := m.Link(ctx, orders.ID, app.ID, "DATABASE_URL", &pieces); err != nil {
		t.Fatal(err)
	}
	values, secret := variableValues(t, m, app.ID)
	want := map[string]string{"DATABASE_URL": "postgres://app:pw@orders:5432/app", "DB_HOST": "orders-rw",
		"DB_PORT": "5432", "DB_DATABASE": "app", "DB_USERNAME": "app", "DB_PASSWORD": "pw-of-orders"}
	if len(values) != len(want) {
		t.Errorf("the app has %v", values)
	}
	for key, value := range want {
		if values[key] != value {
			t.Errorf("%s is %q, want %q", key, values[key], value)
		}
	}
	if !secret["DB_PASSWORD"] || !secret["DATABASE_URL"] || secret["DB_HOST"] || secret["DB_USERNAME"] {
		t.Errorf("which are secret: %v", secret)
	}
	links, _ := db.ListLinksForApp(ctx, app.ID)
	if len(links) != 1 || links[0].Pieces != pieces {
		t.Fatalf("the link keeps %+v", links)
	}

	// Renamed, with the pieces left out: they are kept.
	if err := m.Link(ctx, orders.ID, app.ID, "ORDERS_URL", nil); err != nil {
		t.Fatal(err)
	}
	if keys := variableKeys(t, db, app.ID); keys["DATABASE_URL"] || !keys["ORDERS_URL"] || !keys["DB_PASSWORD"] {
		t.Errorf("after renaming the link the app has %v", keys)
	}
	// Fewer pieces: the ones dropped go.
	fewer := store.LinkPieces{Host: "DB_HOST", Password: "DB_PASSWORD"}
	if err := m.Link(ctx, orders.ID, app.ID, "ORDERS_URL", &fewer); err != nil {
		t.Fatal(err)
	}
	if keys := variableKeys(t, db, app.ID); len(keys) != 3 || keys["DB_PORT"] || keys["DB_USERNAME"] {
		t.Errorf("after dropping three pieces the app has %v", keys)
	}

	if err := m.Unlink(ctx, orders.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if keys := variableKeys(t, db, app.ID); len(keys) != 0 {
		t.Errorf("unlinking left %v", keys)
	}
}

// One name for two things is refused before anything is written: two
// pieces, a piece and the URL, a piece another database's link writes, and a
// piece the engine does not have on its own.
func TestPiecesThatCannotBeDeliveredAreRefused(t *testing.T) {
	m, db, app, orders, analytics := linkHarness(t)
	ctx := t.Context()
	pieces := everyPieceAs
	if err := m.Link(ctx, orders.ID, app.ID, "DATABASE_URL", &pieces); err != nil {
		t.Fatal(err)
	}
	cache := store.Database{EnvironmentID: orders.EnvironmentID, Name: "cache", Slug: "cache", Engine: EngineRedis, Status: "running"}
	if err := db.CreateDatabase(ctx, &cache); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(api.DatabaseCredentials{URL: "redis://default:pw@cache:6379", Host: "cache", Port: 6379, Username: "app", Password: "pw"})
	sealed, _ := m.keyring.Seal(raw, credentialsContext(cache.ID))
	cache.CredentialsEnc = sealed
	if err := db.UpdateDatabase(ctx, &cache); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name     string
		database string
		variable string
		pieces   store.LinkPieces
		code     string
	}{
		{"two pieces under one name", analytics.ID, "ANALYTICS_URL", store.LinkPieces{Host: "A_HOST", Port: "A_HOST"}, "database.pieces_clash"},
		{"a piece under the URL's name", analytics.ID, "ANALYTICS_URL", store.LinkPieces{Host: "ANALYTICS_URL"}, "database.pieces_clash"},
		{"another database's piece", analytics.ID, "ANALYTICS_URL", store.LinkPieces{Password: "DB_PASSWORD"}, "database.variable_taken"},
		{"a URL under another database's piece", analytics.ID, "DB_HOST", store.LinkPieces{}, "database.variable_taken"},
		{"a user Redis does not have", cache.ID, "REDIS_URL", store.LinkPieces{User: "REDIS_USER"}, "database.piece_not_offered"},
		{"a name a container cannot carry", analytics.ID, "ANALYTICS_URL", store.LinkPieces{Host: "A HOST"}, "request.invalid"},
	} {
		err := m.Link(ctx, c.database, app.ID, c.variable, &c.pieces)
		var problem *errdoc.Problem
		if !errors.As(err, &problem) || problem.Code != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
	}
	if keys := variableKeys(t, db, app.ID); len(keys) != 6 {
		t.Errorf("a refused link changed the app's variables: %v", keys)
	}
	values, _ := variableValues(t, m, app.ID)
	if values["DB_PASSWORD"] != "pw-of-orders" {
		t.Errorf("DB_PASSWORD is %q after the refusals", values["DB_PASSWORD"])
	}
}
