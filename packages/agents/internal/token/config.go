// Package token is agent-token's OAuth2 token endpoint: it takes a
// client_credentials grant whose client assertion is a JWT-SVID, as OpenShell's
// supervisor sends for a provider profile's token_grant, and answers with the
// credential its configuration maps the caller's SPIFFE ID and the requested
// audience to (k8s/manifests/agents/README.md, "The token service").
package token

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"gopkg.in/yaml.v3"
)

const (
	// DefaultLifetime is a grant's expires_in (static) or token lifetime (jwt)
	// unless it sets its own.
	DefaultLifetime = 5 * time.Minute
	// MaxLifetime is the longest either may be. The supervisor caches a token
	// for at most an hour anyway.
	MaxLifetime = time.Hour
	// minJWTKeyLen is the shortest HS256 key accepted, PostgREST's minimum
	// for jwt-secret.
	minJWTKeyLen = 32
)

// untyped is where SPIRE puts sandboxes created without an agent type
// (agents.garvey.sh/type label). Their creator chose no identity, so no grant
// may cover them.
const untyped = "/agent/untyped/"

// Config is the service's configuration file (YAML).
type Config struct {
	// TrustDomain is the only trust domain whose SVIDs are accepted.
	TrustDomain string `yaml:"trustDomain"`
	// Audience is the JWT-SVID audience the service accepts: what callers put
	// in jwt_svid_audience. An SVID must carry exactly this one audience.
	Audience string `yaml:"audience"`
	// Grants map a SPIFFE ID and a requested audience to a credential. A
	// request matches at most one; Load refuses overlapping grants.
	Grants []Grant `yaml:"grants"`

	td spiffeid.TrustDomain
}

// Grant hands one credential to the SPIFFE IDs under Path that ask for
// Audience. Exactly one of Static and JWT is set.
type Grant struct {
	// Name identifies the grant in logs.
	Name string `yaml:"name"`
	// Path is a SPIFFE ID path in the trust domain. Ending in "/" it is a
	// prefix ("/agent/pi/" matches every pi sandbox, not "/agent/pi-wakeup/x");
	// otherwise the ID's path must equal it ("/ns/agents/sa/agents-worker").
	Path string `yaml:"path"`
	// Audience is the `audience` form value the caller sends, the profile's
	// token_grant.audience.
	Audience string `yaml:"audience"`

	Static *Static `yaml:"static"`
	JWT    *JWT    `yaml:"jwt"`
}

// Static hands out a secret read from a file, such as a litellm virtual key.
type Static struct {
	File string `yaml:"file"`
	// ExpiresIn is the expires_in sent with it, so how long the supervisor
	// caches it before asking again.
	ExpiresIn time.Duration `yaml:"expiresIn"`
}

// JWT hands out an HS256 JWT signed with the key in KeyFile, such as one
// PostgREST accepts. It carries Claims plus sub (the caller's SPIFFE ID), iat
// and exp.
type JWT struct {
	KeyFile string         `yaml:"keyFile"`
	TTL     time.Duration  `yaml:"ttl"`
	Claims  map[string]any `yaml:"claims"`
}

// Claims the service sets itself, which a grant may not.
var reservedClaims = []string{"sub", "iat", "exp", "nbf"}

// Load reads and checks the configuration, including that every credential
// file is readable and valid now.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse checks a configuration given as YAML.
func Parse(b []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := c.check(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, nil
}

func (c *Config) check() error {
	td, err := spiffeid.TrustDomainFromString(c.TrustDomain)
	if err != nil {
		return fmt.Errorf("trustDomain: %w", err)
	}
	c.td = td
	if c.Audience == "" {
		return errors.New("audience is required")
	}
	names := map[string]bool{}
	for i := range c.Grants {
		g := &c.Grants[i]
		if g.Name == "" {
			return fmt.Errorf("grant %d: name is required", i)
		}
		if names[g.Name] {
			return fmt.Errorf("grant %s: name used twice", g.Name)
		}
		names[g.Name] = true
		if err := g.check(td); err != nil {
			return fmt.Errorf("grant %s: %w", g.Name, err)
		}
		for _, o := range c.Grants[:i] {
			if o.Audience == g.Audience && overlap(o.Path, g.Path) {
				return fmt.Errorf("grant %s: overlaps grant %s (audience %s)", g.Name, o.Name, g.Audience)
			}
		}
	}
	return nil
}

func (g *Grant) check(td spiffeid.TrustDomain) error {
	p := strings.TrimSuffix(g.Path, "/")
	if p == "" {
		return errors.New("path must name a SPIFFE ID path below the trust domain")
	}
	if _, err := spiffeid.FromPath(td, p); err != nil {
		return fmt.Errorf("path: %w", err)
	}
	if overlap(g.Path, untyped) {
		return fmt.Errorf("path %s covers %s, sandboxes without an agent type", g.Path, untyped)
	}
	if g.Audience == "" {
		return errors.New("audience is required")
	}
	switch {
	case (g.Static == nil) == (g.JWT == nil):
		return errors.New("exactly one of static and jwt is required")
	case g.Static != nil:
		if g.Static.File == "" {
			return errors.New("static.file is required")
		}
		if err := lifetime(&g.Static.ExpiresIn); err != nil {
			return fmt.Errorf("static.expiresIn: %w", err)
		}
		if _, err := readStatic(g.Static.File); err != nil {
			return err
		}
	default:
		if g.JWT.KeyFile == "" {
			return errors.New("jwt.keyFile is required")
		}
		if err := lifetime(&g.JWT.TTL); err != nil {
			return fmt.Errorf("jwt.ttl: %w", err)
		}
		for _, k := range reservedClaims {
			if _, ok := g.JWT.Claims[k]; ok {
				return fmt.Errorf("jwt.claims: %s is set by the service", k)
			}
		}
		if _, err := ReadJWTKey(g.JWT.KeyFile); err != nil {
			return err
		}
	}
	return nil
}

func lifetime(d *time.Duration) error {
	if *d == 0 {
		*d = DefaultLifetime
	}
	if *d < time.Minute || *d > MaxLifetime {
		return fmt.Errorf("%s is not between 1m and %s", *d, MaxLifetime)
	}
	return nil
}

// matches reports whether a grant's path covers the SPIFFE ID path id.
func matches(path, id string) bool {
	if strings.HasSuffix(path, "/") {
		return strings.HasPrefix(id, path)
	}
	return id == path
}

// overlap reports whether some SPIFFE ID path matches both grant paths.
func overlap(a, b string) bool {
	switch pa, pb := strings.HasSuffix(a, "/"), strings.HasSuffix(b, "/"); {
	case pa && pb:
		return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
	case pa:
		return matches(a, b)
	case pb:
		return matches(b, a)
	default:
		return a == b
	}
}

// find is the grant for a SPIFFE ID and a requested audience, or nil.
func (c *Config) find(id spiffeid.ID, audience string) *Grant {
	for i := range c.Grants {
		g := &c.Grants[i]
		if g.Audience == audience && matches(g.Path, id.Path()) {
			return g
		}
	}
	return nil
}
