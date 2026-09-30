package builder

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
)

// What an app will reach for once it is running.
//
// Detection used to answer one question — how is this built — and the answer
// was good. The first deploy of an app written with an assistant then failed
// for a different reason nearly every time: it read DATABASE_URL, and nothing
// had made a database. The crash message says so, correctly, after the fact.
// This says it before, while the form is still open and the fix is a checkbox.
//
// The worse case is the one that does not crash. An app that keeps its data in
// SQLite, or in a JSON file through lowdb, works perfectly — for an hour, until
// the next deploy replaces its container and the file with it. Nothing fails,
// nothing is logged, and the data somebody's users typed in is gone. That is
// named here too.
//
// Everything below is evidence rather than inference: a package in a manifest,
// a provider line in a Prisma schema, a name in .env.example. Each Need carries
// the package and the file it was read from, so the interface can say "because
// package.json depends on pg" and a person can check it. A guess that cannot be
// checked is a guess nobody should act on.

// NeedKind is what sort of thing an app needs.
type NeedKind string

const (
	// NeedDatabase is a server the app connects to.
	NeedDatabase NeedKind = "database"
	// NeedEphemeral is data kept in a file inside the container, which the
	// next deploy erases.
	NeedEphemeral NeedKind = "ephemeral"
	// NeedVariables are the settings the app's own .env.example lists.
	NeedVariables NeedKind = "variables"
)

// Engines a Need may name. All but SQL Server are ones this panel runs; SQL
// Server is named so the interface can say honestly that it is not.
const (
	EnginePostgres   = "postgres"
	EngineMySQL      = "mysql"
	EngineMariaDB    = "mariadb"
	EngineMongoDB    = "mongodb"
	EngineRedis      = "redis"
	EngineValkey     = "valkey"
	EngineDragonfly  = "dragonfly"
	EngineClickHouse = "clickhouse"
	EngineMemcached  = "memcached"
	EngineSQLServer  = "sqlserver"
	// EngineSQLite and EngineFile are kinds of ephemeral storage.
	EngineSQLite = "sqlite"
	EngineFile   = "file"
)

// ProvidedEngines are the databases the panel can create and link. A test
// checks this against internal/dbsvc, so the form never offers to create
// something the panel cannot make.
//
// Dragonfly has no client of its own to be detected by — an app talks to it
// with a Redis client — so nothing below names it; it is listed so the two
// lists stay the same, and it is offered where a person chooses an engine.
var ProvidedEngines = []string{
	EnginePostgres, EngineMySQL, EngineMariaDB, EngineMongoDB,
	EngineRedis, EngineValkey, EngineDragonfly, EngineClickHouse, EngineMemcached,
}

// Need is one thing the app needs, and why the panel thinks so.
type Need struct {
	Kind NeedKind `json:"kind"`
	// Engine names the database or the kind of file.
	Engine string `json:"engine,omitempty"`
	// Provided is true when this panel can create it.
	Provided bool `json:"provided"`
	// Evidence is what was found: a package name, a provider, a file.
	Evidence string `json:"evidence,omitempty"`
	// Source is the file the evidence was read from.
	Source string `json:"source,omitempty"`
	// Variable is the name the app reads the connection from, which is the
	// name a linked database has to be given. It is taken from the app's own
	// files when they say, because the panel's default is not always the one
	// the code reads: a Prisma schema for MySQL reads DATABASE_URL.
	Variable string `json:"variable,omitempty"`
	// Variables, for NeedVariables, are the names the app expects.
	Variables []string `json:"variables,omitempty"`
}

