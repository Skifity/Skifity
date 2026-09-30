package plugins

import (
	"strings"
	"testing"
)

// A plugin somebody would actually publish, written as an author would write
// it. If this stops parsing, the standard changed.
const example = `
apiVersion: plugin.skifity.com/v1
id: com.example.backup-to-b2
name: Backup to Backblaze B2
description: Copies every database backup to a Backblaze bucket as it is taken.
version: 1.2.0
homepage: https://example.com/skifity-b2
license: MIT
author:
  name: Example Ltd
  url: https://example.com
image: ghcr.io/example/skifity-b2@sha256:0000000000000000000000000000000000000000000000000000000000000000
requires:
  skifity: ">=1.0.0 <2.0.0"
permissions:
  - databases:read
  - backups:read
  - backups:write
events:
  - event: backup.completed
  - event: backup.failed
settings:
  - key: bucket
    label: Bucket
    required: true
  - key: application_key
    label: Application key
    kind: password
    secret: true
runtime:
  port: 8080
  health: /healthz
  memoryMB: 64
`

func TestTheExampleManifestIsValid(t *testing.T) {
	manifest, err := Parse([]byte(example))
	if err != nil {
		t.Fatalf("the example in the documentation does not parse: %v", err)
	}
	if manifest.ID != "com.example.backup-to-b2" {
		t.Errorf("id = %q", manifest.ID)
	}
	if got := manifest.Scopes(); got != "databases:read,backups:read,backups:write" {
		t.Errorf("scopes = %q", got)
	}
	if manifest.BlocksDeploys() {
		t.Error("a plugin subscribing to nothing blocking was reported as blocking deploys")
	}
}

// The image is the one thing that must be pinned by content.
//
// A tag can be moved by whoever controls the registry, and this image is about
// to be handed an API token: "the plugin you approved" has to mean the bytes
// you approved.
func TestAnImageMustBePinnedByDigest(t *testing.T) {
	for _, image := range []string{
		"ghcr.io/example/plugin:1.2.0",
		"ghcr.io/example/plugin:latest",
		"ghcr.io/example/plugin",
		"ghcr.io/example/plugin@sha256:short",
		"ghcr.io/example/plugin@md5:0000",
	} {
		manifest := mustExample(t)
		manifest.Image = image
		if err := manifest.Validate(); err == nil {
			t.Errorf("%q was accepted", image)
		}
	}
}

// A permission is what an administrator reads before saying yes, so it has to
// name something. "write" is every resource, which nobody can meaningfully
// agree to on a screen.
func TestAPluginCannotAskForEverything(t *testing.T) {
	for _, permission := range []string{"write", "read", "*:write", "keyring:read", "apps:admin"} {
		manifest := mustExample(t)
		manifest.Permissions = []string{permission}
		if err := manifest.Validate(); err == nil {
			t.Errorf("a plugin asking for %q was accepted", permission)
		}
	}
	for _, permission := range []string{"apps:read", "servers:write", "events:read"} {
		manifest := mustExample(t)
		manifest.Permissions = []string{permission}
		if err := manifest.Validate(); err != nil {
			t.Errorf("a plugin asking for %q was refused: %v", permission, err)
		}
	}
}

// An event name the panel never sends is a plugin that silently does nothing
// for ever, which is the failure this whole product keeps finding.
func TestAnEventThePanelDoesNotSendIsRefused(t *testing.T) {
	manifest := mustExample(t)
	manifest.Events = []EventSubscription{{Event: "app.exploded"}}
	err := manifest.Validate()
	if err == nil {
		t.Fatal("a subscription to an event nothing sends was accepted")
	}
	if !strings.Contains(err.Error(), "deploy.before") {
		t.Errorf("the refusal does not list what is available: %v", err)
	}
}

// Only an event that happens before something can be refused. Letting a plugin
// "block" a backup that already finished would be a promise with nothing behind
// it.
func TestOnlyABeforeEventCanBlock(t *testing.T) {
	manifest := mustExample(t)
	manifest.Events = []EventSubscription{{Event: "backup.completed", Blocking: true}}
	if err := manifest.Validate(); err == nil {
		t.Error("blocking on an event that has already happened was accepted")
	}

	manifest.Events = []EventSubscription{{Event: "deploy.before", Blocking: true}}
	if err := manifest.Validate(); err != nil {
		t.Errorf("blocking on deploy.before was refused: %v", err)
	}
	if !manifest.BlocksDeploys() {
		t.Error("a plugin that can stop a deploy did not say so")
	}
}

