package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/cron"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/plugins"
	"skifity/internal/store"
)

func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	env, _, err := s.authorizeEnvironment(r, chi.URLParam(r, "envID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	databases, err := s.db.ListDatabases(r.Context(), env.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, databases)
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	env, _, err := s.authorizeEnvironment(r, chi.URLParam(r, "envID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req CreateDatabaseRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	switch req.Engine {
	case "postgres", "redis", "mysql":
	default:
		writeError(w, r, errdoc.BadRequest("Engine must be postgres, redis or mysql."))
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, r, errdoc.BadRequest("A database needs a name."))
		return
	}
	// After the request is checked, not before: somebody fixing a typo should
	// not then discover that databases are not configured, and a check that
	// only runs on a configured panel cannot be tested without one.
	if s.databases == nil {
		writeError(w, r, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
		return
	}

	record, err := s.databases.Create(r.Context(), env, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForEnvironment(r.Context(), env.ID)
	s.audit(r, teamID, "database.created", "database", record.ID, record.Name)
	s.plugins.Notify(r.Context(), plugins.EventDatabaseCreated, teamID, map[string]any{
		"database_id": record.ID, "database": record.Name,
		"engine": record.Engine, "environment_id": record.EnvironmentID,
	})
	writeJSON(w, http.StatusAccepted, record)
}

func (s *Server) handleGetDatabase(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Refresh from the cluster so a database that finished provisioning while
	// nobody was looking does not stay stuck at "creating".
	if s.databases != nil {
		if status, detail, err := s.databases.Status(r.Context(), record.ID); err == nil && status != record.Status {
			record.Status, record.StatusDetail = status, detail
			_ = s.db.SetDatabaseStatus(r.Context(), record.ID, status, detail)
		}
	}
	links, _ := s.db.ListLinksForDatabase(r.Context(), record.ID)
	writeJSON(w, http.StatusOK, map[string]any{"database": record, "links": links})
}

func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Deleting a database that apps still use breaks them silently, so say so.
	// Before the capability check, not after: a safety check that only runs on
	// a configured panel is one nothing can test without a cluster, which is
	// how this class of mistake keeps surviving review.
	//
	// A query that failed is not "no apps use it": it is a check that did not
	// run, and treating it as permission to delete is how a guard stops
	// guarding exactly when the panel is already unwell.
	links, err := s.db.ListLinksForDatabase(r.Context(), record.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(links) > 0 && !queryBool(r, "force") {
		writeError(w, r, errdoc.New("database.still_linked", "This database is still used by an app").
			WithCause("%d app(s) have a connection string from this database injected.", len(links)).
			WithImpact("Nothing was changed.").
			WithFix("Unlink the apps first, or confirm that you want to delete it anyway.").
			WithStatus(http.StatusConflict))
		return
	}
	// Which team this belongs to is resolved through the database's own row,
	// so it has to be asked before that row is gone. It used to be asked
	// afterwards, which meant the audit event for deleting a database — the one
	// event nobody can afford to lose — was filed against no team at all, and
	// the team's audit log never showed it.
	teamID, err := s.db.TeamIDForDatabase(r.Context(), record.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	if s.databases == nil {
		writeError(w, r, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
		return
	}
	if err := s.databases.Delete(r.Context(), record.ID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "database.deleted", "database", record.ID, record.Name)
	writeOK(w)
}

func (s *Server) handleDatabaseCredentials(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.databases == nil {
		writeError(w, r, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
		return
	}
	credentials, err := s.databases.Credentials(r.Context(), record.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Reading a password is worth an audit entry of its own.
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "database.credentials_viewed", "database", record.ID, record.Name)
	writeJSON(w, http.StatusOK, credentials)
}

type linkDatabaseRequest struct {
	AppID   string `json:"app_id"`
	VarName string `json:"var_name,omitempty"`
}

func (s *Server) handleLinkDatabase(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req linkDatabaseRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	// The app is authorized separately: linking reaches into two resources.
	//
	// Before the capability check below, not after. Telling somebody who may
	// not touch this app whether managed databases are configured is a small
	// thing to leak, and the order costs nothing.
	app, _, err := s.authorizeAppID(r, req.AppID, store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.databases == nil {
		writeError(w, r, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
		return
	}
	if app.EnvironmentID != record.EnvironmentID {
		writeError(w, r, errdoc.BadRequest("An app can only be linked to a database in the same environment."))
		return
	}

	varName := strings.TrimSpace(req.VarName)
	if varName == "" {
		varName = defaultVarNameFor(record.Engine)
	}
	if _, err := kube.SanitiseEnvKey(varName); err != nil {
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}
	if err := s.databases.Link(r.Context(), record.ID, app.ID, varName); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "database.linked", "database", record.ID, app.Name+" as "+varName)
	writeOK(w)
}

func (s *Server) handleUnlinkDatabase(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// The app is authorized separately, the way linking already did it, because
	// unlinking reaches into two resources as well. Without this, an id from
	// another team reached Unlink, which removes no variable it does not own
	// but does re-apply that app's configuration to the cluster: a rollout
	// somebody else's team did not ask for.
	app, _, err := s.authorizeAppID(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.databases == nil {
		writeError(w, r, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
		return
	}
	if err := s.databases.Unlink(r.Context(), record.ID, app.ID); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "database.unlinked", "database", record.ID, app.Name)
	writeOK(w)
}

func defaultVarNameFor(engine string) string {
	switch engine {
	case "redis":
		return "REDIS_URL"
	case "mysql":
		return "MYSQL_URL"
	default:
		return "DATABASE_URL"
	}
}

// --- backups ---

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	backups, err := s.db.ListBackups(r.Context(), "database", record.ID, queryInt(r, "limit", 50))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, backups)
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.backups == nil {
		writeError(w, r, errdoc.NotConfigured("Backups", "Settings, then Storage"))
		return
	}
	backup, err := s.backups.Run(r.Context(), "database", record.ID, "manual")
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "backup.started", "database", record.ID, record.Name)
	writeJSON(w, http.StatusAccepted, backup)
}

// backupPolicyFrom validates a schedule request and builds the policy.
//
// Shared by databases and volumes so the two cannot drift: the checks below
// are the difference between a schedule that runs and one that is stored,
// never matches, and silently never backs anything up.
func (s *Server) backupPolicyFrom(r *http.Request, req setBackupPolicyRequest, targetType, targetID string) (store.BackupPolicy, error) {
	if req.Enabled {
		if s.backups == nil {
			return store.BackupPolicy{}, errdoc.StorageNotConfigured()
		}
		// Fail now, with a clear message, rather than at three in the morning.
		if err := s.backups.Verify(r.Context()); err != nil {
			return store.BackupPolicy{}, errdoc.StorageNotConfigured().
				WithCause("Backup storage is configured but not usable: %s", err.Error())
		}
	}
	if req.Retention < 1 {
		req.Retention = 7
	}
	schedule := defaultString(strings.TrimSpace(req.Schedule), "0 3 * * *")
	// The same reason storage is verified above: a schedule nothing can parse
	// is accepted, stored, and then never matches, so the backups simply do not
	// happen and nothing anywhere says why. The scheduler cannot report it —
	// by then it is a row that is never due.
	if _, err := cron.ParseSchedule(schedule); err != nil {
		return store.BackupPolicy{}, errdoc.New("backup.bad_schedule", "That is not a schedule Skifity understands").
			WithCause("%s", err).
			WithImpact("The schedule was not changed.").
			WithFix("Use five cron fields, for example \"0 3 * * *\" for every day at 03:00 UTC.").
			WithStatus(http.StatusBadRequest)
	}
	return store.BackupPolicy{
		TargetType: targetType, TargetID: targetID,
		Schedule: schedule, Retention: req.Retention,
		Destination: "s3", Enabled: req.Enabled,
	}, nil
}

func (s *Server) handleGetBackupPolicy(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	policy, err := s.db.GetBackupPolicy(r.Context(), "database", record.ID)
	if err != nil {
		// No policy is a normal state, not an error: show the defaults.
		writeJSON(w, http.StatusOK, store.BackupPolicy{
			TargetType: "database", TargetID: record.ID,
			Schedule: "0 3 * * *", Retention: 7, Destination: "s3", Enabled: false,
		})
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

type setBackupPolicyRequest struct {
	Schedule  string `json:"schedule"`
	Retention int    `json:"retention"`
	Enabled   bool   `json:"enabled"`
}

func (s *Server) handleSetBackupPolicy(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setBackupPolicyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	policy, err := s.backupPolicyFrom(r, req, "database", record.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.SetBackupPolicy(r.Context(), &policy); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "backup.policy_changed", "database", record.ID, record.Name)
	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.backups == nil {
		writeError(w, r, errdoc.NotConfigured("Backups", "Settings, then Storage"))
		return
	}
	backupID := chi.URLParam(r, "backupID")
	backup, err := s.db.GetBackup(r.Context(), backupID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// A backup id from another database must not be restorable here.
	if backup.TargetType != "database" || backup.TargetID != record.ID {
		writeError(w, r, errdoc.NotFound("backup", backupID))
		return
	}
	if backup.Status != "succeeded" {
		writeError(w, r, errdoc.BadRequest("That backup did not finish successfully, so it cannot be restored."))
		return
	}

	op, err := s.backups.Restore(r.Context(), backupID, queryBool(r, "overwrite"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "backup.restore_started", "database", record.ID, backupID)
	writeJSON(w, http.StatusAccepted, op)
}

// authorizeAppID is authorizeApp for an id that comes from a request body
// rather than the URL.
func (s *Server) authorizeAppID(r *http.Request, appID string, required store.Role) (store.App, store.User, error) {
	teamID, err := s.db.TeamIDForApp(r.Context(), appID)
	if err != nil {
		return store.App{}, store.User{}, errdoc.NotFound("app", appID)
	}
	user, err := s.authorizeTeam(r, teamID, required)
	if err != nil {
		return store.App{}, store.User{}, err
	}
	app, err := s.db.GetApp(r.Context(), appID)
	if err != nil {
		return store.App{}, store.User{}, errdoc.NotFound("app", appID)
	}
	return app, user, nil
}
