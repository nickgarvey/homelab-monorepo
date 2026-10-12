package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"homelab/agents/internal/gateway"
	"homelab/agents/internal/store"
)

var outboxNow = time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)

func TestParseOutbox(t *testing.T) {
	for _, tc := range []struct {
		file   string
		at     time.Time
		refuse string
	}{
		{`{"at": "2026-10-11T21:00:00-07:00", "note": "end-of-day check-in"}`, time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC), ""},
		{`{"at": "2026-10-11T15:10:00Z"}`, outboxNow.Add(10 * time.Minute), ""},
		{`{"at": "2026-12-10T15:00:00Z", "note": ""}`, outboxNow.Add(60 * 24 * time.Hour), ""},
		{`{"at": "2026-10-11T15:09:00Z", "note": "x"}`, time.Time{}, "9m0s from now"},
		{`{"at": "2026-12-10T15:01:00Z"}`, time.Time{}, "it must be 10m to 60d ahead"},
		{`{"at": "tonight"}`, time.Time{}, "not an RFC 3339 time"},
		{`{"note": "x"}`, time.Time{}, `"at" is missing`},
		{`{"at": "2026-10-11T21:00:00Z", "when": "x"}`, time.Time{}, `unknown field "when"`},
		{`not json`, time.Time{}, "want {"},
		{`{"at": "2026-10-11T21:00:00Z", "note": "` + strings.Repeat("é", MaxNoteChars+1) + `"}`, time.Time{}, "501 characters"},
		{strings.Repeat(" ", MaxOutboxBytes+1), time.Time{}, "over 4096 bytes"},
	} {
		w, refuse := ParseOutbox([]byte(tc.file), outboxNow)
		switch {
		case tc.refuse == "" && (w == nil || !w.At.Equal(tc.at)):
			t.Errorf("%.60s: got %+v, refused %q", tc.file, w, refuse)
		case tc.refuse != "" && (w != nil || !strings.Contains(refuse, tc.refuse)):
			t.Errorf("%.60s: got %+v, refused %q, want %q", tc.file, w, refuse, tc.refuse)
		}
	}
}

