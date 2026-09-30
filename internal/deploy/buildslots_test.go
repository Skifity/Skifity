package deploy

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Builds past the limit wait, in the order they arrived, and a cancelled one
// does not hold up the rest.
func TestBuildsBeyondTheLimitWaitTheirTurn(t *testing.T) {
	slots := newBuildSlots()
	limit := func() int { return 2 }
	quiet := func(int, int) {}

	first, err := slots.acquire(t.Context(), limit, quiet)
	if err != nil {
		t.Fatal(err)
	}
	second, err := slots.acquire(t.Context(), limit, quiet)
	if err != nil {
		t.Fatal(err)
	}

	// Three more arrive in order; none may start while two run.
	var mu sync.Mutex
	var started []int
	var toldAhead []int
	var wg sync.WaitGroup
	releases := make(chan func(), 3)
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := slots.acquire(t.Context(), limit, func(running, ahead int) {
				mu.Lock()
				toldAhead = append(toldAhead, ahead)
				mu.Unlock()
			})
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			started = append(started, i)
			mu.Unlock()
			releases <- release
		}()
		// Arrive one after the other, so the order is known.
		waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(toldAhead) == i+1 })
	}
	mu.Lock()
	if len(started) != 0 {
		t.Fatalf("%v started while two builds ran", started)
	}
	if toldAhead[0] != 0 || toldAhead[1] != 1 || toldAhead[2] != 2 {
		t.Errorf("the queue positions said were %v", toldAhead)
	}
	mu.Unlock()

	first()
	first() // freeing a slot twice frees it once
	(<-releases)()
	second()
	(<-releases)()
	(<-releases)()
	wg.Wait()
	if len(started) != 3 || started[0] != 0 || started[1] != 1 || started[2] != 2 {
		t.Errorf("they started in the order %v, want the order they came in", started)
	}
}

func TestACancelledBuildLeavesTheQueue(t *testing.T) {
	slots := newBuildSlots()
	limit := func() int { return 1 }
	quiet := func(int, int) {}
	running, err := slots.acquire(t.Context(), limit, quiet)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancelled := make(chan error, 1)
	go func() {
		_, err := slots.acquire(ctx, limit, quiet)
		cancelled <- err
	}()
	waitFor(t, func() bool { slots.mu.Lock(); defer slots.mu.Unlock(); return len(slots.queue) == 1 })

	behind := make(chan func(), 1)
	go func() {
		release, err := slots.acquire(t.Context(), limit, quiet)
		if err != nil {
			t.Error(err)
		}
		behind <- release
	}()
	waitFor(t, func() bool { slots.mu.Lock(); defer slots.mu.Unlock(); return len(slots.queue) == 2 })

	cancel()
	if err := <-cancelled; err == nil {
		t.Fatal("a cancelled build got a slot")
	}
	running()
	select {
	case release := <-behind:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("the build behind a cancelled one never started")
	}
}

// A limit raised while builds wait starts them without one having to finish.
func TestARaisedLimitStartsWaitingBuilds(t *testing.T) {
	slots := newBuildSlots()
	var mu sync.Mutex
	allowed := 1
	limit := func() int { mu.Lock(); defer mu.Unlock(); return allowed }
	quiet := func(int, int) {}
	held, err := slots.acquire(t.Context(), limit, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer held()

	got := make(chan func(), 1)
	go func() {
		release, _ := slots.acquire(t.Context(), limit, quiet)
		got <- release
	}()
	waitFor(t, func() bool { slots.mu.Lock(); defer slots.mu.Unlock(); return len(slots.queue) == 1 })
	mu.Lock()
	allowed = 2
	mu.Unlock()
	// Anything that wakes the queue makes it read the limit again; the
	// periodic recheck is the fallback when nothing does.
	slots.mu.Lock()
	slots.wake()
	slots.mu.Unlock()
	select {
	case release := <-got:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("a raised limit did not start the waiting build")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
