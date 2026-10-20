// Package netfence waits, inside a new pod, for that pod's network policy to
// take effect.
//
// The policy controller k3s runs, kube-router, writes a pod's rules once the
// pod has an address, not before its containers start. For that moment — under
// a second on a quiet server, longer on a busy one — a new pod is not fenced at
// all. The first real cluster this ran on showed it: a pod in an app's
// namespace opened a connection to the Kubernetes API on its server's own
// address, which its policy forbids, and was refused a moment later for
// everything after. A connection opened in that moment outlives it, because the
// rules let an established connection be; and the cloud's metadata service,
// with whatever credentials the machine was given, is one request away.
//
// So every pod that runs a team's code starts with a container that does
// nothing but wait for the fence, and the team's code starts after it. The test
// is a connection to the kubelet on the pod's own server: the kubelet listens
// there on every server, and no tenant's policy lets a pod reach it, so the
// connection is accepted until the rules are written and refused from then on.
package netfence

import (
	"context"
	"errors"
	"net"
	"time"
)

// KubeletPort is what the probe connects to on the pod's own server.
const KubeletPort = "10250"

// DialFunc opens a connection, the way net.Dialer.DialContext does. A test
// passes its own.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Result is how the wait ended.
type Result struct {
	// Fenced is true when a connection was refused, or timed out, which is the
	// rules being in place.
	Fenced bool
	// Waited is how long it took.
	Waited time.Duration
}

// interval is the pause between attempts, and attemptTimeout how long one
// connection is given. A dropped packet rather than a refused one is the rules
// too — kube-router refuses, other controllers drop — so a timeout counts as
// fenced, and it is short so that waiting on one does not hold the pod up.
const (
	interval       = 100 * time.Millisecond
	attemptTimeout = 500 * time.Millisecond
)

// Wait connects to target until it cannot, or until patience runs out.
//
// Running out of patience is not an error, and the pod starts anyway: a cluster
// that enforces no policy at all would otherwise never start a pod again, and
// waiting longer would not fence it. The caller says so in the pod's log.
func Wait(ctx context.Context, target string, patience time.Duration, dial DialFunc) (Result, error) {
	if _, _, err := net.SplitHostPort(target); err != nil {
		return Result{}, err
	}
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	start := time.Now()
	deadline := start.Add(patience)
	for {
		attempt, cancel := context.WithTimeout(ctx, attemptTimeout)
		conn, err := dial(attempt, "tcp", target)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return Result{Waited: time.Since(start)}, ctx.Err()
			}
			return Result{Fenced: true, Waited: time.Since(start)}, nil
		}
		_ = conn.Close()
		if !time.Now().Before(deadline) {
			return Result{Waited: time.Since(start)}, nil
		}
		select {
		case <-ctx.Done():
			return Result{Waited: time.Since(start)}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Target is where the probe connects: the kubelet on the server the pod runs
// on, whose address the pod is given by the Kubernetes downward API.
func Target(hostIP string) (string, error) {
	ip := net.ParseIP(hostIP)
	if ip == nil {
		return "", errors.New("the pod was not told its server's address")
	}
	return net.JoinHostPort(ip.String(), KubeletPort), nil
}
