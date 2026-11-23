// glkvm-cert keeps a Let's Encrypt certificate current on the GL.iNet KVM
// itself: it runs on the device, proves the name by DNS-01 with an RFC2136
// update to Knot, and writes the pair where the device's nginx reads it.
package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/syslog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/crypto/acme"
)

type config struct {
	// Domain is the one name the certificate covers.
	Domain string `json:"domain"`
	// Directory is the ACME directory URL.
	Directory string `json:"directory"`
	// Nameserver (host:port) takes the RFC2136 update for Zone, signed with
	// the TSIG key.
	Nameserver string `json:"nameserver"`
	Zone       string `json:"zone"`
	TSIGKey    string `json:"tsig_key"`
	TSIGSecret string `json:"tsig_secret"`
	// CertFile and KeyFile are what nginx serves.
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
	// AccountKeyFile holds the ACME account key; it is created on first use.
	AccountKeyFile string `json:"account_key_file"`
	// NginxPIDFile names the nginx master, which gets SIGHUP after a new
	// certificate is written.
	NginxPIDFile string `json:"nginx_pid_file"`
}

func loadConfig(path string) (*config, error) {
	cfg := &config{
		Directory:    acme.LetsEncryptURL,
		CertFile:     "/etc/kvmd/user/ssl/server.crt",
		KeyFile:      "/etc/kvmd/user/ssl/server.key",
		NginxPIDFile: "/run/kvmd/nginx.pid",
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.AccountKeyFile == "" {
		cfg.AccountKeyFile = filepath.Join(filepath.Dir(path), "account.key")
	}
	for name, v := range map[string]string{
		"domain": cfg.Domain, "nameserver": cfg.Nameserver, "zone": cfg.Zone,
		"tsig_key": cfg.TSIGKey, "tsig_secret": cfg.TSIGSecret,
	} {
		if v == "" {
			return nil, fmt.Errorf("%s: %q is not set", path, name)
		}
	}
	return cfg, nil
}

// renewalReason says why the pair on disk must be replaced, or "" when it is
// still good: readable, for domain, matching, and with more than a third of
// its lifetime left.
func renewalReason(certPEM, keyPEM []byte, domain string, now time.Time) string {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "no certificate"
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "certificate unreadable: " + err.Error()
	}
	if err := cert.VerifyHostname(domain); err != nil {
		return "certificate is not for " + domain
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return "key unreadable: " + err.Error()
	}
	pub, ok := cert.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !pub.Equal(key.Public()) {
		return "key does not match the certificate"
	}
	left := cert.NotAfter.Sub(now)
	if left < cert.NotAfter.Sub(cert.NotBefore)/3 {
		if left < 0 {
			return "certificate expired " + cert.NotAfter.Format(time.DateOnly)
		}
		return fmt.Sprintf("certificate expires %s, in %d days",
			cert.NotAfter.Format(time.DateOnly), int(left.Hours()/24))
	}
	return ""
}

func parseKey(keyPEM []byte) (crypto.Signer, error) {
	for {
		var block *pem.Block
		block, keyPEM = pem.Decode(keyPEM)
		if block == nil {
			return nil, errors.New("no private key")
		}
		switch block.Type {
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(block.Bytes)
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			s, ok := k.(crypto.Signer)
			if !ok {
				return nil, errors.New("unsupported private key type")
			}
			return s, nil
		}
	}
}

// accountKey loads the ACME account key, creating it the first time.
func accountKey(path string) (crypto.Signer, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		return parseKey(raw)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, pemBytes, err := newKey()
	if err != nil {
		return nil, err
	}
	return key, writeFile(path, pemBytes, 0o600)
}

func newKey() (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// challengeUpdate builds the RFC2136 message that replaces (or, with value
// "", removes) the TXT RRset at _acme-challenge.<domain>.
func challengeUpdate(cfg *config, value string) *dns.Msg {
	name := "_acme-challenge." + dns.Fqdn(cfg.Domain)
	m := new(dns.Msg)
	m.SetUpdate(dns.Fqdn(cfg.Zone))
	m.RemoveRRset([]dns.RR{&dns.TXT{
		Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT},
	}})
	if value != "" {
		m.Insert([]dns.RR{&dns.TXT{
			Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60},
			Txt: []string{value},
		}})
	}
	return m
}

func setChallenge(ctx context.Context, cfg *config, value string) error {
	key := dns.Fqdn(cfg.TSIGKey)
	m := challengeUpdate(cfg, value)
	m.SetTsig(key, dns.HmacSHA256, 300, time.Now().Unix())
	c := &dns.Client{Net: "tcp", TsigSecret: map[string]string{key: cfg.TSIGSecret}}
	reply, _, err := c.ExchangeContext(ctx, m, cfg.Nameserver)
	if err != nil {
		return fmt.Errorf("dns update to %s: %w", cfg.Nameserver, err)
	}
	if reply.Rcode != dns.RcodeSuccess {
		return fmt.Errorf("dns update to %s: %s", cfg.Nameserver, dns.RcodeToString[reply.Rcode])
	}
	return nil
}

