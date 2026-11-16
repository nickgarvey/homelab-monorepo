package pad

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// recorder logs every emit and sleep, as a timeline.
type recorder struct{ log []string }

func (r *recorder) exec() *Executor {
	return &Executor{
		Emit: func(ev []Event) error {
			var s []string
			for _, e := range ev {
				s = append(s, e.String())
			}
			r.log = append(r.log, strings.Join(s, ","))
			return nil
		},
		Sleep: func(d time.Duration) { r.log = append(r.log, d.String()) },
	}
}

func (r *recorder) run(t *testing.T, lines ...string) string {
	t.Helper()
	e := r.exec()
	for _, l := range lines {
		if err := e.Run(l); err != nil {
			t.Fatalf("%s: %v", l, err)
		}
	}
	return strings.Join(r.log, " | ")
}

func TestParseChord(t *testing.T) {
	c, err := ParseChord("a+Start+UP+l")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(c.Events(true)); got != "[1/304/1 1/315/1 3/17/-1 1/310/1]" {
		t.Errorf("down: %s", got)
	}
	if got := fmt.Sprint(c.Events(false)); got != "[1/304/0 1/315/0 3/17/0 1/310/0]" {
		t.Errorf("up: %s", got)
	}
	for _, bad := range []string{"A+Q", "", "A+"} {
		if _, err := ParseChord(bad); err == nil {
			t.Errorf("ParseChord accepted %q", bad)
		}
	}
}

func TestSeq(t *testing.T) {
	got := (&recorder{}).run(t, "seq A RIGHT:200 wait:500 b+left:10")
	want := "1/304/1 | 80ms | 1/304/0 | 60ms | " +
		"3/16/1 | 200ms | 3/16/0 | 60ms | " +
		"500ms | " +
		"1/305/1,3/16/-1 | 10ms | 1/305/0,3/16/0 | 60ms"
	if got != want {
		t.Errorf("timeline\n got: %s\nwant: %s", got, want)
	}
}

func TestSticksAndTriggers(t *testing.T) {
	got := (&recorder{}).run(t, "stick l -1 0.5", "stick r 0 1", "trigger r 1", "trigger l 0.5")
	want := "3/0/-32767,3/1/16384 | 3/2/0,3/5/32767 | 3/9/255 | 3/10/128"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestUpAll(t *testing.T) {
	got := (&recorder{}).run(t, "up all")
	if !strings.HasPrefix(got, "1/304/0,1/305/0,") || !strings.HasSuffix(got, "1/318/0,3/16/0,3/17/0") {
		t.Errorf("up all: %s", got)
	}
}

func TestCheckRejectsBeforeRunning(t *testing.T) {
	for _, lines := range [][]string{
		{"tap A", "seq A wait B:x"},
		{"down A", "stick l 2 0"},
		{"tap A", "jump"},
	} {
		if err := Check(lines); err == nil {
			t.Errorf("Check accepted %q", lines)
		}
	}
	if err := Check([]string{"tap A 100", "seq A B wait:100 UP:300", "reset"}); err != nil {
		t.Errorf("Check rejected valid commands: %v", err)
	}
}
