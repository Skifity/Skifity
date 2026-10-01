package api

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/logdrain"
	"skifity/internal/store"
)

// Log drains: where a team's apps' logs are shipped.
//
// A drain belongs to the team, with an optional limit to some of its
// projects, rather than to a project: the services people ship logs to are
// bought per company, and one Datadog account taking the whole team's logs,
// filed under each line's project, is the usual case. A limit covers the
// other — a client's project shipped to the client's own Loki — and is the
// same switch notification channels have. A member limited to some projects
// is refused all of it, as they are every team-wide setting.
//
// Viewers see where the team's logs go and whether the collector is
// running; the settings and the form are administrators'. A secret is never
// answered, to anybody.

// drainSaveTimeout bounds the work a save does after the drain is kept:
// bringing the collector up to date.
const drainSaveTimeout = 30 * time.Second

// Configuration states of a drain, as the list says them.
const (
	drainApplied   = "applied"    // in the configuration the collector runs
	drainPending   = "pending"    // saved, and not yet given to the collector
	drainFailed    = "failed"     // the collector could not be brought up to date
	drainPaused    = "paused"     // switched off
	drainIdle      = "idle"       // limited to projects that are all gone
	drainNoCluster = "no_cluster" // this panel has no cluster to run a collector on
	drainBroken    = "unreadable" // its credentials do not open
)

// logDrainView is one drain as the list shows it.
type logDrainView struct {
	store.LogDrain
	// Destination is where it sends, without a query string.
	Destination string `json:"destination"`
	// Status is how far it has got; see the states above.
	Status string `json:"status"`
	// Settings are its form without its secrets, and Secrets the names of
	// the secrets it has. Administrators only.
	Settings map[string]string `json:"settings,omitempty"`
	Secrets  []string          `json:"secrets,omitempty"`
}

// collectorView is the collector, for everybody in a team with a drain.
type collectorView struct {
	// Configuration is the panel's side: applied, failed, removed or absent.
	Configuration string    `json:"configuration"`
	AppliedAt     time.Time `json:"applied_at,omitzero"`
	Error         string    `json:"error,omitempty"`
	// Live is the cluster's side, when it could be read.
	Live *logdrain.CollectorStatus `json:"live,omitempty"`
}

type logDrainList struct {
	Items     []logDrainView      `json:"items"`
	Total     int                 `json:"total"`
	Kinds     []logdrain.KindSpec `json:"kinds"`
	Limit     int                 `json:"limit"`
	Collector collectorView       `json:"collector"`
}

// authorizeDrainTeam checks the caller's role in a team for a drain, refusing
// a member limited to projects, and says whether they administer it.
func (s *Server) authorizeDrainTeam(r *http.Request, teamID string, required store.Role) (store.User, bool, error) {
	user, membership, err := s.authorizeTeamMember(r, teamID, required)
	if err != nil {
		return store.User{}, false, err
	}
	if membership.Scoped {
		return store.User{}, false, errdoc.ScopedToProjects()
	}
	return user, membership.Role.AtLeast(store.RoleAdmin), nil
}

