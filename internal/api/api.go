package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/docs"
	"skifity/internal/auth"
	"skifity/internal/config"
	"skifity/internal/crypto"
	"skifity/internal/docsite"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/metrics"
	"skifity/internal/notify"
	"skifity/internal/plugins"
	"skifity/internal/runsafe"
	"skifity/internal/store"
	"skifity/internal/upload"
)

// Server is the panel's HTTP API.
type Server struct {
	cfg     config.Config
	db      *store.DB
	keyring *crypto.Keyring
	auth    *auth.Service
	hub     *events.Hub
	log     *slog.Logger
	// metrics is what the panel says about itself, for whatever is watching it.
	metrics *metrics.Registry

	// setup guards first-run: it holds the one-time token until an admin exists.
	setup *setupState

	// cluster is the Kubernetes-facing side of the panel. It is an interface so
	// the API can be tested, and started, without a cluster.
	cluster Cluster

	// orchestrators are injected by the server command. They are interfaces for
	// the same reason.
	provisioner Provisioner
	deployer    Deployer
	databases   DatabaseManager
	backups     BackupManager

	// plugins is how installed plugins hear about what happened here. A value
	// with no Targets sends nothing, so nil is a working configuration.
	plugins plugins.Dispatcher

	// channels is the installed plugins that provide a notification channel,
	// for the kinds this panel does not send itself. Nil is a working
	// configuration and means only the built-in kinds exist.
	channels notify.Provider

	// uploads keeps the code of apps that have no repository. Nil means this
	// panel does not accept uploaded folders.
	uploads *upload.Store

	// frontend serves the embedded UI.
	frontend http.Handler

	router chi.Router
	// mcp is the panel's own MCP endpoint; see mcp_handlers.go.
	mcp http.Handler
}

// Options carries the Server's dependencies.
type Options struct {
	Config      config.Config
	DB          *store.DB
	Keyring     *crypto.Keyring
	Auth        *auth.Service
	Hub         *events.Hub
	Logger      *slog.Logger
	Cluster     Cluster
	Provisioner Provisioner
	Deployer    Deployer
	Databases   DatabaseManager
	Backups     BackupManager
	Plugins     plugins.Dispatcher
	// Channels is the installed plugins that provide a notification channel.
	// Nil is a panel with no plugins, and every built-in channel still works.
	Channels notify.Provider
	// Uploads is where the code of apps with no repository is kept.
	Uploads    *upload.Store
	Frontend   http.Handler
	SetupToken string
	// Metrics is shared with the orchestrators, so a deployment counted there
	// appears on the same page as a request counted here. Nil is fine and
	// means the panel keeps its own.
	Metrics *metrics.Registry
}

