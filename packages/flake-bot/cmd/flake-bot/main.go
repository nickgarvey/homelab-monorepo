// flake-bot keeps homelab-monorepo's dev branch current with
// `nix flake update` and publishes each dev commit's host systems, built
// here and pushed to celler, for hosts to switch to without building (ait
// nix-b77Zn). It runs the UpdateRun and DevBuild workflows on task queue
// flake-bot in Temporal namespace flake-bot, keeps their Schedules, and
// gets its identity (Temporal client certificate, RGW JWT) from the host's
// SPIRE agent, which knows it by its systemd unit, flake-bot.service.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"go.temporal.io/sdk/worker"

	"homelab/flake-bot/internal/bot"
	"homelab/flake-bot/internal/ghapp"
	"homelab/flake-bot/internal/metrics"
	"homelab/flake-bot/internal/temporalconn"
)

type config struct {
	temporal temporalconn.Config
	spire    string

	metricsAddr     string
	metricsInterval time.Duration

	stateDir  string
	remoteURL string
	gitBin    string
	nixBin    string
	system    string
	evalJobs  int
	author    bot.Identity

	appID          int64
	installationID int64
	appKeyFile     string
	githubAPI      string

	cellerBin        string
	cellerCache      string
	cellerConfigHome string

	awsBin     string
	s3Endpoint string
	roleARN    string
	bucket     string
}

func main() {
	var c config
	c.temporal.Flags(flag.CommandLine)
	flag.StringVar(&c.spire, "spire-socket", "unix:///run/spire/agent/public/api.sock", "SPIRE agent Workload API")
	flag.StringVar(&c.stateDir, "state-dir", "/var/lib/flake-bot", "Clone, worktrees and build logs")
	flag.StringVar(&c.remoteURL, "remote", "https://github.com/nickgarvey/homelab-monorepo.git", "The repo")
	flag.StringVar(&c.gitBin, "git", "git", "git")
	flag.StringVar(&c.nixBin, "nix", "nix", "nix")
	flag.StringVar(&c.system, "system", "x86_64-linux", "System whose flake checks are built")
	flag.IntVar(&c.evalJobs, "eval-jobs", 4, "Targets evaluated at once")
	flag.StringVar(&c.author.Name, "author-name", "garvey-homelab-bot[bot]", "Author and committer name")
	flag.StringVar(&c.author.Email, "author-email", "362228766+garvey-homelab-bot[bot]@users.noreply.github.com", "Author and committer email")
	flag.Int64Var(&c.appID, "github-app-id", 5962895, "GitHub App ID")
	flag.Int64Var(&c.installationID, "github-installation-id", 186728033, "The App's installation on the repo")
	flag.StringVar(&c.appKeyFile, "github-app-key", "", "The App's private key (PEM)")
	flag.StringVar(&c.githubAPI, "github-api", "https://api.github.com", "GitHub REST API")
	flag.StringVar(&c.cellerBin, "celler", "celler", "celler")
	flag.StringVar(&c.cellerCache, "celler-cache", "homelab", "Cache to push to")
	flag.StringVar(&c.cellerConfigHome, "celler-config-home", "", "XDG_CONFIG_HOME holding celler/config.toml (with the push token)")
	flag.StringVar(&c.metricsAddr, "metrics-listen", "127.0.0.1:9478", "Address of the Prometheus /metrics endpoint")
	flag.DurationVar(&c.metricsInterval, "metrics-interval", 2*time.Minute, "How often the metrics are read again from Temporal")
	flag.StringVar(&c.awsBin, "aws", "aws", "AWS CLI")
	flag.StringVar(&c.s3Endpoint, "s3-endpoint", "https://rgw.home.garvey.sh", "RGW S3 and STS endpoint")
	flag.StringVar(&c.roleARN, "s3-role", "arn:aws:iam::RGW77873637642885287:role/nix-managed/flake-bot", "STS role for the bucket")
	flag.StringVar(&c.bucket, "s3-bucket", "flake-bot", "Bucket holding manifest.json")
	flag.Parse()
	if err := run(&c); err != nil {
		slog.Error("flake-bot", "error", err)
		os.Exit(1)
	}
}

// jwtSource adapts the Workload API's JWT source to bot.JWTSource.
type jwtSource struct{ src *workloadapi.JWTSource }

func (j jwtSource) JWT(ctx context.Context, audience string) (string, error) {
	s, err := j.src.FetchJWTSVID(ctx, jwtsvid.Params{Audience: audience})
	if err != nil {
		return "", err
	}
	return s.Marshal(), nil
}

func run(c *config) error {
	if c.appKeyFile == "" || c.cellerConfigHome == "" {
		return fmt.Errorf("--github-app-key and --celler-config-home are required")
	}
	ctx := context.Background()
	api := workloadapi.WithClientOptions(workloadapi.WithAddr(c.spire))
	x509src, err := workloadapi.NewX509Source(ctx, api)
	if err != nil {
		return fmt.Errorf("SPIRE X.509 source: %w", err)
	}
	defer x509src.Close()
	jwtsrc, err := workloadapi.NewJWTSource(ctx, api)
	if err != nil {
		return fmt.Errorf("SPIRE JWT source: %w", err)
	}
	defer jwtsrc.Close()

	runDir := filepath.Join(c.stateDir, "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return err
	}
	runner := bot.ExecRunner{}
	app := &ghapp.App{AppID: c.appID, InstallationID: c.installationID, KeyFile: c.appKeyFile,
		BaseURL: c.githubAPI, Client: &http.Client{Timeout: 30 * time.Second}}
	acts := &bot.Activities{
		Git: &bot.Git{Run: runner, Bin: c.gitBin, Dir: filepath.Join(c.stateDir, "repo"),
			RemoteURL: c.remoteURL, GitHub: app, Committer: c.author},
		Nix: &bot.Nix{Run: runner, Bin: c.nixBin, System: c.system, EvalJobs: c.evalJobs,
			GitHub: app, LogDir: filepath.Join(c.stateDir, "logs")},
		Store: &bot.S3Store{Run: runner, AWS: c.awsBin, Endpoint: c.s3Endpoint, RoleARN: c.roleARN,
			Bucket: c.bucket, Key: "manifest.json", SVID: jwtSource{jwtsrc}, TokenDir: runDir},
		CellerBin: c.cellerBin, CellerCache: c.cellerCache, CellerConfigHome: c.cellerConfigHome,
		Run: runner,
	}

	tc, err := c.temporal.Dial(x509src)
	if err != nil {
		return fmt.Errorf("temporal: %w", err)
	}
	defer tc.Close()
	if err := bot.EnsureSchedules(ctx, tc.ScheduleClient()); err != nil {
		return err
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	col := metrics.New(reg)
	go col.Run(ctx, metrics.TemporalSource(tc, c.temporal.Namespace), c.metricsInterval)
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
		err := http.ListenAndServe(c.metricsAddr, mux)
		slog.Error("metrics endpoint", "error", err)
		os.Exit(1)
	}()

	host, _ := os.Hostname()
	w := worker.New(tc, bot.TaskQueue, worker.Options{
		Identity: "flake-bot@" + host,
		// One activity at a time: builds take the whole machine, and the
		// two jobs' git steps share one clone.
		MaxConcurrentActivityExecutionSize: 1,
	})
	w.RegisterWorkflow(bot.UpdateRun)
	w.RegisterWorkflow(bot.DevBuild)
	w.RegisterActivity(acts)
	return w.Run(worker.InterruptCh())
}
