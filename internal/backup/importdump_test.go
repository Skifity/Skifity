package backup

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/dbsvc/engine"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// Importing a dump: what a file is, what is refused before anything changes,
// and the job that loads what is accepted.

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func problemCode(err error) string {
	var problem *errdoc.Problem
	if errors.As(err, &problem) {
		return problem.Code
	}
	return ""
}

func TestADumpIsKnownByItsFirstBytes(t *testing.T) {
	tar := make([]byte, 600)
	copy(tar[257:], "ustar")
	cases := map[string]struct {
		head []byte
		want string
	}{
		"plain SQL":           {[]byte("CREATE TABLE orders (id int);\n"), engine.FormatSQL},
		"pg_dump's own SQL":   {[]byte("--\n-- PostgreSQL database dump\n--\nSET x = 1;\n"), foundPostgreSQL},
		"pg_dumpall":          {[]byte("--\n-- PostgreSQL database cluster dump\n--\n"), foundPGDumpAll},
		"mysqldump":           {[]byte("-- MySQL dump 10.13  Distrib 8.4.0\n"), foundMySQLDump},
		"mariadb-dump":        {[]byte("-- MariaDB dump 10.19\n"), foundMySQLDump},
		"pg_dump custom":      {[]byte("PGDMP\x01\x0e\x00"), engine.FormatCustom},
		"mongodump archive":   {append([]byte{0x6d, 0xe2, 0x99, 0x81}, 0x10, 0, 0, 0), engine.FormatArchive},
		"redis snapshot":      {[]byte("REDIS0011\xfa\x09redis-ver"), engine.FormatRDB},
		"tar":                 {tar, foundTar},
		"binary":              {[]byte{0x00, 0x01, 0x02, 0xff}, foundUnknown},
		"nothing":             {nil, foundUnknown},
		"cut mid-character":   {[]byte("INSERT INTO t VALUES ('caf\xc3"), engine.FormatSQL},
		"a sqlite file, say":  {[]byte("SQLite format 3\x00"), foundUnknown},
		"mysqldump, as stdin": {[]byte("/*!40101 SET NAMES utf8 */;\n"), engine.FormatSQL},
	}
	for name, c := range cases {
		if got := DetectFormat(c.head); got != c.want {
			t.Errorf("%s: detected %q, want %q", name, got, c.want)
		}
	}
}

