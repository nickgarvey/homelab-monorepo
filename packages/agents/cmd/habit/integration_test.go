package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"homelab/agents/internal/habittest"
)

// The integration test runs every command against PostgREST serving the
// habit-helper schema on a throwaway Postgres (internal/habittest), with
// tokens for role habit_agent. In front of PostgREST a proxy stands in for
// the sandbox supervisor: it injects the token and allows only GET, POST and
// PATCH, like the habit-data profile.

func TestIntegration(t *testing.T) {
	pgrst := habittest.Start(t)

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
	r.Header.Set("Authorization", "Bearer "+habittest.Token(s.t, "habit_agent"))
	p.ServeHTTP(w, r)
}

// An archived habit (set by habit_admin, the admin chat's role) is gone for
// the coach: no command sees it or can change it, and its role can neither
// archive nor delete.
func TestArchivedHabitIsHiddenFromTheCoach(t *testing.T) {
	pgrst := habittest.Start(t)
	srv := httptest.NewServer(&supervisor{t: t, target: pgrst})
	defer srv.Close()
	it := &itApp{testApp: newTestApp(t, srv.URL, time.Now())}
	today := dayOf(it.now.In(mustLoc(t))).String()
	for _, name := range []string{"walk", "stretch"} {
		it.ok("add", name, "--cue", "if I finish lunch, then I "+name, "--motivation", "m", "--prep", "p")
	}
	it.ok("log", "walk", today, "done")
	it.ok("note", "walk", "motivation", "fresh air")

	send := func(token, method, path, body string) int {
		t.Helper()
		req, _ := http.NewRequest(method, pgrst+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	coach, admin := habittest.Token(t, "habit_agent"), habittest.Token(t, "habit_admin")

	// The coach cannot archive, nor delete.
	if code := send(coach, "PATCH", "/habits?name=eq.walk", `{"archived_at":"2026-10-14T00:00:00Z"}`); code < 400 {
		t.Errorf("coach archiving: HTTP %d", code)
	}
	if code := send(coach, "POST", "/rpc/delete_habit", `{"habit_name":"walk"}`); code < 400 {
		t.Errorf("coach deleting: HTTP %d", code)
	}

	if code := send(admin, "PATCH", "/habits?name=eq.walk", `{"archived_at":"2026-10-14T00:00:00Z"}`); code >= 300 {
		t.Fatalf("admin archiving: HTTP %d", code)
	}
	if out := it.ok("list", "--all"); strings.Contains(out, "walk") || !strings.Contains(out, "stretch") {
		t.Errorf("list --all after archiving walk: %q", out)
	}
	if out := it.ok("summary"); strings.Contains(out, "walk") {
		t.Errorf("summary: %q", out)
	}
	it.fail(1, `no habit "walk"`, "log", "walk", today, "missed")
	it.fail(1, `no habit "walk"`, "show", "walk")
	it.fail(1, "", "add", "walk", "--cue", "if I wake, then I walk", "--motivation", "m", "--prep", "p")

	// Unarchived, it is back with its entries and notes.
	if code := send(admin, "PATCH", "/habits?name=eq.walk", `{"archived_at":null}`); code >= 300 {
		t.Fatalf("admin unarchiving: HTTP %d", code)
	}
	if out := it.ok("show", "walk"); !strings.Contains(out, "fresh air") {
		t.Errorf("show after unarchiving: %q", out)
	}
}
