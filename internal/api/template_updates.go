package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/runsafe"
	"skifity/internal/store"
	"skifity/internal/templates"
)

// Updating an app through the template it came from.
//
// The catalogue ships inside the panel, pinned to versions, so upgrading the
// panel is what brings a template's newer version. An installed app used to
// know nothing about where it came from: the new version sat in the catalogue
// and the app kept running the old one until somebody noticed and typed a new
// image tag by hand. Cloudron offers the update, backs up first, and only then
// applies it; so does this.

// templateBackupPoll is how often an update checks on the backups it is
// waiting for. A variable so a test does not wait five seconds.
var templateBackupPoll = 5 * time.Second

// templateBackupTimeout is how long an update waits for its backups.
const templateBackupTimeout = 3 * time.Hour

type templateView struct {
	FromTemplate bool `json:"from_template"`
	store.AppTemplate
	// Name is the template's name, for display.
	Name string `json:"name,omitempty"`
	// Current is the image the app runs now; Latest is the one the template
	// in this version of the panel sets, empty when the catalogue no longer
	// has it.
	Current string `json:"current_image,omitempty"`
	Latest  string `json:"latest_image,omitempty"`
	// UpdateAvailable is a newer image in the catalogue than the app runs.
	UpdateAvailable bool `json:"update_available"`
	// ChangedByHand is an image somebody set themselves since the template
	// last did, which an update would replace.
	ChangedByHand bool `json:"changed_by_hand"`
}

func (s *Server) templateView(ctx context.Context, app store.App) (templateView, templates.Service, error) {
	record, err := s.db.GetAppTemplate(ctx, app.ID)
	if errors.Is(err, store.ErrNotFound) {
		return templateView{}, templates.Service{}, nil
	}
	if err != nil {
		return templateView{}, templates.Service{}, err
	}
	view := templateView{FromTemplate: true, AppTemplate: record, Current: app.Image}
	view.ChangedByHand = app.Image != record.InstalledImage
	tpl, ok := templates.Lookup(record.TemplateID)
	if !ok {
		return view, templates.Service{}, nil
	}
	view.Name = tpl.Name
	for _, service := range tpl.Services {
		if service.Name == record.Service {
			view.Latest = service.Image
			view.UpdateAvailable = service.Image != app.Image
			return view, service, nil
		}
	}
	return view, templates.Service{}, nil
}

