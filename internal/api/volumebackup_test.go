package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// A volume backup you cannot restore is a file somebody is paying to store.
//
// The job that puts one back has existed since volume backups were added, and
// for as long as it existed nothing called it: there was no route, no handler
// and no button. Same for the schedule — the scheduler reads a policy's target
// type and a volume is one of them, and there was no way to create the policy.
// These check the route is there, refuses what it should, and belongs to the
// team that owns the volume.

// recordingBackups stands in for the real manager: what these tests are about
// is who may ask and what is refused, and both are decided before a cluster is
// reached.
type recordingBackups struct {
	restoredVolume string
	overwrite      bool
}

func (r *recordingBackups) Run(context.Context, string, string, string) (store.Backup, error) {
	return store.Backup{}, nil
}

func (r *recordingBackups) Restore(context.Context, string, bool) (store.Operation, error) {
	return store.Operation{}, nil
}

func (r *recordingBackups) RestoreVolume(_ context.Context, backupID string, overwrite bool) (store.Operation, error) {
	r.restoredVolume, r.overwrite = backupID, overwrite
	return store.Operation{ID: "op_test", Kind: "volume.restore"}, nil
}

func (r *recordingBackups) Verify(context.Context) error { return nil }

// volumeWithBackup gives a tenant an app, a volume on it, and one finished
// backup of that volume.
func (h *harness) volumeWithBackup(t *testing.T, owner tenant, name string) (store.Volume, store.Backup) {
	t.Helper()
	app := h.app(owner, name)
	volume := store.Volume{AppID: app.ID, Name: "uploads", MountPath: "/data", SizeGB: 1}
	if err := h.db.CreateVolume(t.Context(), &volume); err != nil {
		t.Fatalf("create volume: %v", err)
	}
	backup := store.Backup{TargetType: "volume", TargetID: volume.ID, Status: "succeeded", Kind: "manual"}
	if err := h.db.CreateBackup(t.Context(), &backup); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	return volume, backup
}

func TestAVolumeBackupCanBeRestored(t *testing.T) {
	h := newHarness(t)
	recorder := &recordingBackups{}
	h.api.backups = recorder

	owner := h.newTenant("restorer")
	volume, backup := h.volumeWithBackup(t, owner, "uploads-app")
	app, err := h.db.GetApp(t.Context(), volume.AppID)
	if err != nil {
		t.Fatalf("read app: %v", err)
	}

	path := "/api/apps/" + app.ID + "/volumes/" + volume.ID + "/restore/" + backup.ID
	status, body := h.do(owner, http.MethodPost, path+"?overwrite=true", nil)
	if status != http.StatusAccepted {
		t.Fatalf("restoring a volume backup answered %d: %s", status, body)
	}
	if recorder.restoredVolume != backup.ID {
		t.Fatalf("the manager was asked to restore %q, want %q", recorder.restoredVolume, backup.ID)
	}
	// Replacing what is on a disk is not something to do by default.
	if !recorder.overwrite {
		t.Error("the explicit confirmation did not reach the manager")
	}
}

// A backup id belonging to another volume must not be restorable here, the way
// a database's backups already checked.
func TestAVolumeCannotBeRestoredFromAnotherVolumesBackup(t *testing.T) {
	h := newHarness(t)
	h.api.backups = &recordingBackups{}

	owner := h.newTenant("mixer")
	mine, _ := h.volumeWithBackup(t, owner, "mine-app")
	_, theirs := h.volumeWithBackup(t, owner, "other-app")

	app, err := h.db.GetApp(t.Context(), mine.AppID)
	if err != nil {
		t.Fatalf("read app: %v", err)
	}
	path := "/api/apps/" + app.ID + "/volumes/" + mine.ID + "/restore/" + theirs.ID + "?overwrite=true"
	if status, body := h.do(owner, http.MethodPost, path, nil); status != http.StatusNotFound {
		t.Fatalf("restoring another volume's backup answered %d, want 404: %s", status, body)
	}
}

// And a volume in another team is not reachable by guessing its id.
func TestAnotherTeamCannotRestoreYourVolume(t *testing.T) {
	h := newHarness(t)
	h.api.backups = &recordingBackups{}

	owner := h.newTenant("owner")
	stranger := h.newTenant("stranger")
	volume, backup := h.volumeWithBackup(t, owner, "uploads-app")
	app, err := h.db.GetApp(t.Context(), volume.AppID)
	if err != nil {
		t.Fatalf("read app: %v", err)
	}

	path := "/api/apps/" + app.ID + "/volumes/" + volume.ID + "/restore/" + backup.ID + "?overwrite=true"
	if status, _ := h.do(stranger, http.MethodPost, path, nil); status != http.StatusNotFound {
		t.Fatalf("another team restoring this volume answered %d, want 404", status)
	}
}

