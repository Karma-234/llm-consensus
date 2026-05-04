package debate

import (
	"fmt"
	"strings"

	"github.com/karma-234/llm-consensus/internal/types"
)

type DebatePrompt struct {
}

func NewDebatePrompt() *DebatePrompt {
	return &DebatePrompt{}
}

func (p *DebatePrompt) DraftPrompt(agentName string, messages []types.Message) string {
	history := formatConversationHistory(messages)

	result := fmt.Sprintf(`You are %s, an expert AI assistant participating in a collaborative debate to produce the highest quality response.

						Conversation history:
						%s

						Your task in this phase: Generate a strong, comprehensive initial draft answer to the latest user query above.
						Be thorough, accurate, and well-structured. Use clear reasoning.

						Write your draft now:`, agentName, history)
	return result
}

func (p *DebatePrompt) CritiquePrompt(messages []types.Message, drafts map[string]string, agentName string) string {
	history := formatConversationHistory(messages)

	var draftSection strings.Builder
	for name, draft := range drafts {
		fmt.Fprintf(&draftSection, "\n=== Draft from %s ===\n%s\n", name, draft)
	}

	return fmt.Sprintf(`You are %s, a critical and analytical AI participating in a multi-agent debate.

						Conversation history:
						%s

						Here are the initial drafts from all participating agents:
						%s

						Your task: Critically review ALL drafts.
						- Point out strengths and weaknesses in each draft
						- Identify factual errors, logical gaps, missing information, or biases
						- Suggest specific improvements

						Be constructive but rigorous. Structure your critique clearly, labeling each draft you review.

						Provide your detailed critique:`, agentName, history, draftSection.String())
}

func (p *DebatePrompt) SynthesizePrompt(messages []types.Message, drafts, critiques map[string]string) string {
	history := formatConversationHistory(messages)

	var draftSection, critiqueSection strings.Builder

	for name, draft := range drafts {
		fmt.Fprintf(&draftSection, "\n=== Draft from %s ===\n%s\n", name, draft)
	}
	for name, critique := range critiques {
		fmt.Fprintf(&critiqueSection, "\n=== Critique from %s ===\n%s\n", name, critique)
	}

	return fmt.Sprintf(`You are an expert synthesizer in a multi-agent debate system.

						Conversation history:
						%s

						Initial drafts:
						%s

						Critiques of those drafts:
						%s

						Your task: Synthesize the best possible final answer by combining the strongest elements from all drafts while addressing the issues raised in the critiques.

						Guidelines:
						- Resolve conflicts using the most accurate and well-supported information
						- Eliminate weaknesses identified in critiques
						- Produce a coherent, comprehensive, and polished response
						- Maintain high factual accuracy and logical consistency

						Output only the synthesized answer (no meta-commentary):`, history, draftSection.String(), critiqueSection.String())
}

func (p *DebatePrompt) VotePrompt(messages []types.Message, candidate string, agentName string) string {
	history := formatConversationHistory(messages)

	return fmt.Sprintf(`You are %s, participating in the final consensus phase of a multi-agent debate.

						Conversation history:
						%s

						Current candidate answer:
						%s

						Your task: Carefully evaluate the candidate answer above.

						RESPOND WITH VALID JSON ONLY. Do not include any other text, markdown, code fences, or commentary.

						The JSON must have this exact structure:
						- "approve": MUST be true or false (boolean, not string)
						- "confidence": MUST be a number between 0.0 and 1.0
						- "blocking_issues": MUST be an array of strings (["issue1", "issue2"]), NEVER a boolean
						- "suggestions": MUST be an array of strings (["suggestion1", "suggestion2"]), can be empty

						Example of CORRECT JSON:
						{
						"approve": true,
						"confidence": 0.85,
						"blocking_issues": ["missing citation", "unclear methodology"],
						"suggestions": ["add references", "clarify steps"]
						}

						Example of INCORRECT JSON (do not do this):
						{
						"approve": "true",
						"confidence": "0.85",
						"blocking_issues": true,
						"suggestions": "add more detail"
						}

						CRITICAL RULES:
						1. blocking_issues MUST be an array of strings. If there are no blocking issues, use empty array: []
						2. Never return blocking_issues as a boolean, string, or single value
						3. All four fields (approve, confidence, blocking_issues, suggestions) must be present
						4. Do not include markdown, backticks, or explanatory text
						5. Output only the JSON object, nothing else

						Be honest and rigorous. Only set "approve": true if the answer is excellent and free of major issues.`, agentName, history, candidate)
}

func (p *DebatePrompt) RevisePrompt(messages []types.Message, candidate string, issues []string) string {
	history := formatConversationHistory(messages)

	issuesStr := "None"
	if len(issues) > 0 {
		issuesStr = "- " + strings.Join(issues, "\n- ")
	}

	return fmt.Sprintf(`You are an expert reviser in a multi-agent debate.

						Conversation history:
						%s

						Current candidate answer:
						%s

						Blocking issues identified by the team:
						%s

						Your task: Revise the candidate answer to resolve ALL blocking issues while preserving its strengths.

						Produce an improved version that should achieve higher consensus in the next voting round.

						Output only the revised answer:`, history, candidate, issuesStr)
}

func extractUserQuery(messages []types.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == types.RoleUser {
			return messages[i].Content
		}
	}
	if len(messages) > 0 {
		return messages[len(messages)-1].Content
	}
	return "No query provided."
}

// formatConversationHistory formats the full message history so agents have
// multi-turn context, not just the last user message.
func formatConversationHistory(messages []types.Message) string {
	var sb strings.Builder
	for _, m := range messages {
		fmt.Fprintf(&sb, "[%s]: %s\n", string(m.Role), m.Content)
	}
	return strings.TrimSpace(sb.String())
}
