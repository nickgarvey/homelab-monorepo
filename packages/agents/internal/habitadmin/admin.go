// Package habitadmin is the admin chat for Habit Helper: commands, not an
// agent, that the Matrix bridge runs for @habit-admin's direct chat with an
// allowed user (agents/README.md, "Matrix"). They list, archive and delete
// habits, override their cadence, and show, set and cancel Habit Helper's
// wake-up in its chat with that user. Data goes through PostgREST as role
// habit_admin, which sees archived habits and may delete; wake-ups through
// the conversation's Wakeup workflow.
package habitadmin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"homelab/agents/internal/session"
)

// Data is Habit Helper's data, as habit_admin (Store).
type Data interface {
	Habits(ctx context.Context) ([]Habit, error)
	Cadences(ctx context.Context) (map[string]Cadence, error)
	Entries(ctx context.Context, habit, since string) ([]Entry, error)
	Count(ctx context.Context, habit string) (Deleted, error)
	UpdateHabit(ctx context.Context, name string, body map[string]any) error
	UpdateCadence(ctx context.Context, habit string, body map[string]any) (Cadence, error)
	Delete(ctx context.Context, habit string) (Deleted, error)
}

// Wakeups acts on Habit Helper conversations' wake-ups (TemporalWakeups).
type Wakeups interface {
	Pending(ctx context.Context, conv string) (*session.WakeupStatus, error)
	Set(ctx context.Context, conv string, w session.Wakeup, defaultSeconds int) error
	Cancel(ctx context.Context, conv string) (bool, error)
}

// Admin runs the commands.
type Admin struct {
	Data    Data
	Wakeups Wakeups
	// Conversation is Habit Helper's conversation with the user: its direct
	// chat.
	Conversation func(ctx context.Context, user string) (string, error)
	// Session is Habit Helper's session settings now: its time zone and
	// default wake-up.
	Session func() (Timezone string, DefaultWakeupSeconds int, err error)
	Now     func() time.Time

	mu sync.Mutex
	// deletes are the permanent deletes waiting for each user's "yes".
	deletes map[string]pendingDelete
}

type pendingDelete struct {
	habit   string
	expires time.Time
}

// confirmWithin is how long a permanent delete waits for "yes".
const confirmWithin = 5 * time.Minute

const (
	// adminNote is the note of a wake-up set without one.
	adminNote = "set from the admin chat"
	// overrideReason is the reason recorded on a cadence override.
	overrideReason = "set from the admin chat"
	// maxWakeupLead is the furthest ahead a wake-up may be set.
	maxWakeupLead = session.MaxWakeupLead
)

const help = "Commands:\n" +
	"```\n" +
	"habits [--all]                 habits and next check-ins; --all adds archived\n" +
	"show <habit>                   one habit, with its last 14 days\n" +
	"archive <habit>                hide it from Habit Helper (restorable)\n" +
	"unarchive <habit>              bring it back as it was\n" +
	"pause <habit> | resume <habit>\n" +
	"delete <habit> --permanent     delete it and its days and notes for good\n" +
	"cadence <habit> every <N>d [next <date>]   override the check-in interval\n" +
	"cadence <habit> auto           back to the tapering rule\n" +
	"wakeup                         Habit Helper's pending wake-up\n" +
	"wakeup in <5m|2h|1d> [note]    set it (replaces the pending one)\n" +
	"wakeup at <HH:MM|YYYY-MM-DD HH:MM|tomorrow HH:MM> [note]\n" +
	"wakeup cancel                  none until you next write to Habit Helper\n" +
	"```\n" +
	"Dates: YYYY-MM-DD, today, tomorrow or +N (days). Case does not matter in habit names."

// Run runs one message from user and returns the reply.
func (a *Admin) Run(ctx context.Context, user, text string) string {
	reply, err := a.run(ctx, user, text)
	if err != nil {
		return err.Error()
	}
	return reply
}

func (a *Admin) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Admin) loc() (*time.Location, int, error) {
	tz, def, err := a.Session()
	if err != nil {
		return nil, 0, err
	}
	loc, err := session.Location(tz)
	return loc, def, err
}

