package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/settings"
	"skifity/internal/store"
)

// scanRecorder is a scanner that remembers who asked for what.
type scanRecorder struct{ asked []string }

func (s *scanRecorder) Scan(_ context.Context, appID, requestedBy string) (store.ImageScan, error) {
	s.asked = append(s.asked, appID+" by "+requestedBy)
	return store.ImageScan{ID: "scan_queued", AppID: appID, Image: "registry.example.test/acme/web:2",
		Trigger: store.ScanTriggerManual, Status: store.ScanQueued, Findings: []store.ScanFinding{}}, nil
}

// deployRecorder is a deployer that remembers the requests it was given.
type deployRecorder struct {
	fakeDeployer
	requests []DeployRequest
}

func (d *deployRecorder) Deploy(_ context.Context, req DeployRequest) (store.Deployment, error) {
	d.requests = append(d.requests, req)
	return store.Deployment{ID: "dep_1", AppID: req.AppID, Number: 1,
		AcceptedVulnerabilities: req.AcceptVulnerabilities}, nil
}

// scanned gives an app a deployment that succeeded and a report on its image.
func scanned(t *testing.T, h *harness, app store.App, image string, result store.ScanResult) store.ImageScan {
	t.Helper()
	ctx := t.Context()
	deployment := store.Deployment{AppID: app.ID, Image: image}
	if err := h.db.CreateDeployment(ctx, &deployment); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(ctx, `UPDATE deployments SET status = 'succeeded' WHERE id = ?`, deployment.ID); err != nil {
		t.Fatal(err)
	}
	scan := store.ImageScan{AppID: app.ID, DeploymentID: deployment.ID, Image: image, Trigger: store.ScanTriggerDeploy}
	if err := h.db.CreateImageScan(ctx, &scan); err != nil {
		t.Fatal(err)
	}
	if err := h.db.FinishImageScan(ctx, scan.ID, result); err != nil {
		t.Fatal(err)
	}
	return scan
}

// A viewer reads an app's vulnerabilities; a member asks for a scan, which is
// audited; a viewer cannot; and another team's app is not there at all.
func TestAnAppsVulnerabilitiesAreReadByViewersAndScannedByMembers(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	app := h.app(acme, "web")
	viewer := h.newMember(acme, "watcher", store.RoleViewer)
	member := h.newMember(acme, "dev", store.RoleMember)
	scanner := &scanRecorder{}
	h.api.scanner = scanner

	result := store.ScanResult{
		Counts: store.ScanCounts{Critical: 1, High: 2}, Fixable: 1, FixableCritical: 1, ScannerVersion: "0.74.0",
		Findings: []store.ScanFinding{{ID: "CVE-2025-29927", Package: "next", Installed: "14.2.3", FixedIn: "14.2.25", Severity: "CRITICAL"}},
	}
	scanned(t, h, app, "registry.example.test/acme/web:1", result)

	path := "/api/apps/" + app.ID + "/vulnerabilities"
	status, body := h.do(viewer, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("a viewer reading vulnerabilities got %d: %s", status, body)
	}
	var report vulnerabilityAnswer
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Enabled || report.Blocking || !report.Current || report.Scan == nil ||
		report.Scan.Counts.Critical != 1 || len(report.Scan.Findings) != 1 || report.Latest != nil {
		t.Errorf("the report is %s", body)
	}

	if status, _ := h.do(viewer, http.MethodPost, path+"/scan", nil); status != http.StatusForbidden {
		t.Errorf("a viewer started a scan: %d", status)
	}
	status, body = h.do(member, http.MethodPost, path+"/scan", nil)
	if status != http.StatusAccepted || !strings.Contains(body, "scan_queued") {
		t.Fatalf("a member starting a scan got %d: %s", status, body)
	}
	if len(scanner.asked) != 1 || scanner.asked[0] != app.ID+" by "+member.user.ID {
		t.Errorf("the scanner was asked %v", scanner.asked)
	}
	if events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "app.scan_started", "", 5); len(events) != 1 {
		t.Errorf("starting a scan was audited %d times", len(events))
	}

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		target := path
		if method == http.MethodPost {
			target += "/scan"
		}
		if status, _ := h.do(globex, method, target, nil); status != http.StatusNotFound {
			t.Errorf("another team's %s %s answered %d, want 404", method, target, status)
		}
	}
	if len(scanner.asked) != 1 {
		t.Errorf("refused requests scanned something: %v", scanner.asked)
	}

	// Deployed since, and switched to stopping deploys: the report says it is
	// of an earlier image, and that deploys are being stopped.
	deployment := store.Deployment{AppID: app.ID, Image: "registry.example.test/acme/web:2"}
	if err := h.db.CreateDeployment(t.Context(), &deployment); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(t.Context(), `UPDATE deployments SET status = 'succeeded' WHERE id = ?`, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.db.SetSetting(t.Context(), settings.KeyScanBlockCritical, "true", false, "test"); err != nil {
		t.Fatal(err)
	}
	_, body = h.do(viewer, http.MethodGet, path, nil)
	report = vulnerabilityAnswer{}
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		t.Fatal(err)
	}
	if report.Current || report.Undeployed || !report.Blocking || report.Image != "registry.example.test/acme/web:2" {
		t.Errorf("after a deploy the report is %s", body)
	}

	// A deploy stopped for its image: the newest report is of an image that
	// never went out, and says so.
	stopped := store.Deployment{AppID: app.ID, Image: "registry.example.test/acme/web:3"}
	if err := h.db.CreateDeployment(t.Context(), &stopped); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(t.Context(), `UPDATE deployments SET status = 'failed', error_code = 'deploy.vulnerable' WHERE id = ?`, stopped.ID); err != nil {
		t.Fatal(err)
	}
	scan := store.ImageScan{AppID: app.ID, DeploymentID: stopped.ID, Image: stopped.Image, Trigger: store.ScanTriggerDeploy}
	if err := h.db.CreateImageScan(t.Context(), &scan); err != nil {
		t.Fatal(err)
	}
	if err := h.db.FinishImageScan(t.Context(), scan.ID, result); err != nil {
		t.Fatal(err)
	}
	_, body = h.do(viewer, http.MethodGet, path, nil)
	report = vulnerabilityAnswer{}
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		t.Fatal(err)
	}
	if report.Current || !report.Undeployed || report.Scan == nil || report.Scan.ID != scan.ID {
		t.Errorf("with a stopped deploy the report is %s", body)
	}
}

