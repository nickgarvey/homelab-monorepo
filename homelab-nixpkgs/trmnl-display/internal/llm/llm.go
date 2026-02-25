package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Result holds a generated rhyme and metadata.
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

const prompt = "Write a short, fun rhyme. Keep it under 200 characters total. Reply with only the rhyme."

// Fetch generates a rhyme using the configured LLM.
func Fetch(ctx context.Context, client *http.Client, baseURL, model string) (*Result, error) {
	reqBody := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 100,
		Stream:    false,
		ChatTemplateKwargs: map[string]any{
			"enable_thinking": false,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("llm error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 || cr.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("empty response from LLM")
	}

	text := strings.TrimSpace(cr.Choices[0].Message.Content)
	// Collapse newlines to spaces so the rhyme flows across the bottom bar.
	text = strings.ReplaceAll(text, "\n", " ")

	return &Result{Text: text, FetchedAt: time.Now()}, nil
}
