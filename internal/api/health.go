package api

import (
	"net/http"
	"time"

	"skifity/internal/version"
)

var startedAt = time.Now()

type healthResponse struct {
	Status        string         `json:"status"`
	Product       string         `json:"product"`
	Version       string         `json:"version"`
	Commit        string         `json:"commit"`
	UptimeSeconds int64          `json:"uptime_seconds"`
	Checks        map[string]any `json:"checks"`
}

// handleHealth is liveness: is this process working at all. It must not depend
// on the cluster, or a cluster hiccup would make Kubernetes restart the panel
// that is trying to report the hiccup.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	checks := map[string]any{}
	status := "ok"

	schemaVersion, err := s.db.SchemaVersion(r.Context())
	if err != nil {
		checks["database"] = map[string]any{"ok": false, "error": err.Error()}
		status = "degraded"
	} else {
		checks["database"] = map[string]any{"ok": true, "schema_version": schemaVersion}
	}
	checks["event_subscribers"] = s.hub.SubscriberCount()

	code := http.StatusOK
	if status != "ok" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, healthResponse{
		Status:        status,
		Product:       version.Name,
		Version:       version.Version,
		Commit:        version.Commit,
		UptimeSeconds: int64(time.Since(startedAt).Seconds()),
		Checks:        checks,
	})
}

// handleReady is readiness: should this process receive traffic. Unlike
// liveness, it does check the cluster, because a panel that cannot reach
// Kubernetes cannot do most of what a request would ask for.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	checks := map[string]any{}
	ready := true

	if _, err := s.db.SchemaVersion(r.Context()); err != nil {
		checks["database"] = map[string]any{"ok": false, "error": err.Error()}
		ready = false
	} else {
		checks["database"] = map[string]any{"ok": true}
	}

	if s.cluster == nil {
		checks["cluster"] = map[string]any{"ok": false, "error": "no cluster client configured"}
	} else {
		ctx, cancel := contextWithTimeout(r, 3*time.Second)
		defer cancel()
		if err := s.cluster.Ping(ctx); err != nil {
			// Not fatal for readiness: the panel is still useful for settings,
			// and taking it out of service would hide the cluster problem.
			checks["cluster"] = map[string]any{"ok": false, "error": err.Error()}
		} else {
			checks["cluster"] = map[string]any{"ok": true}
		}
	}

	code := http.StatusOK
	status := "ready"
	if !ready {
		code = http.StatusServiceUnavailable
		status = "not-ready"
	}
	writeJSON(w, code, map[string]any{"status": status, "checks": checks})
}
