package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/events"
	"skifity/internal/store"
)

// fakeDriftDetector answers the same report for every app, and counts the
// times it was asked to put one back.
type fakeDriftDetector struct {
	mu       sync.Mutex
	report   DriftReport
	repaired []string
}

func (f *fakeDriftDetector) CheckDrift(context.Context, string) (DriftReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.report, nil
}

func (f *fakeDriftDetector) RepairDrift(_ context.Context, appID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repaired = append(f.repaired, appID)
	f.report = DriftReport{Status: store.DriftInSync, Items: []DriftItem{}, CheckedAt: time.Now()}
	return nil
}

// eventsCluster answers the two event feeds and nothing else.
type eventsCluster struct {
	Cluster
	apps, databases []string
}

var twoEvents = []ObjectEvent{
	{Type: "Warning", Reason: "BackOff", Kind: "Pod", Name: "web-7d4f8b9c5-x2x9q", Message: "Back-off restarting failed container",
		Count: 14, ExplanationCode: "crash_backoff", Explanation: "The app keeps stopping soon after it starts."},
	{Type: "Normal", Reason: "Pulled", Kind: "Pod", Name: "web-7d4f8b9c5-x2x9q", Message: "Container image already present", Count: 1},
}

func (c *eventsCluster) AppEvents(_ context.Context, app store.App, _ store.Environment) ([]ObjectEvent, error) {
	c.apps = append(c.apps, app.ID)
	return twoEvents, nil
}

func (c *eventsCluster) DatabaseEvents(_ context.Context, database store.Database, _ store.Environment) ([]ObjectEvent, error) {
	c.databases = append(c.databases, database.ID)
	return twoEvents[:1], nil
}

func (h *harness) withDrift(d DriftDetector, c Cluster) {
	h.t.Helper()
	h.api = New(Options{
		DB: h.db, Keyring: h.keyring, Auth: h.auth,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler),
		Cluster: c, Drift: d,
	})
	h.server.Config.Handler = h.api
}

func TestAViewerSeesWhatChangedAndCannotPutItBack(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	viewer := h.newMember(acme, "viewer", store.RoleViewer)
	member := h.newMember(acme, "member", store.RoleMember)
	app := h.app(acme, "web")
	detector := &fakeDriftDetector{report: DriftReport{Status: store.DriftDrifted, Items: []DriftItem{{
		Kind: "Deployment", Name: "web", Path: "spec.replicas", Change: "changed", Panel: "1", Live: "4", ChangedBy: "kubectl",
	}}}}
	h.withDrift(detector, &eventsCluster{})

	status, body := h.do(viewer, http.MethodGet, "/api/apps/"+app.ID+"/drift", nil)
	if status != http.StatusOK {
		t.Fatalf("a viewer reading drift: %d %s", status, body)
	}
	var report DriftReport
	if err := json.Unmarshal([]byte(body), &report); err != nil || report.Status != store.DriftDrifted ||
		len(report.Items) != 1 || report.Items[0].ChangedBy != "kubectl" || report.AutoRepair {
		t.Fatalf("a viewer read %+v, %v", report, err)
	}

	for _, attempt := range []struct{ method, path string }{
		{http.MethodPost, "/api/apps/" + app.ID + "/drift/repair"},
		{http.MethodPut, "/api/apps/" + app.ID + "/drift"},
	} {
		if status, body := h.do(viewer, attempt.method, attempt.path, map[string]any{"auto_repair": true}); status != http.StatusForbidden {
			t.Errorf("a viewer's %s %s answered %d, want 403\n%s", attempt.method, attempt.path, status, body)
		}
	}
	if len(detector.repaired) != 0 {
		t.Fatalf("a viewer put the app back")
	}

	// A member puts it back, and it is in the audit log.
	status, body = h.do(member, http.MethodPost, "/api/apps/"+app.ID+"/drift/repair", nil)
	if status != http.StatusOK {
		t.Fatalf("a member putting the app back: %d %s", status, body)
	}
	if err := json.Unmarshal([]byte(body), &report); err != nil || report.Status != store.DriftInSync {
		t.Errorf("after putting it back the app is %+v", report)
	}
	if len(detector.repaired) != 1 || detector.repaired[0] != app.ID {
		t.Fatalf("put back %v", detector.repaired)
	}
	if entries, _ := h.db.ListAudit(t.Context(), acme.team.ID, "app.drift_repaired", "", 5); len(entries) != 1 ||
		entries[0].ActorID != member.user.ID {
		t.Fatalf("the audit log has %+v", entries)
	}

	// And turns on putting it back automatically, which the next read says.
	if status, body := h.do(member, http.MethodPut, "/api/apps/"+app.ID+"/drift", map[string]any{"auto_repair": true}); status != http.StatusOK {
		t.Fatalf("turning on putting back automatically: %d %s", status, body)
	}
	if stored, _ := h.db.GetAppDrift(t.Context(), app.ID); !stored.AutoRepair {
		t.Fatal("putting back automatically was not stored")
	}
	if entries, _ := h.db.ListAudit(t.Context(), acme.team.ID, "app.drift_auto_repair_on", "", 5); len(entries) != 1 {
		t.Fatalf("switching it on was not audited: %+v", entries)
	}
	if status, body := h.do(member, http.MethodPut, "/api/apps/"+app.ID+"/drift", map[string]any{}); status != http.StatusBadRequest {
		t.Errorf("a request that says neither answered %d\n%s", status, body)
	}
}

