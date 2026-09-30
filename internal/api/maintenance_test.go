package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// guardCluster records what maintenance asks of the cluster: the guard
// installed on first use, and its rules rewritten.
type guardCluster struct {
	Cluster
	mu         sync.Mutex
	installed  bool
	installs   []string
	refreshes  int
	installErr error
}

func (g *guardCluster) ComponentStatus(_ context.Context, name string) (store.ClusterComponent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.installed {
		return store.ClusterComponent{Name: name, Status: "installed"}, nil
	}
	return store.ClusterComponent{Name: name, Status: "available"}, nil
}

func (g *guardCluster) InstallComponent(_ context.Context, name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.installs = append(g.installs, name)
	if g.installErr != nil {
		return g.installErr
	}
	g.installed = true
	return nil
}

func (g *guardCluster) RefreshFirewall(context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refreshes++
	return nil
}

func TestMaintenanceStartsAndEnds(t *testing.T) {
	h := newHarness(t)
	cluster := &guardCluster{}
	h.withCluster(cluster)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	deployed(t, h, app)
	path := "/api/apps/" + app.ID + "/maintenance"
	start := map[string]any{"message": "Back at 14:00.", "allow": []string{" 203.0.113.9/24 ", "198.51.100.7", ""}}

	// Nowhere to show a page.
	if status, body := h.do(acme, http.MethodPut, path, start); status != http.StatusConflict || !strings.Contains(body, "app.maintenance_no_domain") {
		t.Fatalf("an app with no domain answered %d: %s", status, truncate(body, 200))
	}
	if err := h.db.CreateDomain(t.Context(), &store.Domain{AppID: app.ID, Hostname: "web.example.com"}); err != nil {
		t.Fatal(err)
	}

	for name, bad := range map[string]map[string]any{
		"no message":    {"message": "  "},
		"a long one":    {"message": strings.Repeat("x", maxMaintenanceMessage+1)},
		"a bad address": {"message": "m", "allow": []string{"the office"}},
	} {
		status, body := h.do(acme, http.MethodPut, path, bad)
		if status != http.StatusBadRequest || !strings.Contains(body, "app.maintenance_") {
			t.Errorf("%s answered %d: %s", name, status, truncate(body, 200))
		}
	}
	if _, err := h.db.GetMaintenance(t.Context(), app.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a refused request left the app in maintenance")
	}

	status, body := h.do(acme, http.MethodPut, path, start)
	if status != http.StatusOK {
		t.Fatalf("starting maintenance answered %d: %s", status, truncate(body, 200))
	}
	var view maintenanceView
	_ = json.Unmarshal([]byte(body), &view)
	if !view.Active || view.Message != "Back at 14:00." || strings.Join(view.Allow, ",") != "203.0.113.0/24,198.51.100.7" {
		t.Fatalf("answered %+v", view)
	}
	if len(cluster.installs) != 1 || cluster.installs[0] != "firewall" || cluster.refreshes != 1 {
		t.Fatalf("the guard was installed %v and refreshed %d times", cluster.installs, cluster.refreshes)
	}

	// The app says so, for the notice at the top of its page.
	_, body = h.do(acme, http.MethodGet, "/api/apps/"+app.ID, nil)
	if !strings.Contains(body, `"maintenance":{`) {
		t.Fatalf("the app does not say it is in maintenance: %s", truncate(body, 300))
	}

	// Changing the message keeps who started it and when; the guard is not
	// installed twice.
	first, _ := h.db.GetMaintenance(t.Context(), app.ID)
	if status, _ := h.do(acme, http.MethodPut, path, map[string]any{"message": "Back at 15:00."}); status != http.StatusOK {
		t.Fatalf("changing the message answered %d", status)
	}
	second, _ := h.db.GetMaintenance(t.Context(), app.ID)
	if second.Message != "Back at 15:00." || second.StartedAt != first.StartedAt || second.StartedBy != first.StartedBy || len(second.Allow) != 0 {
		t.Fatalf("after a change: %+v, first %+v", second, first)
	}
	if len(cluster.installs) != 1 {
		t.Fatalf("the guard was installed again: %v", cluster.installs)
	}

	if status, _ := h.do(acme, http.MethodDelete, path, nil); status != http.StatusOK {
		t.Fatalf("ending maintenance answered %d", status)
	}
	_, body = h.do(acme, http.MethodGet, path, nil)
	_ = json.Unmarshal([]byte(body), &view)
	if view.Active {
		t.Fatal("maintenance did not end")
	}
	for _, action := range []string{"app.maintenance_started", "app.maintenance_ended"} {
		if events, _ := h.db.ListAudit(t.Context(), acme.team.ID, action, "", 10); len(events) == 0 {
			t.Errorf("%s was not audited", action)
		}
	}
}

