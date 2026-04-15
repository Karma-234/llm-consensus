package debate

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

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

type DebateResult struct {
	FinalAnswer string
	Transcript  *Transcript
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
			transcript.AddVote(agentName, vote)
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

func (o *Orchestrator) updateActiveAgents(votes map[string]Vote) []string {
	var active []string
	for name, v := range votes {
		if !v.Approve || len(v.BlockingIssues) > 0 || v.Confidence < 0.75 {
			active = append(active, name)
		}
	}
	if len(active) == 0 {
		// Fallback: keep at least one agent
		if agents := o.clientFactory.GetAllClients(); len(agents) > 0 {
			active = []string{agents[0]}
		}
	}
	return active
}

func (o *Orchestrator) runSynthesizePhase(messages []types.Message, drafts, critiques map[string]string, transcript *Transcript) string {
	prompt := o.prompt.SynthesizePrompt(messages, drafts, critiques)

	agents := o.clientFactory.GetAllClients()
	if len(agents) == 0 {
		return "No agents available for synthesis."
	}

	client, _ := o.clientFactory.GetClient(agents[0])
	resp, err := client.ChatCompletion(context.Background(), types.ChatRequest{
		Messages:    []types.Message{{Role: "user", Content: prompt}},
		Temperature: 0.5,
	})
	if err != nil {
		log.Printf("Synthesis failed: %v", err)
		return "Synthesis failed."
	}

	synthesized := resp.Content
	transcript.AddSynthesisPhase(synthesized)
	return synthesized
}

func (o *Orchestrator) fallbackToBestCandidate(candidate string, transcript *Transcript) string {
	if candidate != "" && candidate != "Synthesis failed." {
		return candidate
	}
	fallbackMsg := "The agents were unable to reach consensus on this query. " +
		"Please try rephrasing your question or using a different preset (e.g. zettai-paranoid)."

	transcript.SetFinalAnswer(fallbackMsg)
	return fallbackMsg
}

func (o *Orchestrator) RunDebate(ctx context.Context, messages []types.Message, modelName string) (DebateResult, error) {
	start := time.Now()
	preset := o.resolvePreset(modelName)

	maxRounds := preset.MaxRounds
	strictUnanimity := preset.StrictUnanimity
	outputMode := preset.OutputMode
	transcript := NewTranscript(messages)

	drafts, err := o.runDraftPhase(ctx, messages, transcript)
	if err != nil {
		return DebateResult{}, fmt.Errorf("draft phase failed: %w", err)
	}

	critiques, err := o.runCritiquePhase(ctx, messages, drafts, transcript)
	if err != nil {
		return DebateResult{}, fmt.Errorf("critique phase failed: %w", err)
	}

	candidate := o.runSynthesizePhase(messages, drafts, critiques, transcript)

	activeAgents := o.clientFactory.GetAllClients()
	for round := 1; round <= maxRounds; round++ {
		votes, err := o.runSelectiveVotingPhase(ctx, messages, activeAgents, candidate, transcript)
		if err != nil {
			return DebateResult{}, fmt.Errorf("voting phase failed: %w", err)
		}
		result := EvaluateConsensus(votes, strictUnanimity)
		transcript.AddVotingRound(round, votes, result.Issues)
		activeAgents = o.updateActiveAgents(votes)
		if result.ConsensusReached {
			log.Printf("Consensus reached in %d rounds with preset '%s' (%v)", round, modelName, time.Since(start))
			transcript.SetFinalAnswer(candidate)
			return o.buildResult(candidate, transcript, outputMode), nil
		}
		if round < maxRounds {
			activeAgents = o.updateActiveAgents(votes)
			candidate = o.runSynthesizePhase(messages, drafts, critiques, transcript)
		}
	}

	finalAnswer := o.fallbackToBestCandidate(candidate, transcript)
	log.Printf("Debate ended with fallback after %v", time.Since(start))

	transcript.SetFinalAnswer(finalAnswer)
	return DebateResult{
		FinalAnswer: finalAnswer,
		Transcript:  transcript,
	}, nil
}
func (o *Orchestrator) resolvePreset(modelName string) config.Preset {

	name := strings.TrimPrefix(modelName, "llm-")
	if preset, ok := o.cfg.Presets[name]; ok {
		return preset
	}

	return config.Preset{
		MaxRounds:       o.cfg.Debate.MaxRounds,
		StrictUnanimity: o.cfg.Debate.StrictUnanimity,
		OutputMode:      o.cfg.Output.DefaultMode,
	}
}
func (o *Orchestrator) buildResult(answer string, transcript *Transcript, mode string) DebateResult {
	if mode == "debug" {
		return DebateResult{FinalAnswer: transcript.ToCleanSummary(), Transcript: transcript}
	}
	if mode == "audit" {
		return DebateResult{FinalAnswer: transcript.ToJSON(), Transcript: transcript}
	}
	return DebateResult{FinalAnswer: answer, Transcript: transcript}
}
