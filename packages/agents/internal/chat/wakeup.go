package chat

import (
	"context"
	"errors"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"homelab/agents/internal/session"
)

// Names for the Wakeup workflow.
const (
	// TurnSignal reports a finished turn to a Wakeup: a TurnReport.
	TurnSignal = "turn"
	// PendingQuery returns a Wakeup's session.WakeupStatus.
	PendingQuery = "pending"
)

// ErrTypeTurnFailed: a wake-up turn ran and failed. Not retried by
// Temporal, since ChatSession has retried it already: the Wakeup workflow
// tries again in an hour.
const ErrTypeTurnFailed = "TurnFailed"

// floorSteps multiply the type's default wake-up while the user is silent:
// the n-th wake-up in a row with no user message between sets the next one
// floorSteps[n-1] defaults later, the last step repeating.
var floorSteps = []int{1, 1, 1, 3, 7}

// retryAfter is how long after a failed wake-up turn it is tried again.
const retryAfter = time.Hour

// WakeupID is the Wakeup workflow of a conversation.
func WakeupID(typ, conversation string) string {
	return "wakeup/" + typ + "/" + conversation
}

// WakeupInput starts a Wakeup; the fields after Conversation are carried
// across continue-as-new.
type WakeupInput struct {
	Type         string
	Conversation string
	// DefaultSeconds is the type's session.defaultWakeup.
	DefaultSeconds int
	// Pending is the next wake-up; a new Wakeup sets one DefaultSeconds
	// from its start.
	Pending *session.Wakeup
	// Silent counts the wake-ups since the user last wrote.
	Silent int
	// Rejected is why the last turn's wake-up request was refused.
	Rejected string
}

// TurnReport is what CollectOutbox tells a Wakeup after each turn of its
// conversation.
type TurnReport struct {
	// User: the turn was the user's message, not a wake-up.
	User bool
	// Set is the turn's valid wake-up request, if it made one.
	Set *session.Wakeup
	// Rejected is why the turn's request was refused, if it was.
	Rejected string
	// DefaultSeconds is the type's session.defaultWakeup now.
	DefaultSeconds int
}

// floor is how long after a wake-up the next comes when none is requested,
// silent being the wake-ups since the user last wrote, that one included.
func floor(defaultSeconds, silent int) time.Duration {
	i := min(max(silent-1, 0), len(floorSteps)-1)
	return time.Duration(defaultSeconds*floorSteps[i]) * time.Second
}

