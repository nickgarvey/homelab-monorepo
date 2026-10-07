package gcal

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/oauth2/google"
	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// Event is a single calendar event for display.
type Event struct {
	Title       string
	TimeLabel   string // "9:00 AM", "All day", etc.
	DayLabel    string // "Today", "Tomorrow", "Mon Mar 3", etc.
	AllDay      bool
	IsRecurring bool // true if part of a recurring series
	Start       time.Time
}

// Result holds the next 14 days of calendar events.
type Result struct {
	Events    []Event
	FetchedAt time.Time
}

// ParseEvents converts a Google Calendar event list into display Events.
// now is used to compute relative day labels; pass time.Now().In(loc) in production.
// Exported so it can be tested independently.
func ParseEvents(items []*googlecalendar.Event, loc *time.Location, now time.Time) []Event {
	if loc == nil {
		loc = time.Local
	}
	out := make([]Event, 0, len(items))
	for _, e := range items {
		if e.Status == "cancelled" {
			continue
		}
		ev := Event{
			Title:       e.Summary,
			IsRecurring: e.RecurringEventId != "",
		}
		if e.Start.DateTime != "" {
			t, err := time.Parse(time.RFC3339, e.Start.DateTime)
			if err == nil {
				ev.Start = t.In(loc)
				ev.TimeLabel = ev.Start.Format("3:04 PM")
			} else {
				ev.TimeLabel = e.Start.DateTime
			}
		} else if e.Start.Date != "" {
			ev.AllDay = true
			ev.TimeLabel = "All day"
			t, _ := time.ParseInLocation("2006-01-02", e.Start.Date, loc)
			ev.Start = t
		}
		ev.DayLabel = RelativeDayLabel(ev.Start, now)
		out = append(out, ev)
	}
	return out
}

// RelativeDayLabel returns "Today", "Tomorrow", or "Mon Jan 2" for a given event time.
func RelativeDayLabel(t, now time.Time) string {
	tDay := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	nowDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch int(tDay.Sub(nowDay).Hours() / 24) {
	case 0:
		return "Today"
	case 1:
		return "Tomorrow"
	default:
		return t.Format("Mon Jan 2")
	}
}

// Fetch retrieves today's calendar events using a service account JSON key.
func Fetch(ctx context.Context, serviceAccountPath, calendarID string) (*Result, error) {
	data, err := os.ReadFile(serviceAccountPath)
	if err != nil {
		return nil, fmt.Errorf("read service account %s: %w", serviceAccountPath, err)
	}

	creds, err := google.CredentialsFromJSON(ctx, data, googlecalendar.CalendarReadonlyScope)
	if err != nil {
		return nil, fmt.Errorf("parse service account: %w", err)
	}

	svc, err := googlecalendar.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("create calendar service: %w", err)
	}

	return fetchFromService(ctx, svc, calendarID)
}

func fetchFromService(ctx context.Context, svc *googlecalendar.Service, calendarID string) (*Result, error) {
	now := time.Now()
	loc := now.Location()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	endWindow := startOfDay.Add(14 * 24 * time.Hour)

	events, err := svc.Events.List(calendarID).
		Context(ctx).
		TimeMin(startOfDay.Format(time.RFC3339)).
		TimeMax(endWindow.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		Do()
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	return &Result{
		Events:    ParseEvents(events.Items, loc, now),
		FetchedAt: time.Now(),
	}, nil
}
