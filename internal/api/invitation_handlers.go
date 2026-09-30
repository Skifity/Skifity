package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/auth"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Inviting somebody onto this panel.
//
// Adding a member used to look an address up and refuse one it had never seen,
// with the advice "ask them to create an account first" — and nothing could
// create an account except first-run setup, which happens once. A team of one
// was the only team possible, and the dead end was written into the error
// message.
//
// An invitation is a link rather than an email. SMTP is a setting most installs
// have not filled in, and a product that cannot add a colleague without a mail
// server is a product that cannot add a colleague. The link is shown once, the
// same way the recovery key and the setup token are, and whoever opens it sets
// their own password.

// invitationTTL is how long a link works for. A week is long enough for
// somebody on holiday and short enough that a link in a chat history from last
// quarter is not a way in.
const invitationTTL = 7 * 24 * time.Hour

type inviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type inviteResponse struct {
	Invitation store.Invitation `json:"invitation"`
	// URL is returned once, on creation, and never again: the token behind it
	// is stored hashed, exactly like a session or an API token.
	URL string `json:"url"`
}

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	actor, err := s.authorizeTeam(r, teamID, store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req inviteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(email) {
		writeError(w, r, errdoc.BadRequest("That is not an email address."))
		return
	}
	role := store.Role(strings.TrimSpace(req.Role))
	if role == "" {
		role = store.RoleMember
	}
	if !role.Valid() {
		writeError(w, r, errdoc.BadRequest("Role must be owner, admin or member."))
		return
	}
	// The same rule as changing a role: an admin must not be able to invite
	// somebody above themselves and so outrank themselves out of the decision.
	membership, err := s.db.GetMembership(r.Context(), teamID, actor.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !membership.Role.AtLeast(role) {
		writeError(w, r, errdoc.Forbidden("inviting somebody to a role higher than your own"))
		return
	}

	// Somebody who already has an account does not need a link: they need a
	// membership, which is what the members endpoint does. Saying so is more
	// useful than sending them through a sign-up they cannot complete.
	if existing, err := s.db.GetUserByEmail(r.Context(), email); err == nil {
		if _, err := s.db.GetMembership(r.Context(), teamID, existing.ID); err == nil {
			writeError(w, r, errdoc.New("team.already_a_member", "They are already in this team").
				WithCause("%s is already a member.", email).
				WithImpact("No invitation was created.").
				WithFix("Change their role under Members instead.").
				WithStatus(http.StatusConflict))
			return
		}
		writeError(w, r, errdoc.New("team.account_exists", "They already have an account here").
			WithCause("%s has signed in to this panel before, so there is nothing to sign up for.", email).
			WithImpact("No invitation was created.").
			WithFix("Add them to the team under Members, by the same address.").
			WithStatus(http.StatusConflict))
		return
	}

	token, err := crypto.RandomToken(32)
	if err != nil {
		writeError(w, r, err)
		return
	}
	invitation := store.Invitation{
		TeamID: teamID, Email: email, Role: role,
		InvitedBy: actor.ID, ExpiresAt: time.Now().Add(invitationTTL),
	}
	if err := s.db.CreateInvitation(r.Context(), &invitation, auth.HashToken(token)); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "team.invited", "user", invitation.ID, email)
	writeJSON(w, http.StatusCreated, inviteResponse{
		Invitation: invitation,
		URL:        s.invitationURL(r, token),
	})
}

// invitationURL builds the link. It uses the Panel URL setting where one is
// set, because a link built from the request's Host is a link to whatever
// hostname the request happened to use.
func (s *Server) invitationURL(r *http.Request, token string) string {
	base, _, err := s.db.GetSetting(r.Context(), settings.KeyPanelURL)
	if err != nil || strings.TrimSpace(base) == "" {
		base = s.cfg.PublicURL
	}
	return strings.TrimSuffix(strings.TrimSpace(base), "/") + "/invite/" + token
}

func (s *Server) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	invitations, err := s.db.ListInvitations(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, invitations)
}

func (s *Server) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.DeleteInvitation(r.Context(), teamID, chi.URLParam(r, "invitationID")); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "team.invitation_revoked", "user", chi.URLParam(r, "invitationID"), "")
	writeOK(w)
}

// --- the two open routes, for somebody with a link and no account ----------

type invitationLookup struct {
	Email    string `json:"email"`
	TeamName string `json:"team_name"`
	Role     string `json:"role"`
}

// invitationFrom resolves the token in the URL, or explains why it will not.
//
// Everything wrong with a token answers the same way: expired, revoked, already
// used, never existed. Telling an anonymous caller which is which turns this
// into a way to find out whether an address was ever invited.
func (s *Server) invitationFrom(r *http.Request) (store.Invitation, error) {
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	refused := errdoc.New("invite.not_valid", "This invitation cannot be used").
		WithCause("The link has expired, has been used already, or was withdrawn.").
		WithImpact("No account was created.").
		WithFix("Ask whoever invited you for a new link.").
		WithStatus(http.StatusNotFound)

	if len(token) < 20 {
		return store.Invitation{}, refused
	}
	invitation, err := s.db.InvitationByTokenHash(r.Context(), auth.HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Invitation{}, refused
		}
		return store.Invitation{}, err
	}
	if invitation.Expired() || !invitation.AcceptedAt.IsZero() {
		return store.Invitation{}, refused
	}
	return invitation, nil
}

func (s *Server) handleLookupInvitation(w http.ResponseWriter, r *http.Request) {
	invitation, err := s.invitationFrom(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	team, err := s.db.GetTeam(r.Context(), invitation.TeamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, invitationLookup{
		Email: invitation.Email, TeamName: team.Name, Role: string(invitation.Role),
	})
}

type acceptRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (s *Server) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	invitation, err := s.invitationFrom(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req acceptRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, r, errdoc.BadRequest("Please give a name."))
		return
	}
	if err := auth.DefaultPasswordPolicy().Check(req.Password); err != nil {
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}

	// The address comes from the invitation, never from the request: otherwise
	// a link meant for one person makes an account for another.
	if _, err := s.db.GetUserByEmail(r.Context(), invitation.Email); err == nil {
		writeError(w, r, errdoc.New("invite.account_exists", "That address already has an account").
			WithCause("Somebody signed up with %s since this invitation was sent.", invitation.Email).
			WithImpact("No account was created.").
			WithFix("Sign in instead, and ask to be added to the team.").
			WithStatus(http.StatusConflict))
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	user := store.User{Email: invitation.Email, Name: name, PasswordHash: hash}
	if err := s.db.CreateUser(r.Context(), &user); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.AcceptInvitation(r.Context(), invitation.ID, invitation.TeamID, user.ID, invitation.Role); err != nil {
		writeError(w, r, err)
		return
	}

	result, err := s.auth.IssueSession(r.Context(), user, clientIPFrom(r.Context()), r.UserAgent())
	if err != nil {
		writeError(w, r, err)
		return
	}
	http.SetCookie(w, s.auth.SessionCookie(result.Token, result.ExpiresAt))
	http.SetCookie(w, s.auth.CSRFCookie(result.CSRFToken, result.ExpiresAt))
	s.audit(r.WithContext(withUser(r, user)), invitation.TeamID,
		"team.invitation_accepted", "user", user.ID, user.Email)

	writeJSON(w, http.StatusOK, loginResponse{
		User: user, CSRFToken: result.CSRFToken, ExpiresAt: result.ExpiresAt,
	})
}
