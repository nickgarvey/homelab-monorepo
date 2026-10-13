package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Days are the user's local calendar dates. A Day is a time.Time at midnight
// UTC, so adding days and comparing never meets a DST change.
type Day struct{ t time.Time }

const dayLayout = "2006-01-02"

func dayOf(t time.Time) Day {
	y, m, d := t.Date()
	return Day{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

func parseISODay(s string) (Day, error) {
	t, err := time.Parse(dayLayout, s)
	if err != nil {
		return Day{}, err
	}
	return Day{t}, nil
}

func (d Day) String() string    { return d.t.Format(dayLayout) }
func (d Day) IsZero() bool      { return d.t.IsZero() }
func (d Day) AddDays(n int) Day { return Day{d.t.AddDate(0, 0, n)} }
func (d Day) Before(o Day) bool { return d.t.Before(o.t) }
func (d Day) After(o Day) bool  { return d.t.After(o.t) }
func (d Day) Weekday() string   { return d.t.Format("Mon") }
func (d Day) Sub(o Day) int     { return int(d.t.Sub(o.t).Hours() / 24) }
func (d Day) At(hm clock, loc *time.Location) time.Time {
	y, m, dd := d.t.Date()
	return time.Date(y, m, dd, hm.h, hm.m, 0, 0, loc)
}

func (d Day) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + d.String() + `"`), nil
}

func (d *Day) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		*d = Day{}
		return nil
	}
	v, err := parseISODay(strings.Trim(s, `"`))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

func maxDay(a, b Day) Day {
	if a.After(b) {
		return a
	}
	return b
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "sun": time.Sunday,
	"monday": time.Monday, "mon": time.Monday,
	"tuesday": time.Tuesday, "tue": time.Tuesday, "tues": time.Tuesday,
	"wednesday": time.Wednesday, "wed": time.Wednesday,
	"thursday": time.Thursday, "thu": time.Thursday, "thurs": time.Thursday,
	"friday": time.Friday, "fri": time.Friday,
	"saturday": time.Saturday, "sat": time.Saturday,
}

const dateForms = `today, yesterday, a weekday (mon..sun: the latest one, today included), or YYYY-MM-DD`

// parseDay reads a date argument relative to today in the user's zone.
func parseDay(s string, today Day) (Day, error) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "today":
		return today, nil
	case "yesterday":
		return today.AddDays(-1), nil
	default:
		if wd, ok := weekdays[v]; ok {
			back := (int(today.t.Weekday()) - int(wd) + 7) % 7
			return today.AddDays(-back), nil
		}
		d, err := parseISODay(v)
		if err != nil {
			return Day{}, fmt.Errorf("date %q: want %s", s, dateForms)
		}
		return d, nil
	}
}

// clock is a time of day.
type clock struct{ h, m int }

func (c clock) String() string { return fmt.Sprintf("%02d:%02d", c.h, c.m) }

var clockRE = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)

func parseClock(s string) (clock, error) {
	m := clockRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return clock{}, fmt.Errorf("time of day %q: want HH:MM, 24-hour (like 20:00)", s)
	}
	h, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	return clock{h, mm}, nil
}

const whenForms = `due (the earliest due check-in, from habit cadence), ` +
	`+<duration> (like +90m, +3h, +2d, +1d6h), HH:MM (the next time the clock shows it), ` +
	`tomorrow (at the check-in time), YYYY-MM-DD (at the check-in time), ` +
	`"YYYY-MM-DD HH:MM" or YYYY-MM-DDTHH:MM (in the user's time zone), or an RFC 3339 time with offset`

var plusRE = regexp.MustCompile(`^\+(?:(\d+)d)?(.*)$`)

// parseWhen reads a wake-up time other than "due". checkin is the time of day
// used for a bare date.
func parseWhen(s string, now time.Time, loc *time.Location, checkin clock) (time.Time, error) {
	v := strings.TrimSpace(s)
	bad := fmt.Errorf("time %q: want %s", s, whenForms)
	today := dayOf(now.In(loc))
	switch {
	case strings.HasPrefix(v, "+"):
		m := plusRE.FindStringSubmatch(v)
		var d time.Duration
		if m[1] != "" {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				return time.Time{}, bad
			}
			d = time.Duration(n) * 24 * time.Hour
		}
		if m[2] != "" {
			rest, err := time.ParseDuration(m[2])
			if err != nil || rest < 0 {
				return time.Time{}, bad
			}
			d += rest
		}
		if m[1] == "" && m[2] == "" {
			return time.Time{}, bad
		}
		return now.Add(d), nil
	case strings.EqualFold(v, "tomorrow"):
		return today.AddDays(1).At(checkin, loc), nil
	}
	if c, err := parseClock(v); err == nil {
		t := today.At(c, loc)
		if !t.After(now) {
			t = today.AddDays(1).At(c, loc)
		}
		return t, nil
	}
	if d, err := parseISODay(v); err == nil {
		return d.At(checkin, loc), nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Time{}, bad
}

// humanDuration gives a lead time like "1d 3h" or "45m".
func humanDuration(d time.Duration) string {
	if d < 0 {
		return "-" + humanDuration(-d)
	}
	d = d.Round(time.Minute)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	return strings.Join(parts, " ")
}
