// release-watch compares hand-bumped images running in the cluster with
// their projects' GitHub releases and security advisories: the ReleaseWatch
// workflow and its activity, on task queue release-watch in Temporal
// namespace release-watch. It keeps the release-watch Schedule (daily) and
// exports the newest successful run as Prometheus metrics.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/uber-go/tally/v4"
	tallyprom "github.com/uber-go/tally/v4/prometheus"
	sdktally "go.temporal.io/sdk/contrib/tally"
	"go.temporal.io/sdk/worker"

	"homelab/release-watch/internal/metrics"
	"homelab/release-watch/internal/temporalconn"
	"homelab/release-watch/internal/watch"
)

func main() {
	var tc temporalconn.Config
	tc.Flags(flag.CommandLine)
	githubURL := flag.String("github-api", "https://api.github.com", "GitHub REST API base URL")
	metricsAddr := flag.String("metrics-listen", ":9090", "Address of the Prometheus /metrics endpoint")
	metricsInterval := flag.Duration("metrics-interval", 5*time.Minute, "How often the metrics are read again from Temporal")
	flag.Parse()
	if err := run(&tc, *githubURL, *metricsAddr, *metricsInterval); err != nil {
		slog.Error("release-watch", "error", err)
		os.Exit(1)
	}
}

func run(tcfg *temporalconn.Config, githubURL, metricsAddr string, metricsInterval time.Duration) error {
	ctx := context.Background()
	kube, err := watch.InClusterKube()
	if err != nil {
		return fmt.Errorf("kubernetes: %w", err)
	}
	acts := &watch.Activities{
		GitHub: &watch.GitHub{Client: &http.Client{Timeout: 30 * time.Second}, BaseURL: githubURL},
		Kube:   kube,
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		CachedReporter: tallyprom.NewReporter(tallyprom.Options{
			Registerer:      reg,
			OnRegisterError: func(err error) { slog.Error("temporal metric", "error", err) },
		}),
		Separator:       tallyprom.DefaultSeparator,
		SanitizeOptions: &sdktally.PrometheusSanitizeOptions,
	}, time.Second)
	defer closer.Close()
	tcfg.MetricsHandler = sdktally.NewMetricsHandler(sdktally.NewPrometheusNamingScope(scope))

	tc, err := tcfg.Dial()
	if err != nil {
		return fmt.Errorf("temporal: %w", err)
	}
	defer tc.Close()
	if err := watch.EnsureSchedule(ctx, tc.ScheduleClient()); err != nil {
		return fmt.Errorf("schedule %s: %w", watch.ScheduleID, err)
	}

	col := metrics.New(reg)
	go col.Run(ctx, metrics.TemporalSource(tc, tcfg.Namespace), metricsInterval)
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
		err := http.ListenAndServe(metricsAddr, mux)
		slog.Error("metrics endpoint", "error", err)
		os.Exit(1)
	}()

	w := worker.New(tc, watch.TaskQueue, worker.Options{Identity: "release-watch@" + os.Getenv("HOSTNAME")})
	w.RegisterWorkflow(watch.ReleaseWatch)
	w.RegisterActivity(acts)
	return w.Run(worker.InterruptCh())
}
