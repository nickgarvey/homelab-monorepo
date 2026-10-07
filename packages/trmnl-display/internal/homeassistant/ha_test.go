package homeassistant

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchGarage_closed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/states/cover.garage_door" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"closed","entity_id":"cover.garage_door"}`))
	}))
	defer srv.Close()

	state, err := FetchGarage(srv.Client(), srv.URL, "test-token", "cover.garage_door")
	if err != nil {
		t.Fatalf("FetchGarage error: %v", err)
	}
	if state.State != "closed" {
		t.Errorf("State = %q, want closed", state.State)
	}
	if state.FetchedAt.IsZero() {
		t.Error("FetchedAt should not be zero")
	}
}

func TestFetchGarage_open(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"open"}`))
	}))
	defer srv.Close()

	state, err := FetchGarage(srv.Client(), srv.URL, "tok", "cover.garage_door")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.State != "open" {
		t.Errorf("State = %q, want open", state.State)
	}
}

func TestFetchGarage_unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := FetchGarage(srv.Client(), srv.URL, "bad-token", "cover.garage_door")
	if err == nil {
		t.Error("expected error for 401")
	}
}

func TestFetchGarage_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := FetchGarage(srv.Client(), srv.URL, "tok", "cover.garage_door")
	if err == nil {
		t.Error("expected error for 500")
	}
}

func TestFetchGarage_states(t *testing.T) {
	for _, state := range []string{"open", "closed", "opening", "closing"} {
		state := state
		t.Run(state, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"state":"` + state + `"}`))
			}))
			defer srv.Close()

			result, err := FetchGarage(srv.Client(), srv.URL, "tok", "cover.garage_door")
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if result.State != state {
				t.Errorf("State = %q, want %q", result.State, state)
			}
		})
	}
}
