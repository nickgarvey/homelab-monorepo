package chat

import (
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"homelab/agents/internal/session"
)

// DefaultIdleTimeout applies until EnsureSandbox has returned the type's own.
const DefaultIdleTimeout = 6 * time.Hour

// slotGrace is how long past a holder's idle timeout the coordinator keeps
// its slot, for a stop that is slow to report.
const slotGrace = 10 * time.Minute

// MaxMessageBytes bounds a message; the activities refuse a prompt over
// session.MaxPromptBytes in any case.
const MaxMessageBytes = session.MaxPromptBytes

// ChatInput starts a ChatSession. Carry is set only by continue-as-new.
type ChatInput struct {
	Type         string
	Conversation string
	Carry        *Carry
}

// Carry is the state a ChatSession hands its next run.
type Carry struct {
	Sandbox string
	// PrimeSandbox is a sandbox created for this conversation that has not
	// completed a turn yet, so its next turn is still primed.
	PrimeSandbox       string
	LastActivity       time.Time
	IdleTimeoutSeconds int
	HasSlot            bool
}

type Message struct {
	Text string
	// Wakeup: the message is a wake-up's prompt, sent by the Wakeup
	// workflow, not the user's.
	Wakeup bool
}

type Reply struct {
	Text string
}

// ChatSession is one conversation (design section 7), with workflow ID
// WorkflowID(type, conversation). Callers send each message as a
// MessageUpdate, with Update-with-Start and conflict policy UseExisting, and
// get the reply back. Turns run one at a time, in the order the messages
// were accepted; the turn ID is the Update ID. For a type with wake-ups, each
// turn ends with CollectOutbox, and the Wakeup workflow sends its wake-ups
// here as messages too. After the type's idle timeout with nothing in
// flight, or when the coordinator evicts it, it stops the sandbox and
// completes; a message accepted while it stops is still served, and the
// session carries on.
func ChatSession(ctx workflow.Context, in ChatInput) (err error) {
	s := &chat{in: in}
	if c := in.Carry; c != nil {
		s.Carry = *c
	}
	if s.LastActivity.IsZero() {
		s.LastActivity = workflow.Now(ctx)
	}
	if err := workflow.SetUpdateHandlerWithOptions(ctx, MessageUpdate, s.message,
		workflow.UpdateHandlerOptions{Validator: s.validate}); err != nil {
		return err
	}
	evict := workflow.GetSignalChannel(ctx, EvictSignal)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			evict.Receive(ctx, nil)
			s.evicted, s.HasSlot, s.wake = true, false, true
		}
	})
	defer func() {
		// Cancelled: stop the sandbox all the same, outside the cancelled
		// context.
		if temporal.IsCanceledError(err) {
			dctx, cancel := workflow.NewDisconnectedContext(ctx)
			defer cancel()
			s.stopSandbox(dctx)
		}
	}()

	for {
		if err := workflow.Await(ctx, func() bool { return s.idle(ctx) }); err != nil {
			return err
		}
		if s.invalid {
			s.stopSandbox(ctx)
			return nil
		}
		if workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			carry := s.Carry
			return workflow.NewContinueAsNewError(ctx, ChatSession, ChatInput{Type: in.Type, Conversation: in.Conversation, Carry: &carry})
		}
		if s.evicted || workflow.Now(ctx).Sub(s.LastActivity) >= s.idleTimeout() {
			if err := s.stop(ctx); err != nil {
				return err
			}
			// Messages accepted during the stop run next, and start the
			// sandbox again; otherwise the session is over.
			if s.idle(ctx) {
				return nil
			}
			continue
		}
		s.wake = false
		wait := s.idleTimeout() - workflow.Now(ctx).Sub(s.LastActivity)
		if _, err := workflow.AwaitWithTimeout(ctx, wait, func() bool { return s.wake || s.evicted }); err != nil {
			return err
		}
	}
}

type chat struct {
	Carry
	in ChatInput
	// Turns, and the stop, queue in ticket order: next is the ticket the
	// next one takes, serving the one allowed to run.
	next, serving int
	archiving     int
	evicted       bool
	// invalid: the first turn was refused as invalid (an unknown type, a
	// bad conversation ID), which no later message can fix.
	invalid bool
	// wake is set when anything the main loop waits on changes.
	wake bool
	// wakeups: the type has wake-ups, as of the last EnsureSandbox.
	wakeups bool
}

