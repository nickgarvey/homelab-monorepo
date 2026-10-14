package habitadmin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/agents/internal/session"
)

// fakeData is the habits in memory.
type fakeData struct {
	habits  []Habit
	cadence map[string]Cadence
	entries []Entry
	deleted []string
	updates []string
}

func (f *fakeData) Habits(context.Context) ([]Habit, error) { return f.habits, nil }
func (f *fakeData) Cadences(context.Context) (map[string]Cadence, error) {
	return f.cadence, nil
}
func (f *fakeData) Entries(_ context.Context, habit, since string) ([]Entry, error) {
	var es []Entry
	for _, e := range f.entries {
		if e.Habit == habit && e.Day >= since {
			es = append(es, e)
		}
	}
	return es, nil
}
func (f *fakeData) Count(_ context.Context, habit string) (Deleted, error) {
	return Deleted{Entries: 3, Notes: 2}, nil
}
func (f *fakeData) UpdateHabit(_ context.Context, name string, body map[string]any) error {
	for i := range f.habits {
		if f.habits[i].Name != name {
			continue
		}
		for k, v := range body {
			f.updates = append(f.updates, name+" "+k)
			switch k {
			case "archived_at":
				if v == nil {
					f.habits[i].ArchivedAt = nil
				} else {
					t := v.(time.Time)
					f.habits[i].ArchivedAt = &t
				}
			case "status":
				f.habits[i].Status = v.(string)
			}
		}
		return nil
	}
	return errors.New("no such habit")
}
func (f *fakeData) UpdateCadence(_ context.Context, habit string, body map[string]any) (Cadence, error) {
	c := f.cadence[habit]
	if v, ok := body["interval_days"]; ok {
		c.IntervalDays = v.(int)
	}
	if v, ok := body["next_check"]; ok {
		c.NextCheck = v.(string)
	}
	if v, ok := body["reason"]; ok {
		if v == nil {
			c.Reason = nil
		} else {
			r := v.(string)
			c.Reason = &r
		}
	}
	f.cadence[habit] = c
	return c, nil
}
func (f *fakeData) Delete(_ context.Context, habit string) (Deleted, error) {
	f.deleted = append(f.deleted, habit)
	return Deleted{Entries: 3, Notes: 2}, nil
}

type fakeWakeups struct {
	pending  *session.WakeupStatus
	set      []session.Wakeup
	cancels  int
	defaults []int
}

func (f *fakeWakeups) Pending(_ context.Context, conv string) (*session.WakeupStatus, error) {
	return f.pending, nil
}
func (f *fakeWakeups) Set(_ context.Context, conv string, w session.Wakeup, def int) error {
	f.set = append(f.set, w)
	f.defaults = append(f.defaults, def)
	return nil
}
func (f *fakeWakeups) Cancel(context.Context, string) (bool, error) {
	f.cancels++
	return f.pending != nil, nil
}

// now is 2026-10-14 09:00 in Los Angeles.
var now = time.Date(2026, 10, 14, 16, 0, 0, 0, time.UTC)

func newAdmin() (*Admin, *fakeData, *fakeWakeups) {
	d := &fakeData{
		habits: []Habit{
			{Name: "Houdini", Status: "active"},
			{Name: "read, 10 pages", Status: "paused"},
		},
		cadence: map[string]Cadence{
			"Houdini":        {Habit: "Houdini", IntervalDays: 2, NextCheck: "2026-10-15"},
			"read, 10 pages": {Habit: "read, 10 pages", IntervalDays: 1, NextCheck: "2026-10-14"},
		},
		entries: []Entry{{Habit: "Houdini", Day: "2026-10-13", Outcome: "done"}, {Habit: "Houdini", Day: "2026-10-01", Outcome: "missed"}},
	}
	w := &fakeWakeups{}
	a := &Admin{
		Data: d, Wakeups: w,
		Conversation: func(_ context.Context, user string) (string, error) { return "matrix-room", nil },
		Session:      func() (string, int, error) { return "America/Los_Angeles", 86400, nil },
		Now:          func() time.Time { return now },
	}
	return a, d, w
}

func run(a *Admin, text string) string {
	return a.Run(context.Background(), "@ngarvey:matrix.garvey.sh", text)
}

func TestHelpAndUnknown(t *testing.T) {
	a, _, _ := newAdmin()
	assert.Contains(t, run(a, "help"), "delete <habit> --permanent")
	assert.Contains(t, run(a, "frobnicate"), "Send help")
}

func TestHabitsHidesArchivedUnlessAll(t *testing.T) {
	a, d, _ := newAdmin()
	assert.Contains(t, run(a, "habits"), "**Houdini**: active, next check-in 2026-10-15, every 2 days")
	run(a, "archive houdini")
	out := run(a, "habits")
	assert.NotContains(t, out, "Houdini")
	assert.Contains(t, out, "1 archived")
	assert.Contains(t, run(a, "habits --all"), "**Houdini**: archived (was active)")
	assert.Equal(t, []string{"Houdini archived_at"}, d.updates)
}

func TestArchiveAndUnarchive(t *testing.T) {
	a, d, _ := newAdmin()
	assert.Contains(t, run(a, "archive HOUDINI"), "Archived Houdini. Habit Helper no longer sees it")
	assert.Equal(t, "Houdini is archived already.", run(a, "archive houdini"))
	assert.Contains(t, run(a, "pause houdini"), "unarchive it first")
	assert.Equal(t, "Houdini is back, active.", run(a, "unarchive houdini"))
	assert.Nil(t, d.habits[0].ArchivedAt)
	assert.Contains(t, run(a, "archive nope"), `no habit "nope". Habits: Houdini, read, 10 pages`)
}

