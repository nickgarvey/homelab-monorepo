package main

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(defaultTZ)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func d(s string) Day {
	v, err := parseISODay(s)
	if err != nil {
		panic(err)
	}
	return v
}

func TestParseDay(t *testing.T) {
	today := d("2026-10-13") // a Tuesday
	for in, want := range map[string]string{
		"today":      "2026-10-13",
		"Yesterday":  "2026-10-12",
		"tue":        "2026-10-13",
		"tuesday":    "2026-10-13",
		"mon":        "2026-10-12",
		"wed":        "2026-10-07",
		"sun":        "2026-10-11",
		"2026-09-01": "2026-09-01",
		"2026-12-31": "2026-12-31", // future dates parse; log refuses them
	} {
		got, err := parseDay(in, today)
		if err != nil || got.String() != want {
			t.Errorf("parseDay(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"", "tmrw", "2026-13-01", "10/13", "-1"} {
		if _, err := parseDay(in, today); err == nil {
			t.Errorf("parseDay(%q) succeeded", in)
		}
	}
}

func TestTodayInZone(t *testing.T) {
	loc := mustLoc(t)
	// 02:30 UTC on the 14th is still the 13th in Los Angeles.
	now := time.Date(2026, 10, 14, 2, 30, 0, 0, time.UTC)
	if got := dayOf(now.In(loc)).String(); got != "2026-10-13" {
		t.Errorf("today = %s", got)
	}
	a := &app{now: func() time.Time { return now }, loc: time.UTC}
	if got := a.today().String(); got != "2026-10-14" {
		t.Errorf("today in UTC = %s", got)
	}
}

func TestParseWhen(t *testing.T) {
	loc := mustLoc(t)
	now := time.Date(2026, 10, 13, 17, 0, 0, 0, loc) // 17:00 PDT
	checkin := clock{20, 0}
	for in, want := range map[string]string{
		"+90m":                      "2026-10-13T18:30:00-07:00",
		"+3h":                       "2026-10-13T20:00:00-07:00",
		"+2d":                       "2026-10-15T17:00:00-07:00",
		"+1d6h":                     "2026-10-14T23:00:00-07:00",
		"21:15":                     "2026-10-13T21:15:00-07:00",
		"08:00":                     "2026-10-14T08:00:00-07:00", // passed today: tomorrow
		"tomorrow":                  "2026-10-14T20:00:00-07:00",
		"2026-10-20":                "2026-10-20T20:00:00-07:00",
		"2026-11-05":                "2026-11-05T20:00:00-08:00", // after DST ends
		"2026-10-20 09:30":          "2026-10-20T09:30:00-07:00",
		"2026-10-20T09:30":          "2026-10-20T09:30:00-07:00",
		"2026-10-20T16:00:00Z":      "2026-10-20T09:00:00-07:00",
		"2026-10-20T09:30:00-04:00": "2026-10-20T06:30:00-07:00",
	} {
		got, err := parseWhen(in, now, loc, checkin)
		if err != nil {
			t.Errorf("parseWhen(%q): %v", in, err)
			continue
		}
		if s := got.In(loc).Format(time.RFC3339); s != want {
			t.Errorf("parseWhen(%q) = %s; want %s", in, s, want)
		}
	}
	for _, in := range []string{"", "+", "+x", "+-1h", "soon", "25:00", "2026-10-20 9pm", "next week"} {
		if _, err := parseWhen(in, now, loc, checkin); err == nil {
			t.Errorf("parseWhen(%q) succeeded", in)
		}
	}
}

func TestParseClock(t *testing.T) {
	for in, want := range map[string]clock{"20:00": {20, 0}, "7:05": {7, 5}, "00:00": {0, 0}, "23:59": {23, 59}} {
		if got, err := parseClock(in); err != nil || got != want {
			t.Errorf("parseClock(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"24:00", "8", "8pm", "12:60", ""} {
		if _, err := parseClock(in); err == nil {
			t.Errorf("parseClock(%q) succeeded", in)
		}
	}
}

func TestParseISODayOrRelative(t *testing.T) {
	today := d("2026-10-13")
	for in, want := range map[string]string{"today": "2026-10-13", "tomorrow": "2026-10-14", "+7": "2026-10-20", "+7d": "2026-10-20", "2026-11-01": "2026-11-01"} {
		if got, err := parseISODayOrRelative(in, today); err != nil || got.String() != want {
			t.Errorf("%q = %v, %v; want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"+", "+x", "+7w", "next week"} {
		if _, err := parseISODayOrRelative(in, today); err == nil {
			t.Errorf("%q succeeded", in)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0m", 45 * time.Minute: "45m", 3 * time.Hour: "3h", 27*time.Hour + 5*time.Minute: "1d 3h 5m", -10 * time.Minute: "-10m",
	} {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q; want %q", d, got, want)
		}
	}
}

func TestSuggestWakeup(t *testing.T) {
	loc := mustLoc(t)
	checkin := clock{20, 0}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		due  string
		now  string
		want string
	}{
		{"2026-10-13", "2026-10-13 09:00", "2026-10-13 20:00"}, // due today, later today
		{"2026-10-16", "2026-10-13 09:00", "2026-10-16 20:00"}, // due later
		{"2026-10-13", "2026-10-13 19:55", "2026-10-14 20:00"}, // under 10 minutes left today
		{"2026-10-13", "2026-10-13 21:00", "2026-10-14 20:00"}, // today's check-in time passed
		{"2026-10-01", "2026-10-13 09:00", "2026-10-13 20:00"}, // overdue
	} {
		got := suggestWakeup(d(c.due), at(c.now), loc, checkin)
		if !got.Equal(at(c.want)) {
			t.Errorf("due %s now %s: %s; want %s", c.due, c.now, got, c.want)
		}
	}
	// Never further than the worker accepts.
	now := at("2026-10-13 21:00")
	got := suggestWakeup(d("2026-12-12"), now, loc, checkin)
	if got.Sub(now) > maxLead {
		t.Errorf("suggestion %s is over 60d ahead", got)
	}
}
