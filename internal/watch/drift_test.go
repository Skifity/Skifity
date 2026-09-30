package watch

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/api"
	"skifity/internal/notify"
	"skifity/internal/store"
)

// fakeDrift answers with whatever report the test set for each app, and
// remembers what it was asked to do.
type fakeDrift struct {
	mu       sync.Mutex
	reports  map[string]api.DriftReport
	busy     map[string]bool
	checked  []string
	repaired []string
	// duringCheck runs inside a check, for what happens meanwhile.
	duringCheck func(appID string)
	repairErr   error
}

func (f *fakeDrift) CheckDrift(_ context.Context, appID string) (api.DriftReport, error) {
	f.mu.Lock()
	f.checked = append(f.checked, appID)
	report, ok := f.reports[appID]
	hook := f.duringCheck
	f.mu.Unlock()
	if hook != nil {
		hook(appID)
	}
	if !ok {
		return api.DriftReport{}, errors.New("no report for " + appID)
	}
	return report, nil
}

func (f *fakeDrift) RepairDrift(_ context.Context, appID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repaired = append(f.repaired, appID)
	return f.repairErr
}

func (f *fakeDrift) Busy(appID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy[appID]
}

func (f *fakeDrift) set(appID string, report api.DriftReport) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports[appID] = report
}

func (f *fakeDrift) counts() (checked, repaired int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.checked), len(f.repaired)
}

var (
	editedReplicas = api.DriftReport{Status: store.DriftDrifted, Items: []api.DriftItem{{
		Kind: "Deployment", Name: "shop", Path: "spec.replicas", Change: "changed",
		Panel: "1", Live: "4", ChangedBy: "kubectl",
	}}}
	inSync = api.DriftReport{Status: store.DriftInSync, Items: []api.DriftItem{}}
)

func driftWatcher(t *testing.T) (*Watcher, *fakeStore, *fakeDrift, *fakeNotifier) {
	t.Helper()
	db := newFakeStore()
	db.apps = []store.DeployedApp{{
		App:    store.App{ID: "app_1", Name: "shop", Slug: "shop", Status: "running"},
		TeamID: "team_1", ProjectID: "prj_shop", Namespace: "acme-shop-production",
	}}
	drift := &fakeDrift{reports: map[string]api.DriftReport{"app_1": editedReplicas}, busy: map[string]bool{}}
	w, notifier := testWatcher(t, db, &fakeCluster{})
	w.WithDrift(drift)
	// Every pass compares, rather than one in five minutes.
	w.driftEvery = 0
	return w, db, drift, notifier
}

func TestADriftIsSaidOnceAndAgainOnlyAfterItWasPutRight(t *testing.T) {
	w, db, drift, notifier := driftWatcher(t)

	w.Once(t.Context())
	w.Once(t.Context())
	w.Once(t.Context())
	if got := notifier.eventsOf(notify.EventAppDrifted); got != 1 {
		t.Fatalf("the same drift found three times sent %d notifications, want 1", got)
	}
	sent := notifier.sent[0]
	if sent.msg.ProjectID != "prj_shop" || sent.teamID != "team_1" {
		t.Errorf("the notification is about team %q, project %q", sent.teamID, sent.msg.ProjectID)
	}
	if !strings.Contains(sent.msg.Title, "changed outside") || sent.msg.Fields["Changed by"] != "kubectl" ||
		!strings.Contains(sent.msg.Path, "tab=advanced") {
		t.Errorf("the notification says %+v", sent.msg)
	}
	if got, _ := db.GetAppDrift(t.Context(), "app_1"); got.Status != store.DriftDrifted || got.Notified == "" {
		t.Errorf("what was found is recorded as %+v", got)
	}

	// Somebody puts it right; the watcher forgets it said anything.
	drift.set("app_1", inSync)
	w.Once(t.Context())
	if got, _ := db.GetAppDrift(t.Context(), "app_1"); got.Notified != "" || got.Status != store.DriftInSync {
		t.Errorf("back in sync is recorded as %+v", got)
	}

	// The same edit again is a new drift, and news again.
	drift.set("app_1", editedReplicas)
	w.Once(t.Context())
	if got := notifier.eventsOf(notify.EventAppDrifted); got != 2 {
		t.Fatalf("a second drift sent %d notifications in all, want 2", got)
	}

	// Another field changed while the first still stands is news as well.
	also := editedReplicas
	also.Items = append(append([]api.DriftItem{}, editedReplicas.Items...), api.DriftItem{
		Kind: "Ingress", Name: "shop", Change: "deleted",
	})
	also.Status = store.DriftMissing
	drift.set("app_1", also)
	w.Once(t.Context())
	if got := notifier.eventsOf(notify.EventAppDrifted); got != 3 {
		t.Fatalf("a drift that grew sent %d notifications in all, want 3", got)
	}
}

