package token

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

// issue makes the grant's credential for id. The files are read for every
// request, so a rotated secret is handed out as soon as its Secret volume
// updates.
func (g *Grant) issue(id spiffeid.ID, now time.Time) (token string, expiresIn time.Duration, err error) {
	if g.Static != nil {
		token, err := readStatic(g.Static.File)
		return token, g.Static.ExpiresIn, err
	}
	key, err := ReadJWTKey(g.JWT.KeyFile)
	if err != nil {
		return "", 0, err
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", 0, err
	}
	claims := maps.Clone(g.JWT.Claims)
	if claims == nil {
		claims = map[string]any{}
	}
	claims["sub"] = id.String()
	claims["iat"] = now.Unix()
	claims["exp"] = now.Add(g.JWT.TTL).Unix()
	token, err = jwt.Signed(signer).Claims(claims).Serialize()
	return token, g.JWT.TTL, err
}

// readStatic reads a static credential: the file's contents without
// surrounding whitespace, which must be a token68 string (RFC 9110), the only
// access tokens the supervisor injects.
func readStatic(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(bytes.TrimSpace(b))
	if !isToken68(s) {
		return "", fmt.Errorf("%s: not a token68 string", path)
	}
	return s, nil
}

// ReadJWTKey reads an HS256 key: the file's contents without surrounding
// whitespace, the same string PostgREST's jwt-secret is set to.
func ReadJWTKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimSpace(b)
	if len(b) < minJWTKeyLen {
		return nil, fmt.Errorf("%s: key shorter than %d bytes", path, minJWTKeyLen)
	}
	return b, nil
}

// isToken68 is the supervisor's check on a returned access token
// (openshell-core oauth.rs, is_token68): letters, digits, -._~+/, then
// optional trailing "=".
func isToken68(s string) bool {
	padding, value := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '=' {
			padding = true
			continue
		}
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' || c == '+' || c == '/'
		if padding || !ok {
			return false
		}
		value = true
	}
	return value
}
