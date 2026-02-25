package display

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ngarvey/trmnl-display/internal/gcal"
	"github.com/ngarvey/trmnl-display/internal/homeassistant"
	"github.com/ngarvey/trmnl-display/internal/weather"
)

func makeWeatherResult(name string, showUV bool) *weather.Result {
	daily := make([]weather.DayForecast, 3)
	for i := range daily {
		t := time.Date(2026, 2, 24+i, 0, 0, 0, 0, time.UTC)
		daily[i] = weather.DayForecast{
			Date:          t.Format("2006-01-02"),
			DayAbbr:       t.Format("Mon"),
			TempMax:       float64(72 + i),
			TempMin:       float64(55 + i),
			Condition:     "Clear",
			UVMax:         8.2,
			UVLabel:       "Very High",
			PrecipProbMax: 10 + i*5,
		}
	}
	hourly := make([]weather.HourlyPoint, 24)
	for i := range hourly {
		h := (15 + i) % 24
		sf := "P"
		if h < 12 {
			sf = "A"
		}
		hourly[i] = weather.HourlyPoint{
			TimeLabel:  fmt.Sprintf("%d %s", (h%12)+1, sf),
			ShortLabel: fmt.Sprintf("%d%s", (h%12)+1, sf),
			TempF:      float64(72 - i),
			UVIndex:    float64(8-i) / 2,
			PrecipProb: (i * 3) % 40,
		}
	}
	return &weather.Result{
		LocationName: name,
		Today:        daily[0],
		Daily:        daily,
		Hourly:       hourly,
		ShowUV:       showUV,
		FetchedAt:    time.Now(),
	}
}

func renderTest(t *testing.T, weatherResults []*weather.Result, cal *gcal.Result, calErr string, garage *homeassistant.GarageState, garageErr string) string {
	t.Helper()
	html, err := RenderHTML(
		time.Date(2026, 2, 24, 14, 32, 0, 0, time.UTC),
		weatherResults, cal, calErr, garage, garageErr, "Test news text.",
	)
	if err != nil {
		t.Fatalf("RenderHTML error: %v", err)
	}
	return html
}

func TestRenderHTML_containsExpectedFields(t *testing.T) {
	results := []*weather.Result{
		makeWeatherResult("Sunnyvale", true),
		makeWeatherResult("San Francisco", false),
	}
	cal := &gcal.Result{
		Events:    []gcal.Event{{Title: "Morning Standup", TimeLabel: "9:00 AM"}},
		FetchedAt: time.Now(),
	}
	garage := &homeassistant.GarageState{State: "closed", FetchedAt: time.Now()}

	html := renderTest(t, results, cal, "", garage, "")

	checks := []struct {
		label   string
		content string
	}{
		{"Sunnyvale location", "Sunnyvale"},
		{"San Francisco location", "San Francisco"},
		{"sparkline SVG", `<path d=`},
		{"charts container", "wp-charts"},
		{"condition icon ref", "wi-clear"},
		{"3-day forecast", "% rain"},
		{"calendar event", "Morning Standup"},
		{"garage state", "CLOSED"},
		{"updated timestamp", "Feb 24"},
		{"news text", "Test news"},
		{"weather icon defs", "wi-thunder"},
	}

	for _, c := range checks {
		if !strings.Contains(html, c.content) {
			t.Errorf("HTML missing %s (looked for %q)", c.label, c.content)
		}
	}
}

func TestRenderHTML_bothLocationsStackedOrder(t *testing.T) {
	results := []*weather.Result{
		makeWeatherResult("Sunnyvale", true),
		makeWeatherResult("San Francisco", false),
	}
	html := renderTest(t, results, nil, "", nil, "")

	sunPos := strings.Index(html, "Sunnyvale")
	sfPos := strings.Index(html, "San Francisco")
	if sunPos < 0 || sfPos < 0 {
		t.Fatal("both locations should be present")
	}
	if sunPos >= sfPos {
		t.Error("Sunnyvale should appear before San Francisco (stacked top-to-bottom)")
	}
}

func TestRenderHTML_nilWeather_showsUnavailable(t *testing.T) {
	html := renderTest(t, []*weather.Result{nil, nil}, nil, "", nil, "")
	if strings.Count(html, "Unavailable") < 2 {
		t.Error("HTML should show 'Unavailable' twice for two nil weather results")
	}
}

func TestRenderHTML_calendarError(t *testing.T) {
	results := []*weather.Result{makeWeatherResult("A", false), makeWeatherResult("B", false)}
	html := renderTest(t, results, nil, "Calendar: auth error", nil, "")
	if !strings.Contains(html, "auth error") {
		t.Error("HTML should contain calendar error message")
	}
}

func TestRenderHTML_garageOpen(t *testing.T) {
	results := []*weather.Result{makeWeatherResult("A", false), makeWeatherResult("B", false)}
	garage := &homeassistant.GarageState{State: "open", FetchedAt: time.Now()}
	html := renderTest(t, results, nil, "", garage, "")
	if !strings.Contains(html, "OPEN") {
		t.Error("HTML should show OPEN for garage")
	}
}

