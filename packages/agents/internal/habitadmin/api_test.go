package habitadmin

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var apiKey = []byte("0123456789abcdef0123456789abcdef")

func signed(t *testing.T, key []byte, claims map[string]any) string {
	t.Helper()
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	require.NoError(t, err)
	tok, err := jwt.Signed(s).Claims(claims).Serialize()
	require.NoError(t, err)
	return tok
}

func goodClaims() map[string]any {
	return map[string]any{
		"aud": "habit-admin",
		"sub": "spiffe://home.garvey.sh/agent/habit-admin/abc",
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	}
}

func newAPI() (*API, *fakeData) {
	a, d, _ := newAdmin()
	return &API{
		Admin:    a,
		Key:      func() ([]byte, error) { return apiKey, nil },
		Audience: "habit-admin",
		User:     func() (string, error) { return "@ngarvey:matrix.garvey.sh", nil },
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:      func() time.Time { return now },
	}, d
}

func call(api *API, method, token, body string) (int, map[string]string) {
	req := httptest.NewRequest(method, "/run", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestAPIRunsACommand(t *testing.T) {
	api, _ := newAPI()
	code, out := call(api, "POST", signed(t, apiKey, goodClaims()), `{"command":"habits"}`)
	require.Equal(t, http.StatusOK, code, out)
	assert.Contains(t, out["reply"], "**Houdini**: active")
}

func TestAPIDeleteNeedsYesFromTheSameUser(t *testing.T) {
	api, d := newAPI()
	tok := signed(t, apiKey, goodClaims())
	_, out := call(api, "POST", tok, `{"command":"delete Houdini --permanent"}`)
	assert.Contains(t, out["reply"], "Reply yes")
	assert.Empty(t, d.deleted)
	_, out = call(api, "POST", tok, `{"command":"yes"}`)
	assert.Contains(t, out["reply"], "Deleted Houdini")
	assert.Equal(t, []string{"Houdini"}, d.deleted)
}

func TestAPIRefusesBadTokens(t *testing.T) {
	api, _ := newAPI()
	expired := goodClaims()
	expired["exp"] = now.Add(-time.Minute).Unix()
	wrongAud := goodClaims()
	wrongAud["aud"] = "habit-data"
	noExp := goodClaims()
	delete(noExp, "exp")
	for name, tok := range map[string]string{
		"none":      "",
		"garbage":   "not-a-jwt",
		"other key": signed(t, []byte("fedcba9876543210fedcba9876543210"), goodClaims()),
		"expired":   signed(t, apiKey, expired),
		"audience":  signed(t, apiKey, wrongAud),
		"no exp":    signed(t, apiKey, noExp),
	} {
		code, out := call(api, "POST", tok, `{"command":"habits"}`)
		assert.Equal(t, http.StatusUnauthorized, code, name)
		assert.Empty(t, out["reply"], name)
	}
}

func TestAPIRefusesBadRequests(t *testing.T) {
	api, _ := newAPI()
	tok := signed(t, apiKey, goodClaims())
	code, _ := call(api, "GET", tok, "")
	assert.Equal(t, http.StatusMethodNotAllowed, code)
	code, _ = call(api, "POST", tok, `{"command":"  "}`)
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = call(api, "POST", tok, `{"cmd":"habits"}`)
	assert.Equal(t, http.StatusBadRequest, code)
}
