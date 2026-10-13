package habitadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"homelab/agents/internal/token"
)

// AgentToken gets PostgREST tokens from agent-token (k8s/manifests/agents,
// "The token service"): a client_credentials grant whose client assertion is
// the bridge's JWT-SVID, which spiffe-helper keeps fresh in a file. A token is
// reused until a minute before it expires.
type AgentToken struct {
	// URL is agent-token's token endpoint.
	URL string
	// SVIDFile holds the JWT-SVID, with agent-token's audience.
	SVIDFile string
	// Audience is the grant's audience, habit-data.
	Audience string
	HTTP     *http.Client
	Now      func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

func (a *AgentToken) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *AgentToken) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && a.now().Before(a.expires.Add(-time.Minute)) {
		return a.token, nil
	}
	svid, err := os.ReadFile(a.SVIDFile)
	if err != nil {
		return "", fmt.Errorf("JWT-SVID: %w", err)
	}
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {token.AssertionTypeSPIFFE},
		"client_assertion":      {strings.TrimSpace(string(svid))},
		"audience":              {a.Audience},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.URL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hc := a.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var r struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &r); err != nil || resp.StatusCode != http.StatusOK || r.AccessToken == "" {
		return "", fmt.Errorf("agent-token: HTTP %d: %s %s", resp.StatusCode, r.Error, r.Description)
	}
	a.token = r.AccessToken
	a.expires = a.now().Add(time.Duration(r.ExpiresIn) * time.Second)
	return a.token, nil
}
