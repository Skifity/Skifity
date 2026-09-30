package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"skifity/internal/crypto"
	"skifity/internal/notify"
	"skifity/internal/plugins"
	"skifity/internal/store"
)

func providerCluster(t *testing.T) (*Cluster, *store.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("create a keyring: %v", err)
	}
	return New(nil, db, keyring, slog.New(slog.DiscardHandler)), db, ctx
}

// installProvider records a plugin that provides one notification channel.
func installProvider(t *testing.T, c *Cluster, db *store.DB, ctx context.Context,
	pluginID, name, providerID string, enabled bool,
) {
	t.Helper()
	manifest, err := json.Marshal(plugins.Manifest{
		APIVersion: plugins.APIVersion,
		ID:         pluginID,
		Name:       name,
		Provides: []plugins.Provider{{
			Kind: plugins.ProviderNotifyChannel, ID: providerID,
			Name:        "Chat",
			Description: "A message in a channel.",
			Settings: []plugins.Setting{
				{Key: "webhook_url", Label: "Webhook URL", Kind: "password", Secret: true, Required: true},
			},
		}},
		Runtime: plugins.Runtime{Port: 8080},
	})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.NewPluginSigningSecret(pluginID)
	if err != nil {
		t.Fatalf("seal a signing secret: %v", err)
	}
	record := store.Plugin{
		ID: pluginID, Manifest: string(manifest), Version: "1.0.0",
		Status: "running", Enabled: enabled, HMACSealed: sealed,
	}
	if err := db.CreatePlugin(ctx, &record); err != nil {
		t.Fatalf("record the plugin: %v", err)
	}
}

// The delivery this whole half of the standard exists for: a channel a plugin
// provides is offered to the person choosing one, with the plugin's own form.
func TestAChannelAPluginProvidesIsOffered(t *testing.T) {
	c, db, ctx := providerCluster(t)
	installProvider(t, c, db, ctx, "com.example.chat", "Chat notifications", "chat", true)

	kinds, err := c.PluginNotifyChannels(plugins.Dispatcher{}).Kinds(ctx)
	if err != nil {
		t.Fatalf("list the kinds plugins provide: %v", err)
	}
	if len(kinds) != 1 {
		t.Fatalf("%d kinds offered, expected the one the plugin provides", len(kinds))
	}
	got := kinds[0]
	if got.Kind != notify.ProvidedKind("com.example.chat", "chat") {
		t.Errorf("the stored kind is %q, which cannot be traced back to the plugin", got.Kind)
	}
	if got.Name != "Chat" || got.Provider != "Chat notifications" {
		t.Errorf("the panel would not say what this is or where it came from: %+v", got)
	}
	if len(got.Fields) != 1 || got.Fields[0].Key != "webhook_url" || !got.Fields[0].Secret {
		t.Errorf("the plugin's form did not reach the panel: %+v", got.Fields)
	}
}

// A plugin that is switched off is left out rather than offered and refused.
func TestASwitchedOffPluginsChannelIsNotOffered(t *testing.T) {
	c, db, ctx := providerCluster(t)
	installProvider(t, c, db, ctx, "com.example.chat", "Chat notifications", "chat", false)

	kinds, err := c.PluginNotifyChannels(plugins.Dispatcher{}).Kinds(ctx)
	if err != nil {
		t.Fatalf("list the kinds: %v", err)
	}
	if len(kinds) != 0 {
		t.Errorf("a switched-off plugin's channel was offered: %+v", kinds)
	}
}

// A channel outlives the plugin that sent it. That is an ordinary state with
// its own words, not a database failure.
func TestAChannelWhosePluginIsGoneResolvesToNothing(t *testing.T) {
	c, _, ctx := providerCluster(t)
	_, err := c.PluginProvider(ctx, "com.example.gone", plugins.ProviderNotifyChannel, "chat")
	if !errors.Is(err, plugins.ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider for a plugin that is not installed, got %v", err)
	}
}

// Installed, running, and no longer providing this: an upgrade can do that,
// and it is not the same as the plugin being gone.
func TestAPluginThatNoLongerProvidesItResolvesToNothing(t *testing.T) {
	c, db, ctx := providerCluster(t)
	installProvider(t, c, db, ctx, "com.example.chat", "Chat notifications", "chat", true)

	_, err := c.PluginProvider(ctx, "com.example.chat", plugins.ProviderNotifyChannel, "rooms")
	if !errors.Is(err, plugins.ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider for a provider id the plugin does not have, got %v", err)
	}
}

// Two plugins may both provide a channel called "chat". A channel configured
// against one must not start being delivered by the other.
func TestTwoPluginsProvidingTheSameIDAreToldApart(t *testing.T) {
	c, db, ctx := providerCluster(t)
	installProvider(t, c, db, ctx, "com.example.chat", "Example chat", "chat", true)
	installProvider(t, c, db, ctx, "com.other.chat", "Other chat", "chat", true)

	first, err := c.PluginProvider(ctx, "com.example.chat", plugins.ProviderNotifyChannel, "chat")
	if err != nil {
		t.Fatalf("resolve the first plugin: %v", err)
	}
	second, err := c.PluginProvider(ctx, "com.other.chat", plugins.ProviderNotifyChannel, "chat")
	if err != nil {
		t.Fatalf("resolve the second plugin: %v", err)
	}
	if first.ID == second.ID || first.URL == second.URL {
		t.Fatalf("two plugins providing the same id resolved to the same container: %q and %q",
			first.URL, second.URL)
	}

	kinds, err := c.PluginNotifyChannels(plugins.Dispatcher{}).Kinds(ctx)
	if err != nil {
		t.Fatalf("list the kinds: %v", err)
	}
	if len(kinds) != 2 || kinds[0].Kind == kinds[1].Kind {
		t.Fatalf("the two channels are not distinguishable in the list: %+v", kinds)
	}
}
