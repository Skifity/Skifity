package api

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"skifity/internal/auth"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/store"
	"skifity/internal/version"
)

// setupState guards first-run setup.
//
// The installer generates a one-time token and prints it. Until an account
// exists, the only way to create one is to present that token. This is what
// stops a panel that is briefly reachable on the internet from being claimed by
// whoever finds it first.
type setupState struct {
	mu        sync.Mutex
	token     string
	completed bool
	// attempts limits token guessing independently of the login rate limit,
	// because at this point there is no account to rate limit against.
	attempts int
}

const maxSetupAttempts = 10

func newSetupState(token string) *setupState {
	return &setupState{token: token}
}

func (s *setupState) check(candidate string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return errdoc.New("setup.already_done", "Setup has already been completed").
			WithCause("This panel already has an administrator account.").
			WithImpact("No account was created.").
			WithFix("Sign in with the account that was created during setup. If you have lost the password, see the account recovery guide.").
			WithDocs("/docs/troubleshooting#i-have-forgotten-my-password").
			WithStatus(http.StatusConflict)
	}
	if s.token == "" {
		return errdoc.New("setup.no_token", "This panel has no setup token").
			WithCause("The panel started without a one-time setup token, so first-run setup cannot be verified.").
			WithImpact("No account was created.").
			WithFix("Run the installer again, or write a token to the setup token file and restart the panel.").
			WithStatus(http.StatusInternalServerError)
	}
	if s.attempts >= maxSetupAttempts {
		return errdoc.New("setup.too_many_attempts", "Too many incorrect setup tokens").
			WithCause("The setup token has been guessed at too many times.").
			WithImpact("Setup is locked until the panel is restarted.").
			WithFix("Restart the panel, then copy the token from the installer output exactly.").
			WithStatus(http.StatusTooManyRequests)
	}
	if !crypto.ConstantTimeEqual(s.token, strings.TrimSpace(candidate)) {
		s.attempts++
		return errdoc.New("setup.bad_token", "That setup token is not correct").
			WithCause("The token you entered does not match the one this panel generated.").
			WithImpact("No account was created.").
			WithFix("Copy the token from the installer output. It was also written to " + version.ConfigDir + "/setup-token on the server.").
			WithStatus(http.StatusUnauthorized)
	}
	return nil
}

func (s *setupState) complete() {
	s.mu.Lock()
	s.completed = true
	s.token = ""
	s.mu.Unlock()
}

