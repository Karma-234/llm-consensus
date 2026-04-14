package types

import "context"

type LLMClient interface {
	ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// ChatRequest represents a standardized chat request (mirrors OpenAI format but simplified).
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float32   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// Message is a single chat message.
type Message struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

// ChatResponse is the standardized response from any provider.
type ChatResponse struct {
	Content string `json:"content"`
}
