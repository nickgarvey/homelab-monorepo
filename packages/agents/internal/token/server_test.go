package token

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/spiffe/go-spiffe/v2/bundle/jwtbundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAudience = "spiffe://home.garvey.sh/ns/agents/sa/agent-token"
	staticToken  = "sk-litellm-0123456789abcdef"
	jwtKey       = "0123456789abcdef0123456789abcdef-postgrest"
	keyID        = "spire-key-1"
)

var home = spiffeid.RequireTrustDomainFromString("home.garvey.sh")

type fixture struct {
	srv *Server
	key *ecdsa.PrivateKey
	now time.Time
}

// newFixture is a server with a litellm-style static grant for pi sandboxes
// and a PostgREST-style JWT grant for habit-helper ones, trusting one SPIRE
// signing key.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	write := func(name, s string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(s), 0o600))
		return p
	}
	cfg, err := Parse([]byte(`
trustDomain: home.garvey.sh
audience: ` + testAudience + `
grants:
  - name: pi-litellm
    path: /agent/pi/
    audience: litellm
    static:
      file: ` + write("litellm-pi", staticToken+"\n") + `
  - name: habit-data
    path: /agent/habit-helper/
    audience: habit-postgrest
    jwt:
      keyFile: ` + write("postgrest-key", jwtKey+"\n") + `
      ttl: 10m
      claims:
        role: habit_agent
`))
	require.NoError(t, err)
	key := newKey(t)
	bundle := jwtbundle.New(home)
	require.NoError(t, bundle.AddJWTAuthority(keyID, key.Public()))
	now := time.Now().Truncate(time.Second)
	return &fixture{
		srv: &Server{Config: cfg, Bundles: bundle, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }},
		key: key,
		now: now,
	}
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return k
}

type svid struct {
	sub string
	aud []string
	exp time.Time
	key crypto.Signer
}

// mint signs a JWT-SVID the way SPIRE does: ES256 here, with the key ID in
// the header and typ JWT.
func mint(t *testing.T, s svid) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: jose.JSONWebKey{Key: s.key, KeyID: keyID}},
		(&jose.SignerOptions{}).WithType("JWT"))
	require.NoError(t, err)
	tok, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   "https://spiffe-oidc.home.garvey.sh",
		Subject:  s.sub,
		Audience: s.aud,
		Expiry:   jwt.NewNumericDate(s.exp),
		IssuedAt: jwt.NewNumericDate(s.exp.Add(-time.Hour)),
	}).Serialize()
	require.NoError(t, err)
	return tok
}

// valid is a fresh SVID for sub with the service's audience.
func (f *fixture) valid(t *testing.T, sub string) string {
	return mint(t, svid{sub: sub, aud: []string{testAudience}, exp: time.Now().Add(time.Hour), key: f.key})
}

// form is the supervisor's request (openshell-core oauth.rs,
// post_oauth_token_grant) with a jwt-spiffe assertion type.
func form(assertion, audience string) url.Values {
	v := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {AssertionTypeSPIFFE},
		"client_assertion":      {assertion},
	}
	if audience != "" {
		v.Set("audience", audience)
	}
	return v
}

func (f *fixture) post(t *testing.T, v url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	return w
}

// success decodes a 200 response strictly into the fields the supervisor
// reads, and checks the token is one it would inject.
func success(t *testing.T, w *httptest.ResponseRecorder) Response {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.ElementsMatch(t, []string{"access_token", "token_type", "expires_in"}, keys(raw))
	var resp Response
	dec := json.NewDecoder(strings.NewReader(w.Body.String()))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&resp))
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.True(t, isToken68(resp.AccessToken), "token68: %q", resp.AccessToken)
	return resp
}

func keys(m map[string]json.RawMessage) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// failure checks an error response is the OAuth JSON the supervisor reports.
func failure(t *testing.T, w *httptest.ResponseRecorder, status int, code, desc string) {
	t.Helper()
	require.Equal(t, status, w.Code, w.Body.String())
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	var e ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
	assert.Equal(t, code, e.Error)
	assert.Contains(t, e.Description, desc)
}

