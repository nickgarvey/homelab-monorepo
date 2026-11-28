// Package bot is the flake-bot: Temporal workflows that keep dev current
// with `nix flake update` (UpdateRun) and publish each dev commit's builds
// for hosts to pull (DevBuild), with the activities they run on wabbajack.
package bot

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

// TaskQueue is the worker's task queue, in Temporal namespace flake-bot.
const TaskQueue = "flake-bot"

// Schedule IDs; their runs get workflow IDs starting with them.
const (
	UpdateScheduleID = "flake-bot-update"
	DevScheduleID    = "flake-bot-dev"
)

// Error types of failed runs.
const (
	ErrTypeBuildFailed = "BuildFailed"
	ErrTypeConflict    = "PatchConflict"
)

// Short steps: git and S3. Retried a few times over a few minutes.
var stepOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 15 * time.Minute,
	RetryPolicy: &temporal.RetryPolicy{InitialInterval: 30 * time.Second, BackoffCoefficient: 2,
		MaximumInterval: 5 * time.Minute, MaximumAttempts: 5},
}

// Long steps: flake update, builds and celler pushes, which heartbeat. A
// rebuild of everything after a nixpkgs bump takes hours on wabbajack.
var buildOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 10 * time.Hour,
	HeartbeatTimeout:    10 * time.Minute,
	RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Minute, BackoffCoefficient: 2,
		MaximumInterval: 15 * time.Minute, MaximumAttempts: 3,
		NonRetryableErrorTypes: []string{"WorktreeMoved"}},
}

// UpdateResult is what an UpdateRun did, shown in the Temporal UI.
type UpdateResult struct {
	Prepare PrepareResult
	Build   *BuildResult `json:",omitempty"`
	Pushed  bool
	Land    *LandResult `json:",omitempty"`
}

// UpdateRun rebuilds flake-update (dev + a fresh lock update + the carried
// patches), builds every target, pushes what built to celler, force-pushes
// flake-update and, when everything built, fast-forwards dev to it.
//
// A patch that no longer applies stops the run before any push: pushing
// would drop the patch from flake-update.
func UpdateRun(ctx workflow.Context) (UpdateResult, error) {
	step := workflow.WithActivityOptions(ctx, stepOptions)
	long := workflow.WithActivityOptions(ctx, buildOptions)
	var a *Activities
	var res UpdateResult

	if err := workflow.ExecuteActivity(long, a.PrepareUpdate).Get(ctx, &res.Prepare); err != nil {
		return res, err
	}
	p := res.Prepare
	if p.Conflict != "" {
		return res, temporal.NewNonRetryableApplicationError(p.Conflict, ErrTypeConflict, nil, res)
	}
	if p.Head == p.Base {
		// Nothing new: flake-update becomes dev again.
		if err := workflow.ExecuteActivity(step, a.PushBranch, PushBranchRequest{Head: p.Head, OldBranch: p.OldBranch}).Get(ctx, nil); err != nil {
			return res, err
		}
		res.Pushed = p.Head != p.OldBranch
		return res, nil
	}

	var b BuildResult
	if err := workflow.ExecuteActivity(long, a.Build, BuildRequest{Worktree: UpdateWorktree, Rev: p.Head}).Get(ctx, &b); err != nil {
		return res, err
	}
	res.Build = &b
	if err := workflow.ExecuteActivity(long, a.PushCeller, b.Built()).Get(ctx, nil); err != nil {
		return res, err
	}
	if err := workflow.ExecuteActivity(step, a.PushBranch, PushBranchRequest{Head: p.Head, OldBranch: p.OldBranch}).Get(ctx, nil); err != nil {
		return res, err
	}
	res.Pushed = true
	if !b.OK() {
		return res, temporal.NewNonRetryableApplicationError(failedSummary(b), ErrTypeBuildFailed, nil, res)
	}
	var l LandResult
	if err := workflow.ExecuteActivity(step, a.Land, LandRequest{Base: p.Base, Head: p.Head}).Get(ctx, &l); err != nil {
		return res, err
	}
	res.Land = &l
	return res, nil
}

