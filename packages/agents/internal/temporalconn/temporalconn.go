// Package temporalconn connects the worker to Temporal with the pod's
// X.509-SVID, which Temporal's authz table knows by its URI SAN
// (k8s/manifests/temporal/README.md, "Access").
package temporalconn

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"os"

	"go.temporal.io/sdk/client"
)

// Config is where Temporal is and the files that log in to it. spiffe-helper
// rewrites the SVID files as SPIRE rotates them; the certificate is read
// again for every TLS handshake, so a reconnect uses the current one.
type Config struct {
	Address    string
	Namespace  string
	ServerName string
	CertFile   string
	KeyFile    string
	CAFile     string
}

// Flags registers the connection flags on fs.
func (c *Config) Flags(fs *flag.FlagSet) {
	fs.StringVar(&c.Address, "temporal-address", "temporal-frontend.temporal.svc:7233", "Temporal frontend, host:port")
	fs.StringVar(&c.Namespace, "temporal-namespace", "agents", "Temporal namespace")
	fs.StringVar(&c.ServerName, "temporal-server-name", "temporal-frontend.temporal.svc", "Name to verify on the frontend's certificate")
	fs.StringVar(&c.CertFile, "temporal-cert", "", "X.509-SVID certificate (PEM)")
	fs.StringVar(&c.KeyFile, "temporal-key", "", "X.509-SVID key (PEM)")
	fs.StringVar(&c.CAFile, "temporal-ca", "", "SVID trust bundle (PEM), which signed the frontend's certificate")
}

func (c *Config) Dial() (client.Client, error) {
	if c.CertFile == "" || c.KeyFile == "" || c.CAFile == "" {
		return nil, fmt.Errorf("--temporal-cert, --temporal-key and --temporal-ca are required")
	}
	ca, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, fmt.Errorf("temporal CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("temporal CA %s: no certificates", c.CAFile)
	}
	certFile, keyFile := c.CertFile, c.KeyFile
	return client.Dial(client.Options{
		HostPort:  c.Address,
		Namespace: c.Namespace,
		ConnectionOptions: client.ConnectionOptions{
			TLS: &tls.Config{
				RootCAs:    pool,
				ServerName: c.ServerName,
				MinVersion: tls.VersionTLS12,
				GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
					cert, err := tls.LoadX509KeyPair(certFile, keyFile)
					if err != nil {
						return nil, fmt.Errorf("temporal client certificate: %w", err)
					}
					return &cert, nil
				},
			},
		},
	})
}
