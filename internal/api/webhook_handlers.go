package api

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
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
			// A monorepo app whose paths the push did not touch has nothing
			// new to build. The host shows this answer in its delivery log,
			// which is where somebody looks for why a push did not deploy.
			if patterns, _ := gitsrc.ParseWatchPaths(app.WatchPaths); !event.Touches(patterns) && s.runsFrom(r, app.ID, event.Before) {
				result.Skipped = append(result.Skipped, app.Name+" (nothing it watches changed)")
				continue
			}
			if s.deployer == nil {
				continue
			}
			// A locked app is skipped and says why, rather than failing a
			// deploy the webhook's sender would then show as an error.
			if _, err := s.db.GetDeployLock(r.Context(), app.ID); err == nil {
				result.Skipped = append(result.Skipped, app.Name+" (deploys are locked)")
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

// runsFrom reports whether an app's running version was built from a
// commit. A push's files are the difference from the commit before it, so
// they say nothing changed for an app only when that app runs that commit: a
// push that was never deployed — the app was locked, or its build failed —
// is otherwise skipped for good by the next push that touches nothing of it.
func (s *Server) runsFrom(r *http.Request, appID, commit string) bool {
	if commit == "" {
		return false
	}
	last, err := s.db.LatestSuccessfulDeployment(r.Context(), appID)
	return err == nil && last.CommitSHA == commit
}

// previewRef identifies the preview environment for a branch or pull
// request of one repository.
//
// The repository is part of it. It was not, so pull request 3 of the front
// end and pull request 3 of the API, in one project, were one preview: a
// stranger's fork pull request 3 on the API reused the front end's preview,
// deployed its own code into the copy there that held the API's secrets, and
// closing either pull request removed both.
func previewRef(event gitsrc.PushEvent) string {
	ref := "branch-" + kube.Slugify(event.SourceBranch)
	if event.PullRequest > 0 {
		ref = "pr-" + strconv.Itoa(event.PullRequest)
	}
	return ref + "-" + repoTag(event.RepoURL)
}

// repoTag is a short, stable name for a repository, for a preview's ref.
func repoTag(repoURL string) string {
	normal := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(repoURL), "/"), ".git"))
	sum := sha256.Sum256([]byte(normal))
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))[:6]
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
			FromFork:  event.Fork,
		}
		if err := s.db.CreateEnvironment(r.Context(), &env); err != nil {
			return "", err
		}
	}
	// Every time, not only when the environment is made: a namespace that
	// could not be made on the first push is made on the next one, rather
	// than every later deploy failing into a namespace that is not there.
	if s.cluster != nil {
		if err := s.cluster.EnsureNamespace(r.Context(), env, team.ID, project.ID); err != nil {
			return "", err
		}
	}

	// One app per preview environment, copied from the app being previewed.
	previewApp, _, err := s.previewCopy(r, env, app, event.SourceBranch, event.Fork)
	if err != nil {
		return "", err
	}
	// Before the previewed app's own deploy, and on every push: a copy that
	// failed last time is tried again, and one that exists is left alone.
	if sourceEnv.PreviewStack {
		s.previewStack(r, sourceEnv, env, app, event.Fork)
	}
	deployment, err := s.deployer.Deploy(r.Context(), DeployRequest{
		AppID: previewApp.ID, Trigger: "preview", CommitSHA: event.CommitSHA, CreatedBy: "webhook",
	})
	if err != nil {
		return "", err
	}
	return deployment.ID, nil
}

