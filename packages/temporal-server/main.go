// Temporal server with certificate-identity authorization: the stock server
// command (adapted from go.temporal.io/server cmd/server/main.go, MIT), plus
// --authz-table, which swaps in the claim mapper and authorizer from ./authz.
// Without --authz-table it behaves like the stock binary.
package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"
	_ "time/tzdata" // embed tzdata as a fallback

	"github.com/urfave/cli/v2"
	"go.temporal.io/server/common/authorization"
	"go.temporal.io/server/common/build"
	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/debug"
	"go.temporal.io/server/common/dynamicconfig"
	"go.temporal.io/server/common/headers"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/log/tag"
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql" // the shared postgres
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/sqlite"     // tests and local runs
	"go.temporal.io/server/temporal"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"homelab/temporal-server/authz"
)

func main() {
	app := buildCLI()
	_ = app.Run(os.Args)
}

func buildCLI() *cli.App {
	app := cli.NewApp()
	app.Name = "temporal-server"
	app.Usage = "Temporal server with certificate-identity authorization"
	app.Version = headers.ServerVersion
	app.ArgsUsage = " "
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "root",
			Aliases: []string{"r"},
			Value:   ".",
			Usage:   "root directory of execution environment",
			EnvVars: []string{config.EnvKeyRoot},
		},
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Value:   "config",
			Usage:   "config dir path relative to root",
			EnvVars: []string{config.EnvKeyConfigDir},
		},
		&cli.StringFlag{
			Name:    "env",
			Aliases: []string{"e"},
			Value:   "development",
			Usage:   "runtime environment",
			EnvVars: []string{config.EnvKeyEnvironment},
		},
		&cli.StringFlag{
			Name:    "zone",
			Aliases: []string{"az"},
			Usage:   "availability zone",
			EnvVars: []string{config.EnvKeyAvailabilityZone, config.EnvKeyAvailabilityZoneTypo},
		},
		&cli.StringFlag{
			Name:    "authz-table",
			Usage:   "YAML table of subject -> roles; enables certificate-identity authorization (see authz/table.go)",
			EnvVars: []string{"TEMPORAL_AUTHZ_TABLE"},
		},
	}

	app.Commands = []*cli.Command{
		{
			Name:      "render-config",
			Usage:     "Render server config",
			ArgsUsage: " ",
			Action: func(c *cli.Context) error {
				cfg, err := config.LoadConfig(c.String("env"), configDir(c), c.String("zone"))
				if err != nil {
					return cli.Exit(fmt.Errorf("unable to load configuration: %w", err), 1)
				}
				fmt.Println(cfg.String())
				return nil
			},
		},
		{
			// For exec probes: the frontend needs a client certificate, and
			// a kubelet gRPC probe can neither present one nor reach loopback.
			Name:      "healthcheck",
			Usage:     "Exit 0 if the given frontend reports SERVING over plaintext gRPC health",
			ArgsUsage: " ",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "address",
					Value: "127.0.0.1:7236",
					Usage: "plaintext frontend to check; the default is the internal frontend",
				},
			},
			Action: healthcheck,
		},
		{
			Name:      "start",
			Usage:     "Start Temporal server",
			ArgsUsage: " ",
			Flags: []cli.Flag{
				&cli.StringSliceFlag{
					Name:    "service",
					Aliases: []string{"svc"},
					Value:   cli.NewStringSlice(temporal.DefaultServices...),
					Usage:   "service(s) to start",
				},
			},
			Before: func(c *cli.Context) error {
				if c.Args().Len() > 0 {
					return cli.Exit("ERROR: start command doesn't support arguments. Use --service flag instead.", 1)
				}
				return nil
			},
			Action: start,
		},
	}
	return app
}

func healthcheck(c *cli.Context) error {
	conn, err := grpc.NewClient(c.String("address"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{
		Service: "temporal.api.workflowservice.v1.WorkflowService",
	})
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return cli.Exit(resp.GetStatus().String(), 1)
	}
	return nil
}

func configDir(c *cli.Context) string {
	return path.Join(c.String("root"), c.String("config"))
}

