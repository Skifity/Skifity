package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Before 0047 the panel offered one MySQL-compatible engine, called mysql,
// and every database it made under that name runs MariaDB. 0047 renames those
// rows to what they are and widens the CHECK to nine engines — by rebuilding
// the table, which with foreign keys on would have deleted every link and
// every preview's copy along with it.
func TestTheMySQLDatabasesThatWereMariaDBAreCalledThat(t *testing.T) {
	ctx := t.Context()
	sqlDB, err := sql.Open("sqlite", fileDSN(filepath.Join(t.TempDir(), "panel.db")))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	db := &DB{DB: sqlDB, path: "panel.db"}
	t.Cleanup(func() { db.Close() })

	if err := db.migrateUpTo(ctx, 46); err != nil {
		t.Fatalf("migrate to 46: %v", err)
	}
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	prj := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &prj); err != nil {
		t.Fatal(err)
	}
	env := Environment{ProjectID: prj.ID, Name: "Production", Slug: "production", Kind: EnvStandard, Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}
	mariadb := Database{EnvironmentID: env.ID, Name: "blog", Slug: "blog", Engine: "mysql", EngineVersion: "11.4",
		CredentialsEnc: "SKF1.sealed"}
	cache := Database{EnvironmentID: env.ID, Name: "cache", Slug: "cache", Engine: "redis", EngineVersion: "7"}
	// Written the way the schema of the time takes them: CreateDatabase
	// speaks today's, which has the columns 0056 added.
	insertEarly := func(d *Database) error {
		d.ID = NewID("db")
		_, err := db.Exec(ctx, `INSERT INTO databases (id, environment_id, name, slug, engine, engine_version,
			credentials_enc, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
			d.ID, d.EnvironmentID, d.Name, d.Slug, d.Engine, d.EngineVersion, d.CredentialsEnc, Now(), Now())
		return err
	}
	for _, d := range []*Database{&mariadb, &cache} {
		if err := insertEarly(d); err != nil {
			t.Fatalf("insert a database: %v", err)
		}
	}
	if err := db.LinkDatabase(ctx, mariadb.ID, app.ID, "MYSQL_URL"); err != nil {
		t.Fatal(err)
	}
	early := Database{EnvironmentID: env.ID, Name: "docs", Slug: "docs", Engine: "mongodb"}
	if err := insertEarly(&early); err == nil {
		t.Fatal("the schema before 0047 already takes mongodb, so this test is not testing the rebuild")
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got, err := db.GetDatabase(ctx, mariadb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Engine != "mariadb" || got.EngineVersion != "11.4" || got.CredentialsEnc != "SKF1.sealed" || got.Slug != "blog" {
		t.Errorf("the MariaDB that was called mysql is now %+v", got)
	}
	if other, _ := db.GetDatabase(ctx, cache.ID); other.Engine != "redis" {
		t.Errorf("a Redis became %q", other.Engine)
	}
	if links, err := db.ListLinksForDatabase(ctx, mariadb.ID); err != nil || len(links) != 1 || links[0].VarName != "MYSQL_URL" {
		t.Fatalf("the rebuild took the database's link with it: %+v, %v", links, err)
	}

	for _, engine := range []string{"mysql", "mariadb", "mongodb", "valkey", "dragonfly", "clickhouse", "memcached"} {
		d := Database{EnvironmentID: env.ID, Name: engine, Slug: engine + "-db", Engine: engine}
		if err := db.CreateDatabase(ctx, &d); err != nil {
			t.Errorf("a %s database is refused after 0047: %v", engine, err)
		}
	}
	bad := Database{EnvironmentID: env.ID, Name: "x", Slug: "x", Engine: "cassandra"}
	if err := db.CreateDatabase(ctx, &bad); err == nil {
		t.Error("an engine nobody runs was accepted")
	}

	var on int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("foreign keys are %d after the rebuild (%v)", on, err)
	}
	// And the cascades that the rebuild had to switch off work again.
	if err := db.DeleteProject(ctx, prj.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetDatabase(ctx, mariadb.ID); err == nil {
		t.Fatal("deleting the project no longer removes its databases")
	}
}