// DetectNeeds reads what an app will need from the files detection fetched.
func DetectNeeds(tree Tree) []Need {
	deps := readDependencies(tree)
	example, exampleSource := readEnvExample(tree)

	databases := map[string]Need{}
	var ephemeral []Need
	add := func(n Need) {
		if n.Kind == NeedEphemeral {
			for _, existing := range ephemeral {
				if existing.Engine == n.Engine {
					return
				}
			}
			ephemeral = append(ephemeral, n)
			return
		}
		if _, seen := databases[n.Engine]; !seen {
			databases[n.Engine] = n
		}
	}

	// A Prisma schema is the one source that says which database outright, so
	// it is read first and what it says is not second-guessed.
	schemaSays := ""
	if deps.has("node", "@prisma/client") || deps.has("node", "prisma") {
		if need, ok := prismaNeed(tree); ok {
			schemaSays = need.Engine
			add(need)
		}
	}

	for _, rule := range driverRules {
		if name, ok := deps.first(rule.ecosystem, rule.packages...); ok {
			add(Need{
				Kind: kindOf(rule.engine), Engine: rule.engine,
				Evidence: name, Source: deps.source[rule.ecosystem],
			})
		}
	}

	// Frameworks whose default is a file. Django writes db.sqlite3 and a new
	// Rails app ships with the sqlite3 gem; either is fine on a laptop.
	if deps.has("python", "django") {
		add(Need{Kind: NeedEphemeral, Engine: EngineSQLite, Evidence: "django", Source: deps.source["python"]})
	}
	// Laravel says which in .env.example, and since Laravel 11 the answer out
	// of the box is sqlite.
	switch connection := strings.ToLower(example["DB_CONNECTION"]); connection {
	case "sqlite":
		add(Need{Kind: NeedEphemeral, Engine: EngineSQLite, Evidence: "DB_CONNECTION=sqlite", Source: exampleSource})
	case "mysql":
		add(Need{Kind: NeedDatabase, Engine: EngineMySQL, Evidence: "DB_CONNECTION=mysql", Source: exampleSource})
	case "mariadb":
		// Laravel 11 has a driver of its own for MariaDB, and an app that
		// chose it gets one.
		add(Need{Kind: NeedDatabase, Engine: EngineMariaDB, Evidence: "DB_CONNECTION=mariadb", Source: exampleSource})
	case "pgsql":
		add(Need{Kind: NeedDatabase, Engine: EnginePostgres, Evidence: "DB_CONNECTION=pgsql", Source: exampleSource})
	case "mongodb":
		// mongodb/laravel-mongodb's connection name.
		add(Need{Kind: NeedDatabase, Engine: EngineMongoDB, Evidence: "DB_CONNECTION=mongodb", Source: exampleSource})
	}
	// Laravel's cache, which is a server of its own when it says memcached.
	for _, key := range []string{"CACHE_STORE", "CACHE_DRIVER"} {
		if strings.EqualFold(example[key], "memcached") {
			add(Need{Kind: NeedDatabase, Engine: EngineMemcached, Evidence: key + "=memcached", Source: exampleSource})
			break
		}
	}
	// A database file committed beside the code is the plainest evidence of all.
	for _, file := range tree.Files {
		if skipPath(file) {
			continue
		}
		if ext := path.Ext(file); ext == ".sqlite" || ext == ".sqlite3" {
			add(Need{Kind: NeedEphemeral, Engine: EngineSQLite, Evidence: file, Source: file})
			break
		}
	}

	// One app, one MySQL-compatible server. The mysql package alone asks for
	// MariaDB (see driverRules); beside anything that asks for MySQL itself —
	// mysql2, a Prisma schema, Laravel's DB_CONNECTION — it is the same
	// database spoken to by an older driver, not a second one.
	if _, both := databases[EngineMySQL]; both && databases[EngineMariaDB].Evidence == "mysql" {
		delete(databases, EngineMariaDB)
	}

	// SQLite beside a real database is almost always SQLite for development
	// and the real one in production — a Rails Gemfile does exactly that. The
	// warning would be wrong, and a warning that is wrong teaches people to
	// skip the ones that are right. A Prisma schema is the exception: when it
	// says sqlite, that is what the app uses.
	_, postgres := databases[EnginePostgres]
	_, mysql := databases[EngineMySQL]
	_, mariadb := databases[EngineMariaDB]
	if (postgres || mysql || mariadb) && schemaSays != EngineSQLite {
		kept := ephemeral[:0]
		for _, n := range ephemeral {
			if n.Engine != EngineSQLite {
				kept = append(kept, n)
			}
		}
		ephemeral = kept
	}

	var out []Need
	engines := make([]string, 0, len(databases))
	for engine := range databases {
		engines = append(engines, engine)
	}
	sort.Strings(engines)
	for _, engine := range engines {
		need := databases[engine]
		need.Provided = isProvided(engine)
		if need.Variable == "" {
			need.Variable = variableFor(engine, need.Evidence, example)
		}
		out = append(out, need)
	}
	out = append(out, ephemeral...)

	if names := expectedVariables(example); len(names) > 0 {
		out = append(out, Need{Kind: NeedVariables, Source: exampleSource, Variables: names})
	}
	return out
}

