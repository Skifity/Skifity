package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"skifity/internal/store"
)

// fakeStore returns a fixed set of channels.
type fakeStore struct {
	channels []store.NotificationChannel
	// settings stands in for the panel's own SMTP configuration, which an
	// email channel falls back to for anything it does not say itself.
	settings map[string]string
}

func (f fakeStore) ListNotificationChannels(context.Context, string) ([]store.NotificationChannel, error) {
	return f.channels, nil
}

func (f fakeStore) GetSetting(_ context.Context, key string) (string, bool, error) {
	return f.settings[key], false, nil
}

// fakeKeyring hands back whatever was "sealed", so a test needs no master key.
type fakeKeyring struct{}

func (fakeKeyring) Open(stored, _ string) ([]byte, error) { return []byte(stored), nil }

// recorder collects the webhook bodies a dispatcher delivers.
type recorder struct {
	mu   sync.Mutex
	got  []Message
	done chan struct{}
	want int
}

func newRecorder(want int) *recorder {
	return &recorder{done: make(chan struct{}), want: want}
}

func (r *recorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var msg Message
		_ = json.Unmarshal(body, &msg)
		r.mu.Lock()
		r.got = append(r.got, msg)
		reached := len(r.got) == r.want
		r.mu.Unlock()
		if reached {
			close(r.done)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (r *recorder) messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.got...)
}

func webhookConfig(url string) string {
	raw, _ := json.Marshal(map[string]string{"url": url})
	return string(raw)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestDispatcherSendsOnlyToSubscribers is the point of the whole package: a
// channel that asked for one event must not hear about another, and a channel
// that asked for nothing in particular must hear about everything.
func TestDispatcherSendsOnlyToSubscribers(t *testing.T) {
	sink := newRecorder(2)
	server := httptest.NewServer(sink.handler())
	defer server.Close()

	db := fakeStore{channels: []store.NotificationChannel{
		{ID: "chan_failed", Kind: "webhook", Name: "failures", Enabled: true,
			Events: EventDeployFailed, ConfigEnc: webhookConfig(server.URL)},
		{ID: "chan_succeeded", Kind: "webhook", Name: "successes", Enabled: true,
			Events: EventDeploySucceeded, ConfigEnc: webhookConfig(server.URL)},
		{ID: "chan_everything", Kind: "webhook", Name: "everything", Enabled: true,
			Events: "", ConfigEnc: webhookConfig(server.URL)},
		{ID: "chan_off", Kind: "webhook", Name: "switched off", Enabled: false,
			Events: "", ConfigEnc: webhookConfig(server.URL)},
	}}

	d := NewDispatcher(db, fakeKeyring{}, quietLogger(),
		func(context.Context) string { return "https://panel.example.com/" })

	d.Notify(t.Context(), "team_1", EventDeployFailed, Message{
		Title: "Deploying shop failed", Body: "The build ran out of memory.",
		Level: "error", Path: "/apps/app_1/deployments",
	})
	d.Wait()

	select {
	case <-sink.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("the dispatcher delivered %d messages, want 2", len(sink.messages()))
	}

	got := sink.messages()
	if len(got) != 2 {
		t.Fatalf("delivered %d messages, want exactly 2 (the subscriber and the catch-all)", len(got))
	}
	for _, msg := range got {
		if msg.Title != "Deploying shop failed" {
			t.Errorf("delivered %q, want the message that was sent", msg.Title)
		}
		// The producer knows the path; only the dispatcher knows the address.
		if msg.URL != "https://panel.example.com/apps/app_1/deployments" {
			t.Errorf("link is %q, want the panel address joined to the path", msg.URL)
		}
	}
}

// TestDispatcherIgnoresUnsubscribedEvent guards the other direction: an event
// nobody asked for reaches nobody, and Notify still returns.
func TestDispatcherIgnoresUnsubscribedEvent(t *testing.T) {
	sink := newRecorder(1)
	server := httptest.NewServer(sink.handler())
	defer server.Close()

	db := fakeStore{channels: []store.NotificationChannel{
		{ID: "chan_backups", Kind: "webhook", Name: "backups", Enabled: true,
			Events: EventBackupFailed, ConfigEnc: webhookConfig(server.URL)},
	}}
	d := NewDispatcher(db, fakeKeyring{}, quietLogger(), nil)

	d.Notify(t.Context(), "team_1", EventDeploySucceeded, Message{Title: "shop is live"})
	d.Wait()

	if got := sink.messages(); len(got) != 0 {
		t.Fatalf("delivered %d messages to a channel that did not subscribe, want 0", len(got))
	}
}

// TestDispatcherSurvivesACancelledRequest covers the case that made this a
// dispatcher rather than a direct call: the request that produced the event is
// usually over before the notification is sent.
func TestDispatcherSurvivesACancelledRequest(t *testing.T) {
	sink := newRecorder(1)
	server := httptest.NewServer(sink.handler())
	defer server.Close()

	db := fakeStore{channels: []store.NotificationChannel{
		{ID: "chan_all", Kind: "webhook", Name: "everything", Enabled: true,
			ConfigEnc: webhookConfig(server.URL)},
	}}
	d := NewDispatcher(db, fakeKeyring{}, quietLogger(), nil)

	ctx, cancel := context.WithCancel(t.Context())
	d.Notify(ctx, "team_1", EventServerLost, Message{Title: "web-2 stopped answering"})
	cancel()
	d.Wait()

	select {
	case <-sink.done:
	case <-time.After(5 * time.Second):
		t.Fatal("a notification was dropped because the request that caused it ended")
	}
}

// TestSubscribes pins the rule that an empty list means every event, which is
// what the panel stores for a channel created without choosing any.
func TestSubscribes(t *testing.T) {
	for _, tc := range []struct {
		events, event string
		want          bool
	}{
		{"", EventDeployFailed, true},
		{"   ", EventDeployFailed, true},
		{EventDeployFailed, EventDeployFailed, true},
		{EventDeploySucceeded + "," + EventDeployFailed, EventDeployFailed, true},
		{EventDeploySucceeded + ", " + EventDeployFailed, EventDeployFailed, true},
		{EventDeploySucceeded, EventDeployFailed, false},
		{"deploy", EventDeployFailed, false},
	} {
		if got := subscribes(tc.events, tc.event); got != tc.want {
			t.Errorf("subscribes(%q, %q) = %v, want %v", tc.events, tc.event, got, tc.want)
		}
	}
}

// TestNilDispatcherIsSafe covers the panel running without notifications at
// all, which is what a producer sees when it was built with a nil notifier.
func TestNilDispatcherIsSafe(t *testing.T) {
	var d *Dispatcher
	d.Notify(t.Context(), "team_1", EventDeployFailed, Message{Title: "no panic"})
	d.Wait()
}

// TestMain lets these tests reach an httptest server.
//
// The shipped client refuses loopback on purpose — see internal/netguard,
// which is where that behaviour is tested. Every server in this file listens on
// 127.0.0.1, so without this the whole package would be testing the guard
// rather than the protocol handling it is about.
func TestMain(m *testing.M) {
	client = &http.Client{Timeout: 5 * time.Second}
	os.Exit(m.Run())
}

// A backup that succeeded is news only to a channel that asked for it: a
// channel that takes every event was never sent one per database per night,
// and a new event must not start doing that unasked.
func TestASucceededBackupReachesOnlyAChannelThatAskedForIt(t *testing.T) {
	if subscribes("", EventBackupSucceeded) {
		t.Error("a channel with no list is sent every successful backup")
	}
	if !subscribes("", EventBackupMissed) {
		t.Error("a channel with no list is not told a backup was missed")
	}
	if !subscribes("backup.failed, backup.succeeded", EventBackupSucceeded) {
		t.Error("a channel that asked for successful backups is not sent them")
	}
}

// retryingDispatcher is a dispatcher with channels that wait milliseconds, not
// seconds, between attempts.
func retryingDispatcher(url string) *Dispatcher {
	db := fakeStore{channels: []store.NotificationChannel{
		{ID: "chan_1", Kind: "webhook", Name: "ops", Enabled: true, ConfigEnc: webhookConfig(url)},
	}}
	d := NewDispatcher(db, fakeKeyring{}, quietLogger(), nil)
	d.retryAfter = []time.Duration{5 * time.Millisecond, 10 * time.Millisecond}
	return d
}

// A service that was restarting for a moment must not cost the news that a
// deployment failed: that is the message that is worth sending when something
// is going wrong.
func TestANotificationIsTriedAgainWhenTheServiceWasBusy(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	d := retryingDispatcher(server.URL)
	d.Notify(t.Context(), "team_1", EventDeployFailed, Message{Title: "Deploying shop failed"})
	// Wait is the shutdown signal, and ends any waiting to try again: it is not
	// called until the retries have had their turn.
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return attempts >= 3 })
	d.Wait()

	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Errorf("the service was asked %d times, want 3: two refusals and the delivery", attempts)
	}
}

