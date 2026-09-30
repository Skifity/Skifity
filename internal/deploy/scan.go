package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/cluster"
	"skifity/internal/cron"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/notify"
	"skifity/internal/runsafe"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/vulnscan"
)

// Scanning the images apps run for known vulnerabilities.
//
// A scan is a Trivy Job in the build namespace (internal/vulnscan), started in
// three ways: when a deployment's image is built or deployed, on the daily
// schedule for every app's running image, and when somebody asks. They run one
// at a time, through the same kind of queue builds wait in: Trivy keeps its
// database on one volume and locks it, and one scan at a time is also what a
// two-server cluster can spare beside its apps.
//
// Nothing here ever stops a deploy by failing. A scan that cannot run — no
// internet for the database, a registry that says no — is recorded as failed
// and the deploy goes on. The one thing that stops a deploy is a scan that
// worked and found a critical vulnerability with a fix, when an administrator
// asked for exactly that.

// rescanAfter is how recent a scan of an image has to be to stand for another
// one: the vulnerability database is refreshed about daily, and a deploy an
// hour after the nightly scan would learn nothing new by asking again.
const rescanAfter = 12 * time.Hour

// scanTimeout bounds one scan, the wait for its pod included. The Job's own
// deadline is shorter, so it is the Job that says it took too long.
const scanTimeout = vulnscan.DefaultTimeoutSeconds*time.Second + 5*time.Minute

// oneAtATime is the scan queue's limit.
func oneAtATime() int { return 1 }

// canScan reports whether there is anything to run a scan on: a cluster, or
// the scanner a test put in its place.
func (d *Deployer) canScan() bool { return d.cluster != nil || d.scanJob != nil }

// scanningEnabled reports whether images are scanned at all. On unless an
// administrator turned it off.
func (d *Deployer) scanningEnabled(ctx context.Context) bool {
	value, _, err := d.db.GetSetting(ctx, settings.KeyScanEnabled)
	if err != nil {
		return false
	}
	return strings.TrimSpace(value) != "false"
}

// blockingFixableCriticals reports whether a deploy whose image has a critical
// vulnerability with a fix is stopped. Off unless an administrator turned it on.
func (d *Deployer) blockingFixableCriticals(ctx context.Context) bool {
	value, _, err := d.db.GetSetting(ctx, settings.KeyScanBlockCritical)
	return err == nil && strings.TrimSpace(value) == "true"
}

// rescanSchedule is when running apps are scanned again. ok is false when the
// rescan is off.
func (d *Deployer) rescanSchedule(ctx context.Context) (cron.Schedule, bool) {
	value, _, err := d.db.GetSetting(ctx, settings.KeyScanSchedule)
	if err != nil {
		return cron.Schedule{}, false
	}
	value = strings.TrimSpace(value)
	switch value {
	case "off":
		return cron.Schedule{}, false
	case "":
		value = settings.DefaultScanSchedule
	}
	schedule, err := cron.ParseSchedule(value)
	if err != nil {
		d.log.Warn("the image rescan schedule is not a schedule; rescanning at the default time instead",
			"schedule", value, "error", err)
		schedule, _ = cron.ParseSchedule(settings.DefaultScanSchedule)
	}
	return schedule, true
}

// Scan queues a scan of the image an app runs now, or answers the scan of it
// already queued or running. It implements api.Scanner.
func (d *Deployer) Scan(ctx context.Context, appID, requestedBy string) (store.ImageScan, error) {
	if !d.scanningEnabled(ctx) {
		return store.ImageScan{}, errdoc.ScanningDisabled()
	}
	if !d.canScan() {
		return store.ImageScan{}, errdoc.ClusterUnreachable(nil)
	}
	app, err := d.db.GetApp(ctx, appID)
	if err != nil {
		return store.ImageScan{}, err
	}
	current, err := d.db.LatestSuccessfulDeployment(ctx, appID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && current.Image == "") {
		return store.ImageScan{}, errdoc.NothingToScan(app.Name)
	}
	if err != nil {
		return store.ImageScan{}, err
	}
	return d.queueScan(ctx, store.ImageScan{
		AppID: appID, DeploymentID: current.ID, Image: current.Image,
		Trigger: store.ScanTriggerManual, RequestedBy: requestedBy,
	})
}

