package provider

import (
	"context"
	"fmt"

	"github.com/karma-234/llm-consensus/internal/config"
	"github.com/karma-234/llm-consensus/internal/types"
	openai "github.com/sashabaranov/go-openai"
)

type OpenAIClient struct {
	client *openai.Client
	model  string
}

func NewOpenAIClient(agent config.Agent) (*OpenAIClient, error) {
	config := openai.DefaultConfig(agent.APIKey)
	if agent.BaseURL != "" {
		config.BaseURL = agent.BaseURL
	}

	client := openai.NewClientWithConfig(config)
	return &OpenAIClient{
		client: client,
		model:  agent.Model,
	}, nil
}

func (c *OpenAIClient) ChatCompletion(ctx context.Context, req types.ChatRequest) (types.ChatResponse, error) {
	openAIReq := openai.ChatCompletionRequest{
		Model:       c.model,
		Messages:    convertToOpenAIMessages(req.Messages),
		Temperature: float32(req.Temperature),
		MaxTokens:   req.MaxTokens,
	}

	resp, err := c.client.CreateChatCompletion(ctx, openAIReq)
	if err != nil {
		return types.ChatResponse{}, err
	}
	if len(resp.Choices) == 0 {
		return types.ChatResponse{}, fmt.Errorf("openai returned no choices")
	}

	return types.ChatResponse{
		Content: resp.Choices[0].Message.Content,
	}, nil
}

func convertToOpenAIMessages(messages []types.Message) []openai.ChatCompletionMessage {
	openAIMessages := make([]openai.ChatCompletionMessage, len(messages))
	for i, msg := range messages {
		openAIMessages[i] = openai.ChatCompletionMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
	}
	return openAIMessages
}
