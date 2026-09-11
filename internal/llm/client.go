// Package llm implements a minimal OpenAI-compatible /chat/completions client.
//
// The package intentionally avoids pulling a heavyweight SDK so the binary
// stays small. The contract is the public OpenAI Chat Completions schema,
// which is supported by OpenAI, DeepSeek, Moonshot, Zhipu, Ollama, vLLM,
// LM Studio, and most other providers exposing a /v1 endpoint.
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

// Config configures a Client. Zero TimeoutMs means 8s.
type Config struct {
	BaseURL      string
	APIKey       string
	Model        string
	SystemPrompt string
	TimeoutMs    int
}

// Client posts chat completion requests to a BaseURL/chat/completions
// endpoint. It is safe for sequential use; callers should serialize
// requests if they want overlapping calls.
type Client struct {
	cfg    Config
	http   *http.Client
}

// New creates a Client. It validates that BaseURL, APIKey, and Model are set.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("llm: base_url is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("llm: api_key is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("llm: model is required")
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// ChatMessage is a minimal {role, content} entry.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat sends a non-streaming chat completion and returns the assistant text.
// temperature is forced to a low value (0.2) so rewriting stays faithful.
func (c *Client) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("llm: no messages provided")
	}
	payload := map[string]interface{}{
		"model":       c.cfg.Model,
		"messages":    messages,
		"temperature": 0.2,
		"stream":      false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}
	url := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("llm: read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, trimSnippet(string(raw), 240))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("llm: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm: response contained no choices")
	}
	text := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("llm: empty assistant content")
	}
	return text, nil
}

// Polish builds the default two-message polish transcript and sends it.
// The system prompt is c.cfg.SystemPrompt (configurable; sensible default).
func (c *Client) Polish(ctx context.Context, userText string) (string, error) {
	msgs := []ChatMessage{
		{Role: "system", Content: c.cfg.SystemPrompt},
		{Role: "user", Content: userText},
	}
	return c.Chat(ctx, msgs)
}

// IsEnabled returns true when the cfg has the minimum fields required for
// the voice plugin to invoke the LLM at all.
func IsEnabled(cfg Config) bool {
	return strings.TrimSpace(cfg.BaseURL) != "" &&
		strings.TrimSpace(cfg.APIKey) != "" &&
		strings.TrimSpace(cfg.Model) != ""
}

func trimSnippet(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