func start(c *cli.Context) error {
	cfg, err := config.LoadConfig(c.String("env"), configDir(c), c.String("zone"))
	if err != nil {
		return cli.Exit(fmt.Sprintf("Unable to load configuration: %v.", err), 1)
	}

	logger := log.NewZapLogger(log.BuildZapLogger(cfg.Log))
	logger.Info("Build info.",
		tag.NewTimeTag("git-time", build.InfoData.GitTime),
		tag.NewStringTag("git-revision", build.InfoData.GitRevision),
		tag.NewStringTag("go-version", build.InfoData.GoVersion),
		tag.NewStringTag("server-version", headers.ServerVersion),
		tag.NewBoolTag("debug-mode", debug.Enabled),
	)

	var dynamicConfigClient dynamicconfig.Client
	if cfg.DynamicConfigClient != nil {
		dynamicConfigClient, err = dynamicconfig.NewFileBasedClient(cfg.DynamicConfigClient, logger, temporal.InterruptCh())
		if err != nil {
			return cli.Exit(fmt.Sprintf("Unable to create dynamic config client. Error: %v", err), 1)
		}
	} else {
		dynamicConfigClient = dynamicconfig.NewNoopClient()
		logger.Info("Dynamic config client is not configured. Using noop client.")
	}

	authorizer, claimMapper, err := authorizationFrom(c.String("authz-table"), cfg, logger)
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}

	audienceMapper, err := authorization.GetAudienceMapperFromConfig(&cfg.Global.Authorization)
	if err != nil {
		return cli.Exit(fmt.Sprintf("Unable to instantiate audience mapper: %v.", err), 1)
	}

	services := c.StringSlice("service")
	s, err := temporal.NewServer(
		temporal.ForServices(services),
		temporal.WithConfig(cfg),
		temporal.WithDynamicConfigClient(dynamicConfigClient),
		temporal.WithLogger(logger),
		temporal.InterruptOn(temporal.InterruptCh()),
		temporal.WithAuthorizer(authorizer),
		temporal.WithClaimMapper(func(*config.Config) authorization.ClaimMapper { return claimMapper }),
		temporal.WithAudienceGetter(func(*config.Config) authorization.JWTAudienceMapper { return audienceMapper }),
	)
	if err != nil {
		return cli.Exit(fmt.Sprintf("Unable to create server. Error: %v.", err), 1)
	}
	if err := s.Start(); err != nil {
		return cli.Exit(fmt.Sprintf("Unable to start server. Error: %v", err), 1)
	}
	return cli.Exit("All services are stopped.", 0)
}

// authorizationFrom returns the stock authorizer and claim mapper from the
// config when tablePath is empty, and the table-driven pair otherwise. The
// config's own authorizer/claimMapper must then be unset, so it is never
// ambiguous which one runs. The JWT branch is on only if the config lists JWT
// key sources.
func authorizationFrom(tablePath string, cfg *config.Config, logger log.Logger) (authorization.Authorizer, authorization.ClaimMapper, error) {
	authCfg := &cfg.Global.Authorization
	if tablePath == "" {
		authorizer, err := authorization.GetAuthorizerFromConfig(authCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to instantiate authorizer: %w", err)
		}
		claimMapper, err := authorization.GetClaimMapperFromConfig(authCfg, logger)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to instantiate claim mapper: %w", err)
		}
		if authorization.IsNoopAuthorizer(authorizer) {
			logger.Warn("No authorizer: every caller is allowed everything.")
		}
		return authorizer, claimMapper, nil
	}

	if authCfg.Authorizer != "" || authCfg.ClaimMapper != "" {
		return nil, nil, fmt.Errorf("--authz-table is set, so global.authorization.authorizer and claimMapper must be empty (got %q, %q)",
			authCfg.Authorizer, authCfg.ClaimMapper)
	}
	table, err := authz.LoadTable(tablePath)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to load authorization table: %w", err)
	}
	var jwt authorization.ClaimMapper
	if len(authCfg.JWTKeyProvider.KeySourceURIs) > 0 {
		jwt = authorization.NewDefaultJWTClaimMapper(authorization.NewDefaultTokenKeyProvider(authCfg, logger), authCfg, logger)
	}
	logger.Info("Certificate-identity authorization enabled.",
		tag.NewStringTag("authz-table", tablePath),
		tag.NewBoolTag("jwt", jwt != nil),
		tag.NewStringTag("subjects", strings.Join(table.Subjects(), ",")))
	return authz.NewAuthorizer(table), authz.NewClaimMapper(jwt), nil
}
