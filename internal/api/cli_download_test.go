package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"skifity/internal/config"
	"skifity/internal/events"
)

// Somebody deploying from a Mac needs a CLI that runs on a Mac, and the panel
// runs on Linux. These check the other platforms come from the CLI directory,
// that nothing but a known platform ever becomes part of a path, and that the
// panel says which platforms it has.
func TestTheCLIIsServedForOtherPlatforms(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	other := "darwin"
	if runtime.GOOS == "darwin" {
		other = "windows"
	}
	name := cliFileName(other, "arm64")
	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	_, _ = gz.Write([]byte("the " + other + " binary"))
	_ = gz.Close()
	if err := os.WriteFile(filepath.Join(dir, name+".gz"), packed.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Options{
		Config: config.Config{CLIDir: dir}, DB: h.db, Keyring: h.keyring, Auth: h.auth,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler),
	}))
	defer server.Close()

	type answer struct {
		status int
		header http.Header
	}
	get := func(query string, gzipOK bool) (answer, string) {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/cli/download"+query, nil)
		// Set explicitly, so Go's transport does not ask for gzip and quietly
		// undo it: the point is to see what curl, which does not ask, receives.
		if gzipOK {
			req.Header.Set("Accept-Encoding", "gzip")
		} else {
			req.Header.Set("Accept-Encoding", "identity")
		}
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return answer{resp.StatusCode, resp.Header}, string(body)
	}

	resp, body := get("?os="+other+"&arch=arm64", false)
	if resp.status != http.StatusOK || body != "the "+other+" binary" {
		t.Fatalf("the %s build came back as %d %q", other, resp.status, body)
	}
	if !strings.Contains(resp.header.Get("Content-Disposition"), name) {
		t.Errorf("it is not offered under its own name: %s", resp.header.Get("Content-Disposition"))
	}
	resp, body = get("?os="+other+"&arch=arm64", true)
	if resp.header.Get("Content-Encoding") != "gzip" || body != packed.String() {
		t.Errorf("a client that takes gzip was not given the file as it is stored")
	}

	for _, query := range []string{"?os=../../etc&arch=arm64", "?os=darwin&arch=../../passwd", "?os=plan9&arch=amd64"} {
		if resp, body := get(query, false); resp.status != http.StatusBadRequest || !strings.Contains(body, "cli.platform_unknown") {
			t.Errorf("%s answered %d %s", query, resp.status, body)
		}
	}
	if resp, body := get("?os="+other+"&arch=amd64", false); resp.status != http.StatusNotFound ||
		!strings.Contains(body, "cli.platform_unavailable") {
		t.Errorf("a platform with no build answered %d %s", resp.status, body)
	}

	// No platform named is the panel's own, which is the running binary.
	if resp, body := get("", false); resp.status != http.StatusOK || len(body) < 1<<20 {
		t.Errorf("the panel's own binary answered %d with %d bytes", resp.status, len(body))
	}

	meta, err := http.Get(server.URL + "/api/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Body.Close()
	var listed struct {
		Platforms []string `json:"cli_platforms"`
	}
	_ = json.NewDecoder(meta.Body).Decode(&listed)
	want := []string{runtime.GOOS + "/" + runtime.GOARCH, other + "/arm64"}
	if strings.Join(listed.Platforms, ",") != strings.Join(want, ",") {
		t.Errorf("the panel says it has %v, want %v", listed.Platforms, want)
	}
}
