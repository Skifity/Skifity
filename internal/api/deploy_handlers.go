package api

import (
	"bufio"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/cron"
	"skifity/internal/errdoc"
	"skifity/internal/logging"
	"skifity/internal/registry"
	"skifity/internal/store"
)

type deployRequestBody struct {
	CommitSHA string `json:"commit_sha,omitempty"`
	Force     bool   `json:"force,omitempty"`
}

func (s *Server) handleDeployApp(w http.ResponseWriter, r *http.Request) {
	app, user, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req deployRequestBody
	if r.ContentLength > 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
	}
	// After the body is read, so a malformed request is reported as one
	// whether or not the cluster is wired up.
	if s.deployer == nil {
		writeError(w, r, errdoc.NotConfigured("Deployments", "the panel's cluster connection"))
		return
	}
	deployment, err := s.deployer.Deploy(r.Context(), DeployRequest{
		AppID:     app.ID,
		Trigger:   "manual",
		CommitSHA: strings.TrimSpace(req.CommitSHA),
		CreatedBy: user.ID,
		Force:     req.Force,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.deploy_started", "app", app.ID, app.Name)
	writeJSON(w, http.StatusAccepted, deployment)
}

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	deployments, err := s.db.ListDeployments(r.Context(), app.ID, queryInt(r, "limit", 50))
	if err != nil {
		writeError(w, r, err)
		return
	}
	// The panel keeps far more records than the registry keeps images, so the
	// list says which of them can still be gone back to. Without it the button
	// is there on every old version and the rollback fails on a pull.
	store.MarkRollbackTargets(deployments, registry.KeptPerApp)
	writeList(w, deployments)
}

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	deployment, err := s.deploymentForApp(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, deployment)
}

func (s *Server) handleDeploymentLogs(w http.ResponseWriter, r *http.Request) {
	deployment, err := s.deploymentForApp(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	lines, err := s.db.ListBuildLogs(r.Context(), deployment.ID, queryInt(r, "since", 0), queryInt(r, "limit", 0))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deployment_id": deployment.ID,
		"status":        deployment.Status,
		"finished":      deployment.Status.Terminal(),
		"lines":         lines,
	})
}

func (s *Server) handleCancelDeployment(w http.ResponseWriter, r *http.Request) {
	deployment, err := s.deploymentForApp(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer == nil {
		writeError(w, r, errdoc.NotConfigured("Deployments", "the panel's cluster connection"))
		return
	}
	if deployment.Status.Terminal() {
		writeError(w, r, errdoc.BadRequest("This deployment has already finished."))
		return
	}
	if err := s.deployer.Cancel(r.Context(), deployment.ID); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), deployment.AppID)
	s.audit(r, teamID, "deployment.cancelled", "deployment", deployment.ID, "")
	writeOK(w)
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	app, user, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer == nil {
		writeError(w, r, errdoc.NotConfigured("Deployments", "the panel's cluster connection"))
		return
	}
	target := chi.URLParam(r, "deploymentID")
	previous, err := s.db.GetDeployment(r.Context(), target)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if previous.AppID != app.ID {
		writeError(w, r, errdoc.NotFound("deployment", target))
		return
	}
	if previous.Status != store.DeploySucceeded || previous.Image == "" {
		writeError(w, r, errdoc.New("deploy.cannot_rollback", "This version cannot be rolled back to").
			WithCause("Deployment #%d did not finish successfully, so there is no image to go back to.", previous.Number).
			WithImpact("Nothing was changed. The current version is still running.").
			WithFix("Pick a deployment marked as succeeded from the history.").
			WithStatus(http.StatusBadRequest))
		return
	}

	deployment, err := s.deployer.Rollback(r.Context(), app.ID, target, user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.rolled_back", "app", app.ID, app.Name)
	writeJSON(w, http.StatusAccepted, deployment)
}

// deploymentForApp loads a deployment and checks it belongs to the app in the
// URL, so a deployment id from another team cannot be read by guessing.
func (s *Server) deploymentForApp(r *http.Request) (store.Deployment, error) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		return store.Deployment{}, err
	}
	deploymentID := chi.URLParam(r, "deploymentID")
	deployment, err := s.db.GetDeployment(r.Context(), deploymentID)
	if err != nil {
		return store.Deployment{}, errdoc.NotFound("deployment", deploymentID)
	}
	if deployment.AppID != app.ID {
		return store.Deployment{}, errdoc.NotFound("deployment", deploymentID)
	}
	return deployment, nil
}

// --- scaling ---

