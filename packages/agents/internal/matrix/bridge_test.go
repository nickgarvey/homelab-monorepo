package matrix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"homelab/agents/internal/session"
)

const (
	server = "matrix.garvey.sh"
	nick   = id.UserID("@ngarvey:matrix.garvey.sh")
	other  = id.UserID("@mallory:matrix.garvey.sh")
	agent  = id.UserID("@habit-helper:matrix.garvey.sh")
	room   = id.RoomID("!AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcde")
)

// fakeHS records what the bridge does on Matrix.
type fakeHS struct {
	mu        sync.Mutex
	calls     []string
	sent      []sent
	joined    map[id.RoomID][]id.UserID
	invited   map[id.RoomID][]id.UserID
	encrypted map[id.RoomID]bool
	created   []id.RoomID
}

type sent struct {
	user    id.UserID
	room    id.RoomID
	content event.MessageEventContent
	txnID   string
}

func newFakeHS() *fakeHS {
	return &fakeHS{joined: map[id.RoomID][]id.UserID{}, invited: map[id.RoomID][]id.UserID{}, encrypted: map[id.RoomID]bool{}}
}

func (f *fakeHS) record(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func (f *fakeHS) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeHS) Sent() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

func (f *fakeHS) Register(_ context.Context, user id.UserID, name string) error {
	f.record("register %s %q", user, name)
	return nil
}

func (f *fakeHS) Join(_ context.Context, user id.UserID, r id.RoomID) error {
	f.record("join %s %s", user, r)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joined[r] = append(f.joined[r], user)
	return nil
}

func (f *fakeHS) Leave(_ context.Context, user id.UserID, r id.RoomID) error {
	f.record("leave %s %s", user, r)
	return nil
}

func (f *fakeHS) Members(_ context.Context, user id.UserID, r id.RoomID) ([]id.UserID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Contains(f.joined[r], user) {
		return nil, ErrNotInRoom
	}
	return slices.Clone(f.joined[r]), nil
}

func (f *fakeHS) Invited(_ context.Context, _ id.UserID, r id.RoomID) ([]id.UserID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.invited[r]), nil
}

func (f *fakeHS) Encrypted(_ context.Context, _ id.UserID, r id.RoomID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.encrypted[r], nil
}

func (f *fakeHS) JoinedRooms(_ context.Context, user id.UserID) ([]id.RoomID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rooms []id.RoomID
	for r, members := range f.joined {
		if slices.Contains(members, user) {
			rooms = append(rooms, r)
		}
	}
	slices.Sort(rooms)
	return rooms, nil
}

func (f *fakeHS) CreateDM(_ context.Context, user, invitee id.UserID) (id.RoomID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := id.RoomID(fmt.Sprintf("!created%d", len(f.created)))
	f.created = append(f.created, r)
	f.joined[r] = []id.UserID{user}
	f.invited[r] = []id.UserID{invitee}
	f.calls = append(f.calls, fmt.Sprintf("create %s invite %s", user, invitee))
	return r, nil
}

func (f *fakeHS) Send(_ context.Context, user id.UserID, r id.RoomID, c *event.MessageEventContent, txnID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sent{user, r, *c, txnID})
	return nil
}

func (f *fakeHS) Typing(_ context.Context, user id.UserID, r id.RoomID, typing bool, _ time.Duration) error {
	f.record("typing %s %v", user, typing)
	return nil
}

func (f *fakeHS) MarkRead(_ context.Context, user id.UserID, r id.RoomID, evt id.EventID) error {
	f.record("read %s %s", user, evt)
	return nil
}

// fakeTurns answers each message with its reply, once release is closed.
type fakeTurns struct {
	mu      sync.Mutex
	sends   []string
	stopped []session.Conversation
	reply   string
	err     error
	release chan struct{}
}

func (f *fakeTurns) Send(_ context.Context, conv session.Conversation, turnID, text string) (func(context.Context) (string, error), error) {
	f.mu.Lock()
	f.sends = append(f.sends, fmt.Sprintf("%s/%s %s %q", conv.Type, conv.Conversation, turnID, text))
	f.mu.Unlock()
	return func(ctx context.Context) (string, error) {
		if f.release != nil {
			<-f.release
		}
		return f.reply, f.err
	}, nil
}

func (f *fakeTurns) StopWakeups(_ context.Context, conv session.Conversation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, conv)
	return nil
}

