package service

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Strict Responses relays cannot reliably replay output-shaped assistant
// messages beside tool results. Use the equivalent input-message text form;
// tool calls, reasoning, and multimodal content retain their original shape.
func normalizeResponsesAssistantHistory(body []byte) ([]byte, error) {
	for i, item := range gjson.GetBytes(body, "input").Array() {
		if item.Get("role").String() != "assistant" || !item.Get("content").IsArray() {
			continue
		}
		parts := item.Get("content").Array()
		var text strings.Builder
		textOnly := len(parts) > 0
		for _, part := range parts {
			if part.Get("type").String() != "output_text" || part.Get("text").Type != gjson.String {
				textOnly = false
				break
			}
			text.WriteString(part.Get("text").String())
		}
		if !textOnly {
			continue
		}
		var err error
		body, err = sjson.SetBytes(body, "input."+strconv.Itoa(i), map[string]string{"role": "assistant", "content": text.String()})
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}
