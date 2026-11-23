// Package metrics exports the newest successful ReleaseWatch run to
// Prometheus. The values come from Temporal (the run's result and close
// time), not from this process, so a restarted worker exports them again
// without waiting a day for the next run. The rules are in
// k8s/manifests/prometheus/rules/release-watch.yaml.
package metrics

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"homelab/release-watch/internal/watch"
)

// Source gives the newest completed ReleaseWatch run's result and close
// time; found is false if there is none.
type Source func(ctx context.Context) (res watch.Result, closed time.Time, found bool, err error)

// Collector holds the gauges.
type Collector struct {
	info, behind, fixes *prometheus.GaugeVec
	// A vector without labels: a plain gauge would export 0, a check from
	// 1970, before there is a run.
	lastSuccess *prometheus.GaugeVec
}

// New makes the gauges and registers them with reg.
func New(reg prometheus.Registerer) *Collector {
	labels := []string{"name", "repo"}
	c := &Collector{
		info: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_release_watch_info",
			Help: "1, labelled with the version running in the cluster and the newest GitHub release.",
		}, []string{"name", "repo", "running", "latest"}),
		behind: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_release_watch_behind",
			Help: "1 if the running version is older than the newest GitHub release.",
		}, labels),
		fixes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_release_watch_security_fixes",
			Help: "Published security advisories that affect the running version and are fixed in a release up to the newest.",
		}, labels),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_release_watch_last_success_timestamp_seconds",
			Help: "When the newest successful ReleaseWatch run closed.",
		}, nil),
	}
	reg.MustRegister(c.info, c.behind, c.fixes, c.lastSuccess)
	return c
}

// Set replaces the gauges with res, the run that closed at closed.
func (c *Collector) Set(res watch.Result, closed time.Time) {
	c.info.Reset()
	c.behind.Reset()
	c.fixes.Reset()
	for _, t := range res.Targets {
		c.info.WithLabelValues(t.Name, t.Repo, t.Running, t.Latest).Set(1)
		c.behind.WithLabelValues(t.Name, t.Repo).Set(b2f(t.Behind))
		c.fixes.WithLabelValues(t.Name, t.Repo).Set(float64(len(t.Fixes)))
	}
	c.lastSuccess.WithLabelValues().Set(float64(closed.Unix()))
}

// Run refreshes the gauges from src every interval until ctx ends. On an
// error it keeps the previous values: the last-success timestamp then ages
// and alerts.
func (c *Collector) Run(ctx context.Context, src Source, interval time.Duration) {
	for {
		res, closed, found, err := src(ctx)
		switch {
		case err != nil:
			slog.Error("metrics: newest ReleaseWatch run", "error", err)
		case found:
			c.Set(res, closed)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// TemporalSource reads the newest completed ReleaseWatch run in namespace.
// It relies on the SQL visibility store listing closed runs newest close
// time first; it does not support ORDER BY.
func TemporalSource(tc client.Client, namespace string) Source {
	return func(ctx context.Context) (watch.Result, time.Time, bool, error) {
		var res watch.Result
		resp, err := tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Namespace: namespace,
			PageSize:  1,
			Query:     "WorkflowType = '" + watch.WorkflowType + "' AND ExecutionStatus = 'Completed'",
		})
		if err != nil {
			return res, time.Time{}, false, err
		}
		if len(resp.Executions) == 0 {
			return res, time.Time{}, false, nil
		}
		e := resp.Executions[0]
		if e.CloseTime == nil {
			return res, time.Time{}, false, errors.New("completed run without a close time")
		}
		if err := tc.GetWorkflow(ctx, e.Execution.WorkflowId, e.Execution.RunId).Get(ctx, &res); err != nil {
			return res, time.Time{}, false, err
		}
		return res, e.CloseTime.AsTime(), true, nil
	}
}
