package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"

	"skifity/internal/auth"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Passkeys, the two halves of each ceremony and the list on the account page.
// internal/auth/passkey.go holds the rules; this is how they are reached.
//
// Signing in is open, like signing in with a password: somebody who is not
// signed in has nothing else to present. What makes it safe is what has to come
// back — a signature, by a key this panel stored, over a challenge this panel
// issued to this browser less than five minutes ago and has not seen answered.
// Adding and removing a passkey are the account's own front door, so both ask
// for the password again, like linking single sign-on does.

// maxPasskeyName is how long a passkey's name may be.
const maxPasskeyName = 64

// passkeyParty is who this panel is to a passkey: the hostname people reach it
// at, from the Panel URL setting or the address it was started with. Never the
// request's Host, which is whatever the caller sent.
func (s *Server) passkeyParty(ctx context.Context) (auth.RelyingParty, error) {
	address := s.panelAddress(ctx)
	rp, ok := auth.PanelRelyingParty(address, version.Name)
	if !ok {
		return auth.RelyingParty{}, errdoc.PasskeysUnavailable(address)
	}
	return rp, nil
}

// passkeyStatus is what the sign-in and account pages need to decide whether
// to offer passkeys at all.
type passkeyStatus struct {
	Available bool `json:"available"`
}

func (s *Server) passkeyStatus(ctx context.Context) passkeyStatus {
	_, err := s.passkeyParty(ctx)
	return passkeyStatus{Available: err == nil}
}

// passkeyView is one passkey on the account page.
type passkeyView struct {
	store.Passkey
	// Elsewhere is a passkey made for an address the panel no longer answers
	// on. A browser will not offer it here, so it is shown as such.
	Elsewhere bool `json:"elsewhere"`
}

func viewPasskey(p store.Passkey, rp auth.RelyingParty) passkeyView {
	return passkeyView{Passkey: p, Elsewhere: rp.ID != "" && p.RPID != rp.ID}
}

// passkeyName tidies a name somebody gave a passkey, or says why it will not do.
func passkeyName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", errdoc.BadRequest("Give the passkey a name, such as the device it is on, so you can recognise it later.")
	case len([]rune(name)) > maxPasskeyName:
		return "", errdoc.BadRequest("That name is too long. Keep it to 64 characters.")
	case strings.ContainsFunc(name, unicode.IsControl):
		return "", errdoc.BadRequest("A passkey's name cannot contain a control character.")
	}
	return name, nil
}

// --- signing in ---

// handleBeginPasskeySignIn hands the browser a challenge to sign with whichever
// passkey the person picks. It knows nothing about any account yet.
func (s *Server) handleBeginPasskeySignIn(w http.ResponseWriter, r *http.Request) {
	// A JSON body, empty, for the same reason as signing in with a password:
	// a page on a sibling subdomain can post a body with no type and the
	// visitor's cookies, and this sets one of them. See decodeJSON.
	var req struct{}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	rp, err := s.passkeyParty(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	// One value per browser, kept across ceremonies, so two tabs of the
	// sign-in page do not undo each other's.
	binding := s.auth.ReadCookie(r, auth.PasskeyCookieName)
	if !plausibleToken(binding) {
		if binding, err = crypto.RandomToken(32); err != nil {
			writeError(w, r, err)
			return
		}
	}
	options, err := s.auth.BeginPasskeySignIn(r.Context(), rp, binding, clientIPFrom(r.Context()))
	switch {
	case errors.Is(err, auth.ErrLockedOut), errors.Is(err, auth.ErrTooManyPasskeySignIns):
		writeError(w, r, errdoc.RateLimited(s.auth.LockoutWindow().String()))
		return
	case err != nil:
		writeError(w, r, err)
		return
	}
	http.SetCookie(w, s.auth.PasskeyCookie(binding))
	writeJSON(w, http.StatusOK, options)
}

// handleFinishPasskeySignIn checks the browser's answer and signs the person
// in: the same session, the same cookies and the same answer as a password.
func (s *Server) handleFinishPasskeySignIn(w http.ResponseWriter, r *http.Request) {
	var answer json.RawMessage
	if err := decodeJSON(w, r, &answer); err != nil {
		writeError(w, r, err)
		return
	}
	rp, err := s.passkeyParty(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	binding := s.auth.ReadCookie(r, auth.PasskeyCookieName)
	signIn, err := s.auth.FinishPasskeySignIn(r.Context(), rp, binding, answer,
		clientIPFrom(r.Context()), r.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrLockedOut):
			writeError(w, r, errdoc.RateLimited(s.auth.LockoutWindow().String()))
		case errors.Is(err, auth.ErrPasskeyChallengeGone):
			writeError(w, r, errdoc.PasskeyExpired())
		case errors.Is(err, auth.ErrPasskeyCloned):
			// Two copies of one key exist. The person it belongs to should
			// hear about it, so it goes in their audit trail as well as the log.
			s.log.Warn("a passkey sign-in was refused: its signature counter did not go up, so the key may have been copied",
				"user", signIn.User.ID, "passkey", signIn.Passkey.ID)
			s.audit(r.WithContext(withUser(r, signIn.User)), "", "auth.passkey_clone_suspected",
				"passkey", signIn.Passkey.ID, signIn.Passkey.Name)
			writeError(w, r, errdoc.PasskeyRefused())
		case errors.Is(err, auth.ErrPasskeyRefused):
			// Why is for whoever runs the panel. Whoever is asking is anonymous.
			s.log.Warn("a passkey sign-in was refused", "reason", err.Error(), "user", signIn.User.ID)
			writeError(w, r, errdoc.PasskeyRefused())
		default:
			writeError(w, r, err)
		}
		return
	}

	result := signIn.Result
	http.SetCookie(w, s.auth.SessionCookie(result.Token, result.ExpiresAt))
	http.SetCookie(w, s.auth.CSRFCookie(result.CSRFToken, result.ExpiresAt))
	http.SetCookie(w, s.auth.ClearCookie(auth.PasskeyCookieName))
	s.audit(r.WithContext(withUser(r, result.User)), "", "auth.login_passkey",
		"passkey", signIn.Passkey.ID, signIn.Passkey.Name)
	writeJSON(w, http.StatusOK, loginResponse{
		User:      result.User,
		CSRFToken: result.CSRFToken,
		ExpiresAt: result.ExpiresAt,
	})
}

