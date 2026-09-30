package store

import (
	"errors"
	"testing"
	"time"
)

func scanFixture(t *testing.T) (*DB, App, App) {
	t.Helper()
	db := testDB(t)
	ctx := t.Context()
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := Environment{ProjectID: project.ID, Name: "production", Slug: "production", Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	web := App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	worker := App{EnvironmentID: env.ID, Name: "worker", Slug: "worker", Replicas: 1}
	for _, app := range []*App{&web, &worker} {
		if err := db.CreateApp(ctx, app); err != nil {
			t.Fatal(err)
		}
	}
	return db, web, worker
}

// A scan's report goes in and comes out as it was, findings included, and a
// failed one says why.
func TestAScanIsRecordedAndReadBack(t *testing.T) {
	db, web, _ := scanFixture(t)
	ctx := t.Context()

	scan := ImageScan{AppID: web.ID, Image: "registry/acme/web:1", Trigger: ScanTriggerDeploy, RequestedBy: "usr_1"}
	created, err := db.QueueImageScan(ctx, &scan)
	if err != nil || !created || scan.Status != ScanQueued {
		t.Fatalf("queued %+v (%v, %v)", scan, created, err)
	}
	// Asking again while it waits is the same scan.
	again := ImageScan{AppID: web.ID, Image: "registry/acme/web:2", Trigger: ScanTriggerManual}
	if created, err := db.QueueImageScan(ctx, &again); err != nil || created || again.ID != scan.ID {
		t.Fatalf("a second request queued %+v (%v, %v)", again, created, err)
	}
	if err := db.StartImageScan(ctx, scan.ID); err != nil {
		t.Fatal(err)
	}
	result := ScanResult{
		Digest: "sha256:5b0bcabd1ed22e9fb1310cf6c2dec7cdef19f0ad69efa1f392e94a4333501270",
		Counts: ScanCounts{Critical: 1, High: 2, Low: 3}, Fixable: 2, FixableCritical: 1,
		Findings: []ScanFinding{{ID: "CVE-2025-29927", Package: "next", Installed: "14.2.3", FixedIn: "14.2.25", Severity: "CRITICAL"}},
		Omitted:  5, ScannerVersion: "0.74.0", OS: "debian 12.7",
	}
	if err := db.FinishImageScan(ctx, scan.ID, result); err != nil {
		t.Fatal(err)
	}
	got, err := db.LatestSucceededImageScan(ctx, web.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Counts != result.Counts || got.FixableCritical != 1 || got.Omitted != 5 || got.Digest != result.Digest ||
		len(got.Findings) != 1 || got.Findings[0].FixedIn != "14.2.25" || got.FinishedAt.IsZero() || got.StartedAt.IsZero() {
		t.Errorf("read back %+v", got)
	}
	if _, err := db.SucceededImageScanSince(ctx, web.ID, "registry/acme/web:1", time.Now().Add(-time.Hour)); err != nil {
		t.Errorf("a scan a moment ago is not recent: %v", err)
	}
	if _, err := db.SucceededImageScanSince(ctx, web.ID, "registry/acme/web:2", time.Now().Add(-time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("another image's scan stood for this one: %v", err)
	}

	failed := ImageScan{AppID: web.ID, Image: "registry/acme/web:2", Trigger: ScanTriggerSchedule}
	if err := db.CreateImageScan(ctx, &failed); err != nil {
		t.Fatal(err)
	}
	if err := db.FailImageScan(ctx, failed.ID, "scan.timeout", "It took too long.", "Scan it again."); err != nil {
		t.Fatal(err)
	}
	latest, err := db.LatestImageScan(ctx, web.ID)
	if err != nil || latest.ID != failed.ID || latest.Status != ScanFailed || latest.ErrorCode != "scan.timeout" ||
		len(latest.Findings) != 0 {
		t.Errorf("the newest scan is %+v (%v)", latest, err)
	}
	// The report is still the one before it.
	if report, _ := db.LatestSucceededImageScan(ctx, web.ID); report.ID != scan.ID {
		t.Errorf("the newest report is %s", report.ID)
	}
}

// Each app keeps its newest scans and lets the rest go, and a scan queued or
// running is never what goes.
func TestOldScansAreLetGo(t *testing.T) {
	db, web, worker := scanFixture(t)
	ctx := t.Context()
	var first, announced string
	for i := range ScansKeptPerApp + 5 {
		scan := ImageScan{AppID: web.ID, Image: "registry/acme/web:1", Trigger: ScanTriggerSchedule}
		if err := db.CreateImageScan(ctx, &scan); err != nil {
			t.Fatal(err)
		}
		if err := db.FinishImageScan(ctx, scan.ID, ScanResult{}); err != nil {
			t.Fatal(err)
		}
		switch i {
		case 0:
			first = scan.ID
		case 1:
			// The newest report the team was told about is what the next
			// is compared with, so it stays however old it gets.
			if marked, err := db.MarkImageScanAnnounced(ctx, scan.ID); err != nil || !marked {
				t.Fatalf("marking a report announced: %v, %v", marked, err)
			}
			if marked, _ := db.MarkImageScanAnnounced(ctx, scan.ID); marked {
				t.Fatal("a report was announced twice")
			}
			announced = scan.ID
		}
	}
	other := ImageScan{AppID: worker.ID, Image: "redis:7", Trigger: ScanTriggerSchedule}
	if err := db.CreateImageScan(ctx, &other); err != nil {
		t.Fatal(err)
	}
	var kept, others int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_scans WHERE app_id = ?`, web.ID).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_scans WHERE app_id = ?`, worker.ID).Scan(&others); err != nil {
		t.Fatal(err)
	}
	if kept != ScansKeptPerApp+1 || others != 1 {
		t.Errorf("kept %d of web's scans and %d of worker's", kept, others)
	}
	if _, err := db.GetImageScan(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Errorf("the oldest scan is still there: %v", err)
	}
	if baseline, err := db.LatestAnnouncedImageScan(ctx, web.ID); err != nil || baseline.ID != announced {
		t.Errorf("the report the team was last told about went: %+v, %v", baseline, err)
	}
}

// What the daily rescan and the list of apps read.
func TestWhatTheRescanAndTheListRead(t *testing.T) {
	db, web, worker := scanFixture(t)
	ctx := t.Context()
	succeeded := func(app App, image string) Deployment {
		t.Helper()
		d := Deployment{AppID: app.ID, Image: image}
		if err := db.CreateDeployment(ctx, &d); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE deployments SET status = 'succeeded' WHERE id = ?`, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	succeeded(web, "registry/acme/web:1")
	current := succeeded(web, "registry/acme/web:2")
	// A newer deployment that failed leaves the one before it running.
	failed := Deployment{AppID: web.ID, Image: "registry/acme/web:3"}
	if err := db.CreateDeployment(ctx, &failed); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE deployments SET status = 'failed' WHERE id = ?`, failed.ID); err != nil {
		t.Fatal(err)
	}

	scan := ImageScan{AppID: web.ID, DeploymentID: current.ID, Image: "registry/acme/web:2", Trigger: ScanTriggerDeploy}
	if err := db.CreateImageScan(ctx, &scan); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishImageScan(ctx, scan.ID, ScanResult{Counts: ScanCounts{Critical: 2, Medium: 1}}); err != nil {
		t.Fatal(err)
	}
	pending := ImageScan{AppID: worker.ID, Image: "redis:7", Trigger: ScanTriggerManual}
	if err := db.CreateImageScan(ctx, &pending); err != nil {
		t.Fatal(err)
	}

	candidates, err := db.ListScanCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The worker has never been deployed, so it runs nothing to scan.
	if len(candidates) != 1 {
		t.Fatalf("candidates are %+v", candidates)
	}
	c := candidates[0]
	if c.AppID != web.ID || c.Image != "registry/acme/web:2" || c.DeploymentID != current.ID ||
		c.LastScanned.IsZero() || c.Pending || c.TeamID == "" || c.ProjectID == "" {
		t.Errorf("the candidate is %+v", c)
	}

	counts, err := db.LatestScanCounts(ctx, web.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 || counts[web.ID].Critical != 2 || counts[web.ID].Medium != 1 {
		t.Errorf("the list's counts are %+v", counts)
	}

	// A restart leaves nothing queued or running for ever.
	n, err := db.FailInterruptedImageScans(ctx, "scan.interrupted", "The panel stopped.", "Scan it again.")
	if err != nil || n != 1 {
		t.Fatalf("settled %d scans (%v)", n, err)
	}
	if got, _ := db.GetImageScan(ctx, pending.ID); got.Status != ScanFailed || got.ErrorCode != "scan.interrupted" {
		t.Errorf("the interrupted scan is %+v", got)
	}

	// An app deleted takes its scans with it.
	if err := db.DeleteApp(ctx, web.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetImageScan(ctx, scan.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deleted app's scan is still there: %v", err)
	}
}
