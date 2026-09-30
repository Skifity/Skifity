package deploy

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/notify"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// fakeScanner stands in for the Trivy Job: it answers with whatever the test
// set, and counts what it was asked to scan.
type fakeScanner struct {
	mu     sync.Mutex
	images []string
	result store.ScanResult
	err    error
}

func (f *fakeScanner) scan(_ context.Context, scan store.ImageScan) (store.ScanResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images = append(f.images, scan.Image)
	return f.result, f.err
}

func (f *fakeScanner) set(result store.ScanResult, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result, f.err = result, err
}

func (f *fakeScanner) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.images)
}

// recordingNotifier keeps every notification sent.
type recordingNotifier struct {
	mu   sync.Mutex
	sent []sentNotification
}

type sentNotification struct {
	team, event string
	msg         notify.Message
}

func (r *recordingNotifier) Notify(_ context.Context, teamID, event string, msg notify.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, sentNotification{teamID, event, msg})
}

func (r *recordingNotifier) all() []sentNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sentNotification(nil), r.sent...)
}

func critical(id, pkg, fixed string) store.ScanFinding {
	return store.ScanFinding{ID: id, Package: pkg, Installed: "1.0", FixedIn: fixed, Severity: "CRITICAL"}
}

// resultOf is a scan result with these findings, counted the way a real one is.
func resultOf(findings ...store.ScanFinding) store.ScanResult {
	result := store.ScanResult{Findings: findings, ScannerVersion: "0.74.0"}
	for _, f := range findings {
		switch f.Severity {
		case "CRITICAL":
			result.Counts.Critical++
		case "HIGH":
			result.Counts.High++
		default:
			result.Counts.Low++
		}
		if f.FixedIn != "" {
			result.Fixable++
			if f.Severity == "CRITICAL" {
				result.FixableCritical++
			}
		}
	}
	return result
}

func scanningDeployer(t *testing.T) (*Deployer, *store.DB, store.App, *fakeScanner) {
	t.Helper()
	d, db, app, _ := testDeployer(t)
	scanner := &fakeScanner{}
	d.scanJob = scanner.scan
	return d, db, app, scanner
}

func setSetting(t *testing.T, db *store.DB, key, value string) {
	t.Helper()
	if err := db.SetSetting(t.Context(), key, value, false, "test"); err != nil {
		t.Fatalf("SetSetting %s: %v", key, err)
	}
}

func newDeployment(t *testing.T, db *store.DB, d store.Deployment) store.Deployment {
	t.Helper()
	if err := db.CreateDeployment(t.Context(), &d); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return d
}