// --- the account page ---

func (s *Server) handleListPasskeys(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	passkeys, err := s.auth.Passkeys(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// On a panel that cannot use passkeys at the moment, they are still
	// listed, so they can be removed.
	rp, _ := s.passkeyParty(r.Context())
	views := make([]passkeyView, 0, len(passkeys))
	for _, p := range passkeys {
		views = append(views, viewPasskey(p, rp))
	}
	writeList(w, views)
}

// handleBeginPasskeyRegistration hands the browser what it needs to make a
// passkey for this account. Behind requireRecentAuth: a passkey is a new way
// into the account, and a session somebody borrowed must not be able to add
// one and keep it.
func (s *Server) handleBeginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	session, _ := sessionFrom(r.Context())
	rp, err := s.passkeyParty(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	options, err := s.auth.BeginPasskeyRegistration(r.Context(), rp, user, session.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

type addPasskeyRequest struct {
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

// handleAddPasskey checks the browser's answer and stores the new passkey.
func (s *Server) handleAddPasskey(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	session, _ := sessionFrom(r.Context())
	var req addPasskeyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	// The name first, so a name that will not do does not spend the
	// challenge and send the person through their fingerprint again.
	name, err := passkeyName(req.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rp, err := s.passkeyParty(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	passkey, err := s.auth.FinishPasskeyRegistration(r.Context(), rp, user, session.ID, name, req.Credential)
	switch {
	case errors.Is(err, auth.ErrPasskeyChallengeGone):
		writeError(w, r, errdoc.PasskeyExpired())
		return
	case errors.Is(err, auth.ErrPasskeyNotAdded):
		s.log.Warn("a new passkey was refused", "reason", err.Error(), "user", user.ID)
		writeError(w, r, errdoc.PasskeyNotAdded())
		return
	case errors.Is(err, auth.ErrPasskeyTaken):
		writeError(w, r, errdoc.PasskeyTaken())
		return
	case err != nil:
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "auth.passkey_added", "passkey", passkey.ID, passkey.Name)
	writeJSON(w, http.StatusCreated, viewPasskey(passkey, rp))
}

type renamePasskeyRequest struct {
	Name string `json:"name"`
}

// handleRenamePasskey changes the name somebody gave a passkey. A name opens
// nothing, so this does not ask for the password.
func (s *Server) handleRenamePasskey(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	id := chi.URLParam(r, "passkeyID")
	var req renamePasskeyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name, err := passkeyName(req.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	passkey, err := s.auth.RenamePasskey(r.Context(), user.ID, id, name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, errdoc.NotFound("passkey", id))
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "auth.passkey_renamed", "passkey", passkey.ID, passkey.Name)
	rp, _ := s.passkeyParty(r.Context())
	writeJSON(w, http.StatusOK, viewPasskey(passkey, rp))
}

// handleRemovePasskey removes one of the signed-in account's passkeys. Behind
// requireRecentAuth, like adding one.
func (s *Server) handleRemovePasskey(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	id := chi.URLParam(r, "passkeyID")
	passkey, err := s.auth.RemovePasskey(r.Context(), user, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, r, errdoc.NotFound("passkey", id))
		return
	case errors.Is(err, auth.ErrPasskeyLastWayIn):
		writeError(w, r, errdoc.PasskeyLastWayIn())
		return
	case err != nil:
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "auth.passkey_removed", "passkey", passkey.ID, passkey.Name)
	writeOK(w)
}

// plausibleToken reports whether a cookie value looks like one this panel
// made, so anything else is replaced rather than carried along.
func plausibleToken(value string) bool {
	if len(value) < 32 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
