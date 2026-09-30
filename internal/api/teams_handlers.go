package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/store"
)

func (s *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	teams, err := s.db.ListTeamsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, teams)
}

type createTeamRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	var req createTeamRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, r, errdoc.BadRequest("A team needs a name."))
		return
	}
	team := store.Team{Name: name, Slug: kube.Slugify(name)}
	if err := s.db.CreateTeam(r.Context(), &team); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.AddMember(r.Context(), team.ID, user.ID, store.RoleOwner); err != nil {
		writeError(w, r, err)
		return
	}
	team.Role = store.RoleOwner
	s.audit(r, team.ID, "team.created", "team", team.ID, team.Name)
	writeJSON(w, http.StatusCreated, team)
}

func (s *Server) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	user, err := s.authorizeTeam(r, teamID, store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	team, err := s.db.GetTeam(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	membership, err := s.db.GetMembership(r.Context(), teamID, user.ID)
	if err == nil {
		team.Role = membership.Role
	}
	writeJSON(w, http.StatusOK, team)
}

type updateTeamRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleUpdateTeam(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	var req updateTeamRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	team, err := s.db.GetTeam(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		team.Name = name
		// The slug is not changed: it is baked into namespace names, and
		// renaming a namespace would mean recreating every workload in it.
	}
	if err := s.db.UpdateTeam(r.Context(), &team); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "team.updated", "team", teamID, team.Name)
	writeJSON(w, http.StatusOK, team)
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}
	members, err := s.db.ListMembers(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, members)
}

type addMemberRequest struct {
	Email string     `json:"email"`
	Role  store.Role `json:"role"`
}

func (s *Server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	actor, err := s.authorizeTeam(r, teamID, store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req addMemberRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if !req.Role.Valid() {
		writeError(w, r, errdoc.BadRequest("Role must be owner, admin or member."))
		return
	}
	// An admin must not be able to make someone an owner and so outrank
	// themselves out of the decision.
	actorMembership, err := s.db.GetMembership(r.Context(), teamID, actor.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !actorMembership.Role.AtLeast(req.Role) {
		writeError(w, r, errdoc.Forbidden("granting a role higher than your own"))
		return
	}

	target, err := s.db.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, r, errdoc.New("team.no_such_user", "No account with that email").
			WithCause("Nobody on this panel uses the address %s.", req.Email).
			WithImpact("Nobody was added to the team.").
			WithFix("Ask them to create an account first, then add them. Skifity does not send invitations to addresses that have never signed in.").
			WithStatus(http.StatusNotFound))
		return
	}

	// Demoting the last owner would leave a team nobody can administer.
	//
	// Not being a member yet is the ordinary case — that is what adding one is
	// — and there is nothing to demote. Any other failure is a check that did
	// not run, and a guard that stops guarding when a query fails is not a
	// guard, so it refuses rather than assuming the answer it wanted.
	existing, err := s.db.GetMembership(r.Context(), teamID, target.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		writeError(w, r, err)
		return
	case existing.Role == store.RoleOwner && req.Role != store.RoleOwner:
		count, err := s.db.CountOwners(r.Context(), teamID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if count <= 1 {
			writeError(w, r, errdoc.New("team.last_owner", "A team needs at least one owner").
				WithCause("%s is the only owner of this team.", target.Email).
				WithImpact("Nothing was changed.").
				WithFix("Make someone else an owner first, then change this role.").
				WithStatus(http.StatusConflict))
			return
		}
	}

	if err := s.db.AddMember(r.Context(), teamID, target.ID, req.Role); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "team.member_added", "user", target.ID, target.Email)
	writeOK(w)
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	userID := chi.URLParam(r, "userID")
	actor, err := s.authorizeTeam(r, teamID, store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}

	membership, err := s.db.GetMembership(r.Context(), teamID, userID)
	if err != nil {
		writeError(w, r, errdoc.NotFound("team member", userID))
		return
	}
	if membership.Role == store.RoleOwner {
		count, err := s.db.CountOwners(r.Context(), teamID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if count <= 1 {
			writeError(w, r, errdoc.New("team.last_owner", "A team needs at least one owner").
				WithCause("This is the only owner of the team.").
				WithImpact("Nothing was changed.").
				WithFix("Make someone else an owner first, then remove this one.").
				WithStatus(http.StatusConflict))
			return
		}
		// Only an owner may remove another owner. An actor whose own
		// membership cannot be read is not an owner as far as this is
		// concerned: a permission check that passes when the lookup fails is
		// the wrong way round.
		actorMembership, err := s.db.GetMembership(r.Context(), teamID, actor.ID)
		if err != nil || actorMembership.Role != store.RoleOwner {
			writeError(w, r, errdoc.Forbidden("removing an owner"))
			return
		}
	}

	if err := s.db.RemoveMember(r.Context(), teamID, userID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "team.member_removed", "user", userID, "")
	writeOK(w)
}

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	entries, err := s.db.ListAudit(r.Context(), teamID,
		r.URL.Query().Get("action"), r.URL.Query().Get("target_id"), queryInt(r, "limit", 100))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, entries)
}

