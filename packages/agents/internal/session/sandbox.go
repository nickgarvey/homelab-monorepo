package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"homelab/agents/internal/desired"
	"homelab/agents/internal/gateway"
	"homelab/agents/internal/store"
)

type EnsureInput struct {
	Conversation
}

type EnsureResult struct {
	Sandbox string
	// Created is true when the sandbox is new, so the next turn is primed.
	Created bool
	// IdleTimeoutSeconds is the type's session.idleTimeoutSeconds, after
	// which the workflow stops the sandbox.
	IdleTimeoutSeconds int
}

// EnsureSandbox makes the conversation's sandbox ready for a turn, deciding
// from its session row:
//   - running, same type version, and Ready on the gateway: nothing to do;
//   - stopped (or running but found stopped), same type version: start it;
//     pi resumes its own session, so the turn is not primed;
//   - anything else (no row, a different type version, a sandbox that is
//     gone or in phase Error, a failed start): delete the old sandbox,
//     record a new generation as creating, create it; the turn is primed.
//
// A row left in creating or starting by a failed attempt is carried on with.
func (a *Activities) EnsureSandbox(ctx context.Context, in EnsureInput) (EnsureResult, error) {
	if err := in.validate(); err != nil {
		return EnsureResult{}, err
	}
	t, err := a.agentType(in.Type)
	if err != nil {
		return EnsureResult{}, err
	}
	res, err := a.ensure(ctx, in, t)
	res.IdleTimeoutSeconds = t.Session.IdleTimeoutSeconds
	return res, err
}

func (a *Activities) ensure(ctx context.Context, in EnsureInput, t desired.Type) (EnsureResult, error) {
	row, err := a.Store.Session(ctx, in.Type, in.Conversation.Conversation)
	if err != nil {
		return EnsureResult{}, err
	}
	if row != nil && row.TypeVersion == t.Version {
		switch row.State {
		case store.Running, store.Stopped, store.Starting:
			ok, err := a.resume(ctx, row)
			if err != nil {
				return EnsureResult{}, err
			}
			if ok {
				return EnsureResult{Sandbox: row.Sandbox}, nil
			}
		case store.Creating:
			return a.create(ctx, in.Conversation, row)
		}
	}
	if err := a.retire(ctx, row); err != nil {
		return EnsureResult{}, err
	}
	row, err = a.Store.NewGeneration(ctx, in.Type, in.Conversation.Conversation, t.Version)
	if err != nil {
		return EnsureResult{}, err
	}
	return a.create(ctx, in.Conversation, row)
}

