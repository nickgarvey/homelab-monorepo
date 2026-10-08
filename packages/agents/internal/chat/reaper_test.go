package chat

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"homelab/agents/internal/session"
)

func ref(conv, sandbox string) session.SandboxRef {
	return session.SandboxRef{Conversation: session.Conversation{Type: "pi", Conversation: conv}, Sandbox: sandbox}
}

func TestSandboxReaperSummary(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})
	env.OnActivity("PlanReap", mock.Anything).Return(session.ReapPlan{
		Expired: []session.ExpiredRef{
			{SandboxRef: ref("c1", "chat-a-1"), RetentionSeconds: 60},
			// Claimed by a message after the plan.
			{SandboxRef: ref("c2", "chat-b-1"), RetentionSeconds: 60},
		},
		Stuck:    []session.SandboxRef{ref("c3", "chat-c-2")},
		Orphans:  []string{"chat-d-1", "chat-e-1"},
		Vanished: []session.SandboxRef{ref("c4", "chat-f-1")},
	}, nil)
	env.OnActivity("ReapExpired", mock.Anything, mock.Anything).Return(
		func(_ context.Context, in session.ExpiredRef) (bool, error) { return in.Sandbox == "chat-a-1", nil })
	env.OnActivity("ReapStuck", mock.Anything, mock.Anything).Return(true, nil)
	env.OnActivity("ReapOrphan", mock.Anything, mock.Anything).Return(
		func(_ context.Context, name string) (bool, error) {
			if name == "chat-e-1" {
				return false, errors.New("gateway down")
			}
			return true, nil
		})
	env.OnActivity("MarkVanished", mock.Anything, mock.Anything).Return(true, nil)

	env.ExecuteWorkflow(SandboxReaper)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var got ReapSummary
	env.GetWorkflowResult(&got)
	want := ReapSummary{
		Expired:  ReapStep{Done: []string{"chat-a-1"}, Skipped: 1},
		Stuck:    ReapStep{Done: []string{"chat-c-2"}},
		Orphans:  ReapStep{Done: []string{"chat-d-1"}, Failed: []string{"chat-e-1"}},
		Vanished: ReapStep{Done: []string{"chat-f-1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summary %+v, want %+v", got, want)
	}
}

// fakeSchedules is a ScheduleClient holding at most the one Schedule.
type fakeSchedules struct {
	client.ScheduleClient
	client.ScheduleHandle
	schedule *client.Schedule
	updates  int
}

func (f *fakeSchedules) Create(_ context.Context, o client.ScheduleOptions) (client.ScheduleHandle, error) {
	if f.schedule != nil {
		return nil, temporal.ErrScheduleAlreadyRunning
	}
	// As described back: the workflow by its type name.
	action := *o.Action.(*client.ScheduleWorkflowAction)
	spec := o.Spec
	f.schedule = &client.Schedule{Spec: &spec, Action: &action, Policy: &client.SchedulePolicies{Overlap: o.Overlap}}
	return f, nil
}

func (f *fakeSchedules) GetHandle(context.Context, string) client.ScheduleHandle { return f }

func (f *fakeSchedules) Update(_ context.Context, o client.ScheduleUpdateOptions) error {
	u, err := o.DoUpdate(client.ScheduleUpdateInput{Description: client.ScheduleDescription{Schedule: *f.schedule}})
	if errors.Is(err, temporal.ErrSkipScheduleUpdate) {
		return nil
	}
	if err != nil {
		return err
	}
	f.schedule = u.Schedule
	f.updates++
	return nil
}

func TestEnsureReaperSchedule(t *testing.T) {
	ctx := context.Background()
	f := &fakeSchedules{}
	if err := EnsureReaperSchedule(ctx, f); err != nil {
		t.Fatal(err)
	}
	if !scheduleMatches(*f.schedule, reaperSchedule()) || f.schedule.Policy.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_SKIP {
		t.Fatalf("created %+v", f.schedule)
	}

	// Already as wanted: left alone.
	if err := EnsureReaperSchedule(ctx, f); err != nil || f.updates != 0 {
		t.Fatalf("second ensure: %v, %d updates", err, f.updates)
	}

	// Changed by hand: put back, keeping what EnsureReaperSchedule does not
	// manage.
	f.schedule.Spec.Intervals[0].Every = time.Minute
	f.schedule.Policy.Overlap = enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL
	f.schedule.Policy.PauseOnFailure = true
	if err := EnsureReaperSchedule(ctx, f); err != nil || f.updates != 1 {
		t.Fatalf("third ensure: %v, %d updates", err, f.updates)
	}
	if !scheduleMatches(*f.schedule, reaperSchedule()) || !f.schedule.Policy.PauseOnFailure {
		t.Errorf("updated %+v", f.schedule)
	}
}
