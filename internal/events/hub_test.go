package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublishReachesOnlyMatchingSubscribers(t *testing.T) {
	h := NewHub(8)
	ctx := t.Context()

	wanted := h.Subscribe(ctx, 0, "topic-a")
	defer wanted.Close()
	other := h.Subscribe(ctx, 0, "topic-b")
	defer other.Close()

	h.Publish("topic-a", "message", map[string]string{"hello": "world"})

	select {
	case ev := <-wanted.Events():
		if ev.Type != "message" {
			t.Fatalf("event type %q, want message", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber to topic-a never received the event")
	}

	select {
	case ev := <-other.Events():
		t.Fatalf("subscriber to topic-b received an event for topic-a: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSlowSubscriberIsDroppedNotBlocking(t *testing.T) {
	h := NewHub(2)
	ctx := t.Context()
	sub := h.Subscribe(ctx, 0, "busy")
	defer sub.Close()

	// Publishing far more than the buffer must not block. If this test hangs,
	// a stalled browser tab could hold up a deploy.
	done := make(chan struct{})
	go func() {
		for i := range 100 {
			h.Publish("busy", "tick", i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}

	if sub.Dropped() == 0 {
		t.Fatal("a subscriber that consumed nothing reported no drops, so the UI would not know to reload")
	}
}

func TestReplayResumesFromLastSeq(t *testing.T) {
	h := NewHub(64)
	ctx := t.Context()

	var seqs []int64
	for i := range 5 {
		ev := h.Publish("build", "log", i)
		seqs = append(seqs, ev.Seq)
	}

	// A client that saw the third event reconnects and asks for the rest.
	sub := h.Subscribe(ctx, seqs[2], "build")
	defer sub.Close()

	var got []int64
	for range 2 {
		select {
		case ev := <-sub.Events():
			got = append(got, ev.Seq)
		case <-time.After(time.Second):
			t.Fatalf("replay stopped after %d events, want 2", len(got))
		}
	}
	if got[0] != seqs[3] || got[1] != seqs[4] {
		t.Fatalf("replayed %v, want %v", got, seqs[3:])
	}

	select {
	case ev := <-sub.Events():
		t.Fatalf("replay sent an event the client had already seen: %d", ev.Seq)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCloseIsIdempotentAndContextCancelCleansUp(t *testing.T) {
	h := NewHub(8)
	ctx, cancel := context.WithCancel(t.Context())
	sub := h.Subscribe(ctx, 0, "topic")

	if h.SubscriberCount() != 1 {
		t.Fatalf("subscriber count is %d, want 1", h.SubscriberCount())
	}
	sub.Close()
	// Closing twice must not panic on a closed channel.
	sub.Close()
	if h.SubscriberCount() != 0 {
		t.Fatalf("subscriber count is %d after Close, want 0", h.SubscriberCount())
	}

	other := h.Subscribe(ctx, 0, "topic")
	defer other.Close()
	cancel()
	deadline := time.After(time.Second)
	for h.SubscriberCount() != 0 {
		select {
		case <-deadline:
			t.Fatal("cancelling the request context did not remove the subscriber")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestEncodeProducesValidSSE(t *testing.T) {
	ev := Event{Type: "progress", Seq: 42, Data: map[string]any{"step": "join", "percent": 60}}
	raw, err := ev.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "id: 42\nevent: progress\ndata: ") {
		t.Fatalf("unexpected SSE frame:\n%s", text)
	}
	if !strings.HasSuffix(text, "\n\n") {
		t.Fatal("SSE frame must end with a blank line or the browser buffers it")
	}
	body := strings.TrimSuffix(strings.TrimPrefix(text, "id: 42\nevent: progress\ndata: "), "\n\n")
	if strings.Contains(body, "\n") {
		t.Fatal("a newline inside the data field would split the frame")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("data field is not valid JSON: %v", err)
	}
	if decoded["step"] != "join" {
		t.Fatalf("payload lost data: %v", decoded)
	}
}

func TestForgetTopicDropsHistory(t *testing.T) {
	h := NewHub(8)
	ev := h.Publish("deployment:dep_1", "log", "line")
	h.ForgetTopic("deployment:dep_1")

	sub := h.Subscribe(t.Context(), ev.Seq-1, "deployment:dep_1")
	defer sub.Close()
	select {
	case got := <-sub.Events():
		t.Fatalf("history survived ForgetTopic: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestHistoryIsForgotten is the leak this exists for: every deployment, every
// operation and every app's log stream is its own topic, and the hub used to
// keep all of them, with their log lines, for as long as the panel ran.
func TestHistoryIsForgotten(t *testing.T) {
	hub := NewHub(8)
	start := time.Now()

	for i := range 50 {
		hub.Publish(DeploymentTopic(fmt.Sprintf("dep_%d", i)), "log", "a line of build output")
	}
	if got := hub.TopicCount(); got != 50 {
		t.Fatalf("the hub holds %d topics, want 50", got)
	}

	// Nothing has been published to any of them since. A sweep an hour later
	// finds nothing worth keeping.
	if dropped := hub.Sweep(start.Add(time.Hour)); dropped != 50 {
		t.Fatalf("the sweep dropped %d topics, want 50", dropped)
	}
	if got := hub.TopicCount(); got != 0 {
		t.Fatalf("%d topics survived a sweep an hour after their last event", got)
	}
}

// TestALiveTopicIsKept: a build that is still running, and a client that
// reconnects while it is, must not lose its catch-up.
func TestALiveTopicIsKept(t *testing.T) {
	hub := NewHub(8)
	first := hub.Publish(DeploymentTopic("dep_live"), "log", "starting")
	hub.Publish(DeploymentTopic("dep_live"), "log", "still building")

	if dropped := hub.Sweep(time.Now().Add(time.Minute)); dropped != 0 {
		t.Fatalf("the sweep dropped %d topics a minute after they were written to", dropped)
	}

	// A reconnecting client says what it last saw, and gets what came after.
	sub := hub.Subscribe(t.Context(), first.Seq, DeploymentTopic("dep_live"))
	defer sub.Close()
	select {
	case ev := <-sub.Events():
		if ev.Data != "still building" {
			t.Fatalf("replayed %v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("a live topic's history was not replayed")
	}
}

// TestTheHubDoesNotGrowWithoutBound: a burst between sweeps must not be the
// thing that ends the panel.
func TestTheHubDoesNotGrowWithoutBound(t *testing.T) {
	hub := NewHub(8)
	hub.maxTopics = 10

	for i := range 100 {
		hub.Publish(DeploymentTopic(fmt.Sprintf("dep_%d", i)), "log", "output")
	}
	if got := hub.TopicCount(); got > 10 {
		t.Fatalf("the hub holds %d topics with a limit of 10", got)
	}
}

// TestPublishingWhileAClientDisconnectsDoesNotPanic.
//
// Publish used to collect its subscribers under the lock, release it, and then
// send. Between those two steps a client whose request context had just ended
// had its channel closed by the goroutine watching that context — and a send on
// a closed channel is a panic, in whatever was publishing. A browser tab closed
// during a build, which emits thousands of log events, is all it takes.
//
// This fails in a few hundred iterations without the fix.
func TestPublishingWhileAClientDisconnectsDoesNotPanic(t *testing.T) {
	hub := NewHub(1)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				hub.Publish("build", "log", "a line")
			}
		}
	}()

	for i := 0; i < 5000; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		hub.Subscribe(ctx, 0, "build")
		cancel()
	}
	close(stop)
	wg.Wait()
}
