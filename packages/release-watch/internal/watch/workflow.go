package watch

import (
	"context"
	"errors"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TaskQueue is the worker's task queue, in Temporal namespace release-watch.
const TaskQueue = "release-watch"

// ScheduleID is the Schedule that starts ReleaseWatch daily; its runs get
// workflow IDs starting with it.
const ScheduleID = "release-watch"

// WorkflowType is ReleaseWatch's type name, which the metrics query.
const WorkflowType = "ReleaseWatch"

// Result is ReleaseWatch's result, shown in the Temporal UI and read back
// by the metrics.
type Result struct {
	Targets []TargetResult
}

// ErrTypeFailed is the error type of a run in which a target's check failed.
const ErrTypeFailed = "CheckFailed"

// GitHub allows 60 unauthenticated requests an hour per address, shared by
// everything behind the router's NAT64 address: the retries span a few
// hours, past a reset.
var checkOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 2 * time.Minute,
	RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Minute, BackoffCoefficient: 2,
		MaximumInterval: time.Hour, MaximumAttempts: 8},
}

// ReleaseWatch checks every target. A failed check does not stop the
// others; the run fails at the end if any failed, with what the others
// found as the failure's details.
func ReleaseWatch(ctx workflow.Context) (Result, error) {
	ctx = workflow.WithActivityOptions(ctx, checkOptions)
	var res Result
	var failures []error
	for _, t := range Targets {
		var r TargetResult
		if err := workflow.ExecuteActivity(ctx, (*Activities).Check, t).Get(ctx, &r); err != nil {
			workflow.GetLogger(ctx).Error("Check failed", "target", t.Name, "error", err)
			failures = append(failures, fmt.Errorf("%s: %w", t.Name, err))
			continue
		}
		res.Targets = append(res.Targets, r)
	}
	if len(failures) > 0 {
		return res, temporal.NewNonRetryableApplicationError(errors.Join(failures...).Error(), ErrTypeFailed, nil, res)
	}
	return res, nil
}

// EnsureSchedule creates the release-watch Schedule, daily at 08:00 UTC,
// with a first run now, if it does not exist. An existing Schedule is left
// as it is. The worker calls it at start.
func EnsureSchedule(ctx context.Context, c client.ScheduleClient) error {
	_, err := c.Create(ctx, client.ScheduleOptions{
		ID: ScheduleID,
		Spec: client.ScheduleSpec{
			Calendars:    []client.ScheduleCalendarSpec{{Hour: []client.ScheduleRange{{Start: 8}}}},
			TimeZoneName: "UTC",
		},
		Action: &client.ScheduleWorkflowAction{
			ID:                       ScheduleID,
			Workflow:                 WorkflowType,
			TaskQueue:                TaskQueue,
			WorkflowExecutionTimeout: 12 * time.Hour,
		},
		Overlap:            enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		TriggerImmediately: true,
		Note:               "created by release-watch",
	})
	if errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		return nil
	}
	return err
}
