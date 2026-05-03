package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/karma-234/llm-consensus/internal/config"
	"github.com/karma-234/llm-consensus/internal/debate"
	"github.com/karma-234/llm-consensus/internal/provider"
	"github.com/karma-234/llm-consensus/internal/types"
)

const consensusInternalErrorMessage = "consensus computation failed"

var runDebate = func(orchestrator *debate.Orchestrator, ctx context.Context, messages []types.Message, model string) (debate.DebateResult, error) {
	return orchestrator.RunDebate(ctx, messages, model)
}

// ChatCompletionRequest is the incoming OpenAI-compatible request
type ChatCompletionRequest struct {
	Model    string          `json:"model"`
	Messages []types.Message `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
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
}

func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// HandleChatCompletions routes to streaming or normal handler
func HandleChatCompletions(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
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

	factory, err := provider.NewClientFactory(cfg)
	if err != nil {
		log.Printf("Failed to create provider factory: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	orchestrator := debate.NewOrchestrator(cfg, factory)

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
		log.Printf("Debate failed: %v", err)
		writeJSONError(w, http.StatusInternalServerError, consensusInternalErrorMessage)
		return
	}

	response := buildOpenAIResponse(req.Model, result.FinalAnswer)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
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

	// Emit stage_start events immediately so clients see real-time phase progress
	// before RunDebate completes.
	for _, ph := range []string{"draft", "critique", "synthesize", "vote"} {
		sendNamedSSEEvent(w, flusher, "stage_start", map[string]any{"phase": ph}) //nolint:errcheck
	}

	result, err := runDebate(orchestrator, r.Context(), req.Messages, req.Model)
	if err != nil {
		log.Printf("Debate failed: %v", err)
		// Terminating error — emit error event then close the stream.
		sendNamedSSEEvent(w, flusher, "error", map[string]any{"message": consensusInternalErrorMessage}) //nolint:errcheck
		_ = sendSSEDone(w, flusher)
		return
	}

	// Emit stage_complete events for each phase with real per-phase usage.
	for _, ph := range result.Usage.PerPhase {
		sendNamedSSEEvent(w, flusher, "stage_complete", map[string]any{ //nolint:errcheck
			"phase": ph.Phase,
			"usage": ph.Usage,
		})
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
		"total":     result.Usage.Total,
		"per_phase": result.Usage.PerPhase,
		"per_agent": result.Usage.PerAgent,
	})

	_ = sendSSEDone(w, flusher)
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
func buildOpenAIResponse(model, content string) ChatCompletionResponse {
	return ChatCompletionResponse{
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
					Content: content,
				},
				FinishReason: "stop",
			},
		},
	}
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

// HandleModels returns available models (including presets)
func HandleModels(w http.ResponseWriter, r *http.Request) {
	models := []map[string]string{
		{"id": "llm-consensus", "object": "model", "owned_by": "llm"},
		{"id": "llm-fast", "object": "model", "owned_by": "llm"},
		{"id": "llm-balanced", "object": "model", "owned_by": "llm"},
		{"id": "llm-paranoid", "object": "model", "owned_by": "llm"},
	}

	response := map[string]any{
		"object": "list",
		"data":   models,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