// DevResultSummary is what a DevBuild did.
type DevBuildResult struct {
	Dev DevResult
	// Skipped is set when the manifest already covers Dev.Rev.
	Skipped  bool
	Build    *BuildResult `json:",omitempty"`
	Manifest *Manifest    `json:",omitempty"`
}

// DevBuild builds origin/dev's head, if the manifest does not already cover
// it, pushes what built to celler and publishes it in the manifest. Hosts
// that failed keep their previous entry; the run then fails, so the failure
// shows.
func DevBuild(ctx workflow.Context) (DevBuildResult, error) {
	step := workflow.WithActivityOptions(ctx, stepOptions)
	long := workflow.WithActivityOptions(ctx, buildOptions)
	var a *Activities
	var res DevBuildResult

	if err := workflow.ExecuteActivity(step, a.PrepareDev).Get(ctx, &res.Dev); err != nil {
		return res, err
	}
	var m Manifest
	if err := workflow.ExecuteActivity(step, a.ReadManifest).Get(ctx, &m); err != nil {
		return res, err
	}
	if m.Commit == res.Dev.Rev {
		res.Skipped = true
		return res, nil
	}
	var b BuildResult
	if err := workflow.ExecuteActivity(long, a.Build, BuildRequest{Worktree: DevWorktree, Rev: res.Dev.Rev}).Get(ctx, &b); err != nil {
		return res, err
	}
	res.Build = &b
	if err := workflow.ExecuteActivity(long, a.PushCeller, b.Built()).Get(ctx, nil); err != nil {
		return res, err
	}
	if err := workflow.ExecuteActivity(step, a.Publish, PublishRequest{Dev: res.Dev, Build: b}).Get(ctx, &m); err != nil {
		return res, err
	}
	res.Manifest = &m
	if !b.OK() {
		return res, temporal.NewNonRetryableApplicationError(failedSummary(b), ErrTypeBuildFailed, nil, res)
	}
	return res, nil
}

func failedSummary(b BuildResult) string {
	var names []string
	for _, t := range b.Failed() {
		names = append(names, fmt.Sprintf("%s %s", t.Kind, t.Name))
	}
	return fmt.Sprintf("%d of %d targets failed at %s: %s", len(names), len(b.Targets), short(b.Rev), strings.Join(names, ", "))
}

// EnsureSchedules creates the two Schedules if they do not exist: UpdateRun
// daily at 10:00 UTC, DevBuild every 15 minutes. Existing ones are left as
// they are. The worker calls it at start.
func EnsureSchedules(ctx context.Context, c client.ScheduleClient) error {
	for _, s := range []client.ScheduleOptions{
		{
			ID: UpdateScheduleID,
			Spec: client.ScheduleSpec{
				Calendars:    []client.ScheduleCalendarSpec{{Hour: []client.ScheduleRange{{Start: 10}}}},
				TimeZoneName: "UTC",
			},
			Action: &client.ScheduleWorkflowAction{ID: UpdateScheduleID, Workflow: UpdateRun,
				TaskQueue: TaskQueue, WorkflowExecutionTimeout: 20 * time.Hour},
		},
		{
			ID:   DevScheduleID,
			Spec: client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: 15 * time.Minute}}},
			Action: &client.ScheduleWorkflowAction{ID: DevScheduleID, Workflow: DevBuild,
				TaskQueue: TaskQueue, WorkflowExecutionTimeout: 20 * time.Hour},
		},
	} {
		s.Overlap = enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
		s.Note = "created by flake-bot"
		if _, err := c.Create(ctx, s); err != nil && !errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
			return fmt.Errorf("schedule %s: %w", s.ID, err)
		}
	}
	return nil
}