// The list of an environment's apps says which has something critical.
func TestTheAppListCarriesEachAppsCounts(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	web := h.app(acme, "web")
	h.app(acme, "worker")
	scanned(t, h, web, "registry.example.test/acme/web:1", store.ScanResult{Counts: store.ScanCounts{Critical: 3, Low: 1}})

	status, body := h.do(acme, http.MethodGet, "/api/environments/"+acme.env.ID+"/apps", nil)
	if status != http.StatusOK {
		t.Fatalf("listing apps got %d: %s", status, body)
	}
	var list struct {
		Items []store.App `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	for _, app := range list.Items {
		switch app.Name {
		case "web":
			if app.Vulnerabilities == nil || app.Vulnerabilities.Critical != 3 {
				t.Errorf("web's counts are %+v", app.Vulnerabilities)
			}
		case "worker":
			if app.Vulnerabilities != nil {
				t.Errorf("an app never scanned has counts %+v", app.Vulnerabilities)
			}
		}
	}
}

// Deploying past the check is asked for in so many words, reaches the
// deployer, and has an activity entry of its own.
func TestAcceptingVulnerabilitiesIsAudited(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	deployer := &deployRecorder{fakeDeployer: fakeDeployer{log: &recorder{}}}
	h.api.deployer = deployer

	path := "/api/apps/" + app.ID + "/deploy"
	if status, body := h.do(acme, http.MethodPost, path, map[string]any{}); status != http.StatusAccepted {
		t.Fatalf("a deploy got %d: %s", status, body)
	}
	if events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "app.deploy_vulnerabilities_accepted", "", 5); len(events) != 0 {
		t.Fatal("an ordinary deploy was audited as accepting vulnerabilities")
	}
	status, body := h.do(acme, http.MethodPost, path, map[string]any{"accept_vulnerabilities": true})
	if status != http.StatusAccepted || !strings.Contains(body, `"accepted_vulnerabilities":true`) {
		t.Fatalf("an accepted deploy got %d: %s", status, body)
	}
	if len(deployer.requests) != 2 || deployer.requests[0].AcceptVulnerabilities || !deployer.requests[1].AcceptVulnerabilities {
		t.Errorf("the deployer was asked %+v", deployer.requests)
	}
	events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "app.deploy_vulnerabilities_accepted", "", 5)
	if len(events) != 1 || events[0].ActorID != acme.user.ID || events[0].TargetID != app.ID {
		t.Errorf("accepting was audited as %+v", events)
	}

	// A viewer cannot deploy at all, accepted or not.
	viewer := h.newMember(acme, "watcher", store.RoleViewer)
	if status, _ := h.do(viewer, http.MethodPost, path, map[string]any{"accept_vulnerabilities": true}); status != http.StatusForbidden {
		t.Errorf("a viewer deployed past the check: %d", status)
	}
}

// Without a cluster there is no scanner, and asking for a scan says so.
func TestAScanWithNoScannerSaysWhy(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	status, body := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/vulnerabilities/scan", nil)
	if status < 400 || !strings.Contains(body, "config.missing") {
		t.Errorf("a scan with no scanner answered %d: %s", status, body)
	}
	status, body = h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/vulnerabilities", nil)
	if status != http.StatusOK || !strings.Contains(body, `"scan":null`) {
		t.Errorf("an app never scanned answered %d: %s", status, body)
	}
}