func (a *Admin) run(ctx context.Context, user, text string) (string, error) {
	// Phones turn -- into a dash.
	text = strings.NewReplacer("—", "--", "–", "--").Replace(strings.TrimSpace(text))
	cmd, rest, _ := strings.Cut(text, " ")
	rest = strings.TrimSpace(rest)
	cmd = strings.ToLower(cmd)
	if cmd != "yes" {
		a.mu.Lock()
		delete(a.deletes, user)
		a.mu.Unlock()
	}
	switch cmd {
	case "help", "?":
		return help, nil
	case "habits", "list":
		return a.habits(ctx, rest)
	case "show":
		return a.show(ctx, rest)
	case "archive":
		return a.archive(ctx, rest, true)
	case "unarchive":
		return a.archive(ctx, rest, false)
	case "pause":
		return a.setStatus(ctx, rest, "paused")
	case "resume":
		return a.setStatus(ctx, rest, "active")
	case "delete":
		return a.delete(ctx, user, rest)
	case "yes":
		return a.confirm(ctx, user)
	case "cadence":
		return a.cadence(ctx, rest)
	case "wakeup", "wake-up":
		return a.wakeup(ctx, user, rest)
	}
	return "", fmt.Errorf("I don't know %q. Send help for the commands.", cmd)
}

// resolve finds a habit by name, ignoring case.
func (a *Admin) resolve(ctx context.Context, name string) (Habit, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Habit{}, errors.New("name the habit")
	}
	hs, err := a.Data.Habits(ctx)
	if err != nil {
		return Habit{}, err
	}
	for _, h := range hs {
		if strings.EqualFold(h.Name, name) {
			return h, nil
		}
	}
	var names []string
	for _, h := range hs {
		names = append(names, h.Name)
	}
	if len(names) == 0 {
		return Habit{}, fmt.Errorf("no habit %q: there are no habits", name)
	}
	return Habit{}, fmt.Errorf("no habit %q. Habits: %s", name, strings.Join(names, ", "))
}

func status(h Habit) string {
	if h.ArchivedAt != nil {
		return "archived (was " + h.Status + ")"
	}
	return h.Status
}