// New builds the API server and its routes.
func New(opts Options) *Server {
	s := &Server{
		cfg:         opts.Config,
		db:          opts.DB,
		keyring:     opts.Keyring,
		auth:        opts.Auth,
		hub:         opts.Hub,
		log:         opts.Logger,
		cluster:     opts.Cluster,
		provisioner: opts.Provisioner,
		deployer:    opts.Deployer,
		databases:   opts.Databases,
		backups:     opts.Backups,
		plugins:     opts.Plugins,
		channels:    opts.Channels,
		uploads:     opts.Uploads,
		frontend:    opts.Frontend,
		setup:       newSetupState(opts.SetupToken),
		metrics:     opts.Metrics,
	}
	if s.metrics == nil {
		s.metrics = metrics.New()
	}
	s.describeMetrics()
	if s.log == nil {
		s.log = slog.Default()
	}
	s.router = s.routes()
	s.mcp = s.mcpHandler()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.router.ServeHTTP(w, r) }

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()
	r.Use(s.requestContext)
	r.Use(s.recoverer)
	r.Use(s.accessLog)
	r.Use(s.securityHeaders)

	r.Route("/api", func(api chi.Router) {
		api.Use(noCache)
		api.Use(s.authenticate)
		api.Use(s.csrf)

		// Unauthenticated: liveness, readiness and the first-run handshake.
		api.Get("/health", s.handleHealth)
		api.Get("/ready", s.handleReady)
		api.Get("/meta", s.handleMeta)
		// The panel hands out the binary it is itself running, which is also
		// the CLI. See cli_handlers.go for why it is open.
		api.Get("/cli/download", s.handleDownloadCLI)
		api.Get("/setup/status", s.handleSetupStatus)
		api.Post("/setup", s.handleSetupComplete)

		api.Post("/auth/login", s.handleLogin)
		api.Post("/auth/logout", s.handleLogout)
		// Both halves of single sign-on are open, like signing in with a
		// password: somebody with no account has no credentials to present.
		// What makes them safe is the state and the nonce that have to come
		// back, and the ID token's signature.
		api.Get("/auth/sso/start", s.handleSSOStart)
		api.Get("/auth/sso/callback", s.handleSSOCallback)
		// Open for the same reason as signing in: somebody holding an
		// invitation has no account yet. The link's token is the credential,
		// it is stored hashed, single-use and expiring, and everything wrong
		// with one answers the same way so this cannot be used to find out
		// whether an address was invited.
		api.Get("/invitations/{token}", s.handleLookupInvitation)
		api.Post("/invitations/{token}/accept", s.handleAcceptInvitation)

		// Webhooks authenticate with their own per-source signature.
		api.Post("/webhooks/git/{sourceID}", s.handleGitWebhook)

		api.Group(func(authed chi.Router) {
			authed.Use(s.requireAuth)

			authed.Get("/me", s.handleMe)
			authed.Patch("/me", s.handleUpdateMe)
			authed.Post("/me/password", s.handleChangePassword)
			authed.Get("/me/sessions", s.handleListSessions)
			authed.Delete("/me/sessions", s.handleRevokeOtherSessions)
			authed.Delete("/me/sessions/{sessionID}", s.handleRevokeSession)
			authed.Get("/me/tokens", s.handleListTokens)
			authed.Delete("/me/tokens/{tokenID}", s.handleRevokeToken)
			// Proving who you are again. Not behind requireRecentAuth, for the
			// obvious reason.
			authed.Post("/me/reauth", s.handleReauth)
			authed.Get("/me/sso", s.handleGetSSOLink)

			// The three actions that turn a session somebody borrowed into
			// access they keep: switching the second factor off, reading the
			// recovery codes, and minting a token that outlives the session.
			// Each asks for the password again. See requireRecentAuth.
			authed.Group(func(sensitive chi.Router) {
				sensitive.Use(s.requireRecentAuth)
				sensitive.Post("/me/totp", s.handleStartTOTP)
				sensitive.Post("/me/totp/confirm", s.handleConfirmTOTP)
				sensitive.Delete("/me/totp", s.handleDisableTOTP)
				sensitive.Post("/me/tokens", s.handleCreateToken)
				// Linking a provider adds a way into the account, and
				// unlinking one removes it: both are the account's own
				// front door, so both ask for the password again.
				sensitive.Post("/me/sso/link", s.handleStartSSOLink)
				sensitive.Delete("/me/sso", s.handleUnlinkSSO)
			})

			authed.Get("/teams", s.handleListTeams)
			authed.Post("/teams", s.handleCreateTeam)

			authed.Route("/teams/{teamID}", func(team chi.Router) {
				team.Get("/", s.handleGetTeam)
				team.Patch("/", s.handleUpdateTeam)
				team.Get("/members", s.handleListMembers)
				team.Post("/members", s.handleAddMember)
				team.Post("/invitations", s.handleInvite)
				team.Get("/invitations", s.handleListInvitations)
				team.Delete("/invitations/{invitationID}", s.handleRevokeInvitation)
				team.Delete("/members/{userID}", s.handleRemoveMember)
				team.Get("/audit", s.handleListAudit)
				// Everything this team has, in one answer, so that leaving is
				// a command rather than a project. See export_handlers.go.
				team.Get("/export", s.handleExportTeam)

				team.Get("/projects", s.handleListProjects)
				team.Post("/projects", s.handleCreateProject)

				team.Get("/servers", s.handleListServers)
				team.Post("/servers", s.handleAddServer)
				team.Get("/cluster", s.handleClusterSummary)

				// Looking at a repository before creating anything from it.
				team.Post("/detect", s.handleDetect)
				team.Post("/detect-upload", s.handleDetectUpload)

				team.Get("/git-sources", s.handleListGitSources)
				team.Post("/git-sources", s.handleCreateGitSource)
				team.Delete("/git-sources/{sourceID}", s.handleDeleteGitSource)

				team.Get("/notifications/kinds", s.handleListNotificationKinds)
				team.Get("/notifications", s.handleListNotificationChannels)
				team.Post("/notifications", s.handleCreateNotificationChannel)
				team.Delete("/notifications/{channelID}", s.handleDeleteNotificationChannel)
				team.Post("/notifications/{channelID}/test", s.handleTestNotificationChannel)
			})

			authed.Route("/servers/{serverID}", func(server chi.Router) {
				server.Get("/", s.handleGetServer)
				server.Patch("/", s.handleUpdateServer)
				server.Delete("/", s.handleRemoveServer)
				server.Post("/retry", s.handleRetryServer)
				server.Post("/promote", s.handlePromoteServer)
				server.Get("/metrics", s.handleServerMetrics)
				server.Get("/usage", s.handleServerUsage)
				server.Get("/alerts", s.handleGetServerAlerts)
				server.Put("/alerts", s.handleSetServerAlerts)
			})

			authed.Route("/projects/{projectID}", func(project chi.Router) {
				project.Get("/", s.handleGetProject)
				project.Patch("/", s.handleUpdateProject)
				project.Delete("/", s.handleDeleteProject)
				project.Get("/environments", s.handleListEnvironments)
				project.Post("/environments", s.handleCreateEnvironment)
				project.Get("/variables", s.handleListSharedVariables)
				project.Put("/variables", s.handleSetSharedVariable)
				project.Post("/variables/batch", s.handleChangeSharedVariables)
				project.Delete("/variables/{key}", s.handleDeleteSharedVariable)
				project.Get("/canvas", s.handleProjectCanvas)
			})

			authed.Route("/environments/{envID}", func(env chi.Router) {
				env.Get("/", s.handleGetEnvironment)
				env.Patch("/", s.handleUpdateEnvironment)
				env.Delete("/", s.handleDeleteEnvironment)
				env.Get("/quota", s.handleEnvironmentQuota)
				env.Get("/apps", s.handleListApps)
				env.Post("/apps", s.handleCreateApp)
				env.Post("/stack", s.handleCreateStack)
				env.Get("/databases", s.handleListDatabases)
				env.Post("/databases", s.handleCreateDatabase)
			})

			authed.Route("/apps/{appID}", func(app chi.Router) {
				app.Get("/", s.handleGetApp)
				app.Patch("/", s.handleUpdateApp)
				app.Delete("/", s.handleDeleteApp)
				app.Get("/status", s.handleAppStatus)
				// What it used over time, and when to say so.
				app.Get("/metrics", s.handleAppMetrics)
				app.Get("/alerts", s.handleGetAppAlerts)
				app.Put("/alerts", s.handleSetAppAlerts)
				app.Get("/firewall", s.handleGetFirewall)
				app.Put("/firewall", s.handleSetFirewall)
				app.Get("/password", s.handleGetPassword)
				app.Put("/password", s.handleSetPassword)
				app.Delete("/password", s.handleRemovePassword)
				app.Post("/deploy", s.handleDeployApp)
				app.Put("/lock", s.handleLockDeploys)
				app.Delete("/lock", s.handleUnlockDeploys)
				app.Get("/maintenance", s.handleGetMaintenance)
				app.Put("/maintenance", s.handleStartMaintenance)
				app.Delete("/maintenance", s.handleEndMaintenance)
				// The code of an app with no repository, from `skifity up`.
				app.Put("/source", s.handleUploadSource)
				app.Get("/deployments", s.handleListDeployments)
				app.Get("/deployments/{deploymentID}", s.handleGetDeployment)
				app.Get("/deployments/{deploymentID}/logs", s.handleDeploymentLogs)
				app.Post("/deployments/{deploymentID}/cancel", s.handleCancelDeployment)
				app.Post("/rollback/{deploymentID}", s.handleRollback)
				// What that would change, asked before it is done.
				app.Get("/rollback/{deploymentID}/plan", s.handleRollbackPlan)
				app.Get("/logs", s.handleAppLogs)
				// A one-off command runs in the app's own image with the app's
				// own variables. It is where a migration runs.
				app.Post("/run", s.handleRunCommand)
				app.Get("/runs/{runID}/logs", s.handleRunLogs)
				app.Get("/jobs", s.handleListAppJobs)
				app.Post("/jobs", s.handleCreateAppJob)
				app.Patch("/jobs/{jobID}", s.handleUpdateAppJob)
				app.Delete("/jobs/{jobID}", s.handleDeleteAppJob)
				app.Post("/restart", s.handleRestartApp)
				app.Get("/variables", s.handleListVariables)
				app.Put("/variables", s.handleSetVariable)
				// Several at once, one rollout: a pasted .env, `env import`.
				app.Post("/variables/batch", s.handleChangeVariables)
				app.Delete("/variables/{key}", s.handleDeleteVariable)
				app.Put("/variables/{key}/preview", s.handleSetVariablePreview)
				app.Get("/domains", s.handleListDomains)
				app.Post("/domains", s.handleAddDomain)
				app.Delete("/domains/{domainID}", s.handleDeleteDomain)
				app.Get("/scaling", s.handleGetScaling)
				app.Put("/scaling", s.handleSetScaling)
				app.Get("/scaling/readiness", s.handleScalingReadiness)
				app.Get("/volumes", s.handleListVolumes)
				app.Post("/volumes", s.handleCreateVolume)
				app.Delete("/volumes/{volumeID}", s.handleDeleteVolume)
				app.Get("/volumes/{volumeID}/backups", s.handleListVolumeBackups)
				app.Post("/volumes/{volumeID}/backups", s.handleCreateVolumeBackup)
				app.Post("/volumes/{volumeID}/restore/{backupID}", s.handleRestoreVolumeBackup)
				app.Get("/volumes/{volumeID}/backup-policy", s.handleGetVolumeBackupPolicy)
				app.Put("/volumes/{volumeID}/backup-policy", s.handleSetVolumeBackupPolicy)
				app.Get("/advanced", s.handleAppAdvanced)
			})

			authed.Route("/databases/{databaseID}", func(dbr chi.Router) {
				dbr.Get("/", s.handleGetDatabase)
				dbr.Delete("/", s.handleDeleteDatabase)
				dbr.Get("/credentials", s.handleDatabaseCredentials)
				dbr.Post("/link", s.handleLinkDatabase)
				dbr.Delete("/link/{appID}", s.handleUnlinkDatabase)
				dbr.Get("/backups", s.handleListBackups)
				dbr.Post("/backups", s.handleCreateBackup)
				dbr.Get("/backup-policy", s.handleGetBackupPolicy)
				dbr.Put("/backup-policy", s.handleSetBackupPolicy)
				dbr.Post("/restore/{backupID}", s.handleRestoreBackup)
			})

			authed.Get("/operations/{operationID}", s.handleGetOperation)
			authed.Post("/operations/{operationID}/cancel", s.handleCancelOperation)

			// Plugins are owner-only: a plugin receives a token and runs a
			// container somebody else wrote.
			authed.Get("/plugins", s.handleListPlugins)
			authed.Get("/plugins/store", s.handleStoreCatalogue)
			authed.Post("/plugins/inspect", s.handleInspectPlugin)
			authed.Post("/plugins", s.handleInstallPlugin)
			authed.Patch("/plugins/{pluginID}", s.handleUpdatePlugin)
			authed.Delete("/plugins/{pluginID}", s.handleUninstallPlugin)

			authed.Get("/templates", s.handleListTemplates)
			authed.Get("/templates/{templateID}/icon", s.handleTemplateIcon)
			authed.Post("/templates/{templateID}/install", s.handleInstallTemplate)

			authed.Get("/events", s.handleEventStream)

			// The MCP server, for an assistant that cannot run the binary.
			// Each tool call is its own set of API requests with the same
			// token, checked the same way. See mcp_handlers.go.
			authed.Post("/mcp", s.handleMCP)
			authed.Get("/mcp", s.handleMCP)
			authed.Delete("/mcp", s.handleMCP)

			authed.Group(func(admin chi.Router) {
				admin.Use(s.requireAdmin)
				admin.Get("/settings", s.handleListSettings)
				admin.Put("/settings", s.handleUpdateSettings)
				admin.Get("/components", s.handleListComponents)
				admin.Post("/components/{name}/install", s.handleInstallComponent)
				admin.Post("/security/rotate-key", s.handleRotateMasterKey)
				admin.Get("/security/recovery-key", s.handleRecoveryKey)
				admin.Post("/security/recovery-key/saved", s.handleRecoveryKeySaved)
				admin.Get("/upgrade", s.handleUpgradeStatus)
				admin.Post("/upgrade", s.handleUpgrade)
				admin.Get("/panel/backups", s.handleListPanelBackups)
				admin.Post("/panel/backups", s.handleBackUpPanel)
				// Behind the same authentication as everything else. A
				// metrics page says how many apps and servers exist and how
				// the panel is doing, which is not a thing to hand to
				// anybody who can reach the port. Prometheus scrapes it with
				// an API token, the same way the CLI talks to the panel.
				admin.Get("/metrics", s.handleMetrics)
			})
		})

		// Anything under /api that did not match is a 404 in JSON, not HTML.
		api.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, r, errdoc.NotFound("endpoint", r.URL.Path))
		})
		api.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, r, errdoc.BadRequest("That method is not allowed on this endpoint.").
				WithStatus(http.StatusMethodNotAllowed))
		})
	})

	// The documentation is served from inside the binary, before the
	// single-page app gets the rest. An operator whose panel is broken is
	// often on a machine with no other browser and no outbound network, and
	// that is exactly when the troubleshooting page is needed.
	docsHandler := docsite.New(docs.FS(), "/docs/").Handler()
	r.Handle("/docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))
	r.Handle("/docs/*", docsHandler)

	// Everything else is the single-page app.
	if s.frontend != nil {
		r.Handle("/*", s.frontend)
	}
	return r
}

