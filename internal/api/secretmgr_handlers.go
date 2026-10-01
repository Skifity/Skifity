package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
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
	// StoppedResolving is only in the answer to a change of limits that was
	// forced: the variables, as app/KEY, that its new limits refuse.
	StoppedResolving []string `json:"stopped_resolving,omitempty"`
}

func secretManagerViewOf(row store.SecretConnectionRow, usedBy int) secretManagerView {
	return secretManagerView{
		SecretConnection: row.SecretConnection,
		Credentials:      secretmgr.CredentialNames(row.Kind, row.Settings),
		UsedBy:           usedBy,
	}
}

// visibleTo is a connection as a member sees it. A member limited to some
// projects is shown only the connections one of their projects may read
// through, and of each only their own projects among those it is limited
// to: they are not told what else the team has, here any more than anywhere
// else (see authorizeInProject).
func visibleTo(membership store.Membership, row store.SecretConnectionRow) (store.SecretConnectionRow, bool) {
	if !membership.Scoped || len(row.AllowedProjectIDs) == 0 {
		return row, true
	}
	mine := []string{}
	for _, id := range row.AllowedProjectIDs {
		if membership.Reaches(id) {
			mine = append(mine, id)
		}
	}
	if len(mine) == 0 {
		return row, false
	}
	row.AllowedProjectIDs = mine
	return row, true
}