// queueScan records a scan and runs it in the background when its turn comes.
func (d *Deployer) queueScan(ctx context.Context, scan store.ImageScan) (store.ImageScan, error) {
	created, err := d.db.QueueImageScan(ctx, &scan)
	if err != nil {
		return store.ImageScan{}, err
	}
	if !created {
		return scan, nil
	}
	d.publishScan(ctx, scan)
	runsafe.Go(d.log, "scan an image", func() {
		// Not the caller's context: a scan somebody asked for outlives the
		// request that asked, and one queued by a deploy outlives the deploy.
		ctx := context.Background()
		release, err := d.scans.acquire(ctx, oneAtATime, func(int, int) {})
		if err != nil {
			d.failScan(ctx, scan, errdoc.From(err))
			return
		}
		defer release()
		_, _ = d.runScan(ctx, scan, true)
	})
	return scan, nil
}

// runScan runs a scan that holds the scanner, and records what it found.
// announce tells the team about the criticals in it that are new; a deploy
// that waits for its scan announces it itself, once it knows the image is
// going out.
func (d *Deployer) runScan(ctx context.Context, scan store.ImageScan, announce bool) (store.ImageScan, error) {
	defer runsafe.Recover(d.log, "scan "+scan.ID, func(err error) {
		d.failScan(ctx, scan, errdoc.From(err))
	})
	if err := d.db.StartImageScan(ctx, scan.ID); err != nil {
		d.log.Warn("could not mark an image scan as running", "scan", scan.ID, "error", err)
	}
	scan.Status = store.ScanRunning
	d.publishScan(ctx, scan)

	scanCtx, cancel := context.WithTimeout(ctx, scanTimeout)
	result, err := d.scanImage(scanCtx, scan)
	timedOut := errors.Is(scanCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	cancel()
	if err != nil {
		problem := errdoc.From(err)
		if timedOut {
			// Its own limit, not the caller's: a scan whose pod never
			// started ends here rather than at the Job's deadline.
			problem = errdoc.ScanTimedOut(scan.Image)
		}
		d.failScan(ctx, scan, problem)
		return store.ImageScan{}, problem
	}

	finished := context.WithoutCancel(ctx)
	if err := d.db.FinishImageScan(finished, scan.ID, result); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The app was deleted while its image was being scanned.
			return store.ImageScan{}, err
		}
		d.log.Warn("could not record an image scan", "scan", scan.ID, "error", err)
		return store.ImageScan{}, err
	}
	recorded, err := d.db.GetImageScan(finished, scan.ID)
	if err != nil {
		return store.ImageScan{}, err
	}
	if announce {
		d.announce(finished, recorded)
	}
	d.publishScan(finished, recorded)
	return recorded, nil
}

// announce tells an app's team about the critical vulnerabilities in a report
// that the last report they were told about did not have — once per report,
// however many deploys of its image read it.
func (d *Deployer) announce(ctx context.Context, scan store.ImageScan) {
	if scan.Status != store.ScanSucceeded {
		return
	}
	// Read before this report is marked, or it would be its own baseline.
	baseline, err := d.db.LatestAnnouncedImageScan(ctx, scan.AppID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		d.log.Warn("could not read what an app's team was last told", "app", scan.AppID, "error", err)
		return
	}
	marked, err := d.db.MarkImageScanAnnounced(ctx, scan.ID)
	if err != nil {
		d.log.Warn("could not record that a scan was announced", "scan", scan.ID, "error", err)
		return
	}
	if !marked {
		return
	}
	if fresh := vulnscan.NewCriticals(baseline.Findings, scan.Findings); len(fresh) > 0 {
		d.notifyVulnerable(ctx, scan, fresh)
	}
}

