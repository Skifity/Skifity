package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skifity/internal/auth"
	"skifity/internal/config"
	"skifity/internal/crypto"
	"skifity/internal/events"
	"skifity/internal/store"
)

// Upgrading the panel, and what it leaves behind to go back with.

// upgradingCluster answers only the upgrade.
type upgradingCluster struct {
	Cluster
	to []string
}

func (u *upgradingCluster) UpgradePanel(_ context.Context, version string) (string, error) {
	u.to = append(u.to, version)
	return "ghcr.io/example/skifity:v0.1.0", nil
}

func TestAnUpgradeCopiesTheDatabaseFirstAndSaysHowToGoBack(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	keyring, err := crypto.InitKeyring(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(db, keyring, time.Hour, false)
	admin := store.User{Email: "admin@example.test", Name: "Admin", PasswordHash: "x", IsAdmin: true}
	if err := db.CreateUser(t.Context(), &admin); err != nil {
		t.Fatal(err)
	}
	team := store.Team{Name: "Ops", Slug: "ops"}
	if err := db.CreateTeam(t.Context(), &team); err != nil {
		t.Fatal(err)
	}
	if err := db.AddMember(t.Context(), team.ID, admin.ID, store.RoleOwner); err != nil {
		t.Fatal(err)
	}
	_, token, err := authService.CreateAPIToken(t.Context(), admin.ID, team.ID, "test", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cluster := &upgradingCluster{}
	server := httptest.NewServer(New(Options{
		Config: config.Config{Namespace: "skifity-system"}, DB: db, Keyring: keyring, Auth: authService,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler), Cluster: cluster,
	}))
	t.Cleanup(server.Close)

	// A copy restore-db kept aside is not one an upgrade may clear out.
	kept := filepath.Join(dir, "panel.db.before-restore-20260101-000000")
	if err := os.WriteFile(kept, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}

	upgrade := func(version string) map[string]any {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/upgrade",
			strings.NewReader(`{"version":"`+version+`"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var answer map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&answer)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("upgrading to %s answered %d: %v", version, resp.StatusCode, answer)
		}
		return answer
	}

	answer := upgrade("v1.2.0")
	snapshot, _ := answer["snapshot"].(string)
	if !strings.HasPrefix(snapshot, filepath.Join(dir, "panel.db.before-upgrade-")) {
		t.Fatalf("the snapshot is %q", snapshot)
	}
	inspection, err := store.Inspect(t.Context(), snapshot)
	if err != nil || inspection.Integrity != "ok" || inspection.Users != 1 {
		t.Fatalf("the snapshot is not the database: %+v %v", inspection, err)
	}
	// Going back is the old image and the copy: the old version refuses a
	// database the new one has migrated.
	undo, _ := answer["if_it_goes_wrong"].(string)
	if !strings.Contains(undo, "admin restore-db --yes "+snapshot) || !strings.Contains(undo, "rollout undo deploy/skifity-panel") {
		t.Fatalf("the way back is %q", undo)
	}
	// And the same four steps as commands alone, which the interface prints.
	commands, _ := answer["rollback"].([]any)
	if len(commands) != 4 {
		t.Fatalf("the rollback is %v", answer["rollback"])
	}
	for i, want := range []string{"--replicas=0", "admin restore-db --yes " + snapshot, "rollout undo deploy/skifity-panel", "--replicas=1"} {
		if got, _ := commands[i].(string); !strings.Contains(got, want) {
			t.Errorf("rollback step %d is %q, want it to contain %q", i+1, got, want)
		}
	}

	for _, version := range []string{"v1.2.1", "v1.2.2", "v1.3.0"} {
		upgrade(version)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "panel.db.before-upgrade-*"))
	if len(left) != keptUpgradeSnapshots {
		t.Fatalf("%d copies are left after four upgrades, want %d: %v", len(left), keptUpgradeSnapshots, left)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatal("an upgrade removed the copy restore-db kept aside")
	}
	if len(cluster.to) != 4 {
		t.Fatalf("the cluster was asked for %v", cluster.to)
	}
}
