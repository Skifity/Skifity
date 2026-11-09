package templates

import (
	"fmt"
	"slices"
	"strings"

	"skifity/internal/dbsvc/engine"
)

// What a well-known image reads its database from.
//
// A database a template links arrives as one connection string, under its
// var_name, and in pieces only when the template asks for them with vars:.
// Plenty of images read a URL. Plenty do not, and one handed a URL it never
// reads is a database created beside an app that cannot reach it. WordPress
// reads WORDPRESS_DB_HOST and the three beside it, and falls back to a host
// called "mysql"; Ghost reads database__connection__host and the rest; Gitea
// reads GITEA__database__*; n8n reads DB_POSTGRESDB_*. Nothing checked which,
// so the two best-known templates in the catalogue, WordPress and Ghost, made
// their database and never used it, and so did n8n, Gitea, Forgejo and
// Nextcloud with theirs. The only template any test installed was one with no
// database.
//
// This is what each of those images reads, taken from its own documentation
// or source, and CheckKnownImages holds every template on one of them to it —
// the built-in catalogue in its tests, and a team's own when it is read. An
// image that is not listed is not judged: the list says what is known, and
// the catalogue's README says which of the rest have been looked at.
type knownImage struct {
	// reads names, for each engine the image can use, the variables it reads
	// such a database from. Every one has to arrive: as a piece through
	// vars:, as the database's own var_name, or set in variables: — the
	// type a piece cannot say, such as GITEA__database__DB_TYPE.
	reads map[string][]string
	// installer is an image that takes no database setting from its
	// environment because its own installer asks for them in a browser.
	// The database is used once somebody types its connection in there, so
	// the template's notes have to tell them to.
	installer bool
	// instead is an image that takes no database setting at all, and what
	// to run in its place.
	instead string
}

// mysqlAndMariaDB is the two engines that speak MySQL's protocol, which an
// image that reads one reads the other as.
func mysqlAndMariaDB(variables ...string) map[string][]string {
	return map[string][]string{engine.MySQL: variables, engine.MariaDB: variables}
}

// sqlEngines is the same variables for every engine an image supports.
func sqlEngines(variables ...string) map[string][]string {
	return map[string][]string{engine.Postgres: variables, engine.MySQL: variables, engine.MariaDB: variables}
}

// knownImages are keyed by repository, as a template names it without its
// tag. Docker Hub's official images are their bare names.
var knownImages = map[string]knownImage{
	"wordpress": {reads: mysqlAndMariaDB(
		"WORDPRESS_DB_HOST", "WORDPRESS_DB_USER", "WORDPRESS_DB_PASSWORD", "WORDPRESS_DB_NAME")},
	// Ghost 5 runs on MySQL 8 and nothing else in production.
	"ghost": {reads: map[string][]string{engine.MySQL: {
		"database__client", "database__connection__host", "database__connection__user",
		"database__connection__password", "database__connection__database"}}},
	"nextcloud": {reads: map[string][]string{
		engine.MySQL:    {"MYSQL_HOST", "MYSQL_DATABASE", "MYSQL_USER", "MYSQL_PASSWORD"},
		engine.MariaDB:  {"MYSQL_HOST", "MYSQL_DATABASE", "MYSQL_USER", "MYSQL_PASSWORD"},
		engine.Postgres: {"POSTGRES_HOST", "POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD"},
	}},
	"linuxserver/nextcloud": {instead: "the official nextcloud image, which reads MYSQL_* or POSTGRES_*"},
	"gitea/gitea": {reads: sqlEngines("GITEA__database__DB_TYPE", "GITEA__database__HOST",
		"GITEA__database__NAME", "GITEA__database__USER", "GITEA__database__PASSWD")},
	"codeberg.org/forgejo/forgejo": {reads: sqlEngines("FORGEJO__database__DB_TYPE", "FORGEJO__database__HOST",
		"FORGEJO__database__NAME", "FORGEJO__database__USER", "FORGEJO__database__PASSWD")},
	"joomla": {reads: map[string][]string{
		engine.MySQL:    {"JOOMLA_DB_HOST", "JOOMLA_DB_USER", "JOOMLA_DB_PASSWORD", "JOOMLA_DB_NAME"},
		engine.MariaDB:  {"JOOMLA_DB_HOST", "JOOMLA_DB_USER", "JOOMLA_DB_PASSWORD", "JOOMLA_DB_NAME"},
		engine.Postgres: {"JOOMLA_DB_TYPE", "JOOMLA_DB_HOST", "JOOMLA_DB_USER", "JOOMLA_DB_PASSWORD", "JOOMLA_DB_NAME"},
	}},
	"drupal":            {installer: true},
	"mediawiki":         {installer: true},
	"freshrss/freshrss": {installer: true},
	"matomo": {reads: mysqlAndMariaDB(
		"MATOMO_DATABASE_HOST", "MATOMO_DATABASE_USERNAME", "MATOMO_DATABASE_PASSWORD", "MATOMO_DATABASE_DBNAME")},
	"linuxserver/bookstack": {reads: mysqlAndMariaDB("DB_HOST", "DB_USERNAME", "DB_PASSWORD", "DB_DATABASE")},
	"n8nio/n8n": {reads: map[string][]string{engine.Postgres: {"DB_TYPE", "DB_POSTGRESDB_HOST",
		"DB_POSTGRESDB_DATABASE", "DB_POSTGRESDB_USER", "DB_POSTGRESDB_PASSWORD"}}},
	"vikunja/vikunja": {reads: sqlEngines("VIKUNJA_DATABASE_TYPE", "VIKUNJA_DATABASE_HOST",
		"VIKUNJA_DATABASE_USER", "VIKUNJA_DATABASE_PASSWORD", "VIKUNJA_DATABASE_DATABASE")},
	"louislam/uptime-kuma": {reads: mysqlAndMariaDB("UPTIME_KUMA_DB_TYPE", "UPTIME_KUMA_DB_HOSTNAME",
		"UPTIME_KUMA_DB_NAME", "UPTIME_KUMA_DB_USERNAME", "UPTIME_KUMA_DB_PASSWORD")},
	"grafana/grafana":             {reads: sqlEngines("GF_DATABASE_TYPE", "GF_DATABASE_HOST", "GF_DATABASE_NAME", "GF_DATABASE_USER", "GF_DATABASE_PASSWORD")},
	"grafana/grafana-oss":         {reads: sqlEngines("GF_DATABASE_TYPE", "GF_DATABASE_HOST", "GF_DATABASE_NAME", "GF_DATABASE_USER", "GF_DATABASE_PASSWORD")},
	"ghcr.io/pocket-id/pocket-id": {reads: map[string][]string{engine.Postgres: {"DB_CONNECTION_STRING"}}},
	"ghcr.io/fuzzygrim/yamtrack": {reads: map[string][]string{
		engine.Postgres: {"DB_HOST", "DB_NAME", "DB_USER", "DB_PASSWORD"},
		engine.Redis:    {"REDIS_URL"},
	}},
	"flowiseai/flowise": {reads: sqlEngines("DATABASE_TYPE", "DATABASE_HOST", "DATABASE_NAME", "DATABASE_USER", "DATABASE_PASSWORD")},
	// These two read a URL, and are here so the README's list of what has
	// been looked at is not a list of hopes.
	"docuseal/docuseal":         {reads: map[string][]string{engine.Postgres: {"DATABASE_URL"}, engine.MySQL: {"DATABASE_URL"}}},
	"unleashorg/unleash-server": {reads: map[string][]string{engine.Postgres: {"DATABASE_URL"}}},
}

