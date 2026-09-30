package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gzipped(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The first days of a month are the case worth a test: the new file is not
// published yet and the fetch has to fall back one month rather than leaving
// the firewall with no data on the 1st of every month.
func TestTheFirstOfTheMonthFallsBackAMonth(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if strings.Contains(r.URL.Path, "2026-09") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(gzipped(t, "a database"))
	}))
	defer server.Close()

	dir := t.TempDir()
	f := Fetcher{Client: server.Client(), Dir: dir}
	path, err := f.Fetch(context.Background(), "country",
		server.URL+"/free/dbip-country-lite-%s.mmdb.gz", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(asked) != 2 || !strings.Contains(asked[0], "2026-09") || !strings.Contains(asked[1], "2026-08") {
		t.Fatalf("asked for %v, want this month then last month", asked)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "a database" {
		t.Fatalf("read %s: %q %v", path, body, err)
	}
}

// A file already on disk and current is not downloaded again. This runs on
// every start of a process that may restart often, and a component that spends
// its life re-fetching 10 MB is a component nobody wants running.
func TestACurrentFileIsNotFetchedAgain(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write(gzipped(t, "a database"))
	}))
	defer server.Close()

	f := Fetcher{Client: server.Client(), Dir: t.TempDir()}
	url := server.URL + "/db-%s.mmdb.gz"
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	for range 3 {
		if _, err := f.Fetch(context.Background(), "country", url, now); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("downloaded %d times, want once", calls)
	}

	// A month later it is stale and is fetched again.
	if _, err := f.Fetch(context.Background(), "country", url, now.AddDate(0, 1, 0)); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 2 {
		t.Errorf("a stale file was not refreshed: %d downloads", calls)
	}
}

// An outage at the publisher must not become an outage here: last month's data
// is far better than none.
func TestAFailedRefreshKeepsWhatIsAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "country.mmdb")
	if err := os.WriteFile(path, []byte("last month"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Old enough to be refreshed.
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer server.Close()

	f := Fetcher{Client: server.Client(), Dir: dir}
	got, err := f.Fetch(context.Background(), "country", server.URL+"/db-%s.mmdb.gz", time.Now())
	if err != nil {
		t.Fatalf("a failed refresh with a file on disk returned an error: %v", err)
	}
	if got != path {
		t.Errorf("returned %q, want the file already there", got)
	}
	if body, _ := os.ReadFile(path); string(body) != "last month" {
		t.Errorf("the existing file was damaged: %q", body)
	}
}

// With nothing on disk there is nothing to fall back to, and saying so is the
// only honest answer.
func TestAFailedFirstFetchSaysSo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer server.Close()

	f := Fetcher{Client: server.Client(), Dir: t.TempDir()}
	if _, err := f.Fetch(context.Background(), "country", server.URL+"/db-%s.mmdb.gz", time.Now()); err == nil {
		t.Fatal("a failed first fetch reported success")
	}
}

// A URL an operator pasted is somewhere a download can be pointed, so the copy
// is bounded and a half-written file never becomes the database.
func TestADownloadIsBoundedAndAtomic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// More than the limit, uncompressed so the test does not have to
		// generate a hundred megabytes of gzip.
		chunk := bytes.Repeat([]byte("x"), 1<<20)
		for range (MaxDatabaseBytes >> 20) + 2 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	f := Fetcher{Client: server.Client(), Dir: dir}
	if _, err := f.Fetch(context.Background(), "country", server.URL+"/db.mmdb", time.Now()); err == nil {
		t.Fatal("an unbounded download was accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "country.mmdb" {
			t.Error("a refused download still became the database")
		}
	}
}

// An empty setting is how this is turned off, and must not be an error.
func TestNoURLIsNotAFailure(t *testing.T) {
	f := Fetcher{Dir: t.TempDir()}
	path, err := f.Fetch(context.Background(), "country", "", time.Now())
	if err != nil || path != "" {
		t.Errorf("an empty URL gave %q, %v; want nothing and no error", path, err)
	}
}
