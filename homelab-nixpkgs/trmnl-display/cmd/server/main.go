package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/ngarvey/trmnl-display/internal/config"
	"github.com/ngarvey/trmnl-display/internal/display"
	"github.com/ngarvey/trmnl-display/internal/gcal"
	"github.com/ngarvey/trmnl-display/internal/homeassistant"
	"github.com/ngarvey/trmnl-display/internal/llm"
	"github.com/ngarvey/trmnl-display/internal/renderer"
	"github.com/ngarvey/trmnl-display/internal/weather"
)

type apiSetupResponse struct {
	Status     int    `json:"status"`
	APIKey     string `json:"api_key"`
	FriendlyID string `json:"friendly_id"`
	ImageURL   string `json:"image_url"`
	Message    string `json:"message"`
}

type apiDisplayResponse struct {
	Filename        string `json:"filename"`
	FirmwareURL     string `json:"firmware_url"`
	FirmwareVersion string `json:"firmware_version"`
	ImageURL        string `json:"image_url"`
	ImageURLTimeout int    `json:"image_url_timeout"`
	RefreshRate     int    `json:"refresh_rate"`
	ResetFirmware   bool   `json:"reset_firmware"`
	SpecialFunction string `json:"special_function"`
	UpdateFirmware  bool   `json:"update_firmware"`
}

// pollIntervalSeconds returns how often the server should re-render.
// Daytime (08:00–21:00): every 25 min. Overnight: every 90 min.
func pollIntervalSeconds(t time.Time) int {
	h := t.Hour()
	if h >= 8 && h < 21 {
		return 25 * 60
	}
	return 90 * 60
}

// deviceRefreshRateSeconds tells the device how long to sleep before checking
// again. It targets 2 minutes after the next expected server render, so the
// device always wakes to fresh content. Clamped to a minimum of 60s.
func deviceRefreshRateSeconds(renderedAt, now time.Time) int {
	nextRender := renderedAt.Add(time.Duration(pollIntervalSeconds(renderedAt)) * time.Second)
	target := nextRender.Add(2 * time.Minute)
	secs := int(target.Sub(now).Seconds())
	if secs < 60 {
		return 60
	}
	return secs
}

// baseURL builds the scheme+host prefix from the incoming request.
// It respects X-Forwarded-Proto for reverse-proxy setups.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + r.Host
}


type dataCache struct {
	mu          sync.RWMutex
	png         []byte
	renderedAt  time.Time
	weather     []*weather.Result
	calendar    *gcal.Result
	calendarErr string
	garage      *homeassistant.GarageState
	garageErr   string
	llmText     string
	llmErr      string
}

type srv struct {
	cfg      *config.Config
	cache    *dataCache
	renderer *renderer.Client
	haClient *http.Client
}

