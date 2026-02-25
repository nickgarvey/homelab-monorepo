package main

import (
	"context"
	"flag"
	"log"
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

	// Fetch LLM rhyme (only if configured and no static news_text override).
	if s.cfg.LLM.BaseURL != "" && s.cfg.NewsText == "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			llmCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			r, err := llm.Fetch(llmCtx, &http.Client{Timeout: 30 * time.Second}, s.cfg.LLM.BaseURL, s.cfg.LLM.Model)
			if err != nil {
				log.Printf("poll: llm: %v", err)
				s.cache.mu.RLock()
				mu.Lock()
				llmText = s.cache.llmText // keep last successful rhyme
				llmErr = "Rhyme generator offline."
				mu.Unlock()
				s.cache.mu.RUnlock()
			} else {
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

func (s *srv) handleDisplay(w http.ResponseWriter, r *http.Request) {
	s.cache.mu.RLock()
	png := s.cache.png
	renderedAt := s.cache.renderedAt
	s.cache.mu.RUnlock()

	if len(png) == 0 {
		http.Error(w, "display not ready yet — poll in progress", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "image/png")
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

	log.Println("running initial poll...")
	s.poll(context.Background())

	interval := time.Duration(cfg.PollIntervalMin) * time.Minute
	log.Printf("starting background poller (interval: %s)", interval)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			s.poll(context.Background())
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/display", s.handleDisplay)
	mux.HandleFunc("/health", s.handleHealth)

	log.Printf("listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
