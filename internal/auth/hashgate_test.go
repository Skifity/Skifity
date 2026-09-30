package auth

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Argon2 is a memory cost on purpose: 19 MiB per hash is what makes a stolen
// database expensive to crack. It is also 19 MiB that an anonymous caller can
// ask this panel to allocate, once per sign-in attempt — including attempts for
// accounts that do not exist, which have to hash anyway so that timing does not
// say which addresses are real.
//
// On the 1 GB server this product is sold on, fifty in flight is the whole
// machine. These two tests are the gate that stops that.

func TestOnlySoManyPasswordHashesRunAtOnce(t *testing.T) {
	service := &Service{hashes: make(chan struct{}, maxConcurrentHashes)}

	var inFlight, peak atomic.Int64
	var wg sync.WaitGroup
	for range maxConcurrentHashes * 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := service.enterHash(context.Background())
			if err != nil {
				t.Errorf("enterHash: %v", err)
				return
			}
			defer release()

			now := inFlight.Add(1)
			for {
				high := peak.Load()
				if now <= high || peak.CompareAndSwap(high, now) {
					break
				}
			}
			// Long enough that the others pile up behind this one; short
			// enough that the test is not a pause.
			time.Sleep(2 * time.Millisecond)
			inFlight.Add(-1)
		}()
	}
	wg.Wait()

	if peak.Load() > int64(maxConcurrentHashes) {
		t.Fatalf("%d hashes ran at once, and the gate is %d", peak.Load(), maxConcurrentHashes)
	}
	if peak.Load() < 2 {
		// Otherwise the test would pass on a gate of one, or on a gate that
		// never let anything through at all.
		t.Fatalf("only %d ever ran at once; this test is not exercising the gate", peak.Load())
	}
}

// A caller whose browser has gone away must not keep a slot that a real sign-in
// is waiting for.
func TestWaitingForTheHashGateHonoursTheCaller(t *testing.T) {
	service := &Service{hashes: make(chan struct{}, 1)}
	release, err := service.enterHash(context.Background())
	if err != nil {
		t.Fatalf("enterHash: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.enterHash(ctx); err == nil {
		t.Fatal("a cancelled caller was let into a full gate")
	}
}
