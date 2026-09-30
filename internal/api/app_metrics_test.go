package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"skifity/internal/store"
)

func TestAnAppsUsageIsDrawnInBuckets(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	now := time.Now().UTC().Truncate(time.Minute)
	for i := 0; i < 60; i++ {
		sample := store.AppSample{At: now.Add(-time.Duration(59-i) * time.Minute), CPUM: 100, MemoryMB: 200,
			MemoryPeakPct: 40, Ready: 2, Desired: 2}
		if i == 30 {
			sample.MemoryPeakPct = 97
		}
		if err := h.db.RecordAppSample(t.Context(), app.ID, sample); err != nil {
			t.Fatal(err)
		}
	}

	var answer struct {
		BucketSeconds int           `json:"bucket_seconds"`
		Points        []metricPoint `json:"points"`
	}
	status, body := h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/metrics?range=1h", nil)
	if err := json.Unmarshal([]byte(body), &answer); status != http.StatusOK || err != nil {
		t.Fatalf("the metrics answered %d: %s", status, body)
	}
	if answer.BucketSeconds != 60 || len(answer.Points) < 59 {
		t.Fatalf("an hour is %d points of %ds", len(answer.Points), answer.BucketSeconds)
	}

	status, body = h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/metrics?range=6h", nil)
	if err := json.Unmarshal([]byte(body), &answer); status != http.StatusOK || err != nil {
		t.Fatalf("the metrics answered %d: %s", status, body)
	}
	// Six hours in three-minute buckets; the one minute at 97% is the peak of
	// its bucket, not averaged away.
	peak := 0
	for _, point := range answer.Points {
		peak = max(peak, point.MemoryPeakPct)
		if point.CPUM != 100 || point.MemoryMB != 200 {
			t.Fatalf("a bucket's average is %+v", point)
		}
	}
	if answer.BucketSeconds != 180 || len(answer.Points) > 21 || peak != 97 {
		t.Fatalf("six hours is %d points of %ds with a peak of %d", len(answer.Points), answer.BucketSeconds, peak)
	}

	if status, _ := h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/metrics?range=1y", nil); status != http.StatusBadRequest {
		t.Fatalf("a range that is not one answered %d", status)
	}
}

// A minute metrics-server had nothing for is kept for its restarts but is not
// averaged in as zero, and a bucket of only such minutes says so.
func TestAMinuteOfUnknownUsageIsNotAveragedAsZero(t *testing.T) {
	hour := time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)
	points := bucketSamples([]store.AppSample{
		{At: hour, CPUM: 300, MemoryMB: 400, MemoryPeakPct: 80, Ready: 1, Desired: 1},
		{At: hour.Add(time.Minute), UsageUnknown: true, Restarts: 2, Ready: 1, Desired: 1},
		{At: hour.Add(time.Hour), UsageUnknown: true, Restarts: 3, Ready: 0, Desired: 1},
	}, time.Hour)
	if len(points) != 2 {
		t.Fatalf("%d points, want two hours", len(points))
	}
	if first := points[0]; !first.UsageKnown || first.CPUM != 300 || first.MemoryMB != 400 ||
		first.MemoryPeakPct != 80 || first.Restarts != 2 {
		t.Fatalf("the first hour is %+v", first)
	}
	if second := points[1]; second.UsageKnown || second.Restarts != 3 || second.Ready != 0 {
		t.Fatalf("an hour nobody measured is %+v", second)
	}
}

