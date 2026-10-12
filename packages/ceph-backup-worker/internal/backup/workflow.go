package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TaskQueue is the worker's task queue, in Temporal namespace ceph-backup.
const TaskQueue = "ceph-backup"

// ScheduleID is the Schedule that starts CephBackup daily; its runs get
// workflow IDs starting with it.
const ScheduleID = "ceph-backup"

// Input is CephBackup's argument. The Schedule passes the zero value.
type Input struct {
	// FullMaintenance forces full maintenance (true) or quick (false);
	// unset, it is full on Sundays (UTC).
	FullMaintenance *bool `json:",omitempty"`
}

// Summary is CephBackup's result, shown in the Temporal UI; on failure it is
// the failure's details.
type Summary struct {
	Items    []ItemResult
	Failures []string `json:",omitempty"`
}

// ItemResult is what became of one item.
type ItemResult struct {
	Kind, Name string
	SnapshotID string `json:",omitempty"`
	Error      string `json:",omitempty"`
}

// ErrTypeFailed is the error type of a run in which something failed.
const ErrTypeFailed = "BackupFailed"

var (
	discoverOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: 30 * time.Second, BackoffCoefficient: 2, MaximumAttempts: 3},
	}
	// A first full copy of a large bucket took about half an hour.
	backupOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 6 * time.Hour,
		HeartbeatTimeout:    5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: time.Minute, BackoffCoefficient: 2, MaximumAttempts: 3},
	}
	verifyOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 4 * time.Hour,
		HeartbeatTimeout:    5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Minute, BackoffCoefficient: 2, MaximumAttempts: 3,
			NonRetryableErrorTypes: []string{ErrTypeVerify}},
	}
	repoOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 8 * time.Hour,
		HeartbeatTimeout:    5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: time.Minute, BackoffCoefficient: 2, MaximumAttempts: 3},
	}
)

// CephBackup backs up everything in Ceph: Discover, then each item one at a
// time (Backup<Kind>, then Verify of its snapshot), then Maintenance,
// SyncStorj and VerifyStorj. A failed item does not stop the others; the
// run fails at the end if anything failed.
func CephBackup(ctx workflow.Context, in Input) (Summary, error) {
	var sum Summary
	log := workflow.GetLogger(ctx)
	fail := func(what string, err error) {
		log.Error("Failed", "what", what, "error", err)
		sum.Failures = append(sum.Failures, fmt.Sprintf("%s: %v", what, err))
	}

	var inv Inventory
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, discoverOptions),
		(*Activities).Discover).Get(ctx, &inv); err != nil {
		return sum, err
	}
	for _, e := range inv.Errors {
		fail("discover", errors.New(e))
	}

	bctx := workflow.WithActivityOptions(ctx, backupOptions)
	vctx := workflow.WithActivityOptions(ctx, verifyOptions)
	for _, it := range inv.Items {
		act, percent := backupActivity(it.Kind)
		if act == nil {
			continue
		}
		r := ItemResult{Kind: it.Kind, Name: it.Name}
		var res BackupResult
		err := workflow.ExecuteActivity(bctx, act, it).Get(ctx, &res)
		if err == nil {
			r.SnapshotID = res.SnapshotID
			err = workflow.ExecuteActivity(vctx, (*Activities).Verify,
				VerifyInput{BackupResult: res, Percent: percent}).Get(ctx, nil)
		}
		if err != nil {
			r.Error = err.Error()
			fail(it.Kind+" "+it.Name, err)
		}
		sum.Items = append(sum.Items, r)
	}

	rctx := workflow.WithActivityOptions(ctx, repoOptions)
	full := workflow.Now(ctx).UTC().Weekday() == time.Sunday
	if in.FullMaintenance != nil {
		full = *in.FullMaintenance
	}
	if err := workflow.ExecuteActivity(rctx, (*Activities).Maintenance, full).Get(ctx, nil); err != nil {
		fail("maintenance", err)
	}
	if err := workflow.ExecuteActivity(rctx, (*Activities).SyncStorj).Get(ctx, nil); err != nil {
		fail("sync to Storj", err)
	} else if err := workflow.ExecuteActivity(rctx, (*Activities).VerifyStorj).Get(ctx, nil); err != nil {
		fail("verify Storj", err)
	}

	if len(sum.Failures) > 0 {
		return sum, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("%d failed: %s", len(sum.Failures), strings.Join(sum.Failures, "; ")),
			ErrTypeFailed, nil, sum)
	}
	return sum, nil
}