// failScan records why a scan produced no report.
func (d *Deployer) failScan(ctx context.Context, scan store.ImageScan, problem *errdoc.Problem) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	message := problem.Cause
	if message == "" {
		message = problem.Error()
	}
	if err := d.db.FailImageScan(ctx, scan.ID, problem.Code, message, problem.Fix); err != nil &&
		!errors.Is(err, store.ErrNotFound) {
		d.log.Warn("could not record a failed image scan", "scan", scan.ID, "error", err)
	}
	d.log.Info("an image scan did not produce a report", "scan", scan.ID, "app", scan.AppID, "code", problem.Code)
	scan.Status = store.ScanFailed
	d.publishScan(ctx, scan)
}

// scanImage runs the scan, through scanJob when a test has put one there.
func (d *Deployer) scanImage(ctx context.Context, scan store.ImageScan) (store.ScanResult, error) {
	if d.scanJob != nil {
		return d.scanJob(ctx, scan)
	}
	return d.runScanJob(ctx, scan)
}

// publishScan tells the app's page that its scan moved.
func (d *Deployer) publishScan(ctx context.Context, scan store.ImageScan) {
	if d.hub == nil {
		return
	}
	teamID, err := d.db.TeamIDForApp(ctx, scan.AppID)
	if err != nil {
		return
	}
	d.hub.Publish(events.TeamTopic(teamID), "scan", map[string]any{
		"app_id": scan.AppID, "scan_id": scan.ID, "status": scan.Status,
	})
}

// notifyVulnerable tells the app's team about critical vulnerabilities its
// previous scan did not find.
func (d *Deployer) notifyVulnerable(ctx context.Context, scan store.ImageScan, fresh []store.ScanFinding) {
	if d.notifier == nil {
		return
	}
	app, err := d.db.GetApp(ctx, scan.AppID)
	if err != nil {
		return
	}
	teamID, projectID, err := d.db.ProjectOfApp(ctx, scan.AppID)
	if err != nil {
		d.log.Warn("could not work out which team to tell about a vulnerable image", "app", scan.AppID, "error", err)
		return
	}
	title := fmt.Sprintf("%s has %d new critical vulnerabilities", app.Name, len(fresh))
	if len(fresh) == 1 {
		title = app.Name + " has a new critical vulnerability"
	}
	fixable := len(vulnscan.Blocking(fresh))
	body := "A scan of the image it runs found: " + vulnscan.Describe(fresh, 5) + "."
	switch {
	case fixable == 1 && len(fresh) == 1:
		body += " It has a fixed version to move to."
	case fixable == 1:
		body += " One of them has a fixed version to move to."
	case fixable > 1:
		body += fmt.Sprintf(" %d of them have a fixed version to move to.", fixable)
	}
	d.notifier.Notify(ctx, teamID, notify.EventAppVulnerable, notify.Message{
		Title:     title,
		Body:      body,
		Level:     "error",
		Path:      "/apps/" + app.ID + "?tab=security",
		ProjectID: projectID,
		Fields: map[string]string{
			"App":      app.Name,
			"Image":    scan.Image,
			"Critical": strconv.Itoa(scan.Counts.Critical),
			"High":     strconv.Itoa(scan.Counts.High),
		},
	})
}

// RescanAt queues the scheduled rescan of every running app when the
// schedule falls on one of these minutes. The scheduler calls it every minute,
// with the minutes a late tick stepped over, as it does the backups.
//
// It only queues: the scans run one after another in the background, so a
// hundred apps take the early morning rather than the minute tick.
func (d *Deployer) RescanAt(ctx context.Context, minutes []time.Time) int {
	if !d.canScan() || !d.scanningEnabled(ctx) {
		return 0
	}
	schedule, on := d.rescanSchedule(ctx)
	if !on {
		return 0
	}
	due := false
	for _, minute := range minutes {
		if schedule.Matches(minute) {
			due = true
			break
		}
	}
	if !due {
		return 0
	}
	candidates, err := d.db.ListScanCandidates(ctx)
	if err != nil {
		d.log.Warn("could not list the apps to scan", "error", err)
		return 0
	}
	queued := 0
	for _, candidate := range vulnscan.Due(candidates, time.Now(), rescanAfter) {
		if _, err := d.queueScan(ctx, store.ImageScan{
			AppID: candidate.AppID, DeploymentID: candidate.DeploymentID, Image: candidate.Image,
			Trigger: store.ScanTriggerSchedule,
		}); err != nil {
			d.log.Warn("could not queue an image scan", "app", candidate.AppID, "error", err)
			continue
		}
		queued++
	}
	if queued > 0 {
		d.log.Info("queued the scheduled image scans", "count", queued)
	}
	return queued
}

