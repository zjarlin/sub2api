package service

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

const userInputToolPromptMarker = "[sub2api:request-user-input-protocol]"

// HasUserInputToolPrompt reports whether the request already carries the
// gateway's protocol prompt. It keeps retries and fallback passes idempotent.
func HasUserInputToolPrompt(body []byte) bool {
	return strings.Contains(string(body), userInputToolPromptMarker)
}

// UserInputToolPromptForBody returns a protocol prompt when the request exposes
// one of Codex's user-input tools. Some upstream chat models otherwise ask the
// same question as ordinary assistant text, which cannot trigger Codex's input
// UI. The prompt is intentionally limited to tool selection and does not force
// the model to ask when it can proceed safely.
func UserInputToolPromptForBody(body []byte) string {
	names := userInputToolNamesInBody(body)
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf(`%s
The available tools include %s. When the active Codex instructions and tool schema permit this tool, and an optional clarification would materially improve the work, call that tool instead of asking the same question in an ordinary assistant message. Ask at most three concise questions in one call and prefer short multiple-choice options when appropriate. Follow the tool's declared JSON schema exactly. If higher-priority instructions require a plain-text question, follow those instructions. Do not use this tool for permission requests, approval, or safety confirmations. Continue work that does not depend on the answer; if the answer blocks the next step, wait for the tool result before doing dependent work.`, userInputToolPromptMarker, formatToolNamesForPrompt(names))
}

// UserInputToolPromptForModel returns the protocol prompt only for non-GPT
// models. Native GPT requests already follow the Codex tool protocol and must
// remain byte-for-byte compatible unless an operator adds a model prompt.
func UserInputToolPromptForModel(model string, body []byte) string {
	if isGPTSeriesModel(model) {
		return ""
	}
	return UserInputToolPromptForBody(body)
}

func userInputToolNamesInBody(body []byte) []string {
	seen := make(map[string]bool)
	var names []string
	collect := func(tool gjson.Result) {
		if !tool.IsObject() {
			return
		}
		name := strings.TrimSpace(tool.Get("name").String())
		if name == "" {
			name = strings.TrimSpace(tool.Get("function.name").String())
		}
		if name != "request_user_input" && name != "request_user_input_async" {
			return
		}
		if seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	collectTools := func(tools gjson.Result) {
		if !tools.IsArray() {
			return
		}
		tools.ForEach(func(_, tool gjson.Result) bool {
			collect(tool)
			return true
		})
	}

	collectTools(gjson.GetBytes(body, "tools"))
	input := gjson.GetBytes(body, "input")
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() == "additional_tools" {
				collectTools(item.Get("tools"))
			}
			return true
		})
	}
	return names
}

func formatToolNamesForPrompt(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("`%s`", name))
	}
	switch len(quoted) {
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " or " + quoted[1]
	default:
		return strings.Join(quoted, ", ")
	}
}