type scalingResponse struct {
	Replicas     int  `json:"replicas"`
	Autoscale    bool `json:"autoscale"`
	MinReplicas  int  `json:"min_replicas"`
	MaxReplicas  int  `json:"max_replicas"`
	CPUTarget    int  `json:"cpu_target"`
	MemoryTarget int  `json:"memory_target"`
	ScaleToZero  bool `json:"scale_to_zero"`
}

func (s *Server) handleGetScaling(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, scalingResponse{
		Replicas: app.Replicas, Autoscale: app.Autoscale, MinReplicas: app.MinReplicas,
		MaxReplicas: app.MaxReplicas, CPUTarget: app.CPUTarget, MemoryTarget: app.MemoryTarget,
		ScaleToZero: app.ScaleToZero,
	})
}

type setScalingRequest struct {
	Replicas     *int  `json:"replicas,omitempty"`
	Autoscale    *bool `json:"autoscale,omitempty"`
	MinReplicas  *int  `json:"min_replicas,omitempty"`
	MaxReplicas  *int  `json:"max_replicas,omitempty"`
	CPUTarget    *int  `json:"cpu_target,omitempty"`
	MemoryTarget *int  `json:"memory_target,omitempty"`
	ScaleToZero  *bool `json:"scale_to_zero,omitempty"`
}

func (s *Server) handleSetScaling(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setScalingRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	if req.Replicas != nil {
		app.Replicas = *req.Replicas
	}
	if req.Autoscale != nil {
		app.Autoscale = *req.Autoscale
	}
	if req.MinReplicas != nil {
		app.MinReplicas = *req.MinReplicas
	}
	if req.MaxReplicas != nil {
		app.MaxReplicas = *req.MaxReplicas
	}
	if req.CPUTarget != nil {
		app.CPUTarget = *req.CPUTarget
	}
	if req.MemoryTarget != nil {
		app.MemoryTarget = *req.MemoryTarget
	}
	if req.ScaleToZero != nil {
		app.ScaleToZero = *req.ScaleToZero
	}

	if err := validateScaling(&app); err != nil {
		writeError(w, r, err)
		return
	}

	// Scale-to-zero needs KEDA, which is not installed until it is first used.
	if app.ScaleToZero && s.cluster != nil {
		if err := s.cluster.InstallComponent(r.Context(), "keda"); err != nil {
			writeError(w, r, errdoc.New("component.install_failed", "Scale to zero could not be enabled").
				WithCause("Installing KEDA, which provides scale to zero, failed: %s", err.Error()).
				WithImpact("The scaling settings were not changed.").
				WithFix("Check that the cluster has room for another small component and try again.").
				WithStatus(http.StatusBadGateway).Retry())
			return
		}
	}

	if err := s.db.UpdateApp(r.Context(), &app); err != nil {
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
	s.audit(r, teamID, "app.scaling_changed", "app", app.ID, app.Name)

	// Surface the scaling risks with the answer rather than making the user
	// find them: this is the moment they matter.
	var findings []ScalingFinding
	if s.deployer != nil && (app.Replicas > 1 || app.Autoscale) {
		if f, err := s.deployer.ScalingReadiness(r.Context(), app.ID); err == nil {
			findings = f
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scaling": scalingResponse{
			Replicas: app.Replicas, Autoscale: app.Autoscale, MinReplicas: app.MinReplicas,
			MaxReplicas: app.MaxReplicas, CPUTarget: app.CPUTarget, MemoryTarget: app.MemoryTarget,
			ScaleToZero: app.ScaleToZero,
		},
		"warnings": findings,
	})
}

func validateScaling(app *store.App) error {
	if app.Replicas < 0 || app.Replicas > 100 {
		return errdoc.BadRequest("The number of instances must be between 0 and 100.")
	}
	if app.Autoscale {
		if app.MinReplicas < 1 {
			return errdoc.BadRequest("With autoscaling on, the minimum must be at least 1. Use scale to zero if you want the app to stop when idle.")
		}
		if app.MaxReplicas < app.MinReplicas {
			return errdoc.BadRequest("The maximum number of instances cannot be lower than the minimum.")
		}
		if app.MaxReplicas > 100 {
			return errdoc.BadRequest("The maximum number of instances must be 100 or fewer.")
		}
		if app.CPUTarget <= 0 && app.MemoryTarget <= 0 {
			return errdoc.BadRequest("Autoscaling needs a CPU or memory target to scale on.")
		}
		if app.CPUTarget < 0 || app.CPUTarget > 100 || app.MemoryTarget < 0 || app.MemoryTarget > 100 {
			return errdoc.BadRequest("Scaling targets are percentages, so they must be between 1 and 100.")
		}
	}
	if app.ScaleToZero && app.Autoscale && app.MinReplicas > 1 {
		return errdoc.BadRequest("Scale to zero needs the minimum number of instances to be 1.")
	}
	return nil
}

