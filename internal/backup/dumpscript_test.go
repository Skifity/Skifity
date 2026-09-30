package backup

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/dbsvc"
)

// The dump script is a shell program, so it is tested by running it.
//
// Reading it cannot tell an empty backup from a full one. The check that a
// dump is worth uploading used to happen after gzip, and compressing an empty
// file gives about twenty bytes — which every "is this file non-empty" test
// accepts. A dump that produced nothing while reporting success was uploaded,
// recorded, and found to be worthless on the day somebody needed it.
//
// runDumpScript runs the script with a stub dump tool that writes whatever the
// test tells it to, and reports whether the script accepted the result.
func runDumpScript(t *testing.T, engine, tool, output string) (ok bool, combined string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stub dump tool. It writes what the test asked for and succeeds, which
	// is exactly the case the size check exists for.
	stub := "#!/bin/sh\nprintf '%s' " + shellQuote(output) + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, tool), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	script := backupScript(JobSpec{Engine: engine})
	// The script writes beside dumpFile, so give it somewhere writable.
	if err := os.MkdirAll(filepath.Dir(dumpFile), 0o755); err != nil {
		t.Skipf("cannot create %s here: %v", filepath.Dir(dumpFile), err)
	}
	t.Cleanup(func() {
		_ = os.Remove(dumpFile)
		_ = os.Remove(dumpFile + ".raw")
	})

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"DB_NAME=shop", "DB_HOST=db.test", "DB_PORT=5432",
		"DB_USER=shop", "DB_PASSWORD=not-a-real-password")
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// An empty dump has to stop the backup, not become one.
func TestADumpThatProducedNothingIsRefused(t *testing.T) {
	ok, out := runDumpScript(t, dbsvc.EnginePostgres, "pg_dump", "")
	if ok {
		t.Fatalf("an empty dump was accepted and would have been uploaded as a backup:\n%s", out)
	}
	if !strings.Contains(out, "came back empty") {
		t.Errorf("the reason was not said in words somebody can act on:\n%s", out)
	}
}

// A real dump still goes through, compressed.
func TestADumpWithContentIsKept(t *testing.T) {
	ok, out := runDumpScript(t, dbsvc.EnginePostgres, "pg_dump",
		"--\n-- PostgreSQL database dump\n--\nCREATE TABLE orders (id int);\n")
	if !ok {
		t.Fatalf("a real dump was refused:\n%s", out)
	}
	info, err := os.Stat(dumpFile)
	if err != nil {
		t.Fatalf("the compressed dump was not written: %v", err)
	}
	if info.Size() == 0 {
		t.Error("the compressed dump is empty")
	}
	if _, err := os.Stat(dumpFile + ".raw"); !os.IsNotExist(err) {
		t.Error("the uncompressed dump was left behind, which doubles the disk a backup needs")
	}
}

// runRestoreScript runs the restore with a stub loader that records what it was
// given on standard input, and a backup file the test supplies.
func runRestoreScript(t *testing.T, engine, tool string, archive []byte) (ok bool, loaded, combined string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(dir, "loaded")
	// A stub loader that succeeds whatever it is given. That is the case worth
	// testing: the danger was never a loader that fails, it was one that
	// succeeds on whatever little reached it.
	stub := "#!/bin/sh\ncat > " + seen + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, tool), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(dumpFile), 0o755); err != nil {
		t.Skipf("cannot create %s here: %v", filepath.Dir(dumpFile), err)
	}
	if err := os.WriteFile(dumpFile, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(dumpFile)
		_ = os.Remove(dumpFile + ".sql")
	})

	cmd := exec.Command("sh", "-c", restoreScript(JobSpec{Engine: engine}))
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"DB_NAME=shop", "DB_HOST=db.test", "DB_PORT=5432",
		"DB_USER=shop", "DB_PASSWORD=not-a-real-password")
	out, err := cmd.CombinedOutput()
	got, _ := os.ReadFile(seen)
	return err == nil, string(got), string(out)
}

