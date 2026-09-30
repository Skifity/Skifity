package plugins

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"skifity/internal/runsafe"
)

// Sending a plugin what happened.
//
// # Why a signature
//
// A plugin's endpoint is reachable from the panel's namespace and nowhere else,
// which is the first line. It is not the only one: anything that ever ends up
// running beside the panel could otherwise post "deploy.before, allow it" to a
// plugin and be believed. So every event carries an HMAC over its exact bytes,
// keyed with a secret the panel generated for that plugin alone and put in the
// same Secret as its token. A plugin that does not check it is a plugin that
// trusts its network, and the documentation says to check it.
//
// # Why delivery is not guaranteed
//
// It is not a queue and does not pretend to be. An event is posted once, with a
// short timeout; a plugin that was restarting misses it. Saying so plainly is
// better than a retry loop that looks like a guarantee and is not one — a
// plugin that must not miss anything reads the state back through the API,
// which is what its token is for.
//
// The exception is the blocking kind, which is not a notification at all but a
// question the panel is waiting on.

// SignatureHeader carries the HMAC a plugin verifies.
const SignatureHeader = "X-Skifity-Signature"

// EventHeader names the event, so a plugin can route without parsing the body.
const EventHeader = "X-Skifity-Event"

// EventPath is where a plugin receives events.
const EventPath = "/events"

// Event is what a plugin is sent.
type Event struct {
	// Event is the name, such as "deploy.succeeded".
	Event string `json:"event"`
	// At is when it happened, so a plugin can tell a replay from news.
	At time.Time `json:"at"`
	// Data is the payload, which differs per event and is documented per event.
	Data any `json:"data"`
}

// Verdict is what a blocking hook answers.
//
// The zero value refuses, and that is on purpose: a plugin that answers 200
// with an empty body has said nothing, and treating silence as consent is how a
// blocking hook becomes decoration.
type Verdict struct {
	Allow bool `json:"allow"`
	// Reason is shown to the person whose deploy was stopped. Without one they
	// are told a plugin refused and nothing else, which is a support ticket.
	Reason string `json:"reason,omitempty"`
}

// Target is one plugin an event goes to.
type Target struct {
	// ID is the plugin's identifier, for the log and for the refusal message.
	ID string
	// Name is what it calls itself, which is what a person should be told.
	Name string
	// URL is its in-cluster address, without the path.
	URL string
	// Secret is the key its signature is made with.
	Secret string
	// Blocking and Timeout come from its subscription.
	Blocking bool
	Timeout  time.Duration
}

// Dispatcher posts events to plugins.
type Dispatcher struct {
	// Targets returns who wants an event, and is a function rather than a list
	// because plugins are installed and switched off while the panel runs.
	//
	// It takes the team the event belongs to as well as the event's name. A
	// plugin is installed panel-wide but hears only about the teams whoever
	// installed it can already see: without that, an owner of one team installs
	// a plugin and it is told about every other team's deploys, and can refuse
	// them.
	Targets func(ctx context.Context, event, teamID string) ([]Target, error)
	// Providers resolves the one plugin that provides a kind with an id, for
	// Call. Separate from Targets because it is a different question with a
	// different answer shape: an event goes to everybody who subscribed, and a
	// call goes to exactly the plugin whose vendor was chosen. It returns
	// ErrNoProvider when nothing installed provides it, which is an ordinary
	// state rather than a failure.
	//
	// There is no team argument. A provider is configured by an administrator
	// against a named plugin, so the choice has already been made by somebody
	// who could make it; the team check that guards events is about a plugin
	// hearing what it was not meant to hear, and nothing is being told here.
	Providers func(ctx context.Context, pluginID, kind, providerID string) (Target, error)
	Client    *http.Client
	Log       *slog.Logger
}

// MaxVerdictBytes bounds what a plugin may answer with.
//
// A blocking hook's answer is two fields. A plugin that streams a gigabyte at
// the panel while a deploy waits should not be able to.
const MaxVerdictBytes = 8 << 10

// NotifyTimeout bounds a notification, which nobody is waiting for.
const NotifyTimeout = 10 * time.Second

func (d Dispatcher) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: NotifyTimeout}
}

