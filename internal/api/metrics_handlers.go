package api

import (
	"context"
	"net/http"
	"os"
	"time"

	"skifity/internal/metrics"
	"skifity/internal/version"
)

// What the panel says about itself.
//
// Everything here is either counted as it happens or read at scrape time.
// Nothing is kept current by a background loop: a gauge that something has to
// remember to update is a gauge that is wrong after the first time somebody
// adds a code path and forgets.

// describeMetrics gives every metric a type and a sentence, which is what turns
// a page of numbers into something somebody else can build a dashboard on.
func (s *Server) describeMetrics() {
	s.metrics.Describe("skifity_build_info", "gauge",
		"The running version, as a label. Always 1.")
	s.metrics.Describe("skifity_uptime_seconds", "gauge",
		"How long this panel process has been running.")
	s.metrics.Describe("skifity_http_requests_total", "counter",
		"Requests, by method, route pattern and status.")
	s.metrics.Describe("skifity_http_request_duration_seconds", "histogram",
		"How long requests took, by route pattern.")
	s.metrics.Describe("skifity_deployments_total", "counter",
		"Deployments that reached a final state, by result.")
	s.metrics.Describe("skifity_apps", "gauge", "Apps that exist.")
	s.metrics.Describe("skifity_servers", "gauge", "Servers, by status.")
	s.metrics.Describe("skifity_deployments_in_flight", "gauge",
		"Deployments that have not finished. A number that only goes up means "+
			"something is stuck.")
	s.metrics.Describe("skifity_event_clients", "gauge",
		"Browsers and CLIs subscribed to the event stream.")
	s.metrics.Describe("skifity_database_bytes", "gauge",
		"The size of the panel's database file.")
	s.metrics.Describe("skifity_cluster_reachable", "gauge",
		"1 when the Kubernetes API answered the last time it was asked.")
	s.metrics.Describe("skifity_goroutines", "gauge", "Goroutines in this process.")
	s.metrics.Describe("skifity_memory_heap_bytes", "gauge", "Heap in use.")
	s.metrics.Describe("skifity_memory_resident_bytes", "gauge",
		"Memory obtained from the operating system.")
	s.metrics.Describe("skifity_gc_total", "gauge", "Garbage collections since start.")

	s.metrics.Collect(s.collectState)
}

// collectState samples the things that are read rather than counted.
//
// It runs on a scrape, which is the only time anybody wants the answer, and it
// is deliberately tolerant: a failing query makes its own metric absent rather
// than failing the whole page, because a monitoring endpoint that returns 500
// when the database is busy is a monitoring endpoint that alerts on itself.
func (s *Server) collectState(r *metrics.Registry) {
	r.SetGauge("skifity_build_info", 1, "version", version.Version, "commit", version.Commit)
	r.SetGauge("skifity_uptime_seconds", time.Since(metrics.StartedAt).Seconds())

	// A short deadline of its own: a scrape must not hang because SQLite is
	// mid-write, and Prometheus will ask again in fifteen seconds anyway.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if counts, err := s.db.CountApps(ctx); err == nil {
		r.SetGauge("skifity_apps", float64(counts))
	}
	if byStatus, err := s.db.CountServersByStatus(ctx); err == nil {
		for status, count := range byStatus {
			r.SetGauge("skifity_servers", float64(count), "status", status)
		}
	}
	if inFlight, err := s.db.ListUnfinishedDeployments(ctx); err == nil {
		r.SetGauge("skifity_deployments_in_flight", float64(len(inFlight)))
	}
	if s.hub != nil {
		r.SetGauge("skifity_event_clients", float64(s.hub.Subscribers()))
	}
	if info, err := os.Stat(s.db.Path()); err == nil {
		r.SetGauge("skifity_database_bytes", float64(info.Size()))
	}

	reachable := 0.0
	if s.cluster != nil && s.cluster.Ping(ctx) == nil {
		reachable = 1
	}
	r.SetGauge("skifity_cluster_reachable", reachable)
}

// handleMetrics serves the exposition format.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.metrics.Write(w); err != nil {
		s.log.Warn("could not write the metrics page", "error", err)
	}
}
