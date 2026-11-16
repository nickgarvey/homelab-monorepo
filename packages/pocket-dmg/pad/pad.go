// Package pad turns button names and the dmgpad command language into Linux
// input events for the Pocket DMG's built-in controller ("AYANEO Controller").
package pad

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Linux input event types and codes the controller supports.
const (
	EvSyn = 0x00
	EvKey = 0x01
	EvAbs = 0x03

	AbsX     = 0x00
	AbsY     = 0x01
	AbsZ     = 0x02
	AbsRz    = 0x05
	AbsGas   = 0x09
	AbsBrake = 0x0a
	AbsHat0X = 0x10
	AbsHat0Y = 0x11

	StickMax   = 32767 // ABS_X/Y/Z/RZ range is -32768..32767
	TriggerMax = 255   // ABS_GAS/BRAKE range is 0..255
)

// Event is one struct input_event, without the timestamp.
type Event struct {
	Type, Code uint16
	Value      int32
}

func (e Event) String() string { return fmt.Sprintf("%d/%d/%d", e.Type, e.Code, e.Value) }

// Button key codes. The names are Android's (KEYCODE_BUTTON_*), which is what
// games see; Generic.kl maps each code to the keycode of the same name.
var buttonCodes = map[string]uint16{
	"A": 304, "B": 305, "C": 306, "X": 307, "Y": 308, "Z": 309,
	"L1": 310, "R1": 311, "L2": 312, "R2": 313,
	"SELECT": 314, "START": 315, "HOME": 316, "L3": 317, "R3": 318,
}

var aliases = map[string]string{
	"L": "L1", "R": "R1", "MODE": "HOME", "BACK": "SELECT",
}

// D-pad directions: the hat axis and the value that means pressed.
type dir struct {
	axis  uint16
	value int32
}

var dpad = map[string]dir{
	"UP": {AbsHat0Y, -1}, "DOWN": {AbsHat0Y, 1},
	"LEFT": {AbsHat0X, -1}, "RIGHT": {AbsHat0X, 1},
}

// Names lists every input name the command language accepts.
func Names() []string {
	var n []string
	for k := range buttonCodes {
		n = append(n, k)
	}
	for k := range dpad {
		n = append(n, k)
	}
	for k := range aliases {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// Chord is a set of buttons and D-pad directions pressed together.
type Chord []string

// ParseChord parses "A", "a", "A+B" or "UP+RIGHT".
func ParseChord(s string) (Chord, error) {
	var c Chord
	for _, part := range strings.Split(s, "+") {
		name := strings.ToUpper(strings.TrimSpace(part))
		if a, ok := aliases[name]; ok {
			name = a
		}
		_, isButton := buttonCodes[name]
		_, isDir := dpad[name]
		if !isButton && !isDir {
			return nil, fmt.Errorf("unknown button %q (known: %s)", part, strings.Join(Names(), " "))
		}
		c = append(c, name)
	}
	return c, nil
}

// Events returns the events that press (down) or release the chord. Releasing
// a D-pad direction centres its hat axis.
func (c Chord) Events(down bool) []Event {
	var ev []Event
	for _, name := range c {
		if code, ok := buttonCodes[name]; ok {
			v := int32(0)
			if down {
				v = 1
			}
			ev = append(ev, Event{EvKey, code, v})
			continue
		}
		d := dpad[name]
		v := int32(0)
		if down {
			v = d.value
		}
		ev = append(ev, Event{EvAbs, d.axis, v})
	}
	return ev
}

// ReleaseAll releases every button and centres the D-pad.
func ReleaseAll() []Event {
	var codes []int
	for _, c := range buttonCodes {
		codes = append(codes, int(c))
	}
	sort.Ints(codes)
	var ev []Event
	for _, c := range codes {
		ev = append(ev, Event{EvKey, uint16(c), 0})
	}
	return append(ev, Event{EvAbs, AbsHat0X, 0}, Event{EvAbs, AbsHat0Y, 0})
}

// StickValue maps -1..1 to the stick axis range.
func StickValue(v float64) int32 {
	return int32(math.Round(math.Max(-1, math.Min(1, v)) * StickMax))
}

// TriggerValue maps 0..1 to the trigger axis range.
func TriggerValue(v float64) int32 {
	return int32(math.Round(math.Max(0, math.Min(1, v)) * TriggerMax))
}
