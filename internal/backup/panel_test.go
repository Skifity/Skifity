package backup

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/crypto"
	"skifity/internal/events"
	"skifity/internal/notify"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// The panel backing itself up to the bucket its other backups go to.

// fakeS3 is enough of an S3 service for the panel's own backups: it keeps what
// is put, forgets what is deleted, and says where the bucket is.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	refuse  bool
}

func (f *fakeS3) start(t *testing.T) *httptest.Server {
	t.Helper()
	f.objects = map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := r.URL.Query()["location"]; ok {
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><LocationConstraint>us-east-1</LocationConstraint>`)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/backups/")
		switch r.Method {
		case http.MethodPut:
			if f.refuse {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`)
				return
			}
			body, _ := io.ReadAll(r.Body)
			if strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
				body = unchunk(body)
			}
			f.objects[key] = body
			w.Header().Set("ETag", `"etag"`)
		case http.MethodDelete:
			delete(f.objects, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (f *fakeS3) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for key := range f.objects {
		out = append(out, key)
	}
	return out
}

// unchunk reads the aws-chunked body a signed streaming upload sends.
func unchunk(body []byte) []byte {
	var out bytes.Buffer
	reader := bufio.NewReader(bytes.NewReader(body))
	for {
		header, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		size, err := strconv.ParseInt(strings.SplitN(strings.TrimSpace(header), ";", 2)[0], 16, 64)
		if err != nil || size == 0 {
			break
		}
		chunk := make([]byte, size)
		if _, err := io.ReadFull(reader, chunk); err != nil {
			break
		}
		out.Write(chunk)
		_, _ = reader.ReadString('\n')
	}
	return out.Bytes()
}

type recordedNotice struct{ team, event, title string }

type recordingNotifier struct {
	mu      sync.Mutex
	notices []recordedNotice
}

func (n *recordingNotifier) Notify(_ context.Context, teamID, event string, msg notify.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notices = append(n.notices, recordedNotice{teamID, event, msg.Title})
}

// panelHarness is a panel with a database on disk, an administrator in a team,
// and backup storage pointing at a fake S3.
func panelHarness(t *testing.T) (*Manager, *store.DB, *fakeS3, *recordingNotifier, string) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key, _ := crypto.GenerateKey()
	keyring, err := crypto.NewKeyring("k1", key)
	if err != nil {
		t.Fatal(err)
	}

	admin := store.User{Email: "admin@example.test", Name: "Admin", PasswordHash: "x", IsAdmin: true}
	if err := db.CreateUser(t.Context(), &admin); err != nil {
		t.Fatal(err)
	}
	team := store.Team{Name: "Ops", Slug: "ops"}
	if err := db.CreateTeam(t.Context(), &team); err != nil {
		t.Fatal(err)
	}
	if err := db.AddMember(t.Context(), team.ID, admin.ID, store.RoleOwner); err != nil {
		t.Fatal(err)
	}

	s3 := &fakeS3{}
	server := s3.start(t)
	for k, v := range map[string]string{
		settings.KeyS3Endpoint: server.URL, settings.KeyS3Bucket: "backups",
		settings.KeyS3AccessKey: "access", settings.KeyS3SecretKey: "secret-key-for-tests",
		settings.KeyS3Region: "us-east-1", settings.KeyS3PathStyle: "true",
	} {
		if err := db.SetSetting(t.Context(), k, v, false, "test"); err != nil {
			t.Fatal(err)
		}
	}
	notifier := &recordingNotifier{}
	m := New(db, keyring, events.NewHub(16), nil, notifier, slog.New(slog.DiscardHandler))
	return m, db, s3, notifier, team.ID
}

func TestThePanelBacksItselfUpToTheBucket(t *testing.T) {
	m, db, s3, _, _ := panelHarness(t)

	backup, err := m.BackupPanel(t.Context(), "manual")
	if err != nil {
		t.Fatalf("BackupPanel: %v", err)
	}
	if backup.Status != "succeeded" || !strings.HasPrefix(backup.Location, "skifity/panel/") || backup.SizeBytes == 0 {
		t.Fatalf("the backup is %+v", backup)
	}

	// What is in the bucket is the database, compressed, and whole.
	s3.mu.Lock()
	stored := s3.objects[backup.Location]
	s3.mu.Unlock()
	zr, err := gzip.NewReader(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("the upload is not gzip: %v", err)
	}
	plain, _ := io.ReadAll(zr)
	if !bytes.HasPrefix(plain, []byte("SQLite format 3\x00")) {
		t.Fatal("the upload is not a SQLite database")
	}

	// And it is on record, where the settings page lists it.
	listed, err := db.ListBackups(t.Context(), PanelTarget, PanelTarget, 10)
	if err != nil || len(listed) != 1 || listed[0].Status != "succeeded" {
		t.Fatalf("the backup is not recorded: %+v %v", listed, err)
	}
}