// previewCopy is an app's copy in a preview environment: the one already
// there, or a new one with the app's variables, its password, its processes
// and databases of its own. It reports whether it made one.
//
// A copy is made whole or not at all. One left half-made by an error — the
// row there, the password not yet — would be found by the next push and
// deployed as it was, a password-protected site in the open.
func (s *Server) previewCopy(r *http.Request, env store.Environment, app store.App, branch string, fork bool) (store.App, bool, error) {
	previewApps, err := s.db.ListApps(r.Context(), env.ID)
	if err != nil {
		return store.App{}, false, err
	}
	for _, candidate := range previewApps {
		if candidate.Slug == app.Slug {
			return candidate, false, nil
		}
	}

	previewApp := app
	previewApp.ID = ""
	previewApp.EnvironmentID = env.ID
	previewApp.Branch = branch
	previewApp.PreviewDeploys = false
	// The pull request's own events deploy a preview. Deploy on push as
	// well built every push twice, and a fork's pull request from its own
	// main was redeployed with the base repository's main on every push to it.
	previewApp.AutoDeploy = false
	previewApp.SeededAt = ""
	// A preview is throwaway, so it never autoscales and asks for little.
	previewApp.Autoscale = false
	previewApp.Replicas = 1
	if err := s.db.CreateApp(r.Context(), &previewApp); err != nil {
		return store.App{}, false, err
	}
	undo := func(err error) (store.App, bool, error) {
		if removeErr := s.db.DeleteApp(r.Context(), previewApp.ID); removeErr != nil {
			s.log.Warn("could not remove a half-made preview copy", "app", previewApp.ID, "error", removeErr)
		}
		return store.App{}, false, err
	}

	links, err := s.db.ListLinksForApp(r.Context(), app.ID)
	if err != nil {
		return undo(err)
	}
	linked := map[string]bool{}
	for _, link := range links {
		linked[link.VarName] = true
	}
	if err := s.copyPreviewVariables(r, app.ID, previewApp.ID, fork, linked); err != nil {
		return undo(err)
	}
	if fork {
		s.log.Info("a preview from a fork was given no secrets", "app", previewApp.ID)
	}
	// A preview of a site behind a password is behind the same one. Left
	// out, a staging site somebody locked would have every pull request's
	// copy of it open to whoever guessed the address. Only the hash is
	// copied, so a fork's preview learns nothing it could not already.
	if password, ok, err := s.db.GetAppPassword(r.Context(), app.ID); err != nil {
		return undo(err)
	} else if ok {
		password.AppID = previewApp.ID
		if err := s.db.SetAppPassword(r.Context(), password, "webhook"); err != nil {
			return undo(err)
		}
	}
	// Its processes too, one instance each: a pull request that changes a job
	// is tried out by a preview whose worker runs that job. One the app has
	// stopped stays stopped.
	processes, err := s.db.ListProcesses(r.Context(), app.ID)
	if err != nil {
		return undo(err)
	}
	for _, process := range processes {
		process.AppID, process.Instances = previewApp.ID, min(process.Instances, 1)
		if err := s.db.SetProcess(r.Context(), &process); err != nil {
			return undo(err)
		}
	}
	// Last, because a database is made in the cluster and is not undone
	// with a row: nothing after this can fail the copy.
	s.previewDatabases(r, env, previewApp, links, fork)
	return previewApp, true, nil
}

