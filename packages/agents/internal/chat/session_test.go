package chat

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"homelab/agents/internal/session"
)

const idle = 10 * time.Minute

// sessionWorld is a ChatSession in Temporal's test environment, with the
// activities mocked. It records what the activities were asked to do.
type sessionWorld struct {
	testsuite.WorkflowTestSuite
	t   *testing.T
	env *testsuite.TestWorkflowEnvironment

	// created: what each EnsureSandbox call reports; true once the list
	// runs out.
	created []bool
	ensures int
	// ensureErr, if set, is what every EnsureSandbox call returns.
	ensureErr error
	sandbox   int
	turns     []session.TurnInput
	turnAt    []time.Time
	turnErrs  []error
	stops     []string
	// stoppedAt is when each stop started; a stop takes stopTakes.
	stoppedAt []time.Time
	stopTakes time.Duration
	acquires  int
	archived  []string
	archErr   error
	replies   map[string]string
	errs      map[string]error
}

func newSessionWorld(t *testing.T) *sessionWorld {
	w := &sessionWorld{t: t, replies: map[string]string{}, errs: map[string]error{}, stopTakes: 30 * time.Second}
	w.env = w.NewTestWorkflowEnvironment()
	w.env.RegisterActivity(&Activities{})
	w.env.OnActivity("EnsureSandbox", mock.Anything, mock.Anything).Return(
		func(_ context.Context, in session.EnsureInput) (session.EnsureResult, error) {
			w.ensures++
			if w.ensureErr != nil {
				return session.EnsureResult{}, w.ensureErr
			}
			created := w.ensures == 1
			if len(w.created) > 0 {
				created, w.created = w.created[0], w.created[1:]
			}
			if created {
				w.sandbox++
			}
			return session.EnsureResult{Sandbox: fmt.Sprintf("sb-%d", w.sandbox), Created: created, IdleTimeoutSeconds: int(idle / time.Second)}, nil
		})
	w.env.OnActivity("RunTurn", mock.Anything, mock.Anything).After(time.Minute).Return(
		func(_ context.Context, in session.TurnInput) (session.TurnResult, error) {
			w.turns = append(w.turns, in)
			w.turnAt = append(w.turnAt, w.env.Now())
			if len(w.turnErrs) > 0 {
				err := w.turnErrs[0]
				w.turnErrs = w.turnErrs[1:]
				if err != nil {
					return session.TurnResult{}, err
				}
			}
			return session.TurnResult{Reply: "re: " + in.Message}, nil
		})
	w.env.OnActivity("ArchiveSession", mock.Anything, mock.Anything).Return(
		func(_ context.Context, in session.SandboxRef) error {
			w.archived = append(w.archived, in.Sandbox)
			return w.archErr
		})
	w.env.OnActivity("StopSandbox", mock.Anything, mock.Anything).AfterFn(func() time.Duration { return w.stopTakes }).Return(
		func(_ context.Context, in session.SandboxRef) error {
			w.stops = append(w.stops, in.Sandbox)
			w.stoppedAt = append(w.stoppedAt, w.env.Now())
			return nil
		})
	w.env.OnActivity("AcquireSlot", mock.Anything, mock.Anything).Return(
		func(context.Context, SlotRequest) error { w.acquires++; return nil })
	w.env.OnSignalExternalWorkflow(mock.Anything, CoordinatorID, "", mock.Anything, mock.Anything).Return(nil)
	return w
}

// send sends a message as Update id after delay.
func (w *sessionWorld) send(delay time.Duration, id, text string) {
	w.env.RegisterDelayedCallback(func() {
		w.env.UpdateWorkflow(MessageUpdate, id, &testsuite.TestUpdateCallback{
			OnAccept: func() {},
			OnReject: func(err error) { w.errs[id] = err },
			OnComplete: func(res any, err error) {
				if err != nil {
					w.errs[id] = err
					return
				}
				w.replies[id] = res.(Reply).Text
			},
		}, Message{Text: text})
	}, delay)
}

func (w *sessionWorld) run(carry *Carry) {
	w.env.ExecuteWorkflow(ChatSession, ChatInput{Type: "pi", Conversation: "c1", Carry: carry})
	if !w.env.IsWorkflowCompleted() {
		w.t.Fatal("workflow did not complete")
	}
}

