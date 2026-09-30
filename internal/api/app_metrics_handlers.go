package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// An app's usage over time, and the thresholds it is watched against.

// metricRanges are the spans the chart offers, each drawn in at most
// maxPoints: a day at one point a minute is 1,440 points, which a phone draws
// slowly and nobody reads.
var metricRanges = map[string]time.Duration{
	"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "72h": 72 * time.Hour,
}

const maxPoints = 120

// metricPoint is one bucket of samples.
type metricPoint struct {
	At       time.Time `json:"at"`
	CPUM     int64     `json:"cpu_m"`
	MemoryMB int64     `json:"memory_mb"`
	// The peaks are the highest in the bucket, not the average: a minute at
	// the limit is the minute that matters.
	CPUPeakPct    int `json:"cpu_peak_pct"`
	MemoryPeakPct int `json:"memory_peak_pct"`
	Ready         int `json:"ready"`
	Desired       int `json:"desired"`
	Restarts      int `json:"restarts"`
}

func (s *Server) handleAppMetrics(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	name := r.URL.Query().Get("range")
	if name == "" {
		name = "6h"
	}
	span, ok := metricRanges[name]
	if !ok {
		writeError(w, r, errdoc.BadRequest("The range is 1h, 6h, 24h or 72h."))
		return
	}
	now := time.Now().UTC()
	samples, err := s.db.AppSamples(r.Context(), app.ID, now.Add(-span))
	if err != nil {
		writeError(w, r, err)
		return
	}
	bucket := max(span/maxPoints, time.Minute)
	writeJSON(w, http.StatusOK, map[string]any{
		"range":          name,
		"bucket_seconds": int(bucket.Seconds()),
		// Per instance, which is what the peaks are a share of.
		"limits": map[string]int{"cpu_m": app.CPULimitM, "memory_mb": app.MemLimitMB},
		"points": bucketSamples(samples, bucket),
	})
}

// bucketSamples averages samples into buckets of a fixed width, oldest first.
// A bucket with no samples is left out rather than drawn as zero: nothing was
// read then, which is not the same as nothing being used.
func bucketSamples(samples []store.AppSample, width time.Duration) []metricPoint {
	out := []metricPoint{}
	var current metricPoint
	var count int64
	flush := func() {
		if count == 0 {
			return
		}
		current.CPUM /= count
		current.MemoryMB /= count
		out = append(out, current)
	}
	for _, sample := range samples {
		start := sample.At.Truncate(width)
		if count > 0 && !start.Equal(current.At) {
			flush()
			count = 0
		}
		if count == 0 {
			current = metricPoint{At: start, Ready: sample.Ready}
		}
		count++
		current.CPUM += sample.CPUM
		current.MemoryMB += sample.MemoryMB
		current.CPUPeakPct = max(current.CPUPeakPct, sample.CPUPeakPct)
		current.MemoryPeakPct = max(current.MemoryPeakPct, sample.MemoryPeakPct)
		current.Ready = min(current.Ready, sample.Ready)
		current.Desired = max(current.Desired, sample.Desired)
		current.Restarts = max(current.Restarts, sample.Restarts)
	}
	flush()
	return out
}

func (s *Server) handleGetAppAlerts(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	alerts, err := s.db.GetAppAlerts(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

// handleSetAppAlerts changes an app's thresholds. Zero turns one off.
func (s *Server) handleSetAppAlerts(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req store.AppAlerts
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	for _, pct := range []int{req.MemoryPct, req.CPUPct} {
		// Below half is not "close to the limit", it is an ordinary day,
		// and a threshold every day crosses is one people stop reading.
		if pct != 0 && (pct < 50 || pct > 100) {
			writeError(w, r, errdoc.BadRequest("A percentage threshold is between 50 and 100, or 0 for off."))
			return
		}
	}
	if req.Restarts < 0 || req.Restarts > 100 {
		writeError(w, r, errdoc.BadRequest("A restart threshold is between 1 and 100, or 0 for off."))
		return
	}
	if err := s.db.SetAppAlerts(r.Context(), app.ID, req); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.alerts_changed", "app", app.ID, app.Name)
	stored, err := s.db.GetAppAlerts(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}
