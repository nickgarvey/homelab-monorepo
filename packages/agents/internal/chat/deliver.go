package chat

import (
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Delivering a wake-up's reply to the user. The Matrix bridge
// (cmd/agents-matrix) serves DeliverActivity on its own task queue, so the
// appservice token stays with it and the worker never calls Matrix.
const (
	DeliverTaskQueue = "agents-matrix"
	DeliverActivity  = "DeliverReply"
	// MatrixPrefix begins the conversation ID of every Matrix room
	// (matrix.Conversation). Other conversations (agents-chat's) have no
	// room to deliver to.
	MatrixPrefix = "matrix-"
)

// DeliverInput is a reply to post in the conversation's room, as the agent.
type DeliverInput struct {
	Type         string
	Conversation string
	Reply        string
	// TxnID is the Matrix transaction ID, the same on every retry, so
	// Synapse posts the reply once.
	TxnID string
}

// DeliverResult says whether the reply was posted. It is not when the agent
// is in no room with the conversation's ID.
type DeliverResult struct {
	Delivered bool
}

var deliverOptions = workflow.ActivityOptions{
	TaskQueue:           DeliverTaskQueue,
	StartToCloseTimeout: time.Minute,
	// While the bridge is down the reply waits for it, up to this long.
	// Retries come within a minute of each other, well inside the time
	// Synapse remembers a transaction ID.
	ScheduleToCloseTimeout: time.Hour,
	RetryPolicy:            &temporal.RetryPolicy{MaximumInterval: time.Minute},
}

// deliver posts a wake-up turn's reply to the conversation's room, if it
// has one. A failure is logged: the reply is in the chat log either way.
func (w *wakeup) deliver(ctx workflow.Context, reply string) {
	log := workflow.GetLogger(ctx)
	if !strings.HasPrefix(w.Conversation, MatrixPrefix) {
		log.Info("Wake-up turn replied; the conversation has no room to post it to", "reply", reply)
		return
	}
	var res DeliverResult
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, deliverOptions), DeliverActivity, DeliverInput{
		Type:         w.Type,
		Conversation: w.Conversation,
		Reply:        reply,
		TxnID:        "wakeup-" + workflow.GetInfo(ctx).WorkflowExecution.RunID,
	}).Get(ctx, &res)
	switch {
	case err != nil:
		log.Warn("Posting the wake-up's reply failed", "reply", reply, "error", err)
	case !res.Delivered:
		log.Warn("Wake-up turn replied; the agent is in no room for the conversation", "reply", reply)
	default:
		log.Info("Wake-up turn replied; posted to the room")
	}
}
