package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// What a Git connection can read, for the form that creates an app from it.
//
// Both answers are read with the team's token, from the connection's own host
// and nowhere else: the repository is named by its path on that host, never
// by an address somebody typed, so there is no second host a token could be
// sent to. See gitsrc.ListRepositories.

// maxListQuery bounds a search. A repository's full name is at most a few
// hundred characters on any host, and the query is sent on to GitLab's.
const maxListQuery = 100

// handleListGitRepositories lists the repositories a connection can read.
func (s *Server) handleListGitRepositories(w http.ResponseWriter, r *http.Request) {
	source, ok := s.listableGitSource(w, r)
	if !ok {
		return
	}
	listing, err := gitsrc.ListRepositories(r.Context(), s.gitListRequest(r, source))
	if err != nil {
		writeError(w, r, s.gitListingProblem(source, err))
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

// handleListGitBranches lists the branches of one repository a connection can
// read, named by its full name on the connection's host.
func (s *Server) handleListGitBranches(w http.ResponseWriter, r *http.Request) {
	source, ok := s.listableGitSource(w, r)
	if !ok {
		return
	}
	repo := strings.Trim(strings.TrimSpace(r.URL.Query().Get("repo")), "/")
	if !gitsrc.ValidRepoName(source.Kind, repo) {
		writeError(w, r, errdoc.BadRequest("Name the repository as owner/name, the way the list of repositories does."))
		return
	}
	listing, err := gitsrc.ListBranches(r.Context(), s.gitListRequest(r, source), repo)
	if err != nil {
		writeError(w, r, s.gitListingProblem(source, err))
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

// listableGitSource authorizes a listing and finds the connection it asks.
//
// Open to a member limited to projects, as the list of connections and
// detection are: picking the repository is how an app is created in one of
// their projects. Not to a viewer, who creates no apps, since the answer is
// read with the team's credential and names its private repositories.
func (s *Server) listableGitSource(w http.ResponseWriter, r *http.Request) (store.GitSource, bool) {
	teamID := chi.URLParam(r, "teamID")
	if _, _, err := s.authorizeTeamMember(r, teamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return store.GitSource{}, false
	}
	sourceID := chi.URLParam(r, "sourceID")
	source, err := s.db.GetGitSource(r.Context(), sourceID)
	// Another team's connection answers exactly as one that does not exist.
	if err != nil || source.TeamID != teamID {
		writeError(w, r, errdoc.NotFound("Git connection", sourceID))
		return store.GitSource{}, false
	}
	if !gitsrc.CanList(source.Kind) {
		writeError(w, r, errdoc.GitListingUnsupported(source.Name))
		return store.GitSource{}, false
	}
	return source, true
}

func (s *Server) gitListRequest(r *http.Request, source store.GitSource) gitsrc.ListRequest {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > maxListQuery {
		query = query[:maxListQuery]
	}
	return gitsrc.ListRequest{
		Kind:    source.Kind,
		BaseURL: source.BaseURL,
		Token:   s.gitToken(r, source),
		Query:   query,
	}
}

func (s *Server) gitListingProblem(source store.GitSource, err error) *errdoc.Problem {
	if errors.Is(err, gitsrc.ErrListingUnsupported) {
		return errdoc.GitListingUnsupported(source.Name)
	}
	s.log.Warn("could not list from a Git connection", "git_source", source.ID, "error", err)
	return errdoc.GitListingFailed(source.Name, err.Error())
}
