package debate

import (
	"encoding/json"
	"fmt"
	"strings"
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

func (t *Transcript) AddVote(agentName string, vote Vote) {
	t.addPhase("vote", map[string]any{
		"agent":           agentName,
		"approve":         vote.Approve,
		"confidence":      vote.Confidence,
		"blocking_issues": vote.BlockingIssues,
		"suggestions":     vote.Suggestions,
	})

}
func (t *Transcript) AddVotingRound(round int, votes map[string]Vote, blockingIssues []string) {
	voteData := make(map[string]any)
	for agent, v := range votes {
		voteData[agent] = map[string]any{
			"approve":         v.Approve,
			"confidence":      v.Confidence,
			"blocking_issues": v.BlockingIssues,
		}
	}

	t.addPhase("voting_round", map[string]any{
		"round":           round,
		"votes":           voteData,
		"blocking_issues": blockingIssues,
	})
}

func (t *Transcript) AddRevision(agentName, newContent string, issues []string) {
	t.addPhase("revision", map[string]any{
		"agent":            agentName,
		"new_content":      newContent,
		"addressed_issues": issues,
	})
}

func (t *Transcript) SetFinalAnswer(answer string) {
	t.FinalAnswer = answer
}

func getCurrentTimestamp() string {

	return fmt.Sprintf("%s", time.Now().Format(time.RFC3339))
}

func (t *Transcript) ToJSON() string {
	data := map[string]any{
		"original_messages": t.OriginalMessages,
		"phases":            t.Phases,
		"final_answer":      t.FinalAnswer,
		"total_duration":    t.TotalDuration,
		"metadata":          t.MetaData,
	}

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": "failed to marshal transcript: %v"}`, err)
	}
	return string(b)
}

func (t *Transcript) ToCleanSummary() string {
	var sb strings.Builder

	sb.WriteString("=== Zettai-Ittchi Debate Transcript ===\n\n")
	fmt.Fprintf(&sb, "Final Answer:\n%s\n\n", t.FinalAnswer)

	for i, phase := range t.Phases {
		fmt.Fprintf(&sb, "Phase %d: %s\n", i+1, strings.ToUpper(phase.Name))
		switch phase.Name {
		case "draft", "critique":
			if agent, ok := phase.Data["agent"].(string); ok {
				fmt.Fprintf(&sb, "  Agent: %s\n", agent)
			}
			if content, ok := phase.Data["content"].(string); ok {
				// Truncate long content for readability
				if len(content) > 300 {
					content = content[:300] + "..."
				}
				fmt.Fprintf(&sb, "  Content: %s\n", content)
			}
		case "voting_round":
			if round, ok := phase.Data["round"].(int); ok {
				fmt.Fprintf(&sb, "  Round: %d\n", round)
			}
			if votes, ok := phase.Data["votes"].(map[string]interface{}); ok {
				sb.WriteString("  Votes:\n")
				for agent, v := range votes {
					fmt.Fprintf(&sb, "    %s: %v\n", agent, v)
				}
			}
		case "revision":
			if agent, ok := phase.Data["agent"].(string); ok {
				fmt.Fprintf(&sb, "  Revised by: %s\n", agent)
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
