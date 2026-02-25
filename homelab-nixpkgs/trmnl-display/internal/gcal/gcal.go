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
	Title     string
	TimeLabel string // "9:00 AM", "All day", etc.
	AllDay    bool
	Start     time.Time
}

// Result holds today's calendar events.
type Result struct {
	Events    []Event
	FetchedAt time.Time
}

// ParseEvents converts a Google Calendar event list into display Events.
// Exported so it can be tested independently.
func ParseEvents(items []*googlecalendar.Event, loc *time.Location) []Event {
	if loc == nil {
		loc = time.Local
	}
	out := make([]Event, 0, len(items))
	for _, e := range items {
		if e.Status == "cancelled" {
			continue
		}
		ev := Event{Title: e.Summary}
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
		out = append(out, ev)
	}
	return out
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
	endOfDay := startOfDay.Add(24 * time.Hour)

	events, err := svc.Events.List(calendarID).
		Context(ctx).
		TimeMin(startOfDay.Format(time.RFC3339)).
		TimeMax(endOfDay.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		Do()
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	return &Result{
		Events:    ParseEvents(events.Items, loc),
		FetchedAt: time.Now(),
	}, nil
}