func TestRenderHTML_staleWeather(t *testing.T) {
	old := makeWeatherResult("Sunnyvale", true)
	old.FetchedAt = time.Now().Add(-30 * time.Hour)
	html := renderTest(t, []*weather.Result{old, makeWeatherResult("SF", false)}, nil, "", nil, "")
	if !strings.Contains(html, "stale") {
		t.Error("HTML should show stale indicator for old data")
	}
}

func TestRenderHTML_noCalendarEvents(t *testing.T) {
	results := []*weather.Result{makeWeatherResult("A", false), makeWeatherResult("B", false)}
	cal := &gcal.Result{Events: nil, FetchedAt: time.Now()}
	html := renderTest(t, results, cal, "", nil, "")
	if !strings.Contains(html, "No upcoming events") {
		t.Error("HTML should show 'No upcoming events'")
	}
}

func TestRenderHTML_sparklinePresent(t *testing.T) {
	results := []*weather.Result{makeWeatherResult("Sunnyvale", true)}
	html := renderTest(t, results, nil, "", nil, "")
	if !strings.Contains(html, `<path d=`) {
		t.Error("HTML should contain sparkline bezier path element")
	}
	if !strings.Contains(html, "chart-row") {
		t.Error("HTML should contain chart rows")
	}
}

func TestFilterEvery3Hours(t *testing.T) {
	hourly := make([]weather.HourlyPoint, 24)
	for i := range hourly {
		hourly[i] = weather.HourlyPoint{ShortLabel: fmt.Sprintf("%d", i)}
	}
	out := filterEvery3Hours(hourly)
	if len(out) != 8 {
		t.Errorf("filterEvery3Hours: got %d points, want 8", len(out))
	}
	if out[0].ShortLabel != "0" || out[1].ShortLabel != "3" {
		t.Errorf("wrong points selected: got [0]=%s [1]=%s", out[0].ShortLabel, out[1].ShortLabel)
	}
}

func TestBuildSparklineSVG_empty(t *testing.T) {
	result := buildSparklineSVG(nil)
	if result != "" {
		t.Error("empty input should return empty string")
	}
}

func TestBuildSparklineSVG_produces_bezier(t *testing.T) {
	hourly := make([]weather.HourlyPoint, 8)
	for i := range hourly {
		hourly[i] = weather.HourlyPoint{TempF: float64(60 + i), ShortLabel: fmt.Sprintf("%dA", i)}
	}
	svg := string(buildSparklineSVG(hourly))
	if !strings.Contains(svg, `<path d=`) {
		t.Error("sparkline SVG should contain a bezier path")
	}
	if strings.Contains(svg, "<circle") {
		t.Error("sparkline SVG should not contain a dot")
	}
}

// TestPrecipIconID_allWMOCodes tests every real WMO code and key boundaries.
func TestPrecipIconID_allWMOCodes(t *testing.T) {
	cases := []struct {
		code int
		want string
		desc string
	}{
		// Clear / partly cloudy
		{0, "wi-clear", "clear sky"},
		{1, "wi-clear", "mainly clear"},
		{2, "wi-partly", "partly cloudy"},
		// Overcast — boundary: 2→partly, 3→cloud, 4→fog range
		{3, "wi-cloud", "overcast"},
		{4, "wi-fog", "undefined — falls into fog range (≤48)"},
		// Fog
		{45, "wi-fog", "fog"},
		{48, "wi-fog", "rime fog — upper boundary of fog range"},
		// Drizzle — boundary: 49 enters drizzle range, 57 is last drizzle
		{49, "wi-showers", "undefined — falls into drizzle range (≤57)"},
		{51, "wi-showers", "light drizzle"},
		{53, "wi-showers", "moderate drizzle"},
		{55, "wi-showers", "dense drizzle"},
		{56, "wi-showers", "light freezing drizzle"},
		{57, "wi-showers", "heavy freezing drizzle — upper boundary"},
		// Light/moderate rain — boundary: 58 enters this range, 63 is last
		{58, "wi-showers", "undefined — falls into light rain range (≤63)"},
		{61, "wi-showers", "slight rain"},
		{63, "wi-showers", "moderate rain — upper boundary"},
		// Heavy/freezing rain — boundary: 64 enters, 67 is last
		{64, "wi-rain", "undefined — falls into heavy rain range (≤67)"},
		{65, "wi-rain", "heavy rain"},
		{66, "wi-rain", "light freezing rain"},
		{67, "wi-rain", "heavy freezing rain — upper boundary"},
		// Snow — boundary: 68 enters, 77 is last
		{71, "wi-snow", "slight snowfall"},
		{73, "wi-snow", "moderate snowfall"},
		{75, "wi-snow", "heavy snowfall"},
		{77, "wi-snow", "snow grains — upper boundary"},
		// Rain showers — boundary: 78 enters, 81 is last
		{78, "wi-showers", "undefined — falls into rain showers range (≤81)"},
		{80, "wi-showers", "slight rain showers"},
		{81, "wi-showers", "moderate rain showers — upper boundary"},
		// Violent showers — exact code 82
		{82, "wi-rain", "violent rain showers"},
		// Snow showers — boundary: 83 enters, 86 is last
		{83, "wi-snow", "undefined — falls into snow showers range (≤86)"},
		{85, "wi-snow", "slight snow showers"},
		{86, "wi-snow", "heavy snow showers — upper boundary"},
		// Gap 87–94 → default
		{87, "wi-cloud", "undefined — falls to default"},
		{94, "wi-cloud", "undefined — just below thunderstorm range"},
		// Thunderstorm
		{95, "wi-thunder", "thunderstorm"},
		{96, "wi-thunder", "thunderstorm with slight hail"},
		{99, "wi-thunder", "thunderstorm with heavy hail"},
	}
	for _, c := range cases {
		got := precipIconID(c.code)
		if got != c.want {
			t.Errorf("precipIconID(%d) [%s] = %q, want %q", c.code, c.desc, got, c.want)
		}
	}
}

