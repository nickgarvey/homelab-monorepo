// ceph-backup-worker backs up everything stored in Ceph (RBD images, RGW
// buckets and metadata, CephFS) to the Kopia repository on Garage and copies
// it to Storj: the CephBackup workflow and its activities, on task queue
// ceph-backup in Temporal namespace ceph-backup. It keeps the ceph-backup
// Schedule (daily, created paused) and, on the pod holding the
// ceph-backup-metrics Lease, the backup metrics. It runs on the storage
// nodes as a privileged pod, since mapping RBD images and mounting
// filesystems is kernel work.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/uber-go/tally/v4"
	tallyprom "github.com/uber-go/tally/v4/prometheus"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	sdktally "go.temporal.io/sdk/contrib/tally"
	"go.temporal.io/sdk/worker"

	"homelab/ceph-backup-worker/internal/backup"
	"homelab/ceph-backup-worker/internal/cmdrun"
	"homelab/ceph-backup-worker/internal/metrics"
	"homelab/ceph-backup-worker/internal/temporalconn"
)

type options struct {
	tc              temporalconn.Config
	cfg             backup.Config
	nodeName        string
	metricsAddr     string
	metricsInterval time.Duration
	lease           metrics.LeaseConfig
	noLease         bool
}

func main() {
	var o options
	o.tc.Flags(flag.CommandLine)
	c := &o.cfg
	flag.StringVar(&c.CephConf, "ceph-conf", "", "ceph.conf for the Ceph CLIs and mount.ceph (mon_host, ms_*_mode)")
	flag.StringVar(&c.CephID, "ceph-id", "ceph-backup", "cephx user, without client.")
	flag.StringVar(&c.CephKeyring, "ceph-keyring", "", "Its keyring")
	flag.StringVar(&c.RGWEndpoint, "rgw-endpoint", "", "RGW S3 and admin-ops endpoint URL; S3 credentials come from STS through the AWS SDK's environment")
	flag.StringVar(&c.RGWAdminDir, "rgw-admin-dir", "", "Directory with the admin-ops user's access_key and secret_key")
	flag.StringVar(&c.RGWMetaPool, "rgw-meta-pool", "default.rgw.meta", "RGW pool with the OIDC provider objects (namespace oidc)")
	flag.StringVar(&c.KopiaDir, "kopia-dir", "", "Directory with garage_key_id, garage_secret_key and kopia_password")
	flag.StringVar(&c.GarageEndpoint, "garage-endpoint", "garage.home.garvey.sh", "Garage S3 endpoint (host)")
	flag.StringVar(&c.GarageBucket, "garage-bucket", "ceph-backup", "Bucket of the Kopia repository")
	flag.StringVar(&c.GarageRegion, "garage-region", "garage", "Garage's region")
	flag.StringVar(&c.StorjDir, "storj-dir", "", "Directory with the Storj gateway's access_key and secret_key")
	flag.StringVar(&c.StorjEndpoint, "storj-endpoint", "gateway.storjshare.io", "Storj hosted S3 gateway (host)")
	flag.StringVar(&c.StorjBucket, "storj-bucket", "ceph-backup", "Bucket of the off-site copy")
	flag.StringVar(&c.StateDir, "state-dir", "/var/cache/ceph-backup-worker", "Kopia's caches, kept across restarts")
	flag.StringVar(&c.RunDir, "run-dir", "/run/ceph-backup-worker", "Pod-local: Kopia's repository configs and logs, short-lived secret files")
	flag.IntVar(&c.KopiaParallel, "kopia-parallel", 4, "Files Kopia uploads at once (0: Kopia's default)")
	flag.StringVar(&o.nodeName, "node-name", "", "The node this pod runs on (spec.nodeName)")
	flag.StringVar(&o.metricsAddr, "metrics-listen", ":9090", "Address of the Prometheus /metrics endpoint")
	flag.DurationVar(&o.metricsInterval, "metrics-interval", 15*time.Minute, "How often the Lease holder recomputes the backup metrics")
	flag.StringVar(&o.lease.Namespace, "lease-namespace", "ceph-backup", "Namespace of the metrics Lease")
	flag.StringVar(&o.lease.Name, "lease-name", "ceph-backup-metrics", "Name of the metrics Lease")
	flag.BoolVar(&o.noLease, "no-metrics-lease", false, "Do not compute the backup metrics (outside Kubernetes)")
	flag.Parse()
	c.MountRoot = "/run"

	if err := run(&o); err != nil {
		slog.Error("ceph-backup-worker", "error", err)
		os.Exit(1)
	}
}