// issue orders a certificate for cfg.Domain and returns the chain and its key,
// both PEM.
func issue(ctx context.Context, cfg *config) (certPEM, keyPEM []byte, err error) {
	acct, err := accountKey(cfg.AccountKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("account key: %w", err)
	}
	client := &acme.Client{Key: acct, DirectoryURL: cfg.Directory, UserAgent: "glkvm-cert"}
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, nil, fmt.Errorf("register account: %w", err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(cfg.Domain))
	if err != nil {
		return nil, nil, fmt.Errorf("new order: %w", err)
	}
	for _, url := range order.AuthzURLs {
		if err := authorize(ctx, cfg, client, url); err != nil {
			return nil, nil, err
		}
	}
	if order, err = client.WaitOrder(ctx, order.URI); err != nil {
		return nil, nil, fmt.Errorf("order: %w", err)
	}

	key, keyPEM, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{DNSNames: []string{cfg.Domain}}, key)
	if err != nil {
		return nil, nil, err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, nil, fmt.Errorf("finalize order: %w", err)
	}
	for _, der := range chain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return certPEM, keyPEM, nil
}

func authorize(ctx context.Context, cfg *config, client *acme.Client, url string) error {
	authz, err := client.GetAuthorization(ctx, url)
	if err != nil {
		return fmt.Errorf("authorization: %w", err)
	}
	if authz.Status == acme.StatusValid {
		return nil
	}
	var chal *acme.Challenge
	for _, c := range authz.Challenges {
		if c.Type == "dns-01" {
			chal = c
		}
	}
	if chal == nil {
		return errors.New("the CA offered no dns-01 challenge")
	}
	value, err := client.DNS01ChallengeRecord(chal.Token)
	if err != nil {
		return err
	}
	// Knot is the only authoritative server and has committed the update by
	// the time it answers, so there is no propagation to wait for.
	if err := setChallenge(ctx, cfg, value); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := setChallenge(cleanup, cfg, ""); err != nil {
			log.Printf("removing the challenge record: %v", err)
		}
	}()
	if _, err := client.Accept(ctx, chal); err != nil {
		return fmt.Errorf("accept challenge: %w", err)
	}
	if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
		return fmt.Errorf("validation: %w", err)
	}
	return nil
}

// writeFile replaces path atomically, so a reader never sees half a file.
func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// reloadNginx sends SIGHUP to the nginx master, which re-reads the certificate
// without dropping open sessions. With no nginx running there is nothing to
// do: it reads the files when it starts.
func reloadNginx(pidFile string) error {
	raw, err := os.ReadFile(pidFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return fmt.Errorf("%s: %w", pidFile, err)
	}
	return syscall.Kill(pid, syscall.SIGHUP)
}

// ensure renews the certificate if it needs it (or regardless, with force),
// and reports whether it did.
func ensure(ctx context.Context, cfg *config, force bool) (bool, error) {
	certPEM, _ := os.ReadFile(cfg.CertFile)
	keyPEM, _ := os.ReadFile(cfg.KeyFile)
	reason := renewalReason(certPEM, keyPEM, cfg.Domain, time.Now())
	if reason == "" && force {
		reason = "forced"
	}
	if reason == "" {
		return false, nil
	}
	log.Printf("ordering a certificate for %s: %s", cfg.Domain, reason)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	certPEM, keyPEM, err := issue(ctx, cfg)
	if err != nil {
		return false, err
	}
	// The key goes first: for the instant between the two renames the old
	// certificate sits beside the new key, and both are replaced together in
	// every state a reader can otherwise observe.
	if err := writeFile(cfg.KeyFile, keyPEM, 0o600); err != nil {
		return false, err
	}
	if err := writeFile(cfg.CertFile, certPEM, 0o644); err != nil {
		return false, err
	}
	log.Printf("installed a new certificate for %s", cfg.Domain)
	if err := reloadNginx(cfg.NginxPIDFile); err != nil {
		return true, fmt.Errorf("reload nginx: %w", err)
	}
	return true, nil
}

func main() {
	configPath := flag.String("config", "/etc/kvmd/user/glkvm-cert/config.json", "configuration file")
	once := flag.Bool("once", false, "check once, renew if needed, and exit")
	force := flag.Bool("force", false, "with -once: order a certificate even if the current one is good")
	status := flag.Bool("status", false, "print whether the certificate needs renewing and exit")
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *status {
		certPEM, _ := os.ReadFile(cfg.CertFile)
		keyPEM, _ := os.ReadFile(cfg.KeyFile)
		if reason := renewalReason(certPEM, keyPEM, cfg.Domain, time.Now()); reason != "" {
			fmt.Println("needs renewing:", reason)
			os.Exit(1)
		}
		fmt.Println("ok")
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if *once {
		if _, err := ensure(ctx, cfg, *force); err != nil {
			log.Fatal(err)
		}
		return
	}

	// As a daemon it has no terminal: the boot hook starts it detached, so
	// the log goes to the device's syslog.
	if w, err := syslog.New(syslog.LOG_DAEMON|syslog.LOG_INFO, "glkvm-cert"); err == nil {
		log.SetFlags(0)
		log.SetOutput(w)
	}

	// The check is a file read, so it runs every minute: the device is
	// usually off, and at power-on the network and the clock (NTP) arrive some
	// time after this starts. A failed order backs off, up to an hour, which
	// stays inside Let's Encrypt's failed-validation limit.
	const interval = time.Minute
	backoff := interval
	for {
		wait := interval
		if _, err := ensure(ctx, cfg, false); err != nil {
			log.Printf("%v; next attempt in %s", err, backoff)
			wait = backoff
			backoff = min(backoff*3, time.Hour)
		} else {
			backoff = interval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
