// Package temporalconn connects the worker to Temporal with its X.509-SVID
// from the host's SPIRE agent, which Temporal's authz table knows by its URI
// SAN (k8s/manifests/temporal/README.md, "Workers").
package temporalconn

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"

	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"go.temporal.io/sdk/client"
)

// Config is where Temporal is.
type Config struct {
	Address    string
	Namespace  string
	ServerName string
	// MetricsHandler receives the SDK's metrics, if set.
	MetricsHandler client.MetricsHandler
}

// Flags registers the connection flags on fs.
func (c *Config) Flags(fs *flag.FlagSet) {
	fs.StringVar(&c.Address, "temporal-address", "frontend.temporal.k8s.home.garvey.sh:7233", "Temporal frontend, host:port")
	fs.StringVar(&c.Namespace, "temporal-namespace", "flake-bot", "Temporal namespace")
	fs.StringVar(&c.ServerName, "temporal-server-name", "frontend.temporal.k8s.home.garvey.sh", "Name to verify on the frontend's certificate")
}

// Dial connects with the SVID from src. The frontend's certificate is
// verified against the trust domain's bundle, which signed it; each TLS
// handshake takes the SVID src holds then, so rotations need no restart.
func (c *Config) Dial(src *workloadapi.X509Source) (client.Client, error) {
	svid, err := src.GetX509SVID()
	if err != nil {
		return nil, fmt.Errorf("X.509-SVID: %w", err)
	}
	bundle, err := src.GetX509BundleForTrustDomain(svid.ID.TrustDomain())
	if err != nil {
		return nil, fmt.Errorf("trust bundle: %w", err)
	}
	pool := x509.NewCertPool()
	for _, ca := range bundle.X509Authorities() {
		pool.AddCert(ca)
	}
	return client.Dial(client.Options{
		HostPort:       c.Address,
		Namespace:      c.Namespace,
		MetricsHandler: c.MetricsHandler,
		ConnectionOptions: client.ConnectionOptions{
			TLS: &tls.Config{
				RootCAs:    pool,
				ServerName: c.ServerName,
				MinVersion: tls.VersionTLS12,
				GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
					s, err := src.GetX509SVID()
					if err != nil {
						return nil, fmt.Errorf("X.509-SVID: %w", err)
					}
					cert := &tls.Certificate{PrivateKey: s.PrivateKey, Leaf: s.Certificates[0]}
					for _, c := range s.Certificates {
						cert.Certificate = append(cert.Certificate, c.Raw)
					}
					return cert, nil
				},
			},
		},
	})
}
