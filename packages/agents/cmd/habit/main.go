// habit is Habit Helper's interface to its data: habits, daily entries, notes
// and check-in cadence, in PostgREST (k8s/manifests/habit-helper), and its
// wake-up requests to the agents worker. It runs in a habit-helper sandbox,
// whose supervisor adds the PostgREST token to each request. No command
// deletes anything. Documented in agents/habit-helper/README.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"
	_ "time/tzdata" // the binary is static and the image may have no zoneinfo
)

const (
	defaultTZ      = "America/Los_Angeles"
	defaultCheckin = "20:00"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a := &app{stdout: os.Stdout, stderr: os.Stderr, getenv: os.Getenv, now: time.Now}
	os.Exit(a.run(ctx, os.Args[1:]))
}

// app is one invocation.
type app struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	now            func() time.Time

	// Set from the environment and flags before a command runs.
	loc    *time.Location
	json   bool
	api    *client
	outbox string
}

// opts holds every command's flags; each command registers the ones it takes.
type opts struct {
	json               bool
	tz                 string
	name, cue, motiv   string
	prep, reason, next string
	at                 string
	days, every        int
	all, auto          bool
	set                map[string]bool
}

type command struct {
	name    string
	args    string
	summary string
	help    string
	flags   func(fs *flag.FlagSet, o *opts)
	// minArgs, maxArgs bound the positional arguments; maxArgs -1 is
	// unbounded (the rest is joined as text).
	minArgs, maxArgs int
	run              func(ctx context.Context, a *app, args []string, o *opts) error
}

// usageError is a mistake in the command line: exit 2, with a pointer to help.
type usageError struct {
	cmd string
	msg string
}

func (e *usageError) Error() string { return e.msg }

func usagef(cmd, format string, args ...any) error {
	return &usageError{cmd: cmd, msg: fmt.Sprintf(format, args...)}
}

const topHelp = `habit: Habit Helper's habits, daily entries, notes and check-in cadence.

usage: habit <command> [arguments] [flags]

Onboarding:
  add <name> --cue "if <event>, then <behavior>" --motivation "..." --prep "..."
  edit <habit> [--name N] [--cue C] [--motivation M] [--prep P]
  pause <habit> | resume <habit> | retire <habit>
  list [--all]                        habits and their next check-in
  show <habit> [--days N]             one habit: cue, motivation, prep, recent days, notes

Tracking:
  log <habit> [<date>] done|missed ["<why>"]
  pending [<date>]                    active habits with no entry for the day

Coaching and recovery:
  history <habit> [--days N]          day by day, with reasons
  summary [<habit>]                   streaks, and rates over 7 and 30 days
  note <habit> motivation|cue|prep "<what the user said>"

Scheduling:
  cadence [<habit>]                   next check-ins, the earliest due, a suggested wake-up
  cadence <habit> --every N --reason "..." [--next <date>]   override the rule
  cadence <habit> --auto              hand the habit back to the rule
  wake-at <when> "<note>"             ask the worker for a wake-up (writes ` + defaultOutbox + `)

Flags every command takes:
  --json       print JSON instead of text
  --tz ZONE    the user's time zone (default ` + defaultTZ + `, or $HABIT_TZ)

Dates are the user's local dates: ` + dateForms + `.
A habit is named as when added; case does not matter.
Nothing is ever deleted: retire a habit instead, and log a day again to correct it.
"habit <command> --help" explains a command; "habit cadence --help" gives the
tapering rule.

Environment: HABIT_API_URL (default ` + defaultAPIURL + `),
HABIT_TZ, HABIT_OUTBOX (default ` + defaultOutbox + `).
`

