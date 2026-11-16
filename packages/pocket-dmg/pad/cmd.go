package pad

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Default timings for taps.
const (
	DefaultTap = 80 * time.Millisecond // how long a tap holds the button
	TapGap     = 60 * time.Millisecond // release time after a tap in a seq, so repeats register
)

// Help describes the command language, one command per argument.
const Help = `commands (button names are case-insensitive; join with + for chords, as A+B or UP+RIGHT):
  down <btn>            press and hold (stays held after dmgpad exits)
  up <btn>|all          release
  tap <btn> [ms]        press, hold ms (default 80), release
  seq <tok>...          in order: BTN (tap 80ms), BTN:ms (tap for ms), wait:ms;
                        each tap is followed by a 60ms release
  stick l|r <x> <y>     stick position, -1..1 (y positive is down); "stick l 0 0" centres
  trigger l|r <v>       analog trigger, 0..1
  wait <ms>             sleep
  reset                 release everything, centre sticks and triggers
buttons: A B C X Y Z L1 R1 L2 R2 SELECT START HOME L3 R3 UP DOWN LEFT RIGHT
         (aliases: L=L1 R=R1 MODE=HOME BACK=SELECT)`

// Executor runs commands. Emit writes events to the controller, followed by
// a SYN_REPORT; Sleep waits (replaced in tests).
type Executor struct {
	Emit  func([]Event) error
	Sleep func(time.Duration)
}

func parseMs(s string) (time.Duration, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 600000 {
		return 0, fmt.Errorf("bad duration %q (milliseconds, 0..600000)", s)
	}
	return time.Duration(n) * time.Millisecond, nil
}

func parseFloat(s string, lo, hi float64) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < lo || v > hi {
		return 0, fmt.Errorf("bad value %q (%g..%g)", s, lo, hi)
	}
	return v, nil
}

func (e *Executor) tap(c Chord, d time.Duration) error {
	if err := e.Emit(c.Events(true)); err != nil {
		return err
	}
	e.Sleep(d)
	return e.Emit(c.Events(false))
}

// Run executes one command, given as a line ("tap A 100").
func (e *Executor) Run(line string) error {
	f := strings.Fields(line)
	if len(f) == 0 {
		return nil
	}
	cmd, args := strings.ToLower(f[0]), f[1:]
	switch cmd {
	case "reset":
		return e.Emit(append(ReleaseAll(),
			Event{EvAbs, AbsX, 0}, Event{EvAbs, AbsY, 0},
			Event{EvAbs, AbsZ, 0}, Event{EvAbs, AbsRz, 0},
			Event{EvAbs, AbsBrake, 0}, Event{EvAbs, AbsGas, 0}))
	case "down", "up":
		if len(args) != 1 {
			return fmt.Errorf("%s takes one button (use + for chords)", cmd)
		}
		if cmd == "up" && strings.EqualFold(args[0], "all") {
			return e.Emit(ReleaseAll())
		}
		c, err := ParseChord(args[0])
		if err != nil {
			return err
		}
		return e.Emit(c.Events(cmd == "down"))
	case "tap":
		if len(args) < 1 || len(args) > 2 {
			return fmt.Errorf("usage: tap <btn> [ms]")
		}
		c, err := ParseChord(args[0])
		if err != nil {
			return err
		}
		d := DefaultTap
		if len(args) == 2 {
			if d, err = parseMs(args[1]); err != nil {
				return err
			}
		}
		return e.tap(c, d)
	case "stick":
		if len(args) != 3 {
			return fmt.Errorf("usage: stick l|r <x> <y>")
		}
		x, err := parseFloat(args[1], -1, 1)
		if err != nil {
			return err
		}
		y, err := parseFloat(args[2], -1, 1)
		if err != nil {
			return err
		}
		var ax, ay uint16
		switch strings.ToLower(args[0]) {
		case "l":
			ax, ay = AbsX, AbsY
		case "r":
			ax, ay = AbsZ, AbsRz
		default:
			return fmt.Errorf("stick is l or r, not %q", args[0])
		}
		return e.Emit([]Event{{EvAbs, ax, StickValue(x)}, {EvAbs, ay, StickValue(y)}})
	case "trigger":
		if len(args) != 2 {
			return fmt.Errorf("usage: trigger l|r <0..1>")
		}
		v, err := parseFloat(args[1], 0, 1)
		if err != nil {
			return err
		}
		var a uint16
		switch strings.ToLower(args[0]) {
		case "l":
			a = AbsBrake
		case "r":
			a = AbsGas
		default:
			return fmt.Errorf("trigger is l or r, not %q", args[0])
		}
		return e.Emit([]Event{{EvAbs, a, TriggerValue(v)}})
	case "wait":
		if len(args) != 1 {
			return fmt.Errorf("usage: wait <ms>")
		}
		d, err := parseMs(args[0])
		if err != nil {
			return err
		}
		e.Sleep(d)
		return nil
	case "seq":
		steps, err := ParseSeq(args)
		if err != nil {
			return err
		}
		for _, s := range steps {
			if s.Chord == nil {
				e.Sleep(s.D)
				continue
			}
			if err := e.tap(s.Chord, s.D); err != nil {
				return err
			}
			e.Sleep(TapGap)
		}
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", cmd, Help)
}

// Step is one seq token: a tap of Chord for D, or a pause of D when Chord is nil.
type Step struct {
	Chord Chord
	D     time.Duration
}

// ParseSeq parses seq tokens. The whole sequence is checked before any of it runs.
func ParseSeq(toks []string) ([]Step, error) {
	var steps []Step
	for _, t := range toks {
		name, ms, hasMs := strings.Cut(t, ":")
		if l := strings.ToLower(name); l == "wait" || l == "w" {
			if !hasMs {
				return nil, fmt.Errorf("%q needs a duration, as wait:500", t)
			}
			d, err := parseMs(ms)
			if err != nil {
				return nil, err
			}
			steps = append(steps, Step{D: d})
			continue
		}
		c, err := ParseChord(name)
		if err != nil {
			return nil, err
		}
		d := DefaultTap
		if hasMs {
			if d, err = parseMs(ms); err != nil {
				return nil, err
			}
		}
		steps = append(steps, Step{Chord: c, D: d})
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("empty seq")
	}
	return steps, nil
}

// Check parses every command without running any, so a typo late in a
// command list fails before the earlier commands press anything.
func Check(lines []string) error {
	dry := &Executor{Emit: func([]Event) error { return nil }, Sleep: func(time.Duration) {}}
	for _, l := range lines {
		if err := dry.Run(l); err != nil {
			return fmt.Errorf("%q: %w", l, err)
		}
	}
	return nil
}
