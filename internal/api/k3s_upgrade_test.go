package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// upgradeCluster is a cluster of one server and one agent, one minor version
// behind, whose components are a version behind too.
type upgradeCluster struct {
	Cluster
	started    []string
	upgraded   []string
	components map[string]store.ClusterComponent
	foreign    []string
}

func (c *upgradeCluster) K3sUpgradeNodes(context.Context) ([]kube.UpgradeNode, error) {
	return []kube.UpgradeNode{
		{Name: "control", ControlPlane: true, Ready: true, Version: "v1.35.9+k3s1"},
		{Name: "worker", Ready: true, Version: "v1.35.9+k3s1"},
	}, nil
}

func (c *upgradeCluster) K3sReleases(context.Context) ([]K3sRelease, error) {
	return []K3sRelease{{Channel: "v1.36", Version: "v1.36.4+k3s1", Stable: true}, {Channel: "v1.35", Version: "v1.35.9+k3s1"}}, nil
}

func (c *upgradeCluster) ForeignUpgradePlans(context.Context) ([]string, error) {
	return c.foreign, nil
}

func (c *upgradeCluster) StartK3sUpgrade(_ context.Context, target kube.K3sVersion) error {
	c.started = append(c.started, target.String())
	return nil
}

func (c *upgradeCluster) ComponentVersion(_ context.Context, name string) string {
	if name == "cert-manager" {
		return "1.21.2"
	}
	return ""
}

func (c *upgradeCluster) ComponentStatus(_ context.Context, name string) (store.ClusterComponent, error) {
	return c.components[name], nil
}

func (c *upgradeCluster) UpgradeInstalledComponent(_ context.Context, name string) error {
	c.upgraded = append(c.upgraded, name)
	component := c.components[name]
	component.Version = "1.21.2"
	c.components[name] = component
	return nil
}

func adminTenant(h *harness, name string) tenant {
	h.t.Helper()
	admin := h.newTenant(name)
	if _, err := h.db.Exec(h.t.Context(), `UPDATE users SET is_admin = 1 WHERE id = ?`, admin.user.ID); err != nil {
		h.t.Fatal(err)
	}
	return admin
}

func TestK3sIsUpgradedOnlyWithAPlanThatHoldsAndABackup(t *testing.T) {
	h := newHarness(t)
	cluster := &upgradeCluster{components: map[string]store.ClusterComponent{}}
	h.withCluster(cluster)
	admin := adminTenant(h, "ops")

	status, body := h.do(admin, http.MethodGet, "/api/k3s/upgrade?version=v1.36.4%2Bk3s1", nil)
	var answer k3sUpgradeAnswer
	if err := json.Unmarshal([]byte(body), &answer); status != http.StatusOK || err != nil {
		t.Fatalf("the plan answered %d: %s", status, body)
	}
	if len(answer.Nodes) != 2 || len(answer.Releases) != 2 || answer.Plan == nil || len(answer.Plan.Steps) != 2 {
		t.Fatalf("the plan is %+v", answer)
	}
	// No backup of the panel yet: that is a reason not to start.
	if len(answer.Plan.Blockers) != 1 || answer.Plan.Blockers[0].Code != "no_backup" {
		t.Fatalf("an upgrade with no backup behind it: %v", answer.Plan.Blockers)
	}
	if status, body := h.do(admin, http.MethodPost, "/api/k3s/upgrade", map[string]string{"version": "v1.36.4+k3s1"}); status != http.StatusConflict ||
		!strings.Contains(body, "k3s.upgrade_blocked") {
		t.Fatalf("starting without a backup answered %d: %s", status, body)
	}

	backup := store.Backup{TargetType: panelBackupTarget, TargetID: panelBackupTarget, Status: "running", Kind: "manual"}
	if err := h.db.CreateBackup(t.Context(), &backup); err != nil {
		t.Fatal(err)
	}
	if err := h.db.FinishBackup(t.Context(), backup.ID, "succeeded", "s3://bucket/panel.db.gz", 1024, ""); err != nil {
		t.Fatal(err)
	}

	// Two minors at once is still refused, with the version to go to first.
	if _, body := h.do(admin, http.MethodPost, "/api/k3s/upgrade", map[string]string{"version": "v1.37.0+k3s1"}); !strings.Contains(body, "skips v1.36") {
		t.Fatalf("skipping a minor: %s", body)
	}
	if len(cluster.started) != 0 {
		t.Fatal("a refused upgrade was started")
	}

	// Plans somebody applied by hand would upgrade the same servers beside
	// the panel's.
	cluster.foreign = []string{"agent-plan", "server-plan"}
	if _, body := h.do(admin, http.MethodPost, "/api/k3s/upgrade", map[string]string{"version": "v1.36.4+k3s1"}); !strings.Contains(body, "agent-plan server-plan") {
		t.Fatalf("beside plans the panel did not write: %s", body)
	}
	if len(cluster.started) != 0 {
		t.Fatal("an upgrade was started beside another set of plans")
	}
	cluster.foreign = nil

	status, body = h.do(admin, http.MethodPost, "/api/k3s/upgrade", map[string]string{"version": "v1.36.4+k3s1"})
	if status != http.StatusAccepted || len(cluster.started) != 1 || cluster.started[0] != "v1.36.4+k3s1" {
		t.Fatalf("starting answered %d (%v): %s", status, cluster.started, body)
	}
	if entries, _ := h.db.ListAudit(t.Context(), admin.team.ID, "cluster.k3s_upgrade_started", "", 5); len(entries) != 1 {
		t.Fatalf("the upgrade was not audited: %d entries", len(entries))
	}

	// A team owner who is not a panel administrator cannot.
	owner := h.newTenant("shop")
	if status, _ := h.do(owner, http.MethodPost, "/api/k3s/upgrade", map[string]string{"version": "v1.36.4+k3s1"}); status != http.StatusForbidden {
		t.Fatalf("a team owner starting a k3s upgrade answered %d", status)
	}
}