func (s *Server) handleListLogDrains(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	_, admin, err := s.authorizeDrainTeam(r, teamID, store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := s.db.ListLogDrains(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	component, err := s.db.GetComponent(r.Context(), logdrain.Component)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := logDrainList{Items: make([]logDrainView, 0, len(rows)), Total: len(rows),
		Kinds: logdrain.Kinds(), Limit: logdrain.MaxPerTeam, Collector: s.collectorView(r.Context(), component, rows)}
	for _, row := range rows {
		out.Items = append(out.Items, s.drainView(row, component, admin))
	}
	writeJSON(w, http.StatusOK, out)
}

// collectorView is the collector's state, read from the cluster only for a
// team that has a drain switched on: the page of a team without one says
// nothing about anybody else's.
func (s *Server) collectorView(ctx context.Context, component store.ClusterComponent, rows []store.LogDrainRow) collectorView {
	view := collectorView{Configuration: component.Status, AppliedAt: component.InstalledAt}
	if component.Status == "failed" {
		view.Error = component.Detail
	}
	if s.logs == nil {
		view.Configuration = drainNoCluster
		return view
	}
	if !slices.ContainsFunc(rows, func(row store.LogDrainRow) bool { return row.Enabled }) {
		return view
	}
	live, err := s.logs.LogCollectorStatus(ctx)
	if err != nil {
		s.log.Debug("the log collector's state could not be read", "error", err)
		return view
	}
	view.Live = &live
	return view
}

// drainView is one drain as its team sees it.
func (s *Server) drainView(row store.LogDrainRow, component store.ClusterComponent, admin bool) logDrainView {
	view := logDrainView{LogDrain: row.LogDrain}
	view.Destination = logdrain.Drain{Kind: row.Kind, Settings: row.Settings}.Destination()
	switch {
	case !row.Enabled:
		view.Status = drainPaused
	case row.Scoped && len(row.Projects) == 0:
		view.Status = drainIdle
	case s.logs == nil:
		view.Status = drainNoCluster
	case component.Status == "failed":
		view.Status = drainFailed
	case component.Status != "installed" || row.UpdatedAt.After(component.InstalledAt):
		view.Status = drainPending
	default:
		view.Status = drainApplied
	}
	opened, err := logdrain.FromRow(s.keyring, row)
	if err != nil {
		view.Status = drainBroken
	}
	if admin {
		view.Settings = maps.Clone(row.Settings)
		view.Secrets = slices.Sorted(maps.Keys(opened.Secrets))
	}
	return view
}

type createLogDrainRequest struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Settings is every field of the kind's form, its secrets included.
	Settings map[string]string `json:"settings"`
	// Projects limits the drain to some of the team's projects. Absent is
	// every project.
	Projects      []string `json:"projects,omitempty"`
	IncludeBuilds bool     `json:"include_builds"`
}

// handleCreateLogDrain checks a drain, sends its test line, and keeps it only
// when the service took the line.
func (s *Server) handleCreateLogDrain(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	user, _, err := s.authorizeDrainTeam(r, teamID, store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req createLogDrainRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	existing, err := s.db.ListLogDrains(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(existing) >= logdrain.MaxPerTeam {
		writeError(w, r, errdoc.TooManyLogDrains(logdrain.MaxPerTeam))
		return
	}
	name, err := drainName(req.Name, req.Kind)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Before the test, so a name that is taken sends nothing anywhere.
	if slices.ContainsFunc(existing, func(d store.LogDrainRow) bool { return d.Name == name }) {
		writeError(w, r, errdoc.Conflict("This team already has a log drain called "+name+".",
			"Choose another name, or change the drain that has it."))
		return
	}
	settings, secrets, err := logdrain.Split(req.Kind, req.Settings, nil)
	if err != nil {
		writeError(w, r, drainProblem(err))
		return
	}
	scoped, projects, err := s.channelLimits(r, teamID, req.Projects)
	if err != nil {
		writeError(w, r, err)
		return
	}

	drain := logdrain.Drain{
		ID: store.NewID("ldr"), TeamID: teamID, Name: name, Kind: req.Kind,
		Settings: settings, Secrets: secrets, Scoped: scoped, Projects: projects, IncludeBuilds: req.IncludeBuilds,
	}
	if err := s.drainTester.Send(r.Context(), drain); err != nil {
		writeError(w, r, drainTestProblem(name, err))
		return
	}
	row := store.LogDrainRow{LogDrain: store.LogDrain{
		ID: drain.ID, TeamID: teamID, Name: name, Kind: req.Kind, Settings: settings, Enabled: true,
		Scoped: scoped, Projects: projects, IncludeBuilds: req.IncludeBuilds,
		TestedAt: time.Now().UTC(), CreatedBy: user.Email,
	}}
	if row.SealedSecrets, err = logdrain.Seal(s.keyring, drain); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.CreateLogDrain(r.Context(), &row); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "log_drain.created", "log_drain", row.ID, row.Name)
	s.refreshLogCollector(r)
	writeJSON(w, http.StatusCreated, s.savedDrain(r, teamID, row.ID))
}

type updateLogDrainRequest struct {
	// Name is kept when absent.
	Name *string `json:"name,omitempty"`
	// Settings changes the drain's form: a field it names is replaced, and a
	// secret sent empty is kept. Absent keeps every setting.
	Settings map[string]string `json:"settings,omitempty"`
	// Clear forgets the secrets it names, for an optional one that is no
	// longer wanted.
	Clear []string `json:"clear,omitempty"`
	// Enabled is kept when absent.
	Enabled *bool `json:"enabled,omitempty"`
	// Scoped false sends every project's logs again; Projects limits the
	// drain to these. Both absent keep the limit as it is.
	Scoped   *bool    `json:"scoped,omitempty"`
	Projects []string `json:"projects,omitempty"`
	// IncludeBuilds is kept when absent.
	IncludeBuilds *bool `json:"include_builds,omitempty"`
}

// handleUpdateLogDrain changes a drain. A change to where it sends or what it
// sends with is tested again before it is kept, and so is switching it back
// on; a new address forgets the stored credentials, so a credential nobody
// can read back cannot be sent somewhere new by changing the address under
// it.
func (s *Server) handleUpdateLogDrain(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, _, err := s.authorizeDrainTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	drainID := chi.URLParam(r, "drainID")
	row, err := s.db.GetLogDrain(r.Context(), teamID, drainID)
	if err != nil {
		writeError(w, r, errdoc.NotFound("log drain", drainID))
		return
	}
	var req updateLogDrainRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	stored, err := logdrain.FromRow(s.keyring, row)
	if err != nil {
		writeError(w, r, errdoc.LogDrainUnreadable(row.Name))
		return
	}

	next := stored
	if req.Name != nil {
		if next.Name, err = drainName(*req.Name, row.Kind); err != nil {
			writeError(w, r, err)
			return
		}
	}
	retest := false
	if req.Settings != nil || len(req.Clear) > 0 {
		values := maps.Clone(stored.Settings)
		maps.Copy(values, req.Settings)
		keep := maps.Clone(stored.Secrets)
		for _, key := range req.Clear {
			delete(keep, key)
		}
		before, _ := logdrain.Address(row.Kind, stored.Settings)
		after, addressErr := logdrain.Address(row.Kind, defaultsOf(row.Kind, values))
		moved := addressErr == nil && after != before
		if moved {
			keep = nil
		}
		settings, secrets, err := logdrain.Split(row.Kind, values, keep)
		if err != nil {
			problem := drainProblem(err)
			if moved {
				problem.Cause += " (the address changed, so the credentials stored for the old one were not kept: give them again)"
			}
			writeError(w, r, problem)
			return
		}
		next.Settings, next.Secrets = settings, secrets
		retest = !maps.Equal(settings, stored.Settings) || !maps.Equal(secrets, stored.Secrets)
	}
	enabled := row.Enabled
	if req.Enabled != nil {
		retest = retest || (*req.Enabled && !row.Enabled)
		enabled = *req.Enabled
	}
	switch {
	case req.Projects != nil:
		if next.Scoped, next.Projects, err = s.channelLimits(r, teamID, req.Projects); err != nil {
			writeError(w, r, err)
			return
		}
	case req.Scoped != nil && !*req.Scoped:
		next.Scoped, next.Projects = false, nil
	}
	if req.IncludeBuilds != nil {
		next.IncludeBuilds = *req.IncludeBuilds
	}

	tested := row.TestedAt
	if retest && enabled {
		if err := s.drainTester.Send(r.Context(), next); err != nil {
			writeError(w, r, drainTestProblem(next.Name, err))
			return
		}
		tested = time.Now().UTC()
	}
	updated := store.LogDrainRow{LogDrain: store.LogDrain{
		ID: row.ID, TeamID: teamID, Name: next.Name, Kind: row.Kind, Settings: next.Settings, Enabled: enabled,
		Scoped: next.Scoped, Projects: next.Projects, IncludeBuilds: next.IncludeBuilds,
		TestedAt: tested, TestError: row.TestError,
	}}
	if retest && enabled {
		updated.TestError = ""
	}
	// Sealed again whatever changed: the address is part of what the
	// secrets are sealed with.
	if updated.SealedSecrets, err = logdrain.Seal(s.keyring, next); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.UpdateLogDrain(r.Context(), &updated); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "log_drain.updated", "log_drain", row.ID, updated.Name)
	s.refreshLogCollector(r)
	writeJSON(w, http.StatusOK, s.savedDrain(r, teamID, row.ID))
}

// defaultsOf fills in a kind's defaults, so an address worked out from what a
// form sent is the one the drain will have.
func defaultsOf(kind string, values map[string]string) map[string]string {
	out := maps.Clone(values)
	if spec, ok := logdrain.Spec(kind); ok {
		for _, field := range spec.Fields {
			if strings.TrimSpace(out[field.Key]) == "" && field.Default != "" {
				out[field.Key] = field.Default
			}
			out[field.Key] = strings.TrimSpace(out[field.Key])
		}
	}
	return out
}

func (s *Server) handleDeleteLogDrain(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, _, err := s.authorizeDrainTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	drainID := chi.URLParam(r, "drainID")
	row, err := s.db.GetLogDrain(r.Context(), teamID, drainID)
	if err != nil {
		writeError(w, r, errdoc.NotFound("log drain", drainID))
		return
	}
	if err := s.db.DeleteLogDrain(r.Context(), teamID, drainID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "log_drain.deleted", "log_drain", row.ID, row.Name)
	s.refreshLogCollector(r)
	writeOK(w)
}

// handleTestLogDrain sends a stored drain's test line again, and records how
// it went.
func (s *Server) handleTestLogDrain(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, _, err := s.authorizeDrainTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	drainID := chi.URLParam(r, "drainID")
	row, err := s.db.GetLogDrain(r.Context(), teamID, drainID)
	if err != nil {
		writeError(w, r, errdoc.NotFound("log drain", drainID))
		return
	}
	drain, err := logdrain.FromRow(s.keyring, row)
	if err != nil {
		writeError(w, r, errdoc.LogDrainUnreadable(row.Name))
		return
	}
	now := time.Now().UTC()
	sendErr := s.drainTester.Send(r.Context(), drain)
	failure := ""
	if sendErr != nil {
		failure = drainTestProblem(row.Name, sendErr).Cause
	}
	if err := s.db.RecordLogDrainTest(context.WithoutCancel(r.Context()), teamID, row.ID, now, failure); err != nil {
		s.log.Warn("could not record a log drain's test", "drain", row.ID, "error", err)
	}
	if sendErr != nil {
		writeError(w, r, drainTestProblem(row.Name, sendErr))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tested_at": now})
}

// refreshLogCollector brings the collector up to date after a change. A
// failure does not undo the change: the drain is kept, the failure is
// recorded where the list shows it, and the periodic refresh tries again.
func (s *Server) refreshLogCollector(r *http.Request) {
	if s.logs == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), drainSaveTimeout)
	defer cancel()
	if err := s.logs.RefreshLogDrains(ctx); err != nil {
		s.log.Warn("the log collector could not be brought up to date", "error", err)
	}
}

