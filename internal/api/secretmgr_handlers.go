package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/secretmgr"
	"skifity/internal/store"
)

// A team's secret managers: connections to the Vault, Infisical, Doppler or
// AWS Secrets Manager it already runs, so a variable can be read from there
// instead of stored here. See internal/secretmgr.
//
// Administrators add, change, test and remove them: a connection signs in
// with credentials that can read whatever its policy allows. Members use
// them, by setting a variable to be read from one; everybody in the team can
// see which there are, which is names and addresses and never a credential.

// secretManagerTimeout bounds a request that asks a manager something while a
// person waits for the answer.
const secretManagerTimeout = 20 * time.Second

// Periodic refresh is off (0) or between these, in minutes.
const (
	minRefreshMinutes = 5
	maxRefreshMinutes = 24 * 60
)

// connectionName is what a connection may be called: short, and without the
// colon that separates it from the path in connection:path#key.
var connectionName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// secretManagerView is a connection as the API answers it.
type secretManagerView struct {
	store.SecretConnection
	// Credentials names what the connection signs in with. Their values
	// never leave the panel.
	Credentials []string `json:"credentials"`
	// UsedBy is how many variables read it.
	UsedBy int `json:"used_by"`
}

func secretManagerViewOf(row store.SecretConnectionRow, usedBy int) secretManagerView {
	return secretManagerView{
		SecretConnection: row.SecretConnection,
		Credentials:      secretmgr.CredentialNames(row.Kind, row.Settings),
		UsedBy:           usedBy,
	}
}

func (s *Server) handleListSecretManagers(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	// A member limited to some projects sets variables in them too, and
	// picks the connection from this list.
	if _, _, err := s.authorizeTeamMember(r, teamID, store.RoleViewer); err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := s.db.ListSecretConnections(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	counts, err := s.db.CountSecretConnectionUses(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]secretManagerView, 0, len(rows))
	for _, row := range rows {
		out = append(out, secretManagerViewOf(row, counts[row.ID]))
	}
	writeList(w, out)
}

type createSecretManagerRequest struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Settings    map[string]string `json:"settings"`
	Credentials map[string]string `json:"credentials"`
	// RefreshMinutes switches the periodic refresh on. Left out or 0, it is
	// off.
	RefreshMinutes *int `json:"refresh_minutes,omitempty"`
}

func refreshMinutes(asked *int, was int) (int, error) {
	if asked == nil {
		return was, nil
	}
	if *asked != 0 && (*asked < minRefreshMinutes || *asked > maxRefreshMinutes) {
		return 0, errdoc.BadRequest(fmt.Sprintf(
			"A periodic refresh is off (0) or every %d to %d minutes.", minRefreshMinutes, maxRefreshMinutes))
	}
	return *asked, nil
}

// testSecretManager signs in to a manager before it is saved, so a wrong
// token is found now rather than at the next deploy.
func (s *Server) testSecretManager(ctx context.Context, name, kind string, settings, credentials map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, secretManagerTimeout)
	defer cancel()
	if err := s.secrets.Test(ctx, kind, settings, credentials); err != nil {
		return secretmgr.Problem(name, kind, err)
	}
	return nil
}