// checkImage is what a deployment does about scanning once its image exists.
//
// With deploys stopped for fixable criticals, it scans the image now, ahead of
// anything else waiting for the scanner, and stops the deploy when the scan
// finds one and nobody accepted it. Otherwise it queues a scan to run beside
// the rollout, unless the image was looked at recently. Either way a scan that
// cannot run lets the deploy through: that is a scanner's problem, and an app
// that cannot be deployed because a database download failed is a panel that
// has made itself the outage.
func (d *Deployer) checkImage(ctx context.Context, deployment store.Deployment, app store.App) error {
	if deployment.Image == "" || !d.canScan() || !d.scanningEnabled(ctx) {
		return nil
	}
	// A rollback is how somebody gets out of trouble, and the version it goes
	// back to already ran. It is scanned, and never stopped.
	gate := d.blockingFixableCriticals(ctx) && deployment.Trigger != "rollback"
	recent, recentErr := d.db.SucceededImageScanSince(ctx, app.ID, deployment.Image, time.Now().Add(-rescanAfter))
	if !gate {
		if recentErr == nil {
			// The image is going out: a report nobody was told about — one a
			// deploy was stopped for, before the setting was turned off — is
			// told about now. Once, whatever reads it after.
			d.announce(ctx, recent)
			return nil
		}
		if _, err := d.queueScan(ctx, store.ImageScan{
			AppID: app.ID, DeploymentID: deployment.ID, Image: deployment.Image,
			Trigger: store.ScanTriggerDeploy, RequestedBy: deployment.CreatedBy,
		}); err != nil {
			d.log.Warn("could not queue a scan of a deployment's image", "deployment", deployment.ID, "error", err)
		}
		return nil
	}

	scan := recent
	if recentErr != nil {
		d.appendLog(ctx, deployment.ID, "Scanning the image for known vulnerabilities before it goes out.")
		var err error
		scan, err = d.scanNow(ctx, deployment, app)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			d.appendLog(ctx, deployment.ID, "The image could not be scanned, so it goes out unchecked: "+
				errdoc.From(err).Error())
			return nil
		}
	}
	// Only an image that goes out is announced: one a deploy is stopped for
	// is in that deploy's failure, and is not what the app runs.
	if scan.FixableCritical == 0 {
		d.appendLog(ctx, deployment.ID, fmt.Sprintf(
			"The scan found no critical vulnerability with a fix (%d critical, %d high).",
			scan.Counts.Critical, scan.Counts.High))
		d.announce(ctx, scan)
		return nil
	}
	blocking := vulnscan.Blocking(scan.Findings)
	if deployment.AcceptedVulnerabilities {
		d.appendLog(ctx, deployment.ID, fmt.Sprintf(
			"Warning: %d critical vulnerabilities have a fix (%s). Deploying anyway, because this deploy accepted them.",
			scan.FixableCritical, vulnscan.Describe(blocking, 5)))
		d.announce(ctx, scan)
		return nil
	}
	return errdoc.VulnerableImage(app.Name, scan.FixableCritical, vulnscan.Describe(blocking, 10))
}

// scanNow scans a deployment's image while the deployment waits, ahead of the
// scans nobody is waiting for.
func (d *Deployer) scanNow(ctx context.Context, deployment store.Deployment, app store.App) (store.ImageScan, error) {
	scan := store.ImageScan{
		AppID: app.ID, DeploymentID: deployment.ID, Image: deployment.Image,
		Trigger: store.ScanTriggerDeploy, RequestedBy: deployment.CreatedBy,
	}
	if err := d.db.CreateImageScan(ctx, &scan); err != nil {
		return store.ImageScan{}, err
	}
	release, err := d.scans.acquireFirst(ctx, oneAtATime, func(int, int) {
		d.appendLog(ctx, deployment.ID, "Waiting for the scanner, which is busy with another image.")
	})
	if err != nil {
		d.failScan(ctx, scan, errdoc.From(err))
		return store.ImageScan{}, err
	}
	defer release()
	return d.runScan(ctx, scan, false)
}