// eventually waits for a condition that a goroutine will make true.
func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A refusal that will be the same next time is not asked again.
func TestANotificationIsNotRepeatedWhenTheAnswerWillNotChange(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusBadRequest} {
		var mu sync.Mutex
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			attempts++
			mu.Unlock()
			w.WriteHeader(status)
		}))

		d := retryingDispatcher(server.URL)
		d.Notify(t.Context(), "team_1", EventDeployFailed, Message{Title: "x"})
		eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return attempts >= 1 })
		// Long enough for a retry to have been made, were one going to be.
		time.Sleep(60 * time.Millisecond)
		d.Wait()
		server.Close()

		mu.Lock()
		if attempts != 1 {
			t.Errorf("a %d was asked again: %d attempts", status, attempts)
		}
		mu.Unlock()
	}
}

// And it stops being tried when it has been tried enough, and when the panel
// is stopping: Wait must not hold a shutdown for half a minute of retries.
func TestAnEndlesslyBusyServiceIsGivenUpOnAndDoesNotHoldUpShutdown(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	d := retryingDispatcher(server.URL)
	d.Notify(t.Context(), "team_1", EventDeployFailed, Message{Title: "x"})
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return attempts >= 3 })
	time.Sleep(60 * time.Millisecond)
	d.Wait()
	mu.Lock()
	if attempts != 3 {
		t.Errorf("a service that never answered was asked %d times, want 3: the first and two retries", attempts)
	}
	mu.Unlock()

	// A long wait between attempts is cut short by Wait.
	slow := retryingDispatcher(server.URL)
	slow.retryAfter = []time.Duration{time.Hour}
	slow.Notify(t.Context(), "team_1", EventDeployFailed, Message{Title: "x"})
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return attempts >= 4 })
	done := make(chan struct{})
	go func() { slow.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait is held for the hour a retry was going to wait")
	}
}

func TestWhichFailuresAreWorthTryingAgain(t *testing.T) {
	for err, want := range map[error]bool{
		&statusError{code: 503}:                     true,
		&statusError{code: 429}:                     true,
		&statusError{code: 500}:                     true,
		&statusError{code: 404}:                     false,
		&statusError{code: 401}:                     false,
		io.ErrUnexpectedEOF:                         true,
		fmt.Errorf("send notification: %w", io.EOF): true,
		errors.New("this address is not allowed"):   false,
	} {
		if got := transient(err); got != want {
			t.Errorf("%v: transient=%v, want %v", err, got, want)
		}
	}
}
