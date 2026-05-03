package debate

import (
	"context"
	"fmt"
	"log"
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
	Usage       types.UsageReport
}

// usageAccumulator collects per-call AgentUsage records concurrently
// and can produce a UsageReport at the end of the debate.
type usageAccumulator struct {
	mu      sync.Mutex
	records []types.AgentUsage
}

func (a *usageAccumulator) add(agent, phase string, u types.Usage) {
	a.mu.Lock()
	a.records = append(a.records, types.AgentUsage{Agent: agent, Phase: phase, Usage: u})
	a.mu.Unlock()
}

func (a *usageAccumulator) report() types.UsageReport {
	a.mu.Lock()
	defer a.mu.Unlock()

	var total types.Usage
	phaseMap := make(map[string]*types.Usage)

	for _, r := range a.records {
		total.PromptTokens += r.Usage.PromptTokens
		total.CompletionTokens += r.Usage.CompletionTokens
		total.TotalTokens += r.Usage.TotalTokens

		if phaseMap[r.Phase] == nil {
			phaseMap[r.Phase] = &types.Usage{}
		}
		phaseMap[r.Phase].PromptTokens += r.Usage.PromptTokens
		phaseMap[r.Phase].CompletionTokens += r.Usage.CompletionTokens
		phaseMap[r.Phase].TotalTokens += r.Usage.TotalTokens
	}

	// Stable ordered phases for consistent output.
	phaseOrder := []string{"draft", "critique", "synthesize", "vote", "revise"}
	var perPhase []types.PhaseUsage
	for _, ph := range phaseOrder {
		if u, ok := phaseMap[ph]; ok {
			perPhase = append(perPhase, types.PhaseUsage{Phase: ph, Usage: *u})
		}
	}

	return types.UsageReport{
		Total:    total,
		PerPhase: perPhase,
		PerAgent: append([]types.AgentUsage(nil), a.records...),
	}
}

func (o *Orchestrator) runDraftPhase(ctx context.Context, messages []types.Message, transcript *Transcript, acc *usageAccumulator) (map[string]string, error) {
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
					{Role: types.RoleSystem, Content: prompt},
				},
			})
			if err != nil {
				errChan <- fmt.Errorf("agent %s failed to generate draft: %w", agentName, err)
				return
			}
			mu.Lock()
			drafts[agentName] = resp.Content
			transcript.AddDraftPhase(agentName, resp.Content)
			acc.add(agentName, "draft", resp.Usage)
			mu.Unlock()

		}(name)
	}
	wg.Wait()
	close(errChan)

	if len(errChan) > 0 {
		return nil, <-errChan
	}

	return drafts, nil
}

func (o *Orchestrator) runCritiquePhase(ctx context.Context, messages []types.Message, drafts map[string]string, transcript *Transcript, acc *usageAccumulator) (map[string]string, error) {
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
					{Role: types.RoleUser, Content: prompt},
				},
				Temperature: 0.7,
			})
			if err != nil {
				errChan <- fmt.Errorf("agent %s failed to generate critique: %w", agentName, err)
				return
			}
			mu.Lock()
			critiques[agentName] = resp.Content
			transcript.AddCritiquePhase(agentName, resp.Content)
			acc.add(agentName, "critique", resp.Usage)
			mu.Unlock()

		}(name)
	}
	wg.Wait()
	close(errChan)

	if len(errChan) > 0 {
		return nil, <-errChan
	}

	return critiques, nil
}

