package debate

import (
	"fmt"
	"time"

	"github.com/karma-234/llm-consensus/internal/types"
)

type Transcript struct {
	OriginalMessages []types.Message `json:"original_messages"`
	FinalAnswer      string          `json:"final_answer,omitempty"`
	TotalDuration    string          `json:"total_duration,omitempty"`
	MetaData         map[string]any  `json:"meta_data,omitempty"`
	Phases           []Phase         `json:"phases,"`
}
type Phase struct {
	Name      string         `json:"name"`
	Timestamp string         `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

func NewTranscript(messages []types.Message) *Transcript {
	return &Transcript{
		OriginalMessages: messages,
		MetaData:         make(map[string]any),
		Phases:           make([]Phase, 0),
	}
}

func (t *Transcript) addPhase(name string, data map[string]any) {
	t.Phases = append(t.Phases, Phase{
		Name:      name,
		Timestamp: getCurrentTimestamp(),
		Data:      data,
	})
}

func (t *Transcript) AddDraftPhase(agentName, content string) {
	t.addPhase("draft", map[string]any{
		"agent":   agentName,
		"content": content,
	})
}

func (t *Transcript) AddCritiquePhase(agentName, content string) {
	t.addPhase("critique", map[string]any{
		"agent":   agentName,
		"content": content,
	})
}

func (t *Transcript) AddSynthesisPhase(content string) {
	t.addPhase("synthesis", map[string]any{
		"content": content,
	})
}

func (t *Transcript) SetFinalAnswer(answer string) {
	t.FinalAnswer = answer
}

func getCurrentTimestamp() string {

	return fmt.Sprintf("%s", time.Now().Format(time.RFC3339))
}
