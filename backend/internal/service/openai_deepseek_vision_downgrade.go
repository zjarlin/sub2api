package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

const openAIDeepSeekImageOmittedNotice = "[Image input omitted because DeepSeek chat completions does not support vision input.]"

func stripDeepSeekImageInputFromChatBody(body []byte) ([]byte, bool, error) {
	if !openAIRequestBodyMayContainImageInput(body) {
		return body, false, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false, fmt.Errorf("parse chat completions body for DeepSeek image downgrade: %w", err)
	}
	messages, ok := payload["messages"].([]any)
	if !ok || len(messages) == 0 {
		return body, false, nil
	}
	if !stripDeepSeekImageInputFromChatMessages(messages) {
		return body, false, nil
	}
	payload["messages"] = messages

	next, err := marshalOpenAIUpstreamJSON(payload)
	if err != nil {
		return body, false, fmt.Errorf("serialize DeepSeek image-downgraded chat body: %w", err)
	}
	return next, true, nil
}

func stripDeepSeekImageInputFromChatMessages(messages []any) bool {
	changed := false
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		content, ok := message["content"]
		if !ok {
			continue
		}
		text, stripped := stripDeepSeekImageInputFromChatContent(content)
		if !stripped {
			continue
		}
		message["content"] = text
		changed = true
	}
	return changed
}

func stripDeepSeekImageInputFromChatContent(content any) (string, bool) {
	parts, ok := content.([]any)
	if !ok {
		return "", false
	}

	textParts := make([]string, 0, len(parts))
	strippedImage := false
	for _, rawPart := range parts {
		switch part := rawPart.(type) {
		case string:
			if text := strings.TrimSpace(part); text != "" {
				textParts = append(textParts, text)
			}
		case map[string]any:
			partType := strings.TrimSpace(deepSeekVisionStringFromAny(part["type"]))
			if partType == "image_url" || partType == "input_image" || part["image_url"] != nil {
				strippedImage = true
				continue
			}
			if partType == "" || partType == "text" || partType == "input_text" || partType == "output_text" {
				if text := strings.TrimSpace(deepSeekVisionStringFromAny(part["text"])); text != "" {
					textParts = append(textParts, text)
				}
			}
		}
	}
	if !strippedImage {
		return "", false
	}
	if len(textParts) == 0 {
		return openAIDeepSeekImageOmittedNotice, true
	}
	return strings.Join(textParts, "\n\n"), true
}

func deepSeekVisionStringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}
