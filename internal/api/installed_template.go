package api

import (
	"context"

	"skifity/internal/store"
	"skifity/internal/templates"
)

// InstalledTemplate finds the template an app was installed from, in the
// catalogue it came from: the built-in one, or the team's own, read from the
// copy the panel keeps. false is a template that is no longer there, or a
// team catalogue that cannot be read.
//
// Startup hands it to internal/dbsvc, which matches an app installed before
// a link kept its pieces to the pieces its template declares.
func (s *Server) InstalledTemplate(ctx context.Context, installed store.AppTemplate) (templates.Template, bool) {
	if installed.CatalogueID == "" {
		return templates.Lookup(installed.TemplateID)
	}
	teamID, err := s.db.TeamIDForApp(ctx, installed.AppID)
	if err != nil {
		return templates.Template{}, false
	}
	tpl, err := s.resolveTemplate(ctx, teamID, installed.CatalogueID, installed.TemplateID)
	if err != nil {
		return templates.Template{}, false
	}
	return tpl, true
}
