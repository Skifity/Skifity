package watch

import (
	"slices"
	"strings"
	"testing"
	"time"

	"skifity/internal/api"
	"skifity/internal/notify"
	"skifity/internal/store"
)

func serverSamples(start time.Time, points ...store.ServerSample) []store.ServerSample {
	for i := range points {
		points[i].At = start.Add(time.Duration(i) * time.Minute)
	}
	return points
}

func diskAt(pct int64) store.ServerSample {
	return store.ServerSample{DiskUsedMB: pct * 1024, DiskCapacityMB: 100 * 1024,
		MemoryMB: 1024, MemoryCapacityMB: 4096, CPUM: 100, CPUCapacityM: 2000}
}

func TestAServerThresholdIsCrossedOnlyWhenItStays(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	thresholds := store.DefaultServerAlerts()
	unknown := diskAt(95)
	unknown.DiskCapacityMB = 0

	cases := []struct {
		name   string
		recent []store.ServerSample
		want   string
	}{
		{"a full disk for three minutes", serverSamples(start, diskAt(86), diskAt(88), diskAt(91)), "disk"},
		{"two minutes is not three", serverSamples(start, diskAt(95), diskAt(95)), ""},
		{"one minute under breaks it", serverSamples(start, diskAt(95), diskAt(80), diskAt(95)), ""},
		// A minute the disk could not be read is not a minute above it.
		{"an unread minute is not a full one", serverSamples(start, diskAt(95), unknown, diskAt(95)), ""},
		{"memory", serverSamples(start,
			store.ServerSample{MemoryMB: 3800, MemoryCapacityMB: 4096},
			store.ServerSample{MemoryMB: 3900, MemoryCapacityMB: 4096},
			store.ServerSample{MemoryMB: 4000, MemoryCapacityMB: 4096}), "memory"},
		// CPU is off unless somebody turns it on.
		{"a busy CPU by default", serverSamples(start,
			store.ServerSample{CPUM: 2000, CPUCapacityM: 2000},
			store.ServerSample{CPUM: 2000, CPUCapacityM: 2000},
			store.ServerSample{CPUM: 2000, CPUCapacityM: 2000}), ""},
	}
	for _, tc := range cases {
		var got []string
		crossed, _ := evaluateServer(thresholds, tc.recent)
		for _, a := range crossed {
			got = append(got, a.name)
		}
		if strings.Join(got, ",") != tc.want {
			t.Errorf("%s: got %v, want %q", tc.name, got, tc.want)
		}
	}

	full, _ := evaluateServer(thresholds, serverSamples(start, diskAt(90), diskAt(90), diskAt(92)))
	if len(full) != 1 || !strings.Contains(full[0].detail, "92%, 92.0 GB of 100.0 GB") {
		t.Fatalf("the disk warning says %+v", full)
	}
	if got, _ := evaluateServer(store.ServerAlerts{}, serverSamples(start, diskAt(99), diskAt(99), diskAt(99))); len(got) != 0 {
		t.Errorf("thresholds that are off fired: %+v", got)
	}
}

// A ready server's minute is kept, disk included; one whose node is gone is
// not sampled at all.
func TestAServersUsageIsRecordedWithItsDisk(t *testing.T) {
	db := newFakeStore()
	db.servers = []store.Server{
		{ID: "srv_1", TeamID: "team_1", Name: "web-1", NodeName: "web-1", Status: store.ServerReady},
		{ID: "srv_2", TeamID: "team_1", Name: "web-2", NodeName: "web-2", Status: store.ServerReady},
	}
	c := &fakeCluster{
		nodes: []api.NodeInfo{{Name: "web-1", Ready: true, CPUUsedM: 500, CPUCapacityM: 2000,
			MemUsedMB: 1024, MemCapacityMB: 4096, PodCount: 12, UsageKnown: true}},
		disks: map[string]api.NodeDisk{"web-1": {UsedMB: 30000, CapacityMB: 40000}},
	}
	w, _ := testWatcher(t, db, c)
	w.Once(t.Context())

	got := db.serverSamples["srv_1"]
	if len(got) != 1 {
		t.Fatalf("%d samples, want 1", len(got))
	}
	if s := got[0]; s.CPUM != 500 || s.CPUCapacityM != 2000 || s.MemoryMB != 1024 || s.Pods != 12 ||
		s.DiskUsedMB != 30000 || s.DiskCapacityMB != 40000 {
		t.Fatalf("the sample is %+v", s)
	}
	if len(db.serverSamples["srv_2"]) != 0 {
		t.Fatal("a server whose node is gone was sampled")
	}
}

