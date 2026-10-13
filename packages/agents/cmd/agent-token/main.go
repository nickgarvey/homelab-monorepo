// agent-token is the OAuth2 token endpoint OpenShell sandboxes, and other
// workloads with a SPIFFE identity, trade a JWT-SVID at for a credential: a
// client_credentials grant whose client assertion is the JWT-SVID, answered
// with the credential the configuration maps the SPIFFE ID and the requested
// audience to (internal/token). It verifies SVIDs with the trust domain's JWT
// bundle from the SPIRE Workload API and serves HTTPS with a certificate from
// files (k8s/manifests/agents/README.md, "The token service").
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spiffe/go-spiffe/v2/workloadapi"

	"homelab/agents/internal/token"
)

func main() {
	var (
		config, listen, workloadAPI string
		certs                       token.CertFiles
	)
	flag.StringVar(&config, "config", "/etc/agent-token/config.yaml", "Configuration file (YAML)")
	flag.StringVar(&listen, "listen", "[::]:443", "HTTPS address")
	flag.StringVar(&certs.CertFile, "tls-cert", "", "Server certificate (PEM)")
	flag.StringVar(&certs.KeyFile, "tls-key", "", "Its key (PEM)")
	flag.StringVar(&workloadAPI, "workload-api", "unix:///spiffe-workload-api/spire-agent.sock", "SPIRE Workload API address")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log, config, listen, workloadAPI, &certs); err != nil {
		log.Error("agent-token failed", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, config, listen, workloadAPI string, certs *token.CertFiles) error {
	if certs.CertFile == "" || certs.KeyFile == "" {
		return errors.New("--tls-cert and --tls-key are required")
	}
	cfg, err := token.Load(config)
	if err != nil {
		return err
	}
	// Fail at start on files that cannot be served.
	if _, err := certs.GetCertificate(nil); err != nil {
		return fmt.Errorf("certificate: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Waits for the first bundle, then follows SPIRE's key rotations.
	bundles, err := workloadapi.NewJWTSource(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(workloadAPI)))
	if err != nil {
		return fmt.Errorf("workload API: %w", err)
	}
	defer bundles.Close()

	srv := &token.Server{Config: cfg, Bundles: bundles, Log: log}
	mux := http.NewServeMux()
	mux.Handle("/token", srv)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := srv.Ready(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	hs := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: certs.GetCertificate,
		},
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdown)
	}()
	log.Info("Serving", "listen", listen, "grants", len(cfg.Grants), "audience", cfg.Audience)
	if err := hs.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
