package token

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmptyConfig(t *testing.T) {
	c, err := Parse([]byte("trustDomain: home.garvey.sh\naudience: a\ngrants: []\n"))
	require.NoError(t, err)
	assert.Empty(t, c.Grants)
}

func TestConfigErrors(t *testing.T) {
	dir := t.TempDir()
	file := func(name, s string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(s), 0o600))
		return p
	}
	static := file("static", "sk-abc\n")
	key := file("key", strings.Repeat("k", 32))
	short := file("short", strings.Repeat("k", 31))
	notToken := file("spaces", "two words")

	grant := func(name, path, aud, cred string) string {
		return "  - name: " + name + "\n    path: " + path + "\n    audience: " + aud + "\n" + cred
	}
	st := func(f string) string { return "    static:\n      file: " + f + "\n" }
	jw := func(f, extra string) string { return "    jwt:\n      keyFile: " + f + "\n" + extra }

	for name, c := range map[string]struct{ grants, err string }{
		"overlapping prefixes": {grant("a", "/agent/pi/", "x", st(static)) + grant("b", "/agent/pi/sub/", "x", st(static)), "overlaps grant a"},
		"prefix covers exact":  {grant("a", "/ns/agents/sa/w", "x", st(static)) + grant("b", "/ns/agents/", "x", st(static)), "overlaps grant a"},
		"same exact":           {grant("a", "/ns/a/sa/w", "x", st(static)) + grant("b", "/ns/a/sa/w", "x", st(static)), "overlaps grant a"},
		"untyped":              {grant("a", "/agent/untyped/", "x", st(static)), "sandboxes without an agent type"},
		"covers untyped":       {grant("a", "/agent/", "x", st(static)), "sandboxes without an agent type"},
		"root":                 {grant("a", "/", "x", st(static)), "path must name"},
		"bad path":             {grant("a", "/agent//pi/", "x", st(static)), "path:"},
		"no audience":          {grant("a", "/agent/pi/", `""`, st(static)), "audience is required"},
		"both kinds":           {grant("a", "/agent/pi/", "x", st(static)+jw(key, "")), "exactly one"},
		"neither kind":         {grant("a", "/agent/pi/", "x", ""), "exactly one"},
		"duplicate name":       {grant("a", "/agent/pi/", "x", st(static)) + grant("a", "/agent/q/", "x", st(static)), "name used twice"},
		"missing file":         {grant("a", "/agent/pi/", "x", st(filepath.Join(dir, "nope"))), "no such file"},
		"not token68":          {grant("a", "/agent/pi/", "x", st(notToken)), "not a token68"},
		"short key":            {grant("a", "/agent/pi/", "x", jw(short, "")), "shorter than 32"},
		"reserved claim":       {grant("a", "/agent/pi/", "x", jw(key, "      claims:\n        exp: 1\n")), "exp is set by the service"},
		"long ttl":             {grant("a", "/agent/pi/", "x", jw(key, "      ttl: 2h\n")), "not between"},
		"unknown field":        {grant("a", "/agent/pi/", "x", st(static)+"    extra: 1\n"), "field extra not found"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte("trustDomain: home.garvey.sh\naudience: aud\ngrants:\n" + c.grants))
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.err)
		})
	}

	// The same path for different audiences is fine.
	_, err := Parse([]byte("trustDomain: home.garvey.sh\naudience: aud\ngrants:\n" +
		grant("a", "/agent/pi/", "x", st(static)) + grant("b", "/agent/pi/", "y", jw(key, ""))))
	assert.NoError(t, err)

	_, err = Parse([]byte("trustDomain: spiffe://Bad Domain\naudience: aud\n"))
	assert.ErrorContains(t, err, "trustDomain")
	_, err = Parse([]byte("trustDomain: home.garvey.sh\n"))
	assert.ErrorContains(t, err, "audience is required")
}

func TestToken68(t *testing.T) {
	for s, ok := range map[string]bool{
		"sk-abc_DEF.1~2+3/4": true,
		"abc==":              true,
		"":                   false,
		"==":                 false,
		"a=b":                false,
		"a b":                false,
		"a\r\nX: y":          false,
	} {
		assert.Equal(t, ok, isToken68(s), "%q", s)
	}
}
