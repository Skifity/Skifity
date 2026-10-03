package backup

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

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
// long as the dump or restore runs. Each client takes it from somewhere else:
// an environment variable of its own, or, for MongoDB's tools, which take it
// from nowhere but a flag or a file, a file.
//
// Every script of every engine the panel backs up is run here, with each tool
// replaced by a stub that writes down how it was called, and the password is
// looked for in every argument any of them was given.
func TestNoScriptPutsThePasswordOnACommandLine(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}
	const password = "not-a-real-password"
	for _, name := range backedUp() {
		for _, restore := range []bool{false, true} {
			spec := JobSpec{Engine: name}
			script, side := backupScript(spec), "backup"
			if restore {
				script, side = restoreScript(spec), "restore"
			}
			run := runWithStubs(t, script, stubEnv{Password: password})
			if len(run.calls) == 0 {
				t.Fatalf("%s %s ran no client at all:\n%s", name, side, run.output)
			}
			reached := false
			for _, call := range run.calls {
				if strings.Contains(call.args, password) {
					t.Errorf("%s %s passes the password on %s's command line: %s", name, side, call.tool, call.args)
				}
				if strings.Contains(call.env, password) || strings.Contains(call.file, password) {
					reached = true
				}
			}
			if !reached {
				t.Errorf("%s %s never gave a client the password, from its environment or a file:\n%s",
					name, side, run.output)
			}
			if !run.ok {
				t.Errorf("%s %s failed against clients that did everything asked:\n%s", name, side, run.output)
			}
		}
	}
}

// The dump is made by the database's client and read by two other
// containers, running as other users: the one that seals it and the one that
// uploads it. Whatever a script does to keep its own files private, the dump
// is left readable to them.
func TestTheDumpIsLeftReadableForTheContainersAfterIt(t *testing.T) {
	for _, name := range backedUp() {
		// The images' own umask, whatever this test process was started with.
		run := runWithStubs(t, "umask 022\n"+backupScript(JobSpec{Engine: name}),
			stubEnv{Password: "not-a-real-password", NoArchive: true})
		if !run.ok {
			t.Fatalf("%s: the backup failed:\n%s", name, run.output)
		}
		info, err := os.Stat(filepath.Join(run.dir, filepath.Base(dumpFile)))
		if err != nil {
			t.Fatalf("%s: no dump: %v", name, err)
		}
		if info.Mode().Perm()&0o040 == 0 {
			t.Errorf("%s: the dump is %v, which the upload's user cannot read", name, info.Mode().Perm())
		}
	}
}

// A Redis restore starts a server on the snapshot, makes the database its
// replica until it has everything, and makes it a primary again.
func TestARedisRestoreReplicatesAndLetsGo(t *testing.T) {
	for _, name := range []string{dbsvc.EngineRedis, dbsvc.EngineValkey} {
		run := runWithStubs(t, restoreScript(JobSpec{Engine: name}), stubEnv{Password: "not-a-real-password"})
		if !run.ok {
			t.Fatalf("%s: the restore failed:\n%s", name, run.output)
		}
		sent := run.sentToDatabase()
		want := []string{`CONFIG SET masterauth "not-a-real-password"`, "REPLICAOF 10.42.0.9 " + redisRestorePort,
			"REPLICAOF NO ONE", `CONFIG SET masterauth ""`}
		if !slices.Equal(sent, want) {
			t.Errorf("%s: the database was sent %q, want %q", name, sent, want)
		}
		server, _, _ := redisTools(name)
		if !run.called(server) {
			t.Errorf("%s: no %s was started on the snapshot", name, server)
		}
		// The temporary server reads the snapshot the backup unpacked to.
		if !strings.Contains(run.config, "dbfilename dump.gz.sql") || !strings.Contains(run.config, "port "+redisRestorePort) {
			t.Errorf("%s: the temporary server is configured as:\n%s", name, run.config)
		}
	}
}

