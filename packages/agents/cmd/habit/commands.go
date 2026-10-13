package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var commands = []*command{
	{
		name: "add", args: `<name> [--cue "if <event>, then <behavior>"] [--motivation "..."] [--prep "..."]`,
		summary: "add a habit",
		help: `Adds an active habit. Its first check-in is due today. The cue, motivation
and preparation given are also kept as notes, so their history starts here.
Missing ones can be added later with habit edit.

  habit add meditate --cue "if I pour my morning coffee, then I sit for 5 minutes" \
    --motivation "calmer mornings" --prep "cushion by the window"`,
		flags:   fieldFlags,
		minArgs: 1, maxArgs: 1,
		run: cmdAdd,
	},
	{
		name: "edit", args: `<habit> [--name N] [--cue C] [--motivation M] [--prep P]`,
		summary: "change a habit's name, cue, motivation or preparation",
		help: `Changes a habit. Each new cue, motivation or preparation is also kept as a
note. A new name carries over to the habit's entries, notes and cadence.`,
		flags: func(fs *flag.FlagSet, o *opts) {
			fieldFlags(fs, o)
			fs.StringVar(&o.name, "name", "", "new `name`")
		},
		minArgs: 1, maxArgs: 1,
		run: cmdEdit,
	},
	{
		name: "pause", args: "<habit>", summary: "pause a habit",
		help:    `Pauses a habit: it leaves pending, summary and cadence until resumed.`,
		minArgs: 1, maxArgs: 1,
		run: statusCmd("paused"),
	},
	{
		name: "resume", args: "<habit>", summary: "make a paused or retired habit active again",
		help:    `Makes a paused or retired habit active again.`,
		minArgs: 1, maxArgs: 1,
		run: statusCmd("active"),
	},
	{
		name: "retire", args: "<habit>", summary: "retire a habit",
		help: `Retires a habit the user has stopped working on. Its history stays; habit
resume brings it back. (Nothing is ever deleted.)`,
		minArgs: 1, maxArgs: 1,
		run: statusCmd("retired"),
	},
	{
		name: "list", args: "[--all]", summary: "list habits",
		help: `Lists active and paused habits with their next check-in; --all adds retired
ones.`,
		flags:   func(fs *flag.FlagSet, o *opts) { fs.BoolVar(&o.all, "all", false, "include retired habits") },
		minArgs: 0, maxArgs: 0,
		run: cmdList,
	},
	{
		name: "show", args: "<habit> [--days N]", summary: "one habit in full",
		help: `Shows a habit: status, cue, motivation, preparation, cadence, the last N days
(default 7) and every note, oldest first.`,
		flags:   func(fs *flag.FlagSet, o *opts) { fs.IntVar(&o.days, "days", 7, "days of entries to show") },
		minArgs: 1, maxArgs: 1,
		run: cmdShow,
	},
	{
		name: "log", args: `<habit> [<date>] done|missed ["<why>"]`, summary: "record a day",
		help: `Records whether the habit was done on a day (default today), with the
user's reason if they gave one. Logging a day again replaces it (a new log
without a reason clears the old reason). Days after today are refused.
Then the habit's cadence is updated by the tapering rule (habit cadence
--help).

  habit log meditate done
  habit log meditate yesterday missed "overslept"
  habit log walk 2026-10-11 done

Dates: ` + dateForms + `.`,
		minArgs: 2, maxArgs: -1,
		run: cmdLog,
	},
	{
		name: "pending", args: "[<date>]", summary: "active habits not logged for a day",
		help: `Lists the active habits with no entry for a day (default today): what the
end-of-day question should cover.`,
		minArgs: 0, maxArgs: 1,
		run: cmdPending,
	},
	{
		name: "history", args: "<habit> [--days N]", summary: "a habit day by day",
		help: `Shows a habit's last N days (default 14, at most 366), one line per day:
done, missed, or - for no entry, with the reason given.`,
		flags:   func(fs *flag.FlagSet, o *opts) { fs.IntVar(&o.days, "days", 14, "how many days back from today") },
		minArgs: 1, maxArgs: 1,
		run: cmdHistory,
	},
	{
		name: "summary", args: "[<habit>]", summary: "streaks and rates",
		help: `For each active habit (or the one named): the current streak (done entries
in a row up to the latest logged day; days with no entry are skipped), and
done/missed/not-logged counts over the last 7 and 30 days (not before the
habit's first day: the day it was added, or its earliest entry).`,
		minArgs: 0, maxArgs: 1,
		run: cmdSummary,
	},
	{
		name: "note", args: `<habit> motivation|cue|prep "<text>"`, summary: "keep what the user said",
		help: `Keeps something the user said about a habit's motivation, cue or
preparation, with the time. Notes add up over time; they do not change the
habit's current cue etc. (habit edit does that, and keeps a note too).`,
		minArgs: 3, maxArgs: -1,
		run: cmdNote,
	},
	{
		name: "cadence", args: `[<habit>] [--every N --reason "..." [--next <date>] | --auto] [--at HH:MM]`,
		summary: "check-in schedule, and overrides",
		help: `Without flags: each active habit's check-in interval, next check, streak and
misses, the earliest due check, and a suggested wake-up for it (at the
check-in time, --at, default ` + defaultCheckin + `; at least 10 minutes ahead), ready
for habit wake-at due.

--every N --reason "...": override the rule for a habit: check every N days
(1 to 60), the next check on --next (default today + N). The interval stays
pinned until --auto. --next alone (with --reason) moves the next check and
pins the current interval.
--auto: drop the override; the rule sets the cadence from the entries again.

` + cadenceRule,
		flags: func(fs *flag.FlagSet, o *opts) {
			fs.IntVar(&o.every, "every", 0, "override: check every `N` days (1 to 60)")
			fs.StringVar(&o.reason, "reason", "", "why the rule is overridden (required with --every and --next)")
			fs.StringVar(&o.next, "next", "", "override: the next check `date`")
			fs.BoolVar(&o.auto, "auto", false, "drop the override and apply the rule again")
			fs.StringVar(&o.at, "at", defaultCheckin, "check-in time of day for the suggested wake-up, `HH:MM`")
		},
		minArgs: 0, maxArgs: 1,
		run: cmdCadence,
	},
	{
		name: "wake-at", args: `<when> "<note>" [--at HH:MM]`, summary: "request a wake-up",
		help: `Asks the agents worker to wake the agent at a time, by writing
` + defaultOutbox + ` ({"at": "<RFC 3339>", "note": "..."}). After this turn
the worker reads it and it replaces the pending wake-up; a later habit
wake-at in the same turn replaces this one. The time must be 10 minutes to
60 days ahead and the note (for your future self) at most 500 characters.

<when>: ` + whenForms + `.
A bare date or "tomorrow" means at the check-in time, --at (default ` + defaultCheckin + `).

  habit wake-at due "end-of-day check-in: meditate, walk"
  habit wake-at tomorrow "ask how the new cue went"
  habit wake-at +3d "light check-in after the trip"`,
		flags: func(fs *flag.FlagSet, o *opts) {
			fs.StringVar(&o.at, "at", defaultCheckin, "check-in time of day for due, tomorrow and bare dates, `HH:MM`")
		},
		minArgs: 2, maxArgs: -1,
		run: cmdWakeAt,
	},
}

