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

	// Run the full debate (we still need to compute the consensus first)
	result, err := runDebate(orchestrator, r.Context(), req.Messages, req.Model)
	if err != nil {
		log.Printf("Debate failed: %v", err)
		sendErrorEvent(w, flusher, consensusInternalErrorMessage)
		return
	}

	// Stream the final answer word-by-word for a natural feel
	content := result.FinalAnswer
	words := strings.Fields(content)

	for i, word := range words {
		chunk := map[string]any{
			"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]string{
						"content": word + " ",
					},
					"finish_reason": nil,
				},
			},
		}

		if err := sendSSEEvent(w, flusher, chunk); err != nil {
			return
		}

		// Small delay to make streaming visible and feel natural
		if i%3 == 0 {
			time.Sleep(40 * time.Millisecond)
		}
	}

	// Send the final [DONE] chunk
	doneChunk := map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]string{},
				"finish_reason": "stop",
			},
		},
	}

	if err := sendSSEEvent(w, flusher, doneChunk); err != nil {
		return
	}
	_ = sendSSEDone(w, flusher)
}

// Helper to send SSE event
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
	sendSSEEvent(w, flusher, errorData)
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