type setupStatusResponse struct {
	// NeedsSetup is what the frontend routes on.
	NeedsSetup bool   `json:"needs_setup"`
	Product    string `json:"product"`
	Version    string `json:"version"`
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	count, err := s.db.CountUsers(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	if count > 0 {
		s.setup.complete()
	}
	writeJSON(w, http.StatusOK, setupStatusResponse{
		NeedsSetup: count == 0,
		Product:    version.Name,
		Version:    version.Version,
	})
}

type setupRequest struct {
	Token    string `json:"token"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	TeamName string `json:"team_name"`
	Locale   string `json:"locale"`
}

type setupResponse struct {
	User        store.User `json:"user"`
	Team        store.Team `json:"team"`
	CSRFToken   string     `json:"csrf_token"`
	RecoveryKey string     `json:"recovery_key"`
}

// handleSetupComplete creates the first administrator, their team and a default
// project and environment, then signs them in.
func (s *Server) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	// Re-check against the database as well as the in-memory flag, so two
	// concurrent setup requests cannot both create an owner.
	count, err := s.db.CountUsers(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	if count > 0 {
		s.setup.complete()
	}
	if err := s.setup.check(req.Token); err != nil {
		writeError(w, r, err)
		return
	}

	email := strings.TrimSpace(req.Email)
	if !validEmail(email) {
		writeError(w, r, errdoc.BadRequest("That does not look like an email address."))
		return
	}
	if err := auth.DefaultPasswordPolicy().Check(req.Password); err != nil {
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}
	teamName := strings.TrimSpace(req.TeamName)
	if teamName == "" {
		teamName = "My team"
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}

	user := store.User{
		Email:        email,
		Name:         strings.TrimSpace(req.Name),
		PasswordHash: hash,
		IsAdmin:      true,
		Locale:       req.Locale,
	}
	if err := s.db.CreateUser(r.Context(), &user); err != nil {
		writeError(w, r, err)
		return
	}

	team := store.Team{Name: teamName, Slug: kube.Slugify(teamName)}
	if err := s.db.CreateTeam(r.Context(), &team); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.AddMember(r.Context(), team.ID, user.ID, store.RoleOwner); err != nil {
		writeError(w, r, err)
		return
	}

	// A brand new panel with no project is a dead end, so create the first one.
	project := store.Project{TeamID: team.ID, Name: "First project", Slug: "first-project"}
	if err := s.db.CreateProject(r.Context(), &project); err != nil {
		writeError(w, r, err)
		return
	}
	env := store.Environment{
		ProjectID: project.ID,
		Name:      "Production",
		Slug:      "production",
		Kind:      store.EnvStandard,
		Namespace: kube.NamespaceFor(team.Slug, project.Slug, "production"),
	}
	if err := s.db.CreateEnvironment(r.Context(), &env); err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster != nil {
		if err := s.cluster.EnsureNamespace(r.Context(), env, team.ID, project.ID); err != nil {
			// A cluster that is not ready yet must not block setup: the
			// namespace is created again whenever the environment is used.
			s.log.Warn("could not create the first namespace yet", "namespace", env.Namespace, "error", err)
		}
	}

	// The machine this panel is installed on is already a node of the cluster,
	// and until now nothing recorded it — so a brand new install opened on
	// "add your first server" while looking at a cluster that was already
	// running. See adopt.go.
	s.adoptClusterNodes(r.Context(), team.ID)

	s.setup.complete()
	// The token file is the only copy left on disk; remove it now that it has
	// been used, so it cannot be replayed.
	if s.cfg.SetupTokenPath != "" {
		if err := os.Remove(s.cfg.SetupTokenPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("could not remove the setup token file", "path", s.cfg.SetupTokenPath, "error", err)
		}
	}

	login, err := s.auth.IssueSession(r.Context(), user, clientIPFrom(r.Context()), r.UserAgent())
	if err != nil {
		writeError(w, r, err)
		return
	}
	http.SetCookie(w, s.auth.SessionCookie(login.Token, login.ExpiresAt))
	http.SetCookie(w, s.auth.CSRFCookie(login.CSRFToken, login.ExpiresAt))

	recoveryKey, err := s.keyring.RecoveryKey()
	if err != nil {
		s.log.Warn("could not render the recovery key", "error", err)
	}

	s.audit(r.WithContext(withUser(r, user)), team.ID, "setup.completed", "team", team.ID, team.Name)
	writeJSON(w, http.StatusCreated, setupResponse{
		User:        user,
		Team:        team,
		CSRFToken:   login.CSRFToken,
		RecoveryKey: recoveryKey,
	})
}

type metaResponse struct {
	Product   string    `json:"product"`
	Version   string    `json:"version"`
	Commit    string    `json:"commit"`
	Tagline   string    `json:"tagline"`
	Locales   []string  `json:"locales"`
	DevMode   bool      `json:"dev_mode"`
	SSO       ssoStatus `json:"sso"`
	ServerNow string    `json:"server_now"`
	// CLIPlatform is what the binary this panel is running was built for, so a
	// page offering it to download can say whose machine it will run on.
	CLIPlatform string `json:"cli_platform"`
	// CLIPlatforms is every platform /api/cli/download can serve, this one
	// first, so a page can offer the right file for the computer it is on.
	CLIPlatforms []string `json:"cli_platforms"`
}

// handleMeta gives the frontend everything it needs before a user signs in.
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, metaResponse{
		Product:      version.Name,
		Version:      version.Version,
		Commit:       version.Commit,
		Tagline:      version.Tagline,
		Locales:      []string{"en", "id", "hi", "ru", "zh-CN"},
		DevMode:      s.cfg.DevMode,
		SSO:          s.ssoStatus(r),
		CLIPlatform:  cliPlatform(),
		CLIPlatforms: s.cliPlatforms(),
		ServerNow:    time.Now().UTC().Format(time.RFC3339),
	})
}

// validEmail is deliberately permissive: the only reliable test of an address is
// sending to it, and a strict regex rejects valid addresses.
func validEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 {
		return false
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return false
	}
	local, domain := email[:at], email[at+1:]
	if strings.ContainsAny(local, " \t\n") || strings.ContainsAny(domain, " \t\n") {
		return false
	}
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}
