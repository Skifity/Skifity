package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"skifity/internal/store"
)

func TestAServersUsageIsBucketedWithItsDisk(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	server := h.node(acme, "web-1")

	now := time.Now().UTC().Truncate(time.Minute)
	for i, disk := range []int64{50, 70, 0} {
		sample := store.ServerSample{At: now.Add(time.Duration(i-3) * time.Minute),
			CPUM: 500, CPUCapacityM: 2000, MemoryMB: 1024 * int64(i+1), MemoryCapacityMB: 4096, Pods: 10 + i}
		if disk > 0 {
			sample.DiskUsedMB, sample.DiskCapacityMB = disk, 100
		}
		if err := h.db.RecordServerSample(t.Context(), server.ID, sample); err != nil {
			t.Fatal(err)
		}
	}

	status, body := h.do(acme, http.MethodGet, "/api/servers/"+server.ID+"/usage?range=1h", nil)
	if status != http.StatusOK {
		t.Fatalf("answered %d: %s", status, truncate(body, 200))
	}
	var answer struct {
		Points []serverPoint `json:"points"`
		Disk   struct {
			UsedMB     int64 `json:"used_mb"`
			CapacityMB int64 `json:"capacity_mb"`
		} `json:"disk"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Points) != 3 {
		t.Fatalf("%d points for three minutes at one a minute", len(answer.Points))
	}
	first, last := answer.Points[0], answer.Points[2]
	if first.CPUPct != 25 || first.MemoryPct != 25 || first.DiskPct == nil || *first.DiskPct != 50 || first.Pods != 10 {
		t.Fatalf("the first point is %+v", first)
	}
	// A minute the disk was not read is left out of the disk line, not drawn
	// as an empty disk.
	if last.DiskPct != nil {
		t.Fatalf("an unread disk was drawn as %d%%", *last.DiskPct)
	}
	// The disk shown is the last one read.
	if answer.Disk.UsedMB != 70 || answer.Disk.CapacityMB != 100 {
		t.Fatalf("the latest disk is %+v", answer.Disk)
	}

	// Wider buckets keep the highest.
	points := bucketServerSamples([]store.ServerSample{
		{At: now, DiskUsedMB: 60, DiskCapacityMB: 100, MemoryMB: 10, MemoryCapacityMB: 100},
		{At: now.Add(time.Minute), DiskUsedMB: 80, DiskCapacityMB: 100, MemoryMB: 90, MemoryCapacityMB: 100},
	}, time.Hour)
	if len(points) != 1 || *points[0].DiskPct != 80 || points[0].MemoryPct != 90 {
		t.Fatalf("an hour's bucket is %+v", points)
	}
}

func TestAServersThresholdsAreAnAdminsToChange(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	server := h.node(acme, "web-1")
	path := "/api/servers/" + server.ID + "/alerts"

	status, body := h.do(acme, http.MethodGet, path, nil)
	if status != http.StatusOK || !strings.Contains(body, `"disk_pct":85`) {
		t.Fatalf("the defaults answered %d: %s", status, body)
	}
	if status, _ := h.do(acme, http.MethodPut, path, map[string]int{"disk_pct": 30}); status != http.StatusBadRequest {
		t.Fatalf("a disk threshold of 30%% answered %d", status)
	}
	status, body = h.do(acme, http.MethodPut, path, map[string]int{"disk_pct": 80, "memory_pct": 0, "cpu_pct": 95})
	if status != http.StatusOK {
		t.Fatalf("answered %d: %s", status, body)
	}
	if stored, _ := h.db.GetServerAlerts(t.Context(), server.ID); stored.DiskPct != 80 || stored.MemoryPct != 0 || stored.CPUPct != 95 {
		t.Fatalf("stored %+v", stored)
	}
	if events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "server.alerts_changed", "", 10); len(events) != 1 {
		t.Fatalf("the change was not audited: %+v", events)
	}
}
