package watch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"skifity/internal/api"
	"skifity/internal/notify"
	"skifity/internal/runsafe"
	"skifity/internal/store"
	"skifity/internal/version"
)

// DriftInterval is how often each deployed app is compared with what the
// panel applies.
//
// Five minutes rather than every pass, because a comparison is not free: it
// renders the app — variables opened, files read — and asks the API server for
// each of its objects, seven or eight requests for a typical app. Every minute,
// a panel with a hundred apps would put thirteen requests a second on the API
// server of a 2 GB node, for news that is rarely urgent to the minute. Every
// five it is under three. Somebody looking at the app does not wait: the page
// reads the cluster itself when it is opened.
const DriftInterval = 5 * time.Minute

// repairTimeout bounds putting one app back by itself: the apply, and the
// rollout it waits for when the change was to the running version.
const repairTimeout = 15 * time.Minute

// Drift is what the watcher asks about drift: the deployer, which renders the
// app the way it applies it.
type Drift interface {
	CheckDrift(ctx context.Context, appID string) (api.DriftReport, error)
	RepairDrift(ctx context.Context, appID string) error
	// Busy reports that the panel is applying the app right now, when what
	// the cluster holds is half the old and half the new.
	Busy(appID string) bool
}

// WithDrift has the watcher compare every deployed app with what the panel
// applies, every DriftInterval.
func (w *Watcher) WithDrift(d Drift) *Watcher {
	w.drift = d
	w.driftEvery = DriftInterval
	return w
}

// checkDrift compares the deployed apps with what the panel applies, at most
// once every driftEvery.
func (w *Watcher) checkDrift(ctx context.Context) {
	if w.drift == nil {
		return
	}
	now := time.Now()
	if !w.lastDrift.IsZero() && now.Sub(w.lastDrift) < w.driftEvery {
		return
	}
	w.lastDrift = now

	apps, err := w.db.ListDeployedApps(ctx)
	if err != nil {
		w.log.Warn("could not list apps to compare with the cluster", "error", err)
		return
	}
	deploying, known := w.deployingApps(ctx)
	if !known {
		return
	}
	for _, app := range apps {
		if ctx.Err() != nil {
			// Out of time for this pass; the rest are compared next time.
			return
		}
		// A deploy, a rollback or a sync changes the app's objects on
		// purpose, one after another, and reading them halfway would report
		// the panel's own work as somebody else's. They are compared once it
		// has finished.
		if deploying[app.ID] || w.drift.Busy(app.ID) || w.repairing(app.ID) {
			continue
		}
		w.checkAppDrift(ctx, app)
	}
}

// deployingApps are the apps with a deployment that has not finished. known
// is false when that could not be read, which is treated as every app
// deploying: a missed check is five minutes late, and a false alarm is a
// notification nobody can act on.
func (w *Watcher) deployingApps(ctx context.Context) (deploying map[string]bool, known bool) {
	unfinished, err := w.db.ListUnfinishedDeployments(ctx)
	if err != nil {
		w.log.Warn("could not list unfinished deployments; not comparing apps with the cluster this time", "error", err)
		return nil, false
	}
	out := map[string]bool{}
	for _, deployment := range unfinished {
		out[deployment.AppID] = true
	}
	return out, true
}

func (w *Watcher) checkAppDrift(ctx context.Context, app store.DeployedApp) {
	report, err := w.drift.CheckDrift(ctx, app.ID)
	if err != nil {
		w.log.Debug("could not compare an app with the cluster", "app", app.ID, "error", err)
		return
	}
	// A deploy or a sync that began while the app was being read may have
	// been caught halfway — an object it was about to create, not there yet.
	// What was read then is not evidence of anything; the next pass reads it
	// again.
	if report.Status == store.DriftApplying {
		return
	}
	if isDrift(report.Status) {
		if deploying, known := w.deployingApps(ctx); !known || deploying[app.ID] || w.drift.Busy(app.ID) {
			return
		}
	}
	stored, err := w.db.GetAppDrift(ctx, app.ID)
	if err != nil {
		w.log.Warn("could not read what was last found for an app", "app", app.ID, "error", err)
		return
	}
	fingerprint := driftFingerprint(report)
	items, err := json.Marshal(report.Items)
	if err != nil {
		items = []byte("[]")
	}
	if err := w.db.RecordAppDrift(ctx, app.ID, report.Status, string(items), fingerprint, report.CheckedAt); err != nil {
		w.log.Warn("could not record what was found for an app", "app", app.ID, "error", err)
		return
	}
	if report.Status != stored.Status {
		w.publish(app.TeamID, "drift", map[string]any{"app_id": app.ID, "status": report.Status})
	}

	if !isDrift(report.Status) {
		// Back as the panel applied it: the next drift is news again.
		if stored.Notified != "" {
			if err := w.db.SetDriftNotified(ctx, app.ID, ""); err != nil {
				w.log.Warn("could not reset an app's drift notification", "app", app.ID, "error", err)
			}
		}
		return
	}
	w.log.Info("an app was changed outside the panel",
		"app", app.ID, "status", report.Status, "differences", len(report.Items))

	if fingerprint == stored.Notified {
		// Already said. The same drift found again five minutes later is not
		// a second event, and neither is a second attempt to put it back.
		return
	}
	if err := w.db.SetDriftNotified(ctx, app.ID, fingerprint); err != nil {
		w.log.Warn("could not record an app's drift notification", "app", app.ID, "error", err)
		return
	}
	if stored.AutoRepair {
		w.repair(ctx, app, report)
		return
	}
	w.notifyApp(ctx, app, notify.EventAppDrifted, driftMessage(app, report, ""))
}

