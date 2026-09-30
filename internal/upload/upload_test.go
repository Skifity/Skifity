package upload

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// entry is one thing to put in a test archive.
type entry struct {
	name     string
	body     string
	typeflag byte
	link     string
}

func archive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: e.name, Typeflag: typeflag, Mode: 0o644, Linkname: e.link}
		if typeflag == tar.TypeReg {
			header.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAnOrdinaryProjectIsAccepted(t *testing.T) {
	data := archive(t,
		entry{name: "src/", typeflag: tar.TypeDir},
		entry{name: "package.json", body: `{"name":"app"}`},
		entry{name: "./src/index.js", body: "console.log(1)"},
		entry{name: ".env.example", body: "DATABASE_URL="},
	)
	files, unpacked, err := Inspect(bytes.NewReader(data), DefaultLimits)
	if err != nil {
		t.Fatalf("an ordinary project was refused: %v", err)
	}
	if files != 3 || unpacked != int64(len(`{"name":"app"}`)+len("console.log(1)")+len("DATABASE_URL=")) {
		t.Fatalf("files %d, unpacked %d", files, unpacked)
	}
}

func TestWhatCouldLandOutsideTheFolderIsRefused(t *testing.T) {
	cases := map[string]entry{
		"absolute":            {name: "/etc/passwd", body: "x"},
		"backslash absolute":  {name: `\windows\system32`, body: "x"},
		"drive letter":        {name: "C:/boot.ini", body: "x"},
		"climbs out":          {name: "../outside", body: "x"},
		"climbs out midway":   {name: "src/../../outside", body: "x"},
		"climbs with a slash": {name: `src\..\..\outside`, body: "x"},
		"symlink":             {name: "link", typeflag: tar.TypeSymlink, link: "/etc/passwd"},
		"hard link":           {name: "hard", typeflag: tar.TypeLink, link: "/etc/passwd"},
		"device":              {name: "dev", typeflag: tar.TypeChar},
		"fifo":                {name: "pipe", typeflag: tar.TypeFifo},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			data := archive(t, entry{name: "ok.txt", body: "fine"}, bad)
			_, _, err := Inspect(bytes.NewReader(data), DefaultLimits)
			var refusal *Refusal
			if !errors.As(err, &refusal) {
				t.Fatalf("%q was accepted (err %v)", bad.name, err)
			}
			if refusal.Kind != UnsafeEntry {
				t.Fatalf("%q was refused as %q", bad.name, refusal.Kind)
			}
			if refusal.Entry != bad.name {
				t.Fatalf("the refusal names %q, not the entry %q", refusal.Entry, bad.name)
			}
		})
	}
}

func TestTheFileWithRealSecretsIsRefusedButItsTemplateIsNot(t *testing.T) {
	for _, name := range []string{".env", "./.env", "api/.env", ".env.local", ".env.production"} {
		data := archive(t, entry{name: name, body: "DATABASE_URL=postgres://u:p@h/d"})
		_, _, err := Inspect(bytes.NewReader(data), DefaultLimits)
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Kind != SecretsFile {
			t.Errorf("%s was not refused as a secrets file (%v), and would have been built into the image", name, err)
		}
	}
	for _, name := range []string{".env.example", ".env.sample", ".env.template", ".env.dist", "env.ts", ".envrc.md"} {
		data := archive(t, entry{name: name, body: "x"})
		if _, _, err := Inspect(bytes.NewReader(data), DefaultLimits); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
}

func TestTheLimitsHold(t *testing.T) {
	t.Run("entries", func(t *testing.T) {
		data := archive(t, entry{name: "a", body: "1"}, entry{name: "b", body: "2"}, entry{name: "c", body: "3"})
		if _, _, err := Inspect(bytes.NewReader(data), Limits{Compressed: 1 << 20, Unpacked: 1 << 20, Entries: 2}); err == nil {
			t.Fatal("three entries passed a limit of two")
		}
	})
	t.Run("unpacked", func(t *testing.T) {
		// Compresses to almost nothing, which is the point: the limit is on
		// what it becomes, not on what was sent.
		data := archive(t, entry{name: "zeros", body: strings.Repeat("\x00", 4096)})
		if len(data) > 1024 {
			t.Fatalf("the test archive did not compress (%d bytes)", len(data))
		}
		if _, _, err := Inspect(bytes.NewReader(data), Limits{Compressed: 1 << 20, Unpacked: 4095, Entries: 10}); err == nil {
			t.Fatal("an archive that unpacks past the limit was accepted")
		}
	})
	t.Run("compressed", func(t *testing.T) {
		store := &Store{Dir: t.TempDir(), Limits: Limits{Compressed: 64, Unpacked: 1 << 20, Entries: 10}}
		data := archive(t, entry{name: "a", body: strings.Repeat("random-ish text ", 40)})
		if _, err := store.Save("app1", bytes.NewReader(data)); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("an archive over the compressed limit gave %v", err)
		}
	})
}

