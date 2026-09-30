package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/auth"
	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code,omitempty"`
}

type loginResponse struct {
	User      store.User `json:"user"`
	CSRFToken string     `json:"csrf_token"`
	ExpiresAt time.Time  `json:"expires_at"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	result, err := s.auth.Login(r.Context(), req.Email, req.Password, req.TOTPCode,
		clientIPFrom(r.Context()), r.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrTOTPRequired):
			// Not a failure: the client now shows the code field.
			writeJSON(w, http.StatusOK, map[string]any{"totp_required": true})
			return
		case errors.Is(err, auth.ErrLockedOut):
			writeError(w, r, errdoc.RateLimited(s.auth.LockoutWindow().String()))
			return
		case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrInvalidTOTP):
			writeError(w, r, errdoc.New("auth.invalid_credentials", "Email or password is not correct").
				WithCause("The email address and password do not match an account on this panel.").
				WithImpact("You are not signed in.").
				WithFix("Check for typos and try again. After several failed attempts sign-in pauses for a while.").
				WithStatus(http.StatusUnauthorized))
			return
		default:
			writeError(w, r, err)
			return
		}
	}

	http.SetCookie(w, s.auth.SessionCookie(result.Token, result.ExpiresAt))
	http.SetCookie(w, s.auth.CSRFCookie(result.CSRFToken, result.ExpiresAt))

	s.audit(r.WithContext(withUser(r, result.User)), "", "auth.login", "user", result.User.ID, result.User.Email)
	writeJSON(w, http.StatusOK, loginResponse{
		User:      result.User,
		CSRFToken: result.CSRFToken,
		ExpiresAt: result.ExpiresAt,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if session, ok := sessionFrom(r.Context()); ok {
		if err := s.auth.Logout(r.Context(), session.ID); err != nil {
			writeError(w, r, err)
			return
		}
		s.audit(r, "", "auth.logout", "user", session.UserID, "")
	}
	http.SetCookie(w, s.auth.ClearCookie(auth.SessionCookieName))
	http.SetCookie(w, s.auth.ClearCookie(auth.CSRFCookieName))
	writeOK(w)
}

type meResponse struct {
	User          store.User   `json:"user"`
	Teams         []store.Team `json:"teams"`
	RecoverySaved bool         `json:"recovery_saved"`
	// RecoveryCodesLeft is how many unused two-factor recovery codes remain.
	// It is on this response because the account page is the only place that
	// can tell somebody they are down to their last one.
	RecoveryCodesLeft int `json:"recovery_codes_left"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	teams, err := s.db.ListTeamsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// A bound token sees the one team it is for. Listing the others would send
	// the CLI, which picks the only team when there is one, to a team every
	// other request would then refuse.
	if token, ok := apiTokenFrom(r.Context()); ok && token.TeamID != "" {
		bound := make([]store.Team, 0, 1)
		for _, team := range teams {
			if team.ID == token.TeamID {
				bound = append(bound, team)
			}
		}
		teams = bound
	}
	codesLeft, err := s.auth.RecoveryCodesLeft(r.Context(), user.ID)
	if err != nil {
		s.log.Warn("could not count recovery codes", "user", user.ID, "error", err)
	}
	writeJSON(w, http.StatusOK, meResponse{
		User: user, Teams: teams, RecoverySaved: user.RecoverySaved, RecoveryCodesLeft: codesLeft,
	})
}

type updateMeRequest struct {
	Name   *string `json:"name,omitempty"`
	Locale *string `json:"locale,omitempty"`
	Theme  *string `json:"theme,omitempty"`
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	var req updateMeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Name != nil {
		user.Name = strings.TrimSpace(*req.Name)
	}
	if req.Locale != nil {
		locale := strings.TrimSpace(*req.Locale)
		if locale != "" && !supportedLocale(locale) {
			writeError(w, r, errdoc.BadRequest("That language is not one this panel ships with."))
			return
		}
		user.Locale = locale
	}
	if req.Theme != nil {
		switch *req.Theme {
		case "light", "dark", "system":
			user.Theme = *req.Theme
		default:
			writeError(w, r, errdoc.BadRequest("Theme must be light, dark or system."))
			return
		}
	}
	if err := s.db.UpdateUser(r.Context(), &user); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	keep := ""
	if session, ok := sessionFrom(r.Context()); ok {
		keep = session.ID
	}
	if err := s.auth.ChangePassword(r.Context(), &user, req.CurrentPassword, req.NewPassword, keep); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, r, errdoc.BadRequest("Your current password is not correct.").
				WithStatus(http.StatusUnauthorized))
			return
		}
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}
	s.audit(r, "", "auth.password_changed", "user", user.ID, user.Email)
	writeOK(w)
}