func TestAFormatIsHeldToWhatTheEngineLoads(t *testing.T) {
	cases := []struct {
		engine, found, requested string
		want, code               string
	}{
		{engine.Postgres, engine.FormatSQL, "", engine.FormatSQL, ""},
		{engine.Postgres, foundPostgreSQL, "", engine.FormatSQL, ""},
		{engine.Postgres, engine.FormatCustom, "", engine.FormatCustom, ""},
		{engine.Postgres, foundMySQLDump, "", "", "import.wrong_kind"},
		{engine.Postgres, foundPGDumpAll, "", "", "import.wrong_kind"},
		{engine.Postgres, foundTar, "", "", "import.wrong_kind"},
		{engine.Postgres, engine.FormatRDB, "", "", "import.wrong_kind"},
		{engine.Postgres, foundUnknown, "", "", "import.unrecognised"},
		{engine.Postgres, engine.FormatSQL, engine.FormatCustom, "", "import.wrong_kind"},
		{engine.Postgres, engine.FormatSQL, engine.FormatRDB, "", "import.format_invalid"},
		{engine.MySQL, foundMySQLDump, "", engine.FormatSQL, ""},
		{engine.MariaDB, engine.FormatSQL, "", engine.FormatSQL, ""},
		{engine.MySQL, foundPostgreSQL, "", "", "import.wrong_kind"},
		{engine.MySQL, engine.FormatCustom, "", "", "import.wrong_kind"},
		{engine.MongoDB, engine.FormatArchive, "", engine.FormatArchive, ""},
		{engine.MongoDB, engine.FormatArchive, engine.FormatArchiveGzip, engine.FormatArchiveGzip, ""},
		{engine.MongoDB, engine.FormatSQL, "", "", "import.wrong_kind"},
		{engine.Redis, engine.FormatRDB, "", engine.FormatRDB, ""},
		{engine.Valkey, engine.FormatRDB, "", engine.FormatRDB, ""},
		{engine.Dragonfly, engine.FormatRDB, "", "", "import.not_offered"},
		{engine.Memcached, engine.FormatSQL, "", "", "import.not_offered"},
	}
	for _, c := range cases {
		got, err := ResolveFormat(c.engine, c.found, c.requested)
		if c.code != "" {
			if problemCode(err) != c.code {
				t.Errorf("%s, found %q, asked %q: %v, want %s", c.engine, c.found, c.requested, err, c.code)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s, found %q, asked %q: %q, %v; want %q", c.engine, c.found, c.requested, got, err, c.want)
		}
	}
}

func readStaged(t *testing.T, staged StagedDump) []byte {
	t.Helper()
	file, err := os.Open(staged.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("the staged file is not gzipped: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A plain file is compressed on the way in and a gzipped one kept as it
// came; either way the job downloads what a restore downloads.
func TestADumpIsStagedGzippedAndWhole(t *testing.T) {
	dir := t.TempDir()
	plain := []byte(strings.Repeat("INSERT INTO orders VALUES (1);\n", 1000))

	staged, err := StageDump(bytes.NewReader(plain), dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if staged.Found != engine.FormatSQL || staged.Unpacked != int64(len(plain)) {
		t.Errorf("a plain dump staged as %+v", staged)
	}
	if !bytes.Equal(readStaged(t, staged), plain) {
		t.Error("the staged dump does not decompress to what was sent")
	}

	custom := append([]byte("PGDMP\x01\x0e\x00"), bytes.Repeat([]byte{0, 1, 2, 3}, 5000)...)
	sent := gzipped(t, custom)
	staged, err = StageDump(bytes.NewReader(sent), dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	kept, _ := os.ReadFile(staged.Path)
	if staged.Found != engine.FormatCustom || !bytes.Equal(kept, sent) || staged.Unpacked != int64(len(custom)) {
		t.Errorf("a gzipped custom archive staged as %+v, kept as sent: %v", staged, bytes.Equal(kept, sent))
	}
}

// What would fail halfway through loading is refused while the database is
// untouched, and leaves nothing on the panel's disk.
func TestABrokenDumpIsRefusedBeforeAnythingChanges(t *testing.T) {
	dir := t.TempDir()
	whole := gzipped(t, bytes.Repeat([]byte("INSERT INTO t VALUES (1);\n"), 4000))
	cases := map[string]struct {
		body  []byte
		limit int64
		code  string
	}{
		"cut short":         {whole[:len(whole)/2], 1 << 20, "import.damaged"},
		"garbage after it":  {append(append([]byte{}, whole...), []byte("not gzip")...), 1 << 20, "import.damaged"},
		"too large":         {bytes.Repeat([]byte("x"), 2048), 1024, "import.too_large"},
		"too large, zipped": {whole, 64, "import.too_large"},
		"empty":             {nil, 1 << 20, "import.empty"},
		"empty, zipped":     {gzipped(t, nil), 1 << 20, "import.empty"},
	}
	for name, c := range cases {
		_, err := StageDump(bytes.NewReader(c.body), dir, c.limit)
		if problemCode(err) != c.code {
			t.Errorf("%s: %v, want %s", name, err, c.code)
		}
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(left) != 0 {
		t.Errorf("a refused dump left %v behind", left)
	}
}

// One job per engine and format, built from the restore's own parts, with
// every credential from a Secret and the loader the format needs.
func TestAnImportJobLoadsEachFormatWithItsOwnTool(t *testing.T) {
	cases := []struct {
		engine, format string
		want, avoid    []string
	}{
		{engine.Postgres, engine.FormatSQL,
			[]string{"psql", "--single-transaction", "ON_ERROR_STOP=on"}, []string{"pg_restore"}},
		{engine.Postgres, engine.FormatCustom,
			[]string{"pg_restore", "--single-transaction", "--exit-on-error", "--no-owner", "--no-privileges", "--clean --if-exists"},
			[]string{"psql "}},
		{engine.MySQL, engine.FormatSQL, []string{`MYSQL_PWD="$DB_PASSWORD" mysql `}, nil},
		{engine.MariaDB, engine.FormatSQL, []string{`MYSQL_PWD="$DB_PASSWORD" mariadb `}, nil},
		{engine.MongoDB, engine.FormatArchive,
			[]string{"mongorestore", "--nsFrom='$db$.$collection$'", `--nsTo="$DB_NAME"'.$collection$'`,
				"--nsExclude='admin.*'", "--drop", "--config="},
			[]string{"--gzip", "--password"}},
		{engine.MongoDB, engine.FormatArchiveGzip, []string{"mongorestore", "--gzip"}, nil},
		{engine.Redis, engine.FormatRDB, []string{"redis-server", "REPLICAOF"}, nil},
		{engine.Valkey, engine.FormatRDB, []string{"valkey-server", "REPLICAOF"}, nil},
	}
	record := store.Database{ID: "db_1", Name: "orders", Slug: "orders", EngineVersion: ""}
	for _, c := range cases {
		record.Engine = c.engine
		job, err := BuildJob(ImportJobSpec("import-orders-abc", "acme-shop-production", record,
			"import-orders-abc-url", "op_1", c.format, "", 40))
		if err != nil {
			t.Fatalf("%s %s: %v", c.engine, c.format, err)
		}
		download, load := scriptsOf(t, job)
		if !strings.Contains(download, "curl") {
			t.Errorf("%s %s does not download the staged dump", c.engine, c.format)
		}
		for _, want := range c.want {
			if !strings.Contains(load, want) {
				t.Errorf("%s %s: the load does not say %q:\n%s", c.engine, c.format, want, load)
			}
		}
		for _, avoid := range c.avoid {
			if strings.Contains(load, avoid) {
				t.Errorf("%s %s: the load says %q", c.engine, c.format, avoid)
			}
		}
		pod := job.Spec.Template.Spec
		for _, container := range append(append(pod.InitContainers[:0:0], pod.InitContainers...), pod.Containers...) {
			for _, env := range container.Env {
				if env.Value != "" || (env.ValueFrom != nil && env.ValueFrom.SecretKeyRef == nil && env.ValueFrom.FieldRef == nil) {
					t.Errorf("%s %s: %s is not from a Secret", c.engine, c.format, env.Name)
				}
			}
		}
		if size := pod.Volumes[0].EmptyDir.SizeLimit.String(); size != "40Gi" {
			t.Errorf("%s %s: the workspace is %s, want the room the dump needs", c.engine, c.format, size)
		}
		checkShell(t, c.engine+" "+c.format, job)
	}
}

// The workspace has room for the staged file and what it decompresses to.
func TestAnImportHasRoomForItsDump(t *testing.T) {
	if got := importWorkspaceGB(StagedDump{Size: 1 << 20, Unpacked: 10 << 20}); got != 20 {
		t.Errorf("a small dump gets %d GB, want the backup's 20", got)
	}
	if got := importWorkspaceGB(StagedDump{Size: 4 << 30, Unpacked: 30 << 30}); got < 38 {
		t.Errorf("4 GB that decompress to 30 get %d GB", got)
	}
}

// A plain dump's load, run with a stand-in psql: it is handed the file on its
// standard input, inside one transaction, with the password in its own
// variable and nowhere on its command line.
func TestAnImportedSQLDumpReachesPsqlWithoutItsPassword(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}
	const password = "planted-import-password-77"
	job, err := BuildJob(ImportJobSpec("import-orders-abc", "ns", store.Database{Slug: "orders", Engine: engine.Postgres},
		"url", "op_1", engine.FormatSQL, "", 20))
	if err != nil {
		t.Fatal(err)
	}
	_, load := scriptsOf(t, job)
	dir := t.TempDir()
	stub := "#!/bin/sh\nprintf 'ARGS %s\\n' \"$*\" > " + dir + "/seen\nprintf 'PASSWORD %s\\n' \"$PGPASSWORD\" >> " + dir + "/seen\ncat >> " + dir + "/seen\n"
	if err := os.WriteFile(filepath.Join(dir, "psql"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// The job's workspace, here.
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "dump.gz"), gzipped(t, []byte("CREATE TABLE t (id int);\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", strings.ReplaceAll(load, "/work/", work+"/"))
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "DB_PASSWORD="+password,
		"DB_HOST=db", "DB_PORT=5432", "DB_USER=app", "DB_NAME=app")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the load failed: %v\n%s", err, output)
	}
	seen, _ := os.ReadFile(filepath.Join(dir, "seen"))
	args, rest, _ := strings.Cut(string(seen), "\n")
	if strings.Contains(args, password) || !strings.Contains(args, "--single-transaction") {
		t.Errorf("psql was started as %s", args)
	}
	if !strings.Contains(rest, "PASSWORD "+password) || !strings.Contains(rest, "CREATE TABLE t") {
		t.Errorf("psql was not given the password in its variable and the dump on its input:\n%s", rest)
	}
}

// A scheduled backup of a stopped database is skipped and says why, and a
// month of them is one row.
func TestAStoppedDatabasesScheduledBackupIsSkipped(t *testing.T) {
	m, db, _, _, teamID := panelHarness(t)
	ctx := t.Context()
	record := catchUpDatabase(t, db, teamID, "shop")
	if err := db.SetDatabaseStatus(ctx, record.ID, "stopped", ""); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		skipped, err := m.Run(ctx, "database", record.ID, "scheduled")
		if err != nil {
			t.Fatalf("a stopped database's scheduled backup failed: %v", err)
		}
		if skipped.Status != "skipped" || !strings.Contains(skipped.ErrorMessage, "stopped") {
			t.Fatalf("it was recorded as %+v", skipped)
		}
	}
	backups, err := db.ListBackups(ctx, "database", record.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || backups[0].Status != "skipped" {
		raw, _ := json.Marshal(backups)
		t.Fatalf("three skips are recorded as %s", raw)
	}
	// One asked for by hand is not skipped: it is refused, as for any
	// database that is not running.
	if _, err := m.Run(ctx, "database", record.ID, "manual"); err == nil {
		t.Fatal("a backup by hand of a stopped database was taken")
	}
}
