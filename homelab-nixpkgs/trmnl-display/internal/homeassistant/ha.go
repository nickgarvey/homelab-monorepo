package homeassistant

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// GarageState holds the last known state of the garage door.
type GarageState struct {
	State     string // "open", "closed", "opening", "closing", "unknown"
	FetchedAt time.Time
}

type haStateResponse struct {
	State string `json:"state"`
}

// NewClient creates an HTTP client for talking to Home Assistant.
func NewClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{},
		Timeout:   10 * time.Second,
	}
}

// FetchGarage fetches the garage door state from Home Assistant.
func FetchGarage(client *http.Client, baseURL, token, entityID string) (*GarageState, error) {
	url := fmt.Sprintf("%s/api/states/%s", baseURL, entityID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build HA request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch garage state: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("HA auth error (401): check token")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HA returned %d", resp.StatusCode)
	}

	var s haStateResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode HA state: %w", err)
	}

	return &GarageState{
		State:     s.State,
		FetchedAt: time.Now(),
	}, nil
}