func fieldFlags(fs *flag.FlagSet, o *opts) {
	fs.StringVar(&o.cue, "cue", "", `the if/then cue, "if <event>, then <behavior>"`)
	fs.StringVar(&o.motiv, "motivation", "", "why the user wants the habit")
	fs.StringVar(&o.prep, "prep", "", "how the user prepares for it")
}

var cueRE = regexp.MustCompile(`(?is)^\s*(if|when)\b.+\bthen\b.+`)

func checkCue(cue string) error {
	if !cueRE.MatchString(cue) {
		return fmt.Errorf(`--cue %q: a cue is an event-based plan, "if <event>, then <behavior>", like "if I pour my morning coffee, then I sit for 5 minutes"`, cue)
	}
	return nil
}

func checkName(name string) error {
	if name != strings.TrimSpace(name) || name == "" {
		return fmt.Errorf("name %q: no leading or trailing spaces, and not empty", name)
	}
	if utf8.RuneCountInString(name) > 100 {
		return fmt.Errorf("name %q: at most 100 characters", name)
	}
	return nil
}

func checkText(flagName, v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("--%s is empty", flagName)
	}
	return nil
}

// fieldChanges reads --cue/--motivation/--prep: the habits columns to set
// and the notes that keep them.
func fieldChanges(cmd string, o *opts, habit string) (map[string]any, []map[string]any, error) {
	cols := map[string]any{}
	var notes []map[string]any
	for _, f := range []struct{ flag, col, kind, val string }{
		{"cue", "cue", "cue", o.cue},
		{"motivation", "motivation", "motivation", o.motiv},
		{"prep", "preparation", "prep", o.prep},
	} {
		if !o.set[f.flag] {
			continue
		}
		if err := checkText(f.flag, f.val); err != nil {
			return nil, nil, usagef(cmd, "%v", err)
		}
		if f.flag == "cue" {
			if err := checkCue(f.val); err != nil {
				return nil, nil, usagef(cmd, "%v", err)
			}
		}
		v := strings.TrimSpace(f.val)
		cols[f.col] = v
		notes = append(notes, map[string]any{"habit": habit, "kind": f.kind, "body": v})
	}
	return cols, notes, nil
}

