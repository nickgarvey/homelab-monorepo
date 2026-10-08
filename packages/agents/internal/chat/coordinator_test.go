package chat

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

type coordinatorWorld struct {
	testsuite.WorkflowTestSuite
	t       *testing.T
	env     *testsuite.TestWorkflowEnvironment
	evicted []string
	holders map[time.Duration][]string
}

func newCoordinatorWorld(t *testing.T) *coordinatorWorld {
	w := &coordinatorWorld{t: t, holders: map[time.Duration][]string{}}
	w.env = w.NewTestWorkflowEnvironment()
	w.env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, "", EvictSignal, mock.Anything).Return(
		func(_, id, _, _ string, _ any) error {
			w.evicted = append(w.evicted, id)
			return nil
		})
	return w
}

func (w *coordinatorWorld) acquire(at time.Duration, id string, expiresIn time.Duration) {
	w.env.RegisterDelayedCallback(func() {
		w.env.UpdateWorkflowNoRejection(AcquireUpdate, id+at.String(), w.t,
			SlotRequest{Holder: id, Expires: w.env.Now().Add(expiresIn), Cap: 2})
	}, at)
}

func (w *coordinatorWorld) signal(at time.Duration, name string, arg any) {
	w.env.RegisterDelayedCallback(func() { w.env.SignalWorkflow(name, arg) }, at)
}

// snapshot records the holders at a time.
func (w *coordinatorWorld) snapshot(at time.Duration) {
	w.env.RegisterDelayedCallback(func() {
		v, err := w.env.QueryWorkflow(HoldersQuery)
		if err != nil {
			w.t.Fatal(err)
		}
		var hs []Holder
		if err := v.Get(&hs); err != nil {
			w.t.Fatal(err)
		}
		ids := []string{}
		for _, h := range hs {
			ids = append(ids, h.ID)
		}
		w.holders[at] = ids
	}, at)
}

func (w *coordinatorWorld) run(until time.Duration) {
	w.env.RegisterDelayedCallback(w.env.CancelWorkflow, until)
	w.env.ExecuteWorkflow(SessionCoordinator, CoordinatorState{})
}

func TestCapEvictsLongestIdle(t *testing.T) {
	w := newCoordinatorWorld(t)
	w.acquire(time.Minute, "a", time.Hour)
	w.acquire(2*time.Minute, "b", time.Hour)
	// a's turn is more recent than b's acquisition.
	w.signal(3*time.Minute, TouchSignal, SlotRequest{Holder: "a", Expires: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)})
	w.acquire(4*time.Minute, "c", time.Hour)
	w.snapshot(5 * time.Minute)
	// b's release after its eviction changes nothing; a's frees a slot.
	w.signal(6*time.Minute, ReleaseSignal, "b")
	w.signal(6*time.Minute, ReleaseSignal, "a")
	w.acquire(7*time.Minute, "d", time.Hour)
	w.snapshot(8 * time.Minute)
	w.run(9 * time.Minute)

	if len(w.evicted) != 1 || w.evicted[0] != "b" {
		t.Errorf("evicted %v", w.evicted)
	}
	if got := w.holders[5*time.Minute]; len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Errorf("holders at 5m: %v", got)
	}
	if got := w.holders[8*time.Minute]; len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Errorf("holders at 8m: %v", got)
	}
}

func TestReacquiringKeepsOneSlot(t *testing.T) {
	w := newCoordinatorWorld(t)
	w.acquire(time.Minute, "a", time.Hour)
	w.acquire(2*time.Minute, "a", time.Hour)
	w.acquire(3*time.Minute, "b", time.Hour)
	w.run(4 * time.Minute)
	if len(w.evicted) != 0 {
		t.Errorf("evicted %v", w.evicted)
	}
}

func TestExpiredSlotsAreDropped(t *testing.T) {
	w := newCoordinatorWorld(t)
	// a vanishes without releasing; its slot expires after 30 minutes.
	w.acquire(time.Minute, "a", 30*time.Minute)
	w.acquire(2*time.Minute, "b", 2*time.Hour)
	w.snapshot(50 * time.Minute)
	w.acquire(time.Hour, "c", time.Hour)
	w.run(61 * time.Minute)
	if got := w.holders[50*time.Minute]; len(got) != 1 || got[0] != "b" {
		t.Errorf("holders at 50m: %v", got)
	}
	if len(w.evicted) != 0 {
		t.Errorf("evicted %v", w.evicted)
	}
}
