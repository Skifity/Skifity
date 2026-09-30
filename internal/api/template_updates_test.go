package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/templates"
)

// fakeBackups records the backups asked for and leaves them running, so a test
// decides how each ends.
type fakeBackups struct {
	BackupManager
	db      *store.DB
	started []store.Backup
}

func (f *fakeBackups) Run(ctx context.Context, targetType, targetID, kind string) (store.Backup, error) {
	backup := store.Backup{TargetType: targetType, TargetID: targetID, Status: "running", Kind: kind}
	if err := f.db.CreateBackup(ctx, &backup); err != nil {
		return store.Backup{}, err
	}
	f.started = append(f.started, backup)
	return backup, nil
}

// singleServiceTemplate is a catalogue template with one service and no
// databases, which is all an update needs.
func singleServiceTemplate(t *testing.T) (templates.Template, templates.Service) {
	t.Helper()
	for _, tpl := range templates.All() {
		if len(tpl.Services) == 1 && len(tpl.Databases) == 0 {
			return tpl, tpl.Services[0]
		}
	}
	t.Fatal("the catalogue has no single-service template")
	return templates.Template{}, templates.Service{}
}

func installedFrom(t *testing.T, h *harness, owner tenant, name, image, installed string) store.App {
	t.Helper()
	tpl, service := singleServiceTemplate(t)
	app := store.App{EnvironmentID: owner.env.ID, Name: name, Slug: name, SourceType: "image",
		Image: image, Replicas: 1, CPURequestM: 50, CPULimitM: 1000, MemRequestMB: 128, MemLimitMB: 512}
	if err := h.db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	if err := h.db.RecordAppTemplate(t.Context(), store.AppTemplate{
		AppID: app.ID, TemplateID: tpl.ID, Service: service.Name, InstalledImage: installed,
	}); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestAnAppFromATemplateIsOfferedItsUpdate(t *testing.T) {
	h := newHarness(t)
	deploys := &recorder{}
	h.api.deployer = &fakeDeployer{log: deploys}
	acme := h.newTenant("acme")
	_, service := singleServiceTemplate(t)
	app := installedFrom(t, h, acme, "budget", "example/app:1.0", "example/app:1.0")
	path := "/api/apps/" + app.ID + "/template"

	var view templateView
	_, body := h.do(acme, http.MethodGet, path, nil)
	_ = json.Unmarshal([]byte(body), &view)
	if !view.FromTemplate || !view.UpdateAvailable || view.Latest != service.Image || view.ChangedByHand {
		t.Fatalf("the view is %+v", view)
	}

	// Nothing to back up: it applies at once.
	status, body := h.do(acme, http.MethodPost, path+"/update", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("updating answered %d: %s", status, truncate(body, 200))
	}
	updated, _ := h.db.GetApp(t.Context(), app.ID)
	record, _ := h.db.GetAppTemplate(t.Context(), app.ID)
	if updated.Image != service.Image || record.InstalledImage != service.Image || len(deploys.all()) != 1 {
		t.Fatalf("after the update: image %s, installed %s, %d deploys", updated.Image, record.InstalledImage, len(deploys.all()))
	}
	if status, body := h.do(acme, http.MethodPost, path+"/update", map[string]any{}); status != http.StatusConflict ||
		!strings.Contains(body, "template.up_to_date") {
		t.Fatalf("updating twice answered %d: %s", status, truncate(body, 200))
	}

	// An app with no template says so rather than failing.
	plain := h.app(acme, "plain")
	_, body = h.do(acme, http.MethodGet, "/api/apps/"+plain.ID+"/template", nil)
	if !strings.Contains(body, `"from_template":false`) {
		t.Fatalf("an app with no template answered %s", body)
	}
}

// refusingDeployer refuses every deploy, as a lock taken meanwhile does.
type refusingDeployer struct{ fakeDeployer }

func (*refusingDeployer) Deploy(context.Context, DeployRequest) (store.Deployment, error) {
	return store.Deployment{}, errdoc.DeployLocked("ops@example.com", "release freeze")
}

func TestAnUpdateWhoseDeployIsRefusedChangesNothing(t *testing.T) {
	// Said up to date and configured for an image it does not run was the
	// app after this; the next unrelated deploy would have shipped it.
	h := newHarness(t)
	h.api.deployer = &refusingDeployer{fakeDeployer{log: &recorder{}}}
	acme := h.newTenant("acme")
	app := installedFrom(t, h, acme, "budget", "example/app:1.0", "example/app:1.0")
	path := "/api/apps/" + app.ID + "/template/update"
	if status, body := h.do(acme, http.MethodPost, path, map[string]any{}); status < 400 || !strings.Contains(body, "deploy.locked") {
		t.Fatalf("a refused deploy answered %d: %s", status, truncate(body, 200))
	}
	after, _ := h.db.GetApp(t.Context(), app.ID)
	record, _ := h.db.GetAppTemplate(t.Context(), app.ID)
	if after.Image != "example/app:1.0" || record.InstalledImage != "example/app:1.0" {
		t.Fatalf("after a refusal the app runs %s and is recorded at %s", after.Image, record.InstalledImage)
	}

	// And it can be tried again once the lock is gone.
	h.api.deployer = &fakeDeployer{log: &recorder{}}
	if status, body := h.do(acme, http.MethodPost, path, map[string]any{}); status != http.StatusOK {
		t.Fatalf("trying again answered %d: %s", status, truncate(body, 200))
	}
}

// An image somebody chose is not replaced over their head.
func TestAnImageChangedByHandIsKeptUnlessAskedTo(t *testing.T) {
	h := newHarness(t)
	h.api.deployer = &fakeDeployer{log: &recorder{}}
	acme := h.newTenant("acme")
	app := installedFrom(t, h, acme, "budget", "example/app:custom", "example/app:1.0")
	path := "/api/apps/" + app.ID + "/template/update"

	if status, body := h.do(acme, http.MethodPost, path, map[string]any{}); status != http.StatusConflict ||
		!strings.Contains(body, "template.changed_by_hand") {
		t.Fatalf("answered %d: %s", status, truncate(body, 200))
	}
	if status, _ := h.do(acme, http.MethodPost, path, map[string]any{"force": true}); status != http.StatusOK {
		t.Fatalf("forcing answered %d", status)
	}
}

// The disks and the databases are backed up first, and the update waits for
// them; a backup that fails stops it with nothing changed.
func TestAnUpdateBacksUpFirstAndStopsIfABackupFails(t *testing.T) {
	previous := templateBackupPoll
	templateBackupPoll = 5 * time.Millisecond
	t.Cleanup(func() { templateBackupPoll = previous })

	h := newHarness(t)
	deploys := &recorder{}
	h.api.deployer = &fakeDeployer{log: deploys}
	backups := &fakeBackups{db: h.db}
	h.api.backups = backups
	acme := h.newTenant("acme")
	_, service := singleServiceTemplate(t)

	run := func(outcome string) store.App {
		app := installedFrom(t, h, acme, "budget-"+outcome, "example/app:1.0", "example/app:1.0")
		volume := store.Volume{AppID: app.ID, Name: "data", MountPath: "/data", SizeGB: 1}
		if err := h.db.CreateVolume(t.Context(), &volume); err != nil {
			t.Fatal(err)
		}
		status, body := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/template/update", map[string]any{})
		if status != http.StatusAccepted {
			t.Fatalf("answered %d: %s", status, truncate(body, 200))
		}
		backup := backups.started[len(backups.started)-1]
		if backup.TargetType != "volume" || backup.TargetID != volume.ID {
			t.Fatalf("backed up %+v", backup)
		}
		if record, _ := h.db.GetAppTemplate(t.Context(), app.ID); record.UpdateStatus != store.TemplateUpdateBackingUp {
			t.Fatalf("while backing up the status is %q", record.UpdateStatus)
		}
		if still, _ := h.db.GetApp(t.Context(), app.ID); still.Image != "example/app:1.0" {
			t.Fatal("the image changed before the backup finished")
		}
		message := ""
		if outcome == "failed" {
			message = "the bucket refused it"
		}
		if err := h.db.FinishBackup(t.Context(), backup.ID, outcome, "key", 10, message); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			record, _ := h.db.GetAppTemplate(t.Context(), app.ID)
			if record.UpdateStatus != store.TemplateUpdateBackingUp {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		updated, _ := h.db.GetApp(t.Context(), app.ID)
		return updated
	}

	if app := run("succeeded"); app.Image != service.Image {
		t.Fatalf("after the backup succeeded the image is %s", app.Image)
	}
	failed := run("failed")
	if failed.Image != "example/app:1.0" {
		t.Fatalf("after the backup failed the image is %s", failed.Image)
	}
	record, _ := h.db.GetAppTemplate(t.Context(), failed.ID)
	if record.UpdateStatus != store.TemplateUpdateFailed || !strings.Contains(record.UpdateError, "the bucket refused it") {
		t.Fatalf("the failure was recorded as %+v", record)
	}
	if len(deploys.all()) != 1 {
		t.Fatalf("%d deploys, want only the one whose backup succeeded", len(deploys.all()))
	}
}

// Installing remembers where each app came from, and a service the template
// keeps private is internal: a search index or a worker with a port used to
// get a public address like the app in front of it.
func TestAnInstallRemembersItsTemplateAndKeepsPrivateServicesInternal(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	var tpl templates.Template
	for _, candidate := range templates.All() {
		public, private := false, false
		for _, service := range candidate.Services {
			if service.Public {
				public = true
			} else {
				private = true
			}
		}
		if public && private && len(candidate.Databases) == 0 {
			tpl = candidate
			break
		}
	}
	if tpl.ID == "" {
		t.Skip("the catalogue has no template with a public and a private service and no databases")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/templates/"+tpl.ID+"/install", nil).WithContext(t.Context())
	result, err := h.api.installTemplate(request, tpl, acme.env, acme.user, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]store.App{}
	for _, app := range result.Apps {
		byName[app.Name] = app
	}
	for _, service := range tpl.Services {
		app := byName[service.Name]
		if app.Internal == service.Public {
			t.Errorf("%s: public %v in the template, internal %v here", service.Name, service.Public, app.Internal)
		}
		record, err := h.db.GetAppTemplate(t.Context(), app.ID)
		if err != nil || record.TemplateID != tpl.ID || record.Service != service.Name || record.InstalledImage != service.Image {
			t.Errorf("%s: recorded %+v, %v", service.Name, record, err)
		}
	}
}
