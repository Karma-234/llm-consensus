package debate

import (
	"github.com/karma-234/llm-consensus/internal/config"
	"github.com/karma-234/llm-consensus/internal/provider"
)

type Orchestrator struct {
	prompt        *DebatePrompt
	cfg           *config.Config
	clientFactory *provider.ClientFactory
}

func NewOrchestrator(cfg *config.Config, clientFactory *provider.ClientFactory) *Orchestrator {
	prompt := NewDebatePrompt()
	return &Orchestrator{
		prompt:        prompt,
		cfg:           cfg,
		clientFactory: clientFactory,
	}
}
