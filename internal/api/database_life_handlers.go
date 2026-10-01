package api

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/dbsvc/engine"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// A database's life after it is created: stopped and started, resized, given
// a new password, and given a dump somebody already had. The work is the
// database manager's and the backup manager's; what is here is who may ask,
// and what is refused before either is asked.
//
// Stopping, starting and resizing are a member's, as scaling an app is.
// Changing the password and importing are an administrator's, as reading the
// password and restoring a backup are: the first decides who can sign in to
// the database, and the second replaces what is in it.

// defaultImportLimit is the largest dump one import takes. The panel stages
// it on its own disk before it goes to the bucket, so this is room on that
// disk, not a limit of the database's. internal/backup's DefaultImportLimit
// is the same number, for a caller that sets none.
const defaultImportLimit int64 = 5 << 30

// importReadTime is how long a dump may take to arrive: a few gigabytes on
// an ordinary line is longer than the minute every other request gets.
const importReadTime = 2 * time.Hour

func (s *Server) handleStopDatabase(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// The apps that would lose it are named, and have to be meant: the same
	// guard delete has, for the same reason — a query that failed is a check
	// that did not run, not an empty list.
	links, err := s.db.ListLinksForDatabase(r.Context(), record.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(links) > 0 && !queryBool(r, "force") {
		writeError(w, r, errdoc.DatabaseStopLinked(record.Name, s.linkedAppNames(r, links)))
		return
	}
	if err := s.refuseBusyDatabase(r, record); err != nil {
		writeError(w, r, err)
		return
	}
	if s.databases == nil {
		writeError(w, r, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
		return
	}
	stopped, err := s.databases.Stop(r.Context(), record.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "database.stopped", "database", record.ID, record.Name)
	writeJSON(w, http.StatusOK, stopped)
}

func (s *Server) handleStartDatabase(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.refuseBusyDatabase(r, record); err != nil {
		writeError(w, r, err)
		return
	}
	if s.databases == nil {
		writeError(w, r, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
		return
	}
	started, err := s.databases.Start(r.Context(), record.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "database.started", "database", record.ID, record.Name)
	writeJSON(w, http.StatusAccepted, started)
}

func (s *Server) handleResizeDatabase(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req ResizeDatabaseRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req == (ResizeDatabaseRequest{}) {
		writeError(w, r, errdoc.BadRequest("Say what to change: cpu_request_m, cpu_limit_m, mem_request_mb, mem_limit_mb or storage_gb."))
		return
	}
	// The one refusal that needs nothing but the record, and the one most
	// worth saying at once: a disk does not shrink.
	if req.StorageGB != nil && *req.StorageGB < record.StorageGB {
		writeError(w, r, errdoc.DatabaseStorageShrink(record.Name, record.StorageGB, *req.StorageGB))
		return
	}
	if err := s.refuseBusyDatabase(r, record); err != nil {
		writeError(w, r, err)
		return
	}
	if s.databases == nil {
		writeError(w, r, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
		return
	}
	resized, err := s.databases.Resize(r.Context(), record.ID, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "database.resized", "database", record.ID, fmt.Sprintf(
		"%s: %dm CPU, %d MB memory (limit %d MB), %d GB disk", record.Name,
		resized.CPURequestM, resized.MemRequestMB, resized.MemLimitMB, resized.StorageGB))
	writeJSON(w, http.StatusOK, resized)
}

type databasePasswordRequest struct {
	// Password is the one to give it; empty has one generated.
	Password string `json:"password,omitempty"`
}

func (s *Server) handleChangeDatabasePassword(w http.ResponseWriter, r *http.Request) {
	record, user, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req databasePasswordRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
	}
	kind, known := engine.Lookup(record.Engine)
	if !known || !kind.Password {
		title := record.Engine
		if known {
			title = kind.Title
		}
		writeError(w, r, errdoc.DatabasePasswordNotOffered(record.Name, title))
		return
	}
	if err := s.refuseBusyDatabase(r, record); err != nil {
		writeError(w, r, err)
		return
	}
	if s.databases == nil {
		writeError(w, r, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
		return
	}
	op, err := s.databases.ChangePassword(r.Context(), record.ID, req.Password, user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// The label says whether it was chosen, and never what it is.
	how := "generated"
	if req.Password != "" {
		how = "chosen"
	}
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "database.password_changed", "database", record.ID, record.Name+" ("+how+")")
	writeJSON(w, http.StatusAccepted, op)
}

func (s *Server) handleImportDatabase(w http.ResponseWriter, r *http.Request) {
	record, user, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Everything that can be refused without the file is refused before a
	// byte of it is read, so a large upload is not sent only to be told the
	// database cannot take it.
	kind, known := engine.Lookup(record.Engine)
	if !known || len(kind.Imports) == 0 {
		title := record.Engine
		if known {
			title = kind.Title
		}
		writeError(w, r, errdoc.ImportNotOffered(record.Name, title))
		return
	}
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	if format != "" && !slices.Contains(kind.Imports, format) {
		writeError(w, r, errdoc.ImportFormatInvalid(format, kind.Title, strings.Join(kind.Imports, ", ")))
		return
	}
	limit := s.importLimit
	if limit <= 0 {
		limit = defaultImportLimit
	}
	if r.ContentLength > limit {
		writeError(w, r, errdoc.ImportTooLarge(limit>>20))
		return
	}
	if err := s.refuseBusyDatabase(r, record); err != nil {
		writeError(w, r, err)
		return
	}
	if s.backups == nil {
		writeError(w, r, errdoc.NotConfigured("Backups", "Settings, then Storage"))
		return
	}

	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(importReadTime))
	body := http.MaxBytesReader(w, r.Body, limit+1)
	op, err := s.backups.Import(r.Context(), ImportRequest{
		DatabaseID: record.ID, Dump: body, Limit: limit, Format: format, UserID: user.ID,
	})
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		writeError(w, r, errdoc.ImportTooLarge(limit>>20))
		return
	case err != nil:
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "database.import_started", "database", record.ID, record.Name)
	writeJSON(w, http.StatusAccepted, op)
}

// handleListDatabaseOperations is a database's history: its restores,
// imports and password changes, newest first, each step by step.
func (s *Server) handleListDatabaseOperations(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	operations, err := s.db.ListOperationsForTarget(r.Context(), "database", record.ID, queryInt(r, "limit", 20))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, operations)
}

// refuseBusyDatabase refuses a change while something else is being done to
// a database: it is being created, or restored, imported into or given a new
// password.
func (s *Server) refuseBusyDatabase(r *http.Request, record store.Database) error {
	if record.Status == "creating" {
		return errdoc.DatabaseBusy(record.Name)
	}
	running, err := s.db.OperationRunningFor(r.Context(), "database", record.ID)
	if err != nil {
		return err
	}
	if running {
		return errdoc.DatabaseBusy(record.Name)
	}
	return nil
}

// linkedAppNames names the apps linked to a database, for a refusal that has
// to say which.
func (s *Server) linkedAppNames(r *http.Request, links []store.DatabaseLink) string {
	names := make([]string, 0, len(links))
	for _, link := range links {
		if app, err := s.db.GetApp(r.Context(), link.AppID); err == nil {
			names = append(names, app.Name)
		} else {
			names = append(names, link.AppID)
		}
	}
	return strings.Join(names, ", ")
}