// previewStack copies the rest of an environment into a preview of it, for
// an environment that asks for that: the API the front end calls, the
// service a Compose file named, the app another repository builds. Each runs
// the version it runs in the environment it came from — only the pull
// request's own repository is built from the pull request — and stays there:
// it neither deploys on push nor previews itself.
//
// Only what the preview does not have yet is copied and deployed, so it runs
// on every push: an app that could not be copied last time is tried again,
// and one that was is not redeployed.
//
// Best effort, app by app. A copy that cannot be made or deployed is in the
// panel's log, and the rest of the preview is still worth having.
func (s *Server) previewStack(r *http.Request, sourceEnv, env store.Environment, previewed store.App, fork bool) {
	apps, err := s.db.ListApps(r.Context(), sourceEnv.ID)
	if err != nil {
		s.log.Warn("could not list the environment a preview copies", "environment", sourceEnv.ID, "error", err)
		return
	}
	for _, app := range apps {
		// The previewed app is there already, and an app its pull request
		// builds is copied by that app's own delivery of the same event.
		if app.ID == previewed.ID || (app.RepoURL != "" && app.RepoURL == previewed.RepoURL && app.AutoDeploy && app.PreviewDeploys) {
			continue
		}
		request := DeployRequest{Trigger: "preview", CreatedBy: "webhook"}
		if app.SourceType != "image" {
			last, err := s.db.LatestSuccessfulDeployment(r.Context(), app.ID)
			if err != nil {
				s.log.Info("an app a preview would copy has never been deployed", "app", app.ID)
				continue
			}
			request.Image, request.Fingerprint = last.Image, last.BuildFingerprint
			request.CommitSHA, request.CommitMessage, request.CommitAuthor = last.CommitSHA, last.CommitMessage, last.CommitAuthor
			// Its own settings, copied: nothing here was built differently.
			request.Force = true
		}
		copied, made, err := s.previewCopy(r, env, app, app.Branch, fork)
		if err != nil {
			s.log.Warn("could not copy an app into a preview", "app", app.ID, "error", err)
			continue
		}
		if !made {
			continue
		}
		request.AppID = copied.ID
		if _, err := s.deployer.Deploy(r.Context(), request); err != nil {
			s.log.Warn("could not deploy an app copied into a preview", "app", copied.ID, "error", err)
		}
	}
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
//
// A variable a database link wrote is never copied, fork or not: it is the
// address of the app's own database, and a preview is given one of its own
// instead (previewDatabases). Copied, it pointed every pull request at the
// production database — refused by the environment's network policy where one
// is enforced, and a migration from somebody's branch where one is not.
func (s *Server) copyPreviewVariables(r *http.Request, fromAppID, toAppID string, fork bool, linked map[string]bool) error {
	rows, err := s.db.ListVariables(r.Context(), fromAppID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if fork && row.IsSecret {
			continue
		}
		if linked[row.Key] {
			continue
		}
		// What the app said its previews get: nothing, a value of their own
		// — the test key rather than the live one — or its own.
		var plaintext []byte
		switch row.PreviewMode {
		case store.PreviewNone:
			continue
		case store.PreviewValue:
			plaintext, err = s.keyring.Open(row.PreviewSealed, previewVariableContext(fromAppID, row.Key))
		default:
			plaintext, err = s.keyring.Open(row.Sealed, variableContext(fromAppID, row.Key))
		}
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

// previewDatabases gives a new preview a database of its own for each one the
// app it copies is linked to: the same engine and version, small, empty, in the
// preview's namespace, linked under the same variable. It goes when the preview
// does, with the namespace and the environment it belongs to.
//
// Empty on purpose. A copy of production's data would put customers' records
// in every pull request, and a preview whose release command runs migrations
// is exactly the thing that has to run against something it can break.
//
// Not for a fork. Anybody can open a pull request from one, and a database per
// pull request is a way for a stranger to fill somebody's server. That preview
// starts without the variable, which fails loudly, rather than with production's.
//
// A database that cannot be made is not a reason to refuse the preview: the
// panel's log says why, and the preview starts without the variable.
func (s *Server) previewDatabases(r *http.Request, env store.Environment, previewApp store.App, links []store.DatabaseLink, fork bool) {
	if len(links) == 0 {
		return
	}
	if fork {
		s.log.Info("a preview from a fork was given no databases", "app", previewApp.ID)
		return
	}
	if s.databases == nil {
		s.log.Warn("a preview needs a database and this panel cannot create one", "app", previewApp.ID)
		return
	}
	teamID, _ := s.db.TeamIDForEnvironment(r.Context(), env.ID)
	for _, link := range links {
		source, err := s.db.GetDatabase(r.Context(), link.DatabaseID)
		if err != nil {
			s.log.Warn("could not read a linked database for a preview", "database", link.DatabaseID, "error", err)
			continue
		}
		// Two apps sharing a database share the preview's copy of it: the
		// API and its worker, the web and the admin. A copy each would be a
		// worker taking jobs from a queue nobody fills.
		record, found := s.previewDatabaseFor(r, env, source)
		if !found {
			record, err = s.databases.Create(r.Context(), env, CreateDatabaseRequest{
				Name: source.Name, Engine: source.Engine, Version: source.EngineVersion,
				// A throwaway copy for a pull request: one instance, the least
				// storage, whatever production asked for.
				StorageGB: 1, Instances: 1,
			})
			if err != nil {
				s.log.Warn("could not create a database for a preview",
					"app", previewApp.ID, "engine", source.Engine, "error", err)
				continue
			}
			s.audit(r, teamID, "database.created", "database", record.ID, record.Name)
		}
		if err := s.databases.Link(r.Context(), record.ID, previewApp.ID, link.VarName); err != nil {
			s.log.Warn("could not link a preview's database",
				"app", previewApp.ID, "database", record.ID, "error", err)
			continue
		}
		s.audit(r, teamID, "database.linked", "database", record.ID, previewApp.Name+" as "+link.VarName)
	}
}

// previewDatabaseFor finds the preview's copy of a database another app in
// it already made, by the name and engine it was made with.
func (s *Server) previewDatabaseFor(r *http.Request, env store.Environment, source store.Database) (store.Database, bool) {
	existing, err := s.db.ListDatabases(r.Context(), env.ID)
	if err != nil {
		return store.Database{}, false
	}
	for _, record := range existing {
		if record.Name == source.Name && record.Engine == source.Engine {
			return record, true
		}
	}
	return store.Database{}, false
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
	name := "Branch " + strings.TrimSpace(event.SourceBranch)
	if event.PullRequest > 0 {
		name = "Pull request #" + strconv.Itoa(event.PullRequest)
	}
	// Which repository's, for a project with apps from more than one.
	if repo := path.Base(strings.TrimSuffix(strings.TrimSuffix(event.RepoURL, "/"), ".git")); repo != "." && repo != "/" && repo != "" {
		name += " · " + repo
	}
	return name
}