func (d Dispatcher) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Notify tells every subscribed plugin that something happened, and waits for
// none of them.
//
// Errors are logged and not returned: a deploy that succeeded did succeed, and
// a plugin that was restarting must not turn that into a failure on somebody's
// page.
func (d Dispatcher) Notify(ctx context.Context, event, teamID string, data any) {
	if d.Targets == nil {
		return
	}
	targets, err := d.Targets(ctx, event, teamID)
	if err != nil {
		d.log().Error("could not work out which plugins wanted an event",
			"event", event, "error", err)
		return
	}
	payload := Event{Event: event, At: time.Now().UTC(), Data: data}

	var wg sync.WaitGroup
	for _, target := range targets {
		if target.Blocking {
			// A blocking subscription is answered through Ask, where somebody
			// is waiting for it. Posting it here as well would deliver the
			// same event twice.
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A plugin is somebody else's container and its answer is somebody
			// else's bytes. A panic while reading one must not end the panel.
			defer runsafe.Recover(d.log(), "sending an event to a plugin", nil)
			if _, err := d.post(ctx, target, payload, NotifyTimeout); err != nil {
				d.log().Warn("a plugin did not take an event",
					"plugin", target.ID, "event", event, "error", err)
			}
		}()
	}
	wg.Wait()
}

// Ask puts a blocking event to every plugin that subscribed to it and returns
// the first refusal.
//
// Every plugin is asked and all of them have to agree, which is the only
// composition that means anything: a plugin installed to stop something must
// not be overruled by another plugin that does not care.
func (d Dispatcher) Ask(ctx context.Context, event, teamID string, data any) (Verdict, string) {
	if d.Targets == nil {
		return Verdict{Allow: true}, ""
	}
	targets, err := d.Targets(ctx, event, teamID)
	if err != nil {
		// A panel that cannot read its own plugin list must not start refusing
		// deploys over it. This is the one place the answer is yes on failure,
		// and it is because the failure is the panel's, not a plugin's.
		d.log().Error("could not work out which plugins to ask", "event", event, "error", err)
		return Verdict{Allow: true}, ""
	}
	payload := Event{Event: event, At: time.Now().UTC(), Data: data}

	for _, target := range targets {
		if !target.Blocking {
			continue
		}
		timeout := target.Timeout
		if timeout <= 0 || timeout > MaxBlockingTimeoutSeconds*time.Second {
			timeout = MaxBlockingTimeoutSeconds * time.Second
		}
		body, err := d.post(ctx, target, payload, timeout)
		if err != nil {
			// A plugin that was asked and did not answer has not refused. It
			// is the same judgement as a notification: the plugin's own
			// trouble must not become everybody's, and a plugin that must be
			// able to stop a deploy when it is down is a plugin that stops
			// every deploy the moment it is upgraded.
			d.log().Warn("a plugin did not answer a blocking event, so it was not counted",
				"plugin", target.ID, "event", event, "error", err)
			continue
		}
		var verdict Verdict
		if err := json.Unmarshal(body, &verdict); err != nil {
			d.log().Warn("a plugin's answer could not be read, so it was not counted",
				"plugin", target.ID, "event", event, "error", err)
			continue
		}
		if !verdict.Allow {
			reason := verdict.Reason
			if reason == "" {
				reason = "it did not say why"
			}
			d.log().Info("a plugin refused", "plugin", target.ID, "event", event, "reason", reason)
			return verdict, target.Name
		}
	}
	return Verdict{Allow: true}, ""
}

func (d Dispatcher) post(ctx context.Context, target Target, payload Event, timeout time.Duration) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	// Detached from the caller's deadline on purpose for a notification: a
	// browser that navigated away must not cancel telling a plugin what
	// happened. A blocking ask passes its own short timeout in the same way,
	// because the thing waiting is a deploy and not a request.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL+EventPath, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(EventHeader, payload.Event)
	req.Header.Set(SignatureHeader, Sign(target.Secret, raw))

	resp, err := d.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxVerdictBytes))
	if err != nil {
		return nil, fmt.Errorf("read the plugin's answer: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("the plugin answered %s", resp.Status)
	}
	return body, nil
}

// Sign is the signature a plugin verifies.
//
// Written as "sha256=<hex>" so that the algorithm is in the value rather than
// assumed, which is what makes changing it later something other than a
// flag day.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a signature, for a plugin written in Go and for the tests here.
//
// hmac.Equal rather than ==: comparing two MACs with a string comparison leaks
// how much of the value matched through how long the comparison took.
func Verify(secret string, body []byte, signature string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(signature))
}
