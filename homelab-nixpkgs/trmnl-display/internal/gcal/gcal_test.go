package gcal

import (
	"testing"
	"time"

	googlecalendar "google.golang.org/api/calendar/v3"
)

func makeEvent(summary, dateTime, date string) *googlecalendar.Event {
	return makeEventWithRecurring(summary, dateTime, date, "")
}

func makeEventWithRecurring(summary, dateTime, date, recurringEventId string) *googlecalendar.Event {
	e := &googlecalendar.Event{
		Summary:          summary,
		RecurringEventId: recurringEventId,
	}
	e.Start = &googlecalendar.EventDateTime{}
	if dateTime != "" {
		e.Start.DateTime = dateTime
	} else {
		e.Start.Date = date
	}
	return e
}

var testNow = time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)

func TestParseEvents_timedEvent(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	items := []*googlecalendar.Event{
		makeEvent("Team Standup", "2026-02-24T09:00:00-08:00", ""),
	}
	events := ParseEvents(items, loc, time.Now())
	if len(events) != 1 {
		t.Fatalf("len = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Title != "Team Standup" {
		t.Errorf("Title = %q", ev.Title)
	}
	if ev.AllDay {
		t.Error("AllDay should be false")
	}
	if ev.TimeLabel != "9:00 AM" {
		t.Errorf("TimeLabel = %q, want 9:00 AM", ev.TimeLabel)
	}
}

func TestParseEvents_allDay(t *testing.T) {
	items := []*googlecalendar.Event{
		makeEvent("Holiday", "", "2026-02-24"),
	}
	events := ParseEvents(items, time.UTC, testNow)
	if len(events) != 1 {
		t.Fatalf("len = %d, want 1", len(events))
	}
	ev := events[0]
	if !ev.AllDay {
		t.Error("AllDay should be true")
	}
	if ev.TimeLabel != "All day" {
		t.Errorf("TimeLabel = %q, want All day", ev.TimeLabel)
	}
}

func TestParseEvents_skips_cancelled(t *testing.T) {
	items := []*googlecalendar.Event{
		{Summary: "Cancelled Meeting", Status: "cancelled", Start: &googlecalendar.EventDateTime{DateTime: "2026-02-24T10:00:00Z"}},
		makeEvent("Real Meeting", "2026-02-24T11:00:00Z", ""),
	}
	events := ParseEvents(items, time.UTC, testNow)
	if len(events) != 1 {
		t.Fatalf("len = %d, want 1 (cancelled should be skipped)", len(events))
	}
	if events[0].Title != "Real Meeting" {
		t.Errorf("Title = %q", events[0].Title)
	}
}

func TestParseEvents_empty(t *testing.T) {
	events := ParseEvents(nil, time.UTC, testNow)
	if len(events) != 0 {
		t.Errorf("expected empty slice, got %d", len(events))
	}
}

func TestParseEvents_multiple(t *testing.T) {
	items := []*googlecalendar.Event{
		makeEvent("Morning Sync", "2026-02-24T08:30:00-08:00", ""),
		makeEvent("Lunch", "2026-02-24T12:00:00-08:00", ""),
		makeEvent("All Hands", "", "2026-02-24"),
	}
	loc, _ := time.LoadLocation("America/Los_Angeles")
	events := ParseEvents(items, loc, time.Now())
	if len(events) != 3 {
		t.Fatalf("len = %d, want 3", len(events))
	}
	if events[0].TimeLabel != "8:30 AM" {
		t.Errorf("events[0].TimeLabel = %q", events[0].TimeLabel)
	}
	if events[1].TimeLabel != "12:00 PM" {
		t.Errorf("events[1].TimeLabel = %q", events[1].TimeLabel)
	}
	if !events[2].AllDay {
		t.Error("events[2] should be all-day")
	}
}

func TestParseEvents_isRecurring_true(t *testing.T) {
	items := []*googlecalendar.Event{
		makeEventWithRecurring("Weekly Standup", "2026-02-24T09:00:00Z", "", "abc123_recurring"),
	}
	events := ParseEvents(items, time.UTC, testNow)
	if len(events) != 1 {
		t.Fatalf("len = %d, want 1", len(events))
	}
	if !events[0].IsRecurring {
		t.Error("IsRecurring should be true when RecurringEventId is set")
	}
}

func TestParseEvents_isRecurring_false(t *testing.T) {
	items := []*googlecalendar.Event{
		makeEvent("Doctor Appointment", "2026-02-25T14:00:00Z", ""),
	}
	events := ParseEvents(items, time.UTC, testNow)
	if len(events) != 1 {
		t.Fatalf("len = %d, want 1", len(events))
	}
	if events[0].IsRecurring {
		t.Error("IsRecurring should be false when RecurringEventId is empty")
	}
}

func TestRelativeDayLabel_today(t *testing.T) {
	now := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	ev := time.Date(2026, 2, 24, 15, 0, 0, 0, time.UTC)
	got := RelativeDayLabel(ev, now)
	if got != "Today" {
		t.Errorf("got %q, want Today", got)
	}
}

func TestRelativeDayLabel_tomorrow(t *testing.T) {
	now := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	ev := time.Date(2026, 2, 25, 9, 0, 0, 0, time.UTC)
	got := RelativeDayLabel(ev, now)
	if got != "Tomorrow" {
		t.Errorf("got %q, want Tomorrow", got)
	}
}

func TestRelativeDayLabel_future(t *testing.T) {
	now := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	ev := time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)
	got := RelativeDayLabel(ev, now)
	if got != "Thu Mar 5" {
		t.Errorf("got %q, want Thu Mar 5", got)
	}
}

func TestRelativeDayLabel_allDayAtMidnight(t *testing.T) {
	now := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	ev := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	got := RelativeDayLabel(ev, now)
	if got != "Today" {
		t.Errorf("got %q, want Today", got)
	}
}
