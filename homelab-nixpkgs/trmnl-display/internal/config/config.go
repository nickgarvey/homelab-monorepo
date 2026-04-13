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

type LLM struct {
	BaseURL              string   `yaml:"base_url"`
	Model                string   `yaml:"model"`
	WordOfDayCategories  []string `yaml:"word_of_day_categories"`
}

var defaultWordOfDayCategories = []string{
	"Basic words",
	"Food",
	"Computers",
	"Kpop",
	"Board games",
	"Weird stuff (non-vulgar)",
}

type Config struct {
	PlaywrightURL   string         `yaml:"playwright_url"`
	ListenAddrs     ListenAddrs    `yaml:"listen_addr"`
	BaseURL         string         `yaml:"base_url"`
	Locations       []Location     `yaml:"locations"`
	GoogleCal       GoogleCalendar `yaml:"google_calendar"`
	HomeAssistant   HomeAssistant  `yaml:"homeassistant"`
	LLM             LLM            `yaml:"llm"`
	NewsText        string         `yaml:"news_text"`
	PollIntervalMin int            `yaml:"poll_interval_minutes"`
	APIKey          string         `yaml:"api_key"`
}

// ListenAddrs accepts either a single string or a list of strings in YAML.
type ListenAddrs []string

func (l *ListenAddrs) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var single string
	if err := unmarshal(&single); err == nil {
		*l = ListenAddrs{single}
		return nil
	}
	var multi []string
	if err := unmarshal(&multi); err != nil {
		return err
	}
	*l = ListenAddrs(multi)
	return nil
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
	if len(cfg.ListenAddrs) == 0 {
		cfg.ListenAddrs = ListenAddrs{":8080"}
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
	if len(cfg.LLM.WordOfDayCategories) == 0 {
		cfg.LLM.WordOfDayCategories = defaultWordOfDayCategories
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "trmnl-local"
	}
	return &cfg, nil
}
