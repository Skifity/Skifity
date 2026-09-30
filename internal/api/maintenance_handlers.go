package api

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/guard"
	"skifity/internal/store"
)

// Maintenance: visitors get a page saying so, while the addresses the team
// lists still reach the app. See guard/maintenance.go for why the guard is
// what answers.

const (
	maxMaintenanceMessage = 1000
	maxMaintenanceAllow   = 50
)

type maintenanceView struct {
	Active bool `json:"active"`
	store.Maintenance
	// Hostnames are where the page is shown.
	Hostnames []string `json:"hostnames"`
	// YourAddress is the address this request came from, when it is a public
	// one: the one to let through so that whoever is doing the work can check
	// the app before visitors see it.
	YourAddress string `json:"your_address,omitempty"`
}

func (s *Server) maintenanceView(r *http.Request, app store.App) (maintenanceView, error) {
	view := maintenanceView{Maintenance: store.Maintenance{AppID: app.ID, Allow: []string{}}, Hostnames: []string{}}
	current, err := s.db.GetMaintenance(r.Context(), app.ID)
	switch {
	case err == nil:
		view.Active, view.Maintenance = true, current
	case !errors.Is(err, store.ErrNotFound):
		return view, err
	}
	domains, err := s.db.ListDomains(r.Context(), app.ID)
	if err != nil {
		return view, err
	}
	for _, domain := range domains {
		view.Hostnames = append(view.Hostnames, domain.Hostname)
	}
	if addr, err := netip.ParseAddr(clientIPFrom(r.Context())); err == nil {
		addr = addr.Unmap()
		if addr.IsGlobalUnicast() && !addr.IsPrivate() {
			view.YourAddress = addr.String()
		}
	}
	return view, nil
}

func (s *Server) handleGetMaintenance(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.maintenanceView(r, app)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type maintenanceRequest struct {
	Message string   `json:"message"`
	Allow   []string `json:"allow"`
}

// handleStartMaintenance puts an app into maintenance, or changes the message
// and addresses of one already in it.
//
// Member, like a deploy lock: a member can already take the app down with a
// bad deploy, and this is the way of doing it on purpose that says so.
func (s *Server) handleStartMaintenance(w http.ResponseWriter, r *http.Request) {
	app, user, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req maintenanceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" || utf8.RuneCountInString(message) > maxMaintenanceMessage {
		writeError(w, r, errdoc.MaintenanceMessage(maxMaintenanceMessage))
		return
	}
	allow := []string{}
	for _, entry := range req.Allow {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}
		prefix, err := guard.ParseAllowed(entry)
		if err != nil || len(allow) == maxMaintenanceAllow {
			writeError(w, r, errdoc.MaintenanceAddress(entry, maxMaintenanceAllow))
			return
		}
		// Stored the way it was matched, so what the page shows is what the
		// guard does: 203.0.113.9/24 is 203.0.113.0/24.
		if prefix.IsSingleIP() {
			allow = append(allow, prefix.Addr().String())
		} else {
			allow = append(allow, prefix.String())
		}
	}
	domains, err := s.db.ListDomains(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(domains) == 0 {
		writeError(w, r, errdoc.MaintenanceNoDomain())
		return
	}

	// Maintenance is put in front of an app by re-applying the version it
	// runs; an app that has never run one has nothing to re-apply, and
	// starting would answer success with nothing in front of anybody.
	if _, err := s.db.LatestSuccessfulDeployment(r.Context(), app.ID); errors.Is(err, store.ErrNotFound) {
		writeError(w, r, errdoc.BadRequest("This app has not been deployed yet, so there is no version to put the notice in front of. Deploy it first."))
		return
	}
	_, wasActive := s.db.GetMaintenance(r.Context(), app.ID)
	maintenance := store.Maintenance{AppID: app.ID, Message: message, Allow: allow, StartedBy: user.Email}
	if err := s.db.StartMaintenance(r.Context(), &maintenance); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.applyMaintenance(r, app); err != nil {
		// Not in force is not in maintenance: saying it is would be a page
		// that tells the team their visitors see something they do not.
		if errors.Is(wasActive, store.ErrNotFound) {
			_ = s.db.EndMaintenance(r.Context(), app.ID)
		}
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.maintenance_started", "app", app.ID, truncate(message, 200))

	view, err := s.maintenanceView(r, app)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleEndMaintenance(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	before, err := s.db.GetMaintenance(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.EndMaintenance(r.Context(), app.ID); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.applyMaintenance(r, app); err != nil {
		// Still in force, so still recorded: the page says what visitors see.
		if restoreErr := s.db.StartMaintenance(r.Context(), &before); restoreErr != nil {
			s.log.Warn("could not record maintenance as still in force", "app", app.ID, "error", restoreErr)
		}
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.maintenance_ended", "app", app.ID, app.Name)
	writeOK(w)
}

// applyMaintenance makes what is stored true in the cluster: the guard
// running, its rules rewritten, and the app's Ingress with or without the
// middleware in front of it.
//
// Every step has to have happened for it to be true: a guard installed and an
// Ingress that does not send visitors through it is maintenance on a page and
// not in front of anybody.
func (s *Server) applyMaintenance(r *http.Request, app store.App) error {
	if s.cluster == nil {
		return errdoc.ClusterUnreachable(nil)
	}
	// The guard is what answers. It is installed on first use, the way
	// autoscaling installs KEDA: maintenance is wanted in a hurry, and
	// "install the firewall first" is not an answer to give then.
	if component, err := s.cluster.ComponentStatus(r.Context(), "firewall"); err != nil || component.Status != "installed" {
		if active, _ := s.db.AppInMaintenance(r.Context(), app.ID); active {
			if err := s.cluster.InstallComponent(r.Context(), "firewall"); err != nil {
				return err
			}
		}
	}
	if err := s.cluster.RefreshFirewall(r.Context()); err != nil {
		return err
	}
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			// A sync applies everything, the Ingress included, and then waits
			// for the app to be ready. An app that is not — crash-looping, the
			// usual reason to put a notice in front of it — failed that wait
			// with the Ingress already changed, and the panel recorded the
			// opposite of what visitors saw.
			var problem *errdoc.Problem
			if errors.As(err, &problem) && problem.Code == "deploy.rollout_timeout" {
				s.log.Info("maintenance changed while the app is not ready", "app", app.ID, "detail", problem.Cause)
				return nil
			}
			return err
		}
	}
	return nil
}
