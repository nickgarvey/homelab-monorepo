package matrix

import (
	"context"

	"maunium.net/go/mautrix/format"

	"homelab/agents/internal/chat"
)

// Deliver is the chat.DeliverActivity: it posts a wake-up's reply as the
// agent in the room whose conversation it is. The room is found among the
// agent's joined rooms, since a hashed conversation ID cannot be turned back
// into a room ID. An agent in no such room delivers nothing.
func (b *Bridge) Deliver(ctx context.Context, in chat.DeliverInput) (chat.DeliverResult, error) {
	agents, err := b.Agents()
	if err != nil {
		return chat.DeliverResult{}, err
	}
	if !agents[in.Type] {
		return chat.DeliverResult{}, nil
	}
	user := b.AgentUser(in.Type)
	rooms, err := b.HS.JoinedRooms(ctx, user)
	if err != nil {
		return chat.DeliverResult{}, err
	}
	for _, room := range rooms {
		if Conversation(room) != in.Conversation {
			continue
		}
		content := format.RenderMarkdown(in.Reply, true, false)
		if err := b.HS.Send(ctx, user, room, &content, in.TxnID); err != nil {
			return chat.DeliverResult{}, err
		}
		b.Log.Info("Posted a wake-up's reply", "agent", in.Type, "room", room)
		return chat.DeliverResult{Delivered: true}, nil
	}
	b.Log.Warn("A wake-up's reply has no room", "agent", in.Type, "conversation", in.Conversation)
	return chat.DeliverResult{}, nil
}
