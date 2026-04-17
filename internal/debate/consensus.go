package debate

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Vote struct {
	Approve        bool     `json:"approve"`
	Confidence     float64  `json:"confidence"` // 0.0 to 1.0
	BlockingIssues []string `json:"blocking_issues"`
	Suggestions    []string `json:"suggestions,omitempty"`
}

func ParseVoteResponse(content string) (Vote, error) {
	cleanedContent := strings.TrimSpace(content)
	cleanedContent = strings.TrimPrefix(cleanedContent, "```json")
	cleanedContent = strings.TrimPrefix(cleanedContent, "```")
	cleanedContent = strings.TrimSuffix(cleanedContent, "```")
	cleanedContent = strings.TrimSpace(cleanedContent)
	var vote Vote
	if err := json.Unmarshal([]byte(cleanedContent), &vote); err != nil {
		// Fallback: try parsing as raw map to inspect what the model actually returned
		var raw map[string]any
		if err2 := json.Unmarshal([]byte(cleanedContent), &raw); err2 == nil {
			// Model returned valid JSON but wrong schema
			vote.Approve = getBool(raw, "approve", false)
			vote.Confidence = getFloat64(raw, "confidence", 0.5)

			// blocking_issues should be array, but might be boolean or string
			if issues, ok := raw["blocking_issues"].([]any); ok {
				for _, issue := range issues {
					if s, ok := issue.(string); ok {
						vote.BlockingIssues = append(vote.BlockingIssues, s)
					}
				}
			}
			// If blocking_issues is bool/string, leave empty and treat as "no blocking issues"

			if suggestions, ok := raw["suggestions"].([]any); ok {
				for _, sugg := range suggestions {
					if s, ok := sugg.(string); ok {
						vote.Suggestions = append(vote.Suggestions, s)
					}
				}
			}
			return vote, nil
		}
		return Vote{}, fmt.Errorf("Failed to parse vote response: %s", err)
	}
	if vote.Confidence < 0 {
		vote.Confidence = 0
	}
	if vote.Confidence > 1 {
		vote.Confidence = 1
	}
	return vote, nil
}

type ConsensusResult struct {
	ConsensusReached bool     `json:"consensus_reached"`
	Issues           []string `json:"issues,omitempty"`
}

func EvaluateConsensus(votes map[string]Vote, strictUnanimity bool) ConsensusResult {
	approvals := 0
	var issues []string
	for _, vote := range votes {
		if vote.Approve {
			approvals++
		} else {
			issues = append(issues, vote.BlockingIssues...)
		}
	}
	var consensusReached bool
	if strictUnanimity {
		consensusReached = approvals == len(votes)
	} else {
		consensusReached = approvals > len(votes)/2
	}

	return ConsensusResult{
		ConsensusReached: consensusReached,
		Issues:           issues,
	}
}

func GetBestCandidateVote(votes map[string]Vote) (bestCandidate string, vote Vote) {
	var highestConfidence float64
	for candidate, v := range votes {
		if v.Confidence > highestConfidence {
			highestConfidence = v.Confidence
			bestCandidate = candidate
			vote = v
		}
	}
	return bestCandidate, vote
}

func getBool(m map[string]any, key string, defaultValue bool) bool {
	if val, ok := m[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return defaultValue
}

func getFloat64(m map[string]any, key string, defaultValue float64) float64 {
	if val, ok := m[key]; ok {
		if f, ok := val.(float64); ok {
			return f
		}
	}
	return defaultValue
}
