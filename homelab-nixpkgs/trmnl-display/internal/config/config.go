package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Location struct {
	Name       string  `yaml:"name"`
	Lat        float64 `yaml:"lat"`
	Lon        float64 `yaml:"lon"`
	ShowWeekly bool    `yaml:"show_weekly"`
	ShowUV     bool    `yaml:"show_uv"`
}

type GoogleCalendar struct {
	ServiceAccountJSON string `yaml:"service_account_json"`
	CalendarID         string `yaml:"calendar_id"`
}

type HomeAssistant struct {
	URL          string `yaml:"url"`
	Token        string `yaml:"token"`
	GarageEntity string `yaml:"garage_entity"`
	CACert       string `yaml:"ca_cert"`
}

type Config struct {
	PlaywrightURL   string         `yaml:"playwright_url"`
	ListenAddr      string         `yaml:"listen_addr"`
	Locations       []Location     `yaml:"locations"`
	GoogleCal       GoogleCalendar `yaml:"google_calendar"`
	HomeAssistant   HomeAssistant  `yaml:"homeassistant"`
	NewsText        string         `yaml:"news_text"`
	PollIntervalMin int            `yaml:"poll_interval_minutes"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return Parse(data)
}

func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}
	if cfg.PollIntervalMin == 0 {
		cfg.PollIntervalMin = 15
	}
	if cfg.PlaywrightURL == "" {
		cfg.PlaywrightURL = "http://localhost:3000"
	}
	if cfg.HomeAssistant.GarageEntity == "" {
		cfg.HomeAssistant.GarageEntity = "cover.garage_door"
	}
	if cfg.NewsText == "" {
		cfg.NewsText = "Lorem ipsum dolor sit amet, consectetur adipiscing elit."
	}
	return &cfg, nil
}