// Old copies go after a new one succeeds, never before, and two taken in the
// same second are two objects.
func TestOldPanelBackupsAreRemovedAfterANewOne(t *testing.T) {
	m, db, s3, _, _ := panelHarness(t)
	if err := db.SetSetting(t.Context(), settings.KeyPanelBackupKeep, "2", false, "test"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := m.BackupPanel(t.Context(), "manual"); err != nil {
			t.Fatal(err)
		}
	}
	if keys := s3.keys(); len(keys) != 2 {
		t.Fatalf("the bucket holds %d panel backups, want 2: %v", len(keys), keys)
	}
	listed, _ := db.ListBackups(t.Context(), PanelTarget, PanelTarget, 10)
	if len(listed) != 2 {
		t.Fatalf("%d panel backups are on record, want 2", len(listed))
	}
}

// A copy that could not be made is a failure somebody has to hear about, and
// the people who can fix it are the panel's administrators.
func TestAFailedPanelBackupIsRecordedAndTheAdministratorsAreTold(t *testing.T) {
	m, db, s3, notifier, teamID := panelHarness(t)
	s3.refuse = true

	if _, err := m.BackupPanel(t.Context(), "scheduled"); err == nil {
		t.Fatal("a refused upload was reported as a backup")
	}
	listed, _ := db.ListBackups(t.Context(), PanelTarget, PanelTarget, 10)
	if len(listed) != 1 || listed[0].Status != "failed" || listed[0].ErrorMessage == "" {
		t.Fatalf("the failure is not on record: %+v", listed)
	}
	if len(notifier.notices) != 1 || notifier.notices[0].team != teamID ||
		notifier.notices[0].event != notify.EventBackupFailed {
		t.Fatalf("the administrators were not told: %+v", notifier.notices)
	}
}

// With nowhere to put it there is nothing to fail: the scheduled copy is
// quietly not taken, and no record says a backup failed.
func TestNoStorageMeansNoPanelBackupAndNoFailure(t *testing.T) {
	m, db, _, notifier, _ := panelHarness(t)
	if err := db.SetSetting(t.Context(), settings.KeyS3Bucket, "", false, "test"); err != nil {
		t.Fatal(err)
	}
	m.runScheduledPanel(t.Context())
	if listed, _ := db.ListBackups(t.Context(), PanelTarget, PanelTarget, 10); len(listed) != 0 {
		t.Fatalf("a backup was recorded with no storage: %+v", listed)
	}
	if len(notifier.notices) != 0 {
		t.Fatalf("somebody was told about a backup that had nowhere to go: %+v", notifier.notices)
	}
}

func TestWhenThePanelBacksItselfUp(t *testing.T) {
	m, db, _, _, _ := panelHarness(t)
	at := func(clock string) []time.Time {
		minute, _ := time.Parse("15:04", clock)
		return []time.Time{time.Date(2026, 9, 30, minute.Hour(), minute.Minute(), 0, 0, time.UTC)}
	}

	// Nothing set is every day at 03:17.
	if !m.panelDue(t.Context(), at("03:17")) || m.panelDue(t.Context(), at("03:18")) {
		t.Error("the default schedule is not 03:17 every day")
	}
	_ = db.SetSetting(t.Context(), settings.KeyPanelBackupSchedule, "0 */6 * * *", false, "test")
	if !m.panelDue(t.Context(), at("06:00")) || m.panelDue(t.Context(), at("03:17")) {
		t.Error("a schedule that was set is not the one followed")
	}
	_ = db.SetSetting(t.Context(), settings.KeyPanelBackupSchedule, "off", false, "test")
	if m.panelDue(t.Context(), at("03:17")) || m.panelDue(t.Context(), at("06:00")) {
		t.Error("off does not turn it off")
	}
}
