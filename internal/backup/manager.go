package backup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/cluster"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/notify"
	"skifity/internal/plugins"
	"skifity/internal/runsafe"
	"skifity/internal/store"
)

// Manager implements api.BackupManager.
type Manager struct {
	db       *store.DB
	keyring  *crypto.Keyring
	hub      *events.Hub
	cluster  *cluster.Cluster
	notifier notify.Notifier
	log      *slog.Logger

	// Plugins hear when a backup finishes, either way. A zero value sends
	// nothing, so leaving it unset is a working configuration.
	Plugins plugins.Dispatcher
}

// New builds a Manager. notifier may be nil, and then nothing is sent.
func New(db *store.DB, keyring *crypto.Keyring, hub *events.Hub, c *cluster.Cluster, notifier notify.Notifier, log *slog.Logger) *Manager {
	return &Manager{db: db, keyring: keyring, hub: hub, cluster: c, notifier: notifier, log: log}
}

// Verify checks that the configured storage is usable.
func (m *Manager) Verify(ctx context.Context) error {
	storage, err := LoadStorage(ctx, m.db, m.keyring)
	if err != nil {
		return err
	}
	return storage.Verify(ctx)
}

// Run takes a backup now.
func (m *Manager) Run(ctx context.Context, targetType, targetID, kind string) (store.Backup, error) {
	if m.cluster == nil {
		return store.Backup{}, errdoc.ClusterUnreachable(nil)
	}
	switch targetType {
	case "database":
	case "volume":
		return m.runVolumeBackup(ctx, targetID, kind)
	default:
		return store.Backup{}, errdoc.BadRequest("Only databases and volumes can be backed up.")
	}

	record, err := m.db.GetDatabase(ctx, targetID)
	if err != nil {
		return store.Backup{}, err
	}
	if record.Status != "running" {
		return store.Backup{}, errdoc.New("backup.database_not_running", "This database is not running").
			WithCause("%s is %s, so there is nothing to back up.", record.Name, record.Status).
			WithImpact("No backup was taken.").
			WithFix("Wait for the database to be running, then try again.")
	}

	storage, err := LoadStorage(ctx, m.db, m.keyring)
	if err != nil {
		return store.Backup{}, err
	}

	backup := store.Backup{
		TargetType: targetType, TargetID: targetID,
		Status: "running", Kind: kind,
	}
	backup.Location = ObjectKey(targetType, targetID, record.Name, time.Now())
	if err := m.db.CreateBackup(ctx, &backup); err != nil {
		return store.Backup{}, err
	}

	go m.run(context.WithoutCancel(ctx), storage, backup, record)
	return backup, nil
}