func (s *Server) handleCreateSecretManager(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	var req createSecretManagerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if !connectionName.MatchString(name) {
		writeError(w, r, errdoc.BadRequest("A connection's name is lower-case letters, digits, - and _, such as company-vault."))
		return
	}
	settings, credentials, err := secretmgr.Normalize(req.Kind, req.Settings, nonNil(req.Credentials))
	if err != nil {
		writeError(w, r, errdoc.BadRequest(capitalise(err.Error())+"."))
		return
	}
	minutes, err := refreshMinutes(req.RefreshMinutes, 0)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.testSecretManager(r.Context(), name, req.Kind, settings, credentials); err != nil {
		writeError(w, r, err)
		return
	}
	connection := store.SecretConnection{
		ID: store.NewID("sm"), TeamID: teamID, Name: name, Kind: req.Kind, Settings: settings,
		RefreshMinutes: minutes,
	}
	if minutes > 0 {
		connection.NextRefreshAt = time.Now().UTC().Add(time.Duration(minutes) * time.Minute)
	}
	sealed, err := secretmgr.SealCredentials(s.keyring, connection.ID, credentials)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.CreateSecretConnection(r.Context(), &connection, sealed); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "secret_manager.created", "secret_manager", connection.ID,
		connection.Name+" ("+secretmgr.KindLabel(connection.Kind)+")")
	row := store.SecretConnectionRow{SecretConnection: connection}
	writeJSON(w, http.StatusCreated, secretManagerViewOf(row, 0))
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// secretManagerIn finds one of a team's connections for an administrator.
func (s *Server) secretManagerIn(r *http.Request, teamID string) (store.SecretConnectionRow, error) {
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		return store.SecretConnectionRow{}, err
	}
	id := chi.URLParam(r, "connectionID")
	row, err := s.db.FindSecretConnection(r.Context(), teamID, id)
	if errors.Is(err, store.ErrNotFound) {
		return row, errdoc.NotFound("secret manager", id)
	}
	return row, err
}

type updateSecretManagerRequest struct {
	// Settings named here change, and the others stay; an empty one goes
	// back to its default.
	Settings map[string]string `json:"settings,omitempty"`
	// Credentials replace the ones stored when given — a rotated token.
	Credentials    map[string]string `json:"credentials,omitempty"`
	RefreshMinutes *int              `json:"refresh_minutes,omitempty"`
}

func (s *Server) handleUpdateSecretManager(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	row, err := s.secretManagerIn(r, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req updateSecretManagerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	connection := row.SecretConnection
	sealed := ""
	if req.Settings != nil || req.Credentials != nil {
		// The settings named change and the rest stay: a new address does not
		// quietly put the mount back to its default.
		settings := map[string]string{}
		for name, value := range connection.Settings {
			settings[name] = value
		}
		for name, value := range req.Settings {
			settings[name] = value
		}
		credentials := req.Credentials
		if credentials == nil {
			if credentials, err = secretmgr.OpenCredentials(s.keyring, row); err != nil {
				writeError(w, r, err)
				return
			}
		}
		settings, credentials, err = secretmgr.Normalize(connection.Kind, settings, credentials)
		if err != nil {
			writeError(w, r, errdoc.BadRequest(capitalise(err.Error())+"."))
			return
		}
		// Whatever changed is proved before it replaces what worked.
		if err := s.testSecretManager(r.Context(), connection.Name, connection.Kind, settings, credentials); err != nil {
			writeError(w, r, err)
			return
		}
		connection.Settings = settings
		if req.Credentials != nil {
			if sealed, err = secretmgr.SealCredentials(s.keyring, connection.ID, credentials); err != nil {
				writeError(w, r, err)
				return
			}
		}
	}
	if req.RefreshMinutes != nil {
		minutes, err := refreshMinutes(req.RefreshMinutes, connection.RefreshMinutes)
		if err != nil {
			writeError(w, r, err)
			return
		}
		connection.RefreshMinutes = minutes
		connection.RefreshFailures, connection.LastError = 0, ""
		connection.NextRefreshAt = time.Time{}
		if minutes > 0 {
			connection.NextRefreshAt = time.Now().UTC().Add(time.Duration(minutes) * time.Minute)
		}
	}
	if err := s.db.UpdateSecretConnection(r.Context(), &connection, sealed); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "secret_manager.updated", "secret_manager", connection.ID, connection.Name)
	counts, _ := s.db.CountSecretConnectionUses(r.Context(), teamID)
	writeJSON(w, http.StatusOK, secretManagerViewOf(store.SecretConnectionRow{SecretConnection: connection}, counts[connection.ID]))
}

