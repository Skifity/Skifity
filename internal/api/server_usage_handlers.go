package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// A server's usage over time, disk included, and the thresholds it is watched
// against. The watcher keeps a sample a minute for three days; see
// internal/watch/servers_usage.go.

// serverPoint is one bucket of a server's samples, as shares of what it has.
// Each is the highest in the bucket: a minute at the limit is the one that
// matters. Disk is missing from a bucket where it could not be read.
type serverPoint struct {
	At        time.Time `json:"at"`
	CPUPct    *int      `json:"cpu_pct,omitempty"`
	MemoryPct *int      `json:"memory_pct,omitempty"`
	DiskPct   *int      `json:"disk_pct,omitempty"`
	Pods      int       `json:"pods"`
}

func (s *Server) handleServerUsage(w http.ResponseWriter, r *http.Request) {
	server, _, err := s.authorizeServer(r, chi.URLParam(r, "serverID"), store.RoleViewer)
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
	samples, err := s.db.ServerSamples(r.Context(), server.ID, now.Add(-span))
	if err != nil {
		writeError(w, r, err)
		return
	}
	bucket := max(span/maxPoints, time.Minute)
	answer := map[string]any{
		"range":          name,
		"bucket_seconds": int(bucket.Seconds()),
		"points":         bucketServerSamples(samples, bucket),
	}
	if latest, err := s.db.LatestServerSample(r.Context(), server.ID); err == nil {
		answer["disk"] = map[string]any{
			"used_mb": latest.DiskUsedMB, "capacity_mb": latest.DiskCapacityMB, "at": latest.At,
		}
	}
	writeJSON(w, http.StatusOK, answer)
}

// bucketServerSamples folds samples into buckets of a fixed width, oldest
// first. A bucket with no samples is left out rather than drawn as zero.
func bucketServerSamples(samples []store.ServerSample, width time.Duration) []serverPoint {
	out := []serverPoint{}
	var current serverPoint
	started := false
	for _, sample := range samples {
		start := sample.At.Truncate(width)
		if started && !start.Equal(current.At) {
			out = append(out, current)
			started = false
		}
		if !started {
			current = serverPoint{At: start}
			started = true
		}
		// A share is only drawn from a minute where what it is a share of was
		// known; a bucket with none has no point, not a zero.
		highest := func(into **int, used, capacity int64) {
			if capacity <= 0 {
				return
			}
			pct := store.Percent(used, capacity)
			if *into == nil || pct > **into {
				*into = &pct
			}
		}
		highest(&current.CPUPct, sample.CPUM, sample.CPUCapacityM)
		highest(&current.MemoryPct, sample.MemoryMB, sample.MemoryCapacityMB)
		highest(&current.DiskPct, sample.DiskUsedMB, sample.DiskCapacityMB)
		current.Pods = max(current.Pods, sample.Pods)
	}
	if started {
		out = append(out, current)
	}
	return out
}

func (s *Server) handleGetServerAlerts(w http.ResponseWriter, r *http.Request) {
	server, _, err := s.authorizeServer(r, chi.URLParam(r, "serverID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	alerts, err := s.db.GetServerAlerts(r.Context(), server.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

// handleSetServerAlerts changes a server's thresholds. Zero turns one off.
// Admin, like everything else about a server.
func (s *Server) handleSetServerAlerts(w http.ResponseWriter, r *http.Request) {
	server, _, err := s.authorizeServer(r, chi.URLParam(r, "serverID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req store.ServerAlerts
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	for _, pct := range []int{req.DiskPct, req.MemoryPct, req.CPUPct} {
		if pct != 0 && (pct < 50 || pct > 100) {
			writeError(w, r, errdoc.BadRequest("A percentage threshold is between 50 and 100, or 0 for off."))
			return
		}
	}
	if err := s.db.SetServerAlerts(r.Context(), server.ID, req); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, server.TeamID, "server.alerts_changed", "server", server.ID, server.Name)
	stored, err := s.db.GetServerAlerts(r.Context(), server.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

// withDisk adds the watcher's last disk reading to a node's live numbers.
func (s *Server) withDisk(r *http.Request, serverID string, node NodeInfo) NodeInfo {
	latest, err := s.db.LatestServerSample(r.Context(), serverID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("could not read a server's disk", "server", serverID, "error", err)
		}
		return node
	}
	node.DiskUsedMB, node.DiskCapacityMB = latest.DiskUsedMB, latest.DiskCapacityMB
	return node
}
