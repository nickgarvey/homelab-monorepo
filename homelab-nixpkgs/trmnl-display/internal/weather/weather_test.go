package weather

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUVLabel(t *testing.T) {
	cases := []struct {
		uv   float64
		want string
	}{
		{0, "Low"},
		{2.9, "Low"},
		{3, "Moderate"},
		{5.9, "Moderate"},
		{6, "High"},
		{7.9, "High"},
		{8, "Very High"},
		{10.9, "Very High"},
		{11, "Extreme"},
		{15, "Extreme"},
	}
	for _, c := range cases {
		got := UVLabel(c.uv)
		if got != c.want {
			t.Errorf("UVLabel(%.1f) = %q, want %q", c.uv, got, c.want)
		}
	}
}

func TestCondition(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{0, "Clear"},
		{1, "Partly Cloudy"},
		{2, "Partly Cloudy"},
		{3, "Overcast"},
		{45, "Foggy"},
		{51, "Drizzle"},
		{61, "Rain"},
		{71, "Snow"},
		{81, "Showers"},
		{95, "Thunderstorm"},
	}
	for _, c := range cases {
		got := Condition(c.code)
		if got != c.want {
			t.Errorf("Condition(%d) = %q, want %q", c.code, got, c.want)
		}
	}
}

const mockWeatherResponse = `{
  "timezone": "America/Los_Angeles",
  "daily": {
    "time": ["2026-02-24","2026-02-25","2026-02-26"],
    "temperature_2m_max": [72.5, 74.1, 68.0],
    "temperature_2m_min": [55.1, 56.0, 52.3],
    "weathercode": [0, 1, 61],
    "uv_index_max": [8.2, 7.5, 3.1],
    "precipitation_probability_max": [5, 10, 80]
  },
  "hourly": {
    "time": [
      "2026-02-24T00:00","2026-02-24T01:00","2026-02-24T02:00",
      "2026-02-24T03:00","2026-02-24T04:00","2026-02-24T05:00",
      "2026-02-24T06:00","2026-02-24T07:00","2026-02-24T08:00",
      "2026-02-24T09:00","2026-02-24T10:00","2026-02-24T11:00",
      "2026-02-24T12:00","2026-02-24T13:00","2026-02-24T14:00",
      "2026-02-24T15:00","2026-02-24T16:00","2026-02-24T17:00",
      "2026-02-24T18:00","2026-02-24T19:00","2026-02-24T20:00",
      "2026-02-24T21:00","2026-02-24T22:00","2026-02-24T23:00"
    ],
    "temperature_2m": [
      58,57,56,55,55,54,54,55,58,61,65,68,70,71,72,72,71,69,67,65,63,62,61,60
    ],
    "precipitation_probability": [
      0,0,0,0,0,0,0,0,0,0,0,5,5,10,10,10,5,5,0,0,0,0,0,0
    ],
    "uv_index": [
      0,0,0,0,0,0,0,0.1,0.5,2.1,4.3,6.1,7.5,8.2,8.5,7.9,6.2,4.0,1.5,0.2,0,0,0,0
    ]
  }
}`

