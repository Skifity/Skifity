package backup

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"skifity/internal/api"
	"skifity/internal/dbsvc"
	"skifity/internal/dbsvc/engine"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/runsafe"
	"skifity/internal/sealed"
	"skifity/internal/store"
)

// Importing a dump somebody already has into a managed database.
//
// It is a restore of a file the panel did not make, and it is built from the
// restore's own parts: the file goes to the backup bucket, sealed like a
// backup when backups are sealed; a job in the database's namespace downloads
// it with the same presigned URL a restore uses and loads it with the
// database's own client, a Redis snapshot through the same temporary server a
// restore replicates from. What is different is before and after:
//
//   - Before anything is sent to the cluster, the whole file has been read:
//     its format is known from its first bytes, and a gzipped one has been
//     decompressed to its end, so a truncated upload is refused while the
//     database is still untouched.
//   - Before anything is loaded, a backup of the database is taken and waited
//     for, and the import stops if it fails. That backup is how an import is
//     undone.
//   - The staged file is deleted from the bucket when the import ends,
//     however it ends: it is somebody's data, and not a backup.

// DefaultImportLimit is the largest file one import takes: the panel stages
// it on its own disk, beside its database, before it goes to the bucket. A
// bigger dump is loaded with the engine's own client through
// `skifity db connect`, which has no limit.
const DefaultImportLimit int64 = 5 << 30

// sniffSize is how much of a dump's start is read to tell what it is.
const sniffSize = 64 << 10

// What the start of a file says it is, beside the formats the catalogue
// names. These are never imported; they are recognised so the refusal can
// say what the file was.
const (
	foundTar        = "tar"
	foundPGDumpAll  = "pg_dumpall"
	foundMySQLDump  = "mysql_sql"
	foundPostgreSQL = "postgres_sql"
	foundUnknown    = ""
)

// mongoArchiveMagic is the first four bytes of mongodump --archive: the
// magic number 0x8199e26d, little-endian.
var mongoArchiveMagic = []byte{0x6d, 0xe2, 0x99, 0x81}

// DetectFormat says what a dump is from its first bytes, decompressed.
func DetectFormat(head []byte) string {
	switch {
	case len(head) == 0:
		return foundUnknown
	case bytes.HasPrefix(head, []byte("PGDMP")):
		return engine.FormatCustom
	case bytes.HasPrefix(head, mongoArchiveMagic):
		return engine.FormatArchive
	case bytes.HasPrefix(head, []byte("REDIS")):
		return engine.FormatRDB
	case len(head) >= 262 && bytes.Equal(head[257:262], []byte("ustar")):
		return foundTar
	case !looksLikeText(head):
		return foundUnknown
	}
	// Text. The tools that write SQL say who they are in the first lines, and
	// a dump for the wrong server is refused here rather than by the first
	// statement it chokes on, twenty minutes into the import.
	start := string(head[:min(len(head), 4096)])
	switch {
	case strings.Contains(start, "PostgreSQL database cluster dump"):
		return foundPGDumpAll
	case strings.Contains(start, "-- MySQL dump") || strings.Contains(start, "-- MariaDB dump"):
		return foundMySQLDump
	case strings.Contains(start, "-- PostgreSQL database dump"):
		return foundPostgreSQL
	}
	return engine.FormatSQL
}

// looksLikeText is valid UTF-8 with no NUL in it, allowing for a character
// the sniff cut in half at its end.
func looksLikeText(head []byte) bool {
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	for i := 0; i < 4 && !utf8.Valid(head) && len(head) > 0; i++ {
		head = head[:len(head)-1]
	}
	return utf8.Valid(head)
}

