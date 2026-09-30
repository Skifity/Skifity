package backup

import (
	"time"

	"skifity/internal/cron"
)

// The cron parser lives in internal/cron, because an app's scheduled commands
// need the same one and a schedule that means two different things in two
// places is worse than no schedule at all. These aliases keep the backup code
// reading the way it did.

// Schedule is a parsed cron expression.
type Schedule = cron.Schedule

// ParseSchedule reads a five-field cron expression.
func ParseSchedule(expression string) (Schedule, error) { return cron.ParseSchedule(expression) }

// NextRun is when an expression next matches after a moment.
func NextRun(expression string, after time.Time) (time.Time, error) {
	return cron.NextRun(expression, after)
}

func dueNow(expression string, now time.Time) bool { return cron.DueNow(expression, now) }
