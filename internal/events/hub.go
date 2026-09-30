// Package events is the panel's realtime fan-out: install progress, build logs,
// app logs and cluster status all reach the browser through it as Server-Sent
// Events (ADR-0005).
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"skifity/internal/runsafe"
)

// Event is one message sent to subscribers of a topic.
type Event struct {
	// Topic routes the event, for example "operation:op_123" or "app:app_9/logs".
	Topic string `json:"topic"`
	// Type is the SSE event name the browser listens for.
	Type string `json:"type"`
	// Seq lets a reconnecting client skip what it already saw.
	Seq int64 `json:"seq"`
	// Data is the JSON payload.
	Data any `json:"data"`
	// At is when the event was published.
	At time.Time `json:"at"`
}

// Encode renders an event in the SSE wire format.
func (e Event) Encode() ([]byte, error) {
	payload, err := json.Marshal(e.Data)
	if err != nil {
		return nil, fmt.Errorf("encode event data: %w", err)
	}
	// SSE frames cannot contain a bare newline in a data field, and json.Marshal
	// never emits one, so a single data line is always correct here.
	return fmt.Appendf(nil, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, payload), nil
}

// subscriber is one connected client.
type subscriber struct {
	ch     chan Event
	topics map[string]bool
	// dropped counts events this subscriber missed because it was too slow.
	// The UI uses it to decide it must reload rather than trust its state.
	dropped atomic.Int64
}

// Hub fans events out to subscribers.
//
// Publishing never blocks: a subscriber that is not keeping up loses events and
// is told so, rather than stalling a deploy because someone's laptop went to
// sleep with a log window open.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[*subscriber]struct{}
	seq         atomic.Int64
	// buffer is how many events a slow subscriber may fall behind by.
	buffer int
	// history keeps recent events per topic so a reconnecting client can catch
	// up without refetching the whole resource.
	//
	// Every deployment, every operation and every app's log stream is its own
	// topic, so this map grows with everything that ever happened. It is swept
	// by age: a client that reconnects within the window catches up, and one
	// that does not was never going to.
	history      map[string]*topicHistory
	historyLimit int
	// historyTTL is how long a finished topic's events are worth keeping.
	historyTTL time.Duration
	// maxTopics bounds the map between sweeps, for the burst a sweep has not
	// caught up with yet.
	maxTopics int
}

// topicHistory is one topic's recent events and when it last saw one.
type topicHistory struct {
	events []Event
	lastAt time.Time
}

// NewHub creates a hub. buffer is the per-subscriber queue depth.
func NewHub(buffer int) *Hub {
	if buffer <= 0 {
		buffer = 64
	}
	return &Hub{
		subscribers:  map[*subscriber]struct{}{},
		buffer:       buffer,
		history:      map[string]*topicHistory{},
		historyLimit: 200,
		// Half an hour: long enough that a laptop lid closed over lunch still
		// catches up on a build, short enough that a panel running for months
		// is not holding every log line it ever streamed.
		historyTTL: 30 * time.Minute,
		maxTopics:  5000,
	}
}

// Publish sends an event to everyone subscribed to its topic.
func (h *Hub) Publish(topic, eventType string, data any) Event {
	ev := Event{
		Topic: topic,
		Type:  eventType,
		Seq:   h.seq.Add(1),
		Data:  data,
		At:    time.Now().UTC(),
	}

	h.mu.Lock()
	entry := h.history[topic]
	if entry == nil {
		if len(h.history) >= h.maxTopics {
			// A burst between sweeps. Dropping the oldest topic loses a catch-up
			// nobody is waiting for; not dropping it loses the panel.
			h.evictOldestLocked()
		}
		entry = &topicHistory{}
		h.history[topic] = entry
	}
	entry.events = append(entry.events, ev)
	if len(entry.events) > h.historyLimit {
		entry.events = entry.events[len(entry.events)-h.historyLimit:]
	}
	entry.lastAt = ev.At

	// The sends happen under the same lock that registers and removes a
	// subscriber, and this is not an optimisation to undo.
	//
	// Collecting the subscribers, unlocking and then sending looks kinder to
	// the lock and is a panic: between the unlock and the send, a client whose
	// request context has just ended has its channel closed, and sending on a
	// closed channel takes down whatever was publishing. A browser tab closed
	// during a build, which emits thousands of log events, is enough to hit it.
	//
	// Holding the lock is safe because every send below is non-blocking: the
	// default arm drops the event rather than waiting for a reader, so this
	// section never sleeps and never re-enters the hub.
	for s := range h.subscribers {
		if !s.topics[topic] {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			// The client is too far behind. Dropping is the right trade: the
			// alternative is holding up the operation that produced the event.
			s.dropped.Add(1)
		}
	}
	h.mu.Unlock()
	return ev
}

