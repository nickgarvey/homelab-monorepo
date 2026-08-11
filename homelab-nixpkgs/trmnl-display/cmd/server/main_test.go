package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ngarvey/trmnl-display/internal/config"
)

func newTestSrv(pngReady bool) *srv {
	s := &srv{
		cfg:   &config.Config{APIKey: "test-key"},
		cache: &dataCache{},
	}
	if pngReady {
		s.cache.png = []byte("fake-png-data")
		s.cache.renderedAt = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	}
	return s
}

func newRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Host = "trmnl.home.arpa:8080"
	return r
}

// --- /api/setup ---

func TestHandleAPISetup_status200(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPISetup(w, newRequest(http.MethodGet, "/api/setup"))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleAPISetup_contentType(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPISetup(w, newRequest(http.MethodGet, "/api/setup"))
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestHandleAPISetup_jsonFields(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPISetup(w, newRequest(http.MethodGet, "/api/setup"))

	var resp apiSetupResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.APIKey != "test-key" {
		t.Errorf("api_key = %q, want test-key", resp.APIKey)
	}
	if resp.FriendlyID == "" {
		t.Error("friendly_id should not be empty")
	}
	if resp.Message == "" {
		t.Error("message should not be empty")
	}
}

func TestHandleAPISetup_imageURLUsesHost(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPISetup(w, newRequest(http.MethodGet, "/api/setup"))

	var resp apiSetupResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	want := "http://trmnl.home.arpa:8080/dashboard.png"
	if resp.ImageURL != want {
		t.Errorf("image_url = %q, want %q", resp.ImageURL, want)
	}
}


// Firmware parseResponse_apiSetup checks doc["status"] == 200; a missing field
// deserialises to 0, which triggers StatusError and aborts setup.
func TestHandleAPISetup_responseIncludesStatus200(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPISetup(w, newRequest(http.MethodGet, "/api/setup"))

	var raw map[string]any
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	statusVal, ok := raw["status"]
	if !ok {
		t.Fatal("response missing required 'status' field (firmware will interpret as 0 and fail)")
	}
	// JSON numbers decode as float64 in map[string]any
	if status, ok := statusVal.(float64); !ok || int(status) != 200 {
		t.Errorf("status = %v, want 200", statusVal)
	}
}

func TestHandleAPISetup_imageURLRespectsForwardedProto(t *testing.T) {
	r := newRequest(http.MethodGet, "/api/setup")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPISetup(w, r)

	var resp apiSetupResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if !strings.HasPrefix(resp.ImageURL, "https://") {
		t.Errorf("image_url = %q, want https:// prefix", resp.ImageURL)
	}
}

// --- /api/display ---

func TestHandleAPIDisplay_notReady_returns200(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPIDisplay(w, newRequest(http.MethodGet, "/api/display"))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleAPIDisplay_notReady_returnsJSON(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPIDisplay(w, newRequest(http.MethodGet, "/api/display"))
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
}

func TestHandleAPIDisplay_ready_returns200(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(true).handleAPIDisplay(w, newRequest(http.MethodGet, "/api/display"))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleAPIDisplay_ready_jsonFields(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(true).handleAPIDisplay(w, newRequest(http.MethodGet, "/api/display"))

	var resp apiDisplayResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ImageURL == "" {
		t.Error("image_url should not be empty")
	}
	if resp.RefreshRate <= 0 {
		t.Errorf("refresh_rate = %d, want a positive duration", resp.RefreshRate)
	}
	if resp.SpecialFunction != "" {
		t.Errorf("special_function = %q, want empty string (avoid writing SF_SLEEP to NVS on every cycle)", resp.SpecialFunction)
	}
	if resp.UpdateFirmware {
		t.Error("update_firmware should be false")
	}
	if resp.ResetFirmware {
		t.Error("reset_firmware should be false")
	}
}

// firmware's filesystem_purge_old_file parses the last 10 chars as a Unix epoch;
// a static name like "display.png" gets epoch=0, so the file is treated as >24h old
// and deleted on the next write cycle — and checkCurrentFileName matches the cache
// on the second wake, so the device never re-downloads updated content.
func TestHandleAPIDisplay_ready_filenameContainsTimestamp(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(true).handleAPIDisplay(w, newRequest(http.MethodGet, "/api/display"))

	var resp apiDisplayResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Filename == "display.png" {
		t.Error("filename must not be the static 'display.png'; it needs a Unix-epoch suffix so the device re-downloads on each new render")
	}
	// must be parseable as "display-<10-digit-epoch>"
	if len(resp.Filename) < len("display-")+10 {
		t.Errorf("filename %q too short to contain a Unix epoch suffix", resp.Filename)
	}
}

// firmware treats HTTP 503 as HTTPS_RESPONSE_CODE_INVALID → shows WIFI_INTERNAL_ERROR.
// when the display cache is empty the server should return 200 with a short refresh_rate
// so the device retries gracefully instead of showing an error screen.
func TestHandleAPIDisplay_notReady_returns200WithRetryRate(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPIDisplay(w, newRequest(http.MethodGet, "/api/display"))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (not 503) so device retries gracefully", w.Code)
	}
	var resp apiDisplayResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if resp.RefreshRate == 0 || resp.RefreshRate > 120 {
		t.Errorf("refresh_rate = %d, want a short retry window (≤120s) when display not ready", resp.RefreshRate)
	}
	if resp.ImageURL != "" {
		t.Errorf("image_url = %q, want empty string when display not ready", resp.ImageURL)
	}
}

func TestHandleAPIDisplay_ready_imageURLUsesHost(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(true).handleAPIDisplay(w, newRequest(http.MethodGet, "/api/display"))

	var resp apiDisplayResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	want := "http://trmnl.home.arpa:8080/dashboard.png"
	if resp.ImageURL != want {
		t.Errorf("image_url = %q, want %q", resp.ImageURL, want)
	}
}


// --- /api/log ---

// --- /api/log full support ---

// The device POSTs {"logs":[{...}]} with device telemetry.
// Malformed JSON should return 400, not be silently discarded.
// --- poll / device refresh rate ---

func TestDeviceRefreshRateSeconds_comesBack2MinAfterNextRender(t *testing.T) {
	// Render finished at 10:00. pollInterval at 10:00 is 25 min (daytime).
	// Next render ≈ 10:25. Device should arrive at 10:27.
	renderedAt := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC) // 5 min later
	got := deviceRefreshRateSeconds(renderedAt, now)
	// nextRender=10:25, target=10:27, elapsed since now = 22 min = 1320s
	want := 22 * 60
	if got != want {
		t.Errorf("got %ds, want %ds", got, want)
	}
}

func TestDeviceRefreshRateSeconds_clampsToMinimum(t *testing.T) {
	// Render finished at 10:00, next render at 10:25, target 10:27.
	// If now is already 10:28 (past target), clamp to 60s minimum.
	renderedAt := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 1, 1, 10, 28, 0, 0, time.UTC)
	got := deviceRefreshRateSeconds(renderedAt, now)
	if got != 60 {
		t.Errorf("got %ds, want 60s (minimum clamp)", got)
	}
}

func TestDeviceRefreshRateSeconds_overnightSchedule(t *testing.T) {
	// Render at 22:00 (overnight, pollInterval=90min). Next render ≈ 23:30. Target 23:32.
	renderedAt := time.Date(2026, 1, 1, 22, 0, 0, 0, time.UTC)
	now := time.Date(2026, 1, 1, 22, 10, 0, 0, time.UTC) // 10 min later
	got := deviceRefreshRateSeconds(renderedAt, now)
	// 90min - 10min + 2min = 82 min = 4920s
	want := 82 * 60
	if got != want {
		t.Errorf("got %ds, want %ds", got, want)
	}
}

func TestPollIntervalSeconds_daytime(t *testing.T) {
	for _, h := range []int{8, 12, 14, 20} {
		got := pollIntervalSeconds(time.Date(2026, 1, 1, h, 0, 0, 0, time.UTC))
		if got != 25*60 {
			t.Errorf("hour %d: got %ds, want %ds (25min daytime)", h, got, 25*60)
		}
	}
}

func TestPollIntervalSeconds_overnight(t *testing.T) {
	for _, h := range []int{0, 1, 7, 21, 22, 23} {
		got := pollIntervalSeconds(time.Date(2026, 1, 1, h, 0, 0, 0, time.UTC))
		if got != 90*60 {
			t.Errorf("hour %d: got %ds, want %ds (90min overnight)", h, got, 90*60)
		}
	}
}

// --- /display ---

// The firmware reads Content-Length to allocate its buffer and rejects -1 (chunked).
// Without an explicit Content-Length header Go's http server omits it for small
// writes and the device logs "file size too big: -1".
func TestHandleDisplay_ready_hasContentLength(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(true).handleDisplay(w, newRequest(http.MethodGet, "/dashboard.png"))
	if w.Header().Get("Content-Length") == "" {
		t.Error("Content-Length header missing — firmware needs it to allocate image buffer")
	}
	if w.Header().Get("Content-Length") != fmt.Sprintf("%d", len([]byte("fake-png-data"))) {
		t.Errorf("Content-Length = %q, want %d", w.Header().Get("Content-Length"), len([]byte("fake-png-data")))
	}
}

func TestHandleAPILog_malformedJSON_returns400(t *testing.T) {
	body := strings.NewReader(`not json at all`)
	r := httptest.NewRequest(http.MethodPost, "/api/log", body)
	r.Host = "trmnl.home.arpa"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPILog(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for malformed JSON", w.Code)
	}
}

// Valid log payload with a real device entry should return 204.
func TestHandleAPILog_validPayload_returns204(t *testing.T) {
	body := strings.NewReader(`{"logs":[{"id":1,"message":"boot","firmware_version":"1.7.7","wifi_signal":-65,"battery_voltage":4.1,"wake_reason":"timer","free_heap_size":200000,"max_alloc_size":180000}]}`)
	r := httptest.NewRequest(http.MethodPost, "/api/log", body)
	r.Host = "trmnl.home.arpa"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPILog(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

// Empty logs array is valid — device sometimes sends a heartbeat with no entries.
func TestHandleAPILog_emptyLogsArray_returns204(t *testing.T) {
	body := strings.NewReader(`{"logs":[]}`)
	r := httptest.NewRequest(http.MethodPost, "/api/log", body)
	r.Host = "trmnl.home.arpa"
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPILog(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

func TestHandleAPILog_post_returns204(t *testing.T) {
	body := strings.NewReader(`{"logs":[{"message":"boot"}]}`)
	r := httptest.NewRequest(http.MethodPost, "/api/log", body)
	r.Host = "trmnl.home.arpa:8080"
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPILog(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

func TestHandleAPILog_post_emptyBody_returns204(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/log", nil)
	r.Host = "trmnl.home.arpa:8080"
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPILog(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

func TestHandleAPILog_get_returns405(t *testing.T) {
	w := httptest.NewRecorder()
	newTestSrv(false).handleAPILog(w, newRequest(http.MethodGet, "/api/log"))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}
