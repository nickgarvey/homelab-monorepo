package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"homelab/agents/internal/store"
	"homelab/agents/internal/store/storetest"
)

func openTest(t *testing.T) *store.Store { return storetest.Open(t, "agents_store_test") }

func TestSchemaIsIdempotent(t *testing.T) {
	openTest(t)
	openTest(t)
}

func TestSandboxName(t *testing.T) {
	name := store.SandboxName("pi", "a-rather-long-conversation-id-1234567890", 99999)
	if len(name) > 19 {
		t.Errorf("%s is %d characters, over the gateway's 19", name, len(name))
	}
	if !strings.HasPrefix(name, "chat-") || name != strings.ToLower(name) {
		t.Errorf("name %s", name)
	}
	if store.SandboxName("pi", "a", 1) == store.SandboxName("other", "a", 1) {
		t.Error("the type is not in the hash")
	}
}

func TestGenerations(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	got, err := s.Session(ctx, "pi", "c1")
	if err != nil || got != nil {
		t.Fatalf("no row yet: %v %v", got, err)
	}
	first, err := s.NewGeneration(ctx, "pi", "c1", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != 1 || first.State != store.Creating || first.Sandbox != store.SandboxName("pi", "c1", 1) || first.TypeVersion != "v1" {
		t.Errorf("first generation: %+v", first)
	}
	if ok, err := s.Transition(ctx, "pi", "c1", first.Sandbox, []store.State{store.Creating}, store.Running); !ok || err != nil {
		t.Fatalf("creating→running: %v %v", ok, err)
	}
	if ok, err := s.Transition(ctx, "pi", "c1", first.Sandbox, []store.State{store.Running}, store.Stopped); !ok || err != nil {
		t.Fatalf("running→stopped: %v %v", ok, err)
	}
	got, _ = s.Session(ctx, "pi", "c1")
	if got.State != store.Stopped || got.StoppedAt == nil {
		t.Errorf("stopped row: %+v", got)
	}

	second, err := s.NewGeneration(ctx, "pi", "c1", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != 2 || second.State != store.Creating || second.StoppedAt != nil || second.TypeVersion != "v2" {
		t.Errorf("second generation: %+v", second)
	}
	// A transition naming the old sandbox no longer applies.
	if ok, _ := s.Transition(ctx, "pi", "c1", first.Sandbox, []store.State{store.Creating}, store.Running); ok {
		t.Error("a transition for the old generation's sandbox moved the row")
	}
}

func TestTransitionIsCompareAndSet(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	row, _ := s.NewGeneration(ctx, "pi", "c1", "v1")
	s.Transition(ctx, "pi", "c1", row.Sandbox, []store.State{store.Creating}, store.Stopped)

	// The reaper claims the row; the session's claim then fails.
	if ok, _ := s.Transition(ctx, "pi", "c1", row.Sandbox, []store.State{store.Stopped}, store.Deleting); !ok {
		t.Fatal("reaper's claim failed")
	}
	if ok, _ := s.Transition(ctx, "pi", "c1", row.Sandbox, []store.State{store.Stopped}, store.Starting); ok {
		t.Error("session claimed a row the reaper holds")
	}
}

func TestMessages(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	add := func(turn, role, content string) {
		t.Helper()
		if err := s.AddMessage(ctx, "pi", "c1", turn, role, content); err != nil {
			t.Fatal(err)
		}
	}
	add("t1", store.RoleUser, "hello")
	add("t1", store.RoleAssistant, "hi")
	add("t1", store.RoleUser, "hello again") // a retried turn: ignored
	add("t2", store.RoleUser, "how are you")
	add("t2", store.RoleAssistant, "fine")
	add("t3", store.RoleUser, "bye")
	s.AddMessage(ctx, "pi", "other", "t1", store.RoleUser, "elsewhere")

	if c, ok, _ := s.TurnMessage(ctx, "pi", "c1", "t1", store.RoleUser); !ok || c != "hello" {
		t.Errorf("t1 user: %q %v", c, ok)
	}
	if _, ok, _ := s.TurnMessage(ctx, "pi", "c1", "t3", store.RoleAssistant); ok {
		t.Error("t3 has no reply yet")
	}

	got, err := s.Recent(ctx, "pi", "c1", "t3", 3)
	if err != nil {
		t.Fatal(err)
	}
	var contents []string
	for _, m := range got {
		contents = append(contents, m.Role+":"+m.Content)
	}
	want := "assistant:hi user:how are you assistant:fine"
	if strings.Join(contents, " ") != want {
		t.Errorf("recent: %v, want %s", contents, want)
	}
}

func TestReaperQueries(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	stopped, _ := s.NewGeneration(ctx, "pi", "c1", "v1")
	s.Transition(ctx, "pi", "c1", stopped.Sandbox, []store.State{store.Creating}, store.Stopped)
	creating, _ := s.NewGeneration(ctx, "pi", "c2", "v1")

	rows, err := s.Sessions(ctx, store.Stopped, store.Deleting)
	if err != nil || len(rows) != 1 || rows[0].Sandbox != stopped.Sandbox {
		t.Fatalf("stopped and deleting rows: %+v %v", rows, err)
	}
	for _, sb := range []string{stopped.Sandbox, creating.Sandbox} {
		if held, err := s.HoldsSandbox(ctx, sb); !held || err != nil {
			t.Errorf("%s not held: %v", sb, err)
		}
	}
	if held, _ := s.HoldsSandbox(ctx, "chat-unknown-1"); held {
		t.Error("an unknown name is held")
	}

	// Not stopped for an hour yet, and the creating row is not stopped.
	if ok, _ := s.ClaimExpired(ctx, "pi", "c1", stopped.Sandbox, time.Hour); ok {
		t.Error("claimed a row stopped just now with an hour's retention")
	}
	if ok, _ := s.ClaimExpired(ctx, "pi", "c2", creating.Sandbox, 0); ok {
		t.Error("claimed a creating row")
	}
	if ok, err := s.ClaimExpired(ctx, "pi", "c1", stopped.Sandbox, 0); !ok || err != nil {
		t.Fatalf("expired claim: %v %v", ok, err)
	}
	if row, _ := s.Session(ctx, "pi", "c1"); row.State != store.Deleting {
		t.Errorf("claimed row: %+v", row)
	}

	// A deleted row no longer holds its name.
	s.Transition(ctx, "pi", "c1", stopped.Sandbox, []store.State{store.Deleting}, store.Deleted)
	if held, _ := s.HoldsSandbox(ctx, stopped.Sandbox); held {
		t.Error("a deleted row holds its sandbox")
	}
}
