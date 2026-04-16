package types

import (
	"context"
	"encoding/json"
	"fmt"
)

type LLMClient interface {
	ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// ChatRequest represents a standardized chat request (mirrors OpenAI format but simplified).
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// Role is the sender role in a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

func (r Role) IsValid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant:
		return true
	default:
		return false
	}
}

func (r *Role) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}

	parsed := Role(value)
	if !parsed.IsValid() {
		return fmt.Errorf("invalid role %q (expected one of: %q, %q, %q)", value, RoleSystem, RoleUser, RoleAssistant)
	}

	*r = parsed
	return nil
}

// Message is a single chat message.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ChatResponse is the standardized response from any provider.
type ChatResponse struct {
	Content string `json:"content"`
}