func run(o *options) error {
	c := &o.cfg
	for name, v := range map[string]string{
		"--ceph-conf": c.CephConf, "--ceph-keyring": c.CephKeyring, "--rgw-endpoint": c.RGWEndpoint,
		"--rgw-admin-dir": c.RGWAdminDir, "--kopia-dir": c.KopiaDir, "--storj-dir": c.StorjDir,
		"--node-name": o.nodeName,
	} {
		if v == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	// The RGW metadata export holds users' secret keys.
	syscall.Umask(0o077)
	if err := os.MkdirAll(c.RunDir, 0o700); err != nil {
		return err
	}
	ctx := context.Background()

	// The SDK refreshes the STS credentials once less than the window is
	// left, so each bucket starts with fresh ones.
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("default"),
		awsconfig.WithCredentialsCacheOptions(func(o *aws.CredentialsCacheOptions) { o.ExpiryWindow = 50 * time.Minute }))
	if err != nil {
		return fmt.Errorf("AWS config: %w", err)
	}

	acts := &backup.Activities{
		Cfg:       *c,
		Run:       cmdrun.Exec{},
		Admin:     &backup.AdminOps{Endpoint: c.RGWEndpoint, KeyDir: c.RGWAdminDir},
		Creds:     awsCfg.Credentials.Retrieve,
		Heartbeat: func(ctx context.Context) { activity.RecordHeartbeat(ctx, o.nodeName) },
	}
	// A crash leaves mounts in this pod (gone with it) and RBD mappings on
	// this node (not gone): remove the latter before taking work.
	if err := acts.CleanupLocal(ctx); err != nil {
		slog.Error("startup cleanup", "error", err)
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
	o.tc.MetricsHandler = sdktally.NewMetricsHandler(sdktally.NewPrometheusNamingScope(scope))

	tc, err := o.tc.Dial()
	if err != nil {
		return fmt.Errorf("temporal: %w", err)
	}
	defer tc.Close()
	if err := backup.EnsureSchedule(ctx, tc.ScheduleClient()); err != nil {
		return fmt.Errorf("schedule %s: %w", backup.ScheduleID, err)
	}

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
		err := http.ListenAndServe(o.metricsAddr, mux)
		slog.Error("metrics endpoint", "error", err)
		os.Exit(1)
	}()
	if !o.noLease {
		col := metrics.New(reg)
		src := &metricsSource{Activities: acts, tc: tc, namespace: o.tc.Namespace}
		lease := o.lease
		lease.Identity = o.nodeName
		go func() {
			if err := metrics.RunElected(ctx, lease, func(ctx context.Context) {
				col.Lead(ctx, src, o.metricsInterval)
			}); err != nil {
				slog.Error("metrics lease", "error", err)
			}
		}()
	}

	w := worker.New(tc, backup.TaskQueue, worker.Options{
		Identity: "ceph-backup-worker@" + o.nodeName,
		// Items run one at a time; Verify and the repository steps too.
		MaxConcurrentActivityExecutionSize: 1,
	})
	w.RegisterWorkflow(backup.CephBackup)
	w.RegisterActivity(acts)
	return w.Run(worker.InterruptCh())
}

// metricsSource is metrics.Source: Ceph and the repositories through the
// activities' code, the last run from Temporal's visibility store.
type metricsSource struct {
	*backup.Activities
	tc        client.Client
	namespace string
}

// LastRun relies on the SQL visibility store listing closed runs newest
// close time first; it does not support ORDER BY.
func (m *metricsSource) LastRun(ctx context.Context) (bool, time.Time, bool, error) {
	resp, err := m.tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		Namespace: m.namespace,
		PageSize:  1,
		Query:     "WorkflowType = 'CephBackup' AND ExecutionStatus != 'Running'",
	})
	if err != nil {
		return false, time.Time{}, false, err
	}
	if len(resp.Executions) == 0 {
		return false, time.Time{}, false, nil
	}
	e := resp.Executions[0]
	if e.CloseTime == nil {
		return false, time.Time{}, false, errors.New("finished run without a close time")
	}
	return e.Status == enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, e.CloseTime.AsTime(), true, nil
}
