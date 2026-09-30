package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// Two views of the cluster that the Advanced tab shows beside the manifests:
// what somebody changed outside the panel, with the way to put it back, and
// what Kubernetes itself said about the app's objects.

// handleAppDrift compares the app's objects in the cluster with what the
// panel applies, now. A viewer may read it: it is the app's state, as much as
// its instances are.
func (s *Server) handleAppDrift(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.drift == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	report, err := s.drift.CheckDrift(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.withStoredDrift(r, app.ID, &report)
	writeJSON(w, http.StatusOK, report)
}

// withStoredDrift adds what the watcher remembers to a report read now: when
// the app stopped matching, and whether it is put back by itself.
func (s *Server) withStoredDrift(r *http.Request, appID string, report *DriftReport) {
	stored, err := s.db.GetAppDrift(r.Context(), appID)
	if err != nil {
		s.log.Warn("could not read what the watcher found", "app", appID, "error", err)
		return
	}
	report.AutoRepair = stored.AutoRepair
	if drifted(report.Status) && drifted(stored.Status) {
		report.Since = stored.Since
	}
}

func drifted(status string) bool {
	return status == store.DriftDrifted || status == store.DriftMissing
}

type setDriftRequest struct {
	AutoRepair *bool `json:"auto_repair"`
}

// handleSetDriftRepair turns putting the app's objects back by itself on or
// off. Off is the default: a change somebody made on purpose, in the middle of
// an incident, is not something to undo behind their back.
func (s *Server) handleSetDriftRepair(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setDriftRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.AutoRepair == nil {
		writeError(w, r, errdoc.BadRequest("Say whether to put the app back automatically, with auto_repair: true or false."))
		return
	}
	if err := s.db.SetDriftAutoRepair(r.Context(), app.ID, *req.AutoRepair); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	if *req.AutoRepair {
		s.audit(r, teamID, "app.drift_auto_repair_on", "app", app.ID, app.Name)
	} else {
		s.audit(r, teamID, "app.drift_auto_repair_off", "app", app.ID, app.Name)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"auto_repair": *req.AutoRepair})
}

// handleRepairDrift puts the app's objects back: the same apply a change to a
// variable makes. Nothing is built and no deployment is recorded; the version
// that is running is the version that runs.
func (s *Server) handleRepairDrift(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.drift == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	if err := s.drift.RepairDrift(r.Context(), app.ID); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.drift_repaired", "app", app.ID, app.Name)

	// What it looks like now, so the page does not have to ask again.
	report, err := s.drift.CheckDrift(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.withStoredDrift(r, app.ID, &report)
	writeJSON(w, http.StatusOK, report)
}

// handleAppEvents lists what Kubernetes said about the app's objects, newest
// first. ?type=Warning keeps only the warnings.
func (s *Server) handleAppEvents(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	env, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	events, err := s.cluster.AppEvents(r.Context(), app, env)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ofType(events, r.URL.Query().Get("type"))})
}

// handleDatabaseEvents is the same for a managed database.
func (s *Server) handleDatabaseEvents(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	env, err := s.db.GetEnvironment(r.Context(), record.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	events, err := s.cluster.DatabaseEvents(r.Context(), record, env)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ofType(events, r.URL.Query().Get("type"))})
}

// ofType keeps the events of one type, Normal or Warning, whatever its case.
// Empty keeps them all.
func ofType(events []ObjectEvent, kind string) []ObjectEvent {
	out := make([]ObjectEvent, 0, len(events))
	for _, event := range events {
		if kind == "" || strings.EqualFold(event.Type, kind) {
			out = append(out, event)
		}
	}
	return out
}