func TestFetch_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockWeatherResponse))
	}))
	defer srv.Close()

	result, err := fetchFromURL(srv.Client(), srv.URL+"/v1/forecast", "Sunnyvale", true)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}

	if result.LocationName != "Sunnyvale" {
		t.Errorf("LocationName = %q", result.LocationName)
	}
	if result.Today.TempMax != 72.5 {
		t.Errorf("Today.TempMax = %.1f, want 72.5", result.Today.TempMax)
	}
	if result.Today.TempMin != 55.1 {
		t.Errorf("Today.TempMin = %.1f, want 55.1", result.Today.TempMin)
	}
	if result.Today.Condition != "Clear" {
		t.Errorf("Today.Condition = %q, want Clear", result.Today.Condition)
	}
	if result.Today.UVMax != 8.2 {
		t.Errorf("Today.UVMax = %.1f, want 8.2", result.Today.UVMax)
	}
	if result.Today.UVLabel != "Very High" {
		t.Errorf("Today.UVLabel = %q, want Very High", result.Today.UVLabel)
	}
	if result.Today.PrecipProbMax != 5 {
		t.Errorf("Today.PrecipProbMax = %d, want 5", result.Today.PrecipProbMax)
	}
	if len(result.Daily) != 3 {
		t.Errorf("len(Daily) = %d, want 3", len(result.Daily))
	}
	if result.Daily[2].Condition != "Rain" {
		t.Errorf("Daily[2].Condition = %q, want Rain", result.Daily[2].Condition)
	}
	if result.Daily[2].PrecipProbMax != 80 {
		t.Errorf("Daily[2].PrecipProbMax = %d, want 80", result.Daily[2].PrecipProbMax)
	}
	if !result.ShowUV {
		t.Error("ShowUV should be true")
	}
}

func TestFetch_hourlyParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockWeatherResponse))
	}))
	defer srv.Close()

	result, err := fetchFromURL(srv.Client(), srv.URL+"/v1/forecast", "Test", false)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}

	// Up to 24 hourly points (some may be filtered if in the past)
	if len(result.Hourly) > 24 {
		t.Errorf("len(Hourly) = %d, want <= 24", len(result.Hourly))
	}
	for _, h := range result.Hourly {
		if h.TimeLabel == "" {
			t.Error("hourly TimeLabel should not be empty")
		}
		if h.ShortLabel == "" {
			t.Error("hourly ShortLabel should not be empty")
		}
		if h.PrecipProb < 0 || h.PrecipProb > 100 {
			t.Errorf("hourly PrecipProb = %d, want 0-100", h.PrecipProb)
		}
	}
}

func TestParseHourly_filtersToFuture(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")

	// 20 past hours + 24 future hours
	now := time.Now().In(loc)
	h := hourlyResponse{}
	for i := -20; i < 24; i++ {
		ts := now.Add(time.Duration(i) * time.Hour).Truncate(time.Hour)
		h.Time = append(h.Time, ts.Format("2006-01-02T15:04"))
		h.TempF = append(h.TempF, 70+float64(i))
		h.UVIndex = append(h.UVIndex, 2.0)
		h.PrecipProb = append(h.PrecipProb, 10)
	}

	points := parseHourly(h, loc, 24)
	if len(points) > 24 {
		t.Errorf("expected <= 24 future points, got %d", len(points))
	}
	if len(points) == 0 {
		t.Error("expected at least 1 future hourly point")
	}
	// Verify ShortLabel is set
	for _, p := range points {
		if p.ShortLabel == "" {
			t.Error("ShortLabel should not be empty")
		}
	}
}

func TestShortHourLabel(t *testing.T) {
	loc := time.UTC
	cases := []struct{ hour int; want string }{
		{0, "12A"}, {1, "1A"}, {11, "11A"},
		{12, "12P"}, {13, "1P"}, {23, "11P"},
	}
	for _, c := range cases {
		t := time.Date(2026, 1, 1, c.hour, 0, 0, 0, loc)
		got := shortHourLabel(t)
		if got != c.want {
			_ = got // suppress unused warning in loop shadow
		}
	}
	// Re-run with assertion
	for _, c := range cases {
		tm := time.Date(2026, 1, 1, c.hour, 0, 0, 0, loc)
		got := shortHourLabel(tm)
		if got != c.want {
			t.Errorf("shortHourLabel(hour=%d) = %q, want %q", c.hour, got, c.want)
		}
	}
}

func TestFetch_non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := fetchFromURL(srv.Client(), srv.URL+"/v1/forecast", "Test", false)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestFetch_emptyDaily(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"daily":{"time":[]}}`))
	}))
	defer srv.Close()

	_, err := fetchFromURL(srv.Client(), srv.URL+"/v1/forecast", "Test", false)
	if err == nil {
		t.Error("expected error for empty daily data")
	}
}
