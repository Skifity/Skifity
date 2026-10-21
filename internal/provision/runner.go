package provision

import (
	"context"
	"errors"
	"fmt"
	"time"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/runsafe"
	"skifity/internal/store"
)

// This file holds the bookkeeping the state machine leans on: starting work in
// the background, publishing progress, and recording step outcomes.

// maxOperationDuration bounds a whole operation, so a hung SSH connection or a
// download that never finishes cannot leave an operation running forever.
const maxOperationDuration = 30 * time.Minute

// start runs work in the background, tracked so it can be cancelled.
//
// The work deliberately does not inherit the HTTP request's context: the
// request returns in milliseconds and the work takes minutes.
func (p *Provisioner) start(op store.Operation, work func(context.Context)) {
	ctx, cancel := context.WithTimeout(context.Background(), maxOperationDuration)

	p.mu.Lock()
	if existing, running := p.running[op.ID]; running {
		// Never run the same operation twice at once: two k3s installers on one
		// machine is a reliable way to produce a broken node.
		p.mu.Unlock()
		cancel()
		_ = existing
		return
	}
	p.running[op.ID] = cancel
	p.mu.Unlock()

	// Every operation this package runs goes through here, so this is where a
	// panic in one of them is stopped. A server being added is a half-hour of
	// SSH, parsing and cluster calls, and a panel that dies partway through it
	// leaves a machine in an unknown state and nobody to ask.
	go func() {
		defer cancel()
		defer runsafe.Recover(p.log, "operation "+op.ID, func(err error) {
			p.finish(op.ID)
			p.failPanicked(ctx, op, err)
		})
		work(ctx)
	}()
}

// failPanicked marks an operation failed after the goroutine running it
// panicked, so the interface shows a failure rather than a step that never
// finishes.
func (p *Provisioner) failPanicked(ctx context.Context, op store.Operation, err error) {
	ctx = context.WithoutCancel(ctx)
	problem := errdoc.OperationPanicked()

	_ = p.db.SetOperationStatus(ctx, op.ID, store.OpFailed, problem.Code, err.Error())
	// The server too: left "provisioning" with nothing running, its page
	// offered neither Retry nor Remove.
	if op.TargetType == "server" && op.TargetID != "" {
		_ = p.db.SetServerStatus(ctx, op.TargetID, store.ServerFailed, problem.Title)
	}
	p.hub.Publish(events.OperationTopic(op.ID), "failed", problem)
}

// finish clears an operation from the running set.
func (p *Provisioner) finish(operationID string) {
	p.mu.Lock()
	delete(p.running, operationID)
	p.mu.Unlock()
}

// Cancel stops a running operation.
func (p *Provisioner) Cancel(ctx context.Context, operationID string) error {
	p.mu.Lock()
	cancel, running := p.running[operationID]
	p.mu.Unlock()

	if !running {
		return errdoc.New("operation.not_running", "That operation is not running").
			WithCause("It has already finished, or the panel restarted since it started.").
			WithImpact("Nothing was changed.").
			WithFix("Reload the page to see its current state.")
	}
	cancel()
	if err := p.db.SetOperationStatus(ctx, operationID, store.OpCancelled, "cancelled", "Cancelled by a user."); err != nil {
		return err
	}
	// Said here as well as by the run: a run stopped between two steps never
	// reaches the code that would say it.
	if op, err := p.db.GetOperation(ctx, operationID); err == nil && op.TargetType == "server" && op.TargetID != "" {
		_ = p.db.SetServerStatus(ctx, op.TargetID, store.ServerFailed, api.ServerDetailCancelled)
	}
	p.publishOperation(ctx, operationID)
	return nil
}

// setStep records a step's state and tells the UI.
func (p *Provisioner) setStep(ctx context.Context, op store.Operation, key string, status store.StepStatus, note store.StepNote, detail string) {
	if err := p.db.SetStepStatus(ctx, op.ID, key, status, note, detail); err != nil {
		p.log.Warn("could not record a step's state", "operation", op.ID, "step", key, "error", err)
	}
	p.publishOperation(ctx, op.ID)
}

