package runsafe

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func logTo(buffer *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buffer, nil))
}

// The whole point: the goroutine dies and the process does not. If this test
// fails, it fails by taking the test binary down with it, which is exactly what
// a panicking deployment used to do to the panel.
func TestAPanicDoesNotReachTheProcess(t *testing.T) {
	var log bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		defer Recover(logTo(&log), "deploy", nil)
		var app *struct{ Name string }
		_ = app.Name // the nil dereference a cluster response can produce
	}()
	wg.Wait()

	if !strings.Contains(log.String(), "deploy") {
		t.Errorf("the log does not name the task that panicked: %s", log.String())
	}
	if !strings.Contains(log.String(), "stack") {
		t.Errorf("the log has no stack, so the bug cannot be found: %s", log.String())
	}
}

// A panic that is only logged leaves the operation it was running sitting at
// "running" forever, which to the person watching is indistinguishable from a
// deployment that is simply slow.
func TestTheWorkItWasDoingIsReportedAsFailed(t *testing.T) {
	var log bytes.Buffer
	var reported error

	func() {
		defer Recover(logTo(&log), "restore", func(err error) { reported = err })
		panic("the archive had no header")
	}()

	if reported == nil {
		t.Fatal("nothing was reported, so the operation would sit at running forever")
	}
	if !strings.Contains(reported.Error(), "restore") {
		t.Errorf("the reported error does not name the task: %v", reported)
	}
	if !strings.Contains(reported.Error(), "the archive had no header") {
		t.Errorf("the reported error does not carry what happened: %v", reported)
	}
}

// Reporting a failure means writing to the database, and a panel already in
// trouble is exactly where that fails too. A panic in the handler must not
// become the panic that gets through.
func TestAPanicWhileReportingIsAlsoContained(t *testing.T) {
	var log bytes.Buffer

	func() {
		defer Recover(logTo(&log), "backup", func(error) { panic("the database is closed") })
		panic("the first one")
	}()

	if !strings.Contains(log.String(), "panic while reporting") {
		t.Errorf("the second panic was not reported: %s", log.String())
	}
}

func TestNothingHappensWithoutAPanic(t *testing.T) {
	var log bytes.Buffer
	called := false

	func() {
		defer Recover(logTo(&log), "sweep", func(error) { called = true })
	}()

	if called {
		t.Error("a failure was reported for work that succeeded")
	}
	if log.Len() != 0 {
		t.Errorf("an ordinary return was logged as a panic: %s", log.String())
	}
}

// signalling is a log handler that says when something was written to it,
// which is how a test waits for a goroutine it does not own: the log line is
// the last thing Recover does.
type signalling struct {
	slog.Handler
	wrote chan struct{}
}

func (h *signalling) Handle(ctx context.Context, record slog.Record) error {
	err := h.Handler.Handle(ctx, record)
	close(h.wrote)
	return err
}

func TestGoContainsAPanicToo(t *testing.T) {
	var buffer bytes.Buffer
	handler := &signalling{
		Handler: slog.NewTextHandler(&buffer, nil),
		wrote:   make(chan struct{}),
	}

	Go(slog.New(handler), "notify", func() { panic("no such webhook") })

	select {
	case <-handler.wrote:
	case <-time.After(5 * time.Second):
		t.Fatal("Go never reported the panic")
	}
	if !strings.Contains(buffer.String(), "notify") {
		t.Errorf("the report does not name the task: %s", buffer.String())
	}
}
