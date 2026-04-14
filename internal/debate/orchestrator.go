package debate

import (
	"context"
	"fmt"
	"sync"

	"github.com/karma-234/llm-consensus/internal/config"
	"github.com/karma-234/llm-consensus/internal/provider"
	"github.com/karma-234/llm-consensus/internal/types"
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

func (o *Orchestrator) runDraftPhase(ctx context.Context, messages []types.Message, transcript *Transcript) (map[string]string, error) {
	agentNames := o.clientFactory.GetAllClients()
	drafts := make(map[string]string)

	var mu sync.Mutex
	var wg sync.WaitGroup

	errChan := make(chan error, len(agentNames))

	for _, name := range agentNames {
		wg.Add(1)
		go func(agentName string) {
			defer wg.Done()
			client, err := o.clientFactory.GetClient(agentName)
			if err != nil {
				errChan <- fmt.Errorf("failed to get client for agent %s: %w", agentName, err)
				return
			}
			prompt := o.prompt.DraftPrompt(agentName, messages)
			resp, err := client.ChatCompletion(ctx, types.ChatRequest{
				Messages: []types.Message{
					{Role: "system", Content: prompt},
				},
			})
			if err != nil {
				errChan <- fmt.Errorf("agent %s failed to generate draft: %w", agentName, err)
				return
			}
			mu.Lock()
			drafts[agentName] = resp.Content
			mu.Unlock()
			transcript.AddDraftPhase(agentName, resp.Content)

		}(name)
	}
	wg.Wait()
	close(errChan)

	if len(errChan) > 0 {
		return nil, <-errChan
	}

	return drafts, nil
}

func (o *Orchestrator) runCritiquePhase(ctx context.Context, messages []types.Message, drafts map[string]string, transcript *Transcript) (map[string]string, error) {
	agentNames := o.clientFactory.GetAllClients()
	critiques := make(map[string]string)

	var mu sync.Mutex
	var wg sync.WaitGroup

	errChan := make(chan error, len(agentNames))

	for _, name := range agentNames {
		wg.Add(1)
		go func(agentName string) {
			defer wg.Done()
			client, err := o.clientFactory.GetClient(agentName)
			if err != nil {
				errChan <- fmt.Errorf("failed to get client for agent %s: %w", agentName, err)
				return
			}
			prompt := o.prompt.CritiquePrompt(messages, drafts, agentName)
			resp, err := client.ChatCompletion(ctx, types.ChatRequest{
				Messages: []types.Message{
					{Role: "user", Content: prompt},
				},
				Temperature: 0.7,
			})
			if err != nil {
				errChan <- fmt.Errorf("agent %s failed to generate critique: %w", agentName, err)
				return
			}
			mu.Lock()
			critiques[agentName] = resp.Content
			mu.Unlock()
			transcript.AddCritiquePhase(agentName, resp.Content)

		}(name)
	}
	wg.Wait()
	close(errChan)

	if len(errChan) > 0 {
		return nil, <-errChan
	}

	return critiques, nil
}

func (o *Orchestrator) runSelectiveVotingPhase(ctx context.Context, messages []types.Message, activeAgents []string, candidate string, transcript *Transcript) (map[string]Vote, error) {
	votes := make(map[string]Vote)

	var mu sync.Mutex
	var wg sync.WaitGroup

	errChan := make(chan error, len(activeAgents))

	for _, name := range activeAgents {
		wg.Add(1)
		go func(agentName string) {
			defer wg.Done()
			client, err := o.clientFactory.GetClient(agentName)
			if err != nil {
				errChan <- fmt.Errorf("failed to get client for agent %s: %w", agentName, err)
				return
			}
			prompt := o.prompt.VotePrompt(messages, candidate, name)
			resp, err := client.ChatCompletion(ctx, types.ChatRequest{
				Messages: []types.Message{
					{Role: "user", Content: prompt},
				},
				Temperature: 0.0,
			})
			if err != nil {
				errChan <- fmt.Errorf("agent %s failed to generate vote: %w", agentName, err)
				return
			}
			vote, err := ParseVoteResponse(resp.Content)
			if err != nil {
				errChan <- fmt.Errorf("agent %s failed to parse vote response: %w", agentName, err)
				return
			}
			mu.Lock()
			votes[agentName] = vote
			// transcript.A
			mu.Unlock()

		}(name)
	}
	wg.Wait()
	close(errChan)

	if len(errChan) > 0 {
		return nil, <-errChan
	}

	return votes, nil
}
