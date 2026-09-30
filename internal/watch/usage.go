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
		// A running instance metrics-server had nothing for is not idle; its
		// use is not known. The minute is kept for its restarts and readiness,
		// which the pods say themselves, with CPU and memory marked unknown
		// rather than drawn as a dip that ends an alert under pressure.
		if instance.Ready && !instance.UsageKnown {
			sample.UsageUnknown = true
		}
	}
	for _, instance := range status.Instances {
		sample.Restarts += instance.Restarts
		if sample.UsageUnknown {
			continue
		}
		sample.CPUM += instance.CPUM
		sample.MemoryMB += instance.MemoryMB
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

// evaluate decides which of an app's thresholds are crossed and which are
// clearly clear, from its recent samples, oldest first. One that is neither —
// too few minutes to tell, or a peak between the line and the margin under
// it — is left as it was. It is a function of its arguments and nothing else,
// so the rules can be tested without a cluster or a clock.
func evaluate(thresholds store.AppAlerts, recent []store.AppSample, now time.Time) (crossed []alert, clear []string) {
	last := recent
	if len(last) > sustained {
		last = last[len(last)-sustained:]
	}
	state := func(threshold int, peak func(store.AppSample) int) (string, int) {
		if threshold <= 0 || len(last) < sustained {
			return "", 0
		}
		lowest, highest := 100000, 0
		for _, s := range last {
			// A minute whose use is not known is neither over nor under.
			if s.UsageUnknown {
				return "", 0
			}
			lowest, highest = min(lowest, peak(s)), max(highest, peak(s))
		}
		switch {
		case lowest >= threshold:
			return "above", peak(last[len(last)-1])
		case highest < threshold-clearMargin:
			return "below", 0
		}
		return "", 0
	}
	if where, now := state(thresholds.MemoryPct, func(s store.AppSample) int { return s.MemoryPeakPct }); where == "above" {
		crossed = append(crossed, alert{"memory", fmt.Sprintf(
			"The busiest instance has been at %d%% of its memory limit or more for %d minutes; now %d%%. "+
				"At the limit it is killed and restarted.", thresholds.MemoryPct, sustained, now)})
	} else if where == "below" || thresholds.MemoryPct <= 0 {
		clear = append(clear, "memory")
	}
	if where, now := state(thresholds.CPUPct, func(s store.AppSample) int { return s.CPUPeakPct }); where == "above" {
		crossed = append(crossed, alert{"cpu", fmt.Sprintf(
			"The busiest instance has been at %d%% of its CPU limit or more for %d minutes; now %d%%. "+
				"At the limit it is slowed down, not stopped.", thresholds.CPUPct, sustained, now)})
	} else if where == "below" || thresholds.CPUPct <= 0 {
		clear = append(clear, "cpu")
	}
	if thresholds.Restarts <= 0 {
		clear = append(clear, "restarts")
		return crossed, clear
	}
	// Kubernetes counts restarts per instance and starts again at zero for a
	// new one, so the count is the sum of the increases, never the
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
	switch {
	case restarts >= thresholds.Restarts:
		crossed = append(crossed, alert{"restarts", fmt.Sprintf(
			"Its instances restarted %d times in the last %d minutes. The reason is in the logs of the instance before the restart.",
			restarts, int(restartWindow.Minutes()))})
	case restarts == 0 && len(recent) > sustained:
		// None at all over enough minutes to count them in: over. One or two
		// under the threshold, or too few minutes after a restart of the
		// panel, is not yet the end.
		clear = append(clear, "restarts")
	}
	return crossed, clear
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
	crossed, clear := evaluate(thresholds, recent, now)

	// What fires now: what was firing and has not clearly ended, and what
	// has just crossed.
	names := make([]string, 0, len(crossed)+len(thresholds.Firing))
	for _, was := range thresholds.Firing {
		if !slices.Contains(clear, was) {
			names = append(names, was)
		}
	}
	for _, a := range crossed {
		if !slices.Contains(names, a.name) {
			names = append(names, a.name)
		}
		if !slices.Contains(thresholds.Firing, a.name) {
			w.notifyApp(ctx, app, notify.EventAppAlert, notify.Message{
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
			w.notifyApp(ctx, app, notify.EventAppAlert, notify.Message{
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
