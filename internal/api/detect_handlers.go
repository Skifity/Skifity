package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// Looking at a repository before building it.
//
// The detector was written, tested and called by nothing, because the panel had
// no way to read a repository's file list. Railpack does its own detection
// inside the build, so builds worked; what was missing was the panel saying
// "this looks like Next.js and listens on 3000" while somebody is still filling
// in the form, which is the difference between a first deploy that works and
// one that comes back with a port nobody set.

type detectRequest struct {
	RepoURL     string `json:"repo_url"`
	Branch      string `json:"branch,omitempty"`
	RootDir     string `json:"root_dir,omitempty"`
	GitSourceID string `json:"git_source_id,omitempty"`
}

// handleDetect looks at a repository and says what it thinks it is.
//
// A guess, and it says so: Confidence is low when it is working from a file
// extension, and the form presents a low-confidence guess as a question rather
// than filling the field in.
func (s *Server) handleDetect(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}

	var req detectRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	// The same check the app form does, for the same reason: this address
	// decides where a token is sent.
	repoURL, err := gitsrc.ValidateRepoURL(strings.TrimSpace(req.RepoURL))
	if err != nil {
		writeError(w, r, errdoc.BadRequest(capitalise(err.Error())+"."))
		return
	}

	lookup := gitsrc.TreeRequest{
		RepoURL: repoURL,
		Ref:     strings.TrimSpace(req.Branch),
		RootDir: strings.TrimPrefix(strings.TrimSpace(req.RootDir), "/"),
	}
	if req.GitSourceID != "" {
		source, err := s.db.GetGitSource(r.Context(), req.GitSourceID)
		if err != nil {
			writeError(w, r, errdoc.NotFound("Git connection", req.GitSourceID))
			return
		}
		// A connection belongs to a team, and a member of one team asking about
		// another's connection would be reading a credential sideways.
		if source.TeamID != teamID {
			writeError(w, r, errdoc.NotFound("Git connection", req.GitSourceID))
			return
		}
		lookup.Kind, lookup.BaseURL = source.Kind, source.BaseURL
		lookup.Token = s.gitToken(r, source)
	}

	tree, err := gitsrc.ReadTree(r.Context(), lookup)
	if err != nil {
		writeError(w, r, errdoc.New("detect.unreadable", "That repository could not be read").
			WithCause("%s", capitalise(err.Error())+".").
			WithImpact("Nothing was created. You can still fill the form in yourself.").
			WithFix("Check the address and the branch. A private repository needs a "+
				"connected Git account with access to it, chosen below.").
			WithStatus(http.StatusBadRequest))
		return
	}

	detection := builder.Detect(builder.Tree{Files: tree.Files, Contents: tree.Contents})
	writeJSON(w, http.StatusOK, struct {
		builder.Detection
		// Truncated says the repository was larger than the panel read. The
		// guess is still usable, and saying so is what keeps a "nothing found"
		// from reading as "there is nothing there".
		Truncated bool `json:"truncated,omitempty"`
	}{Detection: detection, Truncated: tree.Truncated})
}

// gitToken opens a connection's stored token, or returns an empty string.
//
// A failure is not reported: a public repository needs no token, and the lookup
// that follows will say plainly whether it could be read.
func (s *Server) gitToken(r *http.Request, source store.GitSource) string {
	if source.ConfigEnc == "" {
		return ""
	}
	raw, err := s.keyring.Open(source.ConfigEnc, "git_source:"+source.TeamID+":"+source.Name)
	if err != nil {
		s.log.Warn("could not read a Git connection's token", "git_source", source.ID, "error", err)
		return ""
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		return ""
	}
	return config["token"]
}
