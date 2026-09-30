// Package plugins is Skifity's plugin standard.
//
// # What a plugin is
//
// A container image and a manifest. Nothing else. The panel runs the image as
// an ordinary Deployment in a namespace of its own, gives it an API token
// carrying exactly the permissions its manifest asked for and an administrator
// granted, and sends it the events it subscribed to.
//
// That shape is chosen, not inherited. Skifity is one static Go binary with
// CGO turned off, and Go cannot load code into a running static binary: the
// `plugin` package needs CGO, works on three operating systems, and its own
// documentation says that "runtime crashes are likely to occur unless all parts
// of the program (the application and all its plugins) are compiled using
// exactly the same version of the toolchain, the same build tags, and the same
// values of certain flags and environment variables". Recompiling the panel to
// add a plugin, which is how Caddy and Traefik's static plugins work, means
// every user needs a Go toolchain. Neither is a plugin system somebody else can
// publish to.
//
// A container is. It can be written in any language, it cannot take the panel
// down when it crashes, it cannot read the master key, and what it may do is a
// list an administrator read before saying yes.
//
// # The standard is this file
//
// A manifest that parses and validates here is a valid Skifity plugin. That is
// deliberate: a standard written only in prose is a standard every
// implementation reads differently. `docs/plugins.md` explains it to a person;
// this decides it.
//
// # Compatibility
//
// APIVersion is the contract between a plugin and the panel. A plugin declares
// which one it was written against, and the panel refuses one it does not
// speak rather than running it and hoping. Adding a field is not a new version;
// changing what an existing field means is.
package plugins

import (
	"fmt"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"

	"skifity/internal/auth"
)

// APIVersion is the plugin contract this build speaks.
const APIVersion = "plugin.skifity.com/v1"

// Manifest is a plugin, as its author publishes it.
type Manifest struct {
	APIVersion string `json:"apiVersion"`
	// ID is globally unique and never changes. Reverse-DNS, because a plugin
	// author owns a domain and nobody has to run a name registry.
	ID string `json:"id"`
	// Name and Description are what the store shows. They are English; a
	// plugin translates its own interface through the strings it declares.
	Name        string `json:"name"`
	Description string `json:"description"`
	// Version is semantic. The panel compares it to decide what an upgrade is.
	Version  string `json:"version"`
	Homepage string `json:"homepage,omitempty"`
	// License is an SPDX identifier, or "commercial" for a plugin that is sold.
	// The store shows it before anything is installed.
	License string `json:"license"`
	Author  Author `json:"author"`

	// Image is where the code is, by digest.
	//
	// A digest and never a tag. A tag can be moved under you by whoever
	// controls the registry, and this image is about to be given an API token:
	// "the plugin you approved" has to mean the bytes you approved.
	Image string `json:"image"`

	// Requires is what this plugin needs of the panel.
	Requires Requires `json:"requires,omitempty"`

	// Permissions is what it is asking to be allowed to do, in the same
	// vocabulary as an API token's scopes. An administrator sees this list
	// before installing, and the token the plugin receives carries exactly it.
	Permissions []string `json:"permissions,omitempty"`

	// Events are the hooks it subscribes to.
	Events []EventSubscription `json:"events,omitempty"`
	// Provides is what it brings to the panel: a vendor behind a feature the
	// panel already owns. See provider.go, which is the half of this standard
	// that lets a plugin do something rather than only hear about it.
	Provides []Provider `json:"provides,omitempty"`
	// Settings are its own, shown in the panel under the plugin.
	Settings []Setting `json:"settings,omitempty"`

	// Runtime is how the panel runs the container.
	Runtime Runtime `json:"runtime,omitempty"`
}

