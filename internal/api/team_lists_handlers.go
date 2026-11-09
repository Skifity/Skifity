package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"skifity/internal/store"
)

// Lists that cover a whole team. See store/teamlists.go.

// handleListTeamApps answers every app in the team, with the project and
// environment each lives in and how its newest deploy went. A member limited
// to some projects is answered theirs, as with the projects list.
func (s *Server) handleListTeamApps(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	_, membership, err := s.authorizeTeamMember(r, teamID, store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apps, err := s.db.ListTeamApps(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	visible := apps[:0]
	for _, app := range apps {
		if membership.Reaches(app.ProjectID) {
			visible = append(visible, app)
		}
	}
	writeList(w, visible)
}
