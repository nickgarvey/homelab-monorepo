package renderer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// minimalPNG is a 1x1 white PNG (67 bytes).
var minimalPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
	0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, // IDAT chunk
	0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x00, 0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc,
	0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, // IEND chunk
	0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestRender_success(t *testing.T) {
	var capturedReq renderRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/render" {
			t.Errorf("path = %s, want /render", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)
		w.Header().Set("Content-Type", "image/png")
		w.Write(minimalPNG)
	}))
	defer srv.Close()

	client := New(srv.URL)
	client.httpClient = srv.Client()

	png, err := client.Render("<html></html>", 800, 480)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if len(png) == 0 {
		t.Error("PNG should not be empty")
	}
	if capturedReq.Width != 800 {
		t.Errorf("width = %d, want 800", capturedReq.Width)
	}
	if capturedReq.Height != 480 {
		t.Errorf("height = %d, want 480", capturedReq.Height)
	}
	if capturedReq.HTML != "<html></html>" {
		t.Errorf("HTML = %q", capturedReq.HTML)
	}
}

func TestRender_non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"render failed"}`))
	}))
	defer srv.Close()

	client := New(srv.URL)
	client.httpClient = srv.Client()

	_, err := client.Render("<html></html>", 800, 480)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestRender_tooSmall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("tiny"))
	}))
	defer srv.Close()

	client := New(srv.URL)
	client.httpClient = srv.Client()

	_, err := client.Render("<html></html>", 800, 480)
	if err == nil {
		t.Error("expected error for suspiciously small response")
	}
}

func TestRender_connectionRefused(t *testing.T) {
	client := New("http://127.0.0.1:1") // nothing listening here
	_, err := client.Render("<html></html>", 800, 480)
	if err == nil {
		t.Error("expected error for connection refused")
	}
}
