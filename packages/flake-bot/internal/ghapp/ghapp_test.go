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
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600))

	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/app/installations/42/access_tokens", r.URL.Path)
		jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(jwt, ".")
		require.Len(t, parts, 3)
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		require.NoError(t, err)
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		require.NoError(t, rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig))
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c struct {
			Iss string `json:"iss"`
			Iat int64  `json:"iat"`
			Exp int64  `json:"exp"`
		}
		require.NoError(t, json.Unmarshal(claims, &c))
		require.Equal(t, "7", c.Iss)
		require.Equal(t, now.Add(-time.Minute).Unix(), c.Iat)
		require.LessOrEqual(t, c.Exp-now.Unix(), int64(600))
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"tok-%d","expires_at":%q}`, calls, now.Add(time.Hour).Format(time.RFC3339))
	}))
	defer srv.Close()

	app := &App{AppID: 7, InstallationID: 42, KeyFile: keyFile, BaseURL: srv.URL, Client: srv.Client(),
		Now: func() time.Time { return now }}
	ctx := context.Background()
	tok, err := app.Token(ctx)
	require.NoError(t, err)
	require.Equal(t, "tok-1", tok)
	tok, _ = app.Token(ctx)
	require.Equal(t, "tok-1", tok, "cached while it has more than ten minutes left")
	now = now.Add(55 * time.Minute)
	tok, _ = app.Token(ctx)
	require.Equal(t, "tok-2", tok, "renewed close to expiry")
}
