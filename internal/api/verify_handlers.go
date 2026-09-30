package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// Verifying a backup: downloaded, opened when it is sealed, and read through,
// in the background. The outcome is recorded on the backup, where its list
// shows it. Each kind of backup is verified from where its list is, and by
// whoever may already take one.

func (s *Server) handleVerifyDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.startVerification(w, r, "database", record.ID)
}

func (s *Server) handleVerifyVolumeBackup(w http.ResponseWriter, r *http.Request) {
	volume, _, err := s.authorizeVolume(r, store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.startVerification(w, r, "volume", volume.ID)
}

// panelTarget is what the panel's own backups are recorded against: the
// backup package's PanelTarget, which this package cannot import.
const panelTarget = "panel"

func (s *Server) handleVerifyPanelBackup(w http.ResponseWriter, r *http.Request) {
	s.startVerification(w, r, panelTarget, panelTarget)
}

func (s *Server) startVerification(w http.ResponseWriter, r *http.Request, targetType, targetID string) {
	if s.backups == nil {
		writeError(w, r, errdoc.NotConfigured("Backups", "Settings, then Storage"))
		return
	}
	backupID := chi.URLParam(r, "backupID")
	record, err := s.db.GetBackup(r.Context(), backupID)
	// A backup of something else is not one this route may name.
	if err != nil || record.TargetType != targetType || record.TargetID != targetID {
		writeError(w, r, errdoc.NotFound("backup", backupID))
		return
	}
	if err := s.backups.VerifyBackup(r.Context(), record.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"backup_id": record.ID})
}