// waitForScans waits until an app has no scan queued or running, so a test
// neither reads a result early nor closes the database under a scan.
func waitForScans(t *testing.T, db *store.DB, appID string) store.ImageScan {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		scan, err := db.LatestImageScan(t.Context(), appID)
		if err == nil && scan.Finished() {
			return scan
		}
		if time.Now().After(deadline) {
			t.Fatalf("the scan of %s did not finish: %+v, %v", appID, scan, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func logText(t *testing.T, db *store.DB, deploymentID string) string {
	t.Helper()
	lines, err := db.ListBuildLogs(t.Context(), deploymentID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line.Line)
		b.WriteByte('\n')
	}
	return b.String()
}

// With deploys stopped for fixable criticals, a deploy whose image has one is
// stopped with the packages named; one that accepted them goes through, and
// says so in its log; a rollback is never stopped.
func TestTheGateStopsAFixableCriticalUnlessItIsAccepted(t *testing.T) {
	d, db, app, scanner := scanningDeployer(t)
	ctx := t.Context()
	setSetting(t, db, settings.KeyScanBlockCritical, "true")
	scanner.set(resultOf(
		critical("CVE-2025-29927", "next", "14.2.25, 15.2.3"),
		critical("CVE-2023-45853", "zlib1g", ""),
	), nil)

	image := "registry.example.test/acme/web:1"
	stopped := newDeployment(t, db, store.Deployment{AppID: app.ID, Image: image, Trigger: "manual", CreatedBy: "usr_1"})
	err := d.checkImage(ctx, stopped, app)
	if problemCode(err) != "deploy.vulnerable" {
		t.Fatalf("a fixable critical answered %v", err)
	}
	var problem *errdoc.Problem
	if !errors.As(err, &problem) {
		t.Fatal("not a problem")
	}
	if !strings.Contains(problem.Cause, "1 critical vulnerabilities") ||
		!strings.Contains(problem.Cause, "next 1.0 (fixed in 14.2.25, 15.2.3, CVE-2025-29927)") ||
		strings.Contains(problem.Cause, "zlib1g") {
		t.Errorf("the problem does not name what to move to, and only that: %s", problem.Cause)
	}
	if !strings.Contains(problem.Fix, "--accept-vulnerabilities") {
		t.Errorf("the problem does not say how to deploy anyway: %s", problem.Fix)
	}
	scan := waitForScans(t, db, app.ID)
	if scan.Status != store.ScanSucceeded || scan.DeploymentID != stopped.ID || scan.Trigger != store.ScanTriggerDeploy {
		t.Errorf("the gate's scan was recorded as %+v", scan)
	}

	// The same image, accepted: through, without scanning it again.
	accepted := newDeployment(t, db, store.Deployment{AppID: app.ID, Image: image, Trigger: "manual", AcceptedVulnerabilities: true})
	if err := d.checkImage(ctx, accepted, app); err != nil {
		t.Fatalf("an accepted deploy was stopped: %v", err)
	}
	if !strings.Contains(logText(t, db, accepted.ID), "Deploying anyway, because this deploy accepted them") {
		t.Errorf("the accepted deploy's log does not say so:\n%s", logText(t, db, accepted.ID))
	}
	if scanner.calls() != 1 {
		t.Errorf("an image scanned a moment ago was scanned %d times", scanner.calls())
	}

	// A rollback is how somebody gets out of trouble.
	rollback := newDeployment(t, db, store.Deployment{AppID: app.ID, Image: image, Trigger: "rollback"})
	if err := d.checkImage(ctx, rollback, app); err != nil {
		t.Errorf("a rollback was stopped: %v", err)
	}

	// Without the setting, nothing is stopped and the scan happens beside the
	// rollout.
	setSetting(t, db, settings.KeyScanBlockCritical, "false")
	later := newDeployment(t, db, store.Deployment{AppID: app.ID, Image: "registry.example.test/acme/web:2", Trigger: "push"})
	if err := d.checkImage(ctx, later, app); err != nil {
		t.Fatalf("with the gate off a deploy was stopped: %v", err)
	}
	if scan := waitForScans(t, db, app.ID); scan.Image != later.Image || scan.Status != store.ScanSucceeded {
		t.Errorf("the image was not scanned in the background: %+v", scan)
	}
}

// A scan that cannot run never stops a deploy, and says why in the log.
func TestAScanThatCannotRunLetsTheDeployThrough(t *testing.T) {
	d, db, app, scanner := scanningDeployer(t)
	ctx := t.Context()
	setSetting(t, db, settings.KeyScanBlockCritical, "true")
	scanner.set(store.ScanResult{}, errdoc.ScanDatabaseUnavailable("dial tcp: lookup mirror.gcr.io: no such host"))

	deployment := newDeployment(t, db, store.Deployment{AppID: app.ID, Image: "registry.example.test/acme/web:1", Trigger: "manual"})
	if err := d.checkImage(ctx, deployment, app); err != nil {
		t.Fatalf("a scan that could not run stopped the deploy: %v", err)
	}
	if !strings.Contains(logText(t, db, deployment.ID), "could not be scanned, so it goes out unchecked") {
		t.Errorf("the log does not say the image went out unchecked:\n%s", logText(t, db, deployment.ID))
	}
	scan := waitForScans(t, db, app.ID)
	if scan.Status != store.ScanFailed || scan.ErrorCode != "scan.database_unavailable" || scan.ErrorHint == "" {
		t.Errorf("the failed scan was recorded as %+v", scan)
	}

	// Switched off, nothing is scanned at all.
	setSetting(t, db, settings.KeyScanEnabled, "false")
	before := scanner.calls()
	off := newDeployment(t, db, store.Deployment{AppID: app.ID, Image: "registry.example.test/acme/web:2", Trigger: "manual"})
	if err := d.checkImage(ctx, off, app); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Scan(ctx, app.ID, "usr_1"); problemCode(err) != "scan.disabled" {
		t.Errorf("Scan now with scanning off answered %v", err)
	}
	if scanner.calls() != before {
		t.Error("an image was scanned with scanning switched off")
	}
}

// Through the whole deployment: stopped at the gate, before anything reaches
// the cluster; then deployed with the vulnerabilities accepted, which is
// recorded on the deployment and goes past the gate — to the cluster this
// test does not have.
func TestADeployIsStoppedAtTheGateAndAcceptedPastIt(t *testing.T) {
	d, db, first, scanner := scanningDeployer(t)
	ctx := t.Context()
	setSetting(t, db, settings.KeyScanBlockCritical, "true")
	scanner.set(resultOf(critical("CVE-2021-44228", "org.apache.logging.log4j:log4j-core", "2.15.0")), nil)

	app := store.App{
		EnvironmentID: first.EnvironmentID, Name: "search", Slug: "search", SourceType: "image",
		Image: "ghcr.io/acme/search:7.1", Port: 9200, Replicas: 1,
	}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}

	stopped, err := d.Deploy(ctx, api.DeployRequest{AppID: app.ID, Trigger: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForDeployment(t, db, stopped.ID); got.ErrorCode != "deploy.vulnerable" {
		t.Fatalf("the deploy ended with %q: %s", got.ErrorCode, got.ErrorMessage)
	}

	accepted, err := d.Deploy(ctx, api.DeployRequest{AppID: app.ID, Trigger: "manual", AcceptVulnerabilities: true})
	if err != nil {
		t.Fatal(err)
	}
	got := waitForDeployment(t, db, accepted.ID)
	if !got.AcceptedVulnerabilities {
		t.Error("the deployment does not record that it accepted the vulnerabilities")
	}
	if got.ErrorCode != "cluster.unreachable" {
		t.Errorf("the accepted deploy did not go past the gate: %q %s", got.ErrorCode, got.ErrorMessage)
	}
}

// A new critical is sent once, to the app's project, as app.vulnerable; the
// same finding the next day is not sent again, and one added to it is.
func TestOnlyNewCriticalsAreSent(t *testing.T) {
	d, db, app, scanner := scanningDeployer(t)
	ctx := t.Context()
	notifier := &recordingNotifier{}
	d.notifier = notifier
	_, projectID, err := db.ProjectOfApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}

	scanOnce := func(result store.ScanResult, failure error) {
		t.Helper()
		scanner.set(result, failure)
		scan := store.ImageScan{AppID: app.ID, Image: "registry.example.test/acme/web:1", Trigger: store.ScanTriggerSchedule}
		if err := db.CreateImageScan(ctx, &scan); err != nil {
			t.Fatal(err)
		}
		_, _ = d.runScan(ctx, scan, true)
	}

	nextJS := critical("CVE-2025-29927", "next", "14.2.25")
	scanOnce(resultOf(nextJS, store.ScanFinding{ID: "CVE-1", Package: "braces", Severity: "HIGH", FixedIn: "3.0.3"}), nil)
	sent := notifier.all()
	if len(sent) != 1 || sent[0].event != notify.EventAppVulnerable || sent[0].msg.ProjectID != projectID ||
		!strings.Contains(sent[0].msg.Body, "next 1.0") || !strings.Contains(sent[0].msg.Path, "tab=security") {
		t.Fatalf("the first critical was sent as %+v", sent)
	}

	// The nightly rescan finds the same thing: nothing to say.
	scanOnce(resultOf(nextJS), nil)
	// A scan that fails in between does not make the next one's findings new.
	scanOnce(store.ScanResult{}, errdoc.ScanTimedOut("registry.example.test/acme/web:1"))
	if n := len(notifier.all()); n != 1 {
		t.Fatalf("%d notifications after the same critical was found again", n)
	}

	log4j := critical("CVE-2021-44228", "log4j-core", "2.15.0")
	scanOnce(resultOf(nextJS, log4j), nil)
	sent = notifier.all()
	if len(sent) != 2 {
		t.Fatalf("a new critical was not sent: %d notifications", len(sent))
	}
	if body := sent[1].msg.Body; !strings.Contains(body, "log4j-core") || strings.Contains(body, "next 1.0") {
		t.Errorf("the second notification is not about only the new critical: %s", body)
	}
	if !strings.Contains(sent[1].msg.Title, "new critical vulnerability") {
		t.Errorf("the title is %q", sent[1].msg.Title)
	}
}

// An image a deploy was stopped for never went out, so it is not announced —
// the stopped deploy's failure says what is in it — and the running image's
// next scan is not compared with it. Deployed anyway, it is announced, once,
// however many deploys of it follow.
func TestAStoppedImageIsNotAnnouncedUntilItGoesOut(t *testing.T) {
	d, db, app, scanner := scanningDeployer(t)
	ctx := t.Context()
	notifier := &recordingNotifier{}
	d.notifier = notifier
	setSetting(t, db, settings.KeyScanBlockCritical, "true")

	log4j := critical("CVE-2021-44228", "log4j-core", "2.15.0")
	scanner.set(resultOf(log4j), nil)
	image := "registry.example.test/acme/web:2"
	stopped := newDeployment(t, db, store.Deployment{AppID: app.ID, Image: image, Trigger: "push"})
	if err := d.checkImage(ctx, stopped, app); problemCode(err) != "deploy.vulnerable" {
		t.Fatalf("the deploy answered %v", err)
	}
	if n := len(notifier.all()); n != 0 {
		t.Fatalf("a stopped deploy's image was announced %d times", n)
	}

	for range 2 {
		accepted := newDeployment(t, db, store.Deployment{AppID: app.ID, Image: image, Trigger: "manual", AcceptedVulnerabilities: true})
		if err := d.checkImage(ctx, accepted, app); err != nil {
			t.Fatalf("the accepted deploy answered %v", err)
		}
	}
	sent := notifier.all()
	if len(sent) != 1 || !strings.Contains(sent[0].msg.Body, "log4j-core") {
		t.Fatalf("deploying it anyway, twice, sent %+v", sent)
	}
	if scanner.calls() != 1 {
		t.Errorf("the image was scanned %d times", scanner.calls())
	}

	// The nightly rescan of the image now running finds the same: not news.
	scan := store.ImageScan{AppID: app.ID, Image: image, Trigger: store.ScanTriggerSchedule}
	if err := db.CreateImageScan(ctx, &scan); err != nil {
		t.Fatal(err)
	}
	if _, err := d.runScan(ctx, scan, true); err != nil {
		t.Fatal(err)
	}
	if n := len(notifier.all()); n != 1 {
		t.Errorf("the rescan announced the same critical again: %d notifications", n)
	}
}

// The daily rescan queues the apps that are due when the schedule says, and
// nothing when it does not or is off.
func TestTheScheduledRescanQueuesWhatIsDue(t *testing.T) {
	d, db, app, scanner := scanningDeployer(t)
	ctx := t.Context()
	scanner.set(resultOf(), nil)
	setSetting(t, db, settings.KeyScanSchedule, "30 4 * * *")

	// Two apps running an image: this one scanned an hour ago, the other
	// never.
	running := func(a store.App, image string) {
		t.Helper()
		deployment := newDeployment(t, db, store.Deployment{AppID: a.ID, Image: image})
		if _, err := db.Exec(ctx, `UPDATE deployments SET status = 'succeeded' WHERE id = ?`, deployment.ID); err != nil {
			t.Fatal(err)
		}
	}
	running(app, "registry.example.test/acme/web:1")
	recent := store.ImageScan{AppID: app.ID, Image: "registry.example.test/acme/web:1", Trigger: store.ScanTriggerDeploy}
	if err := db.CreateImageScan(ctx, &recent); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishImageScan(ctx, recent.ID, resultOf()); err != nil {
		t.Fatal(err)
	}
	other := store.App{EnvironmentID: app.EnvironmentID, Name: "worker", Slug: "worker", SourceType: "image", Image: "redis:7", Replicas: 1}
	if err := db.CreateApp(ctx, &other); err != nil {
		t.Fatal(err)
	}
	running(other, "redis:7")
	// And one never deployed, which has nothing to scan.
	idle := store.App{EnvironmentID: app.EnvironmentID, Name: "idle", Slug: "idle", Replicas: 1}
	if err := db.CreateApp(ctx, &idle); err != nil {
		t.Fatal(err)
	}

	day := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	if n := d.RescanAt(ctx, []time.Time{day.Add(4*time.Hour + 29*time.Minute)}); n != 0 {
		t.Fatalf("%d scans were queued a minute early", n)
	}
	if n := d.RescanAt(ctx, []time.Time{day.Add(4*time.Hour + 31*time.Minute), day.Add(4*time.Hour + 30*time.Minute)}); n != 1 {
		t.Fatalf("%d scans were queued at the scheduled minute, want the one app not scanned lately", n)
	}
	scan := waitForScans(t, db, other.ID)
	if scan.Trigger != store.ScanTriggerSchedule || scan.Image != "redis:7" || scan.Status != store.ScanSucceeded {
		t.Errorf("the scheduled scan was %+v", scan)
	}
	if scanner.calls() != 1 {
		t.Errorf("%d images were scanned", scanner.calls())
	}

	setSetting(t, db, settings.KeyScanSchedule, "off")
	if n := d.RescanAt(ctx, []time.Time{day.AddDate(0, 0, 3).Add(4*time.Hour + 30*time.Minute)}); n != 0 {
		t.Errorf("%d scans were queued with the rescan off", n)
	}
}

// Scan now scans what the app runs, and asking twice is one scan.
func TestScanNowScansTheRunningImageOnce(t *testing.T) {
	d, db, app, scanner := scanningDeployer(t)
	ctx := t.Context()
	if _, err := d.Scan(ctx, app.ID, "usr_1"); problemCode(err) != "scan.no_image" {
		t.Fatalf("an app never deployed answered %v", err)
	}

	deployment := newDeployment(t, db, store.Deployment{AppID: app.ID, Image: "registry.example.test/acme/web:1"})
	if _, err := db.Exec(ctx, `UPDATE deployments SET status = 'succeeded' WHERE id = ?`, deployment.ID); err != nil {
		t.Fatal(err)
	}
	// Held, so the second request finds the first still queued.
	release, err := d.scans.acquire(ctx, oneAtATime, func(int, int) {})
	if err != nil {
		t.Fatal(err)
	}
	first, err := d.Scan(ctx, app.ID, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Scan(ctx, app.ID, "usr_2")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if first.ID != second.ID || first.Image != deployment.Image || first.Trigger != store.ScanTriggerManual {
		t.Errorf("two requests made %s and %s of %s", first.ID, second.ID, first.Image)
	}
	waitForScans(t, db, app.ID)
	if scanner.calls() != 1 {
		t.Errorf("the image was scanned %d times", scanner.calls())
	}
}

func waitForDeployment(t *testing.T, db *store.DB, id string) store.Deployment {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		deployment, err := db.GetDeployment(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if deployment.Status.Terminal() {
			return deployment
		}
		if time.Now().After(deadline) {
			t.Fatalf("deployment %s is still %s", id, deployment.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
