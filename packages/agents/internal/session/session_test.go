package session

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"homelab/agents/internal/archive"
	"homelab/agents/internal/desired"
	"homelab/agents/internal/gateway"
	"homelab/agents/internal/store"
	"homelab/agents/internal/store/storetest"
)

// world is the activities with a fake gateway and archive, a test Postgres,
// and a published type "pi". pi in the fake sandboxes replies "re: <prompt>"
// and records each prompt.
type world struct {
	*Activities
	gw      *gateway.Fake
	archive *archive.Fake
	state   *desired.State
	prompts []string
	// sessionFiles is how many pi session files `ls` finds in a sandbox.
	sessionFiles int
}

func newWorld(t *testing.T) *world {
	cpu := "1"
	w := &world{
		gw:      gateway.NewFake(),
		archive: archive.NewFake(),
		state: &desired.State{Types: map[string]desired.Type{"pi": {
			Image:     desired.Image{Ref: "oci.garvey.sh/pi-agent:abc", Cmd: []string{"/bin/sleep", "infinity"}},
			Policy:    desired.Policy{Spec: map[string]any{"version": 1, "landlock": map[string]any{"compatibility": "best_effort"}}},
			Providers: []string{"litellm"},
			Env:       map[string]string{"A": "b"},
			Resources: desired.Resources{CPU: &cpu},
			Session:   desired.Session{IdleTimeoutSeconds: 60, RetentionSeconds: 600, PrimeMessages: 2},
			Version:   "v1",
		}}},
		sessionFiles: 1,
	}
	w.gw.ExecFunc = func(name string, argv []string) (gateway.ExecResult, error) {
		switch argv[0] {
		case "true":
			return gateway.ExecResult{}, nil
		case "pi":
			prompt := argv[len(argv)-1]
			w.prompts = append(w.prompts, prompt)
			return gateway.ExecResult{Stdout: []byte("re: " + prompt + "\n")}, nil
		case "sh":
			if w.sessionFiles != 1 {
				return gateway.ExecResult{ExitCode: 3, Stderr: []byte("want one session file")}, nil
			}
			return gateway.ExecResult{Stdout: []byte(`{"session":"` + name + `"}` + "\n")}, nil
		}
		return gateway.ExecResult{ExitCode: 127}, nil
	}
	w.Activities = &Activities{
		Gateway: w.gw,
		Store:   storetest.Open(t, "agents_session_test"),
		Archive: w.archive,
		State:   func() (*desired.State, error) { return w.state, nil },
	}
	return w
}

var c1 = Conversation{Type: "pi", Conversation: "c1"}