// A blocking hook holds up somebody watching a deployment page, so the panel
// caps it whatever the plugin asked for.
func TestABlockingHookIsCapped(t *testing.T) {
	cases := []struct {
		subscription EventSubscription
		want         int
	}{
		{EventSubscription{Event: EventDeployBefore, Blocking: true, TimeoutSeconds: 3}, 3},
		{EventSubscription{Event: EventDeployBefore, Blocking: true, TimeoutSeconds: 600}, MaxBlockingTimeoutSeconds},
		{EventSubscription{Event: EventDeployBefore, Blocking: true}, MaxBlockingTimeoutSeconds},
		{EventSubscription{Event: EventBackupCompleted}, 0},
	}
	for _, tc := range cases {
		if got := tc.subscription.Timeout(); got != tc.want {
			t.Errorf("%+v timeout = %d, want %d", tc.subscription, got, tc.want)
		}
	}
}

// A field this build does not know is refused rather than ignored: a plugin
// written against a later panel must not install here and quietly lose half of
// what it declared.
func TestAFieldThisBuildDoesNotKnowIsRefused(t *testing.T) {
	raw := example + "\nsomethingNewer: true\n"
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("a manifest with an unknown field was accepted")
	}
}

// And a plugin written for another contract version does not run here.
func TestAnotherApiVersionIsRefused(t *testing.T) {
	manifest := mustExample(t)
	manifest.APIVersion = "plugin.skifity.com/v2"
	err := manifest.Validate()
	if err == nil {
		t.Fatal("a plugin for another contract version was accepted")
	}
	if !strings.Contains(err.Error(), APIVersion) {
		t.Errorf("the refusal does not say what this panel speaks: %v", err)
	}
}

