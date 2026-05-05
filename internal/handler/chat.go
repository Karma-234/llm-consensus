package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/karma-234/llm-consensus/internal/config"
	"github.com/karma-234/llm-consensus/internal/debate"
	"github.com/karma-234/llm-consensus/internal/provider"
	"github.com/karma-234/llm-consensus/internal/store"
	"github.com/karma-234/llm-consensus/internal/types"
)

const consensusInternalErrorMessage = "consensus computation failed"

var runDebate = func(orchestrator *debate.Orchestrator, ctx context.Context, messages []types.Message, model string) (debate.DebateResult, error) {
	return orchestrator.RunDebate(ctx, messages, model)
}

// ChatCompletionRequest is the incoming OpenAI-compatible request
type ChatCompletionRequest struct {
	Model       string          `json:"model"`
	Messages    []types.Message `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	CallbackURL string          `json:"callback_url,omitempty"`
}

// ChatCompletionResponse is the non-streaming response
type ChatCompletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int     `json:"prompt_tokens"`
		CompletionTokens    int     `json:"completion_tokens"`
		TotalTokens         int     `json:"total_tokens"`
		ConsensusConfidence float64 `json:"consensus_confidence"`
		TokenBudgetExceeded bool    `json:"token_budget_exceeded,omitempty"`
	} `json:"usage"`
}

func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// HandleChatCompletions routes to streaming or normal handler
func HandleChatCompletions(w http.ResponseWriter, r *http.Request, cfg *config.Config, ts *store.TranscriptStore) {
	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid json request: %s", err.Error()))
		return
	}

	if len(req.Messages) == 0 {
		writeJSONError(w, http.StatusBadRequest, "no messages provided")
		return
	}

	if err := validateMessages(req.Messages); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.CallbackURL != "" {
		if err := validateCallbackURL(req.CallbackURL); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	factory, err := provider.NewClientFactory(cfg)
	if err != nil {
		slog.Error("failed to create provider factory", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	orchestrator := debate.NewOrchestrator(cfg, factory, ts)

	if req.Stream {
		handleStreaming(w, r, orchestrator, req)
		return
	}

	handleNormal(w, r, orchestrator, req)
}

// Normal (non-streaming) response
func handleNormal(w http.ResponseWriter, r *http.Request, orchestrator *debate.Orchestrator, req ChatCompletionRequest) {
	result, err := runDebate(orchestrator, r.Context(), req.Messages, req.Model)
	if err != nil {
		slog.Error("debate failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, consensusInternalErrorMessage)
		return
	}

	w.Header().Set("X-Debate-ID", result.DebateID)
	response := buildOpenAIResponse(req.Model, result)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("failed to encode response", "error", err)
	}

	if req.CallbackURL != "" {
		go fireWebhook(req.CallbackURL, result)
	}
}

// Streaming response using Server-Sent Events
// Events emitted (in order):
//
//	event: stage_start     — debate phase beginning
//	event: stage_complete  — debate phase done, includes phase-level usage
//	event: answer_chunk    — token of the final answer (streamed word by word)
//	event: usage_summary   — grand total token accounting
//	event: done            — terminal marker (data: [DONE])
func handleStreaming(w http.ResponseWriter, r *http.Request, orchestrator *debate.Orchestrator, req ChatCompletionRequest) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Attach a hook so RunDebate emits stage_start and stage_complete SSE events at real phase boundaries.
	stageHook := debate.StageStartHook(func(stage string) error {
		return sendNamedSSEEvent(w, flusher, "stage_start", map[string]any{"phase": stage})
	})
	emittedPhases := make(map[string]bool)
	stageCompleteHook := debate.StageCompleteHook(func(stage string, usage types.Usage) error {
		emittedPhases[stage] = true
		return sendNamedSSEEvent(w, flusher, "stage_complete", map[string]any{"phase": stage, "usage": usage})
	})
	debateCtx := debate.WithStageCompleteHook(debate.WithStageStartHook(r.Context(), stageHook), stageCompleteHook)

	result, err := runDebate(orchestrator, debateCtx, req.Messages, req.Model)
	if err != nil {
		slog.Error("debate failed", "error", err)
		// Terminating error — emit error event then close the stream.
		sendNamedSSEEvent(w, flusher, "error", map[string]any{"message": consensusInternalErrorMessage}) //nolint:errcheck
		_ = sendSSEDone(w, flusher)
		return
	}

	// Set debate ID header.
	w.Header().Set("X-Debate-ID", result.DebateID)

	// Emit stage_complete for any phases not yet sent via hook (e.g. mocked runDebate or token-budget early exit).
	for _, ph := range result.Usage.PerPhase {
		if !emittedPhases[ph.Phase] {
			sendNamedSSEEvent(w, flusher, "stage_complete", map[string]any{"phase": ph.Phase, "usage": ph.Usage}) //nolint:errcheck
		}
	}

	// Stream the final answer word by word as answer_chunk events.
	words := strings.Fields(result.FinalAnswer)
	for i, word := range words {
		payload := map[string]any{
			"index": 0,
			"delta": map[string]string{"content": word + " "},
		}
		if i == len(words)-1 {
			payload["finish_reason"] = "stop"
		}
		if err := sendNamedSSEEvent(w, flusher, "answer_chunk", payload); err != nil {
			return
		}
		// Small pacing delay every few words for a natural read feel.
		if i%3 == 0 {
			time.Sleep(40 * time.Millisecond)
		}
	}

	// Emit aggregated usage summary before closing the stream.
	sendNamedSSEEvent(w, flusher, "usage_summary", map[string]any{ //nolint:errcheck
		"total":                 result.Usage.Total,
		"per_phase":             result.Usage.PerPhase,
		"per_agent":             result.Usage.PerAgent,
		"consensus_confidence":  result.ConsensusConfidence,
		"token_budget_exceeded": result.TokenBudgetExceeded,
	})

	_ = sendSSEDone(w, flusher)

	if req.CallbackURL != "" {
		go fireWebhook(req.CallbackURL, result)
	}
}

// sendNamedSSEEvent writes a named SSE event (event: <name>\ndata: <json>\n\n).
func sendNamedSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventName string, data map[string]any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, jsonData)
	flusher.Flush()
	return nil
}

// sendSSEEvent writes a generic (unnamed) SSE data line.
func sendSSEEvent(w http.ResponseWriter, flusher http.Flusher, data map[string]any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	flusher.Flush()
	return nil
}

func sendSSEDone(w http.ResponseWriter, flusher http.Flusher) error {
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func sendErrorEvent(w http.ResponseWriter, flusher http.Flusher, message string) {
	errorData := map[string]any{
		"error": map[string]string{"message": message},
	}
	_ = sendNamedSSEEvent(w, flusher, "error", errorData)
}

// Build standard OpenAI response for non-streaming
func buildOpenAIResponse(model string, result debate.DebateResult) ChatCompletionResponse {
	resp := ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().Unix()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{
			{
				Index: 0,
				Message: struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}{
					Role:    string(types.RoleAssistant),
					Content: result.FinalAnswer,
				},
				FinishReason: "stop",
			},
		},
	}
	resp.Usage.PromptTokens = result.Usage.Total.PromptTokens
	resp.Usage.CompletionTokens = result.Usage.Total.CompletionTokens
	resp.Usage.TotalTokens = result.Usage.Total.TotalTokens
	resp.Usage.ConsensusConfidence = result.ConsensusConfidence
	resp.Usage.TokenBudgetExceeded = result.TokenBudgetExceeded
	return resp
}

func validateMessages(messages []types.Message) error {
	for i, msg := range messages {
		if !msg.Role.IsValid() {
			return fmt.Errorf("invalid role at messages[%d]", i)
		}
		if strings.TrimSpace(msg.Content) == "" {
			return fmt.Errorf("blank content at messages[%d]", i)
		}
	}
	return nil
}

// HandleModels returns available virtual models read from config.
func HandleModels(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	models := make([]map[string]string, 0, len(cfg.VirtualModels.Presets)+len(cfg.Agents))

	// Virtual model aliases (e.g. llm-consensus-fast → fast preset)
	for id := range cfg.VirtualModels.Presets {
		models = append(models, map[string]string{
			"id":       id,
			"object":   "model",
			"owned_by": "llm-consensus",
		})
	}
	// Raw agent model names
	for _, a := range cfg.Agents {
		models = append(models, map[string]string{
			"id":       a.Name,
			"object":   "model",
			"owned_by": a.Provider,
		})
	}

	response := map[string]any{
		"object": "list",
		"data":   models,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response) //nolint:errcheck
}

// HandleTranscript retrieves a stored debate transcript by ID.
func HandleTranscript(w http.ResponseWriter, r *http.Request, ts *store.TranscriptStore) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "missing debate id")
		return
	}
	data, ok := ts.Get(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "transcript not found or expired")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data) //nolint:errcheck
}

// validateCallbackURL ensures the URL is a valid http/https URL.
func validateCallbackURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid callback_url: %s", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("callback_url must use http or https scheme, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("callback_url must have a host")
	}
	return nil
}

// webhookClient is a dedicated HTTP client for outbound webhook deliveries.
var webhookClient = &http.Client{Timeout: 10 * time.Second}

// fireWebhook posts the DebateResult JSON to the given URL in a goroutine.
// It never blocks the caller and logs delivery outcomes.
func fireWebhook(callbackURL string, result debate.DebateResult) {
	payload, err := json.Marshal(result)
	if err != nil {
		slog.Error("webhook: failed to marshal result", "url", callbackURL, "error", err)
		return
	}
	resp, err := webhookClient.Post(callbackURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		slog.Error("webhook: delivery failed", "url", callbackURL, "error", err)
		return
	}
	defer resp.Body.Close()
	slog.Info("webhook: delivered", "url", callbackURL, "status", resp.StatusCode)
}
