//go:build unit

package service

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func actionInputFixture() TurnActionRecommendInput {
	return TurnActionRecommendInput{SessionID: "019ccb31-9520-7120-bc17-556e9a92d860", RunID: "turn-1", ContextID: strings.Repeat("a", 64),
		Features: TurnActionFeatures{GitChanges: 2}, Candidates: []TurnActionCandidate{
			{ActionID: "git.commit", Version: "1", Label: "提交代码", Description: "只提交"},
			{ActionID: "git.commit_push", Version: "1", Label: "提交并推送", Description: "推送"},
		}}
}
func TestTurnActionDecisionOnlyReturnsSuppliedCandidatesAboveThreshold(t *testing.T) {
	input := actionInputFixture()
	result, err := ApplyTurnActionDecision(input, []byte(`{"model":"laya","answers":{"action_0":{"noul":0.93},"action_1":{"noul":0.3},"injected":{"noul":1}}}`))
	require.NoError(t, err)
	require.Equal(t, "completed", result.State)
	require.Equal(t, "laya", result.Source)
	require.Len(t, result.Actions, 1)
	require.Equal(t, "git.commit", result.Actions[0].ActionID)
	require.Equal(t, 0.93, result.Actions[0].Confidence)
}
func TestTurnActionDecisionRejectsMalformedConfidenceAndUnknownSource(t *testing.T) {
	for _, payload := range []string{
		`{"model":"laya","answers":{"action_0":{"noul":1.2}}}`,
		`{"model":"laya","answers":{"action_0":{"noul":0.9}}}`,
		`{"model":"invented","answers":{}}`,
		`{"model":"laya","answers":{"action_0":{"noul":null},"action_1":{"noul":0}}}`,
	} {
		_, err := ApplyTurnActionDecision(actionInputFixture(), []byte(payload))
		require.Error(t, err)
	}
}
func TestTurnActionValidationRejectsUnboundedOrDuplicateData(t *testing.T) {
	input := actionInputFixture()
	require.NoError(t, input.Validate())
	input.Candidates = append(input.Candidates, input.Candidates[0])
	require.Error(t, input.Validate())
	input = actionInputFixture()
	input.Features.GitConflicts = -1
	require.Error(t, input.Validate())
	input = actionInputFixture()
	input.RunID = "\n"
	require.Error(t, input.Validate())
	input = actionInputFixture()
	input.SessionID = "wrong"
	require.Error(t, input.Validate())
}