// describeFound is what a file was found to be, for a refusal.
func describeFound(found string) string {
	switch found {
	case engine.FormatCustom:
		return "a pg_dump custom-format archive"
	case engine.FormatArchive:
		return "a mongodump archive"
	case engine.FormatRDB:
		return "a Redis snapshot"
	case foundTar:
		return "a tar archive, such as pg_dump --format=tar writes"
	case foundPGDumpAll:
		return "a dump of a whole PostgreSQL server, from pg_dumpall"
	case foundMySQLDump:
		return "a MySQL or MariaDB dump"
	case foundPostgreSQL:
		return "a PostgreSQL dump"
	case engine.FormatSQL:
		return "SQL"
	}
	return "not a dump"
}

// ResolveFormat decides which format a dump is loaded as: what its start
// says it is, held to what the engine takes, with the one choice the bytes
// cannot make — whether a mongodump archive's collections are gzipped inside
// it — left to whoever named a format.
func ResolveFormat(name, found, requested string) (string, error) {
	kind, ok := engine.Lookup(name)
	if !ok || len(kind.Imports) == 0 {
		return "", errdoc.ImportNotOffered(name, name)
	}
	formats := strings.Join(kind.Imports, ", ")
	if requested != "" && !slices.Contains(kind.Imports, requested) {
		return "", errdoc.ImportFormatInvalid(requested, kind.Title, formats)
	}
	// What is written for one engine and read by another.
	switch {
	case found == foundMySQLDump && (name == engine.MySQL || name == engine.MariaDB):
		found = engine.FormatSQL
	case found == foundPostgreSQL && name == engine.Postgres:
		found = engine.FormatSQL
	}
	if !slices.Contains(kind.Imports, found) {
		if found == foundUnknown {
			return "", errdoc.ImportUnrecognised(kind.Title, formats)
		}
		return "", errdoc.ImportWrongKind(describeFound(found), kind.Title, formats)
	}
	if found == engine.FormatArchive && requested == engine.FormatArchiveGzip {
		return engine.FormatArchiveGzip, nil
	}
	if requested != "" && requested != found {
		return "", errdoc.ImportWrongKind(describeFound(found), kind.Title, formats)
	}
	return found, nil
}

// StagedDump is a dump read to its end and kept, gzipped, on the panel's disk.
type StagedDump struct {
	Path string
	// Found is what its start says it is: DetectFormat's answer.
	Found string
	// Size is the staged file's, and Unpacked what it decompresses to.
	Size     int64
	Unpacked int64
}

// countingReader counts what passes through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// StageDump reads a dump to its end into a gzipped file in dir, and says what
// it is. A file that arrives gzipped is kept as it came, once it has been
// decompressed to its end; any other is compressed on the way in, which is
// the shape the restore job's download already expects.
//
// Nothing about it is trusted before it is read through: its size is counted
// against limit as it arrives, and a gzip stream that ends early or holds
// garbage is refused.
func StageDump(r io.Reader, dir string, limit int64) (StagedDump, error) {
	file, err := os.CreateTemp(dir, ".import-*.gz")
	if err != nil {
		return StagedDump{}, fmt.Errorf("prepare the import: %w", err)
	}
	staged := StagedDump{Path: file.Name()}
	keep := false
	defer func() {
		file.Close()
		if !keep {
			os.Remove(staged.Path)
		}
	}()

	counted := &countingReader{r: io.LimitReader(r, limit+1)}
	reader := bufio.NewReaderSize(counted, sniffSize)
	magic, _ := reader.Peek(2)
	tooLarge := func() error { return errdoc.ImportTooLarge(limit >> 20) }

	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		// Written as it arrives, and decompressed from the same bytes to
		// learn what is inside and that it is whole.
		inflated, err := gzip.NewReader(io.TeeReader(reader, file))
		if err != nil {
			return StagedDump{}, errdoc.ImportDamaged(err.Error())
		}
		head := make([]byte, sniffSize)
		n, err := io.ReadFull(inflated, head)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			if counted.n > limit {
				return StagedDump{}, tooLarge()
			}
			return StagedDump{}, errdoc.ImportDamaged(err.Error())
		}
		rest, err := io.Copy(io.Discard, inflated)
		if counted.n > limit {
			return StagedDump{}, tooLarge()
		}
		if err != nil {
			return StagedDump{}, errdoc.ImportDamaged(err.Error())
		}
		staged.Found = DetectFormat(head[:n])
		staged.Unpacked = int64(n) + rest
	} else {
		head, _ := reader.Peek(sniffSize)
		staged.Found = DetectFormat(head)
		compressor, err := gzip.NewWriterLevel(file, gzip.BestSpeed)
		if err != nil {
			return StagedDump{}, err
		}
		written, err := io.Copy(compressor, reader)
		if counted.n > limit {
			return StagedDump{}, tooLarge()
		}
		if err != nil {
			return StagedDump{}, fmt.Errorf("receive the dump: %w", err)
		}
		if err := compressor.Close(); err != nil {
			return StagedDump{}, fmt.Errorf("stage the dump: %w", err)
		}
		staged.Unpacked = written
	}
	if staged.Unpacked == 0 {
		return StagedDump{}, errdoc.ImportEmpty()
	}
	if err := file.Sync(); err != nil {
		return StagedDump{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return StagedDump{}, err
	}
	staged.Size = info.Size()
	keep = true
	return staged, nil
}

