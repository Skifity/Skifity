package api

import (
	"math"
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
	// UsageKnown is false for a bucket with no minute whose CPU and memory
	// were known; those numbers are then not zero, only absent.
	UsageKnown bool `json:"usage_known"`
}

// TrafficSource says whether the ingress's request counters could be read.
// The watcher is one; it lives in a package that imports this one, so the
// shape is declared here.
type TrafficSource interface {
	// TrafficSource is the ingress as the last pass found it: no_cluster,
	// starting, reading, no_traefik or unreachable (internal/traffic).
	TrafficSource() string
}

// trafficPoint is one bucket of minutes of requests.
type trafficPoint struct {
	At time.Time `json:"at"`
	// Minutes is how many minutes of the bucket were counted, which the
	// requests are spread over: a rate is Requests / Minutes.
	Minutes   int   `json:"minutes"`
	Requests  int64 `json:"requests"`
	Status2xx int64 `json:"status_2xx"`
	Status3xx int64 `json:"status_3xx"`
	Status4xx int64 `json:"status_4xx"`
	Status5xx int64 `json:"status_5xx"`
	// P50Ms is the minutes' medians averaged by their requests, and P95Ms the
	// slowest minute's 95th percentile: a bucket's percentile cannot be rebuilt
	// from its minutes', and the slowest minute is the one somebody asks about,
	// the way the usage peaks are. Nil when nothing in the bucket was timed.
	P50Ms *float64 `json:"p50_ms"`
	P95Ms *float64 `json:"p95_ms"`
	// Partial is true when some minute of the bucket could not count all of
	// its traffic, so the numbers are a floor.
	Partial bool `json:"partial"`
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
	requests, err := s.db.AppTrafficSince(r.Context(), app.ID, now.Add(-span))
	if err != nil {
		writeError(w, r, err)
		return
	}
	domains, err := s.db.ListDomains(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	source := "no_cluster"
	if s.traffic != nil {
		source = s.traffic.TrafficSource()
	}
	bucket := max(span/maxPoints, time.Minute)
	writeJSON(w, http.StatusOK, map[string]any{
		"range":          name,
		"bucket_seconds": int(bucket.Seconds()),
		// Per instance, which is what the peaks are a share of.
		"limits": map[string]int{"cpu_m": app.CPULimitM, "memory_mb": app.MemLimitMB},
		"points": bucketSamples(samples, bucket),
		"traffic": map[string]any{
			// Whether the ingress could be read on the last pass, and whether
			// anything reaches this app through it at all: a worker with no
			// domain has no requests to count, which is not the same as none.
			"source": source,
			"routed": app.Port > 0 && len(domains) > 0,
			"points": bucketTraffic(requests, bucket),
		},
	})
}

// bucketTraffic adds minutes of requests into buckets of a fixed width, oldest
// first. As with usage, a bucket with no minute counted is left out rather
// than drawn as no requests.
func bucketTraffic(minutes []store.AppTraffic, width time.Duration) []trafficPoint {
	out := []trafficPoint{}
	var current trafficPoint
	var timed, medians float64
	flush := func() {
		if current.Minutes == 0 {
			return
		}
		if timed > 0 {
			p50 := math.Round(medians/timed*10) / 10
			current.P50Ms = &p50
		}
		out = append(out, current)
	}
	for _, minute := range minutes {
		start := minute.At.Truncate(width)
		if current.Minutes > 0 && !start.Equal(current.At) {
			flush()
			current = trafficPoint{}
		}
		if current.Minutes == 0 {
			current = trafficPoint{At: start}
			timed, medians = 0, 0
		}
		current.Minutes++
		current.Requests += minute.Requests
		current.Status2xx += minute.Status2xx
		current.Status3xx += minute.Status3xx
		current.Status4xx += minute.Status4xx
		current.Status5xx += minute.Status5xx
		current.Partial = current.Partial || minute.Partial
		if minute.P50Ms != nil {
			// A minute with no requests counted has no median to weigh.
			weight := float64(max(minute.Requests, 1))
			timed += weight
			medians += *minute.P50Ms * weight
		}
		if minute.P95Ms != nil && (current.P95Ms == nil || *minute.P95Ms > *current.P95Ms) {
			p95 := *minute.P95Ms
			current.P95Ms = &p95
		}
	}
	flush()
	return out
}

// bucketSamples averages samples into buckets of a fixed width, oldest first.
// A bucket with no samples is left out rather than drawn as zero: nothing was
// read then, which is not the same as nothing being used.
func bucketSamples(samples []store.AppSample, width time.Duration) []metricPoint {
	out := []metricPoint{}
	var current metricPoint
	var count, known int64
	flush := func() {
		if count == 0 {
			return
		}
		if known > 0 {
			current.CPUM /= known
			current.MemoryMB /= known
		}
		current.UsageKnown = known > 0
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
			known = 0
		}
		count++
		if !sample.UsageUnknown {
			known++
			current.CPUM += sample.CPUM
			current.MemoryMB += sample.MemoryMB
			current.CPUPeakPct = max(current.CPUPeakPct, sample.CPUPeakPct)
			current.MemoryPeakPct = max(current.MemoryPeakPct, sample.MemoryPeakPct)
		}
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
	var body struct {
		store.AppAlerts
		// Optional, and kept as it was when left out: a script written before
		// it existed sends three numbers, and must not turn it off by not
		// knowing about it.
		ServerErrorsPct *int `json:"server_errors_pct"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	req := body.AppAlerts
	if body.ServerErrorsPct != nil {
		req.ServerErrorsPct = *body.ServerErrorsPct
	} else {
		current, err := s.db.GetAppAlerts(r.Context(), app.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		req.ServerErrorsPct = current.ServerErrorsPct
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
	// Any share of failed requests can be worth hearing about — one in a
	// hundred is a lot for most apps — so the floor is 1, not 50.
	if req.ServerErrorsPct < 0 || req.ServerErrorsPct > 100 {
		writeError(w, r, errdoc.BadRequest("A server-error threshold is a percentage between 1 and 100, or 0 for off."))
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
