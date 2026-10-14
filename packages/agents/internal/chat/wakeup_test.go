package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"homelab/agents/internal/session"
)

const day = 24 * time.Hour

var wakeupStart = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

// wakeupWorld is one run of a Wakeup in Temporal's test environment, with
// RunWakeupTurn mocked: a turn takes turnTakes and replies "ok", or returns
// turnErr.
type wakeupWorld struct {
	testsuite.WorkflowTestSuite
	t   *testing.T
	env *testsuite.TestWorkflowEnvironment

	turnTakes time.Duration
	turnErr   error
	disabled  bool
	reply     string
	// delivers records each DeliverActivity; it takes deliverTakes and
	// returns deliverErr.
	delivers     []DeliverInput
	deliverTakes time.Duration
	deliverErr   error
	// turns and turnAt record each RunWakeupTurn, as it returns.
	turns  []WakeupTurnInput
	turnAt []time.Time
}

func newWakeupWorld(t *testing.T, start time.Time) *wakeupWorld {
	w := &wakeupWorld{t: t, turnTakes: time.Minute, reply: "ok", deliverTakes: time.Second}
	w.env = w.NewTestWorkflowEnvironment()
	w.env.SetStartTime(start)
	w.env.RegisterActivity(&Activities{})
	w.env.RegisterActivityWithOptions(func(context.Context, DeliverInput) (DeliverResult, error) {
		return DeliverResult{}, nil
	}, activity.RegisterOptions{Name: DeliverActivity})
	w.env.OnActivity(DeliverActivity, mock.Anything, mock.Anything).AfterFn(func() time.Duration { return w.deliverTakes }).Return(
		func(_ context.Context, in DeliverInput) (DeliverResult, error) {
			w.delivers = append(w.delivers, in)
			if w.deliverErr != nil {
				return DeliverResult{}, w.deliverErr
			}
			return DeliverResult{Delivered: true}, nil
		})
	w.env.OnActivity("RunWakeupTurn", mock.Anything, mock.Anything).AfterFn(func() time.Duration { return w.turnTakes }).Return(
		func(_ context.Context, in WakeupTurnInput) (WakeupTurnResult, error) {
			w.turns = append(w.turns, in)
			w.turnAt = append(w.turnAt, w.env.Now())
			if w.turnErr != nil {
				return WakeupTurnResult{}, w.turnErr
			}
			return WakeupTurnResult{Reply: w.reply, Disabled: w.disabled, DefaultSeconds: int(day / time.Second)}, nil
		})
	return w
}

// at runs f at start+d.
func (w *wakeupWorld) at(d time.Duration, f func()) {
	w.env.RegisterDelayedCallback(f, d)
}

func (w *wakeupWorld) report(d time.Duration, r TurnReport) {
	w.at(d, func() { w.env.SignalWorkflow(TurnSignal, r) })
}

func (w *wakeupWorld) status() session.WakeupStatus {
	v, err := w.env.QueryWorkflow(PendingQuery)
	if err != nil {
		w.t.Fatal(err)
	}
	var st session.WakeupStatus
	if err := v.Get(&st); err != nil {
		w.t.Fatal(err)
	}
	return st
}

// run runs the Wakeup until it continues as new, and returns the input of
// its next run.
func (w *wakeupWorld) run(in WakeupInput) WakeupInput {
	w.t.Helper()
	w.env.ExecuteWorkflow(Wakeup, in)
	if !w.env.IsWorkflowCompleted() {
		w.t.Fatal("workflow did not complete")
	}
	var can *workflow.ContinueAsNewError
	if !errors.As(w.env.GetWorkflowError(), &can) {
		w.t.Fatalf("workflow error %v, want continue-as-new", w.env.GetWorkflowError())
	}
	var next WakeupInput
	if err := converter.GetDefaultDataConverter().FromPayloads(can.Input, &next); err != nil {
		w.t.Fatal(err)
	}
	return next
}

func newInput() WakeupInput {
	return WakeupInput{Type: "pi", Conversation: "c1", DefaultSeconds: int(day / time.Second)}
}

