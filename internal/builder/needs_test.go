package builder

import (
	"sort"
	"strings"
	"testing"
)

func filesTree(files map[string]string, extra ...string) Tree {
	t := Tree{Contents: files}
	for name := range files {
		t.Files = append(t.Files, name)
	}
	t.Files = append(t.Files, extra...)
	sort.Strings(t.Files)
	return t
}

func needOf(needs []Need, kind NeedKind, engine string) (Need, bool) {
	for _, n := range needs {
		if n.Kind == kind && n.Engine == engine {
			return n, true
		}
	}
	return Need{}, false
}

// The app an assistant writes most often: Next.js, Prisma, Postgres. Its first
// deploy crashed on a DATABASE_URL nothing had set.
func TestAPrismaAppSaysItNeedsPostgresAndWhatToCallIt(t *testing.T) {
	needs := DetectNeeds(filesTree(map[string]string{
		"package.json": `{"dependencies":{"next":"15.0.0","@prisma/client":"6.0.0"},"devDependencies":{"prisma":"6.0.0"}}`,
		"prisma/schema.prisma": `
generator client {
  provider = "prisma-client-js"
}

datasource db {
  provider = "postgresql"
  url      = env("DATABASE_URL")
}`,
	}))

	pg, ok := needOf(needs, NeedDatabase, EnginePostgres)
	if !ok {
		t.Fatalf("a Prisma app on PostgreSQL was not said to need one: %+v", needs)
	}
	if !pg.Provided {
		t.Error("PostgreSQL was not offered, though this panel runs it")
	}
	if pg.Variable != "DATABASE_URL" {
		t.Errorf("the variable is %q; the schema reads DATABASE_URL", pg.Variable)
	}
	if pg.Source != "prisma/schema.prisma" {
		t.Errorf("the evidence is not traceable to the schema: %+v", pg)
	}
	// The generator's provider line is not a database.
	for _, n := range needs {
		if strings.Contains(n.Evidence, "prisma-client-js") {
			t.Errorf("the generator block was read as a datasource: %+v", n)
		}
	}
}

// A Prisma schema for MySQL reads DATABASE_URL. The panel's own default for a
// MySQL link is MYSQL_URL, so linking by default would set a variable the app
// never reads and the first deploy would crash anyway.
func TestTheVariableIsTheOneTheAppReadsNotThePanelsDefault(t *testing.T) {
	needs := DetectNeeds(filesTree(map[string]string{
		"package.json":         `{"dependencies":{"@prisma/client":"6.0.0"}}`,
		"prisma/schema.prisma": `datasource db { provider = "mysql"  url = env("DATABASE_URL") }`,
	}))
	my, ok := needOf(needs, NeedDatabase, EngineMySQL)
	if !ok {
		t.Fatalf("MySQL was not detected: %+v", needs)
	}
	if my.Variable != "DATABASE_URL" {
		t.Errorf("the variable is %q, not the DATABASE_URL the schema reads", my.Variable)
	}

	// Without a schema, the app's own .env.example says what it reads.
	needs = DetectNeeds(filesTree(map[string]string{
		"package.json": `{"dependencies":{"pg":"8.0.0"}}`,
		".env.example": "POSTGRES_URL=postgres://localhost/app\nSECRET=\n",
	}))
	pg, _ := needOf(needs, NeedDatabase, EnginePostgres)
	if pg.Variable != "POSTGRES_URL" {
		t.Errorf("the variable is %q; .env.example names POSTGRES_URL", pg.Variable)
	}
}

// The case that does not crash, which is why it is the worst: data in a file
// the next deploy erases.
func TestDataInAFileIsCalledWhatItIs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		extra []string
		want  string
	}{
		{"better-sqlite3", map[string]string{"package.json": `{"dependencies":{"better-sqlite3":"11.0.0"}}`}, nil, EngineSQLite},
		{"lowdb", map[string]string{"package.json": `{"dependencies":{"lowdb":"7.0.0"}}`}, nil, EngineFile},
		{"a Prisma schema on sqlite", map[string]string{
			"package.json":         `{"dependencies":{"@prisma/client":"6.0.0"}}`,
			"prisma/schema.prisma": `datasource db { provider = "sqlite" url = "file:./dev.db" }`,
		}, nil, EngineSQLite},
		{"Django with no database driver", map[string]string{"requirements.txt": "Django==5.1\ngunicorn\n"}, nil, EngineSQLite},
		{"Laravel 11", map[string]string{"composer.json": `{}`, ".env.example": "DB_CONNECTION=sqlite\n"}, nil, EngineSQLite},
		{"Go with SQLite", map[string]string{"go.mod": "module x\n\nrequire modernc.org/sqlite v1.30.0\n"}, nil, EngineSQLite},
		{"a database committed beside the code", map[string]string{"package.json": `{}`}, []string{"data/app.sqlite3"}, EngineSQLite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			needs := DetectNeeds(filesTree(tc.files, tc.extra...))
			if _, ok := needOf(needs, NeedEphemeral, tc.want); !ok {
				t.Errorf("not warned about: %+v", needs)
			}
		})
	}
}

