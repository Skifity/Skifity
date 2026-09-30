package backup

import (
	"context"
	"errors"
	"testing"

	"skifity/internal/notify"
	"skifity/internal/store"
)

// A notification channel can be limited to some projects, and it is left out
// only when the message says which project it is about. A database's backup
// belongs to the database's project, a volume's to its app's; the panel's own
// copy belongs to no project, and so reaches every channel.
func TestABackupsNoticeNamesItsProject(t *testing.T) {
	m, db, _, notifier, teamID := panelHarness(t)
	ctx := context.Background()

	shop := catchUpDatabase(t, db, teamID, "shop")
	_, shopProject, err := db.ProjectOfDatabase(ctx, shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.finished(ctx, store.Backup{ID: "bkp_1", TargetType: "database", TargetID: shop.ID}, "shop", nil)
	m.finished(ctx, store.Backup{ID: "bkp_2", TargetType: "database", TargetID: shop.ID}, "shop", errors.New("disk full"))

	// A volume, in another project, through its app.
	blog := catchUpDatabase(t, db, teamID, "blog")
	blogDatabase, err := db.GetDatabase(ctx, blog.ID)
	if err != nil {
		t.Fatal(err)
	}
	app := store.App{EnvironmentID: blogDatabase.EnvironmentID, Name: "blog", Slug: "blog", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}
	volume := store.Volume{AppID: app.ID, Name: "uploads", MountPath: "/uploads", SizeGB: 1}
	if err := db.CreateVolume(ctx, &volume); err != nil {
		t.Fatal(err)
	}
	_, blogProject, err := db.ProjectOfApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.finished(ctx, store.Backup{ID: "bkp_3", TargetType: "volume", TargetID: volume.ID}, "blog / uploads", errors.New("gone"))

	// The panel's own copy failing.
	m.notifyAdmins(ctx, notify.Message{Title: "This panel could not back itself up"})

	want := []recordedNotice{
		{teamID, notify.EventBackupSucceeded, "shop was backed up", shopProject},
		{teamID, notify.EventBackupFailed, "Backing up shop failed", shopProject},
		{teamID, notify.EventBackupFailed, "Backing up blog / uploads failed", blogProject},
		{teamID, notify.EventBackupFailed, "This panel could not back itself up", ""},
	}
	if shopProject == "" || blogProject == "" || shopProject == blogProject {
		t.Fatalf("the projects are %q and %q", shopProject, blogProject)
	}
	if len(notifier.notices) != len(want) {
		t.Fatalf("notices = %+v, want %+v", notifier.notices, want)
	}
	for i, got := range notifier.notices {
		if got != want[i] {
			t.Errorf("notice %d is %+v, want %+v", i, got, want[i])
		}
	}
}