// repository is an image reference without its tag or digest, and without
// the registry names that mean Docker Hub: wordpress for
// docker.io/library/wordpress:6.9.4-apache.
func repository(image string) string {
	ref, _, _ := strings.Cut(image, "@")
	if colon := strings.LastIndex(ref, ":"); colon >= 0 && !strings.Contains(ref[colon:], "/") {
		ref = ref[:colon]
	}
	for _, prefix := range []string{"docker.io/", "index.docker.io/", "registry-1.docker.io/"} {
		ref = strings.TrimPrefix(ref, prefix)
	}
	// lscr.io is linuxserver's own name for the images it also puts on Docker Hub.
	if rest, ok := strings.CutPrefix(ref, "lscr.io/"); ok {
		ref = rest
	}
	return strings.TrimPrefix(ref, "library/")
}

// CheckKnownImages holds a service on a well-known image to the variables
// that image reads its database from.
//
// A service that sets files: or a command: is not judged, because it may be
// configured another way: a settings file the template writes, or flags it
// starts with.
func CheckKnownImages(template Template) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	for _, svc := range template.Services {
		known, ok := knownImages[repository(svc.Image)]
		if !ok || len(svc.Files) > 0 || svc.Command != "" {
			continue
		}
		// Everything this service is given, whichever way it arrives.
		given := map[string]bool{}
		for key := range svc.Variables {
			given[key] = true
		}
		for _, db := range template.Databases {
			if !slices.Contains(db.LinkTo, svc.Name) {
				continue
			}
			given[db.VarName] = db.VarName != ""
			for name := range db.Vars.Pieces() {
				given[name] = true
			}
		}
		for _, db := range template.Databases {
			if !slices.Contains(db.LinkTo, svc.Name) {
				continue
			}
			switch {
			case known.instead != "":
				add("%s/%s runs %s, which takes no database setting from its environment, so %s is created and "+
					"never used; run %s", template.ID, svc.Name, svc.Image, db.Name, known.instead)
			case known.installer:
				if !strings.Contains(strings.ToLower(template.Notes), "installer") {
					add("%s/%s runs %s, which asks for its database in its own installer and reads none from its "+
						"environment; the notes have to say what to type there, or %s is created and never used",
						template.ID, svc.Name, svc.Image, db.Name)
				}
			default:
				reads, known := known.reads[db.Engine]
				if !known {
					continue
				}
				var missing []string
				for _, name := range reads {
					if !given[name] {
						missing = append(missing, name)
					}
				}
				if len(missing) > 0 {
					add("%s/%s runs %s, which reads its %s database from %s, and is not given %s, so %s is "+
						"created and the app cannot reach it; deliver them through vars: or variables:",
						template.ID, svc.Name, svc.Image, db.Engine, strings.Join(reads, ", "),
						strings.Join(missing, ", "), db.Name)
				}
			}
		}
	}
	return problems
}

// caches are the engines a template links for a cache or a queue, which are
// not what the README's account of databases handed over as a URL is about.
var caches = map[string]bool{engine.Redis: true, engine.Valkey: true, engine.Dragonfly: true, engine.Memcached: true}

// URLOnly reports whether a template links a database as a connection string
// and nothing else, and whether every image given one is known to read it —
// or to ask for it in its installer.
func URLOnly(template Template) (urlOnly, lookedUp bool) {
	lookedUp = true
	for _, db := range template.Databases {
		if caches[db.Engine] || len(db.Vars.Pieces()) > 0 {
			continue
		}
		urlOnly = true
		for _, svc := range template.Services {
			if !slices.Contains(db.LinkTo, svc.Name) {
				continue
			}
			known := knownImages[repository(svc.Image)]
			if !known.installer && !slices.Contains(known.reads[db.Engine], db.VarName) {
				lookedUp = false
			}
		}
	}
	return urlOnly, urlOnly && lookedUp
}
