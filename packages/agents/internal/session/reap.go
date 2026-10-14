package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"homelab/agents/internal/gateway"
	"homelab/agents/internal/store"
)

// DefaultRetention applies to a row whose type is no longer published, or
// publishes none.
const DefaultRetention = 7 * 24 * time.Hour

// ReapPlan is what the reaper found to do.
// Each entry is acted on by its own activity, which checks again before it
// deletes anything, so a plan gone stale is safe.
type ReapPlan struct {
	// Expired: rows stopped longer than their type's retention.
	Expired []ExpiredRef
	// Stuck: rows left in deleting, by a crash between claiming the row and
	// deleting its sandbox.
	Stuck []SandboxRef
	// Orphans: sandboxes labelled managed-by=agents-worker that no live row
	// names.
	Orphans []string
	// Vanished: stopped rows whose sandbox is gone from the gateway.
	Vanished []SandboxRef
}

type ExpiredRef struct {
	SandboxRef
	RetentionSeconds int
}

// PlanReap reads the session rows, the worker's sandboxes on the gateway and
// the published retention of each type.
//
// Only stopped rows can be expired or vanished. A row in creating, starting
// or running belongs to a ChatSession in flight: if its sandbox has gone,
// EnsureSandbox makes another at the next turn, and the idle stop moves the
// row to stopped, where a later run finds it.
func (a *Activities) PlanReap(ctx context.Context) (ReapPlan, error) {
	var plan ReapPlan
	st, err := a.State()
	if err != nil {
		return plan, fmt.Errorf("desired state: %w", err)
	}
	sandboxes, err := a.Gateway.List(ctx, ManagedByLabel+"="+ManagedBy)
	if err != nil {
		return plan, fmt.Errorf("listing sandboxes: %w", err)
	}
	onGateway := map[string]bool{}
	for _, sb := range sandboxes {
		onGateway[sb.Name] = true
	}
	rows, err := a.Store.Sessions(ctx, store.Creating, store.Running, store.Stopped, store.Starting, store.Deleting)
	if err != nil {
		return plan, err
	}
	held := map[string]bool{}
	now := time.Now()
	for _, row := range rows {
		held[row.Sandbox] = true
		ref := SandboxRef{Conversation: Conversation{row.Type, row.Conversation}, Sandbox: row.Sandbox}
		switch row.State {
		case store.Deleting:
			plan.Stuck = append(plan.Stuck, ref)
		case store.Stopped:
			retention := DefaultRetention
			if t, ok := st.Types[row.Type]; ok && t.Session.RetentionSeconds > 0 {
				retention = time.Duration(t.Session.RetentionSeconds) * time.Second
			}
			switch {
			case !onGateway[row.Sandbox]:
				plan.Vanished = append(plan.Vanished, ref)
			case row.StoppedAt != nil && now.Sub(*row.StoppedAt) >= retention:
				plan.Expired = append(plan.Expired, ExpiredRef{SandboxRef: ref, RetentionSeconds: int(retention / time.Second)})
			}
		}
	}
	for _, sb := range sandboxes {
		if !held[sb.Name] && sb.Phase != gateway.PhaseDeleting {
			plan.Orphans = append(plan.Orphans, sb.Name)
		}
	}
	return plan, nil
}

// ReapExpired claims the row (stopped → deleting, only if it is still
// stopped and has been for the retention) and deletes its sandbox, and so
// its PVC. It reports false, deleting nothing, when a message got to the row
// first.
func (a *Activities) ReapExpired(ctx context.Context, in ExpiredRef) (bool, error) {
	ok, err := a.Store.ClaimExpired(ctx, in.Type, in.Conversation.Conversation, in.Sandbox,
		time.Duration(in.RetentionSeconds)*time.Second)
	if err != nil || !ok {
		return false, err
	}
	return true, a.DeleteSandbox(ctx, in.SandboxRef)
}

// ReapStuck deletes the sandbox of a row still in deleting, and marks it
// deleted. It reports false when the row has moved on: EnsureSandbox
// finished the deletion itself.
func (a *Activities) ReapStuck(ctx context.Context, in SandboxRef) (bool, error) {
	row, err := a.Store.Session(ctx, in.Type, in.Conversation.Conversation)
	if err != nil {
		return false, err
	}
	if row == nil || row.Sandbox != in.Sandbox || row.State != store.Deleting {
		return false, nil
	}
	return true, a.DeleteSandbox(ctx, in)
}

// ReapOrphan deletes a sandbox that no live row names. Names are recorded
// before a sandbox is created, so a create in progress is never taken for
// an orphan; the name is checked again here in case a row has come to hold
// it since the plan.
func (a *Activities) ReapOrphan(ctx context.Context, name string) (bool, error) {
	held, err := a.Store.HoldsSandbox(ctx, name)
	if err != nil || held {
		return false, err
	}
	if err := a.Gateway.Delete(ctx, name); err != nil {
		return false, fmt.Errorf("deleting %s: %w", name, err)
	}
	return true, nil
}

// MarkVanished marks a stopped row deleted when its sandbox is gone from the
// gateway (deleted by hand), so the next message creates a fresh one rather
// than trying to start it.
func (a *Activities) MarkVanished(ctx context.Context, in SandboxRef) (bool, error) {
	_, err := a.Gateway.Get(ctx, in.Sandbox)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, gateway.ErrNotFound) {
		return false, err
	}
	return a.Store.Transition(ctx, in.Type, in.Conversation.Conversation, in.Sandbox,
		[]store.State{store.Stopped}, store.Deleted)
}
