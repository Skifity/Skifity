package watch

import (
	"fmt"
	"testing"
	"time"

	"skifity/internal/api"
	"skifity/internal/kube"
	"skifity/internal/notify"
	"skifity/internal/store"
	"skifity/internal/traffic"
)

func minutes(start time.Time, points ...store.AppTraffic) []store.AppTraffic {
	for i := range points {
		points[i].At = start.Add(time.Duration(i) * time.Minute)
	}
	return points
}

func failing(requests, errors int64) store.AppTraffic {
	return store.AppTraffic{Requests: requests, Status5xx: errors, Status2xx: requests - errors}
}

// A share of requests failing is said when it stays, with enough requests to
// mean it, and not otherwise.
func TestServerErrorsAreSaidWhenTheyStay(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 3, 20, 0, time.UTC)
	start := now.Truncate(time.Minute).Add(-2 * time.Minute)

	cases := []struct {
		name    string
		recent  []store.AppTraffic
		crossed bool
		clear   bool
	}{
		{"three minutes over", minutes(start, failing(20, 5), failing(30, 4), failing(10, 1)), true, false},
		{"one minute is a spike", minutes(start, failing(20, 0), failing(20, 20), failing(20, 0)), false, false},
		// 100% of three requests is one visitor's bad luck, not an outage.
		{"too few requests to judge", minutes(start, failing(1, 1), failing(1, 1), failing(1, 1)), false, false},
		{"two minutes is not three", minutes(start.Add(time.Minute), failing(50, 50), failing(50, 50)), false, false},
		// A minute the ingress could not be read in breaks the run.
		{"a gap", []store.AppTraffic{
			{At: start.Add(-time.Minute), Requests: 50, Status5xx: 50},
			{At: start.Add(time.Minute), Requests: 50, Status5xx: 50},
			{At: start.Add(2 * time.Minute), Requests: 50, Status5xx: 50},
		}, false, false},
		{"minutes from before the panel stopped", minutes(start.Add(-time.Hour),
			failing(50, 50), failing(50, 50), failing(50, 50)), false, false},
		// No requests is no measurement: an app its visitors gave up on has
		// not recovered.
		{"a quiet minute", minutes(start, failing(50, 50), store.AppTraffic{}, failing(50, 50)), false, false},
		{"well under, and busy", minutes(start, failing(100, 1), failing(100, 0), failing(100, 2)), false, true},
		{"just under is not over", minutes(start, failing(100, 1), failing(100, 9), failing(100, 2)), false, false},
	}
	for _, tc := range cases {
		crossed, clear := evaluateServerErrors(10, tc.recent, now)
		if (crossed != nil) != tc.crossed || clear != tc.clear {
			t.Errorf("%s: crossed %v, clear %v; want %v, %v", tc.name, crossed, clear, tc.crossed, tc.clear)
		}
	}
	if crossed, clear := evaluateServerErrors(0, minutes(start, failing(50, 50), failing(50, 50), failing(50, 50)), now); crossed != nil || !clear {
		t.Fatal("a threshold that is off fired, or did not end")
	}
}

// The whole pass: the ingress read twice is a minute of requests for every
// app, and an app failing them is said once, with the minute that did it.
func TestRequestsAreRecordedAndFailuresSaid(t *testing.T) {
	db := newFakeStore()
	db.apps = []store.DeployedApp{
		{App: store.App{ID: "app_1", Name: "shop", Slug: "web", Status: "running"}, TeamID: "team_1", Namespace: "acme-shop-production"},
		{App: store.App{ID: "app_2", Name: "worker", Slug: "worker", Status: "running"}, TeamID: "team_1", Namespace: "acme-shop-production"},
	}
	c := &fakeCluster{apps: map[string]api.AppRuntimeStatus{
		"web": {DesiredReplicas: 1, ReadyReplicas: 1}, "worker": {DesiredReplicas: 1, ReadyReplicas: 1},
	}}
	answer := func(ok, failed int) {
		body := fmt.Sprintf(`traefik_service_requests_total{code="200",method="GET",protocol="http",service="acme-shop-production-web-80@kubernetes"} %d
traefik_service_requests_total{code="500",method="GET",protocol="http",service="acme-shop-production-web-80@kubernetes"} %d
`, ok, failed)
		c.traefik = []kube.TraefikPod{{Name: "traefik-a", Instance: "uid/0", Started: time.Now().Add(-time.Hour), Metrics: []byte(body)}}
	}
	w, notifier := testWatcher(t, db, c)
	if got := w.TrafficSource(); got != traffic.StateStarting {
		t.Fatalf("before the first pass the ingress is %q", got)
	}

	answer(100, 0)
	w.Once(t.Context())
	if len(db.traffic["app_1"]) != 0 {
		t.Fatal("the first read, which is only something to compare with, was recorded")
	}
	// Two minutes of failing, as the watcher would have recorded them, and
	// this pass's read makes the third.
	now := time.Now().UTC().Truncate(time.Minute)
	db.traffic["app_1"] = minutes(now.Add(-2*time.Minute), failing(40, 30), failing(40, 30))
	answer(110, 30)
	w.Once(t.Context())

	web, worker := db.traffic["app_1"], db.traffic["app_2"]
	if len(web) != 3 || web[2].Requests != 40 || web[2].Status5xx != 30 || web[2].Status2xx != 10 {
		t.Fatalf("the shop's minutes are %+v", web)
	}
	// Nothing reached the worker, and that is recorded as nothing rather than
	// left out: it was measured.
	if len(worker) != 1 || worker[0].Requests != 0 {
		t.Fatalf("the worker's minutes are %+v", worker)
	}
	if got := w.TrafficSource(); got != traffic.StateReading {
		t.Fatalf("the ingress is %q", got)
	}
	if n := notifier.eventsOf(notify.EventAppAlert); n != 1 {
		t.Fatalf("%d warnings, want one", n)
	}
	if msg := notifier.sent[0].msg; msg.Title != "shop is failing requests" || msg.Level != "warning" {
		t.Fatalf("the warning is %+v", msg)
	}
	if firing := db.alerts["app_1"].Firing; len(firing) != 1 || firing[0] != "errors" {
		t.Fatalf("firing is %v", firing)
	}

	// No Traefik at all is said to the page, and nothing is recorded.
	c.traefik = nil
	w.Once(t.Context())
	if got := w.TrafficSource(); got != traffic.StateNoTraefik {
		t.Fatalf("with no Traefik the ingress is %q", got)
	}
}