func (s *Server) handleListSecretManagers(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	// A member limited to some projects sets variables in them too, and
	// picks the connection from this list.
	_, membership, err := s.authorizeTeamMember(r, teamID, store.RoleViewer)
	if err != nil {
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
		if row, ok := visibleTo(membership, row); ok {
			out = append(out, secretManagerViewOf(row, counts[row.ID]))
		}
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
	// AllowedPaths and AllowedProjectIDs limit what the connection may be
	// used for. Left out or empty, it is every path and every project.
	AllowedPaths      []string `json:"allowed_paths,omitempty"`
	AllowedProjectIDs []string `json:"allowed_project_ids,omitempty"`
}

// allowedPaths checks the paths a connection is asked to be limited to.
func allowedPaths(kind string, settings map[string]string, asked []string) ([]string, error) {
	paths, err := secretmgr.NormalizeAllowedPaths(kind, settings, asked)
	if err != nil {
		return nil, errdoc.BadRequest(capitalise(err.Error()) + ".")
	}
	// A list of nothing but blanks is not taken for an empty one, which is
	// every path: a limit is never lifted by a typo.
	if len(asked) > 0 && len(paths) == 0 {
		return nil, errdoc.BadRequest("The paths to limit the connection to are all blank. Send an empty list to allow every path.")
	}
	return paths, nil
}

// allowedProjects checks the projects a connection is asked to be limited
// to: each is one of the team's, and repeats are dropped.
func (s *Server) allowedProjects(ctx context.Context, teamID string, asked []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range asked {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, errdoc.BadRequest("A project to limit the connection to is blank. Send an empty list to allow every project.")
		}
		if seen[id] {
			continue
		}
		project, err := s.db.GetProject(ctx, id)
		if err != nil || project.TeamID != teamID {
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
			return nil, errdoc.BadRequest(fmt.Sprintf("%s is not a project of this team.", id))
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// limitsLabel is what a connection is limited to, in words, for the audit
// log: its paths, and its projects by name.
func (s *Server) limitsLabel(ctx context.Context, c store.SecretConnection) string {
	paths := "any path"
	if len(c.AllowedPaths) > 0 {
		paths = "paths " + strings.Join(c.AllowedPaths, ", ")
	}
	projects := "every project"
	if len(c.AllowedProjectIDs) > 0 {
		names := make([]string, 0, len(c.AllowedProjectIDs))
		for _, id := range c.AllowedProjectIDs {
			name := id
			if project, err := s.db.GetProject(ctx, id); err == nil {
				name = project.Name
			}
			names = append(names, name)
		}
		projects = "projects " + strings.Join(names, ", ")
	}
	return paths + "; " + projects
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
	paths, err := allowedPaths(req.Kind, settings, req.AllowedPaths)
	if err != nil {
		writeError(w, r, err)
		return
	}
	projects, err := s.allowedProjects(r.Context(), teamID, req.AllowedProjectIDs)
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
		AllowedPaths: paths, AllowedProjectIDs: projects, RefreshMinutes: minutes,
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
		truncate(connection.Name+" ("+secretmgr.KindLabel(connection.Kind)+"): "+s.limitsLabel(r.Context(), connection), 255))
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
	// AllowedPaths and AllowedProjectIDs replace the connection's limits when
	// given; an empty list lifts that limit. Left out, it stays.
	AllowedPaths      *[]string `json:"allowed_paths,omitempty"`
	AllowedProjectIDs *[]string `json:"allowed_project_ids,omitempty"`
	// Force saves limits that leave variables which read the connection now
	// outside them. See cutOff.
	Force bool `json:"force,omitempty"`
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
	signIn := req.Settings != nil || req.Credentials != nil
	var credentials map[string]string
	if signIn {
		// The settings named change and the rest stay: a new address does not
		// quietly put the mount back to its default.
		settings := map[string]string{}
		for name, value := range connection.Settings {
			settings[name] = value
		}
		for name, value := range req.Settings {
			settings[name] = value
		}
		credentials = req.Credentials
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
		connection.Settings = settings
	}

	// The limits, read against the settings as they will be: a Vault prefix
	// written as <mount>/data/... names the new mount.
	if req.AllowedPaths != nil {
		if connection.AllowedPaths, err = allowedPaths(connection.Kind, connection.Settings, *req.AllowedPaths); err != nil {
			writeError(w, r, err)
			return
		}
	}
	if req.AllowedProjectIDs != nil {
		if connection.AllowedProjectIDs, err = s.allowedProjects(r.Context(), teamID, *req.AllowedProjectIDs); err != nil {
			writeError(w, r, err)
			return
		}
	}
	limited := !slices.Equal(row.AllowedPaths, connection.AllowedPaths) ||
		!slices.Equal(row.AllowedProjectIDs, connection.AllowedProjectIDs)
	var stopped []store.SecretConnectionUse
	if limited {
		if stopped, err = s.cutOff(r.Context(), row.SecretConnection, connection); err != nil {
			writeError(w, r, err)
			return
		}
		if len(stopped) > 0 && !req.Force {
			writeError(w, r, errdoc.SecretManagerLimitsBreakReferences(connection.Name, len(stopped), useLabels(stopped)))
			return
		}
	}

	if signIn {
		// Whatever changed is proved before it replaces what worked.
		if err := s.testSecretManager(r.Context(), connection.Name, connection.Kind, connection.Settings, credentials); err != nil {
			writeError(w, r, err)
			return
		}
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
	if signIn || req.RefreshMinutes != nil {
		s.audit(r, teamID, "secret_manager.updated", "secret_manager", connection.ID, connection.Name)
	}
	if limited {
		// What it is limited to now, and what that cut off: who narrowed a
		// connection, and to what, is the first question after a deploy
		// fails with secrets.reference_not_allowed.
		label := connection.Name + ": " + s.limitsLabel(r.Context(), connection)
		if len(stopped) > 0 {
			label += fmt.Sprintf("; forced, %d variables no longer resolve", len(stopped))
		}
		s.audit(r, teamID, "secret_manager.limited", "secret_manager", connection.ID, truncate(label, 255))
	}
	counts, _ := s.db.CountSecretConnectionUses(r.Context(), teamID)
	view := secretManagerViewOf(store.SecretConnectionRow{SecretConnection: connection}, counts[connection.ID])
	for _, use := range stopped {
		view.StoppedResolving = append(view.StoppedResolving, use.Label())
	}
	writeJSON(w, http.StatusOK, view)
}

// cutOff lists the variables that read a connection now and that new limits
// would refuse: what narrowing it breaks. One its old limits already refuse is
// not listed — it is broken already, and a change that breaks nothing more
// needs nobody to insist.
//
// A change that cuts variables off is refused unless the request says force,
// rather than saved and answered with the list. The refusal is the warning,
// before anything changes, for every client — the panel, the CLI, a script, an
// assistant — and not only for a page that remembers to ask first; a list
// answered after saving would be a warning about something already done. It
// is the same line removing a connection in use draws (secrets.connection_in_use),
// with one difference: cutting a project off is sometimes exactly the point,
// so a narrowing can be forced where a removal cannot. What it prevents is the
// next deploy, sync or unattended periodic refresh of those apps failing on a
// limit nobody told their owners about.
func (s *Server) cutOff(ctx context.Context, was, now store.SecretConnection) ([]store.SecretConnectionUse, error) {
	uses, err := s.db.SecretConnectionUses(ctx, was.ID)
	if err != nil {
		return nil, err
	}
	out := []store.SecretConnectionUse{}
	for _, use := range uses {
		if secretmgr.Refuses(was, use.ProjectID, use.Path) == "" && secretmgr.Refuses(now, use.ProjectID, use.Path) != "" {
			out = append(out, use)
		}
	}
	return out, nil
}

// useLabels names at most ten variables that read a connection, as app/KEY,
// and counts the rest.
func useLabels(uses []store.SecretConnectionUse) string {
	labels := make([]string, 0, min(len(uses), 10)+1)
	for _, use := range uses[:min(len(uses), 10)] {
		labels = append(labels, use.Label())
	}
	if len(uses) > 10 {
		labels = append(labels, fmt.Sprintf("and %d more", len(uses)-10))
	}
	return strings.Join(labels, ", ")
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
		writeError(w, r, errdoc.SecretManagerInUse(row.Name, len(uses), useLabels(uses)))
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

// referenceFor checks a reference somebody asked for, for a variable of a
// project — the connection is the team's, the path is one that manager can
// have, and the connection's limits allow both the path and the project —
// and reads it once, so a typo is found when it is written rather than at the
// next deploy. The value read is dropped. It answers the reference to store.
//
// The limits are checked before anything is read: a path outside them is
// never asked for, so a refusal says nothing about whether it exists.
func (s *Server) referenceFor(ctx context.Context, teamID, projectID, variable string, from referenceRequest) (*store.SecretReference, error) {
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
	if err := s.secrets.CheckLimits(ctx, row, projectID, secretmgr.Wanted{Variable: variable, Reference: *ref}); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, secretManagerTimeout)
	defer cancel()
	if err := s.secrets.Check(ctx, teamID, projectID, variable, *ref); err != nil {
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
