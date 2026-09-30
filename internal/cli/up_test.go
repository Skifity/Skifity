package cli

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// write puts files into a folder, creating the directories they need.
func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// entries reads back what an archive holds.
func entries(t *testing.T, archive string) map[string]*tar.Header {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(gz)
	out := map[string]*tar.Header{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[header.Name] = header
	}
}

func packNames(t *testing.T, root string) []string {
	t.Helper()
	folder, err := pack(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(folder.Archive) })
	var names []string
	for name := range entries(t, folder.Archive) {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// ignoreCases is testdata/ignore-cases.json, which the browser's packer is
// held to as well (web/scripts/check-ignore.mjs).
type ignoreCases struct {
	Cases []struct {
		Name  string            `json:"name"`
		Files map[string]string `json:"files"`
		Sent  []string          `json:"sent"`
	} `json:"cases"`
}

func TestUpSendsWhatTheSharedCasesSay(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "ignore-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases ignoreCases
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Cases) < 3 {
		t.Fatalf("only %d cases were read", len(cases.Cases))
	}
	for _, c := range cases.Cases {
		t.Run(c.Name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, c.Files)
			if got := packNames(t, root); !slices.Equal(got, c.Sent) {
				t.Fatalf("sent %v\nwant %v", got, c.Sent)
			}
		})
	}
}

// The same folder packs to the same bytes, so sending it again is the same
// upload and the panel reuses the image rather than building for nothing.
func TestTheSameFolderIsTheSameUpload(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"b.txt": "2", "a/c.txt": "3", "index.html": "1"})
	hash := func() [32]byte {
		folder, err := pack(root)
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(folder.Archive)
		data, err := os.ReadFile(folder.Archive)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(data)
	}
	first := hash()
	// Touched but not changed: a save in an editor, a checkout.
	later := filepath.Join(root, "b.txt")
	if err := os.Chtimes(later, timeNowPlus(t), timeNowPlus(t)); err != nil {
		t.Fatal(err)
	}
	if hash() != first {
		t.Fatal("touching a file changed the upload, so the same code would build again")
	}
	write(t, root, map[string]string{"b.txt": "changed"})
	if hash() == first {
		t.Fatal("changed code made the same upload, so it would never be built")
	}
}

func TestAScriptStaysRunnableAndALinkStaysBehind(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"gradlew": "#!/bin/sh", "index.html": "x"})
	if err := os.Chmod(filepath.Join(root, "gradlew"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "passwd")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	folder, err := pack(root)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(folder.Archive)
	got := entries(t, folder.Archive)
	if got["gradlew"] == nil || got["gradlew"].Mode&0o111 == 0 {
		t.Fatal("an executable lost its executable bit")
	}
	if got["passwd"] != nil || !slices.Equal(folder.Skipped, []string{"passwd"}) {
		t.Fatalf("a link was sent, or not reported as left behind: %v", folder.Skipped)
	}
}

// Running `up` from inside an app sends the whole app. Sending the subfolder
// the terminal was in would replace the app with a corner of itself.
func TestUpFromASubfolderSendsTheWholeApp(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"skifity.toml": `app = "app_1"`, "src/main.go": "package main"})
	found, file, err := upRoot([]string{filepath.Join(root, "src")})
	if err != nil {
		t.Fatal(err)
	}
	if found != root || file.App != "app_1" {
		t.Fatalf("from src/ it would send %s for app %q", found, file.App)
	}
}

func TestADotEnvIsReadLikeThePanelReadsOne(t *testing.T) {
	got := parseDotEnv("# comment\nexport A=1\nB=\"two words\"\nC='x=y'\n bad line\n9X=no\nD=\n")
	want := map[string]string{"A": "1", "B": "two words", "C": "x=y", "D": ""}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
}

func TestAFolderNameBecomesAnAppName(t *testing.T) {
	for folder, want := range map[string]string{
		"My Cool App": "my-cool-app", "todo_list": "todo-list", "  ": "app", "网站": "app",
	} {
		if got := friendlyName(folder); got != want {
			t.Errorf("%q became %q, want %q", folder, got, want)
		}
	}
}

func timeNowPlus(t *testing.T) (when time.Time) {
	t.Helper()
	return time.Now().Add(time.Hour)
}
