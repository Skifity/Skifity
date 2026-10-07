package api

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// A flood from one address costs the panel one address's worth of work.

func guardAt(now *time.Time) *floodGuard {
	g := newFloodGuard()
	g.now = func() time.Time { return *now }
	return g
}

func TestOneAddressCannotAskFasterThanTheBucketRefills(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	g := guardAt(&now)

	// A page of the interface, and a script that is not in a hurry, never meet it.
	for i := 0; i < int(floodBurst); i++ {
		if ok, _ := g.allow("203.0.113.9"); !ok {
			t.Fatalf("request %d was refused, inside the burst", i+1)
		}
	}
	ok, wait := g.allow("203.0.113.9")
	if ok {
		t.Fatal("a request past the burst was let in")
	}
	if wait < 1 {
		t.Errorf("a refused caller was told to wait %d seconds, and would retry at once", wait)
	}

	// Somebody else is not affected.
	if ok, _ := g.allow("198.51.100.4"); !ok {
		t.Error("one address's flood refused another address")
	}

	// And a second later the bucket has refilled enough for the first to go on.
	now = now.Add(time.Second)
	for i := 0; i < int(floodRate); i++ {
		if ok, _ := g.allow("203.0.113.9"); !ok {
			t.Fatalf("after a second of quiet, request %d of the refill was refused", i+1)
		}
	}
	if ok, _ := g.allow("203.0.113.9"); ok {
		t.Error("more came back in a second than the rate says")
	}
}

func TestTheAddressesRememberedStayBounded(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	g := guardAt(&now)
	for i := 0; i < floodMaxBuckets+5000; i++ {
		g.allow("10." + strconv.Itoa(i/65536) + "." + strconv.Itoa(i/256%256) + "." + strconv.Itoa(i%256))
		now = now.Add(time.Microsecond)
	}
	g.mu.Lock()
	n := len(g.buckets)
	g.mu.Unlock()
	if n > floodMaxBuckets {
		t.Errorf("%d addresses are remembered, more than the %d the bound allows", n, floodMaxBuckets)
	}

	// And a quiet panel forgets them.
	now = now.Add(floodForget + time.Second)
	g.sweep()
	g.mu.Lock()
	n = len(g.buckets)
	g.mu.Unlock()
	if n != 0 {
		t.Errorf("%d addresses are still remembered after they went quiet", n)
	}
}

func TestTheAPIAnswersAFloodWithAWaitAndNothingElse(t *testing.T) {
	h := newHarness(t)
	// A tiny bucket, so the test is not a thousand requests long.
	h.api.flood.burst = 3
	h.api.flood.rate = 1

	for i := 0; i < 3; i++ {
		if status, body := h.do(tenant{}, http.MethodGet, "/api/health", nil); status != http.StatusOK {
			t.Fatalf("request %d: %d %s", i+1, status, body)
		}
	}
	status, body := h.do(tenant{}, http.MethodGet, "/api/health", nil)
	if status != http.StatusTooManyRequests {
		t.Fatalf("a request past the burst was answered %d: %s", status, body)
	}
}

func TestOneAccountCannotHoldOpenMoreStreamsThanTheLimit(t *testing.T) {
	var c streamCounter
	for i := 0; i < maxStreamsPerUser; i++ {
		if !c.acquire("user_1") {
			t.Fatalf("stream %d was refused, inside the limit", i+1)
		}
	}
	if c.acquire("user_1") {
		t.Error("a stream past the limit was let in")
	}
	if !c.acquire("user_2") {
		t.Error("one account's streams used up another's")
	}
	c.release("user_1")
	if !c.acquire("user_1") {
		t.Error("a stream that was closed did not give its place back")
	}
	for i := 0; i < maxStreamsPerUser+3; i++ {
		c.release("user_1")
	}
	if len(c.open) > 1 {
		t.Errorf("an account with no streams is still counted: %v", c.open)
	}
}