func TestSilenceBacksOff(t *testing.T) {
	in := newInput()
	now := wakeupStart
	var gaps []time.Duration
	for range 8 {
		w := newWakeupWorld(t, now)
		w.turnTakes = 0
		in = w.run(in)
		if len(w.turns) != 1 {
			t.Fatalf("turns %+v", w.turns)
		}
		gaps = append(gaps, w.turnAt[0].Sub(now))
		now = w.turnAt[0]
		if in.Pending.Note != session.DefaultNote {
			t.Errorf("pending %+v", in.Pending)
		}
	}
	// The first is a default from the start; then 1d, 1d, 1d, 3d, 7d and 7d
	// from then on.
	want := []time.Duration{day, day, day, day, 3 * day, 7 * day, 7 * day, 7 * day}
	for i := range want {
		if gaps[i] != want[i] {
			t.Errorf("gaps %v, want %v", gaps, want)
			break
		}
	}
	if in.Silent != 8 {
		t.Errorf("silent %d", in.Silent)
	}
}

func TestUserMessageResetsTheBackoff(t *testing.T) {
	for _, tc := range []struct {
		name string
		user bool
		want time.Duration
	}{
		{"user wrote", true, day},
		{"wake-up turn only", false, 7 * day},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := newInput()
			in.Silent = 4
			in.Pending = &session.Wakeup{At: wakeupStart.Add(time.Hour), Note: session.DefaultNote}
			w := newWakeupWorld(t, wakeupStart)
			w.turnTakes = 0
			w.report(30*time.Minute, TurnReport{User: tc.user})
			next := w.run(in)
			// The user's turn leaves the pending wake-up alone.
			if len(w.turnAt) != 1 || !w.turnAt[0].Equal(wakeupStart.Add(time.Hour)) {
				t.Fatalf("turns at %v", w.turnAt)
			}
			if got := next.Pending.At.Sub(w.turnAt[0]); got != tc.want {
				t.Errorf("next floor %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRequestTakesPrecedence(t *testing.T) {
	for _, lead := range []time.Duration{2 * time.Hour, 30 * day} {
		w := newWakeupWorld(t, wakeupStart)
		w.turnTakes = 0
		set := session.Wakeup{At: wakeupStart.Add(lead), Note: "end-of-day check-in"}
		w.report(10*time.Minute, TurnReport{User: true, Set: &set})
		next := w.run(newInput())
		if len(w.turns) != 1 || !w.turnAt[0].Equal(set.At) || w.turns[0].Wakeup != set {
			t.Errorf("lead %v: turns %+v at %v", lead, w.turns, w.turnAt)
		}
		// No request in the wake-up turn: the floor follows.
		if next.Pending.At.Sub(set.At) != day || next.Pending.Note != session.DefaultNote {
			t.Errorf("lead %v: next %+v", lead, next.Pending)
		}
	}
}

func TestRequestDuringAWakeupTurn(t *testing.T) {
	w := newWakeupWorld(t, wakeupStart)
	set := session.Wakeup{At: wakeupStart.Add(day + 5*time.Hour), Note: "later"}
	var during, after session.WakeupStatus
	// The turn runs from 1d to 1d+1m; its own CollectOutbox reports at 1d+30s.
	w.at(day+10*time.Second, func() { during = w.status() })
	w.report(day+30*time.Second, TurnReport{Set: &set})
	w.at(day+40*time.Second, func() { after = w.status() })
	next := w.run(newInput())

	if during.Pending != nil || during.NextFloorSeconds != int(day/time.Second) {
		t.Errorf("status during the turn %+v", during)
	}
	if after.Pending == nil || *after.Pending != set {
		t.Errorf("status after the request %+v", after)
	}
	if next.Pending == nil || *next.Pending != set || next.Silent != 1 {
		t.Errorf("next %+v", next)
	}
}

func TestFailedWakeupIsRetriedInAnHour(t *testing.T) {
	in := newInput()
	in.Silent = 2
	in.Pending = &session.Wakeup{At: wakeupStart.Add(time.Hour), Note: "check-in"}
	w := newWakeupWorld(t, wakeupStart)
	w.turnErr = temporal.NewNonRetryableApplicationError("pi exited 1", ErrTypeTurnFailed, nil)
	next := w.run(in)
	end := w.turnAt[0] // recorded as the mocked turn returns
	if !next.Pending.At.Equal(end.Add(time.Hour)) || next.Pending.Note != "retry: check-in" || next.Silent != 2 {
		t.Errorf("next %+v, pending %+v, turn at %v", next, next.Pending, w.turnAt)
	}

	// Failing again does not stack the prefix.
	w = newWakeupWorld(t, end)
	w.turnErr = temporal.NewNonRetryableApplicationError("pi exited 1", ErrTypeTurnFailed, nil)
	if again := w.run(next); again.Pending.Note != "retry: check-in" {
		t.Errorf("again %+v", again.Pending)
	}
}

func TestRefusedRequestReportedUntilTheNextTurn(t *testing.T) {
	w := newWakeupWorld(t, wakeupStart)
	var first, second session.WakeupStatus
	w.report(time.Minute, TurnReport{User: true, Rejected: `"at" is 5m from now`})
	w.at(2*time.Minute, func() { first = w.status() })
	w.report(3*time.Minute, TurnReport{User: true})
	w.at(4*time.Minute, func() { second = w.status() })
	w.run(newInput())
	if first.Rejected == "" || second.Rejected != "" {
		t.Errorf("first %+v, second %+v", first, second)
	}
	// The default pending wake-up stands throughout.
	if first.Pending == nil || !first.Pending.At.Equal(wakeupStart.Add(day)) {
		t.Errorf("first %+v", first)
	}
}

func TestWakeupEndsWhenDisabled(t *testing.T) {
	w := newWakeupWorld(t, wakeupStart)
	w.disabled = true
	w.env.ExecuteWorkflow(Wakeup, newInput())
	if !w.env.IsWorkflowCompleted() || w.env.GetWorkflowError() != nil {
		t.Errorf("completed %v, error %v", w.env.IsWorkflowCompleted(), w.env.GetWorkflowError())
	}
}

func matrixInput() WakeupInput {
	in := newInput()
	in.Conversation = "matrix-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcde"
	return in
}

func TestWakeupReplyIsPostedToTheRoom(t *testing.T) {
	w := newWakeupWorld(t, wakeupStart)
	w.reply = "Did you walk today?"
	w.run(matrixInput())
	if len(w.delivers) != 1 {
		t.Fatalf("delivers %+v", w.delivers)
	}
	d := w.delivers[0]
	if d.Type != "pi" || d.Conversation != matrixInput().Conversation || d.Reply != "Did you walk today?" ||
		!strings.HasPrefix(d.TxnID, "wakeup-") || len(d.TxnID) <= len("wakeup-") {
		t.Errorf("deliver %+v", d)
	}
}

func TestSilentOrRoomlessWakeupPostsNothing(t *testing.T) {
	for name, c := range map[string]struct {
		in    WakeupInput
		reply string
	}{
		"silent":  {matrixInput(), "<silent>"},
		"empty":   {matrixInput(), "  "},
		"no room": {newInput(), "Did you walk today?"},
	} {
		w := newWakeupWorld(t, wakeupStart)
		w.reply = c.reply
		w.run(c.in)
		if len(w.delivers) != 0 {
			t.Errorf("%s: delivers %+v", name, w.delivers)
		}
	}
}

func TestFailedDeliveryStillSetsTheNext(t *testing.T) {
	w := newWakeupWorld(t, wakeupStart)
	w.deliverErr = temporal.NewNonRetryableApplicationError("synapse down", "Down", nil)
	next := w.run(matrixInput())
	if next.Pending == nil || next.Silent != 1 || len(w.delivers) != 1 {
		t.Errorf("next %+v, delivers %+v", next, w.delivers)
	}
}

func TestRequestDuringDeliveryIsKept(t *testing.T) {
	w := newWakeupWorld(t, wakeupStart)
	w.deliverTakes = time.Hour
	set := &session.Wakeup{At: wakeupStart.Add(3 * day), Note: "later"}
	// The default wake-up is at day; its turn takes a minute, the delivery an hour.
	w.report(day+30*time.Minute, TurnReport{User: true, Set: set})
	next := w.run(matrixInput())
	if next.Pending == nil || !next.Pending.At.Equal(set.At) || next.Pending.Note != "later" {
		t.Errorf("next %+v", next.Pending)
	}
}
