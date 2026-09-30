package deploy

import (
	"context"
	"sync"
	"time"
)

// buildSlots lets a few builds run at once and queues the rest, first come
// first served.
//
// Every build is a pod asking for half a gigabyte and allowed three, all in
// one BuildKit. Five pushes landing together — a monorepo's apps, a Renovate
// morning — started five, and on a two-server cluster that is the apps' own
// memory the builds took. Coolify and Dokploy both queue builds; this is the
// same, with the limit a setting that can change while builds wait.
type buildSlots struct {
	mu      sync.Mutex
	running int
	// queue is the tickets waiting, oldest first. Only the head may start.
	queue []uint64
	next  uint64
	// urgent are the tickets that joined at the front, which are always the
	// first len(urgent) of queue, in the order they came. See acquireFirst.
	urgent map[uint64]bool
	// changed is closed, and replaced, whenever a slot frees or the queue
	// moves, which is what a waiting build wakes on.
	changed chan struct{}
}

func newBuildSlots() *buildSlots {
	return &buildSlots{changed: make(chan struct{}), urgent: map[uint64]bool{}}
}

// recheck is how often a waiting build reads the limit again, so a limit
// raised in the settings starts builds without waiting for one to finish.
const recheck = 15 * time.Second

// acquire waits for a slot and returns the function that frees it. limit is
// read each time a build is considered, so a change applies to the queue as
// it stands. waiting is called once, with how many are running, when the
// build has to wait at all.
func (b *buildSlots) acquire(ctx context.Context, limit func() int, waiting func(running, ahead int)) (func(), error) {
	return b.join(ctx, false, limit, waiting)
}

// acquireFirst waits for a slot ahead of everything that joined with acquire,
// and behind anything else that joined this way.
//
// For work somebody is waiting on, in a queue that is otherwise filled by work
// nobody is: a deploy whose image has to be scanned before it goes out, behind
// the nightly rescan of every app in the panel.
func (b *buildSlots) acquireFirst(ctx context.Context, limit func() int, waiting func(running, ahead int)) (func(), error) {
	return b.join(ctx, true, limit, waiting)
}

func (b *buildSlots) join(ctx context.Context, first bool, limit func() int, waiting func(running, ahead int)) (func(), error) {
	b.mu.Lock()
	b.next++
	ticket := b.next
	if first {
		at := len(b.urgent)
		b.queue = append(b.queue[:at], append([]uint64{ticket}, b.queue[at:]...)...)
		b.urgent[ticket] = true
	} else {
		b.queue = append(b.queue, ticket)
	}
	b.mu.Unlock()

	told := false
	for {
		b.mu.Lock()
		allowed := max(limit(), 1)
		if b.queue[0] == ticket && b.running < allowed {
			b.queue = b.queue[1:]
			delete(b.urgent, ticket)
			b.running++
			b.wake()
			b.mu.Unlock()
			var once sync.Once
			return func() { once.Do(b.release) }, nil
		}
		changed, running, ahead := b.changed, b.running, b.position(ticket)
		b.mu.Unlock()

		if !told {
			waiting(running, ahead)
			told = true
		}
		select {
		case <-ctx.Done():
			b.leave(ticket)
			return nil, ctx.Err()
		case <-changed:
		case <-time.After(recheck):
		}
	}
}

func (b *buildSlots) release() {
	b.mu.Lock()
	b.running--
	b.wake()
	b.mu.Unlock()
}

// leave takes a cancelled build out of the queue, so the one behind it is
// not stuck behind a ticket nobody holds.
func (b *buildSlots) leave(ticket uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, t := range b.queue {
		if t == ticket {
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			break
		}
	}
	delete(b.urgent, ticket)
	b.wake()
}

// position is how many builds are queued ahead of a ticket.
func (b *buildSlots) position(ticket uint64) int {
	for i, t := range b.queue {
		if t == ticket {
			return i
		}
	}
	return 0
}

// wake tells every waiting build to look again. The caller holds mu.
func (b *buildSlots) wake() {
	close(b.changed)
	b.changed = make(chan struct{})
}
