package service

import (
	"strings"
)

const openAIResponsesInputTextMaxChars = 10000000

// sanitizeOpenAIResponsesOrphanToolOutputs removes tool-output items that have
// no matching call or item reference anywhere in the current input. Named
// function outputs without a call ID are standalone inputs, not orphan results.
func sanitizeOpenAIResponsesOrphanToolOutputs(reqBody map[string]any, input []any, hasPreviousResponseID bool) bool {
	if len(input) == 0 || hasPreviousResponseID {
		return false
	}

	toolCallIDs := make(map[string]struct{}, len(input))
	referenceIDs := make(map[string]struct{}, len(input))
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemType := strings.TrimSpace(firstNonEmptyString(item["type"]))
		if itemType == "item_reference" {
			if id := strings.TrimSpace(firstNonEmptyString(item["id"])); id != "" {
				referenceIDs[id] = struct{}{}
			}
			continue
		}
		if !isCodexToolCallContextItemType(itemType) {
			continue
		}
		if id := strings.TrimSpace(firstNonEmptyString(item["call_id"], item["id"])); id != "" {
			toolCallIDs[id] = struct{}{}
		}
	}

	modified := false
	normalized := make([]any, 0, len(input))
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok || !isCodexToolCallOutputItemType(strings.TrimSpace(firstNonEmptyString(item["type"]))) {
			normalized = append(normalized, rawItem)
			continue
		}

		callID := strings.TrimSpace(firstNonEmptyString(item["call_id"]))
		// Codex sends externally supplied inputs (such as task delegation) as
		// named function outputs without a preceding function call. Preserve
		// their native type, namespace and output instead of silently dropping
		// the request that started the turn.
		if callID == "" && strings.TrimSpace(firstNonEmptyString(item["type"])) == "function_call_output" &&
			strings.TrimSpace(firstNonEmptyString(item["name"])) != "" {
			normalized = append(normalized, rawItem)
			continue
		}
		_, hasToolCall := toolCallIDs[callID]
		_, hasReference := referenceIDs[callID]
		if callID != "" && (hasToolCall || hasReference) {
			normalized = append(normalized, rawItem)
			continue
		}

		modified = true
	}
	if !modified {
		return false
	}
	reqBody["input"] = normalized
	return true
}

func truncateOpenAIResponsesInputText(_ map[string]any) bool {
	// Do not silently rewrite client or tool output. If an upstream enforces a
	// text limit, forwarding the original value preserves its explicit error for
	// the client and the normal Ops error pipeline. This compatibility shim is
	// retained until the two callers can remove the old mutation hook together.
	return false
}

func openAIResponsesInputMayNeedTruncation(_ []byte) bool {
	// See truncateOpenAIResponsesInputText. Returning false also avoids decoding
	// very large bodies solely for a mutation that must not happen.
	return false
}

// sanitizeOpenAIResponsesUnansweredToolCalls removes tool-call items whose
// call_id has no matching function_call_output / custom_tool_call_output /
// tool_search_output / mcp_tool_call_output anywhere in the same input. It
// also drops orphan tool-output items that have no announcing call (mirrors
// the CC bridge's normalizeChatMessages: unanswered calls are dropped; orphan
// outputs are dropped). This keeps strict Responses relays (e.g. DeepSeek)
// from rejecting histories with 400 "insufficient tool messages following
// tool_calls message" after a mid-execution reconnect, model switch, or
// partial parallel-tool turn.
//
// When hasPreviousResponseID is true, the current input is a continuation of
// an earlier turn and may legitimately reference calls outside this slice;
// skip rewriting to avoid dropping valid continuations.
func sanitizeOpenAIResponsesUnansweredToolCalls(reqBody map[string]any, input []any, hasPreviousResponseID bool) bool {
	if len(input) == 0 || hasPreviousResponseID {
		return false
	}

	callIDs := make(map[string]struct{}, len(input))
	outputCallIDs := make(map[string]struct{}, len(input))
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemType := strings.TrimSpace(firstNonEmptyString(item["type"]))
		if isCodexToolCallContextItemType(itemType) {
			if id := strings.TrimSpace(firstNonEmptyString(item["call_id"], item["id"])); id != "" {
				callIDs[id] = struct{}{}
			}
			continue
		}
		if isCodexToolCallOutputItemType(itemType) {
			if id := strings.TrimSpace(firstNonEmptyString(item["call_id"])); id != "" {
				outputCallIDs[id] = struct{}{}
			}
		}
	}

	modified := false
	normalized := make([]any, 0, len(input))
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			normalized = append(normalized, rawItem)
			continue
		}
		itemType := strings.TrimSpace(firstNonEmptyString(item["type"]))
		switch {
		case isCodexToolCallContextItemType(itemType):
			id := strings.TrimSpace(firstNonEmptyString(item["call_id"], item["id"]))
			if id == "" {
				// No call_id: drop rather than forward an unidentifiable call.
				modified = true
				continue
			}
			if _, hasOutput := outputCallIDs[id]; !hasOutput {
				modified = true
				continue
			}
			normalized = append(normalized, rawItem)
		case isCodexToolCallOutputItemType(itemType):
			id := strings.TrimSpace(firstNonEmptyString(item["call_id"]))
			if id == "" {
				// Named standalone function_call_output (no call_id) is a
				// legitimate external input; keep it as-is (see
				// sanitizeOpenAIResponsesOrphanToolOutputs).
				if strings.TrimSpace(firstNonEmptyString(item["name"])) != "" {
					normalized = append(normalized, rawItem)
					continue
				}
				modified = true
				continue
			}
			if _, hasCall := callIDs[id]; !hasCall {
				modified = true
				continue
			}
			normalized = append(normalized, rawItem)
		default:
			normalized = append(normalized, rawItem)
		}
	}
	if !modified {
		return false
	}
	reqBody["input"] = normalized
	return true
}
