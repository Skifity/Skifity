package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/gitsrc"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// handleGitWebhook receives a push or pull request from a Git host.
//
// Webhooks are unauthenticated in the usual sense: the signature is the
// authentication. An unverified payload is rejected before anything is parsed
// beyond what verification needs, because triggering a deploy is a privileged
// action.
func (s *Server) handleGitWebhook(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "sourceID")
	source, err := s.db.GetGitSource(r.Context(), sourceID)
	if err != nil {
		// Answer the same for an unknown source as for a bad signature, so the
		// endpoint cannot be used to discover which source ids exist.
		writeError(w, r, errdoc.NotFound("git source", sourceID))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20))
	if err != nil {
		writeError(w, r, errdoc.BadRequest("The webhook payload could not be read."))
		return
	}

	if err := s.verifyWebhook(r, source, body); err != nil {
		s.log.Warn("rejected a webhook", "source", sourceID, "error", err)
		writeError(w, r, errdoc.New("webhook.unverified", "This webhook could not be verified").
			WithCause("The signature on the request does not match this connection's webhook secret.").
			WithImpact("No deploy was started.").
			WithFix("Check that the secret in your Git host's webhook settings matches the one in Skifity.").
			WithStatus(http.StatusUnauthorized))
		return
	}

	event, err := gitsrc.ParseWebhook(r.Header, body)
	if err != nil {
		if errors.Is(err, gitsrc.ErrUnsupportedEvent) {
			// Answer 200 so the host does not mark the webhook as failing.
			writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
			return
		}
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}
	if event.Kind == "ping" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "pong"})
		return
	}

	// The response goes out before the deploys start: a Git host times a
	// webhook out in ten seconds, and a build takes minutes.
	result := s.dispatchGitEvent(r, source, event)
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) verifyWebhook(r *http.Request, source store.GitSource, body []byte) error {
	secret, err := s.webhookSecretFor(r, source)
	if err != nil {
		return err
	}
	// How a push is verified is decided by the connection somebody set up, not
	// by a header on the request. The headers say which host *claims* to have
	// sent it, and the sender chooses them: a GitHub connection should be
	// checked the GitHub way whatever arrives, so that the rule cannot be
	// picked by the person being checked.
	switch source.Kind {
	case "gitlab":
		return gitsrc.VerifyGitLabToken(secret, r.Header.Get("X-Gitlab-Token"))
	case "gitea":
		// Gitea sends GitHub's header too on recent versions, and its own on
		// older ones. Both are an HMAC of the body; only the prefix differs.
		if sig := r.Header.Get("X-Hub-Signature-256"); sig != "" {
			return gitsrc.VerifyGitHubSignature(secret, body, sig)
		}
		return gitsrc.VerifyGiteaSignature(secret, body, r.Header.Get("X-Gitea-Signature"))
	default:
		return gitsrc.VerifyGitHubSignature(secret, body, r.Header.Get("X-Hub-Signature-256"))
	}
}

// webhookSecretFor finds a connection's own webhook secret.
//
// There used to be a branch here for a GitHub App, reading a shared secret out
// of the settings. Nothing could create such a connection from the panel, and
// the rest of a GitHub App — the private key, the installation token it is
// exchanged for — was never written, so the branch verified pushes for a kind
// of connection that could not exist.
func (s *Server) webhookSecretFor(r *http.Request, source store.GitSource) (string, error) {
	if source.ConfigEnc == "" {
		return "", errors.New("this Git connection has no stored credentials")
	}
	raw, err := s.keyring.Open(source.ConfigEnc, "git_source:"+source.TeamID+":"+source.Name)
	if err != nil {
		return "", err
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", err
	}
	if secret := config["webhook_secret"]; secret != "" {
		return secret, nil
	}
	// A connection created with only a token reuses it as the webhook secret,
	// which is what the setup instructions tell the user to paste.
	return config["token"], nil
}

