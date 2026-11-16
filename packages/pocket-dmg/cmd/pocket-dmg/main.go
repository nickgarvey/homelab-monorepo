// pocket-dmg drives an AYANEO Pocket DMG over adb: presses its built-in
// controller (through dmgpad, pushed to the device on first use), takes
// screenshots, reads logs, and installs and launches apps. adb picks the
// device as usual (ANDROID_SERIAL when more than one is attached).
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"homelab/pocket-dmg/pad"
)

const remotePad = "/data/local/tmp/dmgpad"

const usage = `usage: pocket-dmg <command> [args]

controller (real presses on the built-in pad; wakes the screen if it is off):
  press <btn> [ms]        tap a button or chord (A, A+B, UP+RIGHT), default 80ms
  hold <btn>              press and keep held
  release <btn>|all       release
  seq <tok>...            BTN, BTN:ms, wait:ms in order, e.g. seq RIGHT:300 A wait:500 START
  stick l|r <x> <y>       stick position, -1..1 (y positive is down)
  trigger l|r <v>         analog trigger, 0..1
  reset                   release everything, centre sticks and triggers
  pad <command>...        raw dmgpad commands, one per argument, run back to back on the device

screen and system:
  shot [file.png]         screenshot (1240x1080); prints the file path
  wake                    wake the screen and dismiss a swipe lock
  touch <x> <y>           tap the touchscreen at screenshot coordinates
  key <KEYCODE>           Android key event, e.g. BACK, HOME, VOLUME_UP

apps:
  top                     the focused window
  launch <pkg>            start an app's launcher activity
  stop <pkg>              force-stop an app
  install <apk>           install or replace an APK
  log [-n N] [pkg]        last N (default 200) logcat lines, only pkg's process if given

` + pad.Help

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "pocket-dmg: "+format+"\n", a...)
	os.Exit(1)
}