// TestPrecipIconID_neverReturnsEmpty verifies every valid WMO code produces an icon.
func TestPrecipIconID_neverReturnsEmpty(t *testing.T) {
	validCodes := []int{0, 1, 2, 3, 45, 48, 51, 53, 55, 56, 57, 61, 63, 65, 66, 67, 71, 73, 75, 77, 80, 81, 82, 85, 86, 95, 96, 99}
	for _, code := range validCodes {
		got := precipIconID(code)
		if got == "" {
			t.Errorf("precipIconID(%d) returned empty string — every code should produce an icon", code)
		}
	}
}

func TestBuildConditionIconsSVG_everySlotHasIcon(t *testing.T) {
	// One of every distinct WMO group to cover all icon paths.
	hourly := []weather.HourlyPoint{
		{ShortLabel: "6A", WeatherCode: 0},  // wi-clear
		{ShortLabel: "9A", WeatherCode: 2},  // wi-partly
		{ShortLabel: "12P", WeatherCode: 3}, // wi-cloud
		{ShortLabel: "3P", WeatherCode: 45}, // wi-fog
		{ShortLabel: "6P", WeatherCode: 61}, // wi-showers
		{ShortLabel: "9P", WeatherCode: 65}, // wi-rain
		{ShortLabel: "12A", WeatherCode: 71}, // wi-snow
		{ShortLabel: "3A", WeatherCode: 95}, // wi-thunder
	}
	svg := string(buildConditionIconsSVG(hourly))

	// Every slot must emit a <use href= element.
	useCount := strings.Count(svg, "<use href=")
	if useCount != len(hourly) {
		t.Errorf("expected %d <use href= elements (one per slot), got %d\nSVG:\n%s", len(hourly), useCount, svg)
	}

	// Spot-check specific icon IDs are present.
	for _, id := range []string{"wi-clear", "wi-partly", "wi-cloud", "wi-fog", "wi-showers", "wi-rain", "wi-snow", "wi-thunder"} {
		if !strings.Contains(svg, id) {
			t.Errorf("expected icon %q to appear in SVG", id)
		}
	}

	// All time labels must be present.
	for _, label := range []string{"6A", "9A", "12P", "3P", "6P", "9P", "12A", "3A"} {
		if !strings.Contains(svg, label) {
			t.Errorf("expected time label %q in SVG", label)
		}
	}
}

func TestBuildConditionIconsSVG_iconIDsMatchCodes(t *testing.T) {
	// Verify the icon ID embedded in the SVG matches the expected mapping per slot.
	hourly := []weather.HourlyPoint{
		{ShortLabel: "1P", WeatherCode: 1},  // wi-clear
		{ShortLabel: "4P", WeatherCode: 3},  // wi-cloud (overcast — was previously empty, now shows icon)
		{ShortLabel: "7P", WeatherCode: 95}, // wi-thunder
	}
	svg := string(buildConditionIconsSVG(hourly))
	for _, want := range []string{"wi-clear", "wi-cloud", "wi-thunder"} {
		if !strings.Contains(svg, `href="#`+want+`"`) {
			t.Errorf("expected href=%q in SVG\nSVG:\n%s", want, svg)
		}
	}
}

func TestConditionIconID(t *testing.T) {
	cases := map[string]string{
		"Clear":         "wi-clear",
		"Partly Cloudy": "wi-partly",
		"Overcast":      "wi-cloud",
		"Foggy":         "wi-fog",
		"Drizzle":       "wi-showers",
		"Showers":       "wi-showers",
		"Rain":          "wi-rain",
		"Snow":          "wi-snow",
		"Thunderstorm":  "wi-thunder",
		"Unknown":       "wi-cloud",
	}
	for cond, want := range cases {
		got := conditionIconID(cond)
		if got != want {
			t.Errorf("conditionIconID(%q) = %q, want %q", cond, got, want)
		}
	}
}

func TestBuildWeather_nilResult(t *testing.T) {
	d := buildWeather(nil)
	if d.LocationName != "Unavailable" {
		t.Errorf("nil buildWeather LocationName = %q", d.LocationName)
	}
}
