package debate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/karma-234/llm-consensus/internal/config"
	"github.com/karma-234/llm-consensus/internal/metrics"
	"github.com/karma-234/llm-consensus/internal/provider"
	"github.com/karma-234/llm-consensus/internal/store"
	"github.com/karma-234/llm-consensus/internal/types"
)

// StageStartHook is called by RunDebate immediately before each phase begins.
type StageStartHook func(stage string) error

type stageStartKey struct{}

// WithStageStartHook returns a child context carrying hook, which fires
// before draft, critique, synthesize, and vote phases begin.
func WithStageStartHook(ctx context.Context, hook StageStartHook) context.Context {
	return context.WithValue(ctx, stageStartKey{}, hook)
}

// emitStageStart fires the StageStartHook from ctx if one is attached.
func emitStageStart(ctx context.Context, stage string) {
	hook, _ := ctx.Value(stageStartKey{}).(StageStartHook)
	if hook == nil {
		return
	}
	if err := hook(stage); err != nil {
		slog.Debug("stage_start hook error", "stage", stage, "error", err)
	}
}

// StageCompleteHook is called by RunDebate immediately after each phase finishes,
// with the real token usage accumulated for that phase.
type StageCompleteHook func(stage string, usage types.Usage) error

type stageCompleteKey struct{}

// WithStageCompleteHook returns a child context carrying hook.
func WithStageCompleteHook(ctx context.Context, hook StageCompleteHook) context.Context {
	return context.WithValue(ctx, stageCompleteKey{}, hook)
}

// emitStageComplete fires the StageCompleteHook from ctx if one is attached.
func emitStageComplete(ctx context.Context, stage string, usage types.Usage) {
	hook, _ := ctx.Value(stageCompleteKey{}).(StageCompleteHook)
	if hook == nil {
		return
	}
	if err := hook(stage, usage); err != nil {
		slog.Debug("stage_complete hook error", "stage", stage, "error", err)
	}
}

type Orchestrator struct {
	prompt        *DebatePrompt
	cfg           *config.Config
	clientFactory *provider.ClientFactory
	store         *store.TranscriptStore
}

func NewOrchestrator(cfg *config.Config, clientFactory *provider.ClientFactory, ts *store.TranscriptStore) *Orchestrator {
	prompt := NewDebatePrompt()
	return &Orchestrator{
		prompt:        prompt,
		cfg:           cfg,
		clientFactory: clientFactory,
		store:         ts,
	}
}

// generateDebateID produces a short unique ID (16 hex chars) using crypto/rand.
func generateDebateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback: timestamp-based ID if crypto/rand is unavailable.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// retryCall executes fn up to 1+maxRetries times with exponential backoff.
// It respects ctx cancellation between retries.
func retryCall(ctx context.Context, maxRetries int, fn func() error) error {
	delays := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := fn(); err != nil {
			lastErr = err
			if attempt < maxRetries {
				delay := delays[min(attempt, len(delays)-1)]
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
		} else {
			return nil
		}
	}
	return lastErr
}

