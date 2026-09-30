// Package runsafe keeps a panic inside the goroutine that caused it.
//
// The panel's HTTP handlers have recovered from a panic since the beginning:
// one bad request returns a 500 and the next request is served. Everything else
// did not. A deployment, a server being provisioned, a database, a backup, a
// restore, the watcher and the scheduler all run in goroutines of their own,
// and a panic in any of them takes the process down — which for a panel that is
// often the only way to reach the cluster means the deployment that crashed it
// also removed the thing you would use to find out why.
//
// The cost of continuing is small and known: a panic is a bug, it is logged
// with its stack, and the one operation it was running is reported as failed.
// The cost of not continuing is every other deployment, backup and login on
// that panel.
//
// # The list above was written by hand, and was wrong
//
// Those are the goroutines somebody remembered. Six others were not: an app's
// own log output being scrubbed on its way to a browser, the fan-out to every
// open tab, an event posted to somebody else's plugin container, the two SSH
// readers during provisioning, the guard in front of an app, and the panel's
// own listener. Each of them could end the process.
//
// A test in this package now reads every goroutine in internal/ and fails the
// build when one has nothing to catch a panic, so the list is computed rather
// than remembered.
//
// It cannot check that recovering is the right thing to do, and sometimes it is
// not enough on its own: a goroutine whose only job is to send on a channel
// turns a crash into a wait that never ends if it merely recovers. Those send
// the failure instead, so whoever is waiting is released. That judgement is the
// author's; the test only makes sure it was made.
package runsafe

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Recover turns a panic into a logged error and, when handle is given, into a
// failure the user can see.
//
// It is used as the first line of a background goroutine:
//
//	defer runsafe.Recover(log, "deploy", func(err error) { fail(step, err) })
//
// handle runs under its own recover, because the thing it usually does is
// write to the database, and a panel already in trouble is exactly where that
// fails too.
func Recover(log *slog.Logger, what string, handle func(error)) {
	rec := recover()
	if rec == nil {
		return
	}
	stack := debug.Stack()
	err := fmt.Errorf("%s panicked: %v", what, rec)

	if log != nil {
		log.Error("panic in a background task", "task", what, "panic", rec, "stack", string(stack))
	}
	if handle == nil {
		return
	}
	defer func() {
		if second := recover(); second != nil && log != nil {
			log.Error("panic while reporting a panic", "task", what, "panic", second,
				"stack", string(debug.Stack()))
		}
	}()
	handle(err)
}

// Go runs fn in a new goroutine, under Recover.
//
// For work with nothing to report a failure to: the dispatcher sending a
// notification, a sweep, a refresh. Where there is an operation or a deployment
// to mark failed, use Recover directly so the user is told.
func Go(log *slog.Logger, what string, fn func()) {
	go func() {
		defer Recover(log, what, nil)
		fn()
	}()
}
