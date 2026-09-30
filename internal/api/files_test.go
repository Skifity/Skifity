package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// An app's files through the API: who may read and change them, what is sent
// back, and what is refused.

func TestAnAppsFilesAreSavedSealedAndSecretOnesAreNotSentBack(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "shop")
	path := "/api/apps/" + app.ID + "/files"

	nginx := "server { listen 8080; }\n"
	if status, body := h.do(acme, http.MethodPut, path, map[string]any{"path": "/etc/nginx/conf.d/default.conf", "content": nginx}); status != http.StatusOK {
		t.Fatalf("saving a file answered %d: %s", status, body)
	}
	secret := "db_password: hunter2-not-real\n"
	if status, body := h.do(acme, http.MethodPut, path, map[string]any{"path": "/app/secrets.yml", "content": secret, "is_secret": true}); status != http.StatusOK {
		t.Fatalf("saving a secret file answered %d: %s", status, body)
	}

	status, body := h.do(acme, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("listing answered %d: %s", status, body)
	}
	if !strings.Contains(body, "listen 8080") {
		t.Errorf("an ordinary file's content is not sent back, so it cannot be edited: %s", body)
	}
	if strings.Contains(body, "hunter2") {
		t.Errorf("a secret file's content was sent back: %s", body)
	}

	// Sealed at rest, under the app and the path.
	rows, err := h.db.ListFiles(t.Context(), app.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("stored %+v (%v)", rows, err)
	}
	for _, row := range rows {
		if strings.Contains(row.Sealed, "hunter2") || strings.Contains(row.Sealed, "listen") {
			t.Errorf("%s is stored in the clear", row.Path)
		}
		if row.Size == 0 {
			t.Errorf("%s has no size", row.Path)
		}
	}

	// Saving the same path again replaces the file rather than adding one,
	// and a secret stays secret when the caller says nothing.
	if status, body := h.do(acme, http.MethodPut, path, map[string]any{"path": "/app/secrets.yml", "content": "db_password: other\n"}); status != http.StatusOK {
		t.Fatalf("replacing answered %d: %s", status, body)
	}
	rows, _ = h.db.ListFiles(t.Context(), app.ID)
	if len(rows) != 2 {
		t.Fatalf("replacing a file added one: %+v", rows)
	}
	for _, row := range rows {
		if row.Path == "/app/secrets.yml" && !row.IsSecret {
			t.Error("replacing a secret file without saying made it an ordinary one, and its content is now sent back")
		}
	}

	var list struct {
		Items []store.AppFile `json:"items"`
	}
	_, body = h.do(acme, http.MethodGet, path, nil)
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list.Items) != 2 {
		t.Fatalf("list = %s (%v)", body, err)
	}
	if status, body := h.do(acme, http.MethodDelete, path+"/"+list.Items[0].ID, nil); status != http.StatusOK {
		t.Fatalf("removing answered %d: %s", status, body)
	}
	if rows, _ := h.db.ListFiles(t.Context(), app.ID); len(rows) != 1 {
		t.Errorf("after removing one, %d are left", len(rows))
	}
}

func TestAFileIsOnlyItsTeamsToReadAndItsMembersToChange(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	app := h.app(acme, "shop")
	path := "/api/apps/" + app.ID + "/files"
	if status, body := h.do(acme, http.MethodPut, path, map[string]any{"path": "/app/config.yml", "content": "a: 1\n"}); status != http.StatusOK {
		t.Fatalf("saving answered %d: %s", status, body)
	}

	if status, body := h.do(globex, http.MethodGet, path, nil); status != http.StatusNotFound {
		t.Errorf("another team listed the files: %d %s", status, body)
	}
	if status, _ := h.do(globex, http.MethodPut, path, map[string]any{"path": "/app/x", "content": "x"}); status != http.StatusNotFound {
		t.Errorf("another team saved a file: %d", status)
	}

	viewer := h.newMember(acme, "auditor", store.RoleViewer)
	if status, body := h.do(viewer, http.MethodGet, path, nil); status != http.StatusOK || !strings.Contains(body, "a: 1") {
		t.Errorf("a viewer cannot read an ordinary file: %d %s", status, body)
	}
	if status, _ := h.do(viewer, http.MethodPut, path, map[string]any{"path": "/app/x", "content": "x"}); status != http.StatusForbidden {
		t.Errorf("a viewer saved a file: %d", status)
	}
	rows, _ := h.db.ListFiles(t.Context(), app.ID)
	if status, _ := h.do(viewer, http.MethodDelete, path+"/"+rows[0].ID, nil); status != http.StatusForbidden {
		t.Errorf("a viewer removed a file: %d", status)
	}
}