// Author identifies who to complain to.
type Author struct {
	Name  string `json:"name"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

// Requires is what a plugin needs of the panel it is installed into.
type Requires struct {
	// Skifity is a semver range, such as ">=1.0.0 <2.0.0". Empty means any.
	Skifity string `json:"skifity,omitempty"`
}

// EventSubscription is one hook.
type EventSubscription struct {
	// Event is the event name, such as "app.deployed".
	//
	// Named "event" and not "on", which is what it read as at first and which
	// would have been a trap laid for every plugin author: `on` is a boolean in
	// YAML 1.1, so `on: backup.completed` parses in some readers as the key
	// `true`. GitHub Actions carries that scar in every workflow file ever
	// written. A standard being defined today should not step on it.
	Event string `json:"event"`
	// Blocking means the panel waits for an answer and lets the plugin refuse.
	//
	// It is the powerful kind and the dangerous one: a blocking hook can stop a
	// deploy. A plugin that only wants to know something happened must not ask
	// for it, and the panel shows blocking subscriptions separately when an
	// administrator is deciding.
	Blocking bool `json:"blocking,omitempty"`
	// TimeoutSeconds bounds a blocking hook. The panel caps it; a plugin that
	// asks for longer gets the cap, because a hook that can hang for a minute
	// is a deploy that can hang for a minute.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

// Setting is a value the plugin needs and the panel collects for it.
//
// Declared rather than served by the plugin, so that the panel can show the
// form before the plugin is running, translate the labels, and store a secret
// sealed with its own keyring rather than handing the plugin somewhere to keep
// credentials.
type Setting struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
	// Kind is text, password, number, bool or choice.
	Kind    string   `json:"kind,omitempty"`
	Options []string `json:"options,omitempty"`
	// Secret is sealed at rest and never shown again, the same as every other
	// secret in the panel.
	Secret   bool `json:"secret,omitempty"`
	Required bool `json:"required,omitempty"`
}

// Runtime is how the container is run.
type Runtime struct {
	// Port is where the plugin answers. The panel posts events to it.
	Port int `json:"port,omitempty"`
	// Health is the path that reports readiness.
	Health string `json:"health,omitempty"`
	// MemoryMB is what it expects to use, shown before installing so that an
	// operator on a small server can see what they are agreeing to.
	MemoryMB int `json:"memoryMB,omitempty"`
}

// Events the panel can send. The list is closed, because an event name a plugin
// subscribes to and the panel never sends is a plugin that silently does
// nothing for ever.
const (
	EventAppCreated      = "app.created"
	EventAppDeleted      = "app.deleted"
	EventDeployBefore    = "deploy.before"
	EventDeploySucceeded = "deploy.succeeded"
	EventDeployFailed    = "deploy.failed"
	EventBackupCompleted = "backup.completed"
	EventBackupFailed    = "backup.failed"
	EventServerAdded     = "server.added"
	EventServerRemoved   = "server.removed"
	EventDatabaseCreated = "database.created"
)

// Events is every event a plugin may subscribe to.
var Events = []string{
	EventAppCreated, EventAppDeleted,
	EventDeployBefore, EventDeploySucceeded, EventDeployFailed,
	EventBackupCompleted, EventBackupFailed,
	EventServerAdded, EventServerRemoved,
	EventDatabaseCreated,
}

// blockingEvents are the events a plugin may ask to hold up. Only the ones
// named "before" something: refusing a deploy is a decision, and refusing to
// acknowledge that a backup already finished is not.
var blockingEvents = map[string]bool{EventDeployBefore: true}

// MaxBlockingTimeoutSeconds is the longest a blocking hook may hold a deploy.
//
// Ten seconds, because the thing on the other end is a person watching a
// deployment page. A plugin that needs longer is a plugin that should answer
// straight away and do its work afterwards.
const MaxBlockingTimeoutSeconds = 10

// Limits on a manifest, so that one cannot be a denial of service by itself.
const (
	MaxManifestBytes = 64 << 10
	MaxEvents        = 32
	MaxSettings      = 64
	MaxPermissions   = 32
)

var (
	// idPattern is reverse-DNS: lowercase letters, digits, hyphens, at least
	// two labels. An author owns a domain, so nobody has to run a registry of
	// names for this to be unique.
	idPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	// semverPattern is the ordinary three numbers, optionally with a
	// pre-release. Build metadata is not accepted: two versions that differ
	// only by it are the same version, and a store cannot show that usefully.
	semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
	// digestPattern is an image pinned by content.
	digestPattern = regexp.MustCompile(`^[^\s@]+@sha256:[a-f0-9]{64}$`)
	// settingKeyPattern keeps a plugin's settings namespaced under itself and
	// out of the panel's own.
	settingKeyPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9_]*[a-z0-9])?$`)
)