// backupActivity is the activity that backs up an item of kind, and the
// percentage of its files Verify reads back: all of the metadata, which is
// a few KB. Nil for kinds that are only reported.
func backupActivity(kind string) (any, float64) {
	switch kind {
	case KindRBD:
		return (*Activities).BackupRBD, 10
	case KindRGW:
		return (*Activities).BackupBucket, 10
	case KindRGWMetadata:
		return (*Activities).BackupRGWMetadata, 100
	case KindCephFS:
		return (*Activities).BackupCephFS, 10
	}
	return nil, 0
}

// RunTimeout bounds a run; the next day's run starts after it.
const RunTimeout = 23 * time.Hour

// backupSchedule is the ceph-backup Schedule: CephBackup daily at 10:00 UTC
// (an hour after Longhorn's own daily backup), skipped while the last run
// is still going.
func backupSchedule() client.Schedule {
	return client.Schedule{
		Spec: &client.ScheduleSpec{
			Calendars:    []client.ScheduleCalendarSpec{{Hour: []client.ScheduleRange{{Start: 10}}}},
			TimeZoneName: "UTC",
		},
		Action: &client.ScheduleWorkflowAction{
			ID:                       ScheduleID,
			Workflow:                 "CephBackup",
			Args:                     []any{Input{}},
			TaskQueue:                TaskQueue,
			WorkflowExecutionTimeout: RunTimeout,
		},
		Policy: &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP},
	}
}

// EnsureSchedule creates the ceph-backup Schedule, paused, or brings an
// existing one's spec, action and overlap policy back to backupSchedule if
// they differ. It never changes whether an existing Schedule is paused:
// unpausing is a decision taken by hand. The worker calls it at start.
func EnsureSchedule(ctx context.Context, c client.ScheduleClient) error {
	want := backupSchedule()
	_, err := c.Create(ctx, client.ScheduleOptions{
		ID:      ScheduleID,
		Spec:    *want.Spec,
		Action:  want.Action,
		Overlap: want.Policy.Overlap,
		Paused:  true,
		Note:    "created paused by ceph-backup-worker; unpause by hand",
	})
	if !errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		return err
	}
	return c.GetHandle(ctx, ScheduleID).Update(ctx, client.ScheduleUpdateOptions{
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

// scheduleMatches compares what EnsureSchedule sets: one calendar at 10:00
// UTC, the workflow and its queue and timeout, and the overlap policy. A
// described Schedule names its workflow by type name and spells out the
// calendar's defaults, so only the hour, minute and second are compared.
func scheduleMatches(got, want client.Schedule) bool {
	if got.Spec == nil || got.Policy == nil || got.Policy.Overlap != want.Policy.Overlap {
		return false
	}
	if got.Spec.TimeZoneName != "UTC" || len(got.Spec.Calendars) != 1 ||
		len(got.Spec.Intervals) != 0 || len(got.Spec.CronExpressions) != 0 {
		return false
	}
	cal := got.Spec.Calendars[0]
	if !single(cal.Hour, 10, false) || !single(cal.Minute, 0, true) || !single(cal.Second, 0, true) {
		return false
	}
	a, ok := got.Action.(*client.ScheduleWorkflowAction)
	w := want.Action.(*client.ScheduleWorkflowAction)
	return ok && a.ID == w.ID && a.Workflow == w.Workflow && a.TaskQueue == w.TaskQueue &&
		a.WorkflowExecutionTimeout == w.WorkflowExecutionTimeout
}

// single reports whether rs matches only v (or is empty, when that means v).
func single(rs []client.ScheduleRange, v int, emptyOK bool) bool {
	if len(rs) == 0 {
		return emptyOK
	}
	return len(rs) == 1 && rs[0].Start == v && (rs[0].End == 0 || rs[0].End == v)
}
