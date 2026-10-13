package token

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/jwtbundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
)

// Client assertion types accepted: OAuth SPIFFE client authentication's
// (what our profiles send) and RFC 7523's, OpenShell's default.
const (
	AssertionTypeSPIFFE = "urn:ietf:params:oauth:client-assertion-type:jwt-spiffe"
	AssertionTypeBearer = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
)

// maxForm bounds a request body; a JWT-SVID is a few hundred bytes.
const maxForm = 64 << 10

// Server answers token requests at its ServeHTTP.
type Server struct {
	Config *Config
	// Bundles holds the trust domain's JWT signing keys: SPIRE's, from the
	// Workload API.
	Bundles jwtbundle.Source
	Log     *slog.Logger
	// Now is the clock for issued tokens; nil means time.Now.
	Now func() time.Time
}

// Response is the successful token response (RFC 6749 section 5.1), the
// fields the supervisor reads (openshell-core oauth.rs, RawTokenResponse).
type Response struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// ErrorResponse is an OAuth error (RFC 6749 section 5.2). The supervisor
// logs both fields, so error_description says what was wrong.
type ErrorResponse struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

type oauthError struct {
	status int
	ErrorResponse
}

func (e *oauthError) Error() string { return e.ErrorResponse.Error + ": " + e.Description }

func fail(status int, code, format string, args ...any) *oauthError {
	return &oauthError{status, ErrorResponse{code, fmt.Sprintf(format, args...)}}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	resp, err := s.grant(w, r)
	if err != nil {
		var oe *oauthError
		if !errors.As(err, &oe) {
			s.Log.Error("Grant failed", "error", err)
			oe = fail(http.StatusInternalServerError, "server_error", "the credential could not be issued")
		}
		reply(w, oe.status, oe.ErrorResponse)
		return
	}
	reply(w, http.StatusOK, resp)
}

func (s *Server) grant(w http.ResponseWriter, r *http.Request) (*Response, error) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		return nil, fail(http.StatusMethodNotAllowed, "invalid_request", "use POST")
	}
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/x-www-form-urlencoded" {
		return nil, fail(http.StatusBadRequest, "invalid_request", "the body must be application/x-www-form-urlencoded")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	if err := r.ParseForm(); err != nil {
		return nil, fail(http.StatusBadRequest, "invalid_request", "unreadable form")
	}
	f := r.PostForm
	for _, k := range []string{"grant_type", "client_assertion_type", "client_assertion", "audience", "scope"} {
		if len(f[k]) > 1 {
			return nil, fail(http.StatusBadRequest, "invalid_request", "%s given more than once", k)
		}
	}
	if gt := f.Get("grant_type"); gt != "client_credentials" {
		return nil, fail(http.StatusBadRequest, "unsupported_grant_type", "grant_type %q is not client_credentials", gt)
	}
	if at := f.Get("client_assertion_type"); at != AssertionTypeSPIFFE && at != AssertionTypeBearer {
		return nil, fail(http.StatusBadRequest, "invalid_request", "client_assertion_type %q is not a JWT-SVID one", at)
	}
	assertion := f.Get("client_assertion")
	if assertion == "" {
		return nil, fail(http.StatusBadRequest, "invalid_request", "client_assertion is required")
	}
	id, err := s.verify(assertion)
	if err != nil {
		s.Log.Warn("Refused client assertion", "error", err)
		return nil, fail(http.StatusUnauthorized, "invalid_client", "%v", err)
	}
	if f.Get("scope") != "" {
		return nil, fail(http.StatusBadRequest, "invalid_scope", "scopes are not supported")
	}
	audience := f.Get("audience")
	if audience == "" {
		return nil, fail(http.StatusBadRequest, "invalid_request", "audience is required")
	}
	g := s.Config.find(id, audience)
	if g == nil {
		s.Log.Warn("No grant", "id", id.String(), "audience", audience)
		return nil, fail(http.StatusBadRequest, "invalid_target", "no credential for %s with audience %s", id, audience)
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	token, expiresIn, err := g.issue(id, now())
	if err != nil {
		return nil, fmt.Errorf("grant %s: %w", g.Name, err)
	}
	s.Log.Info("Granted", "id", id.String(), "audience", audience, "grant", g.Name)
	return &Response{AccessToken: token, TokenType: "Bearer", ExpiresIn: int64(expiresIn / time.Second)}, nil
}

// verify checks a JWT-SVID: signed by a key of the trust domain's bundle,
// not expired, in the configured trust domain, and with the service's
// audience as its only audience, so no other recipient of the same token
// could replay it here.
func (s *Server) verify(assertion string) (spiffeid.ID, error) {
	svid, err := jwtsvid.ParseAndValidate(assertion, s.Bundles, []string{s.Config.Audience})
	if err != nil {
		return spiffeid.ID{}, err
	}
	if td := svid.ID.TrustDomain(); td != s.Config.td {
		return spiffeid.ID{}, fmt.Errorf("trust domain %s is not %s", td, s.Config.td)
	}
	if len(svid.Audience) != 1 {
		return spiffeid.ID{}, fmt.Errorf("JWT-SVID has %d audiences; only %q is accepted", len(svid.Audience), s.Config.Audience)
	}
	return svid.ID, nil
}

// Ready reports whether the trust domain's JWT bundle is at hand.
func (s *Server) Ready() error {
	_, err := s.Bundles.GetJWTBundleForTrustDomain(s.Config.td)
	return err
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