func (w *world) ensure(t *testing.T) EnsureResult {
	t.Helper()
	res, err := w.EnsureSandbox(context.Background(), EnsureInput{c1})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// turn runs a turn in sb, primed if sb was just created, as the workflow does.
func (w *world) turn(t *testing.T, sb *EnsureResult, turn, msg string) string {
	t.Helper()
	res, err := w.RunTurn(context.Background(), TurnInput{Conversation: c1, Sandbox: sb.Sandbox, Turn: turn, Message: msg, Prime: sb.Created})
	sb.Created = false
	if err != nil {
		t.Fatal(err)
	}
	return res.Reply
}

func (w *world) row(t *testing.T) *store.Session {
	t.Helper()
	row, err := w.Store.Session(context.Background(), "pi", "c1")
	if err != nil || row == nil {
		t.Fatalf("session row: %v %v", row, err)
	}
	return row
}

func (w *world) callsTo(method string) int {
	n := 0
	for _, c := range w.gw.Calls {
		if strings.HasPrefix(c, method+" ") {
			n++
		}
	}
	return n
}

func TestFirstMessageCreatesSandbox(t *testing.T) {
	w := newWorld(t)
	sb := w.ensure(t)
	if !sb.Created || sb.Sandbox != store.SandboxName("pi", "c1", 1) {
		t.Fatalf("ensure: %+v", sb)
	}
	got := w.gw.Sandboxes[sb.Sandbox].Spec
	if got.Image != "oci.garvey.sh/pi-agent:abc" || got.Command[0] != "/bin/sleep" || got.Providers[0] != "litellm" ||
		*got.CPU != "1" || got.Env["A"] != "b" || got.Policy == nil {
		t.Errorf("spec: %+v", got)
	}
	if got.Labels[ManagedByLabel] != ManagedBy || got.Labels[ConversationLabel] != store.ConversationHash("pi", "c1") {
		t.Errorf("labels: %v", got.Labels)
	}
	if got.PodLabels[TypePodLabel] != "pi" {
		t.Errorf("pod labels: %v", got.PodLabels)
	}
	if row := w.row(t); row.State != store.Running || row.TypeVersion != "v1" {
		t.Errorf("row: %+v", row)
	}

	if reply := w.turn(t, &sb, "t1", "hello"); reply != "re: hello" {
		t.Errorf("reply %q", reply)
	}
	// Nothing to prime from yet.
	if w.prompts[0] != "hello" {
		t.Errorf("first prompt %q", w.prompts[0])
	}

	again := w.ensure(t)
	if again.Created || again.Sandbox != sb.Sandbox || w.callsTo("create") != 1 {
		t.Errorf("second ensure: %+v, calls %v", again, w.gw.Calls)
	}
}

func TestIdleStopThenResume(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	sb := w.ensure(t)
	w.turn(t, &sb, "t1", "hello")

	ref := SandboxRef{Conversation: c1, Sandbox: sb.Sandbox}
	if err := w.StopSandbox(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := w.StopSandbox(ctx, ref); err != nil {
		t.Fatalf("stopping again: %v", err)
	}
	if row := w.row(t); row.State != store.Stopped || row.StoppedAt == nil {
		t.Errorf("row: %+v", row)
	}
	if w.gw.Sandboxes[sb.Sandbox].Phase != gateway.PhaseStopped {
		t.Errorf("phase %s", w.gw.Sandboxes[sb.Sandbox].Phase)
	}

	resumed := w.ensure(t)
	if resumed.Created || resumed.Sandbox != sb.Sandbox || w.callsTo("start") != 1 {
		t.Errorf("resume: %+v, calls %v", resumed, w.gw.Calls)
	}
	if row := w.row(t); row.State != store.Running {
		t.Errorf("row: %+v", row)
	}
	// pi has its own session: not primed.
	w.turn(t, &resumed, "t2", "again")
	if w.prompts[1] != "again" {
		t.Errorf("prompt after resume %q", w.prompts[1])
	}
}

func TestTypeChangeRecreatesAndPrimes(t *testing.T) {
	w := newWorld(t)
	sb := w.ensure(t)
	w.turn(t, &sb, "t1", "one")
	w.turn(t, &sb, "t2", "two")

	w.state.Types["pi"] = func(t desired.Type) desired.Type { t.Version = "v2"; return t }(w.state.Types["pi"])
	next := w.ensure(t)
	if !next.Created || next.Sandbox != store.SandboxName("pi", "c1", 2) {
		t.Fatalf("ensure: %+v", next)
	}
	if _, ok := w.gw.Sandboxes[sb.Sandbox]; ok {
		t.Error("old sandbox not deleted")
	}
	if row := w.row(t); row.Generation != 2 || row.TypeVersion != "v2" || row.State != store.Running {
		t.Errorf("row: %+v", row)
	}

	w.turn(t, &next, "t3", "three")
	prompt := w.prompts[len(w.prompts)-1]
	// The last two messages (primeMessages), not the first, then the new one.
	if strings.Contains(prompt, ">\none\n<") || !strings.Contains(prompt, "\ntwo\n") ||
		!strings.Contains(prompt, "re: two") || !strings.HasSuffix(prompt, "The new message:\n\nthree") {
		t.Errorf("primed prompt:\n%s", prompt)
	}
}

func TestLostSandbox(t *testing.T) {
	for _, lose := range []struct {
		name string
		do   func(w *world, sandbox string)
	}{
		{"main process exited", func(w *world, sb string) { w.gw.SetPhase(sb, gateway.PhaseError) }},
		{"deleted by hand", func(w *world, sb string) { delete(w.gw.Sandboxes, sb) }},
	} {
		t.Run(lose.name, func(t *testing.T) {
			w := newWorld(t)
			sb := w.ensure(t)
			w.turn(t, &sb, "t1", "hello")
			lose.do(w, sb.Sandbox)

			_, err := w.RunTurn(context.Background(), TurnInput{Conversation: c1, Sandbox: sb.Sandbox, Turn: "t2", Message: "still there?"})
			if !errors.Is(err, ErrSandboxLost) {
				t.Fatalf("turn in a lost sandbox: %v", err)
			}
			next := w.ensure(t)
			if !next.Created || next.Sandbox != store.SandboxName("pi", "c1", 2) {
				t.Fatalf("ensure: %+v", next)
			}
			// The retried turn is primed with the first turn, and its own
			// message is not repeated in the history.
			w.turn(t, &next, "t2", "still there?")
			prompt := w.prompts[len(w.prompts)-1]
			if !strings.Contains(prompt, "re: hello") || strings.Count(prompt, "still there?") != 1 {
				t.Errorf("primed prompt:\n%s", prompt)
			}
		})
	}
}

func TestStoppedSandboxClaimedByReaper(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	sb := w.ensure(t)
	w.StopSandbox(ctx, SandboxRef{Conversation: c1, Sandbox: sb.Sandbox})
	// The reaper claims the row before the message's EnsureSandbox.
	if ok, _ := w.Store.Transition(ctx, "pi", "c1", sb.Sandbox, []store.State{store.Stopped}, store.Deleting); !ok {
		t.Fatal("claim failed")
	}
	next := w.ensure(t)
	if !next.Created || next.Sandbox == sb.Sandbox {
		t.Errorf("ensure: %+v", next)
	}
	if w.callsTo("start") != 0 {
		t.Error("started a sandbox the reaper claimed")
	}
	// The reaper's own delete of the old sandbox still succeeds and leaves
	// the new row alone.
	if err := w.DeleteSandbox(ctx, SandboxRef{Conversation: c1, Sandbox: sb.Sandbox}); err != nil {
		t.Fatal(err)
	}
	if row := w.row(t); row.Sandbox != next.Sandbox || row.State != store.Running {
		t.Errorf("row: %+v", row)
	}
}

func TestStopOfOldGenerationIsIgnored(t *testing.T) {
	w := newWorld(t)
	sb := w.ensure(t)
	w.gw.SetPhase(sb.Sandbox, gateway.PhaseError)
	next := w.ensure(t)
	if err := w.StopSandbox(context.Background(), SandboxRef{Conversation: c1, Sandbox: sb.Sandbox}); err != nil {
		t.Fatal(err)
	}
	if row := w.row(t); row.Sandbox != next.Sandbox || row.State != store.Running {
		t.Errorf("row: %+v", row)
	}
	if w.gw.Sandboxes[next.Sandbox].Phase != gateway.PhaseReady {
		t.Error("the new sandbox was stopped")
	}
}

func TestRetriedTurnRunsPiOnce(t *testing.T) {
	w := newWorld(t)
	sb := w.ensure(t)
	first := w.turn(t, &sb, "t1", "hello")
	second := w.turn(t, &sb, "t1", "hello")
	if first != second || len(w.prompts) != 1 {
		t.Errorf("replies %q %q, pi ran %d times", first, second, len(w.prompts))
	}
}

func TestPiFailureIsAnError(t *testing.T) {
	w := newWorld(t)
	sb := w.ensure(t)
	w.gw.ExecFunc = func(string, []string) (gateway.ExecResult, error) {
		return gateway.ExecResult{ExitCode: 1, Stderr: []byte("model unavailable")}, nil
	}
	_, err := w.RunTurn(context.Background(), TurnInput{Conversation: c1, Sandbox: sb.Sandbox, Turn: "t1", Message: "hi"})
	if err == nil || !strings.Contains(err.Error(), "model unavailable") || errors.Is(err, ErrSandboxLost) {
		t.Errorf("err %v", err)
	}
	if _, ok, _ := w.Store.TurnMessage(context.Background(), "pi", "c1", "t1", store.RoleAssistant); ok {
		t.Error("a failed turn recorded a reply")
	}
}

func TestArchiveSession(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	sb := w.ensure(t)
	ref := SandboxRef{Conversation: c1, Sandbox: sb.Sandbox}
	if err := w.ArchiveSession(ctx, ref); err != nil {
		t.Fatal(err)
	}
	key := "pi/c1/" + sb.Sandbox + ".jsonl"
	if got := string(w.archive.Objects[key]); got != `{"session":"`+sb.Sandbox+`"}`+"\n" {
		t.Errorf("object %s: %q (have %v)", key, got, w.archive.Objects)
	}

	w.sessionFiles = 2
	if err := w.ArchiveSession(ctx, ref); err == nil {
		t.Error("archived with two session files")
	}
	w.sessionFiles = 1
	w.archive.Err = errors.New("rgw down")
	if err := w.ArchiveSession(ctx, ref); err == nil {
		t.Error("archive error swallowed")
	}
}

func TestInvalidRequests(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	for _, in := range []Conversation{{"pi", "has space"}, {"pi", ""}, {"pi", "-dash"}, {"pi", "a/b"}, {"nope", "c1"}, {"", "c1"}} {
		if _, err := w.EnsureSandbox(ctx, EnsureInput{in}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", in, err)
		}
	}
}

func TestTurnHeartbeats(t *testing.T) {
	w := newWorld(t)
	sb := w.ensure(t)
	var beats atomic.Int32
	w.Heartbeat = func(context.Context) { beats.Add(1) }
	w.HeartbeatInterval = time.Millisecond
	w.gw.ExecFunc = func(string, []string) (gateway.ExecResult, error) {
		time.Sleep(50 * time.Millisecond)
		return gateway.ExecResult{Stdout: []byte("done")}, nil
	}
	w.turn(t, &sb, "t1", "slow")
	if beats.Load() == 0 {
		t.Error("no heartbeat while pi ran")
	}
}

func TestPiArgv(t *testing.T) {
	for prompt, want := range map[string]string{"hi": "hi", "-v": " -v", "@/etc/passwd": " @/etc/passwd"} {
		argv := PiArgv("c1", prompt)
		if strings.Join(argv[:4], " ") != "pi -p --session-id c1" || argv[4] != want {
			t.Errorf("%q: %q", prompt, argv)
		}
	}
}

func TestPrimedPromptFits(t *testing.T) {
	big := strings.Repeat("x", MaxPromptBytes/2)
	history := []store.Message{{Role: "user", Content: "FIRSTMSG " + big}, {Role: "assistant", Content: big}, {Role: "user", Content: "newest"}}
	p := PrimedPrompt(history, "now")
	if len(p) > MaxPromptBytes || strings.Contains(p, "FIRSTMSG") || !strings.Contains(p, "newest") {
		t.Errorf("prompt of %d bytes, first kept: %v", len(p), strings.Contains(p, "FIRSTMSG"))
	}
	if PrimedPrompt(nil, "now") != "now" {
		t.Error("no history should give the message alone")
	}
}