func describeHabit(h Habit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s)\n", h.Name, h.Status)
	var missing []string
	for _, f := range []struct {
		label string
		v     *string
		flag  string
	}{{"cue", h.Cue, "--cue"}, {"motivation", h.Motivation, "--motivation"}, {"preparation", h.Preparation, "--prep"}} {
		if f.v == nil {
			missing = append(missing, f.flag)
			continue
		}
		fmt.Fprintf(&b, "  %-12s %s\n", f.label+":", *f.v)
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, "  not set yet: %s (habit edit %q %s ...)\n", strings.Join(missing, ", "), h.Name, missing[0])
	}
	return b.String()
}

func cmdAdd(ctx context.Context, a *app, args []string, o *opts) error {
	name := args[0]
	if err := checkName(name); err != nil {
		return usagef("add", "%v", err)
	}
	cols, notes, err := fieldChanges("add", o, name)
	if err != nil {
		return err
	}
	hs, err := a.habits(ctx)
	if err != nil {
		return err
	}
	for _, h := range hs {
		if strings.EqualFold(h.Name, name) {
			return fmt.Errorf("a habit %q already exists (%s); change it with habit edit, or habit resume it", h.Name, h.Status)
		}
	}
	cols["name"] = name
	var rows []Habit
	if err := a.api.do(ctx, "POST", "/habits", nil, cols, "return=representation", &rows); err != nil {
		if pgCode(err) == pgUniqueViolation {
			return fmt.Errorf("a habit named %q already exists", name)
		}
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("PostgREST returned %d rows for one new habit", len(rows))
	}
	h := rows[0]
	// The row the schema adds is due on the database's (UTC) date.
	cad, err := a.patchCadence(ctx, h.Name, map[string]any{"next_check": a.today()})
	if err != nil {
		return err
	}
	if _, err := a.addNotes(ctx, notes); err != nil {
		return err
	}
	return a.emit(map[string]any{"habit": h, "cadence": cad},
		"added "+describeHabit(h)+fmt.Sprintf("  first check-in: due %s (today)", cad.NextCheck))
}

func cmdEdit(ctx context.Context, a *app, args []string, o *opts) error {
	cols, notes, err := fieldChanges("edit", o, "")
	if err != nil {
		return err
	}
	if o.set["name"] {
		if err := checkName(o.name); err != nil {
			return usagef("edit", "--name: %v", err)
		}
		cols["name"] = o.name
	}
	if len(cols) == 0 {
		return usagef("edit", "nothing to change: give --name, --cue, --motivation or --prep")
	}
	h, err := a.resolve(ctx, args[0])
	if err != nil {
		return err
	}
	rows, err := patch[Habit](ctx, a, "habits", url.Values{"name": {eq(h.Name)}}, cols)
	if err != nil {
		if pgCode(err) == pgUniqueViolation {
			return fmt.Errorf("another habit is already named %q", o.name)
		}
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("habit %q changed while editing; try again", h.Name)
	}
	nh := rows[0]
	for _, n := range notes {
		n["habit"] = nh.Name
	}
	if _, err := a.addNotes(ctx, notes); err != nil {
		return err
	}
	prefix := "updated "
	if nh.Name != h.Name {
		prefix = fmt.Sprintf("renamed %s to ", h.Name)
	}
	return a.emit(nh, prefix+describeHabit(nh))
}

func statusCmd(status string) func(context.Context, *app, []string, *opts) error {
	verb := map[string]string{"active": "resumed", "paused": "paused", "retired": "retired"}[status]
	return func(ctx context.Context, a *app, args []string, o *opts) error {
		h, err := a.resolve(ctx, args[0])
		if err != nil {
			return err
		}
		if h.Status == status {
			return fmt.Errorf("%s is already %s", h.Name, status)
		}
		rows, err := patch[Habit](ctx, a, "habits", url.Values{"name": {eq(h.Name)}}, map[string]any{"status": status})
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return fmt.Errorf("habit %q changed meanwhile; try again", h.Name)
		}
		return a.emit(rows[0], fmt.Sprintf("%s %s (was %s)", verb, h.Name, h.Status))
	}
}

type habitWithCadence struct {
	Habit
	Cadence *Cadence `json:"cadence"`
}

