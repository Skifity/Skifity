package deploy

import (
	"context"
	"strings"
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

// Work somebody is waiting on goes ahead of the queue, in the order it came,
// and the queue keeps its own order behind it: a deploy's scan does not wait
// for the nightly rescan of every app.
func TestWorkSomebodyWaitsOnGoesFirst(t *testing.T) {
	slots := newBuildSlots()
	one := func() int { return 1 }
	quiet := func(int, int) {}
	held, err := slots.acquire(t.Context(), one, quiet)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var started []string
	releases := make(chan func(), 4)
	join := func(name string, first bool) {
		go func() {
			acquire := slots.acquire
			if first {
				acquire = slots.acquireFirst
			}
			release, err := acquire(t.Context(), one, quiet)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			started = append(started, name)
			mu.Unlock()
			releases <- release
		}()
	}
	queued := func(n int) func() bool {
		return func() bool { slots.mu.Lock(); defer slots.mu.Unlock(); return len(slots.queue) == n }
	}
	join("nightly-1", false)
	waitFor(t, queued(1))
	join("nightly-2", false)
	waitFor(t, queued(2))
	join("deploy-1", true)
	waitFor(t, queued(3))
	join("deploy-2", true)
	waitFor(t, queued(4))

	held()
	for range 4 {
		select {
		case release := <-releases:
			release()
		case <-time.After(2 * time.Second):
			t.Fatalf("the queue stopped after %v", started)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(started, ", "); got != "deploy-1, deploy-2, nightly-1, nightly-2" {
		t.Errorf("they started in the order %s", got)
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

// A team that pushed forty builds is not forty places in front of the team that
// pushed one a second later. Of the builds waiting, the next to run belongs to
// the team with the fewest running.
func TestOneTeamsBurstDoesNotHoldEveryoneElseBehindIt(t *testing.T) {
	slots := newBuildSlots()
	two := func() int { return 2 }
	quiet := func(int, int) {}

	// Acme holds both slots, and queues three more behind them.
	heldA, err := slots.acquireFor(t.Context(), "acme", two, quiet)
	if err != nil {
		t.Fatal(err)
	}
	heldB, err := slots.acquireFor(t.Context(), "acme", two, quiet)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	releases := make(chan func(), 8)
	arrive := func(team string, label string) {
		told := make(chan struct{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := slots.acquireFor(t.Context(), team, two, func(int, int) { close(told) })
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, label)
			mu.Unlock()
			releases <- release
		}()
		select {
		case <-told:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not queue", label)
		}
	}
	for _, label := range []string{"acme-3", "acme-4", "acme-5"} {
		arrive("acme", label)
	}
	// And a different team arrives last.
	arrive("globex", "globex-1")

	// One of Acme's builds finishes. Acme still has one running and Globex has
	// none, so Globex goes next although it is last in line.
	heldA()
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(order) == 1 })
	mu.Lock()
	if order[0] != "globex-1" {
		t.Errorf("%s went first, and the team with nothing running should have", order[0])
	}
	mu.Unlock()

	// The rest follow in the order they came, whoever is running what.
	heldB()
	(<-releases)()
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(order) >= 2 })
	(<-releases)()
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(order) >= 3 })
	(<-releases)()
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(order) >= 4 })
	(<-releases)()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"globex-1", "acme-3", "acme-4", "acme-5"}
	for i, label := range want {
		if order[i] != label {
			t.Errorf("the order builds ran in was %v, want %v", order, want)
			break
		}
	}
}