// Wakeup holds a conversation's one pending wake-up, with workflow ID
// WakeupID(type, conversation). CollectOutbox starts it with
// Signal-with-Start after a turn of a type with wake-ups. When the pending
// time comes it runs a wake-up turn in the ChatSession (RunWakeupTurn) and
// sets the next: the one the turn asked for; or, if the turn failed, the
// same again an hour later; or else the floor, which backs off while the
// user is silent. A request from any turn replaces the pending wake-up; one
// that arrives during a wake-up turn is kept for after it. It
// continues-as-new after each wake-up, and ends when the type no longer has
// wake-ups.
func Wakeup(ctx workflow.Context, in WakeupInput) error {
	w := &wakeup{WakeupInput: in}
	if w.Pending == nil {
		w.Pending = &session.Wakeup{At: workflow.Now(ctx).Add(floor(w.DefaultSeconds, 1)), Note: session.DefaultNote}
	}
	if err := workflow.SetQueryHandler(ctx, PendingQuery, w.status); err != nil {
		return err
	}
	reports := workflow.GetSignalChannel(ctx, TurnSignal)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var r TurnReport
			reports.Receive(ctx, &r)
			w.report(ctx, r)
		}
	})
	next := func() error {
		for {
			var r TurnReport
			if !reports.ReceiveAsync(&r) {
				break
			}
			w.report(ctx, r)
		}
		return workflow.NewContinueAsNewError(ctx, Wakeup, w.WakeupInput)
	}

	for {
		if wait := w.Pending.At.Sub(workflow.Now(ctx)); wait > 0 {
			w.changed = false
			if _, err := workflow.AwaitWithTimeout(ctx, wait, func() bool { return w.changed }); err != nil {
				return err
			}
			if workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
				return next()
			}
			continue
		}

		due := *w.Pending
		w.Pending, w.running, w.setDuring = nil, true, nil
		var res WakeupTurnResult
		err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, wakeupTurnOptions),
			(*Activities).RunWakeupTurn, WakeupTurnInput{Type: w.Type, Conversation: w.Conversation, Wakeup: due}).Get(ctx, &res)
		w.running = false
		log := workflow.GetLogger(ctx)
		switch {
		case err != nil:
			log.Warn("Wake-up turn failed; trying again later", "wakeup", due, "retryAfter", retryAfter, "error", err)
		case res.Disabled:
			log.Info("The type no longer has wake-ups; ending", "type", w.Type)
			return nil
		default:
			w.Silent++
			if res.DefaultSeconds > 0 {
				w.DefaultSeconds = res.DefaultSeconds
			}
			// Nothing delivers a wake-up's reply to the user yet: it is in
			// the chat log, and here.
			if session.IsSilent(res.Reply) {
				log.Info("Wake-up turn ended silent", "wakeup", due)
			} else {
				log.Info("Wake-up turn replied", "wakeup", due, "reply", res.Reply)
			}
		}

		now := workflow.Now(ctx)
		switch {
		case w.setDuring != nil:
			w.Pending = w.setDuring
		case err != nil:
			note := due.Note
			if !strings.HasPrefix(note, "retry: ") {
				note = "retry: " + note
			}
			w.Pending = &session.Wakeup{At: now.Add(retryAfter), Note: note}
		default:
			w.Pending = &session.Wakeup{At: now.Add(floor(w.DefaultSeconds, w.Silent)), Note: session.DefaultNote}
		}
		return next()
	}
}

type wakeup struct {
	WakeupInput
	// running: a wake-up turn is in progress; Pending is nil meanwhile.
	running bool
	// setDuring is a request made while a wake-up turn ran.
	setDuring *session.Wakeup
	// changed is set when Pending changes.
	changed bool
}

func (w *wakeup) report(ctx workflow.Context, r TurnReport) {
	if r.User {
		w.Silent = 0
	}
	if r.DefaultSeconds > 0 {
		w.DefaultSeconds = r.DefaultSeconds
	}
	w.Rejected = r.Rejected
	if r.Set != nil {
		workflow.GetLogger(ctx).Info("Wake-up requested", "wakeup", *r.Set)
		if w.running {
			w.setDuring = r.Set
		} else {
			w.Pending, w.changed = r.Set, true
		}
	}
	if r.Rejected != "" {
		workflow.GetLogger(ctx).Info("Wake-up request refused", "reason", r.Rejected)
	}
}

func (w *wakeup) status() (session.WakeupStatus, error) {
	st := session.WakeupStatus{Pending: w.Pending, Rejected: w.Rejected}
	if w.running {
		st.Pending = w.setDuring
		if st.Pending == nil {
			st.NextFloorSeconds = int(floor(w.DefaultSeconds, w.Silent+1) / time.Second)
		}
	}
	return st, nil
}

// WakeupTurnInput is one wake-up to run.
type WakeupTurnInput struct {
	Type         string
	Conversation string
	Wakeup       session.Wakeup
}

type WakeupTurnResult struct {
	Reply string
	// Disabled: the type is no longer published, or has no wake-ups; no turn
	// ran.
	Disabled bool
	// DefaultSeconds is the type's session.defaultWakeup now.
	DefaultSeconds int
}

var wakeupTurnOptions = workflow.ActivityOptions{
	// The turn waits behind any other in the conversation, and ChatSession
	// retries it; a retry of this activity rejoins the same Update.
	StartToCloseTimeout: 3 * time.Hour,
	HeartbeatTimeout:    2 * time.Minute,
	RetryPolicy: &temporal.RetryPolicy{
		MaximumInterval:        time.Minute,
		MaximumAttempts:        5,
		NonRetryableErrorTypes: []string{ErrTypeInvalid, ErrTypeTurnFailed},
	},
}

