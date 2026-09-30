package plugins

import (
	"fmt"
	"strings"
)

// What a plugin provides, as opposed to what it is told.
//
// # Why this exists at all
//
// The first version of this standard had events and nothing else. A plugin
// could be told that a deploy succeeded, and it could refuse one. That is a
// plugin system that can only ever react, and a system that can only react
// produces only small things: every idea worth building turned out to belong
// in the panel itself.
//
// The shape that gives a plugin real work is the other direction: the panel
// owns the feature and the plugin owns the vendor. Skifity knows what a
// notification is, when to send one and what goes in it; it does not know what
// Slack is. A provider plugin fills in the vendor without the panel gaining a
// dependency, and without anybody recompiling the binary. That is how a
// payment gateway is a plugin in a shop and not a fork of it.
//
// # Why the vocabulary is closed
//
// The same reason Events is closed, and the reason is not symmetry. A kind the
// panel never asks for is a plugin that installs, says it provides something,
// and is never called — which looks exactly like a plugin that is broken. So a
// kind exists here only once the panel really asks for it, and a test in this
// package fails the build if one is listed that no caller anywhere uses.
//
// One kind today. Adding the second is cheap; adding it before a caller exists
// is how the list fills up with promises.

// Provider kinds. Closed: see above.
const (
	// ProviderNotifyChannel is a way of delivering a notification that the
	// panel does not know how to deliver itself. The panel decides what to say
	// and when; the plugin decides how it leaves the building.
	ProviderNotifyChannel = "notify.channel"
)

// ProviderKinds is every kind a plugin may provide.
var ProviderKinds = []string{ProviderNotifyChannel}

// ProviderActions is what the panel asks of a provider, per kind.
//
// Closed for the same reason as the kinds, and listed per kind rather than as
// one set because "validate a Slack webhook URL" and "validate an S3 bucket"
// are not the same call with a different argument. A plugin author reads this
// to know what its container has to answer.
var ProviderActions = map[string][]string{
	// ActionValidate is asked while somebody is still looking at the form, so
	// a typo is caught before it is stored. ActionSend is the delivery.
	ProviderNotifyChannel: {ActionValidate, ActionSend},
}

// Actions a provider may be asked to perform.
const (
	ActionValidate = "validate"
	ActionSend     = "send"
)

// MaxProviders bounds a manifest, like every other list in it.
const MaxProviders = 16

// Provider is one vendor a plugin brings to a part of the panel.
type Provider struct {
	// Kind is what part of the panel this plugs into, from ProviderKinds.
	Kind string `json:"kind"`
	// ID is unique within this plugin, and is what the panel sends back when
	// it calls: a plugin may provide two of the same kind, such as a mail
	// service and its sandbox.
	ID string `json:"id"`
	// Name is what a person picks from a list, so it names the vendor rather
	// than the plugin: "Slack", not "Acme notification pack".
	Name string `json:"name"`
	// Description is one line under the name, shown where it is chosen.
	Description string `json:"description,omitempty"`
	// Settings is the form the panel shows for one configured instance of this
	// provider, and is not the plugin's own settings: a plugin providing Slack
	// has one API token of its own and any number of channels configured
	// against it, each with its own webhook URL.
	//
	// The panel collects and stores these, sealed when Secret is set, and
	// hands them back with every call. A provider plugin therefore keeps no
	// per-channel state and no credentials of its own.
	Settings []Setting `json:"settings,omitempty"`
}

// providerIDPattern keeps a provider id readable in a URL and in a channel
// kind, which is where it ends up.
var providerIDPattern = settingKeyPattern

func (m Manifest) validateProviders() error {
	if len(m.Provides) > MaxProviders {
		return fmt.Errorf("a plugin may provide at most %d things", MaxProviders)
	}
	seen := map[string]bool{}
	for _, provider := range m.Provides {
		if !KnownProviderKind(provider.Kind) {
			return fmt.Errorf("%q is not something a plugin can provide to this panel; it can provide %s",
				provider.Kind, strings.Join(ProviderKinds, ", "))
		}
		if !providerIDPattern.MatchString(provider.ID) {
			return fmt.Errorf(
				"%q is not a provider id; they are lowercase letters, digits and underscores, such as slack",
				provider.ID)
		}
		key := provider.Kind + "/" + provider.ID
		if seen[key] {
			return fmt.Errorf("this plugin provides %q twice", key)
		}
		seen[key] = true
		if strings.TrimSpace(provider.Name) == "" {
			return fmt.Errorf("the provider %q has no name; it is what somebody picks from a list", key)
		}
		if err := validateSettingList(provider.Settings,
			fmt.Sprintf("the provider %q", key)); err != nil {
			return err
		}
	}
	return nil
}

// KnownProviderKind reports whether the panel asks anything of a kind.
func KnownProviderKind(kind string) bool {
	for _, known := range ProviderKinds {
		if known == kind {
			return true
		}
	}
	return false
}

// KnownProviderAction reports whether a kind is ever asked to do this.
func KnownProviderAction(kind, action string) bool {
	for _, known := range ProviderActions[kind] {
		if known == action {
			return true
		}
	}
	return false
}

// ProviderFor returns the provider of a kind with an id, if this plugin has it.
func (m Manifest) ProviderFor(kind, id string) (Provider, bool) {
	for _, provider := range m.Provides {
		if provider.Kind == kind && provider.ID == id {
			return provider, true
		}
	}
	return Provider{}, false
}

// ProvidesKind reports whether this plugin provides anything of a kind, which
// is what decides whether its container is worth calling at all.
func (m Manifest) ProvidesKind(kind string) bool {
	for _, provider := range m.Provides {
		if provider.Kind == kind {
			return true
		}
	}
	return false
}