func TestADamagedArchiveIsRefused(t *testing.T) {
	data := archive(t, entry{name: "big", body: strings.Repeat("abcdefgh", 4096)})
	// Cut it off partway through the file's body.
	if _, _, err := Inspect(bytes.NewReader(data[:len(data)/2]), DefaultLimits); err == nil {
		t.Fatal("an archive cut in half was accepted")
	}
	if _, _, err := Inspect(strings.NewReader("PK\x03\x04 a zip, not a tarball"), DefaultLimits); err == nil {
		t.Fatal("a zip was accepted as a gzipped tar")
	}
}

func TestSaveNamesAnUploadByItsHashAndLeavesNothingWhenItRefuses(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	data := archive(t, entry{name: "main.go", body: "package main"})

	first, err := store.Save("app1", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !ValidSHA(first.SHA256) || first.Files != 1 || first.Size != int64(len(data)) {
		t.Fatalf("summary %+v", first)
	}
	again, err := store.Save("app1", bytes.NewReader(data))
	if err != nil || again.SHA256 != first.SHA256 {
		t.Fatalf("the same code sent twice is a different upload: %v %+v", err, again)
	}
	if !store.Has("app1", first.SHA256) || store.Has("app2", first.SHA256) {
		t.Fatal("an upload belongs to the app it was sent for, and only to it")
	}

	bad := archive(t, entry{name: "../escape", body: "x"})
	if _, err := store.Save("app1", bytes.NewReader(bad)); err == nil {
		t.Fatal("a refused archive was saved")
	}
	empty := archive(t, entry{name: "dir/", typeflag: tar.TypeDir})
	if _, err := store.Save("app1", bytes.NewReader(empty)); err == nil {
		t.Fatal("an archive with no files was saved")
	}
	names, _ := os.ReadDir(filepath.Join(store.Dir, "app1"))
	if len(names) != 1 {
		var list []string
		for _, n := range names {
			list = append(list, n.Name())
		}
		t.Fatalf("a refused upload left something behind: %v", list)
	}
}

func TestOpenTakesOnlyWhatCouldBeAnUpload(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	sum, err := store.Save("app1", bytes.NewReader(archive(t, entry{name: "a", body: "1"})))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct{ app, sha string }{
		{"app1", "../app1/" + sum.SHA256},
		{"app1", strings.ToUpper(sum.SHA256)},
		{"app1", sum.SHA256[:63]},
		{"../app1", sum.SHA256},
		{"app1/..", sum.SHA256},
		{"", sum.SHA256},
	} {
		if file, err := store.Open(bad.app, bad.sha); !errors.Is(err, ErrNotFound) {
			if file != nil {
				file.Close()
			}
			t.Errorf("Open(%q, %q) gave %v", bad.app, bad.sha, err)
		}
	}
	if _, err := store.Save("../escape", bytes.NewReader(archive(t, entry{name: "a", body: "1"}))); err == nil {
		t.Fatal("an app id that climbs out of the folder was used as a directory")
	}
}

func TestPruneKeepsTheNewest(t *testing.T) {
	store := &Store{Dir: t.TempDir(), Keep: 3}
	var sums []string
	for i := range 5 {
		sum, err := store.Save("app1", bytes.NewReader(archive(t, entry{name: "v", body: strings.Repeat("x", i+1)})))
		if err != nil {
			t.Fatal(err)
		}
		// The filesystem's clock may not tell two saves in the same instant
		// apart; make the order unambiguous.
		when := time.Now().Add(time.Duration(i-10) * time.Minute)
		_ = os.Chtimes(filepath.Join(store.Dir, "app1", sum.SHA256+".tar.gz"), when, when)
		sums = append(sums, sum.SHA256)
	}
	// Sending the oldest again makes it the newest, so it survives the prune
	// that the next save triggers.
	if _, err := store.Save("app1", bytes.NewReader(archive(t, entry{name: "v", body: "x"}))); err != nil {
		t.Fatal(err)
	}
	if latest, err := store.Latest("app1"); err != nil || latest != sums[0] {
		t.Errorf("the newest upload is %q (%v), want the one just sent again", latest, err)
	}
	if _, err := store.Latest("app2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an app that never uploaded has a latest upload: %v", err)
	}
	for i, sum := range sums {
		want := i == 0 || i >= 3
		if store.Has("app1", sum) != want {
			t.Errorf("upload %d: kept is %v, want %v", i, !want, want)
		}
	}
}

func TestRemoveDeletesAnAppsUploads(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	sum, err := store.Save("app1", bytes.NewReader(archive(t, entry{name: "a", body: "1"})))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Remove("app1"); err != nil {
		t.Fatal(err)
	}
	if store.Has("app1", sum.SHA256) {
		t.Fatal("the upload outlived its app")
	}
}