func (m *Manager) run(ctx context.Context, storage *Storage, backup store.Backup, record store.Database) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Hour)
	defer cancel()

	fail := func(err error) {
		problem := errdoc.From(err)
		m.log.Error("backup failed", "backup", backup.ID, "database", record.ID, "error", err)
		_ = m.db.FinishBackup(ctx, backup.ID, "failed", backup.Location, 0, problem.Error())
		m.publish(ctx, record.ID)
		m.notifyFailure(ctx, record, problem)
		m.tellPlugins(ctx, backup, record.Name, problem)
	}
	// A backup runs unattended, often at night. A panic here used to take the
	// panel with it, so the first anybody knew was that the panel was gone.
	defer runsafe.Recover(m.log, "backup "+backup.ID, fail)

	env, err := m.db.GetEnvironment(ctx, record.EnvironmentID)
	if err != nil {
		fail(err)
		return
	}

	presigned, err := storage.PresignPut(ctx, backup.Location)
	if err != nil {
		fail(err)
		return
	}

	jobName := JobName("backup-"+record.Slug, backup.ID)
	secretName := jobName + "-url"

	// The URL Secret lives only as long as the job, so an expired signature is
	// not left lying in the namespace.
	defer func() {
		if err := m.cluster.Client().Applier().Delete(ctx, "v1", "Secret", env.Namespace, secretName); err != nil {
			m.log.Warn("could not remove the backup URL secret", "backup", backup.ID, "error", err)
		}
	}()

	if err := m.cluster.Client().Applier().Apply(ctx,
		URLSecret(secretName, env.Namespace, presigned)); err != nil {
		fail(err)
		return
	}

	job, err := BuildJob(JobSpec{
		Name:              jobName,
		Namespace:         env.Namespace,
		Engine:            record.Engine,
		CredentialsSecret: kube.ResourceName(record.Slug, "credentials"),
		URLSecret:         secretName,
		BackupID:          backup.ID,
	})
	if err != nil {
		fail(err)
		return
	}
	_ = m.cluster.Client().Applier().Delete(ctx, "batch/v1", "Job", env.Namespace, jobName)
	if err := m.cluster.Client().Applier().Apply(ctx, job); err != nil {
		fail(err)
		return
	}

	if err := m.waitForJob(ctx, env.Namespace, jobName); err != nil {
		fail(err)
		return
	}

	size, err := storage.Stat(ctx, backup.Location)
	if err != nil {
		// The job reported success, so a failure to stat is a storage quirk
		// rather than a failed backup; record it without the size.
		m.log.Warn("could not read the backup's size", "backup", backup.ID, "error", err)
	}
	if err := m.db.FinishBackup(ctx, backup.ID, "succeeded", backup.Location, size, ""); err != nil {
		m.log.Warn("could not record the finished backup", "backup", backup.ID, "error", err)
	}
	m.publish(ctx, record.ID)
	m.tellPlugins(ctx, backup, record.Name, nil)
	m.log.Info("backup finished", "backup", backup.ID, "database", record.Name, "bytes", size)

	m.applyRetention(ctx, storage, "database", record.ID)
}

// applyRetention deletes backups beyond the configured count.
func (m *Manager) applyRetention(ctx context.Context, storage *Storage, targetType, targetID string) {
	policy, err := m.db.GetBackupPolicy(ctx, targetType, targetID)
	if err != nil {
		return
	}
	expired, err := m.db.ExpiredBackups(ctx, targetType, targetID, policy.Retention)
	if err != nil {
		m.log.Warn("could not list expired backups", targetType, targetID, "error", err)
		return
	}
	for _, old := range expired {
		if old.Location != "" {
			if err := storage.Remove(ctx, old.Location); err != nil {
				// A backup that cannot be deleted from storage stays in the
				// list, so it is not silently forgotten while still costing money.
				m.log.Warn("could not delete an expired backup from storage",
					"backup", old.ID, "error", err)
				continue
			}
		}
		if err := m.db.DeleteBackup(ctx, old.ID); err != nil {
			m.log.Warn("could not delete an expired backup record", "backup", old.ID, "error", err)
		}
	}
}

// Restore puts a backup back.
func (m *Manager) Restore(ctx context.Context, backupID string, overwrite bool) (store.Operation, error) {
	backup, err := m.db.GetBackup(ctx, backupID)
	if err != nil {
		return store.Operation{}, err
	}
	if backup.Status != "succeeded" {
		return store.Operation{}, errdoc.BadRequest("That backup did not finish successfully, so it cannot be restored.")
	}
	record, err := m.db.GetDatabase(ctx, backup.TargetID)
	if err != nil {
		return store.Operation{}, err
	}

	// Restoring over live data is destructive and irreversible, so it has to be
	// asked for explicitly rather than being the default.
	//
	// A failure to read the links is not an empty list of them. This used to
	// treat it as one, which turned the check that stands between a restore and
	// somebody else's live database into a check that disappears when a query
	// fails.
	if !overwrite {
		links, err := m.db.ListLinksForDatabase(ctx, record.ID)
		if err != nil {
			return store.Operation{}, err
		}
		if len(links) > 0 {
			return store.Operation{}, errdoc.RestoreRefused(record.Name).
				With("apps_using_it", fmt.Sprint(len(links)))
		}
	}

	env, err := m.db.GetEnvironment(ctx, record.EnvironmentID)
	if err != nil {
		return store.Operation{}, err
	}
	teamID, err := m.db.TeamIDForDatabase(ctx, record.ID)
	if err != nil {
		return store.Operation{}, err
	}

	op := store.Operation{
		TeamID: teamID, Kind: "database.restore",
		TargetType: "database", TargetID: record.ID,
	}
	if err := m.db.CreateOperation(ctx, &op, []string{"prepare", "restore", "verify"}); err != nil {
		return store.Operation{}, err
	}

	go m.runRestore(context.WithoutCancel(ctx), op, backup, record, env)
	return op, nil
}

