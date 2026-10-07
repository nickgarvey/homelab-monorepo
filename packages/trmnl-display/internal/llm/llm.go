package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// Result holds a generated word of the day and metadata.
type Result struct {
	Text      string
	FetchedAt time.Time
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model              string            `json:"model"`
	Messages           []chatMessage     `json:"messages"`
	MaxTokens          int               `json:"max_tokens"`
	Stream             bool              `json:"stream"`
	ChatTemplateKwargs map[string]any    `json:"chat_template_kwargs"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

const wordOfDayPrompt = `Give me 30 Chinese word of the day entries for the category "%s".
Return a JSON array of exactly 30 objects with this exact structure:
[{"character":"水","english":"water","mandarin":"shuǐ","cantonese":"seoi2"}, ...]
Reply with only the JSON array, no markdown fences, no extra text.`

type wordEntry struct {
	Character string `json:"character"`
	English   string `json:"english"`
	Mandarin  string `json:"mandarin"`
	Cantonese string `json:"cantonese"`
}

// parseEntriesJSON parses a JSON array of word entries returned by the LLM and
// formats each one as "character (english) · Mandarin: pinyin · Cantonese: jyutping".
// It tolerates a leading ```json ... ``` markdown fence.
func parseEntriesJSON(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	// Strip optional markdown code fence the LLM sometimes adds.
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		if idx := strings.LastIndex(raw, "```"); idx != -1 {
			raw = raw[:idx]
		}
		raw = strings.TrimSpace(raw)
	}

	var entries []wordEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("invalid JSON from LLM: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("LLM returned empty entry array")
	}

	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, fmt.Sprintf("%s (%s) · Mandarin: %s · Cantonese: %s",
			e.Character, e.English, e.Mandarin, e.Cantonese))
	}
	return out, nil
}

// callLLM makes a single chat-completion request and returns the text content.
func callLLM(ctx context.Context, client *http.Client, baseURL, model, prompt string) (string, error) {
	reqBody := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 1024,
		Stream:    false,
		ChatTemplateKwargs: map[string]any{
			"enable_thinking": false,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("llm error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 || cr.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("empty response from LLM")
	}
	return cr.Choices[0].Message.Content, nil
}

// Fetch requests 30 word-of-the-day entries for the given category from the
// LLM (as JSON), then returns one chosen at random. If the first response
// contains invalid JSON it retries once; if that also fails it returns an error
// so the caller can fall back to the cached value.
func Fetch(ctx context.Context, client *http.Client, baseURL, model, category string) (*Result, error) {
	prompt := fmt.Sprintf(wordOfDayPrompt, category)

	for attempt := 1; attempt <= 2; attempt++ {
		content, err := callLLM(ctx, client, baseURL, model, prompt)
		if err != nil {
			return nil, err // HTTP / network error — no point retrying
		}

		entries, err := parseEntriesJSON(content)
		if err != nil {
			if attempt == 1 {
				continue // retry once on bad JSON
			}
			return nil, fmt.Errorf("invalid JSON from LLM after retry: %w", err)
		}

		chosen := entries[rand.Intn(len(entries))]
		return &Result{Text: chosen, FetchedAt: time.Now()}, nil
	}

	// unreachable, but satisfies the compiler
	return nil, fmt.Errorf("fetch failed")
}