// --- in the cluster ---

// runScanJob runs one scan as a Job and reads its report.
func (d *Deployer) runScanJob(ctx context.Context, scan store.ImageScan) (store.ScanResult, error) {
	if d.cluster == nil {
		return store.ScanResult{}, errdoc.ClusterUnreachable(nil)
	}
	client := d.cluster.Client()
	applier := client.Applier()
	namespace := client.BuildNamespace()

	// The namespace and its guards, and the volume the database is kept on:
	// both may predate nothing, on a panel that has only ever run images.
	if err := d.cluster.EnsureBuildNamespace(ctx); err != nil {
		return store.ScanResult{}, err
	}
	if err := applier.Apply(ctx, vulnscan.CacheClaimObject(namespace)); err != nil {
		return store.ScanResult{}, fmt.Errorf("prepare the scanner's database volume: %w", err)
	}

	app, err := d.db.GetApp(ctx, scan.AppID)
	if err != nil {
		return store.ScanResult{}, err
	}
	spec := vulnscan.JobSpec{
		Name:      kube.ResourceName("scan-"+app.Slug, shortID(scan.ID)),
		Namespace: namespace,
		AppID:     app.ID,
		ScanID:    scan.ID,
		Image:     d.scanReference(ctx, scan.Image),
	}
	auth, err := d.scanAuth(ctx, app.ID, scan.Image)
	if err != nil {
		return store.ScanResult{}, err
	}
	if auth != nil {
		auth.Name = kube.ResourceName(spec.Name, "auth")
		auth.Namespace = namespace
		spec.RegistrySecret = auth.Name
	}
	job, err := vulnscan.BuildJob(spec)
	if err != nil {
		return store.ScanResult{}, errdoc.ScanFailed(scan.Image, err.Error())
	}

	if err := applier.DeleteAndWait(ctx, "batch/v1", "Job", namespace, spec.Name, time.Minute); err != nil {
		return store.ScanResult{}, fmt.Errorf("clear the previous scan: %w", err)
	}
	if auth != nil {
		if err := applier.Apply(ctx, auth); err != nil {
			return store.ScanResult{}, fmt.Errorf("hand the scan its registry credentials: %w", err)
		}
		defer d.removeBuildSecret(ctx, namespace, auth.Name)
	}
	if err := applier.Apply(ctx, job); err != nil {
		return store.ScanResult{}, fmt.Errorf("start the scan: %w", err)
	}
	if auth != nil {
		d.ownScanSecret(ctx, namespace, spec.Name, auth)
	}
	defer func() {
		// A scan that ran out of time or was cancelled stops here, and its
		// Job with it.
		if ctx.Err() == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if err := applier.Delete(cleanup, "batch/v1", "Job", namespace, spec.Name); err != nil {
			d.log.Warn("could not stop a scan that was cancelled", "job", spec.Name, "error", err)
		}
	}()

	pod, err := d.waitForScan(ctx, namespace, spec.Name, scan.Image)
	if err != nil {
		return store.ScanResult{}, err
	}
	report, err := d.readReport(ctx, namespace, pod)
	if err != nil {
		return store.ScanResult{}, err
	}
	return vulnscan.Summarize(report, vulnscan.MaxFindings), nil
}

