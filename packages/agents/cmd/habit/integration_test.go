package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The integration test runs every command against PostgREST serving the
// habit-helper schema on a throwaway Postgres, as in the cluster: roles
// habit_helper (owner, applies the migrations), habit_agent and
// habit_helper_api (PostgREST's NOINHERIT login), no anonymous role, JWTs
// with role habit_agent and aud habit-data. In front of PostgREST a proxy
// stands in for the sandbox supervisor: it injects the token and allows only
// GET, POST and PATCH, like the habit-data profile.
//
// The habit-cli build runs it (packages/agents/habit-cli.nix): it needs
// AGENTS_TEST_POSTGRES (postgresqlTestHook's Postgres), HABIT_TEST_SCHEMA
// (agents/habit-helper/schema) and postgrest on PATH.

const testJWTKey = "integration-test-key-0123456789abcdef0123456789abcdef"

func TestIntegration(t *testing.T) {
	schema := os.Getenv("HABIT_TEST_SCHEMA")
	if os.Getenv("AGENTS_TEST_POSTGRES") == "" || schema == "" {
		t.Skip("needs AGENTS_TEST_POSTGRES and HABIT_TEST_SCHEMA; the habit-cli Nix build runs it")
	}
	postgrestBin, err := exec.LookPath("postgrest")
	if err != nil {
		t.Fatal("postgrest is not on PATH")
	}
	ctx := context.Background()
	setupDatabase(t, ctx, schema)
	pgrst := startPostgREST(t, postgrestBin)

	sup := &supervisor{t: t, target: pgrst}
	srv := httptest.NewServer(sup)
	defer srv.Close()

	it := &itApp{testApp: newTestApp(t, srv.URL, time.Now())}
	today := dayOf(it.now.In(mustLoc(t)))

	// Without the supervisor's token PostgREST refuses, and the CLI says why.
	direct := newTestApp(t, pgrst, time.Now())
	if code, _, errOut := direct.run("list"); code != 1 || !strings.Contains(errOut, "HTTP 401") ||
		!strings.Contains(errOut, "habit-data provider") {
		t.Errorf("without a token: exit %d %q", code, errOut)
	}

	if out := it.ok("list"); !strings.Contains(out, "no habits yet") {
		t.Errorf("list: %q", out)
	}

	// Onboarding.
	var added struct {
		Habit   Habit
		Cadence Cadence
	}
	it.json(&added, "add", "meditate", "--cue", "if I pour my morning coffee, then I sit for 5 minutes",
		"--motivation", "calmer mornings")
	if added.Habit.Name != "meditate" || added.Habit.Status != "active" || str(added.Habit.Cue) == "" ||
		added.Habit.Preparation != nil {
		t.Errorf("add: %+v", added.Habit)
	}
	if added.Cadence.NextCheck != today || added.Cadence.IntervalDays != 1 {
		t.Errorf("new habit's cadence: %+v; want due %s every 1", added.Cadence, today)
	}
	it.fail(1, "already exists", "add", "Meditate")
	// Reserved characters of PostgREST's query syntax in a name.
	it.ok("add", "read, 10 pages (fiction).", "--cue", "When I get into bed, then I read")
	if out := it.ok("edit", "MEDITATE", "--prep", "cushion by the window"); !strings.Contains(out, "preparation: cushion by the window") {
		t.Errorf("edit: %q", out)
	}
	it.fail(1, `no habit "medit". Habits: meditate, read, 10 pages (fiction).`, "log", "medit", "done")

	// Tracking and correcting a day.
	yesterday := today.AddDays(-1)
	var logged struct {
		Entry    Entry
		Previous *Entry
		Cadence  Cadence
	}
	it.json(&logged, "log", "meditate", "yesterday", "missed", "overslept")
	if logged.Entry.Day != yesterday || logged.Entry.Outcome != "missed" || str(logged.Entry.Why) != "overslept" || logged.Previous != nil {
		t.Errorf("log: %+v", logged)
	}
	if c := logged.Cadence; c.IntervalDays != 1 || c.Misses != 1 || c.LastCheck != yesterday || c.NextCheck != today.AddDays(1) {
		t.Errorf("cadence after a miss: %+v", c)
	}
	out := it.ok("log", "meditate", yesterday.String(), "done")
	if !strings.Contains(out, "was missed (overslept)") {
		t.Errorf("relog: %q", out)
	}
	var hist struct {
		Days   []dayLine
		Counts counts
	}
	it.json(&hist, "history", "meditate", "--days", "3")
	// The window starts at the habit's first day, yesterday's entry, not 3 days back.
	if len(hist.Days) != 2 || hist.Days[0].Day != yesterday || str(hist.Days[0].Outcome) != "done" || hist.Days[0].Why != nil {
		t.Errorf("history after relog (why cleared): %+v", hist.Days)
	}
	if hist.Counts != (counts{Days: 2, Done: 1, NotLogged: 1}) {
		t.Errorf("history counts %+v", hist.Counts)
	}

	// Ten done days in a row: 1 -> 2 -> 4 -> 7.
	for i := 9; i >= 0; i-- {
		it.json(&logged, "log", "meditate", today.AddDays(-i).String(), "done")
	}
	if c := logged.Cadence; c.IntervalDays != 7 || c.Streak != 10 || c.Misses != 0 || c.NextCheck != today.AddDays(7) || c.Reason != nil {
		t.Errorf("cadence after 10 done days: %+v", c)
	}
	// Today becomes a slip: 9 done days (interval 4), then a miss steps down
	// to 2. Correcting it restores 7 (the rule replays the entries).
	it.json(&logged, "log", "meditate", "today", "missed", "busy")
	if c := logged.Cadence; c.IntervalDays != 2 || c.Misses != 1 || c.NextCheck != today.AddDays(2) {
		t.Errorf("cadence after a slip: %+v", c)
	}
	it.json(&logged, "log", "meditate", "today", "done")
	if c := logged.Cadence; c.IntervalDays != 7 || c.Streak != 10 {
		t.Errorf("cadence after correcting the slip: %+v", c)
	}

	// pending.
	if out := it.ok("pending"); !strings.Contains(out, "pending for "+today.String()) ||
		!strings.Contains(out, "read, 10 pages (fiction).") || !strings.Contains(out, "already logged: meditate done") {
		t.Errorf("pending: %q", out)
	}
	if out := it.ok("pending", today.AddDays(-30).String()); !strings.Contains(out, "no active habits") {
		t.Errorf("pending before the habits existed: %q", out)
	}

	// Notes and show.
	it.ok("note", "meditate", "motivation", "wants", "to", "feel", "less", "rushed")
	var shown struct {
		Habit   Habit
		Cadence Cadence
		Days    []dayLine
		Notes   []Note
	}
	it.json(&shown, "show", "meditate", "--days", "3")
	var kinds []string
	for _, n := range shown.Notes {
		kinds = append(kinds, n.Kind+":"+n.Body)
	}
	if want := []string{"cue:if I pour my morning coffee, then I sit for 5 minutes", "motivation:calmer mornings",
		"prep:cushion by the window", "motivation:wants to feel less rushed"}; strings.Join(kinds, "|") != strings.Join(want, "|") {
		t.Errorf("notes: %q", kinds)
	}
	if len(shown.Days) != 3 || str(shown.Habit.Preparation) != "cushion by the window" {
		t.Errorf("show: %+v", shown)
	}

	// summary.
	var sum struct{ Habits []habitSummary }
	it.json(&sum, "summary")
	if len(sum.Habits) != 2 {
		t.Fatalf("summary: %+v", sum)
	}
	m := sum.Habits[0]
	if m.Habit != "meditate" || m.Streak != 10 || m.LastLogged != today {
		t.Errorf("summary: %+v", m)
	}
	// The habit was added today with entries back to 9 days ago: the 30-day
	// window starts at the first entry.
	if m.Week != (counts{Days: 7, Done: 7}) || m.Month != (counts{Days: 10, Done: 10}) {
		t.Errorf("summary windows: %+v", m)
	}
	if out := it.ok("summary", "read, 10 pages (fiction)."); !strings.Contains(out, "nothing logged yet") {
		t.Errorf("summary of one: %q", out)
	}

	// cadence, override, --auto.
	var cad dueSummary
	it.json(&cad, "cadence")
	if cad.Earliest == nil || *cad.Earliest != today || len(cad.DueHabits) != 1 || cad.DueHabits[0] != "read, 10 pages (fiction)." || cad.Suggested == nil {
		t.Errorf("cadence: %+v", cad)
	}
	out = it.ok("cadence", "meditate", "--every", "3", "--reason", "user asked for fewer check-ins")
	if !strings.Contains(out, "every 3 days, next check "+today.AddDays(3).String()) || !strings.Contains(out, "overridden: user asked") {
		t.Errorf("override: %q", out)
	}
	it.json(&logged, "log", "meditate", "today", "done")
	if c := logged.Cadence; c.IntervalDays != 3 || str(c.Reason) == "" || c.NextCheck != today.AddDays(3) {
		t.Errorf("log keeps the pinned interval: %+v", c)
	}
	it.ok("cadence", "meditate", "--next", "+5", "--reason", "travelling")
	it.json(&cad, "cadence", "meditate")
	if c := cad.Habits[0]; c.IntervalDays != 3 || c.NextCheck != today.AddDays(5) || str(c.Reason) != "travelling" {
		t.Errorf("--next: %+v", c)
	}
	if out := it.ok("cadence", "meditate", "--auto"); !strings.Contains(out, "follows the rule again: every 7 days") {
		t.Errorf("--auto: %q", out)
	}
	it.json(&cad, "cadence", "meditate")
	if c := cad.Habits[0]; c.Reason != nil || c.IntervalDays != 7 {
		t.Errorf("after --auto: %+v", c)
	}

	// Status changes.
	it.ok("pause", "read, 10 pages (fiction).")
	it.fail(1, "already paused", "pause", "read, 10 pages (fiction).")
	if out := it.ok("pending"); !strings.Contains(out, "nothing pending") {
		t.Errorf("pending with the other habit paused: %q", out)
	}
	it.ok("resume", "read, 10 pages (fiction).")
	it.ok("retire", "read, 10 pages (fiction).")
	if out := it.ok("list"); strings.Contains(out, "read, 10") {
		t.Errorf("list shows a retired habit: %q", out)
	}
	if out := it.ok("list", "--all"); !strings.Contains(out, "read, 10 pages (fiction). (retired)") {
		t.Errorf("list --all: %q", out)
	}

	// Rename carries the history.
	it.ok("edit", "meditate", "--name", "Meditation")
	it.json(&hist, "history", "Meditation", "--days", "10")
	if hist.Counts.Done != 10 {
		t.Errorf("history after rename: %+v", hist.Counts)
	}
	it.fail(1, "another habit is already named", "edit", "Meditation", "--name", "read, 10 pages (fiction).")

	// wake-at due: the suggestion from cadence, into the outbox.
	it.json(&cad, "cadence")
	it.ok("wake-at", "due", "end-of-day check-in")
	data, err := os.ReadFile(it.outbox)
	if err != nil {
		t.Fatal(err)
	}
	var req wakeupRequest
	if err := json.Unmarshal(data, &req); err != nil || req.At != *cad.Suggested || req.Note != "end-of-day check-in" {
		t.Errorf("outbox %s; suggestion %s", data, *cad.Suggested)
	}

	// The CLI never sent a token or a DELETE; the proxy put the token on
	// every request.
	sup.mu.Lock()
	defer sup.mu.Unlock()
	if sup.sentAuth != 0 {
		t.Errorf("the CLI sent Authorization on %d requests", sup.sentAuth)
	}
	var methods []string
	for m := range sup.methods {
		methods = append(methods, m)
	}
	sort.Strings(methods)
	if strings.Join(methods, ",") != "GET,PATCH,POST" {
		t.Errorf("methods used: %v", methods)
	}
}