type DebateResult struct {
	FinalAnswer         string
	Transcript          *Transcript
	Usage               types.UsageReport
	DebateID            string
	ConsensusConfidence float64
	DisagreementSummary string
	TokenBudgetExceeded bool
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

// phaseUsage returns the tokens accumulated for a single phase so far.
func (a *usageAccumulator) phaseUsage(phase string) types.Usage {
	a.mu.Lock()
	defer a.mu.Unlock()
	var u types.Usage
	for _, r := range a.records {
		if r.Phase == phase {
			u.PromptTokens += r.Usage.PromptTokens
			u.CompletionTokens += r.Usage.CompletionTokens
			u.TotalTokens += r.Usage.TotalTokens
		}
	}
	return u
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

func (o *Orchestrator) runDraftPhase(ctx context.Context, maxRetries int, messages []types.Message, transcript *Transcript, acc *usageAccumulator) (map[string]string, error) {
	agentNames := o.clientFactory.GetAllClients()
	drafts := make(map[string]string)

	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, name := range agentNames {
		wg.Add(1)
		go func(agentName string) {
			defer wg.Done()
			client, err := o.clientFactory.GetClient(agentName)
			if err != nil {
				slog.Warn("skipping agent: failed to get client", "agent", agentName, "error", err)
				return
			}
			prompt := o.prompt.DraftPrompt(agentName, messages)
			var resp types.ChatResponse
			err = retryCall(ctx, maxRetries, func() error {
				var callErr error
				resp, callErr = client.ChatCompletion(ctx, types.ChatRequest{
					Messages: []types.Message{{Role: types.RoleSystem, Content: prompt}},
				})
				return callErr
			})
			if err != nil {
				slog.Warn("agent draft failed permanently, skipping", "agent", agentName, "error", err)
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

	if len(drafts) == 0 {
		return nil, fmt.Errorf("all agents failed during draft phase")
	}
	return drafts, nil
}

func (o *Orchestrator) runCritiquePhase(ctx context.Context, maxRetries int, messages []types.Message, drafts map[string]string, transcript *Transcript, acc *usageAccumulator) (map[string]string, error) {
	agentNames := o.clientFactory.GetAllClients()
	critiques := make(map[string]string)

	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, name := range agentNames {
		wg.Add(1)
		go func(agentName string) {
			defer wg.Done()
			client, err := o.clientFactory.GetClient(agentName)
			if err != nil {
				slog.Warn("skipping agent: failed to get client", "agent", agentName, "error", err)
				return
			}
			prompt := o.prompt.CritiquePrompt(messages, drafts, agentName)
			var resp types.ChatResponse
			err = retryCall(ctx, maxRetries, func() error {
				var callErr error
				resp, callErr = client.ChatCompletion(ctx, types.ChatRequest{
					Messages:    []types.Message{{Role: types.RoleUser, Content: prompt}},
					Temperature: 0.7,
				})
				return callErr
			})
			if err != nil {
				slog.Warn("agent critique failed permanently, skipping", "agent", agentName, "error", err)
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

	if len(critiques) == 0 {
		return nil, fmt.Errorf("all agents failed during critique phase")
	}
	return critiques, nil
}

func (o *Orchestrator) runSelectiveVotingPhase(ctx context.Context, maxRetries int, messages []types.Message, activeAgents []string, candidate string, transcript *Transcript, acc *usageAccumulator) (map[string]Vote, error) {
	votes := make(map[string]Vote)

	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, name := range activeAgents {
		wg.Add(1)
		go func(agentName string) {
			defer wg.Done()
			client, err := o.clientFactory.GetClient(agentName)
			if err != nil {
				slog.Warn("skipping agent: failed to get client", "agent", agentName, "error", err)
				return
			}
			prompt := o.prompt.VotePrompt(messages, candidate, agentName)
			var resp types.ChatResponse
			err = retryCall(ctx, maxRetries, func() error {
				var callErr error
				resp, callErr = client.ChatCompletion(ctx, types.ChatRequest{
					Messages:    []types.Message{{Role: types.RoleUser, Content: prompt}},
					Temperature: 0.0,
				})
				return callErr
			})
			if err != nil {
				slog.Warn("agent vote failed permanently, skipping", "agent", agentName, "error", err)
				return
			}
			vote, err := ParseVoteResponse(resp.Content)
			if err != nil {
				slog.Warn("agent vote parse failed, skipping", "agent", agentName, "error", err)
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

	if len(votes) == 0 {
		return nil, fmt.Errorf("all agents failed during voting phase")
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
		slog.Warn("revision failed, keeping previous candidate", "agent", reviseAgent, "error", err)
		return candidate
	}

	newCandidate := resp.Content
	acc.add(reviseAgent, "revise", resp.Usage)
	transcript.AddRevision(reviseAgent, newCandidate, issues)
	slog.Info("revision completed", "agent", reviseAgent)

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
		slog.Error("synthesis failed", "error", err)
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
	// OTel root span
	tracer := otel.Tracer("llm-consensus")
	ctx, span := tracer.Start(ctx, "debate")
	span.SetAttributes(
		attribute.String("debate.model", modelName),
	)
	defer span.End()

	start := time.Now()
	preset := o.resolvePreset(modelName)
	debateID := generateDebateID()

	maxRounds := preset.MaxRounds
	strictUnanimity := preset.StrictUnanimity
	outputMode := preset.OutputMode
	maxRetries := preset.MaxRetries
	transcript := NewTranscript(messages)
	acc := &usageAccumulator{}

	metrics.ActiveDebates.Inc()
	defer metrics.ActiveDebates.Dec()

	slog.Info("debate started", "id", debateID, "model", modelName, "max_rounds", maxRounds)

	// bestCandidate tracks the latest synthesized answer so token-budget
	// exhaustion can return a useful partial result.
	bestCandidate := ""

	budgetExceeded := func() bool {
		if preset.MaxTotalTokens <= 0 {
			return false
		}
		return acc.report().Total.TotalTokens >= preset.MaxTotalTokens
	}

	emitStageStart(ctx, "draft")
	phaseStart := time.Now()
	drafts, err := o.runDraftPhase(ctx, maxRetries, messages, transcript, acc)
	metrics.PhaseDuration.WithLabelValues("draft").Observe(time.Since(phaseStart).Seconds())
	emitStageComplete(ctx, "draft", acc.phaseUsage("draft"))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "draft phase failed")
		metrics.DebateTotal.WithLabelValues(modelName, "error").Inc()
		return DebateResult{}, fmt.Errorf("draft phase failed: %w", err)
	}
	if budgetExceeded() {
		slog.Warn("token budget exceeded after draft phase", "id", debateID)
		res := o.buildResult(bestCandidate, transcript, outputMode, acc.report())
		res.DebateID = debateID
		res.TokenBudgetExceeded = true
		o.persistTranscript(debateID, transcript)
		return res, nil
	}

	emitStageStart(ctx, "critique")
	phaseStart = time.Now()
	critiques, err := o.runCritiquePhase(ctx, maxRetries, messages, drafts, transcript, acc)
	metrics.PhaseDuration.WithLabelValues("critique").Observe(time.Since(phaseStart).Seconds())
	emitStageComplete(ctx, "critique", acc.phaseUsage("critique"))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "critique phase failed")
		metrics.DebateTotal.WithLabelValues(modelName, "error").Inc()
		return DebateResult{}, fmt.Errorf("critique phase failed: %w", err)
	}
	if budgetExceeded() {
		slog.Warn("token budget exceeded after critique phase", "id", debateID)
		res := o.buildResult(bestCandidate, transcript, outputMode, acc.report())
		res.DebateID = debateID
		res.TokenBudgetExceeded = true
		o.persistTranscript(debateID, transcript)
		return res, nil
	}

	emitStageStart(ctx, "synthesize")
	phaseStart = time.Now()
	candidate := o.runSynthesizePhase(ctx, messages, drafts, critiques, transcript, acc)
	metrics.PhaseDuration.WithLabelValues("synthesize").Observe(time.Since(phaseStart).Seconds())
	emitStageComplete(ctx, "synthesize", acc.phaseUsage("synthesize"))
	bestCandidate = candidate
	if budgetExceeded() {
		slog.Warn("token budget exceeded after synthesize phase", "id", debateID)
		res := o.buildResult(bestCandidate, transcript, outputMode, acc.report())
		res.DebateID = debateID
		res.TokenBudgetExceeded = true
		o.persistTranscript(debateID, transcript)
		return res, nil
	}

	activeAgents := o.clientFactory.GetAllClients()
	var bestConfidence float64
	var lastVotes map[string]Vote

	for round := 1; round <= maxRounds; round++ {
		emitStageStart(ctx, "vote")
		phaseStart = time.Now()
		votes, err := o.runSelectiveVotingPhase(ctx, maxRetries, messages, activeAgents, candidate, transcript, acc)
		metrics.PhaseDuration.WithLabelValues("vote").Observe(time.Since(phaseStart).Seconds())
		emitStageComplete(ctx, "vote", acc.phaseUsage("vote"))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "voting phase failed")
			metrics.DebateTotal.WithLabelValues(modelName, "error").Inc()
			return DebateResult{}, fmt.Errorf("voting phase failed: %w", err)
		}
		lastVotes = votes

		consensusResult := EvaluateConsensus(votes, strictUnanimity)
		transcript.AddVotingRound(round, votes, consensusResult.Issues)

		// Track highest approval rate seen across all rounds.
		roundConfidence := float64(consensusResult.ApprovalCount) / float64(len(votes))
		if roundConfidence > bestConfidence {
			bestConfidence = roundConfidence
		}

		activeAgents = o.updateActiveAgents(votes)

		if consensusResult.ConsensusReached {
			slog.Info("consensus reached", "id", debateID, "round", round, "preset", modelName, "duration", time.Since(start).String())
			transcript.SetFinalAnswer(candidate)
			res := o.buildResult(candidate, transcript, outputMode, acc.report())
			res.DebateID = debateID
			res.ConsensusConfidence = bestConfidence
			metrics.DebateTotal.WithLabelValues(modelName, "consensus").Inc()
			metrics.ConsensusReachedTotal.Inc()
			report := acc.report()
			metrics.TokensTotal.WithLabelValues("total", "prompt").Add(float64(report.Total.PromptTokens))
			metrics.TokensTotal.WithLabelValues("total", "completion").Add(float64(report.Total.CompletionTokens))
			span.SetAttributes(attribute.String("debate.id", debateID), attribute.Float64("debate.confidence", bestConfidence))
			o.persistTranscript(debateID, transcript)
			return res, nil
		}

		if round < maxRounds {
			candidate = o.runSelectiveRevisePhase(ctx, candidate, consensusResult.Issues, activeAgents, transcript, messages, acc)
			bestCandidate = candidate
			if budgetExceeded() {
				slog.Warn("token budget exceeded after revise phase", "id", debateID, "round", round)
				res := o.buildResult(bestCandidate, transcript, outputMode, acc.report())
				res.DebateID = debateID
				res.ConsensusConfidence = bestConfidence
				res.TokenBudgetExceeded = true
				o.persistTranscript(debateID, transcript)
				return res, nil
			}
		}
	}

	// Max rounds exhausted without consensus.
	disagreement := ""
	if lastVotes != nil {
		disagreement = SurfaceDisagreement(lastVotes)
	}

	finalAnswer := o.fallbackToBestCandidate(bestCandidate)
	if disagreement != "" {
		finalAnswer = finalAnswer + "\n\n[Disagreement summary: " + disagreement + "]"
	}

	slog.Info("debate ended with fallback", "id", debateID, "duration", time.Since(start).String(), "disagreement", disagreement)
	transcript.SetFinalAnswer(finalAnswer)
	metrics.DebateTotal.WithLabelValues(modelName, "fallback").Inc()
	report := acc.report()
	metrics.TokensTotal.WithLabelValues("total", "prompt").Add(float64(report.Total.PromptTokens))
	metrics.TokensTotal.WithLabelValues("total", "completion").Add(float64(report.Total.CompletionTokens))
	span.SetAttributes(attribute.String("debate.id", debateID), attribute.Float64("debate.confidence", bestConfidence))
	o.persistTranscript(debateID, transcript)

	return DebateResult{
		FinalAnswer:         finalAnswer,
		Transcript:          transcript,
		Usage:               acc.report(),
		DebateID:            debateID,
		ConsensusConfidence: bestConfidence,
		DisagreementSummary: disagreement,
	}, nil
}
func (o *Orchestrator) resolvePreset(modelName string) config.Preset {
	return o.cfg.GetPreset(modelName)
}

// persistTranscript saves the transcript JSON to the store if one is configured.
func (o *Orchestrator) persistTranscript(id string, t *Transcript) {
	if o.store == nil {
		return
	}
	o.store.Put(id, json.RawMessage(t.ToJSON()))
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
