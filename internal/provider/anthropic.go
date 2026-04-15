package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/karma-234/llm-consensus/internal/config"
	"github.com/karma-234/llm-consensus/internal/types"
)

type AnthropicClient struct {
	model  string
	client *http.Client
	APIKey string
}

func NewAnthropicClient(agent config.Agent) (*AnthropicClient, error) {
	return &AnthropicClient{
		model:  agent.Model,
		client: &http.Client{Timeout: 5 * time.Minute},
		APIKey: agent.APIKey,
	}, nil
}

func (c *AnthropicClient) ChatCompletion(ctx context.Context, req types.ChatRequest) (types.ChatResponse, error) {
	requestBody := convertToAnthropicRequest(req, c.model)

	body, err := json.Marshal(requestBody)
	if err != nil {
		return types.ChatResponse{}, fmt.Errorf("Failed to marshal request: %s", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(body))
	if err != nil {
		return types.ChatResponse{}, fmt.Errorf("Failed to create HTTP request: %s", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", c.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return types.ChatResponse{}, fmt.Errorf("HTTP request failed: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return types.ChatResponse{}, fmt.Errorf("Non-200 response: %d", resp.StatusCode)
	}

	var anthropicResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return types.ChatResponse{}, fmt.Errorf("Failed to decode response: %s", err)
	}

	if len(anthropicResp.Content) == 0 {
		return types.ChatResponse{}, fmt.Errorf("Empty response content")
	}
	var fullContent strings.Builder
	for _, block := range anthropicResp.Content {
		if block.Type == "text" {
			fullContent.WriteString(block.Content)
		}
	}

	return types.ChatResponse{
		Content: fullContent.String(),
	}, nil

}

type anthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature float64            `json:"temperature,omitempty"`
	MaxTokens   int                `json:"max_tokens,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"` // "user", "assistant"
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
	ID      string                  `json:"id"`
	Model   string                  `json:"model"`
}

type anthropicContentBlock struct {
	Type    string `json:"type"` // "text", "image", etc.
	Content string `json:"content"`
}

func convertToAnthropicRequest(req types.ChatRequest, fallbackModel string) anthropicRequest {
	anthropicMessages := convertMessagesToAnthropicFormat(req.Messages)
	model := req.Model
	if model == "" {
		model = fallbackModel
	}
	return anthropicRequest{
		Model:       model,
		Messages:    anthropicMessages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
}

func convertMessagesToAnthropicFormat(messages []types.Message) []anthropicMessage {
	anthropicMessages := make([]anthropicMessage, len(messages))
	for i, msg := range messages {
		role := msg.Role
		if msg.Role == "system" {
			role = "user"
		}
		anthropicMessages[i] = anthropicMessage{
			Role:    role,
			Content: msg.Content,
		}
	}
	return anthropicMessages
}