func (s *Server) handleDeleteSecretManager(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	row, err := s.secretManagerIn(r, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Refused while anything reads it: the next deploy of every app that
	// does would fail, and that is a thing to find out now, here.
	uses, err := s.db.SecretConnectionUses(r.Context(), row.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(uses) > 0 {
		labels := make([]string, 0, min(len(uses), 10))
		for _, use := range uses[:min(len(uses), 10)] {
			labels = append(labels, use.Label())
		}
		if len(uses) > 10 {
			labels = append(labels, fmt.Sprintf("and %d more", len(uses)-10))
		}
		writeError(w, r, errdoc.SecretManagerInUse(row.Name, len(uses), strings.Join(labels, ", ")))
		return
	}
	if err := s.db.DeleteSecretConnection(r.Context(), teamID, row.ID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "secret_manager.deleted", "secret_manager", row.ID, row.Name)
	writeOK(w)
}

func (s *Server) handleTestSecretManager(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	row, err := s.secretManagerIn(r, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), secretManagerTimeout)
	defer cancel()
	if err := s.secrets.TestConnection(ctx, row); err != nil {
		writeError(w, r, secretmgr.Problem(row.Name, row.Kind, err))
		return
	}
	writeOK(w)
}

// --- variables that are references ---

// referenceRequest is a variable read from a secret manager, as a request
// names it: the connection, by name or id; the secret's path; and the key
// inside it, for a secret that holds several.
type referenceRequest struct {
	Connection string `json:"connection"`
	Path       string `json:"path"`
	Key        string `json:"key,omitempty"`
}

// referenceFor checks a reference somebody asked for — the connection is the
// team's, the path is one that manager can have — and reads it once, so a
// typo is found when it is written rather than at the next deploy. The value
// read is dropped. It answers the reference to store.
func (s *Server) referenceFor(ctx context.Context, teamID, variable string, from referenceRequest) (*store.SecretReference, error) {
	name := strings.TrimSpace(from.Connection)
	if name == "" {
		return nil, errdoc.BadRequest("Say which secret manager to read it from.")
	}
	row, err := s.db.FindSecretConnection(ctx, teamID, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errdoc.NotFound("secret manager", name)
	}
	if err != nil {
		return nil, err
	}
	path, key := strings.TrimSpace(from.Path), strings.TrimSpace(from.Key)
	if err := secretmgr.CheckReference(row.Kind, path, key); err != nil {
		return nil, errdoc.BadRequest(capitalise(err.Error()) + ".")
	}
	ref := &store.SecretReference{ConnectionID: row.ID, Path: path, Key: key}
	ctx, cancel := context.WithTimeout(ctx, secretManagerTimeout)
	defer cancel()
	if err := s.secrets.Check(ctx, teamID, variable, *ref); err != nil {
		return nil, err
	}
	ref.Connection, ref.Kind = row.Name, row.Kind
	return ref, nil
}

// referenceNames fills in the connection's name and kind on each reference,
// for whoever reads the variables.
func (s *Server) referenceNames(ctx context.Context, teamID string) func(*store.SecretReference) {
	rows, err := s.db.ListSecretConnections(ctx, teamID)
	byID := map[string]store.SecretConnectionRow{}
	if err == nil {
		for _, row := range rows {
			byID[row.ID] = row
		}
	}
	return func(ref *store.SecretReference) {
		if ref == nil {
			return
		}
		if row, ok := byID[ref.ConnectionID]; ok {
			ref.Connection, ref.Kind = row.Name, row.Kind
		}
	}
}

// handleRefreshVariables reads an app's variables from their secret managers
// again, and rolls the app out if any of them changed.
func (s *Server) handleRefreshVariables(w http.ResponseWriter, r *http.Request) {
	app, user, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer == nil {
		writeError(w, r, errdoc.NotConfigured("Deployments", "the panel's cluster connection"))
		return
	}
	result, err := s.deployer.RefreshReferences(r.Context(), app.ID, user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	// The names of what changed, never a value.
	label := "nothing changed"
	if len(result.Changed) > 0 {
		label = truncate(strings.Join(result.Changed, ", "), 255)
	}
	s.audit(r, teamID, "variables.refreshed", "app", app.ID, label)
	writeJSON(w, http.StatusOK, result)
}
