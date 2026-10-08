package session

import (
	"context"
	"reflect"
	"testing"
	"time"

	"homelab/agents/internal/gateway"
	"homelab/agents/internal/store"
)

// stopped makes c1's sandbox and stops it, with pi's retention set to one
// second, and waits past it if expire is set.
func (w *world) stopped(t *testing.T, expire bool) SandboxRef {
	t.Helper()
	pi := w.state.Types["pi"]
	pi.Session.RetentionSeconds = 1
	w.state.Types["pi"] = pi
	sb := w.ensure(t)
	ref := SandboxRef{Conversation: c1, Sandbox: sb.Sandbox}
	if err := w.StopSandbox(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if expire {
		time.Sleep(1100 * time.Millisecond)
	}
	return ref
}

func (w *world) plan(t *testing.T) ReapPlan {
	t.Helper()
	plan, err := w.PlanReap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestReapExpired(t *testing.T) {
	w := newWorld(t)
	ref := w.stopped(t, true)

	plan := w.plan(t)
	want := ReapPlan{Expired: []ExpiredRef{{SandboxRef: ref, RetentionSeconds: 1}}}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan %+v, want %+v", plan, want)
	}
	if ok, err := w.ReapExpired(context.Background(), plan.Expired[0]); !ok || err != nil {
		t.Fatalf("reap: %v %v", ok, err)
	}
	if _, ok := w.gw.Sandboxes[ref.Sandbox]; ok {
		t.Error("sandbox not deleted")
	}
	if row := w.row(t); row.State != store.Deleted {
		t.Errorf("row %+v", row)
	}
	// The next message makes a fresh, primed sandbox.
	if sb := w.ensure(t); !sb.Created || sb.Sandbox == ref.Sandbox {
		t.Errorf("ensure after reaping: %+v", sb)
	}
}

func TestReapNotYetExpired(t *testing.T) {
	w := newWorld(t)
	w.stopped(t, false)
	if plan := w.plan(t); !reflect.DeepEqual(plan, ReapPlan{}) {
		t.Errorf("plan %+v", plan)
	}
}

func TestUnpublishedTypeKeepsDefaultRetention(t *testing.T) {
	w := newWorld(t)
	w.stopped(t, true)
	delete(w.state.Types, "pi")
	if plan := w.plan(t); !reflect.DeepEqual(plan, ReapPlan{}) {
		t.Errorf("plan %+v", plan)
	}
}

// A message claims the row between the plan and the deletion: the reaper
// leaves it, and its sandbox, alone.
func TestReapRacesAMessage(t *testing.T) {
	w := newWorld(t)
	ref := w.stopped(t, true)
	plan := w.plan(t)

	if sb := w.ensure(t); sb.Created || sb.Sandbox != ref.Sandbox {
		t.Fatalf("message: %+v", sb)
	}
	if ok, err := w.ReapExpired(context.Background(), plan.Expired[0]); ok || err != nil {
		t.Errorf("reap: %v %v", ok, err)
	}
	if w.callsTo("delete") != 0 || w.row(t).State != store.Running {
		t.Errorf("calls %v, row %+v", w.gw.Calls, w.row(t))
	}

	// Stopped again since the plan: the retention runs from the new stop.
	if err := w.StopSandbox(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.ReapExpired(context.Background(), plan.Expired[0]); ok {
		t.Error("reaped a row stopped again just now")
	}
}

func TestReapStuck(t *testing.T) {
	w := newWorld(t)
	ref := w.stopped(t, false)
	// A crash after the claim, before the delete.
	w.Store.Transition(context.Background(), "pi", "c1", ref.Sandbox, []store.State{store.Stopped}, store.Deleting)

	plan := w.plan(t)
	if !reflect.DeepEqual(plan, ReapPlan{Stuck: []SandboxRef{ref}}) {
		t.Fatalf("plan %+v", plan)
	}
	if ok, err := w.ReapStuck(context.Background(), ref); !ok || err != nil {
		t.Fatalf("reap: %v %v", ok, err)
	}
	if _, ok := w.gw.Sandboxes[ref.Sandbox]; ok || w.row(t).State != store.Deleted {
		t.Errorf("sandbox left, or row %+v", w.row(t))
	}
	// Finished meanwhile (here: by the run above): nothing to do.
	if ok, err := w.ReapStuck(context.Background(), ref); ok || err != nil {
		t.Errorf("second reap: %v %v", ok, err)
	}
}

func TestReapOrphans(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	labels := map[string]string{ManagedByLabel: ManagedBy}
	w.gw.Create(ctx, "chat-orphan-1", gateway.Spec{Labels: labels})
	w.gw.Create(ctx, "someone-elses", gateway.Spec{})
	// A row being created: its name is recorded and its sandbox exists, but
	// the row is not running yet. Not an orphan.
	row, _ := w.Store.NewGeneration(ctx, "pi", "c2", "v1")
	w.gw.Create(ctx, row.Sandbox, gateway.Spec{Labels: labels})

	plan := w.plan(t)
	if !reflect.DeepEqual(plan, ReapPlan{Orphans: []string{"chat-orphan-1"}}) {
		t.Fatalf("plan %+v", plan)
	}
	if ok, err := w.ReapOrphan(ctx, "chat-orphan-1"); !ok || err != nil {
		t.Fatalf("reap: %v %v", ok, err)
	}
	if ok, _ := w.ReapOrphan(ctx, row.Sandbox); ok {
		t.Error("reaped a sandbox a row holds")
	}
	if _, ok := w.gw.Sandboxes["chat-orphan-1"]; ok {
		t.Error("orphan left")
	}
	if len(w.gw.Sandboxes) != 2 {
		t.Errorf("sandboxes %v", w.gw.Sandboxes)
	}
}

func TestMarkVanished(t *testing.T) {
	w := newWorld(t)
	ref := w.stopped(t, false)
	w.gw.Delete(context.Background(), ref.Sandbox)
	// A running row whose sandbox went is EnsureSandbox's to handle.
	other := Conversation{Type: "pi", Conversation: "c2"}
	running, _ := w.EnsureSandbox(context.Background(), EnsureInput{other})
	w.gw.Delete(context.Background(), running.Sandbox)

	plan := w.plan(t)
	if !reflect.DeepEqual(plan, ReapPlan{Vanished: []SandboxRef{ref}}) {
		t.Fatalf("plan %+v", plan)
	}
	if ok, err := w.MarkVanished(context.Background(), ref); !ok || err != nil {
		t.Fatalf("mark: %v %v", ok, err)
	}
	if row := w.row(t); row.State != store.Deleted {
		t.Errorf("row %+v", row)
	}
	if sb := w.ensure(t); !sb.Created || sb.Sandbox == ref.Sandbox {
		t.Errorf("ensure after vanishing: %+v", sb)
	}
}