func (a *Admin) habits(ctx context.Context, rest string) (string, error) {
	all := false
	switch rest {
	case "":
	case "--all", "all":
		all = true
	default:
		return "", errors.New("habits takes only --all")
	}
	hs, err := a.Data.Habits(ctx)
	if err != nil {
		return "", err
	}
	cs, err := a.Data.Cadences(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	hidden := 0
	for _, h := range hs {
		if h.ArchivedAt != nil && !all {
			hidden++
			continue
		}
		fmt.Fprintf(&b, "- **%s**: %s", h.Name, status(h))
		if c, ok := cs[h.Name]; ok && h.ArchivedAt == nil && h.Status == "active" {
			fmt.Fprintf(&b, ", next check-in %s, every %s", c.NextCheck, days(c.IntervalDays))
			if c.Reason != nil {
				b.WriteString(" (overridden)")
			}
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		b.WriteString("No habits.\n")
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "%d archived; habits --all lists them too.\n", hidden)
	}
	return strings.TrimSpace(b.String()), nil
}

func days(n int) string {
	if n == 1 {
		return "day"
	}
	return fmt.Sprintf("%d days", n)
}

func (a *Admin) show(ctx context.Context, name string) (string, error) {
	h, err := a.resolve(ctx, name)
	if err != nil {
		return "", err
	}
	loc, _, err := a.loc()
	if err != nil {
		return "", err
	}
	cs, err := a.Data.Cadences(ctx)
	if err != nil {
		return "", err
	}
	today := a.now().In(loc)
	since := today.AddDate(0, 0, -13)
	es, err := a.Data.Entries(ctx, h.Name, since.Format(time.DateOnly))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**: %s\n", h.Name, status(h))
	for _, f := range []struct {
		k string
		v *string
	}{{"Cue", h.Cue}, {"Motivation", h.Motivation}, {"Prep", h.Preparation}} {
		if f.v != nil {
			fmt.Fprintf(&b, "- %s: %s\n", f.k, *f.v)
		}
	}
	if c, ok := cs[h.Name]; ok {
		fmt.Fprintf(&b, "- Cadence: every %s, next check-in %s, streak %d", days(c.IntervalDays), c.NextCheck, c.Streak)
		if c.Reason != nil {
			fmt.Fprintf(&b, " (overridden: %s)", *c.Reason)
		}
		b.WriteString("\n")
	}
	got := map[string]string{}
	for _, e := range es {
		got[e.Day] = e.Outcome
	}
	b.WriteString("- Last 14 days: ")
	for d := since; !d.After(today); d = d.AddDate(0, 0, 1) {
		switch got[d.Format(time.DateOnly)] {
		case "done":
			b.WriteString("✓")
		case "missed":
			b.WriteString("✗")
		default:
			b.WriteString("·")
		}
	}
	b.WriteString(" (✓ done, ✗ missed, · not logged; today last)")
	return b.String(), nil
}

func (a *Admin) archive(ctx context.Context, name string, archive bool) (string, error) {
	h, err := a.resolve(ctx, name)
	if err != nil {
		return "", err
	}
	if archive == (h.ArchivedAt != nil) {
		if archive {
			return h.Name + " is archived already.", nil
		}
		return h.Name + " is not archived.", nil
	}
	var at any
	if archive {
		at = a.now().UTC()
	}
	if err := a.Data.UpdateHabit(ctx, h.Name, map[string]any{"archived_at": at}); err != nil {
		return "", err
	}
	if archive {
		return fmt.Sprintf("Archived %s. Habit Helper no longer sees it. Restore with: unarchive %s", h.Name, h.Name), nil
	}
	return fmt.Sprintf("%s is back, %s.", h.Name, h.Status), nil
}

func (a *Admin) setStatus(ctx context.Context, name, to string) (string, error) {
	h, err := a.resolve(ctx, name)
	if err != nil {
		return "", err
	}
	if h.ArchivedAt != nil {
		return "", fmt.Errorf("%s is archived; unarchive it first", h.Name)
	}
	if h.Status == to {
		return fmt.Sprintf("%s is %s already.", h.Name, to), nil
	}
	if err := a.Data.UpdateHabit(ctx, h.Name, map[string]any{"status": to}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s is %s (was %s).", h.Name, to, h.Status), nil
}

func (a *Admin) delete(ctx context.Context, user, rest string) (string, error) {
	name, permanent := strings.CutSuffix(rest, "--permanent")
	if !permanent {
		return "", errors.New("delete removes a habit for good, so it needs --permanent: delete <habit> --permanent. " +
			"To hide a habit and keep its data, archive it.")
	}
	h, err := a.resolve(ctx, name)
	if err != nil {
		return "", err
	}
	n, err := a.Data.Count(ctx, h.Name)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.deletes == nil {
		a.deletes = map[string]pendingDelete{}
	}
	a.deletes[user] = pendingDelete{habit: h.Name, expires: a.now().Add(confirmWithin)}
	a.mu.Unlock()
	return fmt.Sprintf("This deletes **%s** for good: %d entries, %d notes and its cadence. "+
		"Reply yes within 5 minutes to confirm; anything else cancels.", h.Name, n.Entries, n.Notes), nil
}

func (a *Admin) confirm(ctx context.Context, user string) (string, error) {
	a.mu.Lock()
	p, ok := a.deletes[user]
	delete(a.deletes, user)
	a.mu.Unlock()
	if !ok || a.now().After(p.expires) {
		return "", errors.New("nothing to confirm")
	}
	d, err := a.Data.Delete(ctx, p.habit)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted %s for good, with %d entries and %d notes.", p.habit, d.Entries, d.Notes), nil
}

// cadence: <habit> every <N>d [next <date>], or <habit> auto. The habit
// name may have spaces, so the command is read from the end.
func (a *Admin) cadence(ctx context.Context, rest string) (string, error) {
	usage := errors.New("cadence <habit> every <N>d [next <date>], or cadence <habit> auto")
	loc, _, err := a.loc()
	if err != nil {
		return "", err
	}
	today := a.now().In(loc)
	if name, ok := cutWord(rest, "auto"); ok && !strings.Contains(strings.ToLower(name), " every ") {
		h, err := a.resolve(ctx, name)
		if err != nil {
			return "", err
		}
		if _, err := a.Data.UpdateCadence(ctx, h.Name, map[string]any{"reason": nil}); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s follows the tapering rule again from the next day logged.", h.Name), nil
	}
	i := strings.LastIndex(strings.ToLower(rest), " every ")
	if i < 0 {
		return "", usage
	}
	name, spec := rest[:i], strings.Fields(rest[i+len(" every "):])
	if len(spec) != 1 && len(spec) != 3 {
		return "", usage
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(spec[0]), "d"))
	if err != nil || n < 1 || n > 60 {
		return "", fmt.Errorf("every %s: give 1d to 60d", spec[0])
	}
	next := today.AddDate(0, 0, n)
	if len(spec) == 3 {
		if !strings.EqualFold(spec[1], "next") {
			return "", usage
		}
		next, err = parseDay(spec[2], today)
		if err != nil {
			return "", err
		}
		if next.Format(time.DateOnly) < today.Format(time.DateOnly) {
			return "", fmt.Errorf("next %s is before today", spec[2])
		}
	}
	h, err := a.resolve(ctx, name)
	if err != nil {
		return "", err
	}
	c, err := a.Data.UpdateCadence(ctx, h.Name, map[string]any{
		"interval_days": n, "next_check": next.Format(time.DateOnly), "reason": overrideReason,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: every %s, next check-in %s, until cadence %s auto.", h.Name, days(c.IntervalDays), c.NextCheck, h.Name), nil
}

// cutWord removes word from the end of s, ignoring case.
func cutWord(s, word string) (string, bool) {
	f := strings.Fields(s)
	if len(f) < 2 || !strings.EqualFold(f[len(f)-1], word) {
		return s, false
	}
	return strings.TrimSpace(s[:strings.LastIndex(strings.ToLower(s), strings.ToLower(word))]), true
}

// parseDay reads YYYY-MM-DD, today, tomorrow or +N, as a day in today's
// location.
func parseDay(s string, today time.Time) (time.Time, error) {
	switch v := strings.ToLower(s); {
	case v == "today":
		return today, nil
	case v == "tomorrow":
		return today.AddDate(0, 0, 1), nil
	case strings.HasPrefix(v, "+"):
		if n, err := strconv.Atoi(strings.TrimSuffix(v[1:], "d")); err == nil && n >= 0 {
			return today.AddDate(0, 0, n), nil
		}
	default:
		if d, err := time.ParseInLocation(time.DateOnly, v, today.Location()); err == nil {
			return d, nil
		}
	}
	return time.Time{}, fmt.Errorf("date %q: give YYYY-MM-DD, today, tomorrow or +N", s)
}

func (a *Admin) wakeup(ctx context.Context, user, rest string) (string, error) {
	loc, def, err := a.loc()
	if err != nil {
		return "", err
	}
	conv, err := a.Conversation(ctx, user)
	if err != nil {
		return "", err
	}
	now := a.now()
	sub, args, _ := strings.Cut(rest, " ")
	args = strings.TrimSpace(args)
	switch strings.ToLower(sub) {
	case "":
		st, err := a.Wakeups.Pending(ctx, conv)
		if err != nil {
			return "", err
		}
		switch {
		case st == nil:
			return "No wake-up pending: there will be none until you next write to Habit Helper.", nil
		case st.Pending == nil:
			return "A wake-up turn is running now.", nil
		}
		return "Next wake-up: " + describe(*st.Pending, now, loc), nil
	case "cancel":
		had, err := a.Wakeups.Cancel(ctx, conv)
		if err != nil {
			return "", err
		}
		if !had {
			return "No wake-up was pending.", nil
		}
		return "Cancelled. Habit Helper won't wake up until you next write to it.", nil
	case "in", "at":
	default:
		return "", errors.New("wakeup, wakeup in <5m|2h|1d> [note], wakeup at <time> [note], or wakeup cancel")
	}
	if def == 0 {
		return "", errors.New("Habit Helper has no wake-ups (no session.defaultWakeup)")
	}
	var at time.Time
	var note string
	if strings.EqualFold(sub, "in") {
		when, n, _ := strings.Cut(args, " ")
		d, err := parseDuration(when)
		if err != nil {
			return "", err
		}
		at, note = now.Add(d), n
	} else {
		at, note, err = parseAt(args, now.In(loc))
		if err != nil {
			return "", err
		}
	}
	if !at.After(now) {
		return "", fmt.Errorf("%s has passed", at.In(loc).Format("Mon 2006-01-02 15:04"))
	}
	if at.Sub(now) > maxWakeupLead {
		return "", errors.New("a wake-up can be at most 60 days ahead")
	}
	note = unquote(note)
	if note == "" {
		note = adminNote
	}
	if n := []rune(note); len(n) > session.MaxNoteChars {
		return "", fmt.Errorf("the note is %d characters; keep it under %d", len(n), session.MaxNoteChars)
	}
	w := session.Wakeup{At: at.UTC().Truncate(time.Second), Note: note}
	if err := a.Wakeups.Set(ctx, conv, w, def); err != nil {
		return "", err
	}
	return "Set. Next wake-up: " + describe(w, now, loc), nil
}

func describe(w session.Wakeup, now time.Time, loc *time.Location) string {
	return fmt.Sprintf("%s (in %s): %s", w.At.In(loc).Format("Mon 2006-01-02 15:04 MST"), human(w.At.Sub(now)), w.Note)
}

func human(d time.Duration) string {
	if d < 0 {
		return "the past"
	}
	d = d.Round(time.Minute)
	switch day := 24 * time.Hour; {
	case d >= 2*day:
		return fmt.Sprintf("%dd %dh", d/day, (d%day)/time.Hour)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", d/time.Hour, (d%time.Hour)/time.Minute)
	}
	return fmt.Sprintf("%dm", d/time.Minute)
}

// parseDuration reads Go durations (5m, 2h, 1h30m) and whole days (1d, 3d).
func parseDuration(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(strings.ToLower(s), "d"); ok {
		if v, err := strconv.Atoi(n); err == nil && v > 0 {
			return time.Duration(v) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < time.Minute {
		return 0, fmt.Errorf("in %q: give a time like 5m, 2h, 1h30m or 1d, at least a minute", s)
	}
	return d, nil
}

// parseAt reads HH:MM (today, or tomorrow if past), tomorrow HH:MM, or
// <date> HH:MM, then the note.
func parseAt(s string, now time.Time) (time.Time, string, error) {
	f := strings.Fields(s)
	bad := fmt.Errorf("at %q: give HH:MM, tomorrow HH:MM or YYYY-MM-DD HH:MM", s)
	if len(f) == 0 {
		return time.Time{}, "", bad
	}
	day, clock, used := now, f[0], 1
	if !strings.Contains(f[0], ":") {
		if len(f) < 2 {
			return time.Time{}, "", bad
		}
		d, err := parseDay(f[0], now)
		if err != nil {
			return time.Time{}, "", bad
		}
		day, clock, used = d, f[1], 2
	}
	hm, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, "", bad
	}
	at := time.Date(day.Year(), day.Month(), day.Day(), hm.Hour(), hm.Minute(), 0, 0, now.Location())
	if used == 1 && !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at, strings.Join(f[used:], " "), nil
}

// unquote strips one pair of quotes, straight or curly (phones type those).
func unquote(s string) string {
	s = strings.TrimSpace(s)
	for _, q := range [][2]string{{`"`, `"`}, {"“", "”"}, {"'", "'"}, {"‘", "’"}} {
		if len(s) >= len(q[0])+len(q[1]) && strings.HasPrefix(s, q[0]) && strings.HasSuffix(s, q[1]) {
			return strings.TrimSpace(s[len(q[0]) : len(s)-len(q[1])])
		}
	}
	return s
}
