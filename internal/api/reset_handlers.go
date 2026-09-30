package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"skifity/internal/auth"
	"skifity/internal/errdoc"
	"skifity/internal/notify"
	"skifity/internal/runsafe"
	"skifity/internal/settings"
)

// A forgotten password, reset through a link sent by email. See
// internal/auth/reset.go for the rules; this is how they are reached.

// sendResetEmail is how the link is sent. A variable, so a test can read the
// link instead of running a mail server.
var sendResetEmail = func(s *Server, ctx context.Context, to, subject, body string) error {
	return notify.SendPanelEmail(ctx, s.db, s.keyring, s.log, to, subject, body)
}

// resetBase is the address a reset link starts with: the Panel URL setting,
// or the address the panel was started with. Never the request's Host, which
// is whatever the asker sent — a link built from it would carry the token to
// a server of the asker's choosing.
func (s *Server) resetBase(ctx context.Context) string { return s.panelAddress(ctx) }

// panelAddress is the address people reach the panel at: the Panel URL
// setting, or the address the panel was started with. A reset link is built
// from it, and a passkey belongs to its hostname.
func (s *Server) panelAddress(ctx context.Context) string {
	base, _, err := s.db.GetSetting(ctx, settings.KeyPanelURL)
	if err != nil || strings.TrimSpace(base) == "" {
		base = s.cfg.PublicURL
	}
	return strings.TrimSuffix(strings.TrimSpace(base), "/")
}

// passwordResetAvailable is whether the panel can send a reset link at all:
// it needs a server to send through and an address to link to.
func (s *Server) passwordResetAvailable(ctx context.Context) bool {
	return notify.PanelEmailConfigured(ctx, s.db) && s.resetBase(ctx) != ""
}

type resetRequest struct {
	Email string `json:"email"`
}

// handleRequestPasswordReset sends a reset link to an address, if it has an
// account. The answer is the same either way, and so is how long it takes:
// the mail is sent after the answer, so a slow mail server does not tell
// anybody the address was real.
func (s *Server) handleRequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if !s.passwordResetAvailable(r.Context()) {
		writeError(w, r, errdoc.PasswordResetUnavailable())
		return
	}
	if !validEmail(strings.TrimSpace(req.Email)) {
		writeError(w, r, errdoc.BadRequest("Enter the email address you sign in with."))
		return
	}
	user, token, ok, err := s.auth.RequestPasswordReset(r.Context(), req.Email, clientIPFrom(r.Context()))
	if errors.Is(err, auth.ErrResetLimited) {
		writeError(w, r, errdoc.RateLimited(time.Hour.String()))
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	if ok {
		link := s.resetBase(r.Context()) + "/reset-password#token=" + token
		to := user.Email
		runsafe.Go(s.log, "send a password reset link", func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
			defer cancel()
			body := "Somebody asked to reset the password of your account on this panel. " +
				"If it was you, open this link within 30 minutes and choose a new password:\n\n" + link +
				"\n\nIf it was not you, ignore this message: your password has not changed, and the link stops working on its own."
			if err := sendResetEmail(s, ctx, to, "Reset your password", body); err != nil {
				s.log.Warn("could not send a password reset link", "error", err)
			}
		})
		s.audit(r.WithContext(withUser(r, user)), "", "auth.password_reset_requested", "user", user.ID, user.Email)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":   true,
		"note": "If an account uses that address, a link to reset its password is on its way.",
	})
}

type resetConfirmRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// handleConfirmPasswordReset sets a new password through a link. Nobody is
// signed in by it: the person signs in with the new password, and with their
// second factor when the account has one.
func (s *Server) handleConfirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req resetConfirmRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	user, err := s.auth.ResetPassword(r.Context(), strings.TrimSpace(req.Token), req.Password)
	if errors.Is(err, auth.ErrResetInvalid) {
		writeError(w, r, errdoc.PasswordResetInvalid())
		return
	}
	if err != nil {
		writeError(w, r, errdoc.BadRequest(capitalise(err.Error())+"."))
		return
	}
	s.audit(r.WithContext(withUser(r, user)), "", "auth.password_reset", "user", user.ID, user.Email)
	writeOK(w)
}
