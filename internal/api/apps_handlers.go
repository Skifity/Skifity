package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/gitsrc"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/plugins"
	"skifity/internal/settings"
	"skifity/internal/store"
)

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	env, _, err := s.authorizeEnvironment(r, chi.URLParam(r, "envID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apps, err := s.db.ListApps(r.Context(), env.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, apps)
}

type createAppRequest struct {
	Name           string `json:"name"`
	SourceType     string `json:"source_type,omitempty"`
	GitSourceID    string `json:"git_source_id,omitempty"`
	RepoURL        string `json:"repo_url,omitempty"`
	Branch         string `json:"branch,omitempty"`
	RootDir        string `json:"root_dir,omitempty"`
	Builder        string `json:"builder,omitempty"`
	DockerfilePath string `json:"dockerfile_path,omitempty"`
	// BuildCommand and StaticDir are what a front end needs: the command that
	// produces the output, and the directory that output lands in. Without
	// them a static build serves the repository's source.
	BuildCommand   string `json:"build_command,omitempty"`
	StaticDir      string `json:"static_dir,omitempty"`
	Image          string `json:"image,omitempty"`
	Port           int    `json:"port,omitempty"`
	HealthPath     string `json:"health_path,omitempty"`
	StartCommand   string `json:"start_command,omitempty"`
	ReleaseCommand string `json:"release_command,omitempty"`
	// The health check: http, tcp or none, how long an instance may take to
	// start, and how long one check waits. Each left out is what every app
	// had before they could be chosen.
	HealthCheck          string `json:"health_check,omitempty"`
	HealthStartSeconds   *int   `json:"health_start_seconds,omitempty"`
	HealthTimeoutSeconds *int   `json:"health_timeout_seconds,omitempty"`
	// PreviewSeed runs once in each new preview of the app.
	PreviewSeed string `json:"preview_seed,omitempty"`
	// WatchPaths are the patterns a push has to touch to deploy the app, one
	// per line. Empty means every push.
	WatchPaths string `json:"watch_paths,omitempty"`
	// DeployTrigger is branch, the default, or tag; TagPattern is the
	// pattern a pushed tag has to match, v* when it is left out.
	DeployTrigger string `json:"deploy_trigger,omitempty"`
	TagPattern    string `json:"tag_pattern,omitempty"`
	// Internal apps are reached by name from their environment only.
	Internal bool `json:"internal,omitempty"`
	Deploy   bool `json:"deploy,omitempty"`
	// Variables are set on the new app before its first deploy. This exists
	// for the Compose form: a service's environment is most of what the file
	// says, and creating the app and then losing it would make the import a
	// list of names. Every value is sealed like any other variable, and none
	// of them is audited.
	Variables map[string]string `json:"variables,omitempty"`
	// Databases are created in the app's environment and linked to it before
	// its first deploy.
	//
	// This is what detection's "this app needs PostgreSQL" is for. Making the
	// database, linking it, and then deploying used to be three screens, and
	// the order was the whole trick: deploy first and the app starts once with
	// no DATABASE_URL, which for anything built on an ORM is a first deploy that
	// crashes. Somebody who has never deployed anything does not know the
	// order, so it is done for them, here, in one request.
	Databases []initialDatabase `json:"databases,omitempty"`
	// Processes run beside the app on its image: the worker and clock lines
	// of a Procfile that detection found, so they start with the first deploy
	// rather than after somebody finds the page to add them.
	Processes []initialProcess `json:"processes,omitempty"`
}

// initialProcess is one process to create with a new app.
type initialProcess struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Instances *int   `json:"instances,omitempty"`
}

// initialDatabase is one database to create and link with a new app.
type initialDatabase struct {
	Engine string `json:"engine"`
	// Variable is the name the app reads the connection from. Detection fills
	// it in from the app's own files; empty means the panel's default for the
	// engine.
	Variable string `json:"variable,omitempty"`
}