// --- authorization ---
//
// Every team-scoped handler goes through one of these. Authorization lives in a
// single place on purpose: a missing check in a handler is the most common way
// this class of product leaks data between tenants.

// authorizeTeam checks that the caller has at least the given role in a team,
// for something that belongs to the whole team.
//
// A member limited to some projects is refused here by default: servers, the
// cluster, notification channels, creating a project — none of it is inside a
// project. A handler that a limited member may use says so by calling
// authorizeTeamMember instead, which is a decision somebody made and not one a
// new route inherits.
func (s *Server) authorizeTeam(r *http.Request, teamID string, required store.Role) (store.User, error) {
	user, membership, err := s.authorizeTeamMember(r, teamID, required)
	if err != nil {
		return store.User{}, err
	}
	if membership.Scoped {
		return store.User{}, errdoc.ScopedToProjects()
	}
	return user, nil
}

// authorizeTeamMember checks the caller's role in a team and returns the
// membership, limits included, for a handler that serves a member limited to
// some projects and narrows what it answers with Membership.Reaches.
func (s *Server) authorizeTeamMember(r *http.Request, teamID string, required store.Role) (store.User, store.Membership, error) {
	user, membership, err := s.membershipIn(r, teamID)
	if err != nil {
		return store.User{}, store.Membership{}, err
	}
	if !membership.Role.AtLeast(required) {
		return store.User{}, store.Membership{}, errdoc.Forbidden("this action")
	}
	return user, membership, nil
}