func TestAFileThatCannotWorkIsRefused(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "shop")
	path := "/api/apps/" + app.ID + "/files"
	if err := h.db.CreateVolume(t.Context(), &store.Volume{AppID: app.ID, Name: "data", MountPath: "/data", SizeGB: 1}); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]map[string]any{
		"relative":       {"path": "etc/app.conf", "content": "x"},
		"kubelet's":      {"path": "/etc/resolv.conf", "content": "nameserver 1.1.1.1"},
		"a volume's":     {"path": "/data", "content": "x"},
		"not plain":      {"path": "/etc/../etc/app.conf", "content": "x"},
		"over 256 KiB":   {"path": "/app/big.txt", "content": strings.Repeat("x", 256<<10+1)},
		"under the proc": {"path": "/proc/x", "content": "x"},
	} {
		if status, answer := h.do(acme, http.MethodPut, path, body); status != http.StatusBadRequest {
			t.Errorf("a file %s answered %d: %.200s", name, status, answer)
		}
	}
	// Inside a volume is where a configuration file beside its data goes.
	if status, body := h.do(acme, http.MethodPut, path, map[string]any{"path": "/data/settings.json", "content": "{}"}); status != http.StatusOK {
		t.Errorf("a file inside a volume answered %d: %s", status, body)
	}

	// Together they fit in one Secret, or they are refused.
	for i := range 3 {
		status, answer := h.do(acme, http.MethodPut, path, map[string]any{
			"path": "/app/part" + string(rune('a'+i)), "content": strings.Repeat("y", 250<<10),
		})
		if status != http.StatusOK {
			t.Fatalf("file %d of three answered %d: %.200s", i, status, answer)
		}
	}
	status, answer := h.do(acme, http.MethodPut, path, map[string]any{"path": "/app/partd", "content": strings.Repeat("y", 250<<10)})
	if status != http.StatusBadRequest || !strings.Contains(answer, "file.too_large") {
		t.Errorf("a fourth 250 KiB file answered %d: %.300s", status, answer)
	}
}

// Replacing a script's content without saying whether it is executable keeps
// it executable: `skifity files set` without --executable used to stop an
// entrypoint script from running.
func TestReplacingAFileKeepsItExecutable(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "shop")
	path := "/api/apps/" + app.ID + "/files"
	script := "/docker-entrypoint.d/10-init.sh"

	save := func(body map[string]any) bool {
		t.Helper()
		if status, answer := h.do(acme, http.MethodPut, path, body); status != http.StatusOK {
			t.Fatalf("saving answered %d: %s", status, answer)
		}
		rows, _ := h.db.ListFiles(t.Context(), app.ID)
		return len(rows) == 1 && rows[0].Executable
	}
	if !save(map[string]any{"path": script, "content": "#!/bin/sh\n", "executable": true}) {
		t.Fatal("the script was not saved executable")
	}
	if !save(map[string]any{"path": script, "content": "#!/bin/sh\necho hi\n"}) {
		t.Error("replacing the content without saying made the script not executable")
	}
	if save(map[string]any{"path": script, "content": "#!/bin/sh\n", "executable": false}) {
		t.Error("saying it is not executable was ignored")
	}
}