func TestAnInstalledComponentOffersItsNewerVersion(t *testing.T) {
	h := newHarness(t)
	cluster := &upgradeCluster{components: map[string]store.ClusterComponent{
		"cert-manager": {Name: "cert-manager", Status: "installed", Version: "1.20.0"},
	}}
	h.withCluster(cluster)
	admin := adminTenant(h, "ops")
	if err := h.db.SetComponent(t.Context(), cluster.components["cert-manager"]); err != nil {
		t.Fatal(err)
	}

	_, body := h.do(admin, http.MethodGet, "/api/components", nil)
	var listed struct {
		Items []struct {
			Name             string `json:"name"`
			Version          string `json:"version"`
			WantedVersion    string `json:"wanted_version"`
			UpgradeAvailable bool   `json:"upgrade_available"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatal(err)
	}
	for _, item := range listed.Items {
		switch item.Name {
		case "cert-manager":
			if item.Version != "1.20.0" || item.WantedVersion != "1.21.2" || !item.UpgradeAvailable {
				t.Errorf("cert-manager is listed as %+v", item)
			}
		default:
			if item.UpgradeAvailable {
				t.Errorf("%s offers an upgrade it cannot have", item.Name)
			}
		}
	}

	status, body := h.do(admin, http.MethodPost, "/api/components/cert-manager/upgrade", nil)
	if status != http.StatusOK || !strings.Contains(body, `"version":"1.21.2"`) {
		t.Fatalf("upgrading answered %d: %s", status, body)
	}
	if entries, _ := h.db.ListAudit(t.Context(), "", "component.upgraded", "cert-manager", 5); len(entries) != 1 {
		t.Fatalf("the upgrade was not audited")
	}
	if status, _ := h.do(admin, http.MethodPost, "/api/components/monitoring/upgrade", nil); status != http.StatusNotFound {
		t.Fatalf("upgrading a component the panel does not install answered %d", status)
	}
}

func TestAComponentIsOfferedOnlyANewerVersion(t *testing.T) {
	for _, c := range []struct {
		installed, wanted string
		offered           bool
	}{
		{"1.20.0", "1.21.2", true},
		{"1.21.2", "1.21.2", false},
		// A setting cleared after a newer manifest was put in: not an upgrade.
		{"1.22.0", "1.21.2", false},
		{"1.9.0", "1.10.0", true},
		// Installed before versions were recorded: offered, and said so.
		{"", "1.21.2", true},
		{"1.21.2", "", false},
	} {
		if got := settings.UpgradeOffered(c.installed, c.wanted); got != c.offered {
			t.Errorf("%q → %q offered %v, want %v", c.installed, c.wanted, got, c.offered)
		}
	}
}