func (s *chat) conversation() session.Conversation {
	return session.Conversation{Type: s.in.Type, Conversation: s.in.Conversation}
}

func (s *chat) idleTimeout() time.Duration {
	if s.IdleTimeoutSeconds <= 0 {
		return DefaultIdleTimeout
	}
	return time.Duration(s.IdleTimeoutSeconds) * time.Second
}

// idle: no turn or stop queued or running, no archive in flight, and no
// Update handler unfinished.
func (s *chat) idle(ctx workflow.Context) bool {
	return s.next == s.serving && s.archiving == 0 && workflow.AllHandlersFinished(ctx)
}

// queue waits for its turn in ticket order, and returns the func that
// passes the turn on.
func (s *chat) queue(ctx workflow.Context) (func(), error) {
	ticket := s.next
	s.next++
	s.wake = true
	done := func() { s.serving++; s.wake = true }
	if err := workflow.Await(ctx, func() bool { return s.serving == ticket }); err != nil {
		done()
		return nil, err
	}
	return done, nil
}

func (s *chat) validate(ctx workflow.Context, m Message) error {
	if m.Text == "" {
		return errors.New("empty message")
	}
	if len(m.Text) > MaxMessageBytes {
		return errors.New("message too long")
	}
	return nil
}

func (s *chat) message(ctx workflow.Context, m Message) (Reply, error) {
	turnID := workflow.GetCurrentUpdateInfo(ctx).ID
	done, err := s.queue(ctx)
	if err != nil {
		return Reply{}, err
	}
	defer done()
	reply, err := s.turn(ctx, turnID, m)
	s.LastActivity = workflow.Now(ctx)
	if err != nil {
		var app *temporal.ApplicationError
		if s.Sandbox == "" && errors.As(err, &app) && app.Type() == ErrTypeInvalid {
			s.invalid = true
		}
		return Reply{}, err
	}
	if s.wakeups {
		s.collectOutbox(ctx, !m.Wakeup)
	}
	s.touch(ctx)
	s.archive(ctx, s.Sandbox)
	return Reply{Text: reply}, nil
}

// turn ensures the sandbox and runs the turn in it, once more in a fresh
// sandbox if the first is lost under it.
func (s *chat) turn(ctx workflow.Context, turnID string, m Message) (string, error) {
	s.acquire(ctx)
	if err := s.ensure(ctx); err != nil {
		return "", err
	}
	reply, err := s.runTurn(ctx, turnID, m)
	var app *temporal.ApplicationError
	if errors.As(err, &app) && app.Type() == ErrTypeSandboxLost {
		workflow.GetLogger(ctx).Warn("Sandbox lost during a turn, retrying in a fresh one", "sandbox", s.Sandbox, "error", err)
		if err := s.ensure(ctx); err != nil {
			return "", err
		}
		reply, err = s.runTurn(ctx, turnID, m)
	}
	if err != nil {
		return "", err
	}
	if s.PrimeSandbox == s.Sandbox {
		s.PrimeSandbox = ""
	}
	return reply, nil
}

func (s *chat) ensure(ctx workflow.Context) error {
	var res session.EnsureResult
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, ensureOptions),
		(*Activities).EnsureSandbox, session.EnsureInput{Conversation: s.conversation()}).Get(ctx, &res)
	if err != nil {
		return err
	}
	s.Sandbox, s.IdleTimeoutSeconds, s.wakeups = res.Sandbox, res.IdleTimeoutSeconds, res.Wakeups
	if res.Created {
		s.PrimeSandbox = res.Sandbox
	}
	return nil
}

func (s *chat) runTurn(ctx workflow.Context, turnID string, m Message) (string, error) {
	var res session.TurnResult
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, turnOptions),
		(*Activities).RunTurn, session.TurnInput{
			Conversation: s.conversation(),
			Sandbox:      s.Sandbox,
			Turn:         turnID,
			Message:      m.Text,
			Wakeup:       m.Wakeup,
			Prime:        s.PrimeSandbox == s.Sandbox,
		}).Get(ctx, &res)
	return res.Reply, err
}

// collectOutbox hands the turn's wake-up request, if any, to the
// conversation's Wakeup workflow, and tells it whether the user wrote. It
// runs before the reply goes back, so the next turn's header shows the
// result. A failure is logged; the pending wake-up stands.
func (s *chat) collectOutbox(ctx workflow.Context, user bool) {
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, outboxOptions),
		(*Activities).CollectOutbox, OutboxInput{
			SandboxRef: session.SandboxRef{Conversation: s.conversation(), Sandbox: s.Sandbox},
			User:       user,
		}).Get(ctx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Warn("Collecting the wake-up request failed", "sandbox", s.Sandbox, "error", err)
	}
}