// The rest of the shape, each one something that would install and not work.
func TestWhatElseIsRefused(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(*Manifest)
	}{
		{"no id", func(m *Manifest) { m.ID = "" }},
		{"an id that is not reverse-DNS", func(m *Manifest) { m.ID = "backups" }},
		{"an id with capitals", func(m *Manifest) { m.ID = "Com.Example.Backups" }},
		{"no name", func(m *Manifest) { m.Name = "" }},
		{"no description", func(m *Manifest) { m.Description = "" }},
		{"a version that is not one", func(m *Manifest) { m.Version = "1.2" }},
		{"no licence", func(m *Manifest) { m.License = "" }},
		{"nobody to complain to", func(m *Manifest) { m.Author = Author{} }},
		{"a setting with no label", func(m *Manifest) {
			m.Settings = []Setting{{Key: "bucket"}}
		}},
		{"a setting key that is not one", func(m *Manifest) {
			m.Settings = []Setting{{Key: "My Bucket", Label: "x"}}
		}},
		{"the same setting twice", func(m *Manifest) {
			m.Settings = []Setting{{Key: "a", Label: "A"}, {Key: "a", Label: "A again"}}
		}},
		{"a choice with nothing to choose", func(m *Manifest) {
			m.Settings = []Setting{{Key: "mode", Label: "Mode", Kind: "choice"}}
		}},
		{"the same event twice", func(m *Manifest) {
			m.Events = []EventSubscription{{Event: EventDeployFailed}, {Event: EventDeployFailed}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := mustExample(t)
			tc.break_(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func mustExample(t *testing.T) Manifest {
	t.Helper()
	manifest, err := Parse([]byte(example))
	if err != nil {
		t.Fatalf("the example manifest does not parse: %v", err)
	}
	return manifest
}

// A provider plugin, written as an author would write it. This is the half of
// the standard that lets a plugin do something rather than only hear about it,
// so it gets an example of its own.
const providerExample = `
apiVersion: plugin.skifity.com/v1
id: com.example.chat
name: Chat notifications
description: Sends Skifity's notifications to a chat service.
version: 0.3.1
license: Apache-2.0
author:
  name: Example Ltd
image: ghcr.io/example/skifity-chat@sha256:1111111111111111111111111111111111111111111111111111111111111111
provides:
  - kind: notify.channel
    id: chat
    name: Chat
    description: A message in a channel.
    settings:
      - key: webhook_url
        label: Webhook URL
        kind: password
        secret: true
        required: true
      - key: mention
        label: Who to mention
runtime:
  port: 8080
`

func TestAProviderPluginParses(t *testing.T) {
	manifest, err := Parse([]byte(providerExample))
	if err != nil {
		t.Fatalf("a plugin that provides something did not parse: %v", err)
	}
	provider, ok := manifest.ProviderFor(ProviderNotifyChannel, "chat")
	if !ok {
		t.Fatal("the provider it declares cannot be found")
	}
	if provider.Name != "Chat" {
		t.Errorf("the provider's name is %q", provider.Name)
	}
	if len(provider.Settings) != 2 {
		t.Errorf("the provider's form has %d fields", len(provider.Settings))
	}
	if !manifest.ProvidesKind(ProviderNotifyChannel) {
		t.Error("the plugin does not report providing the kind it declares")
	}
	// A plugin may provide something and subscribe to nothing.
	if len(manifest.Events) != 0 {
		t.Error("this example was meant to subscribe to no events")
	}
}

// The same rule as events: something the panel never asks for is refused at
// install time rather than becoming a plugin that is never called.
func TestAKindThePanelNeverAsksForIsRefused(t *testing.T) {
	manifest := parseExample(t)
	manifest.Provides = []Provider{{Kind: "storage.bucket", ID: "b2", Name: "Backblaze"}}
	err := manifest.Validate()
	if err == nil {
		t.Fatal("a plugin providing something the panel never asks for was accepted")
	}
	if !strings.Contains(err.Error(), "storage.bucket") {
		t.Errorf("the message does not name what was wrong: %v", err)
	}
}

func TestAProviderNeedsAnIDAndAName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider Provider
	}{
		{"no id", Provider{Kind: ProviderNotifyChannel, Name: "Chat"}},
		{"an id nobody could put in a URL", Provider{Kind: ProviderNotifyChannel, ID: "Chat Service!", Name: "Chat"}},
		{"no name", Provider{Kind: ProviderNotifyChannel, ID: "chat"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := parseExample(t)
			manifest.Provides = []Provider{tc.provider}
			if err := manifest.Validate(); err == nil {
				t.Error("it was accepted")
			}
		})
	}
}

func TestTheSameProviderTwiceIsRefused(t *testing.T) {
	manifest := parseExample(t)
	manifest.Provides = []Provider{
		{Kind: ProviderNotifyChannel, ID: "chat", Name: "Chat"},
		{Kind: ProviderNotifyChannel, ID: "chat", Name: "Chat again"},
	}
	if err := manifest.Validate(); err == nil {
		t.Error("a plugin providing the same thing twice was accepted")
	}
}

// A provider's form is stored and sealed exactly like the plugin's own
// settings, so it has to pass exactly the same checks.
func TestAProvidersFormIsCheckedLikeAnyOther(t *testing.T) {
	manifest := parseExample(t)
	manifest.Provides = []Provider{{
		Kind: ProviderNotifyChannel, ID: "chat", Name: "Chat",
		Settings: []Setting{{Key: "room", Label: "Room", Kind: "choice"}},
	}}
	err := manifest.Validate()
	if err == nil {
		t.Fatal("a choice with nothing to choose from was accepted in a provider's form")
	}
	if !strings.Contains(err.Error(), "room") {
		t.Errorf("the message does not name the field: %v", err)
	}
}

// Every kind in the vocabulary has to say what it will be asked to do, or a
// plugin author has nothing to implement against.
func TestEveryProviderKindSaysWhatItIsAskedToDo(t *testing.T) {
	for _, kind := range ProviderKinds {
		actions := ProviderActions[kind]
		if len(actions) == 0 {
			t.Errorf("%q is a kind a plugin may provide and nothing is ever asked of it", kind)
		}
		for _, action := range actions {
			if !KnownProviderAction(kind, action) {
				t.Errorf("%q does not recognise its own action %q", kind, action)
			}
		}
	}
}

func parseExample(t *testing.T) Manifest {
	t.Helper()
	manifest, err := Parse([]byte(example))
	if err != nil {
		t.Fatalf("the example manifest does not parse: %v", err)
	}
	return manifest
}
