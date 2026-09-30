package api

import (
	"net/http"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// A password in front of an app: for a staging site, a pull request's preview
// or an internal tool that should not be open to whoever finds the address.
//
// It is the one thing people reach for when a firewall rule is too much — the
// visitor's address changes, or they are a client on a phone — and every
// comparable product has it under one name or another.

// passwordView is what the page needs to draw the form honestly. The hash is
// never in it, and neither is the password.
type passwordView struct {
	Enabled   bool   `json:"enabled"`
	Username  string `json:"username,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	// Hostnames are the addresses the password guards.
	Hostnames []string `json:"hostnames"`
	// PlainHTTP are those among them served without HTTPS. A browser sends a
	// password to them readable by anyone on the path, and the page says so
	// rather than letting the padlock-shaped word "password" imply otherwise.
	PlainHTTP []string `json:"plain_http"`
}

// passwordHashCost is bcrypt's work factor for these hashes.
//
// Deliberately below the default of 10. Traefik checks the hash on every
// request, not once per sign-in, so the cost is paid for every image and
// script a page loads: at 10 that is tens of milliseconds of CPU each, and a
// page with forty assets spends seconds of it. At 6 it is a few milliseconds.
// What this guards is a preview or a staging site, and the hash lives in the
// panel's database and in a Secret in the app's own namespace, not on the
// open internet.
const passwordHashCost = 6

// Limits on what a password may be. bcrypt reads at most 72 bytes and Go's
// implementation refuses more rather than silently ignoring the rest, which is
// the right behaviour and needs a message before it is reached.
const (
	minPasswordLength = 8
	maxPasswordBytes  = 72
	maxUsernameLength = 64
)

func (s *Server) handleGetPassword(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.passwordView(r, app)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type setPasswordRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleSetPassword puts a password in front of an app, or changes it.
//
// Admin, like the firewall: this decides who may reach a running site.
func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setPasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	username := strings.TrimSpace(req.Username)
	if err := validatePasswordAccount(username, req.Password); err != nil {
		writeError(w, r, err)
		return
	}
	hash, err := hashAppPassword(req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}

	user, _ := UserFrom(r.Context())
	if err := s.db.SetAppPassword(r.Context(), store.AppPassword{
		AppID: app.ID, Username: username, Hash: hash,
	}, user.ID); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.password_set", "app", app.ID, username)
	s.syncAfterPasswordChange(r, app)

	view, err := s.passwordView(r, app)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleRemovePassword opens an app to anybody with its address again.
func (s *Server) handleRemovePassword(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.DeleteAppPassword(r.Context(), app.ID); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.password_removed", "app", app.ID, app.Name)
	s.syncAfterPasswordChange(r, app)

	view, err := s.passwordView(r, app)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// syncAfterPasswordChange applies the app's objects again, so the change is on
// the Ingress rather than only in a row.
//
// A cluster that cannot be reached is not a reason to refuse the change: it is
// stored, and the next deploy applies it. The same line the firewall draws.
func (s *Server) syncAfterPasswordChange(r *http.Request, app store.App) {
	if s.deployer == nil {
		return
	}
	if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
		s.log.Warn("the password was saved but the app was not re-applied",
			"app", app.ID, "error", err)
	}
}

func (s *Server) passwordView(r *http.Request, app store.App) (passwordView, error) {
	stored, enabled, err := s.db.GetAppPassword(r.Context(), app.ID)
	if err != nil {
		return passwordView{}, err
	}
	view := passwordView{Enabled: enabled, Hostnames: []string{}, PlainHTTP: []string{}}
	if enabled {
		view.Username = stored.Username
		view.UpdatedAt = stored.UpdatedAt
	}
	domains, err := s.db.ListDomains(r.Context(), app.ID)
	if err != nil {
		return view, err
	}
	for _, domain := range domains {
		view.Hostnames = append(view.Hostnames, domain.Hostname)
		if !domain.TLS {
			view.PlainHTTP = append(view.PlainHTTP, domain.Hostname)
		}
	}
	return view, nil
}

// validatePasswordAccount checks a username and password before anything is
// hashed or stored.
func validatePasswordAccount(username, password string) error {
	if username == "" {
		return errdoc.BadRequest("A username is needed. Anyone opening the app will be asked for it.")
	}
	if len([]rune(username)) > maxUsernameLength {
		return errdoc.BadRequest("That username is too long. Keep it to 64 characters.")
	}
	for _, r := range username {
		// A colon separates the username from the hash in what Traefik reads,
		// so one inside a username would end it early.
		if r == ':' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return errdoc.BadRequest("A username cannot contain a colon, a space or a control character.")
		}
	}
	if len([]rune(password)) < minPasswordLength {
		return errdoc.BadRequest("That password is too short. Use at least 8 characters.")
	}
	if len(password) > maxPasswordBytes {
		return errdoc.BadRequest("That password is too long. Keep it to 72 bytes, which is what the check can read.")
	}
	return nil
}

// hashAppPassword hashes a password into the form Traefik reads.
//
// The "$2y$" prefix is what htpasswd writes and what every reader of that
// format expects. Go writes "$2a$" for the same algorithm; the two differ only
// in how an old C implementation mishandled 8-bit characters, which Go never
// did, so relabelling is exact.
func hashAppPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if err != nil {
		return "", err
	}
	return "$2y$" + strings.TrimPrefix(string(hash), "$2a$"), nil
}
