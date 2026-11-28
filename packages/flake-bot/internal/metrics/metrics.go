// Package metrics exports the state of the flake-bot's newest runs to
// Prometheus. The values come from Temporal (each run's status, close time
// and failure), not from this process, so a restarted worker exports them
// again at once. The rules are in k8s/manifests/prometheus/rules/flake-bot.yaml.
package metrics

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"homelab/flake-bot/internal/bot"
)

// Workflows are the workflow types exported, by their Temporal type names.
var Workflows = []string{"UpdateRun", "DevBuild"}

// Run is one closed run as the metrics see it.
type Run struct {
	Closed time.Time
	Failed bool
	// Reason is a failed run's error type (BuildFailed, PatchConflict, ...)
	// or, without one, its status (Failed, TimedOut, Terminated, Canceled).
	Reason string
	// FailedTargets is how many targets a BuildFailed run could not build.
	FailedTargets int
}

// State is what the metrics export for one workflow type; a nil field
// means no such run yet.
type State struct {
	LastSuccess *time.Time
	Last        *Run
}

// Source reads the state of one workflow type.
type Source func(ctx context.Context, workflow string) (State, error)

// Collector holds the gauges, all labelled by workflow. Vectors, so nothing
// is exported for a workflow with no run yet (a plain gauge would say 1970).
type Collector struct {
	lastSuccess, lastClose, failed, failedTargets *prometheus.GaugeVec
}

// New makes the gauges and registers them with reg.
func New(reg prometheus.Registerer) *Collector {
	c := &Collector{
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_flake_bot_last_success_timestamp_seconds",
			Help: "When the newest successful run of the workflow closed.",
		}, []string{"workflow"}),
		lastClose: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_flake_bot_last_run_timestamp_seconds",
			Help: "When the newest run of the workflow closed, successful or not.",
		}, []string{"workflow"}),
		failed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_flake_bot_last_run_failed",
			Help: "1 if the newest closed run of the workflow failed, labelled with why.",
		}, []string{"workflow", "reason"}),
		failedTargets: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_flake_bot_last_run_failed_targets",
			Help: "Targets (hosts and checks) the newest closed run of the workflow could not build.",
		}, []string{"workflow"}),
	}
	reg.MustRegister(c.lastSuccess, c.lastClose, c.failed, c.failedTargets)
	return c
}

// Set replaces workflow's gauges with s.
func (c *Collector) Set(workflow string, s State) {
	if s.LastSuccess != nil {
		c.lastSuccess.WithLabelValues(workflow).Set(float64(s.LastSuccess.Unix()))
	}
	c.failed.DeletePartialMatch(prometheus.Labels{"workflow": workflow})
	if s.Last != nil {
		c.lastClose.WithLabelValues(workflow).Set(float64(s.Last.Closed.Unix()))
		f := 0.0
		if s.Last.Failed {
			f = 1
		}
		c.failed.WithLabelValues(workflow, s.Last.Reason).Set(f)
		c.failedTargets.WithLabelValues(workflow).Set(float64(s.Last.FailedTargets))
	}
}

// Run refreshes the gauges from src every interval until ctx ends. On an
// error it keeps a workflow's previous values: its last-success timestamp
// then ages and alerts.
func (c *Collector) Run(ctx context.Context, src Source, interval time.Duration) {
	for {
		for _, w := range Workflows {
			s, err := src(ctx, w)
			if err != nil {
				slog.Error("metrics: newest runs", "workflow", w, "error", err)
				continue
			}
			c.Set(w, s)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// TemporalSource reads the newest runs in namespace. It relies on the SQL
// visibility store listing closed runs newest close time first; it does not
// support ORDER BY.
func TemporalSource(tc client.Client, namespace string) Source {
	newest := func(ctx context.Context, query string) (*workflowservice.ListWorkflowExecutionsResponse, error) {
		return tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Namespace: namespace, PageSize: 1, Query: query,
		})
	}
	return func(ctx context.Context, workflow string) (State, error) {
		var s State
		ok, err := newest(ctx, "WorkflowType = '"+workflow+"' AND ExecutionStatus = 'Completed'")
		if err != nil {
			return s, err
		}
		if len(ok.Executions) > 0 && ok.Executions[0].CloseTime != nil {
			t := ok.Executions[0].CloseTime.AsTime()
			s.LastSuccess = &t
		}
		last, err := newest(ctx, "WorkflowType = '"+workflow+"' AND ExecutionStatus != 'Running'")
		if err != nil {
			return s, err
		}
		if len(last.Executions) == 0 {
			return s, nil
		}
		e := last.Executions[0]
		if e.CloseTime == nil {
			return s, errors.New("closed run without a close time")
		}
		r := &Run{Closed: e.CloseTime.AsTime()}
		if e.Status != enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED {
			r.Failed = true
			r.Reason = statusName(e.Status)
			err := tc.GetWorkflow(ctx, e.Execution.WorkflowId, e.Execution.RunId).Get(ctx, nil)
			r.Reason, r.FailedTargets = Failure(err, workflow, r.Reason)
		}
		s.Last = r
		return s, nil
	}
}

// Failure reads a failed run's error: its application error type, and the
// failed targets from the result the workflow attaches as details, decoded
// as that workflow's result type.
func Failure(err error, workflow, fallback string) (reason string, failedTargets int) {
	reason = fallback
	var ae *temporal.ApplicationError
	if !errors.As(err, &ae) {
		return reason, 0
	}
	if ae.Type() != "" {
		reason = ae.Type()
	}
	if ae.HasDetails() {
		var build *bot.BuildResult
		switch workflow {
		case "UpdateRun":
			var res bot.UpdateResult
			if ae.Details(&res) == nil {
				build = res.Build
			}
		case "DevBuild":
			var res bot.DevBuildResult
			if ae.Details(&res) == nil {
				build = res.Build
			}
		}
		if build != nil {
			failedTargets = len(build.Failed())
		}
	}
	return reason, failedTargets
}

func statusName(s enumspb.WorkflowExecutionStatus) string {
	switch s {
	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED:
		return "Failed"
	case enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return "TimedOut"
	case enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return "Terminated"
	case enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return "Canceled"
	case enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
		return "ContinuedAsNew"
	}
	return s.String()
}