type itApp struct{ *testApp }

func (it *itApp) ok(args ...string) string {
	it.t.Helper()
	code, out, errOut := it.run(args...)
	if code != 0 {
		it.t.Fatalf("habit %q: exit %d: %s", args, code, errOut)
	}
	it.t.Logf("$ habit %s\n%s", strings.Join(args, " "), out)
	return out
}

func (it *itApp) json(v any, args ...string) {
	it.t.Helper()
	out := it.ok(append(args, "--json")...)
	if err := json.Unmarshal([]byte(out), v); err != nil {
		it.t.Fatalf("habit %q --json: %v: %s", args, err, out)
	}
}

func (it *itApp) fail(code int, want string, args ...string) {
	it.t.Helper()
	got, _, errOut := it.run(args...)
	it.t.Logf("$ habit %s\n(exit %d) %s", strings.Join(args, " "), got, errOut)
	if got != code || !strings.Contains(errOut, want) {
		it.t.Errorf("habit %q: exit %d %q; want %d and %q", args, got, errOut, code, want)
	}
}

// supervisor stands in for the sandbox supervisor's proxy.
type supervisor struct {
	t        *testing.T
	target   string
	mu       sync.Mutex
	sentAuth int
	methods  map[string]bool
}

func (s *supervisor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.methods == nil {
		s.methods = map[string]bool{}
	}
	s.methods[r.Method] = true
	if r.Header.Get("Authorization") != "" {
		s.sentAuth++
	}
	s.mu.Unlock()
	switch r.Method {
	case "GET", "POST", "PATCH":
	default:
		http.Error(w, `{"error":"policy_denied"}`, http.StatusForbidden)
		return
	}
	u, _ := url.Parse(s.target)
	p := httputil.NewSingleHostReverseProxy(u)
	r.Header.Set("Authorization", "Bearer "+testJWT(s.t, map[string]any{
		"role": "habit_agent", "aud": "habit-data", "sub": "spiffe://home.garvey.sh/agent/habit-helper/test",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	}))
	p.ServeHTTP(w, r)
}