// Minutes of requests are added into buckets; the percentiles are the ones a
// bucket's minutes can honestly give, and the source says why nothing is there.
func TestAnAppsRequestsAreDrawnInBuckets(t *testing.T) {
	ms := func(v float64) *float64 { return &v }
	hour := time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)
	points := bucketTraffic([]store.AppTraffic{
		{At: hour, Requests: 30, Status2xx: 28, Status5xx: 2, P50Ms: ms(10), P95Ms: ms(80)},
		{At: hour.Add(time.Minute), Requests: 10, Status2xx: 5, Status4xx: 5, P50Ms: ms(50), P95Ms: ms(400), Partial: true},
		{At: hour.Add(2 * time.Minute)},
		{At: hour.Add(time.Hour), Requests: 1, Status3xx: 1, P50Ms: ms(5), P95Ms: ms(9)},
	}, time.Hour)
	if len(points) != 2 {
		t.Fatalf("%d points, want two hours", len(points))
	}
	first := points[0]
	if first.Minutes != 3 || first.Requests != 40 || first.Status2xx != 33 || first.Status4xx != 5 ||
		first.Status5xx != 2 || !first.Partial {
		t.Fatalf("the first hour is %+v", first)
	}
	// The median is weighed by requests, (30*10 + 10*50) / 40; the 95th is the
	// slowest minute's, not an average that hides it.
	if first.P50Ms == nil || *first.P50Ms != 20 || first.P95Ms == nil || *first.P95Ms != 400 {
		t.Fatalf("the first hour's percentiles are %v %v", first.P50Ms, first.P95Ms)
	}
	if second := points[1]; second.Minutes != 1 || second.Partial || *second.P95Ms != 9 {
		t.Fatalf("the second hour is %+v", second)
	}

	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	now := time.Now().UTC().Truncate(time.Minute)
	if err := h.db.RecordAppTraffic(t.Context(), app.ID, store.AppTraffic{At: now, Requests: 7, Status2xx: 7}); err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Traffic struct {
			Source string         `json:"source"`
			Routed bool           `json:"routed"`
			Points []trafficPoint `json:"points"`
		} `json:"traffic"`
	}
	status, body := h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/metrics?range=1h", nil)
	if err := json.Unmarshal([]byte(body), &answer); status != http.StatusOK || err != nil {
		t.Fatalf("the metrics answered %d: %s", status, body)
	}
	// This panel has no watcher, and an app with no domain has nothing routed
	// to it; both are said rather than drawn as no requests.
	if answer.Traffic.Source != "no_cluster" || answer.Traffic.Routed ||
		len(answer.Traffic.Points) != 1 || answer.Traffic.Points[0].Requests != 7 {
		t.Fatalf("the requests are %+v", answer.Traffic)
	}
}

func TestAnAppsThresholds(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	path := "/api/apps/" + app.ID + "/alerts"

	var alerts store.AppAlerts
	status, body := h.do(acme, http.MethodGet, path, nil)
	if err := json.Unmarshal([]byte(body), &alerts); status != http.StatusOK || err != nil ||
		alerts.MemoryPct != 90 || alerts.Restarts != 3 || alerts.CPUPct != 0 {
		t.Fatalf("the defaults are %d %s", status, body)
	}
	if status, _ := h.do(acme, http.MethodPut, path, map[string]int{"memory_pct": 30}); status != http.StatusBadRequest {
		t.Fatalf("a threshold every day crosses answered %d", status)
	}
	status, body = h.do(acme, http.MethodPut, path, map[string]int{"memory_pct": 80, "cpu_pct": 95, "restarts": 0})
	// A client that has never heard of the server-error threshold does not
	// turn it off by leaving it out.
	if err := json.Unmarshal([]byte(body), &alerts); status != http.StatusOK || err != nil ||
		alerts.MemoryPct != 80 || alerts.CPUPct != 95 || alerts.Restarts != 0 || alerts.ServerErrorsPct != 10 {
		t.Fatalf("setting them answered %d %s", status, body)
	}
	status, body = h.do(acme, http.MethodPut, path, map[string]int{"memory_pct": 80, "cpu_pct": 95, "restarts": 0,
		"server_errors_pct": 2})
	if err := json.Unmarshal([]byte(body), &alerts); status != http.StatusOK || err != nil || alerts.ServerErrorsPct != 2 {
		t.Fatalf("setting the server-error threshold answered %d %s", status, body)
	}
	if status, _ := h.do(acme, http.MethodPut, path, map[string]int{"server_errors_pct": 101}); status != http.StatusBadRequest {
		t.Fatalf("more than all of the requests answered %d", status)
	}
	if events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "app.alerts_changed", "", 5); len(events) != 2 {
		t.Fatalf("%d changes were audited, want the two that were made", len(events))
	}
}
