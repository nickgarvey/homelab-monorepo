// Package matrix bridges Matrix rooms to agent conversations: a Matrix
// application service, registered with Synapse, that owns one Matrix user
// per agent type (@habit-helper:matrix.garvey.sh). A room is one
// conversation of each agent in it. Text from an allowed user becomes the
// conversation's next message, and the agent's reply is posted back.
//
// The bridge keeps no state of its own: the conversation ID comes from the
// room ID (Conversation), and which agents are in a room is asked of the
// homeserver and cached. Delivery is at most once past the transaction:
// Synapse stops resending a transaction once the bridge has answered it, and
// a turn is handed to Temporal as soon as its event is read, with the event
// ID as the Update ID, so a message read twice still runs one turn.
package matrix

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"homelab/agents/internal/desired"
	"homelab/agents/internal/session"
)

// Homeserver is what the bridge does on Matrix, each call as one of its
// users (an agent's, or the bridge bot's).
type Homeserver interface {
	// Register makes the user if it does not exist, and sets its display
	// name.
	Register(ctx context.Context, user id.UserID, displayName string) error
	Join(ctx context.Context, user id.UserID, room id.RoomID) error
	Leave(ctx context.Context, user id.UserID, room id.RoomID) error
	// Members returns the room's joined members, or ErrNotInRoom if user is
	// not one of them.
	Members(ctx context.Context, user id.UserID, room id.RoomID) ([]id.UserID, error)
	// Invited returns the users invited to the room and not yet joined.
	Invited(ctx context.Context, user id.UserID, room id.RoomID) ([]id.UserID, error)
	// Encrypted: the room has encryption turned on.
	Encrypted(ctx context.Context, user id.UserID, room id.RoomID) (bool, error)
	JoinedRooms(ctx context.Context, user id.UserID) ([]id.RoomID, error)
	// CreateDM makes an unencrypted direct chat from user with invitee, in
	// which invitee cannot turn encryption on.
	CreateDM(ctx context.Context, user, invitee id.UserID) (id.RoomID, error)
	// Send posts content; txnID makes a repeat of the same send a no-op.
	Send(ctx context.Context, user id.UserID, room id.RoomID, content *event.MessageEventContent, txnID string) error
	Typing(ctx context.Context, user id.UserID, room id.RoomID, typing bool, timeout time.Duration) error
	MarkRead(ctx context.Context, user id.UserID, room id.RoomID, evt id.EventID) error
}

// ErrNotInRoom: the user is not joined to the room.
var ErrNotInRoom = errors.New("not in the room")

// Turns runs conversation turns.
type Turns interface {
	// Send hands text to the conversation as its next message, with turnID
	// as its Update ID, and returns once the conversation has accepted it.
	// wait then returns the reply.
	Send(ctx context.Context, conv session.Conversation, turnID, text string) (wait func(context.Context) (string, error), err error)
	// StopWakeups ends the conversation's wake-ups, if it has any.
	StopWakeups(ctx context.Context, conv session.Conversation) error
}

// typingTimeout is how long one typing notice lasts; the bridge renews it
// every typingRenew while a turn runs.
const (
	typingTimeout = 30 * time.Second
	typingRenew   = 20 * time.Second
)

// Bridge handles the events Synapse sends the application service.
type Bridge struct {
	HS    Homeserver
	Turns Turns
	// Server is the homeserver's server name, the part after the colon in
	// user IDs.
	Server string
	// Allowed are the only users the agents talk to: their invites are
	// accepted and their messages answered. Everyone else is ignored.
	Allowed map[id.UserID]bool
	// Agents returns the agent types the bridge serves now: those published
	// whose Matrix user is in the registration's namespace.
	Agents func() (map[string]bool, error)
	Log    *slog.Logger

	mu sync.Mutex
	// members caches which agents are joined to which rooms.
	members map[id.RoomID]map[string]bool
	// encrypted rooms have been told the agents cannot read them.
	encrypted map[id.RoomID]bool
	// typing counts each agent's turns running in each room.
	typing map[typingKey]*typingState
	// inflight is every turn still waiting for its reply.
	inflight sync.WaitGroup
}

type typingKey struct {
	room  id.RoomID
	agent string
}

type typingState struct {
	n    int
	stop chan struct{}
	// send is held for each notice, so a renewal cannot land after the
	// clear.
	send    sync.Mutex
	stopped bool
}

// AgentUser is the Matrix user of an agent type.
func (b *Bridge) AgentUser(typ string) id.UserID {
	return id.NewUserID(typ, b.Server)
}

