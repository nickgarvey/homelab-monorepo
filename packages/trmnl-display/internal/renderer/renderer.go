package renderer

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// Client sends HTML to a playwright-renderer service and receives a PNG.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a renderer Client targeting the given playwright-renderer base URL.
// tlsConfig is used for https URLs; nil means the default transport.
func New(baseURL string, tlsConfig *tls.Config) *Client {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	if tlsConfig != nil {
		httpClient.Transport = &http.Transport{TLSClientConfig: tlsConfig}
	}
	return &Client{baseURL: baseURL, httpClient: httpClient}
}

// MTLSConfig authenticates to the renderer with this workload's X.509-SVID
// and accepts only a server certificate carrying serverID. source follows
// SPIRE's rotations, so the config stays valid for the life of the process.
func MTLSConfig(source *workloadapi.X509Source, serverID string) (*tls.Config, error) {
	id, err := spiffeid.FromString(serverID)
	if err != nil {
		return nil, fmt.Errorf("renderer SPIFFE ID: %w", err)
	}
	return tlsconfig.MTLSClientConfig(source, source, tlsconfig.AuthorizeID(id)), nil
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
