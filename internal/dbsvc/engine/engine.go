// Package engine is the catalogue of the databases Skifity runs: what each is
// called, the port it answers on, the versions offered and the exact image
// behind each, and what the panel does and does not do for it.
//
// It imports nothing of the panel's own. The API validates a request against
// it, internal/dbsvc renders manifests from it, the backup jobs pick their
// client image from it, and the blueprint parser, the MCP server and the
// interface (through GET /api/database-engines) all read the same list, so a
// ninth engine cannot be offered in one place and refused in another.
package engine

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The engines, by the name the API, the database record and a blueprint use.
const (
	Postgres   = "postgres"
	MySQL      = "mysql"
	MariaDB    = "mariadb"
	MongoDB    = "mongodb"
	Redis      = "redis"
	Valkey     = "valkey"
	Dragonfly  = "dragonfly"
	ClickHouse = "clickhouse"
	Memcached  = "memcached"
)

// Engine is one kind of database.
type Engine struct {
	Name string `json:"name"`
	// Title is the product's own name. It is a proper noun and is not
	// translated.
	Title string `json:"title"`
	// Port is the one the connection string names, the tunnel dials and
	// `skifity db connect` listens on locally.
	Port int `json:"port"`
	// DefaultVersion is what a database gets when nobody picks one.
	DefaultVersion string `json:"default_version"`
	// Versions are the ones offered, newest first.
	Versions []string `json:"versions"`
	// Backups says whether the panel backs this engine up. docs/backups.md
	// says why for each one it does not.
	Backups bool `json:"backups"`
	// Replicated is true only for PostgreSQL, which CloudNativePG replicates.
	// Everything else runs as a single instance; see dbsvc.Spec.Validate.
	Replicated bool `json:"replicated"`
	// Storage is false for a cache, which keeps nothing on a disk.
	Storage bool `json:"storage"`
	// Password is false for an engine with no authentication, which is then
	// reachable only by the apps in its own environment.
	Password bool `json:"password"`
	// Variable is what a linked app reads the connection string from when
	// nobody names one.
	Variable string `json:"variable"`
	// StorageGB is the disk a new database gets when the request names none.
	StorageGB int `json:"storage_gb"`

	// What a new database reserves and may use. Not in the API: the panel
	// decides them.
	CPURequestM  int `json:"-"`
	MemRequestMB int `json:"-"`
	MemLimitMB   int `json:"-"`

	// images is each offered version's image, pinned to the release.
	images map[string]string
	// open, when set, renders the image for a version that is not offered.
	// PostgreSQL, Redis and MariaDB took any version before versions were
	// offered, and a database made then still has to be copied into a
	// preview and backed up with its own client.
	open func(version string) string
}