// Subscription is a live connection to the hub.
type Subscription struct {
	hub *Hub
	sub *subscriber
}

// Events is the channel to read from. It is closed when the subscription ends.
func (s *Subscription) Events() <-chan Event { return s.sub.ch }

// Dropped reports how many events this subscriber missed.
func (s *Subscription) Dropped() int64 { return s.sub.dropped.Load() }

// Close ends the subscription and releases its resources.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	if _, ok := s.hub.subscribers[s.sub]; ok {
		delete(s.hub.subscribers, s.sub)
		close(s.sub.ch)
	}
	s.hub.mu.Unlock()
}

// Subscribe registers interest in one or more topics. Events published since
// lastSeq are replayed first, so a reconnect does not lose the middle of a build
// log.
func (h *Hub) Subscribe(ctx context.Context, lastSeq int64, topics ...string) *Subscription {
	sub := &subscriber{
		ch:     make(chan Event, h.buffer),
		topics: make(map[string]bool, len(topics)),
	}
	for _, t := range topics {
		sub.topics[t] = true
	}

	h.mu.Lock()
	// Collect the replay under the same lock that registers the subscriber, so
	// an event published concurrently is either replayed or delivered live,
	// never both and never neither.
	var replay []Event
	if lastSeq > 0 {
		for _, t := range topics {
			entry := h.history[t]
			if entry == nil {
				continue
			}
			for _, ev := range entry.events {
				if ev.Seq > lastSeq {
					replay = append(replay, ev)
				}
			}
		}
	}
	h.subscribers[sub] = struct{}{}
	h.mu.Unlock()

	for _, ev := range replay {
		select {
		case sub.ch <- ev:
		default:
			sub.dropped.Add(1)
		}
	}

	// Tie the subscription's life to the request context.
	go func() {
		// Closing an already-closed channel panics, and this runs in the
		// panel's own process: the fan-out for one browser tab must not be
		// able to end every other session on the panel.
		defer runsafe.Recover(slog.Default(), "releasing an event subscription", nil)
		<-ctx.Done()
		h.mu.Lock()
		if _, ok := h.subscribers[sub]; ok {
			delete(h.subscribers, sub)
			close(sub.ch)
		}
		h.mu.Unlock()
	}()

	return &Subscription{hub: h, sub: sub}
}

// SubscriberCount reports how many clients are connected, for /api/health.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}

// ForgetTopic drops a topic's history once whatever produced it is finished, so
// a long-lived panel does not keep every build log in memory.
func (h *Hub) ForgetTopic(topic string) {
	h.mu.Lock()
	delete(h.history, topic)
	h.mu.Unlock()
}

// Sweep drops the history of topics nothing has published to for a while, and
// returns how many it dropped.
//
// This is what keeps the hub bounded. Producers are not asked to remember: a
// deployment that failed, an operation that was cancelled and an app whose log
// window was closed all stop publishing, and all end up here.
func (h *Hub) Sweep(now time.Time) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	dropped := 0
	for topic, entry := range h.history {
		if now.Sub(entry.lastAt) > h.historyTTL {
			delete(h.history, topic)
			dropped++
		}
	}
	return dropped
}

// TopicCount reports how many topics hold history, for /api/health and for a
// test that wants to see the sweep work.
func (h *Hub) TopicCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.history)
}

// evictOldestLocked removes the least recently used topic. The caller holds the
// lock.
func (h *Hub) evictOldestLocked() {
	var oldest string
	var oldestAt time.Time
	for topic, entry := range h.history {
		if oldest == "" || entry.lastAt.Before(oldestAt) {
			oldest, oldestAt = topic, entry.lastAt
		}
	}
	if oldest != "" {
		delete(h.history, oldest)
	}
}

// Topic helpers keep topic strings in one place rather than spread as literals.

// OperationTopic is the topic for one long-running operation's progress.
func OperationTopic(id string) string { return "operation:" + id }

// DeploymentTopic is the topic for one deployment's build log and status.
func DeploymentTopic(id string) string { return "deployment:" + id }

// AppLogTopic is the topic for one app's runtime logs.
func AppLogTopic(id string) string { return "app-logs:" + id }

// TeamTopic is the topic for team-wide changes, which is what keeps lists fresh.
func TeamTopic(id string) string { return "team:" + id }

// Subscribers is how many clients are listening.
//
// A number that climbs and never comes down is a subscription that is not being
// closed, which on a long-lived panel is the leak that eventually takes it down.
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}