func TestReadAndClearOutbox(t *testing.T) {
	gw := gateway.NewFake()
	gw.Create(context.Background(), "sb", gateway.Spec{Policy: map[string]any{"version": 1}})
	file := `{"at": "2026-10-11T21:00:00Z", "note": "n"}`
	gw.ExecFunc = func(_ string, argv []string) (gateway.ExecResult, error) {
		switch argv[0] {
		case "sh":
			if file == "" {
				return gateway.ExecResult{ExitCode: 10}, nil
			}
			return gateway.ExecResult{Stdout: []byte(file)}, nil
		case "rm":
			if strings.Join(argv, " ") == "rm -f "+OutboxFile {
				file = ""
				return gateway.ExecResult{}, nil
			}
		}
		return gateway.ExecResult{ExitCode: 127}, nil
	}
	a := &Activities{Gateway: gw, Now: func() time.Time { return outboxNow }}
	ref := SandboxRef{Conversation: Conversation{"pi", "c1"}, Sandbox: "sb"}

	out, err := a.ReadOutbox(context.Background(), ref)
	if err != nil || !out.Found || out.Request == nil || out.Request.Note != "n" || out.Rejected != "" {
		t.Fatalf("read: %+v %v", out, err)
	}
	if err := a.ClearOutbox(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if out, err := a.ReadOutbox(context.Background(), ref); err != nil || out.Found || out.Request != nil {
		t.Errorf("read after clear: %+v %v", out, err)
	}

	file = `{"at": "soon"}`
	if out, err := a.ReadOutbox(context.Background(), ref); err != nil || !out.Found || out.Request != nil || out.Rejected == "" {
		t.Errorf("read invalid: %+v %v", out, err)
	}
}

func TestHeader(t *testing.T) {
	loc, err := Location("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 15, 12, 0, 0, time.UTC)
	pending := &Wakeup{At: time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC), Note: "end-of-day check-in"}
	for _, tc := range []struct {
		st   *WakeupStatus
		want string
	}{
		{nil, "[now] 2026-10-11T08:12-07:00 (America/Los_Angeles)\n[next wake-up] none yet\n\n"},
		{&WakeupStatus{Pending: pending},
			"[now] 2026-10-11T08:12-07:00 (America/Los_Angeles)\n[next wake-up] 2026-10-11T21:00-07:00. Note: end-of-day check-in\n\n"},
		{&WakeupStatus{NextFloorSeconds: 3 * 86400},
			"[now] 2026-10-11T08:12-07:00 (America/Los_Angeles)\n[next wake-up] none: this turn is a wake-up. Without a request, the next comes 3d after it.\n\n"},
		{&WakeupStatus{Pending: pending, Rejected: `"at" is missing`},
			"[now] 2026-10-11T08:12-07:00 (America/Los_Angeles)\n[next wake-up] 2026-10-11T21:00-07:00. Note: end-of-day check-in\n" +
				"[wake-up request refused] " + OutboxFile + `: "at" is missing. The next wake-up above is unchanged.` + "\n\n"},
	} {
		if got := Header(now, loc, tc.st); got != tc.want {
			t.Errorf("header for %+v:\n%s\nwant:\n%s", tc.st, got, tc.want)
		}
	}
	if _, err := Location("Mars/Olympus"); err == nil {
		t.Error("unknown timezone accepted")
	}
}

func TestWakeupPrompt(t *testing.T) {
	at := time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC)
	if got := WakeupPrompt(Wakeup{At: at, Note: "check-in"}, time.UTC); got != "[wake-up] Set for 2026-10-12T04:00Z. Note: check-in" {
		t.Errorf("prompt %q", got)
	}
	if got := WakeupPrompt(Wakeup{At: at, Note: DefaultNote}, time.UTC); !strings.Contains(got, "no wake-up was requested") {
		t.Errorf("default prompt %q", got)
	}
	if !IsSilent(" <silent>\n") || IsSilent("<silent> but") {
		t.Error("IsSilent")
	}
}

func TestWakeupTurnHasHeaderAndRole(t *testing.T) {
	w := newWorld(t)
	typ := w.state.Types["pi"]
	typ.Session.DefaultWakeupSeconds = 86400
	typ.Session.Timezone = "America/Los_Angeles"
	w.state.Types["pi"] = typ
	w.Now = func() time.Time { return time.Date(2026, 10, 11, 15, 12, 0, 0, time.UTC) }
	pending := &WakeupStatus{Pending: &Wakeup{At: time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC), Note: "check-in"}}
	w.WakeupStatus = func(context.Context, Conversation) (*WakeupStatus, error) { return pending, nil }
	sb := w.ensure(t)
	if !sb.Wakeups {
		t.Errorf("ensure: %+v", sb)
	}

	w.turn(t, &sb, "t1", "hello")
	res, err := w.RunTurn(context.Background(), TurnInput{Conversation: c1, Sandbox: sb.Sandbox, Turn: "wakeup-r1", Message: "[wake-up] Set for ...", Wakeup: true})
	if err != nil {
		t.Fatal(err)
	}
	header := "[now] 2026-10-11T08:12-07:00 (America/Los_Angeles)\n[next wake-up] 2026-10-11T21:00-07:00. Note: check-in\n\n"
	if w.prompts[0] != header+"hello" || w.prompts[1] != header+"[wake-up] Set for ..." {
		t.Errorf("prompts %q", w.prompts)
	}
	// The header is not recorded; the wake-up's prompt is, as role wakeup.
	if got, ok, _ := w.Store.TurnMessage(context.Background(), "pi", "c1", "wakeup-r1", store.RoleWakeup); !ok || got != "[wake-up] Set for ..." {
		t.Errorf("recorded wake-up %q %v", got, ok)
	}
	if !strings.HasPrefix(res.Reply, "re: [now]") {
		t.Errorf("reply %q", res.Reply)
	}
}