type reauthRequest struct {
	Password string `json:"password"`
	TOTPCode string `json:"totp_code,omitempty"`
}

// handleReauth re-checks the password, and the second factor when the account
// has one, and stamps the session. It is what the dialog in front of a
// sensitive action posts to; nothing is issued and nothing is changed.
func (s *Server) handleReauth(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	session, ok := sessionFrom(r.Context())
	if !ok {
		// A bearer token has nobody at the keyboard to ask.
		writeError(w, r, errdoc.New("auth.needs_person", "This has to be done from the panel").
			WithCause("Confirming who you are needs a browser session, and this request carried an API token.").
			WithImpact("Nothing was changed.").
			WithFix("Sign in to the panel and do it there.").
			WithStatus(http.StatusForbidden))
		return
	}
	var req reauthRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	err := s.auth.Reauthenticate(r.Context(), user, session.ID,
		clientIPFrom(r.Context()), req.Password, req.TOTPCode)
	switch {
	case err == nil:
		s.audit(r, "", "auth.reauthenticated", "user", user.ID, user.Email)
		writeOK(w)
	case errors.Is(err, auth.ErrTOTPRequired):
		writeJSON(w, http.StatusOK, map[string]any{"totp_required": true})
	case errors.Is(err, auth.ErrLockedOut):
		writeError(w, r, errdoc.RateLimited(s.auth.LockoutWindow().String()))
	case errors.Is(err, auth.ErrNoSecondProof):
		writeError(w, r, errdoc.New("auth.no_password", "This account has no password to confirm with").
			WithCause("It signs in through your identity provider, and two-factor authentication is not turned on here, so there is nothing this panel can ask you for.").
			WithImpact("Nothing was changed.").
			WithFix("Turn on two-factor authentication for this account first, or use an account with a password.").
			WithStatus(http.StatusBadRequest))
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrInvalidTOTP):
		writeError(w, r, errdoc.New("auth.reauth_failed", "That is not the right password").
			WithCause("The password, or the two-factor code, did not match this account.").
			WithImpact("Nothing was changed.").
			WithFix("Type it again. After several wrong attempts this pauses for a while.").
			WithStatus(http.StatusUnauthorized))
	default:
		writeError(w, r, err)
	}
}