// A deploy, a rollback or a sync is the panel changing the app's objects, and
// they are never read halfway through.
func TestNothingIsComparedWhileThePanelIsChangingTheApp(t *testing.T) {
	w, db, drift, notifier := driftWatcher(t)

	db.unfinished = []store.Deployment{{ID: "dep_2", AppID: "app_1", Status: store.DeployDeploying}}
	w.Once(t.Context())
	if checked, _ := drift.counts(); checked != 0 {
		t.Fatalf("an app with a rollout in progress was compared %d times", checked)
	}

	db.unfinished = nil
	drift.busy["app_1"] = true
	w.Once(t.Context())
	if checked, _ := drift.counts(); checked != 0 {
		t.Fatalf("an app being synced was compared %d times", checked)
	}
	if len(notifier.sent) != 0 {
		t.Fatalf("sent %+v", notifier.sent)
	}
}

func TestTheWatcherComparesAtItsOwnPace(t *testing.T) {
	w, _, drift, _ := driftWatcher(t)
	w.driftEvery = time.Hour
	w.Once(t.Context())
	w.Once(t.Context())
	if checked, _ := drift.counts(); checked != 1 {
		t.Fatalf("two passes inside the interval compared %d times, want 1", checked)
	}
}

func TestAnAppSetToBePutBackIsPutBackOnceAndSaysSo(t *testing.T) {
	w, db, drift, notifier := driftWatcher(t)
	if err := db.RecordAppDrift(t.Context(), "app_1", store.DriftInSync, "[]", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	db.drift["app_1"] = store.AppDrift{AppID: "app_1", Status: store.DriftInSync, AutoRepair: true}

	w.Once(t.Context())
	w.repairWG.Wait()
	if _, repaired := drift.counts(); repaired != 1 {
		t.Fatalf("put back %d times, want 1", repaired)
	}
	if len(db.audits) != 1 || db.audits[0].Action != "app.drift_repaired" || db.audits[0].TeamID != "team_1" ||
		!strings.Contains(db.audits[0].Metadata, "automatic") {
		t.Fatalf("the audit log has %+v", db.audits)
	}
	if got := notifier.eventsOf(notify.EventAppDrifted); got != 1 ||
		!strings.Contains(notifier.sent[0].msg.Title, "put back") {
		t.Fatalf("sent %+v", notifier.sent)
	}

	// The repair did not take — somebody's controller keeps writing the field.
	// Found again, it is not put back again: that would be the panel and the
	// other side taking turns for ever.
	w.Once(t.Context())
	w.repairWG.Wait()
	if _, repaired := drift.counts(); repaired != 1 {
		t.Fatalf("the same drift was put back %d times", repaired)
	}
}

// Putting an app back must never fight a rollout: the rollout is the panel
// changing the app on purpose, and applying the previous state over it would
// put the old version back.
func TestPuttingBackDoesNotFightARolloutInProgress(t *testing.T) {
	w, db, drift, _ := driftWatcher(t)
	db.drift = map[string]store.AppDrift{"app_1": {AppID: "app_1", Status: store.DriftInSync, AutoRepair: true}}

	// A rollout in progress: not even compared.
	db.unfinished = []store.Deployment{{ID: "dep_2", AppID: "app_1", Status: store.DeployDeploying}}
	w.Once(t.Context())
	w.repairWG.Wait()
	if checked, repaired := drift.counts(); checked != 0 || repaired != 0 {
		t.Fatalf("with a rollout in progress: compared %d, put back %d", checked, repaired)
	}

	// A rollout that starts while the app is being compared: found drifted
	// on the way in, and not put back on the way out.
	db.unfinished = nil
	drift.duringCheck = func(string) {
		db.unfinished = []store.Deployment{{ID: "dep_3", AppID: "app_1", Status: store.DeployQueued}}
	}
	w.Once(t.Context())
	w.repairWG.Wait()
	if checked, repaired := drift.counts(); checked != 1 || repaired != 0 {
		t.Fatalf("a rollout that started meanwhile: compared %d, put back %d", checked, repaired)
	}
}

func TestAFailedPutBackIsSaid(t *testing.T) {
	w, db, drift, notifier := driftWatcher(t)
	db.drift = map[string]store.AppDrift{"app_1": {AppID: "app_1", Status: store.DriftInSync, AutoRepair: true}}
	drift.repairErr = errors.New("the rollout did not finish in 10m0s")

	w.Once(t.Context())
	w.repairWG.Wait()
	if len(notifier.sent) != 1 || notifier.sent[0].msg.Level != "error" ||
		!strings.Contains(notifier.sent[0].msg.Body, "did not finish") {
		t.Fatalf("sent %+v", notifier.sent)
	}
}
