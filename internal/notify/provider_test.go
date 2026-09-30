package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeProvider stands in for the installed plugins.
type fakeProvider struct {
	validated string
	sent      string
	config    map[string]string
	err       error
}

func (f *fakeProvider) Kinds(context.Context) ([]ChannelKind, error) { return nil, nil }

func (f *fakeProvider) Validate(_ context.Context, kind string, config map[string]string) error {
	f.validated, f.config = kind, config
	return f.err
}

func (f *fakeProvider) Send(_ context.Context, kind string, config map[string]string, _ Message) error {
	f.sent, f.config = kind, config
	return f.err
}

// The seam: a kind this package does not know goes to the plugin that does.
func TestAProvidedKindIsValidatedAndSentByThePlugin(t *testing.T) {
	kind := ProvidedKind("com.example.chat", "chat")
	config := map[string]string{"webhook_url": "https://example.test/hook"}
	provider := &fakeProvider{}

	if err := ValidateConfig(context.Background(), kind, config, provider); err != nil {
		t.Fatalf("validating a provided kind: %v", err)
	}
	if provider.validated != kind {
		t.Errorf("the plugin was not asked to validate: %q", provider.validated)
	}
	if err := Send(context.Background(), kind, config, Message{Title: "hello"}, provider); err != nil {
		t.Fatalf("sending through a provided kind: %v", err)
	}
	if provider.sent != kind {
		t.Errorf("the plugin was not asked to send: %q", provider.sent)
	}
	if provider.config["webhook_url"] != "https://example.test/hook" {
		t.Error("the channel's configuration did not reach the plugin")
	}
}

// A built-in kind must not start going through a plugin.
func TestABuiltInKindIsStillSentHere(t *testing.T) {
	provider := &fakeProvider{}
	if err := ValidateConfig(context.Background(), "discord",
		map[string]string{"webhook_url": "https://discord.com/api/webhooks/1/x"}, provider); err != nil {
		t.Fatalf("validating a built-in kind: %v", err)
	}
	if provider.validated != "" {
		t.Errorf("a built-in kind was handed to a plugin: %q", provider.validated)
	}
	for _, kind := range BuiltIn {
		if IsProvided(kind) {
			t.Errorf("the built-in kind %q looks like a provided one", kind)
		}
	}
}

// A channel outlives the plugin that sent it, and "that is not a channel
// Skifity supports" would send somebody looking in entirely the wrong place.
func TestAChannelWhosePluginIsGoneSaysSo(t *testing.T) {
	kind := ProvidedKind("com.example.chat", "chat")
	err := Send(context.Background(), kind, nil, Message{}, nil)
	if err == nil {
		t.Fatal("a channel with no plugin behind it reported success")
	}
	var unknown *UnknownKindError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected an UnknownKindError, got %v", err)
	}
	if unknown.Plugin != "com.example.chat" {
		t.Errorf("the message does not name the plugin to look for: %v", err)
	}
	if !strings.Contains(err.Error(), "com.example.chat") {
		t.Errorf("the words do not point anywhere useful: %v", err)
	}
}

func TestAKindThatIsNotAKindSaysThatInstead(t *testing.T) {
	err := Send(context.Background(), "carrier-pigeon", nil, Message{}, nil)
	var unknown *UnknownKindError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected an UnknownKindError, got %v", err)
	}
	if unknown.Plugin != "" {
		t.Errorf("a kind that was never a plugin's was blamed on one: %+v", unknown)
	}
}

func TestAProvidedKindSurvivesBeingWrittenDownAndReadBack(t *testing.T) {
	kind := ProvidedKind("com.example.chat", "rooms")
	pluginID, providerID, ok := SplitProvidedKind(kind)
	if !ok || pluginID != "com.example.chat" || providerID != "rooms" {
		t.Fatalf("read back %q as %q/%q (ok=%v)", kind, pluginID, providerID, ok)
	}
	for _, bad := range []string{"telegram", "plugin:", "plugin:onlyplugin", "plugin:/onlyprovider"} {
		if _, _, ok := SplitProvidedKind(bad); ok {
			t.Errorf("%q was read as a provided kind", bad)
		}
	}
}

// The list somebody chooses from has to contain every built-in kind, or one of
// them silently stops being offered.
func TestEveryBuiltInKindIsOffered(t *testing.T) {
	offered := map[string]bool{}
	for _, kind := range BuiltInKinds() {
		offered[kind.Kind] = true
	}
	for _, kind := range BuiltIn {
		if !offered[kind] {
			t.Errorf("%q can be sent and is never offered", kind)
		}
		if err := ValidateConfig(context.Background(), kind, map[string]string{}, nil); err != nil {
			var unknown *UnknownKindError
			if errors.As(err, &unknown) {
				t.Errorf("%q is offered and cannot be sent", kind)
			}
		}
	}
}