// A corrupt archive must stop the restore.
//
// It used to be `gzip -dc backup.gz | psql`, and set -e reads only the last
// command in a pipeline: psql succeeded on whatever little reached it and the
// restore reported that it had finished. With a dump taken --clean
// --if-exists, that is tables dropped and not put back.
func TestACorruptBackupStopsTheRestore(t *testing.T) {
	ok, loaded, out := runRestoreScript(t, dbsvc.EnginePostgres, "psql",
		[]byte("this is not a gzip archive at all"))
	if ok {
		t.Fatalf("a corrupt archive was restored and reported as finished:\n%s", out)
	}
	if strings.Contains(loaded, "not a gzip") {
		t.Error("the corrupt bytes were handed to the database")
	}
}

// A real backup still restores, and reaches the database whole.
func TestARealBackupIsRestoredWhole(t *testing.T) {
	var archive bytes.Buffer
	writer := gzip.NewWriter(&archive)
	const sql = "DROP TABLE IF EXISTS orders;\nCREATE TABLE orders (id int);\n"
	if _, err := writer.Write([]byte(sql)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	ok, loaded, out := runRestoreScript(t, dbsvc.EnginePostgres, "psql", archive.Bytes())
	if !ok {
		t.Fatalf("a real backup was refused:\n%s", out)
	}
	if loaded != sql {
		t.Errorf("the database was given %q, want %q", loaded, sql)
	}
	if _, err := os.Stat(dumpFile + ".sql"); !os.IsNotExist(err) {
		t.Error("the unpacked dump was left behind")
	}
}

// An archive that unpacks to nothing is refused rather than restored as an
// empty database over a full one.
func TestABackupThatUnpacksToNothingIsRefused(t *testing.T) {
	var archive bytes.Buffer
	writer := gzip.NewWriter(&archive)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	ok, _, out := runRestoreScript(t, dbsvc.EnginePostgres, "psql", archive.Bytes())
	if ok {
		t.Fatalf("an empty archive was restored over a live database:\n%s", out)
	}
	if !strings.Contains(out, "unpacked to nothing") {
		t.Errorf("the reason was not said in words somebody can act on:\n%s", out)
	}
}

// A password on a command line is readable by anything that can list the
// pod's processes — `ps` in a sidecar, a debugging session, /proc — for as
// long as the dump or restore runs. Each client has an environment variable
// for it, which is not.
func TestNoScriptPutsThePasswordOnACommandLine(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}
	const password = "not-a-real-password"
	tools := map[string]struct{ backup, restore, env string }{
		dbsvc.EnginePostgres: {"pg_dump", "psql", "PGPASSWORD"},
		dbsvc.EngineMySQL:    {"mariadb-dump", "mariadb", "MYSQL_PWD"},
		dbsvc.EngineRedis:    {"redis-cli", "redis-cli", "REDISCLI_AUTH"},
	}
	for engine, tool := range tools {
		for _, side := range []struct{ name, tool, script string }{
			{"backup", tool.backup, backupScript(JobSpec{Engine: engine})},
			{"restore", tool.restore, restoreScript(JobSpec{Engine: engine})},
		} {
			dir := t.TempDir()
			seen := filepath.Join(dir, "seen")
			// The stub writes its arguments and the environment variable it
			// should have been given, and enough output for the script to go on.
			stub := "#!/bin/sh\nprintf 'ARGS %s\\nENV %s\\n' \"$*\" \"$" + tool.env + "\" >> " + shellQuote(seen) +
				"\nprintf 'header and some data'\n"
			if err := os.WriteFile(filepath.Join(dir, side.tool), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", side.script)
			cmd.Env = append(os.Environ(),
				"PATH="+dir+":"+os.Getenv("PATH"),
				"DB_NAME=shop", "DB_HOST=db.test", "DB_PORT=5432",
				"DB_USER=shop", "DB_PASSWORD="+password)
			_, _ = cmd.CombinedOutput() // the restore may fail on the stub's output; the call is what is checked
			recorded, err := os.ReadFile(seen)
			if err != nil {
				t.Fatalf("%s %s never ran %s", engine, side.name, side.tool)
			}
			for _, line := range strings.Split(string(recorded), "\n") {
				if strings.HasPrefix(line, "ARGS ") && strings.Contains(line, password) {
					t.Errorf("%s %s passes the password on %s's command line: %s", engine, side.name, side.tool, line)
				}
			}
			if !strings.Contains(string(recorded), "ENV "+password) {
				t.Errorf("%s %s does not give %s the password in %s", engine, side.name, side.tool, tool.env)
			}
		}
	}
}
