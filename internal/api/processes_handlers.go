package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// An app's other processes: a worker, a clock, the lines of a Procfile beside
// web. See internal/kube/process.go for what they become.

const (
	// maxProcesses is how many an app may have beside web. A Procfile with
	// more than a handful is several apps sharing a repository.
	maxProcesses = 10
	// maxProcessInstances bounds one process the way validateScaling bounds
	// the app.
	maxProcessInstances = 100
	// maxProcessCommand bounds a command, which is one shell line.
	maxProcessCommand = 4096
)

// processView is a process with what is running of it, when the cluster can
// say.
type processView struct {
	store.AppProcess
	Ready *int   `json:"ready,omitempty"`
	Phase string `json:"phase,omitempty"`
}

func (s *Server) handleListProcesses(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	processes, err := s.db.ListProcesses(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items := make([]processView, 0, len(processes))
	var namespace string
	if s.cluster != nil && len(processes) > 0 {
		if env, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID); err == nil {
			namespace = env.Namespace
		}
	}
	for _, process := range processes {
		view := processView{AppProcess: process}
		if namespace != "" {
			// Best effort: the list is worth having with the cluster down,
			// and a process that has never been deployed has no status.
			status, err := s.cluster.AppStatus(r.Context(), namespace, kube.ProcessDeploymentName(app.Slug, process.Name))
			if err == nil {
				ready := status.ReadyReplicas
				view.Ready, view.Phase = &ready, status.Phase
			}
		}
		items = append(items, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

type setProcessRequest struct {
	Command   string `json:"command"`
	Instances *int   `json:"instances"`
}

func (s *Server) handleSetProcess(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setProcessRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	process := store.AppProcess{AppID: app.ID, Name: chi.URLParam(r, "process"), Command: strings.TrimSpace(req.Command), Instances: 1}
	if req.Instances != nil {
		process.Instances = *req.Instances
	}
	existing, err := s.db.ListProcesses(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := validateProcess(process, existing); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.SetProcess(r.Context(), &process); err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			writeError(w, r, err)
			return
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.process_set", "app", app.ID, app.Name+" ("+process.Name+")")
	writeJSON(w, http.StatusOK, process)
}

func (s *Server) handleDeleteProcess(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	name := chi.URLParam(r, "process")
	if err := s.db.DeleteProcess(r.Context(), app.ID, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = errdoc.NotFound("process", name)
		}
		writeError(w, r, err)
		return
	}
	// The next apply removes its Deployment; this is that apply.
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			writeError(w, r, err)
			return
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.process_removed", "app", app.ID, app.Name+" ("+name+")")
	w.WriteHeader(http.StatusNoContent)
}

// validateProcess checks a process against the rules and the app's others.
func validateProcess(process store.AppProcess, existing []store.AppProcess) error {
	if !kube.ValidProcessName(process.Name) {
		return errdoc.ProcessNameInvalid(process.Name)
	}
	if process.Command == "" {
		return errdoc.BadRequest("A process needs a command, the line after its name in a Procfile.")
	}
	if len(process.Command) > maxProcessCommand || strings.ContainsAny(process.Command, "\r\n") {
		return errdoc.BadRequest("A process's command is one line, of at most 4096 characters.")
	}
	if process.Instances < 0 || process.Instances > maxProcessInstances {
		return errdoc.BadRequest("The number of instances must be between 0 and 100.")
	}
	others := 0
	for _, other := range existing {
		if other.Name != process.Name {
			others++
		}
	}
	if others >= maxProcesses {
		return errdoc.TooManyProcesses(maxProcesses)
	}
	return nil
}

// hasProcess reports whether an app has a process of that name.
func (s *Server) hasProcess(r *http.Request, appID, name string) bool {
	processes, err := s.db.ListProcesses(r.Context(), appID)
	if err != nil {
		return false
	}
	for _, process := range processes {
		if process.Name == name {
			return true
		}
	}
	return false
}

// initialProcesses checks the processes a new app is created with, before
// there is an app to attach them to.
func initialProcesses(requested []initialProcess) ([]store.AppProcess, error) {
	out := make([]store.AppProcess, 0, len(requested))
	for _, req := range requested {
		process := store.AppProcess{Name: strings.TrimSpace(req.Name), Command: strings.TrimSpace(req.Command), Instances: 1}
		if req.Instances != nil {
			process.Instances = *req.Instances
		}
		for _, earlier := range out {
			if earlier.Name == process.Name {
				return nil, errdoc.BadRequest("Two processes are called " + process.Name + ".")
			}
		}
		if err := validateProcess(process, out); err != nil {
			return nil, err
		}
		out = append(out, process)
	}
	return out, nil
}
