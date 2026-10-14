package matrix

import (
	"context"
	"errors"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"homelab/agents/internal/chat"
	"homelab/agents/internal/session"
)

// TemporalTurns runs turns in the conversations' ChatSession workflows.
type TemporalTurns struct {
	Client client.Client
}

// Send is the message Update, with Update-with-Start, waiting until the
// ChatSession has accepted it. An Update ID it has seen before rejoins that
// turn instead of running another.
func (t *TemporalTurns) Send(ctx context.Context, conv session.Conversation, turnID, text string) (func(context.Context) (string, error), error) {
	start := t.Client.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
		ID:                       chat.WorkflowID(conv.Type, conv.Conversation),
		TaskQueue:                chat.TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, chat.ChatSession, chat.ChatInput{Type: conv.Type, Conversation: conv.Conversation})
	h, err := t.Client.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: start,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateID:     turnID,
			UpdateName:   chat.MessageUpdate,
			Args:         []any{chat.Message{Text: text}},
			WaitForStage: client.WorkflowUpdateStageAccepted,
		},
	})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		var reply chat.Reply
		if err := h.Get(ctx, &reply); err != nil {
			return "", err
		}
		return reply.Text, nil
	}, nil
}

// StopWakeups cancels the conversation's Wakeup workflow. A conversation
// with none is fine.
func (t *TemporalTurns) StopWakeups(ctx context.Context, conv session.Conversation) error {
	err := t.Client.CancelWorkflow(ctx, chat.WakeupID(conv.Type, conv.Conversation), "")
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return nil
	}
	return err
}