// membershipIn finds the caller's membership of a team, with no role asked
// for yet.
func (s *Server) membershipIn(r *http.Request, teamID string) (store.User, store.Membership, error) {
	user, ok := UserFrom(r.Context())
	if !ok {
		return store.User{}, store.Membership{}, errdoc.Unauthorized()
	}
	// A token is issued for one team — the panel's form has no other option —
	// and until now that binding was stored and never read, so a token made
	// for one team worked on every team its owner belonged to. The answer is
	// the same as for a team that does not exist, so a token cannot be used to
	// find out which other teams there are.
	if token, ok := apiTokenFrom(r.Context()); ok && token.TeamID != "" && token.TeamID != teamID {
		return store.User{}, store.Membership{}, errdoc.NotFound("team", teamID)
	}
	membership, err := s.db.GetMembership(r.Context(), teamID, user.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Not a member: answer exactly as for a team that does not exist,
			// so membership cannot be probed.
			return store.User{}, store.Membership{}, errdoc.NotFound("team", teamID)
		}
		return store.User{}, store.Membership{}, err
	}
	// A team that requires a second factor or single sign-on is not shown to
	// a sign-in that used neither — members, viewers, admins and owners alike,
	// since a borrowed password is as much a way in to one as another.
	if membership.StrongAuthRequired && !s.strongAuth(r, user) {
		return store.User{}, store.Membership{}, errdoc.StrongAuthRequired()
	}
	return user, membership, nil
}

