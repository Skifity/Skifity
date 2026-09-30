package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/netguard"
	"skifity/internal/registry"
	"skifity/internal/store"
)

// A team's private registries: credentials every app in the team pulls its
// images with, and every build pulls its base images with. See
// Deployer.ensureTeamRegistries for where they go.

// checkRegistryLogin asks the registry whether the credentials are good. A
// variable, so a test can answer for a registry it does not run. Guarded: the
// host is something a person typed, and the request is the panel's.
var checkRegistryLogin = func(ctx context.Context, host, username, password string) error {
	return registry.CheckLogin(ctx, registryLoginClient, host, username, password)
}

var registryLoginClient = netguard.Client(15 * time.Second)

func (s *Server) handleListRegistries(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	// Names and hosts only, and a password is never sent back to anybody. A
	// member limited to some projects is not shown the team's settings.
	if _, err := s.authorizeTeam(r, teamID, store.RoleViewer); err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := s.db.ListRegistryCredentials(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]store.RegistryCredential, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.RegistryCredential)
	}
	writeList(w, out)
}

type setRegistryRequest struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleSetRegistry(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	// An administrator's: every app in the team pulls with these.
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	var req setRegistryRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	host, err := kube.NormalizeRegistryHost(req.Host)
	if err != nil {
		writeError(w, r, errdoc.BadRequest(capitalise(err.Error())+"."))
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		writeError(w, r, errdoc.BadRequest("A registry needs a username and a password or token to sign in with."))
		return
	}

	// Asked before it is saved, so a typo is found now rather than as an
	// ImagePullBackOff at the next deploy. A registry the panel cannot reach
	// — one on a private network, which the guard will not dial — is saved
	// unchecked, and the answer says so.
	checked := true
	var unchecked string
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := checkRegistryLogin(ctx, host, username, req.Password); err != nil {
		if errors.Is(err, registry.ErrLoginRefused) {
			writeError(w, r, errdoc.RegistryLoginRefused(host))
			return
		}
		checked, unchecked = false, err.Error()
	}

	sealed, err := s.keyring.Seal([]byte(req.Password), store.RegistryContext(teamID, host))
	if err != nil {
		writeError(w, r, err)
		return
	}
	credential := store.RegistryCredential{
		TeamID: teamID, Host: host, Username: username,
		Name: defaultString(strings.TrimSpace(req.Name), host),
	}
	if err := s.db.SetRegistryCredential(r.Context(), &credential, sealed); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "registry.saved", "team", teamID, host)
	answer := map[string]any{"registry": credential, "checked": checked}
	if !checked {
		answer["unchecked_because"] = unchecked
	}
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) handleDeleteRegistry(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	id := chi.URLParam(r, "registryID")
	var host string
	if rows, err := s.db.ListRegistryCredentials(r.Context(), teamID); err == nil {
		for _, row := range rows {
			if row.ID == id {
				host = row.Host
			}
		}
	}
	if err := s.db.DeleteRegistryCredential(r.Context(), teamID, id); err != nil {
		writeError(w, r, err)
		return
	}
	// The Secret in each namespace goes at each app's next deploy; the panel
	// does not reach into every environment from a settings page.
	s.audit(r, teamID, "registry.deleted", "team", teamID, host)
	writeOK(w)
}