func cmdList(ctx context.Context, a *app, args []string, o *opts) error {
	hs, err := a.habits(ctx)
	if err != nil {
		return err
	}
	cads, err := a.cadences(ctx)
	if err != nil {
		return err
	}
	out := []habitWithCadence{}
	var b strings.Builder
	for _, h := range hs {
		if h.Status == "retired" && !o.all {
			continue
		}
		hc := habitWithCadence{Habit: h}
		if c, ok := cads[h.Name]; ok {
			hc.Cadence = &c
		}
		out = append(out, hc)
		fmt.Fprintf(&b, "%s (%s)", h.Name, h.Status)
		if hc.Cadence != nil && h.Status == "active" {
			fmt.Fprintf(&b, ": every %s, next check %s", days(hc.Cadence.IntervalDays), hc.Cadence.NextCheck)
		}
		if h.Cue != nil {
			fmt.Fprintf(&b, "\n  cue: %s", *h.Cue)
		}
		b.WriteString("\n")
	}
	if len(out) == 0 {
		if len(hs) > 0 {
			b.WriteString("no active or paused habits (habit list --all shows retired ones)")
		} else {
			b.WriteString("no habits yet; add one with habit add")
		}
	}
	return a.emit(out, b.String())
}

func days(n int) string {
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

func checkDays(cmd string, n int) error {
	if n < 1 || n > 366 {
		return usagef(cmd, "--days %d: give 1 to 366", n)
	}
	return nil
}

// dayLine is one day of a habit: its entry, or none.
type dayLine struct {
	Day     Day     `json:"day"`
	Outcome *string `json:"outcome"`
	Why     *string `json:"why"`
}

func window(es []Entry, from, to Day) []dayLine {
	byDay := map[Day]Entry{}
	for _, e := range es {
		byDay[e.Day] = e
	}
	var out []dayLine
	for d := from; !d.After(to); d = d.AddDays(1) {
		l := dayLine{Day: d}
		if e, ok := byDay[d]; ok {
			oc := e.Outcome
			l.Outcome = &oc
			l.Why = e.Why
		}
		out = append(out, l)
	}
	return out
}

func formatDays(b *strings.Builder, lines []dayLine) {
	for _, l := range lines {
		oc := "-"
		if l.Outcome != nil {
			oc = *l.Outcome
		}
		fmt.Fprintf(b, "  %s %s  %-6s", l.Day, l.Day.Weekday(), oc)
		if l.Why != nil {
			fmt.Fprintf(b, "  %s", *l.Why)
		}
		b.WriteString("\n")
	}
}

func describeCadence(c Cadence, today Day) string {
	s := fmt.Sprintf("every %s, next check %s", days(c.IntervalDays), c.NextCheck)
	if !c.NextCheck.After(today) {
		s += " (due)"
	}
	s += fmt.Sprintf("; streak %d, misses in a row %d", c.Streak, c.Misses)
	if c.Reason != nil {
		s += fmt.Sprintf("; overridden: %s", *c.Reason)
	}
	return s
}

func cmdShow(ctx context.Context, a *app, args []string, o *opts) error {
	if err := checkDays("show", o.days); err != nil {
		return err
	}
	h, err := a.resolve(ctx, args[0])
	if err != nil {
		return err
	}
	cad, err := a.cadence(ctx, h.Name)
	if err != nil {
		return err
	}
	es, err := a.entries(ctx, h.Name)
	if err != nil {
		return err
	}
	var notes []Note
	if err := a.api.do(ctx, "GET", "/notes", url.Values{"habit": {eq(h.Name)}, "order": {"at,id"}}, nil, "", &notes); err != nil {
		return err
	}
	today := a.today()
	from := maxDay(today.AddDays(-(o.days - 1)), a.since(h, es))
	lines := window(es, from, today)
	var b strings.Builder
	b.WriteString(describeHabit(h))
	fmt.Fprintf(&b, "  %-12s %s\n", "added:", a.createdDay(h))
	fmt.Fprintf(&b, "  %-12s %s\n", "cadence:", describeCadence(cad, today))
	fmt.Fprintf(&b, "%s:\n", windowLabel(o.days, len(lines)))
	formatDays(&b, lines)
	if len(notes) == 0 {
		b.WriteString("notes: none\n")
	} else {
		b.WriteString("notes, oldest first:\n")
		for _, n := range notes {
			fmt.Fprintf(&b, "  %s %s: %s\n", n.At.In(a.loc).Format("2006-01-02 15:04"), n.Kind, n.Body)
		}
	}
	if notes == nil {
		notes = []Note{}
	}
	return a.emit(map[string]any{"habit": h, "cadence": cad, "days": lines, "notes": notes}, b.String())
}

func cmdLog(ctx context.Context, a *app, args []string, o *opts) error {
	today := a.today()
	rest := args[1:]
	day := today
	if len(rest) > 0 && rest[0] != "done" && rest[0] != "missed" {
		d, err := parseDay(rest[0], today)
		if err != nil {
			return usagef("log", "%v; then done or missed", err)
		}
		day = d
		rest = rest[1:]
	}
	if len(rest) == 0 || (rest[0] != "done" && rest[0] != "missed") {
		got := "nothing"
		if len(rest) > 0 {
			got = fmt.Sprintf("%q", rest[0])
		}
		return usagef("log", "want done or missed after the habit (and the date), got %s", got)
	}
	outcome := rest[0]
	var why any
	if w := strings.TrimSpace(strings.Join(rest[1:], " ")); w != "" {
		why = w
	}
	if day.After(today) {
		return fmt.Errorf("%s is after today (%s, %s); only past days and today can be logged", day, today, a.loc)
	}
	h, err := a.resolve(ctx, args[0])
	if err != nil {
		return err
	}
	var prev []Entry
	if err := a.api.do(ctx, "GET", "/entries", url.Values{"habit": {eq(h.Name)}, "day": {eq(day.String())}}, nil, "", &prev); err != nil {
		return err
	}
	var rows []Entry
	err = a.api.do(ctx, "POST", "/entries", url.Values{"on_conflict": {"habit,day"}},
		map[string]any{"habit": h.Name, "day": day, "outcome": outcome, "why": why},
		"resolution=merge-duplicates,return=representation", &rows)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("PostgREST returned %d rows for one entry", len(rows))
	}
	before, err := a.cadence(ctx, h.Name)
	if err != nil {
		return err
	}
	after, err := a.applyRule(ctx, h.Name, before)
	if err != nil {
		return fmt.Errorf("logged %s %s %s, but updating its cadence failed: %w", h.Name, day, outcome, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "logged %s %s (%s): %s", h.Name, day, day.Weekday(), outcome)
	if why != nil {
		fmt.Fprintf(&b, " (%s)", why)
	}
	if len(prev) == 1 {
		fmt.Fprintf(&b, "; was %s", prev[0].Outcome)
		if prev[0].Why != nil {
			fmt.Fprintf(&b, " (%s)", *prev[0].Why)
		}
	}
	if h.Status != "active" {
		fmt.Fprintf(&b, "; note: %s is %s", h.Name, h.Status)
	}
	b.WriteString("\n")
	interval := fmt.Sprintf("every %s", days(after.IntervalDays))
	if after.IntervalDays != before.IntervalDays {
		interval = fmt.Sprintf("every %s (was %s)", days(after.IntervalDays), days(before.IntervalDays))
	}
	fmt.Fprintf(&b, "cadence: %s, next check %s", interval, after.NextCheck)
	if after.Reason != nil {
		fmt.Fprintf(&b, " (interval pinned: %s)", *after.Reason)
	}
	var previous *Entry
	if len(prev) == 1 {
		previous = &prev[0]
	}
	return a.emit(map[string]any{"entry": rows[0], "previous": previous, "cadence": after, "cadence_before": before}, b.String())
}

func cmdPending(ctx context.Context, a *app, args []string, o *opts) error {
	today := a.today()
	day := today
	if len(args) == 1 {
		d, err := parseDay(args[0], today)
		if err != nil {
			return usagef("pending", "%v", err)
		}
		day = d
	}
	hs, err := a.habits(ctx)
	if err != nil {
		return err
	}
	var es []Entry
	if err := a.api.do(ctx, "GET", "/entries", url.Values{"day": {eq(day.String())}}, nil, "", &es); err != nil {
		return err
	}
	logged := map[string]Entry{}
	for _, e := range es {
		logged[e.Habit] = e
	}
	pending, done := []string{}, []Entry{}
	active := 0
	for _, h := range hs {
		if h.Status != "active" || a.createdDay(h).After(day) {
			continue
		}
		active++
		if e, ok := logged[h.Name]; ok {
			done = append(done, e)
		} else {
			pending = append(pending, h.Name)
		}
	}
	var text string
	switch {
	case active == 0:
		text = fmt.Sprintf("no active habits on %s", day)
	case len(pending) == 0:
		text = fmt.Sprintf("nothing pending for %s (%s): every active habit is logged", day, day.Weekday())
	default:
		text = fmt.Sprintf("pending for %s (%s): %s", day, day.Weekday(), strings.Join(pending, ", "))
		if len(done) > 0 {
			var l []string
			for _, e := range done {
				l = append(l, e.Habit+" "+e.Outcome)
			}
			text += "\nalready logged: " + strings.Join(l, ", ")
		}
	}
	return a.emit(map[string]any{"day": day, "pending": pending, "logged": done}, text)
}

func cmdHistory(ctx context.Context, a *app, args []string, o *opts) error {
	if err := checkDays("history", o.days); err != nil {
		return err
	}
	h, err := a.resolve(ctx, args[0])
	if err != nil {
		return err
	}
	es, err := a.entries(ctx, h.Name)
	if err != nil {
		return err
	}
	today := a.today()
	from := today.AddDays(-(o.days - 1))
	lines := window(es, from, today)
	c := countLines(lines)
	var b strings.Builder
	fmt.Fprintf(&b, "%s, %s to %s: %d done, %d missed, %d not logged", h.Name, from, today, c.Done, c.Missed, c.NotLogged)
	if created := a.createdDay(h); created.After(from) {
		fmt.Fprintf(&b, " (added %s)", created)
	}
	b.WriteString("\n")
	formatDays(&b, lines)
	return a.emit(map[string]any{"habit": h.Name, "from": from, "to": today, "counts": c, "days": lines}, b.String())
}

type counts struct {
	Days      int `json:"days"`
	Done      int `json:"done"`
	Missed    int `json:"missed"`
	NotLogged int `json:"not_logged"`
}

func countLines(lines []dayLine) counts {
	c := counts{Days: len(lines)}
	for _, l := range lines {
		switch {
		case l.Outcome == nil:
			c.NotLogged++
		case *l.Outcome == "done":
			c.Done++
		default:
			c.Missed++
		}
	}
	return c
}

type habitSummary struct {
	Habit      string `json:"habit"`
	Streak     int    `json:"streak"`
	Misses     int    `json:"misses_in_a_row"`
	LastLogged Day    `json:"last_logged"`
	Week       counts `json:"last_7_days"`
	Month      counts `json:"last_30_days"`
}

func cmdSummary(ctx context.Context, a *app, args []string, o *opts) error {
	var hs []Habit
	if len(args) == 1 {
		h, err := a.resolve(ctx, args[0])
		if err != nil {
			return err
		}
		hs = []Habit{h}
	} else {
		all, err := a.habits(ctx)
		if err != nil {
			return err
		}
		for _, h := range all {
			if h.Status == "active" {
				hs = append(hs, h)
			}
		}
	}
	all, err := a.entries(ctx, "")
	if err != nil {
		return err
	}
	byHabit := map[string][]Entry{}
	for _, e := range all {
		byHabit[e.Habit] = append(byHabit[e.Habit], e)
	}
	today := a.today()
	out := []habitSummary{}
	var b strings.Builder
	fmt.Fprintf(&b, "as of %s (%s)\n", today, a.loc)
	for _, h := range hs {
		es := byHabit[h.Name]
		st := replay(outcomes(es))
		since := a.since(h, es)
		s := habitSummary{
			Habit: h.Name, Streak: st.Streak, Misses: st.Misses, LastLogged: st.Last,
			Week:  countLines(window(es, maxDay(today.AddDays(-6), since), today)),
			Month: countLines(window(es, maxDay(today.AddDays(-29), since), today)),
		}
		out = append(out, s)
		fmt.Fprintf(&b, "%s: ", h.Name)
		switch {
		case st.Last.IsZero():
			b.WriteString("nothing logged yet")
		case st.Misses > 0:
			fmt.Fprintf(&b, "missed the last %d logged", st.Misses)
		default:
			fmt.Fprintf(&b, "streak %d done", st.Streak)
		}
		if !st.Last.IsZero() {
			fmt.Fprintf(&b, " (last logged %s)", st.Last)
		}
		fmt.Fprintf(&b, "\n  %s: %s\n", windowLabel(7, s.Week.Days), describeCounts(s.Week))
		if s.Month.Days > s.Week.Days {
			fmt.Fprintf(&b, "  %s: %s\n", windowLabel(30, s.Month.Days), describeCounts(s.Month))
		}
	}
	if len(hs) == 0 {
		b.WriteString("no active habits")
	}
	return a.emit(map[string]any{"as_of": today, "habits": out}, b.String())
}

// windowLabel names a window of n days that is full days long unless the
// habit is younger.
func windowLabel(full, n int) string {
	if n == full {
		return fmt.Sprintf("last %d days", full)
	}
	return fmt.Sprintf("all %s so far", days(n))
}

func describeCounts(c counts) string {
	s := fmt.Sprintf("%d done, %d missed, %d not logged", c.Done, c.Missed, c.NotLogged)
	if logged := c.Done + c.Missed; logged > 0 {
		s += fmt.Sprintf(" (%d%% of logged days done)", (c.Done*100+logged/2)/logged)
	}
	return s
}

var noteKinds = map[string]string{
	"motivation": "motivation", "cue": "cue", "prep": "prep", "preparation": "prep",
}

func cmdNote(ctx context.Context, a *app, args []string, o *opts) error {
	kind, ok := noteKinds[strings.ToLower(args[1])]
	if !ok {
		return usagef("note", "kind %q: want motivation, cue or prep", args[1])
	}
	body := strings.TrimSpace(strings.Join(args[2:], " "))
	if body == "" {
		return usagef("note", "the note text is empty")
	}
	h, err := a.resolve(ctx, args[0])
	if err != nil {
		return err
	}
	notes, err := a.addNotes(ctx, []map[string]any{{"habit": h.Name, "kind": kind, "body": body}})
	if err != nil {
		return err
	}
	if len(notes) != 1 {
		return fmt.Errorf("PostgREST returned %d rows for one note", len(notes))
	}
	return a.emit(notes[0], fmt.Sprintf("noted for %s (%s): %s", h.Name, kind, body))
}

type dueSummary struct {
	Habits    []Cadence `json:"habits"`
	Earliest  *Day      `json:"earliest_due"`
	DueHabits []string  `json:"earliest_due_habits"`
	Suggested *string   `json:"suggested_wakeup"`
}

// due collects the active habits' cadence and the earliest check.
func (a *app) due(ctx context.Context, only string, checkin clock) (dueSummary, error) {
	hs, err := a.habits(ctx)
	if err != nil {
		return dueSummary{}, err
	}
	cads, err := a.cadences(ctx)
	if err != nil {
		return dueSummary{}, err
	}
	ds := dueSummary{Habits: []Cadence{}, DueHabits: []string{}}
	for _, h := range hs {
		if (only == "" && h.Status != "active") || (only != "" && h.Name != only) {
			continue
		}
		if c, ok := cads[h.Name]; ok {
			ds.Habits = append(ds.Habits, c)
		}
	}
	sort.SliceStable(ds.Habits, func(i, j int) bool { return ds.Habits[i].NextCheck.Before(ds.Habits[j].NextCheck) })
	if len(ds.Habits) > 0 {
		e := ds.Habits[0].NextCheck
		ds.Earliest = &e
		for _, c := range ds.Habits {
			if c.NextCheck == e {
				ds.DueHabits = append(ds.DueHabits, c.Habit)
			}
		}
		s := suggestWakeup(e, a.now(), a.loc, checkin).Format(time.RFC3339)
		ds.Suggested = &s
	}
	return ds, nil
}

// suggestWakeup is the check-in time on the due day, or the first check-in
// time at least 10 minutes from now if that has passed, and no further
// than the worker accepts.
func suggestWakeup(due Day, now time.Time, loc *time.Location, checkin clock) time.Time {
	earliest := now.Add(minLead)
	at := due.At(checkin, loc)
	for d := dayOf(now.In(loc)); at.Before(earliest); d = d.AddDays(1) {
		at = d.At(checkin, loc)
	}
	if latest := now.Add(maxLead).Truncate(time.Minute); at.After(latest) {
		at = latest
	}
	return at
}

func cmdCadence(ctx context.Context, a *app, args []string, o *opts) error {
	checkin, err := parseClock(o.at)
	if err != nil {
		return usagef("cadence", "--at: %v", err)
	}
	override := o.set["every"] || o.set["next"]
	if override || o.auto {
		if len(args) != 1 {
			return usagef("cadence", "name the habit to override")
		}
		if override && o.auto {
			return usagef("cadence", "--auto drops an override; it cannot be combined with --every or --next")
		}
		if o.auto && o.set["reason"] {
			return usagef("cadence", "--auto takes no --reason")
		}
		if override && strings.TrimSpace(o.reason) == "" {
			return usagef("cadence", `an override needs --reason "<why>" (like "user asked for weekly check-ins")`)
		}
		if o.set["every"] && (o.every < 1 || o.every > 60) {
			return usagef("cadence", "--every %d: give 1 to 60 days", o.every)
		}
	} else if o.set["reason"] {
		return usagef("cadence", "--reason goes with --every or --next")
	}
	var only string
	if len(args) == 1 {
		h, err := a.resolve(ctx, args[0])
		if err != nil {
			return err
		}
		only = h.Name
	}
	today := a.today()
	var changed string
	switch {
	case override:
		cur, err := a.cadence(ctx, only)
		if err != nil {
			return err
		}
		interval := cur.IntervalDays
		if o.set["every"] {
			interval = o.every
		}
		next := today.AddDays(interval)
		if o.set["next"] {
			d, err := parseISODayOrRelative(o.next, today)
			if err != nil {
				return usagef("cadence", "--next: %v", err)
			}
			if d.Before(today) {
				return usagef("cadence", "--next %s is before today (%s)", d, today)
			}
			next = d
		}
		c, err := a.patchCadence(ctx, only, map[string]any{
			"interval_days": interval, "next_check": next, "reason": strings.TrimSpace(o.reason),
		})
		if err != nil {
			return err
		}
		changed = fmt.Sprintf("overrode %s: %s\n", only, describeCadence(c, today))
	case o.auto:
		cur, err := a.cadence(ctx, only)
		if err != nil {
			return err
		}
		cur.Reason = nil
		c, err := a.applyRule(ctx, only, cur)
		if err != nil {
			return err
		}
		changed = fmt.Sprintf("%s follows the rule again: %s\n", only, describeCadence(c, today))
	}
	ds, err := a.due(ctx, only, checkin)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(changed)
	if len(ds.Habits) == 0 {
		b.WriteString("no active habits, so nothing to check")
		return a.emit(ds, b.String())
	}
	if changed == "" {
		for _, c := range ds.Habits {
			fmt.Fprintf(&b, "%s: %s\n", c.Habit, describeCadence(c, today))
		}
	}
	e := *ds.Earliest
	when := e.String()
	switch n := e.Sub(today); {
	case n < 0:
		when += fmt.Sprintf(" (overdue by %s)", days(-n))
	case n == 0:
		when += " (today)"
	case n == 1:
		when += " (tomorrow)"
	}
	fmt.Fprintf(&b, "earliest due: %s: %s\n", when, strings.Join(ds.DueHabits, ", "))
	fmt.Fprintf(&b, "suggested wake-up: %s (habit wake-at due \"<note>\")", *ds.Suggested)
	return a.emit(ds, b.String())
}

// parseISODayOrRelative reads a future date: YYYY-MM-DD, today, tomorrow or
// +N (days).
func parseISODayOrRelative(s string, today Day) (Day, error) {
	switch v := strings.ToLower(strings.TrimSpace(s)); {
	case v == "today":
		return today, nil
	case v == "tomorrow":
		return today.AddDays(1), nil
	case strings.HasPrefix(v, "+"):
		var n int
		if _, err := fmt.Sscanf(v, "+%dd", &n); err == nil && n >= 0 {
			return today.AddDays(n), nil
		}
		if _, err := fmt.Sscanf(v, "+%d", &n); err == nil && n >= 0 && strings.TrimLeft(v[1:], "0123456789") == "" {
			return today.AddDays(n), nil
		}
	default:
		if d, err := parseISODay(v); err == nil {
			return d, nil
		}
	}
	return Day{}, fmt.Errorf("date %q: want YYYY-MM-DD, today, tomorrow or +N (days from today)", s)
}

func cmdWakeAt(ctx context.Context, a *app, args []string, o *opts) error {
	checkin, err := parseClock(o.at)
	if err != nil {
		return usagef("wake-at", "--at: %v", err)
	}
	now := a.now()
	note := strings.TrimSpace(strings.Join(args[1:], " "))
	var at time.Time
	if strings.EqualFold(args[0], "due") {
		ds, err := a.due(ctx, "", checkin)
		if err != nil {
			return err
		}
		if ds.Suggested == nil {
			return fmt.Errorf("no active habits, so nothing is due; give a time instead of due")
		}
		at, _ = time.Parse(time.RFC3339, *ds.Suggested)
	} else {
		at, err = parseWhen(args[0], now, a.loc, checkin)
		if err != nil {
			return usagef("wake-at", "%v", err)
		}
	}
	if err := checkWakeup(at, now, note); err != nil {
		return err
	}
	data, err := outboxJSON(at, a.loc, note)
	if err != nil {
		return err
	}
	if err := writeOutbox(a.outbox, data); err != nil {
		return err
	}
	local := at.In(a.loc)
	return a.emit(map[string]any{"at": local.Format(time.RFC3339), "note": note, "file": a.outbox},
		fmt.Sprintf("wake-up requested for %s (%s, in %s). Note: %s\nThe worker takes it after this turn; it replaces the pending wake-up.",
			local.Format(time.RFC3339), local.Format("Mon"), humanDuration(at.Sub(now)), note))
}