// RunWakeupTurn sends the wake-up's prompt to the conversation's ChatSession
// as a message, with Update-with-Start, and returns the reply. The Update ID
// is the Wakeup run's, so a retry gets the same turn's reply and does not
// run a second.
func (a *Activities) RunWakeupTurn(ctx context.Context, in WakeupTurnInput) (WakeupTurnResult, error) {
	t, err := a.Session.AgentType(in.Type)
	if errors.Is(err, session.ErrInvalid) || err == nil && t.Session.DefaultWakeupSeconds == 0 {
		return WakeupTurnResult{Disabled: true}, nil
	}
	if err != nil {
		return WakeupTurnResult{}, err
	}
	loc, err := session.Location(t.Session.Timezone)
	if err != nil {
		return WakeupTurnResult{}, classify(err)
	}

	if a.Session.Heartbeat != nil {
		done := make(chan struct{})
		defer close(done)
		go func() {
			tick := time.NewTicker(10 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					a.Session.Heartbeat(ctx)
				}
			}
		}()
	}
	start := a.Client.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
		ID:                       WorkflowID(in.Type, in.Conversation),
		TaskQueue:                TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, ChatSession, ChatInput{Type: in.Type, Conversation: in.Conversation})
	h, err := a.Client.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: start,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateID:     "wakeup-" + activity.GetInfo(ctx).WorkflowExecution.RunID,
			UpdateName:   MessageUpdate,
			Args:         []any{Message{Text: session.WakeupPrompt(in.Wakeup, loc), Wakeup: true}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	if err != nil {
		return WakeupTurnResult{}, err
	}
	var reply Reply
	if err := h.Get(ctx, &reply); err != nil {
		var app *temporal.ApplicationError
		if errors.As(err, &app) {
			return WakeupTurnResult{}, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeTurnFailed, err)
		}
		return WakeupTurnResult{}, err
	}
	return WakeupTurnResult{Reply: reply.Text, DefaultSeconds: t.Session.DefaultWakeupSeconds}, nil
}

// OutboxInput is the sandbox a turn just ran in.
type OutboxInput struct {
	session.SandboxRef
	// User: the turn was the user's message, not a wake-up.
	User bool
}

// CollectOutbox reads the wake-up request the turn left in the sandbox, if
// any, and reports the turn to the conversation's Wakeup with
// Signal-with-Start, which starts the Wakeup if it is not running. Then it
// removes the request. A retry after the signal sends it again, which
// changes nothing.
func (a *Activities) CollectOutbox(ctx context.Context, in OutboxInput) error {
	t, err := a.Session.AgentType(in.Type)
	if err != nil {
		return classify(err)
	}
	if t.Session.DefaultWakeupSeconds == 0 {
		return nil
	}
	out, err := a.Session.ReadOutbox(ctx, in.SandboxRef)
	if err != nil {
		return classify(err)
	}
	conv := in.Conversation.Conversation
	_, err = a.Client.SignalWithStartWorkflow(ctx, WakeupID(in.Type, conv), TurnSignal,
		TurnReport{User: in.User, Set: out.Request, Rejected: out.Rejected, DefaultSeconds: t.Session.DefaultWakeupSeconds},
		client.StartWorkflowOptions{ID: WakeupID(in.Type, conv), TaskQueue: TaskQueue},
		Wakeup, WakeupInput{Type: in.Type, Conversation: conv, DefaultSeconds: t.Session.DefaultWakeupSeconds})
	if err != nil {
		return err
	}
	if out.Found {
		return classify(a.Session.ClearOutbox(ctx, in.SandboxRef))
	}
	return nil
}

// QueryWakeup returns the conversation's pending wake-up for the prompt
// header, or nil if it has no Wakeup.
func QueryWakeup(ctx context.Context, c client.Client, conv session.Conversation) (*session.WakeupStatus, error) {
	v, err := c.QueryWorkflow(ctx, WakeupID(conv.Type, conv.Conversation), "", PendingQuery)
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st session.WakeupStatus
	if err := v.Get(&st); err != nil {
		return nil, err
	}
	return &st, nil
}
