package token

import (
	"crypto/tls"
	"os"
	"sync"
	"time"
)

// CertFiles serves the TLS certificate in a pair of files, reloading it when
// the certificate file changes: cert-manager renews the Secret, the kubelet
// swaps the volume's files, and the next handshake uses the new one.
type CertFiles struct {
	CertFile, KeyFile string

	mu    sync.Mutex
	mtime time.Time
	cert  *tls.Certificate
}

// GetCertificate is for tls.Config.GetCertificate.
func (c *CertFiles) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := os.Stat(c.CertFile)
	if err != nil {
		if c.cert != nil {
			return c.cert, nil
		}
		return nil, err
	}
	if c.cert != nil && st.ModTime().Equal(c.mtime) {
		return c.cert, nil
	}
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		// Mid-swap the pair may not match; keep serving the last good one.
		if c.cert != nil {
			return c.cert, nil
		}
		return nil, err
	}
	c.cert, c.mtime = &cert, st.ModTime()
	return c.cert, nil
}