// scanReference is the reference the scanner is given for an image: the
// in-cluster registry's address in place of its name, see
// vulnscan.InClusterReference.
func (d *Deployer) scanReference(ctx context.Context, image string) string {
	if !strings.HasPrefix(image, kube.RegistryHost()+"/") {
		return image
	}
	client := d.cluster.Client()
	service, err := client.Clientset().CoreV1().Services(client.BuildNamespace()).
		Get(ctx, cluster.RegistryService, metav1.GetOptions{})
	if err != nil {
		d.log.Warn("could not read the registry's address for a scan", "error", err)
		return image
	}
	reference, ok := vulnscan.InClusterReference(image, kube.RegistryHost(), service.Spec.ClusterIP, kube.RegistryPort)
	if !ok {
		d.log.Warn("the registry's cluster address is not a private IPv4 address, so its images are scanned by name and the scan will not reach it",
			"address", service.Spec.ClusterIP)
	}
	return reference
}

// scanAuth is the login a scan pulls its image with: the one for the image's
// registry, from the team's credentials or the panel's external registry, and
// no other. Nil when the image needs none.
func (d *Deployer) scanAuth(ctx context.Context, appID, image string) (*corev1.Secret, error) {
	host := kube.ImageHost(image)
	teamID, err := d.db.TeamIDForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	logins, err := d.teamLogins(ctx, teamID)
	if err != nil {
		return nil, err
	}
	var chosen *kube.RegistryLogin
	for _, login := range logins {
		if strings.EqualFold(login.Host, host) {
			chosen = &login
		}
	}
	// The panel's own external registry, which is where it pushes what it
	// builds, wins over a team's credential for the same host, as it does
	// for a build.
	if address, _, secret, err := d.registryAddress(ctx); err == nil && secret != "" &&
		strings.EqualFold(registryHost(address), host) {
		username, err := d.settingValue(ctx, settings.KeyRegistryUser)
		if err != nil {
			return nil, err
		}
		password, err := d.settingValue(ctx, settings.KeyRegistryPassword)
		if err != nil {
			return nil, err
		}
		if username != "" || password != "" {
			chosen = &kube.RegistryLogin{Host: host, Username: username, Password: password}
		}
	}
	if chosen == nil {
		return nil, nil
	}
	return kube.PullSecret("scan-auth", "", []kube.RegistryLogin{*chosen})
}

// ownScanSecret makes a scan's Job the owner of the Secret made for it, so the
// cluster collects it with the Job if the panel stops first.
func (d *Deployer) ownScanSecret(ctx context.Context, namespace, jobName string, secret *corev1.Secret) {
	applier := d.cluster.Client().Applier()
	job, err := applier.Get(ctx, "batch/v1", "Job", namespace, jobName)
	if err != nil {
		d.log.Warn("could not read the scan to give it its credentials", "job", jobName, "error", err)
		return
	}
	owned := secret.DeepCopy()
	owned.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: "batch/v1", Kind: "Job", Name: job.GetName(), UID: job.GetUID(),
	}}
	if err := applier.Apply(ctx, owned); err != nil {
		d.log.Warn("could not make the scan own its credentials", "job", jobName, "error", err)
	}
}

// waitForScan waits for a scan's Job to finish and answers with its pod, or
// with why it produced no report.
func (d *Deployer) waitForScan(ctx context.Context, namespace, jobName, image string) (string, error) {
	clientset := d.cluster.Client().Clientset()
	deadline := time.Now().Add(scanTimeout)
	// When the scanner's own image was first seen failing to pull. The kubelet
	// retries a pull for as long as the Job lives, and a registry that said no
	// twice in two minutes is not going to be waited out.
	var pullFailing time.Time
	for {
		job, err := clientset.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("read the scan's status: %w", err)
		}
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + jobName})
		if err != nil {
			return "", fmt.Errorf("find the scan's pod: %w", err)
		}
		var pod *corev1.Pod
		if len(pods.Items) > 0 {
			pod = &pods.Items[0]
		}
		switch {
		case job.Status.Succeeded > 0 && pod != nil:
			return pod.Name, nil
		case job.Status.Succeeded > 0:
			return "", errdoc.ScanReportUnreadable("the scan finished and its pod, which held the report, is gone")
		case job.Status.Failed > 0 || jobFailed(job):
			if jobTimedOut(job) {
				return "", errdoc.ScanTimedOut(image)
			}
			output := ""
			if pod != nil {
				output = d.scanOutput(ctx, namespace, *pod)
			}
			return "", vulnscan.Failure(image, output)
		}
		if pod != nil {
			if problem := d.unschedulableScan(ctx, namespace, *pod); problem != nil {
				return "", problem
			}
			if reason := scannerNotStarting(*pod); reason == "" {
				pullFailing = time.Time{}
			} else if pullFailing.IsZero() {
				pullFailing = time.Now()
			} else if time.Since(pullFailing) > 2*time.Minute {
				return "", errdoc.ScanFailed(image, "the scanner itself could not start: "+reason)
			}
		}
		if time.Now().After(deadline) {
			return "", errdoc.ScanTimedOut(image)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// scannerNotStarting is why a scan pod's container cannot start — its image
// will not pull, its configuration is wrong — or empty while it can.
func scannerNotStarting(pod corev1.Pod) string {
	for _, status := range append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		waiting := status.State.Waiting
		if waiting == nil {
			continue
		}
		switch waiting.Reason {
		case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError":
			return waiting.Reason + ": " + waiting.Message
		}
	}
	return ""
}