// agentType is the agent type a user ID belongs to, if it is one the bridge
// serves.
func (b *Bridge) agentType(user id.UserID) (string, bool) {
	local, server, err := user.Parse()
	if err != nil || server != b.Server {
		return "", false
	}
	agents, err := b.Agents()
	if err != nil {
		b.Log.Error("Reading the agent types failed", "error", err)
		return "", false
	}
	return local, agents[local]
}

// Start registers every agent's user and makes sure each allowed user has a
// direct chat with each agent: one the agent is in and the user is in or
// invited to. If not, the agent creates one and invites them.
func (b *Bridge) Start(ctx context.Context) error {
	agents, err := b.Agents()
	if err != nil {
		return err
	}
	for _, typ := range desired.SortedKeys(agents) {
		user := b.AgentUser(typ)
		if err := b.HS.Register(ctx, user, DisplayName(typ)); err != nil {
			return fmt.Errorf("registering %s: %w", user, err)
		}
		for allowed := range b.Allowed {
			if err := b.ensureDM(ctx, typ, allowed); err != nil {
				return fmt.Errorf("direct chat between %s and %s: %w", user, allowed, err)
			}
		}
	}
	return nil
}

func (b *Bridge) ensureDM(ctx context.Context, typ string, with id.UserID) error {
	user := b.AgentUser(typ)
	rooms, err := b.HS.JoinedRooms(ctx, user)
	if err != nil {
		return err
	}
	for _, room := range rooms {
		members, err := b.HS.Members(ctx, user, room)
		if err != nil {
			return err
		}
		if contains(members, with) {
			return nil
		}
		invited, err := b.HS.Invited(ctx, user, room)
		if err != nil {
			return err
		}
		if contains(invited, with) {
			return nil
		}
	}
	room, err := b.HS.CreateDM(ctx, user, with)
	if err != nil {
		return err
	}
	b.setMember(room, typ, true)
	b.Log.Info("Created a direct chat", "agent", typ, "with", with, "room", room)
	return nil
}

// Handle acts on one event from a transaction. A message from an allowed
// user is handed to Temporal before Handle returns, so turns start in the
// order their events arrive; the reply is posted later, from a goroutine.
func (b *Bridge) Handle(ctx context.Context, evt *event.Event) {
	switch evt.Type {
	case event.StateMember:
		b.member(ctx, evt)
	case event.StateEncryption:
		if b.Allowed[evt.Sender] {
			b.refuseEncrypted(ctx, evt.RoomID)
		}
	case event.EventEncrypted:
		if b.Allowed[evt.Sender] {
			b.refuseEncrypted(ctx, evt.RoomID)
		}
	case event.EventMessage:
		if b.Allowed[evt.Sender] {
			b.message(ctx, evt)
		}
	}
}

// Wait waits for the replies of every turn handed off.
func (b *Bridge) Wait() { b.inflight.Wait() }

func (b *Bridge) member(ctx context.Context, evt *event.Event) {
	m := evt.Content.AsMember()
	target := id.UserID(evt.GetStateKey())
	if typ, ok := b.agentType(target); ok {
		switch m.Membership {
		case event.MembershipInvite:
			if !b.Allowed[evt.Sender] {
				b.Log.Info("Ignoring an invite from someone not allowed", "agent", typ, "from", evt.Sender, "room", evt.RoomID)
				return
			}
			if err := b.HS.Join(ctx, target, evt.RoomID); err != nil {
				b.Log.Error("Joining failed", "agent", typ, "room", evt.RoomID, "error", err)
				return
			}
			b.setMember(evt.RoomID, typ, true)
			b.Log.Info("Joined", "agent", typ, "room", evt.RoomID, "invitedBy", evt.Sender)
			// A room made encrypted at creation (Element X does that for
			// direct chats) sends no encryption event the bridge would see.
			if enc, err := b.HS.Encrypted(ctx, target, evt.RoomID); err != nil {
				b.Log.Warn("Checking the room's encryption failed", "agent", typ, "room", evt.RoomID, "error", err)
			} else if enc {
				b.refuseEncrypted(ctx, evt.RoomID)
			}
		case event.MembershipJoin:
			b.setMember(evt.RoomID, typ, true)
		case event.MembershipLeave, event.MembershipBan:
			b.setMember(evt.RoomID, typ, false)
			b.stopWakeups(ctx, typ, evt.RoomID)
		}
		return
	}
	// An allowed user left: the agents leave too, which ends the
	// conversations' wake-ups.
	if b.Allowed[target] && (m.Membership == event.MembershipLeave || m.Membership == event.MembershipBan) {
		for _, typ := range b.agentsIn(ctx, evt.RoomID) {
			if err := b.HS.Leave(ctx, b.AgentUser(typ), evt.RoomID); err != nil {
				b.Log.Error("Leaving failed", "agent", typ, "room", evt.RoomID, "error", err)
			}
			b.setMember(evt.RoomID, typ, false)
			b.stopWakeups(ctx, typ, evt.RoomID)
		}
	}
}

