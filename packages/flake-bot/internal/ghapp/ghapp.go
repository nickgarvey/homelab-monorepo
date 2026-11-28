// Package ghapp gets installation tokens for the flake-bot's GitHub App
// (garvey-homelab-bot): a JWT signed with the App's private key buys a
// one-hour token for the App's installation on the repo.
package ghapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// App is a GitHub App installation that tokens are minted for.
type App struct {
	AppID          int64
	InstallationID int64
	// KeyFile is the App's private key (PEM, PKCS#1 as GitHub issues it, or
	// PKCS#8). It is read for every token, so a rotated key needs no restart.
	KeyFile string
	BaseURL string // https://api.github.com
	Client  *http.Client
	// Now is time.Now, replaced in tests.
	Now func() time.Time

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// Token returns an installation token valid for at least ten more minutes,
// minting a new one when the cached one is closer to expiry.
func (a *App) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if a.token != "" && now.Add(10*time.Minute).Before(a.expiry) {
		return a.token, nil
	}
	jwt, err := a.jwt(now)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", a.BaseURL, a.InstallationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := a.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("installation token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("installation token: GitHub answered %s", resp.Status)
	}
	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("installation token: %w", err)
	}
	if body.Token == "" {
		return "", fmt.Errorf("installation token: empty token")
	}
	a.token, a.expiry = body.Token, body.ExpiresAt
	return a.token, nil
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// jwt signs the App's JWT: issued a minute ago against clock skew, valid
// nine minutes (GitHub allows ten).
func (a *App) jwt(now time.Time) (string, error) {
	key, err := readKey(a.KeyFile)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": fmt.Sprint(a.AppID),
	})
	if err != nil {
		return "", err
	}
	signed := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign App JWT: %w", err)
	}
	return signed + "." + enc.EncodeToString(sig), nil
}

func readKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("App key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("App key %s: no PEM block", path)
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("App key %s: %w", path, err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("App key %s: not an RSA key", path)
	}
	return rk, nil
}
