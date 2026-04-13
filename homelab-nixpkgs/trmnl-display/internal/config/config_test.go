package config

import (
	"testing"
)

func TestParse_full(t *testing.T) {
	yaml := `
playwright_url: "http://playwright:3000"
listen_addr: ":9090"
poll_interval_minutes: 30
locations:
  - name: "Sunnyvale"
    lat: 37.3688
    lon: -122.0363
    show_weekly: true
    show_uv: true
  - name: "San Francisco"
    lat: 37.7749
    lon: -122.4194
google_calendar:
  service_account_json: "/config/sa.json"
  calendar_id: "cal@group.calendar.google.com"
homeassistant:
  url: "https://ha.home.arpa:8123"
  token: "abc123"
  garage_entity: "cover.garage_door"
  ca_cert: "/config/ca.pem"
news_text: "Today's news here."
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cfg.PlaywrightURL != "http://playwright:3000" {
		t.Errorf("PlaywrightURL = %q", cfg.PlaywrightURL)
	}
	if len(cfg.ListenAddrs) != 1 || cfg.ListenAddrs[0] != ":9090" {
		t.Errorf("ListenAddrs = %q, want [\":9090\"]", cfg.ListenAddrs)
	}
	if cfg.PollIntervalMin != 30 {
		t.Errorf("PollIntervalMin = %d", cfg.PollIntervalMin)
	}
	if len(cfg.Locations) != 2 {
		t.Fatalf("len(Locations) = %d", len(cfg.Locations))
	}
	loc := cfg.Locations[0]
	if loc.Name != "Sunnyvale" {
		t.Errorf("Locations[0].Name = %q", loc.Name)
	}
	if !loc.ShowWeekly {
		t.Error("Locations[0].ShowWeekly should be true")
	}
	if !loc.ShowUV {
		t.Error("Locations[0].ShowUV should be true")
	}
	if cfg.GoogleCal.CalendarID != "cal@group.calendar.google.com" {
		t.Errorf("CalendarID = %q", cfg.GoogleCal.CalendarID)
	}
	if cfg.HomeAssistant.Token != "abc123" {
		t.Errorf("HA token = %q", cfg.HomeAssistant.Token)
	}
	if cfg.NewsText != "Today's news here." {
		t.Errorf("NewsText = %q", cfg.NewsText)
	}
}

func TestParse_defaults(t *testing.T) {
	cfg, err := Parse([]byte("{}"))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(cfg.ListenAddrs) != 1 || cfg.ListenAddrs[0] != ":8080" {
		t.Errorf("default ListenAddrs = %q, want [\":8080\"]", cfg.ListenAddrs)
	}
	if cfg.PollIntervalMin != 15 {
		t.Errorf("default PollIntervalMin = %d, want 15", cfg.PollIntervalMin)
	}
	if cfg.PlaywrightURL != "http://localhost:3000" {
		t.Errorf("default PlaywrightURL = %q", cfg.PlaywrightURL)
	}
	if cfg.HomeAssistant.GarageEntity != "cover.garage_door" {
		t.Errorf("default GarageEntity = %q", cfg.HomeAssistant.GarageEntity)
	}
	if cfg.APIKey != "trmnl-local" {
		t.Errorf("default APIKey = %q, want trmnl-local", cfg.APIKey)
	}
}

func TestParse_customAPIKey(t *testing.T) {
	cfg, err := Parse([]byte("api_key: my-secret-key"))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cfg.APIKey != "my-secret-key" {
		t.Errorf("APIKey = %q, want my-secret-key", cfg.APIKey)
	}
}

func TestParse_noAuthKey(t *testing.T) {
	cfg, err := Parse([]byte("{}"))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cfg.APIKey == "" {
		t.Error("APIKey should have a default value")
	}
}

func TestParse_listenAddrList(t *testing.T) {
	cfg, err := Parse([]byte(`listen_addr: ["[::]:8080", "0.0.0.0:8080"]`))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(cfg.ListenAddrs) != 2 {
		t.Fatalf("ListenAddrs = %q, want 2 entries", cfg.ListenAddrs)
	}
	if cfg.ListenAddrs[0] != "[::]:8080" || cfg.ListenAddrs[1] != "0.0.0.0:8080" {
		t.Errorf("ListenAddrs = %q", cfg.ListenAddrs)
	}
}

func TestParse_invalid_yaml(t *testing.T) {
	_, err := Parse([]byte(":\t{invalid"))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}