func newBridge(hs *fakeHS, turns *fakeTurns) *Bridge {
	return &Bridge{
		HS:      hs,
		Turns:   turns,
		Server:  server,
		Allowed: map[id.UserID]bool{nick: true},
		Agents:  func() (map[string]bool, error) { return map[string]bool{"habit-helper": true}, nil },
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func memberEvent(sender, target id.UserID, membership event.Membership) *event.Event {
	key := string(target)
	return &event.Event{
		Type: event.StateMember, Sender: sender, RoomID: room, StateKey: &key, ID: "$member",
		Content: event.Content{Parsed: &event.MemberEventContent{Membership: membership}},
	}
}

func textEvent(sender id.UserID, evtID id.EventID, body string) *event.Event {
	return &event.Event{
		Type: event.EventMessage, Sender: sender, RoomID: room, ID: evtID,
		Content: event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgText, Body: body}},
	}
}

func TestConversation(t *testing.T) {
	// Room version 12: no server part.
	assert.Equal(t, "matrix-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcde", Conversation(room))
	// Earlier versions: the localpart.
	assert.Equal(t, "matrix-xyzABC", Conversation("!xyzABC:matrix.garvey.sh"))
	// Anything that would not be a valid conversation ID: a hash.
	c := Conversation("!a b/c:matrix.garvey.sh")
	assert.Regexp(t, `^matrix-h[0-9a-f]{32}$`, c)
	assert.Equal(t, c, Conversation("!a b/c:matrix.garvey.sh"))
	assert.NotEqual(t, c, Conversation("!a b/d:matrix.garvey.sh"))
}

func TestDisplayName(t *testing.T) {
	assert.Equal(t, "Habit Helper", DisplayName("habit-helper"))
	assert.Equal(t, "Pi", DisplayName("pi"))
}

func TestInviteFromAllowedIsJoined(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{}
	b := newBridge(hs, turns)
	b.Handle(context.Background(), memberEvent(nick, agent, event.MembershipInvite))
	assert.Contains(t, hs.Calls(), "join "+string(agent)+" "+string(room))
	assert.Empty(t, hs.Sent())
}

func TestInviteFromOthersIsIgnored(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{}
	b := newBridge(hs, turns)
	b.Handle(context.Background(), memberEvent(other, agent, event.MembershipInvite))
	assert.Empty(t, hs.Calls())
}

func TestInviteToEncryptedRoomIsRefusedOnce(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{}
	hs.encrypted[room] = true
	b := newBridge(hs, turns)
	ctx := context.Background()
	b.Handle(ctx, memberEvent(nick, agent, event.MembershipInvite))
	b.Handle(ctx, &event.Event{Type: event.EventEncrypted, Sender: nick, RoomID: room, ID: "$enc"})
	require.Len(t, hs.Sent(), 1)
	assert.Equal(t, event.MsgNotice, hs.Sent()[0].content.MsgType)
	assert.Contains(t, hs.Sent()[0].content.Body, "encrypted")
	assert.Empty(t, turns.sends)
}

func TestMessageRunsATurnAndPostsTheReply(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{reply: "Logged **walk** for today."}
	hs.joined[room] = []id.UserID{nick, agent}
	b := newBridge(hs, turns)
	b.Handle(context.Background(), textEvent(nick, "$m1", "  walked today  "))
	b.Wait()

	assert.Equal(t, []string{`habit-helper/` + Conversation(room) + ` matrix-$m1 "walked today"`}, turns.sends)
	require.Len(t, hs.Sent(), 1)
	s := hs.Sent()[0]
	assert.Equal(t, agent, s.user)
	assert.Equal(t, room, s.room)
	assert.Equal(t, "reply-$m1", s.txnID)
	assert.Equal(t, event.MsgText, s.content.MsgType)
	assert.Equal(t, "Logged **walk** for today.", s.content.Body)
	assert.Equal(t, "Logged <strong>walk</strong> for today.", s.content.FormattedBody)
	calls := hs.Calls()
	assert.Equal(t, []string{"read " + string(agent) + " $m1", "typing " + string(agent) + " true", "typing " + string(agent) + " false"}, calls)
}

func TestMessagesFromOthersAreIgnored(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{reply: "hi"}
	hs.joined[room] = []id.UserID{nick, other, agent}
	b := newBridge(hs, turns)
	b.Handle(context.Background(), textEvent(other, "$m1", "hello"))
	b.Wait()
	assert.Empty(t, turns.sends)
	assert.Empty(t, hs.Sent())
	assert.Empty(t, hs.Calls())
}

func TestNoAgentInRoomNoTurn(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{reply: "hi"}
	hs.joined[room] = []id.UserID{nick}
	b := newBridge(hs, turns)
	b.Handle(context.Background(), textEvent(nick, "$m1", "hello"))
	b.Wait()
	assert.Empty(t, turns.sends)
	assert.Empty(t, hs.Sent())
}

