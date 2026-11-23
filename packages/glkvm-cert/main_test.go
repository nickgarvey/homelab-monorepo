package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const testDomain = "glkvm.home.garvey.sh"

func selfSigned(t *testing.T, key *ecdsa.PrivateKey, name string, notBefore, notAfter time.Time) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestRenewalReason(t *testing.T) {
	key, keyPEM, err := newKey()
	if err != nil {
		t.Fatal(err)
	}
	_, otherKeyPEM, err := newKey()
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	expires := issued.Add(90 * 24 * time.Hour)
	good := selfSigned(t, key, testDomain, issued, expires)

	tests := []struct {
		name    string
		cert    []byte
		key     []byte
		now     time.Time
		want    string // substring of the reason; "" means no renewal
		wantNot bool
	}{
		{name: "fresh", cert: good, key: keyPEM, now: issued.Add(24 * time.Hour)},
		{name: "two thirds used", cert: good, key: keyPEM, now: issued.Add(59 * 24 * time.Hour)},
		{name: "last third", cert: good, key: keyPEM, now: issued.Add(61 * 24 * time.Hour), want: "in 29 days"},
		{name: "expired", cert: good, key: keyPEM, now: expires.Add(time.Hour), want: "expired"},
		// A clock that has not been set yet must not trigger an order.
		{name: "clock before issuance", cert: good, key: keyPEM, now: time.Unix(0, 0)},
		{name: "missing", now: issued, want: "no certificate"},
		{name: "wrong name", cert: selfSigned(t, key, "localhost", issued, expires), key: keyPEM, now: issued, want: "not for"},
		{name: "wrong key", cert: good, key: otherKeyPEM, now: issued, want: "does not match"},
		{name: "no key", cert: good, now: issued, want: "key unreadable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renewalReason(tt.cert, tt.key, testDomain, tt.now)
			if tt.want == "" && got != "" {
				t.Errorf("renewalReason = %q, want none", got)
			}
			if tt.want != "" && !strings.Contains(got, tt.want) {
				t.Errorf("renewalReason = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

// TestSetChallenge runs the update against a server that, like Knot, requires
// the TSIG signature.
func TestSetChallenge(t *testing.T) {
	const keyName, secret = "acme-glkvm.", "c2VjcmV0LXNlY3JldC1zZWNyZXQtc2VjcmV0LXNlY3I="
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *dns.Msg, 1)
	srv := &dns.Server{
		Listener:   ln,
		TsigSecret: map[string]string{keyName: secret},
		// The library's default refuses dynamic updates outright.
		MsgAcceptFunc: func(dns.Header) dns.MsgAcceptAction { return dns.MsgAccept },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			reply := new(dns.Msg).SetReply(r)
			if r.IsTsig() == nil || w.TsigStatus() != nil {
				reply.Rcode = dns.RcodeNotAuth
			} else {
				reply.SetTsig(keyName, dns.HmacSHA256, 300, time.Now().Unix())
				got <- r
			}
			w.WriteMsg(reply)
		}),
	}
	go srv.ActivateAndServe()
	defer srv.Shutdown()

	cfg := &config{
		Domain: testDomain, Zone: "home.garvey.sh", Nameserver: ln.Addr().String(),
		TSIGKey: "acme-glkvm", TSIGSecret: secret,
	}
	if err := setChallenge(context.Background(), cfg, "token-digest"); err != nil {
		t.Fatal(err)
	}
	m := <-got
	if m.Opcode != dns.OpcodeUpdate || m.Question[0].Name != "home.garvey.sh." {
		t.Fatalf("not an update for the zone: %v", m)
	}
	// m.Ns also carries nothing but the update; the TSIG record is in Extra.
	if len(m.Ns) != 2 {
		t.Fatalf("want a delete and an insert, got %v", m.Ns)
	}
	for _, rr := range m.Ns {
		if rr.Header().Name != "_acme-challenge."+testDomain+"." || rr.Header().Rrtype != dns.TypeTXT {
			t.Errorf("update touches %s %s", rr.Header().Name, dns.TypeToString[rr.Header().Rrtype])
		}
	}
	if txt := m.Ns[1].(*dns.TXT).Txt; len(txt) != 1 || txt[0] != "token-digest" {
		t.Errorf("inserted TXT = %v", txt)
	}

	cfg.TSIGSecret = "d3Jvbmctd3Jvbmctd3Jvbmctd3Jvbmctd3Jvbmctd3I="
	if err := setChallenge(context.Background(), cfg, "x"); err == nil {
		t.Error("an update signed with the wrong secret succeeded")
	}
}