func kindOf(engine string) NeedKind {
	if engine == EngineSQLite || engine == EngineFile {
		return NeedEphemeral
	}
	return NeedDatabase
}

func isProvided(engine string) bool {
	for _, provided := range ProvidedEngines {
		if provided == engine {
			return true
		}
	}
	return false
}

// driverRule says that one of these packages, in this ecosystem, means this
// engine. Only runtime dependencies count: a SQLite driver among a project's
// devDependencies is its test suite, not where it keeps its data.
type driverRule struct {
	ecosystem string
	engine    string
	packages  []string
}

// The Node package called mysql (mysqljs) is unmaintained and cannot sign in
// to an account on MySQL 8's default authentication, caching_sha2_password
// (its issue #2002). An app that uses it is offered MariaDB, which it can
// sign in to, and which is what every "mysql" database here used to be.
// mysql2 speaks both, and an app with it is offered MySQL.
var driverRules = []driverRule{
	{"node", EnginePostgres, []string{"pg", "postgres", "@neondatabase/serverless", "@vercel/postgres", "pg-promise", "slonik"}},
	{"node", EngineMySQL, []string{"mysql2"}},
	{"node", EngineMariaDB, []string{"mariadb", "mysql"}},
	{"node", EngineRedis, []string{"redis", "ioredis", "@redis/client", "bullmq", "bull"}},
	{"node", EngineValkey, []string{"iovalkey", "@valkey/valkey-glide"}},
	{"node", EngineMongoDB, []string{"mongodb", "mongoose"}},
	{"node", EngineClickHouse, []string{"@clickhouse/client"}},
	{"node", EngineMemcached, []string{"memjs", "memcached"}},
	{"node", EngineSQLite, []string{"better-sqlite3", "sqlite3", "sqlite"}},
	{"node", EngineFile, []string{"lowdb", "nedb", "@seald-io/nedb", "node-json-db"}},

	{"python", EnginePostgres, []string{"psycopg2", "psycopg2-binary", "psycopg", "psycopg-binary", "asyncpg", "pg8000"}},
	{"python", EngineMySQL, []string{"mysqlclient", "pymysql", "mysql-connector-python", "aiomysql"}},
	{"python", EngineMariaDB, []string{"mariadb"}},
	{"python", EngineRedis, []string{"redis", "rq", "django-redis"}},
	{"python", EngineValkey, []string{"valkey", "valkey-glide"}},
	{"python", EngineMongoDB, []string{"pymongo", "motor", "mongoengine", "beanie"}},
	{"python", EngineClickHouse, []string{"clickhouse-connect", "clickhouse-driver"}},
	{"python", EngineMemcached, []string{"pymemcache", "python-memcached", "pylibmc"}},

	{"go", EnginePostgres, []string{"github.com/jackc/pgx", "github.com/lib/pq", "gorm.io/driver/postgres"}},
	{"go", EngineMySQL, []string{"github.com/go-sql-driver/mysql", "gorm.io/driver/mysql"}},
	{"go", EngineRedis, []string{"github.com/redis/go-redis", "github.com/go-redis/redis"}},
	{"go", EngineValkey, []string{"github.com/valkey-io/valkey-go"}},
	{"go", EngineMongoDB, []string{"go.mongodb.org/mongo-driver"}},
	{"go", EngineClickHouse, []string{"github.com/ClickHouse/clickhouse-go"}},
	{"go", EngineMemcached, []string{"github.com/bradfitz/gomemcache"}},
	{"go", EngineSQLite, []string{"github.com/mattn/go-sqlite3", "modernc.org/sqlite", "gorm.io/driver/sqlite", "github.com/glebarez/sqlite"}},

	{"ruby", EnginePostgres, []string{"pg"}},
	{"ruby", EngineMySQL, []string{"mysql2"}},
	{"ruby", EngineRedis, []string{"redis"}},
	{"ruby", EngineMongoDB, []string{"mongoid"}},
	{"ruby", EngineClickHouse, []string{"click_house"}},
	{"ruby", EngineMemcached, []string{"dalli"}},
	{"ruby", EngineSQLite, []string{"sqlite3"}},
}

