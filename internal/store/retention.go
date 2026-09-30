package store

import (
	"context"
	"fmt"
	"time"
)

// Keeping the database from growing forever.
//
// Backups have a retention policy and build logs are pruned with their
// deployment. Deployment rows, audit entries and finished operations were not:
// a panel deploying twenty times a day writes rows nobody will read again and
// removed none of them, and this database is a file on one node's disk. Nothing
// breaks suddenly — it grows, every query over it gets slower, and the backup
// of it gets larger, which is the shape of problem that is only ever noticed
// long after it started.
//
// Deleted rows leave free pages that SQLite reuses rather than returning to the
// filesystem, so the file stops growing rather than shrinking. That is the
// intended outcome: a VACUUM rewrites the whole database and needs room for a
// second copy of it, which is not something to do unattended on the disk this
// is trying to protect.

// Retention says how much history to keep.
type Retention struct {
	// DeploymentsPerApp is how many deployment records an app keeps. Their
	// build logs go with them.
	DeploymentsPerApp int
	// OperationDays is how long a finished operation — adding a server,
	// removing one — is kept.
	OperationDays int
	// AuditDays is how long an audit entry is kept. It is deliberately much
	// longer than the rest: an audit log that forgets is most of the way to not
	// having one, and a shorter window should be somebody's decision rather
	// than a default.
	AuditDays int
}

// DefaultRetention is what a panel uses when nobody has said otherwise.
func DefaultRetention() Retention {
	return Retention{DeploymentsPerApp: 50, OperationDays: 90, AuditDays: 365}
}

// PruneReport says what a pass removed, so the log has a number in it rather
// than "maintenance ran".
type PruneReport struct {
	Deployments int64
	Operations  int64
	AuditEvents int64
}

// Empty reports whether the pass found nothing to do, which is the ordinary
// case and not worth logging.
func (r PruneReport) Empty() bool {
	return r.Deployments == 0 && r.Operations == 0 && r.AuditEvents == 0
}

func (r PruneReport) String() string {
	return fmt.Sprintf("%d deployments, %d operations, %d audit entries",
		r.Deployments, r.Operations, r.AuditEvents)
}

// Prune removes history past the retention window.
//
// It is safe to run at any time and safe to run again: everything it deletes is
// finished, and nothing it deletes is referenced by something that is not.
func (db *DB) Prune(ctx context.Context, keep Retention) (PruneReport, error) {
	if keep.DeploymentsPerApp < 1 {
		keep.DeploymentsPerApp = DefaultRetention().DeploymentsPerApp
	}
	if keep.OperationDays < 1 {
		keep.OperationDays = DefaultRetention().OperationDays
	}
	if keep.AuditDays < 1 {
		keep.AuditDays = DefaultRetention().AuditDays
	}

	var report PruneReport
	now := time.Now().UTC()

	// Deployments, oldest first per app. A deployment that has not finished is
	// never touched however old it looks: a panel that was killed mid-build
	// leaves one behind, and deleting it would take the log that says what
	// happened with it.
	result, err := db.Exec(ctx, `
		DELETE FROM deployments WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY app_id ORDER BY number DESC) AS rn
				FROM deployments
				WHERE status IN ('succeeded','failed','cancelled','superseded')
			) WHERE rn > ?
		)`, keep.DeploymentsPerApp)
	if err != nil {
		return report, fmt.Errorf("prune deployments: %w", err)
	}
	report.Deployments, _ = result.RowsAffected()

	// Finished operations. An unfinished one is either running now or was
	// interrupted, and both are things somebody may still be looking at.
	before := now.Add(-time.Duration(keep.OperationDays) * 24 * time.Hour)
	result, err = db.Exec(ctx, `
		DELETE FROM operations
		WHERE status IN ('succeeded','failed','cancelled') AND created_at < ?`, FormatTime(before))
	if err != nil {
		return report, fmt.Errorf("prune operations: %w", err)
	}
	report.Operations, _ = result.RowsAffected()

	auditBefore := now.Add(-time.Duration(keep.AuditDays) * 24 * time.Hour)
	result, err = db.Exec(ctx, `DELETE FROM audit_events WHERE at < ?`, FormatTime(auditBefore))
	if err != nil {
		return report, fmt.Errorf("prune audit events: %w", err)
	}
	report.AuditEvents, _ = result.RowsAffected()

	return report, nil
}

// DueEvery reports whether a periodic job should run now, and records that it
// did.
//
// The last run is a row rather than a timer because "due" has to survive a
// restart: a panel restarted daily would never reach a weekly timer, and the
// maintenance it was waiting for would simply never happen.
//
// It records the time before the work rather than after. A pass that fails half
// way through has still done what it did, and retrying it on the next tick
// would repeat that every minute; the next window is soon enough, and the
// failure is in the log.
func (db *DB) DueEvery(ctx context.Context, key string, interval time.Duration) bool {
	raw, _, err := db.GetSetting(ctx, key)
	if err != nil {
		return false
	}
	if raw != "" {
		if last, err := time.Parse(time.RFC3339, raw); err == nil && time.Since(last) < interval {
			return false
		}
	}
	return db.SetSetting(ctx, key, time.Now().UTC().Format(time.RFC3339), false, "system") == nil
}
