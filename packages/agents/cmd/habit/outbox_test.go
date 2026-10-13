package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/agents/internal/session"
)

// The CLI's limits and file are the worker's.
func TestOutboxMatchesWorker(t *testing.T) {
	if defaultOutbox != session.OutboxFile {
		t.Errorf("outbox %s; the worker reads %s", defaultOutbox, session.OutboxFile)
	}
	if minLead != session.MinWakeupLead || maxLead != session.MaxWakeupLead || maxNoteChars != session.MaxNoteChars {
		t.Error("wake-up limits differ from packages/agents/internal/session/wakeup.go")
	}
}

// What wake-at writes, the worker accepts as the same request.
func TestOutboxJSONParsesInWorker(t *testing.T) {
	loc := mustLoc(t)
	now := time.Date(2026, 10, 13, 17, 0, 0, 0, loc)
	for _, c := range []struct {
		at   time.Time
		note string
	}{
		{now.Add(10 * time.Minute), "end-of-day check-in"},
		{now.Add(60 * 24 * time.Hour), "far"},
		{now.Add(26 * time.Hour), `quotes " and \ and ünïcödé, <tags> & newlines` + "\n" + strings.Repeat("x", 400)},
	} {
		if err := checkWakeup(c.at, now, c.note); err != nil {
			t.Errorf("checkWakeup(%s): %v", c.at, err)
			continue
		}
		data, err := outboxJSON(c.at, loc, c.note)
		if err != nil {
			t.Fatal(err)
		}
		w, rejected := session.ParseOutbox(data, now)
		if rejected != "" {
			t.Errorf("worker refused %s: %s", data, rejected)
			continue
		}
		if !w.At.Equal(c.at) || w.Note != c.note {
			t.Errorf("worker read %v %q; want %v %q", w.At, w.Note, c.at, c.note)
		}
	}
	data, _ := outboxJSON(time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC), loc, "n")
	if got := string(data); got != `{"at":"2026-10-13T20:00:00-07:00","note":"n"}`+"\n" {
		t.Errorf("file = %s", got)
	}
}

// checkWakeup refuses what the worker refuses.
func TestCheckWakeup(t *testing.T) {
	now := time.Date(2026, 10, 13, 17, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		lead time.Duration
		note string
	}{
		{9 * time.Minute, "soon"},
		{-time.Hour, "past"},
		{60*24*time.Hour + time.Minute, "far"},
		{time.Hour, strings.Repeat("é", 501)},
		{time.Hour, "  "},
	} {
		at := now.Add(c.lead)
		if err := checkWakeup(at, now, c.note); err == nil {
			t.Errorf("checkWakeup(+%v, %d chars) accepted", c.lead, len(c.note))
		}
		// The worker agrees, except that it takes an empty note.
		if strings.TrimSpace(c.note) != "" {
			data, _ := outboxJSON(at, time.UTC, c.note)
			if _, rejected := session.ParseOutbox(data, now); rejected == "" {
				t.Errorf("worker accepted +%v", c.lead)
			}
		}
	}
	if err := checkWakeup(now.Add(time.Hour), now, strings.Repeat("é", 500)); err != nil {
		t.Errorf("500 characters: %v", err)
	}
}

func TestWriteOutbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox", "wakeup.json")
	for _, content := range []string{"first\n", "second\n"} {
		if err := writeOutbox(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != content {
			t.Errorf("file = %q, %v; want %q", got, err, content)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("outbox holds %d files; want only wakeup.json", len(entries))
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode())
	}
}