// TestAVolumeCanBeBackedUpOnASchedule: the engine could always run one, and
// there was no way to ask for it.
func TestAVolumeCanBeBackedUpOnASchedule(t *testing.T) {
	h := newHarness(t)
	h.api.backups = &recordingBackups{}

	owner := h.newTenant("scheduler")
	volume, _ := h.volumeWithBackup(t, owner, "sched-app")
	app, err := h.db.GetApp(t.Context(), volume.AppID)
	if err != nil {
		t.Fatalf("read app: %v", err)
	}
	path := "/api/apps/" + app.ID + "/volumes/" + volume.ID + "/backup-policy"

	// No policy yet is a normal state, not an error.
	status, body := h.do(owner, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("reading a volume's schedule answered %d: %s", status, body)
	}
	if !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("a volume with no schedule should read as off: %s", body)
	}

	status, body = h.do(owner, http.MethodPut, path, map[string]any{
		"enabled": true, "schedule": "30 2 * * *", "retention": 14,
	})
	if status != http.StatusOK {
		t.Fatalf("setting a volume's schedule answered %d: %s", status, body)
	}

	// And it has to be a policy the scheduler will actually find, which is the
	// whole point: it reads every enabled policy, whatever it targets.
	policies, err := h.db.ListEnabledBackupPolicies(t.Context())
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	var found bool
	for _, policy := range policies {
		if policy.TargetType == "volume" && policy.TargetID == volume.ID {
			found = true
			if policy.Schedule != "30 2 * * *" || policy.Retention != 14 {
				t.Errorf("the schedule was stored as %q / %d", policy.Schedule, policy.Retention)
			}
		}
	}
	if !found {
		t.Fatal("the scheduler will never see this volume's schedule")
	}
}

// A schedule nothing can parse is stored, never matches, and silently backs up
// nothing. The database side already refused one; the volume side has to too.
func TestAVolumeScheduleNothingCanParseIsRefused(t *testing.T) {
	h := newHarness(t)
	h.api.backups = &recordingBackups{}

	owner := h.newTenant("badcron")
	volume, _ := h.volumeWithBackup(t, owner, "sched-app")
	app, err := h.db.GetApp(t.Context(), volume.AppID)
	if err != nil {
		t.Fatalf("read app: %v", err)
	}
	path := "/api/apps/" + app.ID + "/volumes/" + volume.ID + "/backup-policy"

	status, body := h.do(owner, http.MethodPut, path, map[string]any{
		"enabled": true, "schedule": "every night please", "retention": 7,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("an unparseable schedule answered %d, want 400: %s", status, body)
	}
}

// TestCreatingAnAppAlwaysAnswersWithTheAppInside.
//
// This endpoint used to answer two different shapes — the app on its own, or
// an object holding the app when a deploy was started — and three things
// decode it: the panel, the CLI and the MCP server. The panel and the MCP
// server handled both. The CLI decoded straight into an App, so the day the
// shape changed it would have written a project file naming no app at all,
// silently, and `skifity deploy` in that directory would have had nothing to
// deploy.
//
// One shape, always, with the app under "app".
func TestCreatingAnAppAlwaysAnswersWithTheAppInside(t *testing.T) {
	h := newHarness(t)
	owner := h.newTenant("shapes")

	for _, deploy := range []bool{false, true} {
		name := "no-deploy"
		if deploy {
			name = "with-deploy"
		}
		t.Run(name, func(t *testing.T) {
			status, body := h.do(owner, http.MethodPost,
				"/api/environments/"+owner.env.ID+"/apps", map[string]any{
					"name": "shape-" + name, "source_type": "image",
					"image": "nginx:1", "port": 80, "deploy": deploy,
				})
			if status != http.StatusCreated {
				t.Fatalf("creating an app answered %d: %s", status, body)
			}
			var answer struct {
				App struct {
					ID string `json:"id"`
				} `json:"app"`
			}
			if err := json.Unmarshal([]byte(body), &answer); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if answer.App.ID == "" {
				t.Fatalf(`the reply has no app under "app", so the CLI would write a project `+
					"file naming nothing: %s", body)
			}
		})
	}
}
