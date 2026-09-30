package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// providerServer stands in for a plugin that provides something: it checks the
// signature, records the request, and answers what the test tells it to.
type providerServer struct {
	*httptest.Server
	last atomic.Value // Request
	// signed records whether every request so far carried a good signature.
	signed atomic.Bool
}

func newProviderServer(t *testing.T, secret string, answer func(Request) Response) *providerServer {
	t.Helper()
	p := &providerServer{}
	p.signed.Store(true)
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ProvidePath {
			t.Errorf("a call arrived at %q, not %q", r.URL.Path, ProvidePath)
		}
		body, _ := io.ReadAll(r.Body)
		if !Verify(secret, body, r.Header.Get(SignatureHeader)) {
			p.signed.Store(false)
		}
		var req Request
		_ = json.Unmarshal(body, &req)
		p.last.Store(req)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer(req))
	}))
	t.Cleanup(p.Close)
	return p
}

func providerAt(target Target) func(context.Context, string, string, string) (Target, error) {
	return func(context.Context, string, string, string) (Target, error) { return target, nil }
}

// The whole point of the provider half: the panel asks, and the answer is used.
func TestCallingAProviderReturnsWhatItAnswered(t *testing.T) {
	const secret = "not-a-real-secret"
	server := newProviderServer(t, secret, func(Request) Response {
		return Response{OK: true, Data: json.RawMessage(`{"delivered":true}`)}
	})
	d := Dispatcher{Providers: providerAt(Target{ID: "com.example.chat", Name: "Chat", URL: server.URL, Secret: secret})}

	data, err := d.Call(context.Background(), Request{
		Plugin: "com.example.chat", Kind: ProviderNotifyChannel, Provider: "chat", Action: ActionSend,
		Config: map[string]string{"webhook_url": "https://example.test/hook"},
	})
	if err != nil {
		t.Fatalf("calling a provider: %v", err)
	}
	if string(data) != `{"delivered":true}` {
		t.Fatalf("the plugin's answer was not returned: %s", data)
	}
	if !server.signed.Load() {
		t.Error("the call was not signed, so a plugin cannot tell it came from the panel")
	}

	got := server.last.Load().(Request)
	if got.Kind != ProviderNotifyChannel || got.Provider != "chat" || got.Action != ActionSend {
		t.Errorf("the plugin was not told what was being asked of it: %+v", got)
	}
	if got.Config["webhook_url"] != "https://example.test/hook" {
		t.Error("the configuration did not travel with the call, so the plugin would have to keep it")
	}
}

// A plugin that refuses is reported as the plugin refusing, not as the panel
// failing — the two need different words in front of a person.
func TestAPluginRefusingIsDistinguishableFromNotReachingIt(t *testing.T) {
	const secret = "not-a-real-secret"
	server := newProviderServer(t, secret, func(Request) Response {
		return Response{OK: false, Error: "that webhook URL was rejected"}
	})
	d := Dispatcher{Providers: providerAt(Target{ID: "com.example.chat", Name: "Chat", URL: server.URL, Secret: secret})}

	_, err := d.Call(context.Background(), Request{
		Plugin: "com.example.chat", Kind: ProviderNotifyChannel, Provider: "chat", Action: ActionValidate,
	})
	if err == nil {
		t.Fatal("a refusal was not reported at all")
	}
	refusal, ok := AsProviderError(err)
	if !ok {
		t.Fatalf("a refusal was not recognisable as one: %v", err)
	}
	if !strings.Contains(refusal.Error(), "that webhook URL was rejected") {
		t.Errorf("the plugin's reason was lost: %v", refusal)
	}
	if !strings.Contains(refusal.Error(), "Chat") {
		t.Errorf("the message does not say which plugin refused: %v", refusal)
	}
}

