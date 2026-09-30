package backup

import (
	"context"
	"fmt"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/runsafe"
	"skifity/internal/store"
)

// Putting a volume back.
//
// The job that does it has existed since volume backups were added —
// VolumeJobSpec.Restore inverts the direction, downloads the archive and
// unpacks it — and nothing ever called it. A backup that cannot be restored is
// not a backup, it is a file somebody is paying to store, and the checklist row
// this belongs to is called "Backup and restore, proven".
//
// The hard part is not the copy. It is that the volume is ReadWriteOnce and the
// app is holding it: unpacking a tar underneath a process with files open on
// the same disk is how a restore turns one bad day into two. So the app is
// stopped first, and put back exactly as it was afterwards — including when the
// restore fails, which is the case that matters.

// RestoreVolume puts an archive back into the volume it came from.
func (m *Manager) RestoreVolume(ctx context.Context, backupID string, overwrite bool) (store.Operation, error) {
	backup, err := m.db.GetBackup(ctx, backupID)
	if err != nil {
		return store.Operation{}, err
	}
	if backup.TargetType != "volume" {
		return store.Operation{}, errdoc.BadRequest("That backup is not a volume backup.")
	}
	if backup.Status != "succeeded" {
		return store.Operation{}, errdoc.BadRequest("That backup did not finish successfully, so it cannot be restored.")
	}

	volume, err := m.db.GetVolume(ctx, backup.TargetID)
	if err != nil {
		return store.Operation{}, err
	}
	app, err := m.db.GetApp(ctx, volume.AppID)
	if err != nil {
		return store.Operation{}, err
	}
	env, err := m.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return store.Operation{}, err
	}

	// Everything on the disk is replaced by what was in the archive, and the
	// app is stopped while that happens. Neither is a surprise somebody should
	// get from pressing a button, so it is asked for rather than assumed.
	if !overwrite {
		return store.Operation{}, errdoc.New("backup.restore_refused", "This would replace what is on the disk now").
			WithCause("Restoring %s writes the archive over everything currently in the volume, and stops %s while it does.", volume.Name, app.Name).
			WithImpact("Nothing has been changed.").
			WithFix("Take a backup of what is there now if you might want it, then confirm the restore.").
			WithStatus(409)
	}

	teamID, err := m.db.TeamIDForApp(ctx, app.ID)
	if err != nil {
		return store.Operation{}, err
	}

	op := store.Operation{
		TeamID: teamID, Kind: "volume.restore",
		TargetType: "volume", TargetID: volume.ID,
	}
	if err := m.db.CreateOperation(ctx, &op, []string{"stop", "restore", "start"}); err != nil {
		return store.Operation{}, err
	}

	go m.runVolumeRestore(context.WithoutCancel(ctx), op, backup, app, env, volume)
	return op, nil
}

