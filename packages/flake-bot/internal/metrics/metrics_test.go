package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"homelab/flake-bot/internal/bot"
)

func TestCollector(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := New(reg)
	ok := time.Unix(1000, 0)
	failedAt := time.Unix(2000, 0)
	c.Set("UpdateRun", State{LastSuccess: &ok, Last: &Run{Closed: failedAt, Failed: true, Reason: "BuildFailed", FailedTargets: 2}})
	c.Set("DevBuild", State{})

	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP homelab_flake_bot_last_run_failed 1 if the newest closed run of the workflow failed, labelled with why.
# TYPE homelab_flake_bot_last_run_failed gauge
homelab_flake_bot_last_run_failed{reason="BuildFailed",workflow="UpdateRun"} 1
# HELP homelab_flake_bot_last_run_failed_targets Targets (hosts and checks) the newest closed run of the workflow could not build.
# TYPE homelab_flake_bot_last_run_failed_targets gauge
homelab_flake_bot_last_run_failed_targets{workflow="UpdateRun"} 2
# HELP homelab_flake_bot_last_run_timestamp_seconds When the newest run of the workflow closed, successful or not.
# TYPE homelab_flake_bot_last_run_timestamp_seconds gauge
homelab_flake_bot_last_run_timestamp_seconds{workflow="UpdateRun"} 2000
# HELP homelab_flake_bot_last_success_timestamp_seconds When the newest successful run of the workflow closed.
# TYPE homelab_flake_bot_last_success_timestamp_seconds gauge
homelab_flake_bot_last_success_timestamp_seconds{workflow="UpdateRun"} 1000
`)), "a workflow with no runs exports nothing")

	// A later success replaces the failure; the old reason label goes away.
	c.Set("UpdateRun", State{LastSuccess: &failedAt, Last: &Run{Closed: failedAt}})
	require.Equal(t, 0.0, testutil.ToFloat64(c.failed.WithLabelValues("UpdateRun", "")))
	n, err := testutil.GatherAndCount(reg, "homelab_flake_bot_last_run_failed")
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestFailure(t *testing.T) {
	res := bot.UpdateResult{Build: &bot.BuildResult{Targets: []bot.TargetResult{
		{Name: "talos", OutPath: "/nix/store/t"},
		{Name: "skyforge", Error: "build failed"},
		{Name: "lydia", Error: "eval: boom"},
	}}}
	err := temporal.NewNonRetryableApplicationError("2 of 3 failed", bot.ErrTypeBuildFailed, nil, res)
	reason, n := Failure(err, "UpdateRun", "Failed")
	require.Equal(t, bot.ErrTypeBuildFailed, reason)
	require.Equal(t, 2, n)

	reason, n = Failure(temporal.NewNonRetryableApplicationError("x", bot.ErrTypeConflict, nil), "UpdateRun", "Failed")
	require.Equal(t, bot.ErrTypeConflict, reason)
	require.Zero(t, n)

	reason, _ = Failure(nil, "DevBuild", "TimedOut")
	require.Equal(t, "TimedOut", reason)
}
