package habitadmin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// API serves the admin commands over HTTP to the habit-admin agent's sandboxes
// (agents/habit-admin): POST /run with {"command": "<text>"} runs the text as
// the admin chat's commands would, for the one allowed user, and answers
// {"reply": "<text>"}. The sandbox's supervisor puts a token from agent-token
// in the Authorization header: an HS256 JWT signed with Key, carrying
// Audience in aud.
type API struct {
	Admin *Admin
	// Key returns the HS256 key, read afresh so a rotated Secret is picked up.
	Key      func() ([]byte, error)
	Audience string
	// User is the Matrix user the commands act for: whose Habit Helper chat a
	// wake-up is in, and whose "yes" confirms a delete.
	User func() (string, error)
	Log  *slog.Logger
	Now  func() time.Time
}

// maxCommand is the longest command text accepted.
const maxCommand = 4096

// leeway is the clock skew allowed on a token's times.
const leeway = 30 * time.Second

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/run" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		apiError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	sub, err := a.authenticate(r)
	if err != nil {
		a.Log.Warn("Refused an admin API request", "remote", r.RemoteAddr, "error", err)
		apiError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxCommand+1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "body: "+err.Error())
		return
	}
	text := strings.TrimSpace(req.Command)
	if text == "" || len(text) > maxCommand {
		apiError(w, http.StatusBadRequest, fmt.Sprintf("command: give 1 to %d bytes", maxCommand))
		return
	}
	user, err := a.User()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	reply := a.Admin.Run(r.Context(), user, text)
	a.Log.Info("Ran an admin command", "command", strings.Fields(text)[0], "caller", sub)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"reply": reply})
}

// authenticate checks the bearer token and returns its subject, the
// sandbox's SPIFFE ID.
func (a *API) authenticate(r *http.Request) (string, error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		return "", errors.New("no bearer token: the sandbox needs the habit-admin-api provider and the habit-admin agent type")
	}
	key, err := a.Key()
	if err != nil {
		return "", fmt.Errorf("reading the key: %w", err)
	}
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	var c jwt.Claims
	if err := tok.Claims(key, &c); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	if c.Expiry == nil {
		return "", errors.New("token: no exp")
	}
	if err := c.ValidateWithLeeway(jwt.Expected{AnyAudience: jwt.Audience{a.Audience}, Time: a.now()}, leeway); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	return c.Subject, nil
}

func apiError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
