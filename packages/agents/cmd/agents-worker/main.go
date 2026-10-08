// agents-worker runs the agent chat sessions (docs/agent-sessions-design.md,
// section 7): the ChatSession, SessionCoordinator and SandboxReaper workflows
// and their activities, on task queue "agents" in Temporal namespace agents,
// and keeps the sandbox-reaper Schedule that runs SandboxReaper hourly. It
// reaches the OpenShell gateway as user agents-worker, Postgres as role agents
// and RGW as role agents-worker, each with its own certificate or token file
// (k8s/manifests/agents/README.md).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"

	"homelab/agents/internal/archive"
	"homelab/agents/internal/chat"
	"homelab/agents/internal/desired"
	"homelab/agents/internal/gateway"
	"homelab/agents/internal/session"
	"homelab/agents/internal/store"
	"homelab/agents/internal/temporalconn"
)

func main() {
	var (
		tc       temporalconn.Config
		gw       gateway.Client
		pgTLS    store.TLSFiles
		postgres string
		rgw      string
		bucket   string
		state    string
		sessCap  int
	)
	tc.Flags(flag.CommandLine)
	flag.StringVar(&gw.Address, "gateway-address", "openshell.openshell.svc.cluster.local:8080", "OpenShell gateway, host:port")
	flag.StringVar(&gw.Workspace, "gateway-workspace", "agents", "Gateway workspace the sandboxes are in")
	flag.StringVar(&gw.TLS.CertFile, "gateway-cert", "", "Client certificate for the gateway (CN=agents-worker, OU=openshell)")
	flag.StringVar(&gw.TLS.KeyFile, "gateway-key", "", "Its key")
	flag.StringVar(&gw.TLS.CAFile, "gateway-ca", "", "CA of the gateway's own certificate")
	flag.StringVar(&postgres, "postgres", "", "Postgres connection string, without ssl* parameters")
	flag.StringVar(&pgTLS.CertFile, "postgres-cert", "", "Postgres client certificate (CN=agents)")
	flag.StringVar(&pgTLS.KeyFile, "postgres-key", "", "Its key")
	flag.StringVar(&pgTLS.CAFile, "postgres-ca", "", "CA of the Postgres server certificate")
	flag.StringVar(&rgw, "rgw-endpoint", "", "RGW S3 endpoint URL; credentials come from the AWS SDK's environment")
	flag.StringVar(&bucket, "rgw-bucket", "agent-sessions", "Bucket for pi's session files")
	flag.StringVar(&state, "state", "", "Published desired state (desired-state.json of the agents-desired-state ConfigMap)")
	flag.IntVar(&sessCap, "session-cap", chat.DefaultCap, "Most session sandboxes running at once")
	flag.Parse()

	if err := run(&tc, &gw, &pgTLS, postgres, rgw, bucket, state, sessCap); err != nil {
		fmt.Fprintln(os.Stderr, "agents-worker:", err)
		os.Exit(1)
	}
}

func run(tc *temporalconn.Config, gw *gateway.Client, pgTLS *store.TLSFiles, postgres, rgw, bucket, state string, sessCap int) error {
	for name, v := range map[string]string{
		"--gateway-cert": gw.TLS.CertFile, "--gateway-key": gw.TLS.KeyFile, "--gateway-ca": gw.TLS.CAFile,
		"--postgres": postgres, "--postgres-cert": pgTLS.CertFile, "--postgres-key": pgTLS.KeyFile, "--postgres-ca": pgTLS.CAFile,
		"--rgw-endpoint": rgw, "--state": state,
	} {
		if v == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	// Fail at start on a state the activities could not read.
	if _, err := desired.Load(state); err != nil {
		return err
	}

	ctx := context.Background()
	st, err := store.Open(ctx, postgres, pgTLS)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer st.Close()
	arc, err := archive.NewS3(ctx, rgw, bucket)
	if err != nil {
		return fmt.Errorf("rgw: %w", err)
	}
	c, err := tc.Dial()
	if err != nil {
		return fmt.Errorf("temporal: %w", err)
	}
	defer c.Close()

	acts := &chat.Activities{
		Session: &session.Activities{
			Gateway: gw,
			Store:   st,
			Archive: arc,
			// The ConfigMap is read afresh for each activity, so a publish
			// by agents-apply reaches the next turn without a restart.
			State:     func() (*desired.State, error) { return desired.Load(state) },
			Heartbeat: func(ctx context.Context) { activity.RecordHeartbeat(ctx) },
		},
		Client: c,
		Cap:    sessCap,
	}
	w := worker.New(c, chat.TaskQueue, worker.Options{})
	w.RegisterWorkflow(chat.ChatSession)
	w.RegisterWorkflow(chat.SessionCoordinator)
	w.RegisterWorkflow(chat.SandboxReaper)
	if err := chat.EnsureReaperSchedule(ctx, c.ScheduleClient()); err != nil {
		return fmt.Errorf("schedule %s: %w", chat.ReaperScheduleID, err)
	}
	w.RegisterActivity(acts)
	return w.Run(worker.InterruptCh())
}