func testJWT(t *testing.T, claims map[string]any) string {
	enc := base64.RawURLEncoding
	h := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	c, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signing := h + "." + enc.EncodeToString(c)
	mac := hmac.New(sha256.New, []byte(testJWTKey))
	mac.Write([]byte(signing))
	return signing + "." + enc.EncodeToString(mac.Sum(nil))
}

// setupDatabase makes the roles and database as k8s/manifests/postgres does
// and applies the migrations as the owner, each file in a transaction, as
// tern does.
func setupDatabase(t *testing.T, ctx context.Context, schemaDir string) {
	admin, err := pgx.Connect(ctx, "user=postgres dbname=postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	for _, q := range []string{
		"CREATE ROLE habit_helper LOGIN",
		"CREATE ROLE habit_agent NOLOGIN",
		"CREATE ROLE habit_helper_api LOGIN NOINHERIT",
		"GRANT habit_agent TO habit_helper_api",
		"CREATE DATABASE habit_helper OWNER habit_helper",
	} {
		if _, err := admin.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	owner, err := pgx.Connect(ctx, "user=habit_helper dbname=habit_helper")
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	files, err := filepath.Glob(filepath.Join(schemaDir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations in %s: %v", schemaDir, err)
	}
	sort.Strings(files)
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startPostgREST runs PostgREST configured like k8s/manifests/habit-helper's
// Deployment, and returns its URL once it is ready.
func startPostgREST(t *testing.T, bin string) string {
	port, admin := freePort(t), freePort(t)
	host := os.Getenv("PGHOST")
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"PGRST_DB_URI=postgresql:///habit_helper?host="+url.QueryEscape(host)+"&user=habit_helper_api",
		"PGRST_DB_SCHEMAS=habit",
		"PGRST_DB_EXTRA_SEARCH_PATH=",
		"PGRST_DB_CONFIG=false",
		"PGRST_DB_POOL=5",
		"PGRST_JWT_SECRET="+testJWTKey,
		"PGRST_JWT_AUD=habit-data",
		"PGRST_SERVER_HOST=127.0.0.1",
		fmt.Sprintf("PGRST_SERVER_PORT=%d", port),
		fmt.Sprintf("PGRST_ADMIN_SERVER_PORT=%d", admin),
		"PGRST_LOG_LEVEL=warn",
		"LC_ALL=C.UTF-8",
	)
	logFile := filepath.Join(t.TempDir(), "postgrest.log")
	lf, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		lf.Close()
		if t.Failed() {
			log, _ := os.ReadFile(logFile)
			t.Logf("postgrest log:\n%s", log)
		}
	})
	ready := fmt.Sprintf("http://127.0.0.1:%d/ready", admin)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		resp, err := http.Get(ready)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return fmt.Sprintf("http://127.0.0.1:%d", port)
			}
		}
	}
	log, _ := os.ReadFile(logFile)
	t.Fatalf("PostgREST did not get ready:\n%s", log)
	return ""
}