func (m *Manager) runVolumeRestore(ctx context.Context, op store.Operation, backup store.Backup,
	app store.App, env store.Environment, volume store.Volume,
) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Hour)
	defer cancel()

	fail := func(step string, err error) {
		problem := errdoc.From(err)
		m.log.Error("volume restore failed",
			"operation", op.ID, "step", step, "volume", volume.ID, "error", err)
		_ = m.db.SetStepStatus(ctx, op.ID, step, store.StepFailed,
			store.StepNote{Message: problem.Title, Key: "problem:" + problem.Code, Args: problem.Args.Title},
			problem.Text())
		_ = m.db.SetOperationStatus(ctx, op.ID, store.OpFailed, problem.Code, problem.Error())
		m.hub.Publish(events.OperationTopic(op.ID), "failed", problem)
	}
	defer runsafe.Recover(m.log, "volume restore "+op.ID, func(err error) { fail("restore", err) })

	_ = m.db.SetOperationStatus(ctx, op.ID, store.OpRunning, "", "")

	// --- stop the app ------------------------------------------------------

	_ = m.db.SetStepStatus(ctx, op.ID, "stop", store.StepRunning, store.StepNote{}, "")
	client := m.cluster.Client()
	// The Deployment is named after the app's slug, which is what AppSpec.Name
	// renders to. Not ResourceName(slug, ""): that is for the objects that
	// hang off an app, like its PersistentVolumeClaims.
	deploymentName := app.Slug

	was, err := client.ScaleDeployment(ctx, env.Namespace, deploymentName, 0)
	if err != nil {
		fail("stop", err)
		return
	}
	// Whatever happens next, the app goes back to the size it was. A restore
	// that fails and leaves the app at zero instances is an outage caused by
	// the thing that was supposed to end one.
	defer func() {
		if _, err := client.ScaleDeployment(context.WithoutCancel(ctx), env.Namespace, deploymentName, was); err != nil {
			m.log.Error("could not start the app again after a volume restore",
				"app", app.ID, "replicas", was, "error", err)
		}
	}()

	if err := client.WaitForNoPods(ctx, env.Namespace, deploymentName, 5*time.Minute); err != nil {
		fail("stop", err)
		return
	}
	_ = m.db.SetStepStatus(ctx, op.ID, "stop", store.StepSucceeded,
		store.StepNote{Message: "The app is stopped", Key: "appStopped"}, "")

	// --- unpack the archive ------------------------------------------------

	_ = m.db.SetStepStatus(ctx, op.ID, "restore", store.StepRunning, store.StepNote{}, "")
	storage, err := LoadStorage(ctx, m.db, m.keyring)
	if err != nil {
		fail("restore", err)
		return
	}
	presigned, err := storage.PresignGet(ctx, backup.Location)
	if err != nil {
		fail("restore", err)
		return
	}

	jobName := JobName("restore-"+app.Slug+"-"+volume.Name, backup.ID)
	secretName := jobName + "-url"
	defer func() {
		if err := client.Applier().Delete(context.WithoutCancel(ctx), "v1", "Secret", env.Namespace, secretName); err != nil {
			m.log.Warn("could not remove the restore URL secret", "operation", op.ID, "error", err)
		}
	}()
	if err := client.Applier().Apply(ctx, URLSecret(secretName, env.Namespace, presigned)); err != nil {
		fail("restore", err)
		return
	}

	job, err := BuildVolumeJob(VolumeJobSpec{
		Name:      jobName,
		Namespace: env.Namespace,
		ClaimName: kube.ResourceName(app.Slug, volume.Name),
		URLSecret: secretName,
		Restore:   true,
		BackupID:  backup.ID,
		// No co-location: the app is stopped, so there is no pod to sit beside
		// and an affinity to pods that do not exist can never be satisfied.
	})
	if err != nil {
		fail("restore", err)
		return
	}
	_ = client.Applier().Delete(ctx, "batch/v1", "Job", env.Namespace, jobName)
	if err := client.Applier().Apply(ctx, job); err != nil {
		fail("restore", err)
		return
	}
	if err := m.waitForJob(ctx, env.Namespace, jobName); err != nil {
		fail("restore", err)
		return
	}
	_ = m.db.SetStepStatus(ctx, op.ID, "restore", store.StepSucceeded,
		store.StepNote{Message: "The archive is unpacked", Key: "archiveUnpacked"}, "")

	// --- start it again ----------------------------------------------------

	_ = m.db.SetStepStatus(ctx, op.ID, "start", store.StepRunning, store.StepNote{}, "")
	if _, err := client.ScaleDeployment(ctx, env.Namespace, deploymentName, was); err != nil {
		fail("start", err)
		return
	}
	if err := client.WaitForRollout(ctx, env.Namespace, deploymentName, 10*time.Minute); err != nil {
		fail("start", err)
		return
	}
	_ = m.db.SetStepStatus(ctx, op.ID, "start", store.StepSucceeded,
		store.StepNote{
			Message: fmt.Sprintf("%s is running again", app.Name),
			Key:     "appRunningAgain", Args: []string{app.Name},
		}, "")

	_ = m.db.SetOperationStatus(ctx, op.ID, store.OpSucceeded, "", "")
	// "operation", with the operation itself, is what every other finished
	// operation publishes and what the interface listens for. A name of its
	// own would be an event nobody hears.
	if finished, err := m.db.GetOperation(ctx, op.ID); err == nil {
		m.hub.Publish(events.OperationTopic(op.ID), "operation", finished)
	}
	m.publish(ctx, app.ID)
	m.log.Info("volume restore finished",
		"operation", op.ID, "app", app.Name, "volume", volume.Name, "backup", backup.ID)
}