func (a *app) run(ctx context.Context, argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.stderr, topHelp)
		return 2
	}
	name := argv[0]
	switch name {
	case "-h", "--help", "-help", "help":
		if name == "help" && len(argv) > 1 {
			if c := findCommand(argv[1]); c != nil {
				fmt.Fprint(a.stdout, c.usage())
				return 0
			}
		}
		fmt.Fprint(a.stdout, topHelp)
		return 0
	}
	c := findCommand(name)
	if c == nil {
		fmt.Fprintf(a.stderr, "habit: unknown command %q. Commands: %s. Run habit --help.\n", name, commandNames())
		return 2
	}
	err := a.runCommand(ctx, c, argv[1:])
	if err == nil {
		return 0
	}
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(a.stdout, c.usage())
		return 0
	}
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintf(a.stderr, "habit %s: %v\nusage: habit %s %s (habit %s --help for more)\n", c.name, err, c.name, c.args, c.name)
		return 2
	}
	fmt.Fprintf(a.stderr, "habit %s: %v\n", c.name, err)
	return 1
}

func (a *app) runCommand(ctx context.Context, c *command, argv []string) error {
	o := &opts{set: map[string]bool{}}
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.json, "json", false, "print JSON")
	fs.StringVar(&o.tz, "tz", "", "the user's time zone")
	if c.flags != nil {
		c.flags(fs, o)
	}
	args, err := parseInterleaved(fs, argv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usagef(c.name, "%v", err)
	}
	fs.Visit(func(f *flag.Flag) { o.set[f.Name] = true })
	if len(args) < c.minArgs || (c.maxArgs >= 0 && len(args) > c.maxArgs) {
		switch {
		case len(args) < c.minArgs:
			return usagef(c.name, "missing arguments")
		default:
			return usagef(c.name, "too many arguments (%q); quote text that has spaces", strings.Join(args[c.maxArgs:], " "))
		}
	}

	tz := o.tz
	if tz == "" {
		tz = a.getenv("HABIT_TZ")
	}
	if tz == "" {
		tz = defaultTZ
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return usagef(c.name, "time zone %q: not a zone name like America/Los_Angeles", tz)
	}
	a.loc = loc
	a.json = o.json
	base := a.getenv("HABIT_API_URL")
	if base == "" {
		base = defaultAPIURL
	}
	a.api = newClient(base)
	a.outbox = a.getenv("HABIT_OUTBOX")
	if a.outbox == "" {
		a.outbox = defaultOutbox
	}
	return c.run(ctx, a, args, o)
}

// parseInterleaved lets flags come before, between or after the arguments.
// Everything after "--" is an argument.
func parseInterleaved(fs *flag.FlagSet, argv []string) ([]string, error) {
	var rest []string
	for i, s := range argv {
		if s == "--" {
			rest = argv[i+1:]
			argv = argv[:i]
			break
		}
	}
	var args []string
	for {
		if err := fs.Parse(argv); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		args = append(args, fs.Arg(0))
		argv = fs.Args()[1:]
	}
	return append(args, rest...), nil
}

func (c *command) usage() string {
	var b strings.Builder
	fmt.Fprintf(&b, "usage: habit %s %s\n\n%s\n", c.name, c.args, strings.TrimSpace(c.help))
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	o := &opts{}
	if c.flags != nil {
		c.flags(fs, o)
	}
	fs.BoolVar(&o.json, "json", false, "print JSON instead of text")
	fs.StringVar(&o.tz, "tz", "", "the user's time zone (default "+defaultTZ+", or $HABIT_TZ)")
	b.WriteString("\nflags:\n")
	fs.SetOutput(&b)
	fs.PrintDefaults()
	return b.String()
}

func findCommand(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

func commandNames() string {
	var names []string
	for _, c := range commands {
		names = append(names, c.name)
	}
	return strings.Join(names, ", ")
}

// emit prints v as JSON with --json, otherwise the text.
func (a *app) emit(v any, text string) error {
	if a.json {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	_, err := io.WriteString(a.stdout, text)
	return err
}

func (a *app) today() Day { return dayOf(a.now().In(a.loc)) }
