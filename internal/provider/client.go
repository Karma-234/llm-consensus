package provider

import (
	"fmt"

	"github.com/karma-234/llm-consensus/internal/config"
	"github.com/karma-234/llm-consensus/internal/types"
)

func NewChatClient(agent config.Agent) (types.LLMClient, error) {
	switch agent.Provider {
	case "openai", "xai", "groq":
		return NewOpenAIClient(agent)
	// case "anthropic":
	// 	return NewAnthropicClient(agent)
	default:
		return nil, fmt.Errorf("unsupported provider: %s for agent %s", agent.Provider, agent.Name)
	}
}

type ClientFactory struct {
	clients map[string]types.LLMClient
}

func NewClientFactory(agents []config.Agent) (*ClientFactory, error) {
	factory := &ClientFactory{clients: make(map[string]types.LLMClient)}
	for _, agent := range agents {
		client, err := NewChatClient(agent)
		if err != nil {
			return nil, fmt.Errorf("failed to create client for agent %s: %w", agent.Name, err)
		}
		factory.clients[agent.Name] = client
	}
	return factory, nil
}

func (f *ClientFactory) GetClient(agentName string) (types.LLMClient, error) {
	client, exists := f.clients[agentName]
	if !exists {
		return nil, fmt.Errorf("client not found for agent: %s", agentName)
	}
	return client, nil
}

func (f *ClientFactory) GetAllClients() []string {
	names := make([]string, 0, len(f.clients))
	for name := range f.clients {
		names = append(names, name)
	}
	return names
}