// dependencies are the package names each manifest declares.
type dependencies struct {
	names  map[string]map[string]bool
	source map[string]string
	// nodeDev are a package.json's devDependencies, kept apart because they
	// count for Prisma, whose CLI lives there, and for nothing else.
	nodeDev map[string]bool
}

func (d dependencies) has(ecosystem, name string) bool {
	if d.names[ecosystem][name] {
		return true
	}
	return ecosystem == "node" && name == "prisma" && d.nodeDev[name]
}

func (d dependencies) first(ecosystem string, names ...string) (string, bool) {
	for _, name := range names {
		if d.names[ecosystem][name] {
			return name, true
		}
	}
	return "", false
}

func readDependencies(tree Tree) dependencies {
	d := dependencies{
		names:   map[string]map[string]bool{},
		source:  map[string]string{},
		nodeDev: map[string]bool{},
	}
	set := func(ecosystem, source, name string) {
		if d.names[ecosystem] == nil {
			d.names[ecosystem] = map[string]bool{}
		}
		d.names[ecosystem][name] = true
		if d.source[ecosystem] == "" {
			d.source[ecosystem] = source
		}
	}

	if raw := tree.Read("package.json"); raw != "" {
		var pkg packageJSON
		if json.Unmarshal([]byte(raw), &pkg) == nil {
			for name := range pkg.Dependencies {
				set("node", "package.json", name)
			}
			for name := range pkg.DevDependencies {
				d.nodeDev[name] = true
			}
			if d.source["node"] == "" && len(pkg.DevDependencies) > 0 {
				d.source["node"] = "package.json"
			}
		}
	}

	if raw := tree.Read("requirements.txt"); raw != "" {
		for _, line := range strings.Split(raw, "\n") {
			if name := requirementName(line); name != "" {
				set("python", "requirements.txt", name)
			}
		}
	}
	if raw := tree.Read("pyproject.toml"); raw != "" {
		for _, rule := range driverRules {
			if rule.ecosystem != "python" {
				continue
			}
			for _, name := range rule.packages {
				if pyprojectMentions(raw, name) {
					set("python", "pyproject.toml", name)
				}
			}
		}
		if pyprojectMentions(raw, "django") {
			set("python", "pyproject.toml", "django")
		}
	}

	if raw := tree.Read("go.mod"); raw != "" {
		for _, rule := range driverRules {
			if rule.ecosystem != "go" {
				continue
			}
			for _, module := range rule.packages {
				if goModRequires(raw, module) {
					set("go", "go.mod", module)
				}
			}
		}
	}

	if raw := tree.Read("Gemfile"); raw != "" {
		for _, match := range gemLine.FindAllStringSubmatch(raw, -1) {
			set("ruby", "Gemfile", match[1])
		}
	}
	return d
}

var (
	gemLine       = regexp.MustCompile(`(?m)^\s*gem\s+["']([A-Za-z0-9_.\-]+)["']`)
	requirementAt = regexp.MustCompile(`[\[=<>~!;@ (]`)
)

// requirementName is the package a requirements.txt line names, normalised the
// way pip compares them, or "" for a comment, an option or a blank line.
func requirementName(line string) string {
	if i := strings.Index(line, "#"); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "-") {
		return ""
	}
	if i := requirementAt.FindStringIndex(line); i != nil {
		line = line[:i[0]]
	}
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(line)), "_", "-")
}

// pyprojectMentions reports whether pyproject.toml lists a package, in either
// of the two shapes people write: a PEP 621 string, or a Poetry table key.
func pyprojectMentions(raw, name string) bool {
	quoted := regexp.MustCompile(`(?i)["']` + regexp.QuoteMeta(name) + `(["'\[<>=~!; ])`)
	poetry := regexp.MustCompile(`(?im)^\s*` + regexp.QuoteMeta(name) + `\s*=`)
	return quoted.MatchString(raw) || poetry.MatchString(raw)
}

// goModRequires reports whether go.mod requires a module, including a major
// version suffix such as /v5.
func goModRequires(raw, module string) bool {
	re := regexp.MustCompile(`(?m)^\s*(require\s+)?` + regexp.QuoteMeta(module) + `(/v\d+)?\s`)
	return re.MatchString(raw)
}

