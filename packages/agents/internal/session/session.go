// Package session holds the chat session activities (design section 7):
// EnsureSandbox, RunTurn, ArchiveSession, StopSandbox and DeleteSandbox, and
// the reaper's PlanReap, ReapExpired, ReapStuck, ReapOrphan and MarkVanished
// (reap.go). They are plain methods here; the worker registers them with
// Temporal. Each is safe to retry.
package session

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"homelab/agents/internal/archive"
	"homelab/agents/internal/desired"
	"homelab/agents/internal/gateway"
	"homelab/agents/internal/store"
)

// Labels on every sandbox the worker creates. The reaper deletes sandboxes
// with ManagedByLabel whose name no session row holds.
const (
	ManagedByLabel    = "managed-by"
	ManagedBy         = "agents-worker"
	ConversationLabel = "conversation"
)

// SessionDir is where pi keeps its session files in the sandbox: under
// PI_CODING_AGENT_DIR (/sandbox/.pi/agent, set by the image), in the
// directory pi names after its working directory, /sandbox.
const SessionDir = "/sandbox/.pi/agent/sessions/--sandbox--"

// MaxPromptBytes keeps a turn's command under the gateway's 256 KiB limit on
// an exec's command line, with room for quoting. Primed history is cut,
// oldest first, to fit.
const MaxPromptBytes = 200 * 1024

var (
	// ErrSandboxLost: the sandbox went away or stopped under a turn. The
	// workflow runs EnsureSandbox again and retries the turn once.
	ErrSandboxLost = errors.New("sandbox lost during the turn")
	// ErrInvalid: the request can never succeed (an unknown type, a bad
	// conversation ID, a message too long); retrying is pointless.
	ErrInvalid = errors.New("invalid request")
)

// conversationRe is what a conversation ID may be: it is pi's --session-id,
// part of a file name and of a glob in a shell command.
var conversationRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func ValidConversation(id string) error {
	if !conversationRe.MatchString(id) {
		return fmt.Errorf("conversation ID %q: want 1-128 of [A-Za-z0-9._-], starting alphanumeric: %w", id, ErrInvalid)
	}
	return nil
}

type Activities struct {
	Gateway gateway.Gateway
	Store   *store.Store
	Archive archive.Archive
	// State returns the published desired state (the agents-desired-state
	// ConfigMap), read afresh for each activity.
	State func() (*desired.State, error)
	// ReadyTimeout bounds waiting for a created or started sandbox to take
	// commands; 5 minutes if zero.
	ReadyTimeout time.Duration
	// Heartbeat, if set, is called every HeartbeatInterval while pi runs.
	Heartbeat         func(ctx context.Context)
	HeartbeatInterval time.Duration
}

// Conversation names a conversation: its agent type and its ID in the type.
type Conversation struct {
	Type         string
	Conversation string
}

func (c Conversation) validate() error {
	if c.Type == "" {
		return fmt.Errorf("no agent type: %w", ErrInvalid)
	}
	return ValidConversation(c.Conversation)
}

func (a *Activities) agentType(name string) (desired.Type, error) {
	s, err := a.State()
	if err != nil {
		return desired.Type{}, fmt.Errorf("desired state: %w", err)
	}
	t, ok := s.Types[name]
	if !ok {
		return desired.Type{}, fmt.Errorf("agent type %q is not published: %w", name, ErrInvalid)
	}
	return t, nil
}

func (a *Activities) readyTimeout() time.Duration {
	if a.ReadyTimeout == 0 {
		return 5 * time.Minute
	}
	return a.ReadyTimeout
}

// tail returns the end of b, for error messages.
func tail(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}
