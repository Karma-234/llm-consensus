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
	reqQuery := extractUserQuery(messages)

	result := fmt.Sprintf(`You are %s, an expert AI assistant participating in a collaborative debate to produce the highest quality response.

							Original query:
							%s

							Your task in this phase: Generate a strong, comprehensive initial draft answer to the query above.
							Be thorough, accurate, and well-structured. Use clear reasoning.

							Write your draft now:`, agentName, reqQuery)

	return result
}

func (p *DebatePrompt) CritiquePrompt(messages []types.Message, drafts map[string]string, agentName string) string {
	userQuery := extractUserQuery(messages)

	var draftSection strings.Builder
	for name, draft := range drafts {
		fmt.Fprintf(&draftSection, "\n=== Draft from %s ===\n%s\n", name, draft)
	}

	return fmt.Sprintf(`You are %s, a critical and analytical AI participating in a multi-agent debate.

						Original query:
						%s

						Here are the initial drafts from all participating agents:
						%s

						Your task: Critically review ALL drafts.
						- Point out strengths and weaknesses in each draft
						- Identify factual errors, logical gaps, missing information, or biases
						- Suggest specific improvements

						Be constructive but rigorous. Structure your critique clearly, labeling each draft you review.

						Provide your detailed critique:`, agentName, userQuery, draftSection.String())
}

func (p *DebatePrompt) SynthesizePrompt(messages []types.Message, drafts, critiques map[string]string) string {
	userQuery := extractUserQuery(messages)

	var draftSection, critiqueSection strings.Builder

	for name, draft := range drafts {
		fmt.Fprintf(&draftSection, "\n=== Draft from %s ===\n%s\n", name, draft)
	}
	for name, critique := range critiques {
		fmt.Fprintf(&critiqueSection, "\n=== Critique from %s ===\n%s\n", name, critique)
	}

	return fmt.Sprintf(`You are an expert synthesizer in a multi-agent debate system.

						Original query:
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

						Output only the synthesized answer (no meta-commentary):`, userQuery, draftSection.String(), critiqueSection.String())
}

func (p *DebatePrompt) VotePrompt(messages []types.Message, candidate string, agentName string) string {
	userQuery := extractUserQuery(messages)

	return fmt.Sprintf(`You are %s, participating in the final consensus phase of a multi-agent debate.

						Original query:
						%s

						Current candidate answer:
						%s

						Your task: Carefully evaluate the candidate answer above.

						Vote by responding with valid JSON only (no other text):

						{
						"approve": true or false,
						"confidence": number between 0.0 and 1.0,
						"blocking_issues": ["list of specific problems that must be fixed before you can approve"],
						"suggestions": ["optional list of improvement suggestions"]
						}

						Be honest and rigorous. Only set "approve": true if the answer is excellent and free of major issues.`, agentName, userQuery, candidate)
}

func (p *DebatePrompt) RevisePrompt(messages []types.Message, candidate string, issues []string) string {
	userQuery := extractUserQuery(messages)

	issuesStr := "None"
	if len(issues) > 0 {
		issuesStr = "- " + strings.Join(issues, "\n- ")
	}

	return fmt.Sprintf(`You are an expert reviser in a multi-agent debate.

						Original query:
						%s

						Current candidate answer:
						%s

						Blocking issues identified by the team:
						%s

						Your task: Revise the candidate answer to resolve ALL blocking issues while preserving its strengths.

						Produce an improved version that should achieve higher consensus in the next voting round.

						Output only the revised answer:`, userQuery, candidate, issuesStr)
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