// handleRevokeOtherSessions signs out every device except this one.
//
// One button, because the moment somebody wants this — a laptop left on a
// train — is not the moment to work through a list deciding which row is which.
func (s *Server) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	current, _ := sessionFrom(r.Context())
	sessions, err := s.db.ListSessions(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	revoked := 0
	for _, sess := range sessions {
		if sess.ID == current.ID {
			continue
		}
		if err := s.db.DeleteSession(r.Context(), sess.ID); err != nil {
			writeError(w, r, err)
			return
		}
		revoked++
	}
	s.audit(r, "", "auth.sessions_revoked", "user", user.ID, user.Email)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revoked": revoked})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	sessions, err := s.db.ListSessions(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	current, _ := sessionFrom(r.Context())
	type sessionView struct {
		store.Session
		Current bool `json:"current"`
	}
	views := make([]sessionView, 0, len(sessions))
	for _, sess := range sessions {
		views = append(views, sessionView{Session: sess, Current: sess.ID == current.ID})
	}
	writeList(w, views)
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	sessionID := chi.URLParam(r, "sessionID")

	// A user may only end their own sessions, so check ownership rather than
	// trusting the id from the URL.
	sessions, err := s.db.ListSessions(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, sess := range sessions {
		if sess.ID == sessionID {
			if err := s.db.DeleteSession(r.Context(), sessionID); err != nil {
				writeError(w, r, err)
				return
			}
			s.audit(r, "", "auth.session_revoked", "session", sessionID, "")
			writeOK(w)
			return
		}
	}
	writeError(w, r, errdoc.NotFound("session", sessionID))
}

type totpSetupResponse struct {
	Secret        string   `json:"secret"`
	URI           string   `json:"uri"`
	RecoveryCodes []string `json:"recovery_codes"`
}

func (s *Server) handleStartTOTP(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	secret, uri, err := s.auth.SetupTOTP(r.Context(), &user, version.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	codes, err := auth.GenerateRecoveryCodes(8)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// This is the only moment these exist in readable form, so they are stored
	// before they are shown. A code the user wrote down and the panel forgot
	// is worse than no recovery at all.
	if err := s.auth.StoreRecoveryCodes(r.Context(), user.ID, codes); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, totpSetupResponse{Secret: secret, URI: uri, RecoveryCodes: codes})
}

type totpConfirmRequest struct {
	Code string `json:"code"`
}

func (s *Server) handleConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	var req totpConfirmRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.auth.ConfirmTOTP(r.Context(), &user, req.Code); err != nil {
		if errors.Is(err, auth.ErrInvalidTOTP) {
			writeError(w, r, errdoc.New("auth.invalid_totp", "That code is not correct").
				WithCause("The six-digit code did not match the one this panel expects.").
				WithImpact("Two-factor authentication was not turned on.").
				WithFix("Check that your phone's clock is correct, then type the current code from your authenticator app.").
				WithStatus(http.StatusBadRequest))
			return
		}
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "auth.totp_enabled", "user", user.ID, user.Email)
	writeOK(w)
}

func (s *Server) handleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	if err := s.auth.DisableTOTP(r.Context(), &user); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "auth.totp_disabled", "user", user.ID, user.Email)
	writeOK(w)
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	tokens, err := s.db.ListAPITokens(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, tokens)
}

type createTokenRequest struct {
	Name     string `json:"name"`
	TeamID   string `json:"team_id"`
	Scopes   string `json:"scopes,omitempty"`
	TTLHours int    `json:"ttl_hours,omitempty"`
}

type createTokenResponse struct {
	Token  store.APIToken `json:"token"`
	Secret string         `json:"secret"`
	Note   string         `json:"note"`
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, r, errdoc.BadRequest("Give the token a name so you can recognise it later."))
		return
	}
	// A token may not grant more access than the person creating it has.
	user, err := s.authorizeTeam(r, req.TeamID, store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}

	var ttl time.Duration
	if req.TTLHours > 0 {
		ttl = time.Duration(req.TTLHours) * time.Hour
	}
	if err := auth.ValidateScopes(req.Scopes); err != nil {
		writeError(w, r, errdoc.BadRequest(capitalise(err.Error())+"."))
		return
	}
	record, secret, err := s.auth.CreateAPIToken(r.Context(), user.ID, req.TeamID, req.Name, req.Scopes, ttl)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, req.TeamID, "token.created", "token", record.ID, record.Name)
	writeJSON(w, http.StatusCreated, createTokenResponse{
		Token:  record,
		Secret: secret,
		Note:   "This is the only time the token is shown. Store it somewhere safe.",
	})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	tokenID := chi.URLParam(r, "tokenID")
	if err := s.db.DeleteAPIToken(r.Context(), tokenID, user.ID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "token.revoked", "token", tokenID, "")
	writeOK(w)
}

// withUser puts a user into a request context, for auditing an action taken
// before the authentication middleware had anyone to put there (setup, login).
func withUser(r *http.Request, user store.User) context.Context {
	return context.WithValue(r.Context(), ctxUser, user)
}

func supportedLocale(locale string) bool {
	switch locale {
	case "en", "id", "hi", "ru", "zh-CN":
		return true
	}
	return false
}
