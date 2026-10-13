package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// The wake-up request the agents worker reads after each turn (its format and
// limits are packages/agents/internal/session/wakeup.go's; a test checks the
// two agree).
const (
	defaultOutbox = "/sandbox/outbox/wakeup.json"
	minLead       = 10 * time.Minute
	maxLead       = 60 * 24 * time.Hour
	maxNoteChars  = 500
)

type wakeupRequest struct {
	At   string `json:"at"`
	Note string `json:"note"`
}

// checkWakeup refuses what the worker would refuse, in words for the agent.
func checkWakeup(at, now time.Time, note string) error {
	lead := at.Sub(now)
	if lead < minLead {
		return fmt.Errorf("%s is %s from now; a wake-up must be at least 10m ahead",
			at.Format(time.RFC3339), humanDuration(lead))
	}
	if lead > maxLead {
		return fmt.Errorf("%s is %s from now; a wake-up must be at most 60d ahead",
			at.Format(time.RFC3339), humanDuration(lead))
	}
	if strings.TrimSpace(note) == "" {
		return fmt.Errorf(`the note is empty: say what the wake-up is for, for your future self (like "end-of-day check-in: meditate, walk")`)
	}
	if n := utf8.RuneCountInString(note); n > maxNoteChars {
		return fmt.Errorf("the note is %d characters; at most %d", n, maxNoteChars)
	}
	return nil
}

// outboxJSON is the file's content: at in the user's zone, with its offset.
func outboxJSON(at time.Time, loc *time.Location, note string) ([]byte, error) {
	b, err := json.Marshal(wakeupRequest{At: at.In(loc).Format(time.RFC3339), Note: note})
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// writeOutbox replaces the file atomically, so the worker never reads half
// of one.
func writeOutbox(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %v", dir, err)
	}
	f, err := os.CreateTemp(dir, ".wakeup-*.json")
	if err != nil {
		return fmt.Errorf("writing in %s: %v", dir, err)
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp)
		return fmt.Errorf("writing %s: %v", tmp, errors.Join(werr, cerr))
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("writing %s: %v", path, err)
	}
	return nil
}