// strongAuth reports whether the request was signed into with more than a
// password.
//
// A browser session says how it was signed into. Somebody with two-factor on
// needs it at every sign-in, so their sessions count whatever they say — one
// from before they turned it on included, since turning it on took a step-up.
// An API token was minted by somebody signed in, and cannot say how; it counts
// when its owner has two-factor on, or signs in through the identity provider.
func (s *Server) strongAuth(r *http.Request, user store.User) bool {
	if user.TOTPEnabled {
		return true
	}
	if session, ok := sessionFrom(r.Context()); ok {
		return session.Method == auth.MethodSSO
	}
	if _, ok := apiTokenFrom(r.Context()); ok {
		identities, err := s.db.IdentitiesForUser(r.Context(), user.ID)
		return err == nil && len(identities) > 0
	}
	return false
}

// authorizeInProject checks that the caller's membership reaches a project and
// then their role. A project outside a member's limits answers exactly as one
// that does not exist, for the same reason another team's does: a limited
// member should not be able to find out what else the team has. That is also
// why the limit is checked first — a role refusal would be a 403, and a 403
// says the thing is there.
func (s *Server) authorizeInProject(r *http.Request, teamID, projectID string, required store.Role, kind, id string) (store.User, error) {
	user, membership, err := s.membershipIn(r, teamID)
	if err != nil {
		return store.User{}, err
	}
	if !membership.Reaches(projectID) {
		return store.User{}, errdoc.NotFound(kind, id)
	}
	if !membership.Role.AtLeast(required) {
		return store.User{}, errdoc.Forbidden("this action")
	}
	return user, nil
}