// ImportObjectKey is where a staged dump waits in the bucket for the job
// that loads it: apart from the backups, so nothing that lists or expires
// backups ever takes it for one.
func ImportObjectKey(databaseID string, at time.Time, id string) string {
	return fmt.Sprintf("skifity/imports/%s/%s-%s.gz", databaseID, at.UTC().Format("20060102-150405"), sanitise(id))
}

// importWorkspaceGB is room in the job for the staged file and what it
// decompresses to, with a tenth to spare, and never less than a backup's.
func importWorkspaceGB(staged StagedDump) int {
	need := (staged.Size + staged.Unpacked) * 11 / 10
	return max(int(need>>30)+1, 20)
}

// Import loads a dump into a database, after a backup of what it holds now.
func (m *Manager) Import(ctx context.Context, req api.ImportRequest) (store.Operation, error) {
	if m.cluster == nil {
		return store.Operation{}, errdoc.ClusterUnreachable(nil)
	}
	record, err := m.db.GetDatabase(ctx, req.DatabaseID)
	if err != nil {
		return store.Operation{}, err
	}
	kind, _ := engine.Lookup(record.Engine)
	if len(kind.Imports) == 0 {
		return store.Operation{}, errdoc.ImportNotOffered(record.Name, engineTitle(record.Engine))
	}
	// Everything that can be refused without the file is refused before a
	// byte of it is read.
	if req.Format != "" && !slices.Contains(kind.Imports, req.Format) {
		return store.Operation{}, errdoc.ImportFormatInvalid(req.Format, kind.Title, strings.Join(kind.Imports, ", "))
	}
	if record.Status != "running" && record.Status != "degraded" {
		return store.Operation{}, errdoc.DatabaseNotRunning(record.Name, record.Status)
	}
	if running, err := m.db.OperationRunningFor(ctx, "database", record.ID); err != nil {
		return store.Operation{}, err
	} else if running {
		return store.Operation{}, errdoc.DatabaseBusy(record.Name)
	}
	env, err := m.db.GetEnvironment(ctx, record.EnvironmentID)
	if err != nil {
		return store.Operation{}, err
	}
	teamID, err := m.db.TeamIDForDatabase(ctx, record.ID)
	if err != nil {
		return store.Operation{}, err
	}
	storage, err := LoadStorage(ctx, m.db, m.keyring)
	if err != nil {
		return store.Operation{}, err
	}
	plan, err := m.sealing(ctx)
	if err != nil {
		return store.Operation{}, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = DefaultImportLimit
	}
	// Beside the panel's database: its root filesystem is read-only, and the
	// data directory is the one place with room for a copy.
	staged, err := StageDump(req.Dump, filepath.Dir(m.db.Path()), limit)
	if err != nil {
		return store.Operation{}, err
	}
	defer os.Remove(staged.Path)
	format, err := ResolveFormat(record.Engine, staged.Found, req.Format)
	if err != nil {
		return store.Operation{}, err
	}
	// Sealed here, in the panel's own process, as the panel's own backups
	// are: a dump of somebody's data does not sit in the bucket in the
	// clear when backups do not.
	if plan.passphrase != "" {
		if err := sealed.SealFile(staged.Path, plan.passphrase); err != nil {
			return store.Operation{}, fmt.Errorf("seal the dump: %w", err)
		}
		if info, err := os.Stat(staged.Path); err == nil {
			staged.Size = info.Size()
		}
	}

	key := ImportObjectKey(record.ID, time.Now(), store.NewID("imp"))
	upload, err := os.Open(staged.Path)
	if err != nil {
		return store.Operation{}, err
	}
	err = storage.Put(ctx, key, upload, staged.Size)
	upload.Close()
	if err != nil {
		return store.Operation{}, err
	}

	op := store.Operation{
		TeamID: teamID, Kind: "database.import", TargetType: "database", TargetID: record.ID,
		CreatedBy: req.UserID,
	}
	if err := m.db.CreateOperation(ctx, &op, []string{"upload", "backup", "import", "verify"}); err != nil {
		_ = storage.Remove(ctx, key)
		return store.Operation{}, err
	}
	received, receivedArgs := errdoc.Sprintf("Received %d MB of %s", max(staged.Unpacked>>20, 1), format)
	_ = m.db.SetStepStatus(ctx, op.ID, "upload", store.StepSucceeded,
		store.StepNote{Message: received, Key: "dumpReceived", Args: receivedArgs}, "")

	job := importJob{
		op: op, record: record, env: env, key: key, format: format, plan: plan,
		workspaceGB: importWorkspaceGB(staged),
	}
	background := context.WithoutCancel(ctx)
	runsafe.Go(m.log, "importing into "+record.ID, func() {
		m.runImport(background, storage, job)
	})
	return op, nil
}

