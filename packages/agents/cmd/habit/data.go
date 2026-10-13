package main

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Rows of schema habit (agents/habit-helper/schema/).

type Habit struct {
	Name        string    `json:"name"`
	Cue         *string   `json:"cue"`
	Motivation  *string   `json:"motivation"`
	Preparation *string   `json:"preparation"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Entry struct {
	Habit   string  `json:"habit"`
	Day     Day     `json:"day"`
	Outcome string  `json:"outcome"`
	Why     *string `json:"why"`
}

type Note struct {
	ID    int64     `json:"id"`
	Habit string    `json:"habit"`
	Kind  string    `json:"kind"`
	Body  string    `json:"body"`
	At    time.Time `json:"at"`
}

type Cadence struct {
	Habit        string  `json:"habit"`
	IntervalDays int     `json:"interval_days"`
	NextCheck    Day     `json:"next_check"`
	Streak       int     `json:"streak"`
	Misses       int     `json:"misses"`
	LastCheck    Day     `json:"last_check"`
	Reason       *string `json:"reason"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (a *app) habits(ctx context.Context) ([]Habit, error) {
	var hs []Habit
	err := a.api.do(ctx, "GET", "/habits", url.Values{"order": {"name"}}, nil, "", &hs)
	return hs, err
}

// resolve finds a habit by name, ignoring case, or says which habits there
// are.
func (a *app) resolve(ctx context.Context, name string) (Habit, error) {
	hs, err := a.habits(ctx)
	if err != nil {
		return Habit{}, err
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, h := range hs {
		if strings.ToLower(h.Name) == want {
			return h, nil
		}
	}
	if len(hs) == 0 {
		return Habit{}, fmt.Errorf("no habit %q: there are no habits yet; add one with habit add", name)
	}
	var names []string
	for _, h := range hs {
		names = append(names, h.Name)
	}
	return Habit{}, fmt.Errorf("no habit %q. Habits: %s", name, strings.Join(names, ", "))
}

func (a *app) entries(ctx context.Context, habit string) ([]Entry, error) {
	q := url.Values{"order": {"day"}}
	if habit != "" {
		q.Set("habit", eq(habit))
	}
	var es []Entry
	err := a.api.do(ctx, "GET", "/entries", q, nil, "", &es)
	return es, err
}

func (a *app) cadences(ctx context.Context) (map[string]Cadence, error) {
	var cs []Cadence
	if err := a.api.do(ctx, "GET", "/cadence", nil, nil, "", &cs); err != nil {
		return nil, err
	}
	m := map[string]Cadence{}
	for _, c := range cs {
		m[c.Habit] = c
	}
	return m, nil
}

func (a *app) cadence(ctx context.Context, habit string) (Cadence, error) {
	var cs []Cadence
	if err := a.api.do(ctx, "GET", "/cadence", url.Values{"habit": {eq(habit)}}, nil, "", &cs); err != nil {
		return Cadence{}, err
	}
	if len(cs) != 1 {
		return Cadence{}, fmt.Errorf("habit %q has no cadence row", habit)
	}
	return cs[0], nil
}

// patch updates the rows matching filter and returns them.
func patch[T any](ctx context.Context, a *app, table string, filter url.Values, body any) ([]T, error) {
	var rows []T
	err := a.api.do(ctx, "PATCH", "/"+table, filter, body, "return=representation", &rows)
	return rows, err
}

func (a *app) patchCadence(ctx context.Context, habit string, body map[string]any) (Cadence, error) {
	rows, err := patch[Cadence](ctx, a, "cadence", url.Values{"habit": {eq(habit)}}, body)
	if err != nil {
		return Cadence{}, err
	}
	if len(rows) != 1 {
		return Cadence{}, fmt.Errorf("habit %q has no cadence row", habit)
	}
	return rows[0], nil
}

func (a *app) addNotes(ctx context.Context, notes []map[string]any) ([]Note, error) {
	if len(notes) == 0 {
		return nil, nil
	}
	var out []Note
	err := a.api.do(ctx, "POST", "/notes", nil, notes, "return=representation", &out)
	return out, err
}

// applyRule sets a habit's cadence from its entries (see cadenceRule). An
// override (cur.Reason set) keeps its interval; with cur.Reason nil the
// row's reason is cleared.
func (a *app) applyRule(ctx context.Context, habit string, cur Cadence) (Cadence, error) {
	es, err := a.entries(ctx, habit)
	if err != nil {
		return Cadence{}, err
	}
	st := replay(outcomes(es))
	interval := st.Interval
	if cur.Reason != nil {
		interval = cur.IntervalDays
	}
	body := map[string]any{
		"interval_days": interval,
		"streak":        st.Streak,
		"misses":        st.Misses,
		"last_check":    st.Last,
		"next_check":    nextCheck(st.Last, interval, a.today()),
		"reason":        cur.Reason,
	}
	return a.patchCadence(ctx, habit, body)
}

func outcomes(es []Entry) []dayOutcome {
	out := make([]dayOutcome, 0, len(es))
	for _, e := range es {
		out = append(out, dayOutcome{e.Day, e.Outcome})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out
}

// createdDay is the local day a habit was added.
func (a *app) createdDay(h Habit) Day { return dayOf(h.CreatedAt.In(a.loc)) }

// since is the first day a habit has data for: the day it was added, or its
// first entry if one was logged for an earlier day.
func (a *app) since(h Habit, es []Entry) Day {
	s := a.createdDay(h)
	for _, e := range es {
		if e.Habit == h.Name && e.Day.Before(s) {
			s = e.Day
		}
	}
	return s
}