// authorizeApp resolves an app's team and checks the caller's role in it.
func (s *Server) authorizeApp(r *http.Request, appID string, required store.Role) (store.App, store.User, error) {
	teamID, projectID, err := s.db.ProjectOfApp(r.Context(), appID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.App{}, store.User{}, errdoc.NotFound("app", appID)
		}
		return store.App{}, store.User{}, err
	}
	user, err := s.authorizeInProject(r, teamID, projectID, required, "app", appID)
	if err != nil {
		return store.App{}, store.User{}, err
	}
	app, err := s.db.GetApp(r.Context(), appID)
	if err != nil {
		return store.App{}, store.User{}, errdoc.NotFound("app", appID)
	}
	return app, user, nil
}

// authorizeEnvironment resolves an environment's team and checks the role.
func (s *Server) authorizeEnvironment(r *http.Request, envID string, required store.Role) (store.Environment, store.User, error) {
	teamID, projectID, err := s.db.ProjectOfEnvironment(r.Context(), envID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Environment{}, store.User{}, errdoc.NotFound("environment", envID)
		}
		return store.Environment{}, store.User{}, err
	}
	user, err := s.authorizeInProject(r, teamID, projectID, required, "environment", envID)
	if err != nil {
		return store.Environment{}, store.User{}, err
	}
	env, err := s.db.GetEnvironment(r.Context(), envID)
	if err != nil {
		return store.Environment{}, store.User{}, errdoc.NotFound("environment", envID)
	}
	return env, user, nil
}

// authorizeProject resolves a project's team and checks the role.
func (s *Server) authorizeProject(r *http.Request, projectID string, required store.Role) (store.Project, store.User, error) {
	project, err := s.db.GetProject(r.Context(), projectID)
	if err != nil {
		return store.Project{}, store.User{}, errdoc.NotFound("project", projectID)
	}
	user, err := s.authorizeInProject(r, project.TeamID, project.ID, required, "project", projectID)
	if err != nil {
		return store.Project{}, store.User{}, err
	}
	return project, user, nil
}

