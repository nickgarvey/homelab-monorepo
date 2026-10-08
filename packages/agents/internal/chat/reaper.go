package chat

import (
	"context"
	"errors"
	"reflect"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"homelab/agents/internal/session"
)

// ReaperScheduleID is the Schedule that runs SandboxReaper; its runs get
// workflow IDs starting with it.
const ReaperScheduleID = "sandbox-reaper"

// ReaperInterval is how often the Schedule runs SandboxReaper.
const ReaperInterval = time.Hour

// ReapSummary is SandboxReaper's result, shown in the Temporal UI.
type ReapSummary struct {
	Expired  ReapStep
	Stuck    ReapStep
	Orphans  ReapStep
	Vanished ReapStep
}

// ReapStep is what one step of the reaper did with the sandboxes its plan
// listed. Done names the sandboxes deleted (for Vanished: whose rows were
// marked deleted); Skipped counts those found changed when the step came to
// them, such as a row a message had claimed.
type ReapStep struct {
	Done    []string `json:",omitempty"`
	Skipped int      `json:",omitempty"`
	Failed  []string `json:",omitempty"`
}

// SandboxReaper deletes the sandboxes, and so the PVCs, no conversation
// needs (design section 7, "PV cleanup"), each by its own activity, which
// checks again before it acts:
//  1. Expired: stopped longer than the type's session retention.
//  2. Stuck: rows left in deleting.
//  3. Orphans: sandboxes labelled managed-by=agents-worker that no live row
//     names.
//  4. Vanished: stopped rows whose sandbox is gone from the gateway are
//     marked deleted.
//
// A failed step is logged and listed; it does not stop the others, and the
// next run tries again.
func SandboxReaper(ctx workflow.Context) (ReapSummary, error) {
	var sum ReapSummary
	var plan session.ReapPlan
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, planOptions),
		(*Activities).PlanReap).Get(ctx, &plan); err != nil {
		return sum, err
	}
	actx := workflow.WithActivityOptions(ctx, reapOptions)
	step := func(st *ReapStep, name string, activity any, arg any) {
		var ok bool
		if err := workflow.ExecuteActivity(actx, activity, arg).Get(ctx, &ok); err != nil {
			workflow.GetLogger(ctx).Error("Reaping failed", "sandbox", name, "error", err)
			st.Failed = append(st.Failed, name)
		} else if ok {
			st.Done = append(st.Done, name)
		} else {
			st.Skipped++
		}
	}
	for _, r := range plan.Expired {
		step(&sum.Expired, r.Sandbox, (*Activities).ReapExpired, r)
	}
	for _, r := range plan.Stuck {
		step(&sum.Stuck, r.Sandbox, (*Activities).ReapStuck, r)
	}
	for _, name := range plan.Orphans {
		step(&sum.Orphans, name, (*Activities).ReapOrphan, name)
	}
	for _, r := range plan.Vanished {
		step(&sum.Vanished, r.Sandbox, (*Activities).MarkVanished, r)
	}
	return sum, nil
}

var (
	planOptions = workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	reapOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3, NonRetryableErrorTypes: []string{ErrTypeInvalid}},
	}
)

// reaperSchedule is the sandbox-reaper Schedule: SandboxReaper hourly on
// the worker's task queue, a run skipped while the last is still going.
func reaperSchedule() client.Schedule {
	return client.Schedule{
		Spec: &client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: ReaperInterval}}},
		Action: &client.ScheduleWorkflowAction{
			ID:                       ReaperScheduleID,
			Workflow:                 "SandboxReaper",
			TaskQueue:                TaskQueue,
			WorkflowExecutionTimeout: ReaperInterval,
		},
		Policy: &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP},
	}
}

// EnsureReaperSchedule creates the sandbox-reaper Schedule, or brings an
// existing one back to reaperSchedule if it differs. The worker calls it at
// start.
func EnsureReaperSchedule(ctx context.Context, c client.ScheduleClient) error {
	want := reaperSchedule()
	_, err := c.Create(ctx, client.ScheduleOptions{
		ID:      ReaperScheduleID,
		Spec:    *want.Spec,
		Action:  want.Action,
		Overlap: want.Policy.Overlap,
	})
	if !errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		return err
	}
	return c.GetHandle(ctx, ReaperScheduleID).Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			if scheduleMatches(in.Description.Schedule, want) {
				return nil, temporal.ErrSkipScheduleUpdate
			}
			s := in.Description.Schedule
			s.Spec, s.Action = want.Spec, want.Action
			if s.Policy == nil {
				s.Policy = &client.SchedulePolicies{}
			}
			s.Policy.Overlap = want.Policy.Overlap
			return &client.ScheduleUpdate{Schedule: &s}, nil
		},
	})
}

// scheduleMatches compares what EnsureReaperSchedule sets: the interval,
// the workflow and its queue and timeout, and the overlap policy. A
// described Schedule names its workflow by type name.
func scheduleMatches(got, want client.Schedule) bool {
	if got.Spec == nil || got.Policy == nil || got.Policy.Overlap != want.Policy.Overlap {
		return false
	}
	var every []time.Duration
	for _, i := range got.Spec.Intervals {
		if i.Offset != 0 {
			return false
		}
		every = append(every, i.Every)
	}
	if !reflect.DeepEqual(every, []time.Duration{ReaperInterval}) ||
		len(got.Spec.Calendars) != 0 || len(got.Spec.CronExpressions) != 0 {
		return false
	}
	a, ok := got.Action.(*client.ScheduleWorkflowAction)
	w := want.Action.(*client.ScheduleWorkflowAction)
	return ok && a.ID == w.ID && a.Workflow == w.Workflow && a.TaskQueue == w.TaskQueue &&
		a.WorkflowExecutionTimeout == w.WorkflowExecutionTimeout
}
