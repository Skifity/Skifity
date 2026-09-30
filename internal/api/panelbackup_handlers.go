package api

import (
	"context"
	"net/http"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// The panel's own backups: the copies of its database the backup manager
// uploads to the bucket, on a schedule or when an administrator asks.

// panelBackupTarget is the target the backup manager records them under.
const panelBackupTarget = "panel"

func (s *Server) handleListPanelBackups(w http.ResponseWriter, r *http.Request) {
	backups, err := s.db.ListBackups(r.Context(), panelBackupTarget, panelBackupTarget, 30)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if backups == nil {
		backups = []store.Backup{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": backups})
}

// handleBackUpPanel takes a copy now. It waits for the upload, which for a
// panel's database is seconds, and it keeps going if the browser that asked
// goes away: a copy half-uploaded because a tab was closed helps nobody.
func (s *Server) handleBackUpPanel(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		writeError(w, r, errdoc.NotConfigured("Backups", "Settings, then Backup storage"))
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	defer cancel()
	backup, err := s.backups.BackupPanel(ctx, "manual")
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "panel.backed_up", "backup", backup.ID, backup.Location)
	writeJSON(w, http.StatusCreated, backup)
}
