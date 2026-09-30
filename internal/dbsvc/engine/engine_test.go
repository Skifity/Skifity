package engine

import (
	"regexp"
	"strings"
	"testing"
)

// A tag that names a release: a version with at least two dots, or a
// MariaDB-style major.minor.patch, and never "latest" or a bare major that
// moves under a running database.
var pinnedTag = regexp.MustCompile(`:v?[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?(-alpine)?$`)

// Every engine offers the version it defaults to, and every version it offers
// is an image — pinned to its release, apart from the three whose images were
// never pinned (PostgreSQL's operator images, and Redis and legacy MariaDB
// versions made before versions were offered).
func TestEveryOfferedVersionIsAPinnedImage(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range All() {
		if seen[e.Name] {
			t.Errorf("%s is in the catalogue twice", e.Name)
		}
		seen[e.Name] = true
		if e.Title == "" || e.Port == 0 || e.Variable == "" {
			t.Errorf("%s is missing its title, port or variable: %+v", e.Name, e)
		}
		if !e.Offers(e.DefaultVersion) {
			t.Errorf("%s defaults to %s, which it does not offer", e.Name, e.DefaultVersion)
		}
		for _, version := range e.Versions {
			image, err := e.Image(version)
			if err != nil {
				t.Errorf("%s %s has no image: %v", e.Name, version, err)
				continue
			}
			if strings.HasSuffix(image, ":latest") || !strings.Contains(image, ":") {
				t.Errorf("%s %s runs %s, which is not a release", e.Name, version, image)
			}
			if e.Name == Postgres || e.Name == Redis {
				continue
			}
			if !pinnedTag.MatchString(image) {
				t.Errorf("%s %s runs %s, which moves: pin it to a release", e.Name, version, image)
			}
		}
	}
	if len(seen) != 9 {
		t.Errorf("the catalogue has %d engines", len(seen))
	}
}

// A version is part of an image's name, so what is not offered is refused
// rather than written into one.
func TestAVersionThatIsNotOfferedIsRefused(t *testing.T) {
	mysql, _ := Lookup(MySQL)
	for _, version := range []string{"11.4", "latest", "8", "8.4.11", "8.4@sha256:0", "8.4 --privileged"} {
		if err := mysql.CheckVersion(version); err == nil {
			t.Errorf("MySQL %q was accepted", version)
		}
		if _, err := mysql.Image(version); err == nil {
			t.Errorf("MySQL %q was given an image", version)
		}
	}
	// A MariaDB made when any version was taken still has an image, of its
	// own version, and nothing that is not a version gets one.
	mariadb, _ := Lookup(MariaDB)
	if image, err := mariadb.Image("10.6"); err != nil || image != "mariadb:10.6" {
		t.Errorf("a MariaDB 10.6 made before versions were offered runs %q (%v)", image, err)
	}
	for _, version := range []string{"latest", "10.6-ubi", "11;rm", "../11"} {
		if err := mariadb.CheckVersion(version); err == nil {
			t.Errorf("MariaDB %q was accepted", version)
		}
	}
	if err := mysql.CheckVersion(""); err != nil {
		t.Errorf("no version, which is the default, was refused: %v", err)
	}
}

// What the panel does not do for an engine is said in the catalogue, where
// the API, the backup jobs and the interface all read it.
func TestWhatIsNotBackedUpIsSaid(t *testing.T) {
	want := map[string]bool{
		Postgres: true, MySQL: true, MariaDB: true, MongoDB: true, Redis: true, Valkey: true,
		Dragonfly: false, ClickHouse: false, Memcached: false,
	}
	for name, backups := range want {
		e, ok := Lookup(name)
		if !ok {
			t.Fatalf("%s is not in the catalogue", name)
		}
		if e.Backups != backups {
			t.Errorf("%s: backups offered is %v, want %v", name, e.Backups, backups)
		}
	}
	memcached, _ := Lookup(Memcached)
	if memcached.Storage || memcached.Password || memcached.StorageGB != 0 {
		t.Errorf("memcached is a cache with no disk and no password: %+v", memcached)
	}
	for _, e := range All() {
		if e.Replicated != (e.Name == Postgres) {
			t.Errorf("%s: replicated is %v; only PostgreSQL is", e.Name, e.Replicated)
		}
	}
}

// The catalogue hands out copies: a caller that changes what it was given
// changes nothing for the next one.
func TestTheCatalogueCannotBeChangedFromOutside(t *testing.T) {
	first := All()
	first[0].Versions[0] = "changed"
	if All()[0].Versions[0] == "changed" {
		t.Fatal("All shares the catalogue's own slices")
	}
	found, _ := Lookup(Postgres)
	found.Versions[0] = "changed"
	if again, _ := Lookup(Postgres); again.Versions[0] == "changed" {
		t.Fatal("Lookup shares the catalogue's own slices")
	}
}
