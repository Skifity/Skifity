package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/plugins"
	"skifity/internal/store"
)

// The accounts on the panel, for its administrator: Settings → Accounts.
//
// Before this, only the account made at first-run setup was ever an
// administrator, nothing could disable an account although every way in
// checks for it, and somebody who left could only be taken out of each team
// one at a time — still able to sign in.
//
// Every route is behind requireAdmin. What gives somebody access they keep —
// making them an administrator, enabling an account again, taking its second
// factor away — asks for the password again, the way the account's own front
// door does, so a token cannot do it and a borrowed session has to prove
// itself first. What takes access away — demoting, disabling, deleting — only
// asks the person to confirm, which the page does.
//
// None of it works on the administrator's own account: that is the account
// page's, and locking yourself out is not something this one should be able
// to do in a click.

// accountView is one account as the Accounts tab lists it.
type accountView struct {
	User  store.User          `json:"user"`
	Teams []store.AccountTeam `json:"teams"`
	// HasPassword is false for an account made through single sign-on that
	// never set one: the provider is its only way in.
	HasPassword bool                `json:"has_password"`
	Passkeys    int                 `json:"passkeys"`
	Tokens      int                 `json:"tokens"`
	SSOIssuers  []string            `json:"sso_issuers"`
	Blockers    accountBlockersView `json:"blockers"`
	// Current is the administrator looking, whose own row has no actions.
	Current bool `json:"current"`
}

// accountBlockersView names what stands in the way of deleting an account,
// with each plugin by the name an administrator knows it by.
type accountBlockersView struct {
	SoleOwnerOf []store.AccountTeam `json:"sole_owner_of"`
	Plugins     []pluginName        `json:"plugins"`
}

type pluginName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	caller, _ := UserFrom(r.Context())
	accounts, err := s.db.ListAccounts(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	names, err := s.pluginNames(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	views := make([]accountView, 0, len(accounts))
	for _, account := range accounts {
		view := accountView{
			User: account.User, Teams: account.Teams, HasPassword: account.User.PasswordHash != "",
			Passkeys: account.Passkeys,
			Tokens:   account.Tokens, SSOIssuers: account.SSOIssuers,
			Blockers: accountBlockersView{
				SoleOwnerOf: account.Blockers.SoleOwnerOf, Plugins: []pluginName{},
			},
			Current: account.User.ID == caller.ID,
		}
		for _, id := range account.Blockers.Plugins {
			view.Blockers.Plugins = append(view.Blockers.Plugins, pluginName{ID: id, Name: names(id)})
		}
		views = append(views, view)
	}
	writeList(w, views)
}

// pluginNames answers a function from a plugin's id to the name its manifest
// gives it, or the id when the manifest cannot be read.
func (s *Server) pluginNames(r *http.Request) (func(string) string, error) {
	installed, err := s.db.ListPlugins(r.Context())
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(installed))
	for _, record := range installed {
		if manifest, err := plugins.Parse([]byte(record.Manifest)); err == nil && manifest.Name != "" {
			names[record.ID] = manifest.Name
		}
	}
	return func(id string) string {
		if name, ok := names[id]; ok {
			return name
		}
		return id
	}, nil
}

// accountTarget reads the account a route names, and refuses the caller's own.
func (s *Server) accountTarget(r *http.Request) (store.User, error) {
	caller, _ := UserFrom(r.Context())
	id := chi.URLParam(r, "userID")
	target, err := s.db.GetUser(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, errdoc.NotFound("account", id)
	}
	if err != nil {
		return store.User{}, err
	}
	if target.ID == caller.ID {
		return store.User{}, errdoc.AccountSelf()
	}
	return target, nil
}

// accountChangeFailed says why a change to an account did not happen.
func accountChangeFailed(err error, id string) error {
	switch {
	case errors.Is(err, store.ErrLastAdmin):
		return errdoc.AccountLastAdmin()
	case errors.Is(err, store.ErrNotFound):
		return errdoc.NotFound("account", id)
	}
	return err
}

// auditAccount records a change to an account in the activity log of every
// team the account is in, and once for the panel when it is in none.
//
// Per team rather than once for the panel: a panel-wide entry is shown to
// every team the administrator is in, and an account's address is the business
// of the teams it belongs to, not of the others.
func (s *Server) auditAccount(r *http.Request, action string, target store.User, teams []store.Team) {
	if len(teams) == 0 {
		s.audit(r, "", action, "user", target.ID, target.Email)
		return
	}
	for _, team := range teams {
		s.audit(r, team.ID, action, "user", target.ID, target.Email)
	}
}

