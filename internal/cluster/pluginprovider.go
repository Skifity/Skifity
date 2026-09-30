package cluster

import (
	"context"
	"encoding/json"
	"fmt"

	"skifity/internal/kube"
	"skifity/internal/notify"
	"skifity/internal/plugins"
)

// Resolving what an installed plugin provides.
//
// The event half of the plugin standard asks "who wants to hear this", and the
// answer is a list. This half asks "who is this", and the answer is exactly
// one plugin or none: a channel row names the plugin that sends it, and a
// channel whose plugin has been removed is a channel that does not send rather
// than one that quietly goes somewhere else.

// PluginProvider finds the plugin that provides a kind with an id.
//
// Read from the database on every call, for the same reason PluginTargets is:
// a plugin is switched off and removed while the panel runs, and a cached
// answer is a request to a container that is no longer there.
func (c *Cluster) PluginProvider(ctx context.Context, pluginID, kind, providerID string) (plugins.Target, error) {
	record, err := c.db.GetPlugin(ctx, pluginID)
	if err != nil {
		// Not found is the ordinary case — a channel outliving its plugin —
		// and gets the words that say so rather than a database error.
		return plugins.Target{}, plugins.ErrNoProvider
	}
	if !record.Enabled || record.Status == "disabled" {
		return plugins.Target{}, plugins.ErrNoProvider
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(record.Manifest), &manifest); err != nil {
		return plugins.Target{}, fmt.Errorf("read the %s plugin's manifest: %w", pluginID, err)
	}
	if _, ok := manifest.ProviderFor(kind, providerID); !ok {
		// Installed, running, and no longer providing this. An upgrade can do
		// that, and it is not the same as the plugin being gone.
		return plugins.Target{}, plugins.ErrNoProvider
	}
	signing, err := c.keyring.Open(record.HMACSealed, pluginSealContext(record.ID))
	if err != nil {
		return plugins.Target{}, fmt.Errorf("read the %s plugin's signing secret: %w", pluginID, err)
	}
	return plugins.Target{
		ID:     manifest.ID,
		Name:   manifest.Name,
		URL:    kube.PluginServiceURL(manifest.ID, manifest.Runtime.Port),
		Secret: string(signing),
	}, nil
}

// NotifyChannels adapts the installed plugins to what internal/notify needs.
//
// It is the whole reason the provider half of the standard exists: the panel
// decides what a notification says, when it goes and who gets it, and a plugin
// decides only how it leaves the building. Adding Slack is now a container
// somebody publishes rather than a change to this binary.
type NotifyChannels struct {
	cluster *Cluster
	calls   plugins.Dispatcher
}

// PluginNotifyChannels returns the adapter. calls is the same dispatcher the
// events go through, so a plugin is reached the same way whichever half of the
// standard it uses.
func (c *Cluster) PluginNotifyChannels(calls plugins.Dispatcher) *NotifyChannels {
	if c == nil {
		return nil
	}
	return &NotifyChannels{cluster: c, calls: calls}
}

// Kinds lists every channel the installed plugins can send.
//
// A plugin that is switched off is left out rather than shown and refused: the
// list is what somebody is about to choose from, and offering a choice that
// cannot work is worse than a shorter list.
func (n *NotifyChannels) Kinds(ctx context.Context) ([]notify.ChannelKind, error) {
	if n == nil || n.cluster == nil {
		return nil, nil
	}
	installed, err := n.cluster.db.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	var kinds []notify.ChannelKind
	for _, record := range installed {
		if !record.Enabled || record.Status == "disabled" {
			continue
		}
		var manifest plugins.Manifest
		if err := json.Unmarshal([]byte(record.Manifest), &manifest); err != nil {
			n.cluster.log.Error("an installed plugin's manifest could not be read, so what it provides is not offered",
				"plugin", record.ID, "error", err)
			continue
		}
		for _, provider := range manifest.Provides {
			if provider.Kind != plugins.ProviderNotifyChannel {
				continue
			}
			kinds = append(kinds, notify.ChannelKind{
				Kind:        notify.ProvidedKind(manifest.ID, provider.ID),
				Name:        provider.Name,
				Description: provider.Description,
				Provider:    manifest.Name,
				Fields:      fieldsOf(provider.Settings),
			})
		}
	}
	return kinds, nil
}

// Validate asks the plugin whether a configuration would work, while the person
// is still looking at the form.
func (n *NotifyChannels) Validate(ctx context.Context, kind string, config map[string]string) error {
	return n.ask(ctx, kind, plugins.ActionValidate, config, nil)
}

// Send asks the plugin to deliver one message.
func (n *NotifyChannels) Send(ctx context.Context, kind string, config map[string]string, msg notify.Message) error {
	return n.ask(ctx, kind, plugins.ActionSend, config, msg)
}

func (n *NotifyChannels) ask(ctx context.Context, kind, action string, config map[string]string, data any) error {
	pluginID, providerID, ok := notify.SplitProvidedKind(kind)
	if !ok {
		return fmt.Errorf("%q is not a channel provided by a plugin", kind)
	}
	req := plugins.Request{
		Plugin: pluginID, Kind: plugins.ProviderNotifyChannel, Provider: providerID,
		Action: action, Config: config,
	}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("prepare the message for %s: %w", pluginID, err)
		}
		req.Data = raw
	}
	_, err := n.calls.Call(ctx, req)
	return err
}

// fieldsOf turns a provider's declared settings into the form the panel shows.
//
// A copy rather than the same type shared between the two packages: internal/
// notify should not have to import the plugin standard to describe a form, and
// the day a field gains something only a plugin can express, this is where it
// is decided what the panel will show.
func fieldsOf(settings []plugins.Setting) []notify.Field {
	if len(settings) == 0 {
		return nil
	}
	fields := make([]notify.Field, 0, len(settings))
	for _, setting := range settings {
		fields = append(fields, notify.Field{
			Key:      setting.Key,
			Label:    setting.Label,
			Help:     setting.Help,
			Kind:     setting.Kind,
			Options:  setting.Options,
			Secret:   setting.Secret,
			Required: setting.Required,
		})
	}
	return fields
}
