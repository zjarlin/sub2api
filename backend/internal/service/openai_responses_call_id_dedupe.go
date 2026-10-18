package service

import (
	"bytes"
	"strings"
)

// dedupeOpenAIResponsesCallIDs drops replayed tool-call items that reuse a
// call_id already present earlier in the same Responses input.
//
// DeepSeek's native /v1/responses rejects the whole request with
// `Duplicate 'call_id': ...` when a conversation replays one tool call (for
// example after a retried turn). The Chat Completions conversion already
// normalizes this in apicompat; the native Responses passthrough needs the same
// guarantee on the raw input item list.
//
// For every duplicated call_id the last complete call/output pair is kept and
// earlier occurrences are removed, so the upstream sees exactly one
// announcement and one matching output while the surviving pair stays in order.
func dedupeOpenAIResponsesCallIDs(body []byte) ([]byte, bool, error) {
	if !bytes.Contains(body, []byte(`"call_id"`)) {
		return body, false, nil
	}
	var payload map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &payload); err != nil {
		return body, false, err
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) == 0 {
		return body, false, nil
	}

	calls := make(map[string][]int)
	outputs := make(map[string][]int)
	for i, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		callID := strings.TrimSpace(stringValue(item["call_id"]))
		if callID == "" {
			continue
		}
		if isResponsesCallOutputType(stringValue(item["type"])) {
			outputs[callID] = append(outputs[callID], i)
			continue
		}
		calls[callID] = append(calls[callID], i)
	}

	drop := make(map[int]struct{})
	for callID, outputIndexes := range outputs {
		lastOutput := outputIndexes[len(outputIndexes)-1]
		lastCall := -1
		for _, index := range calls[callID] {
			if index < lastOutput {
				lastCall = index
			}
		}
		for _, index := range calls[callID] {
			if index != lastCall {
				drop[index] = struct{}{}
			}
		}
		for _, index := range outputIndexes {
			if index != lastOutput {
				drop[index] = struct{}{}
			}
		}
	}
	// No output at all: the last announcement is the one the client is still
	// waiting on, so earlier replayed announcements are the stale ones.
	for callID, callIndexes := range calls {
		if len(outputs[callID]) > 0 {
			continue
		}
		for _, index := range callIndexes[:len(callIndexes)-1] {
			drop[index] = struct{}{}
		}
	}
	if len(drop) == 0 {
		return body, false, nil
	}

	filtered := make([]any, 0, len(input)-len(drop))
	for i, raw := range input {
		if _, removed := drop[i]; removed {
			continue
		}
		filtered = append(filtered, raw)
	}
	payload["input"] = filtered
	rebuilt, err := marshalOpenAIUpstreamJSON(payload)
	if err != nil {
		return body, false, err
	}
	return rebuilt, true, nil
}

// isResponsesCallOutputType reports whether a Responses item carries the result
// of an earlier tool call. Every current *_call_output item follows this shape.
func isResponsesCallOutputType(itemType string) bool {
	return strings.HasSuffix(itemType, "_output")
}