// importJob is one import under way.
type importJob struct {
	op          store.Operation
	record      store.Database
	env         store.Environment
	key         string
	format      string
	plan        sealPlan
	workspaceGB int
}

// importBackupPoll is how often the backup taken first is asked after.
var importBackupPoll = 5 * time.Second

func (m *Manager) runImport(ctx context.Context, storage *Storage, job importJob) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()
	op, record := job.op, job.record

	fail := func(step string, err error) {
		problem := errdoc.From(err)
		m.log.Error("import failed", "operation", op.ID, "database", record.ID, "step", step, "error", err)
		_ = m.db.SetStepStatus(ctx, op.ID, step, store.StepFailed,
			store.StepNote{Message: problem.Title, Key: "problem:" + problem.Code, Args: problem.Args.Title},
			problem.Text())
		_ = m.db.SetOperationStatus(ctx, op.ID, store.OpFailed, problem.Code, problem.Error())
		m.hub.Publish(events.OperationTopic(op.ID), "failed", problem)
	}
	defer runsafe.Recover(m.log, "import "+op.ID, func(err error) { fail("import", err) })
	// The dump is somebody's data and not a backup: it goes when the import
	// does, whichever way it went.
	defer func() {
		if err := storage.Remove(ctx, job.key); err != nil {
			m.log.Warn("could not remove an imported dump from the bucket", "operation", op.ID, "error", err)
		}
	}()

	_ = m.db.SetOperationStatus(ctx, op.ID, store.OpRunning, "", "")

	// --- backup: what is there now, so the import can be undone ---
	_ = m.db.SetStepStatus(ctx, op.ID, "backup", store.StepRunning, store.StepNote{}, "")
	taken, err := m.Run(ctx, "database", record.ID, "before_import")
	if err != nil {
		fail("backup", errdoc.ImportBackupFailed(errdoc.From(err).Error()))
		return
	}
	finished, err := m.awaitBackup(ctx, taken.ID)
	if err != nil {
		fail("backup", errdoc.ImportBackupFailed(err.Error()))
		return
	}
	backedUp, backedUpArgs := errdoc.Sprintf("Backed up first, on %s: restore that backup to undo this import",
		finished.CreatedAt.Format("2 January 2006 at 15:04"))
	_ = m.db.SetStepStatus(ctx, op.ID, "backup", store.StepSucceeded,
		store.StepNote{Message: backedUp, Key: "backedUpFirst", Args: backedUpArgs}, "")

	// --- import ---
	_ = m.db.SetStepStatus(ctx, op.ID, "import", store.StepRunning, store.StepNote{}, "")
	presigned, err := storage.PresignGet(ctx, job.key)
	if err != nil {
		fail("import", err)
		return
	}
	jobName := JobName("import-"+record.Slug, op.ID)
	secretName := jobName + "-url"
	applier := m.cluster.Client().Applier()
	defer func() { _ = applier.Delete(ctx, "v1", "Secret", job.env.Namespace, secretName) }()
	if err := applier.Apply(ctx, JobSecret(secretName, job.env.Namespace, presigned, job.plan.passphrase)); err != nil {
		fail("import", err)
		return
	}
	spec, err := BuildJob(ImportJobSpec(jobName, job.env.Namespace, record, secretName, op.ID, job.format,
		job.plan.image, job.workspaceGB))
	if err != nil {
		fail("import", err)
		return
	}
	_ = applier.Delete(ctx, "batch/v1", "Job", job.env.Namespace, jobName)
	if err := applier.Apply(ctx, spec); err != nil {
		fail("import", err)
		return
	}
	m.giveSecretToJob(ctx, job.env.Namespace, secretName, jobName)
	if err := m.waitForJob(ctx, job.env.Namespace, jobName); err != nil {
		reason := err.Error()
		var problem *errdoc.Problem
		if errors.As(err, &problem) && problem.Code == "backup.failed" && len(problem.Args.Cause) > 1 {
			reason = problem.Args.Cause[1]
		}
		fail("import", errdoc.ImportFailed(reason))
		return
	}
	imported, importedArgs := errdoc.Sprintf("Loaded the %s dump into %s", job.format, record.Name)
	_ = m.db.SetStepStatus(ctx, op.ID, "import", store.StepSucceeded,
		store.StepNote{Message: imported, Key: "dumpLoaded", Args: importedArgs}, "")

	// --- verify: the apps reconnect to what is there now ---
	_ = m.db.SetStepStatus(ctx, op.ID, "verify", store.StepRunning, store.StepNote{}, "")
	restarted := m.restartLinkedApps(ctx, job.env.Namespace, record.ID)
	note, args := errdoc.Sprintf("Restarted %d app(s) so they reconnect", restarted)
	_ = m.db.SetStepStatus(ctx, op.ID, "verify", store.StepSucceeded,
		store.StepNote{Message: note, Key: "appsRestarted", Args: args}, "")

	_ = m.db.SetOperationStatus(ctx, op.ID, store.OpSucceeded, "", "")
	m.hub.Publish(events.OperationTopic(op.ID), "operation", op)
	m.log.Info("import finished", "operation", op.ID, "database", record.ID, "format", job.format)
}

