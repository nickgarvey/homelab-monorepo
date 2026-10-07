package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// --- parseEntriesJSON ---

func TestParseEntriesJSON_validJSON(t *testing.T) {
	raw := `[{"character":"水","english":"water","mandarin":"shuǐ","cantonese":"seoi2"},{"character":"火","english":"fire","mandarin":"huǒ","cantonese":"fo2"}]`
	entries, err := parseEntriesJSON(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	want0 := "水 (water) · Mandarin: shuǐ · Cantonese: seoi2"
	if entries[0] != want0 {
		t.Errorf("entries[0] = %q, want %q", entries[0], want0)
	}
	want1 := "火 (fire) · Mandarin: huǒ · Cantonese: fo2"
	if entries[1] != want1 {
		t.Errorf("entries[1] = %q, want %q", entries[1], want1)
	}
}

func TestParseEntriesJSON_invalidJSON(t *testing.T) {
	_, err := parseEntriesJSON(`not json at all`)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestParseEntriesJSON_emptyArray(t *testing.T) {
	_, err := parseEntriesJSON(`[]`)
	if err == nil {
		t.Error("expected error for empty array, got nil")
	}
}

func TestParseEntriesJSON_stripsMarkdownFence(t *testing.T) {
	raw := "```json\n[{\"character\":\"水\",\"english\":\"water\",\"mandarin\":\"shuǐ\",\"cantonese\":\"seoi2\"}]\n```"
	entries, err := parseEntriesJSON(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
}

// --- Fetch retry behaviour ---

func makeChatResponse(entries []wordEntry) string {
	b := `[`
	for i, e := range entries {
		if i > 0 {
			b += ","
		}
		b += fmt.Sprintf(`{"character":%q,"english":%q,"mandarin":%q,"cantonese":%q}`,
			e.Character, e.English, e.Mandarin, e.Cantonese)
	}
	return `{"choices":[{"message":{"content":"` + jsonEscape(b+`]`) + `"}}]}`
}

func jsonEscape(s string) string {
	// minimal escaping sufficient for test strings
	out := ""
	for _, c := range s {
		switch c {
		case '"':
			out += `\"`
		case '\\':
			out += `\\`
		case '\n':
			out += `\n`
		default:
			out += string(c)
		}
	}
	return out
}

func TestFetch_retriesOnInvalidJSON(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			// First call: LLM returns garbage instead of JSON
			fmt.Fprint(w, `{"choices":[{"message":{"content":"not valid json"}}]}`)
		} else {
			// Second call: valid response
			fmt.Fprint(w, makeChatResponse([]wordEntry{
				{Character: "水", English: "water", Mandarin: "shuǐ", Cantonese: "seoi2"},
			}))
		}
	}))
	defer srv.Close()

	result, err := Fetch(context.Background(), srv.Client(), srv.URL, "test-model", "Nature")
	if err != nil {
		t.Fatalf("expected success on retry, got: %v", err)
	}
	if result.Text != "水 (water) · Mandarin: shuǐ · Cantonese: seoi2" {
		t.Errorf("text = %q, want formatted entry", result.Text)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 LLM calls (1 retry), got %d", calls.Load())
	}
}

func TestFetch_bothAttemptsFailReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"still not json"}}]}`)
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.Client(), srv.URL, "test-model", "Nature")
	if err == nil {
		t.Error("expected error when both attempts return invalid JSON, got nil")
	}
}