func TestAnotherTeamsDriftAndEventsAreNotThere(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	app := h.app(acme, "web")
	database := h.database(acme, "shop-db")
	detector := &fakeDriftDetector{report: DriftReport{Status: store.DriftInSync}}
	c := &eventsCluster{}
	h.withDrift(detector, c)

	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/apps/" + app.ID + "/drift"},
		{http.MethodPut, "/api/apps/" + app.ID + "/drift"},
		{http.MethodPost, "/api/apps/" + app.ID + "/drift/repair"},
		{http.MethodGet, "/api/apps/" + app.ID + "/events"},
		{http.MethodGet, "/api/databases/" + database.ID + "/events"},
	} {
		if status, body := h.do(globex, request.method, request.path, map[string]any{"auto_repair": true}); status != http.StatusNotFound {
			t.Errorf("another team's %s %s answered %d, want 404\n%s", request.method, request.path, status, body)
		}
	}
	if len(detector.repaired) != 0 || len(c.apps) != 0 || len(c.databases) != 0 {
		t.Fatalf("another team reached the cluster: repaired %v, app events %v, database events %v",
			detector.repaired, c.apps, c.databases)
	}
}

func TestAViewerReadsTheEventsAndCanKeepTheWarnings(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	viewer := h.newMember(acme, "viewer", store.RoleViewer)
	app := h.app(acme, "web")
	database := h.database(acme, "shop-db")
	h.withDrift(&fakeDriftDetector{}, &eventsCluster{})

	var answer struct {
		Items []ObjectEvent `json:"items"`
	}
	status, body := h.do(viewer, http.MethodGet, "/api/apps/"+app.ID+"/events", nil)
	if status != http.StatusOK {
		t.Fatalf("a viewer reading events: %d %s", status, body)
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil || len(answer.Items) != 2 {
		t.Fatalf("read %s", body)
	}
	status, body = h.do(viewer, http.MethodGet, "/api/apps/"+app.ID+"/events?type=warning", nil)
	if err := json.Unmarshal([]byte(body), &answer); status != http.StatusOK || err != nil ||
		len(answer.Items) != 1 || answer.Items[0].Reason != "BackOff" || answer.Items[0].ExplanationCode != "crash_backoff" {
		t.Fatalf("the warnings are %d %s", status, body)
	}
	status, body = h.do(viewer, http.MethodGet, "/api/databases/"+database.ID+"/events", nil)
	if status != http.StatusOK || !strings.Contains(body, "BackOff") {
		t.Fatalf("a viewer reading a database's events: %d %s", status, body)
	}
}

// With no cluster the panel says so, rather than answering an empty list that
// reads as "nothing happened".
func TestWithNoClusterTheFeedsSaySo(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	for _, path := range []string{"/api/apps/" + app.ID + "/drift", "/api/apps/" + app.ID + "/events"} {
		if status, body := h.do(acme, http.MethodGet, path, nil); status != http.StatusServiceUnavailable ||
			!strings.Contains(body, "cluster.unreachable") {
			t.Errorf("GET %s with no cluster: %d %s", path, status, body)
		}
	}
}