func TestConcurrentMessagesRunOneAtATime(t *testing.T) {
	w := newSessionWorld(t)
	w.send(0, "u1", "one")
	w.send(0, "u2", "two")
	w.send(0, "u3", "three")
	w.run(nil)
	if err := w.env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	for i, want := range []string{"one", "two", "three"} {
		if w.turns[i].Message != want || w.turns[i].Turn != fmt.Sprintf("u%d", i+1) {
			t.Errorf("turn %d: %+v", i, w.turns[i])
		}
		if i > 0 && w.turnAt[i].Sub(w.turnAt[i-1]) < time.Minute {
			t.Errorf("turn %d started %v after the one before, which takes a minute", i, w.turnAt[i].Sub(w.turnAt[i-1]))
		}
	}
	if w.replies["u2"] != "re: two" || len(w.errs) != 0 {
		t.Errorf("replies %v, errors %v", w.replies, w.errs)
	}
	// Primed only on the sandbox's first turn.
	if !w.turns[0].Prime || w.turns[1].Prime || w.turns[2].Prime {
		t.Errorf("prime: %v %v %v", w.turns[0].Prime, w.turns[1].Prime, w.turns[2].Prime)
	}
	// Idle after the last turn: stopped once, archived after each turn.
	if len(w.stops) != 1 || w.stops[0] != "sb-1" || len(w.archived) != 3 {
		t.Errorf("stops %v, archived %v", w.stops, w.archived)
	}
}

func TestIdleTimeoutResetByEachMessage(t *testing.T) {
	w := newSessionWorld(t)
	w.send(0, "u1", "one")
	w.send(8*time.Minute, "u2", "two")
	w.stopTakes = 0
	start := w.env.Now()
	w.run(nil)
	// u2's turn ends at 9 minutes; the stop comes an idle timeout later.
	if got := w.stoppedAt[0].Sub(start); got != 9*time.Minute+idle {
		t.Errorf("stopped after %v", got)
	}
	if len(w.stops) != 1 {
		t.Errorf("stops %v", w.stops)
	}
}

func TestMessageDuringStopIsServed(t *testing.T) {
	w := newSessionWorld(t)
	w.send(0, "u1", "one")
	// The turn ends at 1m, the idle stop starts at 1m+idle and takes 30s.
	w.send(time.Minute+idle+10*time.Second, "u2", "two")
	w.run(nil)
	if err := w.env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if w.replies["u2"] != "re: two" {
		t.Errorf("replies %v, errors %v", w.replies, w.errs)
	}
	// Stopped, started again (not recreated, so not primed) for u2, and
	// stopped again when idle.
	if len(w.stops) != 2 || w.ensures != 2 || w.turns[1].Prime {
		t.Errorf("stops %v, ensures %d, turns %+v", w.stops, w.ensures, w.turns)
	}
}

func TestLostSandboxRetriedOnceInAFreshOne(t *testing.T) {
	w := newSessionWorld(t)
	lost := temporal.NewNonRetryableApplicationError("gone", ErrTypeSandboxLost, nil)
	w.turnErrs = []error{nil, lost}
	w.created = []bool{true, false, true}
	w.send(0, "u1", "one")
	w.send(2*time.Minute, "u2", "two")
	w.run(nil)
	if w.replies["u2"] != "re: two" {
		t.Fatalf("replies %v, errors %v", w.replies, w.errs)
	}
	// u2 ran twice under the same turn ID, the second time primed, in the
	// recreated sandbox.
	if len(w.turns) != 3 || w.turns[2].Turn != "u2" || w.turns[2].Sandbox != "sb-2" || !w.turns[2].Prime || w.turns[1].Prime {
		t.Errorf("turns %+v", w.turns)
	}
}

func TestLostTwiceFailsTheMessageOnly(t *testing.T) {
	w := newSessionWorld(t)
	lost := temporal.NewNonRetryableApplicationError("gone", ErrTypeSandboxLost, nil)
	w.turnErrs = []error{lost, lost}
	w.send(0, "u1", "one")
	w.send(5*time.Minute, "u2", "two")
	w.run(nil)
	if err := w.env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if w.errs["u1"] == nil || w.replies["u2"] != "re: two" {
		t.Errorf("replies %v, errors %v", w.replies, w.errs)
	}
	// The first sandbox never completed a turn, so u2 is still primed in it.
	if !w.turns[2].Prime {
		t.Errorf("turns %+v", w.turns)
	}
}

