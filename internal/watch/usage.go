package watch

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"skifity/internal/api"
	"skifity/internal/notify"
	"skifity/internal/store"
)

// What apps used, and when to say something about it.

// SampleRetention is how long an app's minute-by-minute usage is kept.
const SampleRetention = 72 * time.Hour

// sustained is how many minutes in a row a threshold has to be crossed before
// it is said: a garbage collection or a deploy's first seconds are not news.
const sustained = 3

// restartWindow is how far back restarts are counted.
const restartWindow = 10 * time.Minute

// recordUsage keeps one minute of what an app used.
func (w *Watcher) recordUsage(ctx context.Context, app store.DeployedApp, status api.AppRuntimeStatus, at time.Time) {
	sample := store.AppSample{At: at, Ready: status.ReadyReplicas, Desired: status.DesiredReplicas}
	for _, instance := range status.Instances {
		sample.CPUM += instance.CPUM
		sample.MemoryMB += instance.MemoryMB
		sample.Restarts += instance.Restarts
		sample.CPUPeakPct = max(sample.CPUPeakPct, percentOf(instance.CPUM, int64(app.CPULimitM)))
		sample.MemoryPeakPct = max(sample.MemoryPeakPct, percentOf(instance.MemoryMB, int64(app.MemLimitMB)))
	}
	if err := w.db.RecordAppSample(ctx, app.ID, sample); err != nil {
		w.log.Warn("could not record an app's usage", "app", app.ID, "error", err)
	}
}

func percentOf(used, limit int64) int {
	if limit <= 0 {
		return 0
	}
	return int(used * 100 / limit)
}

// alert is one threshold going off, and what to say about it.
type alert struct {
	name, detail string
}

// evaluate decides which of an app's thresholds are crossed, from its recent
// samples, oldest first. It is a function of its arguments and nothing else,
// so the rules can be tested without a cluster or a clock.
func evaluate(thresholds store.AppAlerts, recent []store.AppSample, now time.Time) []alert {
	var out []alert
	last := recent
	if len(last) > sustained {
		last = last[len(last)-sustained:]
	}
	above := func(threshold int, peak func(store.AppSample) int) (bool, int) {
		if threshold <= 0 || len(last) < sustained {
			return false, 0
		}
		lowest := 100000
		for _, s := range last {
			lowest = min(lowest, peak(s))
		}
		return lowest >= threshold, peak(last[len(last)-1])
	}
	if crossed, now := above(thresholds.MemoryPct, func(s store.AppSample) int { return s.MemoryPeakPct }); crossed {
		out = append(out, alert{"memory", fmt.Sprintf(
			"The busiest instance has been at %d%% of its memory limit or more for %d minutes; now %d%%. "+
				"At the limit it is killed and restarted.", thresholds.MemoryPct, sustained, now)})
	}
	if crossed, now := above(thresholds.CPUPct, func(s store.AppSample) int { return s.CPUPeakPct }); crossed {
		out = append(out, alert{"cpu", fmt.Sprintf(
			"The busiest instance has been at %d%% of its CPU limit or more for %d minutes; now %d%%. "+
				"At the limit it is slowed down, not stopped.", thresholds.CPUPct, sustained, now)})
	}
	if thresholds.Restarts > 0 {
		// Kubernetes counts restarts per instance and starts again at zero
		// for a new one, so the count is the sum of the increases, never the
		// difference between the ends.
		restarts := 0
		for i := 1; i < len(recent); i++ {
			if now.Sub(recent[i].At) > restartWindow {
				continue
			}
			if grew := recent[i].Restarts - recent[i-1].Restarts; grew > 0 {
				restarts += grew
			}
		}
		if restarts >= thresholds.Restarts {
			out = append(out, alert{"restarts", fmt.Sprintf(
				"Its instances restarted %d times in the last %d minutes. The reason is in the logs of the instance before the restart.",
				restarts, int(restartWindow.Minutes()))})
		}
	}
	return out
}

// checkAlerts says when an app crosses a threshold, and when it comes back,
// once each.
func (w *Watcher) checkAlerts(ctx context.Context, app store.DeployedApp, now time.Time) {
	thresholds, err := w.db.GetAppAlerts(ctx, app.ID)
	if err != nil {
		w.log.Warn("could not read an app's alerts", "app", app.ID, "error", err)
		return
	}
	recent, err := w.db.AppSamples(ctx, app.ID, now.Add(-restartWindow-time.Minute))
	if err != nil {
		w.log.Warn("could not read an app's recent usage", "app", app.ID, "error", err)
		return
	}
	crossed := evaluate(thresholds, recent, now)

	names := make([]string, 0, len(crossed))
	for _, a := range crossed {
		names = append(names, a.name)
		if !slices.Contains(thresholds.Firing, a.name) {
			w.notifyTeam(ctx, app.TeamID, notify.EventAppAlert, notify.Message{
				Title:  alertTitle(app.Name, a.name),
				Body:   a.detail,
				Level:  "warning",
				Path:   "/apps/" + app.ID,
				Fields: map[string]string{"App": app.Name},
			})
		}
	}
	for _, was := range thresholds.Firing {
		if !slices.Contains(names, was) {
			w.notifyTeam(ctx, app.TeamID, notify.EventAppAlert, notify.Message{
				Title:  app.Name + " is back under its " + alertNoun(was) + " threshold",
				Body:   "Nothing to do; this is the end of the earlier warning.",
				Level:  "success",
				Path:   "/apps/" + app.ID,
				Fields: map[string]string{"App": app.Name},
			})
		}
	}
	slices.Sort(names)
	previous := slices.Clone(thresholds.Firing)
	slices.Sort(previous)
	if strings.Join(names, ",") != strings.Join(previous, ",") {
		if err := w.db.SetAlertsFiring(ctx, app.ID, names); err != nil {
			w.log.Warn("could not record an app's alerts", "app", app.ID, "error", err)
		}
		w.publish(app.TeamID, "app", map[string]any{"id": app.ID, "alerts": names})
	}
}

func alertTitle(app, name string) string {
	switch name {
	case "memory":
		return app + " is close to its memory limit"
	case "cpu":
		return app + " is close to its CPU limit"
	default:
		return app + " keeps restarting"
	}
}

func alertNoun(name string) string {
	if name == "restarts" {
		return "restart"
	}
	if name == "cpu" {
		return "CPU"
	}
	return name
}
