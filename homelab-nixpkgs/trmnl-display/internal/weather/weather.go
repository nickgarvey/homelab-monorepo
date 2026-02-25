package weather

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type dailyResponse struct {
	Time              []string  `json:"time"`
	TempMax           []float64 `json:"temperature_2m_max"`
	TempMin           []float64 `json:"temperature_2m_min"`
	WeatherCode       []int     `json:"weathercode"`
	UVIndexMax        []float64 `json:"uv_index_max"`
	PrecipProbMax     []int     `json:"precipitation_probability_max"`
}

type hourlyResponse struct {
	Time          []string  `json:"time"`
	TempF         []float64 `json:"temperature_2m"`
	PrecipProb    []int     `json:"precipitation_probability"`
	UVIndex       []float64 `json:"uv_index"`
	Precipitation []float64 `json:"precipitation"`
	WeatherCode   []int     `json:"weather_code"`
}

type apiResponse struct {
	Timezone string         `json:"timezone"`
	Daily    dailyResponse  `json:"daily"`
	Hourly   hourlyResponse `json:"hourly"`
}

// HourlyPoint holds weather for one hour.
type HourlyPoint struct {
	TimeLabel     string // "3 PM"
	ShortLabel    string // "3P", "10A", "12A"
	TempF         float64
	UVIndex       float64
	PrecipProb    int     // 0–100
	Precipitation float64 // mm
	WeatherCode   int     // WMO weather code
}

// DayForecast holds weather data for a single day.
type DayForecast struct {
	Date          string // "2006-01-02"
	DayAbbr       string // "Mon", "Tue", etc.
	TempMax       float64
	TempMin       float64
	Condition     string
	UVMax         float64
	UVLabel       string
	PrecipProbMax int // 0–100
}

// Result is the parsed result for one location.
type Result struct {
	LocationName string
	Today        DayForecast
	Daily        []DayForecast  // 3 days starting with today
	Hourly       []HourlyPoint  // next 6 hours from now
	ShowUV       bool
	Timezone     string        // IANA timezone name from the API, e.g. "America/Los_Angeles"
	FetchedAt    time.Time
}

// UVLabel returns a WHO risk label for a UV index value.
func UVLabel(uv float64) string {
	switch {
	case uv < 3:
		return "Low"
	case uv < 6:
		return "Moderate"
	case uv < 8:
		return "High"
	case uv < 11:
		return "Very High"
	default:
		return "Extreme"
	}
}

// Condition maps a WMO weather code to a human-readable string.
func Condition(code int) string {
	switch {
	case code == 0:
		return "Clear"
	case code <= 2:
		return "Partly Cloudy"
	case code == 3:
		return "Overcast"
	case code <= 49:
		return "Foggy"
	case code <= 59:
		return "Drizzle"
	case code <= 69:
		return "Rain"
	case code <= 79:
		return "Snow"
	case code <= 82:
		return "Showers"
	case code <= 99:
		return "Thunderstorm"
	default:
		return "Unknown"
	}
}

// Fetch retrieves a weather forecast from the open-meteo API.
func Fetch(client *http.Client, lat, lon float64, name string, showUV bool) (*Result, error) {
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f"+
			"&hourly=temperature_2m,precipitation_probability,uv_index,precipitation,weather_code"+
			"&daily=temperature_2m_max,temperature_2m_min,weathercode,uv_index_max,precipitation_probability_max"+
			"&temperature_unit=fahrenheit&timezone=auto&forecast_days=4",
		lat, lon,
	)
	return fetchFromURL(client, url, name, showUV)
}

func fetchFromURL(client *http.Client, url, name string, showUV bool) (*Result, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch weather for %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather API returned %d for %s", resp.StatusCode, name)
	}

	var data apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode weather for %s: %w", name, err)
	}

	d := data.Daily
	if len(d.Time) == 0 {
		return nil, fmt.Errorf("no daily data in weather response for %s", name)
	}

	// Resolve timezone from API response for correct hourly time parsing.
	loc := time.Local
	if data.Timezone != "" {
		if l, err := time.LoadLocation(data.Timezone); err == nil {
			loc = l
		}
	}

	// Parse daily forecasts (up to 3 days).
	daily := make([]DayForecast, len(d.Time))
	for i := range d.Time {
		uv := safeFloat(d.UVIndexMax, i)
		precip := safeInt(d.PrecipProbMax, i)
		code := safeInt(d.WeatherCode, i)
		t, _ := time.Parse("2006-01-02", d.Time[i])
		daily[i] = DayForecast{
			Date:          d.Time[i],
			DayAbbr:       t.Format("Mon"),
			TempMax:       d.TempMax[i],
			TempMin:       d.TempMin[i],
			Condition:     Condition(code),
			UVMax:         uv,
			UVLabel:       UVLabel(uv),
			PrecipProbMax: precip,
		}
	}

	hourly := parseHourly(data.Hourly, loc, 24)

	// Daily holds the next 3 days (tomorrow through tomorrow+2), excluding today.
	nextDays := daily[1:]
	if len(nextDays) > 3 {
		nextDays = nextDays[:3]
	}

	return &Result{
		LocationName: name,
		Today:        daily[0],
		Daily:        nextDays,
		Hourly:       hourly,
		ShowUV:       showUV,
		Timezone:     data.Timezone,
		FetchedAt:    time.Now(),
	}, nil
}

// parseHourly extracts the next n hourly points starting from the current hour.
func parseHourly(h hourlyResponse, loc *time.Location, n int) []HourlyPoint {
	if len(h.Time) == 0 {
		return nil
	}
	nowHour := time.Now().In(loc).Truncate(time.Hour)

	var points []HourlyPoint
	for i, ts := range h.Time {
		t, err := time.ParseInLocation("2006-01-02T15:04", ts, loc)
		if err != nil {
			continue
		}
		if t.Before(nowHour) {
			continue
		}
		points = append(points, HourlyPoint{
			TimeLabel:     t.Format("3 PM"),
			ShortLabel:    shortHourLabel(t),
			TempF:         safeFloat(h.TempF, i),
			UVIndex:       safeFloat(h.UVIndex, i),
			PrecipProb:    safeInt(h.PrecipProb, i),
			Precipitation: safeFloat(h.Precipitation, i),
			WeatherCode:   safeInt(h.WeatherCode, i),
		})
		if len(points) >= n {
			break
		}
	}
	return points
}

// shortHourLabel returns a compact time label: "12A", "1A", "12P", "3P", "10P".
func shortHourLabel(t time.Time) string {
	h := t.Hour()
	switch {
	case h == 0:
		return "12A"
	case h < 12:
		return fmt.Sprintf("%dA", h)
	case h == 12:
		return "12P"
	default:
		return fmt.Sprintf("%dP", h-12)
	}
}

func safeFloat(s []float64, i int) float64 {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func safeInt(s []int, i int) int {
	if i < len(s) {
		return s[i]
	}
	return 0
}