func TestSilentReplyPostsNothing(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{reply: "<silent>"}
	hs.joined[room] = []id.UserID{nick, agent}
	b := newBridge(hs, turns)
	b.Handle(context.Background(), textEvent(nick, "$m1", "ok"))
	b.Wait()
	assert.Len(t, turns.sends, 1)
	assert.Empty(t, hs.Sent())
}

func TestFailedTurnPostsANotice(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{err: errors.New("sandbox lost\nstack...")}
	hs.joined[room] = []id.UserID{nick, agent}
	b := newBridge(hs, turns)
	b.Handle(context.Background(), textEvent(nick, "$m1", "ok"))
	b.Wait()
	require.Len(t, hs.Sent(), 1)
	assert.Equal(t, event.MsgNotice, hs.Sent()[0].content.MsgType)
	assert.Equal(t, "That turn failed: sandbox lost", hs.Sent()[0].content.Body)
	assert.Equal(t, "notice-$m1", hs.Sent()[0].txnID)
	assert.Contains(t, hs.Calls(), "typing "+string(agent)+" false")
}

func TestNonTextAndEditsRunNoTurn(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{reply: "hi"}
	hs.joined[room] = []id.UserID{nick, agent}
	b := newBridge(hs, turns)
	ctx := context.Background()
	b.Handle(ctx, &event.Event{
		Type: event.EventMessage, Sender: nick, RoomID: room, ID: "$img",
		Content: event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgImage, Body: "cat.jpg"}},
	})
	b.Handle(ctx, &event.Event{
		Type: event.EventMessage, Sender: nick, RoomID: room, ID: "$edit",
		Content: event.Content{Parsed: &event.MessageEventContent{
			MsgType: event.MsgText, Body: "* fixed",
			RelatesTo: &event.RelatesTo{Type: event.RelReplace, EventID: "$m1"},
		}},
	})
	b.Wait()
	assert.Empty(t, turns.sends)
	require.Len(t, hs.Sent(), 1)
	assert.Equal(t, "I can only read text messages.", hs.Sent()[0].content.Body)
}

func TestTypingLastsWhileAnyTurnRuns(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{reply: "ok", release: make(chan struct{})}
	hs.joined[room] = []id.UserID{nick, agent}
	b := newBridge(hs, turns)
	ctx := context.Background()
	b.Handle(ctx, textEvent(nick, "$m1", "one"))
	b.Handle(ctx, textEvent(nick, "$m2", "two"))
	close(turns.release)
	b.Wait()
	var typing []string
	for _, c := range hs.Calls() {
		if c == "typing "+string(agent)+" false" || c == "typing "+string(agent)+" true" {
			typing = append(typing, c)
		}
	}
	// One notice for both turns, cleared once, after the second.
	assert.Equal(t, "typing "+string(agent)+" true", typing[0])
	assert.Equal(t, "typing "+string(agent)+" false", typing[len(typing)-1])
	assert.Equal(t, 1, slices.Index(typing, "typing "+string(agent)+" false")-len(typing)+2)
	assert.Len(t, hs.Sent(), 2)
}

func TestAgentKickedStopsWakeups(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{}
	b := newBridge(hs, turns)
	b.Handle(context.Background(), memberEvent(nick, agent, event.MembershipLeave))
	assert.Equal(t, []session.Conversation{{Type: "habit-helper", Conversation: Conversation(room)}}, turns.stopped)
}

func TestAllowedUserLeavingTakesTheAgentOut(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{}
	hs.joined[room] = []id.UserID{agent}
	b := newBridge(hs, turns)
	b.Handle(context.Background(), memberEvent(nick, nick, event.MembershipLeave))
	assert.Contains(t, hs.Calls(), "leave "+string(agent)+" "+string(room))
	assert.Equal(t, []session.Conversation{{Type: "habit-helper", Conversation: Conversation(room)}}, turns.stopped)
}

func TestStartCreatesTheDirectChatOnce(t *testing.T) {
	hs, turns := newFakeHS(), &fakeTurns{}
	b := newBridge(hs, turns)
	ctx := context.Background()
	require.NoError(t, b.Start(ctx))
	assert.Equal(t, []string{
		`register ` + string(agent) + ` "Habit Helper"`,
		"create " + string(agent) + " invite " + string(nick),
	}, hs.Calls())

	// Invited and not yet joined: no second room.
	require.NoError(t, b.Start(ctx))
	assert.Len(t, hs.created, 1)

	// Joined: none either.
	hs.invited[hs.created[0]] = nil
	hs.joined[hs.created[0]] = append(hs.joined[hs.created[0]], nick)
	require.NoError(t, b.Start(ctx))
	assert.Len(t, hs.created, 1)
}
