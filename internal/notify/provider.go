package notify

import (
	"context"
	"strings"
)

// A channel this package does not know how to send.
//
// # Why the switch was the wrong shape
//
// Telegram, Discord, a webhook and email, and that was the whole list. Adding
// Slack meant editing this package, and adding PagerDuty meant editing it
// again: every vendor anybody would ever want was a change to the binary and a
// release. Meanwhile the panel already had a plugin system, and that system
// could only be told that a notification had been sent.
//
// This is the seam. The panel still decides what a notification says, when it
// goes and who gets it — that is the feature, and it stays here. A provider
// decides only how it leaves the building.
//
// # Why a provided kind is prefixed
//
// A channel's kind is a string in a database row that outlives the plugin that
// provided it. "slack" as a bare kind would collide the day this package grows
// its own Slack support, and would leave no way to tell which plugin a stored
// row was meant for. So a provided kind carries both: plugin:<plugin>/<id>. It
// is ugly and it is never shown to anybody — the panel shows the provider's
// name — but it means a row can always be traced back to what wrote it.

// ProvidedPrefix marks a kind that comes from a plugin rather than from here.
const ProvidedPrefix = "plugin:"

// Provider is the set of channel kinds the installed plugins bring.
//
// Every method takes a context and may fail, which is the difference between
// this and the switch it replaces: a built-in kind is a function call and a
// provided one is a request to another container that may not be running.
type Provider interface {
	// Kinds is what can be chosen right now. It is read when somebody opens
	// the form rather than cached, because a plugin is installed and removed
	// while the panel runs.
	Kinds(ctx context.Context) ([]ChannelKind, error)
	// Validate checks a configuration before it is stored.
	Validate(ctx context.Context, kind string, config map[string]string) error
	// Send delivers one message.
	Send(ctx context.Context, kind string, config map[string]string, msg Message) error
}

// ChannelKind is one way of sending, as the panel offers it.
type ChannelKind struct {
	// Kind is what is stored on the channel row.
	Kind string `json:"kind"`
	// Name and Description are the provider's own words, in English. A plugin
	// translates its own interface through the strings it declares; the panel
	// does not have them in its locale files and must not pretend to.
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Provider names the plugin behind it, so somebody can see where a channel
	// they did not add came from.
	Provider string `json:"provider,omitempty"`
	// Fields is the form to show. Empty for a built-in kind, whose form this
	// panel already has and has translated.
	Fields []Field `json:"fields,omitempty"`
}

// Field is one input in a provider's form.
type Field struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Help    string   `json:"help,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	Options []string `json:"options,omitempty"`
	// Secret fields are sealed with the rest of the channel's configuration
	// and never sent back to a browser.
	Secret   bool `json:"secret,omitempty"`
	Required bool `json:"required,omitempty"`
}

// BuiltIn is every kind this package sends by itself, in the order the panel
// offers them.
//
// Exported because the panel lists the choices in one place now, and a second
// copy of this list in a handler is a kind that exists in one and not the
// other.
var BuiltIn = []string{"telegram", "discord", "webhook", "email"}

// IsBuiltIn reports whether this package can send a kind without help.
func IsBuiltIn(kind string) bool {
	for _, known := range BuiltIn {
		if known == kind {
			return true
		}
	}
	return false
}

// IsProvided reports whether a kind comes from a plugin.
func IsProvided(kind string) bool { return strings.HasPrefix(kind, ProvidedPrefix) }

// ProvidedKind builds the kind stored on a channel a plugin provides.
func ProvidedKind(pluginID, providerID string) string {
	return ProvidedPrefix + pluginID + "/" + providerID
}

// SplitProvidedKind reads one back.
func SplitProvidedKind(kind string) (pluginID, providerID string, ok bool) {
	if !IsProvided(kind) {
		return "", "", false
	}
	rest := strings.TrimPrefix(kind, ProvidedPrefix)
	pluginID, providerID, ok = strings.Cut(rest, "/")
	if !ok || pluginID == "" || providerID == "" {
		return "", "", false
	}
	return pluginID, providerID, true
}

// BuiltInKinds returns the built-in kinds as the panel lists them, so a caller
// building the list of choices does not have to know the shape.
func BuiltInKinds() []ChannelKind {
	kinds := make([]ChannelKind, 0, len(BuiltIn))
	for _, kind := range BuiltIn {
		kinds = append(kinds, ChannelKind{Kind: kind})
	}
	return kinds
}

// unknownKind is the one message for a kind nothing can send, written once so
// that validating and sending say the same thing.
//
// It distinguishes the two ways of not being sendable, because they have
// different fixes: a kind that is simply not a kind, and a kind that a plugin
// used to provide and no longer does. The second is what an operator sees after
// removing a plugin, and "that is not a channel Skifity supports" would send
// them looking in the wrong place entirely.
func unknownKind(kind string) error {
	if plugin, provider, ok := SplitProvidedKind(kind); ok {
		return &UnknownKindError{Kind: kind, Plugin: plugin, Provider: provider}
	}
	return &UnknownKindError{Kind: kind}
}

// UnknownKindError is a channel nothing can send.
type UnknownKindError struct {
	Kind string
	// Plugin and Provider are set when the kind was provided by a plugin that
	// is no longer installed or no longer provides it.
	Plugin   string
	Provider string
}

func (e *UnknownKindError) Error() string {
	if e.Plugin != "" {
		return "this channel is sent by the plugin " + e.Plugin +
			", which is not installed or no longer provides " + e.Provider
	}
	return e.Kind + " is not a notification channel Skifity supports"
}
