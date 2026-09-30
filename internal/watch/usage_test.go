package watch

import (
	"testing"
	"time"

	"skifity/internal/api"
	"skifity/internal/notify"
	"skifity/internal/store"
)

// What apps used, and when it is said.

func samples(start time.Time, points ...store.AppSample) []store.AppSample {
	for i := range points {
		points[i].At = start.Add(time.Duration(i) * time.Minute)
	}
	return points
}

func TestThresholdsAreCrossedOnlyWhenTheyStay(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	thresholds := store.AppAlerts{MemoryPct: 90, CPUPct: 80, Restarts: 3}
	now := start.Add(10 * time.Minute)

	cases := []struct {
		name   string
		recent []store.AppSample
		want   []string
	}{
		{"a spike is not news", samples(start,
			store.AppSample{MemoryPeakPct: 50}, store.AppSample{MemoryPeakPct: 95}, store.AppSample{MemoryPeakPct: 60}), nil},
		{"three minutes over is", samples(start,
			store.AppSample{MemoryPeakPct: 91}, store.AppSample{MemoryPeakPct: 93}, store.AppSample{MemoryPeakPct: 97}), []string{"memory"}},
		{"two minutes of data is not three", samples(start,
			store.AppSample{MemoryPeakPct: 99}, store.AppSample{MemoryPeakPct: 99}), nil},
		{"CPU too, when it is on", samples(start,
			store.AppSample{CPUPeakPct: 85}, store.AppSample{CPUPeakPct: 90}, store.AppSample{CPUPeakPct: 81}), []string{"cpu"}},
		// A new instance starts counting again at zero, so a drop is not
		// negative restarts and an increase after it still counts.
		{"restarts are the increases, across a replaced instance", samples(start.Add(5*time.Minute),
			store.AppSample{Restarts: 4}, store.AppSample{Restarts: 5}, store.AppSample{Restarts: 0},
			store.AppSample{Restarts: 2}), []string{"restarts"}},
		{"restarts long ago are not counted", samples(start.Add(-30*time.Minute),
			store.AppSample{Restarts: 0}, store.AppSample{Restarts: 9}), nil},
	}
	for _, tc := range cases {
		var got []string
		for _, a := range evaluate(thresholds, tc.recent, now) {
			got = append(got, a.name)
		}
		if len(got) != len(tc.want) || len(got) > 0 && got[0] != tc.want[0] {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}

	// Zero is off.
	off := store.AppAlerts{}
	if got := evaluate(off, samples(start, store.AppSample{MemoryPeakPct: 100, Restarts: 0},
		store.AppSample{MemoryPeakPct: 100, Restarts: 50}, store.AppSample{MemoryPeakPct: 100}), now); len(got) != 0 {
		t.Errorf("thresholds that are off fired: %+v", got)
	}
}

// The peaks are the busiest instance against its own limit: three instances
// at a third each is fine, one of them at its limit is not.
func TestUsageIsRecordedWithTheBusiestInstancesShare(t *testing.T) {
	db := newFakeStore()
	db.apps = []store.DeployedApp{{App: store.App{ID: "app_1", Name: "web", Slug: "web", Status: "running",
		CPULimitM: 1000, MemLimitMB: 512}, TeamID: "team_1", Namespace: "ns"}}
	c := &fakeCluster{apps: map[string]api.AppRuntimeStatus{"web": {
		DesiredReplicas: 2, ReadyReplicas: 2,
		Instances: []api.InstanceInfo{
			{CPUM: 100, MemoryMB: 100, Restarts: 1},
			{CPUM: 900, MemoryMB: 480, Restarts: 2},
		},
	}}}
	w, _ := testWatcher(t, db, c)
	w.Once(t.Context())

	got := db.samples["app_1"]
	if len(got) != 1 {
		t.Fatalf("%d samples were recorded, want 1", len(got))
	}
	s := got[0]
	if s.CPUM != 1000 || s.MemoryMB != 580 || s.CPUPeakPct != 90 || s.MemoryPeakPct != 93 ||
		s.Restarts != 3 || s.Ready != 2 || s.Desired != 2 {
		t.Fatalf("the sample is %+v", s)
	}
}

// Crossing a threshold is said once, and so is coming back under it.
func TestAnAlertIsSaidOnceAndSoIsItsEnd(t *testing.T) {
	db := newFakeStore()
	db.apps = []store.DeployedApp{{App: store.App{ID: "app_1", Name: "web", Slug: "web", Status: "running",
		CPULimitM: 1000, MemLimitMB: 100}, TeamID: "team_1", Namespace: "ns"}}
	c := &fakeCluster{apps: map[string]api.AppRuntimeStatus{"web": {
		DesiredReplicas: 1, ReadyReplicas: 1,
		Instances: []api.InstanceInfo{{MemoryMB: 95}},
	}}}
	w, notifier := testWatcher(t, db, c)

	// Three minutes over, as the watcher would have recorded them.
	now := time.Now().UTC().Truncate(time.Minute)
	db.samples["app_1"] = samples(now.Add(-3*time.Minute),
		store.AppSample{MemoryPeakPct: 95}, store.AppSample{MemoryPeakPct: 96})
	w.Once(t.Context())
	w.Once(t.Context())
	if n := notifier.eventsOf(notify.EventAppAlert); n != 1 {
		t.Fatalf("a crossed threshold was said %d times over two passes, want once", n)
	}
	if firing := db.alerts["app_1"].Firing; len(firing) != 1 || firing[0] != "memory" {
		t.Fatalf("firing is %v", firing)
	}

	// Back under it.
	c.apps["web"] = api.AppRuntimeStatus{DesiredReplicas: 1, ReadyReplicas: 1,
		Instances: []api.InstanceInfo{{MemoryMB: 40}}}
	db.samples["app_1"] = nil
	w.Once(t.Context())
	if n := notifier.eventsOf(notify.EventAppAlert); n != 2 {
		t.Fatalf("the end of the alert was not said: %d messages", n)
	}
	if last := notifier.sent[len(notifier.sent)-1]; last.msg.Level != "success" {
		t.Fatalf("the last message was %+v", last)
	}
	if firing := db.alerts["app_1"].Firing; len(firing) != 0 {
		t.Fatalf("firing is %v after coming back under", firing)
	}
}