// A database older than the server that loads the snapshot could not read
// what it would be sent. That is found out before it is touched.
func TestARedisRestoreRefusesADatabaseOlderThanItsSnapshot(t *testing.T) {
	run := runWithStubs(t, restoreScript(JobSpec{Engine: dbsvc.EngineRedis}),
		stubEnv{Password: "not-a-real-password", LiveVersion: "7.2.4", HereVersion: "7.4.1"})
	if run.ok {
		t.Fatalf("a restore into an older Redis went ahead:\n%s", run.output)
	}
	if sent := run.sentToDatabase(); len(sent) != 0 {
		t.Errorf("the database was sent %q before the versions were compared", sent)
	}
	if !strings.Contains(run.output, "older Redis") {
		t.Errorf("the reason was not said:\n%s", run.output)
	}
}

// Whatever goes wrong once the database is a replica, it is made a primary
// again: a replica of a server that is gone refuses every write for good.
func TestARedisRestoreThatFailsHalfwayLetsTheDatabaseGo(t *testing.T) {
	run := runWithStubs(t, restoreScript(JobSpec{Engine: dbsvc.EngineValkey}),
		stubEnv{Password: "not-a-real-password", LinkDown: true, SourceDies: true})
	if run.ok {
		t.Fatalf("a restore whose server died was reported as finished:\n%s", run.output)
	}
	sent := run.sentToDatabase()
	if len(sent) < 2 || sent[len(sent)-2] != "REPLICAOF NO ONE" || sent[len(sent)-1] != `CONFIG SET masterauth ""` {
		t.Errorf("the database was left a replica; it was sent %q", sent)
	}
}

// stubEnv shapes what the stub clients answer.
type stubEnv struct {
	Password string
	// LiveVersion and HereVersion are the versions the database and the
	// restore's own server report.
	LiveVersion, HereVersion string
	// LinkDown keeps the database from ever finishing its synchronisation.
	LinkDown bool
	// SourceDies stops the restore's own server a second after it starts.
	SourceDies bool
	// NoArchive leaves the workspace empty, as a backup finds it.
	NoArchive bool
}

type stubCall struct{ tool, args, env, file string }

type stubRun struct {
	ok     bool
	output string
	// dir stands in for the workspace.
	dir   string
	calls []stubCall
	// stdin is every line a client was given on its standard input to run
	// against the database: -h, not the restore's own server.
	stdin  []string
	config string
}

func (r stubRun) sentToDatabase() []string { return r.stdin }

func (r stubRun) called(tool string) bool {
	for _, call := range r.calls {
		if call.tool == tool {
			return true
		}
	}
	return false
}

// stubTools are every client a backup or restore script runs.
var stubTools = []string{
	"pg_dump", "psql", "mysqldump", "mysql", "mariadb-dump", "mariadb", "mongodump", "mongorestore",
	"redis-cli", "valkey-cli", "redis-server", "valkey-server",
}

