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
	if err := json.Unmarshal([]byte(body), &alerts); status != http.StatusOK || err != nil ||
		alerts.MemoryPct != 80 || alerts.CPUPct != 95 || alerts.Restarts != 0 {
		t.Fatalf("setting them answered %d %s", status, body)
	}
	if events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "app.alerts_changed", "", 5); len(events) != 1 {
		t.Fatal("the change was not audited")
	}
}
