package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"homelab/agents/internal/gateway"
	"homelab/agents/internal/store"
)

type TurnInput struct {
	Conversation
	Sandbox string
	// Turn identifies the message and its reply, so a retry records each
	// once and does not run pi again once the reply is recorded.
	Turn    string
	Message string
	// Prime puts the conversation's recent messages before the new one: the
	// sandbox was just created and pi's own session is empty.
	Prime bool
}

type TurnResult struct {
	Reply string
}

// RunTurn records the message, runs one `pi -p --session-id <conversation>`
// in the sandbox, and records and returns its reply. An exec refused because
// the sandbox is gone, in phase Error or stopped returns ErrSandboxLost.
func (a *Activities) RunTurn(ctx context.Context, in TurnInput) (TurnResult, error) {
	if err := in.validate(); err != nil {
		return TurnResult{}, err
	}
	conv := in.Conversation.Conversation
	if reply, ok, err := a.Store.TurnMessage(ctx, in.Type, conv, in.Turn, store.RoleAssistant); err != nil || ok {
		return TurnResult{Reply: reply}, err
	}
	if err := a.Store.AddMessage(ctx, in.Type, conv, in.Turn, store.RoleUser, in.Message); err != nil {
		return TurnResult{}, err
	}

	prompt := in.Message
	if in.Prime {
		t, err := a.agentType(in.Type)
		if err != nil {
			return TurnResult{}, err
		}
		history, err := a.Store.Recent(ctx, in.Type, conv, in.Turn, t.Session.PrimeMessages)
		if err != nil {
			return TurnResult{}, err
		}
		prompt = PrimedPrompt(history, in.Message)
	}
	if len(prompt) > MaxPromptBytes {
		return TurnResult{}, fmt.Errorf("message is %d bytes, over %d: %w", len(prompt), MaxPromptBytes, ErrInvalid)
	}

	res, err := a.execHeartbeating(ctx, in.Sandbox, PiArgv(conv, prompt))
	if errors.Is(err, gateway.ErrNotFound) || errors.Is(err, gateway.ErrLost) || errors.Is(err, gateway.ErrNotReady) {
		return TurnResult{}, fmt.Errorf("%w: %v", ErrSandboxLost, err)
	}
	if err != nil {
		return TurnResult{}, err
	}
	if res.ExitCode != 0 {
		return TurnResult{}, fmt.Errorf("pi exited %d: %s", res.ExitCode, tail(res.Stderr, 2000))
	}
	reply := strings.TrimRight(string(res.Stdout), "\n")
	if err := a.Store.AddMessage(ctx, in.Type, conv, in.Turn, store.RoleAssistant, reply); err != nil {
		return TurnResult{}, err
	}
	if err := a.Store.Touch(ctx, in.Type, conv); err != nil {
		return TurnResult{}, err
	}
	return TurnResult{Reply: reply}, nil
}

func (a *Activities) execHeartbeating(ctx context.Context, sandbox string, argv []string) (gateway.ExecResult, error) {
	if a.Heartbeat != nil {
		interval := a.HeartbeatInterval
		if interval == 0 {
			interval = 10 * time.Second
		}
		done := make(chan struct{})
		defer close(done)
		go func() {
			tick := time.NewTicker(interval)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					a.Heartbeat(ctx)
				}
			}
		}()
	}
	return a.Gateway.Exec(ctx, sandbox, argv)
}

// PiArgv is the command for one turn. pi has no "--" to end its options: a
// prompt starting with "-" would be read as an option and one starting with
// "@" as a file to attach, so such a prompt gets a leading space.
func PiArgv(conversation, prompt string) []string {
	if strings.HasPrefix(prompt, "-") || strings.HasPrefix(prompt, "@") {
		prompt = " " + prompt
	}
	return []string{"pi", "-p", "--session-id", conversation, prompt}
}

// PrimedPrompt is a fresh sandbox's first prompt: the earlier messages,
// oldest first, then the new one. The oldest are dropped until it fits in
// MaxPromptBytes.
func PrimedPrompt(history []store.Message, message string) string {
	for {
		if len(history) == 0 {
			return message
		}
		var b strings.Builder
		b.WriteString("This conversation started before this session; its earlier messages, oldest first:\n\n")
		for _, m := range history {
			fmt.Fprintf(&b, "<message role=%q time=%q>\n%s\n</message>\n\n", m.Role, m.CreatedAt.UTC().Format(time.RFC3339), m.Content)
		}
		b.WriteString("The new message:\n\n")
		b.WriteString(message)
		if b.Len() <= MaxPromptBytes {
			return b.String()
		}
		history = history[1:]
	}
}

// ArchiveSession copies pi's session file for the conversation to the
// archive, as <type>/<conversation>/<sandbox>.jsonl. Exactly one file must
// match.
func (a *Activities) ArchiveSession(ctx context.Context, in SandboxRef) error {
	if err := in.validate(); err != nil {
		return err
	}
	// The conversation ID is checked to be safe in a glob and a file name.
	script := fmt.Sprintf(`set -- %s/*_%s.jsonl
[ "$#" -eq 1 ] && [ -f "$1" ] || { echo "want one session file, found: $*" >&2; exit 3; }
exec cat "$1"`, SessionDir, in.Conversation.Conversation)
	res, err := a.Gateway.Exec(ctx, in.Sandbox, []string{"sh", "-c", script})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("reading the session file in %s: exit %d: %s", in.Sandbox, res.ExitCode, tail(res.Stderr, 2000))
	}
	key := fmt.Sprintf("%s/%s/%s.jsonl", in.Type, in.Conversation.Conversation, in.Sandbox)
	if err := a.Archive.Put(ctx, key, res.Stdout); err != nil {
		return fmt.Errorf("archiving %s: %w", key, err)
	}
	return nil
}
