package matrix

import (
	"context"
	"errors"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Synapse is the Homeserver over the client-server API, as the application
// service: each call is made as the given user with the as_token.
type Synapse struct {
	AS *appservice.AppService
}

func (s *Synapse) client(user id.UserID) *mautrix.Client {
	return s.AS.Client(user)
}

func (s *Synapse) Register(ctx context.Context, user id.UserID, displayName string) error {
	if err := s.AS.Intent(user).EnsureRegistered(ctx); err != nil {
		return err
	}
	return s.client(user).SetDisplayName(ctx, displayName)
}

func (s *Synapse) Join(ctx context.Context, user id.UserID, room id.RoomID) error {
	_, err := s.client(user).JoinRoomByID(ctx, room)
	return err
}

func (s *Synapse) Leave(ctx context.Context, user id.UserID, room id.RoomID) error {
	_, err := s.client(user).LeaveRoom(ctx, room)
	return err
}

func (s *Synapse) Members(ctx context.Context, user id.UserID, room id.RoomID) ([]id.UserID, error) {
	resp, err := s.client(user).JoinedMembers(ctx, room)
	if errors.Is(err, mautrix.MForbidden) {
		return nil, ErrNotInRoom
	}
	if err != nil {
		return nil, err
	}
	members := make([]id.UserID, 0, len(resp.Joined))
	for u := range resp.Joined {
		members = append(members, u)
	}
	return members, nil
}

func (s *Synapse) Invited(ctx context.Context, user id.UserID, room id.RoomID) ([]id.UserID, error) {
	resp, err := s.client(user).Members(ctx, room, mautrix.ReqMembers{Membership: event.MembershipInvite})
	if err != nil {
		return nil, err
	}
	var invited []id.UserID
	for _, evt := range resp.Chunk {
		invited = append(invited, id.UserID(evt.GetStateKey()))
	}
	return invited, nil
}

func (s *Synapse) Encrypted(ctx context.Context, user id.UserID, room id.RoomID) (bool, error) {
	var content event.EncryptionEventContent
	err := s.client(user).StateEvent(ctx, room, event.StateEncryption, "", &content)
	if errors.Is(err, mautrix.MNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Synapse) JoinedRooms(ctx context.Context, user id.UserID) ([]id.RoomID, error) {
	resp, err := s.client(user).JoinedRooms(ctx)
	if err != nil {
		return nil, err
	}
	return resp.JoinedRooms, nil
}

// CreateDM makes the room as a private direct chat. The invitee gets power
// 50, enough to rename it, and turning encryption on needs 100, which only
// user (the creator) has.
func (s *Synapse) CreateDM(ctx context.Context, user, invitee id.UserID) (id.RoomID, error) {
	stateDefault := 50
	resp, err := s.client(user).CreateRoom(ctx, &mautrix.ReqCreateRoom{
		Preset:   "private_chat",
		IsDirect: true,
		Invite:   []id.UserID{invitee},
		PowerLevelOverride: &event.PowerLevelsEventContent{
			Users:           map[id.UserID]int{invitee: 50},
			Events:          map[string]int{event.StateEncryption.Type: 100},
			StateDefaultPtr: &stateDefault,
		},
	})
	if err != nil {
		return "", err
	}
	return resp.RoomID, nil
}

func (s *Synapse) Send(ctx context.Context, user id.UserID, room id.RoomID, content *event.MessageEventContent, txnID string) error {
	_, err := s.client(user).SendMessageEvent(ctx, room, event.EventMessage, content, mautrix.ReqSendEvent{TransactionID: txnID})
	return err
}

func (s *Synapse) Typing(ctx context.Context, user id.UserID, room id.RoomID, typing bool, timeout time.Duration) error {
	_, err := s.client(user).UserTyping(ctx, room, typing, timeout)
	return err
}

func (s *Synapse) MarkRead(ctx context.Context, user id.UserID, room id.RoomID, evt id.EventID) error {
	return s.client(user).MarkRead(ctx, room, evt)
}
