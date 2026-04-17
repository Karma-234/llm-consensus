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
