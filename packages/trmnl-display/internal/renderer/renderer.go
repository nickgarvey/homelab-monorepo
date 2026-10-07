package renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client sends HTML to a playwright-renderer service and receives a PNG.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a renderer Client targeting the given playwright-renderer base URL.
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type renderRequest struct {
	HTML   string `json:"html"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Render sends HTML to the playwright renderer and returns PNG bytes.
func (c *Client) Render(html string, width, height int) ([]byte, error) {
	reqBody, err := json.Marshal(renderRequest{HTML: html, Width: width, Height: height})
	if err != nil {
		return nil, fmt.Errorf("marshal render request: %w", err)
	}

	resp, err := c.httpClient.Post(
		c.baseURL+"/render",
		"application/json",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return nil, fmt.Errorf("playwright request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("playwright returned %d: %s", resp.StatusCode, errBody)
	}

	png, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read PNG response: %w", err)
	}

	if len(png) < 67 { // smallest valid PNG is 67 bytes
		return nil, fmt.Errorf("response too small to be a PNG (%d bytes)", len(png))
	}

	return png, nil
}
