package matrix

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"maunium.net/go/mautrix/id"

	"homelab/agents/internal/session"
)

// Conversation is the conversation ID of a room: "matrix-" and the room ID's
// opaque part, which for room version 12 is 43 base64url characters
// (!<hash>) and for earlier versions the localpart of !<localpart>:<server>.
// A room ID whose opaque part is not a valid conversation ID gets a hash of
// the whole ID instead. Either way the same room is always the same
// conversation, so the bridge needs no table.
func Conversation(room id.RoomID) string {
	opaque, _, _ := strings.Cut(strings.TrimPrefix(string(room), "!"), ":")
	if c := "matrix-" + opaque; opaque != "" && session.ValidConversation(c) == nil {
		return c
	}
	sum := sha256.Sum256([]byte(room))
	return "matrix-h" + hex.EncodeToString(sum[:16])
}