type webhookResult struct {
	Status      string   `json:"status"`
	Deployments []string `json:"deployments,omitempty"`
	Skipped     []string `json:"skipped,omitempty"`
}

// dispatchGitEvent turns a verified event into deploys.
func (s *Server) dispatchGitEvent(r *http.Request, source store.GitSource, event gitsrc.PushEvent) webhookResult {
	result := webhookResult{Status: "accepted"}

	apps, err := s.db.ListAppsByRepo(r.Context(), event.RepoURL)
	if err != nil {
		s.log.Error("could not find apps for a webhook", "repo", event.RepoURL, "error", err)
		result.Status = "error"
		return result
	}
	if len(apps) == 0 {
		result.Status = "no matching apps"
		return result
	}

	for _, app := range apps {
		// An app belongs to exactly one team; a webhook from another team's
		// connection must not be able to deploy it.
		teamID, err := s.db.TeamIDForApp(r.Context(), app.ID)
		if err != nil || teamID != source.TeamID {
			continue
		}
		if !app.AutoDeploy {
			result.Skipped = append(result.Skipped, app.Name+" (deploy on push is off)")
			continue
		}

		switch event.Kind {
		case "push":
			if event.Deleted {
				s.cleanupPreviewFor(r, app, event)
				continue
			}
			branch := app.Branch
			if branch == "" {
				branch = "main"
			}
			if event.Branch != branch {
				result.Skipped = append(result.Skipped, app.Name+" (watches "+branch+")")
				continue
			}
			if s.deployer == nil {
				continue
			}
			deployment, err := s.deployer.Deploy(r.Context(), DeployRequest{
				AppID: app.ID, Trigger: "push", CommitSHA: event.CommitSHA, CreatedBy: "webhook",
			})
			if err != nil {
				s.log.Error("could not start a deploy from a webhook", "app", app.ID, "error", err)
				continue
			}
			result.Deployments = append(result.Deployments, deployment.ID)

		case "pull_request_opened":
			if !app.PreviewDeploys {
				result.Skipped = append(result.Skipped, app.Name+" (preview environments are off)")
				continue
			}
			deploymentID, err := s.deployPreview(r, app, event)
			if err != nil {
				s.log.Error("could not deploy a preview", "app", app.ID, "error", err)
				continue
			}
			result.Deployments = append(result.Deployments, deploymentID)

		case "pull_request_closed":
			s.cleanupPreviewFor(r, app, event)
		}
	}
	return result
}

// previewRef identifies the preview environment for a branch or pull request.
func previewRef(event gitsrc.PushEvent) string {
	if event.PullRequest > 0 {
		return "pr-" + strconv.Itoa(event.PullRequest)
	}
	return "branch-" + kube.Slugify(event.SourceBranch)
}

