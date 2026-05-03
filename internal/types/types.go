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

// Usage holds raw token counts returned by a provider for one call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// AgentUsage is a single provider-call record tagged with agent name and debate phase.
type AgentUsage struct {
	Agent string `json:"agent"`
	Phase string `json:"phase"`
	Usage Usage  `json:"usage"`
}

// PhaseUsage is the aggregated token cost for one debate phase.
type PhaseUsage struct {
	Phase string `json:"phase"`
	Usage Usage  `json:"usage"`
}

// UsageReport is the full token accounting structure attached to a completed debate.
type UsageReport struct {
	// Total is the grand total across all agents and phases.
	Total Usage `json:"total"`
	// PerPhase groups token counts by debate phase (draft, critique, …).
	PerPhase []PhaseUsage `json:"per_phase"`
	// PerAgent lists every individual provider call with its agent+phase label.
	PerAgent []AgentUsage `json:"per_agent"`
}

// ChatResponse is the standardized response from any provider.
type ChatResponse struct {
	Content string `json:"content"`
	// Usage holds token counts for this single provider call.
	// Zero value means the provider did not return usage data.
	Usage Usage `json:"usage"`
}
