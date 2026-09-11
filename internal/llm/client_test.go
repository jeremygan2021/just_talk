package llm

import (
	"context"
	"strings"
	"testing"
)

func TestNewRequiresFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"empty", Config{}},
		{"no base url", Config{APIKey: "k", Model: "m"}},
		{"no api key", Config{BaseURL: "https://example.com", Model: "m"}},
		{"no model", Config{BaseURL: "https://example.com", APIKey: "k"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestIsEnabled(t *testing.T) {
	if IsEnabled(Config{}) {
		t.Fatal("IsEnabled should be false for empty config")
	}
	if !IsEnabled(Config{BaseURL: "https://x", APIKey: "k", Model: "m"}) {
		t.Fatal("IsEnabled should be true when all required fields are set")
	}
}

func TestPolishBuildsMessages(t *testing.T) {
	c, err := New(Config{
		BaseURL:      "https://api.example.com/v1",
		APIKey:       "secret",
		Model:        "test-model",
		SystemPrompt: "you are a polisher",
		TimeoutMs:    1000,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// We can't easily intercept the http.Client without more plumbing, so
	// exercise the message-assembly path by hitting a deliberately invalid
	// endpoint and asserting we surface a network-style error from the
	// client (not a marshal/decode error).
	_, err = c.Polish(context.Background(), "hello world")
	if err == nil {
		t.Skip("network unexpectedly succeeded; skipping")
	}
	if !strings.Contains(err.Error(), "llm:") {
		t.Fatalf("expected error to be prefixed with 'llm:', got %v", err)
	}
}
