package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Asking a plugin for something, as opposed to telling it something.
//
// # Why this is not Notify with a return value
//
// Notify drops its errors on purpose: nobody is waiting, and a deploy that
// succeeded must not be reported as failed because a plugin was restarting.
// Every judgement here is the opposite one, because somebody is waiting. A
// person is looking at a form, or a notification that says an app went down is
// the reason the panel exists at that moment. So a failure is returned, the
// caller decides what it means, and a plugin that is down is a channel that did
// not deliver rather than a silence.
//
// # Why the configuration travels with the call
//
// The panel collects a provider's settings, seals them with its own keyring and
// sends them with every call. The plugin keeps nothing. That is what makes a
// provider plugin safe to install: it has no database, no credential of its own
// for the vendor, and removing it removes nothing the panel still needs. It
// also means the credential crosses the in-cluster network, which is why the
// request is signed and why a plugin's Service is reachable from the panel's
// namespace and nowhere else.

// ProvidePath is where a plugin answers a request for something it provides.
//
// A second path rather than a field in the event body: a plugin author should
// be able to write the provider half without touching their event handler, and
// a plugin that provides something and subscribes to nothing should not have to
// implement /events at all.
const ProvidePath = "/provide"

// ProviderHeader names what is being asked for, as "kind/id", so a plugin can
// route without parsing the body. ActionHeader names what to do with it.
const (
	ProviderHeader = "X-Skifity-Provider"
	ActionHeader   = "X-Skifity-Action"
)

// ProvideTimeout bounds a call.
//
// Fifteen seconds, the same as the panel's own outbound notification client: a
// provider is usually making one HTTPS request to a vendor, and if that vendor
// is slower than the panel would tolerate itself then the plugin wrapping it
// should not get longer.
const ProvideTimeout = 15 * time.Second

// MaxResponseBytes bounds what a plugin may answer a call with.
const MaxResponseBytes = 64 << 10

// MaxProviderErrorRunes bounds the message a plugin's failure is reported with.
//
// It is written by the plugin and shown to a person, so it is somebody else's
// text on the panel's page. Cut rather than trusted.
const MaxProviderErrorRunes = 500

// Request is what the panel asks a provider.
type Request struct {
	// Plugin is which installed plugin is being asked.
	//
	// Part of the address and not only of the log: a provider id is unique
	// within a manifest and nowhere else, so two plugins may both provide a
	// notify.channel called "slack". Addressing one by its id alone would pick
	// whichever happened to be found first, and a channel configured against
	// one would start being delivered by the other the day both are installed.
	Plugin string `json:"plugin"`
	// Kind and Provider say which of that plugin's providers is meant. A
	// plugin may provide more than one.
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	// Action is from ProviderActions for this kind.
	Action string `json:"action"`
	// Config is one configured instance of this provider: the form the panel
	// collected, unsealed, sent afresh with every call.
	Config map[string]string `json:"config,omitempty"`
	// Data is the action's argument, and differs per action.
	Data json.RawMessage `json:"data,omitempty"`
}

// Response is what a plugin answers.
//
// The zero value is a failure, deliberately: a plugin that answers 200 with an
// empty body has said nothing, and reading silence as success is how a provider
// becomes a channel that quietly delivers nothing.
type Response struct {
	OK bool `json:"ok"`
	// Error is why not, in words for the person who will read it.
	Error string `json:"error,omitempty"`
	// Data is the answer, when the action has one.
	Data json.RawMessage `json:"data,omitempty"`
}

// ProviderError is a plugin refusing or failing, as opposed to the panel being
// unable to reach it.
//
// Separated because the two need different words: "Slack rejected that webhook
// URL" is something the person can fix, and "the Slack plugin is not running"
// is not.
type ProviderError struct {
	// Plugin is the name of the plugin, for the message.
	Plugin string
	// Message is what the plugin said.
	Message string
}

func (e *ProviderError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s refused and did not say why", e.Plugin)
	}
	return fmt.Sprintf("%s: %s", e.Plugin, e.Message)
}

// AsProviderError reports whether an error came from the plugin rather than
// from reaching it.
func AsProviderError(err error) (*ProviderError, bool) {
	var provider *ProviderError
	ok := errors.As(err, &provider)
	return provider, ok
}

// ErrNoProvider is returned when nothing installed provides what was asked for.
//
// Its own error because it is the normal state — a channel configured against a
// plugin that has since been removed — and a caller wants to say so in those
// words rather than reporting a failure.
var ErrNoProvider = errors.New("no installed plugin provides that")

// Call asks a plugin for something and returns its answer.
//
// The target is resolved through Providers, which reads the installed plugins
// every time for the same reason Targets does: a plugin is switched off while
// the panel is running, and a cached answer is a call to a container that is
// no longer there.
func (d Dispatcher) Call(ctx context.Context, req Request) (json.RawMessage, error) {
	if !KnownProviderKind(req.Kind) {
		return nil, fmt.Errorf("%q is not something a plugin provides to this panel", req.Kind)
	}
	if !KnownProviderAction(req.Kind, req.Action) {
		return nil, fmt.Errorf("%q is not something a %s is asked to do; it is asked to %s",
			req.Action, req.Kind, strings.Join(ProviderActions[req.Kind], " or "))
	}
	if d.Providers == nil {
		return nil, ErrNoProvider
	}
	target, err := d.Providers(ctx, req.Plugin, req.Kind, req.Provider)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("prepare the request for %s: %w", target.Name, err)
	}

	// Not detached from the caller's context, which is the other difference
	// from an event: a person who navigated away from the form they were
	// filling in is a request that should stop, not one worth finishing.
	ctx, cancel := context.WithTimeout(ctx, ProvideTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		target.URL+ProvidePath, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(ProviderHeader, req.Kind+"/"+req.Provider)
	httpReq.Header.Set(ActionHeader, req.Action)
	httpReq.Header.Set(SignatureHeader, Sign(target.Secret, raw))

	resp, err := d.client().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("reach the %s plugin: %w", target.Name, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read what the %s plugin answered: %w", target.Name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("the %s plugin answered %s", target.Name, resp.Status)
	}

	var answer Response
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, fmt.Errorf("the %s plugin's answer could not be read: %w", target.Name, err)
	}
	if !answer.OK {
		return nil, &ProviderError{Plugin: target.Name, Message: trimRunes(answer.Error, MaxProviderErrorRunes)}
	}
	return answer.Data, nil
}

func trimRunes(s string, limit int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