// savedDrain is a drain as it reads after a change, for the answer.
func (s *Server) savedDrain(r *http.Request, teamID, id string) logDrainView {
	row, err := s.db.GetLogDrain(r.Context(), teamID, id)
	if err != nil {
		return logDrainView{LogDrain: store.LogDrain{ID: id, TeamID: teamID}}
	}
	component, _ := s.db.GetComponent(r.Context(), logdrain.Component)
	return s.drainView(row, component, true)
}

// drainName is a drain's name, or its kind's when none is given.
func drainName(name, kind string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = kind
	}
	if len(name) > 60 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errdoc.LogDrainInvalid("a drain's name is one line of at most 60 characters")
	}
	return name, nil
}

// drainProblem turns a drain's settings being refused into the error that
// says so.
func drainProblem(err error) *errdoc.Problem {
	var invalid *logdrain.Invalid
	if errors.As(err, &invalid) {
		return errdoc.LogDrainInvalid(invalid.Reason)
	}
	return errdoc.LogDrainInvalid(err.Error())
}

// drainTestProblem is a test that did not get through.
func drainTestProblem(name string, err error) *errdoc.Problem {
	var invalid *logdrain.Invalid
	if errors.As(err, &invalid) {
		return errdoc.LogDrainInvalid(invalid.Reason)
	}
	return errdoc.LogDrainTestFailed(name, err.Error())
}