// resume makes the row's existing sandbox ready, starting it if it is
// stopped. It returns false when the sandbox cannot be used (gone, in phase
// Error, or the row was claimed by the reaper), so a new one is needed.
func (a *Activities) resume(ctx context.Context, row *store.Session) (bool, error) {
	sb, err := a.Gateway.Get(ctx, row.Sandbox)
	if errors.Is(err, gateway.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch sb.Phase {
	case gateway.PhaseError, gateway.PhaseDeleting:
		return false, nil
	case gateway.PhaseReady:
		if row.State != store.Running {
			// Started by an earlier attempt that failed before recording it.
			if ok, err := a.Store.Transition(ctx, row.Type, row.Conversation, row.Sandbox,
				[]store.State{store.Stopped, store.Starting}, store.Running); err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}

	// Claim the row first: the reaper deletes only rows still stopped.
	ok, err := a.Store.Transition(ctx, row.Type, row.Conversation, row.Sandbox,
		[]store.State{store.Running, store.Stopped, store.Starting}, store.Starting)
	if err != nil || !ok {
		return false, err
	}
	if sb.Phase == gateway.PhaseStopped || sb.Phase == gateway.PhaseStopping {
		if err := a.Gateway.Start(ctx, row.Sandbox); errors.Is(err, gateway.ErrNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
	}
	if err := a.waitUsable(ctx, row.Sandbox); errors.Is(err, gateway.ErrNotFound) || errors.Is(err, gateway.ErrLost) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return a.Store.Transition(ctx, row.Type, row.Conversation, row.Sandbox,
		[]store.State{store.Starting}, store.Running)
}

// create creates the row's sandbox (it may already exist, from an earlier
// attempt) and waits until it takes commands.
func (a *Activities) create(ctx context.Context, c Conversation, row *store.Session) (EnsureResult, error) {
	t, err := a.agentType(c.Type)
	if err != nil {
		return EnsureResult{}, err
	}
	err = a.Gateway.Create(ctx, row.Sandbox, gateway.Spec{
		Image:     t.Image.Ref,
		Command:   t.Image.Cmd,
		Policy:    t.Policy.Spec,
		Providers: t.Providers,
		Env:       t.Env,
		CPU:       t.Resources.CPU,
		Memory:    t.Resources.Memory,
		Labels: map[string]string{
			ManagedByLabel:    ManagedBy,
			ConversationLabel: store.ConversationHash(c.Type, c.Conversation),
		},
	})
	if err != nil {
		return EnsureResult{}, fmt.Errorf("creating %s: %w", row.Sandbox, err)
	}
	if err := a.waitUsable(ctx, row.Sandbox); err != nil {
		// A sandbox that fails as it starts is deleted and fails the
		// activity; the retry finds the row deleted and makes another.
		if errors.Is(err, gateway.ErrLost) {
			if rerr := a.retire(ctx, row); rerr != nil {
				return EnsureResult{}, rerr
			}
		}
		return EnsureResult{}, fmt.Errorf("waiting for %s: %w", row.Sandbox, err)
	}
	ok, err := a.Store.Transition(ctx, row.Type, row.Conversation, row.Sandbox,
		[]store.State{store.Creating}, store.Running)
	if err != nil {
		return EnsureResult{}, err
	}
	if !ok {
		return EnsureResult{}, fmt.Errorf("session row for %s changed while it was created", row.Sandbox)
	}
	return EnsureResult{Sandbox: row.Sandbox, Created: true}, nil
}

// waitUsable waits for the sandbox to be Ready and then to run a command:
// exec can be refused for a moment after the phase turns Ready.
func (a *Activities) waitUsable(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, a.readyTimeout())
	defer cancel()
	if err := a.Gateway.WaitReady(ctx, name); err != nil {
		return err
	}
	for {
		res, err := a.Gateway.Exec(ctx, name, []string{"true"})
		if err == nil && res.ExitCode == 0 {
			return nil
		}
		if errors.Is(err, gateway.ErrNotFound) || errors.Is(err, gateway.ErrLost) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s is Ready but exec fails (%v): %w", name, err, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// retire deletes the row's sandbox, if it has one that is not deleted yet,
// and marks the row deleted. The row is moved to deleting first, so a crash
// in between leaves it for the reaper's stuck-deletion sweep.
func (a *Activities) retire(ctx context.Context, row *store.Session) error {
	if row == nil || row.State == store.Deleted {
		return nil
	}
	ok, err := a.Store.Transition(ctx, row.Type, row.Conversation, row.Sandbox,
		[]store.State{row.State}, store.Deleting)
	if err != nil {
		return err
	}
	if !ok {
		// Moved under us (the reaper): read it again and go from there.
		fresh, err := a.Store.Session(ctx, row.Type, row.Conversation)
		if err != nil {
			return err
		}
		if fresh == nil || fresh.Sandbox != row.Sandbox || fresh.State == store.Deleted {
			return nil
		}
		if fresh.State != store.Deleting {
			return a.retire(ctx, fresh)
		}
	}
	return a.DeleteSandbox(ctx, SandboxRef{Conversation: Conversation{row.Type, row.Conversation}, Sandbox: row.Sandbox})
}

// SandboxRef names one sandbox of a conversation.
type SandboxRef struct {
	Conversation
	Sandbox string
}

// DeleteSandbox deletes the sandbox, and so its PVC, and marks its row
// deleted if the row is in deleting and still names it. Callers move the row
// to deleting first; an orphan has no row.
func (a *Activities) DeleteSandbox(ctx context.Context, in SandboxRef) error {
	if err := a.Gateway.Delete(ctx, in.Sandbox); err != nil {
		return fmt.Errorf("deleting %s: %w", in.Sandbox, err)
	}
	_, err := a.Store.Transition(ctx, in.Type, in.Conversation.Conversation, in.Sandbox,
		[]store.State{store.Deleting}, store.Deleted)
	return err
}

// StopSandbox marks the row stopped and stops the sandbox, keeping its PVC.
// A row that has moved on (deleting, deleted, a newer generation) is left
// alone.
func (a *Activities) StopSandbox(ctx context.Context, in SandboxRef) error {
	ok, err := a.Store.Transition(ctx, in.Type, in.Conversation.Conversation, in.Sandbox,
		[]store.State{store.Running, store.Starting, store.Creating}, store.Stopped)
	if err != nil {
		return err
	}
	if !ok {
		row, err := a.Store.Session(ctx, in.Type, in.Conversation.Conversation)
		if err != nil {
			return err
		}
		if row == nil || row.Sandbox != in.Sandbox || row.State != store.Stopped {
			return nil
		}
	}
	if err := a.Gateway.Stop(ctx, in.Sandbox); err != nil {
		return fmt.Errorf("stopping %s: %w", in.Sandbox, err)
	}
	return nil
}
