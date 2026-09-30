package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/registry"
	"skifity/internal/store"
)

// Promoting a version: the exact image one environment runs, deployed to the
// same app in another, without building it again.
//
// Deploying the same commit to production after staging built the same
// thing a second time, and a build is where "it worked in staging" stops
// being true: a dependency resolved differently, a base image moved. Heroku's
// pipelines, Render and Northflank promote the artifact itself; so does this.

// promoteTarget is an app a version can be promoted to: the app of the same
// name in another environment of the project.
type promoteTarget struct {
	AppID           string `json:"app_id"`
	AppName         string `json:"app_name"`
	EnvironmentID   string `json:"environment_id"`
	EnvironmentName string `json:"environment_name"`
}

// handlePromoteTargets lists where an app's versions can be promoted to.
func (s *Server) handlePromoteTargets(w http.ResponseWriter, r *http.Request) {
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
	environments, err := s.db.ListEnvironments(r.Context(), env.ProjectID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	targets := []promoteTarget{}
	for _, other := range environments {
		// A preview is made from a pull request and replaced by the next
		// push; promoting into one is not a thing anybody means.
		if other.ID == env.ID || other.Kind == store.EnvPreview {
			continue
		}
		apps, err := s.db.ListApps(r.Context(), other.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, candidate := range apps {
			if candidate.Slug == app.Slug && candidate.SourceType == app.SourceType {
				targets = append(targets, promoteTarget{
					AppID: candidate.ID, AppName: candidate.Name,
					EnvironmentID: other.ID, EnvironmentName: other.Name,
				})
			}
		}
	}
	writeList(w, targets)
}

type promoteRequest struct {
	// DeploymentID is the version to promote, from another environment.
	DeploymentID string `json:"deployment_id"`
	// Force runs the image even though this app would build it differently.
	Force bool `json:"force,omitempty"`
}

// handlePromote deploys another environment's version of this app here.
func (s *Server) handlePromote(w http.ResponseWriter, r *http.Request) {
	target, user, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req promoteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	source, err := s.db.GetDeployment(r.Context(), req.DeploymentID)
	if err != nil {
		writeError(w, r, errdoc.NotFound("deployment", req.DeploymentID))
		return
	}
	// Whoever promotes has to be able to see where the version comes from,
	// or a deployment id from another team would be a way to run its image.
	sourceApp, _, err := s.authorizeApp(r, source.AppID, store.RoleViewer)
	if err != nil {
		writeError(w, r, errdoc.NotFound("deployment", req.DeploymentID))
		return
	}
	targetEnv, err := s.db.GetEnvironment(r.Context(), target.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	sourceEnv, err := s.db.GetEnvironment(r.Context(), sourceApp.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	switch {
	case sourceApp.ID == target.ID:
		writeError(w, r, errdoc.PromotionNotPossible("That version is already this app's; roll back to it instead."))
		return
	case sourceEnv.ProjectID != targetEnv.ProjectID:
		writeError(w, r, errdoc.PromotionNotPossible("A version is promoted between environments of one project."))
		return
	case sourceApp.SourceType != target.SourceType:
		writeError(w, r, errdoc.PromotionNotPossible("The two apps are not deployed the same way: one runs "+
			sourceApp.SourceType+", the other "+target.SourceType+"."))
		return
	case source.Status != store.DeploySucceeded || source.Image == "":
		writeError(w, r, errdoc.PromotionNotPossible("Version #"+strconv.Itoa(source.Number)+" did not deploy, so there is no image of it to run."))
		return
	}
	// The registry keeps the last few images of the app that built them; one
	// older than that is a record with nothing behind it.
	within, err := s.db.WithinRollbackWindow(r.Context(), sourceApp.ID, source.ID, registry.KeptPerApp)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !within {
		writeError(w, r, errdoc.ImageCollected(source.Number, registry.KeptPerApp))
		return
	}
	if s.deployer == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	// A prebuilt image is the app's own setting, and a deploy reads it from
	// there; promoting one is setting it.
	if target.SourceType == "image" {
		target.Image = source.Image
		if err := s.db.UpdateApp(r.Context(), &target); err != nil {
			writeError(w, r, err)
			return
		}
	}
	deployment, err := s.deployer.Deploy(r.Context(), DeployRequest{
		AppID: target.ID, Trigger: "promote", CreatedBy: user.ID, Force: req.Force,
		CommitSHA: source.CommitSHA, CommitMessage: source.CommitMessage, CommitAuthor: source.CommitAuthor,
		Image: source.Image, Fingerprint: source.BuildFingerprint,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), target.ID)
	s.audit(r, teamID, "app.promoted", "app", target.ID,
		sourceEnv.Name+" #"+strconv.Itoa(source.Number)+" → "+targetEnv.Name)
	writeJSON(w, http.StatusAccepted, deployment)
}
