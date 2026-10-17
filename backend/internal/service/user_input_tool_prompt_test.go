//go:build unit

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserInputToolPromptForBodyResponses(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4.1-flash","input":"hi","tools":[{"type":"function","name":"request_user_input","parameters":{"type":"object"}}]}`)

	prompt := UserInputToolPromptForBody(body)
	require.Contains(t, prompt, "[sub2api:request-user-input-protocol]")
	require.Contains(t, prompt, "`request_user_input`")
	require.Contains(t, prompt, "instead of asking the same question in an ordinary assistant message")
	require.Contains(t, prompt, "If higher-priority instructions require a plain-text question")
	require.Contains(t, prompt, "Do not use this tool for permission requests")
}

func TestUserInputToolPromptForBodyResponsesLiteAdditionalTools(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4.1-flash","input":[{"type":"additional_tools","tools":[{"type":"function","name":"request_user_input_async","parameters":{"type":"object"}}]},{"type":"message","role":"user","content":"hi"}]}`)

	prompt := UserInputToolPromptForBody(body)
	require.Contains(t, prompt, "`request_user_input_async`")
}

func TestUserInputToolPromptForBodyChatCompletions(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"request_user_input","parameters":{"type":"object"}}}]}`)

	prompt := UserInputToolPromptForBody(body)
	require.Contains(t, prompt, "`request_user_input`")
}

func TestUserInputToolPromptForBodyRequiresDeclaredTool(t *testing.T) {
	require.Empty(t, UserInputToolPromptForBody([]byte(`{"model":"deepseek-v4.1-flash","input":"hi","tools":[{"type":"function","name":"exec_command"}]}`)))
	require.Empty(t, UserInputToolPromptForBody([]byte(`{"model":"deepseek-v4.1-flash","input":"hi"}`)))
}

func TestUserInputToolPromptForModelSkipsGPT(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":"hi","tools":[{"type":"function","name":"request_user_input"}]}`)
	require.Empty(t, UserInputToolPromptForModel("gpt-6-astra", body))
	require.NotEmpty(t, UserInputToolPromptForModel("deepseek-v4.1-flash", body))
}

func TestUserInputToolPromptIsIdempotent(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4.1-flash","instructions":"You are Codex.","input":[{"role":"user","content":"hi"}],"tools":[{"type":"function","name":"request_user_input"}]}`)
	prompt := UserInputToolPromptForBody(body)
	updated, changed := InjectModelSystemPrompt(body, prompt)
	require.True(t, changed)
	require.True(t, HasUserInputToolPrompt(updated))

	before := strings.Count(string(updated), userInputToolPromptMarker)
	require.Equal(t, 1, before)
	require.Equal(t, 1, strings.Count(string(UserInputToolPromptForBody(body)), userInputToolPromptMarker))
}