func (m *Manager) runRestore(ctx context.Context, op store.Operation, backup store.Backup, record store.Database, env store.Environment) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Hour)
	defer cancel()

	fail := func(step string, err error) {
		problem := errdoc.From(err)
		m.log.Error("restore failed", "operation", op.ID, "step", step, "error", err)
		_ = m.db.SetStepStatus(ctx, op.ID, step, store.StepFailed,
			store.StepNote{Message: problem.Title, Key: "problem:" + problem.Code, Args: problem.Args.Title},
			problem.Text())
		_ = m.db.SetOperationStatus(ctx, op.ID, store.OpFailed, problem.Code, problem.Error())
		m.hub.Publish(events.OperationTopic(op.ID), "failed", problem)
	}
	// A restore writes over live data. If the panel dies partway through one,
	// the operation sits at "restoring" forever and nobody can tell from the
	// interface whether the data is the old one, the new one, or neither.
	defer runsafe.Recover(m.log, "restore "+op.ID, func(err error) { fail("restore", err) })

	_ = m.db.SetOperationStatus(ctx, op.ID, store.OpRunning, "", "")

	_ = m.db.SetStepStatus(ctx, op.ID, "prepare", store.StepRunning, store.StepNote{}, "")
	storage, err := LoadStorage(ctx, m.db, m.keyring)
	if err != nil {
		fail("prepare", err)
		return
	}
	presigned, err := storage.PresignGet(ctx, backup.Location)
	if err != nil {
		fail("prepare", err)
		return
	}
	jobName := JobName("restore-"+record.Slug, backup.ID)
	secretName := jobName + "-url"
	defer func() {
		_ = m.cluster.Client().Applier().Delete(ctx, "v1", "Secret", env.Namespace, secretName)
	}()
	if err := m.cluster.Client().Applier().Apply(ctx,
		URLSecret(secretName, env.Namespace, presigned)); err != nil {
		fail("prepare", err)
		return
	}
	_ = m.db.SetStepStatus(ctx, op.ID, "prepare", store.StepSucceeded, store.StepNote{Message: "Ready to restore", Key: "readyToRestore"}, "")

	_ = m.db.SetStepStatus(ctx, op.ID, "restore", store.StepRunning, store.StepNote{}, "")
	job, err := BuildJob(JobSpec{
		Name:              jobName,
		Namespace:         env.Namespace,
		Engine:            record.Engine,
		CredentialsSecret: kube.ResourceName(record.Slug, "credentials"),
		URLSecret:         secretName,
		BackupID:          backup.ID,
		Restore:           true,
	})
	if err != nil {
		fail("restore", err)
		return
	}
	_ = m.cluster.Client().Applier().Delete(ctx, "batch/v1", "Job", env.Namespace, jobName)
	if err := m.cluster.Client().Applier().Apply(ctx, job); err != nil {
		fail("restore", err)
		return
	}
	if err := m.waitForJob(ctx, env.Namespace, jobName); err != nil {
		fail("restore", err)
		return
	}
	restored, restoredArgs := errdoc.Sprintf("Restored from the backup taken on %s",
		backup.CreatedAt.Format("2 January 2006 at 15:04"))
	_ = m.db.SetStepStatus(ctx, op.ID, "restore", store.StepSucceeded,
		store.StepNote{Message: restored, Key: "restored", Args: restoredArgs}, "")

	_ = m.db.SetStepStatus(ctx, op.ID, "verify", store.StepRunning, store.StepNote{}, "")
	// Restarting the apps that use this database clears any connection pool
	// holding a transaction against the old data.
	links, err := m.db.ListLinksForDatabase(ctx, record.ID)
	if err == nil {
		for _, link := range links {
			app, err := m.db.GetApp(ctx, link.AppID)
			if err != nil {
				continue
			}
			if err := m.cluster.RestartApp(ctx, env.Namespace, app.Slug); err != nil {
				m.log.Warn("could not restart an app after the restore", "app", app.ID, "error", err)
			}
		}
	}
	restarted, restartedArgs := errdoc.Sprintf("Restarted %d app(s) so they reconnect", len(links))
	_ = m.db.SetStepStatus(ctx, op.ID, "verify", store.StepSucceeded,
		store.StepNote{Message: restarted, Key: "appsRestarted", Args: restartedArgs}, "")

	_ = m.db.SetOperationStatus(ctx, op.ID, store.OpSucceeded, "", "")
	m.hub.Publish(events.OperationTopic(op.ID), "operation", op)
}