// openVersion is what an open engine accepts: numbers and dots, so a version
// can never become "latest", a digest or another image's name.
var openVersion = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,3}){0,2}$`)

// catalogue is every engine, in the order the interface offers them.
//
// Every pinned tag below was read from its registry before it was written
// here, for amd64 and arm64 both: Docker Hub for the official images, and
// ghcr.io (which serves docker.dragonflydb.io) for Dragonfly. A version is
// only ever added with the release it pins.
var catalogue = []Engine{
	{
		Name: Postgres, Title: "PostgreSQL", Port: 5432,
		DefaultVersion: "17", Versions: []string{"18", "17", "16"},
		Backups: true, Replicated: true, Storage: true, Password: true, Variable: "DATABASE_URL",
		StorageGB: 5, CPURequestM: 100, MemRequestMB: 256, MemLimitMB: 1024,
		// CloudNativePG's own images, which it keeps patched under the
		// major's tag; the operator decides minor upgrades.
		open: func(version string) string { return "ghcr.io/cloudnative-pg/postgresql:" + version },
	},
	{
		Name: MySQL, Title: "MySQL", Port: 3306,
		DefaultVersion: "8.4", Versions: []string{"9.7", "8.4"},
		Backups: true, Storage: true, Password: true, Variable: "MYSQL_URL",
		StorageGB: 5, CPURequestM: 100, MemRequestMB: 256, MemLimitMB: 1024,
		// Both are long-term releases: 8.4 is what most software is tested
		// against, 9.7 the newest.
		images: map[string]string{
			"9.7": "mysql:9.7.2",
			"8.4": "mysql:8.4.11",
		},
	},
	{
		Name: MariaDB, Title: "MariaDB", Port: 3306,
		DefaultVersion: "11.8", Versions: []string{"12.3", "11.8", "11.4", "10.11"},
		Backups: true, Storage: true, Password: true,
		// MYSQL_URL, not MARIADB_URL: every "mysql" database made before
		// MariaDB was named on its own is a MariaDB now (migration 0047), and
		// its apps were linked under MYSQL_URL. A blueprint that links one by
		// default must keep finding the same name.
		Variable:  "MYSQL_URL",
		StorageGB: 5, CPURequestM: 100, MemRequestMB: 256, MemLimitMB: 1024,
		// Long-term releases only.
		images: map[string]string{
			"12.3":  "mariadb:12.3.3",
			"11.8":  "mariadb:11.8.9",
			"11.4":  "mariadb:11.4.13",
			"10.11": "mariadb:10.11.19",
		},
		open: func(version string) string { return "mariadb:" + version },
	},
	{
		Name: MongoDB, Title: "MongoDB", Port: 27017,
		DefaultVersion: "8.0", Versions: []string{"8.0", "7.0"},
		Backups: true, Storage: true, Password: true, Variable: "MONGODB_URI",
		StorageGB: 5, CPURequestM: 100, MemRequestMB: 256, MemLimitMB: 1024,
		images: map[string]string{
			"8.0": "mongo:8.0.32",
			"7.0": "mongo:7.0.43",
		},
	},
	{
		Name: Redis, Title: "Redis", Port: 6379,
		DefaultVersion: "7", Versions: []string{"7"},
		Backups: true, Storage: true, Password: true, Variable: "REDIS_URL",
		StorageGB: 5, CPURequestM: 100, MemRequestMB: 256, MemLimitMB: 1024,
		open: func(version string) string { return "redis:" + version + "-alpine" },
	},
	{
		Name: Valkey, Title: "Valkey", Port: 6379,
		DefaultVersion: "9.0", Versions: []string{"9.1", "9.0", "8.1"},
		// REDIS_URL, because Valkey is a drop-in for Redis and that is the
		// name the software that uses it reads.
		Backups: true, Storage: true, Password: true, Variable: "REDIS_URL",
		StorageGB: 5, CPURequestM: 100, MemRequestMB: 256, MemLimitMB: 1024,
		images: map[string]string{
			"9.1": "valkey/valkey:9.1.2-alpine",
			"9.0": "valkey/valkey:9.0.6-alpine",
			"8.1": "valkey/valkey:8.1.10-alpine",
		},
	},
	{
		Name: Dragonfly, Title: "Dragonfly", Port: 6379,
		DefaultVersion: "1.40", Versions: []string{"1.40"},
		// Not backed up: Dragonfly does not implement SYNC, which is how a
		// Redis snapshot is taken from outside the server, and its own
		// snapshots stay on its own disk. See docs/backups.md.
		Backups: false, Storage: true, Password: true, Variable: "REDIS_URL",
		StorageGB: 5, CPURequestM: 100, MemRequestMB: 256, MemLimitMB: 1024,
		images: map[string]string{
			"1.40": "docker.dragonflydb.io/dragonflydb/dragonfly:v1.40.2",
		},
	},
	{
		Name: ClickHouse, Title: "ClickHouse", Port: 8123,
		DefaultVersion: "26.8", Versions: []string{"26.8", "26.3"},
		// Not backed up: see docs/backups.md. ClickHouse's own BACKUP
		// command needs the bucket's credentials inside the database, which
		// is what ADR-0012 keeps out of every namespace.
		Backups: false, Storage: true, Password: true, Variable: "CLICKHOUSE_URL",
		// Analytical queries want memory; a gigabyte is the least ClickHouse
		// is comfortable with, two leaves room for a query.
		StorageGB: 10, CPURequestM: 250, MemRequestMB: 512, MemLimitMB: 2048,
		// Long-term releases.
		images: map[string]string{
			"26.8": "clickhouse/clickhouse-server:26.8.15.10",
			"26.3": "clickhouse/clickhouse-server:26.3.37.3",
		},
	},
	{
		Name: Memcached, Title: "Memcached", Port: 11211,
		DefaultVersion: "1.6", Versions: []string{"1.6"},
		// A cache: nothing on a disk, nothing to back up, and no password —
		// SASL would need a binary protocol most clients no longer speak.
		// The environment's network policy is what keeps it private.
		Backups: false, Storage: false, Password: false, Variable: "MEMCACHED_URL",
		StorageGB: 0, CPURequestM: 50, MemRequestMB: 64, MemLimitMB: 256,
		images: map[string]string{
			"1.6": "memcached:1.6.45-alpine",
		},
	},
}

// All returns every engine, in the order they are offered.
func All() []Engine {
	out := make([]Engine, len(catalogue))
	for i, e := range catalogue {
		e.Versions = slices.Clone(e.Versions)
		out[i] = e
	}
	return out
}

// Names returns every engine's name, in the order they are offered.
func Names() []string {
	out := make([]string, len(catalogue))
	for i, e := range catalogue {
		out[i] = e.Name
	}
	return out
}

// Lookup finds an engine by name.
func Lookup(name string) (Engine, bool) {
	for _, e := range catalogue {
		if e.Name == name {
			e.Versions = slices.Clone(e.Versions)
			return e, true
		}
	}
	return Engine{}, false
}

// Known reports whether a name is an engine Skifity runs.
func Known(name string) bool {
	_, ok := Lookup(name)
	return ok
}

// DefaultVariable is the variable an engine's connection string arrives as
// when nobody names one, and DATABASE_URL for anything unknown.
func DefaultVariable(name string) string {
	if e, ok := Lookup(name); ok {
		return e.Variable
	}
	return "DATABASE_URL"
}

// Offers reports whether a version is one of those offered.
func (e Engine) Offers(version string) bool {
	return slices.Contains(e.Versions, version)
}

// CheckVersion reports a version this engine cannot be created at. An empty
// version is the default, and is always fine.
func (e Engine) CheckVersion(version string) error {
	if version == "" || e.Offers(version) {
		return nil
	}
	if e.open != nil && openVersion.MatchString(version) {
		return nil
	}
	return fmt.Errorf("%s %s is not a version Skifity runs; it runs %s",
		e.Title, version, strings.Join(e.Versions, ", "))
}

// Image is the image a version of this engine runs, pinned to its release
// for every version that is offered.
func (e Engine) Image(version string) (string, error) {
	if version == "" {
		version = e.DefaultVersion
	}
	if image, ok := e.images[version]; ok {
		return image, nil
	}
	if e.open != nil && openVersion.MatchString(version) {
		return e.open(version), nil
	}
	return "", e.CheckVersion(version)
}
