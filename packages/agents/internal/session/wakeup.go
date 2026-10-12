package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Wake-ups: a turn the worker starts itself in a conversation, for types that
// set session.defaultWakeup. The agent asks for one by writing OutboxFile
// during a turn; the chat package's Wakeup workflow keeps the one pending.

const (
	// OutboxFile is where the agent writes a wake-up request:
	// {"at": "<RFC 3339 time>", "note": "<text>"}.
	OutboxFile = "/sandbox/outbox/wakeup.json"
	// MaxOutboxBytes bounds the file; a larger one is refused.
	MaxOutboxBytes = 4096
	// MaxNoteChars bounds a request's note.
	MaxNoteChars = 500
	// MinWakeupLead and MaxWakeupLead bound how far ahead a request may be.
	MinWakeupLead = 10 * time.Minute
	MaxWakeupLead = 60 * 24 * time.Hour

	// DefaultNote is the note of a wake-up the agent did not ask for.
	DefaultNote = "default"
	// Silent is the whole of a reply that is recorded but not meant for
	// the user.
	Silent = "<silent>"
)

// headerTime is how the prompt header and the wake-up prompt give times.
const headerTime = "2006-01-02T15:04Z07:00"

// Wakeup is a pending wake-up: when, and the note for the agent's future
// self.
type Wakeup struct {
	At   time.Time
	Note string
}

// WakeupStatus is what the Wakeup workflow reports for the prompt header.
type WakeupStatus struct {
	// Pending is the next wake-up; nil while a wake-up turn runs.
	Pending *Wakeup
	// NextFloorSeconds, while a wake-up turn runs, is how long after it the
	// next comes if the turn asks for none.
	NextFloorSeconds int
	// Rejected is why the last turn's wake-up request was refused, if it was.
	Rejected string
}

// IsSilent reports whether a reply is the agent choosing to say nothing.
func IsSilent(reply string) bool {
	return strings.TrimSpace(reply) == Silent
}

// Location is a type's session.timezone; UTC if unset.
func Location(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("timezone %q: %v: %w", name, err, ErrInvalid)
	}
	return loc, nil
}

// Header starts every prompt of a type with wake-ups: the time now in the
// type's zone, the pending wake-up (st is nil when the conversation has none
// yet), and a request of the last turn's that was refused.
func Header(now time.Time, loc *time.Location, st *WakeupStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[now] %s (%s)\n", now.In(loc).Format(headerTime), loc)
	switch {
	case st == nil:
		b.WriteString("[next wake-up] none yet\n")
	case st.Pending == nil:
		fmt.Fprintf(&b, "[next wake-up] none: this turn is a wake-up. Without a request, the next comes %s after it.\n",
			formatDuration(time.Duration(st.NextFloorSeconds)*time.Second))
	default:
		fmt.Fprintf(&b, "[next wake-up] %s. Note: %s\n", st.Pending.At.In(loc).Format(headerTime), st.Pending.Note)
	}
	if st != nil && st.Rejected != "" {
		fmt.Fprintf(&b, "[wake-up request refused] %s: %s. The next wake-up above is unchanged.\n", OutboxFile, st.Rejected)
	}
	b.WriteString("\n")
	return b.String()
}

// WakeupPrompt is the message of a wake-up turn.
func WakeupPrompt(w Wakeup, loc *time.Location) string {
	at := w.At.In(loc).Format(headerTime)
	if w.Note == DefaultNote {
		return fmt.Sprintf("[wake-up] Set for %s by default: no wake-up was requested.", at)
	}
	return fmt.Sprintf("[wake-up] Set for %s. Note: %s", at, w.Note)
}

func formatDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

// Outbox is what the agent left in OutboxFile.
type Outbox struct {
	// Found: the file was there, and must be removed once handled.
	Found bool
	// Request is the valid request, if there was one.
	Request *Wakeup
	// Rejected is why the file was refused, if it was.
	Rejected string
}

// ReadOutbox reads the sandbox's OutboxFile, if there is one, and checks it.
// It leaves the file in place: ClearOutbox removes it once the request has
// been handed on.
func (a *Activities) ReadOutbox(ctx context.Context, in SandboxRef) (Outbox, error) {
	if err := in.validate(); err != nil {
		return Outbox{}, err
	}
	script := fmt.Sprintf(`[ -e %[1]s ] || exit 10
exec head -c %[2]d %[1]s`, OutboxFile, MaxOutboxBytes+1)
	res, err := a.Gateway.Exec(ctx, in.Sandbox, []string{"sh", "-c", script})
	if err != nil {
		return Outbox{}, err
	}
	switch res.ExitCode {
	case 0:
	case 10:
		return Outbox{}, nil
	default:
		return Outbox{}, fmt.Errorf("reading %s in %s: exit %d: %s", OutboxFile, in.Sandbox, res.ExitCode, tail(res.Stderr, 2000))
	}
	w, rejected := ParseOutbox(res.Stdout, a.now())
	return Outbox{Found: true, Request: w, Rejected: rejected}, nil
}

// ClearOutbox removes the sandbox's OutboxFile.
func (a *Activities) ClearOutbox(ctx context.Context, in SandboxRef) error {
	if err := in.validate(); err != nil {
		return err
	}
	res, err := a.Gateway.Exec(ctx, in.Sandbox, []string{"rm", "-f", OutboxFile})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("removing %s in %s: exit %d: %s", OutboxFile, in.Sandbox, res.ExitCode, tail(res.Stderr, 2000))
	}
	return nil
}

// ParseOutbox checks a wake-up request against now. It returns the request,
// or why it is refused, in words for the agent.
func ParseOutbox(data []byte, now time.Time) (*Wakeup, string) {
	if len(data) > MaxOutboxBytes {
		return nil, fmt.Sprintf("the file is over %d bytes", MaxOutboxBytes)
	}
	var req struct {
		At   *string `json:"at"`
		Note string  `json:"note"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Sprintf(`want {"at": "<RFC 3339 time>", "note": "<text>"}: %v`, err)
	}
	if req.At == nil {
		return nil, `"at" is missing`
	}
	at, err := time.Parse(time.RFC3339, *req.At)
	if err != nil {
		return nil, fmt.Sprintf(`"at" is not an RFC 3339 time (like 2026-10-11T21:00:00-07:00): %q`, *req.At)
	}
	if lead := at.Sub(now); lead < MinWakeupLead || lead > MaxWakeupLead {
		return nil, fmt.Sprintf(`"at" is %s from now; it must be %s to %s ahead`,
			lead.Round(time.Minute), formatDuration(MinWakeupLead), formatDuration(MaxWakeupLead))
	}
	if n := utf8.RuneCountInString(req.Note); n > MaxNoteChars {
		return nil, fmt.Sprintf(`"note" is %d characters, over %d`, n, MaxNoteChars)
	}
	return &Wakeup{At: at, Note: req.Note}, ""
}
