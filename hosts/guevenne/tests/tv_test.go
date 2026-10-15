// Regression tests for the guevenne TV session: gamescope, Steam Big Picture,
// the HDMI-only output and audio routing.
//
// Every test here exists because the corresponding behaviour broke once. See
// README.md for which bug each one covers.
package tvtest

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

const guevenne = "guevenne"

// run executes a command on a host. Stdout is returned trimmed; stderr is
// folded into the error so a failure says what actually went wrong.
func run(host, cmd string) (string, error) {
	c := exec.Command("ssh",
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new", host, cmd)
	var out, errb strings.Builder
	c.Stdout = &out
	c.Stderr = &errb
	err := c.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		return s, fmt.Errorf("%s: %s: %w (stderr: %s)", host, cmd, err, strings.TrimSpace(errb.String()))
	}
	return s, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func TestGamescopeSessionRunning(t *testing.T) {
	out, err := run(guevenne, "pgrep -a gamescope | head -3")
	if err != nil || out == "" {
		t.Fatalf("no gamescope process: %v (%q)", err, out)
	}
	if !strings.Contains(out, "--prefer-output") {
		t.Errorf("gamescope not started with the TV output pin: %q", out)
	}
}

// Steam's web helper needs its own Xwayland or QtWebEngine children land in the
// game's namespace and die.
func TestTwoXwaylandServers(t *testing.T) {
	out, _ := run(guevenne, "pgrep -c Xwayland")
	if lastLine(out) != "2" {
		t.Errorf("want 2 Xwayland servers, got %q", lastLine(out))
	}
}

func TestSteamRunning(t *testing.T) {
	if out, err := run(guevenne, "pgrep -f 'steam.*-tenfoot' | head -1"); err != nil || out == "" {
		t.Fatalf("Steam Big Picture not running: %v (%q)", err, out)
	}
}

// The broken internal panel must stay dark; the TV must be the only output.
func TestOnlyHDMIOutputEnabled(t *testing.T) {
	hdmi, _ := run(guevenne, "cat /sys/class/drm/card*-HDMI-A-1/status 2>/dev/null | head -1")
	if lastLine(hdmi) != "connected" {
		t.Errorf("HDMI-A-1 status = %q, want connected", lastLine(hdmi))
	}
	edp, _ := run(guevenne, "cat /sys/class/drm/card*-eDP-1/enabled 2>/dev/null | head -1")
	if s := lastLine(edp); s == "enabled" {
		t.Errorf("broken internal panel eDP-1 is enabled")
	}
}

func TestAudioRoutesToTV(t *testing.T) {
	out, err := run(guevenne, "wpctl status 2>/dev/null | grep -A5 Sinks | grep '\\*'")
	if err != nil {
		t.Fatalf("wpctl status failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "hdmi") {
		t.Errorf("default sink is not HDMI: %q", lastLine(out))
	}
}