func (s *srv) poll(ctx context.Context) {
	log.Println("poll: starting")

	var mu sync.Mutex
	weatherResults := make([]*weather.Result, len(s.cfg.Locations))
	var calResult *gcal.Result
	var calErr string
	var garageResult *homeassistant.GarageState
	var garageErr string
	var llmText string
	var llmErr string

	var wg sync.WaitGroup

	// Fetch weather for all locations concurrently.
	for i, loc := range s.cfg.Locations {
		i, loc := i, loc
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := weather.Fetch(
				&http.Client{Timeout: 10 * time.Second},
				loc.Lat, loc.Lon, loc.Name, loc.ShowUV,
			)
			if err != nil {
				log.Printf("poll: weather[%s]: %v", loc.Name, err)
				s.cache.mu.RLock()
				if i < len(s.cache.weather) {
					mu.Lock()
					weatherResults[i] = s.cache.weather[i]
					mu.Unlock()
				}
				s.cache.mu.RUnlock()
			} else {
				mu.Lock()
				weatherResults[i] = r
				mu.Unlock()
			}
		}()
	}

	// Fetch calendar.
	if s.cfg.GoogleCal.ServiceAccountJSON != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := gcal.Fetch(ctx, s.cfg.GoogleCal.ServiceAccountJSON, s.cfg.GoogleCal.CalendarID)
			if err != nil {
				log.Printf("poll: calendar: %v", err)
				s.cache.mu.RLock()
				mu.Lock()
				calResult = s.cache.calendar
				calErr = err.Error()
				mu.Unlock()
				s.cache.mu.RUnlock()
			} else {
				mu.Lock()
				calResult = r
				mu.Unlock()
			}
		}()
	} else {
		calErr = "Calendar: no service account configured"
	}

	// Fetch garage.
	wg.Add(1)
	go func() {
		defer wg.Done()
		r, err := homeassistant.FetchGarage(
			s.haClient,
			s.cfg.HomeAssistant.URL,
			s.cfg.HomeAssistant.Token,
			s.cfg.HomeAssistant.GarageEntity,
		)
		if err != nil {
			log.Printf("poll: garage: %v", err)
			s.cache.mu.RLock()
			mu.Lock()
			garageResult = s.cache.garage
			garageErr = err.Error()
			mu.Unlock()
			s.cache.mu.RUnlock()
		} else {
			mu.Lock()
			garageResult = r
			mu.Unlock()
		}
	}()

	// Fetch word of the day (only if LLM is configured and no static news_text override).
	if s.cfg.LLM.BaseURL != "" && s.cfg.NewsText == "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			categories := s.cfg.LLM.WordOfDayCategories
			category := categories[rand.Intn(len(categories))]
			llmCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			r, err := llm.Fetch(llmCtx, &http.Client{Timeout: 60 * time.Second}, s.cfg.LLM.BaseURL, s.cfg.LLM.Model, category)
			if err != nil {
				log.Printf("poll: llm (category=%q): %v", category, err)
				s.cache.mu.RLock()
				mu.Lock()
				llmText = s.cache.llmText
				llmErr = "Word of the day unavailable."
				mu.Unlock()
				s.cache.mu.RUnlock()
			} else {
				log.Printf("poll: llm: category=%q → %q", category, r.Text)
				mu.Lock()
				llmText = r.Text
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	newsText := s.cfg.NewsText
	if newsText == "" {
		if llmErr != "" && llmText == "" {
			newsText = llmErr
		} else {
			newsText = llmText
		}
	}

	now := time.Now()
	html, err := display.RenderHTML(now, weatherResults, calResult, calErr, garageResult, garageErr, newsText)
	if err != nil {
		log.Printf("poll: display render: %v", err)
		return
	}

	png, err := s.renderer.Render(html, 800, 480)
	if err != nil {
		log.Printf("poll: playwright render: %v — keeping previous PNG", err)
		return
	}

	s.cache.mu.Lock()
	s.cache.png = png
	s.cache.renderedAt = now
	s.cache.weather = weatherResults
	s.cache.calendar = calResult
	s.cache.calendarErr = calErr
	s.cache.garage = garageResult
	s.cache.garageErr = garageErr
	if llmText != "" {
		s.cache.llmText = llmText
	}
	s.cache.llmErr = llmErr
	s.cache.mu.Unlock()

	log.Printf("poll: done — rendered %d-byte PNG at %s", len(png), now.Format("15:04:05"))
}