// SQLite beside a real database is SQLite for development. A warning there
// would be wrong, and a wrong warning teaches people to skip the right ones.
func TestSQLiteForDevelopmentIsNotAWarning(t *testing.T) {
	needs := DetectNeeds(filesTree(map[string]string{
		"Gemfile": "source \"https://rubygems.org\"\ngem \"rails\"\ngroup :development do\n  gem \"sqlite3\"\nend\ngem \"pg\"\n",
	}))
	if _, ok := needOf(needs, NeedDatabase, EnginePostgres); !ok {
		t.Fatalf("the production database was missed: %+v", needs)
	}
	if n, ok := needOf(needs, NeedEphemeral, EngineSQLite); ok {
		t.Errorf("warned about SQLite beside PostgreSQL: %+v", n)
	}

	// A test-only driver in devDependencies is not where the data lives either.
	needs = DetectNeeds(filesTree(map[string]string{
		"package.json": `{"dependencies":{"express":"5.0.0"},"devDependencies":{"better-sqlite3":"11.0.0"}}`,
	}))
	if n, ok := needOf(needs, NeedEphemeral, EngineSQLite); ok {
		t.Errorf("a test dependency was read as the app's storage: %+v", n)
	}

	// But a Prisma schema that says sqlite is what the app uses, whatever else
	// is installed.
	needs = DetectNeeds(filesTree(map[string]string{
		"package.json":         `{"dependencies":{"@prisma/client":"6.0.0","pg":"8.0.0"}}`,
		"prisma/schema.prisma": `datasource db { provider = "sqlite" url = "file:./dev.db" }`,
	}))
	if _, ok := needOf(needs, NeedEphemeral, EngineSQLite); !ok {
		t.Errorf("the schema said sqlite and was overruled: %+v", needs)
	}
}

// A database this panel does not run is named honestly, not left out.
func TestMongoDBIsNamedAndNotOffered(t *testing.T) {
	needs := DetectNeeds(filesTree(map[string]string{"package.json": `{"dependencies":{"mongoose":"8.0.0"}}`}))
	mongo, ok := needOf(needs, NeedDatabase, EngineMongoDB)
	if !ok {
		t.Fatalf("MongoDB was not mentioned: %+v", needs)
	}
	if mongo.Provided {
		t.Error("MongoDB was offered, and this panel cannot create one")
	}
	if mongo.Variable != "MONGODB_URI" {
		t.Errorf("the variable is %q", mongo.Variable)
	}
}

// Every ecosystem's manifest is read, not only Node's.
func TestEachManifestIsRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		files  map[string]string
		engine string
	}{
		{"requirements.txt with extras and a version", map[string]string{"requirements.txt": "psycopg2-binary[pool]>=2.9 # the driver\n"}, EnginePostgres},
		{"pyproject, PEP 621", map[string]string{"pyproject.toml": "[project]\ndependencies = [\"asyncpg>=0.29\"]\n"}, EnginePostgres},
		{"pyproject, Poetry", map[string]string{"pyproject.toml": "[tool.poetry.dependencies]\npymysql = \"^1.1\"\n"}, EngineMySQL},
		{"go.mod with a major version", map[string]string{"go.mod": "module x\n\nrequire (\n\tgithub.com/jackc/pgx/v5 v5.6.0\n)\n"}, EnginePostgres},
		{"Gemfile", map[string]string{"Gemfile": "gem 'redis', '~> 5.0'\n"}, EngineRedis},
		{"a queue that needs Redis", map[string]string{"package.json": `{"dependencies":{"bullmq":"5.0.0"}}`}, EngineRedis},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := needOf(DetectNeeds(filesTree(tc.files)), NeedDatabase, tc.engine); !ok {
				t.Errorf("%s was not detected", tc.engine)
			}
		})
	}
}

// The settings an app's .env.example lists are what somebody has to fill in,
// less PORT, which the panel sets.
func TestTheAppsOwnSettingsTemplateIsListed(t *testing.T) {
	needs := DetectNeeds(filesTree(map[string]string{
		"package.json": `{}`,
		".env.example": "# Settings\nOPENAI_API_KEY=\nexport NEXTAUTH_SECRET=\"change-me\"\nPORT=3000\nnot a line\n",
	}))
	var vars Need
	for _, n := range needs {
		if n.Kind == NeedVariables {
			vars = n
		}
	}
	want := []string{"NEXTAUTH_SECRET", "OPENAI_API_KEY"}
	if strings.Join(vars.Variables, ",") != strings.Join(want, ",") {
		t.Errorf("listed %v, want %v", vars.Variables, want)
	}
	if vars.Source != ".env.example" {
		t.Errorf("the source is %q", vars.Source)
	}
}

// An app that needs nothing is told nothing.
func TestAnAppThatNeedsNothingHearsNothing(t *testing.T) {
	if needs := DetectNeeds(filesTree(map[string]string{
		"package.json": `{"dependencies":{"next":"15.0.0","react":"19.0.0"}}`,
	})); len(needs) != 0 {
		t.Errorf("a plain front end was told it needs %+v", needs)
	}
}

// Detection runs the needs whatever it decided about the build. A repository
// with its own Dockerfile still reads DATABASE_URL.
func TestADockerfileRepositoryStillSaysWhatItNeeds(t *testing.T) {
	d := Detect(filesTree(map[string]string{
		"Dockerfile":   "FROM node:22\nEXPOSE 3000\n",
		"package.json": `{"dependencies":{"pg":"8.0.0"}}`,
	}))
	if d.Builder != BuilderDockerfile {
		t.Fatalf("builder is %q", d.Builder)
	}
	if _, ok := needOf(d.Needs, NeedDatabase, EnginePostgres); !ok {
		t.Errorf("a Dockerfile repository was not told it needs PostgreSQL: %+v", d.Needs)
	}
}