// ImportJobSpec is the job that loads an imported dump: a restore, of a file
// the panel did not make, in the format it was found to be.
func ImportJobSpec(name, namespace string, record store.Database, urlSecret, operationID, format, sealImage string, workspaceGB int) JobSpec {
	return JobSpec{
		Name:              name,
		Namespace:         namespace,
		Engine:            record.Engine,
		Version:           record.EngineVersion,
		CredentialsSecret: kube.ResourceName(record.Slug, "credentials"),
		URLSecret:         urlSecret,
		BackupID:          operationID,
		Restore:           true,
		Format:            format,
		SealImage:         sealImage,
		WorkspaceGB:       workspaceGB,
	}
}

// awaitBackup waits for a backup to finish, and answers why when it did not.
func (m *Manager) awaitBackup(ctx context.Context, backupID string) (store.Backup, error) {
	deadline := time.Now().Add(3 * time.Hour)
	for {
		backup, err := m.db.GetBackup(ctx, backupID)
		if err != nil {
			return store.Backup{}, err
		}
		switch backup.Status {
		case "succeeded":
			return backup, nil
		case "running":
		default:
			reason := strings.TrimSpace(backup.ErrorMessage)
			if reason == "" {
				reason = "the backup did not finish"
			}
			return store.Backup{}, errors.New(reason)
		}
		if time.Now().After(deadline) {
			return store.Backup{}, errors.New("the backup did not finish within three hours")
		}
		select {
		case <-ctx.Done():
			return store.Backup{}, ctx.Err()
		case <-time.After(importBackupPoll):
		}
	}
}

