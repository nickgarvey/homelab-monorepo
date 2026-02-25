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
Return exactly 30 lines, one per line, in this exact format:
<character> (<english meaning>) · Mandarin: <pinyin with tone marks> · Cantonese: <jyutping>
Example: 水 (water) · Mandarin: shuǐ · Cantonese: seoi2
Reply with only the 30 lines, no numbering, no extra text.`

// Fetch requests 30 word-of-the-day entries for the given category from the
// LLM, then returns one chosen at random.
func Fetch(ctx context.Context, client *http.Client, baseURL, model, category string) (*Result, error) {
	prompt := fmt.Sprintf(wordOfDayPrompt, category)

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

	lines := parseEntries(cr.Choices[0].Message.Content)
	if len(lines) == 0 {
		return nil, fmt.Errorf("no valid entries in LLM response")
	}

	chosen := lines[rand.Intn(len(lines))]
	return &Result{Text: chosen, FetchedAt: time.Now()}, nil
}

// parseEntries splits the LLM response into individual entry lines,
// discarding empty lines and any that look like preamble/numbering.
func parseEntries(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip leading list markers like "1." or "1)" or "-"
		if len(line) > 2 && (line[1] == '.' || line[1] == ')') {
			line = strings.TrimSpace(line[2:])
		} else if strings.HasPrefix(line, "- ") {
			line = strings.TrimSpace(line[2:])
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
