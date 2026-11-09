package api

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/dbsvc/engine"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// A new database is backed up.
//
// A schedule used to be something somebody had to go and switch on, on a tab
// nobody had a reason to open, and nothing else ever made one: a database made
// by hand, by a template or with a new app had none, and the first anybody
// heard of it was the day they needed a backup that did not exist. Now a
// database gets a daily one as it is made, whenever there is somewhere to put
// it, and the person creating it can say no.
//
// The check is the settings alone. Reaching the bucket on every database
// created would make creating one as slow and as fragile as the bucket; a
// bucket that turns out to be wrong is reported by the first backup that
// runs, the way a scheduled failure always has been.

// defaultBackupRetention is how many scheduled backups a default schedule
// keeps: a week of them.
const defaultBackupRetention = 7

// Why a new database was not given a schedule, as a word the panel
// translates.
const (
	// backupDeclined: whoever created it said no.
	backupDeclined = "declined"
	// backupNotOffered: the engine is one the panel does not back up.
	backupNotOffered = "not_offered"
	// backupPreview: a pull request's preview, thrown away with it.
	backupPreview = "preview"
	// backupTurnedOff: storage.default_backups is off and nobody asked.
	backupTurnedOff = "turned_off"
	// backupNoStorage: there is nowhere to put a backup.
	backupNoStorage = "no_storage"
	// backupFailed: the panel could not read its settings or save the
	// schedule. Its log says which.
	backupFailed = "failed"
)

// databaseBackup is what creating a database did about its backups.
type databaseBackup struct {
	Enabled bool `json:"enabled"`
	// Reason is why it was not, one of the words above.
	Reason    string `json:"reason,omitempty"`
	Schedule  string `json:"schedule,omitempty"`
	Retention int    `json:"retention,omitempty"`
}

// createdDatabase is a new database, and what was done about its backups.
type createdDatabase struct {
	store.Database
	Backup databaseBackup `json:"backup"`
}

// defaultBackupSchedule is a database's daily schedule: a minute between
// 02:00 and 04:59 UTC worked out from its id, so a panel's databases are not
// all dumped, compressed and uploaded in the same minute, and one database's
// minute is the same whoever asks.
func defaultBackupSchedule(databaseID string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(databaseID))
	minute := hash.Sum32() % (3 * 60)
	return fmt.Sprintf("%d %d * * *", minute%60, 2+minute/60)
}

// defaultBackupPolicy is the schedule a database is given by default.
func defaultBackupPolicy(databaseID string) store.BackupPolicy {
	return store.BackupPolicy{
		TargetType: "database", TargetID: databaseID,
		Schedule: defaultBackupSchedule(databaseID), Retention: defaultBackupRetention,
		Destination: "s3", Enabled: true,
	}
}

// engineBackedUp reports an engine the panel backs up.
func engineBackedUp(name string) bool {
	kind, ok := engine.Lookup(name)
	return ok && kind.Backups
}

// defaultBackupsOn reads storage.default_backups: on unless switched off. A
// setting that cannot be read is the default, which is on.
func (s *Server) defaultBackupsOn(ctx context.Context) bool {
	value, _, err := s.db.GetSetting(ctx, settings.KeyDefaultBackups)
	return err != nil || strings.TrimSpace(value) != "false"
}

// backupStorage says whether backups have somewhere to go. A panel with no
// backup manager has nowhere.
func (s *Server) backupStorage(ctx context.Context) (BackupStorage, error) {
	if s.backups == nil {
		return BackupStorage{}, nil
	}
	return s.backups.Storage(ctx)
}