func (b *Bridge) stopWakeups(ctx context.Context, typ string, room id.RoomID) {
	conv := session.Conversation{Type: typ, Conversation: Conversation(room)}
	if err := b.Turns.StopWakeups(ctx, conv); err != nil {
		b.Log.Error("Stopping the wake-ups failed", "agent", typ, "room", room, "error", err)
		return
	}
	b.Log.Info("Left the room; wake-ups stopped", "agent", typ, "room", room)
}

const encryptedNotice = "This room is encrypted, and I can't read encrypted messages. " +
	"Leave it and use the direct chat I started with you, or start a new chat with encryption off."

// refuseEncrypted tells the room, once, that its agents cannot read it.
func (b *Bridge) refuseEncrypted(ctx context.Context, room id.RoomID) {
	b.mu.Lock()
	if b.encrypted == nil {
		b.encrypted = make(map[id.RoomID]bool)
	}
	told := b.encrypted[room]
	b.encrypted[room] = true
	b.mu.Unlock()
	if told {
		return
	}
	for _, typ := range b.agentsIn(ctx, room) {
		content := &event.MessageEventContent{MsgType: event.MsgNotice, Body: encryptedNotice}
		if err := b.HS.Send(ctx, b.AgentUser(typ), room, content, "encrypted-"+string(room)); err != nil {
			b.Log.Error("Posting the encryption notice failed", "agent", typ, "room", room, "error", err)
		}
	}
}

func (b *Bridge) message(ctx context.Context, evt *event.Event) {
	m := evt.Content.AsMessage()
	if m.RelatesTo != nil && m.RelatesTo.Type == event.RelReplace {
		// An edit: the turn for the original has run.
		return
	}
	text := strings.TrimSpace(m.Body)
	agents := b.agentsIn(ctx, evt.RoomID)
	if len(agents) == 0 {
		return
	}
	if m.MsgType != event.MsgText || text == "" {
		for _, typ := range agents {
			b.notice(ctx, typ, evt, "I can only read text messages.")
		}
		return
	}
	for _, typ := range agents {
		b.turn(ctx, typ, evt, text)
	}
}

// turn hands the message to the agent's conversation and posts the reply
// when it comes.
func (b *Bridge) turn(ctx context.Context, typ string, evt *event.Event, text string) {
	user := b.AgentUser(typ)
	conv := session.Conversation{Type: typ, Conversation: Conversation(evt.RoomID)}
	log := b.Log.With("agent", typ, "room", evt.RoomID, "event", evt.ID)
	if err := b.HS.MarkRead(ctx, user, evt.RoomID, evt.ID); err != nil {
		log.Warn("Marking the message read failed", "error", err)
	}
	wait, err := b.Turns.Send(ctx, conv, "matrix-"+string(evt.ID), text)
	if err != nil {
		log.Error("Sending the message failed", "error", err)
		b.notice(ctx, typ, evt, "I couldn't take that message: "+shortError(err))
		return
	}
	b.startTyping(typ, evt.RoomID)
	b.inflight.Add(1)
	go func() {
		defer b.inflight.Done()
		// Detached from the transaction: the turn can take minutes.
		ctx := context.WithoutCancel(ctx)
		reply, err := wait(ctx)
		b.stopTyping(ctx, typ, evt.RoomID)
		if err != nil {
			log.Error("The turn failed", "error", err)
			b.notice(ctx, typ, evt, "That turn failed: "+shortError(err))
			return
		}
		if session.IsSilent(reply) || strings.TrimSpace(reply) == "" {
			log.Info("The turn ended silent")
			return
		}
		content := format.RenderMarkdown(reply, true, false)
		if err := b.HS.Send(ctx, user, evt.RoomID, &content, "reply-"+string(evt.ID)); err != nil {
			log.Error("Posting the reply failed", "error", err)
		}
	}()
}

// notice posts a short line from the agent about evt.
func (b *Bridge) notice(ctx context.Context, typ string, evt *event.Event, text string) {
	content := &event.MessageEventContent{MsgType: event.MsgNotice, Body: text}
	if err := b.HS.Send(ctx, b.AgentUser(typ), evt.RoomID, content, "notice-"+string(evt.ID)); err != nil {
		b.Log.Error("Posting a notice failed", "agent", typ, "room", evt.RoomID, "error", err)
	}
}