// deployPreview creates or updates the preview environment for a pull request.
func (s *Server) deployPreview(r *http.Request, app store.App, event gitsrc.PushEvent) (string, error) {
	if s.deployer == nil {
		return "", errors.New("deployments are not configured")
	}
	sourceEnv, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
	if err != nil {
		return "", err
	}
	project, err := s.db.GetProject(r.Context(), sourceEnv.ProjectID)
	if err != nil {
		return "", err
	}
	team, err := s.db.GetTeam(r.Context(), project.TeamID)
	if err != nil {
		return "", err
	}

	ref := previewRef(event)
	env, err := s.db.FindEnvironmentBySourceRef(r.Context(), project.ID, ref)
	if err != nil {
		// First push to this pull request: make the environment.
		env = store.Environment{
			ProjectID: project.ID,
			Name:      previewName(event),
			Slug:      ref,
			Kind:      store.EnvPreview,
			SourceRef: ref,
			Namespace: kube.NamespaceFor(team.Slug, project.Slug, ref),
		}
		if err := s.db.CreateEnvironment(r.Context(), &env); err != nil {
			return "", err
		}
		if s.cluster != nil {
			if err := s.cluster.EnsureNamespace(r.Context(), env, team.ID, project.ID); err != nil {
				return "", err
			}
		}
	}

	// One app per preview environment, copied from the app being previewed.
	previewApps, err := s.db.ListApps(r.Context(), env.ID)
	if err != nil {
		return "", err
	}
	var previewApp store.App
	for _, candidate := range previewApps {
		if candidate.Slug == app.Slug {
			previewApp = candidate
			break
		}
	}
	if previewApp.ID == "" {
		previewApp = app
		previewApp.ID = ""
		previewApp.EnvironmentID = env.ID
		previewApp.Branch = event.SourceBranch
		previewApp.PreviewDeploys = false
		// A preview is throwaway, so it never autoscales and asks for little.
		previewApp.Autoscale = false
		previewApp.Replicas = 1
		if err := s.db.CreateApp(r.Context(), &previewApp); err != nil {
			return "", err
		}
		if err := s.copyPreviewVariables(r, app.ID, previewApp.ID, event.Fork); err != nil {
			return "", err
		}
		if event.Fork {
			s.log.Info("a preview from a fork was given no secrets",
				"app", previewApp.ID, "pull_request", event.PullRequest)
		}
	}

	deployment, err := s.deployer.Deploy(r.Context(), DeployRequest{
		AppID: previewApp.ID, Trigger: "preview", CommitSHA: event.CommitSHA, CreatedBy: "webhook",
	})
	if err != nil {
		return "", err
	}
	return deployment.ID, nil
}

// copyPreviewVariables duplicates an app's variables into a preview copy.
//
// Values are resealed under the new app's context rather than copied as
// ciphertext, so the context binding keeps meaning something.
//
// A preview built from a fork gets no secrets. A pull request from another
// repository can be opened by anyone, and its code decides what runs in the
// container the secrets would be handed to: an attacker's first commit would
// be one that prints the environment. Somebody with write access to the
// repository could read them from a deploy anyway, so a same-repository pull
// request is treated as it was. This is the same line GitHub Actions draws.
func (s *Server) copyPreviewVariables(r *http.Request, fromAppID, toAppID string, fork bool) error {
	rows, err := s.db.ListVariables(r.Context(), fromAppID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if fork && row.IsSecret {
			continue
		}
		plaintext, err := s.keyring.Open(row.Sealed, variableContext(fromAppID, row.Key))
		if err != nil {
			return err
		}
		sealed, err := s.keyring.Seal(plaintext, variableContext(toAppID, row.Key))
		if err != nil {
			return err
		}
		variable := store.Variable{AppID: toAppID, Key: row.Key, IsSecret: row.IsSecret, BuildTime: row.BuildTime}
		if err := s.db.SetVariable(r.Context(), &variable, sealed); err != nil {
			return err
		}
	}
	return nil
}

// cleanupPreviewFor removes the preview environment for a closed pull request or
// a deleted branch. Previews that are never cleaned up are how a self-hosted
// cluster quietly runs out of memory.
func (s *Server) cleanupPreviewFor(r *http.Request, app store.App, event gitsrc.PushEvent) {
	sourceEnv, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
	if err != nil {
		return
	}
	env, err := s.db.FindEnvironmentBySourceRef(r.Context(), sourceEnv.ProjectID, previewRef(event))
	if err != nil {
		return
	}
	if s.cluster != nil {
		if err := s.cluster.DeleteNamespace(r.Context(), env.Namespace); err != nil {
			s.log.Warn("could not delete a preview namespace", "namespace", env.Namespace, "error", err)
		}
	}
	if err := s.db.DeleteEnvironment(r.Context(), env.ID); err != nil {
		s.log.Warn("could not delete a preview environment", "environment", env.ID, "error", err)
		return
	}
	s.log.Info("removed a preview environment", "environment", env.Name, "ref", env.SourceRef)
}

func previewName(event gitsrc.PushEvent) string {
	if event.PullRequest > 0 {
		return "Pull request #" + strconv.Itoa(event.PullRequest)
	}
	return "Branch " + strings.TrimSpace(event.SourceBranch)
}
