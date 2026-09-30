package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// A preview somebody asks for, of a branch, without a pull request or a
// webhook: the design somebody wants to show before opening one, a branch on
// a host whose webhooks this panel cannot receive. Kubero starts previews
// from its pull request cards; this is any branch, and the preview is the
// same one a pull request from that branch would get — a copy of the app,
// its own databases and, when the environment asks for it, every other app —
// so opening the pull request later updates it rather than making another.

type startPreviewRequest struct {
	Branch string `json:"branch"`
}

type startedPreview struct {
	EnvironmentID string `json:"environment_id"`
	AppID         string `json:"app_id"`
	DeploymentID  string `json:"deployment_id"`
}

func (s *Server) handleStartPreview(w http.ResponseWriter, r *http.Request) {
	app, branch, ok := s.manualPreview(w, r)
	if !ok {
		return
	}
	if s.deployer == nil {
		writeError(w, r, errdoc.NotConfigured("Deployments", "the panel's cluster connection"))
		return
	}
	event := gitsrc.PushEvent{Kind: "pull_request_opened", RepoURL: app.RepoURL, SourceBranch: branch}
	deploymentID, err := s.deployPreview(r, app, event)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// The preview's copy of the app, for the page to open.
	sourceEnv, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	env, err := s.findPreview(r, sourceEnv.ProjectID, event)
	if err != nil {
		writeError(w, r, err)
		return
	}
	answer := startedPreview{EnvironmentID: env.ID, DeploymentID: deploymentID}
	if copies, err := s.db.ListApps(r.Context(), env.ID); err == nil {
		for _, copy := range copies {
			if copy.Slug == app.Slug {
				answer.AppID = copy.ID
			}
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "preview.started", "app", app.ID, branch)
	writeJSON(w, http.StatusCreated, answer)
}

// handleClosePreview removes a branch's preview, as deleting the branch does.
func (s *Server) handleClosePreview(w http.ResponseWriter, r *http.Request) {
	app, branch, ok := s.manualPreview(w, r)
	if !ok {
		return
	}
	sourceEnv, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	event := gitsrc.PushEvent{Kind: "pull_request_closed", RepoURL: app.RepoURL, SourceBranch: branch}
	if _, err := s.findPreview(r, sourceEnv.ProjectID, event); err != nil {
		writeError(w, r, errdoc.NotFound("preview of "+branch, branch))
		return
	}
	s.cleanupPreviewFor(r, app, event)
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "preview.closed", "app", app.ID, branch)
	writeOK(w)
}

// manualPreview is the app and branch a preview request is about, once the
// caller may deploy the app and the app is one a branch can be previewed of.
func (s *Server) manualPreview(w http.ResponseWriter, r *http.Request) (store.App, string, bool) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return app, "", false
	}
	var req startPreviewRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return app, "", false
	}
	branch := strings.TrimSpace(req.Branch)
	if !validBranchName(branch) {
		writeError(w, r, errdoc.BadRequest("That is not a branch name. Give the branch as Git writes it, such as feature/checkout."))
		return app, "", false
	}
	if app.SourceType != "git" || app.RepoURL == "" {
		writeError(w, r, errdoc.BadRequest("Only an app built from a Git repository has branches to preview."))
		return app, "", false
	}
	env, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return app, "", false
	}
	if env.Kind == store.EnvPreview {
		writeError(w, r, errdoc.BadRequest("This app is a preview already. Preview a branch from the app it was copied from."))
		return app, "", false
	}
	return app, branch, true
}

// validBranchName holds a branch name to Git's own rules, near enough that
// nothing it passes is refused by a clone, and nothing it refuses was ever a
// branch: no spaces or control characters, no .., none of ~^:?*[\, not
// starting with - or /, not ending with / or .lock.
func validBranchName(name string) bool {
	if name == "" || len(name) > 200 || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") ||
		strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".") ||
		strings.Contains(name, "..") || strings.Contains(name, "//") || strings.Contains(name, "@{") {
		return false
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(`~^:?*[\`, r) {
			return false
		}
	}
	return true
}
