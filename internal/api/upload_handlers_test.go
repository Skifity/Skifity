package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/events"
	"skifity/internal/store"
	"skifity/internal/upload"
)

// Deploying a folder with no repository behind it: the app is created with
// nothing to build yet, the code is sent, and only then is there a deploy.

func (h *harness) withUploads() *upload.Store {
	h.t.Helper()
	uploads := &upload.Store{Dir: h.t.TempDir()}
	h.server.Config.Handler = New(Options{
		DB: h.db, Keyring: h.keyring, Auth: h.auth,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler),
		Uploads: uploads,
	})
	return uploads
}

// sendSource puts an archive to an app's source, as `skifity up` does.
func (h *harness) sendSource(as tenant, appID string, archive []byte) (int, string) {
	h.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		h.server.URL+"/api/apps/"+appID+"/source", bytes.NewReader(archive))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("Authorization", "Bearer "+as.token)
	resp, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func folder(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestAFolderBecomesAnAppWithoutARepository(t *testing.T) {
	h := newHarness(t)
	uploads := h.withUploads()
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "notes", "source_type": "upload",
		// Whatever a confused client sends alongside, an upload app has none
		// of it: there is no repository to have a branch of.
		"repo_url": "https://example.test/x.git", "branch": "main",
	})
	if status != http.StatusCreated {
		t.Fatalf("an app with no repository was refused: %d %s", status, body)
	}
	var created struct {
		App store.App `json:"app"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	if created.App.SourceType != "upload" || created.App.RepoURL != "" || created.App.Branch != "" {
		t.Fatalf("the app is %+v", created.App)
	}

	status, body = h.sendSource(acme, created.App.ID, folder(t, map[string]string{"index.html": "<h1>hi</h1>"}))
	if status != http.StatusCreated {
		t.Fatalf("the code was refused: %d %s", status, body)
	}
	var summary upload.Summary
	if err := json.Unmarshal([]byte(body), &summary); err != nil {
		t.Fatal(err)
	}
	if !uploads.Has(created.App.ID, summary.SHA256) || summary.Files != 1 {
		t.Fatalf("the upload was not kept: %+v", summary)
	}

	// Deleting the app deletes its code with it.
	if status, body := h.do(acme, http.MethodDelete, "/api/apps/"+created.App.ID, nil); status != http.StatusOK {
		t.Fatalf("delete: %d %s", status, body)
	}
	if uploads.Has(created.App.ID, summary.SHA256) {
		t.Fatal("the app's code outlived the app")
	}
}

func TestUploadedCodeIsRefusedWhereItDoesNotBelong(t *testing.T) {
	h := newHarness(t)
	h.withUploads()
	acme := h.newTenant("acme")
	other := h.newTenant("other")

	gitApp := h.app(acme, "web")
	status, body := h.sendSource(acme, gitApp.ID, folder(t, map[string]string{"a": "1"}))
	if status != http.StatusConflict || !strings.Contains(body, "upload.wrong_source") {
		t.Fatalf("code sent to an app that builds from a repository gave %d %s", status, body)
	}

	theirs := store.App{EnvironmentID: other.env.ID, Name: "notes", Slug: "notes", SourceType: "upload", Replicas: 1}
	if err := h.db.CreateApp(t.Context(), &theirs); err != nil {
		t.Fatal(err)
	}
	if status, body := h.sendSource(acme, theirs.ID, folder(t, map[string]string{"a": "1"})); status != http.StatusNotFound {
		t.Fatalf("one team sent code to another team's app: %d %s", status, body)
	}

	mine := store.App{EnvironmentID: acme.env.ID, Name: "notes", Slug: "notes", SourceType: "upload", Replicas: 1}
	if err := h.db.CreateApp(t.Context(), &mine); err != nil {
		t.Fatal(err)
	}
	status, body = h.sendSource(acme, mine.ID, folder(t, map[string]string{".env": "SECRET=1"}))
	if status != http.StatusBadRequest || !strings.Contains(body, "upload.secrets_file") {
		t.Fatalf("a .env file was accepted: %d %s", status, body)
	}
	status, body = h.sendSource(acme, mine.ID, []byte("this is not an archive"))
	if status != http.StatusBadRequest || !strings.Contains(body, "upload.not_an_archive") {
		t.Fatalf("something that is not an archive gave %d %s", status, body)
	}
}

// The form shows what a folder is before the app exists, from the archive the
// browser packed, read the same way a repository is.
func TestAFolderIsDetectedBeforeTheAppExists(t *testing.T) {
	h := newHarness(t)
	h.withUploads()
	acme := h.newTenant("acme")

	post := func(archive []byte) (int, string) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			h.server.URL+"/api/teams/"+acme.team.ID+"/detect-upload", bytes.NewReader(archive))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+acme.token)
		resp, err := h.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	status, body := post(folder(t, map[string]string{
		"package.json": `{"dependencies":{"next":"15","pg":"8"}}`,
		".env.example": "DATABASE_URL=\nSTRIPE_KEY=\n",
		"app/page.tsx": "export default function Page() {}",
	}))
	if status != http.StatusOK {
		t.Fatalf("detect: %d %s", status, body)
	}
	var found struct {
		Framework string `json:"framework"`
		Files     int    `json:"files"`
		Needs     []struct {
			Kind   string `json:"kind"`
			Engine string `json:"engine"`
		} `json:"needs"`
	}
	if err := json.Unmarshal([]byte(body), &found); err != nil {
		t.Fatal(err)
	}
	if found.Framework != "Next.js" || found.Files != 3 {
		t.Fatalf("the folder was read as %+v", found)
	}
	postgres := false
	for _, need := range found.Needs {
		postgres = postgres || (need.Kind == "database" && need.Engine == "postgres")
	}
	if !postgres {
		t.Fatalf("the folder's database was not found: %s", body)
	}

	if status, body := post(folder(t, map[string]string{".env": "X=1"})); status != http.StatusBadRequest ||
		!strings.Contains(body, "upload.secrets_file") {
		t.Fatalf("a folder with a .env was read rather than refused: %d %s", status, body)
	}
}
