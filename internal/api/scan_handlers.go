package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// An app's image, scanned for known vulnerabilities. The scanning itself is in
// internal/deploy and internal/vulnscan; this is reading what was found and
// asking for another look.

// vulnerabilityAnswer is an app's Security tab.
type vulnerabilityAnswer struct {
	// Enabled is scanning switched on for this panel.
	Enabled bool `json:"enabled"`
	// Blocking is a deploy stopped by a critical vulnerability with a fix.
	Blocking bool `json:"blocking"`
	// Image is what the app runs now: its newest deployment that succeeded.
	Image string `json:"image,omitempty"`
	// Scan is the newest report, with its findings; null when there is none.
	Scan *store.ImageScan `json:"scan"`
	// Current is that report being of the image the app runs now.
	Current bool `json:"current"`
	// Undeployed is that report being of an image that has not gone out: its
	// deploy is still under way, or was stopped — by this panel for the
	// vulnerabilities in it, or by anything else.
	Undeployed bool `json:"undeployed"`
	// Latest is the newest scan when it is not Scan: one queued or running
	// now, or one that failed, which says why.
	Latest *store.ImageScan `json:"latest,omitempty"`
}

func (s *Server) handleGetVulnerabilities(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ctx := r.Context()
	answer := vulnerabilityAnswer{}
	answer.Enabled, answer.Blocking = s.scanSettings(ctx)

	current, err := s.db.LatestSuccessfulDeployment(ctx, app.ID)
	switch {
	case err == nil:
		answer.Image = current.Image
	case !errors.Is(err, store.ErrNotFound):
		writeError(w, r, err)
		return
	}
	scan, err := s.db.LatestSucceededImageScan(ctx, app.ID)
	switch {
	case err == nil:
		answer.Scan = &scan
		answer.Current = scan.Image != "" && scan.Image == answer.Image
		if !answer.Current && scan.DeploymentID != "" {
			if deployment, err := s.db.GetDeployment(ctx, scan.DeploymentID); err == nil {
				answer.Undeployed = deployment.Status != store.DeploySucceeded
			}
		}
	case !errors.Is(err, store.ErrNotFound):
		writeError(w, r, err)
		return
	}
	latest, err := s.db.LatestImageScan(ctx, app.ID)
	switch {
	case err == nil:
		if answer.Scan == nil || latest.ID != answer.Scan.ID {
			answer.Latest = &latest
		}
	case !errors.Is(err, store.ErrNotFound):
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) handleScanApp(w http.ResponseWriter, r *http.Request) {
	app, user, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.scanner == nil {
		writeError(w, r, errdoc.NotConfigured("Image scanning", "the panel's cluster connection"))
		return
	}
	scan, err := s.scanner.Scan(r.Context(), app.ID, user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.scan_started", "app", app.ID, app.Name)
	writeJSON(w, http.StatusAccepted, scan)
}

// scanSettings reads how scanning is set up: on unless switched off, and
// stopping deploys only when switched on. The deployer reads the same two
// settings the same way; see internal/deploy/scan.go.
func (s *Server) scanSettings(ctx context.Context) (enabled, blocking bool) {
	on, _, err := s.db.GetSetting(ctx, settings.KeyScanEnabled)
	if err != nil {
		return false, false
	}
	block, _, err := s.db.GetSetting(ctx, settings.KeyScanBlockCritical)
	if err != nil {
		return strings.TrimSpace(on) != "false", false
	}
	return strings.TrimSpace(on) != "false", strings.TrimSpace(block) == "true"
}

// withVulnerabilities puts what each app's newest scan counted on a list of an
// environment's apps, so the list can say which has something critical.
func (s *Server) withVulnerabilities(ctx context.Context, environmentID string, apps []store.App) error {
	counts, err := s.db.LatestScanCounts(ctx, environmentID)
	if err != nil {
		return err
	}
	for i := range apps {
		if c, ok := counts[apps[i].ID]; ok {
			apps[i].Vulnerabilities = &c
		}
	}
	return nil
}