func (s *srv) handleAPISetup(w http.ResponseWriter, r *http.Request) {
	resp := apiSetupResponse{
		Status:     http.StatusOK,
		APIKey:     s.cfg.APIKey,
		FriendlyID: "TRMNL1",
		ImageURL:   baseURL(r) + "/dashboard.png",
		Message:    "Welcome to TRMNL Local",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func (s *srv) handleAPIDisplay(w http.ResponseWriter, r *http.Request) {
	s.cache.mu.RLock()
	ready := len(s.cache.png) > 0
	renderedAt := s.cache.renderedAt
	s.cache.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	if !ready {
		// Return 200 with a short refresh_rate so the device retries gracefully
		// rather than treating HTTP 503 as WIFI_INTERNAL_ERROR.
		resp := apiDisplayResponse{
			RefreshRate: 60,
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
		return
	}

	// Use a timestamp-based filename so the device re-downloads whenever we have
	// a new render. The firmware's filesystem_purge_old_file parses the last 10
	// chars as a Unix epoch: a static name like "display.png" produces epoch=0
	// which is always >24h old, and checkCurrentFileName caches it forever.
	filename := fmt.Sprintf("display-%d", renderedAt.Unix())

	resp := apiDisplayResponse{
		Filename:        filename,
		FirmwareURL:     "",
		FirmwareVersion: "",
		ImageURL:        baseURL(r) + "/dashboard.png",
		ImageURLTimeout: 0,
		RefreshRate:     deviceRefreshRateSeconds(renderedAt, time.Now()),
		ResetFirmware:   false,
		SpecialFunction: "",
		UpdateFirmware:  false,
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

type deviceLogEntry struct {
	ID              int     `json:"id"`
	CreatedAt       int64   `json:"created_at"`
	Message         string  `json:"message"`
	SourceLine      int     `json:"source_line"`
	SourcePath      string  `json:"source_path"`
	FirmwareVersion string  `json:"firmware_version"`
	WifiSignal      int     `json:"wifi_signal"`
	WifiStatus      string  `json:"wifi_status"`
	RefreshRate     int     `json:"refresh_rate"`
	SleepDuration   int     `json:"sleep_duration"`
	BatteryVoltage  float64 `json:"battery_voltage"`
	WakeReason      string  `json:"wake_reason"`
	FreeHeapSize    int     `json:"free_heap_size"`
	MaxAllocSize    int     `json:"max_alloc_size"`
	Retry           int     `json:"retry,omitempty"`
}

type deviceLogPayload struct {
	Logs []deviceLogEntry `json:"logs"`
}

func (s *srv) handleAPILog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload deviceLogPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && err.Error() != "EOF" {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	mac := r.Header.Get("ID")
	for _, entry := range payload.Logs {
		log.Printf("device-log mac=%s fw=%s rssi=%d batt=%.2fV heap=%d wake=%q | %s (%s:%d)",
			mac, entry.FirmwareVersion, entry.WifiSignal, entry.BatteryVoltage,
			entry.FreeHeapSize, entry.WakeReason, entry.Message,
			entry.SourcePath, entry.SourceLine)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) handleDisplay(w http.ResponseWriter, r *http.Request) {
	s.cache.mu.RLock()
	png := s.cache.png
	renderedAt := s.cache.renderedAt
	s.cache.mu.RUnlock()

	if len(png) == 0 {
		http.Error(w, "dashboard not ready yet — poll in progress", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(png)))
	w.Header().Set("X-Rendered-At", renderedAt.Format(time.RFC3339))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png) //nolint:errcheck
}

func (s *srv) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.cache.mu.RLock()
	ready := len(s.cache.png) > 0
	renderedAt := s.cache.renderedAt
	s.cache.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if ready {
		w.Write([]byte(`{"status":"ok","rendered_at":"` + renderedAt.Format(time.RFC3339) + `"}`)) //nolint:errcheck
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"initializing"}`)) //nolint:errcheck
	}
}

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	haClient, err := homeassistant.NewClient(cfg.HomeAssistant.CACert)
	if err != nil {
		log.Fatalf("create HA HTTP client: %v", err)
	}

	s := &srv{
		cfg:      cfg,
		cache:    &dataCache{},
		renderer: renderer.New(cfg.PlaywrightURL),
		haClient: haClient,
	}

	const maxStartupAttempts = 5
	const startupRetryDelay = 15 * time.Second
	for attempt := 1; attempt <= maxStartupAttempts; attempt++ {
		log.Printf("running initial poll (attempt %d/%d)...", attempt, maxStartupAttempts)
		s.poll(context.Background())
		s.cache.mu.RLock()
		hasWeather := len(s.cache.weather) > 0 && s.cache.weather[0] != nil
		s.cache.mu.RUnlock()
		if hasWeather {
			log.Println("initial poll succeeded with weather data")
			break
		}
		if attempt < maxStartupAttempts {
			log.Printf("initial poll missing weather data, retrying in %s...", startupRetryDelay)
			time.Sleep(startupRetryDelay)
		} else {
			log.Println("initial poll still missing weather data after all attempts, proceeding anyway")
		}
	}

	log.Println("starting background poller (25m daytime / 90m overnight, ahead of device schedule)")
	go func() {
		for {
			next := time.Duration(pollIntervalSeconds(time.Now())) * time.Second
			timer := time.NewTimer(next)
			<-timer.C
			timer.Stop()
			s.poll(context.Background())
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/setup", s.handleAPISetup)
	mux.HandleFunc("/api/display", s.handleAPIDisplay)
	mux.HandleFunc("/api/log", s.handleAPILog)
	mux.HandleFunc("/dashboard.png", s.handleDisplay)
	mux.HandleFunc("/health", s.handleHealth)

	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s (ID:%s FW:%s)", r.Method, r.URL.Path, r.RemoteAddr,
			r.Header.Get("ID"), r.Header.Get("FW-Version"))
		mux.ServeHTTP(w, r)
	})

	log.Printf("listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, logged); err != nil {
		log.Fatalf("server: %v", err)
	}
}
