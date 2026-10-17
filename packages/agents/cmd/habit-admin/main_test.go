package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunSendsTheCommandAndPrintsTheReply(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/run", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_ = json.NewEncoder(w).Encode(map[string]string{"reply": "- **Houdini**: active"})
	}))
	defer srv.Close()
	reply, err := run(srv.URL, "wakeup in 5m test ping")
	require.NoError(t, err)
	assert.Equal(t, "- **Houdini**: active", reply)
	assert.Equal(t, map[string]string{"command": "wakeup in 5m test ping"}, got)
}

func TestRunExplainsRefusals(t *testing.T) {
	for code, want := range map[int]string{
		http.StatusUnauthorized: "habit-admin-api provider",
		http.StatusBadGateway:   "sandbox proxy",
		http.StatusBadRequest:   "HTTP 400: command: give",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "command: give 1 to 4096 bytes"})
		}))
		_, err := run(srv.URL, "habits")
		srv.Close()
		require.Error(t, err)
		assert.Contains(t, err.Error(), want)
	}
}