// adb runs adb with its output passed through.
func adb(args ...string) error {
	c := exec.Command("adb", args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// adbOut runs adb and returns its stdout.
func adbOut(args ...string) (string, error) {
	c := exec.Command("adb", args...)
	c.Stderr = os.Stderr
	out, err := c.Output()
	return string(out), err
}

// quote single-quotes s for the device's sh (adb shell joins its arguments).
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// shell runs a command line on the device, passing output through.
func shell(cmdline string) error { return adb("shell", cmdline) }

// localPad finds the arm64 dmgpad binary that ships with this program.
func localPad() string {
	if p := os.Getenv("POCKET_DMG_PAD"); p != "" {
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		die("%v", err)
	}
	exe, _ = filepath.EvalSymlinks(exe)
	return filepath.Join(filepath.Dir(exe), "..", "share", "pocket-dmg", "dmgpad")
}

// ensurePad pushes dmgpad unless the device already has this exact build.
func ensurePad() {
	local := localPad()
	b, err := os.ReadFile(local)
	if err != nil {
		die("dmgpad binary: %v (set POCKET_DMG_PAD)", err)
	}
	sum := sha256.Sum256(b)
	want := hex.EncodeToString(sum[:])
	got, _ := adbOut("shell", "sha256sum "+remotePad+" 2>/dev/null")
	if strings.HasPrefix(got, want) {
		return
	}
	c := exec.Command("adb", "push", local, remotePad)
	if out, err := c.CombinedOutput(); err != nil {
		die("adb push: %v: %s", err, out)
	}
	if err := shell("chmod 755 " + remotePad); err != nil {
		die("chmod: %v", err)
	}
}

// runPad checks the commands locally, then runs them on the device in one dmgpad call.
func runPad(lines ...string) {
	if err := pad.Check(lines); err != nil {
		die("%v", err)
	}
	ensurePad()
	q := []string{remotePad}
	for _, l := range lines {
		q = append(q, quote(l))
	}
	if err := shell(strings.Join(q, " ")); err != nil {
		os.Exit(1)
	}
}

func need(args []string, n int, form string) {
	if len(args) != n {
		die("usage: pocket-dmg %s", form)
	}
}

func wake() error { return shell("input keyevent KEYCODE_WAKEUP && wm dismiss-keyguard") }

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "press":
		if len(args) < 1 || len(args) > 2 {
			die("usage: pocket-dmg press <btn> [ms]")
		}
		runPad("tap " + strings.Join(args, " "))
	case "hold":
		need(args, 1, "hold <btn>")
		runPad("down " + args[0])
	case "release":
		need(args, 1, "release <btn>|all")
		runPad("up " + args[0])
	case "seq":
		runPad("seq " + strings.Join(args, " "))
	case "stick":
		runPad("stick " + strings.Join(args, " "))
	case "trigger":
		runPad("trigger " + strings.Join(args, " "))
	case "reset":
		runPad("reset")
	case "pad":
		if len(args) == 0 {
			die("usage: pocket-dmg pad <command>...")
		}
		runPad(args...)

	case "shot":
		path := ""
		if len(args) == 1 {
			path = args[0]
		} else if len(args) == 0 {
			dir := filepath.Join(os.TempDir(), "pocket-dmg")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				die("%v", err)
			}
			path = filepath.Join(dir, "shot-"+time.Now().Format("150405.000")+".png")
		} else {
			die("usage: pocket-dmg shot [file.png]")
		}
		c := exec.Command("adb", "exec-out", "screencap", "-p")
		c.Stderr = os.Stderr
		png, err := c.Output()
		if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG")) {
			die("screencap failed: %v", err)
		}
		if err := os.WriteFile(path, png, 0o644); err != nil {
			die("%v", err)
		}
		abs, _ := filepath.Abs(path)
		fmt.Println(abs)
	case "wake":
		err = wake()
	case "touch":
		need(args, 2, "touch <x> <y>")
		for _, a := range args {
			if _, e := strconv.Atoi(a); e != nil {
				die("touch takes integer coordinates, not %q", a)
			}
		}
		err = shell("input touchscreen tap " + args[0] + " " + args[1])
	case "key":
		need(args, 1, "key <KEYCODE>")
		k := strings.ToUpper(args[0])
		if !strings.HasPrefix(k, "KEYCODE_") {
			k = "KEYCODE_" + k
		}
		err = shell("input keyevent " + quote(k))

	case "top":
		err = shell("dumpsys window | grep -E 'mCurrentFocus|mFocusedApp'")
	case "launch":
		need(args, 1, "launch <pkg>")
		act, e := adbOut("shell", "cmd package resolve-activity --brief "+quote(args[0])+" | tail -n 1")
		act = strings.TrimSpace(act)
		if e != nil || !strings.Contains(act, "/") {
			die("no launcher activity for %s: %s", args[0], act)
		}
		if err = wake(); err == nil {
			err = shell("am start -W -n " + quote(act))
		}
	case "stop":
		need(args, 1, "stop <pkg>")
		err = shell("am force-stop " + quote(args[0]))
	case "install":
		need(args, 1, "install <apk>")
		err = adb("install", "-r", "-d", args[0])
	case "log":
		n := "200"
		if len(args) >= 2 && args[0] == "-n" {
			n, args = args[1], args[2:]
		}
		if _, e := strconv.Atoi(n); e != nil {
			die("-n takes a number")
		}
		la := []string{"logcat", "-d", "-t", n}
		if len(args) == 1 {
			pid, _ := adbOut("shell", "pidof "+quote(args[0]))
			pid = strings.TrimSpace(pid)
			if pid == "" {
				die("%s is not running", args[0])
			}
			la = append(la, "--pid="+strings.Fields(pid)[0])
		} else if len(args) > 1 {
			die("usage: pocket-dmg log [-n N] [pkg]")
		}
		err = adb(la...)

	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		os.Exit(1)
	}
}