// shortError is the first line of an error, cut to a sentence's length.
func shortError(err error) string {
	s, _, _ := strings.Cut(err.Error(), "\n")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// agentsIn returns the agent types joined to the room. What the homeserver
// says is cached; membership events keep the cache current.
func (b *Bridge) agentsIn(ctx context.Context, room id.RoomID) []string {
	agents, err := b.Agents()
	if err != nil {
		b.Log.Error("Reading the agent types failed", "error", err)
		return nil
	}
	var in []string
	for _, typ := range desired.SortedKeys(agents) {
		if b.joined(ctx, typ, room) {
			in = append(in, typ)
		}
	}
	return in
}

// joined reports whether the agent (or admin) typ is in the room, asking
// the homeserver the first time.
func (b *Bridge) joined(ctx context.Context, typ string, room id.RoomID) bool {
	b.mu.Lock()
	joined, known := b.members[room][typ]
	b.mu.Unlock()
	if known {
		return joined
	}
	_, err := b.HS.Members(ctx, b.AgentUser(typ), room)
	switch {
	case err == nil:
		joined = true
	case errors.Is(err, ErrNotInRoom):
		joined = false
	default:
		b.Log.Error("Asking for the room's members failed", "agent", typ, "room", room, "error", err)
		return false
	}
	b.setMember(room, typ, joined)
	return joined
}

// ConversationWith is the conversation of the agent typ's direct chat with
// user: the one room both are joined to.
func (b *Bridge) ConversationWith(ctx context.Context, typ string, user id.UserID) (string, error) {
	rooms, err := b.HS.JoinedRooms(ctx, b.AgentUser(typ))
	if err != nil {
		return "", err
	}
	var found []id.RoomID
	for _, room := range rooms {
		members, err := b.HS.Members(ctx, b.AgentUser(typ), room)
		if err != nil {
			return "", err
		}
		if contains(members, user) {
			found = append(found, room)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("%s has no chat with you that you have joined", DisplayName(typ))
	case 1:
		return Conversation(found[0]), nil
	}
	return "", fmt.Errorf("%s is in %d rooms with you, so I can't tell which chat you mean; leave the ones you don't use", DisplayName(typ), len(found))
}

func (b *Bridge) setMember(room id.RoomID, typ string, joined bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.members == nil {
		b.members = make(map[id.RoomID]map[string]bool)
	}
	if b.members[room] == nil {
		b.members[room] = make(map[string]bool)
	}
	b.members[room][typ] = joined
}

// startTyping shows the agent typing in the room until as many stopTyping
// calls as starts, renewing the notice while it lasts.
func (b *Bridge) startTyping(typ string, room id.RoomID) {
	key := typingKey{room, typ}
	b.mu.Lock()
	if b.typing == nil {
		b.typing = make(map[typingKey]*typingState)
	}
	st := b.typing[key]
	if st != nil {
		st.n++
		b.mu.Unlock()
		return
	}
	st = &typingState{n: 1, stop: make(chan struct{})}
	b.typing[key] = st
	b.mu.Unlock()

	user := b.AgentUser(typ)
	notice := func() {
		st.send.Lock()
		defer st.send.Unlock()
		if st.stopped {
			return
		}
		if err := b.HS.Typing(context.Background(), user, room, true, typingTimeout); err != nil {
			b.Log.Warn("Typing notice failed", "agent", typ, "room", room, "error", err)
		}
	}
	notice()
	go func() {
		tick := time.NewTicker(typingRenew)
		defer tick.Stop()
		for {
			select {
			case <-st.stop:
				return
			case <-tick.C:
				notice()
			}
		}
	}()
}

func (b *Bridge) stopTyping(ctx context.Context, typ string, room id.RoomID) {
	key := typingKey{room, typ}
	b.mu.Lock()
	st := b.typing[key]
	if st == nil {
		b.mu.Unlock()
		return
	}
	st.n--
	last := st.n == 0
	if last {
		delete(b.typing, key)
		close(st.stop)
	}
	b.mu.Unlock()
	if last {
		st.send.Lock()
		defer st.send.Unlock()
		st.stopped = true
		if err := b.HS.Typing(ctx, b.AgentUser(typ), room, false, 0); err != nil {
			b.Log.Warn("Clearing the typing notice failed", "agent", typ, "room", room, "error", err)
		}
	}
}

// DisplayName is an agent type's name for people: habit-helper is
// "Habit Helper".
func DisplayName(typ string) string {
	words := strings.FieldsFunc(typ, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func contains(users []id.UserID, u id.UserID) bool {
	for _, v := range users {
		if v == u {
			return true
		}
	}
	return false
}
