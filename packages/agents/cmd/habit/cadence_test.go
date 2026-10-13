package main

import (
	"strings"
	"testing"
)

// seq makes entries on consecutive days from 2026-01-01: 'd' done, 'm'
// missed, '.' no entry.
func seq(s string) []dayOutcome {
	var out []dayOutcome
	day := d("2026-01-01")
	for _, c := range s {
		switch c {
		case 'd':
			out = append(out, dayOutcome{day, "done"})
		case 'm':
			out = append(out, dayOutcome{day, "missed"})
		}
		day = day.AddDays(1)
	}
	return out
}

func TestReplayGrowth(t *testing.T) {
	// Done every day: the interval climbs the ladder after 3, 3, 4, 7, 14 and
	// 30 days.
	steps := map[int]int{0: 1, 2: 1, 3: 2, 5: 2, 6: 4, 9: 4, 10: 7, 16: 7, 17: 14, 30: 14, 31: 30, 60: 30, 61: 60, 200: 60}
	for n, want := range steps {
		st := replay(seq(strings.Repeat("d", n)))
		if st.Interval != want {
			t.Errorf("after %d done days: interval %d; want %d", n, st.Interval, want)
		}
		if st.Streak != n || st.Misses != 0 {
			t.Errorf("after %d done days: streak %d misses %d", n, st.Streak, st.Misses)
		}
	}
}

func TestReplayMisses(t *testing.T) {
	for _, c := range []struct {
		entries          string
		interval, streak int
		misses           int
	}{
		{"", 1, 0, 0},
		{"m", 1, 0, 1},
		{"mm", 1, 0, 2},
		{"dddddddddd", 7, 10, 0},  // 1->2->4->7
		{"ddddddddddm", 4, 0, 1},  // one slip: down a step
		{"ddddddddddmm", 1, 0, 2}, // a second in a row: reset
		{"ddddddddddmd", 4, 1, 0}, // recovering at 4
		// After a slip the run starts again: 4 more done days to grow.
		{"ddddddddddmdddd", 7, 4, 0},
		{"ddddddddddmddd", 4, 3, 0},
		// Two separate slips each step down once.
		{"ddddddddddmdmd", 2, 1, 0},
		// Days with no entry are skipped.
		{"ddd...ddd", 4, 6, 0},
		{"ddddddddddm...m", 1, 0, 2},
	} {
		st := replay(seq(c.entries))
		if st.Interval != c.interval || st.Streak != c.streak || st.Misses != c.misses {
			t.Errorf("%q: interval %d streak %d misses %d; want %d %d %d",
				c.entries, st.Interval, st.Streak, st.Misses, c.interval, c.streak, c.misses)
		}
	}
}

func TestReplayLast(t *testing.T) {
	st := replay(seq("dd.m.."))
	if st.Last.String() != "2026-01-04" {
		t.Errorf("last = %s", st.Last)
	}
	if !replay(nil).Last.IsZero() {
		t.Error("last with no entries is not zero")
	}
}

func TestLadderSteps(t *testing.T) {
	for in, want := range map[int]int{1: 2, 2: 4, 4: 7, 7: 14, 14: 30, 30: 60, 60: 60} {
		if got := stepUp(in); got != want {
			t.Errorf("stepUp(%d) = %d; want %d", in, got, want)
		}
	}
	for in, want := range map[int]int{1: 1, 2: 1, 4: 2, 7: 4, 14: 7, 30: 14, 60: 30} {
		if got := stepDown(in); got != want {
			t.Errorf("stepDown(%d) = %d; want %d", in, got, want)
		}
	}
	// Every interval fits the schema's check (1..60).
	for _, v := range ladder {
		if v < 1 || v > 60 {
			t.Errorf("ladder has %d", v)
		}
	}
}

func TestNextCheck(t *testing.T) {
	today := d("2026-10-13")
	for _, c := range []struct {
		last     string
		interval int
		want     string
	}{
		{"", 1, "2026-10-13"},           // nothing logged: due today
		{"2026-10-13", 1, "2026-10-14"}, // logged today
		{"2026-10-13", 7, "2026-10-20"},
		{"2026-10-12", 1, "2026-10-14"}, // logged yesterday's: never before tomorrow
		{"2026-10-10", 7, "2026-10-17"},
		{"2026-09-01", 4, "2026-10-14"},
	} {
		var last Day
		if c.last != "" {
			last = d(c.last)
		}
		if got := nextCheck(last, c.interval, today); got.String() != c.want {
			t.Errorf("nextCheck(%q, %d) = %s; want %s", c.last, c.interval, got, c.want)
		}
	}
}

// The rule in the help text matches the code's ladder.
func TestCadenceRuleText(t *testing.T) {
	if !strings.Contains(cadenceRule, "Intervals: 1, 2, 4, 7, 14, 30, 60 days") {
		t.Error("cadenceRule does not list the ladder")
	}
}
