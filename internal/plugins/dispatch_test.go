package plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// pluginServer stands in for a plugin: it records what arrived, checks the
// signature the way a real plugin should, and answers what the test tells it to.
type pluginServer struct {
	*httptest.Server
	received   atomic.Int32
	signatures atomic.Int32
	lastEvent  atomic.Value
}

func newPluginServer(t *testing.T, secret string, answer func() (int, string)) *pluginServer {
	t.Helper()
	p := &pluginServer{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.received.Add(1)
		if Verify(secret, body, r.Header.Get(SignatureHeader)) {
			p.signatures.Add(1)
		}
		var event Event
		_ = json.Unmarshal(body, &event)
		p.lastEvent.Store(event.Event)

		status, reply := answer()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(p.Close)
	return p
}

func targetsOf(list ...Target) func(context.Context, string, string) ([]Target, error) {
	return func(context.Context, string, string) ([]Target, error) { return list, nil }
}

// A notification reaches every subscriber, signed.
func TestNotifyReachesEveryPluginAndIsSigned(t *testing.T) {
	const secret = "not-a-real-secret"
	first := newPluginServer(t, secret, func() (int, string) { return http.StatusOK, "" })
	second := newPluginServer(t, secret, func() (int, string) { return http.StatusOK, "" })

	d := Dispatcher{Targets: targetsOf(
		Target{ID: "a", URL: first.URL, Secret: secret},
		Target{ID: "b", URL: second.URL, Secret: secret},
	)}
	d.Notify(context.Background(), EventDeploySucceeded, "team_1", map[string]string{"app": "web"})

	for name, server := range map[string]*pluginServer{"first": first, "second": second} {
		if server.received.Load() != 1 {
			t.Errorf("%s received %d events, want 1", name, server.received.Load())
		}
		if server.signatures.Load() != 1 {
			t.Errorf("%s got an event whose signature did not verify", name)
		}
		if got := server.lastEvent.Load(); got != EventDeploySucceeded {
			t.Errorf("%s received %v", name, got)
		}
	}
}

// A plugin that is down must not turn a deploy that succeeded into a failure on
// somebody's page.
func TestAPluginThatIsDownDoesNotBreakTheNotification(t *testing.T) {
	const secret = "not-a-real-secret"
	alive := newPluginServer(t, secret, func() (int, string) { return http.StatusOK, "" })
	dead := newPluginServer(t, secret, func() (int, string) { return http.StatusInternalServerError, "no" })

	d := Dispatcher{Targets: targetsOf(
		Target{ID: "dead", URL: dead.URL, Secret: secret},
		Target{ID: "alive", URL: alive.URL, Secret: secret},
	)}
	// It returns nothing at all, so the only thing to assert is that the other
	// plugin was still told.
	d.Notify(context.Background(), EventDeployFailed, "team_1", nil)
	if alive.received.Load() != 1 {
		t.Error("a plugin that was up did not get the event because another was down")
	}
}

// A blocking hook is a question, and a refusal has to stop the thing.
func TestABlockingPluginCanRefuse(t *testing.T) {
	const secret = "not-a-real-secret"
	server := newPluginServer(t, secret, func() (int, string) {
		return http.StatusOK, `{"allow": false, "reason": "there is a change freeze"}`
	})
	d := Dispatcher{Targets: targetsOf(Target{
		ID: "freeze", Name: "Change freeze", URL: server.URL, Secret: secret,
		Blocking: true, Timeout: time.Second,
	})}

	verdict, who := d.Ask(context.Background(), EventDeployBefore, "team_1", nil)
	if verdict.Allow {
		t.Fatal("a plugin that refused was not listened to")
	}
	if verdict.Reason != "there is a change freeze" {
		t.Errorf("reason = %q", verdict.Reason)
	}
	if who != "Change freeze" {
		t.Errorf("the refusal is attributed to %q, and a person needs to know which plugin", who)
	}
}

// Silence is not consent. A plugin that answers 200 with an empty body has said
// nothing, and the zero Verdict refuses.
func TestAnEmptyAnswerIsNotConsent(t *testing.T) {
	const secret = "not-a-real-secret"
	server := newPluginServer(t, secret, func() (int, string) { return http.StatusOK, `{}` })
	d := Dispatcher{Targets: targetsOf(Target{
		ID: "quiet", Name: "Quiet", URL: server.URL, Secret: secret, Blocking: true,
	})}
	if verdict, _ := d.Ask(context.Background(), EventDeployBefore, "team_1", nil); verdict.Allow {
		t.Error("an empty answer was taken as permission")
	}
}

// But a plugin that could not be reached has not refused: one that stops every
// deploy the moment it is upgraded is a plugin nobody installs twice.
func TestAPluginThatCannotBeReachedHasNotRefused(t *testing.T) {
	d := Dispatcher{Targets: targetsOf(Target{
		ID: "gone", Name: "Gone", URL: "http://127.0.0.1:1", Secret: "x",
		Blocking: true, Timeout: 200 * time.Millisecond,
	})}
	verdict, _ := d.Ask(context.Background(), EventDeployBefore, "team_1", nil)
	if !verdict.Allow {
		t.Error("a plugin that was unreachable stopped a deploy")
	}
}

// All of them have to agree: a plugin installed to stop something must not be
// overruled by another plugin that does not care.
func TestEveryBlockingPluginHasToAgree(t *testing.T) {
	const secret = "not-a-real-secret"
	yes := newPluginServer(t, secret, func() (int, string) { return http.StatusOK, `{"allow": true}` })
	no := newPluginServer(t, secret, func() (int, string) {
		return http.StatusOK, `{"allow": false, "reason": "no"}`
	})
	d := Dispatcher{Targets: targetsOf(
		Target{ID: "yes", URL: yes.URL, Secret: secret, Blocking: true},
		Target{ID: "no", Name: "The strict one", URL: no.URL, Secret: secret, Blocking: true},
	)}
	if verdict, who := d.Ask(context.Background(), EventDeployBefore, "team_1", nil); verdict.Allow {
		t.Errorf("one plugin's yes overruled another's no (attributed to %q)", who)
	}
}

// A blocking subscriber must not also receive the notification: that would be
// the same event delivered twice.
func TestABlockingSubscriberIsNotAlsoNotified(t *testing.T) {
	const secret = "not-a-real-secret"
	server := newPluginServer(t, secret, func() (int, string) { return http.StatusOK, `{"allow":true}` })
	d := Dispatcher{Targets: targetsOf(Target{
		ID: "both", URL: server.URL, Secret: secret, Blocking: true,
	})}
	d.Notify(context.Background(), EventDeployBefore, "team_1", nil)
	if server.received.Load() != 0 {
		t.Error("a blocking subscriber was also sent the notification")
	}
}

// The signature is over the exact bytes, and a wrong secret does not verify.
func TestSignAndVerify(t *testing.T) {
	body := []byte(`{"event":"deploy.succeeded"}`)
	signature := Sign("secret", body)

	if !Verify("secret", body, signature) {
		t.Error("a signature did not verify against the secret that made it")
	}
	if Verify("another secret", body, signature) {
		t.Error("a signature verified against the wrong secret")
	}
	if Verify("secret", []byte(`{"event":"deploy.failed"}`), signature) {
		t.Error("a signature verified over different bytes")
	}
	if Verify("secret", body, "") {
		t.Error("an empty signature verified")
	}
	if got := signature[:7]; got != "sha256=" {
		t.Errorf("the signature does not name its algorithm: %q", signature)
	}
}

// A plugin must not be able to hold a deploy for longer than the cap, whatever
// its manifest asked for.
func TestABlockingHookCannotHoldADeployForever(t *testing.T) {
	const secret = "not-a-real-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	d := Dispatcher{Targets: targetsOf(Target{
		ID: "slow", URL: server.URL, Secret: secret,
		Blocking: true, Timeout: 100 * time.Millisecond,
	})}
	started := time.Now()
	verdict, _ := d.Ask(context.Background(), EventDeployBefore, "team_1", nil)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("a slow plugin held the deploy for %s", elapsed)
	}
	if !verdict.Allow {
		t.Error("a plugin that timed out was counted as a refusal")
	}
}
