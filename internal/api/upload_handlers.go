package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/gitsrc"
	"skifity/internal/store"
	"skifity/internal/upload"
)

// uploadReadTime is how long an upload may take to arrive. The server's own
// read timeout is a minute, which is right for every other request and cuts a
// few megabytes off at the knees on a slow line; this one request gets longer.
const uploadReadTime = 15 * time.Minute

// handleUploadSource receives an app's code as a gzipped tar, for an app with
// no repository. `skifity up` is what sends it.
//
// It only stores the code. Deploying it is a second request naming the upload,
// so a deploy is the same thing whichever way the code arrived, and the same
// upload can be deployed again without being sent again.
func (s *Server) handleUploadSource(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.uploads == nil {
		writeError(w, r, errdoc.NotConfigured("Deploying uploaded code", "the panel's data directory"))
		return
	}
	if app.SourceType != "upload" {
		writeError(w, r, errdoc.NotAnUploadApp(app.Name))
		return
	}

	// Best effort: a server that cannot extend it still has its own timeout,
	// which is only worse for somebody on a slow line.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(uploadReadTime))
	limits := s.uploads.Limits
	if limits == (upload.Limits{}) {
		limits = upload.DefaultLimits
	}
	body := http.MaxBytesReader(w, r.Body, limits.Compressed+1)

	summary, err := s.uploads.Save(app.ID, body)
	var refusal *upload.Refusal
	var tooBig *http.MaxBytesError
	switch {
	case errors.Is(err, upload.ErrTooLarge), errors.As(err, &tooBig):
		writeError(w, r, errdoc.UploadTooLarge(limits.Compressed>>20))
		return
	case errors.As(err, &refusal):
		writeError(w, r, refusalProblem(refusal, limits))
		return
	case err != nil:
		writeError(w, r, err)
		return
	}

	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.source_uploaded", "app", app.ID, app.Name)
	writeJSON(w, http.StatusCreated, summary)
}

// handleDetectUpload says what a folder is and what it needs, before any app
// exists: the panel's form sends the folder somebody picked, packed in the
// browser, and shows the answer the way it shows one for a repository.
//
// Nothing is stored. The same archive is sent again, to the app, once it has
// been created — a second trip, and in exchange nothing is kept for a form
// that is abandoned halfway.
func (s *Server) handleDetectUpload(w http.ResponseWriter, r *http.Request) {
	if _, err := s.authorizeTeam(r, chi.URLParam(r, "teamID"), store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}
	if s.uploads == nil {
		writeError(w, r, errdoc.NotConfigured("Deploying uploaded code", "the panel's data directory"))
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(uploadReadTime))
	limits := s.uploads.Limits
	if limits == (upload.Limits{}) {
		limits = upload.DefaultLimits
	}
	body := http.MaxBytesReader(w, r.Body, limits.Compressed+1)

	files, contents, err := upload.ReadTree(body, limits, gitsrc.ReadableFiles(), detectReadLimit)
	var refusal *upload.Refusal
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		writeError(w, r, errdoc.UploadTooLarge(limits.Compressed>>20))
		return
	case errors.As(err, &refusal):
		writeError(w, r, refusalProblem(refusal, limits))
		return
	case err != nil:
		writeError(w, r, err)
		return
	case len(files) == 0:
		writeError(w, r, errdoc.UploadEmpty())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		builder.Detection
		Files int `json:"files"`
	}{builder.Detect(builder.Tree{Files: files, Contents: contents}), len(files)})
}

// detectReadLimit bounds one file read for detection. The files it reads are
// manifests, which are small.
const detectReadLimit = 256 << 10

// refusalProblem says why an archive was turned down, in words that can be
// translated.
func refusalProblem(refusal *upload.Refusal, limits upload.Limits) *errdoc.Problem {
	switch refusal.Kind {
	case upload.UnsafeEntry:
		return errdoc.UploadUnsafeEntry(refusal.Entry)
	case upload.SecretsFile:
		return errdoc.UploadSecretsFile(refusal.Entry)
	case upload.TooManyFiles:
		return errdoc.UploadTooManyFiles(limits.Entries)
	case upload.UnpacksLarge:
		return errdoc.UploadUnpacksTooLarge(limits.Unpacked >> 20)
	case upload.Empty:
		return errdoc.UploadEmpty()
	}
	return errdoc.UploadNotAnArchive()
}
