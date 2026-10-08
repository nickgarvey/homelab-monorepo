// Package chat holds the worker's Temporal workflows (design section 7):
// ChatSession, one per conversation; SessionCoordinator, which keeps the
// number of running session sandboxes under the cap; and SandboxReaper, run
// hourly by the sandbox-reaper Schedule. The activities are the session
// package's, registered through Activities here, which turns their errors
// into ones the workflows can act on.
package chat

import (
	"context"
	"errors"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"homelab/agents/internal/session"
)

// TaskQueue is the queue the worker polls, for both workflows and every
// activity.
const TaskQueue = "agents"

// Names callers and the workflows use.
const (
	// MessageUpdate sends a ChatSession a Message; it returns the Reply.
	MessageUpdate = "message"
	// EvictSignal asks a ChatSession to stop its sandbox early: the session
	// cap was reached and it is the longest idle.
	EvictSignal = "evict"

	CoordinatorID = "session-coordinator"
	// AcquireUpdate gives a ChatSession a slot under the cap.
	AcquireUpdate = "acquire"
	// TouchSignal and ReleaseSignal report a slot holder's activity and its
	// stop.
	TouchSignal   = "touch"
	ReleaseSignal = "release"
	// HoldersQuery lists the coordinator's slot holders, by ID.
	HoldersQuery = "holders"
)

// Types of the activity errors the workflows act on.
const (
	// ErrTypeInvalid: the request can never succeed; not retried.
	ErrTypeInvalid = "Invalid"
	// ErrTypeSandboxLost: the sandbox went away under a turn. Not retried by
	// Temporal: the workflow ensures a sandbox again and retries the turn.
	ErrTypeSandboxLost = "SandboxLost"
)

// WorkflowID is the ChatSession of a conversation.
func WorkflowID(typ, conversation string) string {
	return "chat/" + typ + "/" + conversation
}

// Activities are what the worker registers. Session does the work; Client
// reaches the coordinator for AcquireSlot.
type Activities struct {
	Session *session.Activities
	Client  client.Client
	// Cap is the most session sandboxes running at once.
	Cap int
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, session.ErrInvalid):
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeInvalid, nil)
	case errors.Is(err, session.ErrSandboxLost):
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeSandboxLost, nil)
	}
	return err
}

func (a *Activities) EnsureSandbox(ctx context.Context, in session.EnsureInput) (session.EnsureResult, error) {
	res, err := a.Session.EnsureSandbox(ctx, in)
	return res, classify(err)
}

func (a *Activities) RunTurn(ctx context.Context, in session.TurnInput) (session.TurnResult, error) {
	res, err := a.Session.RunTurn(ctx, in)
	return res, classify(err)
}

func (a *Activities) ArchiveSession(ctx context.Context, in session.SandboxRef) error {
	return classify(a.Session.ArchiveSession(ctx, in))
}

func (a *Activities) StopSandbox(ctx context.Context, in session.SandboxRef) error {
	return classify(a.Session.StopSandbox(ctx, in))
}

// SlotRequest asks the coordinator for a slot, or reports activity on one.
type SlotRequest struct {
	// Holder is the ChatSession's workflow ID.
	Holder string
	// Expires is when the coordinator may drop the slot unless touched
	// again: the holder would have stopped by then had it been running.
	Expires time.Time
	// Cap is the worker's setting, filled in by AcquireSlot.
	Cap int
}

// AcquireSlot asks the coordinator, starting it if need be, for a slot. It
// returns once the slot is granted, which is at once: at the cap, the
// longest-idle holder is told to stop.
func (a *Activities) AcquireSlot(ctx context.Context, req SlotRequest) error {
	req.Cap = a.Cap
	start := a.Client.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
		ID:                       CoordinatorID,
		TaskQueue:                TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, SessionCoordinator, CoordinatorState{})
	h, err := a.Client.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: start,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateName:   AcquireUpdate,
			Args:         []any{req},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	if err != nil {
		return err
	}
	return h.Get(ctx, nil)
}

func (a *Activities) PlanReap(ctx context.Context) (session.ReapPlan, error) {
	plan, err := a.Session.PlanReap(ctx)
	return plan, classify(err)
}

func (a *Activities) ReapExpired(ctx context.Context, in session.ExpiredRef) (bool, error) {
	ok, err := a.Session.ReapExpired(ctx, in)
	return ok, classify(err)
}

func (a *Activities) ReapStuck(ctx context.Context, in session.SandboxRef) (bool, error) {
	ok, err := a.Session.ReapStuck(ctx, in)
	return ok, classify(err)
}

func (a *Activities) ReapOrphan(ctx context.Context, name string) (bool, error) {
	ok, err := a.Session.ReapOrphan(ctx, name)
	return ok, classify(err)
}

func (a *Activities) MarkVanished(ctx context.Context, in session.SandboxRef) (bool, error) {
	ok, err := a.Session.MarkVanished(ctx, in)
	return ok, classify(err)
}