// If the guard cannot be installed, nobody sees the page — so the panel must
// not say the app is in maintenance.
func TestMaintenanceThatCannotBeEnforcedIsNotRecorded(t *testing.T) {
	h := newHarness(t)
	h.withCluster(&guardCluster{installErr: errors.New("no Traefik")})
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	deployed(t, h, app)
	if err := h.db.CreateDomain(t.Context(), &store.Domain{AppID: app.ID, Hostname: "web.example.com"}); err != nil {
		t.Fatal(err)
	}
	status, _ := h.do(acme, http.MethodPut, "/api/apps/"+app.ID+"/maintenance", map[string]any{"message": "m"})
	if status < 400 {
		t.Fatalf("answered %d", status)
	}
	if _, err := h.db.GetMaintenance(t.Context(), app.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("maintenance nobody sees was recorded as started")
	}
}

// failingSyncDeployer cannot re-apply an app: the Kubernetes API refused.
type failingSyncDeployer struct{ fakeDeployer }

func (*failingSyncDeployer) Sync(context.Context, string) error {
	return errors.New("the API server refused")
}

func TestMaintenanceWhoseIngressWasNotChangedIsNotInForce(t *testing.T) {
	// The guard can be installed and its rules written while the app's
	// Ingress, which is what sends visitors through it, is not changed.
	// That was logged and answered as success: a page telling the team
	// visitors see the notice while they see the app.
	h := newHarness(t)
	h.withCluster(&guardCluster{})
	h.api.deployer = &failingSyncDeployer{fakeDeployer{log: &recorder{}}}
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	deployed(t, h, app)
	if err := h.db.CreateDomain(t.Context(), &store.Domain{AppID: app.ID, Hostname: "web.example.com"}); err != nil {
		t.Fatal(err)
	}
	path := "/api/apps/" + app.ID + "/maintenance"
	if status, _ := h.do(acme, http.MethodPut, path, map[string]any{"message": "m"}); status < 400 {
		t.Fatalf("starting answered %d", status)
	}
	if _, err := h.db.GetMaintenance(t.Context(), app.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("maintenance nobody is sent through was recorded as started")
	}

	// Ended while the Ingress cannot be changed: still in force, so still
	// recorded.
	if err := h.db.StartMaintenance(t.Context(), &store.Maintenance{AppID: app.ID, Message: "m", StartedBy: "x"}); err != nil {
		t.Fatal(err)
	}
	if status, _ := h.do(acme, http.MethodDelete, path, nil); status < 400 {
		t.Fatalf("ending answered %d", status)
	}
	if _, err := h.db.GetMaintenance(t.Context(), app.ID); err != nil {
		t.Fatal("maintenance still in force was recorded as over")
	}
}

// deployed gives an app a version that ran, which is what maintenance is put
// in front of.
func deployed(t *testing.T, h *harness, app store.App) {
	t.Helper()
	deployment := store.Deployment{AppID: app.ID, Trigger: "manual", Image: "registry.local/acme/web:1"}
	if err := h.db.CreateDeployment(t.Context(), &deployment); err != nil {
		t.Fatal(err)
	}
	if err := h.db.UpdateDeploymentStatus(t.Context(), deployment.ID, store.DeploySucceeded, "", "", ""); err != nil {
		t.Fatal(err)
	}
}

// unreadySyncDeployer applies an app and then finds it not ready: what a
// crash-looping app, the usual reason for maintenance, gives.
type unreadySyncDeployer struct{ fakeDeployer }

func (*unreadySyncDeployer) Sync(context.Context, string) error {
	return errdoc.RolloutTimedOut("web", 0, 1, "CrashLoopBackOff")
}

func TestMaintenanceInFrontOfACrashingAppIsInForce(t *testing.T) {
	// The Ingress is applied before the wait for the app to be ready. The
	// wait failing meant maintenance was recorded as off while visitors saw
	// the notice.
	h := newHarness(t)
	h.withCluster(&guardCluster{})
	h.api.deployer = &unreadySyncDeployer{fakeDeployer{log: &recorder{}}}
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	path := "/api/apps/" + app.ID + "/maintenance"
	if err := h.db.CreateDomain(t.Context(), &store.Domain{AppID: app.ID, Hostname: "web.example.com"}); err != nil {
		t.Fatal(err)
	}
	// Never deployed: nothing to put the notice in front of.
	if status, body := h.do(acme, http.MethodPut, path, map[string]any{"message": "m"}); status != http.StatusBadRequest {
		t.Fatalf("maintenance on an app never deployed answered %d: %s", status, truncate(body, 200))
	}
	deployed(t, h, app)
	if status, body := h.do(acme, http.MethodPut, path, map[string]any{"message": "m"}); status != http.StatusOK {
		t.Fatalf("maintenance on a crashing app answered %d: %s", status, truncate(body, 200))
	}
	if _, err := h.db.GetMaintenance(t.Context(), app.ID); err != nil {
		t.Fatalf("maintenance in force was not recorded: %v", err)
	}
}
