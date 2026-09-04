package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeAgnesOpenAIReasoningEffort(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		body    string
		path    string
		want    string
		changed bool
	}{
		{name: "nested xhigh", model: "agness-2.0-flash", body: `{"reasoning":{"effort":"xhigh"}}`, path: "reasoning.effort", want: "high", changed: true},
		{name: "nested max", model: "agnes-2.0-flash", body: `{"reasoning":{"effort":"max"}}`, path: "reasoning.effort", want: "high", changed: true},
		{name: "flat xhigh", model: "agness-2.0-flash", body: `{"reasoning_effort":"xhigh"}`, path: "reasoning_effort", want: "high", changed: true},
		{name: "none becomes minimal", model: "agnes-2.0-flash", body: `{"reasoning":{"effort":"none"}}`, path: "reasoning.effort", want: "minimal", changed: true},
		{name: "minimal stays minimal", model: "agnes-2.0-flash", body: `{"reasoning":{"effort":"minimal"}}`, path: "reasoning.effort", want: "minimal", changed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := NormalizeAgnesOpenAIReasoningEffort([]byte(tt.body), tt.model)
			require.Equal(t, tt.changed, changed)
			require.Equal(t, tt.want, gjson.GetBytes(got, tt.path).String())
		})
	}
}

func TestNormalizeAgnesOpenAIReasoningEffortLeavesOtherModelsUntouched(t *testing.T) {
	body := []byte(`{"reasoning":{"effort":"xhigh"}}`)
	got, changed := NormalizeAgnesOpenAIReasoningEffort(body, "gpt-5.6-sol")
	require.False(t, changed)
	require.Equal(t, string(body), string(got))
}

func TestNormalizeAgnesOpenAIReasoningEffortUsesRequestedModelAlias(t *testing.T) {
	body := []byte(`{"model":"agness-2.0-flash","reasoning":{"effort":"xhigh"}}`)
	got, changed := normalizeAgnesOpenAIReasoningEffortForModels(body, "provider-model", "billing-alias", "agness-2.0-flash")
	require.True(t, changed)
	require.Equal(t, "high", gjson.GetBytes(got, "reasoning.effort").String())
}
