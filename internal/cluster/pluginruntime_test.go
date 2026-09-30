package cluster

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"

	"skifity/internal/crypto"
	"skifity/internal/plugins"
	"skifity/internal/store"
)

// A plugin is installed panel-wide and runs with a token belonging to whoever
// installed it. The events it is sent have to draw the same line: an owner of
// one team must not install a plugin and have it told about another team's
// deploys — or, for a blocking hook, be able to stop them.
func TestAPluginOnlyHearsAboutTheTeamsItsInstallerIsIn(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer db.Close()

	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("create a keyring: %v", err)
	}
	c := New(nil, db, keyring, slog.New(slog.DiscardHandler))

	theirs := store.Team{Name: "Theirs", Slug: "theirs"}
	if err := db.CreateTeam(ctx, &theirs); err != nil {
		t.Fatalf("create a team: %v", err)
	}
	mine := store.Team{Name: "Mine", Slug: "mine"}
	if err := db.CreateTeam(ctx, &mine); err != nil {
		t.Fatalf("create a team: %v", err)
	}
	installer := store.User{Email: "owner@mine.test", Name: "Owner"}
	if err := db.CreateUser(ctx, &installer); err != nil {
		t.Fatalf("create the user: %v", err)
	}
	if err := db.AddMember(ctx, mine.ID, installer.ID, store.RoleOwner); err != nil {
		t.Fatalf("add the member: %v", err)
	}

	manifest, err := json.Marshal(plugins.Manifest{
		APIVersion: plugins.APIVersion,
		ID:         "com.example.watcher",
		Name:       "Watcher",
		Events:     []plugins.EventSubscription{{Event: plugins.EventDeploySucceeded}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.NewPluginSigningSecret("com.example.watcher")
	if err != nil {
		t.Fatalf("seal a signing secret: %v", err)
	}
	record := store.Plugin{
		ID: "com.example.watcher", Manifest: string(manifest), Version: "1.0.0",
		Status: "running", Enabled: true, HMACSealed: sealed, InstalledBy: installer.ID,
	}
	if err := db.CreatePlugin(ctx, &record); err != nil {
		t.Fatalf("record the plugin: %v", err)
	}

	targets, err := c.PluginTargets(ctx, plugins.EventDeploySucceeded, mine.ID)
	if err != nil {
		t.Fatalf("PluginTargets for the installer's own team: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("the plugin was not sent its own team's event: %d targets", len(targets))
	}

	targets, err = c.PluginTargets(ctx, plugins.EventDeploySucceeded, theirs.ID)
	if err != nil {
		t.Fatalf("PluginTargets for another team: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("a plugin was told about a team its installer is not in: %d targets", len(targets))
	}

	// An event that does not say which team it belongs to reaches nobody: the
	// safe direction is silence, not everybody.
	targets, err = c.PluginTargets(ctx, plugins.EventDeploySucceeded, "")
	if err != nil {
		t.Fatalf("PluginTargets with no team: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("an event with no team reached %d plugins", len(targets))
	}
}