func TestArchiveFailureDoesNotFailTheTurn(t *testing.T) {
	w := newSessionWorld(t)
	w.archErr = errors.New("rgw down")
	w.send(0, "u1", "one")
	w.run(nil)
	if err := w.env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if w.replies["u1"] != "re: one" || len(w.stops) != 1 {
		t.Errorf("replies %v, errors %v, stops %v", w.replies, w.errs, w.stops)
	}
}

func TestInvalidMessageRejected(t *testing.T) {
	w := newSessionWorld(t)
	w.send(0, "u1", "")
	w.run(nil)
	if w.errs["u1"] == nil || len(w.turns) != 0 {
		t.Errorf("errors %v, turns %v", w.errs, w.turns)
	}
}

func TestInvalidFirstTurnEndsTheSession(t *testing.T) {
	w := newSessionWorld(t)
	w.ensureErr = temporal.NewNonRetryableApplicationError("unknown type", ErrTypeInvalid, nil)
	w.send(0, "u1", "one")
	start := w.env.Now()
	w.run(nil)
	if err := w.env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	// Refused, and over without waiting out the idle timeout.
	if w.errs["u1"] == nil || len(w.turns) != 0 || len(w.stops) != 0 || w.env.Now().Sub(start) >= idle {
		t.Errorf("errors %v, turns %v, stops %v, ended after %v", w.errs, w.turns, w.stops, w.env.Now().Sub(start))
	}
}

func TestEvictStopsEarly(t *testing.T) {
	w := newSessionWorld(t)
	w.send(0, "u1", "one")
	w.env.RegisterDelayedCallback(func() { w.env.SignalWorkflow(EvictSignal, nil) }, 2*time.Minute)
	w.stopTakes = 0
	start := w.env.Now()
	w.run(nil)
	if got := w.stoppedAt[0].Sub(start); got != 2*time.Minute || len(w.stops) != 1 {
		t.Errorf("stopped after %v, stops %v", got, w.stops)
	}
}

func TestCancelStopsTheSandbox(t *testing.T) {
	w := newSessionWorld(t)
	w.send(0, "u1", "one")
	w.env.RegisterDelayedCallback(w.env.CancelWorkflow, 3*time.Minute)
	w.run(nil)
	if !temporal.IsCanceledError(w.env.GetWorkflowError()) {
		t.Errorf("workflow error %v", w.env.GetWorkflowError())
	}
	if len(w.stops) != 1 || w.stops[0] != "sb-1" {
		t.Errorf("stops %v", w.stops)
	}
}

func TestContinueAsNewCarriesState(t *testing.T) {
	w := newSessionWorld(t)
	w.env.SetContinueAsNewSuggested(true)
	w.send(0, "u1", "one")
	w.run(nil)
	var can *workflow.ContinueAsNewError
	if !errors.As(w.env.GetWorkflowError(), &can) {
		t.Fatalf("workflow error %v", w.env.GetWorkflowError())
	}
	var next ChatInput
	if err := converter.GetDefaultDataConverter().FromPayloads(can.Input, &next); err != nil {
		t.Fatal(err)
	}
	c := next.Carry
	if next.Conversation != "c1" || c == nil || c.Sandbox != "sb-1" || c.PrimeSandbox != "" || !c.HasSlot ||
		c.IdleTimeoutSeconds != int(idle/time.Second) || c.LastActivity.IsZero() {
		t.Errorf("carried %+v %+v", next, c)
	}
	if w.replies["u1"] != "re: one" || len(w.stops) != 0 {
		t.Errorf("replies %v, stops %v", w.replies, w.stops)
	}
}

func TestCarriedStateIsUsed(t *testing.T) {
	w := newSessionWorld(t)
	w.created = []bool{false}
	w.sandbox = 1
	w.send(0, "u1", "one")
	w.run(&Carry{Sandbox: "sb-1", PrimeSandbox: "sb-1", LastActivity: w.env.Now(), IdleTimeoutSeconds: 600, HasSlot: true})
	// The sandbox had not completed a turn before continue-as-new.
	if !w.turns[0].Prime {
		t.Errorf("turns %+v", w.turns)
	}
	if w.acquires != 0 {
		t.Errorf("acquired a slot it holds")
	}
}
