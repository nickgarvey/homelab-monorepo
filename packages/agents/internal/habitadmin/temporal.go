package habitadmin

import (
	"context"
	"errors"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"homelab/agents/internal/chat"
	"homelab/agents/internal/session"
)

// TemporalWakeups acts on a conversation's Wakeup workflow
// (internal/chat/wakeup.go) as the bridge, for one agent type.
type TemporalWakeups struct {
	Client client.Client
	Type   string
}

func (t *TemporalWakeups) Pending(ctx context.Context, conv string) (*session.WakeupStatus, error) {
	return chat.QueryWakeup(ctx, t.Client, session.Conversation{Type: t.Type, Conversation: conv})
}

// Set replaces the pending wake-up, as a turn's request would, starting the
// Wakeup if the conversation has none.
func (t *TemporalWakeups) Set(ctx context.Context, conv string, w session.Wakeup, defaultSeconds int) error {
	id := chat.WakeupID(t.Type, conv)
	_, err := t.Client.SignalWithStartWorkflow(ctx, id, chat.TurnSignal,
		chat.TurnReport{Set: &w, DefaultSeconds: defaultSeconds},
		client.StartWorkflowOptions{ID: id, TaskQueue: chat.TaskQueue},
		chat.Wakeup, chat.WakeupInput{Type: t.Type, Conversation: conv, DefaultSeconds: defaultSeconds})
	return err
}

// Cancel ends the Wakeup; the conversation's next turn starts a new one.
// It reports whether there was one.
func (t *TemporalWakeups) Cancel(ctx context.Context, conv string) (bool, error) {
	err := t.Client.CancelWorkflow(ctx, chat.WakeupID(t.Type, conv), "")
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return false, nil
	}
	return err == nil, err
}