// The judgement that separates a call from a notification: a failure reaches
// the caller instead of being logged and dropped.
func TestAProviderThatCannotBeReachedIsAnErrorAndNotASilence(t *testing.T) {
	server := newProviderServer(t, "s", func(Request) Response { return Response{OK: true} })
	url := server.URL
	server.Close()

	d := Dispatcher{Providers: providerAt(Target{ID: "com.example.chat", Name: "Chat", URL: url, Secret: "s"})}
	if _, err := d.Call(context.Background(), Request{
		Plugin: "com.example.chat", Kind: ProviderNotifyChannel, Provider: "chat", Action: ActionSend,
	}); err == nil {
		t.Fatal("a plugin that is not running was reported as a successful delivery")
	}
}

// Answering 200 with an empty body says nothing, and nothing is not yes.
func TestAnEmptyAnswerIsAFailure(t *testing.T) {
	const secret = "not-a-real-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(server.Close)

	d := Dispatcher{Providers: providerAt(Target{ID: "com.example.chat", Name: "Chat", URL: server.URL, Secret: secret})}
	if _, err := d.Call(context.Background(), Request{
		Plugin: "com.example.chat", Kind: ProviderNotifyChannel, Provider: "chat", Action: ActionSend,
	}); err == nil {
		t.Fatal("a plugin that said nothing was treated as having delivered")
	}
}

// A plugin's failure message is shown to a person, so it is somebody else's
// text on the panel's page and is cut rather than trusted.
func TestAPluginCannotPutAnEssayOnThePanelsPage(t *testing.T) {
	const secret = "not-a-real-secret"
	server := newProviderServer(t, secret, func(Request) Response {
		return Response{OK: false, Error: strings.Repeat("x", MaxProviderErrorRunes*10)}
	})
	d := Dispatcher{Providers: providerAt(Target{ID: "com.example.chat", Name: "Chat", URL: server.URL, Secret: secret})}

	_, err := d.Call(context.Background(), Request{
		Plugin: "com.example.chat", Kind: ProviderNotifyChannel, Provider: "chat", Action: ActionSend,
	})
	refusal, ok := AsProviderError(err)
	if !ok {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if len([]rune(refusal.Message)) > MaxProviderErrorRunes+1 {
		t.Errorf("the message was not cut: %d runes", len([]rune(refusal.Message)))
	}
}

// Asking for something no kind covers is refused here rather than posted to a
// plugin that would not know what to do with it.
func TestAnUnknownKindOrActionIsRefusedBeforeAnythingIsSent(t *testing.T) {
	d := Dispatcher{Providers: func(context.Context, string, string, string) (Target, error) {
		t.Error("a plugin was resolved for a request that should never have got that far")
		return Target{}, nil
	}}
	if _, err := d.Call(context.Background(), Request{
		Plugin: "p", Kind: "storage.bucket", Provider: "x", Action: ActionSend,
	}); err == nil {
		t.Error("a kind this panel never asks for was accepted")
	}
	if _, err := d.Call(context.Background(), Request{
		Plugin: "p", Kind: ProviderNotifyChannel, Provider: "x", Action: "explode",
	}); err == nil {
		t.Error("an action this kind is never asked to do was accepted")
	}
}

// Nothing installed providing it is a normal state with its own words.
func TestNothingProvidingItIsNotAFailure(t *testing.T) {
	d := Dispatcher{}
	_, err := d.Call(context.Background(), Request{
		Plugin: "com.example.chat", Kind: ProviderNotifyChannel, Provider: "chat", Action: ActionSend,
	})
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
}

// Two plugins may each provide a notify.channel called "slack". A call has to
// name which one, or a channel configured against one starts being delivered by
// the other the day both are installed.
func TestACallNamesThePluginAndNotOnlyTheProviderID(t *testing.T) {
	var asked [3]string
	d := Dispatcher{Providers: func(_ context.Context, pluginID, kind, providerID string) (Target, error) {
		asked = [3]string{pluginID, kind, providerID}
		return Target{}, ErrNoProvider
	}}
	_, _ = d.Call(context.Background(), Request{
		Plugin: "com.example.chat", Kind: ProviderNotifyChannel, Provider: "slack", Action: ActionSend,
	})
	if asked[0] != "com.example.chat" {
		t.Errorf("the plugin was not part of the address: %q", asked[0])
	}
	if asked[1] != ProviderNotifyChannel || asked[2] != "slack" {
		t.Errorf("the provider was not named: %v", asked)
	}
}