// archive copies pi's session file to RGW after the reply has gone back.
// A failure is logged; the next turn's archive copies the whole file again.
func (s *chat) archive(ctx workflow.Context, sandbox string) {
	s.archiving++
	workflow.Go(ctx, func(ctx workflow.Context) {
		defer func() { s.archiving--; s.wake = true }()
		err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, archiveOptions),
			(*Activities).ArchiveSession, session.SandboxRef{Conversation: s.conversation(), Sandbox: sandbox}).Get(ctx, nil)
		if err != nil {
			workflow.GetLogger(ctx).Warn("Archiving the session file failed", "sandbox", sandbox, "error", err)
		}
	})
}

// acquire takes a slot under the session cap. A coordinator that cannot be
// reached does not hold up the turn.
func (s *chat) acquire(ctx workflow.Context) {
	if s.HasSlot {
		return
	}
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, slotOptions),
		(*Activities).AcquireSlot, s.slotRequest(ctx)).Get(ctx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Warn("No slot from the session coordinator; running the turn anyway", "error", err)
		return
	}
	s.HasSlot, s.evicted = true, false
}

func (s *chat) slotRequest(ctx workflow.Context) SlotRequest {
	return SlotRequest{
		Holder:  workflow.GetInfo(ctx).WorkflowExecution.ID,
		Expires: workflow.Now(ctx).Add(s.idleTimeout() + slotGrace),
	}
}

func (s *chat) touch(ctx workflow.Context) {
	if s.HasSlot {
		s.signalCoordinator(ctx, TouchSignal, s.slotRequest(ctx))
	}
}

func (s *chat) signalCoordinator(ctx workflow.Context, name string, arg any) {
	if err := workflow.SignalExternalWorkflow(ctx, CoordinatorID, "", name, arg).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Warn("Signalling the session coordinator failed", "signal", name, "error", err)
	}
}

// stop stops the sandbox in its turn, so a message that arrives meanwhile
// waits for the stop and then starts the sandbox again.
func (s *chat) stop(ctx workflow.Context) error {
	done, err := s.queue(ctx)
	if err != nil {
		return err
	}
	defer done()
	s.stopSandbox(ctx)
	s.evicted = false
	s.LastActivity = workflow.Now(ctx)
	return nil
}

func (s *chat) stopSandbox(ctx workflow.Context) {
	if s.Sandbox != "" {
		err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, stopOptions),
			(*Activities).StopSandbox, session.SandboxRef{Conversation: s.conversation(), Sandbox: s.Sandbox}).Get(ctx, nil)
		if err != nil {
			workflow.GetLogger(ctx).Error("Stopping the sandbox failed", "sandbox", s.Sandbox, "error", err)
		}
	}
	if s.HasSlot {
		s.signalCoordinator(ctx, ReleaseSignal, workflow.GetInfo(ctx).WorkflowExecution.ID)
		s.HasSlot = false
	}
}

var (
	ensureOptions = workflow.ActivityOptions{
		// Creating a sandbox and waiting for it to take commands takes
		// ~10 s; EnsureSandbox gives up waiting after 5 minutes.
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumInterval:        30 * time.Second,
			MaximumAttempts:        5,
			NonRetryableErrorTypes: []string{ErrTypeInvalid},
		},
	}
	turnOptions = workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumInterval:        time.Minute,
			MaximumAttempts:        3,
			NonRetryableErrorTypes: []string{ErrTypeInvalid, ErrTypeSandboxLost},
		},
	}
	outboxOptions = workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5, NonRetryableErrorTypes: []string{ErrTypeInvalid}},
	}
	archiveOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5, NonRetryableErrorTypes: []string{ErrTypeInvalid}},
	}
	// A sandbox left running when it should have stopped holds a node's
	// resources and escapes the reaper, so stopping retries for a day.
	stopOptions = workflow.ActivityOptions{
		StartToCloseTimeout:    5 * time.Minute,
		ScheduleToCloseTimeout: 24 * time.Hour,
		RetryPolicy:            &temporal.RetryPolicy{MaximumInterval: 5 * time.Minute, NonRetryableErrorTypes: []string{ErrTypeInvalid}},
	}
	slotOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
)
