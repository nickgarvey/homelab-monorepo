package display

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/ngarvey/trmnl-display/internal/gcal"
	"github.com/ngarvey/trmnl-display/internal/homeassistant"
	"github.com/ngarvey/trmnl-display/internal/weather"
)

//go:embed templates/display.html
var htmlTemplate string

// WeatherDisplay is a pre-processed weather result ready for the template.
type WeatherDisplay struct {
	LocationName    string
	Today           weather.DayForecast
	CurrentTemp     float64               // actual current temp from first hourly point
	CurrentUV       float64               // actual current UV from first hourly point
	ConditionIconID string
	Daily           []weather.DayForecast // 3 days
	HourlyEvery3    []weather.HourlyPoint // 8 points (every 3h) — shared x-axis
	TempSparkline   template.HTML         // inline SVG — top chart
	UVText          template.HTML         // inline SVG — middle chart (integer values)
	ConditionIcons  template.HTML         // inline SVG — bottom chart (has time labels)
	Stale           bool
	StaleMsg        string
}

// CalendarDisplay holds calendar data for the template.
type CalendarDisplay struct {
	Events   []gcal.Event
	ErrorMsg string
	Stale    bool
	StaleMsg string
}

// GarageDisplay holds garage state for the template.
type GarageDisplay struct {
	State    string
	Stale    bool
	StaleMsg string
}

// TemplateData is the root context passed to the HTML template.
type TemplateData struct {
	UpdatedAt string
	Locations []WeatherDisplay
	Calendar  CalendarDisplay
	Garage    GarageDisplay
	NewsText  string
}

func staleInfo(fetchedAt time.Time) (bool, string) {
	if fetchedAt.IsZero() {
		return false, ""
	}
	age := time.Since(fetchedAt)
	if age > 24*time.Hour {
		return true, fmt.Sprintf("(stale %dh ago)", int(age.Hours()))
	}
	return false, ""
}

func buildWeather(r *weather.Result) WeatherDisplay {
	if r == nil {
		return WeatherDisplay{
			LocationName: "Unavailable",
			Today:        weather.DayForecast{Condition: "Weather unavailable"},
		}
	}
	stale, msg := staleInfo(r.FetchedAt)
	every3 := filterEvery3Hours(r.Hourly)

	// Pull current values from the actual current hour (1h granularity);
	// fall back to daily high if hourly data is unavailable.
	currentTemp := r.Today.TempMax
	currentUV := r.Today.UVMax
	if len(r.Hourly) > 0 {
		currentTemp = r.Hourly[0].TempF
		currentUV = r.Hourly[0].UVIndex
	}

	return WeatherDisplay{
		LocationName:    r.LocationName,
		Today:           r.Today,
		CurrentTemp:     currentTemp,
		CurrentUV:       currentUV,
		ConditionIconID: conditionIconID(r.Today.Condition),
		Daily:           r.Daily,
		HourlyEvery3:    every3,
		TempSparkline:   buildSparklineSVG(every3),
		UVText:          buildUVTextSVG(every3),
		ConditionIcons:  buildConditionIconsSVG(every3),
		Stale:           stale,
		StaleMsg:        msg,
	}
}

// RenderHTML builds the display HTML from fetched data.
func RenderHTML(
	renderedAt time.Time,
	weatherResults []*weather.Result,
	cal *gcal.Result,
	calErr string,
	garage *homeassistant.GarageState,
	garageErr string,
	newsText string,
) (string, error) {
	// Format the timestamp in the first location's timezone; fall back to UTC.
	displayLoc := time.UTC
	if len(weatherResults) > 0 && weatherResults[0] != nil && weatherResults[0].Timezone != "" {
		if l, err := time.LoadLocation(weatherResults[0].Timezone); err == nil {
			displayLoc = l
		}
	}
	data := TemplateData{
		UpdatedAt: renderedAt.In(displayLoc).Format("Jan 2 3:04 PM"),
		NewsText:  newsText,
	}

	for _, r := range weatherResults {
		data.Locations = append(data.Locations, buildWeather(r))
	}

	if cal != nil {
		stale, msg := staleInfo(cal.FetchedAt)
		data.Calendar = CalendarDisplay{
			Events:   reformatEventsForTZ(cal.Events, displayLoc, renderedAt),
			Stale:    stale,
			StaleMsg: msg,
		}
	} else {
		errMsg := calErr
		if errMsg == "" {
			errMsg = "Calendar unavailable"
		}
		data.Calendar = CalendarDisplay{ErrorMsg: errMsg}
	}

	if garage != nil {
		stale, msg := staleInfo(garage.FetchedAt)
		data.Garage = GarageDisplay{
			State:    garage.State,
			Stale:    stale,
			StaleMsg: msg,
		}
	} else {
		data.Garage = GarageDisplay{State: "unknown", StaleMsg: garageErr}
	}

	funcMap := template.FuncMap{
		"upper": strings.ToUpper,
		"noEvents": func(events []gcal.Event) bool {
			return len(events) == 0
		},
		"condIconID": conditionIconID,
	}

	tmpl, err := template.New("display").Funcs(funcMap).Parse(htmlTemplate)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

// reformatEventsForTZ converts event TimeLabel and DayLabel into the given timezone.
// Events carry a Start time.Time in whatever tz the gcal server used; we display
// them in the same timezone as the first weather location.
func reformatEventsForTZ(events []gcal.Event, loc *time.Location, now time.Time) []gcal.Event {
	if loc == nil {
		return events
	}
	nowInLoc := now.In(loc)
	out := make([]gcal.Event, len(events))
	for i, ev := range events {
		ev2 := ev
		if !ev.Start.IsZero() {
			local := ev.Start.In(loc)
			if ev.AllDay {
				ev2.TimeLabel = "All day"
			} else {
				ev2.TimeLabel = local.Format("3:04 PM")
			}
			ev2.DayLabel = gcal.RelativeDayLabel(local, nowInLoc)
		}
		out[i] = ev2
	}
	return out
}