func (s *Server) handleGetAppTemplate(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, _, err := s.templateView(r.Context(), app)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type templateUpdateRequest struct {
	// Force replaces an image somebody set by hand.
	Force bool `json:"force,omitempty"`
	// SkipBackup updates without backing up first. It has to be asked for.
	SkipBackup bool `json:"skip_backup,omitempty"`
}

// handleUpdateAppTemplate moves an app to the template's current image,
// backing up its disks and databases first.
func (s *Server) handleUpdateAppTemplate(w http.ResponseWriter, r *http.Request) {
	app, user, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req templateUpdateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	view, _, err := s.templateView(r.Context(), app)
	if err != nil {
		writeError(w, r, err)
		return
	}
	switch {
	case !view.FromTemplate || view.Latest == "":
		writeError(w, r, errdoc.TemplateGone(app.Name))
		return
	case view.UpdateStatus == store.TemplateUpdateBackingUp:
		writeError(w, r, errdoc.TemplateUpdateRunning(app.Name))
		return
	case !view.UpdateAvailable:
		writeError(w, r, errdoc.TemplateUpToDate(app.Name, view.Current))
		return
	case view.ChangedByHand && !req.Force:
		writeError(w, r, errdoc.TemplateChangedByHand(view.Current, view.InstalledImage))
		return
	}
	// A locked app would back up and then refuse the deploy.
	if lock, err := s.db.GetDeployLock(r.Context(), app.ID); err == nil {
		writeError(w, r, errdoc.DeployLocked(lock.LockedBy, lock.Reason))
		return
	}

	targets, err := s.updateBackupTargets(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.template_update", "app", app.ID, view.Current+" → "+view.Latest)

	if len(targets) == 0 || req.SkipBackup {
		deployment, err := s.applyTemplateUpdate(r.Context(), app.ID, view.Latest, user.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deployment": deployment})
		return
	}
	if s.backups == nil {
		writeError(w, r, errdoc.TemplateNeedsBackups(app.Name))
		return
	}
	var started []string
	for _, target := range targets {
		backup, err := s.backups.Run(r.Context(), target.kind, target.id, "before_update")
		if err != nil {
			// Nothing has changed yet, and the backups already started are
			// ordinary backups that finish on their own.
			writeError(w, r, err)
			return
		}
		started = append(started, backup.ID)
	}
	if err := s.db.SetTemplateUpdate(r.Context(), app.ID, store.TemplateUpdateBackingUp, view.Latest, ""); err != nil {
		writeError(w, r, err)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	go func() {
		defer runsafe.Recover(s.log, "updating "+app.Name+" through its template", func(err error) {
			_ = s.db.SetTemplateUpdate(ctx, app.ID, store.TemplateUpdateFailed, view.Latest, err.Error())
		})
		s.finishTemplateUpdate(ctx, app, started, view.Latest, user.ID)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"backups": started})
}

type backupTarget struct{ kind, id string }

// updateBackupTargets are what an update backs up first: the app's disks, and
// the databases linked to it.
func (s *Server) updateBackupTargets(ctx context.Context, appID string) ([]backupTarget, error) {
	var out []backupTarget
	volumes, err := s.db.ListVolumes(ctx, appID)
	if err != nil {
		return nil, err
	}
	for _, volume := range volumes {
		out = append(out, backupTarget{"volume", volume.ID})
	}
	links, err := s.db.ListLinksForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		out = append(out, backupTarget{"database", link.DatabaseID})
	}
	return out, nil
}

// finishTemplateUpdate waits for the backups and applies the update only if
// every one of them succeeded.
func (s *Server) finishTemplateUpdate(ctx context.Context, app store.App, backups []string, image, userID string) {
	deadline := time.Now().Add(templateBackupTimeout)
	for {
		pending, failed := 0, ""
		for _, id := range backups {
			backup, err := s.db.GetBackup(ctx, id)
			switch {
			case err != nil:
				failed = err.Error()
			case backup.Status == "running":
				pending++
			case backup.Status != "succeeded":
				failed = fmt.Sprintf("the backup of %s %s failed: %s", backup.TargetType, backup.TargetID,
					strings.TrimSpace(backup.ErrorMessage))
			}
		}
		if failed != "" {
			_ = s.db.SetTemplateUpdate(ctx, app.ID, store.TemplateUpdateFailed, image, failed)
			s.log.Warn("a template update stopped because a backup failed", "app", app.ID, "reason", failed)
			return
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			_ = s.db.SetTemplateUpdate(ctx, app.ID, store.TemplateUpdateFailed, image,
				"the backups did not finish in time")
			return
		}
		time.Sleep(templateBackupPoll)
	}
	if _, err := s.applyTemplateUpdate(ctx, app.ID, image, userID); err != nil {
		_ = s.db.SetTemplateUpdate(ctx, app.ID, store.TemplateUpdateFailed, image, errdoc.From(err).Cause)
	}
}

// applyTemplateUpdate switches the image and deploys it.
func (s *Server) applyTemplateUpdate(ctx context.Context, appID, image, userID string) (store.Deployment, error) {
	app, err := s.db.GetApp(ctx, appID)
	if err != nil {
		return store.Deployment{}, err
	}
	app.Image = image
	if err := s.db.UpdateApp(ctx, &app); err != nil {
		return store.Deployment{}, err
	}
	if err := s.db.FinishTemplateUpdate(ctx, app.ID, image); err != nil {
		return store.Deployment{}, err
	}
	if s.deployer == nil {
		return store.Deployment{}, nil
	}
	return s.deployer.Deploy(ctx, DeployRequest{AppID: app.ID, Trigger: "template", CreatedBy: userID})
}