// A full disk is said once, and so is its end.
func TestADiskWarningIsSaidOnceAndSoIsItsEnd(t *testing.T) {
	db := newFakeStore()
	db.servers = []store.Server{{ID: "srv_1", TeamID: "team_1", Name: "web-1", NodeName: "web-1", Status: store.ServerReady}}
	c := &fakeCluster{
		nodes: []api.NodeInfo{{Name: "web-1", Ready: true, CPUCapacityM: 2000, MemCapacityMB: 4096, UsageKnown: true}},
		disks: map[string]api.NodeDisk{"web-1": {UsedMB: 93, CapacityMB: 100}},
	}
	w, notifier := testWatcher(t, db, c)

	now := time.Now().UTC().Truncate(time.Minute)
	db.serverSamples["srv_1"] = serverSamples(now.Add(-2*time.Minute), diskAt(93), diskAt(93))
	w.Once(t.Context())
	w.Once(t.Context())
	if n := notifier.eventsOf(notify.EventServerAlert); n != 1 {
		t.Fatalf("a full disk was said %d times over two passes, want once", n)
	}
	if firing := db.serverAlerts["srv_1"].Firing; len(firing) != 1 || firing[0] != "disk" {
		t.Fatalf("firing is %v", firing)
	}

	// One reading under it is not the end: after a restart there are too
	// few to say, and a disk at 84% is not clear of 85%.
	c.disks["web-1"] = api.NodeDisk{UsedMB: 84, CapacityMB: 100}
	db.serverSamples["srv_1"] = serverSamples(now.Add(-2*time.Minute), diskAt(84), diskAt(84))
	w.Once(t.Context())
	if n := notifier.eventsOf(notify.EventServerAlert); n != 1 {
		t.Fatalf("a disk just under its threshold ended the warning: %d messages", n)
	}
	c.disks["web-1"] = api.NodeDisk{UsedMB: 40, CapacityMB: 100}
	db.serverSamples["srv_1"] = nil
	w.Once(t.Context())
	if n := notifier.eventsOf(notify.EventServerAlert); n != 1 {
		t.Fatalf("a single reading after a gap ended the warning: %d messages", n)
	}
	db.serverSamples["srv_1"] = serverSamples(now.Add(-2*time.Minute), diskAt(40), diskAt(40))
	w.Once(t.Context())
	if n := notifier.eventsOf(notify.EventServerAlert); n != 2 {
		t.Fatalf("the end of the warning was not said: %d messages", n)
	}
	if last := notifier.sent[len(notifier.sent)-1]; last.msg.Level != "success" || !strings.Contains(last.msg.Title, "disk") {
		t.Fatalf("the last message was %+v", last)
	}
}

// metrics-server had nothing for the node: a minute of 0% CPU and memory is a
// false dip in the graph and ended alerts when the server was under pressure.
// The minute is still kept for its disk, which the kubelet reports either way:
// a full disk is what evicts metrics-server in the first place.
func TestAServerWhoseUsageIsNotKnownIsNotRecordedAsIdle(t *testing.T) {
	db := newFakeStore()
	db.servers = []store.Server{{ID: "srv_1", TeamID: "team_1", Name: "web-1", NodeName: "web-1", Status: store.ServerReady}}
	c := &fakeCluster{
		nodes: []api.NodeInfo{{Name: "web-1", Ready: true, CPUCapacityM: 2000, MemCapacityMB: 4096, PodCount: 7}},
		disks: map[string]api.NodeDisk{"web-1": {UsedMB: 38000, CapacityMB: 40000}},
	}
	w, _ := testWatcher(t, db, c)
	w.Once(t.Context())
	got := db.serverSamples["srv_1"]
	if len(got) != 1 {
		t.Fatalf("%d samples, want the minute kept for its disk", len(got))
	}
	if s := got[0]; s.CPUCapacityM != 0 || s.MemoryCapacityMB != 0 || s.DiskUsedMB != 38000 || s.Pods != 7 {
		t.Fatalf("the minute is %+v", s)
	}

	// Three such minutes over the disk line warn about the disk, and say
	// nothing either way about memory.
	full := []store.ServerSample{got[0], got[0], got[0]}
	crossed, clear := evaluateServer(store.ServerAlerts{DiskPct: 85, MemoryPct: 90}, full)
	if len(crossed) != 1 || crossed[0].name != "disk" {
		t.Fatalf("crossed %+v", crossed)
	}
	if slices.Contains(clear, "memory") {
		t.Fatal("minutes nobody measured ended a memory warning")
	}
}