// waitForJob waits for a backup or restore Job and explains a failure.
func (m *Manager) waitForJob(ctx context.Context, namespace, name string) error {
	clientset := m.cluster.Client().Clientset()
	deadline := time.Now().Add(2 * time.Hour)

	for {
		job, err := clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read the job's status: %w", err)
		}
		if job.Status.Succeeded > 0 {
			return nil
		}
		if job.Status.Failed > 0 {
			return errdoc.BackupFailed(name, m.jobFailureReason(ctx, namespace, name))
		}
		if time.Now().After(deadline) {
			return errdoc.BackupFailed(name, "it did not finish within two hours")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// jobFailureReason reads the last lines of a failed job's log, which is where
// the actual reason is.
//
// The container that failed has to be named. A backup dumps in an init
// container and uploads in the main one, and asking for a pod's log without
// saying which container asks the main one — which, when the dump failed,
// never started and has nothing to say.
func (m *Manager) jobFailureReason(ctx context.Context, namespace, jobName string) string {
	pods, err := m.cluster.Client().Clientset().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil || len(pods.Items) == 0 {
		return "the job failed and its output could not be read"
	}
	pod := pods.Items[0]

	for _, container := range failedContainers(pod) {
		if output := m.containerLog(ctx, namespace, pod.Name, container); output != "" {
			return output
		}
	}
	return "the job failed without printing anything"
}

// failedContainers names the containers worth reading a log from, the one that
// actually exited badly first.
func failedContainers(pod corev1.Pod) []string {
	var failed, rest []string
	for _, status := range append(append([]corev1.ContainerStatus{},
		pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		terminated := status.State.Terminated
		switch {
		case terminated != nil && terminated.ExitCode != 0:
			failed = append(failed, status.Name)
		case status.State.Waiting != nil && status.State.Waiting.Message != "":
			// A container that never started says why in its status rather
			// than in a log: a missing Secret key, an image that will not pull.
			failed = append(failed, status.Name)
		default:
			rest = append(rest, status.Name)
		}
	}
	return append(failed, rest...)
}

func (m *Manager) containerLog(ctx context.Context, namespace, pod, container string) string {
	tail := int64(30)
	stream, err := m.cluster.Client().StreamClientset().CoreV1().Pods(namespace).
		GetLogs(pod, &corev1.PodLogOptions{Container: container, TailLines: &tail}).Stream(ctx)
	if err != nil {
		return ""
	}
	defer stream.Close()

	output, err := io.ReadAll(io.LimitReader(stream, 8<<10))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func (m *Manager) publish(ctx context.Context, databaseID string) {
	if teamID, err := m.db.TeamIDForDatabase(ctx, databaseID); err == nil {
		backups, err := m.db.ListBackups(ctx, "database", databaseID, 10)
		if err == nil {
			m.hub.Publish(events.TeamTopic(teamID), "backups",
				map[string]any{"database_id": databaseID, "backups": backups})
		}
	}
}

// RunScheduled is called by the scheduler for every enabled policy that is due.
func (m *Manager) RunScheduled(ctx context.Context) {
	m.RunScheduledAt(ctx, []time.Time{time.Now().UTC()})
}

// RunScheduledAt runs the policies due in any of the given minutes.
//
// More than one minute because the scheduler's tick can arrive late — a slow
// minute, a restart — and a policy due in a minute nobody evaluated is a backup
// that silently did not happen. A policy that matches several of them still
// runs once: catching up is not a reason to take the same backup twice.
func (m *Manager) RunScheduledAt(ctx context.Context, minutes []time.Time) {
	policies, err := m.db.ListEnabledBackupPolicies(ctx)
	if err != nil {
		m.log.Warn("could not read the backup schedules", "error", err)
		return
	}
	for _, policy := range policies {
		due := false
		for _, minute := range minutes {
			if dueNow(policy.Schedule, minute.UTC()) {
				due = true
				break
			}
		}
		if !due {
			continue
		}
		if _, err := m.Run(ctx, policy.TargetType, policy.TargetID, "scheduled"); err != nil {
			m.log.Error("a scheduled backup could not start",
				"target", policy.TargetID, "error", err)
		}
	}
}

// tellPlugins says a backup finished, whichever way it went.
//
// Four places finish one — a database and a volume, each succeeding and each
// failing — and a plugin subscribed to backup.completed or backup.failed was
// told by none of them. One helper rather than four call sites written out,
// so a fifth kind of backup cannot quietly be added without an event.
func (m *Manager) tellPlugins(ctx context.Context, backup store.Backup, name string, failure error) {
	teamID, err := m.teamForBackup(ctx, backup)
	if err != nil || teamID == "" {
		return
	}
	data := map[string]any{
		"backup_id": backup.ID, "target_type": backup.TargetType,
		"target_id": backup.TargetID, "target": name, "kind": backup.Kind,
	}
	if failure != nil {
		data["error"] = errdoc.From(failure).Error()
		m.Plugins.Notify(ctx, plugins.EventBackupFailed, teamID, data)
		return
	}
	m.Plugins.Notify(ctx, plugins.EventBackupCompleted, teamID, data)
}

// teamForBackup resolves whose backup this was, through whichever kind of
// thing it copied.
func (m *Manager) teamForBackup(ctx context.Context, backup store.Backup) (string, error) {
	switch backup.TargetType {
	case "database":
		return m.db.TeamIDForDatabase(ctx, backup.TargetID)
	case "volume":
		volume, err := m.db.GetVolume(ctx, backup.TargetID)
		if err != nil {
			return "", err
		}
		return m.db.TeamIDForApp(ctx, volume.AppID)
	}
	return "", nil
}

// notifyFailure tells the team a backup did not happen.
//
// A backup that silently fails is the worst kind: it is only discovered when a
// restore is attempted, which is the moment it matters most.
func (m *Manager) notifyFailure(ctx context.Context, record store.Database, problem *errdoc.Problem) {
	if m.notifier == nil {
		return
	}
	teamID, err := m.db.TeamIDForDatabase(ctx, record.ID)
	if err != nil {
		m.log.Warn("could not work out which team to notify", "database", record.ID, "error", err)
		return
	}
	m.notifier.Notify(ctx, teamID, notify.EventBackupFailed, notify.Message{
		Title:  "Backing up " + record.Name + " failed",
		Body:   problem.Error() + "\n\n" + problem.Fix,
		Level:  "error",
		Path:   "/databases/" + record.ID,
		Fields: map[string]string{"Database": record.Name, "Reason": problem.Code},
	})
}

// runVolumeBackup copies an app's volume to storage.
//
// The panel used to refuse, which was honest and still a gap: "back up your
// database" is half an answer to somebody who has just lost a disk, and an
// app's uploads had nowhere to go.
func (m *Manager) runVolumeBackup(ctx context.Context, volumeID, kind string) (store.Backup, error) {
	volume, err := m.db.GetVolume(ctx, volumeID)
	if err != nil {
		return store.Backup{}, err
	}
	app, err := m.db.GetApp(ctx, volume.AppID)
	if err != nil {
		return store.Backup{}, err
	}
	env, err := m.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return store.Backup{}, err
	}

	storage, err := LoadStorage(ctx, m.db, m.keyring)
	if err != nil {
		return store.Backup{}, err
	}

	backup := store.Backup{
		TargetType: "volume", TargetID: volumeID,
		Status: "running", Kind: kind,
	}
	backup.Location = ObjectKey("volume", volumeID, app.Slug+"-"+volume.Name, time.Now())
	if err := m.db.CreateBackup(ctx, &backup); err != nil {
		return store.Backup{}, err
	}

	go m.runVolume(context.WithoutCancel(ctx), storage, backup, app, env, volume)
	return backup, nil
}

func (m *Manager) runVolume(ctx context.Context, storage *Storage, backup store.Backup,
	app store.App, env store.Environment, volume store.Volume,
) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Hour)
	defer cancel()

	fail := func(err error) {
		problem := errdoc.From(err)
		m.log.Error("volume backup failed",
			"backup", backup.ID, "volume", volume.ID, "app", app.ID, "error", err)
		_ = m.db.FinishBackup(ctx, backup.ID, "failed", backup.Location, 0, problem.Error())
		m.publish(ctx, app.ID)
		m.tellPlugins(ctx, backup, app.Name+" / "+volume.Name, problem)
	}
	defer runsafe.Recover(m.log, "volume backup "+backup.ID, fail)

	presigned, err := storage.PresignPut(ctx, backup.Location)
	if err != nil {
		fail(err)
		return
	}

	jobName := JobName("backup-"+app.Slug+"-"+volume.Name, backup.ID)
	secretName := jobName + "-url"
	defer func() {
		if err := m.cluster.Client().Applier().Delete(ctx, "v1", "Secret", env.Namespace, secretName); err != nil {
			m.log.Warn("could not remove the backup URL secret", "backup", backup.ID, "error", err)
		}
	}()

	if err := m.cluster.Client().Applier().Apply(ctx,
		URLSecret(secretName, env.Namespace, presigned)); err != nil {
		fail(err)
		return
	}

	job, err := BuildVolumeJob(VolumeJobSpec{
		Name:         jobName,
		Namespace:    env.Namespace,
		ClaimName:    kube.ResourceName(app.Slug, volume.Name),
		URLSecret:    secretName,
		BackupID:     backup.ID,
		CoLocateWith: m.coLocateWith(ctx, env.Namespace, app),
	})
	if err != nil {
		fail(err)
		return
	}
	_ = m.cluster.Client().Applier().Delete(ctx, "batch/v1", "Job", env.Namespace, jobName)
	if err := m.cluster.Client().Applier().Apply(ctx, job); err != nil {
		fail(err)
		return
	}

	if err := m.waitForJob(ctx, env.Namespace, jobName); err != nil {
		fail(err)
		return
	}

	size, err := storage.Stat(ctx, backup.Location)
	if err != nil {
		m.log.Warn("could not read the backup's size", "backup", backup.ID, "error", err)
	}
	if err := m.db.FinishBackup(ctx, backup.ID, "succeeded", backup.Location, size, ""); err != nil {
		m.log.Warn("could not record the finished backup", "backup", backup.ID, "error", err)
	}
	m.publish(ctx, app.ID)
	m.tellPlugins(ctx, backup, app.Name+" / "+volume.Name, nil)
	m.log.Info("volume backup finished",
		"backup", backup.ID, "app", app.Name, "volume", volume.Name, "bytes", size)

	m.applyRetention(ctx, storage, "volume", volume.ID)
}

// coLocateWith returns the labels of the app's running pods, or nil.
//
// A volume is ReadWriteOnce: this pod and the app's pod must be on the same
// node, and the scheduler only knows that if it is told. With the storage class
// k3s ships the PersistentVolume carries its own node affinity and this changes
// nothing; on a networked volume already attached elsewhere, without it the pod
// sits in a Multi-Attach error until it times out.
//
// Nil when nothing is running, because an affinity to pods that do not exist
// can never be satisfied and would leave the backup Pending forever.
func (m *Manager) coLocateWith(ctx context.Context, namespace string, app store.App) map[string]string {
	status, err := m.cluster.AppStatus(ctx, namespace, app.Slug)
	if err != nil || len(status.Instances) == 0 {
		return nil
	}
	running := false
	for _, instance := range status.Instances {
		if instance.Status == "Running" {
			running = true
		}
	}
	if !running {
		return nil
	}
	return map[string]string{
		"app.kubernetes.io/name":     app.Slug,
		"app.kubernetes.io/instance": app.ID,
	}
}