// databaseResult says what happened to one of them.
//
// A database that could not be made does not undo the app: the app is what
// somebody asked for, and a failed database is something they can retry from
// the app's page. So each one reports on its own and the app is still created.
type databaseResult struct {
	Engine     string `json:"engine"`
	Variable   string `json:"variable"`
	DatabaseID string `json:"database_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Error      string `json:"error,omitempty"`
}

// maxInitialDatabases bounds one request. Three engines are offered; four is
// room for none of them twice and a margin.
const maxInitialDatabases = 4

// maxRunAsUser is the largest uid a container can be given.
const maxRunAsUser = 1<<31 - 1

// validateInitialDatabases refuses a bad request before anything is created,
// so a typo cannot leave a half-made app behind it.
func validateInitialDatabases(requested []initialDatabase) ([]initialDatabase, error) {
	if len(requested) > maxInitialDatabases {
		return nil, errdoc.BadRequest(fmt.Sprintf("A new app can be created with at most %d databases.", maxInitialDatabases))
	}
	seen := map[string]bool{}
	out := make([]initialDatabase, 0, len(requested))
	for _, want := range requested {
		engine := strings.ToLower(strings.TrimSpace(want.Engine))
		offered := false
		for _, provided := range builder.ProvidedEngines {
			if provided == engine {
				offered = true
			}
		}
		if !offered {
			return nil, errdoc.BadRequest(fmt.Sprintf(
				"%q is not a database this panel can create; it can create %s.",
				want.Engine, strings.Join(builder.ProvidedEngines, ", ")))
		}
		if seen[engine] {
			return nil, errdoc.BadRequest(fmt.Sprintf("%s is asked for twice.", engine))
		}
		seen[engine] = true

		variable := strings.TrimSpace(want.Variable)
		if variable == "" {
			variable = defaultVarNameFor(engine)
		}
		key, err := kube.SanitiseEnvKey(variable)
		if err != nil {
			return nil, errdoc.BadRequest(err.Error())
		}
		out = append(out, initialDatabase{Engine: engine, Variable: key})
	}
	return out, nil
}

// createInitialDatabases makes and links each database, before the first
// deploy. Linking writes the connection string as a secret variable and would
// roll the app, but an app that has never been deployed has nothing to roll,
// so the first deploy is the first time it starts — with the variable there.
func (s *Server) createInitialDatabases(r *http.Request, env store.Environment, app store.App, wanted []initialDatabase) []databaseResult {
	if len(wanted) == 0 {
		return nil
	}
	teamID, _ := s.db.TeamIDForEnvironment(r.Context(), env.ID)
	results := make([]databaseResult, 0, len(wanted))
	for _, want := range wanted {
		result := databaseResult{Engine: want.Engine, Variable: want.Variable}
		if s.databases == nil {
			result.Error = "This panel has no cluster connection, so it cannot create databases."
			results = append(results, result)
			continue
		}
		record, err := s.databases.Create(r.Context(), env, CreateDatabaseRequest{
			Name: app.Name + " " + want.Engine, Engine: want.Engine,
		})
		if err != nil {
			result.Error = problemText(err)
			results = append(results, result)
			continue
		}
		result.DatabaseID, result.Name = record.ID, record.Name
		s.audit(r, teamID, "database.created", "database", record.ID, record.Name)
		s.plugins.Notify(r.Context(), plugins.EventDatabaseCreated, teamID, map[string]any{
			"database_id": record.ID, "database": record.Name,
			"engine": record.Engine, "environment_id": record.EnvironmentID,
		})

		if err := s.databases.Link(r.Context(), record.ID, app.ID, want.Variable); err != nil {
			result.Error = problemText(err)
			results = append(results, result)
			continue
		}
		s.audit(r, teamID, "database.linked", "database", record.ID, app.Name+" as "+want.Variable)
		results = append(results, result)
	}
	return results
}

// problemText is an error in the words a person reads: a catalogued problem's
// title and fix when it is one, the error itself otherwise.
func problemText(err error) string {
	var problem *errdoc.Problem
	if errors.As(err, &problem) {
		if problem.Fix != "" {
			return problem.Title + ". " + problem.Fix
		}
		return problem.Title + "."
	}
	return err.Error()
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	env, user, err := s.authorizeEnvironment(r, chi.URLParam(r, "envID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req createAppRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, r, errdoc.BadRequest("An app needs a name."))
		return
	}
	databases, err := validateInitialDatabases(req.Databases)
	if err != nil {
		writeError(w, r, err)
		return
	}
	sourceType := req.SourceType
	if sourceType == "" {
		if req.Image != "" {
			sourceType = "image"
		} else {
			sourceType = "git"
		}
	}
	repoURL := strings.TrimSpace(req.RepoURL)
	switch sourceType {
	case "git":
		if repoURL == "" {
			writeError(w, r, errdoc.BadRequest("Enter the URL of the Git repository to deploy."))
			return
		}
		// The address ends up in a shell script in the build pod and decides
		// where a Git token is sent, so it is checked here rather than trusted
		// there.
		checked, err := gitsrc.ValidateRepoURL(repoURL)
		if err != nil {
			writeError(w, r, errdoc.BadRequest(capitalise(err.Error())+"."))
			return
		}
		repoURL = checked
	case "image":
		if strings.TrimSpace(req.Image) == "" {
			writeError(w, r, errdoc.BadRequest("Enter the image to run, for example nginx:1.27."))
			return
		}
		if err := s.checkImageReference(r.Context(), req.Image, env.Namespace); err != nil {
			writeError(w, r, err)
			return
		}
	case "upload":
		// The code arrives afterwards, from `skifity up`, and nothing from a
		// repository or an image applies to it.
		repoURL = ""
		req.GitSourceID = ""
		req.Branch = ""
		req.Image = ""
		req.Deploy = false
	case "compose":
		// A Compose file describes several services and an app runs one, so
		// "compose" was never a source an app could have: it was accepted,
		// stored, and then deployed as a Git app with no repository. The form
		// reads the file, offers the services, and creates an ordinary app from
		// the one that is picked.
		writeError(w, r, errdoc.BadRequest(
			"A Compose file is several services, and an app runs one. "+
				"Create an app per service: paste the repository address and Skifity "+
				"offers the services it found."))
		return
	default:
		writeError(w, r, errdoc.BadRequest("Source must be git, image or upload."))
		return
	}
	watch, err := watchPaths(req.WatchPaths)
	if err != nil {
		writeError(w, r, err)
		return
	}
	trigger, tagPattern, err := deployTrigger(req.DeployTrigger, req.TagPattern)
	if err != nil {
		writeError(w, r, err)
		return
	}
	processes, err := initialProcesses(req.Processes)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.checkGitSource(r, req.GitSourceID, env.ID); err != nil {
		writeError(w, r, err)
		return
	}

	// An app and a database in one environment share a namespace, and both
	// render a Service under their slug. Two of them under one name is not two
	// things side by side: the second takes the first's Service over.
	slug := kube.Slugify(name)
	if owner, err := s.db.SlugOwnerInEnvironment(r.Context(), env.ID, slug); err != nil {
		writeError(w, r, err)
		return
	} else if owner != "" {
		writeError(w, r, errdoc.NameTaken(owner, name))
		return
	}

	app := store.App{
		EnvironmentID:  env.ID,
		Name:           name,
		Slug:           slug,
		SourceType:     sourceType,
		GitSourceID:    req.GitSourceID,
		RepoURL:        repoURL,
		Branch:         strings.TrimSpace(req.Branch),
		RootDir:        strings.TrimPrefix(strings.TrimSpace(req.RootDir), "/"),
		Builder:        defaultString(req.Builder, "auto"),
		DockerfilePath: strings.TrimSpace(req.DockerfilePath),
		Image:          strings.TrimSpace(req.Image),
		Port:           defaultInt(req.Port, kube.DefaultAppPort),
		HealthPath:     strings.TrimSpace(req.HealthPath),
		BuildCommand:   strings.TrimSpace(req.BuildCommand),
		StaticDir:      strings.TrimSpace(req.StaticDir),
		StartCommand:   strings.TrimSpace(req.StartCommand),
		ReleaseCommand: strings.TrimSpace(req.ReleaseCommand),
		PreviewSeed:    strings.TrimSpace(req.PreviewSeed),
		WatchPaths:     watch,
		DeployTrigger:  trigger,
		TagPattern:     tagPattern,
		Internal:       req.Internal,
		// Safe defaults, per the product principles: one instance, modest
		// limits, health checks on, deploy on push.
		Replicas:     1,
		MinReplicas:  1,
		MaxReplicas:  3,
		CPUTarget:    75,
		CPURequestM:  50,
		CPULimitM:    1000,
		MemRequestMB: 128,
		MemLimitMB:   512,
		AutoDeploy:   true,
		Status:       "created",
	}
	var check *string
	if req.HealthCheck != "" {
		check = &req.HealthCheck
	}
	if err := applyHealthSettings(&app, check, req.HealthStartSeconds, req.HealthTimeoutSeconds, false); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.CreateApp(r.Context(), &app); err != nil {
		writeError(w, r, err)
		return
	}

	// A database created with the app owns the variable it is linked as. The
	// same name in a pasted .env is the address of the one on the computer the
	// file came from — localhost, usually — and would be stored first and then
	// overwritten, or, if the link failed, left pointing at nothing.
	for _, db := range databases {
		delete(req.Variables, db.Variable)
	}

	// Before the first deploy, so the app never starts once without them.
	if err := s.setInitialVariables(r, app, req.Variables); err != nil {
		writeError(w, r, err)
		return
	}
	for i := range processes {
		processes[i].AppID = app.ID
		if err := s.db.SetProcess(r.Context(), &processes[i]); err != nil {
			writeError(w, r, err)
			return
		}
	}

	teamID, _ := s.db.TeamIDForEnvironment(r.Context(), env.ID)
	s.audit(r, teamID, "app.created", "app", app.ID, app.Name)
	s.hub.Publish(events.TeamTopic(teamID), "app.created", app)
	s.plugins.Notify(r.Context(), plugins.EventAppCreated, teamID, map[string]any{
		"app_id": app.ID, "app": app.Name, "environment_id": app.EnvironmentID,
	})

	hook := s.ensureWebhookFor(r, app)

	// Before the deploy, for the same reason as the variables above: the first
	// start is the one that has to have them.
	dbResults := s.createInitialDatabases(r, env, app, databases)

	answer := map[string]any{"app": app, "webhook": hook}
	if len(dbResults) > 0 {
		answer["databases"] = dbResults
	}
	if req.Deploy && s.deployer != nil {
		deployment, err := s.deployer.Deploy(r.Context(), DeployRequest{
			AppID: app.ID, Trigger: "create", CreatedBy: user.ID,
		})
		if err != nil {
			// The app exists; report the deploy failure without losing it.
			s.log.Warn("could not start the first deploy", "app", app.ID, "error", err)
		} else {
			answer["deployment"] = deployment
		}
	}
	writeJSON(w, http.StatusCreated, answer)
}

// checkGitSource refuses a Git connection that is not the environment's
// team's. An app's connection is what its builds clone with, what registers
// its webhook and what reads its repository, so another team's, named by its
// id, was that team's credential used sideways: their private repositories
// cloned into this team's app, and webhooks made with their token.
func (s *Server) checkGitSource(r *http.Request, sourceID, envID string) error {
	if sourceID == "" {
		return nil
	}
	teamID, err := s.db.TeamIDForEnvironment(r.Context(), envID)
	if err != nil {
		return err
	}
	source, err := s.db.GetGitSource(r.Context(), sourceID)
	if err != nil || source.TeamID != teamID {
		return errdoc.NotFound("Git connection", sourceID)
	}
	return nil
}

// webhookStatus is what the panel can tell somebody about deploy on push.
type webhookStatus struct {
	// Registered is true when pushes will reach the panel without anybody
	// setting anything up.
	Registered bool `json:"registered"`
	// URL is where the Git host should deliver, for the cases where somebody
	// has to add it themselves.
	URL string `json:"url,omitempty"`
	// Reason says why it could not be registered, in words to act on.
	Reason string `json:"reason,omitempty"`
}

// ensureWebhookFor asks the Git host to deliver this repository's pushes here.
//
// The connect form says a token is needed "so Skifity can read the repository
// and register a webhook", and for a long time only the first half happened:
// the panel printed a URL and a secret and left somebody to paste them into
// the Git host, once per repository. Miss it and deploy on push silently never
// works — nothing is broken, the panel is simply never told.
//
// Never fatal. A read-only token is the right token for somebody who deploys
// by hand, so a refusal comes back as "here is the URL, add it yourself".
func (s *Server) ensureWebhookFor(r *http.Request, app store.App) webhookStatus {
	if app.SourceType != "git" || app.GitSourceID == "" || !app.AutoDeploy {
		return webhookStatus{}
	}
	source, err := s.db.GetGitSource(r.Context(), app.GitSourceID)
	if err != nil {
		return webhookStatus{}
	}
	deliverTo := s.webhookURL(r, source.ID)
	secret, err := s.webhookSecretFor(r, source)
	if err != nil || secret == "" {
		return webhookStatus{URL: deliverTo, Reason: "this Git connection has no webhook secret"}
	}

	result := gitsrc.EnsureWebhook(r.Context(), gitsrc.HookRequest{
		RepoURL:   app.RepoURL,
		Kind:      source.Kind,
		BaseURL:   source.BaseURL,
		Token:     s.gitToken(r, source),
		DeliverTo: deliverTo,
		Secret:    secret,
	})
	if result.Registered() {
		if result.Created {
			s.audit(r, source.TeamID, "git_source.webhook_registered", "app", app.ID, app.Name)
		}
		return webhookStatus{Registered: true, URL: deliverTo}
	}
	s.log.Info("could not register a webhook; it can be added by hand",
		"app", app.ID, "reason", result.Reason)
	return webhookStatus{URL: deliverTo, Reason: result.Reason}
}

// setInitialVariables stores the variables a new app was created with.
//
// They go through the same key sanitising and the same sealing as a variable
// set by hand later; nothing here is a shortcut past either. A key the cluster
// could not carry stops the request rather than being dropped, because an app
// that starts without half its configuration looks like a broken app.
func (s *Server) setInitialVariables(r *http.Request, app store.App, values map[string]string) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, raw := range keys {
		key, err := kube.SanitiseEnvKey(raw)
		if err != nil {
			return errdoc.BadRequest(err.Error())
		}
		sealed, err := s.keyring.Seal([]byte(values[raw]), variableContext(app.ID, key))
		if err != nil {
			return err
		}
		// Nobody says which of these is a secret, because they arrive as a
		// map — from a Compose file, or from a pasted .env. That is exactly
		// where API keys are, so the same rule decides as everywhere else.
		variable := store.Variable{AppID: app.ID, Key: key, IsSecret: secretness(nil, false, key, values[raw])}
		if err := s.db.SetVariable(r.Context(), &variable, sealed); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// The lock goes with the app, so the page that would offer a deploy
	// knows not to.
	type appWithLock struct {
		store.App
		DeployLock *store.DeployLock `json:"deploy_lock,omitempty"`
		// Maintenance goes with it for the same reason: the page says so
		// at the top, where nobody can miss that visitors see a notice.
		Maintenance *store.Maintenance `json:"maintenance,omitempty"`
	}
	answer := appWithLock{App: app}
	lock, err := s.db.GetDeployLock(r.Context(), app.ID)
	switch {
	case err == nil:
		answer.DeployLock = &lock
	case !errors.Is(err, store.ErrNotFound):
		writeError(w, r, err)
		return
	}
	maintenance, err := s.db.GetMaintenance(r.Context(), app.ID)
	switch {
	case err == nil:
		answer.Maintenance = &maintenance
	case !errors.Is(err, store.ErrNotFound):
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

type lockRequest struct {
	Reason string `json:"reason"`
}

// handleLockDeploys stops every deploy and rollback of an app, from anywhere,
// until somebody unlocks it. A reason is required: a lock nobody can explain
// is one nobody dares lift.
func (s *Server) handleLockDeploys(w http.ResponseWriter, r *http.Request) {
	app, user, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req lockRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeError(w, r, errdoc.BadRequest("Say why deploys are locked, so whoever finds them locked knows who to ask and when it is over."))
		return
	}
	lock := store.DeployLock{AppID: app.ID, Reason: truncate(reason, 200), LockedBy: user.Email}
	if err := s.db.LockDeploys(r.Context(), &lock); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.deploys_locked", "app", app.ID, lock.Reason)
	writeJSON(w, http.StatusOK, lock)
}

func (s *Server) handleUnlockDeploys(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.UnlockDeploys(r.Context(), app.ID); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.deploys_unlocked", "app", app.ID, app.Name)
	writeOK(w)
}

type updateAppRequest struct {
	Name           *string `json:"name,omitempty"`
	Branch         *string `json:"branch,omitempty"`
	RootDir        *string `json:"root_dir,omitempty"`
	Builder        *string `json:"builder,omitempty"`
	DockerfilePath *string `json:"dockerfile_path,omitempty"`
	Image          *string `json:"image,omitempty"`
	Port           *int    `json:"port,omitempty"`
	HealthPath     *string `json:"health_path,omitempty"`
	BuildCommand   *string `json:"build_command,omitempty"`
	StaticDir      *string `json:"static_dir,omitempty"`
	StartCommand   *string `json:"start_command,omitempty"`
	ReleaseCommand *string `json:"release_command,omitempty"`
	// See createAppRequest. Runtime settings: changing one is a rollout.
	HealthCheck          *string `json:"health_check,omitempty"`
	HealthStartSeconds   *int    `json:"health_start_seconds,omitempty"`
	HealthTimeoutSeconds *int    `json:"health_timeout_seconds,omitempty"`
	// PreviewSeed runs once in each new preview of the app.
	PreviewSeed   *string `json:"preview_seed,omitempty"`
	WatchPaths    *string `json:"watch_paths,omitempty"`
	DeployTrigger *string `json:"deploy_trigger,omitempty"`
	TagPattern    *string `json:"tag_pattern,omitempty"`
	// RunAsUser is the uid for an image that names its user; 0 leaves it to
	// the image. A runtime setting, like the health check.
	RunAsUser      *int  `json:"run_as_user,omitempty"`
	Internal       *bool `json:"internal,omitempty"`
	AutoDeploy     *bool `json:"auto_deploy,omitempty"`
	PreviewDeploys *bool `json:"preview_deploys,omitempty"`
	CPURequestM    *int  `json:"cpu_request_m,omitempty"`
	CPULimitM      *int  `json:"cpu_limit_m,omitempty"`
	MemRequestMB   *int  `json:"mem_request_mb,omitempty"`
	MemLimitMB     *int  `json:"mem_limit_mb,omitempty"`
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req updateAppRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	deployedTags := app.AutoDeploy && app.DeployTrigger == gitsrc.DeployOnTag

	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		app.Name = strings.TrimSpace(*req.Name)
	}
	assignString(&app.Branch, req.Branch)
	assignString(&app.RootDir, req.RootDir)
	// As creating the app has it: a root is inside the repository, so a
	// leading slash says nothing, and one kept would make "/apps/web" and
	// "apps/web" two different settings.
	app.RootDir = strings.TrimPrefix(app.RootDir, "/")
	assignString(&app.Builder, req.Builder)
	assignString(&app.DockerfilePath, req.DockerfilePath)
	if req.Image != nil {
		env, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
		if err == nil {
			err = s.checkImageReference(r.Context(), *req.Image, env.Namespace)
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	assignString(&app.Image, req.Image)
	if req.RunAsUser != nil {
		if *req.RunAsUser < 0 || *req.RunAsUser > maxRunAsUser {
			writeError(w, r, errdoc.BadRequest(fmt.Sprintf(
				"Run as user is a uid from 1 to %d, or 0 to leave it to the image; root cannot be pinned.", maxRunAsUser)))
			return
		}
		// An image this panel builds runs as 1000 whatever is said here, so
		// saying something else would be a setting that does nothing.
		if *req.RunAsUser != 0 && (app.SourceType == "git" || app.SourceType == "upload") {
			writeError(w, r, errdoc.BadRequest(
				"Run as user is for an image somebody else built; an app built here always runs as 1000."))
			return
		}
		app.RunAsUser = *req.RunAsUser
	}
	pathWas := app.HealthPath
	assignString(&app.HealthPath, req.HealthPath)
	if err := applyHealthSettings(&app, req.HealthCheck, req.HealthStartSeconds, req.HealthTimeoutSeconds,
		app.HealthPath != pathWas); err != nil {
		writeError(w, r, err)
		return
	}
	assignString(&app.BuildCommand, req.BuildCommand)
	assignString(&app.StaticDir, req.StaticDir)
	assignString(&app.StartCommand, req.StartCommand)
	assignString(&app.ReleaseCommand, req.ReleaseCommand)
	assignString(&app.PreviewSeed, req.PreviewSeed)
	if req.WatchPaths != nil {
		watch, err := watchPaths(*req.WatchPaths)
		if err != nil {
			writeError(w, r, err)
			return
		}
		app.WatchPaths = watch
	}
	if req.DeployTrigger != nil || req.TagPattern != nil {
		trigger, pattern := app.DeployTrigger, app.TagPattern
		if req.DeployTrigger != nil {
			trigger = *req.DeployTrigger
		}
		if req.TagPattern != nil {
			pattern = *req.TagPattern
		}
		app.DeployTrigger, app.TagPattern, err = deployTrigger(trigger, pattern)
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	if req.Port != nil {
		if *req.Port < 0 || *req.Port > 65535 {
			writeError(w, r, errdoc.BadRequest("The port must be between 1 and 65535."))
			return
		}
		app.Port = *req.Port
	}
	if req.AutoDeploy != nil {
		app.AutoDeploy = *req.AutoDeploy
	}
	if req.Internal != nil {
		app.Internal = *req.Internal
	}
	if req.PreviewDeploys != nil {
		app.PreviewDeploys = *req.PreviewDeploys
	}
	if err := applyResourceChanges(&app, req); err != nil {
		writeError(w, r, err)
		return
	}

	if err := s.db.UpdateApp(r.Context(), &app); err != nil {
		writeError(w, r, err)
		return
	}

	// Runtime changes are applied by a rollout, never a rebuild: this is the
	// promise in ADR-0007, and the fix for the most common complaint about
	// comparable products.
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not apply app changes to the cluster", "app", app.ID, "error", err)
		}
	}

	// GitLab delivers a tag only to a hook that asked for tags, and the
	// hook this app's repository has may be older than tags were asked for.
	// The same call that made it adds them, whenever the app starts deploying
	// tags; it never fails the save.
	if app.AutoDeploy && app.DeployTrigger == gitsrc.DeployOnTag && !deployedTags {
		if hook := s.ensureWebhookFor(r, app); hook.Reason != "" {
			s.log.Warn("the repository may not deliver tags", "app", app.ID, "reason", hook.Reason)
		}
	}

	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.updated", "app", app.ID, app.Name)
	writeJSON(w, http.StatusOK, app)
}

func applyResourceChanges(app *store.App, req updateAppRequest) error {
	if req.CPURequestM != nil {
		app.CPURequestM = *req.CPURequestM
	}
	if req.CPULimitM != nil {
		app.CPULimitM = *req.CPULimitM
	}
	if req.MemRequestMB != nil {
		app.MemRequestMB = *req.MemRequestMB
	}
	if req.MemLimitMB != nil {
		app.MemLimitMB = *req.MemLimitMB
	}
	if app.CPURequestM < 10 || app.MemRequestMB < 16 {
		return errdoc.BadRequest("Reserve at least 10 millicores of CPU and 16 MB of memory, or the app will not start reliably.")
	}
	// A limit below the request is rejected by Kubernetes with a message nobody
	// can act on, so catch it here with one they can.
	if app.CPULimitM > 0 && app.CPULimitM < app.CPURequestM {
		return errdoc.BadRequest("The CPU limit cannot be lower than the CPU reservation.")
	}
	if app.MemLimitMB > 0 && app.MemLimitMB < app.MemRequestMB {
		return errdoc.BadRequest("The memory limit cannot be lower than the memory reservation.")
	}
	return nil
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	env, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster != nil {
		if err := s.cluster.DeleteApp(r.Context(), env.Namespace, app.Slug); err != nil {
			s.log.Warn("could not remove app from the cluster", "app", app.ID, "error", err)
		}
	}
	if err := s.db.DeleteApp(r.Context(), app.ID); err != nil {
		writeError(w, r, err)
		return
	}
	if s.uploads != nil {
		// After the row, so an app that could not be deleted keeps its code.
		if err := s.uploads.Remove(app.ID); err != nil {
			s.log.Warn("could not remove an app's uploaded code", "app", app.ID, "error", err)
		}
	}
	teamID, _ := s.db.TeamIDForEnvironment(r.Context(), env.ID)
	s.audit(r, teamID, "app.deleted", "app", app.ID, app.Name)
	s.hub.Publish(events.TeamTopic(teamID), "app.deleted", app)
	s.plugins.Notify(r.Context(), plugins.EventAppDeleted, teamID, map[string]any{
		"app_id": app.ID, "app": app.Name, "environment_id": app.EnvironmentID,
	})
	writeOK(w)
}

func (s *Server) handleAppStatus(w http.ResponseWriter, r *http.Request) {
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
	if s.cluster == nil {
		writeJSON(w, http.StatusOK, AppRuntimeStatus{Phase: "unknown", Detail: "The panel is not connected to a cluster."})
		return
	}
	status, err := s.cluster.AppStatus(r.Context(), env.Namespace, app.Slug)
	if err != nil {
		writeJSON(w, http.StatusOK, AppRuntimeStatus{Phase: "unknown", Detail: err.Error()})
		return
	}
	// An app that is asleep is not an app that is down. The cluster only knows
	// that the last instance is gone, so it says "stopped"; the panel knows the
	// user asked for exactly that and that the next request brings the app
	// back, and "Stopped" on a healthy app reads as a failure.
	if app.ScaleToZero && status.Phase == "stopped" {
		status.Phase = "sleeping"
		status.Detail = "This app is asleep because nobody is using it. The next request starts it again."
	}
	// An internal app has no public address, only the name its environment
	// reaches it by.
	if app.Internal {
		if app.Port > 0 {
			status.InternalAddress = fmt.Sprintf("%s:%d", app.Slug, app.Port)
		}
		writeJSON(w, http.StatusOK, status)
		return
	}
	domains, err := s.db.ListDomains(r.Context(), app.ID)
	if err == nil {
		for _, d := range domains {
			scheme := "http://"
			if d.TLS {
				scheme = "https://"
			}
			status.URLs = append(status.URLs, scheme+d.Hostname)
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleRestartApp(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	env, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	if err := s.cluster.RestartApp(r.Context(), env.Namespace, app.Slug); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForEnvironment(r.Context(), env.ID)
	s.audit(r, teamID, "app.restarted", "app", app.ID, app.Name)
	writeOK(w)
}

func (s *Server) handleAppAdvanced(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	env, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	// This is the one place the panel shows Kubernetes objects, so that an
	// advanced user is never blocked by the abstraction.
	manifests, err := s.cluster.Manifests(r.Context(), app, env)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"namespace": env.Namespace,
		"name":      app.Slug,
		"manifests": manifests,
	})
}

// --- variables ---

func (s *Server) handleListVariables(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := s.db.ListVariables(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]store.Variable, 0, len(rows))
	for _, row := range rows {
		v := row.Variable
		if v.IsSecret {
			// A secret is never shown again after it is set. This is the whole
			// point of storing it encrypted. Nor is its preview value.
			v.Value = ""
		} else {
			if plaintext, err := s.keyring.Open(row.Sealed, variableContext(app.ID, v.Key)); err == nil {
				v.Value = string(plaintext)
			}
			if v.PreviewMode == store.PreviewValue && row.PreviewSealed != "" {
				if plaintext, err := s.keyring.Open(row.PreviewSealed, previewVariableContext(app.ID, v.Key)); err == nil {
					v.PreviewValue = string(plaintext)
				}
			}
		}
		out = append(out, v)
	}
	writeList(w, out)
}

type setVariableRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// IsSecret is nil when the caller said nothing, which is not the same as
	// saying no. See secretness.
	IsSecret *bool `json:"is_secret,omitempty"`
	// BuildTime is nil when the caller said nothing, which keeps what the
	// variable was: a new value for NEXT_PUBLIC_API_URL from a form or a
	// command that does not ask made it a runtime variable, and the next
	// build went without it.
	BuildTime *bool `json:"build_time,omitempty"`
}

// buildTimeness decides whether a variable is read by the build: what the
// caller said, or what it was when the caller said nothing.
func buildTimeness(asked *bool, was bool) bool {
	if asked != nil {
		return *asked
	}
	return was
}

// secretness decides whether a variable is stored as a secret.
//
// asked is what the caller said, and nil when it said nothing. It used to be a
// plain bool, so saying nothing meant "not a secret" — and every path that says
// nothing is a path a secret arrives by. A pasted .env file, where somebody's
// API keys live. The MCP server, which sent false whenever a model left the
// field out, so a model overwriting STRIPE_KEY turned it into a variable the
// next list_variables handed back. The CLI without --secret.
//
// Now silence means: keep it a secret if it was one, and otherwise decide by
// the same rule the log redaction uses. An explicit answer is still obeyed —
// it only ever reveals the value the caller has just sent, never the old one.
func secretness(asked *bool, wasSecret bool, key, value string) bool {
	if asked != nil {
		return *asked
	}
	return wasSecret || logging.LooksSecret(key, value)
}

func (s *Server) handleSetVariable(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setVariableRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	key, err := kube.SanitiseEnvKey(req.Key)
	if err != nil {
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}

	sealed, err := s.keyring.Seal([]byte(req.Value), variableContext(app.ID, key))
	if err != nil {
		writeError(w, r, err)
		return
	}
	existing, err := s.db.ListVariables(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	wasSecret, wasBuildTime := false, false
	for _, row := range existing {
		if row.Key == key {
			wasSecret, wasBuildTime = row.IsSecret, row.BuildTime
		}
	}
	variable := store.Variable{
		AppID: app.ID, Key: key, BuildTime: buildTimeness(req.BuildTime, wasBuildTime),
		IsSecret: secretness(req.IsSecret, wasSecret, key, req.Value),
	}
	if err := s.db.SetVariable(r.Context(), &variable, sealed); err != nil {
		writeError(w, r, err)
		return
	}

	// A build-time variable changes the build fingerprint, so it does rebuild.
	// A runtime variable only rolls out. Either way the user is told which.
	rebuilt := variable.BuildTime
	if s.deployer != nil && !rebuilt {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not apply variable change", "app", app.ID, "error", err)
		}
	}

	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	// The value is never audited, only the key.
	s.audit(r, teamID, "variable.set", "app", app.ID, key)
	variable.Value = ""
	writeJSON(w, http.StatusOK, map[string]any{"variable": variable, "requires_rebuild": rebuilt})
}

type changeVariablesRequest struct {
	Set   []setVariableRequest `json:"set"`
	Unset []string             `json:"unset"`
}

// maxVariableChanges bounds one request. A .env is dozens of lines; thousands
// is not a configuration.
const maxVariableChanges = 500

// handleChangeVariables sets and removes several variables at once: all of
// them or none, and one rollout for the lot. Setting them one by one rolled
// the app out once per line, so pasting a .env of thirty lines restarted the
// app thirty times, and the first twenty-nine of those ran with half a
// configuration.
func (s *Server) handleChangeVariables(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req changeVariablesRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	existing, err := s.db.ListVariables(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	wasSecret, wasBuildTime := map[string]bool{}, map[string]bool{}
	for _, row := range existing {
		wasSecret[row.Key], wasBuildTime[row.Key] = row.IsSecret, row.BuildTime
	}
	batch, err := s.prepareVariableChanges(req, wasSecret, wasBuildTime, true,
		func(key string) string { return variableContext(app.ID, key) })
	if err != nil {
		writeError(w, r, err)
		return
	}

	if err := s.db.ChangeVariables(r.Context(), app.ID, batch.set, req.Unset); err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer != nil && batch.rollout {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not apply variable changes", "app", app.ID, "error", err)
		}
	}

	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	// Keys only, as for one variable: a value is never audited.
	s.audit(r, teamID, "variables.changed", "app", app.ID, batch.label())
	writeJSON(w, http.StatusOK, map[string]any{
		"set": batch.answer(), "unset": req.Unset, "requires_rebuild": batch.rebuild,
	})
}

// handleChangeSharedVariables is handleChangeVariables for a project's shared
// variables, which reach every app in it: one line at a time was one rollout
// of every app per line.
func (s *Server) handleChangeSharedVariables(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req changeVariablesRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	existing, err := s.db.ListSharedVariables(r.Context(), project.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	wasSecret := map[string]bool{}
	for _, row := range existing {
		wasSecret[row.Key] = row.IsSecret
	}
	batch, err := s.prepareVariableChanges(req, wasSecret, nil, false,
		func(key string) string { return sharedVariableContext(project.ID, key) })
	if err != nil {
		writeError(w, r, err)
		return
	}

	if err := s.db.ChangeSharedVariables(r.Context(), project.ID, batch.set, req.Unset); err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer != nil {
		apps, err := s.db.ListAppsForProject(r.Context(), project.ID)
		if err == nil {
			for _, app := range apps {
				if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
					s.log.Warn("could not apply shared variables", "app", app.ID, "error", err)
				}
			}
		}
	}
	s.audit(r, project.TeamID, "shared_variables.changed", "project", project.ID, batch.label())
	writeJSON(w, http.StatusOK, map[string]any{"set": batch.answer(), "unset": req.Unset})
}

// variableBatch is a checked set of changes, ready to store.
type variableBatch struct {
	set     []store.VariableChange
	keys    []string
	rebuild bool
	rollout bool
}

// label is what the audit log says: the keys, a removed one with a minus.
func (b variableBatch) label() string { return truncate(strings.Join(b.keys, ", "), 255) }

// answer is the variables as the response gives them back: never a value.
func (b variableBatch) answer() []store.Variable {
	out := make([]store.Variable, 0, len(b.set))
	for _, change := range b.set {
		change.Variable.Value = ""
		out = append(out, change.Variable)
	}
	return out
}

// prepareVariableChanges checks a batch and seals its values, before anything
// is written: a key that is not one, a key given twice, or a key both set and
// removed refuses the whole batch.
func (s *Server) prepareVariableChanges(req changeVariablesRequest, wasSecret, wasBuildTime map[string]bool,
	buildTimeAllowed bool, sealContext func(key string) string) (variableBatch, error) {
	var batch variableBatch
	if len(req.Set)+len(req.Unset) == 0 {
		return batch, errdoc.BadRequest("Give at least one variable to set or remove.")
	}
	if len(req.Set)+len(req.Unset) > maxVariableChanges {
		return batch, errdoc.BadRequest(fmt.Sprintf("At most %d variables can be changed at once.", maxVariableChanges))
	}
	seen := map[string]bool{}
	batch.rollout = len(req.Unset) > 0
	for _, item := range req.Set {
		key, err := kube.SanitiseEnvKey(item.Key)
		if err != nil {
			return batch, errdoc.BadRequest(err.Error())
		}
		if seen[key] {
			return batch, errdoc.BadRequest(fmt.Sprintf("%s is given twice. Say which value it should have once.", key))
		}
		seen[key] = true
		buildTime := buildTimeness(item.BuildTime, wasBuildTime[key])
		if buildTime && !buildTimeAllowed {
			return batch, errdoc.BadRequest("A shared variable is read when an app runs, never while it builds.")
		}
		sealed, err := s.keyring.Seal([]byte(item.Value), sealContext(key))
		if err != nil {
			return batch, err
		}
		batch.set = append(batch.set, store.VariableChange{
			Variable: store.Variable{
				Key: key, BuildTime: buildTime,
				IsSecret: secretness(item.IsSecret, wasSecret[key], key, item.Value),
			},
			Sealed: sealed,
		})
		if buildTime {
			batch.rebuild = true
		} else {
			batch.rollout = true
		}
		batch.keys = append(batch.keys, key)
	}
	for _, key := range req.Unset {
		if seen[key] {
			return batch, errdoc.BadRequest(fmt.Sprintf("%s is both set and removed. Say which.", key))
		}
		seen[key] = true
		batch.keys = append(batch.keys, "-"+key)
	}
	return batch, nil
}

type variablePreviewRequest struct {
	// Mode is "same", "value" or "none".
	Mode  string `json:"mode"`
	Value string `json:"value,omitempty"`
}

// handleSetVariablePreview says what a pull request's preview gets for one
// variable: the app's own value, a value of its own, or nothing.
//
// Copying every value is how a branch ends up charging real cards with the
// live payment key or mailing real customers. A preview-only value is the
// test key; none is a variable a preview should not have at all. Nothing is
// rolled out: the app itself does not change, and the next preview made is
// the first to see it.
func (s *Server) handleSetVariablePreview(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	key := chi.URLParam(r, "key")
	var req variablePreviewRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	mode, sealed := store.PreviewSame, ""
	switch req.Mode {
	case "", "same":
	case store.PreviewNone:
		mode = store.PreviewNone
	case store.PreviewValue:
		mode = store.PreviewValue
		if sealed, err = s.keyring.Seal([]byte(req.Value), previewVariableContext(app.ID, key)); err != nil {
			writeError(w, r, err)
			return
		}
	default:
		writeError(w, r, errdoc.BadRequest(`A preview gets "same", its own "value", or "none".`))
		return
	}
	if err := s.db.SetVariablePreview(r.Context(), app.ID, key, mode, sealed); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, errdoc.NotFound("variable", key))
			return
		}
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	// The key and the mode, never the value.
	s.audit(r, teamID, "variable.preview_set", "app", app.ID, key+": "+defaultString(mode, "same"))
	writeOK(w)
}

func (s *Server) handleDeleteVariable(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	key := chi.URLParam(r, "key")
	if err := s.db.DeleteVariable(r.Context(), app.ID, key); err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not apply variable removal", "app", app.ID, "error", err)
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "variable.deleted", "app", app.ID, key)
	writeOK(w)
}

func (s *Server) handleListSharedVariables(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := s.db.ListSharedVariables(r.Context(), project.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]store.SharedVariable, 0, len(rows))
	for _, row := range rows {
		v := row.SharedVariable
		if v.IsSecret {
			v.Value = ""
		} else if plaintext, err := s.keyring.Open(row.Sealed, sharedVariableContext(project.ID, v.Key)); err == nil {
			v.Value = string(plaintext)
		}
		out = append(out, v)
	}
	writeList(w, out)
}

func (s *Server) handleSetSharedVariable(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setVariableRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	key, err := kube.SanitiseEnvKey(req.Key)
	if err != nil {
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}
	sealed, err := s.keyring.Seal([]byte(req.Value), sharedVariableContext(project.ID, key))
	if err != nil {
		writeError(w, r, err)
		return
	}
	existing, err := s.db.ListSharedVariables(r.Context(), project.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	wasSecret := false
	for _, row := range existing {
		if row.Key == key {
			wasSecret = row.IsSecret
		}
	}
	variable := store.SharedVariable{
		ProjectID: project.ID, Key: key,
		IsSecret: secretness(req.IsSecret, wasSecret, key, req.Value),
	}
	if err := s.db.SetSharedVariable(r.Context(), &variable, sealed); err != nil {
		writeError(w, r, err)
		return
	}

	// A shared variable reaches every app in the project, so every app needs a
	// rollout, not just the one being edited.
	if s.deployer != nil {
		apps, err := s.db.ListAppsForProject(r.Context(), project.ID)
		if err == nil {
			for _, app := range apps {
				if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
					s.log.Warn("could not apply shared variable", "app", app.ID, "error", err)
				}
			}
		}
	}
	s.audit(r, project.TeamID, "shared_variable.set", "project", project.ID, key)
	variable.Value = ""
	writeJSON(w, http.StatusOK, variable)
}

func (s *Server) handleDeleteSharedVariable(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	key := chi.URLParam(r, "key")
	if err := s.db.DeleteSharedVariable(r.Context(), project.ID, key); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, project.TeamID, "shared_variable.deleted", "project", project.ID, key)
	writeOK(w)
}

// --- domains ---

func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	domains, err := s.db.ListDomains(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Where to point a domain of your own. The panel has known this since
	// there were settings — it is the same address it gives an app's automatic
	// subdomain — and the one screen that asks somebody to create a DNS record
	// never said it, so the record read "pointing to Unknown".
	if s.cluster != nil {
		env, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
		if err == nil {
			if teamID, err := s.db.TeamIDForEnvironment(r.Context(), env.ID); err == nil {
				if target := s.cluster.PublicAddress(r.Context(), teamID); target != "" {
					for i := range domains {
						// An automatic subdomain already points here.
						if !domains[i].Auto {
							domains[i].DNSTarget = target
						}
					}
				}
			}
		}
	}
	writeList(w, domains)
}

type addDomainRequest struct {
	Hostname string `json:"hostname"`
	TLS      *bool  `json:"tls,omitempty"`
	Path     string `json:"path,omitempty"`
}

func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req addDomainRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	hostname := kube.CleanHostname(req.Hostname)
	if !kube.ValidHostname(hostname) {
		writeError(w, r, errdoc.BadRequest("That does not look like a domain name. Enter something like app.example.com."))
		return
	}
	if s.isPanelHostname(r.Context(), hostname) {
		writeError(w, r, errdoc.New("domain.is_the_panel", "That is this panel's own address").
			WithCause("%s is where this panel answers.", hostname).
			WithImpact("Nothing was changed. Routing an app there would put two things behind one "+
				"hostname, and which of them answered would be decided by the ingress controller "+
				"rather than by anybody — including for the requests carrying your sign-in cookie.").
			WithFix("Give the app a hostname of its own. A subdomain of the panel's is fine.").
			WithStatus(http.StatusConflict))
		return
	}

	tls := true
	if req.TLS != nil {
		tls = *req.TLS
	}
	domain := store.Domain{
		AppID:    app.ID,
		Hostname: hostname,
		Path:     defaultString(req.Path, "/"),
		TLS:      tls,
		Status:   "pending",
	}
	if err := s.db.CreateDomain(r.Context(), &domain); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, r, errdoc.Conflict(
				"The domain "+hostname+" is already attached to another app.",
				"Remove it from the other app first, or pick a different hostname."))
			return
		}
		writeError(w, r, err)
		return
	}
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not apply new domain", "app", app.ID, "error", err)
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "domain.added", "app", app.ID, hostname)

	// What its DNS says now, so the answer to adding a domain is also the
	// answer to "is it pointing here yet". A lookup that fails says nothing
	// rather than failing what already succeeded: the domain is added, and
	// the check can be asked for again.
	answer := addedDomain{Domain: domain}
	if check, err := s.checkDomainDNS(r.Context(), app, hostname, dnsCheckOnAddTimeout); err == nil {
		answer.DNS = &check
	} else {
		s.log.Debug("could not check a new domain's DNS", "app", app.ID, "error", err)
	}
	writeJSON(w, http.StatusCreated, answer)
}

// isPanelHostname reports whether a hostname is the one the panel answers on.
//
// Nothing used to stop a member of any team pointing an app at it. Two Ingresses
// with the same host in different namespaces is not an error Kubernetes reports:
// the ingress controller picks one, and which one survives a restart is not
// something anybody decided. The app would then receive the requests a browser
// sends to the panel, session cookie included.
//
// Both sources are consulted because the panel learns its address in two ways:
// the installer passes it in the environment, and an operator can set it in
// Settings afterwards.
func (s *Server) isPanelHostname(ctx context.Context, hostname string) bool {
	candidates := []string{s.cfg.PublicURL}
	if configured, _, err := s.db.GetSetting(ctx, settings.KeyPanelURL); err == nil {
		candidates = append(candidates, configured)
	}
	for _, candidate := range candidates {
		if host := hostOf(candidate); host != "" && host == hostname {
			return true
		}
	}
	return false
}

// hostOf reduces a configured address to a bare lowercase hostname. The value
// may be a full URL, a host with a port, or a bare host, because three
// different things write it.
func hostOf(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "https://"), "http://")
	value = strings.Split(value, "/")[0]
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.TrimSuffix(value, ".")
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	domainID := chi.URLParam(r, "domainID")
	// Confirm the domain belongs to this app rather than trusting the URL.
	domains, err := s.db.ListDomains(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, d := range domains {
		if d.ID != domainID {
			continue
		}
		if d.Auto {
			writeError(w, r, errdoc.BadRequest("The automatic domain cannot be removed. It is how the app stays reachable while you set up your own domain."))
			return
		}
		if err := s.db.DeleteDomain(r.Context(), domainID); err != nil {
			writeError(w, r, err)
			return
		}
		if s.deployer != nil {
			if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
				s.log.Warn("could not apply domain removal", "app", app.ID, "error", err)
			}
		}
		teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
		s.audit(r, teamID, "domain.removed", "app", app.ID, d.Hostname)
		writeOK(w)
		return
	}
	writeError(w, r, errdoc.NotFound("domain", domainID))
}

// --- volumes ---

func (s *Server) handleListVolumes(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	volumes, err := s.db.ListVolumes(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, volumes)
}

type createVolumeRequest struct {
	Name      string `json:"name"`
	MountPath string `json:"mount_path"`
	SizeGB    int    `json:"size_gb"`
}

func (s *Server) handleCreateVolume(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req createVolumeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if !strings.HasPrefix(req.MountPath, "/") {
		writeError(w, r, errdoc.BadRequest("The mount path must be absolute, for example /data."))
		return
	}
	if req.SizeGB < 1 {
		req.SizeGB = 1
	}
	volume := store.Volume{
		AppID:     app.ID,
		Name:      kube.Slugify(defaultString(req.Name, "data")),
		MountPath: strings.TrimSuffix(req.MountPath, "/"),
		SizeGB:    req.SizeGB,
	}
	if err := s.db.CreateVolume(r.Context(), &volume); err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not attach volume", "app", app.ID, "error", err)
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "volume.created", "app", app.ID, volume.Name)
	writeJSON(w, http.StatusCreated, volume)
}

// handleListVolumeBackups lists the copies of one volume.
func (s *Server) handleListVolumeBackups(w http.ResponseWriter, r *http.Request) {
	volume, _, err := s.authorizeVolume(r, store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	backups, err := s.db.ListBackups(r.Context(), "volume", volume.ID, queryInt(r, "limit", 50))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, backups)
}

// handleCreateVolumeBackup copies a volume to storage now.
func (s *Server) handleCreateVolumeBackup(w http.ResponseWriter, r *http.Request) {
	volume, app, err := s.authorizeVolume(r, store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.backups == nil {
		writeError(w, r, errdoc.NotConfigured("Backups", "Settings, then Storage"))
		return
	}
	backup, err := s.backups.Run(r.Context(), "volume", volume.ID, "manual")
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "backup.started", "volume", volume.ID, app.Name+" / "+volume.Name)
	writeJSON(w, http.StatusAccepted, backup)
}

// handleRestoreVolumeBackup puts an archive back into the volume it came from.
//
// A backup that cannot be restored is a file somebody is paying to store. The
// job that does the work has existed since volume backups were added and
// nothing ever called it.
func (s *Server) handleRestoreVolumeBackup(w http.ResponseWriter, r *http.Request) {
	volume, app, err := s.authorizeVolume(r, store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.backups == nil {
		writeError(w, r, errdoc.NotConfigured("Backups", "Settings, then Storage"))
		return
	}
	backupID := chi.URLParam(r, "backupID")
	backup, err := s.db.GetBackup(r.Context(), backupID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// A backup id belonging to another volume must not be restorable here.
	if backup.TargetType != "volume" || backup.TargetID != volume.ID {
		writeError(w, r, errdoc.NotFound("backup", backupID))
		return
	}

	op, err := s.backups.RestoreVolume(r.Context(), backupID, queryBool(r, "overwrite"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "backup.restore_started", "volume", volume.ID, app.Name+" / "+volume.Name)
	writeJSON(w, http.StatusAccepted, op)
}

// handleGetVolumeBackupPolicy reads a volume's backup schedule.
//
// The scheduler has always been able to run one — it reads a policy's target
// type and a volume is one of them — and there was no way to create it. So the
// answer to "back up my uploads every night" was to press a button every night.
func (s *Server) handleGetVolumeBackupPolicy(w http.ResponseWriter, r *http.Request) {
	volume, _, err := s.authorizeVolume(r, store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	policy, err := s.db.GetBackupPolicy(r.Context(), "volume", volume.ID)
	if err != nil {
		// No policy is a normal state, not an error: show the defaults.
		writeJSON(w, http.StatusOK, store.BackupPolicy{
			TargetType: "volume", TargetID: volume.ID,
			Schedule: "0 3 * * *", Retention: 7, Destination: "s3", Enabled: false,
		})
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) handleSetVolumeBackupPolicy(w http.ResponseWriter, r *http.Request) {
	volume, app, err := s.authorizeVolume(r, store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setBackupPolicyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	policy, err := s.backupPolicyFrom(r, req, "volume", volume.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.SetBackupPolicy(r.Context(), &policy); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "backup.policy_changed", "volume", volume.ID, app.Name+" / "+volume.Name)
	writeJSON(w, http.StatusOK, policy)
}

// authorizeVolume resolves a volume through its app, so a volume id from
// another team cannot be reached by guessing it.
func (s *Server) authorizeVolume(r *http.Request, required store.Role) (store.Volume, store.App, error) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), required)
	if err != nil {
		return store.Volume{}, store.App{}, err
	}
	volumeID := chi.URLParam(r, "volumeID")
	volume, err := s.db.GetVolume(r.Context(), volumeID)
	if err != nil || volume.AppID != app.ID {
		return store.Volume{}, store.App{}, errdoc.NotFound("volume", volumeID)
	}
	return volume, app, nil
}

func (s *Server) handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.DeleteVolume(r.Context(), app.ID, chi.URLParam(r, "volumeID")); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "volume.deleted", "app", app.ID, chi.URLParam(r, "volumeID"))
	writeOK(w)
}

// variableContext and sharedVariableContext bind a sealed value to exactly one
// row, so a ciphertext copied between rows cannot be decrypted.
func variableContext(appID, key string) string { return "variable:" + appID + ":" + key }

// previewVariableContext is a variable's preview value's own context, so the
// one ciphertext cannot be moved into the other's column and read as it.
func previewVariableContext(appID, key string) string {
	return "variable_preview:" + appID + ":" + key
}
func sharedVariableContext(projectID, key string) string {
	return "shared_variable:" + projectID + ":" + key
}

// defaultInt is for the settings where zero means "nothing was said" rather
// than "zero".
func defaultInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

// capitalise turns a validator's message into a sentence, because validators
// speak in fragments and the error catalogue speaks in sentences.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func assignString(dst *string, src *string) {
	if src != nil {
		*dst = strings.TrimSpace(*src)
	}
}

// watchPaths checks the patterns an app watches and returns them as stored:
// one per line, comments kept, blank lines and surrounding space dropped.
func watchPaths(text string) (string, error) {
	_, err := gitsrc.ParseWatchPaths(text)
	var bad *gitsrc.BadWatchPath
	switch {
	case errors.As(err, &bad):
		return "", errdoc.WatchPathInvalid(bad.Line)
	case err != nil:
		return "", errdoc.TooManyWatchPaths(gitsrc.MaxWatchPaths)
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), nil
}

// deployTrigger checks what an app deploys on and the pattern a tag has to
// match, and returns them as stored. An empty trigger is a branch and an
// empty pattern is v*, which is what an app has before anybody chooses.
//
// The pattern is kept when the app deploys on its branch, so switching to tags
// and back does not lose what was typed.
func deployTrigger(trigger, pattern string) (string, string, error) {
	trigger = strings.TrimSpace(trigger)
	if trigger == "" {
		trigger = gitsrc.DeployOnBranch
	}
	if trigger != gitsrc.DeployOnBranch && trigger != gitsrc.DeployOnTag {
		return "", "", errdoc.DeployTriggerInvalid(trigger)
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		pattern = gitsrc.DefaultTagPattern
	}
	if err := gitsrc.ValidTagPattern(pattern); err != nil {
		return "", "", errdoc.TagPatternInvalid(pattern)
	}
	return trigger, pattern, nil
}

// checkImageReference refuses an image this panel built for another
// environment.
//
// Built images live at <registry>/<namespace>/<app>:<tag>, in the registry
// inside the cluster — which every node mirrors on 127.0.0.1 and serves to
// anybody — or in an external one whose pull secret is in every namespace.
// Naming another team's path ran their code, and read their image, under an
// app of one's own. An image from anywhere else is the team's own business.
func (s *Server) checkImageReference(ctx context.Context, image, namespace string) error {
	image = strings.TrimSpace(image)
	prefixes := []string{
		kube.RegistryHost() + "/",
		fmt.Sprintf("127.0.0.1:%d/", kube.RegistryNodePort),
		fmt.Sprintf("localhost:%d/", kube.RegistryNodePort),
	}
	if external, _, err := s.db.GetSetting(ctx, settings.KeyRegistryURL); err == nil && strings.TrimSpace(external) != "" {
		prefixes = append(prefixes, strings.TrimSuffix(strings.TrimSpace(external), "/")+"/")
	}
	for _, prefix := range prefixes {
		rest, ok := strings.CutPrefix(image, prefix)
		if !ok {
			continue
		}
		if !strings.HasPrefix(rest, namespace+"/") {
			return errdoc.BadRequest("That image was built by this panel for another environment, and an image from its " +
				"registry runs only in the environment that built it. Promote the version instead.")
		}
	}
	return nil
}
