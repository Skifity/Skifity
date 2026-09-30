package api

import (
	"net/http"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// An app's files: configuration its containers read at a path, mounted
// read-only. See internal/kube/files.go for how they reach the pod.

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := s.db.ListFiles(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]store.AppFile, 0, len(rows))
	for _, row := range rows {
		f := row.AppFile
		// As with a variable: a secret file is never shown again once saved.
		if !f.IsSecret {
			if content, err := s.keyring.Open(row.Sealed, store.FileContext(app.ID, f.Path)); err == nil {
				f.Content = string(content)
			}
		}
		out = append(out, f)
	}
	writeList(w, out)
}

type setFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// IsSecret is nil when the caller said nothing: keep what the file was,
	// and otherwise decide the way a variable is decided. See secretness.
	IsSecret   *bool `json:"is_secret,omitempty"`
	Executable bool  `json:"executable"`
}

func (s *Server) handleSetFile(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setFileRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if err := kube.ValidateFilePath(req.Path); err != nil {
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}
	// JSON carries text, and a file that is not text is data: it belongs in
	// the image or a volume.
	if !utf8.ValidString(req.Content) {
		writeError(w, r, errdoc.BadRequest("A file's content is text; put a binary file in the image or a volume."))
		return
	}
	if len(req.Content) > kube.MaxFileBytes {
		writeError(w, r, errdoc.FilesTooLarge(kube.MaxFileBytes>>10))
		return
	}

	existing, err := s.db.ListFiles(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, wasSecret, replacing := len(req.Content), false, false
	for _, f := range existing {
		if f.Path == req.Path {
			wasSecret, replacing = f.IsSecret, true
			continue
		}
		total += f.Size
	}
	if !replacing && len(existing) >= kube.MaxFiles {
		writeError(w, r, errdoc.TooManyFiles(kube.MaxFiles))
		return
	}
	if total > kube.MaxAllFilesBytes {
		writeError(w, r, errdoc.FilesTooLarge(kube.MaxAllFilesBytes>>10))
		return
	}
	// A file where a volume is mounted would replace the volume's directory.
	volumes, err := s.db.ListVolumes(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, v := range volumes {
		if v.MountPath == req.Path {
			writeError(w, r, errdoc.BadRequest("The volume "+v.Name+" is mounted at "+req.Path+
				", so a file cannot be there too. Put the file inside it, or somewhere else."))
			return
		}
	}

	sealed, err := s.keyring.Seal([]byte(req.Content), store.FileContext(app.ID, req.Path))
	if err != nil {
		writeError(w, r, err)
		return
	}
	file := store.AppFile{
		AppID: app.ID, Path: req.Path, Size: len(req.Content), Executable: req.Executable,
		IsSecret: secretness(req.IsSecret, wasSecret, req.Path, req.Content),
	}
	if err := s.db.SetFile(r.Context(), &file, sealed); err != nil {
		writeError(w, r, err)
		return
	}

	// A file is read when the container starts, so a change rolls the app
	// out — and never rebuilds it: what a container reads at runtime is not
	// what goes into the image (ADR-0007).
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not apply a file change", "app", app.ID, "error", err)
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	// The path, never the content.
	s.audit(r, teamID, "file.saved", "app", app.ID, req.Path)
	writeJSON(w, http.StatusOK, map[string]any{"file": file})
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	id := chi.URLParam(r, "fileID")
	var path string
	rows, err := s.db.ListFiles(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, row := range rows {
		if row.ID == id {
			path = row.Path
		}
	}
	if err := s.db.DeleteFile(r.Context(), app.ID, id); err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not apply a file's removal", "app", app.ID, "error", err)
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "file.deleted", "app", app.ID, path)
	writeOK(w)
}