type updateAccountRequest struct {
	Disabled *bool `json:"disabled,omitempty"`
	IsAdmin  *bool `json:"is_admin,omitempty"`
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	var req updateAccountRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Disabled == nil && req.IsAdmin == nil {
		writeError(w, r, errdoc.BadRequest("Say what to change: disabled, is_admin, or both."))
		return
	}
	target, err := s.accountTarget(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Asked of what the request is for, not of what it would change: an
	// account that is already an administrator answers a token the same way
	// as one that is not, so the answer says nothing about the account.
	if (req.IsAdmin != nil && *req.IsAdmin) || (req.Disabled != nil && !*req.Disabled) {
		if err := s.checkRecentAuth(r); err != nil {
			writeError(w, r, err)
			return
		}
	}
	teams, err := s.db.ListTeamsForUser(r.Context(), target.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	if req.IsAdmin != nil && *req.IsAdmin != target.IsAdmin {
		if err := s.db.SetUserAdmin(r.Context(), target.ID, *req.IsAdmin); err != nil {
			writeError(w, r, accountChangeFailed(err, target.ID))
			return
		}
		if *req.IsAdmin {
			s.auditAccount(r, "account.admin_granted", target, teams)
		} else {
			s.auditAccount(r, "account.admin_revoked", target, teams)
		}
	}
	if req.Disabled != nil && *req.Disabled != target.Disabled {
		if err := s.db.SetUserDisabled(r.Context(), target.ID, *req.Disabled); err != nil {
			writeError(w, r, accountChangeFailed(err, target.ID))
			return
		}
		if *req.Disabled {
			// Every way in already refuses a disabled account, a session
			// included the next time it is used. Ending them now means a
			// session somebody is in the middle of using ends with this.
			if err := s.db.DeleteUserSessions(r.Context(), target.ID); err != nil {
				s.log.Warn("a disabled account's sessions could not be ended", "user", target.ID, "error", err)
			}
			s.auditAccount(r, "account.disabled", target, teams)
		} else {
			s.auditAccount(r, "account.enabled", target, teams)
		}
	}

	updated, err := s.db.GetUser(r.Context(), target.ID)
	if err != nil {
		writeError(w, r, accountChangeFailed(err, target.ID))
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	target, err := s.accountTarget(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Read before the delete, which takes the memberships with it.
	teams, err := s.db.ListTeamsForUser(r.Context(), target.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	err = s.db.DeleteAccount(r.Context(), target.ID)
	if blockers, blocked := store.IsAccountBlocked(err); blocked {
		if len(blockers.SoleOwnerOf) > 0 {
			names := make([]string, 0, len(blockers.SoleOwnerOf))
			for _, team := range blockers.SoleOwnerOf {
				names = append(names, team.Name)
			}
			writeError(w, r, errdoc.AccountLastOwner(strings.Join(names, ", ")))
			return
		}
		name, nameErr := s.pluginNames(r)
		if nameErr != nil {
			writeError(w, r, nameErr)
			return
		}
		names := make([]string, 0, len(blockers.Plugins))
		for _, id := range blockers.Plugins {
			names = append(names, name(id))
		}
		writeError(w, r, errdoc.AccountOwnsPlugins(strings.Join(names, ", ")))
		return
	}
	if err != nil {
		writeError(w, r, accountChangeFailed(err, target.ID))
		return
	}
	s.auditAccount(r, "account.deleted", target, teams)
	writeOK(w)
}

// handleResetAccountTwoFactor turns somebody else's two-factor off, for a
// person who has lost the phone and the recovery codes. It is behind
// requireRecentAuth: it takes a way past the password away from an account.
func (s *Server) handleResetAccountTwoFactor(w http.ResponseWriter, r *http.Request) {
	target, err := s.accountTarget(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teams, err := s.db.ListTeamsForUser(r.Context(), target.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.ResetSecondFactor(r.Context(), target.ID); err != nil {
		writeError(w, r, accountChangeFailed(err, target.ID))
		return
	}
	// Whoever has the phone may be signed in. The reset is the end of their
	// access, not only the start of the owner's.
	if err := s.db.DeleteUserSessions(r.Context(), target.ID); err != nil {
		s.log.Warn("sessions could not be ended after a two-factor reset", "user", target.ID, "error", err)
	}
	s.auditAccount(r, "account.two_factor_reset", target, teams)
	writeOK(w)
}
