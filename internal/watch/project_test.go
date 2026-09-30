package watch

import (
	"testing"
	"time"

	"skifity/internal/api"
	"skifity/internal/notify"
	"skifity/internal/store"
)

// A notification channel can be limited to some projects, and the dispatcher
// can only leave a channel out when the message says which project it is
// about. What the watcher says about an app belongs to the app's project; what
// it says about a server belongs to the whole team.

func TestWhatTheWatcherSaysAboutAnAppNamesItsProject(t *testing.T) {
	db := newFakeStore()
	db.apps = []store.DeployedApp{{
		App:    store.App{ID: "app_1", Name: "shop", Slug: "shop", Status: "running", MemLimitMB: 100},
		TeamID: "team_1", ProjectID: "prj_shop", Namespace: "acme-shop-production",
	}}
	db.servers = []store.Server{{
		ID: "srv_1", TeamID: "team_1", Name: "web-1", NodeName: "web-1", Status: store.ServerReady,
	}}
	c := &fakeCluster{
		nodes: []api.NodeInfo{}, // the server is gone
		apps: map[string]api.AppRuntimeStatus{
			"shop": {DesiredReplicas: 1, ReadyReplicas: 0, Instances: []api.InstanceInfo{{MemoryMB: 95}}},
		},
	}
	now := time.Now().UTC().Truncate(time.Minute)
	db.samples["app_1"] = samples(now.Add(-3*time.Minute),
		store.AppSample{MemoryPeakPct: 95}, store.AppSample{MemoryPeakPct: 96})

	w, notifier := testWatcher(t, db, c)
	w.Once(t.Context())

	if notifier.eventsOf(notify.EventAppUnhealthy) != 1 || notifier.eventsOf(notify.EventAppAlert) != 1 ||
		notifier.eventsOf(notify.EventServerLost) != 1 {
		t.Fatalf("the pass sent %+v; want an unhealthy app, an alert and a lost server", notifier.sent)
	}
	for _, sent := range notifier.sent {
		want := "prj_shop"
		if sent.event == notify.EventServerLost {
			want = ""
		}
		if sent.msg.ProjectID != want {
			t.Errorf("%s was sent about project %q, want %q", sent.event, sent.msg.ProjectID, want)
		}
	}
}