// restartLinkedApps restarts the apps that use a database, so a connection
// pool holding a transaction against what was there before is let go, and
// answers how many were.
func (m *Manager) restartLinkedApps(ctx context.Context, namespace, databaseID string) int {
	links, err := m.db.ListLinksForDatabase(ctx, databaseID)
	if err != nil {
		return 0
	}
	restarted := 0
	for _, link := range links {
		app, err := m.db.GetApp(ctx, link.AppID)
		if err != nil {
			continue
		}
		if err := m.cluster.RestartApp(ctx, namespace, app.Slug); err != nil {
			m.log.Warn("could not restart an app after the import", "app", app.ID, "error", err)
			continue
		}
		restarted++
	}
	return restarted
}

// importLoad is the command that loads an imported dump, by format.
func importLoad(s JobSpec, unpacked string) string {
	switch {
	case s.Engine == dbsvc.EnginePostgres && s.Format == engine.FormatCustom:
		// pg_restore reads the file by name, which is not a secret. In one
		// transaction and stopping at the first error, so a dump that does
		// not load leaves nothing of itself behind. --clean --if-exists
		// replaces the objects it holds; --no-owner and --no-privileges
		// because the roles of the server it came from do not exist here.
		return `PGPASSWORD="$DB_PASSWORD" pg_restore --host="$DB_HOST" --port="$DB_PORT" ` +
			`--username="$DB_USER" --dbname="$DB_NAME" --no-owner --no-privileges --clean --if-exists ` +
			`--single-transaction --exit-on-error "` + unpacked + `"`
	case s.Engine == dbsvc.EnginePostgres:
		// One transaction, as for pg_restore: a statement that fails undoes
		// every one before it.
		return `PGPASSWORD="$DB_PASSWORD" psql --host="$DB_HOST" --port="$DB_PORT" ` +
			`--username="$DB_USER" --dbname="$DB_NAME" --quiet --single-transaction --set ON_ERROR_STOP=on < ` + unpacked
	case s.Engine == dbsvc.EngineMySQL:
		return `MYSQL_PWD="$DB_PASSWORD" mysql --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" "$DB_NAME" < ` + unpacked
	case s.Engine == dbsvc.EngineMariaDB:
		return `MYSQL_PWD="$DB_PASSWORD" mariadb --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" "$DB_NAME" < ` + unpacked
	case s.Engine == dbsvc.EngineMongoDB:
		// A dump from elsewhere names its own database, which is not this
		// one's: every collection is renamed into this database, and the
		// server's own databases are left out before anything is renamed.
		gzipped := ""
		if s.Format == engine.FormatArchiveGzip {
			gzipped = " --gzip"
		}
		return mongoConfig + `mongorestore ` + mongoFlags +
			` --nsExclude='admin.*' --nsExclude='config.*' --nsExclude='local.*'` +
			` --nsFrom='$db$.$collection$' --nsTo="$DB_NAME"'.$collection$'` +
			` --drop --archive` + gzipped + ` < ` + unpacked
	case redisFamily(s.Engine):
		return redisRestore(s.Engine, unpacked)
	}
	return `echo "This dump cannot be loaded here." >&2; exit 1`
}
