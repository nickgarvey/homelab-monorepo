// dmgpad runs on the Pocket DMG (pushed by pocket-dmg to /data/local/tmp) and
// presses its built-in controller's buttons by writing input events into the
// controller's evdev node. Android and games see them as real presses on the
// built-in pad. Each argument is one command; see pad.Help.
//
// The controller is a USB HID device that powers off while the screen is off,
// so if its node is missing dmgpad wakes the screen and waits for it.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"homelab/pocket-dmg/pad"
)

const controllerName = "AYANEO Controller"

// findNode returns the /dev/input/eventN whose device name is exactly name.
func findNode(name string) string {
	matches, _ := filepath.Glob("/sys/class/input/event*/device/name")
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err == nil && strings.TrimSpace(string(b)) == name {
			return "/dev/input/" + filepath.Base(filepath.Dir(filepath.Dir(m)))
		}
	}
	return ""
}

// wakeAndFind wakes the screen, dismisses a swipe keyguard, and waits for
// the controller to enumerate (about 1.5s after wake).
func wakeAndFind(name string, timeout time.Duration) (string, error) {
	fmt.Fprintln(os.Stderr, "dmgpad: controller absent (screen off?); waking the device")
	for _, c := range [][]string{{"input", "keyevent", "KEYCODE_WAKEUP"}, {"wm", "dismiss-keyguard"}} {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return "", fmt.Errorf("%s: %v: %s", strings.Join(c, " "), err, out)
		}
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n := findNode(name); n != "" {
			// Let Android's EventHub open the new device before events arrive.
			time.Sleep(300 * time.Millisecond)
			return n, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", fmt.Errorf("no input device named %q appeared within %s of waking", name, timeout)
}

// encode builds struct input_event records for a 64-bit kernel: a 16-byte
// timeval (ignored on write), u16 type, u16 code, s32 value.
func encode(ev []pad.Event) []byte {
	var b bytes.Buffer
	for _, e := range append(ev, pad.Event{Type: pad.EvSyn}) {
		b.Write(make([]byte, 16))
		binary.Write(&b, binary.LittleEndian, e.Type)
		binary.Write(&b, binary.LittleEndian, e.Code)
		binary.Write(&b, binary.LittleEndian, e.Value)
	}
	return b.Bytes()
}

func main() {
	node := flag.String("node", "", "evdev node to write to (default: find the device by -name)")
	name := flag.String("name", controllerName, "input device name to look up")
	verbose := flag.Bool("v", false, "log each event written")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: dmgpad [-node /dev/input/eventN] [-v] '<command>'...\n%s\n", pad.Help)
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := pad.Check(flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "dmgpad:", err)
		os.Exit(2)
	}

	path := *node
	if path == "" {
		if path = findNode(*name); path == "" {
			var err error
			if path, err = wakeAndFind(*name, 5*time.Second); err != nil {
				fmt.Fprintln(os.Stderr, "dmgpad:", err)
				os.Exit(1)
			}
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dmgpad:", err)
		os.Exit(1)
	}
	defer f.Close()

	start := time.Now()
	ex := &pad.Executor{
		Emit: func(ev []pad.Event) error {
			if *verbose {
				fmt.Fprintf(os.Stderr, "%8.3fs %v\n", time.Since(start).Seconds(), ev)
			}
			_, err := f.Write(encode(ev))
			return err
		},
		Sleep: time.Sleep,
	}
	for _, line := range flag.Args() {
		if err := ex.Run(line); err != nil {
			fmt.Fprintf(os.Stderr, "dmgpad: %q: %v\n", line, err)
			os.Exit(1)
		}
	}
}