// applyDefaultBackupPolicy gives a database that was just made its daily
// schedule, unless one of the reasons above says not to, and answers what it
// did. It never fails the creation: the database exists by now, and a
// schedule that could not be saved is said in the answer and in the log.
//
// requested is what the person creating it asked for: nil for nothing, which
// follows storage.default_backups. Asking for one gets one even with the
// default off; it still needs storage, an engine the panel backs up, and an
// environment that is not a preview.
func (s *Server) applyDefaultBackupPolicy(ctx context.Context, env store.Environment, record store.Database, requested *bool) databaseBackup {
	switch {
	case requested != nil && !*requested:
		return databaseBackup{Reason: backupDeclined}
	case env.Kind == store.EnvPreview:
		return databaseBackup{Reason: backupPreview}
	case !engineBackedUp(record.Engine):
		return databaseBackup{Reason: backupNotOffered}
	case requested == nil && !s.defaultBackupsOn(ctx):
		return databaseBackup{Reason: backupTurnedOff}
	}
	storage, err := s.backupStorage(ctx)
	if err != nil {
		s.log.Warn("could not tell whether backup storage is set up; the new database has no schedule",
			"database", record.ID, "error", err)
		return databaseBackup{Reason: backupFailed}
	}
	if !storage.Configured {
		return databaseBackup{Reason: backupNoStorage}
	}
	policy := defaultBackupPolicy(record.ID)
	if err := s.db.SetBackupPolicy(ctx, &policy); err != nil {
		s.log.Warn("could not save the new database's backup schedule", "database", record.ID, "error", err)
		return databaseBackup{Reason: backupFailed}
	}
	return databaseBackup{Enabled: true, Schedule: policy.Schedule, Retention: policy.Retention}
}

// copyBackupPolicy gives a database made as a copy of another — an
// environment cloned — the other's schedule, on or off, rather than the
// default: somebody chose it. A source with none gives the copy none. A
// source on its default minute gives the copy a default minute of its own.
func (s *Server) copyBackupPolicy(ctx context.Context, source, made store.Database) error {
	policy, err := s.db.GetBackupPolicy(ctx, "database", source.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	schedule := policy.Schedule
	if schedule == defaultBackupSchedule(source.ID) {
		schedule = defaultBackupSchedule(made.ID)
	}
	copied := store.BackupPolicy{
		TargetType: "database", TargetID: made.ID, Schedule: schedule,
		Retention: policy.Retention, Destination: policy.Destination, Enabled: policy.Enabled,
	}
	return s.db.SetBackupPolicy(ctx, &copied)
}

// backupStorageView is what anybody signed in may know about backup storage:
// whether there is any, and what a new database gets. Never where it is or
// how it is reached.
type backupStorageView struct {
	BackupStorage
	// DefaultBackups is storage.default_backups: whether a new database is
	// backed up when whoever creates it does not say.
	DefaultBackups bool `json:"default_backups"`
}

// handleGetBackupStorage answers whether backup storage is set up, for the
// pages that offer a backup to say so before one is asked for, and to people
// who cannot open the page it is set on.
func (s *Server) handleGetBackupStorage(w http.ResponseWriter, r *http.Request) {
	storage, err := s.backupStorage(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, backupStorageView{BackupStorage: storage, DefaultBackups: s.defaultBackupsOn(r.Context())})
}

// defaultedDatabase is one database the defaults were applied to.
type defaultedDatabase struct {
	DatabaseID string `json:"database_id"`
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
}

// handleApplyDefaultBackupPolicies gives every database of a team that has
// no schedule at all the daily one a new database gets: for a panel whose
// storage was set up after its databases were made, which therefore have
// none. A schedule somebody turned off is a row, and is left alone; so are
// previews, and engines the panel does not back up.
//
// The storage is checked the way turning on one schedule checks it, once:
// a team's worth of schedules that all fail at three in the morning is worse
// than one refusal now.
func (s *Server) handleApplyDefaultBackupPolicies(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	storage, err := s.backupStorage(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !storage.Configured {
		writeError(w, r, errdoc.StorageNotConfigured())
		return
	}
	if err := s.backups.Verify(r.Context()); err != nil {
		writeError(w, r, errdoc.StorageNotConfigured().
			WithCause("Backup storage is configured but not usable: %s", err.Error()))
		return
	}
	databases, err := s.db.ListDatabasesWithoutBackupPolicy(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	done := []defaultedDatabase{}
	for _, record := range databases {
		if !engineBackedUp(record.Engine) {
			continue
		}
		policy := defaultBackupPolicy(record.ID)
		if err := s.db.SetBackupPolicy(r.Context(), &policy); err != nil {
			writeError(w, r, err)
			return
		}
		s.audit(r, teamID, "backup.policy_changed", "database", record.ID, record.Name)
		done = append(done, defaultedDatabase{DatabaseID: record.ID, Name: record.Name, Schedule: policy.Schedule})
	}
	writeList(w, done)
}
