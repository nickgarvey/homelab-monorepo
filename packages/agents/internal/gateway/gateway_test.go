package gateway

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The pi policy as agents/desired-state.nix publishes it.
const piPolicy = `{
  "filesystem_policy": {
    "include_workdir": true,
    "read_only": ["/nix/store", "/bin", "/usr", "/etc", "/proc/self", "/dev/urandom"],
    "read_write": ["/sandbox", "/tmp", "/dev/null"]
  },
  "landlock": {"compatibility": "best_effort"},
  "version": 1
}`

func TestPolicy(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal([]byte(piPolicy), &spec); err != nil {
		t.Fatal(err)
	}
	p, err := Policy(spec)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || !p.Filesystem.IncludeWorkdir || len(p.Filesystem.ReadOnly) != 6 ||
		p.Filesystem.ReadWrite[0] != "/sandbox" || p.Landlock.Compatibility != "best_effort" || p.Process != nil {
		t.Errorf("policy: %+v %+v", p, p.Filesystem)
	}

	spec["network_policies"] = map[string]any{"x": map[string]any{}}
	if _, err := Policy(spec); err == nil || !strings.Contains(err.Error(), "network_policies") {
		t.Errorf("network_policies: %v", err)
	}
	if p, err := Policy(nil); p != nil || err != nil {
		t.Errorf("nil policy: %v %v", p, err)
	}
}

// writeCert writes a fresh self-signed certificate and key, as cert-manager
// renewing the Secret would.
func writeCert(t *testing.T, dir string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "agents-worker", OrganizationalUnit: []string{"openshell"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	for name, data := range map[string][]byte{
		"tls.crt": certPEM,
		"tls.key": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		"ca.crt":  certPEM,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClientFollowsRenewedCertificate(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir)
	c := &Client{
		Address:   "https://openshell.invalid:8080",
		Workspace: "agents",
		TLS: TLSFiles{
			CertFile: filepath.Join(dir, "tls.crt"),
			KeyFile:  filepath.Join(dir, "tls.key"),
			CAFile:   filepath.Join(dir, "ca.crt"),
		},
	}
	first, err := c.client()
	if err != nil {
		t.Fatal(err)
	}
	if same, _ := c.client(); same != first {
		t.Error("made a new SDK client for unchanged files")
	}
	writeCert(t, dir)
	renewed, err := c.client()
	if err != nil {
		t.Fatal(err)
	}
	if renewed == first {
		t.Error("kept the SDK client after the certificate changed")
	}
}