func (s *Server) handleScalingReadiness(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer == nil {
		writeList(w, []ScalingFinding{})
		return
	}
	findings, err := s.deployer.ScalingReadiness(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, findings)
}

// --- one-off commands ---

type runRequest struct {
	Command string `json:"command"`
}

// handleRunCommand starts a command in the app's own image.
//
// It answers as soon as the job exists rather than waiting for it: a migration
// takes as long as it takes, and the caller follows the log.
func (s *Server) handleRunCommand(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req runRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}

	handle, err := s.deployer.RunOnce(r.Context(), app.ID, req.Command)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	// The command itself is audited: it ran with the app's credentials, and
	// which one it was is the first question afterwards.
	s.audit(r, teamID, "app.command_run", "app", app.ID, strings.TrimSpace(req.Command))
	writeJSON(w, http.StatusAccepted, map[string]any{
		"run":  handle.Name,
		"note": "The command is running. Read its output at /api/apps/" + app.ID + "/runs/" + handle.Name + "/logs.",
	})
}

// handleRunLogs returns a run's output, or streams it while it is still going.
func (s *Server) handleRunLogs(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}

	name := chi.URLParam(r, "runID")
	stream, err := s.deployer.RunLogs(r.Context(), app.ID, name, queryBool(r, "follow"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer stream.Close()

	lines := []string{}
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, logging.Scrub(scanner.Text()))
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": name, "lines": lines})
}

// --- scheduled commands ---

type appJobRequest struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

func (s *Server) handleListAppJobs(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	jobs, err := s.db.ListAppJobs(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, jobs)
}

func (s *Server) handleCreateAppJob(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req appJobRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	job, err := buildAppJob(app.ID, "", req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.CreateAppJob(r.Context(), &job); err != nil {
		writeError(w, r, err)
		return
	}
	s.syncAfterJobChange(r, app)
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.job_created", "app", app.ID, job.Name)
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleUpdateAppJob(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req appJobRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	job, err := buildAppJob(app.ID, chi.URLParam(r, "jobID"), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.UpdateAppJob(r.Context(), &job); err != nil {
		writeError(w, r, err)
		return
	}
	s.syncAfterJobChange(r, app)
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.job_updated", "app", app.ID, job.Name)
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleDeleteAppJob(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	jobID := chi.URLParam(r, "jobID")
	if err := s.db.DeleteAppJob(r.Context(), app.ID, jobID); err != nil {
		writeError(w, r, err)
		return
	}
	s.syncAfterJobChange(r, app)
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.job_deleted", "app", app.ID, jobID)
	writeOK(w)
}

// buildAppJob validates what the form sent.
//
// The schedule is parsed here rather than by Kubernetes, because Kubernetes
// answers a bad one with a rejected object and the panel would have to explain
// that afterwards. The same parser the backup schedules use is a five-field
// cron expression, which is what people expect to type.
//
// It is stored in the form this panel's parser agrees with, not verbatim.
// Kubernetes parses the string itself, with a different implementation, and
// validating one dialect while the cluster runs another is how "0 3 * * 7"
// — an ordinary way to write Sunday — was accepted, stored, shown with a
// next-run time and then refused by the API server. See cron.Canonical.
func buildAppJob(appID, id string, req appJobRequest) (store.AppJob, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return store.AppJob{}, errdoc.BadRequest("Give the scheduled command a name, such as \"nightly report\".")
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return store.AppJob{}, errdoc.BadRequest("Enter the command to run on the schedule.")
	}
	schedule, err := cron.Canonical(strings.TrimSpace(req.Schedule))
	if err != nil {
		return store.AppJob{}, errdoc.BadRequest(
			"That is not a schedule Skifity understands. Use five cron fields, for example \"0 3 * * *\" for every day at 03:00 UTC.")
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return store.AppJob{
		ID: id, AppID: appID, Name: name, Schedule: schedule, Command: command, Enabled: enabled,
	}, nil
}

// syncAfterJobChange applies the app again so the schedule takes effect now
// rather than at the next deployment.
func (s *Server) syncAfterJobChange(r *http.Request, app store.App) {
	if s.deployer == nil {
		return
	}
	if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
		s.log.Warn("could not apply a schedule change", "app", app.ID, "error", err)
	}
}
