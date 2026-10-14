package matrix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/id"
)

// Synapse answers joined_members for an application service user who is
// not in the room, as long as some member is in the namespace.
func TestSynapseMembersNeedsTheUserJoined(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"joined":{"@ngarvey:matrix.garvey.sh":{},"@habit-helper:matrix.garvey.sh":{}}}`))
	}))
	defer srv.Close()
	as, err := appservice.CreateFull(appservice.CreateOpts{
		Registration:     &appservice.Registration{AppToken: "as", ServerToken: "hs", SenderLocalpart: "agents"},
		HomeserverDomain: server,
		HomeserverURL:    srv.URL,
	})
	require.NoError(t, err)
	s := &Synapse{AS: as}
	ctx := context.Background()

	members, err := s.Members(ctx, agent, room)
	require.NoError(t, err)
	assert.ElementsMatch(t, []id.UserID{nick, agent}, members)

	_, err = s.Members(ctx, "@pi:matrix.garvey.sh", room)
	assert.ErrorIs(t, err, ErrNotInRoom)
}