// Parse reads a manifest and checks it.
//
// Both, always. A Parse that returned an unchecked manifest would be a door
// every caller has to remember to close.
func Parse(raw []byte) (Manifest, error) {
	if len(raw) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("a manifest may not be larger than %d KB", MaxManifestBytes>>10)
	}
	var manifest Manifest
	if err := yaml.UnmarshalStrict(raw, &manifest); err != nil {
		// Strict: a field this build does not know is refused rather than
		// ignored. A plugin written against a later panel must not install
		// here and quietly lose half of what it declared.
		return Manifest{}, fmt.Errorf("this is not a plugin manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Validate refuses a manifest the panel could not honour.
//
// Everything here is something that would otherwise become a plugin that
// installs and does not work, which is worse than one that will not install.
func (m Manifest) Validate() error {
	if m.APIVersion != APIVersion {
		return fmt.Errorf("this plugin is written for %q and this panel speaks %q",
			m.APIVersion, APIVersion)
	}
	if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("%q is not a plugin id; they are reverse-DNS, such as com.example.backups", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("a plugin needs a name")
	}
	if strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("a plugin needs a description; it is what somebody reads before installing it")
	}
	if !semverPattern.MatchString(m.Version) {
		return fmt.Errorf("%q is not a version; they look like 1.2.0", m.Version)
	}
	if strings.TrimSpace(m.License) == "" {
		return fmt.Errorf("a plugin has to say what its licence is, even if that is \"commercial\"")
	}
	if strings.TrimSpace(m.Author.Name) == "" {
		return fmt.Errorf("a plugin has to say who wrote it")
	}
	if !digestPattern.MatchString(m.Image) {
		return fmt.Errorf(
			"%q is not an image pinned by digest; a tag can be moved by whoever controls the registry, "+
				"and this image is about to be given an API token", m.Image)
	}

	if err := m.validatePermissions(); err != nil {
		return err
	}
	if err := m.validateEvents(); err != nil {
		return err
	}
	if err := m.validateSettings(); err != nil {
		return err
	}
	return m.validateProviders()
}

func (m Manifest) validatePermissions() error {
	if len(m.Permissions) > MaxPermissions {
		return fmt.Errorf("a plugin may ask for at most %d permissions", MaxPermissions)
	}
	for _, permission := range m.Permissions {
		permission = strings.TrimSpace(permission)
		// The unscoped forms are deliberately not allowed here, though a
		// person's own token may still use them. "write" would be every
		// resource, which is not a permission anybody can meaningfully agree
		// to on a screen.
		if permission == auth.ScopeRead || permission == auth.ScopeWrite {
			return fmt.Errorf(
				"%q is not a permission a plugin may ask for; name the resource, such as apps:read", permission)
		}
		if err := auth.ValidateScopes(permission); err != nil {
			return fmt.Errorf("permission %q: %w", permission, err)
		}
	}
	return nil
}

func (m Manifest) validateEvents() error {
	if len(m.Events) > MaxEvents {
		return fmt.Errorf("a plugin may subscribe to at most %d events", MaxEvents)
	}
	seen := map[string]bool{}
	for _, subscription := range m.Events {
		if !KnownEvent(subscription.Event) {
			return fmt.Errorf("%q is not an event this panel sends; they are %s",
				subscription.Event, strings.Join(Events, ", "))
		}
		if seen[subscription.Event] {
			return fmt.Errorf("%q is subscribed to twice", subscription.Event)
		}
		seen[subscription.Event] = true

		if subscription.Blocking && !blockingEvents[subscription.Event] {
			return fmt.Errorf(
				"%q cannot be blocking; only an event that happens before something can be refused",
				subscription.Event)
		}
		if subscription.TimeoutSeconds < 0 {
			return fmt.Errorf("%q has a negative timeout", subscription.Event)
		}
	}
	return nil
}

func (m Manifest) validateSettings() error {
	return validateSettingList(m.Settings, "this plugin")
}

// validateSettingList checks one form, whether it is the plugin's own settings
// or a provider's. Shared rather than duplicated: a provider's form is shown in
// the same panel, stored in the same place and sealed the same way, so it had
// better not be allowed to be a shape the plugin's own settings are not.
//
// whose names what is being checked, so the message says which form is wrong
// when a manifest declares several.
func validateSettingList(list []Setting, whose string) error {
	if len(list) > MaxSettings {
		return fmt.Errorf("%s may declare at most %d settings", whose, MaxSettings)
	}
	seen := map[string]bool{}
	for _, setting := range list {
		if !settingKeyPattern.MatchString(setting.Key) {
			return fmt.Errorf("%q is not a setting key; they are lowercase letters, digits and underscores", setting.Key)
		}
		if seen[setting.Key] {
			return fmt.Errorf("%s declares the setting %q twice", whose, setting.Key)
		}
		seen[setting.Key] = true
		if strings.TrimSpace(setting.Label) == "" {
			return fmt.Errorf("the setting %q has no label", setting.Key)
		}
		switch setting.Kind {
		case "", "text", "password", "number", "bool", "choice":
		default:
			return fmt.Errorf("%q is not a kind of setting", setting.Kind)
		}
		if setting.Kind == "choice" && len(setting.Options) == 0 {
			return fmt.Errorf("the setting %q is a choice with nothing to choose from", setting.Key)
		}
	}
	return nil
}

// KnownEvent reports whether the panel sends an event.
func KnownEvent(name string) bool {
	for _, event := range Events {
		if event == name {
			return true
		}
	}
	return false
}

// Timeout is how long a blocking subscription may hold something up, capped.
func (e EventSubscription) Timeout() int {
	if !e.Blocking {
		return 0
	}
	if e.TimeoutSeconds <= 0 || e.TimeoutSeconds > MaxBlockingTimeoutSeconds {
		return MaxBlockingTimeoutSeconds
	}
	return e.TimeoutSeconds
}

// Scopes is the permission list as an API token's scope string.
func (m Manifest) Scopes() string { return strings.Join(m.Permissions, ",") }

// BlocksDeploys reports whether this plugin can stop a deploy, which is the one
// thing an administrator has to be told in plain words before installing.
func (m Manifest) BlocksDeploys() bool {
	for _, subscription := range m.Events {
		if subscription.Blocking {
			return true
		}
	}
	return false
}