func TestPauseResumeWithSpacesInTheName(t *testing.T) {
	a, _, _ := newAdmin()
	assert.Equal(t, "read, 10 pages is active (was paused).", run(a, "resume read, 10 pages"))
	assert.Equal(t, "Houdini is paused (was active).", run(a, "pause Houdini"))
}

func TestDeleteNeedsPermanentAndYes(t *testing.T) {
	a, d, _ := newAdmin()
	assert.Contains(t, run(a, "delete houdini"), "needs --permanent")
	assert.Contains(t, run(a, "yes"), "nothing to confirm")

	assert.Contains(t, run(a, "delete houdini --permanent"), "deletes **Houdini** for good: 3 entries, 2 notes")
	// Anything but yes cancels.
	run(a, "habits")
	assert.Contains(t, run(a, "yes"), "nothing to confirm")
	assert.Empty(t, d.deleted)

	// A phone's dash works as --.
	run(a, "delete houdini —permanent")
	assert.Equal(t, "Deleted Houdini for good, with 3 entries and 2 notes.", run(a, "Yes"))
	assert.Equal(t, []string{"Houdini"}, d.deleted)

	// The confirmation expires.
	run(a, "delete houdini --permanent")
	a.Now = func() time.Time { return now.Add(6 * time.Minute) }
	assert.Contains(t, run(a, "yes"), "nothing to confirm")
	assert.Len(t, d.deleted, 1)
}

func TestCadence(t *testing.T) {
	a, d, _ := newAdmin()
	assert.Equal(t, "Houdini: every 7 days, next check-in 2026-10-21, until cadence Houdini auto.", run(a, "cadence houdini every 7d"))
	assert.Equal(t, overrideReason, *d.cadence["Houdini"].Reason)
	assert.Contains(t, run(a, "cadence read, 10 pages every 3 next tomorrow"), "every 3 days, next check-in 2026-10-15")
	assert.Contains(t, run(a, "cadence houdini every 3d next 2026-10-01"), "before today")
	assert.Contains(t, run(a, "cadence houdini every 90d"), "1d to 60d")
	assert.Contains(t, run(a, "cadence houdini auto"), "follows the tapering rule again")
	assert.Nil(t, d.cadence["Houdini"].Reason)
	assert.Contains(t, run(a, "cadence houdini"), "cadence <habit> every")
}

func TestShow(t *testing.T) {
	a, d, _ := newAdmin()
	cue := "if I finish coffee, then I picture Houdini"
	d.habits[0].Cue = &cue
	out := run(a, "show houdini")
	assert.Contains(t, out, "**Houdini**: active")
	assert.Contains(t, out, "- Cue: "+cue)
	assert.Contains(t, out, "every 2 days, next check-in 2026-10-15")
	// 14 days ending today, from the 1st (missed); the 13th is done.
	assert.Contains(t, out, "Last 14 days: ✗···········✓· ")
}

func TestWakeupShowSetCancel(t *testing.T) {
	a, _, w := newAdmin()
	assert.Contains(t, run(a, "wakeup"), "No wake-up pending")
	w.pending = &session.WakeupStatus{Pending: &session.Wakeup{At: now.Add(11 * time.Hour), Note: "end-of-day check-in: Houdini"}}
	assert.Equal(t, "Next wake-up: Wed 2026-10-14 20:00 PDT (in 11h 0m): end-of-day check-in: Houdini", run(a, "wakeup"))

	assert.Equal(t, "Set. Next wake-up: Wed 2026-10-14 09:05 PDT (in 5m): set from the admin chat", run(a, "wakeup in 5m"))
	assert.Contains(t, run(a, "wakeup in 2d “check on Houdini”"), "Fri 2026-10-16 09:00 PDT (in 2d 0h): check on Houdini")
	// HH:MM that has passed today is tomorrow.
	assert.Contains(t, run(a, "wakeup at 08:30 morning"), "Thu 2026-10-15 08:30 PDT")
	assert.Contains(t, run(a, "wakeup at 20:00"), "Wed 2026-10-14 20:00 PDT")
	assert.Contains(t, run(a, "wakeup at tomorrow 07:00 hi"), "Thu 2026-10-15 07:00 PDT")
	assert.Contains(t, run(a, "wakeup at 2026-10-20 12:00"), "Tue 2026-10-20 12:00 PDT")
	assert.Contains(t, run(a, "wakeup at 2026-10-01 12:00"), "has passed")
	assert.Contains(t, run(a, "wakeup in 61d"), "at most 60 days")
	assert.Contains(t, run(a, "wakeup in soon"), "like 5m")
	require.Len(t, w.set, 6)
	assert.Equal(t, now.Add(5*time.Minute), w.set[0].At)
	assert.Equal(t, 86400, w.defaults[0])

	assert.Equal(t, "Cancelled. Habit Helper won't wake up until you next write to it.", run(a, "wakeup cancel"))
	w.pending = nil
	assert.Equal(t, "No wake-up was pending.", run(a, "wakeup cancel"))
}

func TestWakeupWithoutAChat(t *testing.T) {
	a, _, _ := newAdmin()
	a.Conversation = func(context.Context, string) (string, error) {
		return "", errors.New("Habit Helper has no chat with you")
	}
	assert.Equal(t, "Habit Helper has no chat with you", run(a, "wakeup in 5m"))
}

func TestUnquote(t *testing.T) {
	for in, want := range map[string]string{`"a b"`: "a b", "“a”": "a", "‘a’": "a", "a": "a", `"`: `"`} {
		assert.Equal(t, want, unquote(in), in)
	}
	assert.True(t, strings.HasPrefix(help, "Commands:"))
}