func jobFailed(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func jobTimedOut(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Reason == "DeadlineExceeded" {
			return true
		}
	}
	return false
}

// unschedulableScan is a scan pod that will never start, said now rather than
// at the deadline.
//
// The database volume lives on the server the first scan ran on. When that
// server has gone, every scan after it waits for a server that is not coming
// back; the volume only holds a cache, so it is dropped, and the next scan
// makes a new one wherever it lands.
func (d *Deployer) unschedulableScan(ctx context.Context, namespace string, pod corev1.Pod) *errdoc.Problem {
	if pod.Status.Phase != corev1.PodPending {
		return nil
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type != corev1.PodScheduled || cond.Status != corev1.ConditionFalse || cond.Reason != "Unschedulable" {
			continue
		}
		if strings.Contains(strings.ToLower(cond.Message), "volume node affinity") {
			if err := d.cluster.Client().Applier().Delete(ctx, "v1", "PersistentVolumeClaim", namespace, vulnscan.CacheClaim); err != nil {
				d.log.Warn("could not drop the scanner's database volume", "error", err)
			}
		}
		return errdoc.InsufficientCapacity("The scan", cond.Message)
	}
	return nil
}

// scanOutput is what the scanner printed, and why its containers stopped,
// for the message of a scan that failed.
func (d *Deployer) scanOutput(ctx context.Context, namespace string, pod corev1.Pod) string {
	var out strings.Builder
	for _, status := range append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if waiting := status.State.Waiting; waiting != nil && waiting.Message != "" {
			// A container that never started says why here: an image that
			// will not pull, a volume that will not mount.
			fmt.Fprintf(&out, "%s: %s\n", waiting.Reason, waiting.Message)
		}
	}
	tail := int64(40)
	stream, err := d.cluster.Client().StreamClientset().CoreV1().Pods(namespace).
		GetLogs(pod.Name, &corev1.PodLogOptions{Container: vulnscan.ScanContainer, TailLines: &tail}).Stream(ctx)
	if err == nil {
		defer stream.Close()
		if text, err := io.ReadAll(io.LimitReader(stream, 16<<10)); err == nil {
			out.Write(text)
		}
	}
	// Whatever Trivy printed about a registry is scrubbed like a build's log:
	// an error can quote a URL, and a URL can carry a token.
	return logging.Scrub(out.String())
}

// readReport reads the report a finished scan printed.
func (d *Deployer) readReport(ctx context.Context, namespace, pod string) (vulnscan.Report, error) {
	stream, err := d.cluster.Client().StreamClientset().CoreV1().Pods(namespace).
		GetLogs(pod, &corev1.PodLogOptions{Container: vulnscan.ReportContainer}).Stream(ctx)
	if err != nil {
		return vulnscan.Report{}, errdoc.ScanReportUnreadable(err.Error())
	}
	defer stream.Close()
	report, err := vulnscan.ParseReport(stream)
	if err != nil {
		return vulnscan.Report{}, errdoc.ScanReportUnreadable(err.Error())
	}
	return report, nil
}