// --- projects ---

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}
	projects, err := s.db.ListProjects(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, projects)
}

type createProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}
	var req createProjectRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, r, errdoc.BadRequest("A project needs a name."))
		return
	}

	team, err := s.db.GetTeam(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	project := store.Project{
		TeamID:      teamID,
		Name:        name,
		Slug:        kube.Slugify(name),
		Description: strings.TrimSpace(req.Description),
	}
	if err := s.db.CreateProject(r.Context(), &project); err != nil {
		writeError(w, r, err)
		return
	}

	// Every project starts with a production environment, because a project
	// with nowhere to deploy is not useful.
	env := store.Environment{
		ProjectID: project.ID,
		Name:      "Production",
		Slug:      "production",
		Kind:      store.EnvStandard,
		Namespace: kube.NamespaceFor(team.Slug, project.Slug, "production"),
	}
	if err := s.db.CreateEnvironment(r.Context(), &env); err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster != nil {
		if err := s.cluster.EnsureNamespace(r.Context(), env, teamID, project.ID); err != nil {
			s.log.Warn("could not create namespace", "namespace", env.Namespace, "error", err)
		}
	}

	s.audit(r, teamID, "project.created", "project", project.ID, project.Name)
	s.hub.Publish(events.TeamTopic(teamID), "project.created", project)
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

type updateProjectRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req updateProjectRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		project.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		project.Description = strings.TrimSpace(*req.Description)
	}
	if err := s.db.UpdateProject(r.Context(), &project); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, project.TeamID, "project.updated", "project", project.ID, project.Name)
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Remove the namespaces before the rows, so a failure here does not orphan
	// running workloads with no record of them in the panel.
	envs, err := s.db.ListEnvironments(r.Context(), project.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster != nil {
		for _, env := range envs {
			if err := s.cluster.DeleteNamespace(r.Context(), env.Namespace); err != nil {
				s.log.Warn("could not delete namespace", "namespace", env.Namespace, "error", err)
			}
		}
	}
	if err := s.db.DeleteProject(r.Context(), project.ID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, project.TeamID, "project.deleted", "project", project.ID, project.Name)
	s.hub.Publish(events.TeamTopic(project.TeamID), "project.deleted", project)
	writeOK(w)
}

// --- environments ---

func (s *Server) handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	envs, err := s.db.ListEnvironments(r.Context(), project.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, envs)
}

type createEnvironmentRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req createEnvironmentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, r, errdoc.BadRequest("An environment needs a name."))
		return
	}
	team, err := s.db.GetTeam(r.Context(), project.TeamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	env := store.Environment{
		ProjectID: project.ID,
		Name:      name,
		Slug:      kube.Slugify(name),
		Kind:      store.EnvStandard,
	}
	env.Namespace = kube.NamespaceFor(team.Slug, project.Slug, env.Slug)
	if err := s.db.CreateEnvironment(r.Context(), &env); err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster != nil {
		if err := s.cluster.EnsureNamespace(r.Context(), env, project.TeamID, project.ID); err != nil {
			s.log.Warn("could not create namespace", "namespace", env.Namespace, "error", err)
		}
	}
	s.audit(r, project.TeamID, "environment.created", "environment", env.ID, env.Name)
	writeJSON(w, http.StatusCreated, env)
}

func (s *Server) handleGetEnvironment(w http.ResponseWriter, r *http.Request) {
	env, _, err := s.authorizeEnvironment(r, chi.URLParam(r, "envID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

type updateEnvironmentRequest struct {
	PodSecurity string `json:"pod_security"`
}

// handleUpdateEnvironment changes how strictly an environment confines its pods.
//
// Admin rather than member: this is the one control in the panel that decides
// what a container in this namespace is allowed to be, and a member who can
// deploy should not be able to widen it for everybody else in the team.
func (s *Server) handleUpdateEnvironment(w http.ResponseWriter, r *http.Request) {
	env, _, err := s.authorizeEnvironment(r, chi.URLParam(r, "envID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	project, err := s.db.GetProject(r.Context(), env.ProjectID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req updateEnvironmentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if !kube.ValidPodSecurity(req.PodSecurity) {
		writeError(w, r, errdoc.BadRequest(
			"That is not a confinement level. It is \"restricted\" or \"baseline\"."))
		return
	}
	if err := s.db.SetEnvironmentPodSecurity(r.Context(), env.ID, req.PodSecurity); err != nil {
		writeError(w, r, err)
		return
	}
	env.PodSecurity = req.PodSecurity
	s.audit(r, project.TeamID, "environment.pod_security_changed", "environment", env.ID,
		env.Name+" → "+req.PodSecurity)

	// The label on the live namespace is what Kubernetes actually enforces, so
	// a saved row that never reached the cluster is the setting doing nothing.
	if s.cluster != nil {
		if err := s.cluster.EnsureNamespace(r.Context(), env, project.TeamID, project.ID); err != nil {
			writeError(w, r, errdoc.New("environment.level_not_applied",
				"The level was saved, but the namespace was not changed").
				WithCause("%s", err).
				WithImpact("Kubernetes is still enforcing the previous level, so a deploy will behave as it did before.").
				WithFix("Fix what the cause says and save the level again.").
				WithStatus(http.StatusBadGateway))
			return
		}
	}
	writeJSON(w, http.StatusOK, env)
}

// handleEnvironmentQuota reports how much of an environment's ceiling is used.
//
// Every environment has had a quota from the day it was created and nothing
// showed it, so the first sign of reaching one was a deployment that failed with
// a message about a resource nobody had heard of.
func (s *Server) handleEnvironmentQuota(w http.ResponseWriter, r *http.Request) {
	env, _, err := s.authorizeEnvironment(r, chi.URLParam(r, "envID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		// Not an error: a panel with no cluster has environments on paper and
		// no limits to report against them.
		writeJSON(w, http.StatusOK, EnvironmentQuota{})
		return
	}
	quota, err := s.cluster.QuotaUsage(r.Context(), env.Namespace)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, quota)
}

func (s *Server) handleDeleteEnvironment(w http.ResponseWriter, r *http.Request) {
	env, _, err := s.authorizeEnvironment(r, chi.URLParam(r, "envID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForEnvironment(r.Context(), env.ID)
	if s.cluster != nil {
		if err := s.cluster.DeleteNamespace(r.Context(), env.Namespace); err != nil {
			s.log.Warn("could not delete namespace", "namespace", env.Namespace, "error", err)
		}
	}
	if err := s.db.DeleteEnvironment(r.Context(), env.ID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "environment.deleted", "environment", env.ID, env.Name)
	writeOK(w)
}