// repair puts an app back by itself, in the background: an apply that waits
// for a rollout takes minutes, and the pass has other apps to look at.
func (w *Watcher) repair(ctx context.Context, app store.DeployedApp, report api.DriftReport) {
	// A deployment that started since this pass began is the panel changing
	// the app on purpose; its rollout is not something to put back.
	if deploying, known := w.deployingApps(ctx); !known || deploying[app.ID] || w.drift.Busy(app.ID) {
		return
	}
	w.repairMu.Lock()
	if w.repairs == nil {
		w.repairs = map[string]bool{}
	}
	w.repairs[app.ID] = true
	w.repairMu.Unlock()

	w.repairWG.Add(1)
	runsafe.Go(w.log, "put back "+app.ID, func() {
		defer w.repairWG.Done()
		defer func() {
			w.repairMu.Lock()
			delete(w.repairs, app.ID)
			w.repairMu.Unlock()
		}()
		repairCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), repairTimeout)
		defer cancel()

		err := w.drift.RepairDrift(repairCtx, app.ID)
		outcome := "put back"
		if err != nil {
			w.log.Warn("could not put an app back by itself", "app", app.ID, "error", err)
			outcome = err.Error()
		} else {
			w.log.Info("put an app back as the panel applied it", "app", app.ID)
		}
		w.record(repairCtx, store.AuditEvent{
			TeamID: app.TeamID, ActorLabel: version.Name, Action: "app.drift_repaired",
			TargetType: "app", TargetID: app.ID, TargetLabel: app.Name,
			Metadata: `{"automatic":true}`,
		})
		msg := driftMessage(app, report, outcome)
		if err != nil {
			msg.Level = "error"
		}
		w.notifyApp(repairCtx, app, notify.EventAppDrifted, msg)
	})
}

// repairing reports whether the watcher is putting an app back right now.
func (w *Watcher) repairing(appID string) bool {
	w.repairMu.Lock()
	defer w.repairMu.Unlock()
	return w.repairs[appID]
}

func (w *Watcher) record(ctx context.Context, event store.AuditEvent) {
	if err := w.db.RecordAudit(ctx, &event); err != nil {
		w.log.Warn("could not record an audit event", "action", event.Action, "error", err)
	}
	w.publish(event.TeamID, "audit", struct{}{})
}

func isDrift(status string) bool {
	return status == store.DriftDrifted || status == store.DriftMissing
}

// driftFingerprint identifies a drift by where it is, not by the values:
// somebody editing the same field twice is the same drift, somebody editing
// another field is a new one.
func driftFingerprint(report api.DriftReport) string {
	if !isDrift(report.Status) {
		return ""
	}
	lines := make([]string, 0, len(report.Items))
	for _, item := range report.Items {
		lines = append(lines, item.Kind+"/"+item.Name+" "+item.Path+" "+item.Change)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:12])
}

// driftMessage says what was found. outcome is empty when nothing was done
// about it, "put back" when it was, and the reason when putting it back failed.
func driftMessage(app store.DeployedApp, report api.DriftReport, outcome string) notify.Message {
	var who []string
	seen := map[string]bool{}
	for _, item := range report.Items {
		if item.ChangedBy != "" && !seen[item.ChangedBy] {
			seen[item.ChangedBy] = true
			who = append(who, item.ChangedBy)
		}
	}
	first := ""
	if len(report.Items) > 0 {
		item := report.Items[0]
		first = item.Kind + "/" + item.Name
		if item.Path != "" {
			first += " " + item.Path
		}
	}
	what := fmt.Sprintf("%d of its settings in the cluster no longer match what %s applied", len(report.Items), version.Name)
	if report.Status == store.DriftMissing {
		what = fmt.Sprintf("Some of its objects were deleted from the cluster, and %d settings no longer match what %s applied",
			len(report.Items), version.Name)
	}
	msg := notify.Message{
		Title: app.Name + " was changed outside " + version.Name,
		Level: "warning",
		Path:  "/apps/" + app.ID + "?tab=advanced",
		Fields: map[string]string{
			"App":         app.Name,
			"Differences": strconv.Itoa(len(report.Items)),
		},
	}
	if first != "" {
		msg.Fields["First"] = first
	}
	if len(who) > 0 {
		msg.Fields["Changed by"] = strings.Join(who, ", ")
	}
	switch outcome {
	case "":
		msg.Body = what + ". The app's Advanced tab lists them, and puts them back."
	case "put back":
		msg.Title = app.Name + " was changed outside " + version.Name + " and put back"
		msg.Body = what + ", and the app is set to be put back automatically, so it was."
	default:
		msg.Title = app.Name + " was changed outside " + version.Name + " and could not be put back"
		msg.Body = what + ". Putting it back automatically failed: " + outcome
	}
	return msg
}
