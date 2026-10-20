package netfence

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

// policyAfter answers the way a server does while its policy is being written:
// connections are accepted, then refused once the rules are in.
func policyAfter(accepted int) (DialFunc, *int) {
	attempts := 0
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		attempts++
		if attempts <= accepted {
			client, server := net.Pipe()
			_ = server.Close()
			return client, nil
		}
		return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
	}, &attempts
}

func TestTheTeamsCodeWaitsForTheFence(t *testing.T) {
	dial, attempts := policyAfter(3)
	result, err := Wait(context.Background(), "10.1.0.216:10250", 10*time.Second, dial)
	if err != nil || !result.Fenced {
		t.Fatalf("Wait = %+v, %v", result, err)
	}
	if *attempts != 4 {
		t.Errorf("it tried %d times, want until the first refusal (4)", *attempts)
	}
}

func TestADroppedConnectionIsTheFenceToo(t *testing.T) {
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	result, err := Wait(context.Background(), "10.1.0.216:10250", 10*time.Second, dial)
	if err != nil || !result.Fenced {
		t.Fatalf("Wait = %+v, %v", result, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("one dropped connection held the pod up for %s", time.Since(start))
	}
}

// A cluster that enforces no policy never refuses: the pod starts anyway, and
// says it is not fenced, rather than never starting.
func TestNoFenceAtAllStillStartsThePod(t *testing.T) {
	dial, _ := policyAfter(1 << 30)
	result, err := Wait(context.Background(), "10.1.0.216:10250", 300*time.Millisecond, dial)
	if err != nil {
		t.Fatal(err)
	}
	if result.Fenced {
		t.Error("a server that never refused was taken as fenced")
	}
}

func TestStoppingThePodStopsTheWait(t *testing.T) {
	dial, _ := policyAfter(1 << 30)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if _, err := Wait(ctx, "10.1.0.216:10250", time.Hour, dial); !errors.Is(err, context.Canceled) {
		t.Errorf("Wait = %v, want it cancelled", err)
	}
}

func TestTheTargetIsTheKubeletOnThePodsServer(t *testing.T) {
	if got, err := Target("10.1.0.216"); err != nil || got != "10.1.0.216:10250" {
		t.Errorf("Target = %q, %v", got, err)
	}
	if got, err := Target("2001:db8::1"); err != nil || got != "[2001:db8::1]:10250" {
		t.Errorf("Target = %q, %v", got, err)
	}
	for _, bad := range []string{"", "server", "10.1.0.216:22"} {
		if _, err := Target(bad); err == nil {
			t.Errorf("%q was taken as a server's address", bad)
		}
	}
	if _, err := Wait(context.Background(), "no port", time.Second, nil); err == nil {
		t.Error("a target with no port was dialled")
	}
}