func TestStaticGrant(t *testing.T) {
	f := newFixture(t)
	for _, at := range []string{AssertionTypeSPIFFE, AssertionTypeBearer} {
		v := form(f.valid(t, "spiffe://home.garvey.sh/agent/pi/8921aab8-2cdd-42d5-a0f1-559b011008de"), "litellm")
		v.Set("client_assertion_type", at)
		resp := success(t, f.post(t, v))
		assert.Equal(t, staticToken, resp.AccessToken)
		assert.Equal(t, int64(DefaultLifetime/time.Second), resp.ExpiresIn)
	}
}

func TestJWTGrant(t *testing.T) {
	f := newFixture(t)
	sub := "spiffe://home.garvey.sh/agent/habit-helper/1b7c"
	resp := success(t, f.post(t, form(f.valid(t, sub), "habit-postgrest")))
	assert.Equal(t, int64(600), resp.ExpiresIn)

	tok, err := jwt.ParseSigned(resp.AccessToken, []jose.SignatureAlgorithm{jose.HS256})
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, tok.Claims([]byte(jwtKey), &claims))
	assert.Equal(t, map[string]any{
		"role": "habit_agent",
		"sub":  sub,
		"iat":  float64(f.now.Unix()),
		"exp":  float64(f.now.Add(10 * time.Minute).Unix()),
	}, claims)
	assert.Error(t, tok.Claims([]byte("another-key-another-key-another-key"), &claims), "verifies only with the configured key")
}

func TestExpiredAssertion(t *testing.T) {
	f := newFixture(t)
	a := mint(t, svid{sub: "spiffe://home.garvey.sh/agent/pi/x", aud: []string{testAudience}, exp: time.Now().Add(-5 * time.Minute), key: f.key})
	failure(t, f.post(t, form(a, "litellm")), http.StatusUnauthorized, "invalid_client", "expired")
}

func TestWrongAudience(t *testing.T) {
	f := newFixture(t)
	sub := "spiffe://home.garvey.sh/agent/pi/x"
	for name, aud := range map[string][]string{
		"other":   {"rgw.home.garvey.sh"},
		"extra":   {testAudience, "rgw.home.garvey.sh"},
		"missing": nil,
	} {
		t.Run(name, func(t *testing.T) {
			a := mint(t, svid{sub: sub, aud: aud, exp: time.Now().Add(time.Hour), key: f.key})
			failure(t, f.post(t, form(a, "litellm")), http.StatusUnauthorized, "invalid_client", "audience")
		})
	}
}

func TestWrongTrustDomain(t *testing.T) {
	f := newFixture(t)
	evil := spiffeid.RequireTrustDomainFromString("evil.example")
	a := mint(t, svid{sub: "spiffe://evil.example/agent/pi/x", aud: []string{testAudience}, exp: time.Now().Add(time.Hour), key: f.key})

	// No bundle for the other trust domain.
	failure(t, f.post(t, form(a, "litellm")), http.StatusUnauthorized, "invalid_client", "no bundle found")

	// A bundle for it (federation) still does not make its SVIDs acceptable.
	other := jwtbundle.New(evil)
	require.NoError(t, other.AddJWTAuthority(keyID, f.key.Public()))
	f.srv.Bundles = jwtbundle.NewSet(f.srv.Bundles.(*jwtbundle.Bundle), other)
	failure(t, f.post(t, form(a, "litellm")), http.StatusUnauthorized, "invalid_client", "trust domain evil.example is not home.garvey.sh")
}