// stubScript records each call — its arguments, the password variables it
// was given, and a --config file's contents — and answers like the tool would
// when everything works.
const stubScript = `#!/bin/sh
tool=$(basename "$0")
config=""
for arg in "$@"; do
  case "$arg" in --config=*) config=$(cat "${arg#--config=}") ;; esac
done
# One write for the whole record. A server runs in the background while the
# client calls it, and five writes each let the two records interleave: the
# server's TOOL line landed in the client's record, was overwritten there,
# and the server was never seen to have run.
record=$(printf 'CALL\nTOOL %s\nARGS %s\nENV %s|%s|%s\nFILE %s' "$tool" "$*" \
  "${PGPASSWORD:-}" "${MYSQL_PWD:-}" "${REDISCLI_AUTH:-}" "$config")
printf '%s\n' "$record" >> "$STUB_LOG"
case "$tool" in
  *-server)
    cat "$1" > "$STUB_CONFIG"
    # Up, and answering from here on: what the restore's ping waits for.
    : > "$STUB_LOG.up"
    if [ -n "${SOURCE_DIES:-}" ]; then exec sleep 1; fi
    exec sleep 60 ;;
  pg_dump|mysqldump|mariadb-dump|mongodump)
    printf 'header and some data' ; exit 0 ;;
  psql|mysql|mariadb|mongorestore)
    cat > /dev/null ; exit 0 ;;
esac
# redis-cli and valkey-cli.
target=live
case " $* " in *" -p 6380 "*) target=here ;; esac
case "$*" in
  *--rdb*) printf 'REDIS0011 header and some data' ;;
  *" ping")
    # The restore's own server answers once it has started, as a real one
    # does. Answering before it had, a quick run finished and killed it
    # before it had written down that it ran, and the test failed one time
    # in ten.
    if [ "$target" = here ] && [ ! -e "$STUB_LOG.up" ]; then exit 1; fi
    echo PONG ;;
  *"info server")
    if [ "$target" = here ]; then v="${HERE_VERSION:-7.4.1}"; else v="${LIVE_VERSION:-7.4.1}"; fi
    printf '# Server\r\nredis_version:%s\r\nvalkey_version:%s\r\n' "$v" "$v" ;;
  *"info replication")
    if [ -n "${LINK_DOWN:-}" ]; then
      printf 'role:slave\r\nmaster_link_status:down\r\nmaster_sync_in_progress:1\r\n'
    else
      printf 'role:master\r\nmaster_link_status:up\r\nmaster_sync_in_progress:0\r\n'
    fi ;;
  *"info keyspace") printf '# Keyspace\r\ndb0:keys=3,expires=0,avg_ttl=0\r\ndb2:keys=1,expires=0,avg_ttl=0\r\n' ;;
  *)
    # Commands on standard input, as the script sends the ones that carry
    # the password.
    while IFS= read -r line; do
      if [ "$target" = live ]; then printf '%s\n' "$line" >> "$STUB_STDIN"; fi
      echo OK
    done ;;
esac
`

// runWithStubs runs a script with every client replaced by stubScript, in a
// directory of its own standing in for the workspace.
func runWithStubs(t *testing.T, script string, env stubEnv) stubRun {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range stubTools {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(stubScript), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The scripts work in /work, which a test run as anybody but root cannot
	// create; a directory of its own serves the same.
	script = strings.ReplaceAll(script, workspace, dir)
	if !env.NoArchive {
		var archive bytes.Buffer
		writer := gzip.NewWriter(&archive)
		_, _ = writer.Write([]byte("header and some data\n"))
		_ = writer.Close()
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(dumpFile)), archive.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	logFile, stdinFile, configFile := filepath.Join(dir, "calls"), filepath.Join(dir, "stdin"), filepath.Join(dir, "config")
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"), "TMPDIR="+dir,
		"STUB_LOG="+logFile, "STUB_STDIN="+stdinFile, "STUB_CONFIG="+configFile,
		"DB_NAME=shop", "DB_HOST=db.test", "DB_PORT=6379",
		"DB_USER=shop", "DB_PASSWORD="+env.Password, "POD_IP=10.42.0.9")
	for key, value := range map[string]string{
		"LIVE_VERSION": env.LiveVersion, "HERE_VERSION": env.HereVersion,
		"LINK_DOWN": flag(env.LinkDown), "SOURCE_DIES": flag(env.SourceDies),
	} {
		if value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	// A server a script left running would hold the output open; the script
	// stops its own, and this is the bound if it does not.
	cmd.WaitDelay = 10 * time.Second
	output, err := cmd.CombinedOutput()

	run := stubRun{ok: err == nil, output: string(output), dir: dir}
	logged, _ := os.ReadFile(logFile)
	for _, block := range strings.Split(string(logged), "CALL\n")[1:] {
		var call stubCall
		for _, line := range strings.Split(block, "\n") {
			key, value, _ := strings.Cut(line, " ")
			switch key {
			case "TOOL":
				call.tool = value
			case "ARGS":
				call.args = value
			case "ENV":
				call.env = value
			case "FILE":
				call.file = value
			}
		}
		run.calls = append(run.calls, call)
	}
	if sent, err := os.ReadFile(stdinFile); err == nil {
		run.stdin = strings.Split(strings.TrimSuffix(string(sent), "\n"), "\n")
	}
	config, _ := os.ReadFile(configFile)
	run.config = string(config)
	return run
}

func flag(on bool) string {
	if on {
		return "1"
	}
	return ""
}

func resourceGi(n int64) resource.Quantity {
	return *resource.NewQuantity(n<<30, resource.BinarySI)
}
