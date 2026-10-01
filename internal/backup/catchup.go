package backup

import (
	"context"
	"time"

	"skifity/internal/cron"
	"skifity/internal/errdoc"
	"skifity/internal/notify"
	"skifity/internal/store"
)

// catchUpHorizon is how far back a missed backup is looked for. A week covers
// a panel that was down over a long weekend; a policy whose last time is
// further back than that is weekly or rarer, and its next time is soon.
const catchUpHorizon = 7 * 24 * time.Hour

// Missed is one scheduled backup whose time passed with nothing taken.
type Missed struct {
	Policy store.BackupPolicy
	Due    time.Time
}

// CatchUp takes, once, each scheduled backup whose time passed while the panel
// was not running, and tells the team it was late.
//
// The minute tick catches up on the minutes it stepped over while it was
// running, and that is all it can know about. A panel that was stopped over
// 03:00 — an upgrade, a node reboot, a crash — started again at 03:10 with
// its clock at 03:10, and the nightly backup silently did not happen until
// 03:00 the next night. Coolify runs a missed backup late; this does too, and
// says so rather than letting a late backup look like an on-time one.
//
// A backup of the target started at or after the time it was due, whichever
// way it ended, means it was not missed: a failure was already reported.
func (m *Manager) CatchUp(ctx context.Context, now time.Time) []Missed {
	policies, err := m.db.ListEnabledBackupPolicies(ctx)
	if err != nil {
		m.log.Warn("could not read the backup schedules to catch up on", "error", err)
		return nil
	}
	var missed []Missed
	for _, policy := range policies {
		due, ok := lastDue(policy, now)
		if !ok {
			continue
		}
		latest, err := m.db.ListBackups(ctx, policy.TargetType, policy.TargetID, 1)
		if err != nil {
			m.log.Warn("could not read a target's backups", "target", policy.TargetID, "error", err)
			continue
		}
		if len(latest) > 0 && !latest[0].CreatedAt.Before(due) {
			continue
		}
		missed = append(missed, Missed{Policy: policy, Due: due})
		m.takeMissed(ctx, policy, due)
	}
	return missed
}

// lastDue is the most recent minute before now's own a policy was due in,
// no earlier than the policy was last changed and no further back than the
// horizon. Now's minute is the tick's, which runs it on time.
func lastDue(policy store.BackupPolicy, now time.Time) (time.Time, bool) {
	schedule, err := cron.ParseSchedule(policy.Schedule)
	if err != nil {
		return time.Time{}, false
	}
	now = now.UTC().Truncate(time.Minute)
	floor := now.Add(-catchUpHorizon)
	if policy.UpdatedAt.After(floor) {
		floor = policy.UpdatedAt.UTC()
	}
	for at := now.Add(-time.Minute); at.After(floor); at = at.Add(-time.Minute) {
		if schedule.Matches(at) {
			return at, true
		}
	}
	return time.Time{}, false
}

// takeMissed starts the late backup and tells the team, with the reason when
// it could not start.
func (m *Manager) takeMissed(ctx context.Context, policy store.BackupPolicy, due time.Time) {
	m.log.Info("taking a scheduled backup that was missed",
		"target", policy.TargetID, "due", due.Format(time.RFC3339))
	taken, runErr := m.Run(ctx, policy.TargetType, policy.TargetID, "scheduled")

	// A stopped database's backup was skipped, not missed: there was nothing
	// new to copy then, and there is nothing now.
	if m.notifier == nil || (runErr == nil && taken.Status == "skipped") {
		return
	}
	probe := store.Backup{TargetType: policy.TargetType, TargetID: policy.TargetID}
	teamID, projectID, err := m.ownerOfBackup(ctx, probe)
	if err != nil || teamID == "" {
		return
	}
	name := m.targetName(ctx, policy)
	msg := notify.Message{
		Title: "The " + due.Format("15:04 MST") + " backup of " + name + " was missed",
		Body: "The panel was not running when it was due, on " + due.Format("Monday 2 January") +
			". A backup was started now instead.",
		Level:     "warning",
		Path:      m.backupPath(ctx, probe),
		Fields:    map[string]string{"Backup of": name, "Was due": due.Format(time.RFC3339)},
		ProjectID: projectID,
	}
	if runErr != nil {
		problem := errdoc.From(runErr)
		msg.Body = "The panel was not running when it was due, on " + due.Format("Monday 2 January") +
			", and it could not be taken now either: " + problem.Error() + "\n\n" + problem.Fix
		msg.Level = "error"
		msg.Fields["Reason"] = problem.Code
	}
	m.notifier.Notify(ctx, teamID, notify.EventBackupMissed, msg)
}

// targetName is what a person calls the thing a policy backs up.
func (m *Manager) targetName(ctx context.Context, policy store.BackupPolicy) string {
	switch policy.TargetType {
	case "database":
		if record, err := m.db.GetDatabase(ctx, policy.TargetID); err == nil {
			return record.Name
		}
	case "volume":
		if volume, err := m.db.GetVolume(ctx, policy.TargetID); err == nil {
			if app, err := m.db.GetApp(ctx, volume.AppID); err == nil {
				return app.Name + " / " + volume.Name
			}
			return volume.Name
		}
	}
	return policy.TargetID
}