func TestBadSignature(t *testing.T) {
	f := newFixture(t)
	// Same key ID, another key.
	a := mint(t, svid{sub: "spiffe://home.garvey.sh/agent/pi/x", aud: []string{testAudience}, exp: time.Now().Add(time.Hour), key: newKey(t)})
	failure(t, f.post(t, form(a, "litellm")), http.StatusUnauthorized, "invalid_client", "")

	// A forged signature on a valid token's claims.
	good := f.valid(t, "spiffe://home.garvey.sh/agent/pi/x")
	parts := strings.Split(good, ".")
	other := strings.Split(mint(t, svid{sub: "spiffe://home.garvey.sh/agent/pi/x", aud: []string{testAudience}, exp: time.Now().Add(time.Hour), key: newKey(t)}), ".")
	failure(t, f.post(t, form(parts[0]+"."+parts[1]+"."+other[2], "litellm")), http.StatusUnauthorized, "invalid_client", "")

	failure(t, f.post(t, form("not-a-jwt", "litellm")), http.StatusUnauthorized, "invalid_client", "")
}

func TestUnmapped(t *testing.T) {
	f := newFixture(t)
	for name, c := range map[string]struct{ sub, aud string }{
		"other type":           {"spiffe://home.garvey.sh/agent/pi-wakeup/x", "litellm"},
		"untyped":              {"spiffe://home.garvey.sh/agent/untyped/x", "litellm"},
		"other audience":       {"spiffe://home.garvey.sh/agent/pi/x", "habit-postgrest"},
		"prefix itself":        {"spiffe://home.garvey.sh/agent/pi", "litellm"},
		"service account":      {"spiffe://home.garvey.sh/ns/agents/sa/agents-worker", "litellm"},
		"audience not granted": {"spiffe://home.garvey.sh/agent/pi/x", "nothing"},
	} {
		t.Run(name, func(t *testing.T) {
			failure(t, f.post(t, form(f.valid(t, c.sub), c.aud)), http.StatusBadRequest, "invalid_target", "no credential for "+c.sub)
		})
	}
}

func TestBadRequests(t *testing.T) {
	f := newFixture(t)
	good := f.valid(t, "spiffe://home.garvey.sh/agent/pi/x")

	v := form(good, "litellm")
	v.Set("grant_type", "password")
	failure(t, f.post(t, v), http.StatusBadRequest, "unsupported_grant_type", "password")

	v = form(good, "litellm")
	v.Set("client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:saml2-bearer")
	failure(t, f.post(t, v), http.StatusBadRequest, "invalid_request", "client_assertion_type")

	failure(t, f.post(t, form("", "litellm")), http.StatusBadRequest, "invalid_request", "client_assertion is required")
	failure(t, f.post(t, form(good, "")), http.StatusBadRequest, "invalid_request", "audience is required")

	v = form(good, "litellm")
	v.Set("scope", "echo")
	failure(t, f.post(t, v), http.StatusBadRequest, "invalid_scope", "")

	v = form(good, "litellm")
	v.Add("audience", "habit-postgrest")
	failure(t, f.post(t, v), http.StatusBadRequest, "invalid_request", "audience given more than once")

	r := httptest.NewRequest(http.MethodGet, "/token?"+form(good, "litellm").Encode(), nil)
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	failure(t, w, http.StatusMethodNotAllowed, "invalid_request", "POST")

	r = httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(`{"grant_type":"client_credentials"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	failure(t, w, http.StatusBadRequest, "invalid_request", "x-www-form-urlencoded")
}

func TestCredentialFileUnreadable(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, os.Remove(f.srv.Config.Grants[0].Static.File))
	w := f.post(t, form(f.valid(t, "spiffe://home.garvey.sh/agent/pi/x"), "litellm"))
	failure(t, w, http.StatusInternalServerError, "server_error", "")
	assert.NotContains(t, w.Body.String(), f.srv.Config.Grants[0].Static.File, "no file names to the caller")
}

func TestReady(t *testing.T) {
	f := newFixture(t)
	assert.NoError(t, f.srv.Ready())
	f.srv.Bundles = jwtbundle.NewSet()
	assert.Error(t, f.srv.Ready())
}