func (o *Orchestrator) runSelectiveVotingPhase(ctx context.Context, messages []types.Message, activeAgents []string, candidate string, transcript *Transcript, acc *usageAccumulator) (map[string]Vote, error) {
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
					{Role: types.RoleUser, Content: prompt},
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
			acc.add(agentName, "vote", resp.Usage)
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
func (o *Orchestrator) runSelectiveRevisePhase(ctx context.Context, candidate string, issues []string, activeAgents []string, transcript *Transcript, messages []types.Message, acc *usageAccumulator) string {
	if len(activeAgents) == 0 {
		return candidate
	}

	// Use only the first active agent for revision (efficient)
	reviseAgent := activeAgents[0]
	client, err := o.clientFactory.GetClient(reviseAgent)
	if err != nil {
		return candidate
	}

	prompt := o.prompt.RevisePrompt(messages, candidate, issues)

	resp, err := client.ChatCompletion(ctx, types.ChatRequest{
		Messages:    []types.Message{{Role: types.RoleUser, Content: prompt}},
		Temperature: 0.6,
	})
	if err != nil {
		log.Printf("Revision failed for %s, keeping previous candidate", reviseAgent)
		return candidate
	}

	newCandidate := resp.Content
	acc.add(reviseAgent, "revise", resp.Usage)
	transcript.AddRevision(reviseAgent, newCandidate, issues)
	log.Printf("Revised by %s", reviseAgent)

	return newCandidate
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

func (o *Orchestrator) runSynthesizePhase(ctx context.Context, messages []types.Message, drafts, critiques map[string]string, transcript *Transcript, acc *usageAccumulator) string {
	prompt := o.prompt.SynthesizePrompt(messages, drafts, critiques)

	agents := o.clientFactory.GetAllClients()
	if len(agents) == 0 {
		return "No agents available for synthesis."
	}

	client, _ := o.clientFactory.GetClient(agents[0])
	resp, err := client.ChatCompletion(ctx, types.ChatRequest{
		Messages:    []types.Message{{Role: types.RoleUser, Content: prompt}},
		Temperature: 0.5,
	})
	if err != nil {
		log.Printf("Synthesis failed: %v", err)
		return "Synthesis failed."
	}

	synthesized := resp.Content
	acc.add(agents[0], "synthesize", resp.Usage)
	transcript.AddSynthesisPhase(synthesized)
	return synthesized
}

func (o *Orchestrator) fallbackToBestCandidate(candidate string) string {
	if candidate != "" && candidate != "Synthesis failed." {
		return candidate
	}
	fallbackMsg := "The agents were unable to reach consensus on this query. " +
		"Please try rephrasing your question or using a different preset (e.g. llm-paranoid)."

	return fallbackMsg
}

func (o *Orchestrator) RunDebate(ctx context.Context, messages []types.Message, modelName string) (DebateResult, error) {
	start := time.Now()
	preset := o.resolvePreset(modelName)

	maxRounds := preset.MaxRounds
	strictUnanimity := preset.StrictUnanimity
	outputMode := preset.OutputMode
	transcript := NewTranscript(messages)
	acc := &usageAccumulator{}

	drafts, err := o.runDraftPhase(ctx, messages, transcript, acc)
	if err != nil {
		return DebateResult{}, fmt.Errorf("draft phase failed: %w", err)
	}

	critiques, err := o.runCritiquePhase(ctx, messages, drafts, transcript, acc)
	if err != nil {
		return DebateResult{}, fmt.Errorf("critique phase failed: %w", err)
	}

	candidate := o.runSynthesizePhase(ctx, messages, drafts, critiques, transcript, acc)

	activeAgents := o.clientFactory.GetAllClients()
	for round := 1; round <= maxRounds; round++ {
		votes, err := o.runSelectiveVotingPhase(ctx, messages, activeAgents, candidate, transcript, acc)
		if err != nil {
			return DebateResult{}, fmt.Errorf("voting phase failed: %w", err)
		}
		result := EvaluateConsensus(votes, strictUnanimity)
		transcript.AddVotingRound(round, votes, result.Issues)
		activeAgents = o.updateActiveAgents(votes)
		if result.ConsensusReached {
			log.Printf("Consensus reached in %d rounds with preset '%s' (%v)", round, modelName, time.Since(start))
			transcript.SetFinalAnswer(candidate)
			return o.buildResult(candidate, transcript, outputMode, acc.report()), nil
		}
		if round < maxRounds {
			candidate = o.runSelectiveRevisePhase(ctx, candidate, result.Issues, activeAgents, transcript, messages, acc)
		}
	}

	finalAnswer := o.fallbackToBestCandidate(candidate)
	log.Printf("Debate ended with fallback after %v", time.Since(start))

	transcript.SetFinalAnswer(finalAnswer)
	return DebateResult{
		FinalAnswer: finalAnswer,
		Transcript:  transcript,
		Usage:       acc.report(),
	}, nil
}
func (o *Orchestrator) resolvePreset(modelName string) config.Preset {
	return o.cfg.GetPreset(modelName)
}
func (o *Orchestrator) buildResult(answer string, transcript *Transcript, mode string, usage types.UsageReport) DebateResult {
	if mode == "debug" {
		return DebateResult{FinalAnswer: transcript.ToCleanSummary(), Transcript: transcript, Usage: usage}
	}
	if mode == "audit" {
		return DebateResult{FinalAnswer: transcript.ToJSON(), Transcript: transcript, Usage: usage}
	}
	return DebateResult{FinalAnswer: answer, Transcript: transcript, Usage: usage}
}
