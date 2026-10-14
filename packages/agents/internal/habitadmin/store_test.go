package habitadmin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/agents/internal/habittest"
)

// The store against PostgREST serving the schema, as habit_admin; the
// habit-cli build runs it.
func TestStoreIntegration(t *testing.T) {
	pgrst := habittest.Start(t)
	ctx := context.Background()
	admin := habittest.Token(t, "habit_admin")
	s := &Store{URL: pgrst, Token: func(context.Context) (string, error) { return admin, nil }}

	post := func(path, body string) {
		t.Helper()
		req, _ := http.NewRequest("POST", pgrst+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+admin)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Less(t, resp.StatusCode, 300, path+" "+body)
	}
	post("/habits", `{"name":"walk","cue":"if I finish lunch, then I walk"}`)
	post("/habits", `{"name":"stretch"}`)
	post("/entries", `[{"habit":"walk","day":"2026-10-12","outcome":"done"},{"habit":"walk","day":"2026-10-13","outcome":"missed"}]`)
	post("/notes", `{"habit":"walk","kind":"motivation","body":"fresh air"}`)

	hs, err := s.Habits(ctx)
	require.NoError(t, err)
	require.Len(t, hs, 2)
	assert.Equal(t, "stretch", hs[0].Name)

	require.NoError(t, s.UpdateHabit(ctx, "walk", map[string]any{"archived_at": time.Now().UTC()}))
	hs, err = s.Habits(ctx)
	require.NoError(t, err)
	assert.NotNil(t, hs[1].ArchivedAt, "habit_admin still sees an archived habit")
	assert.Equal(t, "active", hs[1].Status)
	assert.Error(t, s.UpdateHabit(ctx, "nope", map[string]any{"status": "paused"}))

	cs, err := s.Cadences(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, cs["walk"].IntervalDays)
	c, err := s.UpdateCadence(ctx, "walk", map[string]any{"interval_days": 7, "next_check": "2026-10-21", "reason": overrideReason})
	require.NoError(t, err)
	assert.Equal(t, "2026-10-21", c.NextCheck)
	require.NotNil(t, c.Reason)

	es, err := s.Entries(ctx, "walk", "2026-10-13")
	require.NoError(t, err)
	require.Len(t, es, 1)
	assert.Equal(t, "missed", es[0].Outcome)

	n, err := s.Count(ctx, "walk")
	require.NoError(t, err)
	assert.Equal(t, Deleted{Entries: 2, Notes: 1}, n)
	d, err := s.Delete(ctx, "walk")
	require.NoError(t, err)
	assert.Equal(t, Deleted{Entries: 2, Notes: 1}, d)
	hs, err = s.Habits(ctx)
	require.NoError(t, err)
	require.Len(t, hs, 1)
	_, err = s.Delete(ctx, "walk")
	assert.ErrorContains(t, err, "no habit walk")
}

func TestAgentTokenCachesUntilNearExpiry(t *testing.T) {
	svid := filepath.Join(t.TempDir(), "jwt_svid.token")
	require.NoError(t, os.WriteFile(svid, []byte("the-svid\n"), 0o600))
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "client_credentials", r.Form.Get("grant_type"))
		assert.Equal(t, "the-svid", r.Form.Get("client_assertion"))
		assert.Equal(t, "habit-data", r.Form.Get("audience"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":300}`))
	}))
	defer srv.Close()
	clock := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	a := &AgentToken{URL: srv.URL, SVIDFile: svid, Audience: "habit-data", Now: func() time.Time { return clock }}
	for range 3 {
		tok, err := a.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "tok", tok)
	}
	assert.Equal(t, 1, calls)
	clock = clock.Add(4*time.Minute + time.Second)
	_, err := a.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}