// failStep records a failure and stops the operation. An empty serverID leaves
// the server's status as it is, for a failure that changed nothing on it.
//
// The record is written with a context of its own: the run's is the one that
// was cancelled or ran out of time, and writes made with it were refused, so
// a server stopped by "Stop adding" or a timeout stayed "provisioning" for good.
func (p *Provisioner) failStep(ctx context.Context, op store.Operation, serverID, key string, err error) {
	ctx = context.WithoutCancel(ctx)
	problem := errdoc.From(err)

	// A cancelled operation is not a failure to report as one.
	if errors.Is(err, context.Canceled) {
		p.setStep(ctx, op, key, store.StepFailed, store.StepNote{Message: "Cancelled", Key: "cancelled"}, "")
		_ = p.db.SetOperationStatus(ctx, op.ID, store.OpCancelled, "cancelled", "Cancelled by a user.")
		if serverID != "" {
			_ = p.db.SetServerStatus(ctx, serverID, store.ServerFailed, api.ServerDetailCancelled)
		}
		p.publishOperation(ctx, op.ID)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		problem = errdoc.New("operation.timeout", "This took too long and was stopped").
			WithCause("The step %q did not finish within %s.", key, maxOperationDuration).
			WithImpact("The server may be half configured. Retrying is safe: every step can be run again.").
			WithFix("Press Retry. If it times out again, check that the server has working outbound internet access.").
			Retry()
	}

	p.log.Error("provisioning step failed",
		"operation", op.ID, "step", key, "code", problem.Code, "error", err)

	// A failed step's line is the problem's own title, which the error
	// catalogue already translates. "problem:" tells the panel to look there
	// rather than under the step messages.
	p.setStep(ctx, op, key, store.StepFailed,
		store.StepNote{Message: problem.Title, Key: "problem:" + problem.Code, Args: problem.Args.Title},
		problem.Text())
	_ = p.db.SetOperationStatus(ctx, op.ID, store.OpFailed, problem.Code, problem.Error())
	if serverID != "" {
		_ = p.db.SetServerStatus(ctx, serverID, store.ServerFailed, problem.Title)
	}

	// The full problem goes out on the event stream, so the UI can show the
	// cause, the impact, the fix and the "copy for AI" block without another
	// request.
	p.hub.Publish(events.OperationTopic(op.ID), "failed", problem)
	p.publishOperation(ctx, op.ID)
}

// failOperation records a failure that is not tied to a particular step.
//
// The code recorded is the problem's own, not a fixed one: the server's status
// line is the problem's title, and the panel finds that title's translation by
// this code.
func (p *Provisioner) failOperation(ctx context.Context, op store.Operation, serverID string, err error) {
	ctx = context.WithoutCancel(ctx) // as failStep: the run's own may be spent
	problem := errdoc.From(err)
	_ = p.db.SetOperationStatus(ctx, op.ID, store.OpFailed, problem.Code, problem.Error())
	if serverID != "" {
		_ = p.db.SetServerStatus(ctx, serverID, store.ServerFailed, problem.Title)
	}
	p.hub.Publish(events.OperationTopic(op.ID), "failed", problem)
	p.publishOperation(ctx, op.ID)
}

// publishOperation sends the operation's current state to anyone watching.
func (p *Provisioner) publishOperation(ctx context.Context, operationID string) {
	op, err := p.db.GetOperation(ctx, operationID)
	if err != nil {
		return
	}
	p.hub.Publish(events.OperationTopic(operationID), "operation", op)
	if op.TeamID != "" {
		p.hub.Publish(events.TeamTopic(op.TeamID), "operation", op)
	}
}

// completedSteps returns the steps that already succeeded, so a retry skips them.
func (p *Provisioner) completedSteps(ctx context.Context, operationID string) (map[string]bool, error) {
	steps, err := p.db.ListOperationSteps(ctx, operationID)
	if err != nil {
		return nil, fmt.Errorf("read the operation's steps: %w", err)
	}
	done := map[string]bool{}
	for _, step := range steps {
		if step.Status == store.StepSucceeded {
			done[step.Key] = true
		}
	}
	return done, nil
}

// ResumeInterrupted marks operations left running by a panel restart as failed,
// with an explanation, rather than leaving them apparently in progress forever.
//
// They are not resumed automatically: an operation that was halfway through
// installing k3s when the panel died should be looked at, and every step is
// safe to retry from the UI.
func (p *Provisioner) ResumeInterrupted(ctx context.Context) error {
	operations, err := p.db.ListRunningOperations(ctx)
	if err != nil {
		return err
	}
	for _, op := range operations {
		p.log.Info("marking an interrupted operation as failed", "operation", op.ID, "kind", op.Kind)
		problem := errdoc.New("operation.interrupted", "This was interrupted when the panel restarted").
			WithCause("The panel stopped while this operation was running.").
			WithImpact("The server may be half configured.").
			WithFix("Press Retry. Every step is safe to run again, and the ones that already finished are skipped.").
			Retry()
		if err := p.db.SetOperationStatus(ctx, op.ID, store.OpFailed, problem.Code, problem.Error()); err != nil {
			return err
		}
		for _, step := range op.Steps {
			if step.Status == store.StepRunning {
				_ = p.db.SetStepStatus(ctx, op.ID, step.Key, store.StepFailed,
					store.StepNote{Message: "Interrupted by a panel restart", Key: "interrupted"}, "")
			}
		}
		if op.TargetType == "server" {
			_ = p.db.SetServerStatus(ctx, op.TargetID, store.ServerFailed, problem.Title)
		}
	}
	return nil
}
