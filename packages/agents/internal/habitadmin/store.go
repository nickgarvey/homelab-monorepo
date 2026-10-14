package habitadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Rows of schema habit (agents/habit-helper/schema/), as habit_admin sees
// them.

type Habit struct {
	Name        string     `json:"name"`
	Cue         *string    `json:"cue"`
	Motivation  *string    `json:"motivation"`
	Preparation *string    `json:"preparation"`
	Status      string     `json:"status"`
	ArchivedAt  *time.Time `json:"archived_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Entry struct {
	Habit   string `json:"habit"`
	Day     string `json:"day"`
	Outcome string `json:"outcome"`
}

type Cadence struct {
	Habit        string  `json:"habit"`
	IntervalDays int     `json:"interval_days"`
	NextCheck    string  `json:"next_check"`
	Streak       int     `json:"streak"`
	Reason       *string `json:"reason"`
}

// Deleted is what delete_habit removed besides the habit.
type Deleted struct {
	Entries int `json:"entries"`
	Notes   int `json:"notes"`
}

// Store is Habit Helper's PostgREST, as role habit_admin.
type Store struct {
	// URL is PostgREST's base URL.
	URL string
	// Token returns a PostgREST token for role habit_admin.
	Token func(ctx context.Context) (string, error)
	HTTP  *http.Client
}

func (s *Store) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	tok, err := s.Token(ctx)
	if err != nil {
		return fmt.Errorf("getting a PostgREST token: %w", err)
	}
	u := strings.TrimRight(s.URL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == "PATCH" {
		req.Header.Set("Prefer", "return=representation")
	}
	hc := s.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("PostgREST: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("PostgREST: %w", err)
	}
	if resp.StatusCode >= 300 {
		var e struct{ Message, Details, Hint string }
		_ = json.Unmarshal(data, &e)
		msg := e.Message
		if msg == "" {
			msg = strings.TrimSpace(string(data))
		}
		return fmt.Errorf("PostgREST: HTTP %d: %s", resp.StatusCode, msg)
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func eq(v string) string { return "eq." + v }

// Habits returns every habit, archived ones included, by name.
func (s *Store) Habits(ctx context.Context) ([]Habit, error) {
	var hs []Habit
	err := s.do(ctx, "GET", "/habits", url.Values{"order": {"name"}}, nil, &hs)
	return hs, err
}

// Cadences returns every habit's cadence row, by habit.
func (s *Store) Cadences(ctx context.Context) (map[string]Cadence, error) {
	var cs []Cadence
	if err := s.do(ctx, "GET", "/cadence", nil, nil, &cs); err != nil {
		return nil, err
	}
	m := make(map[string]Cadence, len(cs))
	for _, c := range cs {
		m[c.Habit] = c
	}
	return m, nil
}

// Entries returns a habit's entries from the day since (YYYY-MM-DD) on.
func (s *Store) Entries(ctx context.Context, habit, since string) ([]Entry, error) {
	var es []Entry
	err := s.do(ctx, "GET", "/entries", url.Values{
		"habit": {eq(habit)}, "day": {"gte." + since}, "order": {"day"},
	}, nil, &es)
	return es, err
}

// Count returns how many entries and notes a habit has.
func (s *Store) Count(ctx context.Context, habit string) (Deleted, error) {
	var n Deleted
	var es, ns []struct{}
	q := url.Values{"habit": {eq(habit)}, "select": {"habit"}}
	if err := s.do(ctx, "GET", "/entries", q, nil, &es); err != nil {
		return n, err
	}
	if err := s.do(ctx, "GET", "/notes", q, nil, &ns); err != nil {
		return n, err
	}
	return Deleted{Entries: len(es), Notes: len(ns)}, nil
}

// UpdateHabit changes the habit's columns in body.
func (s *Store) UpdateHabit(ctx context.Context, name string, body map[string]any) error {
	var rows []Habit
	if err := s.do(ctx, "PATCH", "/habits", url.Values{"name": {eq(name)}}, body, &rows); err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("no habit %q", name)
	}
	return nil
}

// UpdateCadence changes the habit's cadence columns in body.
func (s *Store) UpdateCadence(ctx context.Context, habit string, body map[string]any) (Cadence, error) {
	var rows []Cadence
	if err := s.do(ctx, "PATCH", "/cadence", url.Values{"habit": {eq(habit)}}, body, &rows); err != nil {
		return Cadence{}, err
	}
	if len(rows) != 1 {
		return Cadence{}, fmt.Errorf("habit %q has no cadence row", habit)
	}
	return rows[0], nil
}

// Delete removes a habit and its rows for good, in one transaction.
func (s *Store) Delete(ctx context.Context, habit string) (Deleted, error) {
	var d Deleted
	err := s.do(ctx, "POST", "/rpc/delete_habit", nil, map[string]string{"habit_name": habit}, &d)
	return d, err
}