// authorizeServer resolves a server's team and checks the role. Servers belong
// to the whole team, so a member limited to projects is refused.
func (s *Server) authorizeServer(r *http.Request, serverID string, required store.Role) (store.Server, store.User, error) {
	server, err := s.db.GetServer(r.Context(), serverID)
	if err != nil {
		return store.Server{}, store.User{}, errdoc.NotFound("server", serverID)
	}
	user, err := s.authorizeTeam(r, server.TeamID, required)
	if err != nil {
		return store.Server{}, store.User{}, err
	}
	return server, user, nil
}

// authorizeDatabase resolves a database's team and checks the role.
func (s *Server) authorizeDatabase(r *http.Request, databaseID string, required store.Role) (store.Database, store.User, error) {
	teamID, projectID, err := s.db.ProjectOfDatabase(r.Context(), databaseID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Database{}, store.User{}, errdoc.NotFound("database", databaseID)
		}
		return store.Database{}, store.User{}, err
	}
	user, err := s.authorizeInProject(r, teamID, projectID, required, "database", databaseID)
	if err != nil {
		return store.Database{}, store.User{}, err
	}
	record, err := s.db.GetDatabase(r.Context(), databaseID)
	if err != nil {
		return store.Database{}, store.User{}, errdoc.NotFound("database", databaseID)
	}
	return record, user, nil
}

// authorizeOperation checks the caller may see an operation. Most belong to
// the team — a server being added, a component installed — and a member
// limited to projects sees those as not there. One about an app, a database,
// an environment or a project is theirs if that project is.
func (s *Server) authorizeOperation(r *http.Request, op store.Operation, required store.Role) error {
	_, membership, err := s.authorizeTeamMember(r, op.TeamID, required)
	if err != nil {
		return err
	}
	if !membership.Scoped {
		return nil
	}
	var projectID string
	switch op.TargetType {
	case "app":
		_, projectID, err = s.db.ProjectOfApp(r.Context(), op.TargetID)
	case "database":
		_, projectID, err = s.db.ProjectOfDatabase(r.Context(), op.TargetID)
	case "environment":
		_, projectID, err = s.db.ProjectOfEnvironment(r.Context(), op.TargetID)
	case "project":
		projectID = op.TargetID
	}
	if err != nil || projectID == "" || !membership.Reaches(projectID) {
		return errdoc.NotFound("operation", op.ID)
	}
	return nil
}

// audit records an action. Audit failures never fail the request they describe,
// because refusing to do work because the log is full helps nobody.
func (s *Server) audit(r *http.Request, teamID, action, targetType, targetID, targetLabel string) {
	user, _ := UserFrom(r.Context())
	event := store.AuditEvent{
		TeamID:      teamID,
		ActorID:     user.ID,
		ActorLabel:  user.Email,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		TargetLabel: targetLabel,
		IP:          clientIPFrom(r.Context()),
		UserAgent:   truncate(r.UserAgent(), 255),
	}
	if err := s.db.RecordAudit(r.Context(), &event); err != nil {
		s.log.Warn("could not record audit event", "action", action, "error", err)
	}
	if teamID != "" {
		s.hub.Publish(events.TeamTopic(teamID), "audit", event)
	}
}

// Background starts the panel's periodic housekeeping. It returns when ctx ends.
func (s *Server) Background(ctx context.Context) {
	// The event hub is swept far more often than the hourly work below: every
	// deployment, operation and log stream is a topic of its own, and a panel
	// that never forgets them holds every log line it ever streamed.
	sweep := time.NewTicker(5 * time.Minute)
	defer sweep.Stop()

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			if dropped := s.hub.Sweep(time.Now()); dropped > 0 {
				s.log.Debug("forgot the history of finished topics", "topics", dropped)
			}
		case <-ticker.C:
			// A panic costs one hour of housekeeping, not the housekeeping.
			func() {
				defer runsafe.Recover(s.log, "the hourly housekeeping", nil)
				if _, err := s.db.PurgeExpiredSessions(ctx); err != nil {
					s.log.Warn("purge expired sessions", "error", err)
				}
				if err := s.db.PurgeOldLoginAttempts(ctx, time.Now().Add(-24*time.Hour)); err != nil {
					s.log.Warn("purge login attempts", "error", err)
				}
				if err := s.db.PurgeOldAudit(ctx, time.Now().Add(-90*24*time.Hour)); err != nil {
					s.log.Warn("purge audit events", "error", err)
				}
			}()
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