// prismaNeed reads the datasource block of a Prisma schema.
//
// Only the datasource: the generator block has a provider line too, and it
// says "prisma-client-js", which is not a database.
func prismaNeed(tree Tree) (Need, bool) {
	for _, file := range []string{"prisma/schema.prisma", "schema.prisma"} {
		raw := tree.Read(file)
		if raw == "" {
			continue
		}
		block := prismaDatasource.FindString(raw)
		if block == "" {
			continue
		}
		provider := ""
		if m := prismaProvider.FindStringSubmatch(block); m != nil {
			provider = m[1]
		}
		engine := map[string]string{
			"postgresql":  EnginePostgres,
			"postgres":    EnginePostgres,
			"cockroachdb": EnginePostgres,
			"mysql":       EngineMySQL,
			"sqlite":      EngineSQLite,
			"mongodb":     EngineMongoDB,
			"sqlserver":   EngineSQLServer,
		}[provider]
		if engine == "" {
			continue
		}
		need := Need{Kind: kindOf(engine), Engine: engine, Evidence: "provider = \"" + provider + "\"", Source: file}
		if m := prismaURLEnv.FindStringSubmatch(block); m != nil && need.Kind == NeedDatabase {
			need.Variable = m[1]
		}
		return need, true
	}
	return Need{}, false
}

var (
	prismaDatasource = regexp.MustCompile(`(?s)datasource\s+\w+\s*\{.*?\}`)
	prismaProvider   = regexp.MustCompile(`provider\s*=\s*"([A-Za-z]+)"`)
	prismaURLEnv     = regexp.MustCompile(`url\s*=\s*env\(\s*"([A-Za-z_][A-Za-z0-9_]*)"\s*\)`)
)

// variableFor is the name a linked database should be given: the one the app's
// own .env.example uses when it names one, and otherwise the convention for
// the package that was found.
func variableFor(engine, evidence string, example map[string]string) string {
	candidates := map[string][]string{
		EnginePostgres:   {"DATABASE_URL", "POSTGRES_URL", "POSTGRESQL_URL", "PG_URL", "DB_URL"},
		EngineMySQL:      {"DATABASE_URL", "MYSQL_URL", "DB_URL"},
		EngineMariaDB:    {"DATABASE_URL", "MARIADB_URL", "MYSQL_URL", "DB_URL"},
		EngineRedis:      {"REDIS_URL", "REDIS_URI", "KV_URL"},
		EngineValkey:     {"VALKEY_URL", "REDIS_URL", "REDIS_URI", "KV_URL"},
		EngineMongoDB:    {"MONGODB_URI", "MONGO_URI", "MONGO_URL", "MONGODB_URL", "DATABASE_URL"},
		EngineClickHouse: {"CLICKHOUSE_URL", "CLICKHOUSE_DSN", "CLICKHOUSE_HOST"},
		EngineMemcached:  {"MEMCACHED_URL", "MEMCACHE_URL", "MEMCACHED_SERVERS", "MEMCACHIER_SERVERS"},
	}[engine]
	for _, name := range candidates {
		if _, ok := example[name]; ok {
			return name
		}
	}
	// @vercel/postgres reads POSTGRES_URL and nothing else.
	if evidence == "@vercel/postgres" {
		return "POSTGRES_URL"
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// readEnvExample reads the template of an app's settings.
//
// .env itself is never here: it is where the real values are, and detection
// has no business fetching somebody's secrets to find out their names. A test
// in internal/gitsrc holds that line.
func readEnvExample(tree Tree) (map[string]string, string) {
	for _, file := range EnvExampleFiles {
		raw := tree.Read(file)
		if raw == "" {
			continue
		}
		out := map[string]string{}
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			key, value, found := strings.Cut(line, "=")
			key = strings.TrimSpace(key)
			if !found || !envName.MatchString(key) {
				continue
			}
			out[key] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
		return out, file
	}
	return nil, ""
}

// EnvExampleFiles are the names an app's settings template goes by.
var EnvExampleFiles = []string{".env.example", ".env.sample", ".env.template"}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// expectedVariables are the names an app expects, less the ones the panel sets
// itself. PORT is the panel's to decide; asking somebody for it would be asking
// them to guess a number the panel then overrides.
func expectedVariables(example map[string]string) []string {
	names := make([]string, 0, len(example))
	for name := range example {
		if name == "PORT" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
